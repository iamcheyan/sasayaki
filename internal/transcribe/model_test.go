package transcribe

import (
	"path/filepath"
	"testing"

	"github.com/iamcheyan/sasayaki/internal/config"
)

func TestParaformerCatalogUsesDedicatedBackendAndDirectory(t *testing.T) {
	p := config.Paths{DataHome: t.TempDir()}
	model, ok := SpeechModelByID("paraformer-zh-int8")
	if !ok {
		t.Fatal("Paraformer model is missing from catalog")
	}
	if model.Architecture != "paraformer" {
		t.Fatalf("architecture = %q, want paraformer", model.Architecture)
	}
	if got, want := ModelDir(p, model.ID), filepath.Join(p.DataHome, "models", model.ID); got != want {
		t.Fatalf("model directory = %q, want %q", got, want)
	}
	files := ModelFiles(model.ID)
	if len(files) != 2 || files[0].Name != "model.int8.onnx" || files[1].Name != "tokens.txt" {
		t.Fatalf("unexpected Paraformer manifest: %#v", files)
	}
}

func TestDefaultSenseVoiceKeepsLegacyDirectory(t *testing.T) {
	p := config.Paths{DataHome: t.TempDir()}
	if got, want := ModelDir(p, "sensevoice-int8"), p.ModelDir(); got != want {
		t.Fatalf("model directory = %q, want %q", got, want)
	}
}
