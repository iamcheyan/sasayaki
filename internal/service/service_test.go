package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/paste"
	"github.com/iamcheyan/sasayaki/internal/protocol"
	"github.com/iamcheyan/sasayaki/internal/recording"
)

// --- fakes ---

type fakeRecorder struct {
	mu        sync.Mutex
	duration  time.Duration
	startErr  error
	stopErr   error
	started   string
	cancelled bool
}

func (f *fakeRecorder) Start(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = path
	return f.startErr
}

func (f *fakeRecorder) Stop() (time.Duration, error) { return f.duration, f.stopErr }

func (f *fakeRecorder) Cancel() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = true
	return nil
}

func (f *fakeRecorder) wasCancelled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled
}

type fakeTranscriber struct {
	mu          sync.Mutex
	warmErr     error
	text        string
	err         error
	block       chan struct{} // Transcribe waits for close or ctx.Done
	shutdown    bool
	transcribed []string
}

func (f *fakeTranscriber) EnsureWarm(ctx context.Context) error {
	return f.warmErr
}

func (f *fakeTranscriber) Transcribe(ctx context.Context, wav string) (string, error) {
	f.mu.Lock()
	f.transcribed = append(f.transcribed, wav)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.text, f.err
}

func (f *fakeTranscriber) Status() (string, string) { return "warm", "" }

func (f *fakeTranscriber) Shutdown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdown = true
}

func (f *fakeTranscriber) wasShutdown() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shutdown
}

type fakePaster struct {
	mu     sync.Mutex
	result paste.Result
	texts  []string
}

func (f *fakePaster) Paste(text string) paste.Result {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	return f.result
}

func (f *fakePaster) pastedTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

