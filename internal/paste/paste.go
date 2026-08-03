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

// windowTarget describes the focused window, resolved from Hyprland when
// available. A zero windowTarget (no Hyprland) falls back to generic chords.
type windowTarget struct {
	Class    string // lowercased WM_CLASS, e.g. "org.mozilla.firefox", "foot"
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
	ensureGraphicalEnvironment()
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
// the sockets are available.
func ensureGraphicalEnvironment() {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = fmt.Sprintf("/run/user/%d", os.Getuid())
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
}

func socketExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
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

// resolveFocus asks Hyprland for the focused window. Returns a zero target
// when hyprctl is missing or nothing is focused; the caller then falls back
// to generic chords for an unknown window.
func resolveFocus(r runner) windowTarget {
	var t windowTarget
	if _, err := r.LookPath("hyprctl"); err != nil {
		return t
	}
	out, err := r.Run("hyprctl", "-j", "activewindow")
	if err != nil {
		return t
	}
	var w struct {
		Class    string `json:"class"`
		Address  string `json:"address"`
		Pid      int    `json:"pid"`
		XWayland bool   `json:"xwayland"`
	}
	if err := json.Unmarshal(out, &w); err != nil {
		return t
	}
	t.Class = strings.ToLower(w.Class)
	t.Address = w.Address
	t.Pid = w.Pid
	t.XWayland = w.XWayland
	return t
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
