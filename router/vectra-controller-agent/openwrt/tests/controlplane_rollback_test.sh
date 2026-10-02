#!/usr/bin/env bash
set -eu
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../../../.." && pwd)"
HELPER="$ROOT/router/vectra-controller-agent/openwrt/files/usr/libexec/vectra-controller/controlplane-direct.sh"
INSTALL="$ROOT/router/vectra-controller-agent/openwrt/files/etc/uci-defaults/93_vectra_controlplane_direct"
SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
mkdir -p "$SANDBOX/bin" "$SANDBOX/state"
export CP_STATE="$SANDBOX/state" FW_INCLUDE_PATH="$SANDBOX/include" DISABLED_PATH="$SANDBOX/disabled" SELF_PATH="$HELPER"
export NFT_BIN="$SANDBOX/bin/nft"
export PATH="$SANDBOX/bin:$PATH"
cat > "$SANDBOX/bin/nft" <<'MOCK'
#!/bin/sh
case "$1" in
-f) cat >/dev/null; touch "$CP_STATE/table" ;;
delete) [ ! -f "$CP_STATE/delete-fails" ] || exit 1; rm -f "$CP_STATE/table" ;;
list) test -f "$CP_STATE/table" ;;
esac
MOCK
cat > "$SANDBOX/bin/uci" <<'MOCK'
#!/bin/sh
[ "$1" != -q ] || shift
case "$1:$2" in
get:firewall.vectra_controlplane) test -f "$CP_STATE/section" && echo include ;;
get:firewall.vectra_controlplane.type) test -f "$CP_STATE/section" && echo script ;;
get:firewall.vectra_controlplane.path) cat "$CP_STATE/path" 2>/dev/null ;;
set:firewall.vectra_controlplane=include) touch "$CP_STATE/section" ;;
set:firewall.vectra_controlplane.path=*) printf '%s\n' "${2#*=}" > "$CP_STATE/path" ;;
delete:firewall.vectra_controlplane) rm -f "$CP_STATE/section" "$CP_STATE/path" ;;
*) exit 0 ;;
esac
MOCK
chmod +x "$SANDBOX/bin/"*
sed -e "s|SELF_SCRIPT=\".*\"|SELF_SCRIPT=\"$HELPER\"|" -e "s|FW_INCLUDE_PATH=\".*\"|FW_INCLUDE_PATH=\"$FW_INCLUDE_PATH\"|" "$INSTALL" > "$SANDBOX/install.sh"
COUNT=0
check() { COUNT=$((COUNT+1)); if eval "$1"; then printf 'ok %s - %s\n' "$COUNT" "$2"; else printf 'FAIL %s - %s\n' "$COUNT" "$2"; exit 1; fi; }
sh "$SANDBOX/install.sh"
check '[ -f "$CP_STATE/table" ] && [ -f "$FW_INCLUDE_PATH" ] && [ -f "$CP_STATE/section" ]' 'install registers own resources'
sh "$HELPER" remove
check '[ ! -f "$CP_STATE/table" ] && [ -f "$FW_INCLUDE_PATH" ] && [ ! -f "$DISABLED_PATH" ]' 'remove is temporary and preserves hooks'
sh "$HELPER" apply
check '[ -f "$CP_STATE/table" ]' 'temporary removal permits automatic restore'
printf 'keep user firewall\n' > "$CP_STATE/user-rule"
sh "$HELPER" disable || true
check '[ -f "$DISABLED_PATH" ] && [ ! -f "$CP_STATE/table" ] && [ ! -f "$CP_STATE/section" ] && [ ! -f "$FW_INCLUDE_PATH" ]' 'disable removes own resources persistently'
sh "$HELPER" apply
check '[ ! -f "$CP_STATE/table" ] && [ ! -f "$FW_INCLUDE_PATH" ]' 'automatic apply respects persistent disable'
sh "$HELPER" disable
check '[ "$(cat "$CP_STATE/user-rule")" = "keep user firewall" ] && [ -x "$HELPER" ]' 'repeat disable preserves user rules and packaged helper'
sh "$SANDBOX/install.sh"
check '[ ! -f "$DISABLED_PATH" ] && [ -f "$CP_STATE/table" ] && [ -f "$CP_STATE/section" ] && [ -f "$FW_INCLUDE_PATH" ]' 'reinstall enables and restores own resources'
# Do not claim a complete rollback when nft refuses to delete its table.
touch "$CP_STATE/delete-fails"
if sh "$HELPER" disable >/dev/null 2>&1; then
	printf 'FAIL - disable swallowed nft deletion failure\n'; exit 1
fi
check '[ -f "$DISABLED_PATH" ] && [ -f "$CP_STATE/table" ]' 'failed rollback reports error and prevents automatic reapply'
rm -f "$CP_STATE/delete-fails"
sh "$HELPER" disable
check '[ ! -f "$CP_STATE/table" ]' 'retry finishes cleanup after nft recovers'
sh "$SANDBOX/install.sh"
# A user repurposed the reserved include: do not delete its section or file.
printf 'custom user include\n' > "$FW_INCLUDE_PATH"
printf '%s\n' "$SANDBOX/user-include" > "$CP_STATE/path"
sh "$HELPER" disable
check '[ -f "$CP_STATE/section" ] && [ "$(cat "$FW_INCLUDE_PATH")" = "custom user include" ]' 'disable preserves repurposed section and custom include'
printf '%s checks PASS\n' "$COUNT"
