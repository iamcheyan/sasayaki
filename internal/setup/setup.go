// Package setup provisions Sasayaki's private runtime, model and user
// service. It is idempotent: re-running repairs missing or corrupt artifacts
// without redownloading valid ones. Steps run in dependency order and the
// report tells the user exactly what changed and what was skipped.
package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/engine"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// StepStatus describes one setup step's outcome.
type StepStatus string

const (
	StepDone    StepStatus = "done"
	StepSkipped StepStatus = "skipped"
	StepFailed  StepStatus = "failed"
)

// Step is one planned unit of setup work.
type Step struct {
	ID     string
	Title  string
	Status StepStatus
	// Detail is a short human summary of what was done or why it was
	// skipped; Error carries the failure message when Status is failed.
	Detail string
	Error  string
	run    func(p config.Paths) (string, error)
	// skip returns (true, reason) when the step has nothing to do.
	skip func(p config.Paths) (bool, string)
}

// Plan is the ordered setup steps for the pinned model.
func Plan(cfg config.Config) []*Step {
	return []*Step{
		{
			ID: "prereqs", Title: "Checking prerequisites",
			run: func(p config.Paths) (string, error) {
				if err := checkPrereqs(p); err != nil {
					return "", err
				}
				return "tools, disk space and network OK", nil
			},
		},
		{
			ID: "dirs", Title: "Creating private directories",
			run: func(p config.Paths) (string, error) {
				if err := p.Ensure(); err != nil {
					return "", err
				}
				return "directories ready", nil
			},
		},
		{
			ID: "config", Title: "Writing configuration",
			run: func(p config.Paths) (string, error) {
				cfg, err := config.Load(p)
				if err != nil {
					cfg = config.Default()
				}
				if err := config.Save(p, cfg); err != nil {
					return "", err
				}
				return "config.json ready", nil
			},
		},
		{
			ID: "engine", Title: "Installing the private engine script",
			run: func(p config.Paths) (string, error) {
				if err := engine.WriteScript(p); err != nil {
					return "", err
				}
				return "engine.py installed", nil
			},
			skip: func(p config.Paths) (bool, string) {
				if engine.ScriptCurrent(p) {
					return true, "engine.py already present"
				}
				return false, ""
			},
		},
		{
			ID: "python", Title: "Checking python3",
			run: func(p config.Paths) (string, error) {
				if _, err := lookPath("python3"); err != nil {
					return "", fmt.Errorf("python3 is required but not installed; install it (e.g. dnf install python3 / apt install python3) and re-run `sasayaki setup`")
				}
				return "python3 found", nil
			},
		},
		{
			ID: "venv", Title: "Creating the private Python runtime",
			run: func(p config.Paths) (string, error) {
				if _, err := lookPath("python3"); err != nil {
					return "", fmt.Errorf("python3 not found")
				}
				if err := run("python3", "-m", "venv", p.VenvDir()); err != nil {
					return "", err
				}
				return "virtual environment created", nil
			},
			skip: func(p config.Paths) (bool, string) {
				if fileExists(filepath.Join(p.VenvDir(), "bin", "python")) && fileExists(p.VenvMarker()) {
					return true, "runtime already installed"
				}
				return false, ""
			},
		},
		{
			ID: "packages", Title: "Installing pinned speech packages",
			run: func(p config.Paths) (string, error) {
				py := filepath.Join(p.VenvDir(), "bin", "python")
				args := []string{"-m", "pip", "install", "--disable-pip-version-check", "-r", p.RequirementsFile()}
				if err := os.MkdirAll(p.RuntimeDir(), 0o700); err != nil {
					return "", err
				}
				if err := os.WriteFile(p.RequirementsFile(), []byte(PinnedRuntime), 0o600); err != nil {
					return "", err
				}
				if err := run(py, args...); err != nil {
					return "", err
				}
				// Mark the runtime complete only after packages are in.
				if err := os.WriteFile(p.VenvMarker(), []byte("installed\n"), 0o600); err != nil {
					return "", err
				}
				return "packages installed and runtime marked ready", nil
			},
			skip: func(p config.Paths) (bool, string) {
				if fileExists(p.VenvMarker()) {
					return true, "runtime already installed"
				}
				return false, ""
			},
		},
		{
			ID: "model", Title: "Downloading and verifying the selected local model",
			run: func(p config.Paths) (string, error) {
				if err := os.MkdirAll(transcribe.ModelDir(p, cfg.SpeechModel), 0o700); err != nil {
					return "", err
				}
				return downloadModel(p, cfg.SpeechModel, progress)
			},
			skip: func(p config.Paths) (bool, string) {
				if transcribe.ModelValidFor(p, cfg.SpeechModel) {
					return true, "model files already verified"
				}
				return false, ""
			},
		},
		{
			ID: "service", Title: "Writing the user service unit",
			run: func(p config.Paths) (string, error) {
				return "service unit written", writeUnit(p, serviceBinary)
			},
			skip: func(p config.Paths) (bool, string) {
				if unitCurrent(p) {
					return true, "service unit already current"
				}
				return false, ""
			},
		},
		{
			ID: "systemd", Title: "Enabling and starting the user service",
			run: func(p config.Paths) (string, error) {
				if err := systemctl("daemon-reload"); err != nil {
					return "", err
				}
				if err := systemctl("enable", "--now", "sasayaki.service"); err != nil {
					return "", err
				}
				// Applying setup is also how a newly selected local model becomes
				// active. `enable --now` does not restart an already active unit.
				if err := systemctl("restart", "sasayaki.service"); err != nil {
					return "", err
				}
				return "service enabled, started and refreshed", nil
			},
		},
	}
}

