//go:build linux

package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iamcheyan/sasayaki/internal/diagnostics"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

func okDiagnostics() diagnostics.Report {
	report := diagnostics.Report{Version: 1}
	for _, name := range []string{"python3", "parecord", "microphone", "clipboard", "paste backend", "systemd user session"} {
		report.Checks = append(report.Checks, diagnostics.Check{Name: name, OK: true})
	}
	return report
}

func TestPlanPrereqsFailOnMissingTools(t *testing.T) {
	p := setupPaths(t)
	// One failing prerequisite check must abort before anything is written.
	report := diagnostics.Report{Version: 1, Checks: []diagnostics.Check{
		{Name: "python3", OK: false, Fix: "Install python3"},
		{Name: "parecord", OK: true},
		{Name: "microphone", OK: true},
		{Name: "clipboard", OK: true},
		{Name: "paste backend", OK: true},
		{Name: "systemd user session", OK: true},
	}}
	stubEnvironment(t, report)
	SetBinary("/usr/local/bin/sasayaki")

	session := NewSession(p)
	result := session.Run()
	if result.AllOK() {
		t.Fatalf("setup must fail when prerequisites are missing: %+v", result.Steps)
	}
	if len(result.Steps) == 0 || result.Steps[0].ID != "prereqs" || result.Steps[0].Status != StepFailed {
		t.Fatalf("first step should be the failing prereqs step: %+v", result.Steps)
	}
	if !strings.Contains(result.Steps[0].Error, "nothing was changed") {
		t.Fatalf("prereq failure must say nothing was changed: %q", result.Steps[0].Error)
	}
	if !strings.Contains(result.Steps[0].Error, "python3") {
		t.Fatalf("prereq failure should name the missing tool: %q", result.Steps[0].Error)
	}
	// Nothing was written.
	if _, err := os.Stat(p.ConfigFile()); !os.IsNotExist(err) {
		t.Fatal("setup wrote files despite failing prerequisites")
	}
}

func TestPlanFullRunAndIdempotency(t *testing.T) {
	p := setupPaths(t)
	stubEnvironment(t, okDiagnostics())
	server := newFakeModelServer(t)
	wireFakeModel(t, server.server.URL)
	SetBinary("/usr/local/bin/sasayaki")

	session := NewSession(p)
	result := session.Run()
	if !result.AllOK() {
		t.Fatalf("setup failed: %+v", result.Steps)
	}

	// Directories, config, engine script, venv marker, model files, unit.
	for _, path := range []string{
		p.ConfigFile(), p.EngineScript(), p.RequirementsFile(),
		p.VenvMarker(), filepath.Join(p.VenvDir(), "bin", "python"),
		filepath.Join(p.ModelDir(), transcribe.Model.Files[0].Name),
		filepath.Join(p.ModelDir(), transcribe.Model.Files[1].Name),
		p.ServiceFile(),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("setup did not create %s: %v", path, err)
		}
	}

	// The unit must run the installed binary with `serve`.
	unit, err := os.ReadFile(p.ServiceFile())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ExecStart=/usr/local/bin/sasayaki serve") {
		t.Fatalf("unit must start the installed binary: %s", unit)
	}
	if !strings.Contains(string(unit), "Restart=on-failure") {
		t.Fatalf("unit should restart on failure: %s", unit)
	}

	// Model files were fetched exactly once each on a fresh tree.
	for _, mf := range transcribe.Model.Files {
		if got := server.count(mf.Name); got != 1 {
			t.Fatalf("file %s fetched %d times, want 1", mf.Name, got)
		}
	}

	// Second run: everything skipped, no re-download, no venv rebuild.
	before := server.count(transcribe.Model.Files[0].Name)
	session2 := NewSession(p)
	result2 := session2.Run()
	if !result2.AllOK() {
		t.Fatalf("second setup failed: %+v", result2.Steps)
	}
	// Second run: the expensive steps are skipped, no re-download.
	byID := map[string]StepResult{}
	for _, step := range result2.Steps {
		byID[step.ID] = step
	}
	for _, id := range []string{"engine", "venv", "packages", "model", "service"} {
		if byID[id].Status != StepSkipped {
			t.Fatalf("second run should skip step %s (status %s)", id, byID[id].Status)
		}
	}
	if after := server.count(transcribe.Model.Files[0].Name); after != before {
		t.Fatalf("second run re-downloaded the model (%d → %d)", before, after)
	}
}

func TestPlanRepairsCorruptModel(t *testing.T) {
	p := setupPaths(t)
	stubEnvironment(t, okDiagnostics())
	server := newFakeModelServer(t)
	wireFakeModel(t, server.server.URL)
	SetBinary("/usr/local/bin/sasayaki")

	session := NewSession(p)
	if result := session.Run(); !result.AllOK() {
		t.Fatalf("setup failed: %+v", result.Steps)
	}

	// Corrupt the model file; setup must detect the checksum mismatch and
	// re-download it.
	name := transcribe.Model.Files[0].Name
	target := filepath.Join(p.ModelDir(), name)
	if err := os.WriteFile(target, []byte("corrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := server.count(name)
	session2 := NewSession(p)
	result2 := session2.Run()
	if !result2.AllOK() {
		t.Fatalf("repair setup failed: %+v", result2.Steps)
	}
	if after := server.count(name); after <= before {
		t.Fatalf("corrupt model was not re-downloaded (%d → %d)", before, after)
	}
	// No .part files left behind.
	matches, _ := filepath.Glob(filepath.Join(p.ModelDir(), "*.part"))
	if len(matches) != 0 {
		t.Fatalf(".part files left behind: %v", matches)
	}
}

func TestUnitCurrentDetection(t *testing.T) {
	p := setupPaths(t)
	SetBinary("/opt/sasayaki")
	if err := os.MkdirAll(filepath.Dir(p.ServiceFile()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ServiceFile(), []byte("[Unit]\nExecStart=/opt/sasayaki serve\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !unitCurrent(p) {
		t.Fatal("unitCurrent should match the current binary")
	}
	// A unit pointing elsewhere must be rewritten.
	if err := os.WriteFile(p.ServiceFile(), []byte("[Unit]\nExecStart=/old/path serve\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if unitCurrent(p) {
		t.Fatal("unitCurrent should reject a stale binary path")
	}
}
