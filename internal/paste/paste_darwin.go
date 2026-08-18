//go:build darwin

package paste

import "strings"

// BackendOsascriptKeystroke injects the paste chord by sending a single
// Cmd+V keystroke through System Events — the standard paste chord every
// macOS application binds.
const BackendOsascriptKeystroke = "osascript-keystroke"

// ClipPbcopy is the macOS pasteboard client.
const ClipPbcopy = "pbcopy"

// Paste copies text to the system pasteboard and injects Cmd+V into the
// frontmost application. The returned Result states truthfully whether the
// keystroke was delivered or the user must paste manually.
func Paste(text string) Result { return PasteWith(execRunner{}, text) }

// PasteWith is Paste with an injectable runner so tests can assert the exact
// pbcopy/osascript argv. macOS needs none of Linux's session plumbing: System
// Events delivers the keystroke to whatever application is frontmost (no
// focus resolution), and launchd agents run inside the GUI session (no
// display-env repair).
func PasteWith(r runner, text string) Result {
	if err := copyToPasteboard(r, []byte(text)); err != nil {
		return Result{Detail: "Clipboard unavailable: " + err.Error() + ". pbcopy ships with macOS."}
	}
	if injectPasteChord(r) {
		return pasted(BackendOsascriptKeystroke)
	}
	return Result{
		Pasted:  false,
		Backend: "clipboard",
		Detail:  "Copied to clipboard; paste it manually (automatic paste needs the Accessibility permission: System Settings > Privacy & Security > Accessibility, enable the app or terminal that runs sasayaki).",
	}
}

// copyToPasteboard places payload on the system pasteboard with pbcopy.
// Unlike wl-copy, pbcopy is a plain pipe — the pasteboard is current the
// moment it exits — so no settle delay is needed before injecting.
func copyToPasteboard(r runner, payload []byte) error {
	if _, err := r.LookPath(ClipPbcopy); err != nil {
		return err
	}
	_, err := r.RunStdin(ClipPbcopy, nil, payload)
	return err
}

// keystrokeScript asks System Events for one Cmd+V keystroke.
const keystrokeScript = `tell application "System Events" to keystroke "v" using command down`

// injectPasteChord sends Cmd+V to the frontmost application through System
// Events. osascript exits nonzero when the process lacks the Accessibility
// permission, which is the only truthful signal available for whether the
// keystroke was delivered. Run bounds it at 3 seconds so a wedged
// AppleScript cannot hang a paste.
func injectPasteChord(r runner) bool {
	_, err := r.Run("osascript", "-e", keystrokeScript)
	return err == nil
}

// ClipboardAvailable reports whether a clipboard tool is installed. pbcopy
// ships with macOS, so this fails only on a broken system.
func ClipboardAvailable(r runner) bool {
	_, err := r.LookPath(ClipPbcopy)
	return err == nil
}

// BestBackend returns the first usable paste backend, or "" when none is
// installed. osascript ships with macOS, so the practical failure is a
// missing Accessibility grant, which only the paste attempt itself reveals.
func BestBackend(r runner) string {
	if _, err := r.LookPath("osascript"); err == nil {
		return BackendOsascriptKeystroke
	}
	return ""
}

// Available reports whether both a clipboard tool and a paste backend exist,
// i.e. the full copy-and-paste path is usable.
func Available(r runner) bool {
	return ClipboardAvailable(r) && BestBackend(r) != ""
}

// AvailableDefault is Available with the real command runner.
func AvailableDefault() bool { return Available(execRunner{}) }

// EnsureDisplayEnv heals stale display env on Linux; macOS launchd agents
// always run inside the GUI session, so there is nothing to repair.
func EnsureDisplayEnv() {}

// SessionCompositorName returns the process name of the session compositor.
// macOS has no compositor — the window server owns the screen — so the
// truthful answer is none.
func SessionCompositorName() string { return "" }

// WaylandGlobals reports the Wayland protocol globals of the session; there
// is no Wayland on macOS.
func WaylandGlobals() map[string]bool { return nil }

// ProbeFocus resolves the frontmost application's bundle id through System
// Events — the same service the paste keystroke depends on. It fails exactly
// when the process lacks the Accessibility permission, so a failed probe
// truthfully warns that a paste will be clipboard-only.
func ProbeFocus() (class, backend string, ok bool) {
	out, err := execRunner{}.Run("osascript", "-e",
		"tell application \"System Events\" to get bundle identifier of first application process whose frontmost is true")
	if err != nil {
		return "", "", false
	}
	bundle := strings.TrimSpace(string(out))
	if bundle == "" {
		return "", "", false
	}
	return bundle, "system-events", true
}
