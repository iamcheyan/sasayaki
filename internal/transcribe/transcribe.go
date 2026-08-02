// Package transcribe manages the private model runtime: a long-lived
// engine.py process that keeps the SenseVoice model warm, plus model
// manifest verification.
package transcribe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/engine"
)

// WorkerState mirrors the observable health of the model worker.
const (
	WorkerDead     = "dead"
	WorkerStarting = "starting"
	WorkerWarm     = "warm"
)

const (
	startTimeout   = 120 * time.Second // cold model load
	initialBackoff = 500 * time.Millisecond
	maxBackoff     = 30 * time.Second
)

// Worker owns one engine.py serve process. It is safe for concurrent use.
type Worker struct {
	paths    config.Paths
	language string
	modelID  string

	mu      sync.Mutex
	state   string
	lastErr string
	proc    *exec.Cmd
	stdin   io.WriteCloser
	pending map[uint64]chan result
	nextID  uint64
	backoff time.Duration
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

type result struct {
	text string
	err  error
}

// NewWorker creates a worker for the given model directory and language.
// It does not start the process; call Start or ensureWarm.
func NewWorker(paths config.Paths, language, modelID string) *Worker {
	return &Worker{
		paths:    paths,
		language: language,
		modelID:  modelID,
		state:    WorkerDead,
		pending:  make(map[uint64]chan result),
		nextID:   1,
		stopCh:   make(chan struct{}),
	}
}

// Start spawns the engine process and waits until the model is warm or the
// start times out. A missing runtime/model yields a descriptive error; the
// worker keeps its dead state so Status() stays truthful.
func (w *Worker) Start(ctx context.Context) error {
	return w.start(ctx, true)
}

// start spawns (or re-spawns) the engine process under w.mu, so concurrent
// callers (EnsureWarm racing the restart loop, or two toggles) never spawn a
// second engine.py — the loser would leak ~1GB of resident model. A starting
// or warm worker is a no-op. The lock is held across cmd.Start so Shutdown
// cannot race a half-spawned process into existence.
func (w *Worker) start(ctx context.Context, recordBackoff bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stoppingLocked() {
		return errors.New("worker is shutting down")
	}
	// Single-flight: a starting or warm worker already owns a process.
	if w.state == WorkerStarting || w.state == WorkerWarm {
		return nil
	}
	return w.startLocked(ctx, recordBackoff)
}

// EnsureWarm blocks until the worker is warm or ctx expires, starting the
// process if needed. It is used before transcription so a cold daemon can
// serve its first toggle without failing.
func (w *Worker) EnsureWarm(ctx context.Context) error {
	w.mu.Lock()
	state := w.state
	w.mu.Unlock()
	if state == WorkerWarm {
		return nil
	}
	if err := w.Start(ctx); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

// Transcribe sends a wav path to the warm worker and returns normalized
// text. A dead or starting worker yields a descriptive error.
func (w *Worker) Transcribe(ctx context.Context, wav string) (string, error) {
	w.mu.Lock()
	if w.state != WorkerWarm || w.proc == nil {
		state, lastErr := w.state, w.lastErr
		w.mu.Unlock()
		if state == WorkerStarting {
			return "", fmt.Errorf("model engine is still starting")
		}
		if lastErr != "" {
			return "", fmt.Errorf("model engine is unavailable: %s", lastErr)
		}
		return "", fmt.Errorf("model engine is unavailable")
	}
	id := w.nextID
	w.nextID++
	ch := make(chan result, 1)
	w.pending[id] = ch
	stdin := w.stdin
	w.mu.Unlock()

	payload, _ := json.Marshal(map[string]interface{}{
		"id": id, "command": "transcribe", "wav": wav, "language": w.language,
	})
	if _, err := stdin.Write(append(payload, '\n')); err != nil {
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
		return "", fmt.Errorf("model engine write failed: %w", err)
	}

	select {
	case r := <-ch:
		return r.text, r.err
	case <-ctx.Done():
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
		return "", ctx.Err()
	}
}

// Status returns the worker state and the last error ("" when healthy).
func (w *Worker) Status() (state, lastErr string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state, w.lastErr
}

// Shutdown stops the restart loop and kills the engine process, waiting for
// it to exit. Idempotent.
func (w *Worker) Shutdown() {
	w.mu.Lock()
	select {
	case <-w.stopCh:
		w.mu.Unlock()
		return
	default:
		close(w.stopCh)
	}
	proc := w.proc
	w.proc = nil
	w.state = WorkerDead
	w.stdin = nil
	for id, ch := range w.pending {
		ch <- result{err: errors.New("engine stopped")}
		delete(w.pending, id)
	}
	w.mu.Unlock()
	if proc != nil && proc.Process != nil {
		_ = proc.Process.Kill()
		_ = proc.Wait()
	}
	w.wg.Wait()
}

// startLocked spawns the process and waits for its ready line. On failure
// the worker transitions to dead with the error recorded. Caller MUST hold
// w.mu and MUST have checked stoppingLocked + single-flight (state not
// Starting/Warm). The lock is released while blocked on the ready line so
// Transcribe/Status can run, then reacquired to record the outcome.
func (w *Worker) startLocked(ctx context.Context, recordBackoff bool) error {
	selected, ok := SpeechModelByID(w.modelID)
	if !ok {
		return fmt.Errorf("unknown speech model %q", w.modelID)
	}
	cmd := exec.Command(engine.Python(w.paths), "-u", w.paths.EngineScript(), "serve",
		"--model-dir", ModelDir(w.paths, selected.ID), "--model-file", selected.ModelFile.Name,
		"--architecture", selected.Architecture, "--language", w.language)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &boundedBuffer{limit: 4 << 10}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		w.state = WorkerDead
		w.lastErr = fmt.Sprintf("starting engine: %v", err)
		return fmt.Errorf("starting model engine: %w", err)
	}

	w.proc = cmd
	w.stdin = stdin
	w.state = WorkerStarting
	ready := make(chan error, 1)
	w.wg.Add(1)
	go w.readLoop(stdout, ready)

	// Release the lock while blocked on the ready line so Transcribe/Status
	// can run, then reacquire it to record the outcome.
	w.mu.Unlock()
	var readyErr error
	select {
	case readyErr = <-ready:
	case <-ctx.Done():
		readyErr = ctx.Err()
	}
	w.mu.Lock()

	// Shutdown may have closed stopCh while we were unlocked. If so, kill
	// the process we just spawned and leave the worker dead — never let a
	// start win against a shutdown.
	if w.stoppingLocked() {
		w.mu.Unlock()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		w.wg.Wait()
		return errors.New("worker is shutting down")
	}

	if readyErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		w.wg.Wait()
		detail := readyErr.Error()
		if readyErr == ctx.Err() {
			detail = "engine did not become ready in time"
		}
		if extra := strings.TrimSpace(stderr.String()); extra != "" {
			detail += " (" + extra + ")"
		}
		w.state = WorkerDead
		w.lastErr = detail
		return fmt.Errorf("%s", detail)
	}
	w.state = WorkerWarm
	w.lastErr = ""
	if recordBackoff {
		w.backoff = initialBackoff
	}
	return nil
}

