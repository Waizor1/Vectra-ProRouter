#!/bin/sh
# vctl data-plane stand — runs INSIDE the OpenWrt container (--privileged).
#
# Proves that the shipped firewall + splice code actually CARRIES PACKETS.
# `xray -test` returning "Configuration OK" is not evidence of a data plane;
# moved bytes and non-zero nft counters are.
#
# Topology (all inside one container, three network namespaces):
#
#   [client ns]              [container root ns = "router"]         [inet ns]
#   10.99.0.2 ---- veth ---- 10.99.0.1        10.44.0.1 ---- veth ---- 10.44.0.2
#   default via                 ip_forward=1                       lo: 44.44.44.44
#   10.99.0.1                   table inet vctl (from vctl)            44.44.44.53
#                               ip rule fwmark + table 100
#
# 44.44.44.0/24 is deliberately OUTSIDE every bypass set in DefaultSpec, so the
# origin is "the internet" as far as the ruleset is concerned.
#
# The FORWARD path for the client subnet is DROPPED by a separate guard table.
# That removes the only way an assertion could pass without the proxy: plain
# routing. If a client packet reaches the origin, it went through xray.
#
# MODE selects the scenario. The three original v4 scenarios:
#   full        — everything applied; every assertion must PASS
#   no-routing  — `vctl firewall routing` is SKIPPED (defect 1 reintroduced)
#   no-mark     — the provider pins sockopt.mark=0, which the splice honours,
#                 so xray's egress sockets carry no fwmark (defect 2)
#
# and three coverage gaps closed later, each in its own file next to this one
# because each brings its own topology and its own controls:
#   procd, procd-no-restore              — mode-procd.sh       (the init.d path)
#   ipv6, ipv6-no-v6-route, ipv6-dual    — mode-ipv6.sh        (the v6 data path)
#   real-egress, real-egress-no-routing  — mode-real-egress.sh (the real nodes)
#   real-egress-own                      — mode-real-egress-own.sh (the same, with the router's own signed agent)
#   killswitch*                          — mode-killswitch.sh  (fail-closed, ARMED)
#   apply-local                          — mode-apply-local.sh (no panel-delivered config)
#   ui, ui-hold                          — mode-ui.sh          (the router UI, end to end)
#   setup, setup-hold                    — mode-setup.sh       (the setup wizard, the claim, the release)
#   passwall-real, passwall-real-no-switch — mode-passwall-real.sh (the REAL PassWall2 next to vctl:
#                                        trial, off, keep, deadman, midnight, reboots)

set -u

MODE="${MODE:-full}"
SKIP_INTERNET="${STAND_SKIP_INTERNET:-0}"

STAND=/stand
WORK=/var/run/vectra-controller-pro
CFG="$STAND/operator-config.json"
# The provider document under test. Modes override it: no-mark uses the
# operator-pinned mark:0 fixture, real-egress uses a live subscription entry.
PROVIDER=""
TPROXY_PORT=12345

CLIENT_NS=client
INET_NS=inet
CLIENT_IP=10.99.0.2
ROUTER_CLIENT_IP=10.99.0.1
CLIENT_NET=10.99.0.0/24
ROUTER_INET_IP=10.44.0.1
INET_PEER_IP=10.44.0.2
ORIGIN_IP=44.44.44.44
ORIGIN_DNS_IP=44.44.44.53
UDP_PORT=9999

EXPECT_BODY=vctl-dataplane-ok

FAILURES=0

say()   { printf '\n=== %s ===\n' "$*"; }
info()  { printf '     %s\n' "$*"; }
die()   { printf 'STAND-ABORT %s\n' "$*"; exit 90; }

# record <name> <PASS|FAIL> <detail...>
record() {
	_n=$1; _v=$2; shift 2
	printf 'RESULT %-22s %-4s %s\n' "$_n" "$_v" "$*"
	[ "$_v" = PASS ] || FAILURES=$((FAILURES + 1))
}

