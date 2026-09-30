# shellcheck shell=sh
# The router's own check of its foreign exits (cmd/vctl/exitcheck.go).
#
# 1111, 2026-09-30: through the provider's American exit Instagram, Facebook,
# X, LinkedIn and Discord ended in a failed handshake while Google and the
# observatory's neutral URL answered — and leastLoad kept handing it
# connections. Here an entry spreads BL-MAIN over two foreign exits,
# bridge-de1 (node a, :2001) and bridge-us1 (node c, :2004). Node c's server
# drops every connection to the "blocked site" (the origin's :9444) and
# carries the rest: the neutral URL (:9443), the observatory's probe, the
# LAN's other requests.
#
# Sourced by stand.sh; the services mode's stand (the package installed with
# opkg, procd, rpcd, the daemon running xray, real xray nodes). Proven on the
# packets: the leg itself through the probe's account for each exit; the LAN
# before the check (the blocked site fails through node c only); the check
# leaving bridge-us1 out of BL-MAIN within a round; every blocked request then
# through node a; the card naming it; a vctl restart keeping it out; nobody
# judged while no exit carries the blocked site; bridge-us1 back after two
# clean rounds.

. "$STAND/mode-services.sh"

EC_DIR=/stand/exitcheck
EC_CONTROL_PORT=9443
EC_BLOCKED_PORT=9444
EC_FIRST=60
EC_EVERY=15
EC_STATE=/etc/vectra-controller-pro/state.json

ec_fixtures() {
	UI_DIR=/tmp/exitcheck-fixtures
	rm -rf "$UI_DIR"
	mkdir -p "$UI_DIR"
	cp /stand/ui/panel-ok.sh /stand/ui/operator-config.json "$UI_DIR/"
	cp "$EC_DIR/subscription.json" "$UI_DIR/subscription.json"
	cp "$EC_DIR/nodes-cut-c.json" "$UI_DIR/nodes-server.json"
}

# ec_origins: the neutral URL and the blocked site, TLS with the stand's
# certificate (trusted through the system bundle, ui_start_subscription).
ec_origins() {
	for _p in "$EC_CONTROL_PORT" "$EC_BLOCKED_PORT"; do
		in_inet sh -c "cd $STAND/tls && exec openssl s_server -accept $ORIGIN_IP:$_p \
			-cert cert.pem -key key.pem -WWW -quiet" >/dev/null 2>&1 &
	done
	for _p in "$EC_CONTROL_PORT" "$EC_BLOCKED_PORT"; do
		ui_wait 15 in_inet curl -sf --max-time 3 -o /dev/null "https://$ORIGIN_IP:$_p/hello" \
			|| die "the stand's TLS origin on :$_p never came up"
	done
	info "the neutral URL on :$EC_CONTROL_PORT and the blocked site on :$EC_BLOCKED_PORT"
}

# ec_nodes <cut-c|cut-ac|open>: restart the nodes with that server config.
ec_nodes() {
	cp "$EC_DIR/nodes-$1.json" "$UI_DIR/nodes-server.json"
	pkill -f "xray run -c $UI_DIR/nodes-server.json" 2>/dev/null
	sleep 1
	: > "$UI_NODES_LOG"
	in_inet /usr/bin/xray run -c "$UI_DIR/nodes-server.json" > /tmp/nodes.log 2>&1 &
	ui_wait 20 nodes_listening || { cat /tmp/nodes.log; die "the nodes never listened again ($1)"; }
}

ec_uci() {
	uci set vectra-controller-pro.main.exit_check_control="https://$ORIGIN_IP:$EC_CONTROL_PORT/hello"
	uci -q delete vectra-controller-pro.main.exit_check_site
	uci add_list vectra-controller-pro.main.exit_check_site="https://$ORIGIN_IP:$EC_BLOCKED_PORT/hello"
	uci set vectra-controller-pro.main.exit_check_first="$EC_FIRST"
	uci set vectra-controller-pro.main.exit_check_every="$EC_EVERY"
	uci commit vectra-controller-pro
}

ec_seed_and_apply() {
	mkdir -p /tmp/sysinfo /etc/vectra-controller-pro
	echo "Vectra Exit Check Stand (synthetic)" > /tmp/sysinfo/model
	cp "$UI_DIR/operator-config.json" /etc/vectra-controller-pro/xray-desired.json
	if ! vctl apply-local -config "$UI_AGENT" > /tmp/ec-apply.log 2>&1; then
		tail -n 20 /tmp/ec-apply.log
		die "vctl apply-local failed"
	fi
	/etc/init.d/vectra-controller-pro restart
	EC_STARTED=$(sv_ms)
	ui_wait 40 engine_running || { ui_dump; die "xray never came up under the daemon"; }
	for n in bridge-de1 bridge-us1; do
		ui_wait 30 node_alive "$n" || { ui_dump; die "the observatory never held $n alive"; }
	done
	info "daemon up; xray running (pid $(xray_pid)); both exits alive"
}

# ec_user <tag>: the exit probe's account for an exit (xray.ExitProbeUser).
ec_user() { printf 'vctl-exit-%s' "$(printf '%s' "$1" | sha256sum | cut -c1-12)"; }

