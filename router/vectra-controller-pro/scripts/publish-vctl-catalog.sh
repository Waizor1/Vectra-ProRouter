#!/usr/bin/env bash
# Publish vctl's catalog (internal/catalog/catalog.json) to a pro* channel:
# what routers know about services and sites besides the provider and the
# owner — the AI names of «Нейросети», where «Нейросети» go by default,
# sites for every router, how often to ask — changed without a release.
# Run only with the owner's go-ahead: every router of the channel takes it
# within its refreshHours (6 h by default), or at once by `vctl catalog refresh`.
#
#   scripts/publish-vctl-catalog.sh [--channel pro-canary] [--file PATH] [--dry-run]
#
# The document is checked here with the router's own parser, strictly
# (`vctl catalog check`: a field this vctl does not know is a typo here), and
# its serial must be above the published one — a router takes only a higher
# serial, so bump it (YYYYMMDDNN) with every change. Rollback = publish the
# old content with a NEW, higher serial.
#
# It is signed ON the build host with the feed's production key, which never
# leaves it (the same key as the channel's Packages.sig: a router takes the
# catalog only under the key that signs its feed), verified there, the
# channel's previous catalog backed up under /opt/vectra-prorouter-backups,
# both files put in place by rename, and read back over HTTPS.
#
# A full feed rebuild (publish-pro-feed.sh) keeps the channel's catalog and
# geo set; the incremental package publish copies the channel and keeps them
# too.
set -euo pipefail
export COPYFILE_DISABLE=1

MODULE="$(cd "$(dirname "$0")/.." && pwd)"
CHANNEL="pro-canary"
FILE="$MODULE/internal/catalog/catalog.json"
SSH_ALIAS="${SSH_ALIAS:-vectra-prod}"
REMOTE_ROOT="/opt/vectra-prorouter"
KEY_DIR="$REMOTE_ROOT/.codex-runtime/pro-feed-keys"
BACKUPS="/opt/vectra-prorouter-backups"
PUBLIC_BASE="https://api.vectra-pro.net/artifacts/openwrt"
DRY_RUN=0
while [[ $# -gt 0 ]]; do
	case "$1" in
	--channel) CHANNEL="$2"; shift 2 ;;
	--file) FILE="$2"; shift 2 ;;
	--dry-run) DRY_RUN=1; shift ;;
	-h | --help) sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
die() { echo "FATAL: $*" >&2; exit 1; }
[[ "$CHANNEL" =~ ^pro[a-z0-9-]*$ ]] || die "channel '$CHANNEL': only pro* channels"
[[ -f "$FILE" ]] || die "no $FILE"
URL="$PUBLIC_BASE/$CHANNEL"
DEST="$REMOTE_ROOT/deploy/runtime/artifacts/openwrt/$CHANNEL"

LOCAL_PREV="$(mktemp)"
trap 'rm -f "$LOCAL_PREV"' EXIT
echo "==> check $FILE with the router's parser"
( cd "$MODULE" && go run ./cmd/vctl catalog check "$FILE" )
serial_of() { sed -n 's/^[[:space:]]*"serial":[[:space:]]*\([0-9][0-9]*\).*/\1/p' | head -n 1; }
local_serial="$(serial_of < "$FILE")"
[[ -n "$local_serial" ]] || die "no serial in $FILE"
code="$(curl -sS -o "$LOCAL_PREV" -w '%{http_code}' "$URL/catalog.json" || echo 000)"
case "$code" in
200) published="$(serial_of < "$LOCAL_PREV")"; [[ -n "$published" ]] || die "the published catalog has no serial" ;;
404) published="" ;;
*) die "could not read the published catalog (http $code): the serial cannot be checked" ;;
esac
echo "    serial $local_serial (published: ${published:-none})"
if [[ -n "$published" && "$local_serial" -le "$published" ]]; then
	die "serial $local_serial is not above the published $published: routers would not take it (bump it)"
fi

remote_arch="$(ssh "$SSH_ALIAS" uname -m)"
case "$remote_arch" in
x86_64) goarch=amd64 ;;
aarch64) goarch=arm64 ;;
*) die "no feedtool build for $remote_arch" ;;
esac
if [[ "$DRY_RUN" == 1 ]]; then
	echo "==> dry run: would sign on $SSH_ALIAS with $KEY_DIR and install $URL/catalog.json(.sig)"
	exit 0
fi

LOCAL="$(mktemp -d)"
trap 'rm -rf "$LOCAL" "$LOCAL_PREV"' EXIT
( cd "$MODULE" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -o "$LOCAL/feedtool" ./tools/feedtool )
REMOTE="/tmp/vctl-catalog-$CHANNEL"

echo "==> ship"
ssh "$SSH_ALIAS" "rm -rf '$REMOTE' && mkdir -p '$REMOTE'"
scp -q "$FILE" "$SSH_ALIAS:$REMOTE/catalog.json"
scp -q "$LOCAL/feedtool" "$SSH_ALIAS:$REMOTE/feedtool"

echo "==> sign on the build host (the key stays there), back up, put in place"
ssh "$SSH_ALIAS" "set -e
	cd '$REMOTE'; chmod 0755 feedtool
	./feedtool sign -sec '$KEY_DIR/vectra-pro.sec' -in catalog.json -out catalog.json.sig
	./feedtool verify -pub '$KEY_DIR/vectra-pro.pub' -in catalog.json -sig catalog.json.sig > /dev/null
	test -d '$DEST' || { echo 'no channel directory $DEST' >&2; exit 1; }
	ts=\$(date -u +%Y%m%d-%H%M%S)
	if [ -f '$DEST/catalog.json' ]; then
		mkdir -p '$BACKUPS/catalog-$CHANNEL-'\$ts
		cp -a '$DEST/catalog.json' '$DEST/catalog.json.sig' '$BACKUPS/catalog-$CHANNEL-'\$ts/ 2>/dev/null || true
		echo \"    backup: $BACKUPS/catalog-$CHANNEL-\$ts\"
	fi
	install -m 0644 catalog.json.sig '$DEST/.catalog.json.sig.new'
	install -m 0644 catalog.json '$DEST/.catalog.json.new'
	mv -f '$DEST/.catalog.json.sig.new' '$DEST/catalog.json.sig'
	mv -f '$DEST/.catalog.json.new' '$DEST/catalog.json'
	rm -rf '$REMOTE'"

echo "==> read back over HTTPS"
want="$(shasum -a 256 "$FILE" | awk '{print $1}')"
got="$(curl -fsSL "$URL/catalog.json" | shasum -a 256 | awk '{print $1}')"
[[ "$want" == "$got" ]] || die "the published catalog ($got) is not the one signed ($want)"
curl -fsSL "$URL/catalog.json.sig" > "$LOCAL/catalog.json.sig"
ssh "$SSH_ALIAS" "cat '$KEY_DIR/vectra-pro.pub'" > "$LOCAL/vectra-pro.pub"
( cd "$MODULE" && go run ./tools/feedtool verify -pub "$LOCAL/vectra-pro.pub" -in "$FILE" -sig "$LOCAL/catalog.json.sig" > /dev/null )
echo "==> published catalog $local_serial to $URL/catalog.json"
