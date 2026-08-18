package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// PinnedRuntime is the exact package set installed into Sasayaki's private
// virtualenv. Versions are fixed so a setup run is reproducible; bump them
// deliberately with a documented reason.
const PinnedRuntime = `numpy==2.5.1
sherpa-onnx==1.13.4
`

// writeRequirements installs the pinned requirement file.
func writeRequirements(p config.Paths) error {
	if err := os.MkdirAll(p.RuntimeDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p.RequirementsFile(), []byte(PinnedRuntime), 0o600)
}

// writeEngine installs the embedded engine.py into the runtime directory.
func writeEngine(p config.Paths) error {
	dir := filepath.Dir(p.EngineScript())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeEmbeddedEngine(p.EngineScript())
}

// writeEmbeddedEngine is replaced by the real embed writer at init time;
// the engine package owns the script bytes.
var writeEmbeddedEngine = func(path string) error {
	return fmt.Errorf("engine script writer not wired")
}

// run executes a command with argv and returns its combined output on error.
// It is a var so tests can stub external commands (venv creation, pip).
var run = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// lookPath is a thin wrapper so tests can stub PATH lookups.
var lookPath = func(name string) (string, error) { return exec.LookPath(name) }
