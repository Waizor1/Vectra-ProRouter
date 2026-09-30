#!/bin/sh
# canary-guard.sh — self-supervising canary for vectra-controller-pro on a live router.
#
# The first canary reverted on a blind timer: it needed a human to say "keep going"
# inside a fixed window, and it rolled back when that window closed for reasons that
# had nothing to do with the router's health. This one reverts on EVIDENCE instead:
# it watches the things that actually matter and hands the router back to PassWall
# the moment they stop being true. The absolute cap is a backstop, not the trigger.
#
# Exit paths:
#   healthy            -> keeps running until CAP_SECONDS, then reverts (backstop)
#   FAIL_STREAK misses -> reverts immediately
#   /tmp/canary-abort  -> reverts immediately (manual panic button)
#   /tmp/canary-extend -> pushes the cap out by CAP_SECONDS again, then is consumed
#
# Everything lives in tmpfs on purpose: a reboot is itself a full recovery.

set -u

# Every knob is overridable so the guard can be exercised against stubs off the
# router. The defaults are the production values; the test harness supplies its
# own. A guard whose revert logic has never been observed firing is just a hope.
: "${DIR:=/tmp/canary}"
: "${STATUS:=$DIR/status.json}"
: "${MEMINFO:=/proc/meminfo}"
: "${PROCDIR:=/proc}"
: "${TEARDOWN:=/usr/libexec/vectra-controller-pro/dataplane-teardown.sh}"
: "${INITSCRIPT:=/etc/init.d/vectra-controller-pro}"
: "${ABORT_FILE:=/tmp/canary-abort}"
: "${EXTEND_FILE:=/tmp/canary-extend}"

: "${CHECK_INTERVAL:=30}"    # seconds between health sweeps
: "${FAIL_STREAK:=3}"        # consecutive bad sweeps before we hand the router back
: "${CAP_SECONDS:=14400}"    # 4h backstop
: "${MEM_FLOOR_KB:=25000}"   # below this the router is heading for the OOM killer
: "${EGRESS_URL:=https://api.ipify.org}"
: "${EGRESS_TIMEOUT:=8}"

LOG=$DIR/guard.log

mkdir -p "$DIR"

log() { echo "$(date -u +%FT%TZ) $*" >>"$LOG"; }

# ---------------------------------------------------------------- health probes

# Our xray, by the pid vctl recorded — not by name. `pgrep -x xray` is a known
# false negative on this fleet because PassWall runs xray under a wrapper, and a
# name match would also happily find someone else's process.
#
# The field really is top-level `pid` (supervisor.Status, written verbatim by
# WriteStatus). It carries `omitempty`, so a stopped supervisor omits it — which
# is exactly the answer we want. Do not guess this name: a wrong one reads as an
# empty pid, reports "dead" on every sweep, and reverts a perfectly healthy run.
xray_alive() {
	grep -q '"state"[[:space:]]*:[[:space:]]*"running"' "$STATUS" 2>/dev/null || return 1
	pid=$(sed -n 's/.*"pid"[[:space:]]*:[[:space:]]*\([0-9]*\).*/\1/p' "$STATUS" 2>/dev/null | head -1)
	[ -n "$pid" ] && [ "$pid" -gt 0 ] 2>/dev/null || return 1
	[ -d "$PROCDIR/$pid" ] || return 1
	# Guard against pid reuse: the cmdline must still be an xray.
	tr '\0' ' ' <"$PROCDIR/$pid/cmdline" 2>/dev/null | grep -q xray
}

mem_ok() {
	kb=$(awk '/MemAvailable/{print $2}' "$MEMINFO")
	[ -n "$kb" ] || return 1
	[ "$kb" -ge "$MEM_FLOOR_KB" ]
}

egress_ok() {
	code=$(curl -s -m "$EGRESS_TIMEOUT" -o /dev/null -w '%{http_code}' "$EGRESS_URL" 2>/dev/null)
	[ "$code" = "200" ]
}

# The data plane must still be installed. If the table or the policy route has
# gone missing, traffic is either black-holed or silently bypassing us; both are
# reasons to stop, and neither shows up in a process check.
dataplane_ok() {
	nft -t list table inet vctl >/dev/null 2>&1 || return 1
	ip rule show 2>/dev/null | grep -q 'fwmark 0x1 lookup 100'
}

