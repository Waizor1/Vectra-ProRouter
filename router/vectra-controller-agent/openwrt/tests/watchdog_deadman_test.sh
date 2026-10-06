#!/bin/sh
# watchdog_deadman_test.sh: off-router unit tests for the GAP-2 dead-man's
# switch in vectra-controller-watchdog. Sources the watchdog as a library
# (VECTRA_WATCHDOG_LIB_ONLY=1 makes it return before the recovery flow) and
# asserts the truth table for should_fail_safe_to_direct (the "armed"
# precondition), the RFC3339 epoch parser, and run_deadman_check end to end:
# a stale panel contact alone never disables the VPN, only a url_test_node
# verdict of "dead" does, the watchdog restores its own direct once a node
# answers, it never interferes with an agent retry, and it is idempotent
# across reboots and overlapping cron runs. No real system state is touched:
# uci, jsonfilter and logger are shell-function stubs, test.sh is a stub
# executable in a temp dir, and every marker path points into that temp dir.
#
# POSIX sh / BusyBox ash compatible. Run with:
#   sh router/vectra-controller-agent/openwrt/tests/watchdog_deadman_test.sh

set -u

# --- Locate the watchdog relative to this test file ------------------------
# Unset CDPATH so a user's CDPATH cannot redirect the `cd` below.
unset CDPATH 2>/dev/null || true
test_dir="$(cd -- "$(dirname -- "$0")" && pwd)"
WATCHDOG="$test_dir/../files/usr/sbin/vectra-controller-watchdog"

if [ ! -f "$WATCHDOG" ]; then
	printf 'FATAL: watchdog not found at %s\n' "$WATCHDOG" >&2
	exit 2
fi

# --- Stubs for impure dependencies -----------------------------------------
# State exposed to the stubs via globals the tests set before each case.
STUB_PASSWALL_ENABLED="1"
STUB_ROUTER_ID=""
STUB_AGENT_TOKEN=""
STUB_LAST_CONTACT=""
STUB_STATE_PATH="/etc/vectra-controller/state.json"
STUB_UCI_DEADMAN_THRESHOLD=""
STUB_PHASE=""
STUB_RETRY_AT=""
# Selected node and the passwall2 UCI sections the candidate resolver reads,
# as "key=value" lines (key relative to passwall2.).
STUB_SELECTED_NODE=""
STUB_PASSWALL_UCI=""

# Records side effects so action tests can assert them.
UCI_DISABLE_CALLED=0
UCI_ENABLE_CALLED=0
PASSWALL_RESTART_CALLED=0

# uci stub: only the read/write shapes the watchdog uses.
uci() {
	# Drop a leading -q if present.
	if [ "${1:-}" = "-q" ]; then
		shift
	fi
	case "${1:-}" in
	get)
		case "${2:-}" in
		vectra-controller.main.state_path)
			[ -n "$STUB_STATE_PATH" ] && printf '%s' "$STUB_STATE_PATH"
			;;
		vectra-controller.main.deadman_threshold)
			[ -n "$STUB_UCI_DEADMAN_THRESHOLD" ] && printf '%s' "$STUB_UCI_DEADMAN_THRESHOLD"
			;;
		"passwall2.@global[0].enabled")
			printf '%s' "$STUB_PASSWALL_ENABLED"
			;;
		"passwall2.@global[0].node")
			[ -n "$STUB_SELECTED_NODE" ] && printf '%s' "$STUB_SELECTED_NODE"
			;;
		passwall2.*)
			stub_key="${2#passwall2.}"
			stub_value="$(printf '%s\n' "$STUB_PASSWALL_UCI" | sed -n "s/^$stub_key=//p" | head -n 1)"
			[ -n "$stub_value" ] || return 1
			printf '%s' "$stub_value"
			;;
		*)
			return 1
			;;
		esac
		;;
	set)
		case "${2:-}" in
		"passwall2.@global[0].enabled=0" | "passwall2.@global[0].enabled='0'")
			UCI_DISABLE_CALLED=1
			;;
		"passwall2.@global[0].enabled=1" | "passwall2.@global[0].enabled='1'")
			UCI_ENABLE_CALLED=1
			;;
		esac
		;;
	commit)
		: # no-op
		;;
	*)
		return 1
		;;
	esac
}

