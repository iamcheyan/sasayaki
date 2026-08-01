# Sasayaki implementation brief for an engineering model

This document is a self-contained implementation brief. Give it to an
engineering model together with this repository. The model should inspect the
repository before changing code, preserve useful existing work, and implement
the product described here rather than applying cosmetic patches.

## Mission

Build **Sasayaki**, a small, polished, standalone Linux voice-input product.
The user presses a global shortcut, speaks, presses it again, and the locally
transcribed text is pasted into the application that had focus. Sasayaki must
be useful outside Sumika, Omarchy, Hyprland, and any particular desktop
environment. A user should be able to download one Go binary, run setup, bind
a desktop shortcut, and use it on KDE, GNOME, Sway, Hyprland, or another Linux
desktop.

The existing source is a Go implementation using Bubble Tea. It is a starting
point, not a sacred design. Improve, reorganize, or replace modules where that
makes the product more reliable and elegant. Do **not** modify or depend on the
existing Sumika voice extension located outside this repository. It can be
read as historical functional reference only.

The completed product should feel like a member of the same application family
as `shirabe`: compact, clear, deliberate, responsive, and quietly delightful.
Read these repository documents before implementation:

- `README.md` — product-facing installation and command contract.
- `docs/architecture.md` — current independent-product boundary and platform
  constraints.
- `docs/tui-design-language.md` — the shared visual and interaction language.

Also inspect `/home/tetsuya/development/shirabe/` only as a style and Bubble Tea
implementation reference. Do not import its package structure blindly and do
not modify that repository.

## Non-negotiable product boundaries

1. **Standalone ownership.** Sasayaki owns all of its state. It must never read
   or write `sumika-shell`, Omarchy, or Shirabe configuration/state paths.
2. **XDG paths.** Use the following locations, respecting `XDG_CONFIG_HOME`,
   `XDG_DATA_HOME`, `XDG_STATE_HOME`, and `XDG_RUNTIME_DIR`:

   ```text
   ~/.config/sasayaki/config.json
   ~/.config/systemd/user/sasayaki.service
   ~/.local/share/sasayaki/models/sensevoice/
   ~/.local/share/sasayaki/runtime/venv/
   ~/.local/state/sasayaki/recordings/
   $XDG_RUNTIME_DIR/sasayaki/sasayaki.sock
   ```

   Create private directories/files with suitable permissions (`0700` dirs,
   `0600` files/socket where applicable).
3. **No root requirement.** The background service is a `systemd --user`
   service. Normal setup should never ask for sudo. If a dependency is missing,
   show a precise human-readable diagnostic and the package/tool required; do
   not try to silently install operating-system packages.
4. **Local speech.** Audio and transcription stay local. The app may download a
   public model chosen by the user/project, but it must not upload recordings
   or text to a web API.
5. **Go product boundary.** Go owns the binary, TUI, configuration, lifecycle,
   control protocol, model management, recording orchestration, diagnostics,
   and paste orchestration. A private Python virtual environment is acceptable
   for a model that has only good Python bindings. It must be created and owned
   by Sasayaki, never imposed on the user's global Python environment.
6. **Desktop shortcut boundary.** Generic Wayland applications cannot secretly
   install global hotkeys or arbitrarily inject input. Provide the stable
   command `sasayaki toggle`; let KDE/GNOME/compositors bind it. Show clear
   instructions and useful config snippets. If an XDG Global Shortcuts Portal
   implementation is added, make it an enhancement, not the only route.

## User journey and final behavior

### First run

1. User runs `sasayaki` and sees a concise control center, not a wall of logs.
2. The screen truthfully shows setup is required and offers an explicit Setup
   action/key.
3. User chooses Setup. The TUI presents a progress/status view or inline status
   rows: checking prerequisites, creating the private runtime, installing model
   packages, downloading/validating model files, writing the user service,
   enabling/starting it, and confirming success.
4. If Python, `parecord`, `wl-copy`, `wtype`, systemd user session, microphone,
   disk space, or network is unavailable, show what failed, what was not
   changed, and a concrete recovery command. Do not pretend setup succeeded.
5. On success the user sees a “Ready” state and can open shortcut help. The app
   tells them to bind `sasayaki toggle` in their desktop settings and supplies:

   ```text
   KDE: System Settings → Shortcuts → Add Command
   GNOME: Settings → Keyboard → Custom Shortcuts
   Hyprland: bind = SUPER, V, exec, sasayaki toggle
   Sway: bindsym $mod+v exec sasayaki toggle
   ```

### Everyday voice input

1. The desktop shortcut invokes `sasayaki toggle`; this short-lived CLI sends a
   request to the already running user service over a private Unix socket.
