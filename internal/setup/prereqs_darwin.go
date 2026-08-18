//go:build darwin

package setup

import (
	"fmt"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// pythonInstallHint completes the "python3 is required but not installed"
// setup error on this platform.
const pythonInstallHint = "install it (e.g. brew install python3, or run xcode-select --install) and re-run `sasayaki setup`"

// checkPrereqs verifies the macOS tools setup needs and then the shared
// disk-space and network gates. It never changes anything. The microphone is
// deliberately not probed: TCC grants it to the Sasayaki menubar app, and a
// terminal probing it would only test the terminal's own grant.
func checkPrereqs(p config.Paths) error {
	var missing []string
	for _, tool := range []string{"python3", "pbcopy", "osascript", "launchctl"} {
		if _, err := lookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing prerequisites — nothing was changed; install them and re-run `sasayaki setup`:\n  - %s",
			strings.Join(missing, "\n  - "))
	}
	progress("note: the microphone is granted to the Sasayaki menubar app (TCC); setup does not probe it")
	// ffmpeg only backs CLI recording and audio normalization; the menubar
	// app records in-process, so a missing ffmpeg is a warning, not a
	// blocker.
	if _, err := lookPath("ffmpeg"); err != nil {
		progress("warning: ffmpeg not found — CLI recording and normalization are disabled; install it with `brew install ffmpeg`")
	}
	return checkDiskAndNetwork(p)
}