# jsonfilter stub: serves the three fields the watchdog reads, from the
# STUB_* globals, ignoring the actual file. Mirrors `jsonfilter -i <f> -e <expr>`.
jsonfilter() {
	expr=""
	while [ "$#" -gt 0 ]; do
		case "$1" in
		-e)
			shift
			expr="${1:-}"
			;;
		esac
		shift 2>/dev/null || break
	done
	case "$expr" in
	'@.router_id')
		[ -n "$STUB_ROUTER_ID" ] && printf '%s' "$STUB_ROUTER_ID"
		;;
	'@.agent_token')
		[ -n "$STUB_AGENT_TOKEN" ] && printf '%s' "$STUB_AGENT_TOKEN"
		;;
	'@.control_plane_recovery.last_successful_control_plane_at')
		[ -n "$STUB_LAST_CONTACT" ] && printf '%s' "$STUB_LAST_CONTACT"
		;;
	'@.control_plane_recovery.phase')
		[ -n "$STUB_PHASE" ] && printf '%s' "$STUB_PHASE"
		;;
	'@.control_plane_recovery.last_passwall_retry_at')
		[ -n "$STUB_RETRY_AT" ] && printf '%s' "$STUB_RETRY_AT"
		;;
	esac
}

# logger stub: capture last message for optional assertions; stay quiet. The
# watchdog's log() gates on `command -v logger`, which resolves to this shell
# function (so log output is captured here, never emitted to a real syslog).
LAST_LOG=""
logger() {
	# -t TAG MESSAGE
	shift 2>/dev/null || true
	shift 2>/dev/null || true
	LAST_LOG="$*"
}

# Force deadman_state_field to use our stub file path that always "exists".
# We point STATE_JSON at a guaranteed-present file so the `[ -f ]` guard
# passes; jsonfilter is stubbed so contents are irrelevant.
SCRATCH_STATE="$(mktemp 2>/dev/null || printf '/tmp/vectra-deadman-state.%s' "$$")"
printf '{}' > "$SCRATCH_STATE" 2>/dev/null || true

# --- Source the watchdog as a library --------------------------------------
VECTRA_WATCHDOG_LIB_ONLY=1
export VECTRA_WATCHDOG_LIB_ONLY
# shellcheck disable=SC1090
. "$WATCHDOG"

# Override paths/markers so nothing escapes the sandbox.
STATE_JSON_DEFAULT="$SCRATCH_STATE"
STUB_STATE_PATH="$SCRATCH_STATE"
DEADMAN_BOOT_MARKER="$(mktemp 2>/dev/null || printf '/tmp/vectra-deadman-boot.%s' "$$")"
rm -f "$DEADMAN_BOOT_MARKER" 2>/dev/null || true

# Keep the threshold deterministic for the truth-table cases.
unset VECTRA_DEADMAN_THRESHOLD_SECONDS 2>/dev/null || true
DEADMAN_THRESHOLD_SECONDS=900
DEADMAN_BOOT_GRACE_SECONDS=900

# --- Tiny assert harness ----------------------------------------------------
PASS=0
FAIL=0

ok() {
	PASS=$((PASS + 1))
	printf 'ok   - %s\n' "$1"
}

not_ok() {
	FAIL=$((FAIL + 1))
	printf 'FAIL - %s\n' "$1"
}

# assert_decision <expected 0|1> <description> <args...>
# expected 0 = should fail safe; 1 = should NOT.
assert_decision() {
	expected="$1"
	desc="$2"
	shift 2
	if should_fail_safe_to_direct "$@"; then
		actual=0
	else
		actual=1
	fi
	if [ "$actual" -eq "$expected" ]; then
		ok "$desc"
	else
		not_ok "$desc (expected rc=$expected, got rc=$actual)"
	fi
}

# assert_eq <expected> <actual> <description>
assert_eq() {
	if [ "$1" = "$2" ]; then
		ok "$3"
	else
		not_ok "$3 (expected '$1', got '$2')"
	fi
}

