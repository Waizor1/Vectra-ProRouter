# shellcheck shell=sh
# Gap 6 — the router UI, end to end.
#
# Sourced by stand.sh. Everything the UI touches runs for real: the package is
# installed with opkg (its postinst must make rpcd pick the plugin up), rpcd
# and uhttpd run from their own init scripts under procd, LuCI serves the page,
# the daemon runs xray, and xray's balancers pick between real nodes that
# carry real packets.
#
#   MODE=ui       every assertion must PASS
#   MODE=ui-hold  the same, then the container stays up for a browser:
#                 run.sh publishes LuCI on 127.0.0.1:${STAND_UI_PORT:-8080}
#                 (login root, empty password — it is a throwaway container)
#
# Topology: stand.sh's (client ns / router / inet ns), plus in the inet ns
#   - an xray acting as three "provider nodes" (vless, no TLS) on 10.44.0.2:
#     :2001 (node-a), :2002 (node-b), :2004 (node-c). :2003 (node-dead) has
#     nothing listening. Its access log names the inbound each client
#     connection arrived on — which node carried it;
#   - the subscription: https://44.44.44.44:8443 (openssl s_server -HTTP with
#     the stand's cert, trusted through the system bundle), two locations. The
#     router's connections to it are counted in nft — so "switching location
#     does not ask the provider again" is a number. (OpenWrt's socat is built
#     without OpenSSL, hence s_server.) Its routing sends the name
#     direct.stand DIRECT, which "My sites" turns around.
#
# The daemon's panel is a dead port and the container's default route is
# removed: nothing here can reach production.

UI_DIR=/stand/ui
UI_PKG=/tmp/ui-pkg
UI_IPK=/tmp/vectra-controller-pro-ui.ipk
UI_WORK=/var/run/vectra-controller-pro
UI_AGENT="$UI_WORK/agent.json"
UI_RENDER="$UI_WORK/xray.json"
UI_OVERRIDES=/etc/vectra-controller-pro/local-overrides.json
UI_NODES_LOG=/tmp/nodes-access.log
UI_NULL_SID=00000000000000000000000000000000
UI_PANEL_IP=44.44.44.81
UI_PANEL_PORT=8080
# Observatory probes go to their own port, so the node access log can tell a
# probe from a client connection (the client talks to :80).
UI_PROBE_PORT=8081

ui_wait() { # <seconds> <command...>: poll once a second until it succeeds
	_t=$1; shift
	_i=0
	while [ "$_i" -lt "$_t" ]; do
		"$@" >/dev/null 2>&1 && return 0
		_i=$((_i + 1)); sleep 1
	done
	return 1
}

# Conditions for ui_wait (functions run in this shell; no nested quoting).
engine_running() { [ "$(ubus -t 20 call vectra status 2>/dev/null | jsonfilter -e '@.engine.state')" = running ]; }
node_probed() { [ -n "$(ubus -t 20 call vectra nodes 2>/dev/null | jsonfilter -e "@.nodes[@.tag='$1'].alive")" ]; }
node_alive() { [ "$(ubus -t 20 call vectra nodes 2>/dev/null | jsonfilter -e "@.nodes[@.tag='$1'].alive")" = true ]; }
xray_pid_changed() { _p=$(xray_pid); [ -n "$_p" ] && [ "$_p" != "$1" ]; }
override_is() { [ "$(xray_override "$1")" = "$2" ]; }
render_has() { grep -q "$1" "$UI_RENDER"; }
status_is() { [ "$(ubus -t 20 call vectra status 2>/dev/null | jsonfilter -e "$1")" = "$2" ]; }
vectra_gone() { ! ubus list vectra >/dev/null 2>&1; }
nodes_listening() { ip netns exec "$INET_NS" netstat -lnt 2>/dev/null | grep -q '10.44.0.2:2004 '; }
panel_checked_in() { grep -q 'check-in' /tmp/ui-panel.log 2>/dev/null; }

