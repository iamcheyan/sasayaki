//go:build darwin

package diagnostics

// launchdCheck verifies launchctl can supervise the user agent — on macOS
// launchd replaces systemd and is always present; a missing binary means a
// broken system install.
func sessionCheck(r Runner) Check {
	if _, err := r.LookPath("launchctl"); err != nil {
		return Check{
			Name:   "launchd user session",
			OK:     false,
			Detail: "launchctl not found",
			Fix:    "launchctl ships with macOS; a missing binary indicates a broken system install",
		}
	}
	return Check{Name: "launchd user session", OK: true, Detail: "launchctl available"}
}

// micCheck on darwin: recording goes through ffmpeg's avfoundation input
// from the CLI, or in-process AVAudioEngine from the menubar app. TCC
// grants apply per process, so only the tool presence is deterministic.
func micCheck(r Runner) Check {
	if _, err := r.LookPath("ffmpeg"); err != nil {
		return Check{
			Name:   "microphone",
			OK:     false,
			Detail: "ffmpeg not found",
			Fix:    "brew install ffmpeg — needed for terminal recording; the menubar app records natively and does not need it",
		}
	}
	return Check{Name: "microphone", OK: true, Detail: "ffmpeg avfoundation capture available (menubar app records natively)"}
}

func clipboardCheck(r Runner) Check {
	if _, err := r.LookPath("pbcopy"); err == nil {
		return Check{Name: "clipboard", OK: true, Detail: "pbcopy found"}
	}
	return Check{Name: "clipboard", OK: false, Detail: "pbcopy not found", Fix: "pbcopy ships with macOS; a missing binary indicates a broken system install"}
}

func pasteBackendCheck(r Runner) Check {
	if _, err := r.LookPath("osascript"); err == nil {
		return Check{Name: "paste backend", OK: true, Detail: "osascript Cmd+V keystroke available"}
	}
	return Check{Name: "paste backend", OK: false, Detail: "osascript not found", Fix: "osascript ships with macOS; a missing binary indicates a broken system install"}
}

// compositorCheck / pasteProtocolCheck / focusCheck have no macOS analogue:
// paste injection is a System Events keystroke into the frontmost app and
// needs only the Accessibility grant, which a script can neither read nor
// grant. They report as not-applicable instead of failing.
func compositorCheck(_ Runner) Check {
	return Check{Name: "compositor", OK: true, Detail: "not applicable on macOS"}
}

func pasteProtocolCheck(_ Runner) Check {
	return Check{Name: "paste protocols", OK: true, Detail: "System Events keystroke (Accessibility grant required)"}
}

func focusCheck(_ Runner) Check {
	return Check{Name: "focus resolution", OK: true, Detail: "frontmost app via System Events"}
}

// runtimeDir is kept for symmetry with the linux checks file; nothing on
// darwin calls it today.
func runtimeDir() string { return "" }
