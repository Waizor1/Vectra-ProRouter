# shellcheck shell=sh
# Russian networks by the kernel (spec 2026-09-30-vctl-service-routing-design,
# decision 8; the owner: «ру сервисы … все в директ отправляй, иначе роутер
# сдохнет»).
#
# Sourced by stand.sh. The ui mode's stand — the package installed with opkg,
# procd, rpcd, the daemon running xray, real xray "provider nodes" in the inet
# ns — with an entry that has the provider's Russian bridge (BL-RU): its
# address rule names geoip:ru, which goes straight out, and an address of its
# own (the stand's origin, 44.44.44.44), which the provider sends through the
# bridge on purpose and which keeps it (the review of r29–r31: an address not
# known Russian keeps the bridge, like nnmclub.to among the names). A real
# address of geoip:ru — 77.88.8.8, Yandex's resolver, in the geo file the
# stand runs with — is held by the stand's inet namespace; nothing leaves the
# stand. Before those rules, a rule proxies blocked.origin.stand by name — a
# blocked site whose real address is that Russian one. The router's dnsmasq
# runs, as a user of its own, and asks through the tunnel.
#
# Proven on the packets: the Russian address leaves by the kernel (the direct
# counter moves, no node sees it); the bridge's own address reaches the
# bridge's node; the proxied name is answered with a FakeDNS address and its
# connection reaches the node, although its real address is the direct one; a
# name no rule proxies gets its real address.

. "$STAND/mode-ui.sh"

RK_DIR=/stand/rukernel
RK_BLOCKED=blocked.origin.stand
RK_PLAIN=plain.origin.stand
RK_RU_IP=77.88.8.8 # in geoip:ru (77.88.8.0/24) of the stand's geo file

# The Russian origin: held by the inet namespace and routed to it, so the
# kernel's "straight out" ends there and nothing leaves the stand.
rk_ru_origin() {
	ip -n "$INET_NS" addr add "$RK_RU_IP/32" dev lo || die "could not give the inet namespace $RK_RU_IP"
	ip route add "$RK_RU_IP/32" via "$INET_PEER_IP" dev vinet || die "could not route $RK_RU_IP to the inet namespace"
	in_inet socat "TCP-LISTEN:80,bind=$RK_RU_IP,fork,reuseaddr" "SYSTEM:$STAND/http-reply.sh" >/dev/null 2>&1 &
	ui_wait 10 sh -c "ip netns exec $INET_NS curl -s --max-time 2 http://$RK_RU_IP/hello | grep -q '$EXPECT_BODY'" \
		|| die "the Russian origin $RK_RU_IP never answered in the inet namespace"
}

rk_fixtures() {
	UI_DIR=/tmp/rukernel-fixtures
	rm -rf "$UI_DIR"
	mkdir -p "$UI_DIR"
	cp /stand/ui/panel-ok.sh /stand/ui/operator-config.json /stand/ui/nodes-server.json "$UI_DIR/"
	cp "$RK_DIR/subscription.json" "$UI_DIR/subscription.json"
}

# The router's resolver: dnsmasq as nobody (vctl redirects the upstream
# queries of non-root dnsmasq into xray), asking the stand's resolver. The
# nodes resolve the proxied name through /etc/hosts.
rk_dnsmasq() {
	grep -q " $RK_BLOCKED\$" /etc/hosts 2>/dev/null || echo "$RK_RU_IP $RK_BLOCKED" >> /etc/hosts
	# The stand's leak guard drops every forwarded LAN packet; a connection
	# vctl sends out by the kernel carries the direct bit (DirectCtMark) and
	# is let past it — anything else forwarded is still a leak.
	nft insert rule inet standguard forward ct mark and 0x10000000 == 0x10000000 counter accept \
		|| die "could not let kernel-direct connections past the leak guard"
	# What fw4 does on a router: the LAN's connections the kernel sends out
	# leave with the router's address (the stand runs no fw4).
	nft -f - <<NFT
table ip rknat {
  chain post {
    type nat hook postrouting priority srcnat; policy accept;
    oifname "vinet" ip saddr $CLIENT_IP masquerade
  }
}
NFT
	dnsmasq -k -u nobody -p 53 --listen-address="$ROUTER_CLIENT_IP" --bind-interfaces \
		--no-hosts --no-resolv "--server=$ORIGIN_DNS_IP" --cache-size=0 -C /dev/null >/tmp/rk-dnsmasq.log 2>&1 &
	ui_wait 10 sh -c "pgrep -x dnsmasq >/dev/null" || die "the router's dnsmasq never came up"
}

