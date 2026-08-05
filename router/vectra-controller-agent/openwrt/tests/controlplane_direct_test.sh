#!/bin/sh
#
# controlplane_direct_test.sh - logic/verification tests for the control-plane
# DIRECT carve-out (the fwmark-based PassWall2 tproxy bypass).
#
# These tests run on a dev box WITHOUT a real nftables/fw4 stack, so `nft`,
# `uci`, and `logger` are replaced with shell stubs that record what the scripts
# under test would have done. We assert on the GENERATED ruleset text / UCI
# commands / config output rather than on live kernel state.
#
# Coverage:
#   1. `sh -n` (syntax) on every shell script this change touches.
#   2. controlplane-direct.sh `apply` feeds nft an atomic ruleset that creates
#      `table inet vectra_controlplane`, an output-hook chain at priority -160,
#      and a rule matching the chosen fwmark with `accept`.
#   3. The chosen mark does NOT equal either PassWall2 mark (0x50535732, 0xff).
#   4. controlplane-direct.sh `apply` writes a fw4 include that re-invokes the
#      carve-out (persistence across `fw4 reload`).
#   5. controlplane-direct.sh `remove` deletes the table; `status` reports state.
#   6. The mark is honored via env override (so the Go lane / operator override
#      flows through end to end).
#   7. The 93_vectra_controlplane_direct uci-default registers the fw4 firewall
#      include in UCI and invokes the carve-out's `apply`.
#   8. render-config.sh reads `control_plane_fwmark` (with the matching default)
#      and emits a top-level `control_plane_fwmark` JSON string. The default in
#      render-config.sh and in controlplane-direct.sh MUST be identical.
#   9. REBOOT PERSISTENCE (the bug fixed in this change): after a simulated
#      reboot (in-kernel table cleared + tmpfs include deleted + the run-once
#      uci-default gone), the /etc/hotplug.d/firewall/30-... hook re-runs
#      `apply` and rebuilds BOTH the table (with mark + accept) and the include.
#      This is what the original 23 assertions did not cover.
#  10. The init.d boot() hook re-applies the carve-out at startup (the
#      belt-and-suspenders path) without breaking normal service start.
#  11. The hotplug hook is POSIX (sh -n clean) and only fires on real firewall
#      build events (add/reload), no-ops on others, and tolerates a missing
#      carve-out helper.
#
# POSIX/ash only. No bashisms.

set -u

# --- locate scripts under test (relative to this test file) ---------------
TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
FILES_DIR="$(cd "$TEST_DIR/../files" && pwd)"
CARVE_SCRIPT="$FILES_DIR/usr/libexec/vectra-controller/controlplane-direct.sh"
RENDER_SCRIPT="$FILES_DIR/usr/libexec/vectra-controller/render-config.sh"
UCI_DEFAULT="$FILES_DIR/etc/uci-defaults/93_vectra_controlplane_direct"
HOTPLUG_SCRIPT="$FILES_DIR/etc/hotplug.d/firewall/30-vectra-controlplane-direct"
INITD_SCRIPT="$FILES_DIR/etc/init.d/vectra-controller"

EXPECTED_MARK="0x564354"
PSW2_TPROXY_MARK="0x50535732"
PSW2_BYPASS_MARK="0xff"

# --- tiny test harness ----------------------------------------------------
TESTS_RUN=0
TESTS_FAILED=0

pass() {
	TESTS_RUN=$((TESTS_RUN + 1))
	printf 'ok   - %s\n' "$1"
}

fail() {
	TESTS_RUN=$((TESTS_RUN + 1))
	TESTS_FAILED=$((TESTS_FAILED + 1))
	printf 'FAIL - %s\n' "$1"
	[ -n "${2:-}" ] && printf '       %s\n' "$2"
}

assert_contains() {
	# assert_contains <haystack> <needle> <message>
	case "$1" in
		*"$2"*) pass "$3" ;;
		*) fail "$3" "expected to find: $2" ;;
	esac
}

assert_not_contains() {
	case "$1" in
		*"$2"*) fail "$3" "did NOT expect to find: $2" ;;
		*) pass "$3" ;;
	esac
}

