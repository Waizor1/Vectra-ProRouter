#!/bin/sh
# watchdog_update_rollback_test.sh: the watchdog's process branches (r47):
#   - a missing/empty agent binary is handed to the self-update rollback guard
#     when a rollback copy exists (and only then);
#   - an incomplete rollback (guard phase restore-failed) is retried;
#   - the legacy agent is never enabled/started (or restored) while vctl owns
#     the router; a merely disabled init script is still enabled + started.
# Runs a copy of the watchdog whose paths point into a sandbox; pgrep, nft,
# logger and sleep are PATH stubs, the init scripts and the guard are stub
# executables that record their calls.
#
#   sh   router/vectra-controller-agent/openwrt/tests/watchdog_update_rollback_test.sh
#   dash router/vectra-controller-agent/openwrt/tests/watchdog_update_rollback_test.sh

set -u
unset CDPATH 2>/dev/null || true
test_dir="$(cd -- "$(dirname -- "$0")" && pwd)"
WATCHDOG="$test_dir/../files/usr/sbin/vectra-controller-watchdog"
[ -f "$WATCHDOG" ] || { echo "FATAL: watchdog not found" >&2; exit 2; }

PASS=0
FAIL=0
assert_eq() {
	if [ "$1" = "$2" ]; then
		PASS=$((PASS + 1))
		printf 'ok   %s\n' "$3"
	else
		FAIL=$((FAIL + 1))
		printf 'FAIL %s\n     expected: [%s]\n     actual:   [%s]\n' "$3" "$1" "$2"
	fi
}

S="$(mktemp -d "${TMPDIR:-/tmp}/vectra-wd-test.XXXXXX")"
trap 'rm -rf "$S"' EXIT INT TERM
mkdir -p "$S/bin" "$S/run" "$S/guard"
export S

