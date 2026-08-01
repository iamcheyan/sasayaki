// Command sasayaki is the single control binary: TUI, setup, service
// lifecycle, and the short-lived toggle client the desktop shortcut binds.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/diagnostics"
	"github.com/iamcheyan/sasayaki/internal/service"
	"github.com/iamcheyan/sasayaki/internal/setup"
	"github.com/iamcheyan/sasayaki/internal/transcribe"
	"github.com/iamcheyan/sasayaki/internal/translate"
	"github.com/iamcheyan/sasayaki/internal/tui"
)

// Exit codes are predictable for integrations.
const (
	exitOK    = 0
	exitError = 1 // operational failure (service error, setup failure)
	exitUsage = 2 // bad command line
)

func main() {
	paths := config.NewPaths()
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	args := os.Args[1:]
	if len(args) == 0 {
		if err := tui.Run(paths); err != nil {
			fail(err)
		}
		return
	}

	switch args[0] {
	case "serve":
		fail(runServe(paths, log))
	case "setup":
		fail(runSetup(paths))
	case "repair":
		fail(runRepair(paths))
	case "toggle":
		fail(runToggle(paths))
	case "status":
		code := runStatus(paths, hasJSONFlag(args[1:]))
		os.Exit(code)
	case "diagnose":
		code := runDiagnose(paths, hasJSONFlag(args[1:]))
		os.Exit(code)
	case "models":
		os.Exit(runModels(paths, args[1:]))
	case "translation":
		os.Exit(runTranslation(paths, args[1:]))
	case "service":
		os.Exit(runServiceCommand(args[1:]))
	case "shortcut":
		fmt.Print(shortcutHelp())
	case "logs":
		runLogs()
	case "help", "--help", "-h":
		fmt.Print(help())
	default:
		fmt.Fprintln(os.Stderr, "sasayaki: unknown command:", args[0])
		fmt.Fprint(os.Stderr, help())
		os.Exit(exitUsage)
	}
}

func runServe(paths config.Paths, log *slog.Logger) error {
	daemon, err := service.New(paths, log)
	if err != nil {
		return err
	}
	return daemon.Run()
}

func runSetup(paths config.Paths) error {
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate the sasayaki binary: %w", err)
	}
	setup.SetBinary(binary)
	setup.SetProgress(func(message string) { fmt.Println("  " + message) })
	session := setup.NewSession(paths)
	result := session.Run()
	for _, step := range result.Steps {
		mark := map[setup.StepStatus]string{setup.StepDone: "ok ", setup.StepSkipped: "-- ", setup.StepFailed: "!! "}[step.Status]
		fmt.Printf("  [%s] %s\n", mark, step.Title)
		if step.Detail != "" {
			fmt.Printf("        %s\n", step.Detail)
		}
		if step.Error != "" {
			fmt.Printf("        %s\n", step.Error)
		}
	}
	if !result.AllOK() {
		fmt.Fprintln(os.Stderr, "sasayaki: setup did not complete; fix the failing step and re-run `sasayaki setup`. No partial state was corrupted.")
		return fmt.Errorf("setup failed at step %s", strings.Join(result.Failed, ", "))
	}
	fmt.Println("Sasayaki is ready. Bind `sasayaki toggle` in your desktop settings.")
	return nil
}

func runRepair(paths config.Paths) error {
	fmt.Println("Checking and repairing Sasayaki…")
	if err := runSetup(paths); err != nil {
		return err
	}
	report := diagnostics.All(paths)
	for _, check := range report.Checks {
		if !check.OK {
			return fmt.Errorf("repair completed but %s still needs attention: %s", check.Name, check.Detail)
		}
	}
	fmt.Println("Local runtime, model, service and desktop tools are ready.")
	return nil
}

func runToggle(paths config.Paths) error {
	response, err := service.Request(paths, "toggle")
	if err != nil {
		return err
	}
	if response.Message != "" {
		fmt.Println(response.Message)
	}
	if !response.OK && response.Error != nil {
		return fmt.Errorf("%s", response.Error.Detail)
	}
	return nil
}

