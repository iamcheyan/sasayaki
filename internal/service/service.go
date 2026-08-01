// Package service implements the Sasayaki user service: the Unix control
// socket, the recording/transcribing/pasting state machine, model worker
// ownership, retention cleanup and the systemd unit lifecycle.
package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
)

// UnitName is the systemd user unit owned by Sasayaki.
const UnitName = "sasayaki.service"

const (
	transcribeTimeout = 2 * time.Minute
	warmTimeout       = 45 * time.Second
	cleanupInterval   = 5 * time.Minute
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
	// generation identifies the active recording/transcription pipeline.
	// Cancelling or starting a new recording invalidates older goroutines so
	// a late model result can never paste into the next user's context.
	generation          uint64
	recordingGeneration uint64

	phase      protocol.Phase
	lastResult string
	lastError  string
	lastPhase  protocol.Phase
	lastAt     time.Time

	workerErr error

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
	d.transcriber = transcribe.NewWorker(paths, cfg.Language)
	return d, nil
}

// newDaemon builds the daemon with the given config and injectable
// dependencies; tests use it directly.
func newDaemon(paths config.Paths, cfg config.Config, log *slog.Logger) *Daemon {
	return &Daemon{
		paths:       paths,
		cfg:         cfg,
		log:         log,
		newRecorder: func() recording.Recorder { return recording.NewParecord() },
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
		d.transcriber = transcribe.NewWorker(d.paths, d.cfg.Language)
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
	d.stateMu.Lock()
	phase, lastResult, lastError := d.phase, d.lastResult, d.lastError
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

	s := &protocol.State{
		Version:      protocol.Version,
		Service:      service,
		Phase:        phase,
		Runtime:      transcribe.Installed(d.paths),
		Model:        transcribe.ModelValid(d.paths),
		Microphone:   d.micOK(),
		Paste:        paste.AvailableDefault(),
		PasteBackend: paste.BestBackend(paste.DefaultRunner()),
		Worker:       workerState,
		LastResult:   lastResult,
		LastError:    lastError,
		LastAt:       formatTime(lastAt),
		LastPhase:    lastPhase,
	}
	if workerErr != "" {
		s.LastError = workerErr // surfaced until next operation
	}
	return s
}

// Toggle implements the idle → recording → transcribing transitions.
func (d *Daemon) Toggle() (string, *protocol.Error) {
	d.opMu.Lock()
	defer d.opMu.Unlock()

	switch d.phase {
	case protocol.PhaseRecording:
		return d.finishRecording()
	case protocol.PhaseIdle, protocol.PhaseSucceeded, protocol.PhaseFailed:
		// Succeeded and failed are terminal snapshots, not locked states. The
		// next toggle must begin a fresh recording while preserving the prior
		// result/error in Last* fields for the TUI and status command.
		return d.startRecording()
	default:
		return "", protocol.NewError(protocol.ErrStillTranscribing, protocol.ClassUser,
			"Still transcribing the previous clip; wait a moment and try again")
	}
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
	d.recordingPath = path
	d.recordingStart = time.Now()
	d.stateMu.Lock()
	d.generation++
	d.recordingGeneration = d.generation
	d.stateMu.Unlock()
	d.setPhase(protocol.PhaseRecording, "", "")
	d.log.Info("recording started")
	return "Recording — press the shortcut again when you are done", nil
}

func (d *Daemon) finishRecording() (string, *protocol.Error) {
	rec, path, generation := d.recorder, d.recordingPath, d.recordingGeneration
	d.recorder = nil
	duration, err := rec.Stop()
	if err != nil {
		_ = os.Remove(path)
		d.setPhase(protocol.PhaseFailed, "", "microphone error: "+err.Error())
		d.log.Error("recording failed", "error", err)
		return "", protocol.NewError(protocol.ErrMicrophoneFailed, protocol.ClassService, err.Error())
	}
	if duration < config.MinRecording {
		_ = os.Remove(path)
		d.setPhase(protocol.PhaseFailed, "", fmt.Sprintf("recording too short (%s)", duration.Round(time.Millisecond)))
		return "", protocol.NewError(protocol.ErrTooShort, protocol.ClassUser,
			fmt.Sprintf("Recording was too short (%s); nothing was transcribed", duration.Round(time.Millisecond)))
	}
	d.log.Info("recording finished", "duration", duration.String())

	// Transcription runs asynchronously so the socket returns promptly.
	go d.runTranscription(path, generation)
	return "Recording stopped — transcribing…", nil
}

// runTranscription executes the transcribe → paste pipeline and records the
// final phase. It must be called with d.opMu released.
func (d *Daemon) runTranscription(path string, generation uint64) {
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
	d.logMeta("transcribed", len(text))
	if d.cfg.VerboseTranscripts {
		d.log.Info("transcript", "text", text)
	}
	if !d.currentOperation(generation) {
		_ = os.Remove(path)
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
			d.setPhase(protocol.PhaseSucceeded, truncate(text), "")
		}
		d.log.Info("paste succeeded", "backend", result.Backend)
		d.opMu.Unlock()
		return
	}
	// Truthful fallback: the text is on the clipboard but was not injected.
	d.opMu.Lock()
	if d.currentOperation(generation) {
		d.setPhase(protocol.PhaseFailed, truncate(text), result.Detail)
	}
	d.opMu.Unlock()
	d.log.Warn("paste did not reach the focused app", "detail", result.Detail)
}

