#!/bin/sh
# Vectra's watch over its own daemon. cron runs it every minute (the
# package's managed block in /etc/crontabs/root); `deadman.sh hold` runs on a
# clock step too (/etc/hotplug.d/ntp/60-vectra-controller-pro). Shell, and
# nothing of vctl's: it has to work when vctl itself is what broke — and on a
# router where nothing else could take over, since Vectra's own routers come
# without PassWall and without the old agent.
#
# vctl should run — Vectra switched on (its UCI switch and its boot links),
# or a trial — and procd does not run it, or its process is gone:
#
# 1. The data plane goes at once. A dead vctl with its tproxy rules loaded
#    black-holes the LAN (with the kill switch on, the router's own traffic
#    too); the internet straight out is the safe state.
# 2. vctl is started again, with a backoff: at once, then after 1, 2, 5, 10
#    and 15 minutes, then every 15 minutes, for as long as it takes. procd
#    gives up on a vctl that keeps crashing (respawn 15 5 20); this does not.
# 3. Only where there is someone to give the router back to — PassWall or the
#    old agent installed, and a breadcrumb of the takeover saying it is owed —
#    and vctl has stayed down for 10 minutes: `vectra off`, in shell (its UCI
#    switch off, the init script's stop — the hand-back — and the boot links
#    gone unless a hand-back is still owed). Without one it never gives up.
# 4. A minute that finds vctl running starts the count again.
#
# While vctl runs, configured (its render and an operator config there) but
# without its data plane for 5 minutes, that is said, and nothing done: the
# daemon's own rescue owns that case.
#
# The legacy agent's cron watchdog, where there is one, is held off again
# while vctl runs (hold_agent_watchdog in the init script: its own throttle
# stamp, 10 minutes ahead). Nothing else sets it again, so once vctl is gone
# the watchdog is free to bring the agent back within 15 minutes, whatever
# else fails. A clock step can put the stamp in the past at once — NTP after
# a long power cut — hence the hotplug.
#
# Never while a power change holds its lock (taken here for as long as this
# acts, so that none starts meanwhile either), nor while the init script's
# stop or reload holds procd's, nor while procd does not answer: those
# minutes count for nothing.

PKG="vectra-controller-pro"
TAG="vectra-controller-pro-deadman"
VCTL_INIT="${VCTL_INIT:-/etc/init.d/$PKG}"
POWER_LOCK="${POWER_LOCK:-/var/lock/vectra-power.lock}"
PROCD_LOCK="${PROCD_LOCK:-/var/lock/procd_$PKG.lock}"
STATE="${STATE:-/var/run/vectra-deadman.state}"
XRAY_RENDER="${XRAY_RENDER:-/var/run/$PKG/xray.json}"
OPERATOR_CONFIG="${OPERATOR_CONFIG:-/etc/$PKG/xray-desired.json}"
HANDBACK_MINUTES=10
IDLE_MINUTES=5

[ -r "$VCTL_INIT" ] || exit 0
# The init script's paths and helpers — its breadcrumbs, the trial, the hold,
# the data plane's teardown, passwall_init. At top level it only sets
# variables and defines functions.
# shellcheck source=/dev/null
INCIDENT_SOURCE=deadman
. "$VCTL_INIT"

# vctl_up: procd runs vctl, and its process is there.
vctl_up() {
	"$VCTL_INIT" running >/dev/null 2>&1 && pgrep -f "$VCTL_BIN agent" >/dev/null 2>&1
}

# should_run: Vectra is switched on — the UCI switch read as the init script
# reads it, and its boot links — or a trial runs.
should_run() {
	trial && return 0
	case "$(uci -q get "$CONF.$SECTION.enabled")" in
	0 | off | false | no | disabled) return 1 ;;
	esac
	"$VCTL_INIT" enabled >/dev/null 2>&1
}

# owed: someone to give the router back to — a breadcrumb of the takeover for
# a service still installed.
owed() {
	local m
	if [ -x "$LEGACY_INIT" ]; then
		[ -f "$LEGACY_MARKER" ] || [ -f "$TRIAL_MARKER_DIR/${LEGACY_MARKER##*/}" ] && return 0
	fi
	passwall_init >/dev/null || return 1
	for m in "$PASSWALL_MARKER" "$PASSWALL_SWITCH_MARKER"; do
		[ -f "$m" ] || [ -f "$TRIAL_MARKER_DIR/${m##*/}" ] && return 0
	done
	return 1
}

# Terse: listed whole, the table's direct set costs nft ~23 MB, every minute.
loaded() {
	nft -t list table inet vctl >/dev/null 2>&1
}