NOW=1000000000          # fixed "now"
FRESH=$((NOW - 60))     # 1 min ago  -> within threshold
STALE=$((NOW - 3600))   # 1 hr ago   -> beyond 900s threshold

# --- Truth table for should_fail_safe_to_direct ----------------------------
# args: <last_contact_epoch> <now> <passwall_enabled> <is_onboarded> <boot_grace_ok>

# 1. stale + enabled + onboarded -> FAIL SAFE
assert_decision 0 "stale contact + passwall enabled + onboarded -> fail safe" \
	"$STALE" "$NOW" 1 1 1

# 2. fresh contact -> NO-OP (even when enabled+onboarded)
assert_decision 1 "fresh contact -> no-op" \
	"$FRESH" "$NOW" 1 1 1

# 3. not onboarded -> NO-OP (fresh box must keep proxy untouched even if stale)
assert_decision 1 "not onboarded -> no-op" \
	"$STALE" "$NOW" 1 0 1

# 4. passwall already disabled -> NO-OP
assert_decision 1 "passwall already disabled -> no-op" \
	"$STALE" "$NOW" 0 1 1

# 5. missing timestamp + onboarded + past boot grace -> FAIL SAFE
assert_decision 0 "missing timestamp + onboarded + past boot grace -> fail safe" \
	"" "$NOW" 1 1 1

# 6. missing timestamp + onboarded + WITHIN boot grace -> NO-OP (don't fight first boot)
assert_decision 1 "missing timestamp + onboarded + within boot grace -> no-op" \
	"" "$NOW" 1 1 0

# 7. exactly at threshold boundary -> NO-OP (strictly greater required)
AT=$((NOW - 900))
assert_decision 1 "age exactly == threshold -> no-op (strict >)" \
	"$AT" "$NOW" 1 1 1

# 8. one second past threshold -> FAIL SAFE
PAST=$((NOW - 901))
assert_decision 0 "age one second past threshold -> fail safe" \
	"$PAST" "$NOW" 1 1 1

# 9. clock skew (last_contact in the future) -> NO-OP
FUTURE=$((NOW + 120))
assert_decision 1 "future last_contact (clock skew) -> no-op" \
	"$FUTURE" "$NOW" 1 1 1

# 10. threshold disabled (0) -> NO-OP even when otherwise stranded
SAVED_THRESH="$DEADMAN_THRESHOLD_SECONDS"
DEADMAN_THRESHOLD_SECONDS=0
assert_decision 1 "threshold=0 disables dead-man's switch" \
	"$STALE" "$NOW" 1 1 1
DEADMAN_THRESHOLD_SECONDS="$SAVED_THRESH"

# --- Env / uci override precedence -----------------------------------------
# uci override shortens the threshold so a 1-min-old contact becomes stale.
STUB_UCI_DEADMAN_THRESHOLD=30
assert_decision 0 "uci deadman_threshold override (30s) makes 60s-old contact stale" \
	"$FRESH" "$NOW" 1 1 1
STUB_UCI_DEADMAN_THRESHOLD=""

# env override beats uci and compile-time default.
VECTRA_DEADMAN_THRESHOLD_SECONDS=30
export VECTRA_DEADMAN_THRESHOLD_SECONDS
STUB_UCI_DEADMAN_THRESHOLD=99999
assert_decision 0 "env override (30s) beats uci + makes 60s-old contact stale" \
	"$FRESH" "$NOW" 1 1 1
unset VECTRA_DEADMAN_THRESHOLD_SECONDS
STUB_UCI_DEADMAN_THRESHOLD=""

# bogus uci value falls back to compile-time default (not "disabled").
STUB_UCI_DEADMAN_THRESHOLD="abc"
assert_eq "900" "$(deadman_threshold_seconds)" "non-numeric uci threshold falls back to default 900"
STUB_UCI_DEADMAN_THRESHOLD=""

