# Architecture

Sasayaki is a standalone Linux application. It does not read, write, or invoke
Sumika, Omarchy, or Shirabe files.

```text
desktop shortcut
       │  `sasayaki toggle`
       ▼
short-lived Go CLI ── Unix socket ── Go user service
                                          │
                              parecord ──┤── private Python/SenseVoice
                                          │
                                          └── wl-copy + wtype → focused app
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
`cancel` and `diagnose` are handled serially under a mutex; a `diagnose`
response carries the full `diagnostics.Report` inside the response envelope.
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
        ──▶ normalized text ──▶ wl-copy ──▶ wtype/ydotool/xdotool paste
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

## Desktop input boundary

Wayland prevents arbitrary programs from globally listening for key presses or
injecting text. Sasayaki therefore asks the desktop/compositor to own the
global shortcut and provides a portable command target: `sasayaki toggle`.
For paste, it uses `wl-copy` and `wtype`. This is visible in status rather than
silently pretending it can work everywhere. X11 and portal-specific backends
can be added behind the paste/shortcut interfaces without changing the service
protocol.

The default is toggle-to-record, which is reliable for generic command
shortcuts. A portal push-to-talk binding is a future enhancement where
`Activated`/`Deactivated` shortcut events are available.
