// Package recording captures microphone audio. Recorder is the interface the
// service depends on; Parecord is the production implementation. Tests use a
// fake recorder so no microphone is required in unit tests.
package recording

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// SampleRate, Channels and Bits are the recording format Sasayaki produces:
// 16 kHz, mono, signed 16-bit PCM, wrapped in a WAV container.
const (
	SampleRate = 16000
	Channels   = 1
	Bits       = 16
)

// Recorder captures audio to a WAV file.
type Recorder interface {
	// Start begins recording to path. It must be idempotent per instance.
	Start(path string) error
	// Stop finalizes the recording and returns its duration. A non-nil
	// error means the recording is unusable (microphone failure, invalid
	// file) and no audio was produced.
	Stop() (time.Duration, error)
	// Cancel abandons the recording, stops the child process and removes
	// partial files. It never returns a duration.
	Cancel() error
}

// Parecord is the production recorder backed by the parecord(1) tool. Audio
// is captured as raw s16le and wrapped into a WAV container by Go so the
// output is valid regardless of parecord's own header handling.
type Parecord struct {
	cmd  *exec.Cmd
	path string
}

// NewParecord returns a recorder using parecord.
func NewParecord() *Parecord { return &Parecord{} }

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

// stop interrupts the child process and waits for it to exit without ever
// orphaning parecord. A hard kill is applied if the process does not exit
// promptly.
func (p *Parecord) stop() (string, error) {
	cmd, path := p.cmd, p.path
	p.cmd, p.path = nil, ""
	if cmd == nil || cmd.Process == nil {
		return "", errors.New("not recording")
	}
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
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

// writeWAV writes a canonical 44-byte PCM WAV header followed by data.
func writeWAV(path string, rate, channels, bits int, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	blockAlign := channels * bits / 8
	byteRate := rate * blockAlign
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+len(data)))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:36], uint16(bits))
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(len(data)))
	if _, err := f.Write(header); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadDuration parses the duration of a Sasayaki WAV file, returning an
// error for anything that is not a valid PCM WAV.
func ReadDuration(path string) (time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	header := make([]byte, 44)
	if _, err := f.Read(header); err != nil {
		return 0, fmt.Errorf("not a WAV file: %w", err)
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return 0, errors.New("not a WAV file")
	}
	if string(header[12:16]) != "fmt " {
		return 0, errors.New("malformed WAV header")
	}
	if binary.LittleEndian.Uint16(header[20:22]) != 1 {
		return 0, errors.New("not a PCM WAV")
	}
	rate := binary.LittleEndian.Uint32(header[24:28])
	channels := binary.LittleEndian.Uint16(header[22:24])
	bits := binary.LittleEndian.Uint16(header[34:36])
	dataSize := binary.LittleEndian.Uint32(header[40:44])
	if dataSize == 0 {
		return 0, errors.New("WAV has no audio data")
	}
	if rate == 0 || channels == 0 || bits == 0 {
		return 0, errors.New("WAV has invalid format fields")
	}
	return time.Duration(dataSize) * time.Second / time.Duration(rate*uint32(channels)*uint32(bits)/8), nil
}
