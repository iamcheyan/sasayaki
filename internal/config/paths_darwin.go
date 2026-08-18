//go:build darwin

package config

import (
	"os"
	"path/filepath"
)

// LaunchAgentLabel is the launchd label of Sasayaki's user agent.
const LaunchAgentLabel = "io.github.iamcheyan.sasayaki"

// ServiceFile is the LaunchAgent plist. launchd only scans ~/Library/
// LaunchAgents, so unlike the XDG-derived locations it follows the real home.
func (p Paths) ServiceFile() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", LaunchAgentLabel+".plist")
}

// LogOutPath and LogErrPath receive the daemon's stdout and stderr: macOS has
// no per-user journal, so the LaunchAgent redirects into the state home.
func (p Paths) LogOutPath() string { return filepath.Join(p.StateHome, "logs", "sasayaki.log") }
func (p Paths) LogErrPath() string { return filepath.Join(p.StateHome, "logs", "sasayaki.err.log") }
