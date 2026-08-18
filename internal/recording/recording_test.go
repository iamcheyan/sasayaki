package recording

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

// ffmpeg inserts a LIST INFO chunk between fmt and data, so the audio
// payload does not sit at the canonical 44-byte offset. ReadDuration must
// walk the chunks instead of trusting fixed offsets, or every darwin
// recording is misparsed.
func TestReadDurationWalksChunks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ffmpeg.wav")

	// 250 ms of 16 kHz mono = 8000 bytes of PCM.
	data := make([]byte, SampleRate*2*250/1000)
	for i := 0; i < len(data); i += 2 {
		binary.LittleEndian.PutUint16(data[i:], 1000)
	}
	// fmt chunk (16 bytes) + LIST chunk ("INFO" + ISFT sub-chunk, 24
	// bytes, even) before data, mirroring ffmpeg's wav muxer.
	list := []byte("INFOISFT\x0c\x00\x00\x00Lavf62.1.1\x00\x00")
	wav := bytes.NewBuffer(nil)
	wav.WriteString("RIFF")
	payload := 4 + 8 + 16 + 8 + len(list) + 8 + len(data)
	binary.Write(wav, binary.LittleEndian, uint32(payload))
	wav.WriteString("WAVE")
	blockAlign := Channels * Bits / 8
	var fmtBody [16]byte
	binary.LittleEndian.PutUint16(fmtBody[0:2], 1) // PCM
	binary.LittleEndian.PutUint16(fmtBody[2:4], uint16(Channels))
	binary.LittleEndian.PutUint32(fmtBody[4:8], SampleRate)
	binary.LittleEndian.PutUint32(fmtBody[8:12], uint32(SampleRate*blockAlign))
	binary.LittleEndian.PutUint16(fmtBody[12:14], uint16(blockAlign))
	binary.LittleEndian.PutUint16(fmtBody[14:16], uint16(Bits))
	wav.WriteString("fmt ")
	binary.Write(wav, binary.LittleEndian, uint32(16))
	wav.Write(fmtBody[:])
	wav.WriteString("LIST")
	binary.Write(wav, binary.LittleEndian, uint32(len(list)))
	wav.Write(list)
	wav.WriteString("data")
	binary.Write(wav, binary.LittleEndian, uint32(len(data)))
	wav.Write(data)
	if err := os.WriteFile(path, wav.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	duration, err := ReadDuration(path)
	if err != nil {
		t.Fatal(err)
	}
	if duration != 250*time.Millisecond {
		t.Fatalf("duration = %v, want 250ms", duration)
	}
}