# --- RFC3339 -> epoch parser ------------------------------------------------
# Known-good UTC values (matches agent's recovery.FormatTime: UTC, 'Z', no sub-second).
assert_eq "1000000000" "$(deadman_rfc3339_to_epoch '2001-09-09T01:46:40Z')" \
	"parse 2001-09-09T01:46:40Z -> 1000000000"
assert_eq "1751205296" "$(deadman_rfc3339_to_epoch '2025-06-29T13:54:56Z')" \
	"parse 2025-06-29T13:54:56Z -> 1751205296"
assert_eq "0" "$(deadman_rfc3339_to_epoch '1970-01-01T00:00:00Z')" \
	"parse epoch zero -> 0"
# +00:00 offset form is tolerated and treated as UTC.
assert_eq "1000000000" "$(deadman_rfc3339_to_epoch '2001-09-09T01:46:40+00:00')" \
	"parse +00:00 offset form -> 1000000000"
# Empty / garbage -> empty (caller treats as 'no usable timestamp').
assert_eq "" "$(deadman_rfc3339_to_epoch '')" "empty timestamp -> empty"
assert_eq "" "$(deadman_rfc3339_to_epoch 'not-a-date')" "garbage timestamp -> empty"

# --- RFC3339 parser: FORCE the pure-shell fallback -------------------------
# On real BusyBox/OpenWrt `date -u -d <RFC3339>` frequently cannot parse the
# 'T'/'Z' form, so the deterministic fallback is the PRODUCTION path. Stub
# `date` to always fail here and re-assert the calendar arithmetic so we know
# it is correct without any date(1) help. (date is restored afterwards.)
date() { return 1; }
assert_eq "1000000000" "$(deadman_rfc3339_to_epoch '2001-09-09T01:46:40Z')" \
	"[fallback] parse 2001-09-09T01:46:40Z -> 1000000000"
assert_eq "1751205296" "$(deadman_rfc3339_to_epoch '2025-06-29T13:54:56Z')" \
	"[fallback] parse 2025-06-29T13:54:56Z -> 1751205296"
assert_eq "0" "$(deadman_rfc3339_to_epoch '1970-01-01T00:00:00Z')" \
	"[fallback] parse epoch zero -> 0"
assert_eq "951868800" "$(deadman_rfc3339_to_epoch '2000-03-01T00:00:00Z')" \
	"[fallback] leap-year boundary 2000-03-01 -> 951868800"
assert_eq "1709251200" "$(deadman_rfc3339_to_epoch '2024-03-01T00:00:00Z')" \
	"[fallback] leap-year boundary 2024-03-01 -> 1709251200"
assert_eq "1456761600" "$(deadman_rfc3339_to_epoch '2016-02-29T16:00:00Z')" \
	"[fallback] Feb 29 leap day -> 1456761600"
assert_eq "1735689599" "$(deadman_rfc3339_to_epoch '2024-12-31T23:59:59Z')" \
	"[fallback] year-end -> 1735689599"
assert_eq "1000000000" "$(deadman_rfc3339_to_epoch '2001-09-09T01:46:40+00:00')" \
	"[fallback] +00:00 offset form -> 1000000000"
assert_eq "" "$(deadman_rfc3339_to_epoch 'not-a-date')" "[fallback] garbage -> empty"
unset -f date 2>/dev/null || true

# --- Boot-grace marker behavior --------------------------------------------
# First observation of a boot is inside the grace window (rc=1).
rm -f "$DEADMAN_BOOT_MARKER" 2>/dev/null || true
if deadman_past_boot_grace "$NOW"; then
	not_ok "first boot observation should be within grace (rc=1)"
else
	ok "first boot observation is within grace window"
fi
# Marker now exists with NOW; a later 'now' well past the grace reports past-grace.
LATER=$((NOW + DEADMAN_BOOT_GRACE_SECONDS + 1))
if deadman_past_boot_grace "$LATER"; then
	ok "past boot grace once elapsed >= grace window"
else
	not_ok "should be past boot grace once elapsed >= grace window"
fi

