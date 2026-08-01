# Sasayaki

Sasayaki is a small, standalone voice-input application for Linux. It records
audio, transcribes it locally with SenseVoice, and pastes the result into the
focused application.

It is intentionally independent of Sumika, Omarchy, and any one desktop
environment. One binary owns its configuration, model runtime, user service,
and desktop-shortcut guidance.

## What it needs

- Linux with PipeWire/PulseAudio recording support (`parecord`)
- Python 3, used privately for the local `sherpa-onnx` inference runtime
- `wl-copy` and a paste backend (`wtype` is preferred on Wayland)
- systemd user services (for the background recorder/transcriber)

The first-run setup installs Python packages in Sasayaki's own data directory
and downloads the SenseVoice model. It does not modify an existing Python
installation.

## Quick start

```sh
sasayaki setup
sasayaki
```

Then bind the following command in KDE, GNOME, Hyprland, Sway, or another
desktop's keyboard-shortcut settings:

```sh
sasayaki toggle
```

The shortcut is toggle-to-record: press once to start recording and again to
transcribe and paste. This works consistently across desktop environments;
portal-native push-to-talk can be added later where supported.

## Commands

```text
sasayaki                 Open the control center
sasayaki setup           Install the private runtime, model and user service
sasayaki toggle          Start/stop recording through the running service
sasayaki status          Print service and runtime status
sasayaki service start   Start the background service
sasayaki service stop    Stop the background service
sasayaki shortcut        Show desktop-specific shortcut instructions
sasayaki logs            Follow the user-service log
```

## Files

All application data is owned by Sasayaki:

```text
~/.config/sasayaki/config.json                 preferences
~/.local/share/sasayaki/models/sensevoice/     downloaded model
~/.local/share/sasayaki/runtime/venv/          private Python environment
~/.local/state/sasayaki/                       recordings and logs
~/.config/systemd/user/sasayaki.service        user service
$XDG_RUNTIME_DIR/sasayaki/sasayaki.sock        control socket
```

## Development

```sh
go test ./...
go run ./cmd/sasayaki
```

The visual language is documented in
[docs/tui-design-language.md](docs/tui-design-language.md).
