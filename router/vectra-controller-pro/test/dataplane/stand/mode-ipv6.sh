#!/bin/sh
# Gap 2 — the IPv6 data path.
#
# `vctl firewall apply` renders v6 nft rules and `vctl firewall routing`
# installs a v6 policy route. Both were visible in the log and neither had ever
# carried a packet. This mode drives real IPv6 TCP / TLS / DNS / UDP from the
# client namespace and reports what actually happens.
#
# MODE=ipv6              the SHIPPED operator config, i.e. inbounds.tproxy.
#                        listenIP = "0.0.0.0" — what internal/config defaults to
#                        and what both panel fixtures send. Everything must PASS.
# MODE=ipv6-no-v6-route  the same run with ONE thing removed: the v6 fwmark
#                        policy route. The v6 assertions must FAIL and
#                        v4_control_tcp must still PASS.
# MODE=ipv6-dual         listenIP "::" instead. Everything must PASS, so an
#                        operator who pins the v6 wildcard loses nothing on v4.
# MODE=ipv6-killswitch   listenIP "::" with the kill switch ARMED. Everything
#                        must PASS: nothing had ever run the v6 data path
#                        fail-closed.
# MODE=ipv6-refused      IPv6 off, as the daemon runs unless UCI ipv6 '1'
#                        (Spec.RefuseIPv6, applied with -refuse-ipv6): the
#                        client's IPv6 to the outside is refused at once and
#                        counted, the router's own v6 address and IPv4 work.
# MODE=ipv6-refused-off  its control: the same run with the carrying ruleset;
#                        v6_refused_at_once and v6_refusal_counted must FAIL.
# MODE=ipv6-refused-nolan the refusal with no LAN device known (the daemon
#                        when netifd cannot say): IPv6 from outside into the
#                        LAN is refused too; v6_inbound_left_to_the_firewall
#                        must FAIL.
# MODE=ipv6-no-ct-return the defect ipv6-killswitch found: the output chain's
#                        returns for replies deleted (`ct direction reply
#                        return` from 0.6.0-r4, and `ct state
#                        established,related return`), so the router's replies
#                        to its own LAN clients are marked and routed to lo.
#                        v6_router_reachable must FAIL.
#
# THE RESULT WAS NOT WHAT WAS EXPECTED, and that is worth writing down. The
# hypothesis going in was that listenIP "0.0.0.0" binds v4-only, so v6 would be
# captured by the ruleset and then black-holed (or worse, leak). It carries
# fine. Go's net.Listen treats the v4 WILDCARD as a wildcard: favoriteAddrFamily
# returns AF_INET6 with IPV6_V6ONLY=0 for a wildcard listen, so
# `Listen("tcp", "0.0.0.0:12345")` is a dual-stack socket and the v6 TPROXY
# socket lookup finds it. The stand was written to fail here and passed instead.
#
# Which is why the control is now surgical rather than configurational: the only
# difference between `ipv6` and `ipv6-no-v6-route` is `ip -6 rule del fwmark`.
# Everything else — ruleset, xray, config, origins, second — is identical, so a
# v6 failure there can only be the v6 policy route, and v4 carrying on in the
# same run proves the harness itself is alive.
#
# Address plan: 2001:db8::/32 (RFC 3849 documentation prefix). It is deliberately
# NOT a ULA — fc00::/7 is inside DefaultSpec's bypass6 set, so ULA traffic would
# be returned before the TPROXY rule and the whole mode would be vacuous.

CLIENT6=2001:db8:99::2
ROUTER_CLIENT6=2001:db8:99::1
CLIENT_NET6=2001:db8:99::/64
ROUTER_INET6=2001:db8:44::1
INET_PEER6=2001:db8:44::2
ORIGIN6=2001:db8:44::44
ORIGIN_DNS6=2001:db8:44::53

guard6_ctr() {
	_v=$(nft list counter inet standguard leaked6 2>/dev/null | awk '/packets/{print $2; exit}')
	[ -n "$_v" ] || _v=-1
	echo "$_v"
}

setup_kernel6() {
	echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null || true
	echo 1 > /proc/sys/net/ipv6/conf/all/forwarding   2>/dev/null \
		|| die "cannot enable IPv6 forwarding (kernel has no IPv6?)"
	# Duplicate address detection would leave the addresses "tentative" for a
	# second or two and every bind would fail with EADDRNOTAVAIL.
	echo 0 > /proc/sys/net/ipv6/conf/default/accept_dad 2>/dev/null || true
	echo 0 > /proc/sys/net/ipv6/conf/all/accept_dad     2>/dev/null || true
}

