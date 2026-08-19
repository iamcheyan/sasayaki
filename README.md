# Sasayaki

Sasayaki is a standalone, Go-based voice input application for Linux and
macOS. It records audio, transcribes it locally with a selectable offline
engine, and pastes the result into the focused application — with an
optional online translation step in between.

It is fully independent of any desktop environment: one binary owns its
configuration, model runtime, user service, control center (TUI), and
desktop-shortcut guidance. When the [Sumika Shell](https://github.com/iamcheyan/oh-my-desktop)
desktop is present, Sasayaki additionally registers itself as a first-class
module (taskbar icon, popup, settings page, key bindings). The desktop
integration is an add-on; the program works identically without it.

## Features

- **Fully offline speech recognition** — audio never leaves the machine.
  Multilingual **SenseVoice** (Chinese, English, Japanese, Korean,
  Cantonese) and Chinese-first **Paraformer** backends, selectable in the
  control center.
- **Toggle-to-record** — press the hotkey once to start recording, again to
  transcribe and paste into the focused window. Press `Escape` to cancel.
  On macOS the menu-bar app binds **F13** (dictate) and **F14** (dictate +
  translate) as global hotkeys; on Linux bind `sasayaki toggle` /
  `sasayaki translate-toggle` in your desktop's shortcut settings.
- **Automatic paste** — copies the result to the clipboard and pastes it
  into the focused window. On Linux this is window-aware: it resolves the
  focused window (via `hyprctl` on Hyprland), uses kitty's native remote
  paste in kitty, sends the chord the app actually binds (`Ctrl+V` for GUI
  apps, `Shift+Insert`/`Ctrl+Shift+V` for terminals), and reaches XWayland
  windows through the X11 clipboard (`xsel`) plus `xdotool`. Backends fall
  back in order: `wtype` -> `ydotool` -> `hyprctl send_key_state` ->
  `xdotool`. On macOS the menu-bar app posts `Cmd+V` via CGEvent from its
  own process (a single Accessibility grant), avoiding the fragile
  osascript -> System Events chain.
- **Optional online translation** — after transcription, send the text to any
  OpenAI-compatible endpoint and paste the translated result. Bound to a
  dedicated hotkey; a plain `toggle` never translates.
- **Bubble Tea control center (TUI)** — keyboard-first management of models,
  translation, service, and diagnostics. Launched from a terminal, or
  automatically in a terminal emulator when started from a GUI.
- **Fast desktop feedback** — the taskbar icon flips to recording the instant
  the hotkey is pressed (optimistic UI), then confirms from the service.
- **Private by design** — model, runtime, and recordings live in Sasayaki's
  own XDG directories; nothing touches an existing Python install.
- **One binary** — a single `sasayaki` executable (with a private Python
  runtime for sherpa-onnx inference) that works on any Linux with PipeWire or
  PulseAudio, and on macOS 11+ via a menu-bar app bundle.

## What it needs

**Linux**

- PipeWire/PulseAudio recording support (`parecord`)
- Python 3, used privately for the local `sherpa-onnx` inference runtime
- `wl-copy` and a paste backend (`wtype` is preferred on Wayland; `ydotool`
  or `xdotool` also work); `xsel` extends pasting to XWayland programs
- systemd user services (for the background recorder/transcriber)

**macOS** (11+)

- The `Sasayaki.app` menu-bar bundle (built by `mac/build.sh`) — holds the
  TCC microphone grant and records via native `AVAudioEngine`.
- Python 3 (system Python 3.9+ is fine), used privately for sherpa-onnx.
- `ffmpeg` (Homebrew) — the fallback recorder when the menu-bar app is not
  running; the native path needs no extra tools.
- Accessibility permission for `Sasayaki.app` (System Settings > Privacy &
  Security > Accessibility) — required for the automatic Cmd+V paste.
- launchd user agents (built in) for the background daemon.

First-run setup installs Python packages in Sasayaki's own data directory and
downloads the selected offline model. It does not modify an existing Python
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

Then bind `sasayaki toggle` in KDE, GNOME, Hyprland, Sway, or any other
desktop's keyboard-shortcut settings. Run `sasayaki shortcut` for
desktop-specific instructions.

**Using it without Sumika Shell** (any Linux desktop, or none at all) is
documented in [docs/standalone-usage.md](docs/standalone-usage.md): the two
driving commands are `sasayaki toggle` (recognize + paste) and
`sasayaki translate-toggle` (recognize + translate + paste), plus per-desktop
shortcut setup, Wayland/X11 paste notes, and headless usage.

**Distribution packaging** (RPM/DEB with system dependencies pulled in
automatically) lives in [packaging/rpm](packaging/rpm/README.md) and
[packaging/deb](packaging/deb/README.md); build with `make dist/rpm` /
`make dist/deb`.

## Quick start (macOS)

On macOS the menu-bar app (`Sasayaki.app`) is the primary interface: it owns
the microphone permission, records via native `AVAudioEngine`, and pastes via
`Cmd+V` (CGEvent). The Go daemon runs as a launchd user agent and does the
transcription.

```sh
# 1. Build the app bundle (Go binary + Swift menu-bar app, signed with a
#    stable self-signed identity so TCC grants survive rebuilds):
sh mac/build.sh

# 2. First-run setup — installs the private Python runtime + model, and
#    installs the launchd agent:
dist/Sasayaki.app/Contents/MacOS/sasayaki setup

# 3. Launch the menu-bar app:
open dist/Sasayaki.app
```

On first launch macOS will prompt for two permissions — grant both:

- **Microphone** — the app records audio via `AVAudioEngine`. Without it the
  recorder produces an empty file.
- **Accessibility** (System Settings > Privacy & Security > Accessibility) —
  required for the automatic `Cmd+V` paste. The app prompts once on launch;
  enable `Sasayaki` in the list. If you rebuilt the app and paste stops
  working, toggle the switch off and on once to refresh the grant.

Then press **F13** once to start recording, again to stop and transcribe —
the result is pasted into whatever window has focus. **F14** records and
translates. The menu-bar icon shows state (idle/recording/transcribing).

### Installing a prebuilt download

Each push to `main` publishes a rolling [**Latest build**
release](https://github.com/iamcheyan/sasayaki/releases/latest) with
binaries for all four targets:

- `Sasayaki-macos-arm64.zip` — Apple Silicon
- `Sasayaki-macos-amd64.zip` — Intel
- `sasayaki-linux-amd64.tar.gz` / `sasayaki-linux-arm64.tar.gz`

The macOS bundles are **ad-hoc signed** (CI has no Developer ID, so they
are not notarized). macOS Gatekeeper will block the first launch — strip
the quarantine attribute and grant the two TCC permissions:

```sh
xattr -dr com.apple.quarantine /path/to/Sasayaki.app
open /path/to/Sasayaki.app        # then grant Microphone + Accessibility
```

Ad-hoc signing keys the TCC grant to the binary's cdhash, so a freshly
downloaded build needs its own grant. For a grant that survives rebuilds,
build from source instead (below) — `mac/sign.sh` signs with a stable
self-signed identity.

### Rebuilding during development

When iterating on the menu-bar app, recompile and re-sign in one step so
the Accessibility/Microphone grants survive:

```sh
sh mac/dev-rebuild.sh   # swiftc the menubar binary + re-sign + relaunch
```

`dev-rebuild.sh` re-signs with the stable `sumika-voice-dev` identity, so
the TCC grants stay valid across rebuilds. A bare `swiftc` without
re-signing falls back to ad-hoc (cdhash) signing and **breaks auto-paste
on every recompile** — that is the "paste stops working after rebuild"
symptom. If you hit it, re-run `mac/dev-rebuild.sh` (or `mac/sign.sh`)
once; the next rebuild is fine.

> The signing identity (`sumika-voice-dev`) is created automatically by
> `mac/sign.sh` on first build. TCC grants bind to the code signature, so a
> fixed identity means you grant once and rebuild freely. Ad-hoc signing
> would invalidate the grants on every rebuild.

## Commands

```text
sasayaki                      Open the control center (TUI)
sasayaki setup                Install/repair the local runtime, model and service
sasayaki repair               Re-run checks and repair local components
sasayaki toggle               Start or finish voice input (desktop shortcut)
sasayaki translate-toggle     Record and translate (requires translation enabled)
sasayaki cancel               Cancel active recording or transcription
sasayaki bindings             Print keybindings for desktop integration
sasayaki status [--json]      Show service and readiness state
sasayaki diagnose [--json]    Full dependency and capability report
sasayaki models …             List, select, or download a local speech model
sasayaki translation …        Configure/test optional online translation
sasayaki service start|stop|restart|status
sasayaki shortcut             Show desktop shortcut instructions
sasayaki logs                 Follow the user-service log
```

Exit codes are predictable for scripting: `0` success, `1` operational
failure (service down, setup incomplete, empty recording, …), `2` usage
error. `status` and `diagnose` print JSON when given `--json`.

## Models

Sasayaki's model catalog includes multilingual **SenseVoice** — fast int8
(229 MB) or quality-first full precision (894 MB) — and Chinese-first
**Paraformer** (232 MB). The SenseVoice variants support Chinese, English,
Japanese, Korean, and Cantonese; Paraformer is a separate recognizer backend
that supports Chinese only.

```sh
sasayaki models
sasayaki models download paraformer-zh-int8
# or: sasayaki models select sensevoice-full && sasayaki setup
```

Models are pinned by SHA-256 and kept in separate private directories. The
ONNX files work on both x86_64 and ARM64; the private `sherpa-onnx` Python
runtime is installed as the wheel appropriate to the user's CPU architecture.

## Translation

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

`translate-toggle` is gated on the global `translation.enabled` flag: when
disabled it fails fast with a clear message instead of silently degrading to
plain dictation. A plain `toggle` never translates, even when the flag is on.

## Sumika Shell integration

When running inside the [Sumika Shell](https://github.com/iamcheyan/oh-my-desktop)
desktop, Sasayaki also ships as a QML extension module and registers:

- **Taskbar icon** (right slot) — colored by state: white idle, yellow
  recording with a pulse ring, blue transcribing/translating, red error.
  Click to toggle recording; right-click for the context menu.
- **Popup** — status card with live state, copy/cancel controls, and a
  history of recent results.
- **Context menu** — Control Center (TUI), Edit Configuration, Service
  Restart, and Diagnose.
- **Settings page** — Voice Input page inside Sumika's settings dialog.
- **Key bindings** — `sasayaki bindings` feeds Hyprland's `bindings.lua`;
  entries come from Sasayaki's own config (`voice_bindings` /
  `translation_binding` in `~/.config/sasayaki/config.json`).
- **CapsLock wake** — enable it (`sasayaki capslock toggle`, the TUI
  settings menu, or the taskbar context menu) and a bare tap of the
  CapsLock key toggles voice input in every keyboard state — stock or
  ctrl↔caps-swapped, any layout. Held chords like `Ctrl+C` are unaffected.
  See [docs/wake-keys.md](docs/wake-keys.md).
- **Floating TUI** — the control center opens in a centered floating window
  (1180×760) via Hyprland window rules, matching the other Sumika TUI tools.

The QML layer is a thin client: it polls `sasayaki status --json` for the
taskbar/popup state and forwards hotkey presses to the `sasayaki` binary.
Disabling the module (`modules.disabled: ["sasayaki"]` in Sumika config)
leaves the standalone program fully functional.

## Privacy

Everything runs on this machine. Audio never leaves the device: recording is
transcribed locally with SenseVoice and there are no network calls at
runtime unless you configure and explicitly trigger online translation. The
service journal logs metadata and errors only — not transcripts. To opt into
full transcript logging, set `verbose_transcripts` in
`~/.config/sasayaki/config.json` to `true`.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `toggle` prints "service is not running" | `sasayaki service start`, or `sasayaki setup` on first run |
| `diagnose` shows a failed check | The `fix` field names the command; e.g. install `pulseaudio-utils` or start PipeWire/PulseAudio |
| "microphone produced an empty recording" | Check input device/level; run `pactl list short sources` |
| Model re-downloads on every setup | Corrupt model files; `sasayaki setup` repairs automatically |
| Korean text looks over-spaced | Known SenseVoice limitation; text content is correct |
| Nothing is pasted but clipboard was set | Paste backends unavailable (needs `wtype`/`ydotool`/`xdotool`); the failure state says so |
| Translation hotkey records but does not translate | `translation.enabled` is false in `~/.config/sasayaki/config.json`; enable it in the control center |
| macOS: F13 does nothing, no recording starts | Grant Microphone to `Sasayaki.app` (System Settings > Privacy & Security > Microphone) |
| macOS: transcribes but text is not pasted | Grant Accessibility to `Sasayaki.app`; if already granted, toggle it off/on once after a rebuild |
| macOS: text is pasted twice | Rebuild the app (`sh mac/build.sh`) — the CLI no longer pastes when the menubar app owns the paste |
| macOS: `service is not running` | `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/io.github.iamcheyan.sasayaki.plist`, or re-run `sasayaki setup` |

## Removing

```sh
systemctl --user disable --now sasayaki.service
rm ~/.config/systemd/user/sasayaki.service
rm -rf ~/.config/sasayaki ~/.local/share/sasayaki ~/.local/state/sasayaki
systemctl --user daemon-reload
```

This removes the config, model, private runtime and recordings. If installed
as a Sumika extension, also remove the extension directory and drop
`sasayaki` from the module list.

**macOS**:

```sh
launchctl bootout gui/$(id -u)/io.github.iamcheyan.sasayaki 2>/dev/null
rm ~/Library/LaunchAgents/io.github.iamcheyan.sasayaki.plist
rm -rf ~/.config/sasayaki ~/.local/share/sasayaki ~/.local/state/sasayaki
```

## Files

All application data is owned by Sasayaki:

```text
~/.config/sasayaki/config.json                 preferences
~/.local/share/sasayaki/models/sensevoice/     downloaded model
~/.local/share/sasayaki/runtime/venv/          private Python environment
~/.local/state/sasayaki/                       recordings and logs
~/.config/systemd/user/sasayaki.service        user service (Linux)
$XDG_RUNTIME_DIR/sasayaki/sasayaki.sock        control socket (Linux)

# macOS
~/Library/LaunchAgents/io.github.iamcheyan.sasayaki.plist   launchd agent
~/Library/Caches/sasayaki/sasayaki.sock                     control socket
dist/Sasayaki.app                                           menu-bar app bundle
```

## Development

```sh
go test ./...
go run ./cmd/sasayaki
```

macOS-specific code lives under `mac/` (the Swift menu-bar app) and behind
`//go:build darwin` tags in `internal/` (paste, recording, service unit,
setup, diagnostics). Build the macOS app bundle with `sh mac/build.sh`;
Linux builds are unaffected by the darwin-only files. Cross-check both
platforms with `GOOS=linux go build ./...` from macOS.

The visual language is documented in
[docs/tui-design-language.md](docs/tui-design-language.md).
The implementation boundary and desktop-integration decisions are in
[docs/architecture.md](docs/architecture.md). The speech model, checksums,
measured latency and license are in [docs/model.md](docs/model.md).
Non-Sumika usage is in [docs/standalone-usage.md](docs/standalone-usage.md).
