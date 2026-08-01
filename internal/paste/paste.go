package paste

import (
	"bytes"
	"fmt"
	"os/exec"
)

func Available() bool {
	_, copyErr := exec.LookPath("wl-copy")
	_, typeErr := exec.LookPath("wtype")
	return copyErr == nil && typeErr == nil
}

func Text(text string) error {
	copy := exec.Command("wl-copy", "--trim-newline")
	copy.Stdin = bytes.NewBufferString(text)
	if output, err := copy.CombinedOutput(); err != nil {
		return fmt.Errorf("copy to clipboard: %w: %s", err, output)
	}

	if _, err := exec.LookPath("wtype"); err == nil {
		cmd := exec.Command("wtype", "-M", "ctrl", "-M", "shift", "-k", "v", "-m", "shift", "-m", "ctrl")
		if output, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			return fmt.Errorf("paste with wtype: %w: %s", err, output)
		}
	}
	return fmt.Errorf("clipboard updated, but no supported paste backend is available (install wtype)")
}
