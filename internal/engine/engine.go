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

// ScriptCurrent reports whether the installed runtime is exactly the engine
// embedded in this binary. Setup uses this rather than merely checking that a
// file exists, so a new recognizer backend can never leave an old script in
// place after an application upgrade.
func ScriptCurrent(p config.Paths) bool {
	b, err := os.ReadFile(p.EngineScript())
	return err == nil && string(b) == string(script)
}

// Python returns the interpreter of Sasayaki's private virtualenv.
func Python(p config.Paths) string { return filepath.Join(p.VenvDir(), "bin", "python") }
