#!/usr/bin/env bash
#
# Capture-XrayParityCorpus.sh — READ-ONLY capture of the Xray config PassWall2
# generated on a live router, for the vctl parity oracle (TestParityCorpus in
# router/vectra-controller-pro/internal/coreengine/xray/parity_test.go).
#
# GATED: this connects to a router over SSH. It performs ONLY `find` + `cat`
# (no mutation). Use it against a test/lab router or — read-only — a live one,
# per the deploy doctrine. It pins the host key via a known_hosts file.
#
# It captures the "passwall side" (<name>.passwall-xray.json). You still author
# the equivalent operator-side config.Config as <name>.operator.json (see
# testdata/parity/README.md) so the test can render+diff the two.
#
# Usage:
#   scripts/Capture-XrayParityCorpus.sh \
#     --router-host 192.168.1.1 --router-user root \
#     --known-hosts ./router-known_hosts --name ax3000t-reality
#
set -euo pipefail

ROUTER_HOST=""
ROUTER_USER="root"
KNOWN_HOSTS=""
NAME=""
DEST_DIR="router/vectra-controller-pro/internal/coreengine/xray/testdata/parity"

usage() { sed -n '2,30p' "$0"; }

while [ $# -gt 0 ]; do
	case "$1" in
		--router-host) ROUTER_HOST="$2"; shift 2;;
		--router-user) ROUTER_USER="$2"; shift 2;;
		--known-hosts) KNOWN_HOSTS="$2"; shift 2;;
		--name) NAME="$2"; shift 2;;
		--dest-dir) DEST_DIR="$2"; shift 2;;
		-h|--help) usage; exit 0;;
		*) echo "unknown arg: $1" >&2; usage; exit 2;;
	esac
done

[ -n "$ROUTER_HOST" ] || { echo "--router-host required" >&2; exit 2; }
[ -n "$NAME" ] || { echo "--name required" >&2; exit 2; }

SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=10)
if [ -n "$KNOWN_HOSTS" ]; then
	SSH_OPTS+=(-o StrictHostKeyChecking=yes -o UserKnownHostsFile="$KNOWN_HOSTS")
else
	echo "WARNING: no --known-hosts given; host key is NOT pinned." >&2
fi

mkdir -p "$DEST_DIR"
OUT="$DEST_DIR/${NAME}.passwall-xray.json"

# Read-only remote: locate PassWall2's generated Xray config and print the
# largest candidate (the main global config). No writes, no service touch.
REMOTE_CMD='set -e; f=$(ls -S /tmp/etc/passwall2/*/global.json /tmp/etc/passwall2/*.json 2>/dev/null | head -n1); [ -n "$f" ] || { echo "no passwall-generated xray config found under /tmp/etc/passwall2" >&2; exit 3; }; echo "# source: $f" >&2; cat "$f"'

echo "Capturing PassWall2-generated Xray config from $ROUTER_USER@$ROUTER_HOST ..." >&2
ssh "${SSH_OPTS[@]}" "${ROUTER_USER}@${ROUTER_HOST}" "$REMOTE_CMD" >"$OUT"

if [ ! -s "$OUT" ]; then
	echo "capture produced an empty file; removing" >&2
	rm -f "$OUT"
	exit 3
fi

echo "Wrote $OUT ($(wc -c <"$OUT") bytes)." >&2
echo "Next: author $DEST_DIR/${NAME}.operator.json (an internal/config Config for the same node)," >&2
echo "then run: (cd router/vectra-controller-pro && go test ./internal/coreengine/xray/ -run TestParityCorpus -v)" >&2