// Session drives one setup run.
type Session struct {
	paths   config.Paths
	steps   []*Step
	results []StepResult
}

// StepResult is the final outcome of one step.
type StepResult struct {
	ID     string     `json:"id"`
	Title  string     `json:"title"`
	Status StepStatus `json:"status"`
	Detail string     `json:"detail,omitempty"`
	Error  string     `json:"error,omitempty"`
}

// PlanResult is the full setup outcome, ready for TUI rows or --json.
type PlanResult struct {
	Steps   []StepResult `json:"steps"`
	Skipped int          `json:"skipped"`
	Failed  []string     `json:"failed,omitempty"`
}

// AllOK reports whether every step completed or was skipped.
func (r PlanResult) AllOK() bool { return len(r.Failed) == 0 }

// NewSession builds a session for the given paths.
func NewSession(paths config.Paths) *Session {
	cfg, err := config.Load(paths)
	if err != nil {
		cfg = config.Default()
	}
	return &Session{paths: paths, steps: Plan(cfg)}
}

// Run executes the plan in order, stopping on the first failure so the user
// always sees a coherent partial state and the failing step.
func (s *Session) Run() PlanResult {
	var result PlanResult
	for _, step := range s.steps {
		out := StepResult{ID: step.ID, Title: step.Title, Status: StepDone}
		if step.skip != nil {
			if skip, reason := step.skip(s.paths); skip {
				out.Status = StepSkipped
				out.Detail = reason
				result.Skipped++
				s.results = append(s.results, out)
				result.Steps = append(result.Steps, out)
				continue
			}
		}
		detail, err := step.run(s.paths)
		if err != nil {
			out.Status = StepFailed
			out.Error = err.Error()
			result.Failed = append(result.Failed, step.ID)
			s.results = append(s.results, out)
			result.Steps = append(result.Steps, out)
			return result
		}
		out.Detail = detail
		s.results = append(s.results, out)
		result.Steps = append(result.Steps, out)
	}
	return result
}

// Results returns the last run's step outcomes.
func (s *Session) Results() []StepResult { return s.results }

// progress receives one line of human progress.
var progress = func(message string) {}

// SetProgress overrides the progress sink (used by the TUI and CLI).
func SetProgress(fn func(string)) { progress = fn }

// serviceBinary is resolved before a session runs; the TUI and CLI set it.
var serviceBinary string

// SetBinary tells setup which sasayaki binary the user unit must start.
func SetBinary(path string) { serviceBinary = path }

// unitCurrent reports whether the existing unit matches what setup would
// write for the current binary.
func unitCurrent(p config.Paths) bool {
	if serviceBinary == "" {
		return false
	}
	b, err := os.ReadFile(p.ServiceFile())
	if err != nil {
		return false
	}
	return strings.Contains(string(b), "ExecStart="+serviceBinary+" serve")
}

// fileExists is a tiny helper kept local to setup.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
