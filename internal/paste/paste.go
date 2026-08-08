// Package paste copies transcription text to the clipboard and injects the
// paste chord into the focused application. Wayland does not permit generic
// applications to inject input, so Sasayaki picks a strategy that fits the
// focused window instead of one fixed shortcut:
//
//   - native Wayland windows: virtual-keyboard chords (wtype → ydotool →
//     Hyprland send_key_state → xdotool), choosing the chord by window class
//     (terminals paste with Shift+Insert/Ctrl+Shift+V, GUI apps with Ctrl+V);
//   - XWayland windows: the Wayland virtual keyboard cannot reach them and
//     the Wayland clipboard is not the X11 CLIPBOARD, so the text is written
//     with xsel/xclip and injected with a targeted xdotool key --window;
//   - kitty: a native `kitty @` remote paste when the exact target resolves,
//     which preserves bracketed-paste semantics without a synthetic shortcut.
//
// The returned Result always states truthfully what was achieved; a clipboard
// write without injection is never reported as a paste.
package paste

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Result reports exactly what a Paste call achieved.
type Result struct {
	// Pasted is true only when text was injected into the focused app.
	Pasted bool
	// Backend names the paste backend used, or "clipboard" when only the
	// clipboard was updated.
	Backend string
	// Detail is a truthful human-readable outcome.
	Detail string
}

// runner abstracts command execution so tests can assert exact argv and
// simulate missing tools.
type runner interface {
	LookPath(name string) (string, error)
	Run(name string, args ...string) ([]byte, error)
	RunStdin(name string, args []string, stdin []byte) ([]byte, error)
}

type execRunner struct{}

func (execRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (execRunner) Run(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}
func (execRunner) RunStdin(name string, args []string, stdin []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	// Clipboard tools (wl-copy, xclip, xsel) fork a background daemon that
	// holds the selection. CombinedOutput would block on EOF of the stdout
	// pipe, which the forked daemon inherits — so it only returns when the
	// daemon exits (i.e. when the clipboard is next replaced), not when the
	// parent finishes. Routing stdout/stderr to /dev/null (a real FD, not a
	// pipe) lets the daemon inherit something that never blocks us, so
	// cmd.Run() returns as soon as the parent has set the clipboard.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: open /dev/null: %w", name, err)
	}
	defer devnull.Close()
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	return nil, cmd.Run()
}

// Backends in preference order for injecting the paste chord.
const (
	BackendWtype   = "wtype"
	BackendYdotool = "ydotool"
	BackendXdotool = "xdotool"
)

// Clipboard tools in preference order.
const (
	ClipWlCopy = "wl-copy"
	ClipXclip  = "xclip"
	ClipXsel   = "xsel"
)

// windowTarget describes the focused window, resolved from the compositor
// when it exposes one (Hyprland, Sway, wlroots-family compositors such as
// labwc, KWin, or plain X11). A zero windowTarget (no backend reported a
// window) falls back to generic chords.
type windowTarget struct {
	Class    string // lowercased app class, e.g. "org.mozilla.firefox", "foot"
	Address  string // Hyprland window address "0x...." ("" when unknown)
	Pid      int
	XWayland bool
}

// chord is one paste shortcut with per-transport encodings. Transports are
// tried in order: wtype (Wayland virtual keyboard), ydotool (uinput),
// Hyprland send_key_state (targeted), xdotool (X11 active window, last —
// only meaningful on plain X11 sessions).
type chord struct {
	wtypeArgs []string // wtype argv, e.g. {"-M","ctrl","-k","v","-m","ctrl"}
	ydotool   string   // ydotool key combo, e.g. "29:1 47:1 47:0 29:0"
	hyprMods  string   // Hyprland send_key_state mods, e.g. "CTRL SHIFT"
	hyprKey   string   // Hyprland send_key_state key name, e.g. "V", "Insert"
	xdotool   string   // xdotool key name, e.g. "ctrl+v"
}

var (
	chordCtrlV = chord{
		wtypeArgs: []string{"-M", "ctrl", "-k", "v", "-m", "ctrl"},
		ydotool:   "29:1 47:1 47:0 29:0",
		hyprMods:  "CTRL",
		hyprKey:   "V",
		xdotool:   "ctrl+v",
	}
	chordShiftInsert = chord{
		wtypeArgs: []string{"-M", "shift", "-k", "Insert", "-m", "shift"},
		ydotool:   "42:1 110:1 110:0 42:0",
		hyprMods:  "SHIFT",
		hyprKey:   "Insert",
		xdotool:   "shift+Insert",
	}
	chordCtrlShiftV = chord{
		wtypeArgs: []string{"-M", "ctrl", "-M", "shift", "-k", "v", "-m", "shift", "-m", "ctrl"},
		ydotool:   "29:1 42:1 47:1 47:0 42:0 29:0",
		hyprMods:  "CTRL SHIFT",
		hyprKey:   "V",
		xdotool:   "ctrl+shift+v",
	}
)

// Paste copies text and injects it into the focused application using the
// best available backend for that window. The returned Result states
// truthfully whether the paste chord succeeded or the user must paste
// manually.
func Paste(text string) Result { return PasteWith(execRunner{}, text) }

