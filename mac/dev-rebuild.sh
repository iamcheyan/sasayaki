#!/bin/sh
# Fast iterative rebuild for the macOS menu bar app.
#
# Recompiles ONLY the Swift menubar binary and re-signs the existing bundle
# with the stable self-signed identity (mac/sign.sh). The Go `sasayaki` CLI
# is copied through unchanged when its build/ copy is present — no `go build`
# on every keystroke.
#
# Why re-sign every time: the Accessibility TCC grant (auto-paste via CGEvent
# Cmd+V) binds to the bundle's code signature. A bare swiftc binary is
# adhoc/linker-signed with a cdhash designated requirement, so the grant DIES
# on every recompile. Re-signing with the fixed cert yields a cdhash-free DR
# (identifier + certificate leaf hash) that survives rebuilds — grant once,
# paste forever.
#
# Usage: sh mac/dev-rebuild.sh   (recompiles Swift + re-signs + relaunches)
set -eu
cd "$(dirname "$0")/.."

APP="dist/Sasayaki.app"

# If the bundle or the Go CLI is missing, do a full clean build first.
if [ ! -d "$APP" ] || [ ! -f "$APP/Contents/MacOS/sasayaki" ]; then
  echo "full build needed (bundle or Go CLI missing)"
  sh mac/build.sh
  exit 0
fi

echo "compiling Swift menubar binary…"
mkdir -p build
swiftc -O -o build/sasayaki-menubar mac/StatusBar.swift

echo "updating bundle binaries…"
cp build/sasayaki-menubar "$APP/Contents/MacOS/sasayaki-menubar"
# Refresh the Go CLI too if build/sasayaki is newer than the bundled copy.
if [ build/sasayaki -nt "$APP/Contents/MacOS/sasayaki" ] 2>/dev/null; then
  cp build/sasayaki "$APP/Contents/MacOS/sasayaki"
fi
chmod +x "$APP/Contents/MacOS/"*

echo "re-signing with stable identity…"
sh mac/sign.sh

# Relaunch: kill any running instance, then open the freshly signed bundle.
pkill -x sasayaki-menubar 2>/dev/null || true
# A brief pause after pkill: `open` racing the kill returns -600
# (procNotFound) on macOS LaunchServices.
sleep 0.5