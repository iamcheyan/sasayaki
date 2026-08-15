package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// PinnedRuntime is the exact package set installed into Sasayaki's private
// virtualenv. Versions are fixed so a setup run is reproducible; bump them
// deliberately with a documented reason.
const PinnedRuntime = `numpy==2.5.1
sherpa-onnx==1.13.4
`

// writeRequirements installs the pinned requirement file.
func writeRequirements(p config.Paths) error {
	if err := os.MkdirAll(p.RuntimeDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p.RequirementsFile(), []byte(PinnedRuntime), 0o600)
}

// unitTemplate renders the systemd user unit. The service runs the actual
// installed binary with `sasayaki serve`; Restart=on-failure makes systemd
// resurrect a crashed daemon.
const unitTemplate = `[Unit]
Description=Sasayaki local voice input
After=graphical-session.target

[Service]
Type=simple
# systemd user services may not inherit the desktop shell PATH. Keep the
# external audio, clipboard, and input tools discoverable for the daemon.
Environment=PATH=%s
%sExecStart=%s serve
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`

// serviceEnvironment discovers common NixOS paths without making the unit
// depend on a machine-specific /nix/store hash. On other distributions the
// extra paths simply do not exist and are omitted. The Nix C++ runtime is
// needed by sherpa-onnx wheels, while parecord and wl-copy live in the system
// or user profile.
func serviceEnvironment(home string) (string, string) {
	pathParts := []string{"/run/current-system/sw/bin", filepath.Join(home, ".nix-profile/bin"), "/nix/profile/bin", filepath.Join(home, ".local/bin"), "/usr/local/bin", "/usr/bin", "/bin"}
	libParts := []string{"/run/current-system/sw/lib", filepath.Join(home, ".nix-profile/lib")}
	for _, pattern := range []string{"/nix/store/*-gcc-*-lib/lib", "/nix/store/*-gcc-*/lib"} {
		matches, _ := filepath.Glob(pattern)
		libParts = append(libParts, matches...)
	}
	pathValue := strings.Join(pathParts, ":")
	var ld string
	seen := map[string]bool{}
	for _, p := range libParts {
		if _, err := os.Stat(p); err == nil && !seen[p] {
			seen[p] = true
			if ld != "" {
				ld += ":"
			}
			ld += p
		}
	}
	ldLine := ""
	if ld != "" {
		ldLine = "Environment=LD_LIBRARY_PATH=" + ld + "\n"
	}
	return pathValue, ldLine
}

// writeUnit writes the user unit when it does not already target the given
// binary.
func writeUnit(p config.Paths, binary string) error {
	if binary == "" {
		return fmt.Errorf("sasayaki binary path is unknown; install sasayaki into PATH and re-run setup")
	}
	dir := filepath.Dir(p.ServiceFile())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pathValue, ldLine := serviceEnvironment(os.Getenv("HOME"))
	return os.WriteFile(p.ServiceFile(), []byte(fmt.Sprintf(unitTemplate, pathValue, ldLine, binary)), 0o600)
}

// writeEngine installs the embedded engine.py into the runtime directory.
func writeEngine(p config.Paths) error {
	dir := filepath.Dir(p.EngineScript())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writeEmbeddedEngine(p.EngineScript())
}

// writeEmbeddedEngine is replaced by the real embed writer at init time;
// the engine package owns the script bytes.
var writeEmbeddedEngine = func(path string) error {
	return fmt.Errorf("engine script writer not wired")
}

// run executes a command with argv and returns its combined output on error.
// It is a var so tests can stub external commands (venv creation, pip).
var run = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// lookPath is a thin wrapper so tests can stub PATH lookups.
var lookPath = func(name string) (string, error) { return exec.LookPath(name) }

// systemctl executes systemctl --user.
var systemctl = func(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}
