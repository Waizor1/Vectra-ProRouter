#!/usr/bin/env bash
# One command: build and run the vctl data-plane stand, including the negative
# controls that prove it can still detect the defects that took a customer's
# home internet down.
#
#   ./test/dataplane/run.sh              # every scenario + the self-check
#   ./test/dataplane/run.sh full ipv6    # just these
#
# Requires: docker pointed at a Linux host with a real kernel (Colima, a Linux
# box, or any docker context whose kernel has nft_tproxy/nft_socket), and Go on
# the host to cross-compile vctl.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE="$(cd "$HERE/../.." && pwd)"
BUILD="$HERE/build"
LOGS="$HERE/logs"
# One tag per checkout when several run the stand on the same docker host:
# each scenario resolves the tag when it starts, so a build from another
# worktree in between would run THAT tree's code.
IMAGE="${STAND_IMAGE:-vctl-dataplane-stand:latest}"

XRAY_VERSION="${XRAY_VERSION:-v26.3.27}"
XRAY_ASSET="Xray-linux-arm64-v8a.zip"
XRAY_URL="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${XRAY_ASSET}"
GEO_BASE="${GEO_BASE:-https://api.vectra-pro.net/artifacts/canary/vctl}"
# MODE=passwall-real*: the real luci-app-passwall2 bundle (the ipks the fleet's
# test router runs, aarch64_cortex-a53) and the legacy agent's real cron
# watchdog, both mounted into the container rather than baked into the image.
PASSWALL_BUNDLE="${STAND_PASSWALL_BUNDLE:-$BUILD/passwall2-26.8.10}"
LEGACY_WATCHDOG="$MODULE/openwrt/testdata/vectra-controller-watchdog.main"
OPENWRT_PLATFORM="linux/aarch64_generic"

