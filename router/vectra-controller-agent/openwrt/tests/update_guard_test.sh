#!/bin/sh
# update_guard_test.sh: off-router tests for the self-update rollback guard
# (update-guard/vectra-update-guard.sh). The guard is sourced as a library
# (VECTRA_GUARD_LIB_ONLY=1) with VECTRA_GUARD_ROOT pointing at a sandbox
# router tree: real tar/awk do the backup and the restore, the clock is a
# variable, and uci/jsonfilter/pidof/logger/uclient-fetch/the init script are
# stubs driven by files in the sandbox.
#
# POSIX sh / BusyBox ash / dash:
#   sh   router/vectra-controller-agent/openwrt/tests/update_guard_test.sh
#   dash router/vectra-controller-agent/openwrt/tests/update_guard_test.sh

set -u
unset CDPATH 2>/dev/null || true
test_dir="$(cd -- "$(dirname -- "$0")" && pwd)"
GUARD="$test_dir/../update-guard/vectra-update-guard.sh"
[ -f "$GUARD" ] || { echo "FATAL: guard not found at $GUARD" >&2; exit 2; }

PASS=0
FAIL=0
assert_eq() { # <expected> <actual> <name>
	if [ "$1" = "$2" ]; then
		PASS=$((PASS + 1))
		printf 'ok   %s\n' "$3"
	else
		FAIL=$((FAIL + 1))
		printf 'FAIL %s\n     expected: [%s]\n     actual:   [%s]\n' "$3" "$1" "$2"
	fi
}
assert_true() { # <name> <command...>
	t_name="$1"
	shift
	if "$@" >/dev/null 2>&1; then assert_eq 0 0 "$t_name"; else assert_eq "true" "false" "$t_name"; fi
}
assert_false() {
	t_name="$1"
	shift
	if "$@" >/dev/null 2>&1; then assert_eq "false" "true" "$t_name"; else assert_eq 0 0 "$t_name"; fi
}

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/vectra-guard-test.XXXXXX")"
R="$SANDBOX/root"
STUB="$SANDBOX/stub"
trap 'rm -rf "$SANDBOX"' EXIT INT TERM
mkdir -p "$STUB"