# --- End-to-end: run_deadman_check ---------------------------------------
# Everything the watchdog writes is redirected into a sandbox.
SANDBOX="$(mktemp -d 2>/dev/null || printf '/tmp/vectra-deadman-sandbox.%s' "$$")"
mkdir -p "$SANDBOX/answers" "$SANDBOX/etc" "$SANDBOX/run"
STATE_FILE="$SANDBOX/run/watchdog.state"
DEADMAN_BOOT_MARKER="$SANDBOX/run/boot-epoch"
DEADMAN_OWNER_MARKER="$SANDBOX/etc/watchdog-direct"
DEADMAN_LAST_ACTION_FILE="$SANDBOX/run/last-action"
DEADMAN_LOCK_DIR="$SANDBOX/run/lock"
MEMINFO_FILE="$SANDBOX/meminfo"
DEADMAN_URL_TEST="$SANDBOX/test.sh"
PROBE_LOG="$SANDBOX/probes.log"

# test.sh stub: answers url_test_node <node> from answers/<node> (default 000)
# and logs every probe. A real executable, so the `timeout` wrapper works.
cat > "$DEADMAN_URL_TEST" <<STUB
#!/bin/sh
[ "\$1" = "url_test_node" ] || exit 1
printf '%s\n' "\$2" >> "$PROBE_LOG"
if [ -f "$SANDBOX/answers/\$2" ]; then cat "$SANDBOX/answers/\$2"; else printf '000:0.000000\n'; fi
STUB
chmod +x "$DEADMAN_URL_TEST"

deadman_fail_safe_to_direct() {
	uci -q set passwall2.@global[0].enabled='0' 2>/dev/null || true
	PASSWALL_RESTART_CALLED=1
	STUB_PASSWALL_ENABLED=0
}
deadman_restore_proxy() {
	uci -q set passwall2.@global[0].enabled='1' 2>/dev/null || true
	PASSWALL_RESTART_CALLED=1
	STUB_PASSWALL_ENABLED=1
}

E2E_NOW=1000000000                       # 2001-09-09T01:46:40Z
now_epoch() { printf '%s' "$E2E_NOW"; }
STALE_CONTACT="2001-09-09T00:46:40Z"     # 1 h before E2E_NOW -> armed
FRESH_CONTACT="2001-09-09T01:45:40Z"     # 60 s before E2E_NOW -> not armed

answer() { printf '%s\n' "$2" > "$SANDBOX/answers/$1"; }
probes_run() { [ -f "$PROBE_LOG" ] && wc -l < "$PROBE_LOG" | tr -d ' ' || printf 0; }

# Reset to: onboarded shunt router, PassWall on, panel silent for 1 h, past
# boot grace, plenty of RAM, every node dead, no watchdog history.
reset_e2e() {
	UCI_DISABLE_CALLED=0
	UCI_ENABLE_CALLED=0
	PASSWALL_RESTART_CALLED=0
	STUB_PASSWALL_ENABLED=1
	STUB_ROUTER_ID="router-xyz"
	STUB_AGENT_TOKEN="token-abc"
	STUB_LAST_CONTACT="$STALE_CONTACT"
	STUB_PHASE="monitoring"
	STUB_RETRY_AT=""
	STUB_SELECTED_NODE="myshunt"
	STUB_PASSWALL_UCI="myshunt.protocol=_shunt
myshunt.default_node=_direct
myshunt.WorldProxy=world
myshunt.YouTube=yt
myshunt.Proxy=world
world.protocol=vless
yt.protocol=vless"
	rm -rf "$SANDBOX/answers" "$DEADMAN_LOCK_DIR"
	mkdir -p "$SANDBOX/answers"
	rm -f "$STATE_FILE" "$DEADMAN_OWNER_MARKER" "$DEADMAN_LAST_ACTION_FILE" "$PROBE_LOG"
	printf '0' > "$DEADMAN_BOOT_MARKER"
	printf 'MemTotal:         240000 kB\nMemAvailable:     120000 kB\n' > "$MEMINFO_FILE"
	E2E_NOW=1000000000
}

# --- Candidate resolution mirrors proxyNodeReachableForRecovery ------------
reset_e2e
assert_eq "world yt" "$(deadman_proxy_candidates | tr '\n' ' ' | sed 's/ $//')" \
	"shunt: concrete slot nodes, deduplicated, _direct skipped"
