# Sasayaki DEB packaging

Debian/Ubuntu packaging for Sasayaki. The package installs only the single
Go binary; the private Python runtime, speech model, and systemd user
service are provisioned per-user by `sasayaki setup` on first run.

System-level tools are declared as dependencies so `apt install sasayaki`
yields a fully working install:

- `python3` — interpreter for the private sherpa-onnx runtime
- `pulseaudio-utils` — `parecord` (microphone capture)
- `wl-clipboard` — `wl-copy` (Wayland clipboard)
- `wtype` — Wayland virtual-keyboard paste backend
- `xclip` — X11 clipboard fallback (paste to XWayland windows)
- `xdotool` — X11 paste backend fallback
- `ydotool` — uinput paste backend fallback

## Building

```sh
# from the repository root
make dist/deb
```

This invokes `scripts/build-deb.sh`, which builds the binary and assembles
a Debian package tree under `dist/deb/` using `dpkg-deb`. Requires `golang-go`
and `dpkg-dev`.

For a proper source package with `debian/` metadata, copy `packaging/deb/`
to `debian/` and run `dpkg-buildpackage`:

```sh
mkdir debian && cp packaging/deb/control debian/
dpkg-buildpackage -us -uc
```

## Installation

```sh
apt install ./sasayaki_*.deb
```

Then, once per user:

```sh
sasayaki setup
```

`setup` is idempotent; `sasayaki repair` re-runs the same steps and also
refreshes desktop integration where present.

## Upgrades

The package owns only `/usr/bin/sasayaki`. User data lives under
`~/.config/sasayaki`, `~/.local/share/sasayaki`, `~/.local/state/sasayaki`,
and `~/.config/systemd/user/sasayaki.service`, none of which the package
touches. Upgrading never disturbs an existing installation.

## Design notes

- **No root-owned state.** Everything mutable lives under the user's XDG
  directories and the systemd user unit.
- **Dependencies are the hard floor.** Without a paste backend the program
  still works (clipboard-only, honestly reported); the dependencies above
  are what make the default experience automatic.
- **Architecture.** The `sherpa-onnx` wheel is architecture-specific; setup
  installs the wheel matching the user's CPU at first run.
