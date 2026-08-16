// Command sasayaki is the single control binary: TUI, setup, service
// lifecycle, and the short-lived toggle client the desktop shortcut binds.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"

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
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		// journald already stamps every line with a timestamp; drop slog's
		// own so a line reads "level=INFO msg=…" instead of a duplicated
		// time=… beside the journalctl prefix.
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))

	args := os.Args[1:]
	if len(args) == 0 {
		if !isatty.IsTerminal(os.Stdin.Fd()) {
			// Started from a GUI (desktop button, launcher, file manager)
			// where stdin is not a TTY: open the TUI in a terminal
			// emulator instead of failing. No desktop component is needed.
			if err := launchInTerminal(os.Args[0]); err != nil {
				fail(err)
			}
			return
		}
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
	case "translate-toggle":
		fail(runTranslateToggle(paths))
	case "cancel":
		fail(runCancel(paths))
	case "bindings":
		runBindings(paths)
	case "wake":
		wakeKey, wakeArg := "", ""
		if len(args) > 1 {
			wakeKey = args[1]
		}
		if len(args) > 2 {
			wakeArg = args[2]
		}
		os.Exit(runWake(paths, wakeKey, wakeArg))
	case "capslock":
		// Compatibility alias: `sasayaki capslock …` === `sasayaki wake capslock …`
		os.Exit(runWake(paths, "capslock", firstArg(args)))
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

// launchInTerminal re-executes the TUI inside a terminal emulator so the
// control center opens when sasayaki is launched from a GUI, where stdin is
// not a TTY. It tries $TERMINAL, xdg-terminal-exec, then common emulators.
// This keeps the control center usable on any desktop without Sumika.
func launchInTerminal(bin string) error {
	if os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DISPLAY") == "" {
		return errors.New("no display and no terminal; run sasayaki from a terminal")
	}
	if t := os.Getenv("TERMINAL"); t != "" {
		if args, ok := terminalArgs(t, bin); ok {
			return startDetached(t, args)
		}
	}
	// UWSM app launch: gives the terminal a dedicated app-id that window
	// managers match on (floating rules for the TUI). Generic Wayland tool,
	// present on any uwsm session — not Sumika-specific.
	if _, err := exec.LookPath("uwsm-app"); err == nil {
		if x, err := exec.LookPath("xdg-terminal-exec"); err == nil {
			if err := startDetached("setsid", []string{"uwsm-app", "--", x,
				"--app-id=io.github.iamcheyan.sasayaki", "-e", bin}); err == nil {
				return nil
			}
		}
	}
	if p, err := exec.LookPath("xdg-terminal-exec"); err == nil {
		if err := startDetached(p, []string{"--app-id=io.github.iamcheyan.sasayaki", "-e", bin}); err == nil {
			return nil
		}
	}
	for _, t := range []string{"kitty", "foot", "alacritty", "ghostty", "wezterm", "konsole", "xterm"} {
		if p, err := exec.LookPath(t); err == nil {
			if args, ok := terminalArgs(t, bin); ok {
				return startDetached(p, args)
			}
		}
	}
	return errors.New("no terminal emulator found; run sasayaki from a terminal")
}

// terminalArgs returns the argv that makes terminal t run bin, and whether t
// is a terminal we know how to launch.
func terminalArgs(t, bin string) ([]string, bool) {
	switch t {
	case "gnome-terminal":
		return []string{"--", bin}, true
	default:
		// kitty, foot, alacritty, ghostty, wezterm, konsole, xterm,
		// xfce4-terminal, x-terminal-emulator, … all take -e.
		return []string{"-e", bin}, true
	}
}

// startDetached launches bin and detaches: the emulator outlives this
// short-lived launcher process.
func startDetached(bin string, args []string) error {
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	cmd.Process.Release()
	return nil
}

func runServe(paths config.Paths, log *slog.Logger) error {
	daemon, err := service.New(paths, log)
	if err != nil {
		return err
	}
	return daemon.Run()
}

