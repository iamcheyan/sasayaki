//go:build linux

package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/paste"
)

// systemdCheck verifies a systemd user session can supervise the service.
func sessionCheck(r Runner) Check {
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
