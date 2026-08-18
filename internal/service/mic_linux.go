//go:build linux

package service

import "os/exec"

// micAvailable is the linux readiness probe for the microphone path: the
// service records through parecord, so PulseAudio/PipeWire must be present.
func micAvailable() bool {
	_, err := exec.LookPath("parecord")
	return err == nil
}

// micHint is the user-facing hint when recording cannot start on linux.
const micHint = "parecord is not installed; install pulseaudio-utils and re-run sasayaki setup"
