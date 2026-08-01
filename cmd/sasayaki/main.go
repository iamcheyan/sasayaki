package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/iamcheyan/sasayaki/internal/config"
	"github.com/iamcheyan/sasayaki/internal/service"
	"github.com/iamcheyan/sasayaki/internal/setup"
	"github.com/iamcheyan/sasayaki/internal/tui"
)

func main() {
	paths := config.NewPaths()
	args := os.Args[1:]
	if len(args) == 0 {
		fail(tui.Run(paths))
		return
	}
	switch args[0] {
	case "serve":
		d, err := service.New(paths)
		fail(err)
		fail(d.Run())
	case "setup":
		binary, err := os.Executable()
		fail(err)
		fail(setup.Run(paths, binary, func(message string) { fmt.Println(message) }))
	case "toggle", "status":
		r, err := service.Request(paths, args[0])
		fail(err)
		if r.Message != "" {
			fmt.Println(r.Message)
		}
		if args[0] == "status" && r.State != nil {
			fmt.Printf("service=%s recording=%t model=%t runtime=%t paste=%t\n", r.State.Service, r.State.Recording, r.State.Model, r.State.Runtime, r.State.Paste)
		}
		if !r.OK {
			os.Exit(1)
		}
	case "service":
		serviceCommand(args[1:])
	case "shortcut":
		fmt.Println(shortcutHelp())
	case "logs":
		cmd := exec.Command("journalctl", "--user", "-u", "sasayaki.service", "-f", "--no-pager")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		fail(cmd.Run())
	case "help", "--help", "-h":
		fmt.Print(help())
	default:
		fmt.Fprintln(os.Stderr, "Unknown command:", args[0])
		fmt.Print(help())
		os.Exit(2)
	}
}

func serviceCommand(args []string) {
	if len(args) != 1 || (args[0] != "start" && args[0] != "stop" && args[0] != "restart") {
		fmt.Fprintln(os.Stderr, "Usage: sasayaki service <start|stop|restart>")
		os.Exit(2)
	}
	fail(service.Systemctl(args[0], "sasayaki.service"))
}

func shortcutHelp() string {
	return "Bind this command as a global shortcut:\n\n  sasayaki toggle\n\nKDE: System Settings → Shortcuts → Add Command\nGNOME: Settings → Keyboard → View and Customize Shortcuts → Custom Shortcuts\nHyprland: bind = SUPER, V, exec, sasayaki toggle\nSway: bindsym $mod+v exec sasayaki toggle\n\nPress once to record; press again to transcribe and paste.\n"
}

func help() string {
	return "Sasayaki — local voice input\n\nUsage:\n  sasayaki                       Open the control center\n  sasayaki setup                 Install local runtime, model and service\n  sasayaki toggle                Start/finish voice input\n  sasayaki status                Show current readiness\n  sasayaki service start|stop|restart\n  sasayaki shortcut              Show desktop shortcut instructions\n  sasayaki logs                  Follow the service log\n"
}
func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "sasayaki:", strings.TrimSpace(err.Error()))
		os.Exit(1)
	}
}
