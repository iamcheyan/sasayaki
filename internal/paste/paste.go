// Package paste copies transcription text to the clipboard and injects the
// paste chord into the focused application. Neither Wayland nor macOS lets a
// generic application inject input freely, so each platform picks a strategy
// that fits its permission model:
//
//   - Linux (paste_linux.go): virtual-keyboard chords (wtype → ydotool →
//     xdotool) chosen by the focused window's class — terminals paste with
//     Shift+Insert or Ctrl+Shift+V, GUI apps with Ctrl+V — an xsel+xdotool
//     path for XWayland windows, and a native `kitty @` remote paste when the
//     exact target resolves;
//   - macOS (paste_darwin.go): pbcopy writes the system pasteboard, then one
//     Cmd+V keystroke is sent to the frontmost application through System
//     Events, which requires the pasting process to hold the Accessibility
//     permission.
//
// The returned Result always states truthfully what was achieved; a clipboard
// write without injection is never reported as a paste.
package paste

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
	// A clipboard tool can fork a background daemon that holds the selection
	// (wl-copy on Linux does). CombinedOutput would block on EOF of the
	// stdout pipe, which the forked daemon inherits — so it only returns
	// when the daemon exits (i.e. when the clipboard is next replaced), not
	// when the parent finishes. Routing stdout/stderr to /dev/null (a real
	// FD, not a pipe) lets the daemon inherit something that never blocks
	// us, so cmd.Run() returns as soon as the parent has set the clipboard.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: open /dev/null: %w", name, err)
	}
	defer devnull.Close()
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	return nil, cmd.Run()
}

func pasted(transport string) Result {
	return Result{Pasted: true, Backend: transport, Detail: "Pasted with " + transport}
}

// DefaultRunner exposes the real command runner for consumers that need
// capability checks without constructing their own.
func DefaultRunner() runner { return execRunner{} }