setup_topology6() {
	for ns in "$CLIENT_NS" "$INET_NS"; do
		ip netns exec "$ns" sh -c '
			echo 0 > /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null
			echo 0 > /proc/sys/net/ipv6/conf/all/accept_dad   2>/dev/null
			echo 0 > /proc/sys/net/ipv6/conf/default/accept_dad 2>/dev/null' || true
	done

	ip addr add "$ROUTER_CLIENT6/64" dev vclient nodad
	ip -n "$CLIENT_NS" addr add "$CLIENT6/64" dev vclient-c nodad
	ip -n "$CLIENT_NS" route add default via "$ROUTER_CLIENT6" dev vclient-c

	ip addr add "$ROUTER_INET6/64" dev vinet nodad
	ip -n "$INET_NS" addr add "$INET_PEER6/64" dev vinet-c nodad
	ip -n "$INET_NS" addr add "$ORIGIN6/128" dev lo nodad
	ip -n "$INET_NS" addr add "$ORIGIN_DNS6/128" dev lo nodad
	ip -n "$INET_NS" route add default via "$ROUTER_INET6" dev vinet-c

	ip route add "$ORIGIN6/128" via "$INET_PEER6" dev vinet
	ip route add "$ORIGIN_DNS6/128" via "$INET_PEER6" dev vinet
	sleep 1

	# A v6 resolver the client can actually reach: `ip netns exec` bind-mounts
	# /etc/netns/<ns>/resolv.conf over /etc/resolv.conf inside the namespace.
	mkdir -p "/etc/netns/$CLIENT_NS"
	printf 'nameserver %s\n' "$ORIGIN_DNS6" > "/etc/netns/$CLIENT_NS/resolv.conf"
}

# Same anti-false-pass control as the v4 stand, extended to v6: plain forwarding
# for the client prefix is dropped and counted, so a v6 packet that reaches the
# origin can only have gone through xray — and one that falls through to plain
# forwarding is visible instead of silently succeeding.
setup_guard6() {
	nft -f - <<EOF || die "guard table (v4+v6)"
table inet standguard {
  counter leaked  { }
  counter leaked6 { }
  chain forward {
    type filter hook forward priority filter; policy accept;
    ip  saddr $CLIENT_NET  counter name "leaked"  drop
    ip  daddr $CLIENT_NET  counter name "leaked"  drop
    ip6 saddr $CLIENT_NET6 counter name "leaked6" drop
    ip6 daddr $CLIENT_NET6 counter name "leaked6" drop
  }
}
EOF
}

# The v6 origins are plain relays into the v4 listeners that start_origins
# already brought up, so both families answer with byte-identical payloads and
# any difference in the result is the data plane's, not the origin's.
start_origins6() {
	in_inet socat "TCP6-LISTEN:80,bind=[$ORIGIN6],fork,reuseaddr" \
		"TCP4:$ORIGIN_IP:80" >/dev/null 2>&1 &
	in_inet socat "TCP6-LISTEN:443,bind=[$ORIGIN6],fork,reuseaddr" \
		"TCP4:$ORIGIN_IP:443" >/dev/null 2>&1 &
	in_inet socat "UDP6-RECVFROM:$UDP_PORT,bind=[$ORIGIN6],fork" \
		"UDP4:$ORIGIN_IP:$UDP_PORT" >/dev/null 2>&1 &
	in_inet socat "UDP6-RECVFROM:53,bind=[$ORIGIN_DNS6],fork" \
		"UDP4:$ORIGIN_DNS_IP:53" >/dev/null 2>&1 &

	_i=0
	while [ "$_i" -lt 20 ]; do
		if in_inet curl -s -g --max-time 2 "http://[$ORIGIN6]/hello" 2>/dev/null | grep -q "$EXPECT_BODY"; then
			info "v6 origins up in the inet ns (http/https/udp-echo/dns relays)"
			return 0
		fi
		_i=$((_i + 1)); sleep 1
	done
	die "v6 origin never answered inside the inet ns"
}

# --------------------------------------------------------------- assertions ---