cat > "$S/bin/logger" <<'EOF'
#!/bin/sh
shift 2
printf '%s\n' "$*" >> "$S/log"
EOF
cat > "$S/bin/pgrep" <<'EOF'
#!/bin/sh
[ -f "$S/agent.running" ]
EOF
cat > "$S/bin/nft" <<'EOF'
#!/bin/sh
[ -f "$S/vctl.table" ] && echo 'table inet vctl'
exit 0
EOF
cat > "$S/bin/sleep" <<'EOF'
#!/bin/sh
exit 0
EOF
cat > "$S/init" <<'EOF'
#!/bin/sh
echo "$1" >> "$S/init.calls"
case "$1" in
enabled) [ ! -f "$S/init.disabled" ] || exit 1 ;;
start) : > "$S/agent.running" ;;
esac
exit 0
EOF
cat > "$S/vctl-init" <<'EOF'
#!/bin/sh
[ "$1" = running ] && [ -f "$S/vctl.running" ]
EOF
cat > "$S/guard/guard.sh" <<'EOF'
#!/bin/sh
printf '%s|%s\n' "$1" "$2" >> "$S/guard.calls"
exit 0
EOF
chmod 0755 "$S"/bin/* "$S/init" "$S/vctl-init" "$S/guard/guard.sh"

sed -e "s#^INIT_SCRIPT=.*#INIT_SCRIPT=\"$S/init\"#" \
	-e "s#^AGENT_BIN=.*#AGENT_BIN=\"$S/agent\"#" \
	-e "s#^STATE_FILE=.*#STATE_FILE=\"$S/run/state\"#" \
	-e "s#^LAST_RESTART_FILE=.*#LAST_RESTART_FILE=\"$S/run/last-restart\"#" \
	-e "s#^UPDATE_GUARD=.*#UPDATE_GUARD=\"$S/guard/guard.sh\"#" \
	-e "s#^UPDATE_GUARD_META=.*#UPDATE_GUARD_META=\"$S/guard/meta\"#" \
	-e "s#^UPDATE_GUARD_PHASE=.*#UPDATE_GUARD_PHASE=\"$S/guard/phase\"#" \
	-e "s#^VCTL_INIT=.*#VCTL_INIT=\"$S/vctl-init\"#" \
	-e "s#^VCTL_HANDOVER_MARKERS=.*#VCTL_HANDOVER_MARKERS=\"$S/vctl-dir/.legacy-agent-disabled-by-vctl\"#" \
	"$WATCHDOG" > "$S/watchdog"

reset() {
	rm -f "$S/log" "$S/init.calls" "$S/guard.calls" "$S/agent.running" "$S/vctl.table" "$S/vctl.running" \
		"$S/init.disabled" "$S/guard/meta" "$S/guard/phase" "$S/run/state" "$S/run/last-restart" "$S/agent"
	rm -rf "$S/vctl-dir"; mkdir -p "$S/vctl-dir"
}
run_wd() { PATH="$S/bin:$PATH" sh "$S/watchdog"; }
starts() { cat "$S/init.calls" 2>/dev/null | grep -Ec "^(enable|start|restart)$" || true; }
guard_calls() { cat "$S/guard.calls" 2>/dev/null || true; }

# 1. Binary missing, a rollback copy exists: the guard restores.
reset
: > "$S/guard/meta"
run_wd
assert_eq "rollback|watchdog: $S/agent missing or empty" "$(guard_calls)" "missing binary + rollback copy -> guard rollback"
assert_eq 1 "$(grep -c 'restoring the previous version' "$S/log")" "missing binary + rollback copy -> logged"

# 2. Same, but vctl owns the router: no restore, logged once.
reset
: > "$S/guard/meta"
: > "$S/vctl-dir/.legacy-agent-disabled-by-vctl"
run_wd
run_wd
assert_eq "" "$(guard_calls)" "missing binary + vctl handover marker -> no restore"
assert_eq 1 "$(grep -c 'not restoring the legacy agent' "$S/log")" "missing binary + vctl -> logged once"

# 3. Binary missing, no rollback copy: the old CRITICAL path.
reset
run_wd
assert_eq "" "$(guard_calls)" "missing binary, no copy -> no guard call"
assert_eq 1 "$(grep -c 'manual recovery required' "$S/log")" "missing binary, no copy -> CRITICAL log"

# A real binary from here on.
mkbin() { printf 'bin' > "$S/agent"; chmod 0755 "$S/agent"; }

# 4. Process missing, vctl's table present: never started.
reset; mkbin
: > "$S/vctl.table"
run_wd
run_wd
assert_eq 0 "$(starts)" "vctl table -> legacy agent not enabled/started"
assert_eq 1 "$(grep -c 'vctl owns this router; not starting' "$S/log")" "vctl table -> logged once"

# 5. vctl's service running.
reset; mkbin
: > "$S/vctl.running"
run_wd
assert_eq 0 "$(starts)" "vctl running -> legacy agent not started"

# 6. Init script disabled, no vctl anywhere: the old behaviour, enable + start.
reset; mkbin
: > "$S/init.disabled"
run_wd
assert_eq 2 "$(starts)" "init disabled without vctl -> enable + start as before"

# 6b. vctl's other markers outlive a hand-back (`vectra off`): they hold nothing.
reset; mkbin
: > "$S/vctl-dir/.passwall-retired-by-vctl"
: > "$S/vctl-dir/.passwall-disabled-by-vctl"
run_wd
assert_eq 2 "$(starts)" "only .passwall-retired/.passwall-disabled-by-vctl -> agent enabled + started"

# 6c. An incomplete rollback: the guard's throttled retry is nudged, and the
#     watchdog carries on with its other branches (here: start the agent).
reset; mkbin
: > "$S/guard/meta"
echo restore-failed > "$S/guard/phase"
run_wd
assert_eq "retry|" "$(guard_calls)" "restore-failed -> watchdog nudges the guard's retry"
assert_eq 2 "$(starts)" "restore-failed -> the watchdog does not exit early"

# 6d. Same with a missing binary: no second, unthrottled rollback.
reset
: > "$S/guard/meta"
echo manual > "$S/guard/phase"
run_wd
assert_eq "retry|" "$(guard_calls)" "manual + missing binary -> only the throttled retry"

# 7. vctl owns it and the throttle file is in the future (the 1111 case): held
#    by the vctl check, not by the accident of a negative delta.
reset; mkbin
: > "$S/vctl.table"
echo 99999999999 > "$S/run/last-restart"
run_wd
assert_eq 0 "$(grep -c 'throttle' "$S/log")" "vctl + future throttle -> the hold decides, not the throttle"
assert_eq 1 "$(grep -c 'vctl owns this router' "$S/log")" "vctl + future throttle -> held"

# 8. Regression: no vctl, enabled, nothing recent -> enable + start.
reset; mkbin
run_wd
assert_eq 2 "$(starts)" "plain missing process -> enable + start"
assert_eq 1 "$(grep -c 'restarted successfully' "$S/log")" "plain missing process -> restarted"

printf '\n# %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
