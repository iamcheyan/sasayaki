// Package service implements the Sasayaki user service: the Unix control
// socket, the recording/transcribing/pasting state machine, model worker
// ownership, retention cleanup and the systemd unit lifecycle.
package service

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/diagnostics"
	"github.com/iamcheyan/sasayaki/internal/paste"
	"github.com/iamcheyan/sasayaki/internal/protocol"
	"github.com/iamcheyan/sasayaki/internal/recording"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
	"github.com/iamcheyan/sasayaki/internal/translate"
)

// UnitName is the systemd user unit owned by Sasayaki.
const UnitName = "sasayaki.service"

const (
	transcribeTimeout = 2 * time.Minute
	warmTimeout       = 45 * time.Second
	cleanupInterval   = 5 * time.Minute
	// readinessTTL bounds how often State() re-runs the readiness probes.
	// transcribe.ModelValidFor SHA-256s the ONNX model files (hundreds of
	// MB); the test overlay polls State() every 300ms, so recomputing on
	// every call thrashes the CPU and starves inference. 30s is safe: the
	// model/runtime files only change via `sasayaki setup`, which restarts
	// the daemon.
	readinessTTL = 30 * time.Second
)

// Transcriber is the model runtime the service drives. transcribe.Worker is
// the production implementation; tests inject fakes.
type Transcriber interface {
	EnsureWarm(ctx context.Context) error
	Transcribe(ctx context.Context, wav string) (string, error)
	Status() (string, string)
	Shutdown()
}

// Paster injects text into the focused app. paste.Paste is the production
// implementation; tests inject fakes.
type Paster interface {
	Paste(text string) paste.Result
}

// pastePaster adapts the paste package's package-level function.
type pastePaster struct{}

func (pastePaster) Paste(text string) paste.Result { return paste.Paste(text) }

// Daemon is the running service. All exported methods are safe for
// concurrent use.
type Daemon struct {
	paths config.Paths
	cfg   config.Config
	log   *slog.Logger

	// deps are injectable side effects; production uses the defaults.
	newRecorder func() recording.Recorder
	transcriber Transcriber
	paster      Paster
	micOK       func() bool

	// opMu serializes user operations (toggle/cancel) so two toggles can
	// never race; stateMu guards the snapshot fields.
	opMu    sync.Mutex
	stateMu sync.Mutex

	recorder       recording.Recorder
	recordingPath  string
	recordingStart time.Time
	// testMode marks the current recording as a test overlay recording.
	// testSpeechOnly forces speech-only recognition without LLM translation.
	testMode       bool
	testSpeechOnly bool
	// opTranslate marks the current operation as an explicit translation
	// request (translate-toggle). Plain toggles never translate, even when
	// the global translation.enabled flag is set.
	opTranslate bool
	// generation identifies the active recording/transcription pipeline.
	// Cancelling or starting a new recording invalidates older goroutines so
	// a late model result can never paste into the next user's context.
	generation          uint64
	recordingGeneration uint64

	phase          protocol.Phase
	lastResult     string
	lastTranscript string
	lastError      string
	lastPhase      protocol.Phase
	lastAt         time.Time

	workerErr error

	// Cached readiness probes, refreshed at most every readinessTTL. See
	// the const comment for why these must not be recomputed per State().
	readyAt      time.Time
	runtimeOK    bool
	modelOK      bool
	microphoneOK bool
	pasteOK      bool
	pasteBackend string

	done chan struct{}
	once sync.Once
}

// New creates a daemon from the current config with production
// dependencies. It does not start anything.
func New(paths config.Paths, log *slog.Logger) (*Daemon, error) {
	cfg, err := config.Load(paths)
	if err != nil {
		return nil, err
	}
	d := newDaemon(paths, cfg, log)
	d.transcriber = transcribe.NewWorker(paths, cfg.Language, cfg.SpeechModel)
	return d, nil
}

