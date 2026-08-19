# Architecture

Sasayaki is a standalone application for Linux and macOS. It does not read,
write, or invoke Sumika, Omarchy, or Shirabe files.

```text
desktop shortcut
       │  `sasayaki toggle`
       ▼
short-lived Go CLI ── Unix socket ── Go user service
                                          │
                              parecord ──┤── private Python/SenseVoice
                                          │
                                          └── wl-copy → window-aware paste → focused app
```

## Why Go plus Python

Go is responsible for the product boundary: a single distributable control
binary, a Bubble Tea control center, a stable local protocol, setup, model
download, service lifecycle and desktop integration. The speech model remains
in a private Python virtual environment because Sherpa-ONNX offers the most
mature SenseVoice bindings there. Users never need to manage that environment:
`sasayaki setup` creates it under Sasayaki's data directory.

This is deliberately not a system Python dependency. Packaging the Go binary
does not bundle a Python interpreter, so `python3` is still a prerequisite, but
the Python package set and model are isolated and reproducible.

## Background service

`~/.config/systemd/user/sasayaki.service` starts `sasayaki serve` in the user's
session. The service exposes a user-only Unix socket at
`$XDG_RUNTIME_DIR/sasayaki/sasayaki.sock` (directory `0700`, socket `0600`);
one-off invocations such as a KDE or GNOME shortcut send a small JSON request
through it and exit immediately.

### Control protocol

Versioned, newline-delimited JSON over the socket. `status`, `toggle`,
`cancel`, `deliver` and `diagnose` are handled serially under a mutex; a
`diagnose` response carries the full `diagnostics.Report` inside the response
envelope. `deliver` (macOS) accepts an externally finalized WAV from the
menu-bar app, which owns the microphone grant and records via AVAudioEngine.
Errors are typed (`code`, `class`, `detail`) so the TUI and CLI can present
them distinctly. Unknown operations and wrong protocol versions are rejected
with specific error codes.

### Operation phases

The service tracks `idle → recording → transcribing → pasting →
succeeded | failed`. A toggle starts or stops recording; stopping hands the
raw `s16le` capture to a Go-side WAV wrapper and enqueues transcription on
the warm worker, then pastes. The last result (phase, time, truncated
text/error) is kept in state; full transcripts are never logged unless
`verbose_transcripts` is enabled. Empty speech, too-short recordings, model
failures and paste failures each produce a specific `failed` state and never
claim a paste that only reached the clipboard.

## Speech pipeline

```text
parecord (16 kHz mono s16le raw) ──▶ Go WAV wrapper
        ──▶ warm worker ──▶ venv sherpa-onnx SenseVoice (use_itn, 4 threads)
        ──▶ normalized text ──▶ wl-copy ──▶ window-aware paste (wtype/ydotool/xdotool)
```

A worker process is kept warm in the service (model resident in memory,
~70–150 ms per utterance; see [docs/model.md](model.md)); a crashed worker
restarts with capped backoff. Engine stdout is a line-delimited JSON
protocol (`ready` banner, then `{id, ok, text}` responses to
`{id, command: transcribe, wav}` requests).

No root privileges are needed. `sasayaki service stop` only stops the user
service; it leaves profiles, recordings, model and runtime intact. Removing the
service is documented rather than made a prominent action:

```sh
systemctl --user disable --now sasayaki.service
rm ~/.config/systemd/user/sasayaki.service
systemctl --user daemon-reload
```

## macOS platform

The Go service and protocol are platform-agnostic; macOS adapts three edges
behind `//go:build darwin` tags:

- **Recording** — the menu-bar app (`mac/StatusBar.swift`, an
  `LSUIElement` status-item app) records via `AVAudioEngine` and a
  converter tap that resamples to 16 kHz mono s16le, writing an
  `AVAudioFile`. This keeps the TCC microphone grant attributed to the app
  process — `ffmpeg`/`parecord` spawned from the launchd daemon would be
  attributed to a different binary and silently zero-fill. `ffmpeg`
  remains as a fallback recorder when the app is not running.
- **Service lifecycle** — `internal/service/unit_darwin.go` maps the
  `service start|stop|restart|status` verbs to `launchctl`
  `bootstrap`/`bootout` against a user LaunchAgent
  (`~/Library/LaunchAgents/io.github.iamcheyan.sasayaki.plist`). The plist
  injects the user's `PATH` so Homebrew tools resolve.
- **Paste** — the launchd daemon has no Aqua session, so daemon-side
  `pbcopy`/osascript silently miss. Paste is owned by the client: the
  menu-bar app sets `NSPasteboard` and posts `Cmd+V` via `CGEvent` from its
  own process (one Accessibility grant). The CLI `deliver --no-paste` flag
  prevents the CLI from racing the app's paste (the cause of double-paste).

