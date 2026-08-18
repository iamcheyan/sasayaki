//go:build darwin

package setup

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/diagnostics"
)

// darwinSetupPaths isolates the LaunchAgent plist under a temp home; on
// darwin ServiceFile follows the real home, not the XDG roots.
func darwinSetupPaths(t *testing.T) config.Paths {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return setupPaths(t)
}

func TestDarwinPrereqsFailOnMissingPython3(t *testing.T) {
	p := darwinSetupPaths(t)
	stubEnvironment(t, diagnostics.Report{Version: 1}) // darwin prereqs never consult diagnostics
	orig := lookPath
	lookPath = func(name string) (string, error) {
		if name == "python3" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	t.Cleanup(func() { lookPath = orig })
	SetBinary("/usr/local/bin/sasayaki")

	session := NewSession(p)
	result := session.Run()
	if result.AllOK() {
		t.Fatal("setup must fail when python3 is missing")
	}
	if len(result.Steps) == 0 || result.Steps[0].ID != "prereqs" || result.Steps[0].Status != StepFailed {
		t.Fatalf("first step should be the failing prereqs step: %+v", result.Steps)
	}
	if !strings.Contains(result.Steps[0].Error, "python3") {
		t.Fatalf("prereq failure should name the missing tool: %q", result.Steps[0].Error)
	}
	if _, err := os.Stat(p.ConfigFile()); !os.IsNotExist(err) {
		t.Fatal("setup wrote files despite failing prerequisites")
	}
}

func TestDarwinPrereqsTolerateMissingFFmpeg(t *testing.T) {
	p := darwinSetupPaths(t)
	stubEnvironment(t, diagnostics.Report{Version: 1})
	server := newFakeModelServer(t)
	wireFakeModel(t, server.server.URL)
	orig := lookPath
	lookPath = func(name string) (string, error) {
		if name == "ffmpeg" {
			return "", errors.New("not found")
		}
		return "/usr/bin/" + name, nil
	}
	t.Cleanup(func() { lookPath = orig })
	SetBinary("/usr/local/bin/sasayaki")

	session := NewSession(p)
	result := session.Run()
	if !result.AllOK() {
		t.Fatalf("a missing ffmpeg is a warning, not a blocker: %+v", result.Steps)
	}
	if result.Steps[0].ID != "prereqs" || result.Steps[0].Status != StepDone {
		t.Fatalf("prereqs should pass without ffmpeg: %+v", result.Steps[0])
	}
}

func TestDarwinPlanWritesLaunchAgent(t *testing.T) {
	p := darwinSetupPaths(t)
	stubEnvironment(t, diagnostics.Report{Version: 1})
	server := newFakeModelServer(t)
	wireFakeModel(t, server.server.URL)
	SetBinary("/usr/local/bin/sasayaki")

	session := NewSession(p)
	if result := session.Run(); !result.AllOK() {
		t.Fatalf("setup failed: %+v", result.Steps)
	}

	plist, err := os.ReadFile(p.ServiceFile())
	if err != nil {
		t.Fatalf("setup did not create the LaunchAgent: %v", err)
	}
	for _, want := range []string{
		"<string>io.github.iamcheyan.sasayaki</string>",
		"<string>/usr/local/bin/sasayaki</string>",
		"<string>serve</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<key>StandardOutPath</key>",
		"<string>" + p.LogOutPath() + "</string>",
	} {
		if !strings.Contains(string(plist), want) {
			t.Fatalf("LaunchAgent plist missing %s:\n%s", want, plist)
		}
	}

	// Second run: the plist already targets the current binary, so the
	// service step is skipped.
	session2 := NewSession(p)
	result2 := session2.Run()
	if !result2.AllOK() {
		t.Fatalf("second darwin setup failed: %+v", result2.Steps)
	}
	for _, step := range result2.Steps {
		if step.ID == "service" && step.Status != StepSkipped {
			t.Fatalf("second run should skip the service step: %+v", step)
		}
	}
}

func TestDarwinUnitCurrentDetection(t *testing.T) {
	p := darwinSetupPaths(t)
	SetBinary("/opt/sasayaki")
	if err := writeUnit(p, "/opt/sasayaki"); err != nil {
		t.Fatal(err)
	}
	if !unitCurrent(p) {
		t.Fatal("unitCurrent should accept the freshly written plist")
	}
	// A plist pointing elsewhere must be rewritten.
	if err := writeUnit(p, "/old/path"); err != nil {
		t.Fatal(err)
	}
	if unitCurrent(p) {
		t.Fatal("unitCurrent should reject a stale binary path")
	}
}