STUB_PASSWALL_UCI="myshunt.protocol=_shunt
myshunt.default_node=bal
myshunt.WorldProxy=world
bal.protocol=_balancing
world.protocol=vless"
assert_eq "world" "$(deadman_proxy_candidates | tr '\n' ' ' | sed 's/ $//')" \
	"shunt: a virtual slot (balancer) is not a candidate"
STUB_SELECTED_NODE="plain"
STUB_PASSWALL_UCI="plain.protocol=vless"
assert_eq "plain" "$(deadman_proxy_candidates)" "concrete selected node is its own candidate"
STUB_SELECTED_NODE="bal"
STUB_PASSWALL_UCI="bal.protocol=_balancing"
assert_eq "" "$(deadman_proxy_candidates)" "balancer selected -> no candidate (inconclusive)"
STUB_SELECTED_NODE="_direct"
assert_eq "" "$(deadman_proxy_candidates)" "_direct selected -> no candidate"

# --- 1. Panel outage + live proxy: the VPN stays on ------------------------
reset_e2e
answer yt "204:0.310000"
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "panel silent 1 h but a slot node answers -> PassWall stays enabled"
assert_eq "deadman-hold-alive" "$(cat "$STATE_FILE" 2>/dev/null)" "hold-alive decision recorded"
assert_eq "" "$(cat "$DEADMAN_OWNER_MARKER" 2>/dev/null)" "no ownership marker when nothing was switched"
# The hold is logged once, not every tick.
LAST_LOG=""
E2E_NOW=$((E2E_NOW + 300))
run_deadman_check
assert_eq "" "$LAST_LOG" "repeated hold-alive tick does not log again"

# --- 2. Panel outage + dead proxy: fail safe to direct ---------------------
reset_e2e
run_deadman_check
assert_eq "1" "$UCI_DISABLE_CALLED" "panel silent + every node fails url_test_node -> PassWall disabled"
assert_eq "1" "$PASSWALL_RESTART_CALLED" "passwall2 restarted on the switch to direct"
assert_eq "deadman-direct" "$(cat "$STATE_FILE" 2>/dev/null)" "deadman-direct state recorded"
assert_eq "$E2E_NOW" "$(cat "$DEADMAN_OWNER_MARKER" 2>/dev/null)" "ownership marker holds the switch epoch"
assert_eq "2" "$(probes_run)" "both slot nodes were probed before declaring the proxy dead"

# --- 3. Fresh contact: nothing to judge, nothing probed --------------------
reset_e2e
STUB_LAST_CONTACT="$FRESH_CONTACT"
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "fresh contact -> no-op"
assert_eq "0" "$(probes_run)" "fresh contact -> no url_test_node spawned"

# --- 4. Inconclusive verdicts never cut the VPN ----------------------------
reset_e2e
rm -f "$DEADMAN_URL_TEST.disabled"
mv "$DEADMAN_URL_TEST" "$DEADMAN_URL_TEST.disabled"
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "no test.sh -> inconclusive -> PassWall stays enabled"
assert_eq "deadman-hold-inconclusive" "$(cat "$STATE_FILE" 2>/dev/null)" "inconclusive decision recorded"
mv "$DEADMAN_URL_TEST.disabled" "$DEADMAN_URL_TEST"

reset_e2e
printf 'MemTotal:         240000 kB\nMemAvailable:      30000 kB\n' > "$MEMINFO_FILE"
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "MemAvailable below floor -> inconclusive -> PassWall stays enabled"
assert_eq "0" "$(probes_run)" "MemAvailable below floor -> no xray spawned by url_test_node"

reset_e2e
STUB_SELECTED_NODE="bal"
STUB_PASSWALL_UCI="bal.protocol=_balancing"
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "balancer selection -> inconclusive -> PassWall stays enabled"