// newDaemon builds the daemon with the given config and injectable
// dependencies; tests use it directly.
func newDaemon(paths config.Paths, cfg config.Config, log *slog.Logger) *Daemon {
	return &Daemon{
		paths:       paths,
		cfg:         cfg,
		log:         log,
		newRecorder: func() recording.Recorder { return recording.NewDefault() },
		transcriber: nil, // set by New or by the test
		paster:      pastePaster{},
		micOK:       func() bool { _, err := exec.LookPath("parecord"); return err == nil },
		phase:       protocol.PhaseIdle,
		done:        make(chan struct{}),
	}
}

// Run starts the daemon: it ensures private paths, opens the control socket,
// starts the model worker, runs retention cleanup and serves requests until
// shutdown. Signals (SIGTERM/SIGINT) trigger graceful cleanup.
func (d *Daemon) Run() error {
	if err := d.paths.Ensure(); err != nil {
		return err
	}
	if err := os.Remove(d.paths.Socket()); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", d.paths.Socket())
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(d.paths.Socket())
	if err := os.Chmod(d.paths.Socket(), 0o600); err != nil {
		return err
	}

	d.startWorker()
	go d.cleanupLoop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sig)

	errCh := make(chan error, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-d.done:
					return
				default:
				}
				errCh <- err
				return
			}
			go d.handle(conn)
		}
	}()

	select {
	case err := <-errCh:
		d.Shutdown()
		return err
	case <-sig:
		d.log.Info("shutting down on signal")
		d.Shutdown()
		return nil
	case <-d.done:
		d.Shutdown()
		return nil
	}
}

// Shutdown stops the recorder, model worker and retention loop. It never
// deletes configuration, model or runtime; only the socket and the active
// recording's partial file are cleaned.
func (d *Daemon) Shutdown() {
	d.once.Do(func() {
		close(d.done)
		d.opMu.Lock()
		defer d.opMu.Unlock()
		if d.recorder != nil {
			_ = d.recorder.Cancel()
			d.recorder = nil
		}
		if d.transcriber != nil {
			d.transcriber.Shutdown()
		}
	})
}

// startWorker starts the model worker (or records the failure). It is a
// no-op when a transcriber was injected (tests).
func (d *Daemon) startWorker() {
	if d.transcriber == nil {
		d.transcriber = transcribe.NewWorker(d.paths, d.cfg.Language, d.cfg.SpeechModel)
	}
	ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
	defer cancel()
	if err := d.transcriber.EnsureWarm(ctx); err != nil {
		d.workerErr = err
		d.log.Warn("model worker failed to start", "error", err)
	} else {
		d.workerErr = nil
		d.log.Info("model worker warm")
	}
}

// State returns a full snapshot of readiness and last operation.
func (d *Daemon) State() *protocol.State {
	// Sampling the recording tail needs the phase and the capture path; both
	// are guarded by stateMu (startRecording writes them under it).
	d.stateMu.Lock()
	phase, recordingPath := d.phase, d.recordingPath
	lastResult, lastTranscript, lastError := d.lastResult, d.lastTranscript, d.lastError
	lastPhase, lastAt := d.lastPhase, d.lastAt
	d.stateMu.Unlock()

	workerState, workerErr := "", ""
	if d.transcriber != nil {
		workerState, workerErr = d.transcriber.Status()
	}

	service := protocol.ServiceRunning
	if workerState == transcribe.WorkerDead && workerErr != "" {
		service = protocol.ServiceUnhealthy
	}

	runtimeOK, modelOK, micOK, pasteOK, pasteBackend := d.readiness()
	s := &protocol.State{
		Version:      protocol.Version,
		Service:      service,
		Phase:        phase,
		Runtime:      runtimeOK,
		Model:        modelOK,
		SpeechModel:  d.cfg.SpeechModel,
		Language:     d.cfg.Language,
		Translation:  translationState(d.cfg.Translation),
		Microphone:   micOK,
		Paste:        pasteOK,
		PasteBackend: pasteBackend,
		Worker:       workerState,
		LastResult:   lastResult,
		Transcript:   lastTranscript,
		LastError:    lastError,
		LastAt:       formatTime(lastAt),
		LastPhase:    lastPhase,
	}
	if workerErr != "" {
		s.LastError = workerErr // surfaced until next operation
	}
	if phase == protocol.PhaseRecording {
		s.MicLevel = sampleMicLevel(recordingPath + ".raw")
	}
	return s
}

