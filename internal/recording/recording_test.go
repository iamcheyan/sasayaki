package recording

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParecordArgs(t *testing.T) {
	// The recorder receives the final WAV path and captures raw s16le to
	// <wav>.raw; Go wraps it into the WAV container afterwards.
	got := ParecordArgs("/tmp/rec.wav")
	want := []string{
		"--format=s16le",
		"--rate=16000",
		"--channels=1",
		"--latency-msec=10",
		"/tmp/rec.wav.raw",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParecordArgs = %v, want %v", got, want)
	}
}

func TestWriteWAVHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")

	// 1 second of 16 kHz mono silence = 32000 bytes of s16le.
	data := make([]byte, SampleRate*2)
	if err := writeWAV(path, SampleRate, Channels, Bits, data); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Canonical 44-byte header.
	if len(raw) != 44+len(data) {
		t.Fatalf("file size = %d, want %d", len(raw), 44+len(data))
	}
	if string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" ||
		string(raw[12:16]) != "fmt " || string(raw[36:40]) != "data" {
		t.Fatalf("bad WAV markers: %q", raw[:44])
	}
	if got := binary.LittleEndian.Uint16(raw[20:22]); got != 1 {
		t.Fatalf("audio format = %d, want 1 (PCM)", got)
	}
	if got := binary.LittleEndian.Uint16(raw[22:24]); got != uint16(Channels) {
		t.Fatalf("channels = %d, want %d", got, Channels)
	}
	if got := binary.LittleEndian.Uint32(raw[24:28]); got != uint32(SampleRate) {
		t.Fatalf("sample rate = %d, want %d", got, SampleRate)
	}
	if got := binary.LittleEndian.Uint16(raw[34:36]); got != 16 {
		t.Fatalf("bits per sample = %d, want 16", got)
	}
}

func TestReadDuration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.wav")

	// 300 ms of 16 kHz mono = 9600 bytes of PCM.
	data := make([]byte, SampleRate*2*300/1000)
	if err := writeWAV(path, SampleRate, Channels, Bits, data); err != nil {
		t.Fatal(err)
	}
	duration, err := ReadDuration(path)
	if err != nil {
		t.Fatal(err)
	}
	if duration != 300*time.Millisecond {
		t.Fatalf("duration = %v, want 300ms", duration)
	}

	// Invalid input must error, not produce a bogus duration.
	bad := filepath.Join(dir, "bad.wav")
	if err := os.WriteFile(bad, []byte("not a wav"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDuration(bad); err == nil {
		t.Fatal("ReadDuration accepted a non-WAV file")
	}
}

func TestFinalizeEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.wav")
	if _, err := finalize(path); err == nil {
		t.Fatal("finalize accepted a missing raw file")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("finalize left a partial WAV behind")
	}
}

func TestFinalizeWrapsRaw(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "in.wav")
	raw := wav + ".raw" // finalize reads <path>.raw

	samples := make([]byte, SampleRate*2*500/1000) // 500 ms silence
	// Fill with a tone so the data section is not mistaken for all-zero.
	for i := 0; i < len(samples); i += 2 {
		binary.LittleEndian.PutUint16(samples[i:], 1000)
	}
	if err := os.WriteFile(raw, samples, 0o600); err != nil {
		t.Fatal(err)
	}

	duration, err := finalize(wav)
	if err != nil {
		t.Fatal(err)
	}
	if duration != 500*time.Millisecond {
		t.Fatalf("duration = %v, want 500ms", duration)
	}
	got, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[44:], samples) {
		t.Fatal("WAV data section does not match the raw capture")
	}
	// The raw capture must be consumed.
	if _, err := os.Stat(raw); !os.IsNotExist(err) {
		t.Fatal("finalize left the raw capture behind")
	}
}