# Scenario -> assertions that MUST report FAIL for the stand to be trustworthy.
# A mode with an empty list is the inverse: nothing may fail.
#
# nft_tproxy_hits is deliberately NOT in the no-routing list: with the policy
# route missing the TPROXY rule still matches every client packet (measured:
# 43,241) while nothing is carried. Delivery is what breaks, so
# nft_tproxy_delivered is the counter assertion that must fail. See the README.
MUSTFAIL_no_routing="tcp_through_proxy tls_through_proxy dns_through_proxy udp_through_proxy nft_tproxy_delivered"
MUSTFAIL_no_mark="no_egress_loop"
# The historical regression: stop/remove used to leave the router with NO
# controller at all, recoverable only by a site visit.
MUSTFAIL_procd_no_restore="stop_restores_legacy prerm_restores_legacy passwall_restored_on_stop passwall_restored_on_remove"
# The state the FIRST canary was rolled back from: the hand-over disables the
# legacy agent but never takes the OTHER proxy stack down, so PassWall keeps
# running — its own xray, its own fwmark policy route — alongside vctl.
MUSTFAIL_procd_no_passwall_stop="passwall_stopped_on_handover passwall_stays_down"
# The defect MODE=ipv6-killswitch found on its first run: without the output
# chain's `ct state established,related return`, the router's replies to its
# own LAN clients take the tproxy fwmark and are routed to `local ::/0 dev lo`.
# Invisible on v4, where every RFC1918 LAN is inside @bypass4.
MUSTFAIL_ipv6_no_ct_return="v6_router_reachable"
# Surgical v6 control: identical run with the v6 fwmark policy route removed and
# the v4 one left in place, so v4_control_tcp must still PASS in that scenario.
MUSTFAIL_ipv6_no_v6_route="v6_plumbing_applied v6_tcp_through_proxy v6_tls_through_proxy v6_dns_through_proxy v6_udp_through_proxy v6_nft_tproxy_delivered"
# The refusal's control: the same run with the ruleset that carries IPv6 (no
# -refuse-ipv6), so the client's IPv6 goes through and nothing is counted.
MUSTFAIL_ipv6_refused_off="v6_refused_at_once v6_refusal_counted"
# The refusal with no LAN device known refuses IPv6 coming into the LAN too.
MUSTFAIL_ipv6_refused_nolan="v6_inbound_left_to_the_firewall"
# real_tproxy_delivered, NOT nft_tproxy_delivered: against a real provider the
# absolute egress_exempt counter is non-zero on a dead data plane, because
# burstObservatory dials every subject outbound at start. This control caught
# that — see the note above assert_real_tproxy_delivered.
MUSTFAIL_real_egress_no_routing="real_tcp_through_node real_tls_through_node real_dns_through_node real_public_ip_differs real_tproxy_delivered"
# The kill-switch controls. Each is the negative control for a DIFFERENT claim,
# which is why there are four of them rather than one:
#
#   disarmed  — the armed ruleset with the kill-switch's terminal drops
#               (vctl_killswitch_drops, vctl_unproxied_other; prerouting and
#               forward) surgically removed. Proves the leak assertions are
#               watching those rules and not something incidental.
#   shadow    — the SHIPPED fleet default (killSwitch:false). Same stimulus, and
#               the stand's own guard counts the packets reaching plain
#               forwarding, which is the direct evidence the armed run's zero is
#               the ruleset's doing.
#   no-routing— armed with the fwmark policy route missing, so traffic that
#               SHOULD have been carried is eaten by the forward guard. Proves
#               the healthy-LAN assertion can see a kill switch taking the LAN
#               down.
#   policydrop— the defect this mode found on its first run: `policy drop` on the
#               prerouting chain, every rule untouched. In an nftables base chain
#               `return` falls through to the policy, so all five exemptions
#               became silent drops. Kept as the regression gate.
MUSTFAIL_killswitch_disarmed="ks_drops_when_xray_down ks_leak_blocked_at_forward ks_leak_instrument"
MUSTFAIL_killswitch_shadow="ks_drops_when_xray_down ks_leak_blocked_at_forward"
MUSTFAIL_killswitch_no_routing="tcp_through_proxy tls_through_proxy dns_through_proxy udp_through_proxy nft_tproxy_delivered ks_lan_not_dropped_when_healthy"
MUSTFAIL_killswitch_policydrop="tcp_through_proxy ks_lan_not_dropped_when_healthy ks_router_itself_reachable ks_bypass_forwarded"
# The REAL PassWall2 with the takeover's hold on its own switch stripped out:
# the switch stays on, and "midnight" — the restart subscribe.lua ends every
# nightly run with, and subscribe.lua itself — brings PassWall's whole stack
# back next to vctl's. What the test router showed before the switch existed.
MUSTFAIL_passwall_real_no_switch="pw_trial_switch_off pw_midnight_restart pw_midnight_subscribe"
# The router before the failover watchdog: the main balancer's node stops
# answering and nothing but the provider's observatory (300 s) moves it.
MUSTFAIL_failover_no_watchdog="failover_recovers_in_10s"
# The router before r35: the DNS inbound and outbound on the provider's level,
# every query's session alive for minutes after its answer.
MUSTFAIL_dns_sessions_provider_idle="dns_sessions_die_in_seconds"
# The router before r35 after a reboot: xray's first round ran with the nodes'
# names resolving nowhere, and the nodes wait for its next random probe.
MUSTFAIL_reprobe_no_watchdog="reprobe_alive_in_40s"
# No DNS door (UCI dns_rate '0'): one device's DNS storm reaches xray whole.
MUSTFAIL_dns_door_off="dns_door_holds_a_storm"

ALL_MODES=(full no-routing no-mark procd procd-no-restore procd-no-passwall-stop ipv6 ipv6-no-v6-route ipv6-dual ipv6-killswitch ipv6-no-ct-return ipv6-refused ipv6-refused-off ipv6-refused-nolan
	killswitch killswitch-disarmed killswitch-shadow killswitch-no-routing killswitch-policydrop
	apply-local ui setup passwall-real passwall-real-no-switch reports failover failover-no-watchdog failover-stage services ru-kernel exitcheck dns-sessions dns-sessions-provider-idle dns-door-off reprobe reprobe-no-watchdog
	real-egress real-egress-no-routing real-egress-own)

