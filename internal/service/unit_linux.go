//go:build linux

package service

import (
	"fmt"
	"os/exec"
	"strings"
)

// Systemctl runs a systemctl --user command.
func Systemctl(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// IsActive reports whether the user unit is active.
func IsActive() bool { return Systemctl("is-active", "--quiet", UnitName) == nil }
