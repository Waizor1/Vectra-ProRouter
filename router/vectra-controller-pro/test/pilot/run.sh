#!/usr/bin/env bash
# The BloopCat pilot's router, end to end, on real OpenWrt userland, with the
# bytes Vectra published and every Vectra host on the far side of its WAN:
#
#   ./test/pilot/run.sh                 # both scenarios
#   ./test/pilot/run.sh pilot           # just this one
#   DOCKER_CONTEXT=colima ./test/pilot/run.sh
#
# Scenarios:
#   pilot     a new router, as the pilot sets it up: /etc/config/vectra-reporter
#             written first with enabled '0', the published installer with
#             --standby, passwd, remote_shell=0, `vectra on --force`; then a
#             reboot, a power cut, and the controls (the reporter and the
#             support shell switched on prove each refusal could have failed)
#   upgrade   a router that ran the previous release (0.7.0-r28, built here
#             from its commit): its identity made, a report queued and not yet
#             delivered, the owner's switches turned off — then the published
#             installer run over it, a power cut, and the controls
#   upgrade06 the same from 0.6.0-r36 (vectra-reporter 1.0.0-r2, state.json
#             still plaintext, no support-shell switch yet)
#
# PILOT_NEW_COMMIT=<commit>: a release not published yet in the published
# one's place — built from that commit as the old ones are, signed with a
# throwaway key, served at the same pro-canary path.
#
# What is real here: OpenWrt 24.10.8's userland (openwrt/rootfs), its procd,
# rc.d boot and shutdown order, busybox crond, fw4, opkg; the installer, the
# feed index, its signature and the packages exactly as Vectra published them
# (downloaded once, each checked against the index); vctl and vectra-reporter
# as those packages install them; a reboot is a stopped container (procd's
# shutdown order first), a power cut a killed one, /tmp a tmpfs.
#
# What is not: the hardware (the container is an aarch64 armsr userland told
# to accept aarch64_cortex-a53 packages; one kernel for every reboot, the
# docker host's), netifd (eth0 is configured by docker; fw4's wan zone is
# bound to it by device), and the panel and Connect, which are stubs on the
# WAN side (test/pilot/wan). Nothing here reaches Vectra's production hosts:
# the router resolves Vectra's names to the stand (its resolver, and vctl's
# own lookups through 8.8.8.8 and 77.88.8.8, are the stand's), and every
# packet to a production address is dropped and counted — the count must stay 0.
#
# Needs: docker on a kernel with nftables fib (Colima, not Docker Desktop),
# Go on the host, internet (OpenWrt's feeds; the published feed, once; GitHub
# for the old release's xray).
set -euo pipefail
export COPYFILE_DISABLE=1

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE="$(cd "$HERE/../.." && pwd)"
BUILD="$HERE/build"
LOGS="$HERE/logs"
OPENWRT_VERSION="${OPENWRT_VERSION:-24.10.8}"
IMAGE="openwrt/rootfs:aarch64_generic-$OPENWRT_VERSION"
# The older releases the upgrades start from, built from their own commits:
# the last 0.7.0-r28 commit, and 0.6.0-r36 (the last before 0.7.0).
OLD_COMMIT="${PILOT_OLD_COMMIT:-9ebd3e13}"
OLD06_COMMIT="${PILOT_OLD06_COMMIT:-0c4e4807}"
PUBLISHED_HOST="https://api.vectra-pro.net"
PUBLISHED_FEED="$PUBLISHED_HOST/artifacts/openwrt/pro-canary"
ARCH=aarch64_cortex-a53
NET="vctl-pilot-$$"
SUBNET=44.77.0.0/24
WAN_IP=44.77.0.2
ALL=(pilot upgrade upgrade06)
# Phases of each scenario: <phase>:<how it ends> — reboot (procd's shutdown
# order, then the container stops and starts), powercut (killed, started),
# end.
PHASES_pilot="install:reboot after-reboot:powercut after-powercut:reboot controls:end"
PHASES_upgrade="old:reboot old-after-reboot:reboot upgrade:powercut after-powercut:reboot controls:end"
PHASES_upgrade06="$PHASES_upgrade"

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
fail() { printf '\n\033[1mFATAL: %s\033[0m\n' "$*" >&2; exit 2; }

