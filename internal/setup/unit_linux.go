//go:build linux

package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// unitTemplate renders the systemd user unit. The service runs the actual
// installed binary with `sasayaki serve`; Restart=on-failure makes systemd
// resurrect a crashed daemon.
const unitTemplate = `[Unit]
Description=Sasayaki local voice input
After=graphical-session.target

[Service]
Type=simple
# systemd user services may not inherit the desktop shell PATH. Keep the
# external audio, clipboard, and input tools discoverable for the daemon.
Environment=PATH=%s
%sExecStart=%s serve
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`

// serviceEnvironment discovers common NixOS paths without making the unit
// depend on a machine-specific /nix/store hash. On other distributions the
// extra paths simply do not exist and are omitted. The Nix C++ runtime is
// needed by sherpa-onnx wheels, while parecord and wl-copy live in the system
// or user profile.
func serviceEnvironment(home string) (string, string) {
	pathParts := []string{"/run/current-system/sw/bin", filepath.Join(home, ".nix-profile/bin"), "/nix/profile/bin", filepath.Join(home, ".local/bin"), "/usr/local/bin", "/usr/bin", "/bin"}
	libParts := []string{"/run/current-system/sw/lib", filepath.Join(home, ".nix-profile/lib")}
	for _, pattern := range []string{"/nix/store/*-gcc-*-lib/lib", "/nix/store/*-gcc-*/lib"} {
		matches, _ := filepath.Glob(pattern)
		libParts = append(libParts, matches...)
	}
	pathValue := strings.Join(pathParts, ":")
	var ld string
	seen := map[string]bool{}
	for _, p := range libParts {
		if _, err := os.Stat(p); err == nil && !seen[p] {
			seen[p] = true
			if ld != "" {
				ld += ":"
			}
			ld += p
		}
	}
	ldLine := ""
	if ld != "" {
		ldLine = "Environment=LD_LIBRARY_PATH=" + ld + "\n"
	}
	return pathValue, ldLine
}

// writeUnit writes the user unit when it does not already target the given
// binary.
func writeUnit(p config.Paths, binary string) error {
	if binary == "" {
		return fmt.Errorf("sasayaki binary path is unknown; install sasayaki into PATH and re-run setup")
	}
	dir := filepath.Dir(p.ServiceFile())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pathValue, ldLine := serviceEnvironment(os.Getenv("HOME"))
	return os.WriteFile(p.ServiceFile(), []byte(fmt.Sprintf(unitTemplate, pathValue, ldLine, binary)), 0o600)
}

// unitCurrent reports whether the existing unit matches what setup would
// write for the current binary.
func unitCurrent(p config.Paths) bool {
	if serviceBinary == "" {
		return false
	}
	b, err := os.ReadFile(p.ServiceFile())
	if err != nil {
		return false
	}
	text := string(b)
	if !strings.Contains(text, "ExecStart="+serviceBinary+" serve") {
		return false
	}
	// Re-render units created by older Sasayaki versions. In particular,
	// NixOS keeps parecord and the C++ runtime outside /usr/bin; an old fixed
	// PATH makes the repair button appear to succeed while recording fails.
	if _, err := os.Stat("/run/current-system/sw/bin"); err == nil &&
		!strings.Contains(text, "Environment=PATH=/run/current-system/sw/bin") {
		return false
	}
	if _, err := os.Stat("/nix/store"); err == nil &&
		!strings.Contains(text, "Environment=LD_LIBRARY_PATH=") {
		return false
	}
	return true
}

// systemctl executes systemctl --user.
var systemctl = func(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// enableAndStart reloads systemd, enables the user unit and refreshes a
// daemon that was already running.
func enableAndStart() (string, error) {
	if err := systemctl("daemon-reload"); err != nil {
		return "", err
	}
	if err := systemctl("enable", "--now", "sasayaki.service"); err != nil {
		return "", err
	}
	// Applying setup is also how a newly selected local model becomes
	// active. `enable --now` does not restart an already active unit.
	if err := systemctl("restart", "sasayaki.service"); err != nil {
		return "", err
	}
	return "service enabled, started and refreshed", nil
}