// readLoop consumes engine stdout. The first line is the ready marker; later
// lines are routed to pending request channels. When the process exits
// (EOF/read error) all pending requests fail and a restart is scheduled.
func (w *Worker) readLoop(out io.Reader, ready chan<- error) {
	defer w.wg.Done()
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	first := true
	for scanner.Scan() {
		line := scanner.Bytes()
		var msg struct {
			ID      uint64 `json:"id"`
			OK      bool   `json:"ok"`
			Ready   bool   `json:"ready"`
			Text    string `json:"text"`
			Error   string `json:"error"`
			Command string `json:"command"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if first {
			first = false
			if msg.Ready {
				ready <- nil
			} else {
				ready <- fmt.Errorf("model engine failed to start: %s", msg.Error)
				return
			}
			continue
		}
		if msg.Command == "ping" {
			continue
		}
		w.mu.Lock()
		ch, ok := w.pending[msg.ID]
		delete(w.pending, msg.ID)
		w.mu.Unlock()
		if !ok {
			continue
		}
		if msg.OK {
			ch <- result{text: msg.Text}
		} else {
			ch <- result{err: errors.New(msg.Error)}
		}
	}
	// Process exited or pipe broke.
	w.mu.Lock()
	hadProc := w.proc != nil
	if hadProc {
		w.proc = nil
		w.stdin = nil
	}
	for id, ch := range w.pending {
		ch <- result{err: errors.New("model engine exited")}
		delete(w.pending, id)
	}
	select {
	case <-w.stopCh:
		w.mu.Unlock()
		return
	default:
	}
	lastErr := w.lastErr
	if hadProc {
		lastErr = "model engine exited unexpectedly"
	}
	w.state = WorkerDead
	w.lastErr = lastErr
	w.mu.Unlock()
	w.scheduleRestart()
}

// scheduleRestart respawns the engine after a capped backoff unless the
// worker is shutting down. It loops rather than recurses so a persistently
// dead model cannot grow the call stack over time.
func (w *Worker) scheduleRestart() {
	for {
		w.mu.Lock()
		if w.backoff == 0 {
			w.backoff = initialBackoff
		}
		delay := w.backoff
		if w.backoff < maxBackoff {
			w.backoff *= 2
		}
		stopping := w.stoppingLocked()
		w.mu.Unlock()
		if stopping {
			return
		}

		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-w.stopCh:
			timer.Stop()
			return
		}
		timer.Stop()

		// Re-check shutdown after the sleep; a Shutdown during the wait
		// would otherwise spawn a process into a dead worker.
		ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
		if err := w.start(ctx, false); err != nil {
			cancel()
			continue // keep retrying with backoff; state is already dead
		}
		cancel()
		return // warm — restart loop exits, readLoop owns the next death
	}
}

func (w *Worker) stoppingLocked() bool {
	select {
	case <-w.stopCh:
		return true
	default:
		return false
	}
}

// boundedBuffer keeps the most recent bytes of child stderr for diagnostics.
type boundedBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > b.limit {
		p = p[len(p)-b.limit:]
	}
	if len(b.buf)+len(p) > b.limit {
		b.buf = append(b.buf[len(b.buf)-b.limit+len(p):], p...)
	} else {
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
