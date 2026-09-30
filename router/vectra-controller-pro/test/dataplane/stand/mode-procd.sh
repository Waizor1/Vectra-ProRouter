#!/bin/sh
# Gap 1 — the procd start path.
#
# Everything else in this stand drives xray through `vctl supervise`. On a real
# router nothing does that: procd starts the daemon from
# /etc/init.d/vectra-controller-pro, and the init script does far more than exec
# a binary — it disables the legacy vectra-controller agent, writes a marker so
# the router can be handed back, renders the daemon config from UCI, and
# registers a respawn policy. None of that was covered, and it has already
# produced one surprise in the field: the service came up on its own after an
# install that set VECTRA_SKIP_POSTINST_RESTART=1.
#
# So this mode installs a REAL .ipk with the REAL maintainer scripts (extracted
# from openwrt/Makefile by run.sh, not copied), under a real procd + ubusd, and
# exercises install -> start -> respawn -> stop -> remove.
#
# MODE=procd             everything must PASS
# MODE=procd-no-restore  the historical defect is reintroduced: BOTH restore
#                        calls are stripped out of stop_service, so a
#                        stopped/removed vctl leaves the router with no
#                        controller and no proxy. All four restore assertions
#                        MUST FAIL.
# MODE=procd-no-passwall-stop
#                        the state the first canary was rolled back from: the
#                        hand-over disables the legacy agent but never takes the
#                        OTHER proxy stack down, so PassWall keeps running
#                        alongside vctl. passwall_stopped_on_handover and
#                        passwall_stays_down MUST FAIL.
#
# Two things this mode will NEVER do:
#   * install the real legacy agent — it would check in against production with
#     a real router identity. /stand/legacy-stub stands in for it: an init
#     script, a work loop, and — since the first canary — the PassWall WATCHDOG
#     that loop really runs, because a sleep loop could not reproduce the thing
#     that broke.
#   * reach the production control plane. The box has NO route off it (the
#     default route is deleted) and api/router.vectra-pro.net are sinkholed in
#     /etc/hosts to 127.0.0.99, which nothing listens on. Both facts are
#     asserted, not assumed.

PKG_NAME=vectra-controller-pro
IPK=/tmp/$PKG_NAME.ipk
PKG_DATA=/tmp/pkg-data
LEGACY_INIT=/etc/init.d/vectra-controller
PASSWALL_INIT=/etc/init.d/passwall2
PASSWALL_MARKER=/etc/$PKG_NAME/.passwall-disabled-by-vctl
VCTL_INIT=/etc/init.d/$PKG_NAME
LEGACY_MARKER=/etc/$PKG_NAME/.legacy-agent-disabled-by-vctl
CP_SINKHOLE_IP=127.0.0.99
CP_DECOY_IP=127.0.0.98
CP_PROBE_IP=1.1.1.1

PKG_CONFFILE="/etc/config/$PKG_NAME"

# Every path the package owns, DERIVED from the payload rather than listed here,
# so a file added to openwrt/files/ is covered the day it lands instead of the
# day someone remembers to update this list.
PKG_PATHS="$(cd /stand/pkg && find . -type f | sed 's|^\.||' | sort)"

# The subset the REMOVAL must take away. /etc/config/vectra-controller-pro is a
# conffile: opkg deliberately keeps it once an operator has edited it, so the
# router's UCI survives an uninstall. That is correct behaviour and is asserted
# as such (conffile_preserved), not treated as leftover rubbish.
PKG_VOLATILE_PATHS="$(echo "$PKG_PATHS" | grep -vx "$PKG_CONFFILE")"

# ------------------------------------------------------------------ probes ---

# Pids of the running daemon, as seen from outside procd.
daemon_pids() { ps w 2>/dev/null | grep '[v]ctl agent' | awk '{print $1}'; }
daemon_pid()  { daemon_pids | head -1; }