in_client() { ip netns exec "$CLIENT_NS" "$@"; }
in_inet()   { ip netns exec "$INET_NS" "$@"; }

# ctr <counter-name> -> packet count, or -1 when the counter does not exist
ctr() {
	_v=$(nft list counter inet vctl "$1" 2>/dev/null | awk '/packets/{print $2; exit}')
	[ -n "$_v" ] || _v=-1
	echo "$_v"
}

guard_ctr() {
	_v=$(nft list counter inet standguard leaked 2>/dev/null | awk '/packets/{print $2; exit}')
	[ -n "$_v" ] || _v=-1
	echo "$_v"
}

# ---------------------------------------------------------------- setup ----

setup_kernel() {
	echo 1 > /proc/sys/net/ipv4/ip_forward || die "cannot enable ip_forward"
	# TPROXY delivers packets whose destination is not local; strict reverse-path
	# filtering would discard them before the socket lookup.
	for f in /proc/sys/net/ipv4/conf/*/rp_filter; do
		echo 0 > "$f" 2>/dev/null || true
	done
}

setup_topology() {
	mkdir -p /var/run/netns
	ip netns add "$CLIENT_NS" || die "ip netns add $CLIENT_NS"
	ip netns add "$INET_NS"   || die "ip netns add $INET_NS"

	ip link add vclient type veth peer name vclient-c || die "veth client"
	ip link set vclient-c netns "$CLIENT_NS"
	ip addr add "$ROUTER_CLIENT_IP/24" dev vclient
	ip link set vclient up
	ip -n "$CLIENT_NS" link set lo up
	ip -n "$CLIENT_NS" addr add "$CLIENT_IP/24" dev vclient-c
	ip -n "$CLIENT_NS" link set vclient-c up
	ip -n "$CLIENT_NS" route add default via "$ROUTER_CLIENT_IP"

	ip link add vinet type veth peer name vinet-c || die "veth inet"
	ip link set vinet-c netns "$INET_NS"
	ip addr add "$ROUTER_INET_IP/30" dev vinet
	ip link set vinet up
	ip -n "$INET_NS" link set lo up
	ip -n "$INET_NS" addr add "$INET_PEER_IP/30" dev vinet-c
	ip -n "$INET_NS" link set vinet-c up
	ip -n "$INET_NS" addr add "$ORIGIN_IP/32" dev lo
	ip -n "$INET_NS" addr add "$ORIGIN_DNS_IP/32" dev lo
	ip -n "$INET_NS" route add default via "$ROUTER_INET_IP"

	ip route add "$ORIGIN_IP/32" via "$INET_PEER_IP" dev vinet
	ip route add "$ORIGIN_DNS_IP/32" via "$INET_PEER_IP" dev vinet
}

# The anti-false-pass control. Without it, a client packet could reach the
# origin by plain forwarding and every assertion would pass with the proxy
# completely dead.
setup_guard() {
	nft -f - <<EOF || die "guard table"
table inet standguard {
  counter leaked { }
  chain forward {
    type filter hook forward priority filter; policy accept;
    ip saddr $CLIENT_NET counter name "leaked" drop
    ip daddr $CLIENT_NET counter name "leaked" drop
  }
}
EOF
}

start_origins() {
	# HTTP
	in_inet socat "TCP-LISTEN:80,bind=$ORIGIN_IP,fork,reuseaddr" \
		"SYSTEM:$STAND/http-reply.sh" >/dev/null 2>&1 &
	# UDP echo
	in_inet socat "UDP-RECVFROM:$UDP_PORT,bind=$ORIGIN_IP,fork" PIPE >/dev/null 2>&1 &
	# HTTPS (self-signed; the stand asserts a completed TLS handshake, not PKI)
	in_inet sh -c "cd $STAND/tls && exec openssl s_server -accept $ORIGIN_IP:443 \
		-cert cert.pem -key key.pem -WWW -quiet" >/dev/null 2>&1 &
	# DNS
	in_inet dnsmasq -k -u root -p 53 -a "$ORIGIN_DNS_IP" --bind-interfaces \
		--no-hosts --no-resolv "--address=/origin.stand/$ORIGIN_IP" \
		--log-facility=/dev/null >/dev/null 2>&1 &

	_ok=0
	_i=0
	while [ "$_i" -lt 30 ]; do
		if in_inet curl -s --max-time 2 "http://$ORIGIN_IP/hello" 2>/dev/null | grep -q "$EXPECT_BODY"; then
			_ok=1; break
		fi
		_i=$((_i + 1)); sleep 1
	done
	[ "$_ok" = 1 ] || die "origin HTTP never came up inside the inet ns"
	info "origin services up in the inet ns (http/https/udp-echo/dns)"
}

# no-mark needs xray's egress sockets to carry NO fwmark. It used to get there
# with a provider document pinning "sockopt": {"mark": 0}, which the splice
# honoured. The splice no longer honours it: injectOutboundMark now REFUSES any
# provider-supplied mark that is not exactly the router's, and fails closed
# without writing. Good change — that trap is now a product guard rather than a
# stand fixture, and assert_splice_refuses_zero_mark covers it.
#
# But the loop detector still needs a control, and the product can no longer
# produce the wire state it detects. So the stand produces it: render through
# the SHIPPED path (`vctl supervise -dry-run`, i.e. ScanAllowInsecure → splice →
# `xray run -test` gate → atomic write), strip the sockopt the splice added to
# the OUTBOUNDS, and exec the real wrapper on the result with the same argv the
# supervisor uses. Real wrapper, real xray, identical wire behaviour — only the
# config is doctored, which is now the only route to it.
#
# The doctoring is self-checking: it targets the exact object the splice emits
# for an outbound (`,"streamSettings":{"sockopt":{"mark":N}}`) and not the
# inbound's, whose sockopt also carries "tproxy" and so cannot match. If the
# pattern ever stops matching, assert_splice_mark sees two marks instead of one
# and fails.
start_xray_unmarked() {
	mkdir -p "$WORK"
	info "rendering through the shipped path, then stripping the outbound sockopt"
	if ! vctl supervise -config "$CFG" -provider "$STAND/provider-marked.json" \
		-dry-run > "$WORK/supervise.log" 2>&1; then
		cat "$WORK/supervise.log"
		die "dry-run render failed"
	fi
	sed -i 's/,"streamSettings":{"sockopt":{"mark":[0-9]*}}//g' "$WORK/xray.json"
	/usr/sbin/vctl-xray-wrapper run -c "$WORK/xray.json" >> "$WORK/supervise.log" 2>&1 &
}

start_xray() {
	if [ "$MODE" = no-mark ]; then
		start_xray_unmarked
		_i=0
		while [ "$_i" -lt 40 ]; do
			netstat -lnt 2>/dev/null | grep -q ":$TPROXY_PORT " && return 0
			_i=$((_i + 1)); sleep 1
		done
		echo "--- supervise.log ---"; cat "$WORK/supervise.log" 2>/dev/null
		die "xray never listened on :$TPROXY_PORT"
	fi
	_provider="${PROVIDER:-$STAND/provider-marked.json}"
	mkdir -p "$WORK"
	info "provider document: $_provider"

	# vctl supervise = the shipped path: ScanAllowInsecure -> SpliceInbounds
	# (tproxy inbound + sockopt.mark injection) -> xray run -test gate ->
	# atomic write -> supervised exec of /usr/sbin/vctl-xray-wrapper.
	vctl supervise -config "$CFG" -provider "$_provider" \
		-status "$WORK/status.json" > "$WORK/supervise.log" 2>&1 &

	_i=0
	while [ "$_i" -lt 40 ]; do
		netstat -lnt 2>/dev/null | grep -q ":$TPROXY_PORT " && return 0
		_i=$((_i + 1)); sleep 1
	done
	echo "--- supervise.log ---"; cat "$WORK/supervise.log" 2>/dev/null
	die "xray never listened on :$TPROXY_PORT"
}

apply_firewall() {
	# FW_EXTRA: a mode's extra flags (ipv6-refused: -refuse-ipv6, the daemon's
	# refusal of the LAN's IPv6).
	# shellcheck disable=SC2086
	vctl firewall apply -config "$CFG" -yes ${FW_EXTRA:-} || die "vctl firewall apply failed"
	case "$MODE" in
	no-routing | real-egress-no-routing | killswitch-no-routing)
		info "MODE=$MODE: SKIPPING 'vctl firewall routing' (defect 1 reintroduced)"
		;;
	*)
		vctl firewall routing -config "$CFG" -yes
		;;
	esac
}

# ----------------------------------------------------------- assertions ----

# The splice is supposed to stamp sockopt.mark on every dialling outbound.
# Assert what actually landed in the config so the no-mark control is provably
# testing what it claims to test.
assert_splice_mark() {
	_cfg="$WORK/xray.json"
	[ -f "$_cfg" ] || { record splice_sockopt_mark FAIL "no $_cfg"; return; }
	# Expected in the full stand: 2 non-zero marks — the tproxy inbound and the
	# one dialling (freedom) outbound. blackhole is mark-exempt by design.
	# The provider's own bytes are re-emitted verbatim, whitespace included, so
	# an operator-pinned "mark": 0 keeps its formatting. Match tolerantly.
	_nonzero=$(tr ',' '\n' < "$_cfg" | grep -c '"mark": *[1-9]')
	_zero=$(tr ',' '\n' < "$_cfg" | grep -c '"mark": *0')
	if [ "$MODE" = no-mark ]; then
		# Exactly one mark left: the tproxy inbound's. Every dialling outbound
		# lost its sockopt to the strip in start_xray_unmarked, so this doubles
		# as the check that the strip hit what it was aiming at.
		if [ "$_nonzero" = 1 ] && [ "$_zero" = 0 ]; then
			record splice_sockopt_mark PASS "outbound egress unmarked (inbound still marked) — control is armed"
		else
			record splice_sockopt_mark FAIL "want exactly 1 non-zero and 0 zero marks, got $_nonzero/$_zero — the strip missed"
		fi
	else
		if [ "$_nonzero" -ge 2 ]; then
			record splice_sockopt_mark PASS "$_nonzero sockopt.mark stamped (inbound + dialling outbounds)"
		else
			record splice_sockopt_mark FAIL "splice stamped only $_nonzero sockopt.mark"
		fi
	fi
}

# A provider document that pins its own sockopt.mark must be REFUSED, and
# refused BEFORE anything is written, so a live router keeps its previous good
# config. The trap it closes: `"mark": 0` (or any value that is not the router's)
# silently un-marks xray's egress, which the output chain then steers back into
# TPROXY — the 85,150-packet loop.
#
# provider-unmarked.json is that document. It used to be the no-mark scenario's
# fixture, back when the splice honoured an operator-set mark; the product now
# fails closed on it instead, and this is where that is checked.
assert_splice_refuses_zero_mark() {
	_before=$(sha256sum "$WORK/xray.json" 2>/dev/null | awk '{print $1}')
	if vctl supervise -config "$CFG" -provider "$STAND/provider-unmarked.json" \
		-dry-run > "$WORK/refuse.log" 2>&1; then
		record splice_refuses_zero_mark FAIL "a provider pinning sockopt.mark=0 was ACCEPTED — xray's egress would be unmarked"
		return
	fi
	_after=$(sha256sum "$WORK/xray.json" 2>/dev/null | awk '{print $1}')
	if [ "$_before" != "$_after" ]; then
		record splice_refuses_zero_mark FAIL "refused, but the rendered config was overwritten anyway (not fail-closed)"
		return
	fi
	record splice_refuses_zero_mark PASS "$(grep -o 'refusing the document' "$WORK/refuse.log" >/dev/null && echo 'refused' || echo 'rejected'); rendered config untouched"
}

assert_tcp() {
	_out=$(in_client curl -sS --max-time 12 "http://$ORIGIN_IP/hello" 2>&1)
	if echo "$_out" | grep -q "$EXPECT_BODY"; then
		record tcp_through_proxy PASS "GET http://$ORIGIN_IP/hello -> $EXPECT_BODY"
	else
		record tcp_through_proxy FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

assert_tls() {
	_out=$(in_client curl -sSk --max-time 12 "https://$ORIGIN_IP/hello" 2>&1)
	if echo "$_out" | grep -q "$EXPECT_BODY"; then
		record tls_through_proxy PASS "TLS handshake + GET https://$ORIGIN_IP/hello"
	else
		record tls_through_proxy FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

assert_dns() {
	_out=$(in_client nslookup origin.stand "$ORIGIN_DNS_IP" 2>&1)
	if echo "$_out" | grep -q "$ORIGIN_IP"; then
		record dns_through_proxy PASS "origin.stand -> $ORIGIN_IP via $ORIGIN_DNS_IP"
	else
		record dns_through_proxy FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

assert_udp() {
	_out=$(in_client sh -c "echo udp-$EXPECT_BODY | socat -T6 - UDP4:$ORIGIN_IP:$UDP_PORT" 2>&1)
	if echo "$_out" | grep -q "udp-$EXPECT_BODY"; then
		record udp_through_proxy PASS "UDP echo $ORIGIN_IP:$UDP_PORT round-tripped"
	else
		record udp_through_proxy FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

# Packets reached the TPROXY rule at all. This catches "the ruleset was never
# applied" — and NOTHING ELSE.
#
# Read that again before trusting it: this counter is non-zero even when the
# data plane is completely dead. With `firewall routing` skipped it measured
# 43,241 packets while not one byte was carried, because the rule still MATCHES
# every client packet; it is the kernel's subsequent inability to DELIVER them
# that breaks. A stand that asserted only "counters are non-zero" would have
# passed the broken router. Delivery is asserted separately below.
assert_tproxy_counter() {
	_v=$(ctr vctl_tproxy_hits)
	if [ "$_v" -gt 0 ] 2>/dev/null; then
		record nft_tproxy_hits PASS "vctl_tproxy_hits=$_v packets matched the TPROXY rule"
	else
		record nft_tproxy_hits FAIL "vctl_tproxy_hits=$_v (ruleset never matched a packet)"
	fi
}

# Packets that entered TPROXY were actually DELIVERED to xray. xray only ever
# opens an outbound socket because it accepted a tproxied connection, so
# vctl_egress_exempt moving is proof of delivery. This is the counter check that
# fails when the fwmark policy route is missing.
assert_tproxy_delivered() {
	_v=$(ctr vctl_egress_exempt)
	if [ "$_v" -gt 0 ] 2>/dev/null; then
		record nft_tproxy_delivered PASS "vctl_egress_exempt=$_v — xray accepted and dialled out"
	else
		record nft_tproxy_delivered FAIL "vctl_egress_exempt=$_v — TPROXY matched but never delivered to xray"
	fi
}

# The loop detector. Around ONE small client request:
#
#   vctl_output_marked MUST NOT move — nothing of xray's egress fell through to
#     the marking rule at the bottom of the output chain, so nothing was steered
#     by the policy route into the local TPROXY socket. (Defect 2: unmarked
#     egress.)
#   vctl_tproxy_hits MUST stay bounded — a single HTTP GET is ~10 packets. If
#     xray's own egress is being re-captured, each packet re-enters PREROUTING
#     and the counter runs away. (Measured with the mark collision: 85,150
#     packets for a handful of requests, xray RSS 381 MiB.)
#
# An earlier version of this check asserted only "egress_exempt rises, marked
# stays flat" and PASSED during that 85k-packet loop — the loop makes
# egress_exempt rise. Amplification, not exemption, is the signal.
LOOP_BOUND=300
# real-egress does NOT reuse this: against a real provider both of the absolutes
# below have background traffic underneath them. See assert_no_egress_loop_real.
assert_no_egress_loop() {
	sleep 2 # let any in-flight retransmits drain so the window is quiet
	_ex0=$(ctr vctl_egress_exempt)
	_om0=$(ctr vctl_output_marked)
	_th0=$(ctr vctl_tproxy_hits)
	in_client curl -sS --max-time 12 -o /dev/null "http://$ORIGIN_IP/hello" 2>/dev/null
	sleep 2
	_dex=$(( $(ctr vctl_egress_exempt) - _ex0 ))
	_dom=$(( $(ctr vctl_output_marked) - _om0 ))
	_dth=$(( $(ctr vctl_tproxy_hits) - _th0 ))
	if [ "$_dom" -eq 0 ] && [ "$_dth" -lt "$LOOP_BOUND" ] && [ "$_dex" -gt 0 ]; then
		record no_egress_loop PASS "one request: tproxy_hits +$_dth (<$LOOP_BOUND), output_marked +$_dom, egress_exempt +$_dex"
	else
		record no_egress_loop FAIL "one request: tproxy_hits +$_dth (want <$LOOP_BOUND), output_marked +$_dom (want 0), egress_exempt +$_dex (want >0)"
	fi
}

# internal/firewall's `vctl_killswitch_drops` is NOT asserted here, on purpose.
# It is emitted only when Spec.KillSwitch is on, and every operator config in
# this mode sets killSwitch:false — the fleet default, fail-open-to-direct — so
# reading it here would return 0 forever and prove nothing. That assertion lives
# in mode-killswitch.sh (MODE=killswitch and its four controls), which runs the
# same stimulus against an armed ruleset.
#
# The two counters are not interchangeable and both are wanted. This guard is a
# stand-local table that DROPS the leak, so it proves a packet reached plain
# forwarding inside a topology where nothing else could have carried it. The
# shipped counter is what an operator can read on a live router, where the leak
# would actually have left over the WAN. Asserting only the stand's guard would
# leave the shipped rule itself untested; asserting only the shipped counter
# would trust the ruleset to police itself.
assert_no_leak() {
	_v=$(guard_ctr)
	if [ "$_v" = 0 ]; then
		record no_unproxied_leak PASS "guard forward counter 0 — nothing bypassed the proxy"
	else
		record no_unproxied_leak FAIL "guard forward counter $_v — client traffic hit plain forwarding"
	fi
}

assert_internet_tls() {
	_code=$(in_client curl -sS --max-time 20 -o /dev/null -w '%{http_code}' \
		--resolve one.one.one.one:443:1.1.1.1 https://one.one.one.one/ 2>&1)
	case "$_code" in
	2*|3*) record internet_tls PASS "https://one.one.one.one -> HTTP $_code" ;;
	*)     record internet_tls FAIL "$(echo "$_code" | tr '\n' ' ' | cut -c1-160)" ;;
	esac
}

assert_internet_dns() {
	_out=$(in_client nslookup example.com 1.1.1.1 2>&1)
	if echo "$_out" | grep -qE 'Address[^:]*: *[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+'; then
		record internet_dns PASS "example.com resolved through the proxy via 1.1.1.1"
	else
		record internet_dns FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

# ---------------------------------------------------------------- main -----

# The original v4 data-plane run: full / no-routing / no-mark.
run_v4_mode() {
	setup_kernel
	setup_topology
	setup_guard
	start_origins

	say "apply the ruleset with the SHIPPED code"
	apply_firewall
	nft list ruleset | sed -n '/table inet vctl/,/^}/p'
	echo "--- ip rule ---"; ip rule list
	echo "--- table 100 ---"; ip route show table 100 2>/dev/null || echo "(empty)"

	say "start xray through vctl supervise + vctl-xray-wrapper"
	start_xray
	info "$(grep -m1 'spliced provider config' "$WORK/supervise.log" 2>/dev/null || echo 'no splice line in log')"

	say "assertions"
	assert_splice_mark
	assert_splice_refuses_zero_mark
	assert_tcp
	assert_tls
	assert_dns
	assert_udp
	assert_tproxy_counter
	assert_tproxy_delivered
	assert_no_egress_loop
	assert_no_leak
	if [ "$SKIP_INTERNET" = 1 ]; then
		info "STAND_SKIP_INTERNET=1 — real-internet assertions skipped"
	else
		assert_internet_tls
		assert_internet_dns
	fi

	say "nft counters (table inet vctl)"
	nft list counters table inet vctl
	echo "--- guard (must stay 0) ---"
	nft list counter inet standguard leaked

	say "xray / supervisor log (tail)"
	tail -n 25 "$WORK/supervise.log" 2>/dev/null
}

say "MODE=$MODE  (OpenWrt $(sed -n "s/^DISTRIB_RELEASE='\(.*\)'/\1/p" /etc/openwrt_release 2>/dev/null), kernel $(uname -r))"
info "vctl: $(vctl version 2>/dev/null || echo '(no version subcommand)')"
info "xray: $(/usr/bin/xray version 2>/dev/null | head -1)"

case "$MODE" in
full|no-routing|no-mark)
	run_v4_mode
	;;
procd|procd-no-restore|procd-no-passwall-stop)
	. "$STAND/mode-procd.sh"
	run_procd_mode
	;;
ipv6|ipv6-dual|ipv6-no-v6-route|ipv6-killswitch|ipv6-no-ct-return|ipv6-refused|ipv6-refused-off|ipv6-refused-nolan)
	. "$STAND/mode-ipv6.sh"
	run_ipv6_mode
	;;
real-egress|real-egress-no-routing)
	. "$STAND/mode-real-egress.sh"
	run_real_egress_mode
	;;
real-egress-own)
	. "$STAND/mode-real-egress.sh"
	. "$STAND/mode-real-egress-own.sh"
	run_real_egress_own_mode
	;;
killswitch|killswitch-disarmed|killswitch-shadow|killswitch-no-routing|killswitch-policydrop)
	. "$STAND/mode-killswitch.sh"
	run_killswitch_mode
	;;
apply-local)
	. "$STAND/mode-apply-local.sh"
	run_apply_local_mode
	;;
ui|ui-hold)
	. "$STAND/mode-ui.sh"
	run_ui_mode
	;;
setup|setup-hold)
	. "$STAND/mode-setup.sh"
	run_setup_mode
	;;
passwall-real|passwall-real-no-switch)
	. "$STAND/mode-passwall-real.sh"
	run_passwall_real_mode
	;;
reports)
	. "$STAND/mode-reports.sh"
	run_reports_mode
	;;
failover|failover-no-watchdog|failover-stage)
	. "$STAND/mode-failover.sh"
	run_failover_mode
	;;
services)
	. "$STAND/mode-services.sh"
	run_services_mode
	;;
ru-kernel)
	. "$STAND/mode-rukernel.sh"
	run_rukernel_mode
	;;
exitcheck)
	. "$STAND/mode-exitcheck.sh"
	run_exitcheck_mode
	;;
*)
	die "unknown MODE=$MODE"
	;;
esac

printf '\nSTAND-EXIT %d failures=%d mode=%s\n' "$([ "$FAILURES" -eq 0 ] && echo 0 || echo 1)" "$FAILURES" "$MODE"
[ "$FAILURES" -eq 0 ] || exit 1
exit 0
