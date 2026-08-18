package setup

import (
	"fmt"
	"net/http"
	"syscall"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/diagnostics"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// modelSpaceBytes is the disk budget (model ~240 MB + venv + headroom).
const modelSpaceBytes = 600 << 20

// diagnose is the diagnostics entry point, overridable in tests.
var diagnose = diagnostics.All

// checkDiskAndNetwork verifies the disk budget and, when a download will
// actually happen, that the model server is reachable. It never changes
// anything.
func checkDiskAndNetwork(p config.Paths) error {
	if free, err := diskFreeBytes(p.DataHome); err == nil && free < modelSpaceBytes {
		return fmt.Errorf("not enough free disk space (need ~%d MB, %d MB available) — free space and re-run `sasayaki setup`",
			modelSpaceBytes>>20, free>>20)
	}

	cfg, err := config.Load(p)
	if err != nil {
		return err
	}
	if !transcribe.ModelValidFor(p, cfg.SpeechModel) {
		if err := checkNetwork(); err != nil {
			return fmt.Errorf("cannot reach the model server (huggingface.co): %v — connect to the network and re-run `sasayaki setup`", err)
		}
	}
	return nil
}

// checkNetwork probes the model host with a short timeout; a probe is only
// done when a download will actually happen. Overridable in tests.
var checkNetwork = func() error {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodHead, transcribe.Model.Source, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("model server returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// diskFreeBytes reports free space on the filesystem holding path.
func diskFreeBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
