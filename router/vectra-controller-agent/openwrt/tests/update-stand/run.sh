#!/usr/bin/env bash
# The legacy agent's self-update, as the panel generates it, on real OpenWrt
# userland: every way it can go wrong ends on the previous version, by itself.
#
#   ./run.sh                      # every scenario
#   ./run.sh crash reboot         # just these
#
# Built here: fake vectra-controller-agent / luci-app-vectra-controller ipks
# (v1 = 0.1.13-r46, v2 = 0.1.13-r47 variants) whose "agent" is a small sh
# loop that writes state.json's last_successful_control_plane_at whenever the
# stand's panel answers /api/health, padded past the command's 1 MB binary
# check; the real init script and render-config.sh of the package. The
# self-update command is rendered by the panel's own generator
# (apps/web/src/lib/controller-update-jobs.ts) with the guard's windows shrunk.
# A feed and a panel (uhttpd) run on a private docker network; each scenario
# is a fresh openwrt/rootfs container booted by router.sh (ubusd, procd, logd,
# cron, the enabled vectra-controller): real opkg, procd, BusyBox, crond.
#
# Scenarios:
#   healthy     v2 runs and reaches the panel: kept, guard gone, no marker
#   crash       v2 exits at start (procd respawns it): back to v1
#   selfdelete  v2 removes its own binary after start: back to v1
#   nobin       v2's postinst leaves no binary (post-install check fails):
#               v1 files back at once, the running v1 never restarted
#   midfail     LuCI v2 preinst fails, opkg stops mid-pair: same
#   silent      v2 runs but never contacts while the panel answers: back to v1
#   offline     panel unreachable from the router: v2 kept after the window
#   reboot      silent v2, container restarted inside the window: the guard
#               resumes from cron, re-arms its clock, rolls back
#   noroom      no room for a copy on /etc or /tmp: exit 73, nothing changed
#   tmpcopy     no room on /etc: copy on /tmp; crash v2 rolled back from it
set -euo pipefail
export COPYFILE_DISABLE=1

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$HERE/../../../../.." && pwd)"
OPENWRT="$REPO/router/vectra-controller-agent/openwrt"
BUILD="$HERE/build"
LOGS="$HERE/logs"
IMAGE="openwrt/rootfs:aarch64_generic-${OPENWRT_VERSION:-24.10.8}"
PLATFORM=linux/aarch64_generic
RUN_ID="vupd-$$"
NET="$RUN_ID-net"
V1=0.1.13-r46
V2=0.1.13-r47
TIMINGS='{"stable":15,"crash":45,"contact":60,"probeFrom":30,"probeEvery":5,"hold":15,"network":150,"tick":3,"restartDelay":3,"preparedTimeout":60,"armedTimeout":30}'
ALL=(healthy crash selfdelete nobin midfail silent offline reboot noroom tmpcopy)

step() { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
fatal() { printf '\nFATAL: %s\n' "$*" >&2; exit 2; }

command -v docker >/dev/null || fatal "docker not found"
command -v go >/dev/null || fatal "go not found"
ESBUILD="$REPO/apps/web/node_modules/.bin/esbuild"
[[ -x "$ESBUILD" ]] || fatal "esbuild missing: pnpm install in $REPO"

if [[ $# -gt 0 ]]; then SELECTED=("$@"); else SELECTED=("${ALL[@]}"); fi

cleanup() {
	docker ps -aq --filter "name=^$RUN_ID-" | xargs -r docker rm -f >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# ---------------------------------------------------------------- build ----
step "fixtures"
rm -rf "$BUILD" && mkdir -p "$BUILD/www" "$BUILD/fixtures/v1" "$BUILD/cmd" "$BUILD/panel/api" "$LOGS"
(cd "$REPO/router/vectra-controller-pro" && go build -o "$BUILD/feedtool" ./tools/feedtool)
echo ok > "$BUILD/panel/api/health"
# Incompressible padding: the command refuses an agent binary under 1 MB, and
# noroom needs a copy of a few MB.
head -c 4000000 /dev/urandom | base64 | sed 's/^/#/' > "$BUILD/padding"

agent_script() { # <variant>
	cat <<EOF
#!/bin/sh
VARIANT=$1
EOF
	cat <<'EOF'
url="$(uci -q get vectra-controller.main.control_url)"
state=/etc/vectra-controller/state.json
mkdir -p /etc/vectra-controller
echo "$VARIANT $$ $(date +%s)" >> /tmp/stand-agent.starts
case "$VARIANT" in
crash) exit 1 ;;
selfdelete) sleep 3; rm -f /usr/sbin/vectra-controller-agent; exit 1 ;;
esac
while :; do
	if [ "$VARIANT" != silent ] && uclient-fetch -q -T 3 -O /dev/null "$url/api/health" >/dev/null 2>&1; then
		printf '{"router_id":"stand","agent_token":"t","control_plane_recovery":{"last_successful_control_plane_at":"%s"}}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$state.tmp" && mv "$state.tmp" "$state"
	fi
	sleep 3
done
exit 0
EOF
	cat "$BUILD/padding"
}

agent_ipk() { # <variant> <version> <out> [postinst body]
	local d
	d="$(mktemp -d)"
	mkdir -p "$d/data/usr/sbin" "$d/data/etc/init.d" "$d/data/etc/config" "$d/data/usr/libexec/vectra-controller" "$d/ctrl"
	agent_script "$1" > "$d/data/usr/sbin/vectra-controller-agent"
	cp "$OPENWRT/files/etc/init.d/vectra-controller" "$d/data/etc/init.d/vectra-controller"
	cp "$OPENWRT/files/usr/libexec/vectra-controller/render-config.sh" "$d/data/usr/libexec/vectra-controller/render-config.sh"
	cp "$OPENWRT/files/etc/config/vectra-controller" "$d/data/etc/config/vectra-controller"
	chmod 0755 "$d/data/usr/sbin/vectra-controller-agent" "$d/data/etc/init.d/vectra-controller" "$d/data/usr/libexec/vectra-controller/render-config.sh"
	chmod 0644 "$d/data/etc/config/vectra-controller"
	printf 'Package: vectra-controller-agent\nVersion: %s\nArchitecture: all\nMaintainer: stand\nDescription: update-stand fixture (%s)\n' "$2" "$1" > "$d/ctrl/control"
	echo /etc/config/vectra-controller > "$d/ctrl/conffiles"
	if [[ -n "${4:-}" ]]; then
		printf '#!/bin/sh\n%s\n' "$4" > "$d/ctrl/postinst"
		chmod 0755 "$d/ctrl/postinst"
	fi
	"$BUILD/feedtool" ipk -data "$d/data" -control "$d/ctrl" -out "$3"
	rm -rf "$d"
}

luci_ipk() { # <version> <out> [preinst body]
	local d
	d="$(mktemp -d)"
	mkdir -p "$d/data/usr/share/luci/menu.d" "$d/data/usr/share/rpcd/acl.d" "$d/data/usr/libexec/vectra-controller" "$d/data/www/luci-static/resources/view/vectra-controller" "$d/ctrl"
	echo "{\"v\":\"$1\"}" > "$d/data/usr/share/luci/menu.d/luci-app-vectra-controller.json"
	echo "{\"v\":\"$1\"}" > "$d/data/usr/share/rpcd/acl.d/luci-app-vectra-controller.json"
	printf '#!/bin/sh\necho %s\n' "$1" > "$d/data/usr/libexec/vectra-controller/luci-bridge.sh"
	chmod 0755 "$d/data/usr/libexec/vectra-controller/luci-bridge.sh"
	echo "// $1" > "$d/data/www/luci-static/resources/view/vectra-controller/status.js"
	printf 'Package: luci-app-vectra-controller\nVersion: %s\nDepends: vectra-controller-agent\nArchitecture: all\nMaintainer: stand\nDescription: update-stand fixture\n' "$1" > "$d/ctrl/control"
	if [[ -n "${3:-}" ]]; then
		printf '#!/bin/sh\n%s\n' "$3" > "$d/ctrl/preinst"
		chmod 0755 "$d/ctrl/preinst"
	fi
	"$BUILD/feedtool" ipk -data "$d/data" -control "$d/ctrl" -out "$2"
	rm -rf "$d"
}

agent_ipk v1 "$V1" "$BUILD/fixtures/v1/vectra-controller-agent.ipk"
luci_ipk "$V1" "$BUILD/fixtures/v1/luci-app-vectra-controller.ipk"
v2_variant() { # <name> <agent variant> [agent postinst] [luci preinst]
	mkdir -p "$BUILD/www/$1"
	agent_ipk "$2" "$V2" "$BUILD/www/$1/vectra-controller-agent.ipk" "${3:-}"
	luci_ipk "$V2" "$BUILD/www/$1/luci-app-vectra-controller.ipk" "${4:-}"
}
v2_variant healthy healthy
v2_variant crash crash
v2_variant selfdelete selfdelete
v2_variant silent silent
v2_variant nobin healthy 'rm -f /usr/sbin/vectra-controller-agent; exit 0'
v2_variant midfail healthy '' 'echo "stand: LuCI preinst refuses" >&2; exit 1'
ls -la "$BUILD/www/healthy"

step "commands from the panel's generator"
"$ESBUILD" "$HERE/render-command.ts" --bundle --platform=node --format=esm --log-level=warning \
	--tsconfig="$REPO/apps/web/tsconfig.json" --outfile="$BUILD/render.mjs"
for name in healthy crash selfdelete silent nobin midfail; do
	a="$BUILD/www/$name/vectra-controller-agent.ipk"
	l="$BUILD/www/$name/luci-app-vectra-controller.ipk"
	node "$BUILD/render.mjs" "$(printf '{"version":"%s","agentUrl":"http://feed:8080/%s/vectra-controller-agent.ipk","agentSha":"%s","luciUrl":"http://feed:8080/%s/luci-app-vectra-controller.ipk","luciSha":"%s","timings":%s}' \
		"$V2" "$name" "$(shasum -a 256 "$a" | cut -d' ' -f1)" "$name" "$(shasum -a 256 "$l" | cut -d' ' -f1)" "$TIMINGS")" > "$BUILD/cmd/$name.sh"
done
echo "  $(wc -c < "$BUILD/cmd/healthy.sh") bytes per command"

step "feed and panel on a private network"
docker network create "$NET" >/dev/null
docker run -d --rm --name "$RUN_ID-feed" --network "$NET" --network-alias feed --platform "$PLATFORM" \
	-v "$BUILD/www:/www:ro" "$IMAGE" /usr/sbin/uhttpd -f -p 0.0.0.0:8080 -h /www >/dev/null
docker run -d --rm --name "$RUN_ID-panel" --network "$NET" --network-alias panel --platform "$PLATFORM" \
	-v "$BUILD/panel:/www:ro" "$IMAGE" /usr/sbin/uhttpd -f -p 0.0.0.0:8080 -h /www >/dev/null

# ------------------------------------------------------------- scenarios ----
start_router() { # <name> <tmp size> [extra docker args...]
	local name="$1" tmp="$2"
	shift 2
	docker run -d --name "$RUN_ID-$name" --hostname "$name" --privileged --network "$NET" --platform "$PLATFORM" \
		--tmpfs "/tmp:rw,exec,size=$tmp" --tmpfs /overlay:rw,size=64m "$@" \
		-v "$BUILD/fixtures:/fixtures:ro" -v "$BUILD/cmd:/cmd:ro" -v "$HERE/router.sh:/test/router.sh:ro" \
		"$IMAGE" /bin/sh /test/router.sh boot >/dev/null
}
rx() { docker exec "$RUN_ID-$1" sh /test/router.sh "${@:2}"; }
state_of() { rx "$1" state 2>/dev/null | grep '^STATE ' || echo "STATE unreachable"; }
field() { sed -n "s/.* $2=\([^ ]*\).*/\1/p" <<< " ${1#STATE }"; }
record() { echo "RESULT $1 $2 $3"; } # <name> PASS|FAIL <detail>
check() { # <name> <detail> <state line> <key> <expected glob>
	local v
	v="$(field "$3" "$4")"
	# shellcheck disable=SC2053
	if [[ "$v" == $5 ]]; then record "$1" PASS "$2 ($4=$v)"; else record "$1" FAIL "$2 ($4=$v, want $5)"; fi
}
wait_state() { # <router> <seconds> <key> <glob>: poll until the field matches
	local deadline=$((SECONDS + $2)) s
	while ((SECONDS < deadline)); do
		s="$(state_of "$1")"
		# shellcheck disable=SC2053
		[[ "$(field "$s" "$3")" == $4 ]] && { echo "$s"; return 0; }
		sleep 3
	done
	state_of "$1"
	return 1
}
setup() { # <router>: prints the SETUP-OK line
	local out
	out="$(docker exec -e PANEL_URL="${PANEL_URL:-http://panel:8080}" -e EXPECT_CONTACT="${EXPECT_CONTACT:-1}" "$RUN_ID-$1" sh /test/router.sh setup)" || { echo "$out"; echo "STAND-FATAL setup $1"; return 1; }
	echo "$out" | tail -n 1
}
update() { # <router> <command>: prints the output, returns its exit code via UPDATE_RC
	local out
	out="$(rx "$1" update "$2")"
	echo "$out"
	UPDATE_RC="$(sed -n 's/^UPDATE-EXIT //p' <<< "$out")"
}
rolled_back() { # <name> <state> <marker glob>
	check "$1" "back on v1" "$2" agent "$V1"
	check "$1" "v1 agent binary on disk" "$2" variant v1
	check "$1" "LuCI back on v1" "$2" luci "$V1"
	check "$1" "agent running" "$2" pid '[0-9]*'
	check "$1" "rollback marker" "$2" marker "$3"
	check "$1" "guard cleaned" "$2" session gone
	check "$1" "cron line removed" "$2" cron 0
}

scenario() { # <name>
	local n="$1" s pid line md5
	case "$n" in
	healthy)
		start_router "$n" 64m
		setup "$n"
		update "$n" healthy
		[[ "$UPDATE_RC" == 0 ]] && record "$n" PASS "update exit 0" || record "$n" FAIL "update exit $UPDATE_RC"
		s="$(wait_state "$n" 150 session gone)" || true
		check "$n" "v2 kept" "$s" agent "$V2"
		check "$n" "v2 agent running" "$s" variant healthy
		check "$n" "agent running" "$s" pid '[0-9]*'
		check "$n" "no rollback marker" "$s" marker none
		check "$n" "cron line removed" "$s" cron 0
		check "$n" "copy removed" "$s" persistcopy no
		;;
	crash | selfdelete | silent)
		start_router "$n" 64m
		setup "$n"
		update "$n" "$n"
		[[ "$UPDATE_RC" == 0 ]] && record "$n" PASS "update exit 0" || record "$n" FAIL "update exit $UPDATE_RC"
		s="$(wait_state "$n" 30 phase watching)" && record "$n" PASS "guard restarted v2 and watches it" || record "$n" FAIL "guard never watched: $s"
		s="$(wait_state "$n" 200 session gone)" || true
		case "$n" in
		crash) rolled_back "$n" "$s" '*crash_loop*' ;;
		selfdelete) rolled_back "$n" "$s" '*missing_or_not_executable*' ;;
		silent) rolled_back "$n" "$s" '*has_answered_from_this_router*' ;;
		esac
		;;
	nobin | midfail)
		start_router "$n" 64m
		line="$(setup "$n")"
		echo "$line"
		pid="$(sed -n 's/.*pid=\([0-9]*\).*/\1/p' <<< "$line")"
		update "$n" "$n"
		[[ "$UPDATE_RC" == 1 ]] && record "$n" PASS "update exit 1" || record "$n" FAIL "update exit $UPDATE_RC"
		s="$(state_of "$n")"
		rolled_back "$n" "$s" "$([[ $n == nobin ]] && echo '*missing_or_non-executable*' || echo '*opkg_install_controller/LuCI_pair*')"
		check "$n" "the running v1 was never restarted" "$s" pid "$pid"
		;;
	offline)
		start_router "$n" 64m
		PANEL_URL=http://nopanel.invalid:8080 EXPECT_CONTACT=0 setup "$n"
		update "$n" healthy
		[[ "$UPDATE_RC" == 0 ]] && record "$n" PASS "update exit 0" || record "$n" FAIL "update exit $UPDATE_RC"
		sleep 110
		s="$(state_of "$n")"
		check "$n" "past the contact deadline, panel down: still watching" "$s" session present
		check "$n" "past the contact deadline, panel down: v2 not rolled back" "$s" agent "$V2"
		s="$(wait_state "$n" 200 session gone)" || true
		check "$n" "v2 kept after the network window" "$s" agent "$V2"
		check "$n" "v2 agent running" "$s" variant healthy
		check "$n" "no rollback marker" "$s" marker none
		check "$n" "cron line removed" "$s" cron 0
		;;
	reboot)
		start_router "$n" 64m
		setup "$n"
		update "$n" silent
		[[ "$UPDATE_RC" == 0 ]] && record "$n" PASS "update exit 0" || record "$n" FAIL "update exit $UPDATE_RC"
		s="$(wait_state "$n" 30 phase watching)" && record "$n" PASS "guard watching before the reboot" || record "$n" FAIL "guard never watched: $s"
		sleep 10
		docker restart -t 1 "$RUN_ID-$n" >/dev/null
		for _ in $(seq 30); do docker exec "$RUN_ID-$n" test -f /tmp/stand-booted 2>/dev/null && break; sleep 1; done
		s="$(state_of "$n")"
		check "$n" "after reboot: still v2, session kept on /etc" "$s" session present
		local boot
		boot="$(docker exec "$RUN_ID-$n" cat /tmp/stand-booted 2>/dev/null || echo 0)"
		s="$(wait_state "$n" 260 session gone)" || true
		rolled_back "$n" "$s" '*has_answered_from_this_router*'
		local at
		at="$(docker exec "$RUN_ID-$n" sed -n 's/^epoch=//p' /etc/vectra-controller/update-rollback.marker 2>/dev/null || echo 0)"
		if (( at - boot >= 60 )); then
			record "$n" PASS "clock re-armed at boot: rollback $((at - boot))s after boot (contact window 60s)"
		else
			record "$n" FAIL "rollback only $((at - boot))s after boot: the window was not re-armed"
		fi
		;;
	noroom)
		start_router "$n" 22m --tmpfs /etc/vectra-controller:rw,size=3m
		line="$(setup "$n")"
		echo "$line"
		pid="$(sed -n 's/.*pid=\([0-9]*\).*/\1/p' <<< "$line")"
		md5="$(sed -n 's/.*status_md5=\([0-9a-f]*\).*/\1/p' <<< "$line")"
		update "$n" healthy
		[[ "$UPDATE_RC" == 73 ]] && record "$n" PASS "update exit 73" || record "$n" FAIL "update exit $UPDATE_RC"
		s="$(state_of "$n")"
		check "$n" "still v1" "$s" agent "$V1"
		check "$n" "v1 binary untouched" "$s" variant v1
		check "$n" "same agent process" "$s" pid "$pid"
		check "$n" "opkg status byte-identical" "$s" status_md5 "$md5"
		check "$n" "no guard session" "$s" session gone
		check "$n" "no cron line" "$s" cron 0
		check "$n" "no /tmp copy left" "$s" tmpcopy no
		check "$n" "no marker" "$s" marker none
		;;
	tmpcopy)
		start_router "$n" 64m --tmpfs /etc/vectra-controller:rw,size=3m
		setup "$n"
		update "$n" crash
		[[ "$UPDATE_RC" == 0 ]] && record "$n" PASS "update exit 0" || record "$n" FAIL "update exit $UPDATE_RC"
		s="$(state_of "$n")"
		check "$n" "copy on /tmp" "$s" tmpcopy yes
		check "$n" "no copy on /etc" "$s" persistcopy no
		s="$(wait_state "$n" 200 session gone)" || true
		rolled_back "$n" "$s" '*crash_loop*'
		;;
	*) echo "STAND-FATAL unknown scenario $n" ;;
	esac
	echo "## guard log"
	rx "$n" log 30 || true
	echo "STAND-DONE"
}

