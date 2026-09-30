# shellcheck shell=sh
# The failover watchdog, end to end (docs/superpowers/specs/2026-09-30-vctl-
# service-routing-design.md, decision 5).
#
# Sourced by stand.sh. The ui mode's stand — the package installed with opkg,
# procd, the daemon running xray, real xray "provider nodes" in the inet ns —
# with a provider entry shaped like the real «Авто» one: BL-MAIN (leastLoad,
# expected 1) over node-a and node-b, fallback DIRECT; the provider's own
# observatory (300 s, timeout 10 s); bittorrent DIRECT; a catch-all to BL-MAIN.
#
# The node the main balancer uses stops answering — its port dropped in the
# inet ns, so SYNs vanish, as they did on the provider's Netherlands node on
# 2026-09-29 — and the LAN keeps asking. Recovered = a request succeeds
# through the OTHER node.
#
#   MODE=failover              the watchdog on: recovered within 10 s, logged,
#                              an incident queued; the override let go once
#                              the node answers again
#   MODE=failover-no-watchdog  UCI failover_watchdog '0' — the router before
#                              the watchdog: the observatory's next probe
#                              decides, and failover_recovers_in_10s must FAIL
#   MODE=failover-stage        the entry as the provider served it on
#                              2026-09-30: BL-MAIN is node-a alone, its
#                              fallback a loopback stage into BL-B (node-b);
#                              the watchdog puts BL-MAIN on the stage
#
# node-b's probes are answered a second late (its own route to a slow
# responder), so every probe leaves xray's leastLoad preferring node-a: which
# node is dropped is fixed, and the balancer cannot wander off it by chance in
# the control. The entry routes node-a.stand straight to node-a — the stand's
# way to put an answered connection on the recovered node (in the field: the
# observatory's next probe, or another balancer's traffic).

. "$STAND/mode-ui.sh"

FO_DIR=/stand/failover
FO_BUDGET_MS=10000
# What BL-MAIN is moved to, and the entry's remark.
FO_TARGET=node-b
FO_REMARK="🧪 Stand failover"
FO_SUB=subscription.json
if [ "$MODE" = failover-stage ]; then
	FO_TARGET=stage-b
	FO_REMARK="🧪 Stand failover stage"
	FO_SUB=subscription-stage.json
fi
FO_INBOX=/var/run/vectra-reporter/inbox

fo_ms() { awk '{printf "%d\n", $1 * 1000}' /proc/uptime; }

# The ui mode's pieces, with this mode's provider entry and nodes.
fo_fixtures() {
	UI_DIR=/tmp/failover-fixtures
	rm -rf "$UI_DIR"
	mkdir -p "$UI_DIR"
	cp /stand/ui/panel-ok.sh /stand/ui/operator-config.json "$UI_DIR/"
	cp "$FO_DIR/nodes-server.json" "$UI_DIR/"
	cp "$FO_DIR/$FO_SUB" "$UI_DIR/subscription.json"
}

# Where node-b's probes go (failover/nodes-server.json): the origin's reply, a
# second late.
fo_start_slow_probe() {
	in_inet socat "TCP-LISTEN:8082,bind=$ORIGIN_IP,fork,reuseaddr" \
		"SYSTEM:sleep 1; exec $STAND/http-reply.sh" >/dev/null 2>&1 &
}

fo_principle() { vctl api balancers BL-MAIN 2>/dev/null | jsonfilter -e "@['BL-MAIN'].principle[0]" 2>/dev/null; }

# fo_released: xray answers for BL-MAIN, prefers something, and holds no override.
fo_released() {
	_j=$(vctl api balancers BL-MAIN 2>/dev/null)
	[ -n "$(jsonfilter -s "$_j" -e "@['BL-MAIN'].principle[0]" 2>/dev/null)" ] \
		&& [ -z "$(jsonfilter -s "$_j" -e "@['BL-MAIN'].override" 2>/dev/null)" ]
}

