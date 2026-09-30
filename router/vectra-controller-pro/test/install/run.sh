#!/usr/bin/env bash
# The universal installer on real OpenWrt userland: every path it can take.
#
#   ./test/install/run.sh                        # every scenario, aarch64_generic
#   ./test/install/run.sh lifecycle standby      # just these
#   INSTALL_ARCHS="aarch64_generic x86_64" ./test/install/run.sh lifecycle
#
# It builds the pro feed for the architectures under test, signs it with a
# throwaway key, serves it from a container on a private docker network, and
# runs each scenario (test/install/router.sh) in a fresh
# openwrt/rootfs:<arch>-<version> container that reaches OpenWrt's own feeds
# over the internet. A non-native architecture needs qemu binfmt handlers on the
# docker host. Point docker at Colima as for the data-plane stand:
# DOCKER_CONTEXT=colima ./test/install/run.sh
#
# Scenarios:
#   lifecycle         install on a fresh router, run again, --uninstall, --purge
#   geodata-links     links an earlier vectra-geodata left in /usr/share/v2ray go
#   check             --check changes nothing
#   standby           next to the old agent and PassWall2: installed, left off
#   standby-upgrade   the owner's router as it is: PassWall2, the old agent and
#                     an old vctl left off — upgraded, still off, PassWall2's
#                     process never interrupted
#   passwall          PassWall2 + official xray-core: refused without --yes,
#                     taken over with it (xray upgraded), given back on stop
#   passwall-upgrade  taken over from PassWall2, then upgraded: vctl
#                     restarted in place, PassWall2 never ran meanwhile
#   dnsmasq-rollback  a dnsmasq-full that never runs: the old dnsmasq comes back
#   mirror            downloads.openwrt.org unreachable: through a mirror
#   refuse-*          arch, apk, release, memory, storage, conflict, fleet,
#                     signature, feed-down: refused, the router unchanged
set -euo pipefail
export COPYFILE_DISABLE=1

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE="$(cd "$HERE/../.." && pwd)"
BUILD="$HERE/build"
LOGS="$HERE/logs"
OPENWRT_VERSION="${OPENWRT_VERSION:-24.10.8}"
read -r -a ARCHS <<< "${INSTALL_ARCHS:-aarch64_generic}"
NET="vctl-install-$$"
FEED_NAME="vctl-install-feed-$$"
FEED_BASE="http://$FEED_NAME:8080"
# The mirror scenario proves the rewrite of OpenWrt's own feeds against a public
# OpenWrt mirror: the stand never talks to Vectra's production hosts (the same
# host that runs openwrt-cache is the control plane every router checks in with).
OPENWRT_MIRROR="${OPENWRT_MIRROR:-https://mirror-03.infra.openwrt.org}"
PARALLEL="${INSTALL_PARALLEL:-4}"
ALL=(lifecycle geodata-links check standby standby-upgrade passwall passwall-upgrade dnsmasq-rollback mirror
	refuse-arch refuse-apk refuse-release refuse-memory refuse-storage
	refuse-conflict refuse-fleet refuse-signature refuse-feed-down)

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
fail() { printf '\n\033[1mFATAL: %s\033[0m\n' "$*" >&2; exit 2; }

