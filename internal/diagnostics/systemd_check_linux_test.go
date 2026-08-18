//go:build linux

package diagnostics

import (
	"strings"
	"testing"
)

func TestSystemdCheckUsesNeutralManagerQuery(t *testing.T) {
	var got []string
	runner := recordingRunner{run: func(name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		if name == "pactl" {
			return []byte(pactlListing), nil
		}
		return nil, nil
	}}
	check := sessionCheck(runner)
	if !check.OK {
		t.Fatalf("neutral systemd query should succeed: %+v", check)
	}
	want := []string{"systemctl", "--user", "show-environment"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("systemd argv = %q, want %q", got, want)
	}
}
