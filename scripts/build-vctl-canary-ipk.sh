#!/usr/bin/env bash

set -euo pipefail

# Build vectra-controller-pro (vctl) with the OpenWrt SDK on the VPS and stage
# the .ipk to the CANARY artifact path.
#
# Deliberately NOT scripts/build-vectra-openwrt-feed.sh: that one publishes a
# signed opkg feed. The fleet feed at artifacts/openwrt/stable/... is what all
# 30 routers install the controller from, and `sync-runtime-artifacts.sh` is
# `rsync -a --delete` — syncing a directory that holds only vctl would DELETE
# the controller packages fleet-wide. Canary needs no feed at all: the .ipk is
# fetched by direct URL. This script therefore never writes under
# artifacts/openwrt/ and never invokes the sync helper.
#
# Recipe notes, learned the hard way:
#   * The SDK's .config has no CONFIG_PACKAGE_vectra-controller-pro, so the ipk
#     is never emitted. Append the symbol, build, then restore .config
#     byte-identically.
#   * Do NOT use DEVELOPER=1 to force ipk emission. It is global and breaks on
#     package/toolchain in this musl SDK (libquadmath.so.* does not exist).
#   * VECTRA_PRO_SOURCE_DIR must point at the Go tree; the package Makefile
#     guards on $(VECTRA_PRO_SOURCE_DIR)/go.mod.

export COPYFILE_DISABLE=1

SSH_ALIAS="vectra-prod"
SDK_ROOT="/opt/openwrt-sdk-24.10.6-mediatek-filogic_gcc-13.3.0_musl.Linux-x86_64"
CANARY_DIR="/opt/vectra-prorouter/deploy/runtime/artifacts/canary/vctl"
REMOTE_SRC="/tmp/vctl-build"
BRANCH=""
VERSION=""

usage() {
	cat <<'EOF'
Build vctl .ipk with the OpenWrt SDK and stage it to the canary artifact path.

Usage:
  scripts/build-vctl-canary-ipk.sh --branch <git-branch> [options]

Required:
  --branch NAME        Git branch to ship to the builder (git archive)

Optional:
  --version VERSION    Override PKG_VERSION (default: read from openwrt/Makefile)
  --ssh-alias NAME     SSH alias of the build host (default: vectra-prod)
  --sdk-root PATH      SDK root on the build host
  --canary-dir PATH    Destination directory on the build host
  --help               Show this help

Never touches artifacts/openwrt/** and never runs sync-runtime-artifacts.sh.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
		--branch) BRANCH="$2"; shift 2 ;;
		--version) VERSION="$2"; shift 2 ;;
		--ssh-alias) SSH_ALIAS="$2"; shift 2 ;;
		--sdk-root) SDK_ROOT="$2"; shift 2 ;;
		--canary-dir) CANARY_DIR="$2"; shift 2 ;;
		--help) usage; exit 0 ;;
		*) echo "Unknown argument: $1" >&2; usage; exit 1 ;;
	esac
done

[ -n "$BRANCH" ] || { echo "--branch is required" >&2; usage; exit 1; }

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

git rev-parse --verify "$BRANCH" >/dev/null 2>&1 || {
	echo "No such branch: $BRANCH" >&2
	exit 1
}

echo "==> shipping $BRANCH to $SSH_ALIAS:$REMOTE_SRC"
ssh "$SSH_ALIAS" "rm -rf '$REMOTE_SRC' && mkdir -p '$REMOTE_SRC'"
git archive "$BRANCH" router/vectra-controller-pro | ssh "$SSH_ALIAS" "tar -x -C '$REMOTE_SRC'"

echo "==> building"
ssh "$SSH_ALIAS" "bash -s" <<REMOTE
set -euo pipefail
SDK="$SDK_ROOT"
SRC="$REMOTE_SRC/router/vectra-controller-pro"

[ -f "\$SRC/go.mod" ] || { echo "go.mod missing under \$SRC" >&2; exit 1; }

# Stage only the OpenWrt packaging into the SDK; the Go tree stays in /tmp and
# is reached through VECTRA_PRO_SOURCE_DIR, which keeps SDK scan depth happy.
rm -rf "\$SDK/package/vectra-controller-pro"
mkdir -p "\$SDK/package/vectra-controller-pro"
cp -a "\$SRC/openwrt/." "\$SDK/package/vectra-controller-pro/"

# The build needs the package selected; restore .config afterwards so the SDK
# is left exactly as found (nothing else selects this package).
cp -a "\$SDK/.config" "\$SDK/.config.vctl-backup"
trap 'cp -a "\$SDK/.config.vctl-backup" "\$SDK/.config"' EXIT
grep -q '^CONFIG_PACKAGE_vectra-controller-pro=' "\$SDK/.config" \
	|| echo 'CONFIG_PACKAGE_vectra-controller-pro=m' >> "\$SDK/.config"

VECTRA_PRO_SOURCE_DIR="\$SRC" make -C "\$SDK" package/vectra-controller-pro/compile -j\$(nproc) V=s 2>&1 | tail -25

IPK="\$(find "\$SDK/bin" -name 'vectra-controller-pro_*.ipk' -newermt '-30 minutes' | sort | tail -1)"
[ -n "\$IPK" ] || { echo "no freshly built ipk found" >&2; exit 1; }

mkdir -p "$CANARY_DIR"
install -m0644 "\$IPK" "$CANARY_DIR/\$(basename "\$IPK")"
echo "STAGED \$(basename "\$IPK")"
sha256sum "$CANARY_DIR/\$(basename "\$IPK")"
REMOTE

echo "==> verifying the fleet feed was not touched"
ssh "$SSH_ALIAS" "ls -l /opt/vectra-prorouter/deploy/runtime/artifacts/openwrt/stable/aarch64_cortex-a53/ | tail -5"

echo "==> done"
