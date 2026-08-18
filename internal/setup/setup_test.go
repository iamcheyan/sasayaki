package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/diagnostics"
	"github.com/iamcheyan/sasayaki/internal/engine"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// fakeModelServer serves the manifest files with the exact bytes setup's
// checksum validation expects and counts requests so tests can assert
// re-download avoidance.
type fakeModelServer struct {
	mu     sync.Mutex
	server *httptest.Server
	gets   map[string]int
	files  map[string][]byte
}

func newFakeModelServer(t *testing.T) *fakeModelServer {
	t.Helper()
	f := &fakeModelServer{gets: map[string]int{}, files: map[string][]byte{}}
	for _, mf := range transcribe.Model.Files {
		body := make([]byte, 64)
		for i := range body {
			body[i] = byte(i + 1)
		}
		f.files[mf.Name] = body
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		f.mu.Lock()
		f.gets[name]++
		f.mu.Unlock()
		body, ok := f.files[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeModelServer) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gets[name]
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func setupPaths(t *testing.T) config.Paths {
	t.Helper()
	base := t.TempDir()
	return config.Paths{
		ConfigHome: filepath.Join(base, "config"),
		DataHome:   filepath.Join(base, "data"),
		StateHome:  filepath.Join(base, "state"),
		Runtime:    filepath.Join(base, "runtime"),
	}
}

// stubEnvironment replaces the external-command seams (prereqs diagnostics,
// network probe, venv/pip execution) with hermetic fakes. It returns a
// restore func for t.Cleanup.
func stubEnvironment(t *testing.T, diagnoseReport diagnostics.Report) {
	t.Helper()
	origDiagnose, origNetwork, origRun, origLookPath, origSysctl := diagnose, checkNetwork, run, lookPath, systemctl
	diagnose = func(config.Paths) diagnostics.Report { return diagnoseReport }
	checkNetwork = func() error { return nil }
	// Fake venv creation materializes bin/python; fake pip is a no-op.
	run = func(name string, args ...string) error {
		if len(args) >= 2 && args[0] == "-m" && args[1] == "venv" {
			dir := args[2]
			if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
				return err
			}
			for _, bin := range []string{"python", "pip"} {
				if err := os.WriteFile(filepath.Join(dir, "bin", bin), []byte("#!/bin/sh\n"), 0o700); err != nil {
					return err
				}
			}
		}
		return nil
	}
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	systemctl = func(args ...string) error { return nil }
	t.Cleanup(func() {
		diagnose, checkNetwork, run, lookPath, systemctl = origDiagnose, origNetwork, origRun, origLookPath, origSysctl
	})
}

// wireFakeModel redirects the download source to the fake server and swaps
// the manifest files for small fixtures whose checksums match what the
// server serves, so download validation runs end to end.
func wireFakeModel(t *testing.T, serverURL string) {
	t.Helper()
	originalSource := transcribe.Model.Source
	originalFiles := transcribe.Model.Files
	transcribe.Model.Source = serverURL + "/"
	var files []transcribe.ModelFile
	for _, name := range []string{"model.int8.onnx", "tokens.txt", "LICENSE"} {
		body := make([]byte, 64)
		for i := range body {
			body[i] = byte(i + 1)
		}
		files = append(files, transcribe.ModelFile{Name: name, SHA256: sha256Hex(body), Size: 64})
	}
	transcribe.Model.Files = files
	t.Cleanup(func() {
		transcribe.Model.Source = originalSource
		transcribe.Model.Files = originalFiles
	})
}

func TestDownloadChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.onnx")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wrong bytes"))
	}))
	defer server.Close()

	downloadClient = &http.Client{}
	err := downloadFile(server.URL, dest, strings.Repeat("a", 64), 0, nil)
	if err == nil {
		t.Fatal("downloadFile must reject a checksum mismatch")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error should name the checksum: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("failed download must not publish the destination file")
	}
}

func TestDownloadResume(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.onnx")
	body := []byte("0123456789abcdef")
	want := sha256Hex(body)
	var servedRange bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			servedRange = true
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[5:])
			return
		}
		_, _ = w.Write(body)
	}))
	defer server.Close()

	downloadClient = &http.Client{}
	// Pre-seed a .part file with the first 5 bytes; the server must be asked
	// for the remainder and the file verified as whole afterwards.
	if err := os.WriteFile(dest+".part", body[:5], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := downloadFile(server.URL, dest, want, 0, nil); err != nil {
		t.Fatal(err)
	}
	if !servedRange {
		t.Fatal("resume did not send a Range request")
	}
	got, err := os.ReadFile(dest + ".part")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("resumed file = %q, want %q", got, body)
	}
}

func TestDownloadRestartsWhenServerIgnoresRange(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.onnx")
	body := []byte("0123456789abcdef")
	want := sha256Hex(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Intentionally return 200 even when Range is supplied, as some
		// redirects/CDNs do. The downloader must replace, not append to, .part.
		_, _ = w.Write(body)
	}))
	defer server.Close()
	if err := os.WriteFile(dest+".part", body[:5], 0o600); err != nil {
		t.Fatal(err)
	}
	downloadClient = &http.Client{}
	if err := downloadFile(server.URL, dest, want, 0, nil); err != nil {
		t.Fatalf("ignored-range download should restart cleanly: %v", err)
	}
	got, err := os.ReadFile(dest + ".part")
	if err != nil || string(got) != string(body) {
		t.Fatalf("restarted .part = %q, err %v", got, err)
	}
}

func TestDownloadChecksumFailureDiscardsCorruptPart(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.onnx")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wrong bytes"))
	}))
	defer server.Close()
	downloadClient = &http.Client{}
	if err := downloadFile(server.URL, dest, strings.Repeat("a", 64), 0, nil); err == nil {
		t.Fatal("checksum mismatch should fail")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("known-corrupt partial download should be removed, err=%v", err)
	}
}

func TestDownloadRangeNotSatisfiableVerifies(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model.onnx")
	body := []byte("full-file-content")
	want := sha256Hex(body)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer server.Close()

	// The .part already holds the complete file; a 416 must fall through to
	// checksum verification and succeed.
	if err := os.WriteFile(dest+".part", body, 0o600); err != nil {
		t.Fatal(err)
	}
	downloadClient = &http.Client{}
	if err := downloadFile(server.URL, dest, want, 0, nil); err != nil {
		t.Fatalf("416 with complete .part should verify and succeed: %v", err)
	}
	if got, err := os.ReadFile(dest + ".part"); err != nil || string(got) != string(body) {
		t.Fatalf(".part should remain intact after 416: %v", err)
	}
}

func TestEngineScriptWrites(t *testing.T) {
	p := setupPaths(t)
	if err := engine.WriteScript(p); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(p.EngineScript())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), "serve") {
		t.Fatal("engine script must contain the serve subcommand")
	}
	if len(script) < 500 {
		t.Fatalf("engine script suspiciously short: %d bytes", len(script))
	}
}