assert_file_contains() {
	# assert_file_contains <file> <needle> <message>
	if [ -f "$1" ] && grep -qF -- "$2" "$1"; then
		pass "$3"
	else
		fail "$3" "expected file $1 to contain: $2"
	fi
}

# --- per-test sandbox -----------------------------------------------------
SANDBOX="$(mktemp -d -t vectra-cpd-test.XXXXXX 2>/dev/null || echo /tmp/vectra-cpd-test.$$)"
mkdir -p "$SANDBOX/bin"
cleanup() { rm -rf "$SANDBOX" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

# Mock `nft`: record stdin (for `-f -`) and argv to files, always succeed.
# For `list table` we mimic "table absent" unless a sentinel file says present.
cat > "$SANDBOX/bin/nft" <<'EOF'
#!/bin/sh
echo "ARGS: $*" >> "$NFT_ARGS_LOG"
case "$*" in
	*"-f -"*|*"-f"*)
		cat >> "$NFT_STDIN_LOG"
		;;
	"list table"*)
		if [ -f "$NFT_TABLE_PRESENT" ]; then
			echo "table inet vectra_controlplane { }"
			exit 0
		fi
		exit 1
		;;
esac
exit 0
EOF
chmod +x "$SANDBOX/bin/nft"

# Mock `uci`: record argv; `get` returns nothing (so "not yet configured").
cat > "$SANDBOX/bin/uci" <<'EOF'
#!/bin/sh
echo "uci $*" >> "$UCI_LOG"
case "$1" in
	-q)
		shift
		[ "$1" = "get" ] && exit 1
		;;
	get)
		exit 1
		;;
esac
exit 0
EOF
chmod +x "$SANDBOX/bin/uci"

# Mock `logger`: no-op.
cat > "$SANDBOX/bin/logger" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$SANDBOX/bin/logger"

NFT_STDIN_LOG="$SANDBOX/nft_stdin.log"
NFT_ARGS_LOG="$SANDBOX/nft_args.log"
NFT_TABLE_PRESENT="$SANDBOX/table_present"
UCI_LOG="$SANDBOX/uci.log"
export NFT_STDIN_LOG NFT_ARGS_LOG NFT_TABLE_PRESENT UCI_LOG

PATH="$SANDBOX/bin:$PATH"
export PATH

# =========================================================================
# 1. Syntax checks (sh -n) on every script we wrote/touched.
# =========================================================================
for s in "$CARVE_SCRIPT" "$RENDER_SCRIPT" "$UCI_DEFAULT" "$HOTPLUG_SCRIPT" "$INITD_SCRIPT"; do
	if [ ! -f "$s" ]; then
		fail "syntax: $s exists" "file not found"
		continue
	fi
	if sh -n "$s" 2>/dev/null; then
		pass "syntax: sh -n $(basename "$s")"
	else
		fail "syntax: sh -n $(basename "$s")" "$(sh -n "$s" 2>&1)"
	fi
done

# =========================================================================
# 2. controlplane-direct.sh `apply` produces the expected nft ruleset.
# =========================================================================
: > "$NFT_STDIN_LOG"
: > "$NFT_ARGS_LOG"
APPLY_INCLUDE="$SANDBOX/vectra-controlplane.include"
NFT_BIN="$SANDBOX/bin/nft" \
	FW_INCLUDE_PATH="$APPLY_INCLUDE" \
	SELF_PATH="$CARVE_SCRIPT" \
	sh "$CARVE_SCRIPT" apply >/dev/null 2>&1

RULESET="$(cat "$NFT_STDIN_LOG" 2>/dev/null)"

assert_contains "$RULESET" "table inet vectra_controlplane" \
	"apply: creates dedicated table inet vectra_controlplane"
assert_contains "$RULESET" "flush table inet vectra_controlplane" \
	"apply: flushes table first (idempotent re-apply)"
assert_contains "$RULESET" "type route hook output priority -160" \
	"apply: output-hook chain at priority -160 (beats PassWall2's -151)"
assert_contains "$RULESET" "meta mark $EXPECTED_MARK counter accept" \
	"apply: rule matches the control-plane mark and accepts (terminates hook)"

# =========================================================================
# 3. Chosen mark must not collide with PassWall2's marks.
# =========================================================================
assert_not_contains "$RULESET" "$PSW2_TPROXY_MARK" \
	"mark: does NOT reuse PassWall2 tproxy mark $PSW2_TPROXY_MARK"
