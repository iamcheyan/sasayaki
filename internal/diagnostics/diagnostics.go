// Package diagnostics runs prerequisite and capability checks and produces
// human- and machine-readable reports with concrete remediation. It never
// modifies the system.
package diagnostics

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
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
	return exec.Command(name, args...).CombinedOutput()
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
	report.Checks = append(report.Checks,
		toolCheck(r, "python3", "Python 3 interpreter", "Install python3 (e.g. dnf install python3 / apt install python3)"),
		toolCheck(r, "parecord", "Microphone recorder (PulseAudio/PipeWire)", "Install pulseaudio-utils (dnf/apt/pacman)"),
	)
	report.Checks = append(report.Checks, systemdCheck(r))
	report.Checks = append(report.Checks, runtimeCheck(p))
	report.Checks = append(report.Checks, modelCheck(p))
	report.Checks = append(report.Checks, micCheck(r))
	report.Checks = append(report.Checks, clipboardCheck(r))
	report.Checks = append(report.Checks, pasteBackendCheck(r))
	report.Checks = append(report.Checks, socketCheck(p))
	report.Model = transcribe.VerifyModel(p)
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
	out, err := r.Run("systemctl", "--user", "is-system-running")
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

func modelCheck(p config.Paths) Check {
	problems := transcribe.VerifyModel(p)
	if len(problems) == 0 {
		return Check{Name: "speech model", OK: true, Detail: "SenseVoice model verified (" + transcribe.Model.Version + ")"}
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
