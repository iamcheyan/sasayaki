#!/usr/bin/env bash
# Build a .deb for sasayaki without a full dpkg-buildpackage setup.
# Produces dist/deb/sasayaki_<version>_<arch>.deb.
set -euo pipefail

cd "$(dirname "$0")/.."
version="${VERSION:-1.0.0}"
command -v dpkg-deb >/dev/null 2>&1 || { echo "error: dpkg-deb not found; install dpkg-dev" >&2; exit 1; }
arch="$(dpkg --print-architecture 2>/dev/null || uname -m)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
pkgdir="$work/sasayaki"
mkdir -p "$pkgdir/DEBIAN" "$pkgdir/usr/bin"

# control: expand Variables and merge the shared dependency list.
{
  sed -e "s/@VERSION@/$version/g" packaging/deb/control
} > "$pkgdir/DEBIAN/control"

go build -trimpath -ldflags="-s -w" -o "$pkgdir/usr/bin/sasayaki" ./cmd/sasayaki

mkdir -p dist/deb
dpkg-deb --build --root-owner-group "$pkgdir" "dist/deb/sasayaki_${version}_${arch}.deb"
echo "DEB built:"
ls -1 dist/deb/*.deb