vcall() { # <method> [params-json]
	if [ $# -ge 2 ]; then
		ubus -t 40 call vectra "$1" "$2" 2>&1
	else
		ubus -t 40 call vectra "$1" 2>&1
	fi
}

jf() { # <json> <expr>
	jsonfilter -s "$1" -e "$2" 2>/dev/null
}

xray_override() { # <balancer> -> the override xray itself reports
	vctl api balancers "$1" 2>/dev/null | jsonfilter -e "@['$1'].override" 2>/dev/null
}

# xray runs under vctl-xray-wrapper, which keeps its own argv[0]; the daemon
# knows the pid for certain.
xray_pid() { ubus -t 20 call vectra status 2>/dev/null | jsonfilter -e '@.engine.pid' 2>/dev/null; }

# New TCP connections from the router to the subscription server.
sub_requests() { nft list counter inet uistand subfetch 2>/dev/null | awk '/packets/{print $2; exit}'; }

# client_via: one client request through the proxy, answering which node
# inbound carried it ("a-in", "b-in", "c-in") or "none".
client_via() {
	: > "$UI_NODES_LOG"
	in_client curl -sS --max-time 12 -o /dev/null "http://$ORIGIN_IP/hello" 2>/dev/null
	sleep 1
	grep "tcp:$ORIGIN_IP:80 " "$UI_NODES_LOG" 2>/dev/null | grep -o '\[[a-z]-in' | tr -d '[' | sort -u | tr '\n' ' ' | sed 's/ $//' || true
}

# My sites. Two names for the one origin address: the stand's provider routes
# direct.stand DIRECT (ui/subscription.json) and has no word for origin.stand
# (its catch-all sends it to BL-MAIN). curl --resolve connects to the origin's
# address — there is no DNS here — and names the site in the Host header and
# the SNI, which is what xray sniffs; so the only difference between two
# requests is which way the router sends them.
UI_BODY=/tmp/ui-site-body

# site_via <http|https> <name>: like client_via, for a request that names the
# site; prints the node inbound(s) that carried it, nothing when no node did.
# The body lands in $UI_BODY (site_ok).
site_via() {
	_port=80
	[ "$1" = https ] && _port=443
	: > "$UI_NODES_LOG"
	rm -f "$UI_BODY"
	in_client curl -sSk --max-time 12 -o "$UI_BODY" --resolve "$2:$_port:$ORIGIN_IP" "$1://$2/hello" 2>/dev/null
	sleep 1
	grep -F -e "tcp:$ORIGIN_IP:$_port " -e "tcp:$2:$_port " "$UI_NODES_LOG" 2>/dev/null \
		| grep -o '\[[a-z]-in' | tr -d '[' | sort -u | tr '\n' ' ' | sed 's/ $//' || true
}
site_ok() { grep -q "$EXPECT_BODY" "$UI_BODY" 2>/dev/null; }

# Bytes xray's own counter says the provider's freedom outbound received.
direct_down() {
	_v=$(curl -s --max-time 4 http://127.0.0.1:10086/debug/vars 2>/dev/null | jsonfilter -e '@.stats.outbound.DIRECT.downlink' 2>/dev/null)
	echo "${_v:-0}"
}

# rules_are <direct,csv> <proxy,csv>: what the router keeps — and it keeps a
# change only once it runs.
rules_are() {
	_rr=$(ubus -t 20 call vectra rules 2>/dev/null)
	[ "$(jsonfilter -s "$_rr" -e '@.direct[*]' 2>/dev/null | tr '\n' ',' | sed 's/,$//')" = "$1" ] \
		&& [ "$(jsonfilter -s "$_rr" -e '@.proxy[*]' 2>/dev/null | tr '\n' ',' | sed 's/,$//')" = "$2" ]
}
node_a_or_b_alive() { node_alive node-a || node_alive node-b; }
render_route_only() { jsonfilter -i "$UI_RENDER" -e '@.inbounds[0].sniffing.routeOnly' 2>/dev/null; }

# ui_set_rules <params> <direct,csv> <proxy,csv>: set_rules, then wait until
# the router runs it — kept, a new xray, BL-MAIN's nodes probed again. Prints
# the answer's code; fails with the reason.
ui_set_rules() {
	_sp=$(xray_pid)
	_sc=$(jf "$(vcall set_rules "$1")" '@.code')
	case "$_sc" in
	rules_set | pending) ;;
	*) echo "answered '$_sc'"; return 1 ;;
	esac
	ui_wait 40 rules_are "$2" "$3" || { echo "$_sc, but the router keeps: $(vcall rules | tr -d ' \n' | cut -c1-120)"; return 1; }
	ui_wait 30 xray_pid_changed "$_sp" || { echo "$_sc, but xray was not restarted"; return 1; }
	ui_wait 30 engine_running
	ui_wait 20 node_a_or_b_alive
	echo "$_sc"
}

# ------------------------------------------------------------------- setup ---

ui_isolate() {
	ip route del default 2>/dev/null || true
	ip -6 route del default 2>/dev/null || true
	ip -n "$INET_NS" addr add "$UI_PANEL_IP/32" dev lo
	ip route add "$UI_PANEL_IP/32" via "$INET_PEER_IP" dev vinet
	: > /tmp/ui-panel.log
	in_inet socat "TCP-LISTEN:18080,bind=127.0.0.1,fork,reuseaddr" "SYSTEM:$UI_DIR/panel-ok.sh" >/dev/null 2>&1 &
	in_inet socat "TCP-LISTEN:$UI_PROBE_PORT,bind=$ORIGIN_IP,fork,reuseaddr" "SYSTEM:$STAND/http-reply.sh" >/dev/null 2>&1 &
	in_inet /usr/bin/stand-tlsproxy -listen "$UI_PANEL_IP:$UI_PANEL_PORT" \
		-upstream http://127.0.0.1:18080 >/tmp/stand-panel-tls.log 2>&1 &
	uci set vectra-controller-pro.main.control_url="https://$UI_PANEL_IP:$UI_PANEL_PORT"
	uci set vectra-controller-pro.main.panel_url="https://$UI_PANEL_IP:$UI_PANEL_PORT"
	uci set vectra-controller-pro.main.poll_interval=10
	uci commit vectra-controller-pro
	info "default route removed; the panel is a stub at $UI_PANEL_IP:$UI_PANEL_PORT that accepts check-ins"
}

ui_start_nodes() {
	: > "$UI_NODES_LOG"
	in_inet /usr/bin/xray run -c "$UI_DIR/nodes-server.json" > /tmp/nodes.log 2>&1 &
	ui_wait 20 nodes_listening || { cat /tmp/nodes.log; die "the stand's nodes never listened"; }
	info "nodes up in the inet ns: node-a :2001, node-b :2002, node-c :2004 (node-dead :2003 closed)"
}

ui_start_subscription() {
	cat /stand/tls/cert.pem >> /etc/ssl/certs/ca-certificates.crt
	# -HTTP serves ./<path> as a COMPLETE response, headers included, so the
	# JSON variant goes out as application/json like the provider's.
	mkdir -p /tmp/subsrv/api/sub
	_len=$(wc -c < "$UI_DIR/subscription.json" | tr -d ' ')
	{ printf 'HTTP/1.0 200 OK\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: %s\r\n\r\n' "$_len"
	  cat "$UI_DIR/subscription.json"; } > /tmp/subsrv/api/sub/stand
	printf 'HTTP/1.0 200 OK\r\nContent-Length: 2\r\n\r\nok' > /tmp/subsrv/ready
	in_inet sh -c "cd /tmp/subsrv && exec openssl s_server -accept $ORIGIN_IP:8443 \
		-cert /stand/tls/cert.pem -key /stand/tls/key.pem -HTTP -quiet" >/dev/null 2>&1 &
	# Count the router's connections to it (SYNs leaving the router).
	nft -f - <<EOF || die "uistand table"
table inet uistand {
  counter subfetch { }
  chain out {
    type filter hook output priority filter; policy accept;
    ip daddr $ORIGIN_IP tcp dport 8443 tcp flags & (syn|ack) == syn counter name "subfetch"
  }
}
EOF
	ui_wait 20 curl -sf --max-time 3 -o /dev/null "https://$ORIGIN_IP:8443/ready" \
		|| die "the stand's subscription server never came up (or its cert is not trusted)"
	nft reset counter inet uistand subfetch >/dev/null
	info "subscription up at https://$ORIGIN_IP:8443 (cert trusted; connections counted)"
}

