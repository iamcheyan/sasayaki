//go:build linux

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
