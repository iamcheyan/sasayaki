//go:build linux

package config

import "path/filepath"

// ServiceFile is the systemd user unit. It is derived from the XDG config
// home so redirected environments stay hermetic.
func (p Paths) ServiceFile() string {
	return filepath.Join(filepath.Dir(p.ConfigHome), "systemd", "user", "sasayaki.service")
}