// --- harness ---

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	base := t.TempDir()
	return config.Paths{
		ConfigHome: filepath.Join(base, "config"),
		DataHome:   filepath.Join(base, "data"),
		StateHome:  filepath.Join(base, "state"),
		Runtime:    filepath.Join(base, "runtime"),
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testDaemon wires a daemon with fakes and returns them.
func testDaemon(t *testing.T, paths config.Paths) (*Daemon, *fakeRecorder, *fakeTranscriber, *fakePaster) {
	t.Helper()
	rec := &fakeRecorder{duration: 2 * time.Second}
	tr := &fakeTranscriber{text: "hello world"}
	pa := &fakePaster{result: paste.Result{Pasted: true, Backend: "wtype", Detail: "Pasted with wtype"}}
	d := newDaemon(paths, config.Default(), discardLogger())
	d.newRecorder = func() recording.Recorder { return rec }
	d.transcriber = tr
	d.paster = pa
	d.micOK = func() bool { return true }
	return d, rec, tr, pa
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func recordingFiles(t *testing.T, paths config.Paths) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.RecordingsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// --- state machine ---

func TestToggleHappyPath(t *testing.T) {
	paths := testPaths(t)
	d, _, _, pa := testDaemon(t, paths)

	msg, perr := d.Toggle()
	if perr != nil {
		t.Fatalf("toggle to start: %v", perr)
	}
	if !strings.Contains(msg, "press the shortcut again") {
		t.Fatalf("start message = %q", msg)
	}
	if d.State().Phase != protocol.PhaseRecording {
		t.Fatalf("phase = %q, want recording", d.State().Phase)
	}

	msg, perr = d.Toggle()
	if perr != nil {
		t.Fatalf("toggle to stop: %v", perr)
	}
	if !strings.Contains(msg, "transcribing") {
		t.Fatalf("stop message = %q", msg)
	}

	waitFor(t, "succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })
	state := d.State()
	if state.LastResult != "hello world" {
		t.Fatalf("last result = %q", state.LastResult)
	}
	if got := pa.pastedTexts(); len(got) != 1 || got[0] != "hello world" {
		t.Fatalf("paste texts = %v", got)
	}
}

func TestToggleStartsANewRecordingAfterTerminalState(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, pa := testDaemon(t, paths)

	// Complete a normal first recording.
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "first succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })

	// A future shortcut press must start recording rather than incorrectly
	// reporting that the completed operation is still transcribing.
	if _, perr := d.Toggle(); perr != nil {
		t.Fatalf("toggle after success: %+v", perr)
	}
	if got := d.State().Phase; got != protocol.PhaseRecording {
		t.Fatalf("phase after a second recording start = %q, want recording", got)
	}
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "second succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })
	if got := len(pa.pastedTexts()); got != 2 {
		t.Fatalf("paste count = %d, want 2", got)
	}
	if got := len(tr.transcribed); got != 2 {
		t.Fatalf("transcription count = %d, want 2", got)
	}
}

func TestToggleStartsANewRecordingAfterFailure(t *testing.T) {
	paths := testPaths(t)
	d, rec, _, _ := testDaemon(t, paths)
	rec.duration = 10 * time.Millisecond
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := d.Toggle(); perr == nil {
		t.Fatal("short recording should fail")
	}
	if got := d.State().Phase; got != protocol.PhaseFailed {
		t.Fatalf("phase = %q, want failed", got)
	}
	if _, perr := d.Toggle(); perr != nil {
		t.Fatalf("toggle after failure: %+v", perr)
	}
	if got := d.State().Phase; got != protocol.PhaseRecording {
		t.Fatalf("phase after retry = %q, want recording", got)
	}
}

func TestToggleTooShort(t *testing.T) {
	paths := testPaths(t)
	d, rec, _, _ := testDaemon(t, paths)
	rec.duration = 100 * time.Millisecond

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	_, perr := d.Toggle()
	if perr == nil || perr.Code != protocol.ErrTooShort {
		t.Fatalf("want ErrTooShort, got %+v", perr)
	}
	if d.State().Phase != protocol.PhaseFailed {
		t.Fatalf("phase = %q, want failed", d.State().Phase)
	}
	if files := recordingFiles(t, paths); len(files) != 0 {
		t.Fatalf("too-short recording was not removed: %v", files)
	}
}

func TestToggleWhileTranscribingRejected(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, pa := testDaemon(t, paths)
	tr.block = make(chan struct{})

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "transcribing phase", func() bool { return d.State().Phase == protocol.PhaseTranscribing })

	_, perr := d.Toggle()
	if perr == nil || perr.Code != protocol.ErrStillTranscribing {
		t.Fatalf("toggle while transcribing must be rejected, got %+v", perr)
	}

	close(tr.block)
	waitFor(t, "succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })
	if len(pa.pastedTexts()) != 1 {
		t.Fatalf("paste should run once after unblocking")
	}
}

func TestEmptySpeechFails(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, _ := testDaemon(t, paths)
	tr.text = "   "

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	_, perr := d.Toggle()
	if perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "failed phase", func() bool { return d.State().Phase == protocol.PhaseFailed })
	if d.State().LastError == "" || !strings.Contains(d.State().LastError, "no speech") {
		t.Fatalf("empty speech should record an explanatory error: %q", d.State().LastError)
	}
}

func TestModelFailureFails(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, _ := testDaemon(t, paths)
	tr.warmErr = os.ErrPermission

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	_, perr := d.Toggle()
	if perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "failed phase", func() bool { return d.State().Phase == protocol.PhaseFailed })
	if d.State().LastError == "" {
		t.Fatal("model failure must surface in LastError")
	}
}

func TestPasteFailureTruthful(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, pa := testDaemon(t, paths)
	tr.text = "clipboard-only text"
	pa.result = paste.Result{
		Pasted:  false,
		Backend: "clipboard",
		Detail:  "Copied to clipboard; paste it manually (install wtype, ydotool or xdotool for automatic paste).",
	}

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	_, perr := d.Toggle()
	if perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "failed phase", func() bool { return d.State().Phase == protocol.PhaseFailed })
	state := d.State()
	if !strings.Contains(state.LastError, "paste it manually") {
		t.Fatalf("paste failure must be reported truthfully: %q", state.LastError)
	}
	if state.LastResult != "clipboard-only text" {
		t.Fatalf("clipboard fallback should still record the text: %q", state.LastResult)
	}
}

