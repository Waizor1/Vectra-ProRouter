#!/bin/sh
# Assemble a REAL .ipk from the package payload and install it with the real
# opkg, so postinst/prerm run the way they do on a router.
#
#   build-ipk.sh <data-tree> <control-dir> <out.ipk>
#
# Why hand-assembled and not the SDK's: the published .ipk is built for
# aarch64_cortex-a53 and predates the code under test (see README). What must be
# exercised here is the *maintainer scripts* and procd's start path, and those
# are byte-identical — run.sh extracts postinst/prerm straight out of
# openwrt/Makefile rather than keeping a second copy.
#
# Outer container: gzipped tar. opkg 24.10 (libarchive-based) accepts both that
# and the `ar` form, and busybox has no `ar` applet, so tar it is. Verified in
# the stand: install runs postinst, remove runs prerm, files land and are
# removed at their real paths.
set -eu

DATA="$1"
CTRL="$2"
OUT="$3"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

( cd "$DATA" && tar czf "$WORK/data.tar.gz" ./* )
( cd "$CTRL" && tar czf "$WORK/control.tar.gz" ./* )
echo "2.0" > "$WORK/debian-binary"

( cd "$WORK" && tar czf "$OUT" ./debian-binary ./control.tar.gz ./data.tar.gz )
