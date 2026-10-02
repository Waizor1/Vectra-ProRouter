#!/usr/bin/env bash
#
# packaging_modes_test.sh - guards against shipping router helpers without +x.
#
# r34-r42 published vectra-controller-agent with the control-plane carve-out
# (93 uci-default, firewall hotplug hook, controlplane-direct.sh) as 0644: the
# feed is assembled by scripts/build-vectra-openwrt-feed.sh, not the OpenWrt
# Makefile, and its hand-written chmod list had never been extended. Every
# `[ -x ... ]` guard then skipped the carve-out on every router.
#
# Coverage:
#   1. Every file under the executable dirs of both packages' source trees is
#      executable in the checkout (git mode 100755).
#   2. The build script's assert_ipk_scripts_executable rejects an IPK whose
#      data.tar.gz carries a 0644 script, and names the offender.
#   3. mark_shipped_scripts_executable fixes such a tree, after which the same
#      assertion passes; non-script files (etc/config) keep their mode.
#
# The helpers are extracted from the real build script, so the test exercises
# exactly what the feed build runs.

set -euo pipefail

TEST_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$TEST_DIR/../../../.." && pwd)"
BUILD_SCRIPT="$REPO_ROOT/scripts/build-vectra-openwrt-feed.sh"

TESTS_RUN=0
TESTS_FAILED=0
pass() { TESTS_RUN=$((TESTS_RUN + 1)); printf 'ok   - %s\n' "$1"; }
fail() {
	TESTS_RUN=$((TESTS_RUN + 1)); TESTS_FAILED=$((TESTS_FAILED + 1))
	printf 'FAIL - %s\n' "$1"
	if [[ -n "${2:-}" ]]; then printf '       %s\n' "$2"; fi
}

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/vectra-pkg-modes.XXXXXX")"
trap 'rm -rf "$SANDBOX"' EXIT

# --- load SHIPPED_SCRIPT_DIRS + helpers from the build script ---------------
sed -n '/^SHIPPED_SCRIPT_DIRS=(/,/^remove_macos_metadata()/p' "$BUILD_SCRIPT" \
	| sed '$d' > "$SANDBOX/helpers.sh"
# shellcheck source=/dev/null
source "$SANDBOX/helpers.sh"
if declare -F mark_shipped_scripts_executable >/dev/null \
	&& declare -F assert_ipk_scripts_executable >/dev/null \
	&& [[ "${#SHIPPED_SCRIPT_DIRS[@]}" -gt 0 ]]; then
	pass "helpers: extracted SHIPPED_SCRIPT_DIRS and both helpers from the build script"
else
	fail "helpers: extracted SHIPPED_SCRIPT_DIRS and both helpers from the build script"
	exit 1
fi

# =========================================================================
# 1. Source trees: every file under an executable dir is +x.
# =========================================================================
for root in \
	"$REPO_ROOT/router/vectra-controller-agent/openwrt/files" \
	"$REPO_ROOT/router/luci-app-vectra-controller/root"; do
	offenders=""
	for dir in "${SHIPPED_SCRIPT_DIRS[@]}"; do
		[[ -d "$root/$dir" ]] || continue
		while IFS= read -r file; do
			[[ -x "$file" ]] || offenders+=" ${file#"$REPO_ROOT"/}"
		done < <(find "$root/$dir" -type f)
	done
	if [[ -z "$offenders" ]]; then
		pass "source: all scripts executable under ${root#"$REPO_ROOT"/}"
	else
		fail "source: all scripts executable under ${root#"$REPO_ROOT"/}" "not +x:$offenders"
	fi
done

# --- fake IPK builder -------------------------------------------------------
make_ipk() {
	local data_dir="$1" out="$2" work
	work="$(mktemp -d "$SANDBOX/ipk.XXXXXX")"
	mkdir -p "$work/control"
	printf 'Package: test\n' > "$work/control/control"
	printf '2.0\n' > "$work/debian-binary"
	tar -czf "$work/control.tar.gz" -C "$work/control" .
	tar -czf "$work/data.tar.gz" -C "$data_dir" .
	tar -czf "$out" -C "$work" ./debian-binary ./control.tar.gz ./data.tar.gz
}