func PasteWith(r runner, text string) Result {
	ensureGraphicalEnvironment(r)
	payload := []byte(text)
	if _, copyErr := copyToClipboard(r, payload); copyErr != nil {
		return Result{Detail: "Clipboard unavailable: " + copyErr.Error() + ". Install wl-clipboard (wl-copy), xclip, or xsel."}
	}
	// Let the compositor and any clipboard manager serve the new selection
	// before injecting; pasting immediately after wl-copy can race it.
	time.Sleep(150 * time.Millisecond)

	t := resolveFocus(r)

	// XWayland windows cannot be reached by Wayland virtual-keyboard tools
	// (wtype/ydotool) and their paste reads the X11 CLIPBOARD, which
	// wl-copy never touches — they need the dedicated xsel+xdotool path.
	if t.XWayland {
		if ok, transport := pasteXWayland(r, t, payload); ok {
			return pasted(transport)
		}
		return Result{
			Pasted:  false,
			Backend: "clipboard",
			Detail:  "Copied to clipboard; paste it manually (XWayland windows need xsel or xclip plus xdotool for automatic paste).",
		}
	}

	// Kitty first gets its native remote paste: one bracketed transaction
	// instead of a synthetic shortcut, so TUIs never see split input.
	if isKitty(t.Class) {
		if ok, transport := pasteKittyRemote(r, t, payload); ok {
			return pasted(transport)
		}
	}

	for _, ch := range chordsFor(t.Class) {
		if ok, transport := sendChord(r, ch, t); ok {
			return pasted(transport)
		}
	}
	return Result{
		Pasted:  false,
		Backend: "clipboard",
		Detail:  "Copied to clipboard; paste it manually (install wtype, ydotool or xdotool for automatic paste).",
	}
}

// ensureGraphicalEnvironment repairs the environment of a user service that
// started before the graphical session imported its display variables.
// systemd may launch sasayaki with XDG_RUNTIME_DIR but without
// WAYLAND_DISPLAY/DISPLAY; clipboard and input tools then fail even though
// the sockets are available. It engages only for the real runner: the
// session probe reads live logind/proc state that a fake runner cannot
// simulate, and unit tests must stay hermetic (applySessionEnv overwrites
// XDG_CURRENT_DESKTOP from the active session).
func ensureGraphicalEnvironment(r runner) {
	if _, ok := r.(execRunner); !ok {
		return
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = fmt.Sprintf("/run/user/%d", os.Getuid())
	}

	// A boot-enabled user service can also outlive a session switch and keep
	// display env pointing at the previous compositor (a dead Hyprland
	// instance, or a labwc test instance on another socket). The compositor
	// of the user's ACTIVE logind session is authoritative for what they are
	// looking at right now; when it resolves, use its env and skip the
	// socket-sniffing fallbacks below.
	if env := sessionCompositorEnv(); len(env) > 0 {
		applySessionEnv(env, runtimeDir)
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			return
		}
	}

	wayland := os.Getenv("WAYLAND_DISPLAY")
	if wayland == "" || !socketExists(filepath.Join(runtimeDir, wayland)) {
		matches, _ := filepath.Glob(filepath.Join(runtimeDir, "wayland-*"))
		for _, path := range matches {
			if socketExists(path) {
				wayland = filepath.Base(path)
				break
			}
		}
	}
	if wayland != "" {
		_ = os.Setenv("WAYLAND_DISPLAY", wayland)
	}

	if os.Getenv("DISPLAY") == "" {
		matches, _ := filepath.Glob("/tmp/.X11-unix/X*")
		for _, path := range matches {
			if !socketExists(path) {
				continue
			}
			display := strings.TrimPrefix(filepath.Base(path), "X")
			if display != "" && allDigits(display) {
				_ = os.Setenv("DISPLAY", ":"+display)
				break
			}
		}
	}

	// Hyprland's hyprctl needs HYPRLAND_INSTANCE_SIGNATURE to locate its
	// IPC socket. A user service started outside the Hyprland session
	// never inherits it, so hyprctl -j activewindow fails and paste falls
	// back to the X11 path (which marks every window XWayland and breaks
	// native Wayland paste). Probe the hypr/ runtime dir for the live
	// instance directory; hyprctl instances is a fallback.
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") == "" {
		entries, _ := os.ReadDir(filepath.Join(runtimeDir, "hypr"))
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.Contains(name, "_") && socketExists(filepath.Join(runtimeDir, "hypr", name, ".socket.sock")) {
				_ = os.Setenv("HYPRLAND_INSTANCE_SIGNATURE", name)
				break
			}
		}
	}
}

// EnsureDisplayEnv heals stale display env exactly the way the paste path
// does: when the process env does not point at the active session's
// compositor, WAYLAND_DISPLAY / DISPLAY / HYPRLAND_INSTANCE_SIGNATURE are
// re-pointed at it. Diagnostics calls this before probing so a shell
// started in an old session (a dead wayland-1 from a previous Hyprland
// login) cannot produce false negatives under labwc.
func EnsureDisplayEnv() { ensureGraphicalEnvironment(execRunner{}) }

func socketExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

