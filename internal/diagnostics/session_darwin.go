//go:build darwin

package diagnostics

// pythonCheck: python3 is required to run the private engine venv.
func pythonCheck(r Runner) Check {
	if path, err := r.LookPath("python3"); err == nil {
		return Check{Name: "python3", OK: true, Detail: "python3 found at " + path}
	}
	return Check{Name: "python3", OK: false, Detail: "python3 not found", Fix: "Install the Xcode Command Line Tools (xcode-select --install)"}
}

// sessionCheck names the supervision check for this platform.
func sessionCheckName() string { return "launchd user session" }
