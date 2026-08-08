// Package diagnostics runs prerequisite and capability checks and produces
// human- and machine-readable reports with concrete remediation. It never
// modifies the system.
package diagnostics

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/paste"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// Runner abstracts tool lookup and execution for tests.
type Runner interface {
	LookPath(name string) (string, error)
	Run(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (execRunner) Run(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// DefaultRunner executes real commands.
var DefaultRunner Runner = execRunner{}

// Check is one capability probe.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Report is the full diagnostic snapshot.
type Report struct {
	// Version is the diagnostics schema version.
	Version int     `json:"version"`
	Checks  []Check `json:"checks"`
	// Model lists problems found by model manifest verification.
	Model []string `json:"model_problems,omitempty"`
	// PasteBackend is the best detected paste backend, if any.
	PasteBackend string `json:"paste_backend,omitempty"`
}

// All runs every check against the given paths.
func All(p config.Paths) Report { return AllWith(DefaultRunner, p) }

// AllWith runs every check with a custom runner.
func AllWith(r Runner, p config.Paths) Report {
	report := Report{Version: 1}
	cfg, err := config.Load(p)
	if err != nil {
		cfg = config.Default()
	}
	// Verify the model manifest once and reuse the result for both the
	// speech-model check and report.Model. Hashing ~230MB of ONNX twice
	// per diagnose call wastes ~1s of CPU and a full disk read.
	modelProblems := transcribe.VerifyModelFor(p, cfg.SpeechModel)
	report.Checks = append(report.Checks,
		toolCheck(r, "python3", "Python 3 interpreter", "Install python3 (e.g. dnf install python3 / apt install python3)"),
		toolCheck(r, "parecord", "Microphone recorder (PulseAudio/PipeWire)", "Install pulseaudio-utils (dnf/apt/pacman)"),
	)
	report.Checks = append(report.Checks, systemdCheck(r))
	report.Checks = append(report.Checks, runtimeCheck(p))
	report.Checks = append(report.Checks, modelCheckFrom(p, cfg.SpeechModel, modelProblems))
	report.Checks = append(report.Checks, micCheck(r))
	report.Checks = append(report.Checks, clipboardCheck(r))
	report.Checks = append(report.Checks, pasteBackendCheck(r))
	report.Checks = append(report.Checks, compositorCheck(r))
	report.Checks = append(report.Checks, pasteProtocolCheck(r))
	report.Checks = append(report.Checks, focusCheck(r))
	report.Checks = append(report.Checks, socketCheck(p))
	report.Model = modelProblems
	return report
}

func toolCheck(r Runner, tool, purpose, fix string) Check {
	path, err := r.LookPath(tool)
	if err != nil {
		return Check{Name: tool, OK: false, Detail: purpose + " not found", Fix: fix}
	}
	return Check{Name: tool, OK: true, Detail: purpose + ": " + path}
}

func systemdCheck(r Runner) Check {
	// `is-system-running` deliberately exits non-zero for a *degraded*
	// manager. A degraded user session can still start and supervise
	// Sasayaki perfectly well, so use a neutral manager query instead.
	// This avoids blocking setup because an unrelated user unit failed.
	out, err := r.Run("systemctl", "--user", "show-environment")
	if err != nil {
		return Check{
			Name:   "systemd user session",
			OK:     false,
			Detail: "systemctl --user unavailable: " + strings.TrimSpace(string(out)),
			Fix:    "Log in to a desktop session with systemd user services (loginctl enable-linger can help on headless setups)",
		}
	}
	return Check{Name: "systemd user session", OK: true, Detail: "systemctl --user works"}
}

func runtimeCheck(p config.Paths) Check {
	ok := fileExists(p.EngineScript()) && fileExists(p.VenvMarker()) && fileExists(filepath.Join(p.VenvDir(), "bin", "python"))
	detail := "private runtime missing"
	if ok {
		detail = "private Python runtime installed"
	}
	return Check{
		Name:   "sasayaki runtime",
		OK:     ok,
		Detail: detail,
		Fix:    "Run `sasayaki setup` to create the private runtime",
	}
}

func modelCheckFrom(p config.Paths, id string, problems []string) Check {
	model, known := transcribe.SpeechModelByID(id)
	if len(problems) == 0 {
		return Check{Name: "speech model", OK: true, Detail: model.Label + " verified (" + model.Architecture + ")"}
	}
	if !known {
		return Check{Name: "speech model", OK: false, Detail: strings.Join(problems, "; "), Fix: "Choose a known model with `sasayaki models`"}
	}
	return Check{
		Name:   "speech model",
		OK:     false,
		Detail: strings.Join(problems, "; "),
		Fix:    "Run `sasayaki setup` to download or repair the model",
	}
}

func micCheck(r Runner) Check {
	out, err := r.Run("pactl", "list", "short", "sources")
	if err != nil {
		return Check{
			Name:   "microphone",
			OK:     false,
			Detail: "could not query audio sources (pactl unavailable or no PulseAudio server)",
			Fix:    "Start a PipeWire/PulseAudio session; install pipewire-pulse or pulseaudio",
		}
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.Contains(line, ".monitor") {
			continue
		}
		count++
	}
	if count == 0 {
		return Check{Name: "microphone", OK: false, Detail: "no input sources found", Fix: "Plug in or enable a microphone and unmute it in your audio settings"}
	}
	return Check{Name: "microphone", OK: true, Detail: fmt.Sprintf("%d input source(s) available", count)}
}

func clipboardCheck(r Runner) Check {
	for _, tool := range []string{"wl-copy", "xclip", "xsel"} {
		if path, err := r.LookPath(tool); err == nil {
			return Check{Name: "clipboard", OK: true, Detail: tool + " found at " + path}
		}
	}
	return Check{Name: "clipboard", OK: false, Detail: "no clipboard tool found", Fix: "Install wl-clipboard (Wayland) or xclip/xsel (X11)"}
}

func pasteBackendCheck(r Runner) Check {
	for _, tool := range []string{"wtype", "ydotool", "xdotool"} {
		if path, err := r.LookPath(tool); err == nil {
			return Check{Name: "paste backend", OK: true, Detail: tool + " found at " + path}
		}
	}
	return Check{
		Name:   "paste backend",
		OK:     false,
		Detail: "no paste backend found; transcription will be copied to the clipboard only",
		Fix:    "Install wtype (Wayland), ydotool or xdotool for automatic paste",
	}
}

// liveProbe returns true when r executes real commands. The compositor,
// paste-protocol and focus checks probe live logind/proc/Wayland state that
// a stub runner cannot simulate; they must not dial the compositor in unit
// tests, so they degrade to a skipped-OK Check under any other runner.
func liveProbe(r Runner) bool {
	_, ok := r.(execRunner)
	return ok
}

// compositorCheck identifies the compositor in the active session and
// verifies the display env points at a live socket. A service started
// before the session can hold env from a previous compositor (Hyprland →
// labwc or the reverse); the active session's compositor is what the paste
// path targets.
func compositorCheck(r Runner) Check {
	if !liveProbe(r) {
		return Check{Name: "compositor", OK: true, Detail: "skipped (stub runner)"}
	}
	// A shell started in an old session can carry stale display env (e.g.
	// a dead wayland-1 from a previous Hyprland login). Heal it the same
	// way the paste path does before judging the sockets.
	paste.EnsureDisplayEnv()
	name := paste.SessionCompositorName()
	if name == "" {
		name = strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
	}
	live := ""
	if wl := os.Getenv("WAYLAND_DISPLAY"); wl != "" && socketExists(filepath.Join(runtimeDir(), wl)) {
		live = "Wayland socket " + wl
	}
	if live == "" {
		if d := os.Getenv("DISPLAY"); d != "" {
			live = "X11 display " + d
		}
	}
	if name == "" && live == "" {
		return Check{
			Name:   "compositor",
			OK:     false,
			Detail: "no compositor session detected",
			Fix:    "Start Sasayaki from inside the graphical session, or export the display env: systemctl --user import-environment WAYLAND_DISPLAY DISPLAY XDG_CURRENT_DESKTOP && systemctl --user restart sasayaki",
		}
	}
	detail := "compositor session detected"
	if name != "" {
		detail = "compositor: " + name
	}
	if live != "" {
		detail += " (" + live + " live)"
	}
	return Check{Name: "compositor", OK: true, Detail: detail}
}

// pasteProtocolCheck verifies the compositor exposes the Wayland globals
// the paste stack depends on: zwp_virtual_keyboard_manager_v1 (wtype
// injection), zwlr_data_control_manager_v1 (wl-copy) and
// zwlr_foreign_toplevel_manager_v1 (focused-window resolution). labwc,
// sway, Hyprland and KWin all expose them; a compositor missing any of
// them cannot do automatic paste. X11 sessions skip the probe.
func pasteProtocolCheck(r Runner) Check {
	if !liveProbe(r) {
		return Check{Name: "paste protocols", OK: true, Detail: "skipped (stub runner)"}
	}
	paste.EnsureDisplayEnv()
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return Check{Name: "paste protocols", OK: true, Detail: "not a Wayland session; automatic paste uses X11 (xdotool/xsel)"}
	}
	globals := paste.WaylandGlobals()
	if len(globals) == 0 {
		return Check{
			Name:   "paste protocols",
			OK:     false,
			Detail: "cannot reach the compositor at " + os.Getenv("WAYLAND_DISPLAY"),
			Fix:    "Verify the session's WAYLAND_DISPLAY is exported into Sasayaki's environment (systemctl --user import-environment WAYLAND_DISPLAY)",
		}
	}
	var missing []string
	for _, req := range []struct{ global, purpose string }{
		{"zwp_virtual_keyboard_manager_v1", "wtype injection"},
		{"zwlr_data_control_manager_v1", "wl-copy clipboard"},
		{"zwlr_foreign_toplevel_manager_v1", "focused-window resolution"},
	} {
		if !globals[req.global] {
			missing = append(missing, req.global+" ("+req.purpose+")")
		}
	}
	if len(missing) > 0 {
		return Check{
			Name:   "paste protocols",
			OK:     false,
			Detail: "compositor lacks: " + strings.Join(missing, ", "),
			Fix:    "Automatic paste needs a compositor exposing the virtual-keyboard, data-control and foreign-toplevel globals (labwc, sway, Hyprland and KWin do)",
		}
	}
	return Check{
		Name:   "paste protocols",
		OK:     true,
		Detail: "compositor exposes the Wayland paste stack (virtual keyboard, data control, foreign toplevel)",
	}
}

// focusCheck resolves the currently focused window through the real
// resolver chain and reports the backend that found it. Under labwc this
// exercises the raw zwlr_foreign_toplevel_manager_v1 client; under
// Hyprland it exercises hyprctl. It proves the paste target lookup works on
// the running compositor.
func focusCheck(r Runner) Check {
	if !liveProbe(r) {
		return Check{Name: "focus resolution", OK: true, Detail: "skipped (stub runner)"}
	}
	paste.EnsureDisplayEnv()
	class, backend, ok := paste.ProbeFocus()
	if !ok {
		return Check{
			Name:   "focus resolution",
			OK:     false,
			Detail: "no focused window resolved (empty desktop or no compositor access)",
			Fix:    "Focus a window and re-run; if it still fails, check the compositor and paste-protocol checks above",
		}
	}
	return Check{Name: "focus resolution", OK: true, Detail: "focused window " + class + " resolved via " + backend}
}

func runtimeDir() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}

func socketExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func socketCheck(p config.Paths) Check {
	fi, err := os.Stat(p.Socket())
	if err != nil {
		return Check{Name: "control socket", OK: false, Detail: "service is not running (no socket)", Fix: "Run `sasayaki service start`"}
	}
	mode := fi.Mode()
	if mode&os.ModeSocket == 0 {
		return Check{Name: "control socket", OK: false, Detail: "socket path exists but is not a socket", Fix: "Remove " + p.Socket() + " and restart the service"}
	}
	return Check{Name: "control socket", OK: true, Detail: "service socket present at " + p.Socket()}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