step "scenarios: ${SELECTED[*]}"
for n in "${SELECTED[@]}"; do
	(scenario "$n" > "$LOGS/$n.log" 2>&1 || echo "STAND-FATAL scenario aborted" >> "$LOGS/$n.log") &
done
wait

step "results"
bad=0 total=0 passed=0
for n in "${SELECTED[@]}"; do
	log="$LOGS/$n.log"
	p="$(grep -c '^RESULT .* PASS' "$log" || true)"
	f="$(grep -c '^RESULT .* FAIL' "$log" || true)"
	printf '  %-11s %s pass, %s fail%s\n' "$n" "$p" "$f" "$(grep -q '^STAND-DONE' "$log" || echo ', DID NOT FINISH')"
	grep '^RESULT .* FAIL' "$log" | sed 's/^/    /' || true
	grep '^STAND-FATAL' "$log" | sed 's/^/    /' || true
	{ [[ "$f" -eq 0 && "$p" -gt 0 ]] && grep -q '^STAND-DONE' "$log" && ! grep -q '^STAND-FATAL' "$log"; } || bad=1
	total=$((total + p + f))
	passed=$((passed + p))
done
if [[ $bad -eq 0 ]]; then
	printf '\n\033[1mSELF-UPDATE ROLLBACK PROVEN: %s/%s assertions on %s.\033[0m\n' "$passed" "$total" "$IMAGE"
else
	printf '\n\033[1mNOT PROVEN: %s/%s passed — logs in %s\033[0m\n' "$passed" "$total" "$LOGS"
	exit 1
fi