func resolveBinary() string {
	binary, err := os.Executable()
	if err == nil && !strings.HasPrefix(binary, os.TempDir()) && !strings.Contains(binary, "/go-build") && !strings.Contains(binary, "/scratch") {
		return binary
	}
	if p, err := exec.LookPath("sasayaki"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin", "sasayaki")
}

func runSetup(paths config.Paths) error {
	setup.SetBinary(resolveBinary())
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
		fmt.Fprintln(os.Stderr, "sasayaki: setup did not complete; fix the failing step and re-run `sasayaki setup`.")
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
	if err := repairDesktopIntegration(); err != nil {
		return err
	}
	report := diagnostics.All(paths)
	for _, check := range report.Checks {
		if !check.OK {
			return fmt.Errorf("repair completed but %s still needs attention: %s", check.Name, check.Detail)
		}
	}
	fmt.Println("Local runtime, model, service, desktop bindings and Sumika integration are ready.")
	return nil
}

// repairDesktopIntegration reapplies the desktop pieces that live outside
// Sasayaki's private paths. It is best-effort across desktops: Hyprland and
// Sumika are refreshed when present, while standalone installations simply
// skip those integration steps.
func repairDesktopIntegration() error {
	if err := reloadHyprlandBindings(); err != nil {
		return err
	}

	if restart, err := exec.LookPath("sumika-restart"); err == nil {
		if output, err := exec.Command(restart, "--quickshell-only").CombinedOutput(); err != nil {
			return fmt.Errorf("could not restart Quickshell: %w: %s", err, strings.TrimSpace(string(output)))
		}
		fmt.Println("  [ok ] Restarted Quickshell integration")
	} else {
		fmt.Println("  [-- ] Sumika Shell not found; skipped Quickshell restart")
	}
	return nil
}

// reloadHyprlandBindings re-runs the Hyprland config (which regenerates the
// binds from `sasayaki bindings`) only when a live Hyprland instance exists.
// hyprctl may be installed while the session runs labwc/sway (no instance),
// in which case the reload is skipped silently.
func reloadHyprlandBindings() error {
	if sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); sig == "" {
		return nil
	}
	hyprctl, err := exec.LookPath("hyprctl")
	if err != nil {
		return nil
	}
	if output, err := exec.Command(hyprctl, "reload").CombinedOutput(); err != nil {
		return fmt.Errorf("could not reload Hyprland bindings: %w: %s", err, strings.TrimSpace(string(output)))
	}
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

func runTranslateToggle(paths config.Paths) error {
	response, err := service.Request(paths, "translate-toggle")
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

func runCancel(paths config.Paths) error {
	response, err := service.Request(paths, "cancel")
	if err != nil {
		return err
	}
	if response.Message != "" {
		fmt.Println(response.Message)
	}
	return nil
}

// runBindings prints the configured Hyprland keybindings in the
// tab-delimited "kind<TAB>binding" format consumed by the Sumika Shell
// Hyprland bindings generator. Voice bindings trigger sasayaki.toggle;
// the translation binding triggers sasayaki.translate-toggle.
func runBindings(paths config.Paths) {
	cfg, err := config.Load(paths)
	if err != nil {
		// Fall back to defaults on config error so keybinds still work.
		cfg = config.Default()
	}
	// No voice binding by default: ALT+A conflicted with other setups, so
// users opt in via voice_bindings in config.json (wake keys still emit
// voicetap lines independently).
	for _, b := range cfg.VoiceBindings {
		if strings.TrimSpace(b) == "" {
			continue
		}
		fmt.Printf("voice\t%s\n", strings.TrimSpace(b))
	}
	tb := strings.TrimSpace(cfg.TranslationBinding)
	if tb == "" {
		tb = config.DefaultTranslationBinding
	}
	if tb != "" {
		fmt.Printf("translation\t%s\n", tb)
	}
	// Tap bindings for enabled wake keys, emitted as their own kind so the
	// Sumika bindings generator registers them with release-only semantics
	// (chords like Ctrl+C stay untouched). A key only wakes on a completed
	// bare tap: press + release with no other key in between.
	//   code:66 — physical caps keycode: unswapped caps position, and the
	//   bottom-left key when ctrl-caps-swap moves the role there. Survives
	//   XKB keysym remaps such as compose:caps.
	//   F24 — emitted by keyd overload(control, f24) on the caps position
	//   while the swap preset is active (hold = Ctrl, tap = F24).
	//   code:37 — physical left Ctrl keycode. Transparent: chords still
	//   reach clients; only the bare tap fires the bind.
	//   code:105 — physical right Ctrl keycode, same semantics.
	if cfg.WakeKeys.CapsLock {
		fmt.Printf("voicetap\tcode:66\n")
		fmt.Printf("voicetap\tF24\n")
	}
	if cfg.WakeKeys.LeftCtrl {
		fmt.Printf("voicetap\tcode:37\n")
	}
	if cfg.WakeKeys.RightCtrl {
		fmt.Printf("voicetap\tcode:105\n")
	}
}

// runWake implements `sasayaki wake <key> <on|off|toggle|status>` — the
// per-key wake matrix behind the voicetap bindings. Mutations persist and
// refresh the live Hyprland binds (and the keyd overload for the caps
// position) so the change is effective immediately. `sasayaki capslock …`
// stays as a compatibility alias for `sasayaki wake capslock …`.
func runWake(paths config.Paths, key, arg string) int {
	cfg, err := config.Load(paths)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sasayaki:", err)
		return exitError
	}
	// `sasayaki wake status` (no key): report every wake key, one per line,
	// so UIs can fetch the whole matrix with one call. "status" arrives in
	// the key slot when the user omits the key entirely.
	if key == "" || key == "status" {
		for _, k := range []struct {
			name string
			on   bool
		}{{"capslock", cfg.WakeKeys.CapsLock}, {"leftctrl", cfg.WakeKeys.LeftCtrl}, {"rightctrl", cfg.WakeKeys.RightCtrl}} {
			state := "off"
			if k.on {
				state = "on"
			}
			fmt.Printf("%s %s\n", k.name, state)
		}
		return exitOK
	}
	var field *bool
	keyLabel := key
	switch key {
	case "caps", "capslock":
		field = &cfg.WakeKeys.CapsLock
		keyLabel = "CapsLock"
	case "lctrl", "leftctrl":
		field = &cfg.WakeKeys.LeftCtrl
		keyLabel = "LeftCtrl"
	case "rctrl", "rightctrl":
		field = &cfg.WakeKeys.RightCtrl
		keyLabel = "RightCtrl"
	default:
		fmt.Fprintln(os.Stderr, "sasayaki: unknown wake key:", key,
			"(use capslock | leftctrl | rightctrl)")
		return exitUsage
	}
	state := func() string {
		if *field {
			return "on"
		}
		return "off"
	}
	switch arg {
	case "on", "off":
		*field = arg == "on"
	case "toggle":
		*field = !*field
	default: // status, ""
		fmt.Println(state())
		return exitOK
	}
	if err := config.Save(paths, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "sasayaki:", err)
		return exitError
	}
	reloadHyprlandBindings()
	// The keyd side must be re-rendered too: the ctrl-caps-swap preset
	// turns the caps position into overload(control, f24) only while the
	// capslock wake is on. Best-effort — without keyboard-remap the plain
	// swap stays and the code:66 binding still covers the unswapped case.
	if keyLabel == "CapsLock" {
		applyKeyboardRemap()
	}
	fmt.Printf("%s wake: %s\n", keyLabel, state())
	return exitOK
}

