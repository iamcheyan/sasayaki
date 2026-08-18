//go:build linux

package setup

import (
	"fmt"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// prerequisiteChecks are the diagnostics checks that must pass before setup
// writes anything. Socket/model/runtime checks are excluded: their absence
// is exactly what setup repairs. Names match diagnostics.Check.Name.
var prerequisiteChecks = []string{"python3", "parecord", "microphone", "clipboard", "paste backend", "systemd user session"}

// pythonInstallHint completes the "python3 is required but not installed"
// setup error on this platform.
const pythonInstallHint = "install it (e.g. dnf install python3 / apt install python3) and re-run `sasayaki setup`"

// checkPrereqs verifies tools, disk space and (when the model is not yet
// installed) network reachability. It never changes anything.
func checkPrereqs(p config.Paths) error {
	report := diagnose(p)
	var problems []string
	for _, check := range report.Checks {
		if check.OK || !contains(prerequisiteChecks, check.Name) {
			continue
		}
		problems = append(problems, check.Name+": "+check.Fix)
	}
	if len(problems) > 0 {
		return fmt.Errorf("missing prerequisites — nothing was changed; install them and re-run `sasayaki setup`:\n  - %s",
			strings.Join(problems, "\n  - "))
	}
	return checkDiskAndNetwork(p)
}