# procd's OWN view: the instance it believes it is supervising.
procd_pid_of() {
	ubus -S call service list "{\"name\":\"$1\"}" 2>/dev/null \
		| tr ',{}' '\n\n\n' | sed -n 's/.*"pid": *\([0-9][0-9]*\).*/\1/p' | head -1
}

svc_enabled() { [ -n "$(ls /etc/rc.d/S*"$1" 2>/dev/null)" ]; }
legacy_running() { ps w 2>/dev/null | grep -q '[l]egacy-agent-loop.sh'; }
# The OTHER proxy stack. Independent service, own rc.d symlink, own process.
passwall_running() { ps w 2>/dev/null | grep -q '[p]asswall-loop.sh'; }

guard_ctr_named() { # <counter> -> packets, or -1
	_v=$(nft list counter inet standguard "$1" 2>/dev/null | awk '/packets/{print $2; exit}')
	[ -n "$_v" ] || _v=-1
	echo "$_v"
}

wait_for() { # <seconds> <shell-condition...>
	_n=$1; shift
	_i=0
	while [ "$_i" -lt "$_n" ]; do
		if "$@"; then return 0; fi
		_i=$((_i + 1)); sleep 1
	done
	return 1
}

daemon_up()   { [ -n "$(daemon_pid)" ]; }
daemon_down() { [ -z "$(daemon_pid)" ]; }

# ------------------------------------------------------------------- setup ---

# The box is cut off from the world before anything is installed, so no ordering
# accident can let the daemon reach production.
procd_isolate() {
	ip route del default 2>/dev/null || true
	ip -6 route del default 2>/dev/null || true
	printf '%s api.vectra-pro.net router.vectra-pro.net\n' "$CP_SINKHOLE_IP" >> /etc/hosts

	# Counters, not policy: the sinkhole is what makes production unreachable.
	# cp_decoy exists so cp_sinkhole moving is evidence of a *specific*
	# destination and not of the counter matching everything.
	nft -f - <<EOF || die "control-plane guard table"
table inet standguard {
  counter cp_sinkhole { }
  counter cp_decoy { }
  counter cp_probe { }
  chain output {
    type filter hook output priority filter; policy accept;
    ip daddr $CP_SINKHOLE_IP counter name "cp_sinkhole"
    ip daddr $CP_DECOY_IP    counter name "cp_decoy"
    ip daddr $CP_PROBE_IP    counter name "cp_probe"
  }
}
EOF
	info "default route deleted; api/router.vectra-pro.net -> $CP_SINKHOLE_IP (nothing listens there)"
}

procd_start_service_manager() {
	mkdir -p /var/lock /var/run /var/state /tmp/lock
	ubusd &
	sleep 1
	procd &
	if ! wait_for 15 sh -c 'ubus list 2>/dev/null | grep -q "^service$"'; then
		die "procd/ubusd never came up"
	fi
	info "ubusd + procd up; ubus 'service' object present"
}

install_legacy_stub() {
	# The PassWall stack FIRST, because the agent's watchdog starts looking for
	# it immediately. Enabled and started in its own right — that independence
	# is the point: it survives the agent being stopped, which is exactly why
	# disabling the agent was never enough. Its own switch is on, as on the
	# fleet: the stub, like the real one, starts nothing with it off.
	cp /stand/legacy-stub/passwall2.config /etc/config/passwall2
	cp /stand/legacy-stub/passwall2 "$PASSWALL_INIT"
	chmod +x "$PASSWALL_INIT" /stand/legacy-stub/passwall-loop.sh
	"$PASSWALL_INIT" enable
	"$PASSWALL_INIT" start
	wait_for 10 passwall_running || die "passwall stub never started"

	cp /stand/legacy-stub/vectra-controller "$LEGACY_INIT"
	chmod +x "$LEGACY_INIT"
	"$LEGACY_INIT" enable
	"$LEGACY_INIT" start
	wait_for 10 legacy_running || die "legacy stub never started"
	info "legacy stub + PassWall stack installed, enabled and running"
}

