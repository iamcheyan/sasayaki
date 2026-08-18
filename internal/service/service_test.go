package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
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

// --- deliver (macOS menubar app hands over a finished WAV) ---

func writeWav(t *testing.T, path string, size int) {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDeliverRunsPipelineAndPastes(t *testing.T) {
	paths := testPaths(t)
	d, _, _, pa := testDaemon(t, paths)
	wav := filepath.Join(t.TempDir(), "delivered.wav")
	writeWav(t, wav, 4096)

	msg, perr := d.Deliver(wav, false)
	if perr != nil {
		t.Fatalf("deliver: %v", perr)
	}
	if !strings.Contains(msg, "transcribing") {
		t.Fatalf("deliver message = %q", msg)
	}
	// The service took ownership: the source file is consumed.
	if _, err := os.Stat(wav); !os.IsNotExist(err) {
		t.Fatalf("source wav still exists: %v", err)
	}
	waitFor(t, "succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })
	if got := pa.pastedTexts(); len(got) != 1 || got[0] != "hello world" {
		t.Fatalf("paste texts = %v", got)
	}
	// The delivered clip lives in the recordings dir (retention owns it).
	if len(recordingFiles(t, paths)) != 1 {
		t.Fatalf("recordings = %v, want the delivered clip", recordingFiles(t, paths))
	}
}

func TestDeliverRejectsMissingOrEmptyWav(t *testing.T) {
	paths := testPaths(t)
	d, _, _, _ := testDaemon(t, paths)

	if _, perr := d.Deliver(filepath.Join(t.TempDir(), "nope.wav"), false); perr == nil {
		t.Fatal("missing wav accepted")
	}
	tiny := filepath.Join(t.TempDir(), "tiny.wav")
	writeWav(t, tiny, 16)
	if _, perr := d.Deliver(tiny, false); perr == nil {
		t.Fatal("empty wav accepted")
	}
}

func TestDeliverWhileRecordingRejected(t *testing.T) {
	paths := testPaths(t)
	d, _, _, _ := testDaemon(t, paths)
	if _, perr := d.Toggle(); perr != nil {
		t.Fatalf("start recording: %v", perr)
	}
	wav := filepath.Join(t.TempDir(), "d.wav")
	writeWav(t, wav, 4096)
	if _, perr := d.Deliver(wav, false); perr == nil {
		t.Fatal("deliver during recording accepted")
	}
}

func TestDiagnoseUsesOneResponse(t *testing.T) {
	paths := testPaths(t)
	d, _, _, _ := testDaemon(t, paths)
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go d.handle(right)
	if err := json.NewEncoder(left).Encode(protocol.Request{Version: protocol.Version, Operation: protocol.OpDiagnose}); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(left)
	var response protocol.Response
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Diagnostics == nil || response.State == nil {
		t.Fatalf("diagnose response should include state and diagnostics: %+v", response)
	}
	// The peer closes after one response. A second decode must not yield a
	// second valid protocol message.
	var unexpected protocol.Response
	if err := decoder.Decode(&unexpected); err == nil {
		t.Fatalf("diagnose must send exactly one response, got %+v", unexpected)
	}
}

// TestTestToggleDoesNotPaste guards the test overlay contract: recognition
// only. Sasayaki never pastes during a test recording — clipboard and paste
// shortcuts are other programs' interfaces — but the transcript must still
// surface as the succeeded result.
func TestTestToggleDoesNotPaste(t *testing.T) {
	paths := testPaths(t)
	d, _, _, pa := testDaemon(t, paths)

	if _, perr := d.TestToggle(); perr != nil {
		t.Fatal(perr)
	}
	if d.State().Phase != protocol.PhaseRecording {
		t.Fatalf("phase = %q, want recording", d.State().Phase)
	}
	if _, perr := d.TestToggle(); perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })
	if got := d.State().LastResult; got != "hello world" {
		t.Fatalf("last result = %q, want the transcript", got)
	}
	if got := pa.pastedTexts(); len(got) != 0 {
		t.Fatalf("test recording pasted %v; must never paste", got)
	}

	// A normal toggle after the test recording must paste again.
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "second succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })
	if got := pa.pastedTexts(); len(got) != 1 {
		t.Fatalf("paste count = %d, want 1 after a real toggle", len(got))
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

func TestLastResultKeepsFullText(t *testing.T) {
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
	st := d.State()
	if st.LastResult != long {
		t.Fatalf("LastResult must carry the complete text: len=%d", len(st.LastResult))
	}
	if st.Transcript != long {
		t.Fatalf("Transcript must carry the complete original text: len=%d", len(st.Transcript))
	}
}

// Translation must not destroy the original transcript: the socket snapshot
// keeps the complete pre-translation text in Transcript while LastResult
// carries the complete translated text. Translation is only performed for
// explicit translate-toggle requests.
func TestTranslationKeepsOriginalTranscript(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, _ := testDaemon(t, paths)
	tr.text = "original speech text"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"翻訳されたテキスト"}}]}`)
	}))
	defer srv.Close()

	d.cfg.Translation = config.TranslationConfig{
		Enabled:        true,
		BaseURL:        srv.URL,
		Model:          "test-model",
		APIKey:         "test-key",
		TargetLanguage: "Japanese",
	}

	if _, perr := d.TranslateToggle(); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := d.TranslateToggle(); perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })
	st := d.State()
	if st.Transcript != "original speech text" {
		t.Fatalf("Transcript = %q, want the original pre-translation text", st.Transcript)
	}
	if st.LastResult != "翻訳されたテキスト" {
		t.Fatalf("LastResult = %q, want the complete translated text", st.LastResult)
	}
}

// Plain toggles must never translate, even when translation is globally
// enabled: translation is an explicit translate-toggle request only.
func TestToggleDoesNotTranslate(t *testing.T) {
	paths := testPaths(t)
	d, _, tr, _ := testDaemon(t, paths)
	tr.text = "raw recognition text"

	d.cfg.Translation = config.TranslationConfig{
		Enabled:        true,
		BaseURL:        "http://127.0.0.1:1", // unreachable — must never be called
		Model:          "test-model",
		APIKey:         "test-key",
		TargetLanguage: "Japanese",
	}

	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	if _, perr := d.Toggle(); perr != nil {
		t.Fatal(perr)
	}
	waitFor(t, "succeeded phase", func() bool { return d.State().Phase == protocol.PhaseSucceeded })
	st := d.State()
	if st.LastResult != "raw recognition text" {
		t.Fatalf("LastResult = %q, want the raw recognition text (no translation)", st.LastResult)
	}
}

// translate-toggle fails fast with a user-facing error when translation is
// globally disabled, instead of silently degrading to plain dictation.
func TestTranslateToggleRequiresEnabled(t *testing.T) {
	paths := testPaths(t)
	d, _, _, _ := testDaemon(t, paths)
	d.cfg.Translation.Enabled = false

	_, perr := d.TranslateToggle()
	if perr == nil {
		t.Fatal("expected error when translation is disabled")
	}
	if perr.Code != protocol.ErrTranslationDisabled {
		t.Fatalf("error code = %q, want %q", perr.Code, protocol.ErrTranslationDisabled)
	}
	if d.State().Phase != protocol.PhaseIdle {
		t.Fatalf("phase = %q, want idle (no recording started)", d.State().Phase)
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

// TestStateReadinessCached guards the performance fix for the test-overlay
// lag: State() must not SHA-256 the ONNX model files on every call. The
// readiness probes are cached with a TTL, so repeated calls within the TTL
// reuse the cached snapshot instead of re-hashing.
func TestStateReadinessCached(t *testing.T) {
	d, _, _, _ := testDaemon(t, testPaths(t))

	first := d.State()
	if !first.Microphone {
		t.Fatalf("first State: Microphone = false, want true (fake micOK)")
	}
	d.stateMu.Lock()
	stampAfterFirst := d.readyAt
	d.stateMu.Unlock()
	if stampAfterFirst.IsZero() {
		t.Fatal("first State() did not populate the readiness cache (readyAt zero)")
	}

	// Immediate second call: cache hit, readyAt must not move.
	_ = d.State()
	d.stateMu.Lock()
	stampAfterSecond := d.readyAt
	d.stateMu.Unlock()
	if !stampAfterSecond.Equal(stampAfterFirst) {
		t.Fatalf("second State() recomputed readiness (cache miss): readyAt %v -> %v", stampAfterFirst, stampAfterSecond)
	}

	// Age the cache past the TTL: the next State() must refresh readyAt.
	d.stateMu.Lock()
	d.readyAt = time.Now().Add(-readinessTTL - time.Second)
	d.stateMu.Unlock()
	_ = d.State()
	d.stateMu.Lock()
	stampAfterRefresh := d.readyAt
	d.stateMu.Unlock()
	if !stampAfterRefresh.After(stampAfterFirst) {
		t.Fatalf("State() did not refresh stale readiness: readyAt %v not after %v", stampAfterRefresh, stampAfterFirst)
	}
}

// --- mic level sampling ---

func TestSampleMicLevel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "capture.raw")

	// Missing capture: not recording yet -> 0.
	if got := sampleMicLevel(path); got != 0 {
		t.Fatalf("missing capture should sample 0, got %d", got)
	}

	writeS16 := func(samples ...int16) {
		t.Helper()
		var buf []byte
		for _, s := range samples {
			buf = append(buf, byte(s), byte(s>>8))
		}
		if err := os.WriteFile(path, buf, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Too short (< 0.1s at 16 kHz): no level yet.
	writeS16(make([]int16, 100)...)
	if got := sampleMicLevel(path); got != 0 {
		t.Fatalf("too-short capture should sample 0, got %d", got)
	}

	// A capture at full scale: 3200+ silent samples then a loud burst.
	samples := make([]int16, 4000)
	for i := 3000; i < 3100; i++ {
		samples[i] = 12000
	}
	writeS16(samples...)
	if got := sampleMicLevel(path); got != 100 {
		t.Fatalf("peak 12000 should sample 100, got %d", got)
	}

	// Moderate speech: amplitude 1200 -> 10/100.
	for i := range samples {
		samples[i] = 0
	}
	for i := 0; i < 4000; i++ {
		samples[i] = 1200
	}
	writeS16(samples...)
	if got := sampleMicLevel(path); got != 10 {
		t.Fatalf("peak 1200 should sample 10, got %d", got)
	}

	// Silence: 0.
	for i := range samples {
		samples[i] = 0
	}
	writeS16(samples...)
	if got := sampleMicLevel(path); got != 0 {
		t.Fatalf("silence should sample 0, got %d", got)
	}
}

// State() reports the live mic level while recording, 0 otherwise, and the
// capture is read safely (recordingPath is guarded by stateMu).
func TestStateReportsMicLevelWhileRecording(t *testing.T) {
	paths := testPaths(t)
	d, rec, _, _ := testDaemon(t, paths)
	_ = rec

	d.stateMu.Lock()
	d.phase = protocol.PhaseRecording
	d.recordingPath = filepath.Join(t.TempDir(), "cap")
	d.stateMu.Unlock()

	// Loud capture -> level > 0 in State().
	var buf []byte
	for i := 0; i < 4000; i++ {
		s := int16(6000)
		buf = append(buf, byte(s), byte(s>>8))
	}
	if err := os.WriteFile(d.recordingPath+".raw", buf, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := d.State().MicLevel; got != 50 {
		t.Fatalf("recording state should report mic level 50, got %d", got)
	}

	// Idle -> 0.
	d.stateMu.Lock()
	d.phase = protocol.PhaseIdle
	d.stateMu.Unlock()
	if got := d.State().MicLevel; got != 0 {
		t.Fatalf("idle state should report mic level 0, got %d", got)
	}
}
