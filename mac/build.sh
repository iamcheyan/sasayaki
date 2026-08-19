#!/bin/sh
# Build the macOS menu bar app bundle for Sasayaki.
# Output: dist/Sasayaki.app (Contents/MacOS: sasayaki-menubar + sasayaki).
#
# Env knobs:
#   SIGN_MODE=adhoc  — ad-hoc sign instead of mac/sign.sh's stable cert
#                      (used in CI, where the self-signed identity is absent).
#   GOARCH           — Go target arch (default: host). Matches the runner.
set -eu
cd "$(dirname "$0")/.."

mkdir -p build
HOST_ARCH="$(uname -m)"
[ "$HOST_ARCH" = "x86_64" ] && HOST_ARCH=amd64
CGO_ENABLED=0 GOARCH="${GOARCH:-$HOST_ARCH}" \
  go build -trimpath -ldflags="-s -w" -o build/sasayaki ./cmd/sasayaki
swiftc -O -o build/sasayaki-menubar mac/StatusBar.swift

APP="dist/Sasayaki.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp build/sasayaki-menubar build/sasayaki "$APP/Contents/MacOS/"
chmod +x "$APP/Contents/MacOS/"*

cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Sasayaki</string>
  <key>CFBundleDisplayName</key><string>语音输入</string>
  <key>CFBundleIdentifier</key><string>io.github.iamcheyan.sasayaki.menubar</string>
  <key>CFBundleVersion</key><string>1.0.0</string>
  <key>CFBundleShortVersionString</key><string>1.0.0</string>
  <key>CFBundleExecutable</key><string>sasayaki-menubar</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSMicrophoneUsageDescription</key><string>语音输入需要访问麦克风进行语音转文字</string>
</dict>
</plist>
PLIST

# Re-sign with the stable identity last — see mac/sign.sh. Ad-hoc signing
# would orphan TCC grants (microphone, accessibility) on every rebuild.
# CI builds (SIGN_MODE=adhoc) have no local cert, so ad-hoc sign there;
# consumers re-grant TCC once per download, or run mac/sign.sh locally for
# a stable grant.
if [ "${SIGN_MODE:-cert}" = "adhoc" ]; then
  codesign --force --deep -s - "$APP"
  echo "adhoc-signed: $APP"
else
  "$(dirname "$0")/sign.sh"
fi

echo "built: $APP"
echo "launch: open $APP"