func (d *Daemon) fail(path string, generation uint64, code, detail string) {
	_ = os.Remove(path)
	if !d.currentOperation(generation) {
		return
	}
	d.opMu.Lock()
	if d.currentOperation(generation) {
		d.setPhase(protocol.PhaseFailed, "", detail)
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
	case protocol.OpToggle:
		message, perr := d.Toggle()
		d.respond(conn, perr == nil, message, perr, d.State())
	case protocol.OpCancel:
		message, perr := d.Cancel()
		d.respond(conn, perr == nil, message, perr, d.State())
	case protocol.OpDiagnose:
		report := diagnostics.All(d.paths)
		d.respond(conn, true, "", nil, d.State())
		// Diagnostics travel in the response payload via a second message.
		d.respondDiagnostics(conn, report)
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

func (d *Daemon) respondDiagnostics(conn net.Conn, report diagnostics.Report) {
	_ = json.NewEncoder(conn).Encode(protocol.Response{Version: protocol.Version, OK: true, Diagnostics: report})
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
	if response.Diagnostics != nil {
		return response, nil
	}
	return response, nil
}

// RequestDiagnose sends a diagnose request and returns the report carried in
// the follow-up message.
func RequestDiagnose(paths config.Paths) (diagnostics.Report, error) {
	conn, err := net.DialTimeout("unix", paths.Socket(), 3*time.Second)
	if err != nil {
		return diagnostics.Report{}, fmt.Errorf("service is not running; run `sasayaki service start` or `sasayaki setup`")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_ = json.NewEncoder(conn).Encode(protocol.Request{Version: protocol.Version, Operation: protocol.OpDiagnose})
	reader := bufio.NewReader(conn)
	// First message: the status response; the diagnostics report follows as
	// a second protocol.Response carrying it in Diagnostics.
	var status protocol.Response
	if err := json.NewDecoder(reader).Decode(&status); err != nil {
		return diagnostics.Report{}, err
	}
	var wrapped protocol.Response
	if err := json.NewDecoder(reader).Decode(&wrapped); err != nil {
		return diagnostics.Report{}, err
	}
	data, err := json.Marshal(wrapped.Diagnostics)
	if err != nil {
		return diagnostics.Report{}, err
	}
	var report diagnostics.Report
	if err := json.Unmarshal(data, &report); err != nil {
		return diagnostics.Report{}, err
	}
	return report, nil
}

// Systemctl runs a systemctl --user command.
func Systemctl(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// IsActive reports whether the user unit is active.
func IsActive() bool { return Systemctl("is-active", "--quiet", UnitName) == nil }

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func truncate(text string) string {
	const max = 60
	if len(text) <= max {
		return text
	}
	return text[:max] + "…"
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
