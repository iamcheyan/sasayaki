// Package engine owns the embedded engine.py runtime script and the paths
// of the private Python environment. Model runtime health and request
// execution live in internal/transcribe.
package engine

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/iamcheyan/sasayaki/internal/config"
)

//go:embed engine.py
var script []byte

// WriteScript installs the embedded engine.py into Sasayaki's runtime
// directory.
func WriteScript(p config.Paths) error {
	if err := os.MkdirAll(filepath.Dir(p.EngineScript()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p.EngineScript(), script, 0o700)
}

// Python returns the interpreter of Sasayaki's private virtualenv.
func Python(p config.Paths) string { return filepath.Join(p.VenvDir(), "bin", "python") }