// sessionCompositorEnv returns the environment of the compositor running in
// the user's active logind session, or nil when it cannot be determined. A
// boot-enabled user service inherits the user manager's environment; when
// the user later logs into a different compositor (Hyprland → labwc or the
// reverse), the daemon's WAYLAND_DISPLAY / HYPRLAND_INSTANCE_SIGNATURE /
// DISPLAY still reference the previous session. The compositor process of
// the currently active session is the authoritative source for the display
// env the paste must target. Compositors do not always export
// WAYLAND_DISPLAY in their own environment (labwc does not), so it is
// derived from the sockets they actually hold open when absent.
func sessionCompositorEnv() map[string]string {
	pid, desktop := activeSessionCompositor()
	if pid == 0 {
		return nil
	}
	env := environOf(pid)
	if env == nil {
		return nil
	}
	if env["WAYLAND_DISPLAY"] == "" {
		env["WAYLAND_DISPLAY"] = waylandDisplayOf(pid)
	}
	if env["XDG_CURRENT_DESKTOP"] == "" {
		env["XDG_CURRENT_DESKTOP"] = desktop
	}
	return env
}

// SessionCompositorName returns the process name of the compositor running
// in the user's active logind session (labwc, hyprland, sway, …), or ""
// when it cannot be determined. Diagnostics reports it so the user sees
// which compositor the paste stack is targeting.
func SessionCompositorName() string {
	pid, _ := activeSessionCompositor()
	if pid == 0 {
		return ""
	}
	return processName(pid)
}

// activeSessionCompositor returns the pid of the compositor process in the
// user's active logind session plus the session's reported desktop name,
// or (0, "") when it cannot be determined. The session leader may have
// exited while the compositor (its child) keeps running, so the cgroup
// scope is walked rather than trusting the leader pid.
func activeSessionCompositor() (pid int, desktop string) {
	uid := strconv.Itoa(os.Getuid())
	out, err := commandTimeout(4*time.Second, "loginctl", "list-sessions", "--no-legend").Output()
	if err != nil {
		return 0, ""
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != uid {
			continue
		}
		active, scope, desktop := sessionProps(fields[0])
		if active != "yes" || scope == "" {
			continue
		}
		if pid := compositorPIDInScope(scope); pid != 0 {
			return pid, desktop
		}
	}
	return 0, ""
}

// commandTimeout builds a context-bounded command so a wedged logind or
// D-Bus cannot hang a paste.
func commandTimeout(d time.Duration, name string, args ...string) *exec.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Cancel = func() error {
		if err := cmd.Process.Kill(); err != nil {
			return err
		}
		cancel()
		return nil
	}
	return cmd
}

// sessionProps reports the Active flag, cgroup scope and reported desktop of
// a logind session. loginctl may reorder requested properties, so the
// Key=Value form is parsed rather than positional --value output.
func sessionProps(id string) (active, scope, desktop string) {
	out, err := commandTimeout(4*time.Second, "loginctl", "show-session", id,
		"-p", "Active", "-p", "Scope", "-p", "Desktop").Output()
	if err != nil {
		return "", "", ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "Active":
			active = strings.TrimSpace(value)
		case "Scope":
			scope = strings.TrimSpace(value)
		case "Desktop":
			desktop = strings.TrimSpace(value)
		}
	}
	return active, scope, desktop
}

// compositorPIDInScope returns the pid of the compositor process inside a
// logind session cgroup scope, or 0 when none is found. The session leader
// may have exited while the compositor (its child) keeps running, so the
// scope is walked rather than trusting the leader pid.
func compositorPIDInScope(scope string) int {
	root := fmt.Sprintf("/sys/fs/cgroup/user.slice/user-%d.slice/%s", os.Getuid(), scope)
	data, err := os.ReadFile(filepath.Join(root, "cgroup.procs"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 || !isCompositor(pid) {
			continue
		}
		return pid
	}
	return 0
}

// compositorNames are the process basenames of Wayland compositors whose
// toplevel state the paste backends can query.
var compositorNames = map[string]bool{
	"labwc": true, "hyprland": true, "sway": true, "wayfire": true,
	"river": true, "hikari": true, "weston": true, "kwin_wayland": true,
	"mutter": true, "gnome-shell": true,
}

func isCompositor(pid int) bool {
	return compositorNames[processName(pid)]
}

// processName returns the process basename from /proc/<pid>/cmdline's
// argv[0], or "" when the process is gone or unreadable.
func processName(pid int) string {
	cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return ""
	}
	return filepath.Base(strings.SplitN(string(cmdline), "\x00", 2)[0])
}

// environOf returns the environment of a process as a map.
func environOf(pid int) map[string]string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return nil
	}
	env := map[string]string{}
	for _, kv := range strings.Split(string(data), "\x00") {
		if i := strings.IndexByte(kv, '='); i > 0 {
			env[kv[:i]] = kv[i+1:]
		}
	}
	return env
}

// waylandDisplayOf derives the WAYLAND_DISPLAY value of a process from the
// unix sockets it holds open, matching socket inodes against /proc/net/unix.
// This covers compositors that never export the variable in their own
// environment.
func waylandDisplayOf(pid int) string {
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return ""
	}
	inodes := map[string]bool{}
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(fdDir, e.Name()))
		if err == nil && strings.HasPrefix(target, "socket:[") {
			inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = true
		}
	}
	if len(inodes) == 0 {
		return ""
	}
	data, err := os.ReadFile("/proc/net/unix")
	if err != nil {
		return ""
	}
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	prefix := runtimeDir + "/wayland-"
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 || !inodes[fields[6]] {
			continue
		}
		path := strings.Join(fields[7:], " ")
		if strings.HasPrefix(path, prefix) {
			return filepath.Base(path)
		}
	}
	return ""
}