func TestLastResultTruncated(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, _ := testDaemon(t, paths)
	long := strings.Repeat("x", 200)
	tr.text = long

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	_, perr := d.Toggle()
	if perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })
	if got := d.State().LastResult; got != strings.Repeat("x", 60)+"…" {
		t.Fatalf("LastResult not truncated to 60 runes: len=%d %q", len(got), got)
	}
}

func TestCancelWhileRecording(t *testing.T) {
	paths := testPaths(t)
	d, rec, _, _ := testDaemon(t, paths)

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	msg, perr := d.Cancel()
	if perr != nil {
		t.Fatal(perr)
	}
	if !strings.Contains(msg, "Recording cancelled") {
		t.Fatalf("cancel message = %q", msg)
	}
	if d.State().Phase != protocol.PhaseIdle {
		t.Fatalf("phase after cancel = %q", d.State().Phase)
	}
	if !rec.wasCancelled() {
		t.Fatal("recorder.Cancel was not called")
	}
	if files := recordingFiles(t, paths); len(files) != 0 {
		t.Fatalf("cancel must remove the partial recording: %v", files)
	}
}

func TestCancelWhileTranscribing(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, pa := testDaemon(t, paths)
	tr.block = make(chan struct{})

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "transcribing phase", func() bool { return d.State().Phase == protocol.PhaseTranscribing })

	_, perr := d.Cancel()
	if perr != nil {
		t.Fatal(perr)
	}
	if d.State().Phase != protocol.PhaseIdle {
		t.Fatalf("phase after cancel = %q", d.State().Phase)
	}
	close(tr.block)
	// The cancelled goroutine must drain, remove the file and never paste.
	waitFor(t, "recording removal", func() bool { return len(recordingFiles(t, paths)) == 0 })
	if got := pa.pastedTexts(); len(got) != 0 {
		t.Fatalf("cancelled transcription must not paste: %v", got)
	}
}

func TestCancelledTranscriptionCannotPasteAfterNextRecordingStarts(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, pa := testDaemon(t, paths)
	tr.block = make(chan struct{})

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "transcribing phase", func() bool { return d.State().Phase == protocol.PhaseTranscribing })
	if _, perr := d.Cancel(); perr != nil {
		t.Fatal(perr)
	}
	// Begin the next clip before the old model request is allowed to return.
	if _, perr := d.Toggle(); perr != nil {
		t.Fatalf("start next recording: %v", perr)
	}
	if got := d.State().Phase; got != protocol.PhaseRecording {
		t.Fatalf("phase = %s, want recording", got)
	}
	close(tr.block)
	time.Sleep(50 * time.Millisecond)
	if got := len(pa.pastedTexts()); got != 0 {
		t.Fatalf("cancelled old result must not paste, got %v", pa.pastedTexts())
	}
	if got := d.State().Phase; got != protocol.PhaseRecording {
		t.Fatalf("late old result changed next recording state to %s", got)
	}
}

func TestCancelWhenIdle(t *testing.T) {
	d, _, _, _ := testDaemon(t, testPaths(t))
	msg, perr := d.Cancel()
	if perr != nil {
		t.Fatal(perr)
	}
	if !strings.Contains(msg, "Nothing to cancel") {
		t.Fatalf("idle cancel message = %q", msg)
	}
}

func TestMicMissingRejectsToggle(t *testing.T) {
	paths := testPaths(t)
	d, _, _, _ := testDaemon(t, paths)
	d.micOK = func() bool { return false }

	_, perr := d.Toggle()
	if perr == nil || perr.Code != protocol.ErrNotReady {
		t.Fatalf("want ErrNotReady, got %+v", perr)
	}
	if d.State().Phase != protocol.PhaseIdle {
		t.Fatalf("no recording should start without a mic")
	}
}

func TestRecorderStartFailure(t *testing.T) {
	paths := testPaths(t)
	d, rec, _, _ := testDaemon(t, paths)
	rec.startErr = os.ErrPermission

	_, perr := d.Toggle()
	if perr == nil || perr.Code != protocol.ErrMicrophoneFailed {
		t.Fatalf("want ErrMicrophoneFailed, got %+v", perr)
	}
}