if [ "$EXPECTED_MARK" = "$PSW2_TPROXY_MARK" ]; then
	fail "mark: distinct from PassWall2 tproxy mark" "marks are equal"
else
	pass "mark: distinct from PassWall2 tproxy mark $PSW2_TPROXY_MARK"
fi
if [ "$EXPECTED_MARK" = "$PSW2_BYPASS_MARK" ] || [ "$EXPECTED_MARK" = "255" ]; then
	fail "mark: distinct from PassWall2 bypass sentinel" "marks are equal"
else
	pass "mark: distinct from PassWall2 bypass sentinel $PSW2_BYPASS_MARK/255"
fi

# =========================================================================
# 4. apply writes a fw4 include that re-invokes the carve-out.
# =========================================================================
assert_file_contains "$APPLY_INCLUDE" "$CARVE_SCRIPT" \
	"persistence: fw4 include re-invokes the carve-out script path"
assert_file_contains "$APPLY_INCLUDE" "apply" \
	"persistence: fw4 include calls apply"

# =========================================================================
# 5. remove deletes the table; status reports presence/absence.
# =========================================================================
: > "$NFT_ARGS_LOG"
NFT_BIN="$SANDBOX/bin/nft" \
	FW_INCLUDE_PATH="$APPLY_INCLUDE" \
	SELF_PATH="$CARVE_SCRIPT" \
	sh "$CARVE_SCRIPT" remove >/dev/null 2>&1
REMOVE_ARGS="$(cat "$NFT_ARGS_LOG" 2>/dev/null)"
assert_contains "$REMOVE_ARGS" "delete table inet vectra_controlplane" \
	"remove: deletes the dedicated table"

rm -f "$NFT_TABLE_PRESENT"
STATUS_ABSENT="$(NFT_BIN="$SANDBOX/bin/nft" SELF_PATH="$CARVE_SCRIPT" \
	sh "$CARVE_SCRIPT" status 2>/dev/null)"
assert_contains "$STATUS_ABSENT" "absent" \
	"status: reports 'absent' when table missing"

touch "$NFT_TABLE_PRESENT"
STATUS_PRESENT="$(NFT_BIN="$SANDBOX/bin/nft" SELF_PATH="$CARVE_SCRIPT" \
	sh "$CARVE_SCRIPT" status 2>/dev/null)"
assert_contains "$STATUS_PRESENT" "table inet vectra_controlplane" \
	"status: dumps the table when present"
rm -f "$NFT_TABLE_PRESENT"

# =========================================================================
# 6. Mark override flows through (Go lane / operator override path).
# =========================================================================
: > "$NFT_STDIN_LOG"
OVERRIDE_MARK="0x6abcd1"
NFT_BIN="$SANDBOX/bin/nft" \
	FW_INCLUDE_PATH="$APPLY_INCLUDE" \
	SELF_PATH="$CARVE_SCRIPT" \
	VECTRA_CONTROL_PLANE_FWMARK="$OVERRIDE_MARK" \
	sh "$CARVE_SCRIPT" apply >/dev/null 2>&1
OVERRIDE_RULESET="$(cat "$NFT_STDIN_LOG" 2>/dev/null)"
assert_contains "$OVERRIDE_RULESET" "meta mark $OVERRIDE_MARK counter accept" \
	"override: honors VECTRA_CONTROL_PLANE_FWMARK env override"

# =========================================================================
# 7. uci-default registers the fw4 include and invokes apply.
#    We point SELF_SCRIPT-equivalent indirectly: the uci-default hardcodes
#    /usr/libexec/... so we shim that path into the sandbox.
# =========================================================================
: > "$UCI_LOG"
APPLY_MARKER="$SANDBOX/apply_called"
rm -f "$APPLY_MARKER"

# Shim the carve-out at the absolute path the uci-default expects, recording
# that `apply` was invoked. We do this in a fakeroot via a wrapper that rewrites
# the hardcoded path through a sandboxed copy of the uci-default.
SHIM_ROOT="$SANDBOX/root"
mkdir -p "$SHIM_ROOT/usr/libexec/vectra-controller" "$SHIM_ROOT/etc/uci-defaults"
cat > "$SHIM_ROOT/usr/libexec/vectra-controller/controlplane-direct.sh" <<EOF
#!/bin/sh
echo "carve \$*" >> "$APPLY_MARKER"
exit 0
EOF
chmod +x "$SHIM_ROOT/usr/libexec/vectra-controller/controlplane-direct.sh"

