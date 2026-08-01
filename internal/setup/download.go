package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
)

// downloadClient is overridable in tests.
var downloadClient = &http.Client{Timeout: 30 * time.Minute}

// downloadFile fetches url into destination via a .part file, resuming any
// existing partial download. The destination is replaced only after the body
// downloads fully and matches wantSHA; interrupted downloads never corrupt a
// valid file and leave a resumable .part behind.
func downloadFile(url, destination, wantSHA string) error {
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

	switch resp.StatusCode {
	case http.StatusOK:
		// A number of CDNs ignore Range requests. Appending a full response to
		// an existing partial file makes the checksum mismatch forever. Start
		// over safely when that happens; the known-good destination is still
		// untouched until this new .part verifies.
		if offset > 0 {
			if err := out.Truncate(0); err != nil {
				out.Close()
				return err
			}
			if _, err := out.Seek(0, io.SeekStart); err != nil {
				out.Close()
				return err
			}
		}
		if _, err := io.Copy(out, resp.Body); err != nil {
			out.Close()
			return fmt.Errorf("downloading %s: %w", url, err)
		}
	case http.StatusPartialContent:
		if _, err := io.Copy(out, resp.Body); err != nil {
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

	sum, err := sha256File(part)
	if err != nil {
		return err
	}
	if sum != wantSHA {
		// Do not preserve a known-corrupt prefix: a subsequent resume would
		// append to it and could never produce a valid model file.
		_ = os.Remove(part)
		return fmt.Errorf("checksum mismatch for %s: download is corrupt; re-run `sasayaki setup` to retry", filepath.Base(destination))
	}
	return nil
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

// downloadModel downloads every manifest file that is missing or corrupt and
// atomically renames the verified .part file into place. Skipped (already
// valid) files keep their original mtime.
func downloadModel(p config.Paths, modelID string, progress func(string)) (string, error) {
	var downloaded []string
	files := transcribe.ModelFiles(modelID)
	if len(files) == 0 {
		return "", fmt.Errorf("unknown speech model %q", modelID)
	}
	for _, f := range files {
		dest := filepath.Join(p.ModelDir(), f.Name)
		if fileValid(dest, f.SHA256) {
			continue
		}
		progress("Downloading " + f.Name + "…")
		if err := downloadFile(transcribe.Model.Source+f.Name, dest, f.SHA256); err != nil {
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