func runStatus(paths config.Paths, asJSON bool) int {
	response, err := service.Request(paths, "status")
	if err != nil {
		fmt.Fprintln(os.Stderr, "sasayaki:", err)
		return exitError
	}
	state := response.State
	if asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(state)
		return exitOK
	}
	fmt.Printf("service:   %s\n", state.Service)
	fmt.Printf("phase:     %s\n", state.Phase)
	fmt.Printf("runtime:   %t\n", state.Runtime)
	fmt.Printf("model:     %t\n", state.Model)
	fmt.Printf("microphone:%t\n", state.Microphone)
	fmt.Printf("paste:     %t (%s)\n", state.Paste, state.PasteBackend)
	fmt.Printf("worker:    %s\n", state.Worker)
	if state.LastPhase != "" {
		fmt.Printf("last:      %s (%s)\n", state.LastPhase, state.LastAt)
		if state.LastError != "" {
			fmt.Printf("           error: %s\n", state.LastError)
		} else if state.LastResult != "" {
			fmt.Printf("           text: %q\n", state.LastResult)
		}
	}
	return exitOK
}

func runDiagnose(paths config.Paths, asJSON bool) int {
	report := diagnostics.All(paths)
	if asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(report)
	} else {
		for _, check := range report.Checks {
			mark := "ok "
			if !check.OK {
				mark = "!! "
			}
			fmt.Printf("  [%s] %-18s %s\n", mark, check.Name, check.Detail)
			if !check.OK && check.Fix != "" {
				fmt.Printf("        fix: %s\n", check.Fix)
			}
		}
		if len(report.Model) > 0 {
			fmt.Printf("  [!!] speech model:\n")
			for _, problem := range report.Model {
				fmt.Printf("        - %s\n", problem)
			}
		}
	}
	for _, check := range report.Checks {
		if !check.OK {
			return exitError
		}
	}
	return exitOK
}

func runModels(paths config.Paths, args []string) int {
	cfg, err := config.Load(paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sasayaki:", err)
		return exitError
	}
	if len(args) == 2 && (args[0] == "select" || args[0] == "download") {
		selected, ok := transcribe.SpeechModelByID(args[1])
		if !ok {
			fmt.Fprintln(os.Stderr, "sasayaki: unknown speech model:", args[1])
			return exitUsage
		}
		cfg.SpeechModel = selected.ID
		if err := config.Save(paths, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "sasayaki:", err)
			return exitError
		}
		if args[0] == "download" {
			fmt.Printf("Selected %s. Preparing its private runtime and model files…\n", selected.Label)
			if err := runSetup(paths); err != nil {
				return exitError
			}
			return exitOK
		}
		fmt.Printf("Selected %s. Run `sasayaki models download %s` (or `sasayaki setup`) to download and activate it.\n", selected.Label, selected.ID)
		return exitOK
	}
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "sasayaki: usage: sasayaki models [select|download <id>]")
		return exitUsage
	}
	for _, model := range transcribe.SpeechModels {
		mark := " "
		if model.ID == cfg.SpeechModel {
			mark = "*"
		}
		installed := transcribe.ModelValidFor(paths, model.ID)
		state := "not installed"
		if installed {
			state = "installed"
		}
		fmt.Printf("%s %-22s %-46s %-10s %s\n", mark, model.ID, model.Label, model.Architecture, state)
	}
	return exitOK
}