// sampleMicLevel reads the tail of an in-progress s16le mono capture and
// returns a 0-100 peak-ish level, mirroring the reference UI's live audio
// feedback. A missing or too-short capture yields 0. The last ~0.2s (6400
// bytes at 16 kHz) is subsampled so every 150ms poll stays cheap.
func sampleMicLevel(rawPath string) int {
	f, err := os.Open(rawPath)
	if err != nil {
		return 0
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	size := st.Size()
	if size < 3200 { // need ~0.1s before reporting a level
		return 0
	}
	nbytes := int64(6400)
	if nbytes > size {
		nbytes = size
	}
	if _, err := f.Seek(size-nbytes, io.SeekStart); err != nil {
		return 0
	}
	raw := make([]byte, nbytes)
	if _, err := io.ReadFull(f, raw); err != nil {
		return 0
	}
	n := len(raw) / 2
	step := n / 400
	if step < 1 {
		step = 1
	}
	var peak int32
	for i := 0; i < n; i += step {
		v := int32(int16(binary.LittleEndian.Uint16(raw[i*2:])))
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	level := int(peak) * 100 / 12000
	if level > 100 {
		level = 100
	}
	return level
}

// readiness returns the cached runtime/model/microphone/paste probes,
// refreshing them when older than readinessTTL. Model verification hashes
// the ONNX files, so this must not run on every State() call (the test
// overlay polls at 300ms).
func (d *Daemon) readiness() (runtime, model, mic, pasteAvail bool, pasteBackend string) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if time.Since(d.readyAt) > readinessTTL {
		model = transcribe.ModelValidFor(d.paths, d.cfg.SpeechModel)
		runtime = model
		if _, err := os.Stat(d.paths.EngineScript()); err != nil {
			runtime = false
		}
		if _, err := os.Stat(d.paths.VenvMarker()); err != nil {
			runtime = false
		}
		d.modelOK, d.runtimeOK = model, runtime
		d.microphoneOK = d.micOK()
		d.pasteOK = paste.AvailableDefault()
		d.pasteBackend = paste.BestBackend(paste.DefaultRunner())
		d.readyAt = time.Now()
	}
	return d.runtimeOK, d.modelOK, d.microphoneOK, d.pasteOK, d.pasteBackend
}

// Toggle implements the idle → recording → transcribing transitions for the
// full pipeline: the result is pasted into the focused app. Plain toggles
// never translate — translation is an explicit translate-toggle request.
func (d *Daemon) Toggle() (string, *protocol.Error) {
	return d.toggle(false, false, false)
}

// TranslateToggle is Toggle with translation forced on for this operation.
// It fails fast when the global translation.enabled flag is off so the
// binding never silently degrades to plain dictation. The flag is read from
// disk so edits made in the control center take effect without a restart.
func (d *Daemon) TranslateToggle() (string, *protocol.Error) {
	if !d.translationEnabledFromDisk() {
		return "", protocol.NewError(protocol.ErrTranslationDisabled, protocol.ClassUser,
			"translation is disabled; enable it in the control center")
	}
	return d.toggle(false, false, true)
}

// translationEnabledFromDisk reports whether the translation feature is
// enabled, preferring the on-disk config (fresh edits win) and falling back
// to the in-memory snapshot when the file is unreadable.
func (d *Daemon) translationEnabledFromDisk() bool {
	if _, err := os.Stat(d.paths.ConfigFile()); err == nil {
		if freshCfg, err := config.Load(d.paths); err == nil {
			return freshCfg.Translation.Enabled
		}
	}
	return d.cfg.Translation.Enabled
}

func (d *Daemon) TestToggle() (string, *protocol.Error) {
	return d.toggle(true, true, false)
}

func (d *Daemon) TestToggleSpeech() (string, *protocol.Error) {
	return d.toggle(true, true, false)
}

func (d *Daemon) TestToggleTranslation() (string, *protocol.Error) {
	return d.toggle(true, false, true)
}

func (d *Daemon) toggle(test, speechOnly, translate bool) (string, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()

	switch d.phase {
	case protocol.PhaseRecording:
		return d.finishRecording()
	case protocol.PhaseIdle, protocol.PhaseSucceeded, protocol.PhaseFailed:
		d.testMode = test
		d.testSpeechOnly = speechOnly
		d.opTranslate = translate
		return d.startRecording()
	default:
		return "", protocol.NewError(protocol.ErrStillTranscribing, protocol.ClassUser,
			"Still transcribing the previous clip; wait a moment and try again")
	}
}

// Deliver transcribes a recording that was captured outside the service
// and runs the same transcribe → (translate) → paste pipeline a finished
// toggle uses. On macOS the menubar app owns the microphone because TCC
// grants follow the recording process, so it records with AVAudioEngine
// and ships the finalized WAV here; the service never records for a
// deliver.
func (d *Daemon) Deliver(wavPath string, translate bool) (string, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()

	switch d.phase {
	case protocol.PhaseIdle, protocol.PhaseSucceeded, protocol.PhaseFailed:
	default:
		return "", protocol.NewError(protocol.ErrStillTranscribing, protocol.ClassUser,
			"Still transcribing the previous clip; wait a moment and try again")
	}
	info, err := os.Stat(wavPath)
	if err != nil || info.Size() < 1024 {
		return "", protocol.NewError(protocol.ErrTooShort, protocol.ClassUser,
			"delivered recording is missing or empty")
	}
	// Take ownership through the recordings directory so retention cleanup
	// applies to delivered clips exactly like service-recorded ones.
	if err := os.MkdirAll(d.paths.RecordingsDir(), 0o700); err != nil {
		return "", protocol.NewError(protocol.ErrInternal, protocol.ClassService,
			"could not accept the delivered recording: "+err.Error())
	}
	dest := filepath.Join(d.paths.RecordingsDir(), fmt.Sprintf("%d.wav", time.Now().UnixNano()))
	if err := os.Rename(wavPath, dest); err != nil {
		if err := copyFile(wavPath, dest); err != nil {
			return "", protocol.NewError(protocol.ErrInternal, protocol.ClassService,
				"could not accept the delivered recording: "+err.Error())
		}
	}
	d.testMode = false
	d.testSpeechOnly = false
	d.opTranslate = translate
	d.stateMu.Lock()
	d.generation++
	d.recordingGeneration = d.generation
	d.stateMu.Unlock()
	generation := d.recordingGeneration
	d.log.Info("delivered recording accepted — transcribing…", "bytes", info.Size())
	go d.runTranscription(dest, generation, true)
	return "Delivered — transcribing…", nil
}

// copyFile is the cross-device fallback for Deliver when the source WAV
// lives on another volume than the recordings directory.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// Cancel aborts a recording or in-flight transcription.
func (d *Daemon) Cancel() (string, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()

	switch d.phase {
	case protocol.PhaseRecording:
		_ = d.recorder.Cancel()
		d.recorder = nil
		_ = os.Remove(d.recordingPath)
		d.invalidateOperation()
		d.setPhase(protocol.PhaseIdle, "", "")
		return "Recording cancelled", nil
	case protocol.PhaseTranscribing, protocol.PhasePasting:
		d.invalidateOperation()
		d.setPhase(protocol.PhaseIdle, "", "cancelled by user")
		return "Transcription cancelled", nil
	default:
		return "Nothing to cancel", nil
	}
}

func (d *Daemon) startRecording() (string, *protocol.Error) {
	if !d.micOK() {
		return "", protocol.NewError(protocol.ErrNotReady, protocol.ClassService,
			"parecord is not installed; install pulseaudio-utils and re-run sasayaki setup")
	}
	path := filepath.Join(d.paths.RecordingsDir(), fmt.Sprintf("%d.wav", time.Now().UnixNano()))
	rec := d.newRecorder()
	if err := rec.Start(path); err != nil {
		return "", protocol.NewError(protocol.ErrMicrophoneFailed, protocol.ClassService,
			"could not start the microphone: "+err.Error())
	}
	d.recorder = rec
	d.stateMu.Lock()
	d.recordingPath = path
	d.recordingStart = time.Now()
	d.generation++
	d.recordingGeneration = d.generation
	d.stateMu.Unlock()
	d.setPhase(protocol.PhaseRecording, "", "")
	mode := "real"
	if d.testMode {
		mode = "test"
	}
	d.log.Info("recording started — speak into the mic", "mode", mode)
	return "Recording — press the shortcut again when you are done", nil
}

func (d *Daemon) finishRecording() (string, *protocol.Error) {
	rec, path, generation := d.recorder, d.recordingPath, d.recordingGeneration
	d.recorder = nil
	duration, err := rec.Stop()
	if err != nil {
		_ = os.Remove(path)
		d.setPhase(protocol.PhaseFailed, "", "microphone error: "+err.Error())
		d.setTranscript("")
		d.log.Error("recording failed", "error", err)
		return "", protocol.NewError(protocol.ErrMicrophoneFailed, protocol.ClassService, err.Error())
	}
	if duration < config.MinRecording {
		_ = os.Remove(path)
		d.setPhase(protocol.PhaseFailed, "", fmt.Sprintf("recording too short (%s)", duration.Round(time.Millisecond)))
		d.setTranscript("")
		return "", protocol.NewError(protocol.ErrTooShort, protocol.ClassUser,
			fmt.Sprintf("Recording was too short (%s); nothing was transcribed", duration.Round(time.Millisecond)))
	}
	d.log.Info("recording finished — transcribing…", "duration", duration.String())

	// Transcription runs asynchronously so the socket returns promptly.
	// Test recordings never paste; real ones run the full pipeline.
	go d.runTranscription(path, generation, !d.testMode)
	return "Recording stopped — transcribing…", nil
}

// runTranscription executes the transcribe → (paste) pipeline and records
// the final phase. Test recordings (paste=false) stop after transcription:
// the result is exposed for the test overlay and nothing is pasted. It must
// be called with d.opMu released.
func (d *Daemon) runTranscription(path string, generation uint64, paste bool) {
	d.opMu.Lock()
	if !d.currentOperation(generation) {
		d.opMu.Unlock()
		_ = os.Remove(path)
		return
	}
	d.setPhase(protocol.PhaseTranscribing, "", "")
	d.opMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), transcribeTimeout)
	defer cancel()

	if !d.currentOperation(generation) {
		_ = os.Remove(path)
		return
	}
	if err := d.transcriber.EnsureWarm(ctx); err != nil {
		d.fail(path, generation, protocol.ErrModelFailed, "model engine is not ready: "+err.Error())
		return
	}
	if !d.currentOperation(generation) {
		_ = os.Remove(path)
		return
	}
	text, err := d.transcriber.Transcribe(ctx, path)
	if err != nil {
		d.fail(path, generation, protocol.ErrModelFailed, "transcription failed: "+err.Error())
		return
	}
	if strings.TrimSpace(text) == "" {
		d.fail(path, generation, protocol.ErrEmptySpeech, "no speech detected in the recording")
		return
	}
	transcript := text // original speech text, kept for the socket snapshot
	d.logMeta("transcribed", len(text))
	if d.cfg.VerboseTranscripts {
		d.log.Info("transcript", "text", text)
	}
	if !d.currentOperation(generation) {
		_ = os.Remove(path)
		return
	}
	// Reload translation config from disk so that settings changed in the TUI
	// are picked up immediately without restarting the daemon. Fall back to the
	// in-memory config if the file cannot be read (e.g. first run, disk error).
	translationCfg := d.cfg.Translation
	if _, err := os.Stat(d.paths.ConfigFile()); err == nil {
		if freshCfg, err := config.Load(d.paths); err == nil {
			translationCfg = freshCfg.Translation
		}
	}

	// Translate only for explicit translate-toggle operations (or test
	// translation). Plain toggles paste raw recognition even when the
	// global translation.enabled flag is on — the flag gates whether
	// translate-toggle is available at all.
	if d.opTranslate && translationCfg.Enabled && !d.testSpeechOnly {
		d.opMu.Lock()
		if d.currentOperation(generation) {
			d.setPhase(protocol.PhaseTranslating, "", "")
		}
		d.opMu.Unlock()
		translated, err := translate.Translate(ctx, translationCfg, strings.TrimSpace(text))
		if err != nil {
			d.log.Warn("translation failed, keeping raw speech transcript", "error", err)
			if !paste {
				d.opMu.Lock()
				if d.currentOperation(generation) {
					d.setPhase(protocol.PhaseSucceeded, transcript, "translation failed: "+err.Error())
					d.setTranscript(transcript)
				}
				d.opMu.Unlock()
				return
			}
		} else {
			text = translated
		}
	}

	if !paste {
		// Test overlay: recognition only. Expose the transcript as the
		// succeeded result; the paste pipeline is deliberately untouched.
		d.opMu.Lock()
		if d.currentOperation(generation) {
			d.setPhase(protocol.PhaseSucceeded, text, "")
			d.setTranscript(transcript)
		}
		d.opMu.Unlock()
		d.log.Info("test transcription ready — result exposed, nothing pasted")
		return
	}

	d.opMu.Lock()
	if !d.currentOperation(generation) {
		d.opMu.Unlock()
		_ = os.Remove(path)
		return
	}
	d.setPhase(protocol.PhasePasting, "", "")
	d.opMu.Unlock()
	if !d.currentOperation(generation) {
		_ = os.Remove(path)
		return
	}

	result := d.paster.Paste(strings.TrimSpace(text))
	if !d.currentOperation(generation) {
		return
	}
	if result.Pasted {
		d.opMu.Lock()
		if d.currentOperation(generation) {
			d.setPhase(protocol.PhaseSucceeded, text, "")
			d.setTranscript(transcript)
		}
		d.log.Info("paste succeeded", "backend", result.Backend)
		d.opMu.Unlock()
		return
	}
	// Truthful fallback: the text is on the clipboard but was not injected.
	d.opMu.Lock()
	if d.currentOperation(generation) {
		d.setPhase(protocol.PhaseFailed, text, result.Detail)
		d.setTranscript(transcript)
	}
	d.opMu.Unlock()
	d.log.Warn("paste did not reach the focused app", "detail", result.Detail)
}

