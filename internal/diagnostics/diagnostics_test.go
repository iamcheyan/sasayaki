package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// stubRunner reports LookPath results from a fixed table and canned command
// output, so no real tools are required.
type stubRunner struct {
	present   map[string]bool
	runOutput map[string]string
}

func (s stubRunner) LookPath(name string) (string, error) {
	if s.present[name] {
		return "/usr/bin/" + name, nil
	}
	return "", os.ErrNotExist
}

func (s stubRunner) Run(name string, args ...string) ([]byte, error) {
	if out, ok := s.runOutput[name]; ok {
		return []byte(out), nil
	}
	return nil, nil
}

// pactlListing is a fake `pactl list short sources` with one real source and
// one monitor (which must be ignored).
const pactlListing = "0\tsource_0\talsa_output.pci-0000_00_1f.3.analog-stereo.monitor\tmonitor\t\n" +
	"1\tsource_1\talsa_input.pci-0000_00_1f.3.analog-stereo\tinput\t\n"

func completeRunner() stubRunner {
	return stubRunner{
		present: map[string]bool{
			"python3": true, "parecord": true, "wl-copy": true, "wtype": true,
			"pactl": true, "systemctl": true,
		},
		runOutput: map[string]string{"pactl": pactlListing},
	}
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	base := t.TempDir()
	return config.Paths{
		ConfigHome: filepath.Join(base, "config"),
		DataHome:   filepath.Join(base, "data"),
		StateHome:  filepath.Join(base, "state"),
		Runtime:    filepath.Join(base, "runtime"),
	}
}

func TestDiagnosticsMissingTools(t *testing.T) {
	p := testPaths(t)
	report := AllWith(stubRunner{present: map[string]bool{}}, p)
	byName := map[string]Check{}
	for _, check := range report.Checks {
		byName[check.Name] = check
	}
	for _, tool := range []string{"python3", "parecord"} {
		check, ok := byName[tool]
		if !ok {
			t.Fatalf("missing check for %s", tool)
		}
		if check.OK {
			t.Fatalf("%s check should fail when the tool is absent", tool)
		}
		if check.Fix == "" {
			t.Fatalf("%s check must carry a remediation", tool)
		}
		if !strings.Contains(strings.ToLower(check.Fix), "install") {
			t.Fatalf("%s remediation should name an install command: %q", tool, check.Fix)
		}
	}
}

func TestDiagnosticsToolChecksPass(t *testing.T) {
	p := testPaths(t)
	report := AllWith(completeRunner(), p)
	byName := map[string]Check{}
	for _, check := range report.Checks {
		byName[check.Name] = check
	}
	for _, name := range []string{"python3", "parecord", "microphone", "clipboard", "paste backend"} {
		check, ok := byName[name]
		if !ok {
			t.Fatalf("missing check %q", name)
		}
		if !check.OK {
			t.Fatalf("check %q failed with complete tools: %s", name, check.Detail)
		}
	}
	// The monitor source must be ignored.
	if check := byName["microphone"]; !strings.Contains(check.Detail, "1 input source") {
		t.Fatalf("microphone detail = %q, want exactly 1 real source counted", check.Detail)
	}
}

func TestSystemdCheckUsesNeutralManagerQuery(t *testing.T) {
	var got []string
	runner := recordingRunner{run: func(name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		if name == "pactl" {
			return []byte(pactlListing), nil
		}
		return nil, nil
	}}
	check := systemdCheck(runner)
	if !check.OK {
		t.Fatalf("neutral systemd query should succeed: %+v", check)
	}
	want := []string{"systemctl", "--user", "show-environment"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("systemd argv = %q, want %q", got, want)
	}
}

type recordingRunner struct {
	run func(name string, args ...string) ([]byte, error)
}

func (r recordingRunner) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }
func (r recordingRunner) Run(name string, args ...string) ([]byte, error) {
	return r.run(name, args...)
}

func TestDiagnosticsModelSuggestsSetup(t *testing.T) {
	p := testPaths(t)
	report := AllWith(stubRunner{present: map[string]bool{"python3": true}}, p)
	var model Check
	for _, check := range report.Checks {
		if check.Name == "speech model" {
			model = check
		}
	}
	if model.OK {
		t.Fatal("empty model dir should fail the model check")
	}
	if !strings.Contains(model.Fix, "sasayaki setup") {
		t.Fatalf("model check fix should suggest setup: %q", model.Fix)
	}
	if len(report.Model) == 0 {
		t.Fatal("empty model dir should produce model problems")
	}
}

func TestDiagnosticsSocket(t *testing.T) {
	p := testPaths(t)
	report := AllWith(stubRunner{present: map[string]bool{}}, p)
	found := false
	for _, check := range report.Checks {
		if check.Name == "control socket" && !check.OK {
			found = true
			if !strings.Contains(check.Detail, "not running") {
				t.Fatalf("socket check should say the service is not running: %q", check.Detail)
			}
		}
	}
	if !found {
		t.Fatal("control socket check missing or unexpectedly OK")
	}
}

func TestDiagnosticsPasteBackendMissing(t *testing.T) {
	p := testPaths(t)
	report := AllWith(stubRunner{present: map[string]bool{"wl-copy": true}}, p)
	for _, check := range report.Checks {
		if check.Name == "paste backend" && check.OK {
			t.Fatal("paste backend check should fail without wtype/ydotool/xdotool")
		}
	}
}
