# Sasayaki RPM packaging

Sasayaki ships as a single static Go binary plus a small private Python
runtime that `sasayaki setup` provisions in the user's home directory. The
RPM therefore only installs the binary; the runtime, model, and user service
are created per-user on first run.

System-level tools are declared as package dependencies so a user gets a
fully working install from one `dnf install`:

- `python3` — interpreter for the private sherpa-onnx runtime
- `pulseaudio-utils` — `parecord` (microphone capture)
- `wl-clipboard` — `wl-copy` (Wayland clipboard)
- `wtype` — Wayland virtual-keyboard paste backend
- `xclip` — X11 clipboard fallback (paste to XWayland windows)
- `xdotool` — X11 paste backend fallback
- `ydotool` — uinput paste backend fallback
- `systemd` — user service for the background recorder/transcriber
  (`systemctl --user`)

## Building

```sh
# from the repository root
make dist/rpm
```

This invokes `scripts/build-rpm.sh`, which builds the binary with
`-trimpath` and reproducible `-ldflags`, then runs `rpmbuild` with
`packaging/rpm/sasayaki.spec`. Set `RPMBUILD_OPTS` to pass extra flags
(e.g. `--define 'debug_package %{nil}'`).

Requires: `rpm-build`, `rpmdevtools` (for `rpmdev-setuptree`).

## Installation

```sh
dnf install ./sasayaki-*.rpm
```

The package drops the binary into `/usr/bin/sasayaki`. It does **not** run
setup — that is a per-user, interactive step:

```sh
sasayaki setup    # first run: python venv + model + user service
```

`setup` is idempotent; `sasayaki repair` re-runs the same steps and also
refreshes desktop integration where present.

## Upgrades

The RPM owns only `/usr/bin/sasayaki`. User data lives under
`~/.config/sasayaki`, `~/.local/share/sasayaki`, `~/.local/state/sasayaki`,
and `~/.config/systemd/user/sasayaki.service`, none of which the package
touches. Upgrading the RPM never disturbs an existing installation; run
`dnf upgrade` then `sasayaki repair` to refresh the private runtime after a
binary update if desired.

## Design notes

- **No root-owned state.** Everything mutable lives under the user's XDG
  directories and the systemd user unit. The package installs no service
  files, no config, no Python packages, no model files.
- **Dependencies are the hard floor.** Without a paste backend the program
  still works (clipboard-only, honestly reported); the dependencies above
  are what make the default experience automatic.
- **Architecture.** The `sherpa-onnx` wheel is architecture-specific; setup
  installs the wheel matching the user's CPU at first run, so the RPM does
  not need per-arch Python packaging.
