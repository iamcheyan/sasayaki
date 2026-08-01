package engine

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/iamcheyan/sasayaki/internal/config"
)

//go:embed engine.py
var script []byte

func Installed(p config.Paths) bool {
	_, scriptErr := os.Stat(p.EngineScript())
	_, modelErr := os.Stat(filepath.Join(p.ModelDir(), "model.int8.onnx"))
	_, tokensErr := os.Stat(filepath.Join(p.ModelDir(), "tokens.txt"))
	return scriptErr == nil && modelErr == nil && tokensErr == nil
}

func WriteScript(p config.Paths) error {
	if err := os.MkdirAll(filepath.Dir(p.EngineScript()), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p.EngineScript(), script, 0o700)
}

func Python(p config.Paths) string { return filepath.Join(p.VenvDir(), "bin", "python") }

func Transcribe(p config.Paths, wav, language string) (string, error) {
	cmd := exec.Command(Python(p), p.EngineScript(), "transcribe", "--model-dir", p.ModelDir(), "--language", language, wav)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("voice engine: %w: %s", err, string(b))
	}
	return string(b), nil
}