rk_seed_and_apply() {
	mkdir -p /tmp/sysinfo /etc/vectra-controller-pro
	echo "Vectra Russian Kernel Stand (synthetic)" > /tmp/sysinfo/model
	cp "$UI_DIR/operator-config.json" /etc/vectra-controller-pro/xray-desired.json
	if ! vctl apply-local -config "$UI_AGENT" > /tmp/rk-apply.log 2>&1; then
		tail -n 20 /tmp/rk-apply.log
		die "vctl apply-local failed"
	fi
	/etc/init.d/vectra-controller-pro restart
	ui_wait 40 engine_running || { ui_dump; die "xray never came up under the daemon"; }
	for n in sticky-de1 sticky-by1; do
		ui_wait 30 node_alive "$n" || { ui_dump; die "the observatory never held $n alive"; }
	done
	info "daemon up; xray running (pid $(xray_pid))"
}

rk_direct_ctr() { nft list counter inet vctl vctl_direct_new 2>/dev/null | sed -n 's/.*packets \([0-9]*\).*/\1/p' | head -n 1 | grep . || echo 0; }
rk_answer() { in_client nslookup "$1" "$ROUTER_CLIENT_IP" 2>/dev/null | awk '/^Name:/{n=1} n && /^Address/ {print $NF; exit}'; }
rk_in_direct() { nft get element inet vctl vctl_direct4 "{ $1 }" >/dev/null 2>&1; }
rk_direct_loaded() { rk_in_direct "$RK_RU_IP"; }

# ----------------------------------------------------------------- asserts ---

assert_rk_render() {
	if grep -q '"fakedns","domains":\["domain:blocked.origin.stand"\]' "$UI_RENDER" && grep -q '"ipPool":"198.18.0.0/16"' "$UI_RENDER"; then
		record rk_render PASS "the render answers blocked.origin.stand with FakeDNS (198.18.0.0/16)"
	else
		record rk_render FAIL "no FakeDNS for the proxied name in the render: $(grep -o '"fakedns[^]]*]' "$UI_RENDER" | head -2 | tr '\n' ' ')"
	fi
}

assert_rk_direct_set() {
	if ui_wait 40 rk_direct_loaded && ! rk_in_direct "$ORIGIN_IP"; then
		record rk_direct_set PASS "the Russian address ($RK_RU_IP, geoip:ru) is in the kernel's direct set; the bridge's own address ($ORIGIN_IP) is not"
	else
		record rk_direct_set FAIL "$RK_RU_IP in vctl_direct4: $(rk_in_direct "$RK_RU_IP" && echo yes || echo no); $ORIGIN_IP: $(rk_in_direct "$ORIGIN_IP" && echo yes || echo no) ($(logread 2>/dev/null | grep -i 'direct' | tail -n 2 | tr '\n' ' ' | cut -c1-200))"
	fi
}

assert_rk_answers() {
	_b=$(rk_answer "$RK_BLOCKED")
	_p=$(rk_answer "$RK_PLAIN")
	case "$_b" in
	198.18.*) _bo=yes ;;
	*) _bo= ;;
	esac
	if [ -n "$_bo" ] && [ "$_p" = "$ORIGIN_IP" ]; then
		record rk_answers PASS "the router answers $RK_BLOCKED with $_b (FakeDNS), $RK_PLAIN with its real $ORIGIN_IP"
	else
		record rk_answers FAIL "$RK_BLOCKED -> '${_b:-none}' (want 198.18.x.x), $RK_PLAIN -> '${_p:-none}' (want $ORIGIN_IP)"
	fi
}

assert_rk_kernel() {
	_c0=$(rk_direct_ctr)
	: > "$UI_NODES_LOG"
	rm -f "$UI_BODY"
	in_client curl -sS --max-time 12 -o "$UI_BODY" "http://$RK_RU_IP/hello" 2>/dev/null
	sleep 1
	_via=$(grep "tcp:$RK_RU_IP:80 " "$UI_NODES_LOG" 2>/dev/null | grep -o '\[[a-z]-in' | tr -d '[' | sort -u | tr '\n' ' ')
	_c1=$(rk_direct_ctr)
	if site_ok && [ -z "$_via" ] && [ "$_c1" -gt "$_c0" ]; then
		record rk_kernel PASS "a request to the Russian address $RK_RU_IP left by the kernel: answered, no node saw it, vctl_direct_new $_c0 -> $_c1"
	else
		record rk_kernel FAIL "answered=$(site_ok && echo yes || echo no) nodes='${_via:-none}' vctl_direct_new $_c0 -> $_c1"
	fi
}

