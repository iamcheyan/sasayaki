//go:build darwin

package service

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// stubLaunchctl records every translated launchctl invocation and delegates
// the reply to the fake.
func stubLaunchctl(t *testing.T, reply func(args []string) error) *[][]string {
	t.Helper()
	var calls [][]string
	orig := launchctl
	launchctl = func(args ...string) error {
		calls = append(calls, args)
		return reply(args)
	}
	t.Cleanup(func() { launchctl = orig })
	return &calls
}

func joined(calls [][]string) []string {
	out := make([]string, len(calls))
	for i, call := range calls {
		out[i] = strings.Join(call, " ")
	}
	return out
}

func TestSystemctlTranslatesStartVerbs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	gui := fmt.Sprintf("gui/%d", os.Getuid())
	plist := config.NewPaths().ServiceFile()
	want := []string{"bootstrap " + gui + " " + plist}

	for _, verbs := range [][]string{
		{"enable", "--now", "sasayaki.service"},
		{"restart", "sasayaki.service"},
		{"start", "sasayaki.service"},
	} {
		calls := stubLaunchctl(t, func([]string) error { return nil })
		if err := Systemctl(verbs...); err != nil {
			t.Fatalf("Systemctl(%v): %v", verbs, err)
		}
		if got := joined(*calls); !slices.Equal(got, want) {
			t.Fatalf("Systemctl(%v) ran %v, want %v", verbs, got, want)
		}
	}
}

func TestSystemctlToleratesAlreadyBootstrappedAgent(t *testing.T) {
	bootstraps := 0
	calls := stubLaunchctl(t, func(args []string) error {
		if args[0] == "bootstrap" {
			bootstraps++
			if bootstraps == 1 {
				// launchd's reply for an agent that is already loaded; the
				// bootout + retry cycle below then succeeds.
				return errors.New("Bootstrap failed: 5: Input/output error")
			}
		}
		return nil
	})
	if err := Systemctl("restart", "sasayaki.service"); err != nil {
		t.Fatalf("restarting an already-bootstrapped agent must not fail: %v", err)
	}
	// Already loaded: the agent is cycled (bootout + bootstrap) so the
	// CURRENT plist is read, then kickstarted.
	gui := fmt.Sprintf("gui/%d", os.Getuid())
	plist := config.NewPaths().ServiceFile()
	want := []string{
		"bootstrap " + gui + " " + plist,
		"bootout " + gui + "/" + config.LaunchAgentLabel,
		"bootstrap " + gui + " " + plist,
	}
	if got := joined(*calls); !slices.Equal(got, want) {
		t.Fatalf("restart must cycle the agent: %v", got)
	}
}

func TestSystemctlReportsRealStartFailures(t *testing.T) {
	stubLaunchctl(t, func([]string) error { return errors.New("operation failed") })
	if err := Systemctl("enable", "--now", "sasayaki.service"); err == nil {
		t.Fatal("enable -- now must fail when kickstart fails")
	}
}

func TestSystemctlTranslatesStopVerbs(t *testing.T) {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), config.LaunchAgentLabel)
	for _, verbs := range [][]string{
		{"disable", "--now", "sasayaki.service"},
		{"stop", "sasayaki.service"},
	} {
		calls := stubLaunchctl(t, func([]string) error { return nil })
		if err := Systemctl(verbs...); err != nil {
			t.Fatalf("Systemctl(%v): %v", verbs, err)
		}
		if got := joined(*calls); !slices.Equal(got, []string{"bootout " + target}) {
			t.Fatalf("Systemctl(%v) ran %v, want bootout %s", verbs, got, target)
		}
	}
}

func TestSystemctlToleratesUnloadedAgent(t *testing.T) {
	stubLaunchctl(t, func(args []string) error {
		if args[0] == "bootout" {
			return errors.New("Boot-out error: 2: No such file or directory")
		}
		return errors.New("could not find service") // launchctl print: not loaded
	})
	if err := Systemctl("disable", "--now", "sasayaki.service"); err != nil {
		t.Fatalf("disabling an unloaded agent must not fail: %v", err)
	}
}

func TestSystemctlFailsWhenAgentRefusesBootout(t *testing.T) {
	stubLaunchctl(t, func(args []string) error {
		if args[0] == "bootout" {
			return errors.New("Boot-out error: 5: Input/output error")
		}
		return nil // print succeeds: the agent is still loaded
	})
	if err := Systemctl("stop", "sasayaki.service"); err == nil {
		t.Fatal("stop must fail when a loaded agent cannot be booted out")
	}
}

func TestSystemctlDaemonReloadRunsNothing(t *testing.T) {
	stubLaunchctl(t, func(args []string) error {
		t.Errorf("daemon-reload must not run launchctl: %v", args)
		return nil
	})
	if err := Systemctl("daemon-reload"); err != nil {
		t.Fatalf("daemon-reload must be a no-op: %v", err)
	}
}

func TestSystemctlRejectsUntranslatedVerbs(t *testing.T) {
	stubLaunchctl(t, func([]string) error { return nil })
	if err := Systemctl("mask", "sasayaki.service"); err == nil {
		t.Fatal("verbs without a launchctl translation must fail loudly")
	}
}

func TestIsActiveProbesTheControlSocket(t *testing.T) {
	// macOS caps socket paths at 104 characters; keep the runtime dir short.
	dir, err := os.MkdirTemp("", "isa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	sock := config.NewPaths().Socket()
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	if IsActive() {
		t.Fatal("IsActive must be false without a listener")
	}
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if !IsActive() {
		t.Fatal("IsActive must see the listening daemon socket")
	}
}
