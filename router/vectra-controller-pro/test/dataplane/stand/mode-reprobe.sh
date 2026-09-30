# shellcheck shell=sh
# The nodes' names come back (r35).
#
# Sourced by stand.sh. The failover mode's stand — the package installed with
# opkg, procd, the daemon running xray, node-a and node-b in the inet ns — with
# the entry's nodes by name. xray starts while the name resolves nowhere: what
# a router does after a reboot, its WAN not up yet. Its observatory's first
# round finds both nodes dead, and xray's own next probe comes at a random
# moment of its window (600 s here). Then the name resolves (/etc/hosts, which
# xray's resolver and the watchdog's both read first).
#
#   MODE=reprobe              the watchdog sees the name back and restarts xray
#                             once: both nodes alive within 40 s, logged
#   MODE=reprobe-no-watchdog  UCI failover_watchdog '0' — the router before
#                             r35: the nodes wait for xray's own next probe,
#                             and reprobe_alive_in_40s must FAIL
#
# The budget: the watchdog asks the names again every 15 s while one does not
# resolve, Go reads /etc/hosts again after 5 s, then xray's restart and its
# first round.

. "$STAND/mode-failover.sh"

RP_NAME=nodes.stand
RP_BUDGET=40

# The failover entry, its nodes by name, a probe window long enough that only
# the watchdog brings them back in time.
rp_fixtures() {
	fo_fixtures
	sed -e "s/\"address\": \"10.44.0.2\"/\"address\": \"$RP_NAME\"/" -e 's/"interval": "300s"/"interval": "600s"/' \
		"$FO_DIR/subscription.json" > "$UI_DIR/subscription.json"
	[ "$(grep -c "\"address\": \"$RP_NAME\"" "$UI_DIR/subscription.json")" = 2 ] || die "the entry's nodes are not by name"
	grep -q '"interval": "600s"' "$UI_DIR/subscription.json" || die "the entry's probe window is not 600 s"
}

rp_name() { # on|off: the nodes' name in /etc/hosts
	sed -i "/ $RP_NAME\$/d" /etc/hosts
	[ "$1" = on ] && echo "10.44.0.2 $RP_NAME" >> /etc/hosts
	return 0
}

rp_both_alive() { node_alive node-a && node_alive node-b; }
rp_both_dead() {
	[ "$(ubus -t 20 call vectra nodes 2>/dev/null | jsonfilter -e "@.nodes[@.tag='node-a'].alive")" = false ] \
		&& [ "$(ubus -t 20 call vectra nodes 2>/dev/null | jsonfilter -e "@.nodes[@.tag='node-b'].alive")" = false ]
}

rp_seed_and_apply() {
	mkdir -p /tmp/sysinfo /etc/vectra-controller-pro
	echo "Vectra Reprobe Stand (synthetic)" > /tmp/sysinfo/model
	cp "$UI_DIR/operator-config.json" /etc/vectra-controller-pro/xray-desired.json
	if ! vctl apply-local -config "$UI_AGENT" > /tmp/rp-apply.log 2>&1; then
		tail -n 20 /tmp/rp-apply.log
		die "vctl apply-local failed"
	fi
	/etc/init.d/vectra-controller-pro restart
	ui_wait 40 engine_running || { ui_dump; die "xray never came up under the daemon"; }
	if [ "$MODE" = reprobe-no-watchdog ]; then
		grep -q '"noFailoverWatchdog": *true' "$UI_AGENT" || die "UCI failover_watchdog '0' did not reach $UI_AGENT"
	else
		grep -q noFailoverWatchdog "$UI_AGENT" && die "the watchdog is off in $UI_AGENT"
	fi
	info "daemon up; xray running (pid $(xray_pid))"
}

# ----------------------------------------------------------------- asserts ---

assert_rp_dead_without_names() {
	if ui_wait 30 rp_both_dead; then
		record reprobe_dead_without_names PASS "xray's first round, the name resolving nowhere: node-a and node-b dead"
	else
		record reprobe_dead_without_names FAIL "the nodes are not held dead with their name unresolvable: $(ubus -t 20 call vectra nodes 2>/dev/null | tr -d '\n' | cut -c1-200)"
	fi
}

assert_rp_alive_in_time() {
	_pid0=$(xray_pid)
	rp_name on
	_t0=$(date +%s)
	info "the name resolves from $(date +%T)"
	_t1=""
	while [ $(($(date +%s) - _t0)) -le "$RP_BUDGET" ]; do
		if rp_both_alive; then
			_t1=$(date +%s)
			break
		fi
		sleep 1
	done
	_pid1=$(xray_pid)
	if [ -n "$_t1" ]; then
		record reprobe_alive_in_40s PASS "both nodes alive $((_t1 - _t0)) s after their name resolved (xray pid $_pid0 -> $_pid1)"
	else
		record reprobe_alive_in_40s FAIL "the nodes still dead $RP_BUDGET s after their name resolved (xray pid $_pid0 -> $_pid1)"
	fi
}

assert_rp_logged() {
	_l=$(logread 2>/dev/null | grep "names resolve again" | tail -n 1)
	if [ -n "$_l" ]; then
		record reprobe_logged PASS "$(echo "$_l" | sed 's/.*failover watchdog/failover watchdog/' | cut -c1-170)"
	else
		record reprobe_logged FAIL "no 'names resolve again' in logread ($(logread 2>/dev/null | grep -c vctl) vctl lines)"
	fi
}

run_reprobe_mode() {
	rp_fixtures
	setup_kernel
	setup_topology
	setup_guard
	start_origins
	ui_isolate
	fo_start_slow_probe
	ui_start_nodes
	ui_start_subscription
	ui_boot_services
	/sbin/logd -S 256 >/dev/null 2>&1 &
	ui_wait 10 logread || die "logd never came up"

	say "install the package; the entry's nodes by a name that resolves nowhere yet"
	ui_install_package
	ui_wait 15 ubus list vectra || die "the package installed but rpcd never exposed 'vectra'"
	if [ "$MODE" = reprobe-no-watchdog ]; then
		uci set vectra-controller-pro.main.failover_watchdog=0
		uci commit vectra-controller-pro
		info "UCI failover_watchdog '0': the control, the router before r35"
	fi
	rp_name off
	rp_seed_and_apply
	assert_rp_dead_without_names

	say "the name resolves"
	assert_rp_alive_in_time
	if [ "$MODE" = reprobe ]; then
		assert_rp_logged
		say "data plane integrity"
		assert_tcp
		assert_no_leak
	fi
	[ "$FAILURES" -gt 0 ] && { say "diagnostics for the failures above"; ui_dump; }
}