assert_rk_name_proxied() {
	_f=$(rk_answer "$RK_BLOCKED")
	: > "$UI_NODES_LOG"
	rm -f "$UI_BODY"
	in_client curl -sS --max-time 12 -o "$UI_BODY" --resolve "$RK_BLOCKED:80:$_f" "http://$RK_BLOCKED/hello" 2>/dev/null
	sleep 1
	_via=$(grep -F -e "tcp:$RK_BLOCKED:80 " -e "tcp:$RK_RU_IP:80 " "$UI_NODES_LOG" 2>/dev/null | grep -o '\[[a-z]-in' | tr -d '[' | sort -u | tr '\n' ' ' | sed 's/ $//')
	if [ "$_via" = a-in ]; then
		record rk_name_proxied PASS "$RK_BLOCKED (real address in the direct set) went through BL-MAIN's node by its fake $_f$(site_ok && echo ', answered')"
	else
		record rk_name_proxied FAIL "$RK_BLOCKED by $_f went via '${_via:-no node}', want a-in"
	fi
}

assert_rk_plain_name_kernel() {
	_c0=$(rk_direct_ctr)
	: > "$UI_NODES_LOG"
	rm -f "$UI_BODY"
	in_client curl -sS --max-time 12 -o "$UI_BODY" --resolve "$RK_PLAIN:80:$RK_RU_IP" "http://$RK_PLAIN/hello" 2>/dev/null
	sleep 1
	_via=$(grep -F -e "tcp:$RK_PLAIN:80 " -e "tcp:$RK_RU_IP:80 " "$UI_NODES_LOG" 2>/dev/null | grep -o '\[[a-z]-in' | tr -d '[' | sort -u | tr '\n' ' ')
	_c1=$(rk_direct_ctr)
	if site_ok && [ -z "$_via" ] && [ "$_c1" -gt "$_c0" ]; then
		record rk_plain_name_kernel PASS "a name no rule proxies, at a Russian address: by the kernel ($_c0 -> $_c1), no node"
	else
		record rk_plain_name_kernel FAIL "answered=$(site_ok && echo yes || echo no) nodes='${_via:-none}' vctl_direct_new $_c0 -> $_c1"
	fi
}

# The provider's own address in its Russian rule keeps the bridge: through
# BL-RU's node (b-in), not by the kernel.
assert_rk_bridge_address() {
	_c0=$(rk_direct_ctr)
	: > "$UI_NODES_LOG"
	rm -f "$UI_BODY"
	in_client curl -sS --max-time 12 -o "$UI_BODY" "http://$ORIGIN_IP/hello" 2>/dev/null
	sleep 1
	_via=$(grep "tcp:$ORIGIN_IP:80 " "$UI_NODES_LOG" 2>/dev/null | grep -o '\[[a-z]-in' | tr -d '[' | sort -u | tr '\n' ' ' | sed 's/ $//')
	_c1=$(rk_direct_ctr)
	if site_ok && [ "$_via" = b-in ] && [ "$_c1" -eq "$_c0" ]; then
		record rk_bridge_address PASS "the bridge's own address $ORIGIN_IP went through BL-RU's node (b-in), not the kernel: answered, vctl_direct_new $_c0 -> $_c1"
	else
		record rk_bridge_address FAIL "answered=$(site_ok && echo yes || echo no) nodes='${_via:-none}' (want b-in) vctl_direct_new $_c0 -> $_c1"
	fi
}

run_rukernel_mode() {
	rk_fixtures
	setup_kernel
	setup_topology
	setup_guard
	start_origins
	rk_ru_origin
	ui_isolate
	ui_start_nodes
	ui_start_subscription
	ui_boot_services
	/sbin/logd -S 256 >/dev/null 2>&1 &
	ui_wait 10 logread || die "logd never came up"

	say "install the package; an entry with the provider's Russian bridge"
	ui_install_package
	ui_wait 15 ubus list vectra || die "the package installed but rpcd never exposed 'vectra'"
	rk_dnsmasq
	rk_seed_and_apply

	say "the render and the kernel's direct set"
	assert_rk_render
	assert_rk_direct_set

	say "names: proxied ones fake, the rest real"
	assert_rk_answers

	say "the Russian address by the kernel, the bridge's own by its node, the proxied name by the node"
	assert_rk_kernel
	assert_rk_bridge_address
	assert_rk_name_proxied
	assert_rk_plain_name_kernel
	[ "$FAILURES" -gt 0 ] && { say "diagnostics for the failures above"; ui_dump; }
}