command -v docker > /dev/null || fail "docker not found"
command -v go > /dev/null || fail "go not found"
if [[ $# -gt 0 ]]; then SELECTED=("$@"); else SELECTED=("${ALL[@]}"); fi

cleanup() {
	docker rm -f "$FEED_NAME" > /dev/null 2>&1 || true
	docker network rm "$NET" > /dev/null 2>&1 || true
}
trap cleanup EXIT

# ----------------------------------------------------------------- build ----

step "feed for ${ARCHS[*]}"
rm -rf "$BUILD/feed" "$BUILD/broken" "$BUILD/newer" "$BUILD/upgrade" "$BUILD/fixtures"
mkdir -p "$BUILD" "$LOGS" "$BUILD/fixtures"
arch_args=()
for a in "${ARCHS[@]}"; do arch_args+=(--arch "$a"); done
"$MODULE/scripts/build-pro-feed.sh" --out "$BUILD/feed" "${arch_args[@]}" | tail -n 4
"$MODULE/scripts/sign-pro-feed.sh" --feed "$BUILD/feed" --key-dir "$BUILD/keys" --new-key --feed-url "$FEED_BASE/pro"
FEEDTOOL="$MODULE/dist/cache/feedtool"
cp "$BUILD/feed/install.sh" "$BUILD/fixtures/install.sh"

step "fixtures"
fake() { # <package> <version>: a package that only has to exist
	local d
	d="$(mktemp -d)"
	mkdir -p "$d/data/usr/share/doc/$1" "$d/ctrl"
	echo "stands in for $1 in test/install" > "$d/data/usr/share/doc/$1/README"
	printf 'Package: %s\nVersion: %s\nArchitecture: all\nMaintainer: test\nDescription: test/install fixture\n' "$1" "$2" > "$d/ctrl/control"
	"$FEEDTOOL" ipk -data "$d/data" -control "$d/ctrl" -out "$BUILD/fixtures/$1.ipk"
	rm -rf "$d"
}
fake podkop 0.2.5-r1
fake vectra-controller-agent 0.1.13-r40
fake luci-app-passwall2 26.8.10-r1
# A key the feed is not signed with.
"$FEEDTOOL" keygen -pub "$BUILD/fixtures/rogue.pub" -sec "$BUILD/fixtures/rogue.sec" -comment rogue > "$BUILD/fixtures/rogue.id"
# The same feed, plus a dnsmasq-full newer than OpenWrt's that installs a README
# and nothing that runs: the swap must put the old dnsmasq back.
fake dnsmasq-full 99.0-r1
mv "$BUILD/fixtures/dnsmasq-full.ipk" "$BUILD/dnsmasq-full-broken.ipk"
for a in "${ARCHS[@]}"; do
	mkdir -p "$BUILD/broken/$a"
	cp "$BUILD/feed/$a/"*.ipk "$BUILD/broken/$a/"
	cp "$BUILD/dnsmasq-full-broken.ipk" "$BUILD/broken/$a/dnsmasq-full_99.0-r1_all.ipk"
	"$FEEDTOOL" index -arch "$a" "$BUILD/broken/$a" > /dev/null
	"$FEEDTOOL" sign -sec "$BUILD/keys/vectra-pro.sec" -in "$BUILD/broken/$a/Packages"
done
# The feed's vctl relabelled 0.2.3-r1 with the Makefile's plain Depends: what
# an earlier canary left installed (and off) on the owner's router.
old="$(mktemp -d)"
mkdir -p "$old/data" "$old/ctrl"
tar -xzOf "$BUILD/feed/${ARCHS[0]}/vectra-controller-pro_"*"_${ARCHS[0]}.ipk" ./data.tar.gz | tar -xzf - -C "$old/data"
tar -xzOf "$BUILD/feed/${ARCHS[0]}/vectra-controller-pro_"*"_${ARCHS[0]}.ipk" ./control.tar.gz | tar -xzf - -C "$old/ctrl"
sed -e 's/^Version: .*/Version: 0.2.3-r1/' -e 's/, vectra-geodata//' -e 's/, vectra-reporter//' -e 's/xray-core (>= [^)]*)/xray-core/' "$old/ctrl/control" > "$old/control.new"
mv "$old/control.new" "$old/ctrl/control"
"$FEEDTOOL" ipk -data "$old/data" -control "$old/ctrl" -out "$BUILD/fixtures/vectra-controller-pro-old.ipk"
rm -rf "$old"
# PassWall2's xray-core, newer than the feed's (the owner's router has 26.4.25),
# and a feed that also offers an even newer one: an upgrade must leave
# PassWall's alone rather than take the newest from any feed.
pw="$(mktemp -d)"
mkdir -p "$pw/data" "$pw/ctrl"
tar -xzOf "$BUILD/feed/${ARCHS[0]}/xray-core_"*"_${ARCHS[0]}.ipk" ./data.tar.gz | tar -xzf - -C "$pw/data"
tar -xzOf "$BUILD/feed/${ARCHS[0]}/xray-core_"*"_${ARCHS[0]}.ipk" ./control.tar.gz | tar -xzf - -C "$pw/ctrl"
sed -e 's/^Version: .*/Version: 26.4.25-r1/' "$pw/ctrl/control" > "$pw/control.new"
mv "$pw/control.new" "$pw/ctrl/control"
"$FEEDTOOL" ipk -data "$pw/data" -control "$pw/ctrl" -out "$BUILD/fixtures/xray-core-passwall.ipk"
rm -rf "$pw"
fake xray-core 99.0-r1
mv "$BUILD/fixtures/xray-core.ipk" "$BUILD/xray-core-newest.ipk"
for a in "${ARCHS[@]}"; do
	mkdir -p "$BUILD/newer/$a"
	cp "$BUILD/feed/$a/"*.ipk "$BUILD/newer/$a/"
	rm -f "$BUILD/newer/$a/xray-core_"*.ipk
	cp "$BUILD/xray-core-newest.ipk" "$BUILD/newer/$a/xray-core_99.0-r1_all.ipk"
	"$FEEDTOOL" index -arch "$a" "$BUILD/newer/$a" > /dev/null
	"$FEEDTOOL" sign -sec "$BUILD/keys/vectra-pro.sec" -in "$BUILD/newer/$a/Packages"
done
# The same feed with its vctl relabelled 9.9.9-r1: an upgrade of the one the
# feed installs, which runs the installed package's own prerm.
for a in "${ARCHS[@]}"; do
	up="$(mktemp -d)"
	mkdir -p "$up/data" "$up/ctrl" "$BUILD/upgrade/$a"
	tar -xzOf "$BUILD/feed/$a/vectra-controller-pro_"*"_$a.ipk" ./data.tar.gz | tar -xzf - -C "$up/data"
	tar -xzOf "$BUILD/feed/$a/vectra-controller-pro_"*"_$a.ipk" ./control.tar.gz | tar -xzf - -C "$up/ctrl"
	sed -i.bak -e 's/^Version: .*/Version: 9.9.9-r1/' "$up/ctrl/control" && rm -f "$up/ctrl/control.bak"
	cp "$BUILD/feed/$a/"*.ipk "$BUILD/upgrade/$a/"
	rm -f "$BUILD/upgrade/$a/vectra-controller-pro_"*.ipk
	"$FEEDTOOL" ipk -data "$up/data" -control "$up/ctrl" -out "$BUILD/upgrade/$a/vectra-controller-pro_9.9.9-r1_$a.ipk"
	"$FEEDTOOL" index -arch "$a" "$BUILD/upgrade/$a" > /dev/null
	"$FEEDTOOL" sign -sec "$BUILD/keys/vectra-pro.sec" -in "$BUILD/upgrade/$a/Packages"
	rm -rf "$up"
done
ls "$BUILD/fixtures"

# ----------------------------------------------------------------- serve ----

step "feed server on a private network"
docker network create "$NET" > /dev/null
# OpenWrt's own uhttpd serves it, from the image the scenarios run anyway.
docker run -d --rm --name "$FEED_NAME" --network "$NET" --platform linux/aarch64_generic \
	-v "$BUILD/feed:/www/pro:ro" -v "$BUILD/broken:/www/broken:ro" -v "$BUILD/newer:/www/newer:ro" -v "$BUILD/upgrade:/www/upgrade:ro" \
	"openwrt/rootfs:aarch64_generic-$OPENWRT_VERSION" /usr/sbin/uhttpd -f -p 0.0.0.0:8080 -h /www > /dev/null
echo "  $FEED_BASE/pro (signed, key $("$FEEDTOOL" fingerprint -pub "$BUILD/keys/vectra-pro.pub"))"

# ------------------------------------------------------------------- run ----

# The rootfs images name their platform by OpenWrt architecture
# (linux/mips_24kc), except x86_64, which is plain linux/amd64. Docker needs it
# spelled out to pull a foreign one; qemu runs it.
platform_of() { [[ "$1" == x86_64 ]] && echo linux/amd64 || echo "linux/$1"; }

# What qemu-user does not emulate, passed to router.sh:
#   JAIL=0     procd's jail needs namespaces qemu-user lacks;
#   DNS=runs   under qemu-mips dnsmasq's replies never come back to the client
#              (native, ARM and x86 answer) — the checks see it run instead;
#   QEMU_CPU   qemu-i386's default CPU has no SSE2, which Go and xray's 386
#              builds need and every i386_pentium4 router has. Even with it,
#              Go's 386 runtime dies with SIGSEGV under qemu-user (its TLS
#              through %gs): i386_pentium4 is built but NOT run here.
HOST_ARCH="$(docker info --format '{{.Architecture}}' 2>/dev/null || echo unknown)"
native() {
	case "$HOST_ARCH/$1" in
	aarch64/aarch64_* | arm64/aarch64_* | x86_64/x86_64) return 0 ;;
	*) return 1 ;;
	esac
}
emulation_env() { # <arch> -> -e flags
	native "$1" && { echo "-e JAIL=1 -e DNS=answers"; return; }
	case "$1" in
	mips*) echo "-e JAIL=0 -e DNS=runs" ;;
	i386_*) echo "-e JAIL=0 -e DNS=answers -e QEMU_CPU=max" ;;
	*) echo "-e JAIL=0 -e DNS=answers" ;;
	esac
}