func runTranslation(paths config.Paths, args []string) int {
	cfg, err := config.Load(paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sasayaki:", err)
		return exitError
	}
	if len(args) == 0 || (len(args) == 1 && args[0] == "status") {
		t := cfg.Translation
		fmt.Printf("translation: %t\nendpoint: %s\nmodel: %s\ntarget: %s\napi key: %t\n", t.Enabled, t.BaseURL, t.Model, t.TargetLanguage, t.APIKey != "")
		return exitOK
	}
	if len(args) == 1 && args[0] == "disable" {
		cfg.Translation.Enabled = false
		if err := config.Save(paths, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "sasayaki:", err)
			return exitError
		}
		fmt.Println("Translation disabled.")
		return exitOK
	}
	if len(args) >= 1 && args[0] == "test" {
		text := "Hello from Sasayaki"
		if len(args) > 1 {
			text = strings.Join(args[1:], " ")
		}
		out, err := translate.Translate(context.Background(), cfg.Translation, text)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sasayaki: translation test failed:", err)
			return exitError
		}
		fmt.Println(out)
		return exitOK
	}
	if len(args) > 0 && args[0] == "configure" {
		flags := flag.NewFlagSet("translation configure", flag.ContinueOnError)
		baseURL := flags.String("base-url", cfg.Translation.BaseURL, "OpenAI-compatible base URL")
		model := flags.String("model", cfg.Translation.Model, "translation model")
		target := flags.String("target", cfg.Translation.TargetLanguage, "target language")
		key := flags.String("api-key", cfg.Translation.APIKey, "API key (stored in Sasayaki private config)")
		enabled := flags.Bool("enabled", true, "enable translation")
		if err := flags.Parse(args[1:]); err != nil {
			return exitUsage
		}
		cfg.Translation = config.TranslationConfig{Enabled: *enabled, BaseURL: *baseURL, Model: *model, TargetLanguage: *target, APIKey: *key}
		if err := config.Save(paths, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "sasayaki:", err)
			return exitError
		}
		fmt.Println("Translation configuration saved. New recordings will translate before pasting.")
		return exitOK
	}
	fmt.Fprintln(os.Stderr, "sasayaki: usage: sasayaki translation [status|disable|configure --base-url URL --model ID --target LANGUAGE --api-key KEY]")
	return exitUsage
}

func runServiceCommand(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "sasayaki: usage: sasayaki service <start|stop|restart|status>")
		return exitUsage
	}
	switch args[0] {
	case "start", "stop", "restart":
		if err := service.Systemctl(args[0], "sasayaki.service"); err != nil {
			fmt.Fprintln(os.Stderr, "sasayaki:", err)
			return exitError
		}
		return exitOK
	case "status":
		if service.IsActive() {
			fmt.Println("sasayaki.service: active")
			return exitOK
		}
		fmt.Fprintln(os.Stderr, "sasayaki.service: inactive (run `sasayaki service start` or `sasayaki setup`)")
		return exitError
	}
	fmt.Fprintln(os.Stderr, "sasayaki: usage: sasayaki service <start|stop|restart|status>")
	return exitUsage
}

func runLogs() {
	cmd := exec.Command("journalctl", "--user", "-u", "sasayaki.service", "-f", "--no-pager")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fail(err)
	}
}

func shortcutHelp() string {
	return `Sasayaki global shortcut

Bind this command as a keyboard shortcut in your desktop settings:

    sasayaki toggle

Press it once to start recording, again to stop, transcribe and paste.

KDE:        System Settings → Shortcuts → Add Command
GNOME:      Settings → Keyboard → View and Customize Shortcuts → Custom Shortcuts
Hyprland:   bind = SUPER, V, exec, sasayaki toggle
Sway:       bindsym $mod+v exec sasayaki toggle
`
}

func help() string {
	return `Sasayaki — local voice input

Usage:
  sasayaki                      Open the control center
  sasayaki setup                Install/repair the selected local runtime, model and service
  sasayaki repair               Re-run checks and repair local components
  sasayaki toggle               Start or finish voice input (desktop shortcut)
  sasayaki status [--json]      Show service and readiness state
  sasayaki diagnose [--json]    Full dependency and capability report
  sasayaki models [select|download ID]
                              List, select, or download a local speech model
  sasayaki translation …       Configure/test optional online translation
  sasayaki service start|stop|restart|status
  sasayaki shortcut             Show desktop shortcut instructions
  sasayaki logs                 Follow the user-service log
  sasayaki help                 Show this help

Exit codes: 0 success, 1 operational failure, 2 usage error.
All speech stays on this machine.

To remove Sasayaki completely:
  systemctl --user disable --now sasayaki.service
  rm ~/.config/systemd/user/sasayaki.service
  rm -rf ~/.config/sasayaki ~/.local/share/sasayaki ~/.local/state/sasayaki
  systemctl --user daemon-reload
`
}

func hasJSONFlag(args []string) bool {
	flags := flag.NewFlagSet("", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "machine-readable output")
	_ = flags.Parse(args)
	return *asJSON
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "sasayaki:", strings.TrimSpace(err.Error()))
		os.Exit(exitError)
	}
}