# Client traffic must actually be arriving. A data plane that is loaded but
# capturing nothing means the LAN is bypassing us — the failure mode that looks
# healthy from every angle except this one.
tproxy_advancing() {
	now=$(nft -t list table inet vctl 2>/dev/null |
		sed -n 's/.*vctl_tproxy_hits.*packets \([0-9]*\).*/\1/p' | head -1)
	# An unreadable counter means the table is gone, which is dataplane_ok's
	# question, not ours. Answering it here too would revert with a misleading
	# reason and rob the operator of the real diagnosis.
	[ -n "$now" ] || return 0
	prev=$(cat "$DIR/.last_hits" 2>/dev/null || echo 0)
	echo "$now" >"$DIR/.last_hits"
	# A counter that goes BACKWARD means the table was torn down and rebuilt
	# underneath us — a real fault. A counter that merely stands still means the
	# household is idle, which is not, so only a regression is treated as a miss.
	[ "$now" -ge "$prev" ]
}

# ---------------------------------------------------------------------- revert

revert() {
	reason=$1
	log "REVERT: $reason"

	# Prefer the init script. It is the ONLY code that hands the router back to
	# the legacy vectra-controller agent: start_service disables that agent and
	# drops a marker, and only stop_service consumes the marker to re-enable it.
	#
	# `killall vctl` bypasses all of that. The router would keep working —
	# PassWall still routes traffic — but it would be invisible to the panel,
	# with no way to fix it remotely. On a customer router that is a site visit.
	# Reach for killall only when there is no init script to call.
	if [ -x "$INITSCRIPT" ]; then
		log "stopping via init script (hands the router back to the legacy agent)"
		"$INITSCRIPT" stop >>"$LOG" 2>&1
	else
		log "no init script; killing vctl directly"
		killall vctl 2>/dev/null
	fi
	sleep 1
	# Run the helper exactly once. It is normally executable; a lost +x bit must
	# not skip the teardown, which is the whole point of the `sh` fallback.
	# stop_service already unloads the data plane, but this is a belt-and-braces
	# path for the manual-supervise case and for a half-installed package.
	if [ -x "$TEARDOWN" ]; then
		"$TEARDOWN" >>"$LOG" 2>&1
	elif [ -f "$TEARDOWN" ]; then
		sh "$TEARDOWN" >>"$LOG" 2>&1
	else
		log "teardown helper missing; falling through to manual unwind"
	fi
	nft delete table inet vctl 2>/dev/null
	ip rule del fwmark 0x1 lookup 100 2>/dev/null
	ip route del local 0.0.0.0/0 dev lo table 100 2>/dev/null
	ip -6 rule del fwmark 0x1 lookup 100 2>/dev/null
	ip -6 route del local ::/0 dev lo table 100 2>/dev/null
	uci set passwall2.@global[0].enabled=1
	uci commit passwall2
	/etc/init.d/passwall2 restart >>"$LOG" 2>&1
	log "REVERT done; passwall restarted"
	echo "$reason" >"$DIR/revert-reason"
	exit 0
}

# ------------------------------------------------------------------- main loop

started=$(date +%s)
deadline=$((started + CAP_SECONDS))
streak=0
log "guard start: interval=${CHECK_INTERVAL}s streak=${FAIL_STREAK} cap=${CAP_SECONDS}s mem_floor=${MEM_FLOOR_KB}kB"

while :; do
	sleep "$CHECK_INTERVAL"
	now=$(date +%s)

	[ -f "$ABORT_FILE" ] && revert "manual abort"

	if [ -f "$EXTEND_FILE" ]; then
		rm -f "$EXTEND_FILE"
		deadline=$((now + CAP_SECONDS))
		log "cap extended to $(date -u -d "@$deadline" +%FT%TZ 2>/dev/null || echo "+${CAP_SECONDS}s")"
	fi

	[ "$now" -ge "$deadline" ] && revert "absolute cap reached"

	# A regressing counter is a HARD fault: it means something tore our table
	# down and built a new one underneath us. Unlike memory or egress it cannot
	# be a noisy sample, so it must not go through the streak — and it provably
	# cannot survive one. The probe rebases `.last_hits` to the new value, so the
	# very next sweep compares 5 against 5, passes, and resets the streak. Three
	# consecutive regressions are unreachable; on the streak this check would be
	# dead code that looks alive.
	tproxy_advancing || revert "health: counter_regressed"

	bad=""
	xray_alive || bad="$bad xray_dead"
	mem_ok || bad="$bad low_mem"
	dataplane_ok || bad="$bad dataplane_gone"
	egress_ok || bad="$bad egress_down"

	if [ -n "$bad" ]; then
		streak=$((streak + 1))
		log "unhealthy ($streak/$FAIL_STREAK):$bad"
		[ "$streak" -ge "$FAIL_STREAK" ] && revert "health:$bad"
	else
		[ "$streak" -gt 0 ] && log "recovered after $streak miss(es)"
		streak=0
		log "ok mem=$(awk '/MemAvailable/{print $2}' "$MEMINFO")kB hits=$(cat "$DIR/.last_hits" 2>/dev/null) left=$((deadline - now))s"
	fi
done
