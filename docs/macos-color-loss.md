# macOS TUI color loss

## Symptom

On macOS, the Bubble Tea TUI launched from the menu-bar app's "Control Center
(TUI)" menu item renders in black and white — no violet title, no green READY
badge, no colored panel borders — even though the same Go code shows full
truecolor on Linux.  The terminal is kitty, which supports 24-bit color.

## Root cause

**Three environment variables leak from the launching context into the
terminal session and disable color detection in lipgloss/termenv.**

The menu-bar app (`Sasayaki.app`) is a GUI process.  When it is launched from
a shell that sets CI/agent-related env vars (e.g. an agent harness, a CI
runner), it inherits them.  When the menu-bar app then launches kitty to run
the TUI, kitty inherits the dirty env.  On macOS, GUI apps are single-instance
by default: the new kitty process delegates to the already-running kitty
instance, which opens a new window using *its own* environment (the one it
captured when it was started — including the leaked vars).  The Swift-side
env cleaning in `launchInTerminal` does not fully prevent this because the
existing kitty instance, not the freshly launched process, owns the child
window's environment.

### The three vars

| Variable     | Effect in termenv                                                    |
|--------------|----------------------------------------------------------------------|
| `NO_COLOR`   | `EnvNoColor()` returns true → `EnvColorProfile()` returns `Ascii`.  |
| `CLICOLOR=0` | Same as above (combined with `NO_COLOR` being unset).               |
| `CI`         | `isTTY()` returns false even when stdout is a real TTY → `ColorProfile()` returns `Ascii`. |

`CI` is the sneakiest: `isatty.IsTerminal(os.Stdout.Fd())` returns true
(stdout *is* a PTY), but termenv checks `CI` *before* checking `isatty` and
short-circuits to "not a TTY".  This means clearing `NO_COLOR` alone is not
enough — `CI` independently forces `Ascii`.

### Inheritance chain

```text
agent harness / CI shell
  sets NO_COLOR=1, CI=true, TERM=dumb
       │
       ▼  open dist/Sasayaki.app  (inherits env)
Sasayaki.app (Swift menubar)
  inherits NO_COLOR=1, CI=true
       │
       ▼  Process → kitty --title … sasayaki  (env passed to kitty)
kitty (new process)
  macOS single-instance: delegates to existing kitty (PID from earlier)
       │
       ▼  existing kitty opens new window
kitty (existing instance, env from when it was first started)
  still has NO_COLOR=1, CI=true
       │
       ▼  runs sasayaki binary in new PTY
sasayaki (Go TUI)
  termenv detects: NO_COLOR=1 → Ascii, CI=true → not a TTY → Ascii
  result: grayscale TUI
```

## Fix

Two layers, defense in depth:

### 1. Go TUI (`internal/tui/tui.go`, `Run`)

The reliable fix.  Before `tea.NewProgram` is created (which triggers the
first lipgloss render and color-profile detection), check if stdout is a real
TTY.  If so, the TUI is running inside an interactive terminal and the
inherited env vars are noise — clear them.

```go
func Run(paths config.Paths) error {
    if isatty.IsTerminal(os.Stdout.Fd()) {
        os.Unsetenv("NO_COLOR")
        os.Unsetenv("CLICOLOR")
        os.Unsetenv("CI")
    }
    program := tea.NewProgram(New(paths), tea.WithAltScreen())
    _, err := program.Run()
    return err
}
```

`os.Unsetenv` affects subsequent `os.Getenv` calls (termenv reads the
environment dynamically via `os.Getenv`, not from a cached snapshot), so the
color profile detection that happens during the first `View()` render sees a
clean environment.

A user who genuinely wants no color can set `NO_COLOR` in their shell profile
(`.zshrc`), which is sourced *after* the inherited env is applied — or use a
future `--no-color` flag if one is added.

### 2. Swift menu-bar app (`mac/StatusBar.swift`, `launchInTerminal`)

Belt-and-suspenders.  Strip the same vars from the env passed to the kitty
`Process`, so even a binary that predates the Go-side fix gets a clean
environment.  Also strips `KITTY_*` vars to discourage single-instance
delegation (though this is not fully effective on macOS).

### 3. Go CLI launcher (`cmd/sasayaki/main.go`, `startDetached`)

The `launchInTerminalDarwin` path (used when `sasayaki` is run without args and
stdin is not a TTY) also cleans the env via `cleanColorEnv` before starting
the terminal emulator.

## Debugging methodology

1. **Screenshot pixel analysis** — `PIL` + `numpy` to count non-gray pixels
   and match theme hex values (`#a78bfa`, `#6ee7b7`, `#fcd34d`).  The
   black-and-white TUI had 0 non-gray pixels; the fixed TUI had ~700K.

2. **Env capture wrapper** — temporarily replace the app-bundle `sasayaki`
   binary with a shell script that dumps `env` to a file before `exec`ing the
   real binary.  This revealed `NO_COLOR=1`, `CI=true`, and `TERM=dumb` inside
   the kitty session.

3. **Go debug logging** — temporarily write `TERM`, `COLORTERM`, `NO_COLOR`,
   `CI`, and `isatty.IsTerminal` result to a file at the top of `tui.Run`.
   This confirmed the variables were present and that stdout was a real TTY.

4. **PTY color test** — a minimal Go program using lipgloss/termenv, run
   inside a `pty.Start` with controlled env vars, to isolate which
   combination triggers `Ascii` vs `TrueColor`.