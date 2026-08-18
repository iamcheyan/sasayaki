// Package diagnostics runs prerequisite and capability checks and produces
// human- and machine-readable reports with concrete remediation. It never
// modifies the system.
package diagnostics

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// Runner abstracts tool lookup and execution for tests.
type Runner interface {
	LookPath(name string) (string, error)
	Run(name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }
func (execRunner) Run(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// DefaultRunner executes real commands.
var DefaultRunner Runner = execRunner{}

// Check is one capability probe.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

// Report is the full diagnostic snapshot.
type Report struct {
	// Version is the diagnostics schema version.
	Version int     `json:"version"`
	Checks  []Check `json:"checks"`
	// Model lists problems found by model manifest verification.
	Model []string `json:"model_problems,omitempty"`
	// PasteBackend is the best detected paste backend, if any.
	PasteBackend string `json:"paste_backend,omitempty"`
}

// All runs every check against the given paths.
func All(p config.Paths) Report { return AllWith(DefaultRunner, p) }

// AllWith runs every check with a custom runner.
func AllWith(r Runner, p config.Paths) Report {
	report := Report{Version: 1}
	cfg, err := config.Load(p)
	if err != nil {
		cfg = config.Default()
	}
	// Verify the model manifest once and reuse the result for both the
	// speech-model check and report.Model. Hashing ~230MB of ONNX twice
	// per diagnose call wastes ~1s of CPU and a full disk read.
	modelProblems := transcribe.VerifyModelFor(p, cfg.SpeechModel)
	report.Checks = append(report.Checks,
		pythonCheck(r),
	)
	report.Checks = append(report.Checks, sessionCheck(r))
	report.Checks = append(report.Checks, runtimeCheck(p))
	report.Checks = append(report.Checks, modelCheckFrom(p, cfg.SpeechModel, modelProblems))
	report.Checks = append(report.Checks, micCheck(r))
	report.Checks = append(report.Checks, clipboardCheck(r))
	report.Checks = append(report.Checks, pasteBackendCheck(r))
	report.Checks = append(report.Checks, compositorCheck(r))
	report.Checks = append(report.Checks, pasteProtocolCheck(r))
	report.Checks = append(report.Checks, focusCheck(r))
	report.Checks = append(report.Checks, socketCheck(p))
	report.Model = modelProblems
	return report
}

func toolCheck(r Runner, tool, purpose, fix string) Check {
	path, err := r.LookPath(tool)
	if err != nil {
		return Check{Name: tool, OK: false, Detail: purpose + " not found", Fix: fix}
	}
	return Check{Name: tool, OK: true, Detail: purpose + ": " + path}
}

func runtimeCheck(p config.Paths) Check {
	ok := fileExists(p.EngineScript()) && fileExists(p.VenvMarker()) && fileExists(filepath.Join(p.VenvDir(), "bin", "python"))
	detail := "private runtime missing"
	if ok {
		detail = "private Python runtime installed"
	}
	return Check{
		Name:   "sasayaki runtime",
		OK:     ok,
		Detail: detail,
		Fix:    "Run `sasayaki setup` to create the private runtime",
	}
}

func modelCheckFrom(p config.Paths, id string, problems []string) Check {
	model, known := transcribe.SpeechModelByID(id)
	if len(problems) == 0 {
		return Check{Name: "speech model", OK: true, Detail: model.Label + " verified (" + model.Architecture + ")"}
	}
	if !known {
		return Check{Name: "speech model", OK: false, Detail: strings.Join(problems, "; "), Fix: "Choose a known model with `sasayaki models`"}
	}
	return Check{
		Name:   "speech model",
		OK:     false,
		Detail: strings.Join(problems, "; "),
		Fix:    "Run `sasayaki setup` to download or repair the model",
	}
}

func socketExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func socketCheck(p config.Paths) Check {
	fi, err := os.Stat(p.Socket())
	if err != nil {
		return Check{Name: "control socket", OK: false, Detail: "service is not running (no socket)", Fix: "Run `sasayaki service start`"}
	}
	mode := fi.Mode()
	if mode&os.ModeSocket == 0 {
		return Check{Name: "control socket", OK: false, Detail: "socket path exists but is not a socket", Fix: "Remove " + p.Socket() + " and restart the service"}
	}
	return Check{Name: "control socket", OK: true, Detail: "service socket present at " + p.Socket()}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