2. First invocation starts microphone recording. It should return promptly and
   print a useful status for a terminal caller.
3. Second invocation stops recording and immediately reports “Transcribing…”.
4. The service transcribes with the local model, copies output to the Wayland
   clipboard, then pastes it into the focused app using the best available
   backend.
5. The service tracks the complete operation state: idle, recording,
   transcribing, pasting, succeeded, failed. The TUI and `sasayaki status` can
   show the last meaningful result/error, not merely whether the process is
   alive.
6. Empty speech, a too-short recording, model failure, or paste failure should
   preserve the clipboard when possible and result in a specific visible
   failure state. Never claim that text was pasted if only the clipboard write
   succeeded.
7. Temporary recordings should be removed after a configurable short retention
   period, with an option to retain them only for diagnostics. Do not accumulate
   private audio indefinitely.

### Lifecycle and recovery

- `sasayaki setup` is idempotent. Re-running it repairs missing runtime/model
  artifacts and starts/enables the service without needlessly redownloading a
  valid model.
- `sasayaki service start`, `stop`, `restart`, and `status` are useful from a
  normal shell. State their effect accurately.
- In the TUI, use an explicit `D` key for Disable/stop. Do not make Enter the
  dangerous default. Stopping leaves user configuration, model and private
  runtime untouched.
- Do not add an attractive “uninstall” button. Help should explain safe manual
  removal:

  ```sh
  systemctl --user disable --now sasayaki.service
  rm ~/.config/systemd/user/sasayaki.service
  rm -rf ~/.config/sasayaki ~/.local/share/sasayaki ~/.local/state/sasayaki
  systemctl --user daemon-reload
  ```

- A service crash should be restarted by systemd. The TUI must show an unhealthy
  service rather than a stale optimistic “Ready” badge.

## Functional architecture requirements

Use clear packages with narrow responsibilities. The exact names can differ,
but the boundaries must be apparent and testable.

```text
cmd/sasayaki/            command dispatch only
internal/app/            Bubble Tea model, layouts, overlays, notices
internal/config/         XDG paths, validated config, atomic persistence
internal/service/        systemd unit lifecycle, Unix control server/client
internal/recording/      recorder interface and parecord implementation
internal/transcribe/     model runtime interface, health, request execution
internal/paste/          clipboard/paste backend interface and capability checks
internal/diagnostics/    prerequisite checks and actionable reports
internal/setup/          idempotent transactional-ish setup workflow
internal/protocol/       versioned socket messages and state snapshots
assets/ or embed/        private engine assets/templates
docs/                    user-facing design and engineering documentation
```

### Control protocol

The service protocol must be local, line-delimited JSON or equivalently simple
and versionable. Include a protocol version from the beginning. It should
support at least:

```text
status       return all readiness and last-operation state
toggle       transition idle → recording or recording → transcribing
cancel       cancel a recording/transcription safely
diagnose     return capability checks
```

The response should distinguish transport/service errors from user-action
errors. Concurrent toggles must not race: define behavior such as rejecting a
second request while transcribing with “Still transcribing the previous clip.”

### Recording

- Use a Go interface so `parecord` is an implementation detail and tests use a
  fake recorder.
- Record 16 kHz, mono, signed 16-bit WAV unless the chosen model requires a
  different documented format.
- Guard against zero-byte/invalid recordings. Stop child processes cleanly on
  cancellation and service shutdown; do not orphan `parecord`.
- Surface microphone errors in the service state and journal logs.

### Model runtime

The initial code uses a private Python/Sherpa-ONNX SenseVoice runtime. An
alternative price/performance model may replace it. Before choosing a model,
compare quality in Chinese/Japanese/English, CPU/RAM use, cold-start and
warm-start latency, package size, model license, offline support, model download
reliability, and bindings usable from a Go product.

Whatever model is selected must satisfy:

- local/offline inference after download;
- clearly documented model source, version, checksum and license;
- model download that writes to a `.part` file, validates before rename, and
  resumes or safely retries;
- no inference process startup for every recording if the library permits a
  long-lived runtime. Keep the model warm in the user service or in a managed
  private worker process;
- robust worker restart/backoff and a clear state if the worker dies;
- transcription output normalization appropriate to the model (for example,
  stripping control tags only when they are model output, preserving user text);
- configurable language/automatic language selection stored in config.

If Python remains necessary, do not make the user run pip commands manually.
`sasayaki setup` creates a venv under Sasayaki's own data directory and runs
the exact pinned requirements. Prefer a lock file or a documented version map.

### Pasting

Pasting is desktop-specific. Implement it behind an ordered backend abstraction
that reports exactly what it did:

1. Copy text with `wl-copy` on Wayland, preferably preserving/restoring the
   prior clipboard only if this can be done reliably and without races.
