//go:build darwin

package recording

import (
	"reflect"
	"testing"
)

func TestFFMpegArgs(t *testing.T) {
	// The exact avfoundation capture verified in the field: default
	// microphone (":0"), resampled to Sasayaki's 16 kHz mono WAV.
	got := FFMpegArgs("/tmp/rec.wav")
	want := []string{
		"-nostdin",
		"-loglevel", "error",
		"-f", "avfoundation",
		"-i", ":0",
		"-ar", "16000",
		"-ac", "1",
		"-f", "wav",
		"/tmp/rec.wav",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FFMpegArgs = %v, want %v", got, want)
	}
}

// Stop/Cancel without a running capture must error instead of blocking on a
// nil process or reporting a bogus duration.
func TestFFMpegStopCancelWithoutStart(t *testing.T) {
	f := NewFFMpeg()
	if _, err := f.Stop(); err == nil {
		t.Fatal("Stop without Start must fail")
	}
	if err := f.Cancel(); err == nil {
		t.Fatal("Cancel without Start must fail")
	}
}

// NewDefault on darwin dispatches to the ffmpeg recorder.
func TestNewDefaultIsFFMpeg(t *testing.T) {
	if _, ok := NewDefault().(*FFMpeg); !ok {
		t.Fatalf("NewDefault = %T, want *FFMpeg", NewDefault())
	}
}