func translationState(c config.TranslationConfig) string {
	if !c.Enabled {
		return "disabled"
	}
	if ok, _ := translate.Ready(c); ok {
		return "ready"
	}
	return "misconfigured"
}

func (d *Daemon) fail(path string, generation uint64, code, detail string) {
	_ = os.Remove(path)
	if !d.currentOperation(generation) {
		return
	}
	d.opMu.Lock()
	if d.currentOperation(generation) {
		d.setPhase(protocol.PhaseFailed, "", detail)
		d.setTranscript("")
	}
	d.opMu.Unlock()
	d.log.Warn("operation failed", "code", code, "detail", detail)
}

// setPhase records the current phase and, for terminal phases, the last
// operation outcome. Callers must hold opMu.
func (d *Daemon) setPhase(phase protocol.Phase, result, lastErr string) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	d.phase = phase
	switch phase {
	case protocol.PhaseSucceeded, protocol.PhaseFailed:
		d.lastPhase = phase
		d.lastResult = result
		d.lastError = lastErr
		d.lastAt = time.Now()
	}
}

// setTranscript records the complete original transcript of the last
// operation, before any translation. Callers must hold opMu, matching
// setPhase, and clear it (setTranscript("")) when an operation fails.
func (d *Daemon) setTranscript(transcript string) {
	d.stateMu.Lock()
	d.lastTranscript = transcript
	d.stateMu.Unlock()
}

