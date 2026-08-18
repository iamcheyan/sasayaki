//go:build darwin

package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/service"
)

// plistTemplate renders the LaunchAgent that runs the daemon. launchd starts
// it at login (RunAtLoad) and resurrects crashes (KeepAlive); stdout and
// stderr go to Sasayaki's private logs because there is no per-user journal.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
	</array>
	<!-- launchd agents get the bare system PATH (/usr/bin:...); homebrew
	     tools (ffmpeg for recording, kitty for the TUI) live outside it,
	     so carry the installing user's PATH through. -->
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`

// writeUnit writes the LaunchAgent when it does not already target the given
// binary.
func writeUnit(p config.Paths, binary string) error {
	if binary == "" {
		return fmt.Errorf("sasayaki binary path is unknown; install sasayaki into PATH and re-run setup")
	}
	if err := os.MkdirAll(filepath.Dir(p.ServiceFile()), 0o700); err != nil {
		return err
	}
	// launchd creates the log files itself, but only inside existing
	// directories.
	if err := os.MkdirAll(filepath.Dir(p.LogOutPath()), 0o700); err != nil {
		return err
	}
	// launchd agents otherwise see only the bare system PATH; carry the
	// installing user's PATH so homebrew tools (ffmpeg, kitty) resolve.
	agentPath := os.Getenv("PATH")
	if agentPath == "" {
		agentPath = "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin"
	}
	plist := fmt.Sprintf(plistTemplate, config.LaunchAgentLabel, binary, agentPath, p.LogOutPath(), p.LogErrPath())
	return os.WriteFile(p.ServiceFile(), []byte(plist), 0o600)
}

// unitCurrent reports whether the existing LaunchAgent matches what setup
// would write for the current binary.
func unitCurrent(p config.Paths) bool {
	if serviceBinary == "" {
		return false
	}
	b, err := os.ReadFile(p.ServiceFile())
	if err != nil {
		return false
	}
	text := string(b)
	// ProgramArguments must run the current binary with `serve`, and the
	// agent must still be kept alive.
	return strings.Contains(text, "<string>"+serviceBinary+"</string>") &&
		strings.Contains(text, "<string>serve</string>") &&
		strings.Contains(text, "<key>KeepAlive</key>")
}

// systemctl translates the systemd vocabulary of the shared plan onto
// launchd; service.Systemctl owns the mapping table.
var systemctl = func(args ...string) error { return service.Systemctl(args...) }

// enableAndStart loads the LaunchAgent and (re)starts the daemon. The darwin
// translation of `enable --now` already restarts a running agent, so unlike
// the systemd path no separate restart pass is needed.
func enableAndStart() (string, error) {
	if err := systemctl("enable", "--now", config.LaunchAgentLabel); err != nil {
		return "", err
	}
	return "LaunchAgent loaded, started and refreshed", nil
}