command -v docker > /dev/null || fail "docker not found"
command -v go > /dev/null || fail "go not found"
if [[ $# -gt 0 ]]; then SELECTED=("$@"); else SELECTED=("${ALL[@]}"); fi

step "the docker host's kernel"
if ! docker run --rm --privileged --network none "$IMAGE" \
	sh -c 'nft add table inet probe && nft add chain inet probe c "{ type filter hook prerouting priority 0; }" && nft add rule inet probe c fib daddr type local return' > /dev/null 2>&1; then
	fail "this docker host's kernel ($(docker info --format '{{.KernelVersion}}' 2>/dev/null)) has no nftables fib: run it on Colima — DOCKER_CONTEXT=colima $0"
fi
echo "  nft fib: yes ($(docker info --format '{{.KernelVersion}}' 2>/dev/null))"

cleanup() {
	for s in "${SELECTED[@]}"; do docker rm -f "vctl-pilot-$s-$$" > /dev/null 2>&1 || true; done
	docker rm -f "vctl-pilot-wan-$$" > /dev/null 2>&1 || true
	docker network rm "$NET" > /dev/null 2>&1 || true
	for d in "$BUILD"/old-src-*; do
		[[ -d "$d" ]] || continue
		git -C "$MODULE" worktree remove --force "$d" > /dev/null 2>&1 || true
		rm -rf "$d"
	done
}
trap cleanup EXIT
mkdir -p "$BUILD" "$LOGS"

# ----------------------------------------------------------------- build ----

step "the stand's WAN side (test/pilot/wan)"
( cd "$MODULE" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "$BUILD/wan/wan" ./test/pilot/wan )
echo "  $BUILD/wan/wan"

# PILOT_NEW_COMMIT: a release not published yet, built from its commit as the
# old ones are (signed with a throwaway key, served at the pro-canary path) and
# run in the published one's place — what a router would get once it is.
if [[ -z "${PILOT_NEW_COMMIT:-}" ]]; then
	step "the published release, mirrored once ($PUBLISHED_FEED)"
	PUB="$BUILD/mirror/published"
	rm -rf "$PUB"
	mkdir -p "$PUB/artifacts/openwrt/pro-canary/$ARCH"
	curl -fsS --max-time 60 -o "$PUB/install.sh" "$PUBLISHED_HOST/install"
	curl -fsS --max-time 60 -o "$PUB/artifacts/openwrt/pro-canary/feed.json" "$PUBLISHED_FEED/feed.json"
	for f in Packages Packages.gz Packages.sig; do
		curl -fsS --max-time 60 -o "$PUB/artifacts/openwrt/pro-canary/$ARCH/$f" "$PUBLISHED_FEED/$ARCH/$f"
	done
	(
		cd "$PUB/artifacts/openwrt/pro-canary/$ARCH"
		awk '/^Filename:/ { f = $2 } /^SHA256sum:/ { print $2 "  " f }' Packages > "$BUILD/published.sha256"
		while read -r _ f; do curl -fsS --max-time 300 -o "$f" "$PUBLISHED_FEED/$ARCH/$f"; done < "$BUILD/published.sha256"
		shasum -a 256 -c "$BUILD/published.sha256" | sed 's/^/  /'
	)
	{
		echo "mirrored $(date -u +%FT%TZ) from $PUBLISHED_HOST"
		echo "install.sh $(shasum -a 256 "$PUB/install.sh" | cut -d' ' -f1)"
		echo "Packages $(shasum -a 256 "$PUB/artifacts/openwrt/pro-canary/$ARCH/Packages" | cut -d' ' -f1)"
		echo "Packages.sig $(shasum -a 256 "$PUB/artifacts/openwrt/pro-canary/$ARCH/Packages.sig" | cut -d' ' -f1)"
		sed 's/^/package /' "$BUILD/published.sha256"
		cat "$PUB/artifacts/openwrt/pro-canary/feed.json"
	} > "$BUILD/published.manifest"
	grep -E "^BAKED_(VERSION|FEED_URL|FEED_KEY_ID)=" "$PUB/install.sh" | sed 's/^/  /'
fi

# build_old <name> <commit>: an older release from its own commit, signed with
# a throwaway key, at the pro-canary path the published installer has — a
# router that ran it took it from the same path.
build_old() {
	local name="$1" commit="$2" src="$BUILD/old-src-$1" mirror="$BUILD/mirror/$1" mod
	step "$name: the release at $commit, from its commit, signed with a throwaway key"
	# Built already from this very commit: the same bytes again.
	if [[ -f "$mirror/install.sh" && -s "$BUILD/$name.version" && "$(cat "$BUILD/$name.commit" 2> /dev/null)" == "$(git -C "$MODULE" rev-parse "$commit")" ]]; then
		echo "  $(cat "$BUILD/$name.version"), built before from $(cut -c1-8 "$BUILD/$name.commit")"
		return
	fi
	rm -rf "$mirror" "$BUILD/$name-feed"
	rm -f "$BUILD/$name.commit" "$BUILD/$name.version"
	# What an interrupted run left: the worktree, or only its directory.
	git -C "$MODULE" worktree remove --force "$src" > /dev/null 2>&1 || true
	rm -rf "$src"
	git -C "$MODULE" worktree add --force --detach "$src" "$commit" > /dev/null
	mod="$src/router/vectra-controller-pro"
	"$mod/scripts/build-pro-feed.sh" --arch "$ARCH" --out "$BUILD/$name-feed" | tail -n 1
	"$mod/scripts/sign-pro-feed.sh" --feed "$BUILD/$name-feed" --key-dir "$BUILD/$name-keys" --new-key --feed-url "$PUBLISHED_FEED" | tail -n 1
	mkdir -p "$mirror/artifacts/openwrt/pro-canary"
	cp "$BUILD/$name-feed/install.sh" "$mirror/install.sh"
	cp -R "$BUILD/$name-feed/$ARCH" "$mirror/artifacts/openwrt/pro-canary/$ARCH"
	cp "$BUILD/$name-feed/feed.json" "$mirror/artifacts/openwrt/pro-canary/feed.json"
	awk '/^Package: vectra-controller-pro$/ { p = 1 } p && /^Version:/ { print $2; exit }' "$mirror/artifacts/openwrt/pro-canary/$ARCH/Packages" > "$BUILD/$name.version"
	echo "  $(cat "$BUILD/$name.version"), $(grep -E '^BAKED_FEED_URL=' "$mirror/install.sh")"
	git -C "$MODULE" rev-parse "$commit" > "$BUILD/$name.commit"
	git -C "$MODULE" worktree remove --force "$src"
}
for s in "${SELECTED[@]}"; do
	case "$s" in
	upgrade) build_old old "$OLD_COMMIT" ;;
	upgrade06) build_old old06 "$OLD06_COMMIT" ;;
	esac
