//go:build darwin

package service

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/iamcheyan/sasayaki/internal/config"
)

// guiDomain is the per-user launchd domain that owns the LaunchAgent.
func guiDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

// agentTarget is the launchd service target kickstart and bootout address.
func agentTarget() string { return guiDomain() + "/" + config.LaunchAgentLabel }

// launchctl runs one launchctl command. It is a var so tests can capture the
// translated argv without touching the real service manager.
var launchctl = func(args ...string) error {
	cmd := exec.Command("launchctl", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// loadAgent (re)loads the LaunchAgent and starts it. launchd only reads the
// plist at bootstrap time: kickstart -k restarts the process but keeps the
// OLD environment, so a rewritten plist (new binary, new PATH) is never
// picked up. Reload therefore boots an already-loaded agent out so the
// fresh plist is the one that lands. RunAtLoad starts the process; the
// trailing kickstart is deliberately NOT issued — it would SIGTERM the
// freshly-booted agent (the -15 exit codes observed in launchctl list) for
// no benefit.
func loadAgent() error {
	plist := config.NewPaths().ServiceFile()
	gui := guiDomain()
	if err := launchctl("bootstrap", gui, plist); err != nil {
		// "Bootstrap failed: 5: Input/output error" is launchd's
		// already-loaded reply. Cycle the agent so the CURRENT plist is
		// read. launchd deregisters the bootout asynchronously, so the
		// immediate re-bootstrap races with it; retry briefly. A bootout
		// failing because nothing is loaded is fine — the retried
		// bootstrap failure is what the caller sees.
		_ = launchctl("bootout", agentTarget())
		var lastErr error
		for range 5 {
			time.Sleep(200 * time.Millisecond)
			lastErr = launchctl("bootstrap", gui, plist)
			if lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			return lastErr
		}
	}
	return nil
}

// Systemctl translates the systemctl invocations the codebase makes onto
// launchd. Sasayaki owns a single user agent, so the unit operand is ignored
// and the verbs map as follows:
//
//	enable --now <unit>, restart <unit>, start <unit> → loadAgent
//	disable --now <unit>, stop <unit>                 → bootout
//	is-active --quiet <unit>                          → probe the control socket
//	daemon-reload                                     → nothing (launchd has no caches)
func Systemctl(args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("systemctl: no arguments")
	}
	switch {
	case len(args) >= 2 && args[0] == "enable" && args[1] == "--now",
		len(args) >= 2 && (args[0] == "restart" || args[0] == "start"):
		return loadAgent()
	case len(args) >= 2 && args[0] == "disable" && args[1] == "--now",
		len(args) >= 2 && args[0] == "stop":
		if err := launchctl("bootout", agentTarget()); err != nil && agentLoaded() {
			return err
		}
		return nil
	case len(args) >= 2 && args[0] == "is-active" && args[1] == "--quiet":
		if IsActive() {
			return nil
		}
		return fmt.Errorf("systemctl %s: service is not active", strings.Join(args, " "))
	case args[0] == "daemon-reload":
		return nil
	}
	return fmt.Errorf("systemctl %s: no launchctl translation", strings.Join(args, " "))
}

// agentLoaded reports whether launchd still knows the agent; booting out an
// agent that is not loaded must not be an error.
func agentLoaded() bool { return launchctl("print", agentTarget()) == nil }

// IsActive reports whether the daemon answers on its control socket. launchd
// may be between restarts (KeepAlive), so the socket is the source of truth.
func IsActive() bool {
	conn, err := net.DialTimeout("unix", config.NewPaths().Socket(), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
