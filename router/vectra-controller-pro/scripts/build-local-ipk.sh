#!/usr/bin/env bash
# Build an installable .ipk for the fleet's hardware (Xiaomi AX3000T and the
# other Filogic units: aarch64_cortex-a53, OpenWrt 24.10) straight from THIS
# checkout — for a hands-on canary, without the SDK and without the feed.
#
#   scripts/build-local-ipk.sh [out-dir]      (default: ./dist, gitignored)
#
# The payload, maintainer scripts, Depends and version come from
# openwrt/Makefile (scripts/lib/pkg.sh). The one difference from an SDK build:
# vctl is a static CGO_ENABLED=0 binary (the SDK links it against musl). Same
# code; the data-plane stand runs exactly this kind of build on OpenWrt 24.10
# userland. For every architecture at once, signed, see build-pro-feed.sh.
#
# It does NOT publish anything. Installing it is a manual, per-router step —
# see docs/CANARY.md for the order that keeps a way back at every point.
set -euo pipefail
# macOS tar would otherwise add AppleDouble (._*) entries for extended
# attributes — files opkg would install onto the router.
export COPYFILE_DISABLE=1

PKG_MODULE="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=lib/pkg.sh
. "$PKG_MODULE/scripts/lib/pkg.sh"
OUT="${1:-$PKG_MODULE/dist}"
ARCH="${IPK_ARCH:-aarch64_cortex-a53}"
full="$(pkg_version)"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
data="$work/data"
ctrl="$work/control"
mkdir -p "$data" "$ctrl" "$OUT"

echo "==> vctl $full for linux/arm64"
install -d "$data/usr/sbin"
pkg_build_vctl "$data/usr/sbin/vctl" arm64

echo "==> payload, as openwrt/Makefile installs it"
n="$(pkg_stage_payload "$data")"
echo "    $n files"
pkg_maintainer_scripts "$ctrl"

deps="$(pkg_depends)"
cat > "$ctrl/control" <<CTL
Package: vectra-controller-pro
Version: $full
Depends: $deps
Source: router/vectra-controller-pro
License: MIT
Section: net
Architecture: $ARCH
Maintainer: Vectra
Description: Vectra Controller Pro (xray-direct) with the router UI — canary build $(git -C "$PKG_MODULE" rev-parse --short HEAD 2>/dev/null || echo dev)
CTL
echo "/etc/config/vectra-controller-pro" > "$ctrl/conffiles"

ipk="$OUT/vectra-controller-pro_${full}_${ARCH}.ipk"
( cd "$PKG_MODULE" && go run ./tools/feedtool ipk -data "$data" -control "$ctrl" -out "$ipk" -mtime "$(pkg_commit_epoch)" )

echo "==> $ipk"
echo "    $(wc -c < "$ipk" | tr -d ' ') bytes, sha256 $(shasum -a 256 "$ipk" | awk '{print $1}')"
echo "    depends: $deps"