ui_boot_services() {
	mkdir -p /var/lock /var/run /var/state /tmp/lock
	ubusd &
	sleep 1
	procd &
	ui_wait 15 sh -c 'ubus list | grep -q "^service$"' || die "procd/ubusd never came up"
	# A container never boots, so first-boot uci-defaults never ran. These two
	# are what a real router's first boot does for LuCI: /ubus on uhttpd, and
	# rpcd's socket path.
	for f in /etc/uci-defaults/00_uhttpd_ubus /etc/uci-defaults/50-migrate-rpcd-ubus-sock.sh; do
		[ -f "$f" ] && ( . "$f" ) >/dev/null 2>&1
	done
	/etc/init.d/rpcd start
	ui_wait 15 sh -c 'ubus list | grep -q "^session$"' || die "rpcd never came up"
	/etc/init.d/uhttpd start
	ui_wait 15 curl -s -o /dev/null http://127.0.0.1/ || die "uhttpd never came up"
	info "procd, ubusd, rpcd and uhttpd up from their own init scripts"
}

ui_install_package() {
	rm -rf "$UI_PKG"
	mkdir -p "$UI_PKG"
	cp -R /stand/pkg/. "$UI_PKG/"
	/stand/build-ipk.sh "$UI_PKG" /stand/pkg-control "$UI_IPK"
	opkg install --force-reinstall "$UI_IPK" 2>&1 | sed 's/^/       opkg: /'
}

# ----------------------------------------------------------------- asserts ---

assert_ui_rpcd_registration() {
	# Anti-vacuity: before the install nothing may answer as "vectra".
	if ubus list vectra >/dev/null 2>&1; then
		record ui_rpcd_registered FAIL "a 'vectra' ubus object existed BEFORE the package was installed"
		return
	fi
	ui_install_package
	if ui_wait 15 ubus list vectra; then
		record ui_rpcd_registered PASS "opkg install -> postinst HUP'd rpcd -> ubus object 'vectra' ($(ubus -v list vectra 2>/dev/null | grep -c '"'))"
	else
		record ui_rpcd_registered FAIL "the package installed but rpcd never exposed 'vectra' (postinst reload missing?)"
	fi
}

ui_seed_and_apply() {
	mkdir -p /tmp/sysinfo /etc/vectra-controller-pro
	echo "Vectra UI Stand (synthetic)" > /tmp/sysinfo/model
	cp "$UI_DIR/operator-config.json" /etc/vectra-controller-pro/xray-desired.json
	vctl apply-local -config "$UI_AGENT" > /tmp/ui-apply.log 2>&1
	_rc=$?
	sed 's/^/       /' /tmp/ui-apply.log | tail -n 8
	_reqs=$(sub_requests)
	if [ "$_rc" = 0 ] && [ "$_reqs" = 1 ] && [ -s "$UI_RENDER" ] && [ -s /etc/vectra-controller-pro/provider-entries.json.gz ]; then
		record ui_fetch_and_cache PASS "one fetch, both locations cached, render written"
	else
		record ui_fetch_and_cache FAIL "apply-local rc=$_rc, $_reqs fetch(es), render=$(wc -c < "$UI_RENDER" 2>/dev/null || echo none)B"
	fi
	/etc/init.d/vectra-controller-pro restart
	if ui_wait 40 engine_running; then
		info "daemon up; xray running (pid $(xray_pid))"
	else
		ubus call vectra status 2>&1 | head -30
		logread 2>/dev/null | tail -n 30
		die "xray never came up under the daemon"
	fi
	# The deadman: the ruleset stays only if a check-in confirms it.
	ui_wait 40 panel_checked_in || info "no check-in reached the stub panel yet"
	# burstObservatory probes every node once at start; give it the timeout.
	ui_wait 20 node_alive node-a || true
	ui_wait 20 node_probed node-dead || true
}

# On any failure, the state that explains it.
ui_dump() {
	echo "--- nft tables / vctl table ---"; nft list tables 2>&1
	nft list table inet vctl 2>&1 | head -40
	echo "--- ip rule ---"; ip rule list 2>&1
	echo "--- logread (vctl) ---"; logread 2>/dev/null | grep -E 'vctl|vectra' | tail -n 40
	echo "--- xray.log ---"; tail -n 20 /var/log/vectra-controller-pro/xray.log 2>/dev/null
	echo "--- nodes answer ---"; ubus -t 20 call vectra nodes 2>&1 | head -60
	echo "--- panel stub hits ---"; tail -n 5 /tmp/ui-panel.log 2>/dev/null
}

assert_ui_status() {
	_s=$(vcall status)
	_state=$(jf "$_s" '@.engine.state')
	_api=$(jf "$_s" '@.api.reachable')
	_met=$(jf "$_s" '@.metrics.reachable')
	_ctl=$(jf "$_s" '@.controller.running')
	_ver=$(jf "$_s" '@.version')
	_loaded=$(jf "$_s" '@.dataplane.loaded')
	# xray's soft memory limit is the WRAPPER's (80 MiB), not the controller's
	# own 32 MiB leaking into the child's environment.
	_lim=$(jf "$_s" '@.engine.memoryLimitMiB')
	if [ "$_state" = running ] && [ "$_api" = true ] && [ "$_met" = true ] && [ "$_ctl" = true ] && [ -n "$_ver" ] && [ "$_loaded" = true ] && [ "$_lim" = 80 ]; then
		record ui_status PASS "engine running (GOMEMLIMIT ${_lim} MiB), xray api + metrics reachable, data plane loaded, controller $_ver"
	else
		record ui_status FAIL "state=$_state api=$_api metrics=$_met controller=$_ctl version='$_ver' dataplane=$_loaded memoryLimitMiB=$_lim (want 80)"
	fi
}

