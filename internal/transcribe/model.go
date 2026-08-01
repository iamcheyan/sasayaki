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

// ModelFile is one file of the pinned model.
type ModelFile struct {
	Name   string
	SHA256 string
	Size   int64
}

// VerifyModel checks every manifest file for existence and checksum.
// It returns the list of problems; an empty list means the model is valid.
func VerifyModel(p config.Paths) []string {
	var problems []string
	for _, f := range Model.Files {
		path := filepath.Join(p.ModelDir(), f.Name)
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
func ModelValid(p config.Paths) bool { return len(VerifyModel(p)) == 0 }

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
