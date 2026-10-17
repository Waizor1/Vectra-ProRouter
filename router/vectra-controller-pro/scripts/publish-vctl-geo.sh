#!/usr/bin/env bash
# Publish the router's geo lists (geosite.dat, geoip.dat), built fresh from
# the upstream releases tools/geobuild pins, to a pro* channel. Run only with
# the owner's go-ahead: every vctl on the channel takes what this writes.
#
#   scripts/publish-vctl-geo.sh [--channel pro-canary] [--accept CODE,...] [--dry-run]
#   publish-vctl-geo.sh --cron [--channel pro-canary]          (on the build host)
#
# Where it lands: /opt/vectra-prorouter/deploy/runtime/artifacts/openwrt/<channel>/
# on the build host (ssh alias vectra-prod), served as
# https://api.vectra-pro.net/artifacts/openwrt/<channel>/ — next to the feed
# whose key signs it:
#   geo/geosite-<sha12>.dat, geo/geoip-<sha12>.dat   content-addressed
#   geo.json, geo.json.sig                           the manifest (usign)
# Only pro* channels: openwrt/stable is the legacy fleet's feed.
#
# From a workstation it builds geobuild and feedtool for the build host and
# installs them, with this script, in $TOOLS there; the rest runs ON the host:
# geobuild into a staging directory against the published geo.json (every
# category it had stays, none shrinks below half — --accept waives that for
# the named ones, once), feedtool signs geo.json with the feed's key (which
# never leaves the host) and verifies it, the serial must be above the
# published one, then the new .dat files go into the channel, the signature
# and the manifest last, each by rename. Files of the last 7 builds stay (a
# router may be fetching what the previous manifest named); nothing is ever
# synced with --delete. Last, the channel is read back over HTTPS and the
# served geo.json verified with the channel's served key. Upstreams that have
# not changed leave nothing to publish, which is not an error.
#
# --dry-run builds on this machine against the published build (read over
# HTTPS), checks the key is on the host, and writes nothing there.
#
# --cron is the host half alone, for the host's own crontab, with the tools a
# workstation run installed; a lock keeps runs apart, --accept is refused (a
# waiver is a person's decision). Nothing installs it. To enable it, on the
# build host, e.g.:
#   17 4 * * * /opt/vectra-prorouter/.codex-runtime/vctl-geo/publish-vctl-geo.sh --cron --channel pro-canary >> /opt/vectra-prorouter/.codex-runtime/vctl-geo/cron.log 2>&1
#
# NOTE: scripts/publish-pro-feed.sh syncs the channel with rsync --delete and
# takes geo/ and geo.json* away with it; publish the geo lists again after it
# until that sync leaves them be.
set -euo pipefail
export COPYFILE_DISABLE=1

CHANNEL="pro-canary"
SSH_ALIAS="${SSH_ALIAS:-vectra-prod}"
REMOTE_ROOT="${REMOTE_ROOT:-/opt/vectra-prorouter}"
KEY_DIR="${KEY_DIR:-$REMOTE_ROOT/.codex-runtime/pro-feed-keys}"
TOOLS="${TOOLS:-$REMOTE_ROOT/.codex-runtime/vctl-geo}"
PUBLIC_BASE="${PUBLIC_BASE:-https://api.vectra-pro.net/artifacts/openwrt}"
KEEP_BUILDS=7
ACCEPT=""
DRY_RUN=0
MODE=workstation
while [[ $# -gt 0 ]]; do
	case "$1" in
	--channel) CHANNEL="$2"; shift 2 ;;
	--accept) ACCEPT="$2"; shift 2 ;;
	--dry-run) DRY_RUN=1; shift ;;
	--cron) MODE=cron; shift ;;
	--on-host) MODE=host; shift ;; # what a workstation run executes on the host
	-h | --help) sed -n '2,41p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
die() { echo "FATAL: $*" >&2; exit 1; }
[[ "$CHANNEL" =~ ^pro[a-z0-9-]*$ ]] || die "channel '$CHANNEL': only pro* channels are published by this script"
[[ -z "$ACCEPT" || "$ACCEPT" =~ ^[A-Za-z0-9,_-]+$ ]] || die "--accept takes comma-separated category names"
URL="$PUBLIC_BASE/$CHANNEL"

# The files a manifest names, one "path sha256" per line. geo.json is
# geobuild's own indented JSON: in each file object "path" comes right before
# its "sha256" (a source's sha256 has no path before it).
manifest_files() {
	awk -F'"' '/^ *"path": "/ { p = $4; next } /^ *"sha256": "/ && p != "" { print p, $4; p = "" }' "$1"
}
manifest_serial() { sed -n 's/^  "serial": \([0-9][0-9]*\),$/\1/p' "$1"; }
valid_path() { [[ "$1" =~ ^geo/(geosite|geoip)-[0-9a-f]{12}\.dat$ ]]; }

# --------------------------------------------------------------- host half --

