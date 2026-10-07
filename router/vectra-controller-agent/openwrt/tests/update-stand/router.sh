#!/bin/sh
# Inside one stand router (openwrt/rootfs container). Modes:
#   boot            PID 1: what a router brings up at boot, for this test —
#                   ubusd, procd, logd, then the enabled cron and
#                   vectra-controller services (an rc.d stand-in). Runs forever;
#                   `docker restart` is the reboot.
#   setup           install the v1 pair (r46) from /fixtures/v1, point it at
#                   $PANEL_URL, start it, wait for its first panel contact
#   update <name>   run the panel-generated command /cmd/<name>.sh as the
#                   agent's terminal runner does (sh -c), print its exit code
#   state           one line of facts the host judges
set -u

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

wait_for() { # <seconds> <command...>
	w="$1"
	shift
	while [ "$w" -gt 0 ]; do
		"$@" >/dev/null 2>&1 && return 0
		sleep 1
		w=$((w - 1))
	done
	return 1
}

pkg_version() {
	awk -v pkg="$1" '/^Package: / { c = ($2 == pkg); next } c && /^Version: / { print $2; exit }' /usr/lib/opkg/status
}

case "${1:-}" in
boot)
	mkdir -p /var/lock /var/run /var/state /tmp/lock
	ubusd &
	sleep 1
	procd &
	wait_for 15 sh -c 'ubus list | grep -q "^service$"' || echo "STAND: procd did not come up" >&2
	/sbin/logd -S 64 &
	wait_for 10 sh -c 'ubus list | grep -q "^log$"'
	if [ ! -f /etc/stand-first-boot-done ]; then
		for f in /etc/uci-defaults/*; do
			[ -f "$f" ] && (. "$f") >/dev/null 2>&1 && rm -f "$f"
		done
		: > /etc/stand-first-boot-done
	fi
	for s in cron vectra-controller; do
		if [ -x "/etc/init.d/$s" ] && "/etc/init.d/$s" enabled; then
			"/etc/init.d/$s" start >/dev/null 2>&1
		fi
	done
	date +%s > /tmp/stand-booted
	while :; do sleep 3600; done
	;;
setup)
	wait_for 30 test -f /tmp/stand-booted || { echo "STAND-FATAL not booted"; exit 1; }
	opkg install /fixtures/v1/vectra-controller-agent.ipk /fixtures/v1/luci-app-vectra-controller.ipk >/tmp/stand-setup.log 2>&1 || { cat /tmp/stand-setup.log; echo "STAND-FATAL v1 install"; exit 1; }
	uci set vectra-controller.main.control_url="${PANEL_URL:-http://panel:8080}"
	uci set vectra-controller.main.panel_url="${PANEL_URL:-http://panel:8080}"
	uci commit vectra-controller
	/etc/init.d/vectra-controller enable
	/etc/init.d/vectra-controller start
	if [ "${EXPECT_CONTACT:-1}" = 1 ]; then
		wait_for 40 sh -c 'jsonfilter -i /etc/vectra-controller/state.json -e "@.control_plane_recovery.last_successful_control_plane_at" | grep -q T' ||
			{ echo "STAND-FATAL v1 never contacted the panel"; exit 1; }
	else
		wait_for 20 pidof vectra-controller-agent || { echo "STAND-FATAL v1 not running"; exit 1; }
	fi
	echo "SETUP-OK pid=$(pidof vectra-controller-agent) status_md5=$(md5sum /usr/lib/opkg/status | cut -d' ' -f1)"
	;;
update)
	sh -c "$(cat "/cmd/$2.sh")" >/tmp/stand-update.out 2>&1
	rc=$?
	sed 's/^/  | /' /tmp/stand-update.out | tail -n 15
	echo "UPDATE-EXIT $rc"
	;;
state)
	pid="$(pidof vectra-controller-agent 2>/dev/null | awk '{ print $1 }')"
	variant="$(sed -n 's/^VARIANT=//p' /usr/sbin/vectra-controller-agent 2>/dev/null | head -n 1)"
	[ -n "$variant" ] || variant=none
	session=gone
	[ -e /etc/vectra-controller/update-rollback ] && session=present
	cron="$(grep -c 'vectra-update-guard (managed) >>>' /etc/crontabs/root 2>/dev/null)"
	marker="$(sed -n 's/^reason=//p' /etc/vectra-controller/update-rollback.marker 2>/dev/null | tr ' ' '_')"
	attempts="$(sed -n 's/^attempts=//p' /etc/vectra-controller/update-rollback.marker 2>/dev/null)"
	tmpcopy=no
	[ -f /tmp/vectra-update-rollback/backup.tgz ] && tmpcopy=yes
	persistcopy=no
	[ -f /etc/vectra-controller/update-rollback/backup.tgz ] && persistcopy=yes
	phase="$(cat /etc/vectra-controller/update-rollback/phase 2>/dev/null)"
	echo "STATE agent=$(pkg_version vectra-controller-agent) luci=$(pkg_version luci-app-vectra-controller) variant=$variant pid=${pid:-none} session=$session phase=${phase:-none} cron=${cron:-0} persistcopy=$persistcopy tmpcopy=$tmpcopy status_md5=$(md5sum /usr/lib/opkg/status | cut -d' ' -f1) attempts=${attempts:-none} marker=${marker:-none}"
	;;
log)
	logread 2>/dev/null | grep -E 'vectra-update-guard|vectra-controller' | tail -n "${2:-20}"
	;;
*)
	echo "usage: $0 boot|setup|update <name>|state|log" >&2
	exit 2
	;;
esac