# Invalid archive input must fail closed even when the helper is used in if.
printf 'not an archive\n' > "$SANDBOX/corrupt.ipk"
if (assert_ipk_scripts_executable "$SANDBOX/corrupt.ipk") >/dev/null 2>&1; then
	fail "assert: rejects an unreadable IPK"
else
	pass "assert: rejects an unreadable IPK"
fi
mkdir -p "$SANDBOX/missing-data"
printf '2.0\n' > "$SANDBOX/missing-data/debian-binary"
tar -czf "$SANDBOX/missing-data.ipk" -C "$SANDBOX/missing-data" ./debian-binary
if (assert_ipk_scripts_executable "$SANDBOX/missing-data.ipk") >/dev/null 2>&1; then
	fail "assert: rejects an IPK without data.tar.gz"
else
	pass "assert: rejects an IPK without data.tar.gz"
fi

DATA="$SANDBOX/data"
mkdir -p "$DATA/etc/uci-defaults" "$DATA/usr/libexec/vectra-controller" \
	"$DATA/etc/hotplug.d/firewall" "$DATA/etc/config"
printf '#!/bin/sh\n' > "$DATA/etc/uci-defaults/93_test"
printf '#!/bin/sh\n' > "$DATA/usr/libexec/vectra-controller/helper.sh"
printf '#!/bin/sh\n' > "$DATA/etc/hotplug.d/firewall/30-test"
printf 'config main\n' > "$DATA/etc/config/test"
chmod 0644 "$DATA/etc/uci-defaults/93_test" "$DATA/etc/hotplug.d/firewall/30-test" \
	"$DATA/etc/config/test"
chmod 0755 "$DATA/usr/libexec/vectra-controller/helper.sh"

# =========================================================================
# 2. The assertion rejects a 0644 script and names it.
# =========================================================================
make_ipk "$DATA" "$SANDBOX/bad.ipk"
if out="$( (assert_ipk_scripts_executable "$SANDBOX/bad.ipk") 2>&1 )"; then
	fail "assert: rejects IPK with 0644 scripts" "assertion passed unexpectedly"
else
	pass "assert: rejects IPK with 0644 scripts"
	case "$out" in
		*etc/uci-defaults/93_test*) pass "assert: names the 0644 uci-default" ;;
		*) fail "assert: names the 0644 uci-default" "$out" ;;
	esac
	case "$out" in
		*etc/hotplug.d/firewall/30-test*) pass "assert: names the 0644 hotplug hook" ;;
		*) fail "assert: names the 0644 hotplug hook" "$out" ;;
	esac
	case "$out" in
		*helper.sh*) fail "assert: does not flag an already-executable script" "$out" ;;
		*) pass "assert: does not flag an already-executable script" ;;
	esac
fi

# =========================================================================
# 3. mark_shipped_scripts_executable fixes the tree; config stays 0644.
# =========================================================================
mark_shipped_scripts_executable "$DATA"
make_ipk "$DATA" "$SANDBOX/good.ipk"
if out="$( (assert_ipk_scripts_executable "$SANDBOX/good.ipk") 2>&1 )"; then
	pass "mark: after marking, the IPK passes the assertion"
else
	fail "mark: after marking, the IPK passes the assertion" "$out"
fi
if [[ ! -x "$DATA/etc/config/test" ]]; then
	pass "mark: leaves non-script files (etc/config) non-executable"
else
	fail "mark: leaves non-script files (etc/config) non-executable"
fi

echo
echo "----------------------------------------"
echo "ran $TESTS_RUN assertions, $TESTS_FAILED failed"
if [[ "$TESTS_FAILED" -eq 0 ]]; then
	echo "RESULT: PASS"
	exit 0
fi
echo "RESULT: FAIL"
exit 1