# Copy the real uci-default but retarget its hardcoded /usr/libexec path and
# /var/etc include path into the sandbox so it is side-effect-free.
sed \
	-e "s#/usr/libexec/vectra-controller/controlplane-direct.sh#$SHIM_ROOT/usr/libexec/vectra-controller/controlplane-direct.sh#g" \
	-e "s#/var/etc/vectra-controlplane.include#$SANDBOX/var-etc-include#g" \
	"$UCI_DEFAULT" > "$SHIM_ROOT/etc/uci-defaults/93_test"
chmod +x "$SHIM_ROOT/etc/uci-defaults/93_test"

sh "$SHIM_ROOT/etc/uci-defaults/93_test" >/dev/null 2>&1

UCI_CMDS="$(cat "$UCI_LOG" 2>/dev/null)"
assert_contains "$UCI_CMDS" "firewall.vectra_controlplane=include" \
	"uci-default: registers firewall.vectra_controlplane as include"
assert_contains "$UCI_CMDS" "firewall.vectra_controlplane.type=script" \
	"uci-default: include type is script"
assert_contains "$UCI_CMDS" "commit firewall" \
	"uci-default: commits firewall config"

if [ -f "$APPLY_MARKER" ] && grep -q "carve apply" "$APPLY_MARKER"; then
	pass "uci-default: invokes carve-out apply"
else
	fail "uci-default: invokes carve-out apply" "apply marker not recorded"
fi

# =========================================================================
# 8. render-config.sh emits control_plane_fwmark with the matching default.
#    render-config.sh has hard OpenWrt deps (/lib/functions.sh, jshn) we cannot
#    source here, so we assert on its source: the read (with default) and the
#    JSON emit must both be present, and the default must match the carve-out.
# =========================================================================
assert_file_contains "$RENDER_SCRIPT" \
	'control_plane_fwmark="$(uci_get_or_default control_plane_fwmark 0x564354)"' \
	"render-config: reads control_plane_fwmark uci option with default 0x564354"
assert_file_contains "$RENDER_SCRIPT" \
	'json_add_string control_plane_fwmark "$control_plane_fwmark"' \
	"render-config: emits control_plane_fwmark JSON string"

# Cross-check the default is byte-for-byte identical in both files (the contract
# the Go lane depends on: render default == carve-out default).
RENDER_DEFAULT="$(sed -n 's/.*uci_get_or_default control_plane_fwmark \([^)]*\))".*/\1/p' "$RENDER_SCRIPT" | head -n1)"
CARVE_DEFAULT="$(sed -n 's/^VECTRA_CONTROL_PLANE_FWMARK="\${VECTRA_CONTROL_PLANE_FWMARK:-\([^}]*\)}".*/\1/p' "$CARVE_SCRIPT" | head -n1)"
if [ -n "$RENDER_DEFAULT" ] && [ "$RENDER_DEFAULT" = "$CARVE_DEFAULT" ] && [ "$RENDER_DEFAULT" = "$EXPECTED_MARK" ]; then
	pass "contract: render-config default ($RENDER_DEFAULT) == carve-out default ($CARVE_DEFAULT) == $EXPECTED_MARK"
else
	fail "contract: render-config default == carve-out default == $EXPECTED_MARK" \
		"render='$RENDER_DEFAULT' carve='$CARVE_DEFAULT'"
fi

# =========================================================================
# 9. REBOOT PERSISTENCE — the regression the original suite missed.
#
# Setup: a STATEFUL nft mock backed by a sentinel file that models the actual
# in-kernel table lifecycle:
#   * `nft -f -` with `add table inet vectra_controlplane` -> table now PRESENT
#     (sentinel created), and the fed ruleset is captured for assertions.
#   * `nft list table inet vectra_controlplane`            -> exit 0 iff PRESENT.
#   * `nft delete table inet vectra_controlplane`          -> table ABSENT.
# This lets us prove a full reboot cycle: build -> wipe -> hotplug rebuild.
# =========================================================================
RB_DIR="$SANDBOX/reboot"
mkdir -p "$RB_DIR/bin" "$RB_DIR/usr/libexec/vectra-controller" \
	"$RB_DIR/etc/hotplug.d/firewall" "$RB_DIR/var/etc"