func (d *Daemon) currentOperation(generation uint64) bool {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	return d.generation == generation
}

func (d *Daemon) invalidateOperation() {
	d.stateMu.Lock()
	d.generation++
	d.stateMu.Unlock()
}

// logMeta logs operation metadata without private text.
func (d *Daemon) logMeta(event string, textLen int) {
	d.log.Info(event, "chars", textLen)
}

// cleanupLoop removes recordings older than the configured retention. With
// keep_recordings enabled nothing is removed.
func (d *Daemon) cleanupLoop() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.cleanupRecordings()
		}
	}
}

func (d *Daemon) cleanupRecordings() {
	d.stateMu.Lock()
	keep := d.cfg.KeepRecordings
	retention := time.Duration(d.cfg.Retention)
	d.stateMu.Unlock()
	if keep {
		return
	}
	cutoff := time.Now().Add(-retention)
	entries, err := os.ReadDir(d.paths.RecordingsDir())
	if err != nil {
		return
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".wav") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(d.paths.RecordingsDir(), entry.Name())); err == nil {
				removed++
			}
		}
	}
	if removed > 0 {
		d.log.Info("removed expired recordings", "count", removed)
	}
}

// handle serves one socket connection.
func (d *Daemon) handle(conn net.Conn) {
	defer conn.Close()
	decoder := json.NewDecoder(bufio.NewReader(conn))
	var req protocol.Request
	if err := decoder.Decode(&req); err != nil {
		d.respond(conn, false, "invalid request",
			protocol.NewError(protocol.ErrInvalidRequest, protocol.ClassUser, "request is not valid JSON"), nil)
		return
	}
	if req.Version != protocol.Version {
		d.respond(conn, false, "protocol version mismatch",
			protocol.NewError(protocol.ErrBadVersion, protocol.ClassUser,
				fmt.Sprintf("client speaks version %d, service speaks %d", req.Version, protocol.Version)), nil)
		return
	}
	switch req.Operation {
	case protocol.OpStatus:
		d.respond(conn, true, "", nil, d.State())
	case protocol.OpDeliver:
		message, perr := d.Deliver(req.Wav, req.Translate)
		d.respond(conn, perr == nil, message, perr, d.State())
	case protocol.OpToggle:
		message, perr := d.Toggle()
		d.respond(conn, perr == nil, message, perr, d.State())
	case protocol.OpTranslateToggle:
		message, perr := d.TranslateToggle()
		d.respond(conn, perr == nil, message, perr, d.State())
	case protocol.OpTestToggle, protocol.OpTestSpeech:
		message, perr := d.TestToggleSpeech()
		d.respond(conn, perr == nil, message, perr, d.State())
	case protocol.OpTestTranslation:
		message, perr := d.TestToggleTranslation()
		d.respond(conn, perr == nil, message, perr, d.State())
	case protocol.OpCancel:
		message, perr := d.Cancel()
		d.respond(conn, perr == nil, message, perr, d.State())
	case protocol.OpDiagnose:
		report := diagnostics.All(d.paths)
		// A request maps to exactly one response. Sending a second JSON object
		// made generic protocol clients silently lose diagnostics after reading
		// the status snapshot.
		response := protocol.Response{
			Version:     protocol.Version,
			OK:          true,
			State:       d.State(),
			Diagnostics: report,
		}
		_ = json.NewEncoder(conn).Encode(response)
	default:
		d.respond(conn, false, "unknown operation",
			protocol.NewError(protocol.ErrUnknownOperation, protocol.ClassUser, req.Operation), nil)
	}
}