# --- 5. Never kill a retry the agent is judging ----------------------------
reset_e2e
STUB_PHASE="passwall_retry_wait"
STUB_RETRY_AT="2001-09-09T01:45:40Z"       # 60 s ago: inside the warmup
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "agent mid-retry -> watchdog does not disable"
assert_eq "0" "$(probes_run)" "agent mid-retry -> watchdog does not even probe"
assert_eq "deadman-defer" "$(cat "$STATE_FILE" 2>/dev/null)" "deferral recorded"

reset_e2e
STUB_PHASE="passwall_retry_wait"
STUB_RETRY_AT="2001-09-09T00:46:40Z"       # 1 h ago: abandoned retry
run_deadman_check
assert_eq "1" "$UCI_DISABLE_CALLED" "abandoned agent retry + dead proxy -> watchdog acts"

# --- 6. Agent re-enables proxy after the watchdog's direct: no loop -------
reset_e2e
run_deadman_check                           # dead -> direct (owned)
assert_eq "1" "$UCI_DISABLE_CALLED" "loop scenario: initial dead proxy -> direct"
# The agent's local rescue sees the node answer and re-enables PassWall.
answer world "204:0.200000"
STUB_PASSWALL_ENABLED=1
UCI_DISABLE_CALLED=0
E2E_NOW=$((E2E_NOW + 300))
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "loop scenario: agent's retry with a live node is not killed"
assert_eq "" "$(cat "$DEADMAN_OWNER_MARKER" 2>/dev/null)" "loop scenario: ownership released once PassWall is on again"
# Even if the node flaps dead right away, the flap cooldown holds the next flip.
rm -f "$SANDBOX/answers/world"
E2E_NOW=$((E2E_NOW + 300))
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "loop scenario: no second flip inside the action cooldown"
E2E_NOW=$((E2E_NOW + DEADMAN_ACTION_COOLDOWN_SECONDS))
run_deadman_check
assert_eq "1" "$UCI_DISABLE_CALLED" "loop scenario: a still-dead proxy is cut again after the cooldown"

# --- 7. The watchdog gives back its own direct -----------------------------
reset_e2e
run_deadman_check                           # dead -> direct (owned)
answer world "204:0.250000"
E2E_NOW=$((E2E_NOW + 120))
run_deadman_check
assert_eq "0" "$UCI_ENABLE_CALLED" "restore waits for the action cooldown"
E2E_NOW=$((E2E_NOW + DEADMAN_ACTION_COOLDOWN_SECONDS))
run_deadman_check
assert_eq "1" "$UCI_ENABLE_CALLED" "node answers while panel still silent -> watchdog restores proxy"
assert_eq "" "$(cat "$DEADMAN_OWNER_MARKER" 2>/dev/null)" "ownership marker removed after restore"

reset_e2e
run_deadman_check                           # dead -> direct (owned)
E2E_NOW=$((E2E_NOW + DEADMAN_ACTION_COOLDOWN_SECONDS + 1))
run_deadman_check
assert_eq "0" "$UCI_ENABLE_CALLED" "node still dead -> watchdog keeps direct"
assert_eq "$((E2E_NOW - DEADMAN_ACTION_COOLDOWN_SECONDS - 1))" "$(cat "$DEADMAN_OWNER_MARKER" 2>/dev/null)" "ownership kept while the node is dead"

# Not ours: a direct created by the agent or an operator is never restored.
reset_e2e
STUB_PASSWALL_ENABLED=0
answer world "204:0.250000"
run_deadman_check
assert_eq "0" "$UCI_ENABLE_CALLED" "operator/agent direct (no marker) is never restored by the watchdog"

# Contact back -> the agent owns PassWall again.
reset_e2e
run_deadman_check
STUB_LAST_CONTACT="$FRESH_CONTACT"
answer world "204:0.250000"
E2E_NOW=$((E2E_NOW + DEADMAN_ACTION_COOLDOWN_SECONDS))
STUB_LAST_CONTACT="2001-09-09T01:59:40Z"   # 60 s before the new now
run_deadman_check
assert_eq "0" "$UCI_ENABLE_CALLED" "contact back -> watchdog leaves the restore to the agent"
assert_eq "" "$(cat "$DEADMAN_OWNER_MARKER" 2>/dev/null)" "contact back -> ownership released"

