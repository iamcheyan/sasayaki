//go:build darwin

package config

import (
	"path/filepath"
	"testing"
)

func TestServiceFileIsTheLaunchAgentPlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, "Library", "LaunchAgents", LaunchAgentLabel+".plist")
	if got := NewPaths().ServiceFile(); got != want {
		t.Errorf("service file = %q, want %q", got, want)
	}
}

func TestLogPathsLiveInTheStateHome(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	p := NewPaths()
	if want := filepath.Join(base, "state", "sasayaki", "logs", "sasayaki.log"); p.LogOutPath() != want {
		t.Errorf("stdout log = %q, want %q", p.LogOutPath(), want)
	}
	if want := filepath.Join(base, "state", "sasayaki", "logs", "sasayaki.err.log"); p.LogErrPath() != want {
		t.Errorf("stderr log = %q, want %q", p.LogErrPath(), want)
	}
}
