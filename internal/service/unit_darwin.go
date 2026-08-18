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

// loadAgent bootstraps the LaunchAgent and then (re)starts it. Bootstrapping
// an already-loaded agent is launchd's way of saying the agent is fine, so a
// bootstrap failure is only surfaced when the kickstart that follows fails
// too; kickstart -k starts a stopped agent and restarts a running one.
func loadAgent() error {
	bootErr := launchctl("bootstrap", guiDomain(), config.NewPaths().ServiceFile())
	if err := launchctl("kickstart", "-k", agentTarget()); err != nil {
		if bootErr != nil {
			return fmt.Errorf("%w; %v", err, bootErr)
		}
		return err
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