# macOS ships bash 3.2, which has no associative arrays. Plain case statements.
short_of() {
	case "$1" in
	no-routing)             echo no-rt ;;
	no-mark)                echo no-mk ;;
	procd-no-restore)       echo procd-nr ;;
	procd-no-passwall-stop) echo procd-npw ;;
	ipv6-no-v6-route)       echo ipv6-nr ;;
	ipv6-refused)           echo ipv6-ref ;;
	ipv6-refused-off)       echo ipv6-refoff ;;
	ipv6-refused-nolan)     echo ipv6-refnolan ;;
	killswitch)             echo ks ;;
	killswitch-disarmed)    echo ks-dis ;;
	killswitch-shadow)      echo ks-shdw ;;
	killswitch-no-routing)  echo ks-nort ;;
	killswitch-policydrop)  echo ks-poldr ;;
	apply-local)            echo aplocal ;;
	ui)                     echo ui ;;
	setup)                  echo setup ;;
	passwall-real)          echo pw-real ;;
	reports)                echo reports ;;
	failover)               echo fo ;;
	failover-no-watchdog)   echo fo-nowd ;;
	failover-stage)         echo fo-stage ;;
	services)               echo svc ;;
	ru-kernel)              echo ru-k ;;
	dns-sessions)           echo dns-s ;;
	dns-sessions-provider-idle) echo dns-s-pi ;;
	dns-door-off)           echo dns-nodoor ;;
	reprobe)                echo reprobe ;;
	reprobe-no-watchdog)    echo reprobe-nowd ;;
	exitcheck)              echo exit ;;
	passwall-real-no-switch) echo pw-nosw ;;
	real-egress)            echo real ;;
	real-egress-no-routing) echo real-nr ;;
	real-egress-own)        echo real-own ;;
	*)                      echo "$1" ;;
	esac
}