2. Use `wtype` for a normal GUI paste chord; test the chosen syntax.
3. Add optional backends for supported systems such as `ydotool` or X11 tools.
   Do not claim arbitrary universal injection is possible under Wayland.
4. When no paste backend works, still offer a truthful “copied to clipboard;
   paste it manually” result and explain which dependency is missing.

The app should expose the selected backend and its readiness in Diagnostics.

## TUI and interaction requirements

This is not a dashboard and not an imitation of an IDE. It is a compact,
keyboard-first control center. Read `docs/tui-design-language.md` and follow it
closely.

### Main screen

- Centered shared maximum width (approximately 118–132 cells) with a generous
  black/deep-charcoal background.
- Header: `✦ sasayaki` in soft violet, a muted product descriptor, and a small
  mint/amber status pill aligned right. Header and cards have exactly one blank
  terminal row between them.
- Two thin rounded cards with titles sitting on the border, equal calculated
  height, and a consistent gap. At narrower widths they stack rather than
  squeeze/overlap. Footer stays centered below, with one blank row of space.
- Left **VOICE** card: current activity, selected shortcut mode, primary record
  action, last transcript summary (privacy-safe/truncated), and short guidance.
- Right **RUNTIME** card: service state, model state, microphone state,
  paste-backend state, and diagnostics/setup action.
- Use `◆` or another short section marker, not tall decorative vertical bars
  that visually collide with following characters.
- Do not repeat information in header, card, footer, and sidebars. Main screen
  should answer: “Will it work?”, “What happens if I press the shortcut?”, and
  “What must I fix?”

### Navigation and input

- Arrow keys move spatially among every focusable action across both cards:
  left/right crosses cards; up/down moves rows. `Tab` is a secondary shortcut.
- `Enter` activates the focused harmless action. Use explicit letters for
  significant actions: `T` toggle, `S` setup, `D` disable/stop, `B` shortcut
  help, `L` logs, `?` help, `Q` quit.
- Make all focus states visually unmistakable without being loud.
- Overlay dialogs are appropriate for help, shortcut instructions, logs,
  diagnostics details and confirmation of a consequential action. `Esc` always
  closes an overlay. Do not use a modal just to announce success.
- Use Bubble Tea's `tea.Tick` or an equivalent event/state mechanism for
  transient notices. An operation should temporarily replace the footer hint
  with e.g. “Recording…”, “Transcribing…”, “Pasted”, or an error, then restore
  normal key hints after several seconds.
- Do not display raw stack traces or package installation logs in the main UI.
  Put detailed output in a scrollable logs/diagnostics overlay and journal.

### Accessibility and terminal resilience

- Work at 80×24; do not panic at narrower terminals. At very small dimensions,
  show a compact actionable view rather than corrupt borders.
- Avoid relying solely on color: use labels/icons/text for Ready, Error,
  Recording, and Disabled.
- Handle no-color and low-color terminals gracefully.
- Mouse support is optional. If added, it must use the same geometry model as
  keyboard focus; do not maintain mismatched hard-coded hitboxes.

## CLI contract

Keep these commands functional and documented:

```text
sasayaki                         launch TUI
sasayaki setup                   provision/repair runtime, model and service
sasayaki toggle                  record or finish through the running service
sasayaki status                  concise machine/human-readable readiness state
sasayaki diagnose                full dependency and capability report
sasayaki service start
sasayaki service stop
sasayaki service restart
sasayaki shortcut                desktop-specific binding guidance
sasayaki logs                    follow/read user-service logs
sasayaki help
```

Use predictable exit codes. Add `--json` to `status` and `diagnose` if it can
be implemented cleanly; this makes integrations possible without weakening the
human output. Do not make the TUI the only way to operate the application.

## Quality, safety, and code standards

- Use contexts/timeouts for external commands, downloads, socket calls and
  shutdown. Never let a failed child process hang the service indefinitely.
- Use atomic config writes and never truncate a known-good config on failure.
- Sanitize all external command arguments; do not use shell interpolation.
- Do not log full transcribed private text by default. Log metadata/errors;
  make verbose transcript logging opt-in.
- Keep model downloads and venv creation cancellable where feasible, with clear
  cleanup of partial artifacts.
- Include meaningful unit tests for config paths/persistence, protocol state
  transitions, recording state machine, diagnostics, setup planning and layout
  breakpoints. Use fake command runners/HTTP servers rather than requiring an
  actual microphone or model in unit tests.
- Add a small integration-test mode or manual test harness for the socket
  protocol and fake transcription/paste backend.
- Run `gofmt`, `go vet ./...`, `go test ./...`, and `go build ./cmd/sasayaki`.
  Do not leave generated binaries, Python caches, recordings, `.part` files or
  secrets tracked.