# ec_via_probe <tag> <port>: the origin's answer through that exit alone.
ec_via_probe() {
	curl -s --max-time 8 -o /dev/null -w '%{http_code}' -x http://127.0.0.1:10087 \
		--proxy-user "$(ec_user "$1"):vctl" "https://$ORIGIN_IP:$2/hello" 2>/dev/null
}

# ec_lan <n>: n requests from the LAN to the blocked site; prints "ok fail".
ec_lan() {
	_ok=0
	_fail=0
	_i=0
	while [ "$_i" -lt "$1" ]; do
		if in_client curl -sk --max-time 5 -o /dev/null "https://$ORIGIN_IP:$EC_BLOCKED_PORT/hello" 2>/dev/null; then
			_ok=$((_ok + 1))
		else
			_fail=$((_fail + 1))
		fi
		_i=$((_i + 1))
	done
	echo "$_ok $_fail"
}

ec_main() { jsonfilter -i "$UI_RENDER" -e "@.routing.balancers[@.tag='BL-MAIN'].selector[*]" 2>/dev/null | tr '\n' ' ' | sed 's/ $//'; }
ec_left_out() { ! ec_main | grep -qw bridge-us1; }
ec_back_in() { ec_main | grep -qw bridge-us1; }
ec_card() { ubus -t 20 call vectra status 2>/dev/null | jsonfilter -e '@.route.unfit[*].tag' 2>/dev/null | tr '\n' ' ' | sed 's/ $//'; }
ec_rounds() { logread 2>/dev/null | grep -c 'exit check: round'; }

# ----------------------------------------------------------------- asserts ---

assert_ec_leg() {
	_de=$(ec_via_probe bridge-de1 "$EC_BLOCKED_PORT")
	_us=$(ec_via_probe bridge-us1 "$EC_BLOCKED_PORT")
	_usc=$(ec_via_probe bridge-us1 "$EC_CONTROL_PORT")
	if [ "$_de" = 200 ] && [ "$_us" = 000 ] && [ "$_usc" = 200 ]; then
		record ec_leg PASS "through the probe's accounts: the blocked site via bridge-de1 $_de, via bridge-us1 $_us; the neutral URL via bridge-us1 $_usc"
	else
		record ec_leg FAIL "blocked via de1 '$_de' (want 200), via us1 '$_us' (want 000); neutral via us1 '$_usc' (want 200)"
	fi
}

# Before the first round both exits carry the LAN: the blocked site fails on
# node c's share.
assert_ec_lan_before() {
	_r=$(ec_lan 12)
	_waited=$(( ($(sv_ms) - EC_STARTED) / 1000 ))
	if [ "${_r#* }" -gt 0 ] && [ "${_r% *}" -gt 0 ] && [ "$_waited" -lt "$EC_FIRST" ] && [ "$(ec_rounds)" = 0 ]; then
		record ec_lan_before PASS "before any round (${_waited}s in): the blocked site $_r (ok fail) — node c's share fails"
	else
		record ec_lan_before FAIL "the blocked site $_r (ok fail), ${_waited}s after the start, rounds $(ec_rounds); want both non-zero before the first round"
	fi
}

assert_ec_left_out() {
	_p=$(xray_pid)
	if ui_wait $((EC_FIRST + EC_EVERY + 60)) ec_left_out && ui_wait 30 engine_running && ui_wait 30 node_alive bridge-de1; then
		_t=$(( ($(sv_ms) - EC_STARTED) / 1000 ))
		if [ "$(xray_pid)" != "$_p" ] && logread | grep -q 'leaving it out of the balancers.*bridge-us1'; then
			record ec_left_out PASS "BL-MAIN now selects [$(ec_main)] ${_t}s after the start; xray restarted onto it; the log says why"
		else
			record ec_left_out FAIL "BL-MAIN [$(ec_main)], xray pid $_p -> $(xray_pid), log: $(logread | grep 'exit check' | tail -n 2 | tr '\n' ' ')"
		fi
	else
		record ec_left_out FAIL "BL-MAIN still selects [$(ec_main)]; $(logread | grep 'exit check' | tail -n 3 | tr '\n' ' ')"
	fi
}

assert_ec_lan_after() {
	: > "$UI_NODES_LOG"
	_r=$(ec_lan 12)
	sleep 1
	_via=$(grep -F "tcp:$ORIGIN_IP:$EC_BLOCKED_PORT " "$UI_NODES_LOG" 2>/dev/null | grep -o '\[[a-z]-in' | tr -d '[' | sort -u | tr '\n' ' ' | sed 's/ $//')
	if [ "$_r" = "12 0" ] && [ "$_via" = a-in ]; then
		record ec_lan_after PASS "the blocked site 12/12 from the LAN, all through node a"
	else
		record ec_lan_after FAIL "the blocked site $_r (ok fail), through '${_via:-none}' (want 12 0 via a-in)"
	fi
}

