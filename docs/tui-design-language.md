# Sasayaki TUI design language

Sasayaki belongs to the same family as Shirabe: calm, focused terminal tools
that make a small number of system actions feel trustworthy.

## Principles

1. Show the current state before offering an action. A person should be able to
   tell whether the voice service, model, microphone and paste path are ready
   without opening a dialog.
2. Keep the main screen to two equal-height cards: **Voice** for the immediate
   recording action and **Runtime** for operational state. Details, logs and
   desktop-specific instructions live behind explicit overlays or commands.
3. Treat the terminal as responsive. At comfortable widths the cards sit side
   by side; below that threshold they stack. Never leave desktop-width gaps on
   a narrow terminal.
4. Use one quiet hierarchy: near-black background, soft violet for structure
   and focus, mint for healthy state, amber for attention, and muted gray for
   explanation. Color never carries meaning alone.
5. Draw thin rounded borders around distinct cards. Card titles sit in the top
   border; inner sections use a small `◆` marker rather than a tall vertical
   bar that can collide with nearby text.
6. Reserve the footer for keys and transient feedback. It is centered under the
   cards with a blank row above it. A successful action replaces its normal key
   hints briefly, then restores them; it never needs a disruptive success
   dialog.

## Layout

```text
  ✦ sasayaki   voice input                                  ● READY
  Local speech, pasted where you are typing

  ╭ VOICE ───────────────╮  ╭ RUNTIME ──────────────────────╮
  │ ◆ RECORDING          │  │ ◆ SERVICE                     │
  │   Ready to listen    │  │   ● Running                   │
  │                      │  │                               │
  │ ◆ SHORTCUT           │  │ ◆ LOCAL ENGINE                │
  │   sasayaki toggle    │  │   SenseVoice · installed      │
  ╰──────────────────────╯  ╰───────────────────────────────╯

            [T] toggle  [S] setup  [B] shortcut  [?] help
```

The header, cards and footer are constrained to a shared maximum width. There
is exactly one terminal row between header and cards, and one row between cards
and footer. Both cards use the same calculated height even if one has shorter
content.

## Interaction

- Arrow keys move through actionable rows spatially. `Tab` is a secondary
  focus switch, not the only navigation path.
- Enter activates the focused harmless action. Recording, service stop, and
  other consequential operations have explicit letter keys too.
- Destructive or service-changing operations use unambiguous keys (`D` to
  disable/stop), never an Enter-focused default.
- `?` opens help; `Esc` closes an overlay. Shortcut setup is explanatory,
  because desktop environments own global keyboard bindings.
- Status messages use Bubble Tea ticks and disappear after a short delay.

## Implementation notes

Keep rendering separate from service and desktop integration. A pure layout
function should calculate every hit area and focus target from terminal size;
keyboard and mouse navigation use that same result. Service calls should return
small state snapshots so the renderer can remain deterministic.