explain_of() {
	case "$1" in
	full)                   echo "everything applied; every assertion must PASS" ;;
	no-routing)             echo "defect 1: 'firewall routing' skipped" ;;
	no-mark)                echo "defect 2: xray egress sockets carry no fwmark" ;;
	procd)                  echo "gap 1: install, start via /etc/init.d, respawn, stop, prerm" ;;
	procd-no-restore)       echo "defect 3: stop/prerm no longer hand the router back" ;;
	procd-no-passwall-stop) echo "gap 1 control: the hand-over never stops PassWall — two proxy stacks" ;;
	ipv6)                   echo "gap 2: IPv6 driven through the proxy, shipped listenIP 0.0.0.0" ;;
	ipv6-no-v6-route)       echo "gap 2 control: v6 policy route removed, v4 route left alone" ;;
	ipv6-dual)              echo "gap 2: the same with listenIP '::'" ;;
	ipv6-killswitch)        echo "gap 2+4: the kill switch ARMED, on the v6 data path" ;;
	ipv6-no-ct-return)      echo "gap 2 control: the router's own v6 replies marked and black-holed" ;;
	ipv6-refused)           echo "IPv6 off (the daemon's default): the LAN's IPv6 to the outside refused at once and counted, the router's own v6 and IPv4 untouched" ;;
	ipv6-refused-off)       echo "IPv6 off control: the ruleset that carries IPv6 — the refusal assertions must FAIL" ;;
	ipv6-refused-nolan)     echo "IPv6 off with no LAN device known: IPv6 from outside into the LAN refused too — v6_inbound_left_to_the_firewall must FAIL" ;;
	killswitch)             echo "gap 4: the kill switch ARMED — fail-closed, and what must still work" ;;
	killswitch-disarmed)    echo "gap 4 control: the kill switch's terminal drops removed, everything else intact" ;;
	killswitch-shadow)      echo "gap 4 control: the shipped fleet default (killSwitch:false), same stimulus" ;;
	killswitch-no-routing)  echo "gap 4 control: armed with no policy route — the LAN gets eaten" ;;
	killswitch-policydrop)  echo "gap 4 control: 'policy drop' on prerouting, the defect this mode found" ;;
	apply-local)            echo "gap 5: 'vctl apply-local' — a data plane with NO panel-delivered config" ;;
	ui)                     echo "gap 6: the router UI — package, rpcd, LuCI; pins and locations proven on packets" ;;
	ui-hold)                echo "gap 6, held open for a browser on 127.0.0.1:\${STAND_UI_PORT:-8080}" ;;
	setup)                  echo "gap 7: the setup wizard, the claim (code, rotation, hash, owner), and the owner releasing a configured router" ;;
	reports)                echo "the reporter end to end: a vctl crash, the data plane without xray, an unexpected reboot, switched off — every report verified by a stub Connect" ;;
	failover)               echo "the failover watchdog: the main balancer's node stops answering, new connections move within 10 s; logged, reported, released" ;;
	failover-no-watchdog)   echo "failover control: the watchdog off — the provider's observatory decides" ;;
	failover-stage)         echo "failover, the entry as served 2026-09-30: a one-node BL-MAIN goes onto its fallback stage within 10 s" ;;
	services)               echo "a server per service: TikTok through a chosen country in the router UI, the rest untouched; a dead country falls back to TikTok's own path" ;;
	exitcheck)              echo "the router's own check of its exits: an exit that carries no blocked site leaves BL-MAIN within a round, stays out across a restart, is never judged without a witness, and comes back after two clean rounds" ;;
	ru-kernel)              echo "Russian networks by the kernel: a geoip:ru address leaves without xray, the bridge's own address keeps the bridge, a proxied name with a Russian address gets a FakeDNS answer and the node" ;;
	dns-sessions)           echo "DNS sessions live seconds: a burst of unique names through the router's dnsmasq, every one answered, xray back to its baseline within 20 s; one device's DNS storm (1000 names a second) held at the router's door" ;;
	dns-door-off)           echo "DNS door control: UCI dns_rate '0' — one device's storm reaches xray whole" ;;
	dns-sessions-provider-idle) echo "DNS sessions control: the DNS inbound and outbound on the provider's level — still alive 20 s later" ;;
	reprobe)                echo "the nodes' names come back: xray started with them resolving nowhere holds both nodes dead; the watchdog restarts it once and both are alive within 40 s" ;;
	reprobe-no-watchdog)    echo "reprobe control: the watchdog off — the nodes wait for xray's own next probe" ;;
	setup-hold)             echo "gap 7, an unboxed router held open for a browser on 127.0.0.1:\${STAND_UI_PORT:-8080}" ;;
	passwall-real)          echo "gap 8: the REAL PassWall2 26.8.10 next to vctl — trial, off, keep, deadman, midnight, WAN ifup, the agent's watchdog, reboots" ;;
	passwall-real-no-switch) echo "gap 8 control: the takeover leaves PassWall's own switch on — two stacks at midnight" ;;
	real-egress)            echo "gap 3: REAL subscription, REAL nodes, synthetic HWID" ;;
	real-egress-no-routing) echo "gap 3 control: real nodes with no policy route" ;;
	real-egress-own)        echo "gap 3 with the router's own agent: \$STAND_SUB_URL/json fetched by 'vctl subscribe fetch -signed'" ;;
	*)                      echo "?" ;;
	esac
}

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
warn() { printf '  !! %s\n' "$*" >&2; }
fail() { printf '\n\033[1mFATAL: %s\033[0m\n' "$*" >&2; exit 2; }

mustfail_of() { # <mode> -> the MUSTFAIL_* list for it, or empty
	local var="MUSTFAIL_${1//-/_}"
	echo "${!var:-}"
}

# ------------------------------------------------------------------ host ----

step "host preflight"
command -v docker >/dev/null || fail "docker not found"
command -v go     >/dev/null || fail "go not found (needed to cross-compile vctl)"
CONTEXT="$(docker context show 2>/dev/null || echo unknown)"
echo "  docker context : $CONTEXT"
echo "  kernel         : $(docker run --rm alpine:3.20 uname -sr 2>/dev/null || echo '?')"