# held <lock file>: someone holds it. Looked at, not kept.
held() {
	[ -f "$1" ] && ! flock -n "$1" true >/dev/null 2>&1
}

# backoff <attempts made>: minutes to the next start.
backoff() {
	case "$1" in
	1) echo 1 ;;
	2) echo 2 ;;
	3) echo 5 ;;
	4) echo 10 ;;
	*) echo 15 ;;
	esac
}

# The state, on tmpfs: minutes down, starts made, the minute of the next
# start, minutes idle.
down=0 attempts=0 next=0 idle=0
[ -f "$STATE" ] && read -r down attempts next idle < "$STATE"
for v in down attempts next idle; do
	eval "case \"\$$v\" in '' | *[!0-9]*) $v=0 ;; esac"
done
save() {
	mkdir -p "${STATE%/*}" 2>/dev/null
	echo "$down $attempts $next $idle" > "$STATE"
}

ubus -t 5 list service >/dev/null 2>&1 || exit 0
# procd's lock for this service first: `running` below takes it, and a verb
# in flight (a stop, a reload) — or anything that kept it — would hold every
# minute's run here. A minute skipped counts for nothing.
held "$PROCD_LOCK" && exit 0

if vctl_up; then
	[ -x "$LEGACY_INIT" ] && hold_agent_watchdog
	[ "${1:-}" = hold ] && exit 0
	down=0 attempts=0 next=0
	if [ -f "$XRAY_RENDER" ] && [ -f "$OPERATOR_CONFIG" ] && ! loaded; then
		idle=$((idle + 1))
		if [ "$idle" -ge "$IDLE_MINUTES" ] && [ $(((idle - IDLE_MINUTES) % 15)) -eq 0 ]; then
			logger -p daemon.warn -t "$TAG" \
				"vctl runs, configured, but its data plane has not been loaded for $idle minutes: the LAN goes out without the VPN (the daemon's own rescue owns this; logread -e vctl)"
		fi
	else
		idle=0
	fi
	save
	exit 0
fi
[ "${1:-}" = hold ] && exit 0

# The first minutes after a boot are the boot's own: its start of vctl (S95)
# may still be on the way, and a second one from here would race it. An
# uptime that cannot be read counts as settled.
if read -r up _ < "${UPTIME_FILE:-/proc/uptime}" 2>/dev/null && [ "${up%%.*}" -lt "${BOOT_SETTLE:-180}" ] 2>/dev/null; then
	exit 0
fi

if ! should_run; then
	rm -f "$STATE"
	exit 0
fi
command -v flock >/dev/null 2>&1 || exit 0
mkdir -p "${POWER_LOCK%/*}" 2>/dev/null
exec 9>>"$POWER_LOCK"
flock -n 9 || exit 0
held "$PROCD_LOCK" && exit 0

down=$((down + 1))
idle=0
if loaded; then
	logger -p daemon.err -t "$TAG" "vctl does not run and its data plane is still loaded: unloading it — the LAN goes out without the VPN until vctl is back"
	unload_dataplane
fi

if owed && [ "$down" -ge "$HANDBACK_MINUTES" ]; then
	logger -p daemon.crit -t "$TAG" \
		"vctl has not run for $down minutes after $attempts starts; turning Vectra off and giving the router back"
	incident HANDBACK "vctl down for 10 minutes" "vctl stayed down for $down minutes after $attempts starts; the dead-man gave the router back"
	rm -f "$TRIAL_FILE" "$STATE"
	if [ "$(uci -q get "$CONF.$SECTION.enabled")" != 0 ] &&
		! { uci -q set "$CONF.$SECTION=controller" && uci -q set "$CONF.$SECTION.enabled=0" && uci -q commit "$CONF"; }; then
		logger -p daemon.crit -t "$TAG" "could not turn $CONF.$SECTION.enabled off; handing the router back all the same"
	fi
	"$VCTL_INIT" stop 9>&- >/dev/null 2>&1
	if owed; then
		logger -p daemon.crit -t "$TAG" "a hand-back is still owed; the boot links stay, so every boot tries it again"
	else
		"$VCTL_INIT" disable 9>&- >/dev/null 2>&1
	fi
	exit 0
fi

if [ "$down" -ge "$next" ]; then
	attempts=$((attempts + 1))
	next=$((down + $(backoff "$attempts")))
	logger -p daemon.err -t "$TAG" \
		"vctl should run and does not (down $down minutes): starting it, attempt $attempts; the next one in $((next - down)) minutes"
	incident VCTL_DOWN "vctl was not running" "vctl was switched on and not running; the dead-man started it (attempt $attempts)"
	"$VCTL_INIT" start 9>&- >/dev/null 2>&1
fi
save
exit 0
