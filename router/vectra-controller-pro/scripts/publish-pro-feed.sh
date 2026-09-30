#!/usr/bin/env bash
# Publish the pro feed and its installer to the production artifact host.
# Run only with the owner's go-ahead: routers install from what this writes.
#
#   scripts/publish-pro-feed.sh [--channel pro-canary] [--arch ARCH]... [--create-key] [--dry-run]
#
# Where it lands: /opt/vectra-prorouter/deploy/runtime/artifacts/openwrt/<channel>/
# on the build host, served as https://api.vectra-pro.net/artifacts/openwrt/<channel>/.
# It never writes under openwrt/stable (the fleet's feed: all routers install
# the legacy controller from there, and its sync is rsync --delete) — a channel
# is refused unless it starts with "pro".
#
# The feed is built here, unsigned; it is signed ON the build host with the
# production key, which never leaves it (--create-key makes that key, once;
# without it a missing key stops the run). The installer is baked there too,
# with the key's public half and the channel's URL. Then the channel is synced
# with deploy/scripts/sync-runtime-artifacts.sh (--delete inside the channel
# only) and read back over HTTPS: the installer's hash and every arch's
# Packages.sig must match what was signed.
set -euo pipefail
export COPYFILE_DISABLE=1

MODULE="$(cd "$(dirname "$0")/.." && pwd)"
CHANNEL="pro-canary"
SSH_ALIAS="${SSH_ALIAS:-vectra-prod}"
REMOTE_ROOT="/opt/vectra-prorouter"
KEY_DIR="$REMOTE_ROOT/.codex-runtime/pro-feed-keys"
PUBLIC_BASE="https://api.vectra-pro.net/artifacts/openwrt"
CREATE_KEY=0
DRY_RUN=0
ARCH_ARGS=()
while [[ $# -gt 0 ]]; do
	case "$1" in
	--channel) CHANNEL="$2"; shift 2 ;;
	--arch) ARCH_ARGS+=(--arch "$2"); shift 2 ;;
	--create-key) CREATE_KEY=1; shift ;;
	--dry-run) DRY_RUN=1; shift ;;
	-h | --help) sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
done
die() { echo "FATAL: $*" >&2; exit 1; }
[[ "$CHANNEL" =~ ^pro[a-z0-9-]*$ ]] || die "channel '$CHANNEL': only pro* channels are published by this script"
[[ ${#ARCH_ARGS[@]} -gt 0 ]] || ARCH_ARGS=(--arch aarch64_cortex-a53)

URL="$PUBLIC_BASE/$CHANNEL"
LOCAL="$MODULE/dist/publish-$CHANNEL"
REMOTE="/tmp/vectra-pro-feed-$CHANNEL"

echo "==> build ($CHANNEL: ${ARCH_ARGS[*]})"
"$MODULE/scripts/build-pro-feed.sh" --out "$LOCAL" "${ARCH_ARGS[@]}" | tail -n 3

echo "==> the build host"
remote_arch="$(ssh "$SSH_ALIAS" uname -m)"
case "$remote_arch" in
x86_64) goarch=amd64 ;;
aarch64) goarch=arm64 ;;
*) die "no feedtool build for $remote_arch" ;;
esac
( cd "$MODULE" && GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -o "$LOCAL.feedtool" ./tools/feedtool )
if ! ssh "$SSH_ALIAS" "test -f '$KEY_DIR/vectra-pro.sec'"; then
	[[ "$CREATE_KEY" == 1 ]] || die "no production key in $SSH_ALIAS:$KEY_DIR (the first publish passes --create-key)"
	new_key="--new-key"
else
	new_key=""
fi

if [[ "$DRY_RUN" == 1 ]]; then
	echo "==> dry run: would sign on $SSH_ALIAS with $KEY_DIR${new_key:+ (new key)}, sync to artifacts/openwrt/$CHANNEL, serve $URL"
	exit 0
fi

echo "==> ship"
ssh "$SSH_ALIAS" "rm -rf '$REMOTE' && mkdir -p '$REMOTE'"
tar --no-xattrs -C "$(dirname "$LOCAL")" -cf - "$(basename "$LOCAL")" | ssh "$SSH_ALIAS" "tar -xf - -C '$REMOTE'"
scp -q "$LOCAL.feedtool" "$SSH_ALIAS:$REMOTE/feedtool"
scp -q "$MODULE/scripts/sign-pro-feed.sh" "$SSH_ALIAS:$REMOTE/sign-pro-feed.sh"

echo "==> sign on the build host (the key stays there)"
ssh "$SSH_ALIAS" "set -e; chmod 0755 '$REMOTE/feedtool' '$REMOTE/sign-pro-feed.sh'
	bash '$REMOTE/sign-pro-feed.sh' --feed '$REMOTE/$(basename "$LOCAL")' --key-dir '$KEY_DIR' --feed-url '$URL' --feedtool '$REMOTE/feedtool' $new_key
	rm -f '$REMOTE/$(basename "$LOCAL")/install.sh.in'"

echo "==> sync artifacts/openwrt/$CHANNEL"
ssh "$SSH_ALIAS" "bash '$REMOTE_ROOT/deploy/scripts/sync-runtime-artifacts.sh' --source '$REMOTE/$(basename "$LOCAL")' --channel 'openwrt/$CHANNEL'"

echo "==> read back over HTTPS"
want="$(ssh "$SSH_ALIAS" "sha256sum '$REMOTE/$(basename "$LOCAL")/install.sh'" | awk '{print $1}')"
got="$(curl -fsSL "$URL/install.sh" | shasum -a 256 | awk '{print $1}')"
[[ "$want" == "$got" ]] || die "the published installer ($got) is not the signed one ($want)"
for d in "$LOCAL"/*/; do
	a="$(basename "$d")"
	curl -fsS -o /dev/null "$URL/$a/Packages.sig" || die "$URL/$a/Packages.sig is not served"
	echo "  $a: Packages.sig served"
done
ssh "$SSH_ALIAS" "rm -rf '$REMOTE'"

echo "==> published: $URL"
echo "    wget -qO /tmp/vectra.sh $URL/install.sh && sh /tmp/vectra.sh"
echo "    installer sha256 $got"
