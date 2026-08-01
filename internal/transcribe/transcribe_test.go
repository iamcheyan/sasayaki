package transcribe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// fakeEngineScript is a drop-in replacement for the venv python binary. It
// ignores the real engine.py and speaks the serve wire protocol itself, so
// worker tests need no venv or model.
const fakeEngineScript = `#!/usr/bin/env python3
import json, sys
sys.stdout.write(json.dumps({"ready": True, "language": "auto"}) + "\n")
sys.stdout.flush()
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    req = json.loads(line)
    rid = req.get("id")
    sys.stdout.write(json.dumps({"id": rid, "ok": True, "text": "fake-%d" % rid}) + "\n")
    sys.stdout.flush()
`

func fakePaths(t *testing.T) config.Paths {
	t.Helper()
	root := t.TempDir()
	p := config.Paths{
		ConfigHome: filepath.Join(root, "config", "sasayaki"),
		DataHome:   filepath.Join(root, "data", "sasayaki"),
		StateHome:  filepath.Join(root, "state", "sasayaki"),
		Runtime:    filepath.Join(root, "runtime", "sasayaki"),
	}
	bin := filepath.Join(p.VenvDir(), "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "python"), []byte(fakeEngineScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.EngineScript(), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestWorkerTranscribeIDs exercises the request/response loop over the wire
// protocol. Regression: worker IDs used to start at 0 and the read loop
// dropped id-0 responses, so the very first transcription hung until the
// context deadline. Requests beyond the first must also keep working.
func TestWorkerTranscribeIDs(t *testing.T) {
	p := fakePaths(t)
	w := NewWorker(p, "auto", "sensevoice-int8")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.EnsureWarm(ctx); err != nil {
		t.Fatalf("EnsureWarm: %v", err)
	}
	if s, e := w.Status(); s != WorkerWarm || e != "" {
		t.Fatalf("status = %q %q, want warm/''", s, e)
	}
	wav := filepath.Join(p.StateHome, "fake.wav")
	for i := 1; i <= 5; i++ {
		text, err := w.Transcribe(ctx, wav)
		if err != nil {
			t.Fatalf("Transcribe #%d: %v", i, err)
		}
		if want := "fake-" + itoa(i); text != want {
			t.Fatalf("Transcribe #%d = %q, want %q", i, text, want)
		}
	}
	w.Shutdown()
}

// TestWorkerDeadWithoutVenv: a missing runtime must fail fast with a
// descriptive error and a truthful dead status, not hang.
func TestWorkerDeadWithoutVenv(t *testing.T) {
	root := t.TempDir()
	p := config.Paths{
		ConfigHome: filepath.Join(root, "config", "sasayaki"),
		DataHome:   filepath.Join(root, "data", "sasayaki"),
		StateHome:  filepath.Join(root, "state", "sasayaki"),
		Runtime:    filepath.Join(root, "runtime", "sasayaki"),
	}
	w := NewWorker(p, "auto", "sensevoice-int8")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := w.EnsureWarm(ctx)
	if err == nil {
		t.Fatal("EnsureWarm succeeded with no venv")
	}
	if s, _ := w.Status(); s != WorkerDead {
		t.Fatalf("status = %q, want dead", s)
	}
	if !strings.Contains(err.Error(), "no such file") && !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func itoa(n int) string {
	return string(rune('0' + n))
}