// applyKeyboardRemap re-renders the keyd config so the caps-wake overload
// appears or disappears immediately. Silently skipped when the Sumika
// keyboard-remap extension is not installed.
func applyKeyboardRemap() {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(os.Getenv("HOME"), ".local", "share")
	}
	extDir := os.Getenv("SUMIKA_SHELL_EXTENSIONS_DIR")
	if extDir == "" {
		extDir = filepath.Join(dataHome, "sumika-shell", "extensions")
	}
	apply := filepath.Join(extDir, "keyboard-remap", "bin", "omarchy-keyboard-apply")
	if _, err := os.Stat(apply); err != nil {
		return
	}
	_ = exec.Command(apply).Run()
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
  sasayaki translate-toggle     Record and translate (requires translation enabled)
  sasayaki cancel               Cancel active recording or transcription
  sasayaki bindings             Print Hyprland keybindings for desktop integration
  sasayaki wake capslock|leftctrl|rightctrl on|off|toggle|status
                              Voice wake by tapping that key alone (chord-safe;
                              combinations freely selectable, all may be off)
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

func firstArg(args []string) string {
	if len(args) > 1 {
		return args[1]
	}
	return ""
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "sasayaki:", strings.TrimSpace(err.Error()))
		os.Exit(exitError)
	}
}
