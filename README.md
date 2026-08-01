# Sasayaki

Sasayaki is a small, standalone voice-input application for Linux. It records
audio, transcribes it locally with a selectable offline engine, and pastes the result into the
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
and downloads the selected offline model. It does not modify an existing Python
installation.

## Quick start

```sh
sasayaki setup
sasayaki
```

To build and install from source:

```sh
make install
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
sasayaki status [--json] Print service and runtime state
sasayaki diagnose [--json]
                         Check prerequisites, runtime, model, microphone, paste
sasayaki service start   Start the background service
sasayaki service stop    Stop the background service
sasayaki service restart Restart the background service
sasayaki service status  Show whether the user service is running
sasayaki shortcut        Show desktop-specific shortcut instructions
sasayaki logs            Follow the user-service log
```

## Models, translation and repair

Sasayaki detects the selected local speech model before it records. Its model
catalog includes multilingual **SenseVoice** (fast int8 or quality-first full
precision) and Chinese-first **Paraformer**. The first is compatible with
Chinese, English, Japanese, Korean, and Cantonese; Paraformer is a separate
recognizer backend, not merely a precision variant.

Choose and download a model in one command (or select it first and let Setup
download it later):

```sh
sasayaki models
sasayaki models download paraformer-zh-int8
# or: sasayaki models select sensevoice-full && sasayaki setup
```

Models are pinned by SHA-256 and kept in separate private directories. The
ONNX files work on both x86_64 and ARM64; the private `sherpa-onnx` Python
runtime is installed as the wheel appropriate to the user's CPU architecture.

Optional translation runs only after local speech recognition. It works with
an OpenAI-compatible endpoint and is completely independent of OpenCode or
the desktop environment:

```sh
sasayaki translation configure \
  --base-url https://example.com/v1 \
  --model your-fast-model \
  --target Japanese \
  --api-key "$YOUR_API_KEY"
sasayaki translation test "hello"
```

Run `sasayaki repair` to re-check local tools, repair the selected runtime and
model, and restart the user service. `sasayaki diagnose` is read-only and
lists every failed check with a remediation.

Exit codes are predictable for scripting: `0` success, `1` operational
failure (service down, setup incomplete, empty recording, …), `2` usage
error.

`status` and `diagnose` print JSON when given `--json`.

## First run

```sh
sasayaki setup
sasayaki
```

`setup` is transactional and idempotent: it checks prerequisites (python3,
`parecord`, `wl-copy`, `wtype`, systemd user session, microphone, disk space,
network), creates the private runtime, downloads and verifies the model
against pinned SHA-256 checksums, installs the user service and enables it.
Re-run it any time to repair a broken install (corrupt model files are
re-downloaded).

## Privacy

Everything runs on this machine. Audio never leaves the device: recording is
transcribed locally with SenseVoice and there are no network calls at
runtime. The service journal logs metadata and errors only — not
transcripts. To opt into full transcript logging, set `verbose_transcripts`
in `~/.config/sasayaki/config.json` to `true`.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `toggle` prints "service is not running" | `sasayaki service start`, or `sasayaki setup` on first run |
| `diagnose` shows a failed check | The `fix` field names the command; e.g. install `pulseaudio-utils` or start PipeWire/PulseAudio |
| "microphone produced an empty recording" | Check input device/level; run `pactl list short sources` |
| Model re-downloads on every setup | Corrupt model files; `sasayaki setup` repairs automatically |
| Korean text looks over-spaced | Known SenseVoice limitation; text content is correct |
| Nothing is pasted but clipboard was set | Paste backends unavailable (needs `wtype`/`ydotool`/`xdotool`); the failure state says so |

## Removing

```sh
systemctl --user disable --now sasayaki.service
rm ~/.config/systemd/user/sasayaki.service
rm -rf ~/.config/sasayaki ~/.local/share/sasayaki ~/.local/state/sasayaki
systemctl --user daemon-reload
```

This removes the config, model, private runtime and recordings.

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
The implementation boundary and desktop-integration decisions are in
[docs/architecture.md](docs/architecture.md). The speech model, checksums,
measured latency and license are in [docs/model.md](docs/model.md).

For a detailed engineering handoff/acceptance specification, see
[docs/model-implementation-brief.md](docs/model-implementation-brief.md).