# TPROXY lives in nft_tproxy/nft_socket. Autoload usually handles it, but a
# freshly booted VM may not have them; loading is idempotent.
if [[ "$CONTEXT" == colima* ]] && command -v colima >/dev/null; then
	colima ssh -- sudo modprobe nft_tproxy nft_socket 2>/dev/null \
		&& echo "  modules        : nft_tproxy nft_socket loaded in the Colima VM" \
		|| warn "modprobe in the Colima VM failed; relying on autoload"
elif [[ "$(uname -s)" == "Linux" ]]; then
	sudo modprobe nft_tproxy nft_socket 2>/dev/null \
		&& echo "  modules        : nft_tproxy nft_socket loaded" \
		|| warn "modprobe failed; relying on autoload"
else
	warn "unknown docker host type; relying on nft module autoload"
fi

# The live subscription for the real-egress modes. It is a SECRET: it is read
# from a gitignored file, never echoed, and handed to the container by NAME
# (`docker run -e STAND_SUB_URL`) so the value never appears in a command line.
if [[ -z "${STAND_SUB_URL:-}" ]]; then
	SUB_URL_FILE="${STAND_SUB_URL_FILE:-}"
	if [[ -z "$SUB_URL_FILE" ]]; then
		for cand in \
			"$(git -C "$MODULE" rev-parse --show-toplevel 2>/dev/null || true)/.omc/autopilot/secrets/sub-url.txt" \
			"$(dirname "$(git -C "$MODULE" rev-parse --git-common-dir 2>/dev/null || echo /nonexistent)")/.omc/autopilot/secrets/sub-url.txt"
		do
			[[ -f "$cand" ]] && { SUB_URL_FILE="$cand"; break; }
		done
	fi
	if [[ -n "$SUB_URL_FILE" && -f "$SUB_URL_FILE" ]]; then
		STAND_SUB_URL="$(tr -d ' \t\r\n' < "$SUB_URL_FILE")"
		export STAND_SUB_URL
		echo "  subscription   : available (value withheld)"
	fi
fi
[[ -n "${STAND_SUB_URL:-}" ]] || echo "  subscription   : NOT available — real-egress modes will be skipped"

# ...and the User-Agent to fetch it with, which has NO default. The provider's
# anti-fraud sweep deletes a device on the first request whose "Happ…" agent is
# not the real client's "Happ/<version>/<OS>/<build>", and disables the account
# behind the subscription. The stand used to send "Happ/1.0" — exactly such an
# agent. Supply a verbatim client agent for the account the URL belongs to (the
# test account, never a customer's), or the real-egress modes are skipped. vctl
# itself refuses a malformed Happ agent (internal/uaguard) before any byte
# leaves, so a bad value here fails the fetch rather than the account.
if [[ -n "${STAND_SUB_URL:-}" && -z "${STAND_SUB_UA:-}" ]]; then
	echo "  subscription UA: NOT set (STAND_SUB_UA) — real-egress and real-egress-no-routing will be skipped (real-egress-own signs its own)"
fi

# ----------------------------------------------------------------- build ----

step "build artifacts"
mkdir -p "$BUILD" "$LOGS"

echo "  vctl: cross-compiling from $MODULE"
( cd "$MODULE" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
	go build -trimpath -ldflags "-s -w -X main.Version=dataplane-stand" \
	-o "$BUILD/vctl" ./cmd/vctl )