if [[ "$MODE" != workstation ]]; then
	[[ "$DRY_RUN" == 0 ]] || die "--dry-run runs on a workstation"
	[[ "$MODE" != cron || -z "$ACCEPT" ]] || die "--cron takes no --accept: a waiver is a person's decision"
	[[ "$MODE" != cron ]] || echo "==> $(date -u +%Y-%m-%dT%H:%M:%SZ) geo lists for $CHANNEL"
	CHANNEL_DIR="$REMOTE_ROOT/deploy/runtime/artifacts/openwrt/$CHANNEL"
	STATE="$TOOLS/$CHANNEL"
	[[ -x "$TOOLS/geobuild" && -x "$TOOLS/feedtool" ]] || die "no geobuild/feedtool in $TOOLS (a workstation run installs them)"
	[[ -d "$REMOTE_ROOT/deploy/runtime/artifacts" ]] || die "no artifact root under $REMOTE_ROOT"
	[[ -f "$KEY_DIR/vectra-pro.sec" && -f "$KEY_DIR/vectra-pro.pub" ]] || die "no feed key in $KEY_DIR"
	# A router takes the geo lists only from a key that also signs the
	# channel's Packages: the channel must carry this key's feed.
	[[ -f "$CHANNEL_DIR/vectra-pro.pub" ]] || die "$CHANNEL_DIR has no feed (vectra-pro.pub): publish the feed first"
	cmp -s "$KEY_DIR/vectra-pro.pub" "$CHANNEL_DIR/vectra-pro.pub" || die "$CHANNEL_DIR/vectra-pro.pub is not the key in $KEY_DIR"
	mkdir -p "$STATE"
	if command -v flock > /dev/null; then
		exec 9> "$TOOLS/.lock"
		flock -n 9 || die "another publish holds $TOOLS/.lock"
	else
		[[ "$MODE" != cron ]] || die "no flock here"
		echo "  (no flock: running unlocked)"
	fi
	stage="$(mktemp -d "$TOOLS/stage.XXXXXX")"
	trap 'rm -rf "$stage"' EXIT

	published=0
	prev=()
	if [[ -f "$CHANNEL_DIR/geo.json" ]]; then
		published="$(manifest_serial "$CHANNEL_DIR/geo.json")"
		[[ -n "$published" ]] || die "$CHANNEL_DIR/geo.json has no serial"
		prev=(-prev "$CHANNEL_DIR/geo.json")
		# The history the old files are kept by starts with what is served.
		[[ -f "$STATE/$published.json" ]] || install -m 0644 "$CHANNEL_DIR/geo.json" "$STATE/$published.json"
	fi

	echo "==> build (published serial $published)"
	"$TOOLS/geobuild" -out "$stage" ${prev[@]+"${prev[@]}"} ${ACCEPT:+-accept "$ACCEPT"}
	if [[ ! -f "$stage/geo.json" ]]; then
		echo "==> nothing to publish: the upstreams have not changed"
		exit 0
	fi
	serial="$(manifest_serial "$stage/geo.json")"
	[[ -n "$serial" && "$serial" -gt "$published" ]] || die "serial ${serial:-?} is not above the published $published"
	files="$(manifest_files "$stage/geo.json")"
	[[ "$(wc -l <<< "$files")" -eq 2 ]] || die "geo.json does not name two files"
	while read -r path sum; do
		valid_path "$path" || die "geo.json names $path"
		[[ "$(sha256sum "$stage/$path" | awk '{print $1}')" == "$sum" ]] || die "$path is not what geo.json says"
	done <<< "$files"

	echo "==> sign on the build host (the key stays there)"
	"$TOOLS/feedtool" sign -sec "$KEY_DIR/vectra-pro.sec" -in "$stage/geo.json" -out "$stage/geo.json.sig"
	"$TOOLS/feedtool" verify -pub "$KEY_DIR/vectra-pro.pub" -in "$stage/geo.json" -sig "$stage/geo.json.sig" > /dev/null

	echo "==> publish serial $serial to $CHANNEL_DIR"
	put() { # src dst: replace dst by rename, readable by the web server
		install -m 0644 "$1" "$(dirname "$2")/.$(basename "$2").$$"
		mv -f "$(dirname "$2")/.$(basename "$2").$$" "$2"
	}
	install -d -m 0755 "$CHANNEL_DIR/geo"
	while read -r path sum; do
		cmp -s "$stage/$path" "$CHANNEL_DIR/$path" 2> /dev/null || put "$stage/$path" "$CHANNEL_DIR/$path"
		echo "  $path"
	done <<< "$files"
	put "$stage/geo.json.sig" "$CHANNEL_DIR/geo.json.sig"
	put "$stage/geo.json" "$CHANNEL_DIR/geo.json"
	install -m 0644 "$stage/geo.json" "$STATE/$serial.json"

	echo "==> keep the files of the last $KEEP_BUILDS builds"
	serials() { for f in "$STATE"/*.json; do f="${f##*/}"; [[ "$f" =~ ^[0-9]+\.json$ ]] && echo "${f%.json}"; done; }
	keep="$(serials | sort -rn | head -n "$KEEP_BUILDS")"
	for s in $(serials); do
		grep -qx "$s" <<< "$keep" || rm -f "$STATE/$s.json"
	done
	wanted="$(for s in $keep; do manifest_files "$STATE/$s.json"; done | awk '{print $1}' | sort -u)"
	for f in "$CHANNEL_DIR"/geo/geosite-*.dat "$CHANNEL_DIR"/geo/geoip-*.dat; do
		[[ -f "$f" ]] || continue
		p="geo/$(basename "$f")"
		valid_path "$p" || continue
		grep -qxF "$p" <<< "$wanted" || { rm -f "$f"; echo "  removed $p"; }
	done

	echo "==> read back over HTTPS"
	back="$stage/back"
	mkdir -p "$back"
	for f in geo.json geo.json.sig vectra-pro.pub; do
		curl -fsS --max-time 60 -o "$back/$f" "$URL/$f" || die "$URL/$f is not served"
	done
	cmp -s "$back/geo.json" "$stage/geo.json" || die "the served geo.json is not serial $serial"
	"$TOOLS/feedtool" verify -pub "$back/vectra-pro.pub" -in "$back/geo.json" -sig "$back/geo.json.sig" > /dev/null \
		|| die "the served geo.json does not verify with the channel's served key"
	while read -r path sum; do
		got="$(curl -fsS --max-time 120 "$URL/$path" | sha256sum | awk '{print $1}')"
		[[ "$got" == "$sum" ]] || die "$URL/$path: sha256 $got, geo.json says $sum"
	done <<< "$files"
	echo "==> published serial $serial: $URL/geo.json"
	exit 0
