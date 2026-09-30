#!/bin/sh
# The legacy agent's work loop — and, deliberately, its PASSWALL WATCHDOG.
#
# This used to be a bare `sleep 3600` loop, and that gap cost a canary. The real
# vectra-controller agent does not just sit there: it watches the PassWall stack
# and restarts it whenever it finds it stopped. On 2026-08-05 that watchdog
# brought PassWall back up underneath a running vctl, and the router ended up
# with two xray processes and two fwmark policy routes fighting over the same
# traffic. The stand could not reproduce any of it, because its stand-in did
# nothing.
#
# So it does the one thing that matters now. Two consequences the stand can
# finally observe:
#
#   * stopping PassWall without first disabling THIS agent is pointless — the
#     stack is back within a few seconds. That is why the hand-over's order
#     (agent first, stack second) is load-bearing;
#   * a hand-over that never stops the stack at all leaves two of them running,
#     which is what MODE=procd-no-passwall-stop demonstrates.
#
# It acts as the real agent's watchdog does (passwall_watchdog.go): only while
# PassWall's own switch, uci passwall2.@global[0].enabled, is on (the agent's
# inventory.PasswallEnabled), by `/etc/init.d/passwall2 restart`, and then it
# holds off before it may act again (the real agent: 5 minutes; here 10 s).
# "PassWall runs" is the stand's stub process, or the real luci-app-passwall2's
# proxies, which run from its temp bin directory (MODE=passwall-real). Its first
# look comes a few seconds after it starts, as the real agent's first loop —
# config, inventory, check-in — takes: at boot PassWall's own delayed start
# (1 s in MODE=passwall-real) comes first, and a restart that finds PassWall's
# lock held does nothing.
#
# It opens no sockets and reaches no network, exactly like the loop it replaces.
PASSWALL_INIT=/etc/init.d/passwall2
HOLD=10

passwall_runs() {
	# Bracketed: the real PassWall's stop SIGKILLs every process that names
	# "passwall2/" in its command line, a grep for it included.
	ps w 2>/dev/null | grep -q -e '[p]asswall-loop.sh' -e '[/]tmp/etc/passwall[2]/bin/'
}

sleep 4
while :; do
	if [ -x "$PASSWALL_INIT" ] && [ "$(uci -q get passwall2.@global[0].enabled)" = 1 ] && ! passwall_runs; then
		"$PASSWALL_INIT" restart >/dev/null 2>&1 || true
		sleep "$HOLD"
	fi
	sleep 2
done