func TestShutdownCancelsRecorderAndWorker(t *testing.T) {
	paths := testPaths(t)
	d, rec, tr, _ := testDaemon(t, paths)

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	d.Shutdown()
	if !rec.wasCancelled() {
		t.Fatal("Shutdown must cancel the active recorder")
	}
	if !tr.wasShutdown() {
		t.Fatal("Shutdown must stop the model worker")
	}
	// Shutdown is idempotent.
	d.Shutdown()
}

func TestStartRecordingFailsCleanly(t *testing.T) {
	paths := testPaths(t)
	d, rec, _, _ := testDaemon(t, paths)
	rec.startErr = os.ErrPermission

	_, perr := d.Toggle()
	if perr == nil || perr.Code != protocol.ErrMicrophoneFailed {
		t.Fatalf("want ErrMicrophoneFailed, got %+v", perr)
	}
	if d.State().Phase != protocol.PhaseIdle {
		t.Fatalf("failed start must leave the daemon idle")
	}
}

// --- socket integration ---

func TestSocketRoundTrip(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, _ := testDaemon(t, paths)
	tr.text = "integration hello"

	// Run the daemon in the background like `sasayaki serve`.
	runErr := make(chan error, 1)
	go func() { runErr <- d.Run() }()
	t.Cleanup(func() {
		d.Shutdown()
		select {
		case err := <-runErr:
			if err != nil {
				t.Errorf("Run returned error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("Run did not exit after Shutdown")
		}
	})

	// Wait for the socket to accept.
	waitFor(t, "socket", func() bool {
		conn, err := net.Dial("unix", paths.Socket())
		if err == nil {
			conn.Close()
			return true
		}
		return false
	})

	resp, err := Request(paths, protocol.OpStatus)
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	if !resp.OK || resp.State == nil || resp.State.Service != protocol.ServiceRunning {
		t.Fatalf("status = %+v", resp)
	}

	// toggle → recording → toggle → succeeded, all over the wire.
	resp, err = Request(paths, protocol.OpToggle)
	if err != nil || !resp.OK {
		t.Fatalf("toggle: %v %+v", err, resp)
	}
	if resp.State.Phase != protocol.PhaseRecording {
		t.Fatalf("wire phase after toggle = %q", resp.State.Phase)
	}

	resp, err = Request(paths, protocol.OpToggle)
	if err != nil || !resp.OK {
		t.Fatalf("toggle stop: %v %+v", err, resp)
	}

	waitFor(t, "wire succeeded", func() bool {
		r, err := Request(paths, protocol.OpStatus)
		return err == nil && r.State != nil && r.State.Phase == protocol.PhaseSucceeded
	})
	resp, err = Request(paths, protocol.OpStatus)
	if err != nil {
		t.Fatal(err)
	}
	if resp.State.LastResult != "integration hello" {
		t.Fatalf("wire last result = %q", resp.State.LastResult)
	}

	// Diagnose returns a full report over the follow-up message.
	report, err := RequestDiagnose(paths)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if len(report.Checks) == 0 {
		t.Fatal("diagnose returned no checks")
	}

	// Unknown operation is rejected with a typed error.
	resp, err = Request(paths, "frobnicate")
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Error == nil || resp.Error.Code != protocol.ErrUnknownOperation {
		t.Fatalf("unknown op = %+v", resp)
	}

	// Bad version is rejected.
	conn, err := net.Dial("unix", paths.Socket())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(conn).Encode(struct {
		Version   int    `json:"version"`
		Operation string `json:"operation"`
	}{Version: 999, Operation: protocol.OpStatus}); err != nil {
		t.Fatal(err)
	}
	var bad protocol.Response
	if err := json.NewDecoder(conn).Decode(&bad); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if bad.OK || bad.Error == nil || bad.Error.Code != protocol.ErrBadVersion {
		t.Fatalf("bad version = %+v", bad)
	}
}

func TestRequestWhenNoSocket(t *testing.T) {
	paths := testPaths(t)
	_, err := Request(paths, protocol.OpStatus)
	if err == nil {
		t.Fatal("Request must fail without a running service")
	}
	if !strings.Contains(err.Error(), "sasayaki service start") {
		t.Fatalf("transport error should name the recovery command: %v", err)
	}
}
