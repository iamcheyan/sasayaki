package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// downloadClient is overridable in tests.
var downloadClient = &http.Client{Timeout: 30 * time.Minute}

type progressWriter struct {
	writer     io.Writer
	total      int64
	downloaded int64
	fileName   string
	lastReport time.Time
	progressFn func(string)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.writer.Write(p)
	pw.downloaded += int64(n)
	now := time.Now()
	if pw.progressFn != nil && (now.Sub(pw.lastReport) >= 200*time.Millisecond || (pw.total > 0 && pw.downloaded >= pw.total)) {
		pw.lastReport = now
		pct := 0
		if pw.total > 0 {
			pct = int(float64(pw.downloaded) / float64(pw.total) * 100)
		}
		if pct > 100 {
			pct = 100
		}
		mbDone := float64(pw.downloaded) / (1024 * 1024)
		mbTotal := float64(pw.total) / (1024 * 1024)

		barWidth := 20
		filled := (pct * barWidth) / 100
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("=", filled)
		if filled < barWidth {
			bar += ">" + strings.Repeat(" ", barWidth-filled-1)
		} else {
			bar = strings.Repeat("=", barWidth)
		}

		var msg string
		if pw.total > 0 {
			msg = fmt.Sprintf("Downloading %s: %.1f/%.1f MB [%s] %d%%", pw.fileName, mbDone, mbTotal, bar, pct)
		} else {
			msg = fmt.Sprintf("Downloading %s: %.1f MB", pw.fileName, mbDone)
		}
		pw.progressFn(msg)
	}
	return n, err
}

func downloadFile(url, destination, wantSHA string, expectedSize int64, progress func(string)) error {
	part := destination + ".part"
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	offset, err := out.Seek(0, io.SeekEnd)
	if err != nil {
		out.Close()
		return err
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		out.Close()
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		out.Close()
		return fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()

	if expectedSize <= 0 && resp.ContentLength > 0 {
		expectedSize = resp.ContentLength + offset
	}

	pw := &progressWriter{
		writer:     out,
		total:      expectedSize,
		downloaded: offset,
		fileName:   filepath.Base(destination),
		progressFn: progress,
	}

	switch resp.StatusCode {
	case http.StatusOK:
		if offset > 0 {
			if err := out.Truncate(0); err != nil {
				out.Close()
				return err
			}
			if _, err := out.Seek(0, io.SeekStart); err != nil {
				out.Close()
				return err
			}
			pw.downloaded = 0
		}
		if _, err := io.Copy(pw, resp.Body); err != nil {
			out.Close()
			return fmt.Errorf("downloading %s: %w", url, err)
		}
	case http.StatusPartialContent:
		if _, err := io.Copy(pw, resp.Body); err != nil {
			out.Close()
			return fmt.Errorf("downloading %s: %w", url, err)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// The .part already holds the full file; fall through to verify.
	default:
		out.Close()
		return fmt.Errorf("downloading %s: HTTP %s", url, resp.Status)
	}
	if err := out.Close(); err != nil {
		return err
	}

	if progress != nil {
		progress("Verifying SHA-256 checksum for " + filepath.Base(destination) + "…")
	}

	sum, err := sha256File(part)
	if err != nil {
		return err
	}
	if sum != wantSHA {
		_ = os.Remove(part)
		return fmt.Errorf("checksum mismatch for %s: download is corrupt; re-run `sasayaki setup` to retry", filepath.Base(destination))
	}
	return nil
}

func downloadModel(p config.Paths, modelID string, progress func(string)) (string, error) {
	var downloaded []string
	files := transcribe.ModelFiles(modelID)
	if len(files) == 0 {
		return "", fmt.Errorf("unknown speech model %q", modelID)
	}
	for _, f := range files {
		dest := filepath.Join(transcribe.ModelDir(p, modelID), f.Name)
		if fileValid(dest, f.SHA256) {
			continue
		}
		if progress != nil {
			progress("Downloading " + f.Name + "…")
		}
		if err := downloadFile(transcribe.ModelSource(modelID)+f.Name, dest, f.SHA256, f.Size, progress); err != nil {
			return "", err
		}
		if err := os.Rename(dest+".part", dest); err != nil {
			return "", err
		}
		downloaded = append(downloaded, f.Name)
	}
	if len(downloaded) == 0 {
		return "model files already verified", nil
	}
	return "downloaded and verified: " + join(downloaded), nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileValid(path, wantSHA string) bool {
	sum, err := sha256File(path)
	if err != nil {
		return false
	}
	return sum == wantSHA
}

func join(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += item
	}
	return out
}
