# Standalone usage (no Sumika Shell)

Sasayaki is a standalone program. The Sumika Shell desktop integration is an
optional add-on; without it you lose the taskbar icon, popup, and automatic
key bindings, but the core pipeline — record, transcribe, paste — works
identically.

This guide covers using Sasayaki on any Linux desktop: GNOME, KDE, i3, Sway,
Xfce, a bare window manager, or no desktop at all (a headless machine with a
sound server).

## What it needs

System tools (installed by your distribution's package manager):

| Tool | Purpose | Package (Fedora / Debian) |
|---|---|---|
| `python3` | private sherpa-onnx runtime | `python3` / `python3` |
| `parecord` | microphone capture | `pulseaudio-utils` / `pulseaudio-utils` |
| `wl-copy` | Wayland clipboard | `wl-clipboard` / `wl-clipboard` |
| `wtype` | Wayland paste (preferred) | `wtype` / `wtype` |
| `xclip` | X11 clipboard (XWayland paste) | `xclip` / `xclip` |
| `xdotool` | X11 paste fallback | `xdotool` / `xdotool` |
| `ydotool` | uinput paste fallback | `ydotool` / `ydotool` |
| systemd | user service (`systemctl --user`) | `systemd` / `systemd` |

If you install the distro package (see `packaging/rpm` and `packaging/deb`),
these come automatically. If you install the binary by hand, install the
tools yourself — `sasayaki diagnose` reports exactly what is missing and how
to install it.

> Paste backends are the only "soft" dependency: without `wtype`/`ydotool`/
> `xdotool` the result is still copied to the clipboard and Sasayaki
> honestly reports that you must paste manually.

## First run

```sh
sasayaki setup
```

This provisions, all in Sasayaki's private directories:

1. prerequisite check (tools, disk space, network only when a download is
   needed) — never modifies anything;
2. private directories (`~/.config/sasayaki`, `~/.local/share/sasayaki`, …);
3. `config.json`;
4. the embedded `engine.py`;
5. a private Python venv with pinned `sherpa-onnx`;
6. the selected speech model (verified by SHA-256);
7. a systemd **user** unit `sasayaki.service` and starts it.

It never touches your system Python, and needs no root. Re-run it any time
to repair; `sasayaki repair` also re-applies desktop integration where
present and verifies with diagnostics.

## Triggering

Sasayaki is driven by the short-lived commands below — bind them to
keyboard shortcuts in **your** desktop's settings:

```sh
sasayaki toggle            # press once: record; press again: transcribe + paste
sasayaki translate-toggle  # same, but translate the result before pasting
sasayaki cancel            # cancel an active recording/transcription
```

`toggle` is a toggle: the first press starts recording, the second stops and
pastes. There is no separate "stop" command.

A CapsLock tap can drive the same command (see
[architecture](architecture.md) / [wake-keys.md](wake-keys.md) for
how Sumika wires it); on other desktops bind `sasayaki toggle` to
`CapsLock` yourself — release-only if your compositor supports it, so
chords like `Ctrl+C` (when you swap ctrl/caps) still reach apps. If you want cancel-on-`Escape`
like the Sumika integration, bind `sasayaki cancel` to `Escape` in your
desktop (Hyprland users get it automatically).

### GNOME

Settings → Keyboard → View and Customize Shortcuts → Custom Shortcuts → add:

```sh
gsettings set org.gnome.settings-daemon.plugins.media-keys custom-keybindings \
  "['/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/sasayaki-toggle/']"
gsettings set org.gnome.settings-daemon.plugins.media-keys.custom-keybinding:/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/sasayaki-toggle/ name 'Sasayaki toggle'
gsettings set org.gnome.settings-daemon.plugins.media-keys.custom-keybinding:/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/sasayaki-toggle/ command 'sasayaki toggle'
gsettings set org.gnome.settings-daemon.plugins.media-keys.custom-keybinding:/org/gnome/settings-daemon/plugins/media-keys/custom-keybindings/sasayaki-toggle/ binding '<Alt>a'
```

Repeat for `sasayaki translate-toggle` with a second entry.

### KDE Plasma

System Settings → Shortcuts → Add New → Global Shortcut → Command/URL.
Enter `sasayaki toggle` as the command and assign a key.

### i3 / Sway

```text
bindsym $mod+a exec sasayaki toggle
bindsym $mod+Shift+a exec sasayaki translate-toggle
```

### Hyprland

`sumika-action sasayaki.toggle` works only with Sumika; standalone users
bind the binary directly:

```text
bind = ALT, A, exec, sasayaki toggle
bind = ALT, SHIFT, A, exec, sasayaki translate-toggle
```

### Focused-window resolution

Sasayaki picks the paste shortcut from the focused window's class, resolved
per compositor (first one that reports a window wins):

- **Hyprland** — `hyprctl activewindow`;
- **Sway** — `swaymsg get_tree`;
- **KDE Plasma** — KWin scripting over D-Bus (`qdbus6`/`qdbus`);
- **GNOME** — the third-party `window-calls-extended` shell extension
  (GNOME Wayland has no official focus API; without the extension the
  generic shortcut is used);
- **Any X11 session** (i3, Xfce, KDE/GNOME on X11, …) — the EWMH
  `_NET_ACTIVE_WINDOW` root property via `xprop`.

Where no backend reports a window, Sasayaki falls back to the generic
`Ctrl+V` chord.

### Wayland vs X11 paste paths

- **Wayland** (GNOME, KDE, Sway, …): pasting uses the Wayland virtual
  keyboard (`wtype`, fallback `ydotool`). XWayland apps get the X11 path
  (`xsel`/`xclip` + `xdotool`).
- **X11**: `xdotool` injects into the focused window.

In terminals, `Ctrl+V` is a literal control character and will not paste;
terminals are detected by class (foot, kitty, alacritty, …) and get
`Shift+Insert`/`Ctrl+Shift+V` instead. Where the class stays unknown (no
backend installed, or a desktop without a focus API), use a TUI test
(`sasayaki`, then `t`) to see the raw result, or bind a terminal that
accepts `Ctrl+V` paste.

## Control center (TUI)

```sh
sasayaki            # stdin is a terminal → interactive control center
sasayaki shortcut   # desktop-specific shortcut instructions
```

From a GUI file manager / launcher (stdin not a TTY), bare `sasayaki`
auto-opens the TUI in a terminal emulator (`$TERMINAL`, `xdg-terminal-exec`,
then kitty/foot/alacritty/…).

Key TUI shortcuts: `t` speech test, `T` translation test, `s` setup,
`r` full repair, `d` diagnostics, `?` help.

## Status and diagnostics

```sh
sasayaki status --json   # machine-readable snapshot for scripts/widgets
sasayaki diagnose        # full dependency report with fixes
sasayaki logs            # follow the service log
```

`status --json` fields: `service`, `phase`, `runtime`, `model`, `microphone`,
`paste`, `paste_backend`, `speech_model`, `language`, `translation`,
`mic_level`, `worker`, `last_result`, `last_error`, `last_phase`, `last_at`.
Phase values: `idle`, `recording`, `transcribing`, `translating`, `pasting`,
`succeeded`, `failed`.

## Translation

`translate-toggle` works only when translation is enabled and configured:

```sh
sasayaki translation configure \
  --base-url https://example.com/v1 --model your-model \
  --target Japanese --api-key "$KEY"
sasayaki translation test "hello"
```

The translation binding is an explicit, separate action. A plain `toggle`
never translates, and `translate-toggle` fails fast (instead of silently
degrading) when translation is disabled.

## Headless / no graphical session

A machine with a sound server but no display can still run the service and
record/transcribe; only pasting needs a display. Useful for scripting:

```sh
sasayaki toggle   # records; result is pasted (no-op without display) and logged
sasayaki status   # read the last result via last_result
```

Enable `verbose_transcripts` in `~/.config/sasayaki/config.json` to log full
transcripts to the journal for scripted use.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `toggle` prints "service is not running" | `sasayaki service start` (first run: `sasayaki setup`) |
| `diagnose` shows a failed check | the `fix` field names the command |
| Recording starts but nothing is pasted | install a paste backend (`wtype`/`ydotool`/`xdotool`); text is on the clipboard |
| No audio / empty recording | `pactl list short sources`; check the input device is unmuted |
| `translate-toggle` records but doesn't translate | translation disabled/not configured; see Translation |