# Agent's recovery took over the park -> the watchdog stands down.
reset_e2e
run_deadman_check
STUB_PHASE="operator_attention"
answer world "204:0.250000"
E2E_NOW=$((E2E_NOW + DEADMAN_ACTION_COOLDOWN_SECONDS))
run_deadman_check
assert_eq "0" "$UCI_ENABLE_CALLED" "agent recovery owns PassWall -> watchdog does not restore"
assert_eq "" "$(cat "$DEADMAN_OWNER_MARKER" 2>/dev/null)" "agent recovery owns PassWall -> ownership released"

# --- 8. Idempotent across reboot -------------------------------------------
reset_e2e
run_deadman_check                           # dead -> direct (owned)
# Reboot: /var/run is wiped, /etc (marker, state.json) survives. The clock
# comes up behind the switch epoch before NTP catches up.
rm -f "$STATE_FILE" "$DEADMAN_LAST_ACTION_FILE" "$DEADMAN_BOOT_MARKER"
rm -rf "$DEADMAN_LOCK_DIR"
E2E_NOW=$((E2E_NOW + 600))
answer world "204:0.250000"
run_deadman_check
assert_eq "1" "$UCI_ENABLE_CALLED" "after reboot the persisted marker still lets the watchdog restore a live proxy"
# A second tick after the restore changes nothing.
UCI_ENABLE_CALLED=0
UCI_DISABLE_CALLED=0
E2E_NOW=$((E2E_NOW + 300))
run_deadman_check
assert_eq "00" "$UCI_ENABLE_CALLED$UCI_DISABLE_CALLED" "post-restore tick is a no-op"

# --- 9. Overlapping cron runs ---------------------------------------------
reset_e2e
mkdir -p "$DEADMAN_LOCK_DIR"
printf '%s' "$E2E_NOW" > "$DEADMAN_LOCK_DIR/epoch"
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "a live lock from another run -> this run does nothing"
E2E_NOW=$((E2E_NOW + DEADMAN_LOCK_STALE_SECONDS))
run_deadman_check
assert_eq "1" "$UCI_DISABLE_CALLED" "a stale lock is broken and the check runs"
if [ -d "$DEADMAN_LOCK_DIR" ]; then
	not_ok "lock released after the run"
else
	ok "lock released after the run"
fi

# --- 10. Cron entry install is idempotent ---------------------------------
CRON_SANDBOX="$SANDBOX/cron"
mkdir -p "$CRON_SANDBOX"
printf '0 4 * * * reboot\n' > "$CRON_SANDBOX/root"
sed -e "s#^CRONTAB=.*#CRONTAB=\"$CRON_SANDBOX/root\"#" \
	-e "s#^WATCHDOG_BIN=.*#WATCHDOG_BIN=\"$DEADMAN_URL_TEST\"#" \
	-e "s#/etc/init.d/cron#$CRON_SANDBOX/no-cron#g" \
	"$test_dir/../files/etc/uci-defaults/92_vectra_controller_watchdog" > "$CRON_SANDBOX/install.sh"
sh "$CRON_SANDBOX/install.sh"
sh "$CRON_SANDBOX/install.sh"
sh "$CRON_SANDBOX/install.sh"
assert_eq "1" "$(grep -c 'vectra-controller-watchdog (managed) >>>' "$CRON_SANDBOX/root")" \
	"cron install run three times -> exactly one managed block"
assert_eq "1" "$(grep -c "^\*/5 \* \* \* \* $DEADMAN_URL_TEST " "$CRON_SANDBOX/root")" \
	"cron install run three times -> exactly one watchdog line"
assert_eq "1" "$(grep -c '^0 4 \* \* \* reboot$' "$CRON_SANDBOX/root")" \
	"cron install keeps unrelated entries"

# --- Cleanup ---------------------------------------------------------------
rm -f "$SCRATCH_STATE" 2>/dev/null || true
rm -rf "$SANDBOX" 2>/dev/null || true

# --- Summary ---------------------------------------------------------------
printf '\n# %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