run_one() { # <arch> <scenario>
	local log="$LOGS/$1-$2.log" emu
	read -r -a emu <<< "$(emulation_env "$1")"
	docker run --rm --privileged --network "$NET" --platform "$(platform_of "$1")" \
		-e "SCENARIO=$2" -e "FEED_BASE=$FEED_BASE" -e "OPENWRT_MIRROR=$OPENWRT_MIRROR" "${emu[@]}" \
		-v "$BUILD/fixtures:/fixtures:ro" \
		-v "$HERE/router.sh:/test/router.sh:ro" \
		-v "$MODULE/test/dataplane/stand/legacy-stub:/stub:ro" \
		"openwrt/rootfs:$1-$OPENWRT_VERSION" /bin/sh /test/router.sh > "$log" 2>&1 || true
	printf '  %-18s %-17s %s\n' "$1" "$2" "$(grep -c '^RESULT .* PASS' "$log" || true) pass, $(grep -c '^RESULT .* FAIL' "$log" || true) fail$(grep -q '^INSTALL-EXIT ' "$log" || echo ', DID NOT FINISH')"
}

step "scenarios (${#SELECTED[@]} x ${#ARCHS[@]}, $PARALLEL at a time)"
for a in "${ARCHS[@]}"; do for s in "${SELECTED[@]}"; do rm -f "$LOGS/$a-$s.log"; done; done
n=0
for a in "${ARCHS[@]}"; do
	for s in "${SELECTED[@]}"; do
		run_one "$a" "$s" &
		n=$((n + 1))
		[[ $((n % PARALLEL)) -ne 0 ]] || wait
	done