fo_incident() { ls "$FO_INBOX"/*-PROXY_FAILOVER-*.json >/dev/null 2>&1; }

# fo_drop <port>: the node's port stops answering — its SYNs vanish.
fo_drop() {
	in_inet nft -f - <<EOF
table inet fostand {
  chain input {
    type filter hook input priority filter; policy accept;
    tcp dport $1 counter drop
  }
}
EOF
}
fo_undrop() { in_inet nft delete table inet fostand 2>/dev/null; }

fo_watchdog_lines() { logread 2>/dev/null | grep -E 'node failing|balancer back|no answer through|re-point|main balancer answers'; }

fo_seed_and_apply() {
	mkdir -p /tmp/sysinfo /etc/vectra-controller-pro
	echo "Vectra Failover Stand (synthetic)" > /tmp/sysinfo/model
	cp "$UI_DIR/operator-config.json" /etc/vectra-controller-pro/xray-desired.json
	if ! vctl apply-local -config "$UI_AGENT" > /tmp/fo-apply.log 2>&1; then
		tail -n 20 /tmp/fo-apply.log
		die "vctl apply-local failed"
	fi
	/etc/init.d/vectra-controller-pro restart
	ui_wait 40 engine_running || { ui_dump; die "xray never came up under the daemon"; }
	ui_wait 40 panel_checked_in || info "no check-in reached the stub panel yet"
	ui_wait 30 node_alive node-a || { ui_dump; die "the observatory never held node-a alive"; }
	ui_wait 30 node_alive node-b || { ui_dump; die "the observatory never held node-b alive"; }
	if [ "$MODE" = failover-no-watchdog ]; then
		grep -q '"noFailoverWatchdog": *true' "$UI_AGENT" || die "UCI failover_watchdog '0' did not reach $UI_AGENT"
		grep -q '"noRussiaDirect": *true' "$UI_AGENT" || die "UCI russia_direct '0' did not reach $UI_AGENT"
	else
		grep -q -e noFailoverWatchdog -e noRussiaDirect "$UI_AGENT" && die "a switch is off in $UI_AGENT"
	fi
	info "daemon up; xray running (pid $(xray_pid)); node-a and node-b alive"
}

# ----------------------------------------------------------------- asserts ---

# `vctl route provider` (a dry run) renders the entry the way the daemon runs
# it and asks xray — the check before a native router is moved.
assert_fo_route_check() {
	_o=$(vctl route provider -config "$UI_AGENT" -entry "$FO_REMARK" 2>&1)
	_rc=$?
	if [ "$_rc" = 0 ] && echo "$_o" | grep -q 'xray -test OK' && echo "$_o" | grep -q 'nothing changed'; then
		record route_provider_check PASS "$(echo "$_o" | grep 'render:' | sed 's/^ *//' | cut -c1-150)"
	else
		record route_provider_check FAIL "rc=$_rc: $(echo "$_o" | tail -n 3 | tr '\n' ' ' | cut -c1-220)"
	fi
}

assert_fo_recovery() {
	_u=$(fo_principle)
	_via=$(client_via)
	if [ "$_u" != node-a ] || [ "$_via" != a-in ]; then
		ui_dump
		die "BL-MAIN should use node-a (node-b's probes are a second late): principle '$_u', a client request went via '$_via'"
	fi
	info "BL-MAIN uses node-a; a client request went through a-in"

	: > "$UI_NODES_LOG"
	fo_drop 2001 || die "could not drop node-a's port"
	FO_T0=$(fo_ms)
	info "node-a's port dropped at $(date +%T)"
	_t1=""
	while [ $(($(fo_ms) - FO_T0)) -lt 30000 ]; do
		_s=$(fo_ms)
		if in_client curl -s --max-time 1 "http://$ORIGIN_IP/hello" 2>/dev/null | grep -q "$EXPECT_BODY"; then
			_t1=$(fo_ms)
			break
		fi
		# Answered at once without the body (no outbound at all): do not spin.
		[ $(($(fo_ms) - _s)) -lt 500 ] && sleep 1
	done
	sleep 1
	_via=$(grep "tcp:$ORIGIN_IP:80 " "$UI_NODES_LOG" 2>/dev/null | grep -o '\[[a-z]-in' | tr -d '[' | sort -u | tr '\n' ' ' | sed 's/ $//')
	_ov=$(xray_override BL-MAIN)
	if [ -z "$_t1" ]; then
		record failover_recovers_in_10s FAIL "no request succeeded in 30 s after node-a stopped answering (override '$_ov', principle '$(fo_principle)')"
		return
	fi
	_dt=$((_t1 - FO_T0))
	if [ "$_dt" -le "$FO_BUDGET_MS" ] && [ "$_via" = b-in ] && { [ "$MODE" = failover-no-watchdog ] || [ "$_ov" = "$FO_TARGET" ]; }; then
		record failover_recovers_in_10s PASS "the first request to succeed after node-a stopped answering: ${_dt} ms, through node-b (override '$_ov')"
	else
		record failover_recovers_in_10s FAIL "the first request to succeed: ${_dt} ms (budget $FO_BUDGET_MS), carried by '${_via:-no node}' (override '$_ov')"
	fi
}

