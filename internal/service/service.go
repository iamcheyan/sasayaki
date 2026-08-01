package service

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/engine"
	"github.com/iamcheyan/sasayaki/internal/paste"
	"github.com/iamcheyan/sasayaki/internal/protocol"
)

type Daemon struct {
	paths     config.Paths
	config    config.Config
	mu        sync.Mutex
	recorder  *exec.Cmd
	recording string
}

func New(paths config.Paths) (*Daemon, error) {
	c, err := config.Load(paths)
	return &Daemon{paths: paths, config: c}, err
}

func (d *Daemon) Run() error {
	if err := d.paths.Ensure(); err != nil {
		return err
	}
	_ = os.Remove(d.paths.Socket())
	listener, err := net.Listen("unix", d.paths.Socket())
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(d.paths.Socket())
	if err := os.Chmod(d.paths.Socket(), 0o600); err != nil {
		return err
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go d.handle(conn)
	}
}

func (d *Daemon) handle(conn net.Conn) {
	defer conn.Close()
	var req protocol.Request
	err := json.NewDecoder(conn).Decode(&req)
	if err != nil {
		d.respond(conn, false, "invalid request", nil)
		return
	}
	switch req.Operation {
	case "status":
		d.respond(conn, true, "", d.State())
	case "toggle":
		message, err := d.Toggle()
		d.respond(conn, err == nil, message, d.State())
	case "stop":
		message, err := d.Stop()
		d.respond(conn, err == nil, message, d.State())
	default:
		d.respond(conn, false, "unknown operation", nil)
	}
}

func (d *Daemon) respond(conn net.Conn, ok bool, message string, state *protocol.State) {
	_ = json.NewEncoder(conn).Encode(protocol.Response{OK: ok, Message: message, State: state})
}

func (d *Daemon) State() *protocol.State {
	d.mu.Lock()
	defer d.mu.Unlock()
	return &protocol.State{Service: "running", Recording: d.recorder != nil, Model: engine.Installed(d.paths), Runtime: exists(engine.Python(d.paths)), Paste: paste.Available()}
}

func (d *Daemon) Toggle() (string, error) {
	d.mu.Lock()
	if d.recorder == nil {
		path := filepath.Join(d.paths.RecordingsDir(), fmt.Sprintf("%d.wav", time.Now().UnixNano()))
		cmd := exec.Command("parecord", "--format=s16le", "--rate=16000", "--channels=1", "--file-format=wav", "--latency-msec=10", path)
		if err := cmd.Start(); err != nil {
			d.mu.Unlock()
			return "Could not start microphone: " + err.Error(), err
		}
		d.recorder, d.recording = cmd, path
		d.mu.Unlock()
		return "Recording — press the shortcut again when you are done", nil
	}
	cmd, recording := d.recorder, d.recording
	d.recorder, d.recording = nil, ""
	d.mu.Unlock()
	_ = cmd.Process.Signal(os.Interrupt)
	_ = cmd.Wait()
	go d.transcribe(recording)
	return "Listening stopped — transcribing and pasting…", nil
}

func (d *Daemon) Stop() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.recorder == nil {
		return "No recording is active", nil
	}
	err := d.recorder.Process.Signal(os.Interrupt)
	d.recorder, d.recording = nil, ""
	return "Recording cancelled", err
}

func (d *Daemon) transcribe(recording string) {
	text, err := engine.Transcribe(d.paths, recording, d.config.Language)
	if err != nil || strings.TrimSpace(text) == "" {
		return
	}
	_ = paste.Text(strings.TrimSpace(text))
}

func Request(paths config.Paths, operation string) (protocol.Response, error) {
	conn, err := net.DialTimeout("unix", paths.Socket(), 700*time.Millisecond)
	if err != nil {
		return protocol.Response{}, errors.New("Sasayaki service is not running; run `sasayaki setup` or `sasayaki service start`")
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(protocol.Request{Operation: operation}); err != nil {
		return protocol.Response{}, err
	}
	var response protocol.Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&response); err != nil {
		return protocol.Response{}, err
	}
	return response, nil
}

func Systemctl(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func IsActive() bool { return Systemctl("is-active", "--quiet", "sasayaki.service") == nil }

func exists(path string) bool { _, err := os.Stat(path); return err == nil }
