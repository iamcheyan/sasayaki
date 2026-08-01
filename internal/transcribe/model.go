package transcribe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// Model describes the pinned SenseVoice model: source, license and the exact
// files with SHA-256 checksums. Setup downloads and verifies against this
// manifest; diagnostics verify installed files against it.
//
// Source: FunAudioLLM/SenseVoice converted to ONNX by k2-fsa/sherpa-onnx.
// The HF repository LICENSE file refers to the FunASR repository; the
// upstream SenseVoice code is MIT-licensed (Copyright (c) 2025 FunASR) and
// sherpa-onnx is Apache-2.0. Inference runs fully offline.
var Model = struct {
	Version   string
	Source    string
	License   string
	LicenseID string
	Files     []ModelFile
}{
	Version:   "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17",
	Source:    "https://huggingface.co/csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/resolve/main/",
	License:   "SenseVoice: MIT (FunAudioLLM); sherpa-onnx bindings: Apache-2.0",
	LicenseID: "MIT / Apache-2.0",
	Files: []ModelFile{
		// model.int8.onnx is LFS-tracked; its HF oid IS the content sha256.
		{Name: "model.int8.onnx", SHA256: "c71f0ce00bec95b07744e116345e33d8cbbe08cef896382cf907bf4b51a2cd51", Size: 239233841},
		// tokens.txt and LICENSE are ordinary git blobs on HF: their repo
		// oids are git-blob sha1s, NOT content hashes. The values below are
		// the actual content sha256 of the bytes resolve/main serves.
		{Name: "tokens.txt", SHA256: "f449eb28dc567533d7fa59be34e2abca8784f771850c78a47fb731a31429a1dc", Size: 315894},
		{Name: "LICENSE", SHA256: "221c6df10b0931a5629adad671ea48fb7747e034c414b6d2bfa275bc3dd4ea17", Size: 71},
	},
}

// SpeechModel is one complete offline recognizer choice. Architecture is the
// ASR backend (not the user's CPU): SenseVoice is multilingual; Paraformer is
// Chinese-first. Each model owns its manifest and private data directory.
type SpeechModel struct {
	ID           string
	Label        string
	Architecture string
	Languages    string
	Description  string
	Source       string
	ModelFile    ModelFile
	Files        []ModelFile
}

var SpeechModels = []SpeechModel{
	{ID: "sensevoice-int8", Label: "SenseVoice Small · int8 · 229 MB", Architecture: "sensevoice", Languages: "Chinese · English · Japanese · Korean · Cantonese", Description: "Fast multilingual default for everyday dictation.", Source: Model.Source, ModelFile: Model.Files[0]},
	{ID: "sensevoice-full", Label: "SenseVoice Small · full precision · 894 MB", Architecture: "sensevoice", Languages: "Chinese · English · Japanese · Korean · Cantonese", Description: "Higher-quality multilingual model; needs more disk and memory.", Source: Model.Source, ModelFile: ModelFile{Name: "model.onnx", SHA256: "977016bd9c79f9eb343430b5cc305e07ab64d5212dff41b0dcfa1694bee9a8cb", Size: 937617178}},
	{ID: "paraformer-zh-int8", Label: "Paraformer Large · int8 · 232 MB", Architecture: "paraformer", Languages: "Chinese", Description: "Chinese-first alternative with a dedicated Paraformer backend.", Source: "https://huggingface.co/csukuangfj/sherpa-onnx-paraformer-zh-2023-09-14/resolve/main/", ModelFile: ModelFile{Name: "model.int8.onnx", SHA256: "f36a0433bcf096bd6d6f11b80a3ac8bed110bdca632fe0d731df8d1a84475945", Size: 243371218}, Files: []ModelFile{{Name: "model.int8.onnx", SHA256: "f36a0433bcf096bd6d6f11b80a3ac8bed110bdca632fe0d731df8d1a84475945", Size: 243371218}, {Name: "tokens.txt", SHA256: "59aba8873a2ed1e122c25fee421e25f283b63290efbde85c1c01a853d83cb6e6", Size: 75756}}},
}

func SpeechModelByID(id string) (SpeechModel, bool) {
	for _, model := range SpeechModels {
		if model.ID == id {
			return model, true
		}
	}
	return SpeechModel{}, false
}

func ModelFiles(id string) []ModelFile {
	selected, ok := SpeechModelByID(id)
	if !ok {
		return nil
	}
	if len(selected.Files) != 0 {
		return append([]ModelFile(nil), selected.Files...)
	}
	var voice ModelFile
	switch id {
	case "sensevoice-int8":
		// Read the default manifest dynamically so verification/download tests
		// can substitute a hermetic manifest without duplicating catalog data.
		voice = Model.Files[0]
	case "sensevoice-full":
		voice = selected.ModelFile
	}
	return []ModelFile{voice, Model.Files[1], Model.Files[2]}
}

func ModelSource(id string) string {
	// Keep the default manifest live so setup tests and downstream packagers
	// can substitute its mirror without rebuilding the catalog.
	if id == "sensevoice-int8" || id == "sensevoice-full" {
		return Model.Source
	}
	selected, ok := SpeechModelByID(id)
	if !ok || selected.Source == "" {
		return ""
	}
	return selected.Source
}

func ModelDir(p config.Paths, id string) string {
	// Preserve the directory used by the first public Sasayaki release.
	if id == "sensevoice-int8" {
		return p.ModelDir()
	}
	return p.ModelDirFor(id)
}

// ModelFile is one file of the pinned model.
type ModelFile struct {
	Name   string
	SHA256 string
	Size   int64
}

// VerifyModel checks every manifest file for existence and checksum.
// It returns the list of problems; an empty list means the model is valid.
func VerifyModel(p config.Paths) []string { return VerifyModelFor(p, "sensevoice-int8") }

func VerifyModelFor(p config.Paths, id string) []string {
	var problems []string
	files := ModelFiles(id)
	if len(files) == 0 {
		return []string{fmt.Sprintf("unknown speech model %q", id)}
	}
	for _, f := range files {
		path := filepath.Join(ModelDir(p, id), f.Name)
		fi, err := os.Stat(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("missing %s", f.Name))
			continue
		}
		if fi.Size() != f.Size {
			problems = append(problems, fmt.Sprintf("%s has size %d, want %d", f.Name, fi.Size(), f.Size))
			continue
		}
		sum, err := sha256File(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f.Name, err))
			continue
		}
		if sum != f.SHA256 {
			problems = append(problems, fmt.Sprintf("%s checksum mismatch (corrupt, re-run sasayaki setup)", f.Name))
		}
	}
	return problems
}

// ModelValid reports whether the installed model passes verification.
func ModelValid(p config.Paths) bool               { return ModelValidFor(p, "sensevoice-int8") }
func ModelValidFor(p config.Paths, id string) bool { return len(VerifyModelFor(p, id)) == 0 }

// Installed reports whether the full local runtime is present: the engine
// script, a completed venv and a valid model.
func Installed(p config.Paths) bool {
	if _, err := os.Stat(p.EngineScript()); err != nil {
		return false
	}
	if _, err := os.Stat(p.VenvMarker()); err != nil {
		return false
	}
	return ModelValid(p)
}

func InstalledFor(p config.Paths, id string) bool {
	if _, err := os.Stat(p.EngineScript()); err != nil {
		return false
	}
	if _, err := os.Stat(p.VenvMarker()); err != nil {
		return false
	}
	return ModelValidFor(p, id)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
