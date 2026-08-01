package setup

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/engine"
	"github.com/iamcheyan/sasayaki/internal/service"
)

const modelBase = "https://huggingface.co/csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/resolve/main/"

func Run(paths config.Paths, binary string, progress func(string)) error {
	if err := paths.Ensure(); err != nil {
		return err
	}
	if err := config.Save(paths, config.Config{Language: "auto"}); err != nil {
		return err
	}
	if err := engine.WriteScript(paths); err != nil {
		return err
	}
	if _, err := exec.LookPath("python3"); err != nil {
		return fmt.Errorf("python3 is required")
	}
	if _, err := exec.LookPath("parecord"); err != nil {
		return fmt.Errorf("parecord is required (install pulseaudio-utils)")
	}

	if _, err := os.Stat(engine.Python(paths)); os.IsNotExist(err) {
		progress("Creating Sasayaki's private Python runtime…")
		if err := run("python3", "-m", "venv", paths.VenvDir()); err != nil {
			return err
		}
		progress("Installing local speech runtime…")
		if err := run(engine.Python(paths), "-m", "pip", "install", "--upgrade", "pip", "sherpa-onnx", "numpy"); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(paths.ModelDir(), 0o700); err != nil {
		return err
	}
	for _, name := range []string{"model.int8.onnx", "tokens.txt"} {
		dest := filepath.Join(paths.ModelDir(), name)
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			progress("Downloading SenseVoice " + name + "…")
			if err := download(modelBase+name, dest); err != nil {
				return err
			}
		}
	}
	progress("Installing Sasayaki user service…")
	if err := installUnit(paths, binary); err != nil {
		return err
	}
	if err := service.Systemctl("daemon-reload"); err != nil {
		return err
	}
	if err := service.Systemctl("enable", "--now", "sasayaki.service"); err != nil {
		return err
	}
	progress("Ready — bind `sasayaki toggle` in your desktop settings.")
	return nil
}

func installUnit(paths config.Paths, binary string) error {
	if err := os.MkdirAll(filepath.Dir(paths.ServiceFile()), 0o700); err != nil {
		return err
	}
	unit := "[Unit]\nDescription=Sasayaki local voice input\nAfter=graphical-session.target\n\n[Service]\nType=simple\nExecStart=" + binary + " serve\nRestart=on-failure\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n"
	return os.WriteFile(paths.ServiceFile(), []byte(unit), 0o600)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func download(url, destination string) error {
	response, err := http.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, response.Status)
	}
	temp := destination + ".part"
	f, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, response.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(temp)
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp, destination)
}