done
if [[ -n "${PILOT_NEW_COMMIT:-}" ]]; then
	build_old published "$PILOT_NEW_COMMIT"
	PUB="$BUILD/mirror/published"
	{
		echo "built $(date -u +%FT%TZ) from $(cat "$BUILD/published.commit") (PILOT_NEW_COMMIT), not published"
		echo "install.sh $(shasum -a 256 "$PUB/install.sh" | cut -d' ' -f1)"
		echo "Packages $(shasum -a 256 "$PUB/artifacts/openwrt/pro-canary/$ARCH/Packages" | cut -d' ' -f1)"
		(cd "$PUB/artifacts/openwrt/pro-canary/$ARCH" && shasum -a 256 ./*.ipk | sed 's/^/package /')
		cat "$PUB/artifacts/openwrt/pro-canary/feed.json"
	} > "$BUILD/published.manifest"
	grep -E "^BAKED_(VERSION|FEED_URL|FEED_KEY_ID)=" "$PUB/install.sh" | sed 's/^/  /'
fi

# What the router must end up with: the version the feed names, the two
# binaries exactly as the published packages carry them, and the installer's
# feed key.
pubfeed="$PUB/artifacts/openwrt/pro-canary/$ARCH"
awk '/^Package: vectra-controller-pro$/ { p = 1 } p && /^Version:/ { print $2; exit }' "$pubfeed/Packages" > "$BUILD/expected.version"
inner_sha() { # <ipk> <path inside data.tar.gz>
	tar -xzOf "$1" ./data.tar.gz | tar -xzOf - "$2" | shasum -a 256 | cut -d' ' -f1
}
inner_sha "$pubfeed/vectra-controller-pro_$(cat "$BUILD/expected.version")_$ARCH.ipk" ./usr/sbin/vctl > "$BUILD/expected.vctl.sha256"
inner_sha "$(ls "$pubfeed"/vectra-reporter_*_"$ARCH".ipk)" ./usr/sbin/vectra-reporter > "$BUILD/expected.reporter.sha256"
sed -n "s/^BAKED_FEED_KEY_ID=//p" "$PUB/install.sh" | tr -d "'" > "$BUILD/expected.keyid"
echo "  expected: vectra-controller-pro $(cat "$BUILD/expected.version"), vctl $(cut -c1-16 "$BUILD/expected.vctl.sha256"), reporter $(cut -c1-16 "$BUILD/expected.reporter.sha256"), key $(cat "$BUILD/expected.keyid")"

# ----------------------------------------------------------------- serve ----

step "the WAN: a network of its own, Vectra's hosts on $WAN_IP"
docker network create --subnet "$SUBNET" "$NET" > /dev/null
WANOUT="$LOGS/wan"
rm -rf "$WANOUT"
mkdir -p "$WANOUT"
docker run -d --name "vctl-pilot-wan-$$" --network "$NET" --ip "$WAN_IP" \
	-v "$BUILD/wan:/wan:ro" -v "$BUILD/mirror:/mirror:ro" -v "$WANOUT:/out" \
	"$IMAGE" /wan/wan -ip "$WAN_IP" -out /out -mirror /mirror > /dev/null
for _ in $(seq 1 30); do [[ -f "$WANOUT/ready" ]] && break; sleep 1; done
[[ -f "$WANOUT/ready" ]] || { docker logs "vctl-pilot-wan-$$" | tail -n 20; fail "the WAN side never came up"; }
echo "  DNS, mirror, stub panel and report sink up"

# ------------------------------------------------------------------- run ----

# One router per scenario, each its own address and MAC (vctl's identity
# follows the MAC): the WAN side tells them apart by address.
ip_of() { case "$1" in pilot) echo 44.77.0.10 ;; upgrade) echo 44.77.0.11 ;; upgrade06) echo 44.77.0.12 ;; esac; }
mac_of() { case "$1" in pilot) echo 02:77:00:00:00:10 ;; upgrade) echo 02:77:00:00:00:11 ;; upgrade06) echo 02:77:00:00:00:12 ;; esac; }
old_of() { case "$1" in upgrade) echo old ;; upgrade06) echo old06 ;; esac; }

run_scenario() { # <scenario>
	local s="$1" out name phases p phase how n
	out="$LOGS/$s"
	name="vctl-pilot-$s-$$"
	rm -rf "$out"
	mkdir -p "$out"
	cp "$BUILD"/expected.* "$out/"
	phases="PHASES_$s"
	n=0
	for p in ${!phases}; do
		phase="${p%%:*}"
		how="${p#*:}"
		echo "$phase" > "$out/phase"
		if [[ $n -eq 0 ]]; then
			docker run -d --name "$name" --privileged --network "$NET" --ip "$(ip_of "$s")" --mac-address "$(mac_of "$s")" \
				--hostname OpenWrt --tmpfs /tmp:rw,exec,nosuid,nodev,size=256m \
				-e "SCENARIO=$s" -e "WAN_IP=$WAN_IP" -e "ROUTER_IP=$(ip_of "$s")" \
				-e "OLD_FEED=$(old_of "$s")" -e "OLD_VERSION=$(cat "$BUILD/$(old_of "$s").version" 2> /dev/null)" \
				-v "$HERE:/stand:ro" -v "$out:/out" -v "$WANOUT:/wanctl" \
				"$IMAGE" /bin/sh /stand/router.sh > /dev/null
		else
			docker start "$name" > /dev/null
		fi
		n=$((n + 1))
		# A phase that installs downloads from OpenWrt's feeds; the reporter's
		# checks wait for cron's minutes.
		local w=0
		while [[ ! -f "$out/$phase.done" ]] && [[ $w -lt 1500 ]]; do
			docker inspect -f '{{.State.Running}}' "$name" 2> /dev/null | grep -q true || break
			sleep 5
			w=$((w + 5))
		done
		[[ -f "$out/$phase.done" ]] || {
			echo "STAND-FATAL $phase never finished (${w}s)" >> "$out/$phase.log"
			docker logs "$name" > "$out/container.log" 2>&1 || true
			return
		}
		case "$how" in
		reboot)
			# procd's own shutdown: the rc.d K scripts in order, then everything stops.
			docker exec "$name" /bin/sh /stand/router.sh shutdown >> "$out/$phase.log" 2>&1 || true
			docker stop -t 20 "$name" > /dev/null
			;;
		powercut) docker kill "$name" > /dev/null ;;
		end) docker logs "$name" > "$out/container.log" 2>&1 || true ;;
		esac
	done
}

step "scenarios: ${SELECTED[*]} (in parallel; each takes ~15 minutes, most of it cron's minutes)"
for s in "${SELECTED[@]}"; do
	phases="PHASES_$s"
	[[ -n "${!phases:-}" ]] || fail "no scenario $s (${ALL[*]})"
	run_scenario "$s" &
done
wait

# ----------------------------------------------------------------- verdict --

step "results"
bad=0
total=0
passed=0
for s in "${SELECTED[@]}"; do
	phases="PHASES_$s"
	for p in ${!phases}; do
		phase="${p%%:*}"
		log="$LOGS/$s/$phase.log"
		if [[ ! -f "$log" ]] || grep -q '^STAND-FATAL' "$log" || [[ ! -f "$LOGS/$s/$phase.done" ]]; then
			bad=1
			echo "  [$s $phase] did not finish: $(grep '^STAND-FATAL' "$log" 2> /dev/null | head -n 1)"
			continue
		fi
		grep '^RESULT' "$log" | sed "s/^RESULT /  [$s $phase] /" | grep -v ' PASS ' || true
		[[ $(grep -c '^RESULT' "$log" || true) -gt 0 ]] || { bad=1; echo "  [$s $phase] no assertions ran"; }
		total=$((total + $(grep -c '^RESULT' "$log" || true)))
		passed=$((passed + $(grep -c '^RESULT .* PASS' "$log" || true)))
		grep -q '^RESULT .* FAIL' "$log" && bad=1
	done
done
cp "$BUILD/published.manifest" "$LOGS/published.manifest"
if [[ $bad -eq 0 ]]; then
	printf '\n\033[1mPILOT PROVEN: %s/%s assertions passed (%s; OpenWrt %s; published %s).\033[0m\n' "$passed" "$total" "${SELECTED[*]}" "$OPENWRT_VERSION" "$(sed -n 's/^BAKED_VERSION=//p' "$PUB/install.sh" | tr -d "'")"
else
	printf '\n\033[1mPILOT NOT PROVEN: %s/%s passed — logs in %s\033[0m\n' "$passed" "$total" "$LOGS"
	exit 1
fi