- Organize commits by coherent capability. Do not combine unrelated formatting,
  generated artifacts, and functional changes in one commit.

## Step-by-step acceptance plan

Treat every phase as a gate. Do not call the work complete simply because a TUI
renders or code compiles.

### Gate 0 — repository hygiene

- [ ] `git status --short` is clean before and after each logical phase.
- [ ] Root contains a concise README, Go module, license/ignore/build metadata;
      implementation is under `cmd/`, `internal/`, `assets/`, and `docs/`.
- [ ] No references to Sumika or Omarchy paths remain in code/config behavior.
- [ ] Documentation accurately matches the current implementation.

### Gate 1 — static application shell

- [ ] `sasayaki help` lists every supported command and exits successfully.
- [ ] `sasayaki` opens a Bubble Tea TUI that follows the two-card visual spec.
- [ ] Resize manually at 80×24, 100×30 and a wide terminal; cards remain
      aligned/equal-height when side-by-side and stack cleanly when narrow.
- [ ] Arrow keys, Tab, Enter, ? and Esc behave as specified.
- [ ] No setup is assumed: a fresh environment clearly displays what is absent.

### Gate 2 — configuration and diagnostics

- [ ] Override XDG variables in tests and verify every path is under the
      override, with no writes to real home directories.
- [ ] `sasayaki diagnose` identifies installed/missing `python3`, `parecord`,
      `wl-copy`, paste backend, systemd user manager, runtime, model and socket.
- [ ] A missing tool produces a concrete remediation message, not a Go error
      dump.

### Gate 3 — setup and service lifecycle

- [ ] With test/fake download sources, setup creates correct directories,
      config, runtime asset and user unit.
- [ ] Setup is repeatable and avoids downloading already valid artifacts.
- [ ] The written systemd service uses the actual installed binary path and
      starts `sasayaki serve` as the user.
- [ ] `systemctl --user status sasayaki.service` reports active after success.
- [ ] Stop/restart/disable correctly change state and never delete user data.
- [ ] A failed setup leaves clear recoverable state and no corrupted config.

### Gate 4 — service state machine

- [ ] Socket permissions prevent another local user from controlling it.
- [ ] A client can request `status`, `toggle` and `cancel` against a test
      service.
- [ ] First toggle starts recording; second toggle transitions exactly once to
      transcribing; extra toggles during transcription are handled predictably.
- [ ] Service shutdown cancels/cleans child processes and removes socket.
- [ ] Status shows last success/failure and current phase after every action.

### Gate 5 — local transcription and paste

- [ ] A fixture WAV reaches the selected local model and returns normalized
      expected text.
- [ ] Model is warm/persistent where possible; measure and record cold vs warm
      latency in documentation or diagnostics.
- [ ] Valid live recording produces text locally without network after model is
      installed.
- [ ] Paste backend tests assert the exact commands/arguments used.
- [ ] If paste is unavailable, clipboard/manual-paste fallback is truthful.
- [ ] Temporary recording retention/cleanup works and does not expose files to
      other users.

### Gate 6 — real desktop smoke tests

Run manually on at least one Wayland compositor, ideally both a full desktop
(KDE or GNOME) and a wlroots compositor (Hyprland or Sway):

- [ ] Bind `sasayaki toggle` as a global shortcut following the on-screen text.
- [ ] Start recording in a terminal and finish it; text is pasted into the
      terminal or copied with a clear fallback message.
- [ ] Repeat in a normal GUI text field.
- [ ] Stop service then invoke shortcut; output tells user how to repair it.
- [ ] Start service again; existing setup/model works without rerunning setup.
- [ ] TUI status agrees with `sasayaki status` and `systemctl --user status`.

### Gate 7 — release readiness

- [ ] `gofmt -w` changes nothing; `go vet ./...`, `go test ./...`, and
      `go build ./cmd/sasayaki` all pass.
- [ ] Build a stripped release binary and verify `sasayaki help` and
      `sasayaki status` from that binary.
- [ ] README contains prerequisites, install method, first-run steps, desktop
      shortcut guidance, privacy statement, troubleshooting and removal steps.
- [ ] Architecture/design docs reflect the chosen model and actual backends.
- [ ] Git history is clean, all intended commits are pushed, and the final
      change set is reviewable without unrelated modifications.

## Definition of done

The job is done only when a person who has never installed Sumika can install a
Sasayaki binary, run one understandable setup flow, create a shortcut in their
own desktop environment, dictate text locally, see a truthful result, and
recover from common failures using the application’s own diagnostics. The main
screen must remain small and calm while still making the system’s readiness
obvious. A beautiful mockup, a compiling binary, or a service that merely
starts is not sufficient on its own.