// applySessionEnv applies the display env of the active session's compositor
// to the daemon. Values are validated against live sockets; stale values are
// unset so downstream fail-fast resolvers see the truth (a dead Hyprland
// signature must not make hyprctl connect to a foreign instance).
func applySessionEnv(env map[string]string, runtimeDir string) {
	if wl := env["WAYLAND_DISPLAY"]; wl != "" && socketExists(filepath.Join(runtimeDir, wl)) {
		_ = os.Setenv("WAYLAND_DISPLAY", wl)
	} else {
		_ = os.Unsetenv("WAYLAND_DISPLAY")
	}
	if d := env["DISPLAY"]; d != "" {
		_ = os.Setenv("DISPLAY", d)
	} else {
		_ = os.Unsetenv("DISPLAY")
	}
	if sig := env["HYPRLAND_INSTANCE_SIGNATURE"]; sig != "" &&
		socketExists(filepath.Join(runtimeDir, "hypr", sig, ".socket.sock")) {
		_ = os.Setenv("HYPRLAND_INSTANCE_SIGNATURE", sig)
	} else {
		_ = os.Unsetenv("HYPRLAND_INSTANCE_SIGNATURE")
	}
	if desk := env["XDG_CURRENT_DESKTOP"]; desk != "" {
		_ = os.Setenv("XDG_CURRENT_DESKTOP", desk)
	}
}

func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

func pasted(transport string) Result {
	return Result{Pasted: true, Backend: transport, Detail: "Pasted with " + transport}
}

// resolveFocus asks the compositor for the focused window. Backends are
// tried in order — Hyprland, Sway, KWin (Plasma), GNOME (via the
// window-calls-extended shell extension), then plain X11 (EWMH) — and each
// fails fast when its compositor is absent, so the first success wins. A
// zero target makes the caller fall back to generic chords.
// focusBackends is the resolver chain in priority order. Each backend fails
// fast when its compositor is absent, so the first success wins.
var focusBackends = []struct {
	name string
	fn   func(runner) (windowTarget, bool)
}{
	{"hyprland", resolveHyprlandFocus},
	{"sway", resolveSwayFocus},
	{"wlroots", resolveWlrootsFocus},
	{"kwin", resolveKWinFocus},
	{"gnome", resolveGNOMEFocus},
	{"x11", resolveX11Focus},
}

func resolveFocus(r runner) windowTarget {
	t, _ := resolveFocusWithBackend(r)
	return t
}

// resolveFocusWithBackend returns the focused window and the name of the
// resolver that found it.
func resolveFocusWithBackend(r runner) (windowTarget, string) {
	for _, b := range focusBackends {
		if t, ok := b.fn(r); ok {
			return t, b.name
		}
	}
	return windowTarget{}, ""
}

// ProbeFocus resolves the currently focused window with the real resolver
// chain and returns its class and the resolver backend that identified it.
// ok is false when no backend could resolve a focused window (empty
// desktop, or no compositor IPC / Wayland access). Diagnostics uses it to
// prove the focus path works on the running compositor — under labwc this
// exercises the raw zwlr_foreign_toplevel_manager_v1 client.
func ProbeFocus() (class, backend string, ok bool) {
	t, b := resolveFocusWithBackend(execRunner{})
	if t.Class == "" {
		return "", "", false
	}
	return t.Class, b, true
}

// resolveHyprlandFocus asks Hyprland for the focused window.
func resolveHyprlandFocus(r runner) (windowTarget, bool) {
	var t windowTarget
	if _, err := r.LookPath("hyprctl"); err != nil {
		return t, false
	}
	out, err := r.Run("hyprctl", "-j", "activewindow")
	if err != nil {
		return t, false
	}
	var w struct {
		Class    string `json:"class"`
		Address  string `json:"address"`
		Pid      int    `json:"pid"`
		XWayland bool   `json:"xwayland"`
	}
	if err := json.Unmarshal(out, &w); err != nil || w.Class == "" {
		return t, false
	}
	t.Class = strings.ToLower(w.Class)
	t.Address = w.Address
	t.Pid = w.Pid
	t.XWayland = w.XWayland
	return t, true
}

// resolveSwayFocus parses `swaymsg -t get_tree`, walking the node tree for
// the focused window. Native Wayland windows carry app_id; XWayland windows
// carry window_properties.class and shell "xwayland".
func resolveSwayFocus(r runner) (windowTarget, bool) {
	var t windowTarget
	if _, err := r.LookPath("swaymsg"); err != nil {
		return t, false
	}
	out, err := r.Run("swaymsg", "-t", "get_tree")
	if err != nil {
		return t, false
	}
	var root swayNode
	if err := json.Unmarshal(out, &root); err != nil {
		return t, false
	}
	n, ok := focusedSwayWindow(root)
	if !ok {
		return t, false
	}
	if n.AppID != nil {
		t.Class = strings.ToLower(*n.AppID)
	} else if n.WindowProps != nil {
		t.Class = strings.ToLower(n.WindowProps.Class)
	} else {
		return t, false
	}
	t.Pid = n.PID
	t.XWayland = n.Shell == "xwayland"
	return t, true
}

// swayNode is one node of sway's layout tree.
type swayNode struct {
	Focused       bool          `json:"focused"`
	AppID         *string       `json:"app_id"`
	WindowProps   *swayWinProps `json:"window_properties"`
	Shell         string        `json:"shell"`
	PID           int           `json:"pid"`
	Nodes         []swayNode    `json:"nodes"`
	FloatingNodes []swayNode    `json:"floating_nodes"`
}