# Stage the package payload. In procd-no-restore the historical regression is
# reintroduced HERE, in the payload, so it travels through the real ipk and the
# real install — not by editing a file after the fact.
stage_package() {
	rm -rf "$PKG_DATA"
	mkdir -p "$PKG_DATA"
	cp -R /stand/pkg/. "$PKG_DATA/"
	if [ "$MODE" = procd-no-passwall-stop ]; then
		# The state the FIRST canary was rolled back from, reintroduced in the
		# payload so it travels through the real ipk: the hand-over disables the
		# legacy agent but never takes the other proxy stack down. The call site
		# is tab-indented; the function definition is not, so this anchors on the
		# call only.
		# The call site is `stop_passwall_stack || hand_back_and_fail ...`; the
		# function definition is at column 0, so anchoring on a leading tab plus
		# the name hits the call and only the call.
		sed -i 's/^\tstop_passwall_stack .*/\t: # DEFECT INJECTED by the stand/' \
			"$PKG_DATA/etc/init.d/$PKG_NAME"
		grep -q 'DEFECT INJECTED' "$PKG_DATA/etc/init.d/$PKG_NAME" \
			|| die "no-passwall-stop: defect injection missed its anchor"
		info "MODE=procd-no-passwall-stop: stop_passwall_stack stripped from start_service"
	fi
	if [ "$MODE" = procd-no-restore ]; then
		# Line 86 of the shipped init script; the function definition on line 30
		# is NOT tab-indented, so this anchors on the call site only.
		sed -i -e 's/^\trestore_legacy_controller$/\t: # DEFECT INJECTED by the stand/' \
			-e 's/^\trestore_passwall_stack$/\t: # DEFECT INJECTED by the stand/' \
			"$PKG_DATA/etc/init.d/$PKG_NAME"
		grep -q 'DEFECT INJECTED' "$PKG_DATA/etc/init.d/$PKG_NAME" \
			|| die "no-restore: defect injection missed its anchor"
		info "MODE=procd-no-restore: restore_legacy_controller stripped from stop_service"
	fi
	# The image bakes the package files in at their real paths. Remove them so
	# `opkg install` putting them back is a real observation.
	for p in $PKG_PATHS; do rm -f "$p"; done
	/stand/build-ipk.sh "$PKG_DATA" /stand/pkg-control "$IPK"
	info "built $IPK ($(wc -c < "$IPK") bytes) from the shipped payload + Makefile maintainer scripts"
}

opkg_install() { opkg install --force-reinstall "$IPK" 2>&1 | sed 's/^/       opkg: /'; }
opkg_remove()  { opkg remove "$PKG_NAME" 2>&1 | sed 's/^/       opkg: /'; }

# --------------------------------------------------------------- assertions ---