RB_TABLE_PRESENT="$RB_DIR/table_present"   # sentinel: table is in-kernel
RB_NFT_STDIN="$RB_DIR/nft_stdin.log"       # last ruleset fed to `nft -f -`
RB_INCLUDE="$RB_DIR/var/etc/vectra-controlplane.include"
export RB_TABLE_PRESENT RB_NFT_STDIN

cat > "$RB_DIR/bin/nft" <<'EOF'
#!/bin/sh
case "$*" in
	*"-f -"*|*"-f"*)
		ruleset="$(cat)"
		printf '%s' "$ruleset" > "$RB_NFT_STDIN"
		# Simulate the kernel: the atomic ruleset (re)creates the table.
		case "$ruleset" in
			*"add table inet vectra_controlplane"*) : > "$RB_TABLE_PRESENT" ;;
		esac
		exit 0
		;;
	"list table"*"vectra_controlplane"*)
		if [ -f "$RB_TABLE_PRESENT" ]; then
			echo "table inet vectra_controlplane { }"
			exit 0
		fi
		exit 1
		;;
	"delete table"*"vectra_controlplane"*)
		rm -f "$RB_TABLE_PRESENT"
		exit 0
		;;
esac
exit 0
EOF
chmod +x "$RB_DIR/bin/nft"

cat > "$RB_DIR/bin/logger" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$RB_DIR/bin/logger"

# Install a real copy of the carve-out helper at the ABSOLUTE path the hotplug
# hook hardcodes, retargeting only the nft binary, include path, and self path so
# the hook can be exercised exactly as shipped (it calls the helper by abs path).
RB_HELPER="$RB_DIR/usr/libexec/vectra-controller/controlplane-direct.sh"
cp "$CARVE_SCRIPT" "$RB_HELPER"
chmod +x "$RB_HELPER"

# A tiny launcher that runs a script with the reboot sandbox on PATH and the
# carve-out env vars pointed at the sandbox (so no real system state is touched).
rb_run() {
	# $1 = script to source/run; remaining args forwarded.
	script="$1"; shift
	PATH="$RB_DIR/bin:$PATH" \
	NFT_BIN="$RB_DIR/bin/nft" \
	FW_INCLUDE_PATH="$RB_INCLUDE" \
	SELF_PATH="$RB_HELPER" \
		sh "$script" "$@"
}

# --- 9a. First install/boot: apply builds the table + include. ----------------
rm -f "$RB_TABLE_PRESENT" "$RB_INCLUDE" "$RB_NFT_STDIN"
rb_run "$RB_HELPER" apply >/dev/null 2>&1

if [ -f "$RB_TABLE_PRESENT" ]; then
	pass "reboot/9a: initial apply creates the in-kernel table"
else
	fail "reboot/9a: initial apply creates the in-kernel table" "table sentinel absent"
fi
assert_file_contains "$RB_INCLUDE" "$RB_HELPER" \
	"reboot/9a: initial apply writes the fw4 include"

# --- 9b. Simulate a REBOOT: kernel table gone, tmpfs include gone, and the
#         run-once uci-default already deleted by OpenWrt. Nothing remains. -----
rm -f "$RB_TABLE_PRESENT"   # in-kernel nft table wiped on reboot
rm -f "$RB_INCLUDE"         # /var/etc (tmpfs) include gone on reboot
: > "$RB_NFT_STDIN"

if [ ! -f "$RB_TABLE_PRESENT" ] && [ ! -f "$RB_INCLUDE" ]; then
	pass "reboot/9b: post-reboot precondition — carve-out fully gone"
else
	fail "reboot/9b: post-reboot precondition — carve-out fully gone" "residual state"
fi

