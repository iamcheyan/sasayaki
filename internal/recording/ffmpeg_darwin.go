//go:build darwin

package recording

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// FFMpeg is the production recorder backed by ffmpeg's avfoundation input:
// the ":0" device is the default microphone. ffmpeg writes the WAV
// container itself and flushes it (header plus buffered samples) only on
// graceful shutdown, so Stop must interrupt, never hard-kill — a killed
// ffmpeg leaves an empty file. ffmpeg also inserts a LIST INFO chunk into
// the header, which ReadDuration's chunk walk handles.
//
// Best-effort for terminal use: tccd attributes microphone access to the
// host terminal, and a terminal without the grant captures silence. The
// menubar app records in-process with AVAudioEngine when it can.
type FFMpeg struct {
	cmd    *exec.Cmd
	path   string
	stderr bytes.Buffer
}

// NewFFMpeg returns a recorder using ffmpeg avfoundation.
func NewFFMpeg() *FFMpeg { return &FFMpeg{} }

// NewDefault returns the production recorder for the platform: ffmpeg
// against AVFoundation on macOS.
func NewDefault() Recorder { return NewFFMpeg() }

// FFMpegArgs returns the exact argv used to record to path. Kept as a pure
// function so tests can assert the command contract. The leading -nostdin
// keeps ffmpeg from consuming the daemon's stdin.
func FFMpegArgs(path string) []string {
	return []string{
		"-nostdin",
		"-loglevel", "error",
		"-f", "avfoundation",
		"-i", ":0",
		"-ar", "16000",
		"-ac", "1",
		"-f", "wav",
		path,
	}
}

func (f *FFMpeg) Start(path string) error {
	if f.cmd != nil {
		return errors.New("already recording")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f.stderr.Reset()
	cmd := exec.Command("ffmpeg", FFMpegArgs(path)...)
	cmd.Stderr = &f.stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting ffmpeg: %w", err)
	}
	f.cmd, f.path = cmd, path
	return nil
}

func (f *FFMpeg) Stop() (time.Duration, error) {
	path, err := f.stop()
	if err != nil {
		return 0, err
	}
	duration, err := ReadDuration(path)
	if err != nil {
		// ffmpeg's own complaint (device busy, no microphone access)
		// is more actionable than a bare header-parse failure.
		if msg := firstLine(f.stderr.Bytes()); msg != "" {
			return 0, fmt.Errorf("recording unusable: %s (%v)", msg, err)
		}
		return 0, fmt.Errorf("recording unusable: %w", err)
	}
	return duration, nil
}

func (f *FFMpeg) Cancel() error {
	path, err := f.stop()
	if err != nil {
		return err
	}
	_ = os.Remove(path)
	return nil
}

// stop interrupts ffmpeg and waits for it to write the complete WAV.
func (f *FFMpeg) stop() (string, error) {
	cmd, path := f.cmd, f.path
	f.cmd, f.path = nil, ""
	if cmd == nil || cmd.Process == nil {
		return "", errors.New("not recording")
	}
	stopInterrupt(cmd)
	return path, nil
}

// firstLine returns the first non-empty line of ffmpeg's stderr.
func firstLine(b []byte) string {
	for _, line := range bytes.Split(b, []byte("\n")) {
		if s := string(bytes.TrimSpace(line)); s != "" {
			return s
		}
	}
	return ""
}
