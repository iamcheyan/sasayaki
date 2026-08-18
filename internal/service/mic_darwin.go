//go:build darwin

package service

import "os/exec"

// micAvailable is the darwin readiness probe for the microphone path. The
// service itself records through ffmpeg's avfoundation input; the macOS TCC
// grant applies to whichever process opens the mic, so the CLI path works
// when the owning terminal has the grant and the menubar app always records
// in-process instead. ffmpeg missing is the only deterministic failure.
func micAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// micHint is the user-facing hint when recording cannot start on darwin.
const micHint = "ffmpeg is not installed (brew install ffmpeg); the menubar app records natively and does not need it"
