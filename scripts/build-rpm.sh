#!/usr/bin/env bash
# Build an RPM for sasayaki. Produces dist/rpm/*.rpm.
#
# The spec installs only /usr/bin/sasayaki; user state is provisioned by
# `sasayaki setup`. Pass extra rpmbuild flags via RPMBUILD_OPTS.
set -euo pipefail

cd "$(dirname "$0")/.."
version="${VERSION:-1.0.0}"
topdir="$(mktemp -d)"
trap 'rm -rf "$topdir"' EXIT

mkdir -p "$topdir"/{BUILD,BUILDROOT,RPMS,SOURCES,SPECS,SRPMS}

# Source tarball with the exact layout %setup expects: sasayaki-<version>/
git archive --format=tar.gz --prefix="sasayaki-$version/" -o "$topdir/SOURCES/sasayaki-$version.tar.gz" HEAD

rpmbuild \
  --define "_topdir $topdir" \
  --define "sasayaki_version $version" \
  ${RPMBUILD_OPTS:-} \
  -bb packaging/rpm/sasayaki.spec

mkdir -p dist/rpm
cp "$topdir"/RPMS/noarch/*.rpm "$topdir"/RPMS/*/*.rpm dist/rpm/ 2>/dev/null || true
echo "RPM built:"
ls -1 dist/rpm/*.rpm