assert_ui_probe_override() {
	_b=$(vcall balancers)
	_iv=$(jf "$_b" '@.probe.intervalSec')
	_pv=$(jf "$_b" '@.probe.providerIntervalSec')
	_src=$(jf "$_b" '@.probe.source')
	if [ "$_iv" = 600 ] && [ "$_pv" = 43200 ] && [ "$_src" = default ] && grep -q '"interval":"600s"' "$UI_RENDER"; then
		record ui_probe_default PASS "the provider's 43200s became the router's 600s, in the running config"
	else
		record ui_probe_default FAIL "interval=$_iv provider=$_pv source=$_src; render has: $(grep -o '"interval":"[^"]*"' "$UI_RENDER")"
	fi
}

assert_ui_balancer_view() {
	_b=$(vcall balancers)
	_role=$(jf "$_b" '@.balancers[@.tag="BL-MAIN"].role')
	_fb=$(jf "$_b" '@.balancers[@.tag="BL-MAIN"].fallback.balancer')
	_rrole=$(jf "$_b" '@.balancers[@.tag="BL-BACKUP"].role')
	_sel=$(jf "$_b" '@.balancers[@.tag="BL-MAIN"].selected[*]')
	if [ "$_role" = main ] && [ "$_fb" = BL-BACKUP ] && [ "$_rrole" = reserve ] && [ -n "$_sel" ]; then
		record ui_balancers PASS "BL-MAIN (main) -> fallback BL-BACKUP (reserve); xray selects: $_sel"
	else
		record ui_balancers FAIL "BL-MAIN role=$_role fallback=$_fb selected='$_sel'; BL-BACKUP role=$_rrole"
	fi
}

assert_ui_observatory() {
	_n=$(vcall nodes)
	_a=$(jf "$_n" '@.nodes[@.tag="node-a"].alive')
	_ad=$(jf "$_n" '@.nodes[@.tag="node-a"].delayMs')
	_dead=$(jf "$_n" '@.nodes[@.tag="node-dead"].alive')
	if [ "$_a" = true ] && [ -n "$_ad" ] && [ "$_dead" = false ]; then
		record ui_observatory PASS "node-a alive (${_ad} ms), node-dead reported dead — xray's own probes, read through metrics"
	else
		record ui_observatory FAIL "node-a alive=$_a delay=$_ad; node-dead alive=$_dead"
	fi
}

# xray's API and metrics listen on loopback with no authentication. Two ways
# at them that are not "root on the router", both refused by the vctl table's
# local_guard chain:
#   - a LAN client: sniffing makes "Host: 127.0.0.1:10086" the destination, the
#     provider routes geoip:private DIRECT (as the real one does), and xray's
#     freedom outbound dials the router's own 127.0.0.1:10086;
#   - a non-root process on the router (dnsmasq has its own uid).
# Anti-vacuity: root still reads the metrics (the UI's own path).
local_guard_ctr() { nft list counter inet vctl vctl_local_guard 2>/dev/null | awk '/packets/{print $2; exit}'; }
assert_ui_local_guard() {
	_g0=$(local_guard_ctr)
	_lan=$(in_client curl -s --max-time 8 -H 'Host: 127.0.0.1:10086' "http://$ORIGIN_IP:10086/debug/vars" 2>/dev/null | head -c 200)
	_nobody=$(start-stop-daemon -S -c nobody -x /usr/bin/curl -- -s --max-time 4 http://127.0.0.1:10086/debug/vars 2>/dev/null | head -c 200)
	_root=$(curl -s --max-time 4 http://127.0.0.1:10086/debug/vars 2>/dev/null | head -c 200)
	_g1=$(local_guard_ctr)
	if ! echo "$_lan" | grep -q memstats && ! echo "$_nobody" | grep -q memstats && echo "$_root" | grep -q memstats \
		&& [ "${_g1:-0}" -gt "${_g0:-0}" ]; then
		record ui_local_guard PASS "LAN client via sniffing + DIRECT refused, non-root refused, root reads metrics; guard counter ${_g0:-0} -> ${_g1:-0}"
	else
		record ui_local_guard FAIL "lan got '$(echo "$_lan" | cut -c1-40)' nobody got '$(echo "$_nobody" | cut -c1-40)' root got '$(echo "$_root" | cut -c1-20)' guard ${_g0:-?} -> ${_g1:-?}"
	fi
}

assert_ui_pin_steers_traffic() {
	_before=$(xray_override BL-MAIN)
	_r=$(vcall pin_balancer '{"balancer":"BL-MAIN","node":"node-b"}')
	_code=$(jf "$_r" '@.code')
	_after=$(xray_override BL-MAIN)
	_via=$(client_via)
	if [ -z "$_before" ] && [ "$_code" = balancer_pinned ] && [ "$_after" = node-b ] && [ "$_via" = b-in ]; then
		record ui_pin_live PASS "pin BL-MAIN -> node-b: xray override '' -> node-b, and the next client connection arrived on node-b"
	else
		record ui_pin_live FAIL "override before='$_before' after='$_after', answer=$_code, traffic via '$_via'"
	fi
	# ...and to the other node, so the answer above is not node-b by accident.
	vcall pin_balancer '{"balancer":"BL-MAIN","node":"node-a"}' >/dev/null
	_via2=$(client_via)
	if [ "$(xray_override BL-MAIN)" = node-a ] && [ "$_via2" = a-in ]; then
		record ui_pin_steers PASS "re-pinned to node-a: traffic followed ($_via2)"
	else
		record ui_pin_steers FAIL "re-pin to node-a: override '$(xray_override BL-MAIN)', traffic via '$_via2'"
	fi
}

assert_ui_pin_refuses() {
	_r1=$(jf "$(vcall pin_balancer '{"balancer":"BL-BACKUP","node":"node-a"}')" '@.code')
	_r2=$(jf "$(vcall pin_balancer '{"balancer":"BL-MAIN","node":"DIRECT"}')" '@.code')
	_r3=$(jf "$(vcall pin_balancer '{"balancer":"BL-NOPE","node":"node-a"}')" '@.code')
	_ov=$(xray_override BL-BACKUP)
	if [ "$_r1" = unknown_node ] && [ "$_r2" = unknown_node ] && [ "$_r3" = unknown_balancer ] && [ -z "$_ov" ]; then
		record ui_pin_refuses PASS "another balancer's node, a non-node and a missing balancer all refused; xray untouched"
	else
		record ui_pin_refuses FAIL "codes: $_r1 / $_r2 / $_r3; BL-BACKUP override '$_ov'"
	fi
}

assert_ui_pin_survives_restart() {
	_pid0=$(xray_pid)
	_r=$(jf "$(vcall restart_xray)" '@.code')
	ui_wait 30 xray_pid_changed "$_pid0"
	_pid1=$(xray_pid)
	ui_wait 30 override_is BL-MAIN node-a
	_ov=$(xray_override BL-MAIN)
	_via=$(client_via)
	if [ "$_r" = xray_restarted ] && [ -n "$_pid1" ] && [ "$_pid1" != "$_pid0" ] && [ "$_ov" = node-a ] && [ "$_via" = a-in ]; then
		record ui_pin_survives_restart PASS "xray pid $_pid0 -> $_pid1 (a fresh xray has no overrides); the pin came back and still steers ($_via)"
	else
		record ui_pin_survives_restart FAIL "restart=$_r pid $_pid0 -> ${_pid1:-none}, override after '$_ov', traffic via '$_via'"
	fi
}

assert_ui_unpin() {
	_r=$(jf "$(vcall unpin_balancer '{"balancer":"BL-MAIN"}')" '@.code')
	_ov=$(xray_override BL-MAIN)
	if [ "$_r" = balancer_unpinned ] && [ -z "$_ov" ] && ! grep -q 'BL-MAIN' "$UI_OVERRIDES"; then
		record ui_unpin PASS "released live and on disk"
	else
		record ui_unpin FAIL "answer=$_r override='$_ov' overrides: $(tr -d '\n' < "$UI_OVERRIDES" | cut -c1-120)"
	fi
}

assert_ui_locations() {
	_e=$(vcall entries)
	_n=$(jf "$_e" '@.entries[*].index' | wc -l | tr -d ' ')
	_act=$(jf "$_e" '@.active')
	if [ "$_n" = 2 ] && [ "$_act" = 0 ] && [ "$(jf "$_e" '@.cached')" = true ]; then
		record ui_entries PASS "2 locations from the cache, running #0"
	else
		record ui_entries FAIL "entries=$_n active=$_act"
	fi

	_reqs0=$(sub_requests)
	_r=$(vcall select_entry '{"index":1}')
	_code=$(jf "$_r" '@.code')
	[ "$_code" = pending ] && ui_wait 30 status_is '@.subscription.entryIndex' 1
	ui_wait 20 render_has '"node-c"'
	ui_wait 20 node_alive node-c # the fresh xray probes node-c once, at start
	_st=$(vcall status)
	_via=$(client_via)
	_reqs1=$(sub_requests)
	if { [ "$_code" = entry_selected ] || [ "$_code" = pending ]; } \
		&& [ "$(jf "$_st" '@.subscription.entryIndex')" = 1 ] && [ "$(jf "$_st" '@.subscription.source')" = local ] \
		&& [ "$_reqs1" = "$_reqs0" ] && [ "$_via" = c-in ]; then
		record ui_select_entry PASS "switched to #1 from the cache: 0 new fetches, running entry 1 (local), traffic now via node-c"
	else
		record ui_select_entry FAIL "answer=$_code entry=$(jf "$_st" '@.subscription.entryIndex') source=$(jf "$_st" '@.subscription.source') fetches $_reqs0->$_reqs1 traffic via '$_via'"
	fi

	_r=$(jf "$(vcall reset_entry)" '@.code')
	ui_wait 20 render_has '"node-a"'
	[ "$_r" = pending ] && ui_wait 30 status_is '@.subscription.entryIndex' 0
	_st=$(vcall status)
	if { [ "$_r" = entry_reset ] || [ "$_r" = pending ]; } && [ "$(jf "$_st" '@.subscription.entryIndex')" = 0 ] && [ "$(jf "$_st" '@.subscription.source')" = panel ]; then
		record ui_reset_entry PASS "back to the panel's location (#0)"
	else
		record ui_reset_entry FAIL "answer=$_r entry=$(jf "$_st" '@.subscription.entryIndex') source=$(jf "$_st" '@.subscription.source')"
	fi
}

assert_ui_probe_interval() {
	_r=$(jf "$(vcall set_probe_interval '{"seconds":120}')" '@.code')
	ui_wait 20 render_has '"interval":"120s"'
	_b=$(vcall balancers)
	_ok1=no
	[ "$_r" = probe_interval_set ] && [ "$(jf "$_b" '@.probe.intervalSec')" = 120 ] && [ "$(jf "$_b" '@.probe.source')" = local ] && _ok1=yes
	_before=$(sha256sum "$UI_RENDER" | awk '{print $1}')
	_bad=$(jf "$(vcall set_probe_interval '{"seconds":5}')" '@.code')
	_after=$(sha256sum "$UI_RENDER" | awk '{print $1}')
	_r0=$(jf "$(vcall set_probe_interval '{"seconds":0}')" '@.code')
	ui_wait 20 render_has '"interval":"600s"'
	if [ "$_ok1" = yes ] && [ "$_bad" = invalid_params ] && [ "$_before" = "$_after" ] && [ "$_r0" = probe_interval_set ] && grep -q '"interval":"600s"' "$UI_RENDER"; then
		record ui_probe_interval PASS "120 s applied (source local); 5 s refused, render untouched; 0 restored the 600 s default"
	else
		record ui_probe_interval FAIL "120s:$_ok1/$_r bad:$_bad render-changed:$([ "$_before" = "$_after" ] && echo no || echo yes) reset:$_r0"
	fi
}

assert_ui_diagnostics_and_logs() {
	# The last change restarted xray; node-dead's verdict comes with its
	# first probe.
	ui_wait 20 node_probed node-dead
	_d=$(vcall diagnostics)
	_x=$(jf "$_d" '@.checks[@.id="xray_running"].status')
	_dead=$(jf "$_d" '@.checks[@.id="dead_nodes"].status')
	_l=$(vcall logs '{"lines":50}')
	_xl=$(jf "$_l" '@.lines[@.source="xray"].message' | wc -l | tr -d ' ')
	if [ "$_x" = ok ] && [ "$_dead" = warn ] && [ "$_xl" -gt 0 ]; then
		record ui_diagnostics PASS "xray_running ok, dead_nodes warn (node-dead); logs carry $_xl xray line(s)"
	else
		record ui_diagnostics FAIL "xray_running=$_x dead_nodes=$_dead xray log lines=$_xl"
	fi
}

# My sites (set_rules / rules), proven on the packets like the pins: the
# nodes' access log names the node a connection arrived on, and xray's own
# counter says what its DIRECT outbound carried. Every change restarts xray;
# ui_set_rules waits for the new one and its first probes.

# (1) A direct site reaches its origin with no node in the path.
assert_ui_rules_direct() {
	# Anti-vacuity, under the same sniffing: with a site set, but another one,
	# origin.stand goes through BL-MAIN's node.
	_r1=$(ui_set_rules '{"direct":["unrelated.stand"],"proxy":[]}' unrelated.stand "") || _r1="FAILED: $_r1"
	_via0=$(site_via http origin.stand)
	site_ok && _ok0=yes || _ok0=no
	# While sites are set the router sniffs with routeOnly, whatever the
	# operator's config says (this stand's says false).
	_ro=$(render_route_only)
	# The origin itself, typed as a person pastes it.
	_r2=$(ui_set_rules '{"direct":["HTTPS://WWW.Origin.Stand:443/hello?x=1"],"proxy":[]}' origin.stand "") || _r2="FAILED: $_r2"
	_d0=$(direct_down)
	_via1=$(site_via http origin.stand)
	site_ok && _ok1=yes || _ok1=no
	_via2=$(site_via https origin.stand)
	site_ok && _ok2=yes || _ok2=no
	_d1=$(direct_down)
	if [ -n "$_via0" ] && [ "$_ok0" = yes ] && [ "$_ro" = true ] && [ "${_r2#FAILED}" = "$_r2" ] \
		&& [ -z "$_via1" ] && [ "$_ok1" = yes ] && [ -z "$_via2" ] && [ "$_ok2" = yes ] && [ "$_d1" -gt "$_d0" ]; then
		record ui_rules_direct PASS "origin.stand via $_via0 until set direct (pasted as a URL, kept as origin.stand); then HTTP and HTTPS answered with no node in the path, xray's DIRECT carried ${_d0} -> ${_d1} B; routeOnly on while sites are set"
	else
		record ui_rules_direct FAIL "before ($_r1): via '$_via0' ok=$_ok0; routeOnly=$_ro; after ($_r2): http via '$_via1' ok=$_ok1, https via '$_via2' ok=$_ok2, DIRECT ${_d0} -> ${_d1} B"
	fi
}

# (2) A proxy site goes through a node although the provider sends it direct.
assert_ui_rules_proxy() {
	# Anti-vacuity: direct.stand is the provider's own DIRECT rule.
	_d0=$(direct_down)
	_via0=$(site_via http direct.stand)
	site_ok && _ok0=yes || _ok0=no
	_d1=$(direct_down)
	_r=$(ui_set_rules '{"direct":["origin.stand"],"proxy":["direct.stand"]}' origin.stand direct.stand) || _r="FAILED: $_r"
	_via1=$(site_via http direct.stand)
	site_ok && _ok1=yes || _ok1=no
	_via2=$(site_via https direct.stand)
	site_ok && _ok2=yes || _ok2=no
	# The direct site beside it still goes direct.
	_via3=$(site_via http origin.stand)
	if [ -z "$_via0" ] && [ "$_ok0" = yes ] && [ "$_d1" -gt "$_d0" ] && [ "${_r#FAILED}" = "$_r" ] \
		&& [ -n "$_via1" ] && [ "$_ok1" = yes ] && [ -n "$_via2" ] && [ "$_ok2" = yes ] && [ -z "$_via3" ]; then
		record ui_rules_proxy PASS "direct.stand went the provider's DIRECT (no node, DIRECT ${_d0} -> ${_d1} B) until set proxy; then HTTP via $_via1 and HTTPS via $_via2; origin.stand still direct"
	else
		record ui_rules_proxy FAIL "provider's way: via '$_via0' ok=$_ok0 DIRECT ${_d0} -> ${_d1} B; set: $_r; then http via '$_via1' ok=$_ok1, https via '$_via2' ok=$_ok2; origin.stand via '$_via3'"
	fi
}

# (3) Garbage is refused, and refused before anything moves.
assert_ui_rules_refuse() {
	_p0=$(xray_pid)
	_x0=$(sha256sum "$UI_RENDER" | awk '{print $1}')
	_o0=$(sha256sum "$UI_OVERRIDES" | awk '{print $1}')
	_k0=$(vcall rules | tr -d ' \n')
	_a=$(vcall set_rules '{"direct":["origin.stand","exa mple..stand"],"proxy":[]}')
	_c1=$(jf "$_a" '@.code')
	_t1=$(jf "$_a" '@.detail')
	_c2=$(jf "$(vcall set_rules '{"direct":["x.stand"],"proxy":["https://www.x.stand/"]}')" '@.code')
	_c3=$(jf "$(vcall set_rules '{"direct":["x.stand"]}')" '@.code')
	_c4=$(jf "$(vcall set_rules '{"direct":["1.2.3.4/33"],"proxy":[]}')" '@.code')
	sleep 3
	_p1=$(xray_pid)
	_x1=$(sha256sum "$UI_RENDER" | awk '{print $1}')
	_o1=$(sha256sum "$UI_OVERRIDES" | awk '{print $1}')
	_k1=$(vcall rules | tr -d ' \n')
	if [ "$_c1" = invalid_params ] && echo "$_t1" | grep -qF 'direct[1] "exa mple..stand"' \
		&& [ "$_c2" = invalid_params ] && [ "$_c3" = invalid_params ] && [ "$_c4" = invalid_params ] \
		&& [ "$_p1" = "$_p0" ] && [ "$_x1" = "$_x0" ] && [ "$_o1" = "$_o0" ] && [ "$_k1" = "$_k0" ]; then
		record ui_rules_refuse PASS "garbage, a site both ways, a missing list, a bad CIDR: invalid_params ('$_t1'); xray, render, overrides and rules untouched"
	else
		record ui_rules_refuse FAIL "codes $_c1/$_c2/$_c3/$_c4, detail '$_t1'; xray pid $_p0 -> $_p1, render $([ "$_x0" = "$_x1" ] && echo same || echo CHANGED), overrides $([ "$_o0" = "$_o1" ] && echo same || echo CHANGED), rules $_k0 -> $_k1"
	fi
}

# (4) An xray restart (a fresh xray) runs the same sites.
assert_ui_rules_survive_restart() {
	_p0=$(xray_pid)
	_r=$(jf "$(vcall restart_xray)" '@.code')
	ui_wait 30 xray_pid_changed "$_p0"
	_p1=$(xray_pid)
	ui_wait 30 engine_running
	ui_wait 20 node_a_or_b_alive
	_via1=$(site_via http origin.stand)
	site_ok && _ok1=yes || _ok1=no
	_via2=$(site_via http direct.stand)
	site_ok && _ok2=yes || _ok2=no
	if [ "$_r" = xray_restarted ] && [ -n "$_p1" ] && [ "$_p1" != "$_p0" ] && rules_are origin.stand direct.stand \
		&& [ -z "$_via1" ] && [ "$_ok1" = yes ] && [ -n "$_via2" ] && [ "$_ok2" = yes ]; then
		record ui_rules_survive_restart PASS "xray pid $_p0 -> $_p1: origin.stand still direct, direct.stand still via $_via2"
	else
		record ui_rules_survive_restart FAIL "restart=$_r pid $_p0 -> ${_p1:-none}; origin.stand via '$_via1' ok=$_ok1, direct.stand via '$_via2' ok=$_ok2; rules: $(vcall rules | tr -d ' \n' | cut -c1-120)"
	fi
}

# Nothing left behind for the assertions after these: the lists emptied, the
# render back to the provider's routing and the operator's sniffing.
assert_ui_rules_cleared() {
	_r=$(ui_set_rules '{"direct":[],"proxy":[]}' "" "") || _r="FAILED: $_r"
	_ro=$(render_route_only)
	_via=$(client_via)
	if [ "${_r#FAILED}" = "$_r" ] && ! grep -qF '"inboundTag":["tproxy-in"]' "$UI_RENDER" && [ "$_ro" != true ] \
		&& ! grep -q '"direct"' "$UI_OVERRIDES" && [ -n "$_via" ]; then
		record ui_rules_cleared PASS "emptied ($_r): no owner's rule in the render, the operator's sniffing back (routeOnly '${_ro:-unset}'), traffic via $_via"
	else
		record ui_rules_cleared FAIL "emptying: $_r; routeOnly '$_ro'; overrides: $(tr -d '\n ' < "$UI_OVERRIDES" | cut -c1-120); traffic via '$_via'"
	fi
}

# The browser's path: LuCI's HTTP JSON-RPC, gated by the session and the ACL.
assert_ui_http_and_acl() {
	_anon=$(curl -s http://127.0.0.1/ubus -H 'Content-Type: application/json' \
		-d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"call\",\"params\":[\"$UI_NULL_SID\",\"vectra\",\"status\",{}]}")
	_login=$(curl -s http://127.0.0.1/ubus -H 'Content-Type: application/json' \
		-d "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"call\",\"params\":[\"$UI_NULL_SID\",\"session\",\"login\",{\"username\":\"root\",\"password\":\"\"}]}")
	_sid=$(jf "$_login" '@.result[1].ubus_rpc_session')
	_auth=$(curl -s http://127.0.0.1/ubus -H 'Content-Type: application/json' \
		-d "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"call\",\"params\":[\"$_sid\",\"vectra\",\"status\",{}]}")
	_anon_code=$(jf "$_anon" '@.result[0]')
	[ -n "$_anon_code" ] || _anon_code=$(jf "$_anon" '@.error.code')
	_state=$(jf "$_auth" '@.result[1].engine.state')
	if [ -n "$_sid" ] && [ "$_state" = running ] && [ "$_anon_code" != 0 ]; then
		record ui_http_acl PASS "anonymous call refused ($_anon_code); logged-in session reads status over /ubus (engine $_state)"
	else
		record ui_http_acl FAIL "anon='$(echo "$_anon" | cut -c1-80)' sid='$_sid' authed state='$_state'"
	fi
}

assert_ui_luci_page() {
	_app=$(curl -s -o /tmp/app.js -w '%{http_code}' http://127.0.0.1/luci-static/vectra/vectra-app.js)
	_view=$(curl -s -o /tmp/view.js -w '%{http_code}' http://127.0.0.1/luci-static/resources/view/vectra/app.js)
	_cookie=$(curl -s -i http://127.0.0.1/cgi-bin/luci/ --data 'luci_username=root&luci_password=' \
		| sed -n 's/^[Ss]et-[Cc]ookie: \(sysauth[^;]*\).*/\1/p' | head -1)
	_page=$(curl -s -o /tmp/page.html -w '%{http_code}' -H "Cookie: $_cookie" http://127.0.0.1/cgi-bin/luci/admin/vectra)
	# Vectra is LuCI's landing page: `admin` is a firstchild node, and Vectra
	# is its first child (order 1), so the page after login is ours.
	_land=$(curl -s -L -o /tmp/landing.html -w '%{http_code} %{url_effective}' -H "Cookie: $_cookie" http://127.0.0.1/cgi-bin/luci/admin)
	_sz=$(wc -c < /tmp/app.js | tr -d ' ')
	if [ "$_app" = 200 ] && [ "$_sz" -gt 10000 ] && [ "$_view" = 200 ] && [ -n "$_cookie" ] && [ "$_page" = 200 ] \
		&& grep -q 'vectra' /tmp/page.html && [ "${_land%% *}" = 200 ] \
		&& { [ "${_land##*/}" = vectra ] || grep -q 'vectra/app' /tmp/landing.html; }; then
		record ui_luci_page PASS "LuCI login -> Vectra page 200, and /admin lands on it ($_land); view + app bundle ($_sz B) served"
	else
		record ui_luci_page FAIL "bundle=$_app (${_sz}B) view=$_view cookie='${_cookie:+set}' page=$_page landing='$_land'"
	fi
}

# The operator's lock (UCI ui_lock), for customer routers: the UI offers only
# the simple view, and the ROUTER refuses the Pro methods — hiding a tab is not
# what enforces it. vctl reads the option at every call, so nothing restarts
# between the commit and the next ubus call. Anti-vacuity: the refused pin
# names a real balancer and node, so without the lock xray's override moves.
ui_lock() { # 1|0
	uci set vectra-controller-pro.main.ui_lock="$1"
	uci commit vectra-controller-pro
}

assert_ui_operator_lock() {
	ui_lock 1
	_st=$(vcall status)
	_n=$(jf "$(vcall nodes)" '@.code')
	_b=$(jf "$(vcall balancers)" '@.code')
	_l=$(jf "$(vcall logs '{"lines":10}')" '@.code')
	_p=$(jf "$(vcall pin_balancer '{"balancer":"BL-MAIN","node":"node-b"}')" '@.code')
	_pi=$(jf "$(vcall set_probe_interval '{"seconds":600}')" '@.code')
	_ov=$(xray_override BL-MAIN)
	_e=$(vcall entries)
	_d=$(vcall diagnostics)
	# My sites is the simple view's: served. set_rules is probed with garbage,
	# so being served shows as invalid_params — and nothing changes.
	_ru=$(vcall rules)
	_sr=$(jf "$(vcall set_rules '{"direct":["exa mple.stand"],"proxy":[]}')" '@.code')
	# Lifted before anything is judged: no later assertion runs locked. A
	# change the lock failed to refuse is undone as well.
	ui_lock 0
	[ "$_p" = locked ] || vcall unpin_balancer '{"balancer":"BL-MAIN"}' >/dev/null
	[ "$_pi" = locked ] || vcall set_probe_interval '{"seconds":0}' >/dev/null
	_st2=$(vcall status)
	_n2=$(vcall nodes)
	_locked=$(jf "$_st" '@.ui.locked')
	_unlocked=$(jf "$_st2" '@.ui.locked')
	_ecount=$(jf "$_e" '@.entries[*].index' | wc -l | tr -d ' ')
	_dx=$(jf "$_d" '@.checks[@.id="xray_running"].status')
	_na=$(jf "$_n2" '@.nodes[@.tag="node-a"].tag')
	if [ "$_locked" = true ] && [ "$_n" = locked ] && [ "$_b" = locked ] && [ "$_l" = locked ] \
		&& [ "$_p" = locked ] && [ "$_pi" = locked ] && [ -z "$_ov" ] \
		&& [ "$_ecount" = 2 ] && [ -z "$(jf "$_e" '@.code')" ] && [ "$_dx" = ok ] \
		&& [ "$(jf "$_ru" '@.max')" = 100 ] && [ -z "$(jf "$_ru" '@.code')" ] && [ "$_sr" = invalid_params ] \
		&& [ "$_unlocked" = false ] && [ "$_na" = node-a ] && [ -z "$(jf "$_n2" '@.code')" ]; then
		record ui_operator_lock PASS "ui_lock=1: status.ui.locked; nodes, balancers, logs, pin, probe interval refused 'locked' (xray's override untouched); entries, diagnostics, rules and set_rules served. ui_lock=0: nodes served again"
	else
		record ui_operator_lock FAIL "locked: ui.locked=$_locked nodes=$_n balancers=$_b logs=$_l pin=$_p probe=$_pi override='$_ov' entries=$_ecount xray_running=$_dx rules.max=$(jf "$_ru" '@.max') set_rules=$_sr; unlocked: ui.locked=$_unlocked node-a='$_na'"
	fi
}

assert_ui_socket_private() {
	# busybox on OpenWrt has no stat applet; ls prints the mode.
	_mode=$(ls -l "$UI_WORK/ui.sock" 2>/dev/null | cut -c1-10)
	if [ "$_mode" = srw------- ]; then
		record ui_socket_private PASS "the daemon's UI socket is 0600 (root only)"
	else
		record ui_socket_private FAIL "ui.sock mode '$_mode'"
	fi
}

assert_ui_removal() {
	opkg remove vectra-controller-pro 2>&1 | sed 's/^/       opkg: /'
	if ui_wait 15 vectra_gone; then
		_gone=yes
	else
		_gone=no
	fi
	if [ "$_gone" = yes ] && [ ! -e /usr/share/luci/menu.d/luci-app-vectra-controller-pro.json ] && [ ! -e /usr/libexec/rpcd/vectra ]; then
		record ui_removed PASS "opkg remove: plugin, ACL and menu gone; rpcd dropped 'vectra'"
	else
		record ui_removed FAIL "after remove: ubus object gone=$_gone, menu $( [ -e /usr/share/luci/menu.d/luci-app-vectra-controller-pro.json ] && echo present || echo gone)"
	fi
}

# -------------------------------------------------------------------- main ---

run_ui_mode() {
	setup_kernel
	setup_topology
	setup_guard
	start_origins
	ui_isolate
	ui_start_nodes
	ui_start_subscription
	ui_boot_services

	say "install the package; its postinst must register the UI with rpcd"
	assert_ui_rpcd_registration

	say "first fetch through 'vctl apply-local', then the daemon brings xray up"
	ui_seed_and_apply

	say "read-only answers"
	assert_ui_status
	assert_ui_probe_override
	assert_ui_balancer_view
	assert_ui_observatory
	assert_ui_socket_private
	assert_ui_local_guard

	say "balancer pins, proven on the packets"
	assert_ui_pin_steers_traffic
	assert_ui_pin_refuses
	assert_ui_pin_survives_restart
	assert_ui_unpin

	say "locations and the probe interval"
	assert_ui_locations
	assert_ui_probe_interval
	assert_ui_diagnostics_and_logs

	say "my sites: always direct / always through the VPN, proven on the packets"
	assert_ui_rules_direct
	assert_ui_rules_proxy
	assert_ui_rules_refuse
	assert_ui_rules_survive_restart
	assert_ui_rules_cleared

	say "LuCI: the browser's path"
	assert_ui_http_and_acl
	assert_ui_luci_page

	say "the operator's lock, enforced by the router (lifted again after)"
	assert_ui_operator_lock

	say "data plane integrity"
	assert_tcp
	assert_no_leak

	[ "$FAILURES" -gt 0 ] && { say "diagnostics for the failures above"; ui_dump; }

	if [ "$MODE" = ui-hold ]; then
		say "HOLD: LuCI at http://127.0.0.1:${STAND_UI_PORT:-8080}/cgi-bin/luci/admin/vectra (root, empty password)"
		printf 'STAND-HOLD ready\n'
		while :; do sleep 3600; done
	fi

	say "removal"
	assert_ui_removal
}