func (d *Daemon) respond(conn net.Conn, ok bool, message string, perr *protocol.Error, state *protocol.State) {
	response := protocol.Response{Version: protocol.Version, OK: ok, Message: message, State: state}
	if perr != nil {
		response.Error = perr
	}
	_ = json.NewEncoder(conn).Encode(response)
}

// Request sends one operation to the running service and decodes the
// response. Transport failures (no socket) are returned as errors; protocol
// and user-action errors come back as a typed Response.
func Request(paths config.Paths, operation string) (protocol.Response, error) {
	return RequestWithTimeout(paths, operation, 3*time.Second)
}

// RequestWithTimeout is Request with a caller-chosen deadline.
func RequestWithTimeout(paths config.Paths, operation string, timeout time.Duration) (protocol.Response, error) {
	conn, err := net.DialTimeout("unix", paths.Socket(), timeout)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("service is not running (no socket at %s); run `sasayaki service start` or `sasayaki setup`", paths.Socket())
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(protocol.Request{Version: protocol.Version, Operation: operation}); err != nil {
		return protocol.Response{}, err
	}
	reader := bufio.NewReader(conn)
	var response protocol.Response
	if err := json.NewDecoder(reader).Decode(&response); err != nil {
		return protocol.Response{}, err
	}
	return response, nil
}

