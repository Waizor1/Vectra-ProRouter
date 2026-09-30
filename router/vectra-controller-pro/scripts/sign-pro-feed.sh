#!/usr/bin/env bash
# Sign a feed built by build-pro-feed.sh and bake its installer.
#
#   scripts/sign-pro-feed.sh --feed DIR --key-dir DIR [--feed-url URL] [--new-key] [--feedtool PATH]
#
# --key-dir holds vectra-pro.sec and vectra-pro.pub. The production key lives
# only on the build host that publishes the feed; for the stand and local tests
# use a throwaway key (--new-key creates one where there is none — it never
# replaces a key). Every arch's Packages gets its Packages.sig, verified again
# with the public key before anything is written next to it.
#
# The installer (install.sh.in from the build) gets, baked in, the feed's URL,
# its public key, the architectures it holds and the versions — so the script a
# router runs cannot be pointed at another key by swapping one file on the
# server. The result: DIR/install.sh and DIR/vectra-pro.pub.
set -euo pipefail

FEED=""
KEY_DIR=""
FEED_URL="https://api.vectra-pro.net/artifacts/openwrt/pro"
NEW_KEY=0
FEEDTOOL=""
while [[ $# -gt 0 ]]; do
	case "$1" in
	--feed) FEED="$2"; shift 2 ;;
	--key-dir) KEY_DIR="$2"; shift 2 ;;
	--feed-url) FEED_URL="${2%/}"; shift 2 ;;
	--new-key) NEW_KEY=1; shift ;;
	--feedtool) FEEDTOOL="$2"; shift 2 ;;
	-h | --help) sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
die() { echo "FATAL: $*" >&2; exit 1; }
[[ -n "$FEED" && -n "$KEY_DIR" ]] || die "--feed and --key-dir are required"
[[ -f "$FEED/feed.json" && -f "$FEED/install.sh.in" ]] || die "$FEED is not a feed from build-pro-feed.sh"
case "$FEED_URL" in https://* | http://*) ;; *) die "--feed-url must be an http(s) URL" ;; esac

if [[ -z "$FEEDTOOL" ]]; then
	here="$(cd "$(dirname "$0")/.." && pwd)"
	FEEDTOOL="$here/dist/cache/feedtool"
	[[ -x "$FEEDTOOL" ]] || ( cd "$here" && go build -o "$FEEDTOOL" ./tools/feedtool )
fi

SEC="$KEY_DIR/vectra-pro.sec"
PUB="$KEY_DIR/vectra-pro.pub"
if [[ ! -f "$SEC" ]]; then
	[[ "$NEW_KEY" == 1 ]] || die "no $SEC (pass --new-key to create a throwaway one)"
	[[ ! -f "$PUB" ]] || die "$PUB exists without its secret key"
	mkdir -p "$KEY_DIR"
	chmod 0700 "$KEY_DIR"
	"$FEEDTOOL" keygen -pub "$PUB" -sec "$SEC" -comment "Vectra Pro feed" > /dev/null
	echo "==> new key $("$FEEDTOOL" fingerprint -pub "$PUB") in $KEY_DIR"
fi
FP="$("$FEEDTOOL" fingerprint -pub "$PUB")"

archs=()
for d in "$FEED"/*/; do
	d="${d%/}"
	[[ -f "$d/Packages" ]] || continue
	"$FEEDTOOL" sign -sec "$SEC" -in "$d/Packages" -out "$d/Packages.sig"
	"$FEEDTOOL" verify -pub "$PUB" -in "$d/Packages" -sig "$d/Packages.sig" > /dev/null
	archs+=("$(basename "$d")")
done
[[ ${#archs[@]} -gt 0 ]] || die "no architecture directories with a Packages index in $FEED"
install -m 0644 "$PUB" "$FEED/vectra-pro.pub"

# feed.json is flat and written by build-pro-feed.sh.
field() { sed -n "s/^  \"$1\": \"\([^\"]*\)\".*/\1/p" "$FEED/feed.json"; }
version="$(field version)"
xray="$(field xray)"
[[ -n "$version" && -n "$xray" ]] || die "feed.json lacks version or xray"

# The block between the markers is replaced; everything else is the template.
key_b64="$(sed -n 2p "$PUB")"
awk -v url="$FEED_URL" -v key="$key_b64" -v fp="$FP" -v archs="${archs[*]}" -v ver="$version" -v xray="$xray" '
	/^# >>> baked by sign-pro-feed.sh/ {
		print
		print "BAKED_FEED_URL=\047" url "\047"
		print "BAKED_FEED_KEY=\047" key "\047"
		print "BAKED_FEED_KEY_ID=\047" fp "\047"
		print "BAKED_ARCHS=\047" archs "\047"
		print "BAKED_VERSION=\047" ver "\047"
		print "BAKED_XRAY_MIN=\047" xray "\047"
		skip = 1
		next
	}
	/^# <<< baked/ { skip = 0 }
	!skip { print }
' "$FEED/install.sh.in" > "$FEED/install.sh"
grep -q "^BAKED_FEED_KEY_ID='$FP'$" "$FEED/install.sh" || die "the installer template has no bake markers"
chmod 0755 "$FEED/install.sh"

echo "==> signed ${#archs[@]} architectures with key $FP"
echo "    installer: $FEED/install.sh  sha256 $(shasum -a 256 "$FEED/install.sh" | awk '{print $1}')"
echo "    feed url : $FEED_URL"
