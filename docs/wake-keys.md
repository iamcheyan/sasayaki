# Voice Wake Keys

Tap one of the enabled keys — **CapsLock, LeftCtrl, RightCtrl** — to toggle
voice input. Each key is an independent switch: enable any combination, or
none at all. In every keyboard state: stock layout, any XKB layout (`jp`,
`us`, …), or with a keyd ctrl↔caps swap active. Remaps keep working exactly
as before; wake is purely additive.

```text
Wake key OFF ── nothing changes for that key
Wake key ON ── a bare tap of that key toggles voice input
               (press + release with no other key in between)
```

Chords are never affected: `Ctrl+C`, `Ctrl+A`, … fire normally because
another key was pressed while the modifier was held. Only a completed bare
tap wakes.

## Where it lives

| Layer | File | Role |
|---|---|---|
| Setting | `~/.config/sasayaki/config.json` → `wake_keys` | `{capslock, leftctrl, rightctrl}` bools, source of truth |
| CLI | `sasayaki wake capslock\|leftctrl\|rightctrl on\|off\|toggle\|status` | mutate + refresh Hyprland and keyd |
| CLI | `sasayaki wake status` | print the whole matrix, `<key> <on\|off>` per line |
| TUI | menu item 9 "Voice Wake Keys" (`internal/tui`) | three-row matrix, cursor or number keys toggle |
| Bar menu | `bar/SasayakiContextMenu.qml` | three checkable items |
| Hyprland | `hypr/bindings.lua` (Sumika repo) | registers the actual key binds |
| keyd | Sumika `keyboard-remap` extension | caps-position overload, only while a swap is active |

Legacy configs with the old top-level `caps_lock_wake` bool are migrated on
load: it maps onto `wake_keys.capslock`. The old `sasayaki capslock …` CLI
spelling remains as an alias of `sasayaki wake capslock …`.

## How a tap reaches `sasayaki toggle`

`sasayaki bindings` prints one `voicetap` line per enabled wake key (in
addition to the regular `voice` / `translation` lines):

```
voicetap	code:66
voicetap	F24
voicetap	code:37
voicetap	code:105
```

Sumika's `bindings.lua` turns every `voicetap` into a release-only bind on
`sasayaki.toggle`:

- **`code:66`** — the evdev keycode of the CapsLock key (CapsLock wake only).
  Keycodes identify *hardware keys*, not keysyms, so it survives every XKB
  remap (including `kb_options = compose:caps`). This bind is
  **transparent**: the press still reaches clients, so the key keeps its
  normal CapsLock/Compose behavior.
- **`F24`** — emitted only on the caps *position* while the keyboard-remap
  ctrl-caps-swap preset is active with CapsLock wake on (below). Consumed by
  the bind (no application expects F24), so a tap does nothing else.
- **`code:37`** — the evdev keycode of the physical left Ctrl key (LeftCtrl
  wake). Transparent like code:66: chords still reach clients, only the bare
  tap fires.
- **`code:105`** — the physical right Ctrl key (RightCtrl wake), same
  semantics.

All binds are `release = true`: they fire when the key is released without
any other key intervening — i.e. exactly on a completed bare tap.

## The keyd overload (swap active)

Sumika's keyboard-remap extension renders its keyd config from user
profiles. The ctrl-caps-swap preset normally produces:

```ini
[main]
leftcontrol = capslock
capslock    = leftcontrol
```

With CapsLock wake enabled the second line becomes:

```ini
capslock    = overload(control, f24)
```

`overload` gives the caps-position key two roles, decided by *usage*:

- **held with another key** → the `control` layer: a real Ctrl for chords;
  nothing else changes;
- **pressed and released alone** → emits `F24`, which the Hyprland bind
  above turns into the voice toggle.

Notes:

- The first argument of `overload()` is a *layer* name (`control`), not a
  key name — `overload(leftcontrol, …)` parses as a warning and silently
  degrades to a plain modifier.
- The plain swap output is byte-identical when sasayaki is absent or wake
  is off, so the preset stays a general-purpose feature for machines that
  never install sasayaki.
- `leftcontrol = capslock` (the other half of the swap) is untouched, so
  the bottom-left key still acts as CapsLock — and its keycode (66) is
  exactly what the transparent bind above listens for.
- LeftCtrl/RightCtrl wake never touches keyd: their binds are plain
  keycode binds, orthogonal to any remap.

## Matrix

| State | Tap caps key (A-above) | Tap bottom-left key | Tap left/right Ctrl | Chords |
|---|---|---|---|---|
| No swap, caps wake on | wakes voice (+ caps state flips, transparent) | plain Ctrl | wakes voice if that key's wake is on (transparent) | work |
| Swap on, caps wake off | — | toggles caps | wakes voice if that key's wake is on (transparent) | work (plain swap) |
| Swap on, caps wake on | wakes voice (F24, clean) | wakes voice (code:66, + caps state flips) | wakes voice if that key's wake is on (transparent) | work (`control` layer) |
| All wake keys off | — | — | — | work |

## Switching keys on/off

Every entry point re-renders keyd and reloads Hyprland bindings, so the
change is effective immediately:

```sh
sasayaki wake status                 # whole matrix
sasayaki wake capslock toggle        # one key
sasayaki wake leftctrl on
sasayaki wake rightctrl off
```

The TUI (menu → *Voice Wake Keys*) and the taskbar context menu items call
the same code path. Toggling is a server-side flip, so UI state races
cannot send the wrong direction.
