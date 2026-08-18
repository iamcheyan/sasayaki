// Package recording captures microphone audio. Recorder is the interface
// the service depends on; parecord is the production implementation on Linux
// and ffmpeg (AVFoundation) on macOS. Tests use a fake recorder so no
// microphone is required in unit tests.
package recording

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
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

// stopInterrupt interrupts the child process and waits for it to exit
// without ever orphaning it. A hard kill is applied if the process does not
// exit promptly. SIGINT is load-bearing for both recorders: parecord
// flushes its raw capture and ffmpeg writes the whole WAV container (header
// plus buffered samples) only on graceful shutdown — a hard kill leaves
// ffmpeg's output empty.
func stopInterrupt(cmd *exec.Cmd) {
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
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
// error for anything that is not a valid PCM WAV. The chunks are walked
// rather than assumed at fixed offsets: ffmpeg (the darwin recorder)
// inserts a LIST INFO chunk between fmt and data, so the canonical
// 44-byte layout does not hold for its files.
func ReadDuration(path string) (time.Duration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return 0, errors.New("not a WAV file")
	}
	var rate, channels, bits, dataSize uint32
	haveFmt, haveData := false, false
	// Each chunk is 8 bytes of (FourCC, size) followed by its payload,
	// padded to an even length.
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := binary.LittleEndian.Uint32(data[off+4 : off+8])
		body := off + 8
		if body+int(size) > len(data) {
			// A truncated final chunk still yields a usable duration
			// when it is the audio payload.
			if id == "data" {
				dataSize, haveData = uint32(len(data)-body), true
			}
			break
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return 0, errors.New("malformed WAV header")
			}
			if binary.LittleEndian.Uint16(data[body:body+2]) != 1 {
				return 0, errors.New("not a PCM WAV")
			}
			channels = uint32(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			rate = binary.LittleEndian.Uint32(data[body+4 : body+8])
			bits = uint32(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			haveFmt = true
		case "data":
			dataSize, haveData = size, true
		}
		if haveFmt && haveData {
			break
		}
		next := body + int(size)
		if size%2 == 1 {
			next++
		}
		off = next
	}
	if !haveFmt {
		return 0, errors.New("malformed WAV header")
	}
	if !haveData || dataSize == 0 {
		return 0, errors.New("WAV has no audio data")
	}
	if rate == 0 || channels == 0 || bits == 0 {
		return 0, errors.New("WAV has invalid format fields")
	}
	return time.Duration(dataSize) * time.Second / time.Duration(rate*channels*bits/8), nil
}
