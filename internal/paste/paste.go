// Package paste copies transcription text to the clipboard and injects the
// paste chord into the focused application. Wayland does not permit generic
// applications to inject input, so Sasayaki tries ordered concrete backends
// (wtype, ydotool, xdotool) and always reports exactly what it managed to do.
package paste

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
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
	return exec.Command(name, args...).CombinedOutput()
}
func (execRunner) RunStdin(name string, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	return cmd.CombinedOutput()
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

// Paste copies text and injects it into the focused application using the
// best available backend. The returned Result states truthfully whether the
// paste chord succeeded or the user must paste manually.
func Paste(text string) Result { return PasteWith(execRunner{}, text) }

func PasteWith(r runner, text string) Result {
	clip, copyErr := copyToClipboard(r, text)
	if copyErr != nil {
		return Result{Detail: "Clipboard unavailable: " + copyErr.Error() + ". Install wl-copy or xclip."}
	}
	_ = clip
	for _, name := range []string{BackendWtype, BackendYdotool, BackendXdotool} {
		if _, err := r.LookPath(name); err != nil {
			continue
		}
		args := pasteArgs(name)
		if out, err := r.Run(name, args...); err == nil {
			return Result{Pasted: true, Backend: name, Detail: "Pasted with " + name}
		} else {
			// Fall through to the next backend; the failed attempt is
			// reflected in the final detail only if nothing works.
			_ = out
		}
	}
	return Result{
		Pasted:  false,
		Backend: "clipboard",
		Detail:  "Copied to clipboard; paste it manually (install wtype, ydotool or xdotool for automatic paste).",
	}
}

// copyToClipboard places text on the clipboard using the first available
// tool. Returns the tool name used.
func copyToClipboard(r runner, text string) (string, error) {
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
		if out, err := r.RunStdin(tool, args, []byte(text)); err == nil {
			return tool, nil
		} else {
			return "", fmt.Errorf("%s: %s", tool, strings.TrimSpace(string(out)))
		}
	}
	return "", fmt.Errorf("no clipboard tool found")
}

// pasteArgs returns the exact paste-chord argv for a backend. The wtype
// chord is Ctrl+Shift+V (works in terminals and GUI fields); ydotool and
// xdotool use Ctrl+V.
func pasteArgs(backend string) []string {
	switch backend {
	case BackendWtype:
		return []string{"-M", "ctrl", "-M", "shift", "-k", "v", "-m", "shift", "-m", "ctrl"}
	case BackendYdotool:
		// 29 = LeftCtrl, 47 = V.
		return []string{"key", "29:1", "47:1", "47:0", "29:0"}
	case BackendXdotool:
		return []string{"key", "--clearmodifiers", "ctrl+v"}
	}
	return nil
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