type swayWinProps struct {
	Class string `json:"class"`
}

// focusedSwayWindow walks the tree to the focused window node (the only
// nodes with focused=true that also carry an app identity).
func focusedSwayWindow(n swayNode) (swayNode, bool) {
	if n.Focused && (n.AppID != nil || n.WindowProps != nil) {
		return n, true
	}
	for _, child := range n.Nodes {
		if w, ok := focusedSwayWindow(child); ok {
			return w, true
		}
	}
	for _, child := range n.FloatingNodes {
		if w, ok := focusedSwayWindow(child); ok {
			return w, true
		}
	}
	return swayNode{}, false
}

// resolveKWinFocus asks KWin for the focused window by loading a tiny
// scripting plugin over D-Bus and scraping its print() output from the
// journal. KWin's queryWindowInfo D-Bus method is interactive (it waits for
// the user to click a window), so reading workspace.activeWindow from a
// script and scraping `js:` lines from journalctl is the only general
// non-interactive path. Fails fast when the session is not Plasma or qdbus/
// journalctl are absent; a script racing KWin's own runtime (class "js::…")
// is also treated as a miss.
func resolveKWinFocus(r runner) (windowTarget, bool) {
	var t windowTarget
	if !isKDE(os.Getenv("XDG_CURRENT_DESKTOP")) {
		return t, false
	}
	qdbus := "qdbus6"
	if _, err := r.LookPath(qdbus); err != nil {
		qdbus = "qdbus"
		if _, err := r.LookPath(qdbus); err != nil {
			return t, false
		}
	}
	if _, err := r.LookPath("journalctl"); err != nil {
		return t, false
	}
	tmp, err := os.CreateTemp("", "sasayaki-kwin-*.js")
	if err != nil {
		return t, false
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(`if (workspace.activeWindow) { print(workspace.activeWindow.resourceClass); print(workspace.activeWindow.pid); }`); err != nil {
		tmp.Close()
		return t, false
	}
	tmp.Close()
	defer os.Remove(tmpName)

	scriptID := fmt.Sprintf("sasayaki_%d", os.Getpid())
	out, err := r.Run(qdbus, "org.kde.KWin", "/Scripting",
		"org.kde.kwin.Scripting.loadScript", tmpName, scriptID)
	if err != nil {
		return t, false
	}
	instance := strings.TrimSpace(firstLine(out))
	if instance == "" {
		return t, false
	}
	if !strings.HasPrefix(instance, "Script") {
		instance = "Script" + instance
	}
	runPath := "/Scripting/" + instance

	before := time.Now()
	if _, err := r.Run(qdbus, "org.kde.KWin", runPath, "org.kde.kwin.Script.run"); err != nil {
		return t, false
	}
	// KWin executes scripts asynchronously; give the print() time to land.
	time.Sleep(150 * time.Millisecond)
	since := before.Add(-2 * time.Second).Format("2006-01-02 15:04:05")
	out, err = r.Run("journalctl", "--since", since, "-o", "cat", "--no-pager")
	if err != nil {
		return t, false
	}
	var jsLines []string
	for _, line := range strings.Split(string(out), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "js: "); ok {
			jsLines = append(jsLines, rest)
		}
	}
	if len(jsLines) < 2 {
		return t, false
	}
	class := jsLines[len(jsLines)-2]
	if strings.Contains(class, "js::") {
		return t, false // KWin's scripting runtime briefly became active
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(jsLines[len(jsLines)-1]))
	// Unload the injected plugin (best effort; it is also removed on exit).
	_, _ = r.Run(qdbus, "org.kde.KWin", runPath, "org.kde.kwin.Script.stop")
	_, _ = r.Run(qdbus, "org.kde.KWin", "/Scripting", "org.kde.kwin.Scripting.unloadScript", scriptID)

	t.Class = strings.ToLower(strings.TrimSpace(class))
	t.Pid = pid
	return t, t.Class != ""
}

// isKDE reports whether the desktop session is KDE Plasma.
func isKDE(desk string) bool {
	desk = strings.ToLower(desk)
	return strings.Contains(desk, "kde") || strings.Contains(desk, "plasma")
}

// resolveGNOMEFocus reads the focused window class from the
// window-calls-extended GNOME shell extension over D-Bus. GNOME Wayland has
// no official focus API, so this is the only non-interactive path, and it
// requires the user to have installed that extension. Any failure (extension
// absent, no GNOME session) falls through to the next backend.
func resolveGNOMEFocus(r runner) (windowTarget, bool) {
	var t windowTarget
	if !strings.Contains(strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP")), "gnome") {
		return t, false
	}
	if _, err := r.LookPath("gdbus"); err != nil {
		return t, false
	}
	out, err := r.Run("gdbus", "call", "--session", "--dest", "org.gnome.Shell",
		"--object-path", "/org/gnome/Shell/Extensions/WindowsExt",
		"--method", "org.gnome.Shell.Extensions.WindowsExt.FocusClass")
	if err != nil {
		return t, false
	}
	// gdbus renders the reply as e.g. `(<'firefox'>)`.
	m := gnomeFocusRe.FindSubmatch(out)
	if m == nil {
		return t, false
	}
	t.Class = strings.ToLower(string(m[1]))
	return t, t.Class != ""
}