assert_ec_card() {
	ui_wait 20 sh -c '[ -n "$(ubus -t 20 call vectra status 2>/dev/null | jsonfilter -e "@.route.unfit[0].tag")" ]' || true
	_c=$(ec_card)
	_cc=$(ubus -t 20 call vectra status 2>/dev/null | jsonfilter -e '@.route.unfit[0].country' 2>/dev/null)
	_st=$(jsonfilter -i "$EC_STATE" -e '@.unfit_exits["bridge-us1"]' 2>/dev/null)
	if [ "$_c" = bridge-us1 ] && [ "$_cc" = US ] && [ -n "$_st" ]; then
		record ec_card PASS "the server card names bridge-us1 (US); state.json keeps it (since $_st)"
	else
		record ec_card FAIL "card unfit '$_c' country '$_cc'; state.json unfit_exits.bridge-us1 '$_st'"
	fi
}

# A restart comes up with the render that already leaves the exit out, before
# any round of its own.
assert_ec_restart_keeps() {
	/etc/init.d/vectra-controller-pro restart
	EC_STARTED=$(sv_ms)
	ui_wait 40 engine_running || { record ec_restart_keeps FAIL "xray never came up after the restart"; return; }
	_t=$(( ($(sv_ms) - EC_STARTED) / 1000 ))
	if ec_left_out && [ "$_t" -lt "$EC_FIRST" ]; then
		record ec_restart_keeps PASS "after a vctl restart BL-MAIN selects [$(ec_main)] from the start (${_t}s in, before any round)"
	else
		record ec_restart_keeps FAIL "after a restart BL-MAIN selects [$(ec_main)] (${_t}s in)"
	fi
}

# Negative control: node a fails the blocked site too. With no exit carrying
# it the sites may be down, or the router's own network: nobody is judged.
assert_ec_no_witness() {
	ec_nodes cut-ac
	ui_wait 30 node_alive bridge-de1 || true
	_r0=$(ec_rounds)
	ui_wait $((EC_FIRST + 3 * EC_EVERY + 30)) sh -c "[ \$(logread | grep -c 'exit check: round') -ge $((_r0 + 2)) ]" || true
	_r1=$(ec_rounds)
	if [ "$_r1" -ge $((_r0 + 2)) ] && ec_main | grep -qw bridge-de1 && [ "$(ec_card)" = bridge-us1 ]; then
		record ec_no_witness PASS "$((_r1 - _r0)) rounds with no exit carrying the blocked site: bridge-de1 kept, only bridge-us1 out"
	else
		record ec_no_witness FAIL "rounds $_r0 -> $_r1; BL-MAIN [$(ec_main)]; card '$(ec_card)'"
	fi
}

assert_ec_back() {
	ec_nodes open
	ui_wait 30 node_alive bridge-de1 || true
	_t0=$(sv_ms)
	if ui_wait $((3 * EC_EVERY + 60)) ec_back_in && ui_wait 30 engine_running; then
		_t=$(( ($(sv_ms) - _t0) / 1000 ))
		ui_wait 20 sh -c '[ -z "$(ubus -t 20 call vectra status 2>/dev/null | jsonfilter -e "@.route.unfit[0].tag")" ]' || true
		_st=$(jsonfilter -i "$EC_STATE" -e '@.unfit_exits' 2>/dev/null)
		if [ -z "$(ec_card)" ] && [ -z "$_st" ] && logread | grep -q 'carries blocked sites again.*bridge-us1'; then
			record ec_back PASS "node c carries the blocked site again: bridge-us1 back in BL-MAIN [$(ec_main)] after ${_t}s; the card and state.json clear"
		else
			record ec_back FAIL "back in BL-MAIN, but card '$(ec_card)', state '$_st'"
		fi
	else
		record ec_back FAIL "bridge-us1 not back: BL-MAIN [$(ec_main)]; $(logread | grep 'exit check' | tail -n 3 | tr '\n' ' ')"
	fi
}

run_exitcheck_mode() {
	ec_fixtures
	setup_kernel
	setup_topology
	setup_guard
	start_origins
	ui_isolate
	ui_start_nodes
	ui_start_subscription
	ec_origins
	ui_boot_services
	/sbin/logd -S 256 >/dev/null 2>&1 &
	ui_wait 10 logread || die "logd never came up"

	say "install the package; BL-MAIN over a German and an American exit"
	ui_install_package
	ui_wait 15 ubus list vectra || die "the package installed but rpcd never exposed 'vectra'"
	ec_uci
	ec_seed_and_apply

	say "the leg as it is, and the LAN before the check"
	assert_ec_leg
	assert_ec_lan_before

	say "the check leaves the American exit out"
	assert_ec_left_out
	assert_ec_lan_after
	assert_ec_card

	say "a restart keeps it out"
	assert_ec_restart_keeps

	say "negative control: no exit carries the blocked site"
	assert_ec_no_witness

	say "the American exit carries it again"
	assert_ec_back

	say "data plane integrity"
	assert_tcp
	assert_no_leak
	[ "$FAILURES" -gt 0 ] && { say "diagnostics for the failures above"; ui_dump; logread | grep 'exit check' | tail -n 20; }
}