// RequestDeliver ships a finalized WAV to the service for the transcribe →
// (translate) → paste pipeline. Used by the macOS menubar app, which owns
// the microphone, and by `sasayaki deliver`.
func RequestDeliver(paths config.Paths, wav string, translate bool, timeout time.Duration) (protocol.Response, error) {
	conn, err := net.DialTimeout("unix", paths.Socket(), timeout)
	if err != nil {
		return protocol.Response{}, fmt.Errorf("service is not running (no socket at %s); run `sasayaki service start` or `sasayaki setup`", paths.Socket())
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	req := protocol.Request{Version: protocol.Version, Operation: protocol.OpDeliver, Wav: wav, Translate: translate}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return protocol.Response{}, err
	}
	reader := bufio.NewReader(conn)
	var response protocol.Response
	if err := json.NewDecoder(reader).Decode(&response); err != nil {
		return protocol.Response{}, err
	}
	return response, nil
}

// RequestDiagnose sends a diagnose request and returns the report carried in
// its single versioned response.
func RequestDiagnose(paths config.Paths) (diagnostics.Report, error) {
	conn, err := net.DialTimeout("unix", paths.Socket(), 3*time.Second)
	if err != nil {
		return diagnostics.Report{}, fmt.Errorf("service is not running; run `sasayaki service start` or `sasayaki setup`")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_ = json.NewEncoder(conn).Encode(protocol.Request{Version: protocol.Version, Operation: protocol.OpDiagnose})
	reader := bufio.NewReader(conn)
	var response protocol.Response
	if err := json.NewDecoder(reader).Decode(&response); err != nil {
		return diagnostics.Report{}, err
	}
	if !response.OK {
		if response.Error != nil {
			return diagnostics.Report{}, fmt.Errorf("diagnose: %s", response.Error.Detail)
		}
		return diagnostics.Report{}, fmt.Errorf("diagnose request failed")
	}
	data, err := json.Marshal(response.Diagnostics)
	if err != nil {
		return diagnostics.Report{}, err
	}
	var report diagnostics.Report
	if err := json.Unmarshal(data, &report); err != nil {
		return diagnostics.Report{}, err
	}
	return report, nil
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