done
wait

# ----------------------------------------------------------------- verdict --

step "results"
bad=0
total=0
passed=0
for a in "${ARCHS[@]}"; do
	for s in "${SELECTED[@]}"; do
		log="$LOGS/$a-$s.log"
		grep '^RESULT' "$log" | sed "s/^RESULT /  [$a $s] /" | grep -v ' PASS ' || true
		grep -q '^INSTALL-EXIT 0$' "$log" || { bad=1; grep -E '^STAND-FATAL' "$log" || true; }
		[[ $(grep -c '^RESULT' "$log" || true) -gt 0 ]] || { bad=1; echo "  [$a $s] no assertions ran"; }
		total=$((total + $(grep -c '^RESULT' "$log" || true)))
		passed=$((passed + $(grep -c '^RESULT .* PASS' "$log" || true)))
	done
done
if [[ $bad -eq 0 ]]; then
	printf '\n\033[1mINSTALLER PROVEN: %s/%s assertions passed on %s (OpenWrt %s).\033[0m\n' "$passed" "$total" "${ARCHS[*]}" "$OPENWRT_VERSION"
else
	printf '\n\033[1mINSTALLER NOT PROVEN: %s/%s passed — logs in %s\033[0m\n' "$passed" "$total" "$LOGS"
	exit 1
fi