var gnomeFocusRe = regexp.MustCompile(`'([^']+)'`)

// resolveX11Focus reads the focused window over the EWMH _NET_ACTIVE_WINDOW
// root property. This covers every X11 window manager (i3, Xfce, KDE/GNOME
// on X11, …) and, under a Wayland session, the XWayland window — the only X
// windows that exist there. Setting XWayland lets the caller use the
// xsel+xdotool path for it.
func resolveX11Focus(r runner) (windowTarget, bool) {
	var t windowTarget
	if _, err := r.LookPath("xprop"); err != nil {
		return t, false
	}
	out, err := r.Run("xprop", "-root", "_NET_ACTIVE_WINDOW")
	if err != nil {
		return t, false
	}
	winID := x11WindowID(out)
	if winID == "" || winID == "0x0" {
		return t, false
	}
	out, err = r.Run("xprop", "-id", winID, "WM_CLASS", "_NET_WM_PID")
	if err != nil {
		return t, false
	}
	t.Class, t.Pid = parseX11Props(out)
	t.Class = strings.ToLower(t.Class)
	// Under Wayland, any X window is an XWayland window.
	t.XWayland = os.Getenv("WAYLAND_DISPLAY") != ""
	return t, t.Class != ""
}

// x11WindowID extracts the window id from an xprop -root line such as
// `_NET_ACTIVE_WINDOW(WINDOW): window id # 0x3e00005`.
func x11WindowID(out []byte) string {
	const marker = "window id # "
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, marker); i >= 0 {
			return strings.TrimSpace(line[i+len(marker):])
		}
	}
	return ""
}

// parseX11Props extracts the WM_CLASS resource class and _NET_WM_PID from
// `xprop -id … WM_CLASS _NET_WM_PID` output:
//
//	WM_CLASS(STRING) = "instance", "class"
//	_NET_WM_PID(CARDINAL) = 12345
func parseX11Props(out []byte) (class string, pid int) {
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "WM_CLASS"):
			quoted := xpropQuoted(line)
			if len(quoted) >= 2 {
				class = quoted[1] // the resource class, not the instance name
			}
		case strings.HasPrefix(line, "_NET_WM_PID"):
			fields := strings.Fields(line)
			if n := len(fields); n > 0 {
				if p, err := strconv.Atoi(fields[n-1]); err == nil {
					pid = p
				}
			}
		}
	}
	return class, pid
}

// xpropQuoted returns the double-quoted strings in an xprop property line,
// in order.
func xpropQuoted(line string) []string {
	var out []string
	for {
		i := strings.IndexByte(line, '"')
		if i < 0 {
			return out
		}
		line = line[i+1:]
		j := strings.IndexByte(line, '"')
		if j < 0 {
			return out
		}
		out = append(out, line[:j])
		line = line[j+1:]
	}
}

func isKitty(class string) bool {
	return strings.HasPrefix(class, "kitty")
}

func isTerminalClass(class string) bool {
	switch {
	case class == "st":
		return true
	case strings.HasPrefix(class, "foot"),
		strings.HasPrefix(class, "kitty"),
		strings.HasPrefix(class, "alacritty"),
		strings.HasPrefix(class, "ghostty"),
		strings.HasPrefix(class, "wezterm"),
		strings.HasPrefix(class, "org.wezfurlong.wezterm"),
		strings.HasPrefix(class, "xdg-term"),
		strings.HasPrefix(class, "konsole"),
		strings.HasPrefix(class, "org.konsole"),
		strings.HasPrefix(class, "gnome-terminal"),
		strings.HasPrefix(class, "org.gnome.terminal"),
		strings.HasPrefix(class, "xterm"),
		strings.HasPrefix(class, "urxvt"),
		strings.HasPrefix(class, "rxvt"),
		strings.HasPrefix(class, "tmux"),
		strings.HasPrefix(class, "com.mitchellh.ghostty"):
		return true
	}
	return false
}

// chordsFor returns the paste-shortcut order for a window class, matching
// what each application family actually binds. The Hyprland companion uses
// the same ordering.
func chordsFor(class string) []chord {
	if !isTerminalClass(class) {
		// GUI apps: Ctrl+V is the universal paste; Shift+Insert covers
		// xterm-family fields, Ctrl+Shift+V the plain-text-paste subset.
		return []chord{chordCtrlV, chordShiftInsert, chordCtrlShiftV}
	}
	switch {
	case strings.HasPrefix(class, "alacritty"), strings.HasPrefix(class, "ghostty"), strings.HasPrefix(class, "com.mitchellh.ghostty"):
		return []chord{chordCtrlShiftV, chordShiftInsert, chordCtrlV}
	case strings.HasPrefix(class, "foot"), strings.HasPrefix(class, "kitty"):
		return []chord{chordShiftInsert, chordCtrlShiftV, chordCtrlV}
	default:
		// Generic terminals bind Shift+Insert or Ctrl+V.
		return []chord{chordShiftInsert, chordCtrlV, chordCtrlShiftV}
	}
}

