#!/bin/sh
# Stable code-signing identity for the menubar app.
#
# TCC grants (microphone, accessibility) bind to the app's code signature.
# With ad-hoc signing the grant is tied to the binary's cdhash and DIES on
# every rebuild — the user re-grants forever. A fixed self-signed identity
# makes the grant survive rebuilds: sign once, grant once.
set -eu
cd "$(dirname "$0")/.."

IDENTITY="sumika-voice-dev"
APP="dist/Sasayaki.app"

if ! security find-certificate -c "$IDENTITY" 2>/dev/null | grep -q "labl"; then
  TMP="$(mktemp -d)"
  openssl req -newkey rsa:2048 -nodes -keyout "$TMP/key.pem" \
    -x509 -days 3650 -out "$TMP/cert.pem" \
    -subj "/CN=$IDENTITY" \
    -addext "keyUsage=critical,digitalSignature" \
    -addext "extendedKeyUsage=codeSigning" 2>/dev/null
  # NB: openssl 3's PBES2 PBE breaks macOS PKCS12 import — import the key
  # and certificate separately as PEM instead.
  security import "$TMP/key.pem" \
    -k "$HOME/Library/Keychains/login.keychain-db" \
    -T /usr/bin/codesign -T /usr/bin/security >/dev/null
  security import "$TMP/cert.pem" \
    -k "$HOME/Library/Keychains/login.keychain-db" \
    -T /usr/bin/codesign -T /usr/bin/security >/dev/null
else
  echo "identity present: $IDENTITY"
fi

# Sign by SHA-1 hash: identical-CN identities would be ambiguous by name.
CERT_SHA="$(security find-certificate -c "$IDENTITY" -a -Z 2>/dev/null | awk '/SHA-1/{print $3; exit}')"
[ -n "$CERT_SHA" ] || { echo "identity not found after creation" >&2; exit 1; }
# NOTE: no pinned -r here — for cert-signed binaries the default DR is
# already "identifier + certificate leaf hash" (no cdhash), which stays
# stable across every rebuild signed with this same cert. The one gotcha:
# TCC entries granted to an EARLIER signature (e.g. ad-hoc) keep their old
# cdhash DR even when toggled off/on — such entries must be deleted (−) and
# re-added (+) once to snapshot the stable DR.
ENTITLEMENTS="$(dirname "$0")/entitlements.plist"
codesign --force --deep --options runtime \
  --entitlements "$ENTITLEMENTS" -s "$CERT_SHA" "$APP" 2>/dev/null \
  || codesign --force --deep -s "$CERT_SHA" "$APP"
echo "signed: $APP"
codesign -dv "$APP" 2>&1 | grep -E "Identifier|Authority|TeamIdentifier" | head -3
