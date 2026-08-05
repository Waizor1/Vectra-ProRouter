#!/bin/sh
# watchdog_deadman_test.sh: off-router unit tests for the GAP-2 dead-man's
# switch in vectra-controller-watchdog. Sources the watchdog as a library
# (VECTRA_WATCHDOG_LIB_ONLY=1 makes it return before the recovery flow) and
# asserts the truth table for should_fail_safe_to_direct plus the RFC3339
# epoch parser. No real system state is touched: uci, jsonfilter, logger and
# /etc/init.d/passwall2 are replaced by shell-function stubs.
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

# Records side effects so action tests can assert them.
UCI_DISABLE_CALLED=0
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

# --- End-to-end action wiring (run_deadman_check) --------------------------
# Stranded onboarded box: stale timestamp, passwall enabled, past boot grace.
# Expect: uci disable + passwall restart invoked, STATE_FILE marked.
UCI_DISABLE_CALLED=0
PASSWALL_RESTART_CALLED=0
STATE_FILE="$(mktemp 2>/dev/null || printf '/tmp/vectra-deadman-statefile.%s' "$$")"
rm -f "$STATE_FILE" 2>/dev/null || true
STUB_PASSWALL_ENABLED=1
STUB_ROUTER_ID="router-xyz"
STUB_AGENT_TOKEN="token-abc"
STUB_LAST_CONTACT="2001-09-09T01:46:40Z"   # very old relative to real now

# Provide a passwall init stub on PATH so the restart side effect is observable
# without touching a real init script.
INIT_STUB_DIR="$(mktemp -d 2>/dev/null || printf '/tmp/vectra-deadman-init.%s' "$$")"
mkdir -p "$INIT_STUB_DIR/etc/init.d" 2>/dev/null || true
# Redefine the action to point at our observable stub instead of /etc/init.d.
deadman_fail_safe_to_direct() {
	uci -q set passwall2.@global[0].enabled='0' 2>/dev/null || true
	uci -q commit passwall2 2>/dev/null || true
	PASSWALL_RESTART_CALLED=1
}
# Pre-age the boot marker so run_deadman_check sees us past grace.
printf '%s' "0" > "$DEADMAN_BOOT_MARKER" 2>/dev/null || true

run_deadman_check

assert_eq "1" "$UCI_DISABLE_CALLED" "run_deadman_check disabled passwall2 for a stranded onboarded box"
assert_eq "1" "$PASSWALL_RESTART_CALLED" "run_deadman_check restarted passwall2 for a stranded onboarded box"
assert_eq "deadman-direct" "$(cat "$STATE_FILE" 2>/dev/null)" "run_deadman_check wrote deadman-direct state marker"

# Fresh, onboarded, enabled box: run_deadman_check must be a NO-OP.
# Pin now_epoch to a value just 30s after a known-epoch contact timestamp so
# the parsed age (30s) is well inside the 900s threshold — no dependence on
# wall-clock or a date(1) inverse.
UCI_DISABLE_CALLED=0
PASSWALL_RESTART_CALLED=0
rm -f "$STATE_FILE" 2>/dev/null || true
STUB_LAST_CONTACT="2001-09-09T01:46:40Z"          # epoch 1000000000
now_epoch() { printf '%s' "1000000030"; }         # 30s later -> fresh
run_deadman_check
assert_eq "0" "$UCI_DISABLE_CALLED" "run_deadman_check is a no-op for a fresh contact"
assert_eq "0" "$PASSWALL_RESTART_CALLED" "run_deadman_check does not restart passwall2 for a fresh contact"

# --- Cleanup ---------------------------------------------------------------
rm -f "$SCRATCH_STATE" "$DEADMAN_BOOT_MARKER" "$STATE_FILE" 2>/dev/null || true
rm -rf "$INIT_STUB_DIR" 2>/dev/null || true

# --- Summary ---------------------------------------------------------------
printf '\n# %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
exit 0