// sendChord tries one shortcut across every transport. A transport counts as
// used only when its process exits successfully.
func sendChord(r runner, ch chord, t windowTarget) (bool, string) {
	if _, err := r.LookPath(BackendWtype); err == nil {
		if _, err := r.Run(BackendWtype, ch.wtypeArgs...); err == nil {
			return true, BackendWtype
		}
	}
	if _, err := r.LookPath(BackendYdotool); err == nil {
		args := append([]string{"key", "-d", "1"}, strings.Fields(ch.ydotool)...)
		if _, err := r.Run(BackendYdotool, args...); err == nil {
			return true, BackendYdotool
		}
	}
	if ok, transport := sendHyprland(r, ch, t); ok {
		return true, transport
	}
	if _, err := r.LookPath(BackendXdotool); err == nil {
		if _, err := r.Run(BackendXdotool, "key", "--clearmodifiers", ch.xdotool); err == nil {
			return true, BackendXdotool
		}
	}
	return false, ""
}

// sendHyprland injects the chord with Hyprland's send_key_state dispatch,
// explicitly targeted at the focused window. It is a fallback because the
// dispatch can leave synthetic keys repeating; the up event follows the down
// after a short pause, mirroring the companion extension.
func sendHyprland(r runner, ch chord, t windowTarget) (bool, string) {
	if _, err := r.LookPath("hyprctl"); err != nil {
		return false, ""
	}
	target := t.Address
	if target == "" {
		target = "activewindow"
	}
	down := fmt.Sprintf(`hl.dsp.send_key_state({ mods = "%s", key = "%s", state = "down", window = "%s" })`, ch.hyprMods, ch.hyprKey, target)
	if _, err := r.Run("hyprctl", "dispatch", down); err != nil {
		return false, ""
	}
	time.Sleep(50 * time.Millisecond)
	up := fmt.Sprintf(`hl.dsp.send_key_state({ mods = "%s", key = "%s", state = "up", window = "%s" })`, ch.hyprMods, ch.hyprKey, target)
	if _, err := r.Run("hyprctl", "dispatch", up); err != nil {
		return false, ""
	}
	return true, "hyprland"
}

// pasteXWayland pastes into an XWayland window: the payload must first reach
// the X11 CLIPBOARD (xsel/xclip), then the chord is injected at the X server
// with XTEST (xdotool key without --window). XTEST events are real input to
// the focused X window — the target is always the focused window here — and
// unlike XSendEvent-style `xdotool key --window`, GTK applications accept
// them. Without XTEST, Wayland virtual-keyboard tools cannot reach XWayland
// windows at all.
func pasteXWayland(r runner, t windowTarget, payload []byte) (bool, string) {
	if _, err := r.LookPath(BackendXdotool); err != nil {
		return false, ""
	}
	if !setX11Clipboard(r, payload) {
		return false, ""
	}
	// Let the xsel/xclip daemon take clipboard ownership before pasting;
	// an immediate paste can race the selection and come back empty.
	time.Sleep(100 * time.Millisecond)
	for _, ch := range []struct {
		key       string
		transport string
	}{
		{"ctrl+v", "xdotool-x11"},
		{"shift+Insert", "xdotool-x11-shift-insert"},
	} {
		if _, err := r.Run(BackendXdotool, "key", "--clearmodifiers", ch.key); err == nil {
			return true, ch.transport
		}
	}
	return false, ""
}

// setX11Clipboard writes payload to the X11 CLIPBOARD selection so XWayland
// windows can paste it. xsel is preferred, xclip is the fallback.
func setX11Clipboard(r runner, payload []byte) bool {
	for _, tool := range []string{ClipXsel, ClipXclip} {
		if _, err := r.LookPath(tool); err != nil {
			continue
		}
		var args []string
		if tool == ClipXsel {
			args = []string{"--clipboard", "--input"}
		} else {
			args = []string{"-selection", "clipboard", "-i"}
		}
		if _, err := r.RunStdin(tool, args, payload); err == nil {
			return true
		}
	}
	return false
}

// firstLine returns the first non-empty line of command output.
func firstLine(b []byte) string {
	line := strings.TrimSpace(string(b))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	return line
}

// pasteKittyRemote asks kitty to perform one native paste in the exact
// target window. This preserves bracketed-paste semantics (TUIs receive the
// payload as one transaction) without re-injecting a keyboard shortcut.
func pasteKittyRemote(r runner, t windowTarget, payload []byte) (bool, string) {
	if _, err := r.LookPath("kitty"); err != nil {
		return false, ""
	}
	for _, sock := range kittySockets(t.Pid) {
		if ok, transport := sendToKitty(r, "unix:"+sock, payload); ok {
			return true, transport
		}
	}
	// Last resort: kitty's default socket from its own environment.
	if ok, transport := sendToKitty(r, "", payload); ok {
		return true, transport
	}
	return false, ""
}