```text
F13 hotkey (Carbon EventHotKey)
       │  toggle
       ▼
Sasayaki.app (Swift) ── AVAudioEngine ──▶ WAV
       │  sasayaki deliver <wav> --no-paste --json
       ▼
Go daemon (launchd) ── socket ── private Python/SenseVoice ──▶ text
       │  status poll (phase=succeeded, last_result)
       ▼
Sasayaki.app ── NSPasteboard + CGEvent Cmd+V ──▶ focused app
```

Signing: `mac/sign.sh` creates a fixed self-signed identity
(`sumika-voice-dev`) so TCC grants (microphone, accessibility) bind to a
stable designated requirement and survive rebuilds. Ad-hoc signing would
re-prompt on every rebuild.

### Menu-bar icon rendering

The status-item icon (`mac/StatusBar.swift`) goes through several states
(idle mic → white↔blue pulsing waveform → green check → red waveform).
Coloring SF Symbols in an `NSStatusItem` has two traps that both cost a
round-trip each:

1. **`NSStatusBarButton` ignores `contentTintColor`.** It enforces template
   rendering and strips custom color, so:
   - a *non-template* symbol + `contentTintColor` renders the symbol's
     **built-in multicolor palette**, not your tint —
     `exclamationmark.triangle.fill` showed up as a stray **yellow ⚠️**
     (its default palette) instead of the intended red;
   - a *template* image + `contentTintColor` renders as a flat **monochrome
     mask** (black) with the tint discarded — every colored icon went black.
2. **The fix: bake the colors into the image.** Build the symbol with
   `NSImage.SymbolConfiguration(paletteColors:)` →
   `.withSymbolConfiguration(cfg)`, set `isTemplate = false`, and assign it.
   The menu bar then renders the baked pixels directly. Each colored state
   (waveform, checkmark, failed) is baked; the idle mic stays a plain
   **template** image so it adapts to light/dark menus.
3. **Animation = re-bake, not tint animation.** The white↔blue pulse can't
   animate `contentTintColor` (ignored); instead it re-bakes the waveform
   each frame (~20 fps) with a palette color sine-interpolated between
   white and `systemBlue`.

The baked colors were verified by rasterizing each symbol and sampling
   pixels (blue waveform `r0g3b5`, white `r5g5b5`, red `r5g1b1`, green
   circle + white check) — don't trust the menu bar visually until the
   pixel check passes.

## Desktop input boundary

Wayland prevents arbitrary programs from globally listening for key presses or
injecting text. Sasayaki therefore asks the desktop/compositor to own the
global shortcut and provides a portable command target: `sasayaki toggle`.
For paste, `internal/paste` resolves the focused window and picks the path
that actually works for it, rather than silently pretending one chord works
everywhere:

- **Native Wayland** — the text goes to the Wayland clipboard (`wl-copy`),
  then the paste chord the app binds is injected: kitty gets its native
  remote paste (`kitty @ action paste_from_clipboard`), GUI apps get
  `Ctrl+V`, terminals get `Shift+Insert` / `Ctrl+Shift+V`. Key injection
  falls back `wtype` → `ydotool` → `hyprctl send_key_state` → `xdotool`.
- **XWayland windows** (Hyprland: `hyprctl activewindow` reports
  `xwayland: true`) — the text is written to the X11 clipboard (`xsel`,
  `xclip` fallback) and the chord is injected at the X server with
  `xdotool` XTEST, which reaches the focused X window where Wayland virtual
  keyboards cannot.
- **No usable backend** — the clipboard is still set and the result is
  reported as clipboard-only (never as pasted); `sasayaki diagnose` names
  the missing tool.

Backends live behind the `internal/paste` interface, so the service protocol
does not change when one is added or removed.

The default is toggle-to-record, which is reliable for generic command
shortcuts. A portal push-to-talk binding is a future enhancement where
`Activated`/`Deactivated` shortcut events are available.

### Wake keys

Sasayaki can also be toggled by tapping an enabled wake key (CapsLock,
LeftCtrl or RightCtrl — any combination, all optional). The feature spans
three cooperating layers — the `wake_keys` config struct, the
`voicetap` lines of `sasayaki bindings` (consumed by the desktop's binding
generator), and, when a keyd ctrl↔caps swap is active, a Sumika
keyboard-remap overload that turns a bare tap of the caps position into
`F24` while held chords stay real Ctrl. See [wake-keys.md](wake-keys.md)
for the full matrix and implementation notes.