fi

# -------------------------------------------------------- workstation half --

MODULE="$(cd "$(dirname "$0")/.." && pwd)"
LOCAL="$MODULE/dist/publish-geo-$CHANNEL"
rm -rf "$LOCAL"
mkdir -p "$LOCAL"

if [[ "$DRY_RUN" == 1 ]]; then
	echo "==> dry run: build here against the published $URL/geo.json"
	( cd "$MODULE" && go build -trimpath -o "$LOCAL/geobuild" ./tools/geobuild )
	prev=()
	if curl -fsS --max-time 60 -o "$LOCAL/prev/geo.json" --create-dirs "$URL/geo.json" 2> /dev/null; then
		while read -r path sum; do
			valid_path "$path" || die "the published geo.json names $path"
			curl -fsS --max-time 120 -o "$LOCAL/prev/$path" --create-dirs "$URL/$path" || die "$URL/$path is not served"
		done < <(manifest_files "$LOCAL/prev/geo.json")
		prev=(-prev "$LOCAL/prev/geo.json")
		echo "  published serial $(manifest_serial "$LOCAL/prev/geo.json")"
	else
		echo "  nothing published yet"
	fi
	"$LOCAL/geobuild" -out "$LOCAL/stage" ${prev[@]+"${prev[@]}"} ${ACCEPT:+-accept "$ACCEPT"}
	ssh "$SSH_ALIAS" "test -f '$KEY_DIR/vectra-pro.sec'" || die "no feed key in $SSH_ALIAS:$KEY_DIR"
	echo "==> dry run: would install geobuild and feedtool in $SSH_ALIAS:$TOOLS, build and sign there with $KEY_DIR,"
	echo "    publish into artifacts/openwrt/$CHANNEL and serve $URL/geo.json (this build: $LOCAL/stage)"
	exit 0
fi

echo "==> tools for the build host"
remote_arch="$(ssh "$SSH_ALIAS" uname -m)"
case "$remote_arch" in
x86_64) goarch=amd64 ;;
aarch64) goarch=arm64 ;;
*) die "no build for $remote_arch" ;;
esac
for t in geobuild feedtool; do
	( cd "$MODULE" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -o "$LOCAL/$t" "./tools/$t" )
done
cp "$0" "$LOCAL/publish-vctl-geo.sh"

echo "==> install in $SSH_ALIAS:$TOOLS"
ssh "$SSH_ALIAS" "mkdir -p '$TOOLS'"
tar --no-xattrs -C "$LOCAL" -cf - geobuild feedtool publish-vctl-geo.sh | ssh "$SSH_ALIAS" "set -e
	d=\$(mktemp -d '$TOOLS/install.XXXXXX'); tar -xf - -C \"\$d\"
	for f in geobuild feedtool publish-vctl-geo.sh; do chmod 0755 \"\$d/\$f\"; mv -f \"\$d/\$f\" '$TOOLS/'\"\$f\"; done
	rmdir \"\$d\""

echo "==> build, sign and publish on the build host"
ssh "$SSH_ALIAS" "REMOTE_ROOT='$REMOTE_ROOT' KEY_DIR='$KEY_DIR' TOOLS='$TOOLS' PUBLIC_BASE='$PUBLIC_BASE' \
	bash '$TOOLS/publish-vctl-geo.sh' --on-host --channel '$CHANNEL' ${ACCEPT:+--accept '$ACCEPT'}"