// kittySockets lists candidate kitty control sockets, pid-targeted first.
// kitty names its socket /tmp/mykitty-<pid> when a mykitty config is used,
// /tmp/kitty or $XDG_RUNTIME_DIR/kitty-<pid> by default.
func kittySockets(pid int) []string {
	var socks []string
	if pid > 0 {
		socks = append(socks, fmt.Sprintf("/tmp/mykitty-%d", pid))
	}
	for _, pat := range []string{"/tmp/mykitty", "/tmp/mykitty-*", "/tmp/kitty"} {
		if matches, err := filepath.Glob(pat); err == nil {
			socks = append(socks, matches...)
		}
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" {
		if matches, err := filepath.Glob(filepath.Join(rt, "kitty-*")); err == nil {
			socks = append(socks, matches...)
		}
	}
	seen := make(map[string]bool)
	out := socks[:0]
	for _, s := range socks {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// kitty @ ls JSON structure: an array of OS windows, each with tabs of
// windows. Only the id of the single focused window is used as a target:
// `--match state:focused` can match several kitty windows at once and
// `send-text` would then write the payload into every one.
type kittyOSWindow struct {
	IsFocused bool       `json:"is_focused"`
	Tabs      []kittyTab `json:"tabs"`
}
type kittyTab struct {
	IsActive bool          `json:"is_active"`
	Windows  []kittyWindow `json:"windows"`
}
type kittyWindow struct {
	IsFocused bool  `json:"is_focused"`
	ID        int64 `json:"id"`
}

func resolveKittyWindowID(ls []byte) (int64, bool) {
	var osWindows []kittyOSWindow
	if err := json.Unmarshal(ls, &osWindows); err != nil || len(osWindows) == 0 {
		return 0, false
	}
	osw := osWindows[0]
	for _, w := range osWindows {
		if w.IsFocused {
			osw = w
			break
		}
	}
	if len(osw.Tabs) == 0 {
		return 0, false
	}
	tab := osw.Tabs[0]
	for _, t := range osw.Tabs {
		if t.IsActive {
			tab = t
			break
		}
	}
	if len(tab.Windows) == 0 {
		return 0, false
	}
	w := tab.Windows[0]
	for _, c := range tab.Windows {
		if c.IsFocused {
			w = c
			break
		}
	}
	return w.ID, true
}

// sendToKitty pastes the payload into the resolved kitty window via one
// remote call. toArg is "unix:<socket>" or "" for kitty's default socket.
// kitty's native paste action is tried first; the bracketed send-text
// fallback clears the clipboard first so kitty's OSC 5522 enhanced paste
// cannot request the Wayland selection as a second payload, then restores it.
func sendToKitty(r runner, toArg string, payload []byte) (bool, string) {
	lsArgs := []string{"@", "ls"}
	if toArg != "" {
		lsArgs = []string{"@", "--to", toArg, "ls"}
	}
	out, err := r.Run("kitty", lsArgs...)
	if err != nil {
		return false, ""
	}
	winID, ok := resolveKittyWindowID(out)
	if !ok {
		return false, ""
	}
	match := fmt.Sprintf("id:%d", winID)

	if _, err := r.Run("kitty", kittyArgs(toArg, "action", "--match", match, "paste_from_clipboard")...); err == nil {
		return true, "kitty-native-paste"
	}
	if _, err := r.LookPath(ClipWlCopy); err == nil {
		_, _ = r.Run(ClipWlCopy, "-c") // clear: OSC 5522 must not double-paste
	}
	_, sendErr := r.RunStdin("kitty", kittyArgs(toArg, "send-text", "--match", match, "--stdin", "--bracketed-paste", "auto"), payload)
	if _, err := r.LookPath(ClipWlCopy); err == nil {
		time.Sleep(50 * time.Millisecond)
		_, _ = r.RunStdin(ClipWlCopy, []string{"--trim-newline"}, payload) // restore
	}
	if sendErr == nil {
		return true, "kitty-bracketed-fallback"
	}
	return false, ""
}

// kittyArgs builds argv for a kitty remote command: "@ [--to TO] VERB ...".
func kittyArgs(toArg, verb string, tail ...string) []string {
	args := []string{"@"}
	if toArg != "" {
		args = append(args, "--to", toArg)
	}
	args = append(args, verb)
	return append(args, tail...)
}

// copyToClipboard places text on the clipboard using the first available
// tool. Returns the tool name used.
func copyToClipboard(r runner, text []byte) (string, error) {
	for _, tool := range []string{ClipWlCopy, ClipXclip, ClipXsel} {
		if _, err := r.LookPath(tool); err != nil {
			continue
		}
		var args []string
		switch tool {
		case ClipWlCopy:
			args = []string{"--trim-newline"}
		case ClipXclip:
			args = []string{"-selection", "clipboard", "-i"}
		case ClipXsel:
			args = []string{"--clipboard", "--input"}
		}
		if _, err := r.RunStdin(tool, args, text); err == nil {
			return tool, nil
		}
		// Fall through to the next tool: a present-but-failing wl-copy
		// should not prevent xclip/xsel from being tried.
	}
	return "", fmt.Errorf("no clipboard tool found (tried wl-copy, xclip, xsel)")
}

// ClipboardAvailable reports whether any clipboard tool is installed.
func ClipboardAvailable(r runner) bool {
	for _, tool := range []string{ClipWlCopy, ClipXclip, ClipXsel} {
		if _, err := r.LookPath(tool); err == nil {
			return true
		}
	}
	return false
}

// BestBackend returns the first usable paste backend, or "" when none is
// installed.
func BestBackend(r runner) string {
	for _, name := range []string{BackendWtype, BackendYdotool, BackendXdotool} {
		if _, err := r.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// Available reports whether both a clipboard tool and a paste backend exist,
// i.e. the full copy-and-paste path is usable.
func Available(r runner) bool {
	return ClipboardAvailable(r) && BestBackend(r) != ""
}

// DefaultRunner exposes the real command runner for consumers that need
// capability checks without constructing their own.
func DefaultRunner() runner { return execRunner{} }

// AvailableDefault is Available with the real command runner.
func AvailableDefault() bool { return Available(execRunner{}) }