assert_fo_logged() {
	_l=$(logread 2>/dev/null | grep 'node failing; balancer moved' | grep 'node-a' | grep "$FO_TARGET" | tail -n 1)
	if [ -n "$_l" ]; then
		record failover_logged PASS "$(echo "$_l" | sed 's/.*node failing/node failing/' | cut -c1-150)"
	else
		record failover_logged FAIL "no 'node failing; balancer moved' from node-a to $FO_TARGET in logread ($(logread 2>/dev/null | grep -c vctl) vctl lines)"
	fi
}

assert_fo_incident() {
	# Queued after the one request that confirms the move (up to 5 s).
	if ui_wait 15 fo_incident; then
		_f=$(ls "$FO_INBOX"/*-PROXY_FAILOVER-*.json | head -n 1)
		record failover_incident PASS "$(jsonfilter -i "$_f" -e '@.title' 2>/dev/null | cut -c1-110) ($(jsonfilter -i "$_f" -e '@.details.seconds' 2>/dev/null) s)"
	else
		record failover_incident FAIL "no PROXY_FAILOVER in $FO_INBOX: $(ls "$FO_INBOX" 2>/dev/null | tr '\n' ' ')"
	fi
}

assert_fo_released() {
	_before=$(xray_override BL-MAIN)
	fo_undrop
	_via=$(site_via http node-a.stand)
	if [ "$_before" != "$FO_TARGET" ]; then
		record failover_released FAIL "BL-MAIN was not held on $FO_TARGET before node-a came back (override '$_before')"
		return
	fi
	# The watchdog keeps its override 30 s at least (failover.ReleaseHold).
	if ui_wait 45 fo_released; then
		_l=$(logread 2>/dev/null | grep 'balancer back to its own choice' | tail -n 1 | sed 's/.*balancer back/balancer back/' | cut -c1-120)
		record failover_released PASS "node-a answered again (via ${_via:-?}); the watchdog let go: $_l"
	else
		record failover_released FAIL "node-a answered again (via '${_via:-no node}') but BL-MAIN still holds '$(xray_override BL-MAIN)' after 45 s"
	fi
}

run_failover_mode() {
	fo_fixtures
	setup_kernel
	setup_topology
	setup_guard
	start_origins
	ui_isolate
	fo_start_slow_probe
	ui_start_nodes
	ui_start_subscription
	ui_boot_services
	# vctl's lines reach logread through procd and logd. /etc/init.d/log starts
	# nothing without a 'system' section, which a container never got.
	/sbin/logd -S 256 >/dev/null 2>&1 &
	ui_wait 10 logread || die "logd never came up"

	say "install the package; provider mode with an «Авто»-shaped entry over node-a and node-b"
	ui_install_package
	ui_wait 15 ubus list vectra || die "the package installed but rpcd never exposed 'vectra'"
	if [ "$MODE" = failover-no-watchdog ]; then
		# russia_direct rides along: the entry has no Russian bridge, so it
		# changes nothing here but proves the switch reaches agent.json.
		uci set vectra-controller-pro.main.failover_watchdog=0
		uci set vectra-controller-pro.main.russia_direct=0
		uci commit vectra-controller-pro
		info "UCI failover_watchdog '0' (and russia_direct '0'): the control, the router before the watchdog"
	fi
	fo_seed_and_apply
	if [ "$MODE" != failover-no-watchdog ]; then
		say "the migration's render check, against the running entry"
		assert_fo_route_check
	fi

	say "node-a stops answering; the LAN keeps asking"
	assert_fo_recovery
	if [ "$MODE" != failover-no-watchdog ]; then
		assert_fo_logged
		assert_fo_incident
		say "node-a answers again"
		assert_fo_released
	else
		fo_undrop
	fi

	say "data plane integrity"
	assert_tcp
	assert_no_leak

	info "the watchdog's lines:"
	fo_watchdog_lines | sed 's/^/       /' | tail -n 12
	[ "$FAILURES" -gt 0 ] && { say "diagnostics for the failures above"; ui_dump; }
}