# --- 9c. fw4 fires the `firewall` hotplug on boot. Run the SHIPPED hotplug hook
#         exactly as hotplug-call would: ACTION set, helper at its abs path. -----
# The shipped hook hardcodes /usr/libexec/.../controlplane-direct.sh; retarget
# that single line to our sandbox copy so the hook is otherwise verbatim.
RB_HOTPLUG="$RB_DIR/etc/hotplug.d/firewall/30-vectra-controlplane-direct"
sed -e "s#/usr/libexec/vectra-controller/controlplane-direct.sh#$RB_HELPER#g" \
	"$HOTPLUG_SCRIPT" > "$RB_HOTPLUG"
chmod +x "$RB_HOTPLUG"

ACTION="add" rb_run "$RB_HOTPLUG" >/dev/null 2>&1

if [ -f "$RB_TABLE_PRESENT" ]; then
	pass "reboot/9c: firewall hotplug (ACTION=add) rebuilds the in-kernel table"
else
	fail "reboot/9c: firewall hotplug (ACTION=add) rebuilds the in-kernel table" \
		"table not restored after reboot"
fi

RB_RULESET="$(cat "$RB_NFT_STDIN" 2>/dev/null)"
assert_contains "$RB_RULESET" "table inet vectra_controlplane" \
	"reboot/9c: rebuilt ruleset recreates table inet vectra_controlplane"
assert_contains "$RB_RULESET" "type route hook output priority -160" \
	"reboot/9c: rebuilt chain keeps output-hook priority -160"
assert_contains "$RB_RULESET" "meta mark $EXPECTED_MARK counter accept" \
	"reboot/9c: rebuilt rule re-stamps mark $EXPECTED_MARK with accept"
assert_file_contains "$RB_INCLUDE" "$RB_HELPER" \
	"reboot/9c: hotplug rebuild also rewrites the fw4 include (not include-dependent)"

# --- 9d. fw4 reload also fires the hotplug (ACTION=reload): same rebuild. ------
rm -f "$RB_TABLE_PRESENT" "$RB_INCLUDE"; : > "$RB_NFT_STDIN"
ACTION="reload" rb_run "$RB_HOTPLUG" >/dev/null 2>&1
if [ -f "$RB_TABLE_PRESENT" ]; then
	pass "reboot/9d: fw4 reload hotplug (ACTION=reload) rebuilds the table"
else
	fail "reboot/9d: fw4 reload hotplug (ACTION=reload) rebuilds the table" "table absent"
fi

# =========================================================================
# 10. init.d boot() belt-and-suspenders: re-applies the carve-out at startup
#     WITHOUT breaking the normal service start. We source the init.d as a
#     library (rc.common is mocked) and drive boot() with stubbed start/helper.
# =========================================================================
RB_INITD_DRIVER="$RB_DIR/initd_driver.sh"
RB_START_MARKER="$RB_DIR/start_called"
RB_BOOT_APPLY_MARKER="$RB_DIR/boot_apply_called"
rm -f "$RB_START_MARKER" "$RB_BOOT_APPLY_MARKER" "$RB_TABLE_PRESENT"

# Helper shim that records it was invoked by boot() and also flips the table on.
RB_BOOT_HELPER="$RB_DIR/usr/libexec/vectra-controller/boot-helper.sh"
cat > "$RB_BOOT_HELPER" <<EOF
#!/bin/sh
echo "apply" >> "$RB_BOOT_APPLY_MARKER"
: > "$RB_TABLE_PRESENT"
exit 0
EOF
chmod +x "$RB_BOOT_HELPER"

cat > "$RB_INITD_DRIVER" <<EOF
#!/bin/sh
# Mock the rc.common shebang harness: provide just enough so sourcing the
# init.d defines its functions without launching procd.
USE_PROCD=1
config_load() { :; }
config_get_bool() { eval "\$1=1"; }
# Record that the normal start path ran (boot() must still call start).
start() { echo "start \$*" >> "$RB_START_MARKER"; }
# Source the real init.d to import its boot()/apply_controlplane_direct().
. "$INITD_SCRIPT"
# Retarget the helper path the init.d uses to our boot shim.
CONTROLPLANE_DIRECT="$RB_BOOT_HELPER"
boot
EOF
chmod +x "$RB_INITD_DRIVER"

rm -f "$RB_TABLE_PRESENT"
sh "$RB_INITD_DRIVER" >/dev/null 2>&1

