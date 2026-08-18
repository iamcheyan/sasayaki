//go:build linux

package config

import (
	"path/filepath"
	"testing"
)

func TestServiceFileIsTheSystemdUserUnit(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "cfg"))
	want := filepath.Join(base, "cfg", "systemd", "user", "sasayaki.service")
	if got := NewPaths().ServiceFile(); got != want {
		t.Errorf("service file = %q, want %q", got, want)
	}
}