assert_pkg_installed() {
	_missing=
	_notexec=
	for p in $PKG_PATHS; do
		[ -e "$p" ] || { _missing="$_missing $p"; continue; }
		# The init script guards its helpers with `[ -x ]`, so a payload file
		# that arrives 0644 turns a real step (the data-plane teardown, the
		# renderer) into a SILENT no-op. git stores these 0644; the Makefile
		# installs them with INSTALL_BIN and run.sh reproduces that.
		case "$p" in
		*/init.d/* | */uci-defaults/* | */libexec/* | */sbin/*)
			[ -x "$p" ] || _notexec="$_notexec $p" ;;
		esac
	done
	if [ -z "$_missing" ] && [ -z "$_notexec" ]; then
		record pkg_installed PASS "opkg delivered all $(echo "$PKG_PATHS" | wc -l | tr -d ' ') package paths, executables executable"
	else
		record pkg_installed FAIL "missing:${_missing:- none} not-executable:${_notexec:- none}"
	fi
}

# The field surprise, made into a check: an install that sets
# VECTRA_SKIP_POSTINST_RESTART=1 must leave the service down AND unenabled.
assert_postinst_skip_honored() {
	_pid=$(daemon_pid)
	_en=no; svc_enabled "$PKG_NAME" && _en=yes
	if [ -z "$_pid" ] && [ "$_en" = no ]; then
		record postinst_skip_honored PASS "VECTRA_SKIP_POSTINST_RESTART=1: daemon down, service not enabled"
	else
		record postinst_skip_honored FAIL "daemon pid='${_pid:-none}' enabled=$_en — postinst started/enabled it anyway"
	fi
}

# Negative control for the above: without the env var the same install MUST
# bring the service up. Without this pair, "daemon down" proves nothing — it
# would also hold if the package simply never worked.
assert_postinst_starts_by_default() {
	wait_for 25 daemon_up || true
	_pid=$(daemon_pid)
	_en=no; svc_enabled "$PKG_NAME" && _en=yes
	if [ -n "$_pid" ] && [ "$_en" = yes ]; then
		record postinst_starts_default PASS "plain install: daemon pid=$_pid, service enabled — the skip check is armed"
	else
		record postinst_starts_default FAIL "plain install left pid='${_pid:-none}' enabled=$_en"
	fi
}

assert_procd_owns_daemon() {
	_ppid=$(procd_pid_of "$PKG_NAME")
	_rpid=$(daemon_pid)
	if [ -n "$_ppid" ] && [ "$_ppid" = "$_rpid" ]; then
		record procd_owns_daemon PASS "ubus service list reports instance pid=$_ppid, matching the running vctl agent"
	else
		record procd_owns_daemon FAIL "procd pid='${_ppid:-none}' vs running pid='${_rpid:-none}'"
	fi
}

assert_legacy_disabled_on_start() {
	_run=no; legacy_running && _run=yes
	_en=no; svc_enabled vectra-controller && _en=yes
	_mk=no; [ -f "$LEGACY_MARKER" ] && _mk=yes
	if [ "$_run" = no ] && [ "$_en" = no ] && [ "$_mk" = yes ]; then
		record legacy_disabled_on_start PASS "legacy stopped + disabled, hand-back marker written"
	else
		record legacy_disabled_on_start FAIL "legacy running=$_run enabled=$_en marker=$_mk"
	fi
}

# The mutual exclusion above has exactly ONE implementation, start_service, and
# `vctl supervise` typed by hand goes straight around it. That is how the canary
# ended up with two proxy stacks: xray under vctl, and PassWall brought back
# underneath it by the legacy agent's own watchdog.
#
# Both directions are checked on a REAL rc.common, because the guard's whole
# probe is `/etc/init.d/vectra-controller enabled` — the Go tests drive the same
# command against a hand-written `#!/bin/sh` stand-in, which is not proof that
# OpenWrt's rc.common answers the way the guard assumes.
#
# The config path is deliberately absent: the guard runs BEFORE config.Load, so
# a refusal here also proves it fires before the command does any work, and the
# "must not refuse" direction fails on the missing config instead — which is how
# we know it got past the gate rather than never reaching it.
SUPERVISE_PROBE="/nonexistent/agent.json"

assert_supervise_legacy_guard() {
	_out=$(vctl supervise -config "$SUPERVISE_PROBE" -provider "$SUPERVISE_PROBE" 2>&1)
	if echo "$_out" | grep -q "still owns this router"; then
		record supervise_legacy_guard PASS \
			"vctl supervise refused while the legacy agent was enabled and running, naming the hand-over"
	else
		record supervise_legacy_guard FAIL \
			"vctl supervise did NOT refuse with the legacy agent live — a hand-started xray would fight PassWall: $(echo "$_out" | tr '\n' ' ' | cut -c1-120)"
	fi
}

# The direction that would strand a canary: after the hand-over the legacy
# script is still INSTALLED, just stopped and disabled, and vctl must run.
assert_supervise_after_handover() {
	_out=$(vctl supervise -config "$SUPERVISE_PROBE" -provider "$SUPERVISE_PROBE" 2>&1)
	if echo "$_out" | grep -q "still owns this router"; then
		record supervise_after_handover FAIL \
			"vctl supervise still refuses after the hand-over — an installed-but-disabled legacy agent must not block it"
	else
		record supervise_after_handover PASS \
			"after the hand-over the guard is inert (failed on the absent config, as expected): $(echo "$_out" | tr '\n' ' ' | cut -c1-70)"
	fi
}

# procd's respawn policy (15 5 20) is the only thing keeping the daemon alive
# after a crash. SIGKILL it and require a DIFFERENT pid to appear.
assert_respawn() {
	_old=$(daemon_pid)
	[ -n "$_old" ] || { record procd_respawn FAIL "no daemon to kill"; return; }
	kill -9 "$_old" 2>/dev/null
	_i=0; _new=
	while [ "$_i" -lt 40 ]; do
		_new=$(daemon_pid)
		[ -n "$_new" ] && [ "$_new" != "$_old" ] && break
		_new=; _i=$((_i + 1)); sleep 1
	done
	if [ -n "$_new" ]; then
		record procd_respawn PASS "SIGKILL $_old -> procd respawned as $_new after ${_i}s"
	else
		record procd_respawn FAIL "SIGKILL $_old -> no new pid within 40s"
	fi
}

# Negative control for procd_respawn: after an explicit stop the daemon must
# STAY down. Otherwise "a vctl agent process exists" could be satisfied by
# something the stand never controlled.
#
# procd's stop is ASYNCHRONOUS — SIGTERM, then up to term_timeout (5s) before
# SIGKILL — so the daemon is routinely still alive when `/etc/init.d/... stop`
# returns. An earlier version sampled immediately and therefore passed or failed
# on how long stop_service happened to take: it passed in `procd` (where
# restore_legacy_controller runs two init-script calls first) and failed in
# `procd-no-restore` (where stop_service returns instantly). Wait for the exit
# first — that is part of what "stop" means — then watch that it stays gone.
assert_no_respawn_after_stop() {
	if ! wait_for 20 daemon_down; then
		record no_respawn_after_stop FAIL "daemon pid=$(daemon_pid) never exited within 20s of stop"
		return
	fi
	_i=0
	while [ "$_i" -lt 10 ]; do
		_p=$(daemon_pid)
		if [ -n "$_p" ]; then
			record no_respawn_after_stop FAIL "daemon pid=$_p reappeared ${_i}s after it had exited"
			return
		fi
		_i=$((_i + 1)); sleep 1
	done
	record no_respawn_after_stop PASS "daemon exited on stop and stayed down for 10s — respawn is procd's, not a ghost"
}

assert_stop_restores_legacy() {
	wait_for 15 legacy_running || true
	_run=no; legacy_running && _run=yes
	_en=no; svc_enabled vectra-controller && _en=yes
	_mk=present; [ -f "$LEGACY_MARKER" ] || _mk=gone
	if [ "$_run" = yes ] && [ "$_en" = yes ] && [ "$_mk" = gone ]; then
		record stop_restores_legacy PASS "stop handed the router back: legacy enabled + running, marker consumed"
	else
		record stop_restores_legacy FAIL "after stop: legacy running=$_run enabled=$_en marker=$_mk (router left with no controller)"
	fi
}

# Negative control for the two restore assertions: a legacy agent the OPERATOR
# disabled must never be switched back on. The marker is what tells the two
# cases apart, so this proves the restore is guarded and not unconditional.
assert_marker_guard() {
	# Precondition, not masking: the case under test is "vctl has never disabled
	# this agent". In procd-no-restore the previous stop deliberately failed to
	# consume the marker, and a stale marker is a DIFFERENT state — one the two
	# restore assertions already caught. Clear it so this check measures the
	# guard rather than re-reporting the injected defect.
	rm -f "$LEGACY_MARKER"
	"$LEGACY_INIT" stop >/dev/null 2>&1
	"$LEGACY_INIT" disable >/dev/null 2>&1
	sleep 2
	if legacy_running || svc_enabled vectra-controller; then
		record marker_guard_no_resurrect FAIL "could not put the legacy agent into the operator-disabled state"
		return
	fi
	"$VCTL_INIT" start >/dev/null 2>&1
	wait_for 25 daemon_up || true
	_mk=no; [ -f "$LEGACY_MARKER" ] && _mk=yes
	"$VCTL_INIT" stop >/dev/null 2>&1
	sleep 3
	_run=no; legacy_running && _run=yes
	_en=no; svc_enabled vectra-controller && _en=yes
	if [ "$_mk" = no ] && [ "$_run" = no ] && [ "$_en" = no ]; then
		record marker_guard_no_resurrect PASS "operator-disabled legacy stayed disabled across a vctl start/stop (no marker written)"
	else
		record marker_guard_no_resurrect FAIL "marker=$_mk legacy running=$_run enabled=$_en — vctl resurrected an agent it never disabled"
	fi
}

assert_prerm_restores_legacy() {
	wait_for 15 legacy_running || true
	_run=no; legacy_running && _run=yes
	_en=no; svc_enabled vectra-controller && _en=yes
	_left=
	for p in $PKG_VOLATILE_PATHS; do [ -e "$p" ] && _left="$_left $p"; done
	_pid=$(daemon_pid)
	if [ "$_run" = yes ] && [ "$_en" = yes ] && [ -z "$_left" ] && [ -z "$_pid" ]; then
		record prerm_restores_legacy PASS "opkg remove: package files gone, daemon gone, legacy enabled + running"
	else
		record prerm_restores_legacy FAIL "legacy running=$_run enabled=$_en daemon='${_pid:-none}' files left:${_left:- none}"
	fi
}

# opkg keeps a conffile the operator has touched. If it ever stopped doing so,
# an uninstall would silently take the router's control_url, poll interval and
# job-safety floors with it.
assert_conffile_preserved() {
	if [ -f "$PKG_CONFFILE" ] && grep -q "poll_interval '2'" "$PKG_CONFFILE" 2>/dev/null; then
		record conffile_preserved PASS "$PKG_CONFFILE survived the removal with the operator's edit intact"
	else
		record conffile_preserved FAIL "$PKG_CONFFILE missing or reverted after removal"
	fi
}

# The field report that motivated this mode: "the service came up on its own
# after install despite VECTRA_SKIP_POSTINST_RESTART=1". A FRESH flagged install
# does honour the flag (postinst_skip_honored). This is the other path an update
# takes — a re-install over a package that is already installed, enabled and
# RUNNING. opkg removes first, which runs prerm and stops the daemon; the
# flagged postinst must then leave it stopped.
assert_postinst_skip_on_reinstall() {
	_pid=$(daemon_pid)
	if [ -z "$_pid" ]; then
		record postinst_skip_on_reinstall PASS "flagged re-install over a running service left the daemon stopped"
	else
		record postinst_skip_on_reinstall FAIL "daemon pid=$_pid running after a re-install that set VECTRA_SKIP_POSTINST_RESTART=1"
	fi
}

# ---- the OTHER proxy stack -------------------------------------------------

# ANTI-VACUITY, and it runs BEFORE vctl exists. Everything below is of the form
# "PassWall is down and stays down", which a stand where PassWall simply never
# restarts would satisfy for free. So prove the watchdog is real first: stop the
# stack with the legacy agent still running, and require it BACK.
#
# This is the behaviour that cost the first canary. The stand could not
# reproduce any of it while its legacy stand-in was a sleep loop.
assert_legacy_watchdog_armed() {
	"$PASSWALL_INIT" stop >/dev/null 2>&1 || true
	if wait_for 10 passwall_running; then
		record legacy_watchdog_armed PASS \
			"the legacy agent restarted the PassWall stack after it was stopped — the watchdog is real, so every 'stays down' below means something"
	else
		record legacy_watchdog_armed FAIL \
			"PassWall did not come back with the legacy agent running; the watchdog is not armed and the assertions below prove nothing"
	fi
}

# THE HAND-OVER, the half that was missing entirely. Disabling the agent stops
# what RECONFIGURES PassWall; PassWall itself is an independent service with its
# own rc.d symlink and keeps running — and on a real unit, its own xray and its
# own fwmark policy route with it.
assert_passwall_stopped_on_handover() {
	_run=yes; passwall_running || _run=no
	_en=yes; svc_enabled passwall2 || _en=no
	_mk=no; [ -f "$PASSWALL_MARKER" ] && _mk=yes
	if [ "$_run" = no ] && [ "$_en" = no ] && [ "$_mk" = yes ]; then
		record passwall_stopped_on_handover PASS \
			"the other proxy stack is stopped and disabled, hand-back marker written"
	else
		record passwall_stopped_on_handover FAIL \
			"two proxy stacks on one router: passwall running=$_run enabled=$_en marker=$_mk"
	fi
}

# And it must STAY down. The agent was disabled first precisely so its watchdog
# is gone by now; if the order were wrong, or the agent survived, the stack is
# back within a couple of ticks. Waited out rather than assumed.
assert_passwall_stays_down() {
	sleep 8
	if passwall_running; then
		record passwall_stays_down FAIL \
			"the PassWall stack came back under vctl — something is still watchdogging it"
	else
		record passwall_stays_down PASS \
			"still down after 8s (4 watchdog ticks): the agent was disabled before the stack, so nothing restarts it"
	fi
}

# The fallback direction, which is the whole reason the hand-back exists.
# PW_RUN_AFTER_STOP is sampled by the caller IMMEDIATELY after the vctl stop
# returns. Reading it later would be reading the restored legacy agent's
# watchdog: it ticks every 2s and calls `start`, so `running=yes` would be true
# whether or not restore_passwall_stack did anything. (It never calls `enable`,
# so the enabled and marker halves stay discriminating either way.)
assert_passwall_restored() { # <assertion-name> <what-happened>
	_run="${PW_RUN_AFTER_STOP:-no}"
	_en=no; svc_enabled passwall2 && _en=yes
	_mk=present; [ -f "$PASSWALL_MARKER" ] || _mk=gone
	if [ "$_run" = yes ] && [ "$_en" = yes ] && [ "$_mk" = gone ]; then
		record "$1" PASS "$2: the PassWall stack was enabled and already running the moment vctl stopped, marker consumed"
	else
		record "$1" FAIL "$2: passwall running=$_run enabled=$_en marker=$_mk — the router has no proxy at all"
	fi
}

assert_control_plane_sinkholed() {
	_hit=$(guard_ctr_named cp_sinkhole)
	_decoy=$(guard_ctr_named cp_decoy)
	if [ "$_hit" -gt 0 ] 2>/dev/null && [ "$_decoy" = 0 ]; then
		record cp_sinkholed PASS "daemon dialled the control plane $_hit times, all into $CP_SINKHOLE_IP; decoy counter 0"
	else
		record cp_sinkholed FAIL "cp_sinkhole=$_hit (want >0) cp_decoy=$_decoy (want 0)"
	fi
}

assert_never_leaves_box() {
	_v4=$(ip route show default 2>/dev/null)
	_v6=$(ip -6 route show default 2>/dev/null)
	curl -s --max-time 4 -o /dev/null "http://$CP_PROBE_IP/" 2>/dev/null && _reach=yes || _reach=no
	_probe=$(guard_ctr_named cp_probe)
	if [ -z "$_v4" ] && [ -z "$_v6" ] && [ "$_reach" = no ] && [ "$_probe" = 0 ]; then
		record cp_never_leaves_box PASS "no default route v4/v6; probe to $CP_PROBE_IP unreachable; 0 packets left toward it"
	else
		record cp_never_leaves_box FAIL "default v4='$_v4' v6='$_v6' reachable=$_reach probe_pkts=$_probe"
	fi
}

# ------------------------------------------------------------------- main -----

run_procd_mode() {
	say "isolate the box before anything is installed"
	procd_isolate
	procd_start_service_manager
	install_legacy_stub

	say "the legacy agent owns the router: a hand-started vctl must refuse"
	assert_supervise_legacy_guard

	say "the legacy agent's PassWall watchdog is armed (control for everything below)"
	assert_legacy_watchdog_armed

	say "build and install the real .ipk (VECTRA_SKIP_POSTINST_RESTART=1)"
	stage_package
	VECTRA_SKIP_POSTINST_RESTART=1 opkg_install
	sleep 3
	assert_pkg_installed
	assert_postinst_skip_honored

	say "control: the same install WITHOUT the skip flag must start the service"
	opkg_remove
	sleep 2
	opkg_install
	assert_postinst_starts_by_default
	assert_procd_owns_daemon
	assert_legacy_disabled_on_start
	assert_passwall_stopped_on_handover
	assert_passwall_stays_down
	assert_supervise_after_handover
	# Tighten the loop so the control-plane dial counter moves quickly. This
	# also makes the conffile "operator-modified", which is what opkg keys on.
	uci -q set "$PKG_NAME.main.poll_interval=2" && uci -q commit "$PKG_NAME"

	say "the field surprise: a FLAGGED re-install over a running service"
	VECTRA_SKIP_POSTINST_RESTART=1 opkg_install
	sleep 6
	assert_postinst_skip_on_reinstall

	say "restore the service for the respawn test"
	opkg_install
	wait_for 30 daemon_up || info "daemon did not come back after the plain re-install"

	say "procd respawn"
	assert_respawn

	say "stop must hand the router back to the legacy agent"
	# The package's dead-man (deadman.sh, cron's, every minute) starts again a
	# vctl that is switched on, has its boot links and does not run — on
	# purpose: that is what brings vctl back from a restart cut off between its
	# stop and its start. A bare stop leaves both, so the next minute starts it
	# and takes the agent again, and every assertion below raced that minute
	# (a new pid 7 s after the exit, the agent disabled again). This phase is
	# the init script's stop alone: cron waits it out. The dead-man's own
	# behaviour is openwrt/watch_test.go's and the passwall-real mode's.
	/etc/init.d/cron stop >/dev/null 2>&1
	"$VCTL_INIT" stop 2>&1 | sed 's/^/       init: /'
	# Sampled HERE, not inside the assertion: everything below takes tens of
	# seconds, and the legacy agent this stop just restored watchdogs PassWall
	# every 2s. Read later, "running" would be the watchdog's doing rather than
	# restore_passwall_stack's.
	PW_RUN_AFTER_STOP=no; passwall_running && PW_RUN_AFTER_STOP=yes
	assert_no_respawn_after_stop
	assert_stop_restores_legacy
	assert_passwall_restored passwall_restored_on_stop "after 'stop'"
	/etc/init.d/cron start >/dev/null 2>&1

	say "control: an operator-disabled legacy agent must NOT be resurrected"
	assert_marker_guard

	say "prerm must hand the router back on removal"
	"$LEGACY_INIT" enable >/dev/null 2>&1
	"$LEGACY_INIT" start >/dev/null 2>&1
	wait_for 10 legacy_running || die "could not restage the legacy stub"
	"$VCTL_INIT" start 2>&1 | sed 's/^/       init: /'
	wait_for 25 daemon_up || info "daemon did not come up before removal"
	sleep 3
	opkg_remove
	PW_RUN_AFTER_STOP=no; passwall_running && PW_RUN_AFTER_STOP=yes
	sleep 3
	assert_prerm_restores_legacy
	assert_passwall_restored passwall_restored_on_remove "after 'opkg remove'"
	assert_conffile_preserved

	say "the stand never talked to the production control plane"
	assert_control_plane_sinkholed
	assert_never_leaves_box

	say "procd view + counters"
	ubus call service list 2>/dev/null | head -40
	nft list counters table inet standguard 2>/dev/null
}