if [ -f "$RB_BOOT_APPLY_MARKER" ]; then
	pass "boot/10: init.d boot() re-applies the control-plane carve-out"
else
	fail "boot/10: init.d boot() re-applies the control-plane carve-out" "apply not invoked"
fi
if [ -f "$RB_START_MARKER" ]; then
	pass "boot/10: init.d boot() still starts the service (does not break startup)"
else
	fail "boot/10: init.d boot() still starts the service (does not break startup)" \
		"start not invoked"
fi
if [ -f "$RB_TABLE_PRESENT" ]; then
	pass "boot/10: carve-out present after boot() before first firewall hotplug"
else
	fail "boot/10: carve-out present after boot() before first firewall hotplug" "table absent"
fi

# =========================================================================
# 11. Hotplug hook hardening: no-ops on non-build ACTIONs and tolerates the
#     carve-out helper being absent (must never abort firewall processing).
# =========================================================================
# 11a. A non-build ACTION must NOT touch nft (no rebuild).
rm -f "$RB_TABLE_PRESENT" "$RB_INCLUDE"; : > "$RB_NFT_STDIN"
ACTION="remove" rb_run "$RB_HOTPLUG" >/dev/null 2>&1
if [ ! -f "$RB_TABLE_PRESENT" ] && [ ! -s "$RB_NFT_STDIN" ]; then
	pass "hotplug/11a: ACTION=remove does not rebuild (carve-out untouched)"
else
	fail "hotplug/11a: ACTION=remove does not rebuild (carve-out untouched)" \
		"nft was invoked on a non-build action"
fi

# 11b. Missing helper: hook must exit 0 and not error out.
RB_HOTPLUG_NOHELPER="$RB_DIR/etc/hotplug.d/firewall/30-nohelper"
sed -e "s#/usr/libexec/vectra-controller/controlplane-direct.sh#$RB_DIR/does-not-exist.sh#g" \
	"$HOTPLUG_SCRIPT" > "$RB_HOTPLUG_NOHELPER"
chmod +x "$RB_HOTPLUG_NOHELPER"
if ACTION="add" rb_run "$RB_HOTPLUG_NOHELPER" >/dev/null 2>&1; then
	pass "hotplug/11b: missing carve-out helper is tolerated (hook exits 0)"
else
	fail "hotplug/11b: missing carve-out helper is tolerated (hook exits 0)" \
		"hook returned non-zero with helper absent"
fi

# 11c. Idempotency: running apply many times never stacks rules or errors. Each
# run must feed a `flush table` (atomic replace) and exit 0.
rm -f "$RB_TABLE_PRESENT" "$RB_INCLUDE"; : > "$RB_NFT_STDIN"
rb_run "$RB_HELPER" apply >/dev/null 2>&1
rb_run "$RB_HELPER" apply >/dev/null 2>&1
if rb_run "$RB_HELPER" apply >/dev/null 2>&1; then
	IDEMPOTENT_RULESET="$(cat "$RB_NFT_STDIN" 2>/dev/null)"
	assert_contains "$IDEMPOTENT_RULESET" "flush table inet vectra_controlplane" \
		"idempotency/11c: repeated apply uses atomic flush+replace (no rule stacking)"
else
	fail "idempotency/11c: repeated apply uses atomic flush+replace (no rule stacking)" \
		"apply errored on repeat"
fi

# =========================================================================
# 12. init.d start_service applies the carve-out on EVERY start.
#     A controller self-update performs `restart` (stop + start_service), which
#     does NOT run boot(); without applying the carve-out in start_service the
#     nft table is missing until the next reboot/fw4 reload. (Canary 2026-06-30.)
# =========================================================================
START_SVC_BODY="$(awk '/^start_service\(\)/{f=1} f{print} f&&/procd_open_instance/{exit}' "$INITD_SCRIPT")"
assert_contains "$START_SVC_BODY" "apply_controlplane_direct" \
	"start_service/12: start_service applies the carve-out before launching the agent"

# --- summary --------------------------------------------------------------
echo
echo "----------------------------------------"
echo "ran $TESTS_RUN assertions, $TESTS_FAILED failed"
if [ "$TESTS_FAILED" -eq 0 ]; then
	echo "RESULT: PASS"
	exit 0
fi
echo "RESULT: FAIL"
exit 1
