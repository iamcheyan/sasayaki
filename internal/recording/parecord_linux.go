//go:build linux

package recording

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Parecord is the production recorder backed by the parecord(1) tool. Audio
// is captured as raw s16le and wrapped into a WAV container by Go so the
// output is valid regardless of parecord's own header handling.
type Parecord struct {
	cmd  *exec.Cmd
	path string
}

// NewParecord returns a recorder using parecord.
func NewParecord() *Parecord { return &Parecord{} }

// NewDefault returns the production recorder for the platform: parecord
// against the PipeWire/PulseAudio stack on Linux.
func NewDefault() Recorder { return NewParecord() }

// ParecordArgs returns the exact argv used to record to path. Kept as a
// pure function so tests can assert the command contract.
func ParecordArgs(path string) []string {
	return []string{
		"--format=s16le",
		"--rate=16000",
		"--channels=1",
		"--latency-msec=10",
		path + ".raw",
	}
}

func (p *Parecord) Start(path string) error {
	if p.cmd != nil {
		return errors.New("already recording")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	cmd := exec.Command("parecord", ParecordArgs(path)...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting parecord: %w", err)
	}
	p.cmd, p.path = cmd, path
	return nil
}

func (p *Parecord) Stop() (time.Duration, error) {
	path, err := p.stop()
	if err != nil {
		return 0, err
	}
	return finalize(path)
}

func (p *Parecord) Cancel() error {
	path, err := p.stop()
	if err != nil {
		return err
	}
	_ = os.Remove(path + ".raw")
	_ = os.Remove(path)
	return nil
}

// stop interrupts parecord and waits for it to flush the raw capture.
func (p *Parecord) stop() (string, error) {
	cmd, path := p.cmd, p.path
	p.cmd, p.path = nil, ""
	if cmd == nil || cmd.Process == nil {
		return "", errors.New("not recording")
	}
	stopInterrupt(cmd)
	return path, nil
}

// finalize wraps the raw s16le capture in a WAV container and validates it.
// A missing or empty raw file means the microphone never delivered audio.
func finalize(path string) (time.Duration, error) {
	data, err := os.ReadFile(path + ".raw")
	if err != nil {
		return 0, fmt.Errorf("microphone produced no audio (%w)", err)
	}
	defer os.Remove(path + ".raw")
	if len(data) < 2 {
		return 0, errors.New("microphone produced an empty recording")
	}
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	if err := writeWAV(path, SampleRate, Channels, Bits, data); err != nil {
		return 0, err
	}
	duration := time.Duration(len(data)/2) * time.Second / SampleRate
	return duration, nil
}