assert_v6_plumbing() {
	_rules=$(nft list table inet vctl 2>/dev/null | grep -c 'ip6 daddr')
	_rule=$(ip -6 rule list 2>/dev/null | grep -c 'fwmark 0x1')
	_rt=$(ip -6 route show table 100 2>/dev/null | grep -c 'local')
	if [ "$_rules" -ge 4 ] && [ "$_rule" -ge 1 ] && [ "$_rt" -ge 1 ]; then
		record v6_plumbing_applied PASS "$_rules ip6 rules in table inet vctl, v6 fwmark rule + local ::/0 in table 100"
	else
		record v6_plumbing_applied FAIL "ip6 rules=$_rules v6 ip rule=$_rule table100=$_rt"
	fi
}

assert_v6_tcp() {
	_out=$(in_client curl -sS -g --max-time 12 "http://[$ORIGIN6]/hello" 2>&1)
	if echo "$_out" | grep -q "$EXPECT_BODY"; then
		record v6_tcp_through_proxy PASS "GET http://[$ORIGIN6]/hello -> $EXPECT_BODY"
	else
		record v6_tcp_through_proxy FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

assert_v6_tls() {
	_out=$(in_client curl -sSk -g --max-time 12 "https://[$ORIGIN6]/hello" 2>&1)
	if echo "$_out" | grep -q "$EXPECT_BODY"; then
		record v6_tls_through_proxy PASS "TLS handshake + GET over IPv6"
	else
		record v6_tls_through_proxy FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

assert_v6_dns() {
	_out=$(in_client nslookup origin.stand "$ORIGIN_DNS6" 2>&1)
	if echo "$_out" | grep -q "$ORIGIN_IP"; then
		record v6_dns_through_proxy PASS "origin.stand resolved via [$ORIGIN_DNS6]"
	else
		record v6_dns_through_proxy FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

assert_v6_udp() {
	_out=$(in_client sh -c "echo udp6-$EXPECT_BODY | socat -T6 - UDP6:[$ORIGIN6]:$UDP_PORT" 2>&1)
	if echo "$_out" | grep -q "udp6-$EXPECT_BODY"; then
		record v6_udp_through_proxy PASS "UDP echo [$ORIGIN6]:$UDP_PORT round-tripped"
	else
		record v6_udp_through_proxy FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

# Deltas measured across the v6 phase ONLY, so the v4 control run afterwards
# cannot contaminate them. The same trap as the v4 stand applies: hits moving is
# not evidence of delivery, which is why the two are separate assertions.
assert_v6_counters() {
	_dth=$(( $(ctr vctl_tproxy_hits) - V6_TH0 ))
	_dex=$(( $(ctr vctl_egress_exempt) - V6_EX0 ))
	if [ "$_dth" -gt 0 ]; then
		record v6_nft_tproxy_hits PASS "+$_dth packets matched the TPROXY rule during the v6 phase"
	else
		record v6_nft_tproxy_hits FAIL "+$_dth — v6 never reached the TPROXY rule at all"
	fi
	if [ "$_dex" -gt 0 ]; then
		record v6_nft_tproxy_delivered PASS "+$_dex egress_exempt — xray accepted the v6 connections and dialled out"
	else
		record v6_nft_tproxy_delivered FAIL "+$_dex egress_exempt — TPROXY matched v6 but nothing was delivered to xray"
	fi
}

# What happens to v6 client traffic the proxy does not take? The guard drops and
# counts it. A non-zero counter means the packet fell through PREROUTING to
# plain forwarding: on a router with a v6 default route that is an UNPROXIED
# LEAK, not a black hole.
#
# `vctl_killswitch_drops` is not read here: both v6 operator configs set
# killSwitch:false, so the counter is not even emitted. The armed ruleset is
# covered by MODE=killswitch (v4) — see the note above assert_no_leak in
# stand.sh for why the two counters are not interchangeable.
assert_v6_leak() {
	_v=$(guard6_ctr)
	if [ "$_v" = 0 ]; then
		record v6_no_unproxied_leak PASS "guard v6 counter 0 — no v6 client packet reached plain forwarding"
	else
		record v6_no_unproxied_leak FAIL "guard v6 counter $_v — v6 client traffic fell through PREROUTING to plain forwarding"
	fi
}

# The harness control. Same box, same ruleset, same second: if IPv4 carries and
# IPv6 does not, the difference is the data plane's and not the stand's.
assert_v4_control() {
	_out=$(in_client curl -sS --max-time 12 "http://$ORIGIN_IP/hello" 2>&1)
	if echo "$_out" | grep -q "$EXPECT_BODY"; then
		record v4_control_tcp PASS "IPv4 through the same proxy still works — the harness is sound"
	else
		record v4_control_tcp FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

# ---- the kill switch, ARMED, on IPv6 ---------------------------------------
#
# MODE=killswitch proves the fail-closed logic on v4. The v6 chains come out of
# the same template, so what that does NOT prove is the kernel half: that v6
# traffic is still CARRIED, and that the v6 exemptions still return, once the
# chain ends in a terminal drop.
#
# That is exactly the shape of the defect this stand caught on v4 — `policy drop`
# turned every `return` into an uncounted drop and the router black-holed itself
# — and on v6 nothing had ever run with the switch armed at all.
#
# The proof that costs nothing extra is the mode's own traffic assertions: with
# MODE=ipv6-killswitch they run against an ARMED ruleset, so v6_tcp/tls/dns/udp
# passing means armed v6 carries packets. These add the two checks that are
# specific to being armed.

V6_SVC_PORT=8081

# A service on the router's own v6 address, for `fib daddr type local return` —
# the exemption that stands for SSH, LuCI and DNS over IPv6 on a real unit.
start_router_service6() {
	socat "TCP6-LISTEN:$V6_SVC_PORT,bind=[$ROUTER_CLIENT6],fork,reuseaddr" \
		"SYSTEM:$STAND/http-reply.sh" >/dev/null 2>&1 &
	_i=0
	while [ "$_i" -lt 20 ]; do
		curl -s -g --max-time 2 "http://[$ROUTER_CLIENT6]:$V6_SVC_PORT/hello" 2>/dev/null | grep -q "$EXPECT_BODY" && {
			info "router v6 service up on [$ROUTER_CLIENT6]:$V6_SVC_PORT"
			return 0
		}
		_i=$((_i + 1)); sleep 1
	done
	die "the router's v6 service never came up"
}

assert_v6_ks_ruleset() {
	# BOTH chain policies, like the v4 twin: in an nftables base chain a `return`
	# falls through to the POLICY, so a `policy drop` anywhere turns every
	# exemption above it into an uncounted drop.
	_pol=$(nft list chain inet vctl prerouting 2>/dev/null | sed -n 's/.*policy \([a-z]*\);.*/\1/p' | head -1)
	_polf=$(nft list chain inet vctl forward 2>/dev/null | sed -n 's/.*policy \([a-z]*\);.*/\1/p' | head -1)
	_drops=$(nft list table inet vctl 2>/dev/null | grep -c 'counter name "vctl_killswitch_drops" drop')
	if [ "$_pol" = accept ] && [ "$_polf" = accept ] && [ "$_drops" -eq 2 ]; then
		record v6_ks_ruleset PASS "armed on v6: prerouting+forward policy accept, 2 terminal counted drops"
	else
		record v6_ks_ruleset FAIL "prerouting=$_pol forward=$_polf terminal_drops=$_drops (want accept/accept/2)"
	fi
}

assert_v6_router_reachable() {
	_out=$(in_client curl -sS -g --max-time 8 "http://[$ROUTER_CLIENT6]:$V6_SVC_PORT/hello" 2>&1)
	if echo "$_out" | grep -q "$EXPECT_BODY"; then
		record v6_router_reachable PASS \
			"a LAN client still reaches a service on the router's OWN v6 address with the switch armed"
	else
		record v6_router_reachable FAIL \
			"the router stopped answering on its own v6 address — SSH/LuCI/DNS over v6 would be gone: $(echo "$_out" | tr '\n' ' ' | cut -c1-110)"
	fi
}

# ---- IPv6 off (Spec.RefuseIPv6) ----------------------------------------------
#
# The provider's nodes carry no IPv6: a connection over it hung at the exit.
# The daemon refuses the LAN's IPv6 to the outside with a reset (TCP) or
# admin-prohibited (the rest), so a device takes IPv4 straight away.

# A refusal, not a hang and not a pass: curl gives up at once (exit 7), well
# inside the second a device waits before IPv4, and the ruleset's refusal
# counter moved during that very attempt — the reset was vctl's, not the
# kernel lacking a route. (This curl words a refused connection "Error"; socat
# names the errno, kept for the record.)
assert_v6_refused() {
	_c0=$(ctr vctl_ipv6_refused)
	_out=$(in_client curl -sS -g --max-time 10 -o /dev/null -w 't=%{time_total}' "http://[$ORIGIN6]/hello" 2>&1)
	_rc=$?
	_c1=$(ctr vctl_ipv6_refused)
	_t=$(echo "$_out" | sed -n 's/.*t=\([0-9.]*\).*/\1/p' | tail -n 1)
	_fast=$(awk -v t="${_t:-99}" 'BEGIN { print (t < 2) ? "yes" : "no" }')
	_why=$(in_client socat -T3 - "TCP6:[$ORIGIN6]:80" </dev/null 2>&1 | sed -n 's/.*: \([A-Za-z ]*\)$/\1/p' | tail -n 1)
	if [ "$_rc" -eq 7 ] && [ "$_fast" = yes ] && [ "$_c0" -ge 0 ] && [ "$_c1" -gt "$_c0" ]; then
		record v6_refused_at_once PASS "the client's IPv6 to the outside was refused by the ruleset in ${_t}s (vctl_ipv6_refused $_c0 -> $_c1; socat: ${_why:-?}) — a device falls back to IPv4 at once"
	else
		record v6_refused_at_once FAIL "curl exit $_rc in ${_t:-?}s, vctl_ipv6_refused $_c0 -> $_c1: $(echo "$_out" | tr '\n' ' ' | cut -c1-140)"
	fi
}

# The rest of IPv6 (here UDP) is refused too — admin-prohibited, counted by
# the same rule set — not carried and not left to time out.
assert_v6_refusal_counted() {
	_u0=$(ctr vctl_ipv6_refused)
	_echo=$(in_client sh -c "echo udp-$EXPECT_BODY | socat -T2 - UDP6:[$ORIGIN6]:$UDP_PORT" 2>&1)
	_u1=$(ctr vctl_ipv6_refused)
	if [ "$_u0" -ge 0 ] && [ "$_u1" -gt "$_u0" ] && ! echo "$_echo" | grep -q "udp-$EXPECT_BODY"; then
		record v6_refusal_counted PASS "UDP over IPv6 refused by the ruleset too (vctl_ipv6_refused $_u0 -> $_u1; socat: $(echo "$_echo" | sed -n 's/.*: \([A-Za-z ]*\)$/\1/p' | tail -n 1))"
	else
		record v6_refusal_counted FAIL "vctl_ipv6_refused $_u0 -> $_u1, echo: $(echo "$_echo" | tr '\n' ' ' | cut -c1-100)"
	fi
}

# From outside into a LAN device is not the LAN's IPv6 to the outside: vctl
# leaves it to the router's own firewall (here the stand's guard, which drops
# and counts it), as without vctl. The review of r29–r31 found the refusal
# had no direction.
assert_v6_inbound_left() {
	_r0=$(ctr vctl_ipv6_refused)
	_g0=$(guard6_ctr)
	in_inet curl -sS -g --max-time 3 -o /dev/null "http://[$CLIENT6]:80/" >/dev/null 2>&1
	_r1=$(ctr vctl_ipv6_refused)
	_g1=$(guard6_ctr)
	if [ "$_r0" -ge 0 ] && [ "$_r1" -eq "$_r0" ] && [ "$_g1" -gt "$_g0" ]; then
		record v6_inbound_left_to_the_firewall PASS "IPv6 from outside into the LAN passed vctl untouched (vctl_ipv6_refused $_r0 -> $_r1) and reached the next firewall (guard $_g0 -> $_g1)"
	else
		record v6_inbound_left_to_the_firewall FAIL "vctl_ipv6_refused $_r0 -> $_r1, guard $_g0 -> $_g1 — vctl judged IPv6 coming into the LAN"
	fi
}

# ------------------------------------------------------------------- main -----

run_ipv6_mode() {
	CFG="$STAND/operator-config.json"
	case "$MODE" in
	ipv6-dual)       CFG="$STAND/operator-config-v6.json" ;;
	ipv6-killswitch) CFG="$STAND/operator-config-v6-killswitch.json" ;;
	ipv6-refused)       FW_EXTRA="-refuse-ipv6 -lan-devices vclient" ;;
	ipv6-refused-nolan) FW_EXTRA=-refuse-ipv6 ;;
	esac
	PROVIDER="$STAND/provider-marked.json"
	info "operator config: $CFG (listenIP $(sed -n 's/.*"listenIP": *"\([^"]*\)".*/\1/p' "$CFG"))"

	setup_kernel
	setup_kernel6
	setup_topology
	setup_topology6
	setup_guard6
	start_origins
	start_origins6
	start_router_service6

	say "apply the ruleset with the SHIPPED code"
	apply_firewall
	if [ "$MODE" = ipv6-no-ct-return ]; then
		# The defect MODE=ipv6-killswitch found on its first run, reintroduced by
		# deleting exactly one rule: without it the router's replies to its own
		# LAN clients fall through to the marking rule, take the tproxy fwmark
		# and are routed to `local ::/0 dev lo`. v6_router_reachable must FAIL.
		# From 0.6.0-r4 the replies are carried by `ct direction reply return`
		# first (the established line after it keeps older flows of the
		# router's own untouched), so the defect is both lines gone.
		_hs=$(nft -a list chain inet vctl output | awk '/ct direction reply return|ct state established,related return/{print $NF}')
		[ -n "$_hs" ] || die "no-ct-return: no reply or established return in chain output to remove"
		for _h in $_hs; do
			nft delete rule inet vctl output handle "$_h" || die "no-ct-return: delete failed"
		done
		info "MODE=ipv6-no-ct-return: $(echo "$_hs" | wc -w | tr -d ' ') return rule(s) for replies and established connections removed from chain output, everything else untouched"
	fi
	if [ "$MODE" = ipv6-no-v6-route ]; then
		# The defect, and nothing else: the v4 policy route stays exactly where
		# `vctl firewall routing` put it.
		ip -6 rule del fwmark 0x1 lookup 100 2>/dev/null
		ip -6 route del local ::/0 dev lo table 100 2>/dev/null
		info "MODE=ipv6-no-v6-route: v6 fwmark rule + local ::/0 removed (v4 policy route untouched)"
	fi
	echo "--- ip -6 rule ---"; ip -6 rule list
	echo "--- ip -6 route table 100 ---"; ip -6 route show table 100 2>/dev/null || echo "(empty)"

	say "start xray through vctl supervise + vctl-xray-wrapper"
	start_xray

	case "$MODE" in
	ipv6-refused | ipv6-refused-off | ipv6-refused-nolan)
		# ipv6-refused-off carries IPv6: its ruleset has no vctl_ipv6_refused
		# (ctr reads -1), and both must FAIL.
		say "assertions (IPv6 off: refused at once, counted)"
		assert_v6_refused
		assert_v6_refusal_counted
		[ "$MODE" = ipv6-refused-off ] || assert_v6_inbound_left
		# v6_no_unproxied_leak is not read: the refusal rejects in vctl's forward
		# chain at priority mangle (-150), before the stand's guard at 0, so a
		# zero there would be free (as with the kill switch armed).
		say "the router must still answer on its OWN v6 address"
		assert_v6_router_reachable
		say "assertions (IPv4 control on the same box)"
		assert_v4_control
		say "nft counters (table inet vctl)"
		nft list counters table inet vctl
		return 0
		;;
	esac

	say "assertions (IPv6 phase)"
	assert_v6_plumbing
	V6_TH0=$(ctr vctl_tproxy_hits)
	V6_EX0=$(ctr vctl_egress_exempt)
	assert_v6_tcp
	assert_v6_tls
	assert_v6_dns
	assert_v6_udp
	assert_v6_counters
	if [ "$MODE" = ipv6-killswitch ]; then
		# NOT run when armed, because it cannot fail there. The stand's guard
		# chain counts at `hook forward priority filter` (0); the armed vctl
		# forward chain ends in an unconditional drop at `priority mangle`
		# (-150). vctl kills the packet 150 priority units before standguard can
		# see it, so `leaked6` can never move and the assertion would report PASS
		# whatever the data plane did. mode-killswitch.sh handles the identical
		# situation deliberately (ks_leak_blocked_at_forward reads the guard only
		# where it CAN move, paired with two modes where it does).
		info "MODE=$MODE: skipping v6_no_unproxied_leak — the armed forward chain drops at priority -150, before the stand guard at 0, so a zero there would be free"
	else
		assert_v6_leak
	fi

	say "the router must still answer on its OWN v6 address"
	assert_v6_router_reachable
	if [ "$MODE" = ipv6-killswitch ]; then
		say "assertions (the kill switch ARMED, on v6)"
		assert_v6_ks_ruleset
	fi

	say "assertions (IPv4 control on the same box)"
	assert_v4_control

	say "nft counters (table inet vctl)"
	nft list counters table inet vctl
	echo "--- guard (v4 must stay 0) ---"
	nft list counter inet standguard leaked
	echo "--- guard v6 ---"
	nft list counter inet standguard leaked6
	echo "--- xray listening sockets ---"
	netstat -lnt 2>/dev/null | grep ":$TPROXY_PORT " || echo "(no listener on :$TPROXY_PORT)"

	say "xray / supervisor log (tail)"
	tail -n 25 "$WORK/supervise.log" 2>/dev/null
}