# --- PATH stubs ---------------------------------------------------------------
cat > "$STUB/logger" <<'EOF'
#!/bin/sh
shift 2
printf '%s\n' "$*" >> "$STUB_DIR/log"
EOF
cat > "$STUB/pidof" <<'EOF'
#!/bin/sh
[ -s "$STUB_DIR/agent.pid" ] || exit 1
cat "$STUB_DIR/agent.pid"
EOF
cat > "$STUB/uclient-fetch" <<'EOF'
#!/bin/sh
echo "$*" >> "$STUB_DIR/fetches"
[ -f "$STUB_DIR/panel.up" ]
EOF
cat > "$STUB/uci" <<'EOF'
#!/bin/sh
[ "$1" = -q ] && shift
[ "$1" = get ] || exit 1
case "$2" in
vectra-controller.main.control_url) printf 'https://panel.test' ;;
*) exit 1 ;;
esac
EOF
cat > "$STUB/jsonfilter" <<'EOF'
#!/bin/sh
[ -s "$STUB_DIR/contact" ] && cat "$STUB_DIR/contact"
exit 0
EOF
cat > "$STUB/nft" <<'EOF'
#!/bin/sh
[ -f "$STUB_DIR/vctl.table" ] && echo 'table inet vctl'
exit 0
EOF
chmod 0755 "$STUB"/*
STUB_DIR="$SANDBOX"
export STUB_DIR
PATH="$STUB:$PATH"
export PATH

# --- A router with v1 of both packages -------------------------------------
INFO="$R/usr/lib/opkg/info"
AGENT_FILES="/usr/sbin/vectra-controller-agent /etc/init.d/vectra-controller /etc/config/vectra-controller /usr/sbin/vectra-controller-watchdog"
LUCI_FILES="/www/luci-static/resources/view/vectra-controller/status.js /usr/libexec/vectra-controller/luci-bridge.sh"

stanza() { # <pkg> <version>
	printf 'Package: %s\nVersion: %s\nDepends: libc\nStatus: install user installed\nArchitecture: all\nConffiles:\n /etc/config/x 0123\nInstalled-Time: 1\n\n' "$1" "$2"
}

install_version() { # <v1|v2> : write the package files, lists and stanzas of that version
	v="$1"
	mkdir -p "$INFO" "$R/etc/init.d" "$R/etc/config" "$R/usr/sbin" "$R/usr/libexec/vectra-controller" "$R/www/luci-static/resources/view/vectra-controller"
	: > "$INFO/vectra-controller-agent.list"
	for f in $AGENT_FILES; do
		[ "$f" = /etc/init.d/vectra-controller ] && continue
		printf 'agent %s %s\n' "$v" "$f" > "$R$f"
		echo "$f" >> "$INFO/vectra-controller-agent.list"
	done
	# The init script is a stub recording what it was asked to do.
	cat > "$R/etc/init.d/vectra-controller" <<EOF
#!/bin/sh
echo "\$1" >> "$SANDBOX/init.calls"
case "\$1" in
running) [ -s "$SANDBOX/agent.pid" ] ;;
stop) : > "$SANDBOX/agent.pid" ;;
start|restart) echo 4242 > "$SANDBOX/agent.pid" ;;
esac
exit 0
EOF
	chmod 0755 "$R/etc/init.d/vectra-controller" "$R/usr/sbin/vectra-controller-agent"
	echo /etc/init.d/vectra-controller >> "$INFO/vectra-controller-agent.list"
	: > "$INFO/luci-app-vectra-controller.list"
	for f in $LUCI_FILES; do
		printf 'luci %s %s\n' "$v" "$f" > "$R$f"
		echo "$f" >> "$INFO/luci-app-vectra-controller.list"
	done
	if [ "$v" = v2 ]; then
		mkdir -p "$R/usr/share/vectra-v2"
		echo new > "$R/usr/share/vectra-v2/only-in-v2"
		echo /usr/share/vectra-v2/only-in-v2 >> "$INFO/vectra-controller-agent.list"
		echo "postinst v2" > "$INFO/vectra-controller-agent.postinst"
	else
		rm -f "$INFO/vectra-controller-agent.postinst"
	fi
	printf 'Package: vectra-controller-agent\nVersion: 0.1.13-%s\n' "$v" > "$INFO/vectra-controller-agent.control"
	printf 'Package: luci-app-vectra-controller\nVersion: 0.1.13-%s\n' "$v" > "$INFO/luci-app-vectra-controller.control"
	{
		stanza busybox 1.36.1-r3
		stanza vectra-controller-agent "0.1.13-$v"
		stanza libfoo 1.0
		stanza luci-app-vectra-controller "0.1.13-$v"
	} > "$R/usr/lib/opkg/status"
}

fresh_router() {
	rm -rf "$R" "$SANDBOX/log" "$SANDBOX/init.calls" "$SANDBOX/fetches" "$SANDBOX/contact" "$SANDBOX/panel.up" "$SANDBOX/vctl.table"
	mkdir -p "$R/etc/crontabs" "$R/tmp" "$R/etc/vectra-controller"
	printf '30 4 * * * reboot\n' > "$R/etc/crontabs/root"
	echo '{}' > "$R/etc/vectra-controller/state.json"
	echo 1111 > "$SANDBOX/agent.pid"
	install_version v1
	cp "$R/usr/lib/opkg/status" "$SANDBOX/status.v1"
	mkdir -p "$R/etc/vectra-controller/update-rollback"
	cp "$GUARD" "$R/etc/vectra-controller/update-rollback/guard.sh"
}

# --- The guard, as a library -----------------------------------------------
VECTRA_GUARD_ROOT="$R"
VECTRA_GUARD_LIB_ONLY=1
export VECTRA_GUARD_ROOT
# shellcheck disable=SC1090
. "$GUARD"
NOW=1000000
g_now() { printf '%s' "$NOW"; }
sleep() { :; }
# The detached loop would run on the real clock: record instead.
g_launch() { echo launch >> "$SANDBOX/launches"; }
# Copies are made by the test user here, by root on a router.
G_OWNER_UID="$(id -u)"
# tar with injectable failures: TAR_FAIL_EXTRACT=n fails the next n extractions
# over the root; TAR_TRUNCATE_AGENT=n truncates the agent binary after them.
TAR_FAIL_EXTRACT=0
TAR_TRUNCATE_AGENT=0
tar() {
	case "$1 ${2:-} ${3:-}" in
	"-xzf "*" -C")
		if [ "$TAR_FAIL_EXTRACT" -gt 0 ]; then
			TAR_FAIL_EXTRACT=$((TAR_FAIL_EXTRACT - 1))
			return 1
		fi
		command tar "$@" || return $?
		if [ "$TAR_TRUNCATE_AGENT" -gt 0 ]; then
			TAR_TRUNCATE_AGENT=$((TAR_TRUNCATE_AGENT - 1))
			printf 'agent v1 /usr/sbin/vec' > "$R/usr/sbin/vectra-controller-agent"
		fi
		return 0
		;;
	esac
	command tar "$@"
}
PERSIST_FREE=100
TMP_FREE=100
g_df_free_mb() {
	case "$1" in
	"$R/tmp" | "$R/tmp/"*) printf '%s' "$TMP_FREE" ;;
	*) printf '%s' "$PERSIST_FREE" ;;
	esac
}

contact_at() { # <epoch>: state.json's last successful contact
	date -u -r "$1" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null > "$SANDBOX/contact" ||
		date -u -d "@$1" +%Y-%m-%dT%H:%M:%SZ > "$SANDBOX/contact"
}
cron_lines() { grep -c 'vectra-update-guard (managed) >>>' "$R/etc/crontabs/root" 2>/dev/null || true; }
session_gone() { [ ! -e "$G_DIR" ] && [ ! -e "$G_TMP_DIR" ] && [ "$(cron_lines)" = 0 ]; }
agent_is() { grep -q "agent $1 /usr/sbin/vectra-controller-agent" "$R/usr/sbin/vectra-controller-agent"; }
luci_is() { grep -q "luci $1 " "$R/www/luci-static/resources/view/vectra-controller/status.js"; }
v1_restored() {
	agent_is v1 && luci_is v1 && [ ! -e "$R/usr/share/vectra-v2/only-in-v2" ] &&
		[ ! -e "$INFO/vectra-controller-agent.postinst" ] &&
		grep -q 'Version: 0.1.13-v1' "$INFO/vectra-controller-agent.control" &&
		[ "$(g_pkg_version vectra-controller-agent)" = 0.1.13-v1 ] &&
		[ "$(g_pkg_version luci-app-vectra-controller)" = 0.1.13-v1 ] &&
		[ "$(g_pkg_version busybox)" = 1.36.1-r3 ] && [ "$(g_pkg_version libfoo)" = 1.0 ]
}
stanza_of() { # <pkg> <status file>
	awk -v p="$1" '/^Package: / { c = ($2 == p) } c { print } c && $0 == "" { c = 0 }' "$2"
}
tick() { g_tick; }
# ticks_until <end> <step>: tick every <step> s while the next tick is before <end>.
ticks_until() {
	while [ $((NOW + $2)) -lt "$1" ]; do
		NOW=$((NOW + $2))
		tick
	done
}

# Prepared, installed v2, restarted: the watching window opens at $NOW.
updated_and_watching() {
	fresh_router
	g_prepare 0.1.13-v2 >/dev/null 2>&1
	install_version v2
	g_main arm
	g_start_window
	: > "$SANDBOX/init.calls"
	echo 2222 > "$SANDBOX/agent.pid"
}

# === 1. prepare ================================================================
fresh_router
g_prepare 0.1.13-v2 >/dev/null 2>&1
assert_eq 0 "$?" "prepare: succeeds with room"
assert_true "prepare: copy kept on the overlay" test -f "$G_DIR/backup.tgz"
assert_true "prepare: copy holds the agent binary" sh -c "tar -tzf '$G_DIR/backup.tgz' | grep -qx 'usr/sbin/vectra-controller-agent'"
assert_true "prepare: copy holds the opkg info files" sh -c "tar -tzf '$G_DIR/backup.tgz' | grep -qx 'usr/lib/opkg/info/luci-app-vectra-controller.list'"
assert_eq 2 "$(grep -c '^Package: ' "$G_STANZAS")" "prepare: both status stanzas kept, nothing else"
(. "$G_META"; printf '%s %s %s' "$G_FROM_AGENT" "$G_TO" "$G_STABLE") > "$SANDBOX/meta.out"
assert_eq "0.1.13-v1 0.1.13-v2 120" "$(cat "$SANDBOX/meta.out")" "prepare: metadata (from, to, default stable window)"
assert_eq prepared "$(g_phase)" "prepare: phase prepared"
assert_eq 1 "$(cron_lines)" "prepare: one managed cron line"
assert_true "prepare: unrelated cron entries kept" grep -qx '30 4 \* \* \* reboot' "$R/etc/crontabs/root"
assert_true "prepare: cron runs the guard tick" grep -q "^\* \* \* \* \* sh $G_SELF tick" "$R/etc/crontabs/root"
g_prepare 0.1.13-v3 >/dev/null 2>&1
assert_eq 74 "$?" "prepare: refused (74) while a previous update is being verified"
NOW=$((NOW + 8000))
g_prepare 0.1.13-v3 >/dev/null 2>&1
assert_eq 0 "$?" "prepare: a stale session (> 2 h) is discarded"
assert_eq 1 "$(cron_lines)" "prepare: still one managed cron line"
NOW=1000000

fresh_router
PERSIST_FREE=8
TMP_FREE=100
g_prepare 0.1.13-v2 >/dev/null 2>&1
assert_eq 0 "$?" "prepare: overlay too small -> /tmp"
(. "$G_META"; printf '%s' "$G_WHERE") > "$SANDBOX/meta.out"
assert_eq tmp "$(cat "$SANDBOX/meta.out")" "prepare: copy on /tmp"
assert_true "prepare: /tmp copy exists" test -f "$G_TMP_DIR/backup.tgz"

fresh_router
PERSIST_FREE=8
TMP_FREE=15
cp "$R/usr/lib/opkg/status" "$SANDBOX/status.before"
g_prepare 0.1.13-v2 >/dev/null 2>"$SANDBOX/err"
assert_eq 73 "$?" "prepare: no room anywhere -> 73"
assert_true "prepare 73: message names the missing room" grep -q 'no room for a rollback copy' "$SANDBOX/err"
assert_false "prepare 73: no metadata" test -e "$G_META"
assert_false "prepare 73: no /tmp copy" test -e "$G_TMP_DIR"
assert_eq 0 "$(cron_lines)" "prepare 73: no cron line"
assert_true "prepare 73: status untouched" cmp -s "$SANDBOX/status.before" "$R/usr/lib/opkg/status"
PERSIST_FREE=100
TMP_FREE=100

# === 2. restore-files (opkg failed mid-install) ===============================
fresh_router
g_prepare 0.1.13-v2 >/dev/null 2>&1
install_version v2
: > "$SANDBOX/init.calls"
g_main restore-files "opkg install controller/LuCI pair" >/dev/null 2>&1
assert_eq 0 "$?" "restore-files: succeeds"
assert_true "restore-files: v1 files, info and stanzas back, v2-only file gone" v1_restored
for p in busybox libfoo vectra-controller-agent luci-app-vectra-controller; do
	assert_eq "$(stanza_of "$p" "$SANDBOX/status.v1")" "$(stanza_of "$p" "$R/usr/lib/opkg/status")" "restore-files: status stanza of $p as before the update"
done
assert_eq "" "$(grep -v '^running$' "$SANDBOX/init.calls")" "restore-files: the running (old) agent is not restarted"
assert_true "restore-files: marker written" grep -q '^reason=opkg install controller/LuCI pair' "$G_MARKER"
assert_true "restore-files: session cleaned (copy, metadata, cron line)" session_gone

# === 3. judgement ================================================================
# 3a. crash loop: no process at all.
updated_and_watching
: > "$SANDBOX/agent.pid"
W=$NOW
NOW=$((W + 299)); tick
assert_true "crash: still v2 before the crash window ends" agent_is v2
NOW=$((W + 300)); tick
assert_true "crash: rolled back at the crash window" v1_restored
assert_true "crash: agent stopped then started again" sh -c "grep -qx stop '$SANDBOX/init.calls' && grep -Eqx 'start|restart' '$SANDBOX/init.calls'"
assert_true "crash: marker says crash loop" grep -q 'crash loop' "$G_MARKER"
assert_true "crash: session cleaned" session_gone

# 3b. crash loop: procd respawns, a new pid every tick.
updated_and_watching
W=$NOW
p=3000
while [ "$NOW" -lt $((W + 300)) ]; do
	NOW=$((NOW + 30)); p=$((p + 1)); echo "$p" > "$SANDBOX/agent.pid"; tick
done
assert_true "respawn loop: rolled back" v1_restored

# 3c. healthy: stable and a contact after the restart.
updated_and_watching
W=$NOW
contact_at $((W - 5))
NOW=$((W + 60)); tick
NOW=$((W + 200)); tick
assert_true "contact before the restart does not count" test -f "$G_META"
contact_at $((W + 150))
NOW=$((W + 210)); tick
assert_true "healthy: v2 kept" agent_is v2
assert_true "healthy: session cleaned, copy removed" session_gone
assert_false "healthy: no rollback marker" test -e "$G_MARKER"

# 3d. alive, never contacts, panel answers -> rollback after the hold.
updated_and_watching
W=$NOW
: > "$SANDBOX/panel.up"
ticks_until $((W + 900)) 10
assert_true "silent+panel up: still v2 before the contact deadline" agent_is v2
assert_true "silent+panel up: probed the panel's /api/health" grep -q 'https://panel.test/api/health' "$SANDBOX/fetches"
NOW=$((W + 900)); tick
assert_true "silent+panel up: rolled back at the contact deadline" v1_restored
assert_true "silent+panel up: marker names the panel" grep -q 'panel.test' "$G_MARKER"

# 3e. alive, never contacts, panel unreachable: wait for the network up to the
#     network deadline, then back to the known-good version (the job itself
#     proved the panel was reachable before the update).
updated_and_watching
W=$NOW
ticks_until $((W + 3600)) 30
assert_true "silent+panel down: v2 kept while waiting for the network" agent_is v2
assert_true "silent+panel down: still watching" test -f "$G_META"
NOW=$((W + 3600)); tick
assert_true "silent+panel down: rolled back at the network deadline" v1_restored
assert_true "silent+panel down: marker says the panel does not answer" grep -q 'does not answer from this router' "$G_MARKER"
assert_true "silent+panel down: session cleaned" session_gone

# 3f. panel comes back at 30 min, agent still silent -> rollback after the hold.
updated_and_watching
W=$NOW
ticks_until $((W + 1801)) 30
: > "$SANDBOX/panel.up"
ticks_until $((W + 2011)) 30
assert_true "panel back: not rolled back before the hold" agent_is v2
ticks_until $((W + 2100)) 30
assert_true "panel back: rolled back after the hold" v1_restored

# 3g. binary missing -> immediate rollback.
updated_and_watching
rm -f "$R/usr/sbin/vectra-controller-agent"
NOW=$((NOW + 10)); tick
assert_true "binary missing: rolled back at once" v1_restored
assert_true "binary missing: marker" grep -q 'missing or not executable' "$G_MARKER"

# 3h. reboot inside the window: the clock re-arms from boot.
updated_and_watching
rm -f "$SANDBOX/launches"
W=$NOW
: > "$SANDBOX/agent.pid"
NOW=$((W + 200)); tick
rm -rf "$G_RUN"
NOW=$((W + 260)); tick
assert_eq $((W + 260)) "$(cat "$G_DIR/window")" "reboot: window re-armed at the first tick after boot"
assert_eq 1 "$(grep -c launch "$SANDBOX/launches" 2>/dev/null)" "reboot: the detached loop is launched again"
NOW=$((W + 500)); tick
assert_true "reboot: not judged on pre-reboot time" agent_is v2
NOW=$((W + 560)); tick
assert_true "reboot: crash loop after the reboot rolls back" v1_restored

# 3i. copy on /tmp lost by the reboot -> stop without a net.
fresh_router
PERSIST_FREE=8
g_prepare 0.1.13-v2 >/dev/null 2>&1
PERSIST_FREE=100
install_version v2
g_main arm
g_start_window
rm -rf "$G_RUN" "$G_TMP_DIR"
NOW=$((NOW + 60)); tick
NOW=$((NOW + 10)); tick
assert_true "lost /tmp copy: v2 stays" agent_is v2
assert_true "lost /tmp copy: session ended" session_gone
assert_true "lost /tmp copy: logged" grep -q 'is gone' "$SANDBOX/log"

# 3j. the update command died after prepare (job timeout mid-opkg).
fresh_router
g_prepare 0.1.13-v2 >/dev/null 2>&1
install_version v2
NOW=$((NOW + 299)); tick
assert_true "prepared: nothing before the timeout" agent_is v2
NOW=$((NOW + 1)); tick
assert_true "prepared: rolled back after the timeout" v1_restored

# 3k. armed, but the detached guard never restarted the agent -> cron does.
fresh_router
g_prepare 0.1.13-v2 >/dev/null 2>&1
install_version v2
g_main arm
: > "$SANDBOX/init.calls"
NOW=$((NOW + 59)); tick
assert_eq "" "$(grep -Ex 'start|restart' "$SANDBOX/init.calls")" "armed: no restart before the timeout"
NOW=$((NOW + 1)); tick
assert_eq watching "$(g_phase)" "armed: cron restarted the agent and watches it"
assert_true "armed: init restart/start called" grep -Eqx 'start|restart' "$SANDBOX/init.calls"

# 3l. vctl owns the router: files restored, legacy agent not started.
updated_and_watching
: > "$SANDBOX/agent.pid"
: > "$SANDBOX/vctl.table"
NOW=$((NOW + 300)); tick
assert_true "vctl: files restored" v1_restored
assert_eq "" "$(grep -Ex 'start|restart|enable' "$SANDBOX/init.calls")" "vctl: legacy agent not enabled/started"
rm -f "$SANDBOX/vctl.table"

# 3m. watchdog-triggered rollback is idempotent.
updated_and_watching
g_main rollback "watchdog: binary missing" >/dev/null 2>&1
assert_true "rollback: restored" v1_restored
g_main rollback "again" >/dev/null 2>&1
assert_eq 0 "$?" "rollback: second call is a no-op"
assert_true "rollback: marker keeps the first reason" grep -q 'watchdog: binary missing' "$G_MARKER"

# 3n. a live lock holder: the cron tick does nothing.
updated_and_watching
: > "$SANDBOX/agent.pid"
mkdir -p "$G_RUN/lock"
sh -c 'sleep 30' &
holder=$!
printf '%s %s\n' "$holder" "$NOW" > "$G_RUN/lock/owner"
NOW=$((NOW + 400))
g_main tick
assert_true "lock held: tick skipped" agent_is v2
kill "$holder" 2>/dev/null
wait "$holder" 2>/dev/null
g_main tick
assert_true "lock owner dead: next tick acts" v1_restored

# === 5. review fixes ===========================================================
# 5a. the first extraction fails (full filesystem): copy and session kept,
#     retried from the tick, the second attempt restores v1.
updated_and_watching
: > "$SANDBOX/agent.pid"
W=$NOW
TAR_FAIL_EXTRACT=1
NOW=$((W + 300)); tick
assert_eq restore-failed "$(g_phase)" "failed extraction: phase restore-failed"
assert_true "failed extraction: rollback copy kept" test -f "$G_DIR/backup.tgz"
assert_eq 1 "$(cron_lines)" "failed extraction: cron line kept"
assert_false "failed extraction: no success marker" test -e "$G_MARKER"
NOW=$((W + 330)); tick
assert_eq restore-failed "$(g_phase)" "failed extraction: not retried before the retry interval"
NOW=$((W + 361)); tick
assert_true "failed extraction: second attempt restores v1" v1_restored
assert_true "failed extraction: no v2-only file left (lists kept from attempt 1)" test ! -e "$R/usr/share/vectra-v2/only-in-v2"
assert_true "failed extraction: marker keeps the first reason" grep -q 'crash loop' "$G_MARKER"
assert_true "failed extraction: marker counts 2 attempts" grep -qx 'attempts=2' "$G_MARKER"
assert_true "failed extraction: session cleaned after success" session_gone

# 5b. the extraction "succeeds" but leaves a truncated binary: not trusted.
updated_and_watching
TAR_TRUNCATE_AGENT=1
g_main rollback "watchdog: binary missing" >/dev/null 2>&1
assert_eq restore-failed "$(g_phase)" "truncated binary: phase restore-failed"
assert_true "truncated binary: copy kept" test -f "$G_DIR/backup.tgz"
g_prepare 0.1.13-v3 >/dev/null 2>&1
assert_eq 74 "$?" "truncated binary: a new update is refused while the restore is incomplete"
NOW=$((NOW + 9000))
g_prepare 0.1.13-v3 >/dev/null 2>&1
assert_eq 74 "$?" "truncated binary: even hours later the incomplete restore is not discarded"
g_main rollback "watchdog: retry" >/dev/null 2>&1
assert_true "truncated binary: the watchdog's retry restores v1" v1_restored
assert_true "truncated binary: marker keeps the first reason" grep -q 'watchdog: binary missing' "$G_MARKER"

# 5c. a version rolled back here less than 24 h ago is refused (75).
fresh_router
printf 'time=x\nepoch=%s\nreason=crash loop\nfailed_version=0.1.13-v2\nrestored_version=0.1.13-v1\n' "$NOW" > "$G_MARKER"
g_prepare 0.1.13-v2 >/dev/null 2>"$SANDBOX/err"
assert_eq 75 "$?" "marker: same version within 24 h -> 75"
assert_true "marker: message tells to force from the panel" grep -q 'force the update from the panel' "$SANDBOX/err"
assert_false "marker: 75 leaves no session" test -e "$G_META"
g_prepare 0.1.13-v9 >/dev/null 2>&1
assert_eq 0 "$?" "marker: another version is accepted"
fresh_router
printf 'epoch=%s\nfailed_version=0.1.13-v2\n' "$NOW" > "$G_MARKER"
VECTRA_GUARD_FORCE=1 g_prepare 0.1.13-v2 >/dev/null 2>&1
assert_eq 0 "$?" "marker: forced from the panel -> accepted"
unset VECTRA_GUARD_FORCE
fresh_router
printf 'epoch=%s\nfailed_version=0.1.13-v2\n' "$((NOW - 90000))" > "$G_MARKER"
g_prepare 0.1.13-v2 >/dev/null 2>&1
assert_eq 0 "$?" "marker: older than 24 h -> accepted"

# 5d. clock stepped back: an old contact stamp must change to count.
fresh_router
g_prepare 0.1.13-v2 >/dev/null 2>&1
install_version v2
W=$NOW
contact_at $((W + 500))
g_main arm
g_start_window
echo 2222 > "$SANDBOX/agent.pid"
NOW=$((W + 60)); tick
NOW=$((W + 200)); tick
assert_true "clock back: unchanged stamp (numerically after the window) is not success" test -f "$G_META"
contact_at $((W + 205))
NOW=$((W + 210)); tick
assert_true "clock back: a new stamp is success" session_gone

# 5e. /tmp copy: private directory; a symlinked or foreign copy is never extracted.
fresh_router
PERSIST_FREE=8
g_prepare 0.1.13-v2 >/dev/null 2>&1
PERSIST_FREE=100
assert_eq drwx------ "$(ls -ld "$G_TMP_DIR" | cut -c1-10)" "tmp copy: directory is 0700"
install_version v2
mv "$G_TMP_DIR/backup.tgz" "$SANDBOX/elsewhere.tgz"
ln -s "$SANDBOX/elsewhere.tgz" "$G_TMP_DIR/backup.tgz"
g_main rollback "test" >/dev/null 2>&1
assert_true "tmp copy: symlinked copy not extracted (v2 stays)" agent_is v2
assert_true "tmp copy: refusal logged" grep -q 'not owned by root' "$SANDBOX/log"
fresh_router
PERSIST_FREE=8
g_prepare 0.1.13-v2 >/dev/null 2>&1
PERSIST_FREE=100
install_version v2
G_OWNER_UID=4242424
g_main rollback "test" >/dev/null 2>&1
assert_true "tmp copy: copy owned by someone else not extracted" agent_is v2
G_OWNER_UID="$(id -u)"

# 5f. vctl's markers are any .*-by-vctl file.
fresh_router
mkdir -p "$R/etc/vectra-controller-pro"
: > "$R/etc/vectra-controller-pro/.passwall-retired-by-vctl"
assert_true "vctl: any .*-by-vctl marker means vctl owns the router" g_vctl_owns
rm -f "$R/etc/vectra-controller-pro/.passwall-retired-by-vctl"
assert_false "vctl: no marker, no table, no service -> not owned" g_vctl_owns

# === 4. helpers ==================================================================
assert_eq 1782995696 "$(g_rfc3339_to_epoch 2026-07-02T12:34:56Z)" "rfc3339: date -d or fallback"
date() { [ "${1:-}" = -u ] && [ "${2:-}" = -d ] && return 1; command date "$@"; }
assert_eq 1782995696 "$(g_rfc3339_to_epoch 2026-07-02T12:34:56Z)" "rfc3339: pure-shell fallback"
assert_eq 951782400 "$(g_rfc3339_to_epoch 2000-02-29T00:00:00Z)" "rfc3339: leap day"
assert_eq "" "$(g_rfc3339_to_epoch garbage)" "rfc3339: garbage -> empty"
unset -f date

printf '\n# %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