echo "  vectra-reporter, reportsink, standkey: cross-compiling"
( cd "$MODULE" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=stand" -o "$BUILD/vectra-reporter" ./cmd/vectra-reporter )
( cd "$MODULE" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o "$BUILD/reportsink" ./test/dataplane/reportsink )
( cd "$MODULE" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o "$BUILD/standkey" ./test/dataplane/standkey )
rm -rf "$BUILD/reporter-etc" && cp -R "$MODULE/openwrt/reporter/etc" "$BUILD/reporter-etc"

if [[ ! -x "$BUILD/xray" ]]; then
	echo "  xray: fetching $XRAY_VERSION"
	curl -fsSL "$XRAY_URL" -o "$BUILD/$XRAY_ASSET"
	if curl -fsSL "${XRAY_URL}.dgst" -o "$BUILD/${XRAY_ASSET}.dgst" 2>/dev/null; then
		want="$(awk '/^SHA2-256=|^SHA256=/{print $2; exit}' "$BUILD/${XRAY_ASSET}.dgst")"
		got="$(shasum -a 256 "$BUILD/$XRAY_ASSET" | awk '{print $1}')"
		if [[ -n "$want" && "$want" != "$got" ]]; then
			fail "xray checksum mismatch: want $want got $got"
		fi
		echo "  xray: sha256 verified against the release .dgst"
	else
		warn "no .dgst published for $XRAY_VERSION; checksum NOT verified"
	fi
	( cd "$BUILD" && unzip -oq "$XRAY_ASSET" xray && chmod +x xray )
fi
echo "  xray: $BUILD/xray"

for f in geoip.dat geosite.dat; do
	[[ -f "$BUILD/$f" ]] || curl -fsSL "$GEO_BASE/$f" -o "$BUILD/$f"
done
echo "  geo : $(ls -l "$BUILD/geoip.dat" "$BUILD/geosite.dat" | awk '{print $NF" ("$5"B)"}' | tr '\n' ' ')"

# The .ipk payload, straight out of the package tree, at the paths it installs.
rm -rf "$BUILD/pkg" "$BUILD/pkg-control"
mkdir -p "$BUILD/pkg" "$BUILD/pkg-control"
cp -R "$MODULE/openwrt/files/." "$BUILD/pkg/"
# GoPackage installs cmd/vctl here; the stand's freshly built binary takes its
# place so the .ipk the procd mode installs carries the code under test.
mkdir -p "$BUILD/pkg/usr/sbin"
cp "$BUILD/vctl" "$BUILD/pkg/usr/sbin/vctl"

# git stores the payload's scripts 0644 and the Makefile installs them with
# INSTALL_BIN (0755). The init script guards its helpers with `[ -x ]` — see
# unload_dataplane — so a 0644 payload turns a real step into a SILENT no-op.
# Today they happen to be executable in the working tree; a fresh clone would
# not be, and "works because of your checkout" is not something a stand may rest
# on. The list comes from the Makefile so it cannot drift.
while read -r rel; do
	[[ -n "$rel" && -f "$BUILD/pkg/$rel" ]] && chmod 0755 "$BUILD/pkg/$rel"
done < <(grep -o '(INSTALL_BIN) \./files/[^ ]*' "$MODULE/openwrt/Makefile" | sed 's|.*\./files/||')

# postinst/prerm are EXTRACTED from openwrt/Makefile rather than copied, so the
# procd mode can never drift from the maintainer scripts that actually ship.
# `$$` is make's escape for a literal `$`.
extract_maint() { # <postinst|prerm>
	awk -v want="define Package/\$(PKG_NAME)/$1" '
		$0 == want { inblk = 1; next }
		inblk && $0 == "endef" { exit }
		inblk { print }
	' "$MODULE/openwrt/Makefile" | sed 's/\$\$/$/g'
}
for s in postinst prerm postrm; do
	extract_maint "$s" > "$BUILD/pkg-control/$s"
	[[ -s "$BUILD/pkg-control/$s" ]] || fail "could not extract $s from openwrt/Makefile"
	grep -q '^#!/bin/sh' "$BUILD/pkg-control/$s" || fail "$s does not look like a shell script"
	chmod +x "$BUILD/pkg-control/$s"
done
PKG_VER="$(sed -n 's/^VECTRA_VERSION?=\(.*\)$/\1/p' "$MODULE/openwrt/Makefile")"
PKG_REL="$(sed -n 's/^VECTRA_RELEASE?=\(.*\)$/\1/p' "$MODULE/openwrt/Makefile")"
# No Depends: the stand image already carries every dependency and has no feed
# to resolve them from. Everything else mirrors the real control file.
cat > "$BUILD/pkg-control/control" <<EOF
Package: vectra-controller-pro
Version: ${PKG_VER:-0.0.0}-${PKG_REL:-0}
Architecture: aarch64_generic
Maintainer: Vectra
Section: net
Description: Vectra Controller Pro (xray-direct) — data-plane stand build
EOF
echo "/etc/config/vectra-controller-pro" > "$BUILD/pkg-control/conffiles"
echo "  ipk : maintainer scripts extracted from openwrt/Makefile ($(wc -c < "$BUILD/pkg-control/postinst" | tr -d ' ')B postinst, $(wc -c < "$BUILD/pkg-control/prerm" | tr -d ' ')B prerm, $(wc -c < "$BUILD/pkg-control/postrm" | tr -d ' ')B postrm)"

step "build stand image"
# The base image declares platform `linux/aarch64_generic` (an OpenWrt arch
# string, not a Docker one). BuildKit needs it verbatim to resolve the manifest;
# passing linux/arm64 instead would pick a different image and defeat the point.
docker build --platform "$OPENWRT_PLATFORM" -t "$IMAGE" "$HERE" 2>&1 | tail -5

# ------------------------------------------------------------------- run ----

overall=0
declare -a RAN=()

run_mode() {
	local mode="$1" expectation="$2"
	local log="$LOGS/$mode.log"
	step "scenario: $mode  ($expectation)"
	if [[ "$mode" == *-hold ]]; then
		# Detached and published: LuCI with the real router UI, for a browser.
		local name="vctl-$mode"
		docker rm -f "$name" >/dev/null 2>&1 || true
		docker run -d --name "$name" --privileged -e "MODE=$mode" \
			-p "127.0.0.1:${STAND_UI_PORT:-8080}:80" "$IMAGE" >/dev/null
		local i=0
		until docker logs "$name" 2>&1 | grep -q 'STAND-HOLD ready'; do
			i=$((i + 1)); [[ $i -lt 300 ]] || { docker logs "$name" 2>&1 | tail -40; fail "$mode never became ready"; }
			sleep 1
		done
		docker logs "$name" > "$log" 2>&1
		grep '^RESULT' "$log" || true
		bold "  LuCI: http://127.0.0.1:${STAND_UI_PORT:-8080}/cgi-bin/luci/admin/vectra  (root, empty password)"
		if [[ "$mode" == setup-hold ]]; then
			bold "  the panel names an owner:  docker exec $name sh -c 'echo \"Stand Owner\" > /tmp/setup-panel-owner'"
			bold "  the panel configures it:   docker exec $name sh /stand/setup/link.sh"
			bold "  the owner unbinds it:      docker exec $name touch /tmp/setup-panel-released"
		fi
		bold "  stop it with: docker rm -f $name"
		exit 0
	fi
	local extra=()
	if [[ "$mode" == passwall-real* ]]; then
		extra=(-v "$PASSWALL_BUNDLE:/stand/passwall2:ro" -v "$LEGACY_WATCHDOG:/stand/legacy-watchdog:ro")
	fi
	set +e
	docker run --rm --privileged -e "MODE=$mode" \
		${STAND_SKIP_INTERNET:+-e "STAND_SKIP_INTERNET=$STAND_SKIP_INTERNET"} \
		${STAND_SUB_URL:+-e STAND_SUB_URL} \
		${STAND_SUB_UA:+-e STAND_SUB_UA} \
		${STAND_PW_ONLY:+-e STAND_PW_ONLY} \
		${extra[@]+"${extra[@]}"} \
		"$IMAGE" 2>&1 | tee "$log"
	local rc=${PIPESTATUS[0]}
	set -e
	echo "  (container exit $rc; full log: $log)"
	RAN+=("$mode")
}

verdict_of() { # <log> <assertion-name>
	awk -v n="$2" '$1=="RESULT" && $2==n {print $3; exit}' "$1"
}

check_positive() { # <mode>
	local mode="$1" log="$LOGS/$1.log" bad=0
	while read -r name v; do
		[[ "$v" == PASS ]] || { warn "$mode: $name reported $v"; bad=1; }
	done < <(awk '$1=="RESULT"{print $2, $3}' "$log")
	grep -q 'STAND-EXIT 0 ' "$log" || bad=1
	[[ $(awk '$1=="RESULT"' "$log" | wc -l) -gt 0 ]] || { warn "$mode: no assertions ran"; bad=1; }
	return $bad
}

check_negative() { # <mode> <must-fail list>
	local mode="$1" log="$LOGS/$1.log" bad=0
	for name in $2; do
		local v; v="$(verdict_of "$log" "$name")"
		if [[ "$v" == FAIL ]]; then
			printf '  detected: %-24s FAIL (as required)\n' "$name"
		else
			warn "$mode: $name reported '${v:-<missing>}' but the stand must catch this defect"
			bad=1
		fi
	done
	return $bad
}

# Which scenarios to run: everything by default, or exactly what was asked for.
if [[ $# -gt 0 ]]; then
	SELECTED=("$@")
else
	SELECTED=("${ALL_MODES[@]}")
fi

for mode in "${SELECTED[@]}"; do
	if [[ "$mode" == real-egress* && -z "${STAND_SUB_URL:-}" ]]; then
		warn "skipping $mode: no subscription URL available"
		continue
	fi
	if [[ "$mode" == passwall-real* && ! -f "$PASSWALL_BUNDLE/luci-app-passwall2_26.8.10-r1_all.ipk" ]]; then
		warn "skipping $mode: no PassWall2 bundle at $PASSWALL_BUNDLE (STAND_PASSWALL_BUNDLE)"
		continue
	fi
	# real-egress-own sends the router's own agent, so it needs no STAND_SUB_UA.
	if [[ "$mode" == real-egress* && "$mode" != real-egress-own && -z "${STAND_SUB_UA:-}" ]]; then
		warn "skipping $mode: no STAND_SUB_UA (there is deliberately no default — see above)"
		continue
	fi
	run_mode "$mode" "$(explain_of "$mode")"
done

# bash 3.2 (what macOS ships) errors on "${RAN[@]}" under `set -u` when the array
# is empty, so bail before the self-check rather than in the middle of it.
[[ ${#RAN[@]} -gt 0 ]] || fail "no scenarios ran"

step "stand self-check"
for mode in "${RAN[@]}"; do
	mf="$(mustfail_of "$mode")"
	if [[ -z "$mf" ]]; then
		if check_positive "$mode"; then
			bold "  $(printf '%-22s' "$mode") OK — every assertion passed"
		else
			overall=1; bold "  $(printf '%-22s' "$mode") BROKEN"
		fi
	else
		if check_negative "$mode" "$mf"; then
			bold "  $(printf '%-22s' "$mode") OK — defect detected"
		else
			overall=1; bold "  $(printf '%-22s' "$mode") NOT DETECTED"
		fi
	fi
done

step "summary"
{
	printf '%-26s' ASSERTION
	for m in "${RAN[@]}"; do printf '%-11s' "$(short_of "$m")"; done
	printf '\n'
	# Union of assertion names, in first-seen order.
	names="$(for m in "${RAN[@]}"; do awk '$1=="RESULT"{print $2}' "$LOGS/$m.log"; done | awk '!seen[$0]++')"
	for n in $names; do
		printf '%-26s' "$n"
		for m in "${RAN[@]}"; do
			v="$(verdict_of "$LOGS/$m.log" "$n")"
			printf '%-11s' "${v:--}"
		done
		printf '\n'
	done
}

# Belt and braces on the secret: nothing the run produced may contain the URL.
if [[ -n "${STAND_SUB_URL:-}" ]]; then
	if grep -rlF "$STAND_SUB_URL" "$LOGS" "$BUILD" "$HERE/stand" 2>/dev/null | head -1 | grep -q .; then
		overall=1
		bold $'\nSECRET LEAK: the subscription URL appears in a stand artefact. Fix before committing.'
	else
		echo
		echo "  secret check: subscription URL absent from logs/, build/ and stand/"
	fi
fi

if [[ $overall -eq 0 ]]; then
	bold $'\nDATA PLANE PROVEN: traffic moved, counters non-zero, every defect still detectable.'
else
	bold $'\nSTAND FAILED — see the scenario logs above.'
fi
exit $overall
