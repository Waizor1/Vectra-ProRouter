#!/bin/sh
# Gap 4 — the kill switch, ARMED.
#
# Every other configuration in this stand sets `killSwitch: false`, the fleet
# default. So the fail-closed path had never been exercised: `internal/firewall`
# renders a `drop` prerouting policy, a terminal `counter ... drop` at two hook
# points and a `vctl_killswitch_drops` counter that no scenario could ever make
# move. Reading that counter in the existing modes returns 0 forever, which is
# the false negative the TODO in stand.sh warned about.
#
# This matters more than the other gaps. With the switch off a mistake costs the
# ROUTER its proxy and degrades to direct. With the switch armed a mistake costs
# the whole LAN its internet — and, if the control-plane exemption were wrong,
# the operator's ability to undo it remotely.
#
# MODE=killswitch              the armed ruleset. Everything must PASS.
# MODE=killswitch-disarmed     the armed config, with the kill switch's terminal
#                              drops (tcp/udp and the rest, at both hook points)
#                              SURGICALLY removed after apply and the prerouting
#                              policy put back to accept. Nothing else changes.
#                              The leak assertions must FAIL.
# MODE=killswitch-shadow       the SHIPPED fleet default (killSwitch:false), run
#                              through the identical measurement. The leak
#                              assertions must FAIL, and `vctl_would_leak`
#                              quantifies what the fleet is losing today.
# MODE=killswitch-no-routing   armed, with `vctl firewall routing` skipped. The
#                              through-proxy assertions and the healthy-LAN
#                              assertion must FAIL — i.e. the stand can see a
#                              kill switch eating traffic it should have carried.
# MODE=killswitch-policydrop   armed, with the prerouting chain POLICY put back
#                              to drop and every rule left alone. That is the
#                              defect this mode found on its first run; it is
#                              kept as a control so the exemption assertions are
#                              shown failing on it.
#
# WHAT THE FOUR DROP POINTS ARE, and which mode reaches which:
#
#   prerouting fall-through — xray is DOWN, the TPROXY socket lookup NFT_BREAKs
#     out of the rule, its mark+accept never runs, and the packet lands on the
#     terminal `counter ... drop`. Phase C of every mode here.
#   forward guard          — xray is UP and captured the packet, but the fwmark
#     policy route is missing, so the kernel routes it out the WAN instead of to
#     the local TPROXY socket. PREROUTING already said `accept`, so only the
#     FORWARD hook can still see it. That is `killswitch-no-routing`.
#
# THE ANTI-VACUITY PROBLEM, and how each assertion answers it.
#
# "A counter stayed at 0" and "a counter went up" are both trivially satisfiable
# by a stand that is not measuring anything, and this project has shipped both
# mistakes. So:
#
#   ks_leak_blocked_at_forward is the load-bearing one, and it does NOT read a
#     vctl counter at all. It reads the stand's OWN guard table — the same
#     anti-false-pass forward guard the whole stand rests on — and requires that
#     not one client packet reached plain forwarding. In `killswitch-shadow` and
#     `killswitch-disarmed` that same guard counts the packets, which is the
#     direct demonstration that the topology CAN deliver a leak to plain
#     forwarding and that the armed ruleset is what stopped it. Same box, same
#     stimulus, same second.
#   ks_drops_when_xray_down reads `vctl_killswitch_drops` by name, because that
#     is what an operator can read on a live router. Its threshold is relative
#     to a measured idle window with an absolute floor, never a bare count.
#   ks_lan_not_dropped_when_healthy is a "stayed near 0" assertion, so it is
#     armed twice over: it fails unless the window it measured actually carried
#     client packets (vctl_tproxy_hits moved), and `killswitch-no-routing` shows
#     it going FAIL when the LAN really is being eaten.
#   ks_ctl_survives_killswitch is armed by ks_ctl_mark_load_bearing, which
#     severs exactly that mark in a stand-local chain and requires the same probe
#     to stop arriving. A probe that could never fail would prove nothing.
#
# The control plane is a stand-local fake panel at 44.44.44.80:8080 in the inet
# namespace. 44.44.44.0/24 is outside every bypass set in DefaultSpec, so it is
# "the internet" as far as the ruleset is concerned and the CtlMark exemption is
# the only thing that can get a packet to it. Nothing here talks to production.

PANEL_IP=44.44.44.80
PANEL_PORT=8080
KS_PANEL_LOG=/tmp/ks-panel-hits.log

# A service ON the router, reachable from the LAN. It exercises
# `fib daddr type { local, broadcast, multicast }` in the prerouting chain —
# the exemption that stands for SSH, LuCI, dnsmasq and DHCP on a real unit.
ROUTER_SVC_PORT=8081
# A destination inside DefaultSpec's bypass4 (192.168.0.0/16) that is NOT the
# router, so it can only be reached by plain FORWARDING. It exercises
# `ip daddr @bypass4` — LAN-to-LAN and inter-VLAN routing on a real unit.
BYPASS_IP=192.168.77.77

# One window length for every measurement, so an idle window and a stimulus
# window are directly comparable.
KS_WINDOW=6
# The deterministic part of the leak stimulus: N single-datagram UDP sends, one
# socat process each, so the packet count is not at the mercy of TCP retransmit
# timing. The floor below is derived from it rather than picked.
KS_UDP_PKTS=20

# Filled in by ks_detect_marks from the LIVE ruleset rather than hardcoded, so
# they cannot drift from firewall.DefaultControlMark / config.DefaultXraySockMark.
CTL_MARK=""
CTL_MARK_DEC=""
SOCK_MARK=""
# The kill-switch counter THIS mode's ruleset emits: vctl_killswitch_drops when
# armed, vctl_would_leak when off. Two names by design — see CounterKillSwitchShadow.
KILL_CTR=""

# ------------------------------------------------------------------ helpers ---

ks_kill_ctr() { ctr "$KILL_CTR"; }

ks_guard_named() { # <counter> -> packets, or -1
	_v=$(nft list counter inet standguard "$1" 2>/dev/null | awk '/packets/{print $2; exit}')
	[ -n "$_v" ] || _v=-1
	echo "$_v"
}

ks_armed() { [ "$MODE" != killswitch-shadow ]; }

# The mark values come out of the applied ruleset. If the output chain ever
# stops leading with the control-plane exemption this dies loudly instead of
# silently probing with the wrong number.
ks_detect_marks() {
	CTL_MARK=$(nft list chain inet vctl output 2>/dev/null \
		| awk '$1=="meta" && $2=="mark" && $3 ~ /^0x/ {print $3; exit}')
	SOCK_MARK=$(nft list chain inet vctl output 2>/dev/null \
		| awk '$1=="meta" && $2=="mark" && $3 ~ /^0x/ {n++; if (n==2) {print $3; exit}}')
	[ -n "$CTL_MARK" ] || die "could not read the control-plane mark out of chain inet vctl output"
	[ -n "$SOCK_MARK" ] || die "could not read xray's socket mark out of chain inet vctl output"
	[ "$CTL_MARK" != "$SOCK_MARK" ] || die "CtlMark and SockMark are the same value ($CTL_MARK)"
	CTL_MARK_DEC=$(( CTL_MARK ))
	[ "$CTL_MARK_DEC" -gt 1 ] || die "CtlMark ($CTL_MARK) collides with the tproxy fwmark"
	info "marks read from the live ruleset: CtlMark=$CTL_MARK ($CTL_MARK_DEC) SockMark=$SOCK_MARK"
	if ks_armed; then KILL_CTR=vctl_killswitch_drops; else KILL_CTR=vctl_would_leak; fi
}

# -------------------------------------------------------------------- setup ---

# The fake panel. Deliberately on 44.44.44.0/24 (outside every bypass set), so
# reaching it is evidence of the CtlMark exemption and not of a bypass rule.
ks_start_panel() {
	: > "$KS_PANEL_LOG"
	export KS_PANEL_LOG
	ip -n "$INET_NS" addr add "$PANEL_IP/32" dev lo
	ip route add "$PANEL_IP/32" via "$INET_PEER_IP" dev vinet
	in_inet socat "TCP-LISTEN:$PANEL_PORT,bind=$PANEL_IP,fork,reuseaddr" \
		"SYSTEM:$STAND/panel-reply.sh" >/dev/null 2>&1 &

	_i=0
	while [ "$_i" -lt 20 ]; do
		in_inet sh -c "printf 'GET /ks-warmup HTTP/1.0\r\n\r\n' | socat -T2 - TCP4:$PANEL_IP:$PANEL_PORT" >/dev/null 2>&1
		grep -q '/ks-warmup ' "$KS_PANEL_LOG" 2>/dev/null && {
			info "fake panel up at $PANEL_IP:$PANEL_PORT (inet ns), logging to $KS_PANEL_LOG"
			return 0
		}
		_i=$((_i + 1)); sleep 1
	done
	die "fake panel never answered inside the inet ns"
}

# Stand-local observation of the control-plane path, in the guard table so it is
# never confused with the shipped ruleset:
#
#   ctlobserve (priority 0) counts packets toward the panel by mark, which is how
#     the stand knows its own probe really carried SO_MARK — a harness control.
#   ctlkill (priority 10) is empty until ks_sever_ctl fills it. It is the teeth
#     for ks_ctl_survives_killswitch: sever exactly that mark and the same probe
#     must stop arriving.
#
# Both run AFTER the shipped output chain (type route hook output priority
# mangle, -150), so the mark they see is the mark that chain left behind.
ks_setup_ctl_guard() {
	nft add counter inet standguard ctl_marked   || die "ctl_marked counter"
	nft add counter inet standguard ctl_unmarked || die "ctl_unmarked counter"
	nft add counter inet standguard ctl_severed  || die "ctl_severed counter"
	nft add chain inet standguard ctlobserve \
		'{ type filter hook output priority 0; policy accept; }' || die "ctlobserve chain"
	nft add chain inet standguard ctlkill \
		'{ type filter hook output priority 10; policy accept; }' || die "ctlkill chain"
	nft add rule inet standguard ctlobserve ip daddr "$PANEL_IP" \
		meta mark "$CTL_MARK" counter name '"ctl_marked"' || die "ctl_marked rule"
	nft add rule inet standguard ctlobserve ip daddr "$PANEL_IP" \
		meta mark != "$CTL_MARK" counter name '"ctl_unmarked"' || die "ctl_unmarked rule"
}

ks_sever_ctl() {
	nft add rule inet standguard ctlkill ip daddr "$PANEL_IP" \
		meta mark "$CTL_MARK" counter name '"ctl_severed"' drop || die "sever rule"
}
ks_restore_ctl() { nft flush chain inet standguard ctlkill; }

# A service on the router itself, and a bypass destination behind it.
#
# The stand's guard drops ALL forwarding for the client subnet, which is what
# makes "the origin answered" mean "xray carried it". A bypass destination is
# supposed to be plain-forwarded, so it needs an explicit carve-out — with its
# own counter, so the exception is visible in the log rather than hidden. The
# `leaked` counter keeps its meaning for every other destination.
ks_start_local_services() {
	socat "TCP-LISTEN:$ROUTER_SVC_PORT,bind=$ROUTER_CLIENT_IP,fork,reuseaddr" \
		"SYSTEM:$STAND/http-reply.sh" >/dev/null 2>&1 &

	ip -n "$INET_NS" addr add "$BYPASS_IP/32" dev lo
	ip route add "$BYPASS_IP/32" via "$INET_PEER_IP" dev vinet
	in_inet socat "TCP-LISTEN:80,bind=$BYPASS_IP,fork,reuseaddr" \
		"SYSTEM:$STAND/http-reply.sh" >/dev/null 2>&1 &

	nft add counter inet standguard bypass_fwd || die "bypass_fwd counter"
	nft insert rule inet standguard forward ip saddr "$BYPASS_IP" counter name '"bypass_fwd"' accept
	nft insert rule inet standguard forward ip daddr "$BYPASS_IP" counter name '"bypass_fwd"' accept

	_i=0
	while [ "$_i" -lt 20 ]; do
		if curl -s --max-time 2 "http://$ROUTER_CLIENT_IP:$ROUTER_SVC_PORT/hello" 2>/dev/null | grep -q "$EXPECT_BODY" \
			&& in_inet curl -s --max-time 2 "http://$BYPASS_IP/hello" 2>/dev/null | grep -q "$EXPECT_BODY"; then
			info "router service up on $ROUTER_CLIENT_IP:$ROUTER_SVC_PORT; bypass origin up on $BYPASS_IP (carved out of the stand guard)"
			return 0
		fi
		_i=$((_i + 1)); sleep 1
	done
	die "the router service or the bypass origin never came up"
}

# A port forward from the WAN to a server on the LAN. Its answers are the reply
# direction of a connection the WAN opened: not the LAN's traffic, not a leak,
# and nothing xray carries — so they must pass the armed switch, with xray up
# and with xray dead. The server sees the WAN client's own address (no
# masquerade), so the client's is a public one on no bypass list.
KS_WAN_CLIENT=44.44.44.47
KS_FWD_TCP=8080
KS_FWD_UDP=8081
ks_start_port_forward() {
	ip -n "$INET_NS" addr add "$KS_WAN_CLIENT/32" dev lo
	ip route add "$KS_WAN_CLIENT/32" via "$INET_PEER_IP" dev vinet
	in_client socat "TCP-LISTEN:$KS_FWD_TCP,bind=$CLIENT_IP,fork,reuseaddr" 'SYSTEM:echo "lan server saw $SOCAT_PEERADDR"' >/dev/null 2>&1 &
	in_client socat "UDP-RECVFROM:$KS_FWD_UDP,bind=$CLIENT_IP,fork" 'SYSTEM:echo "lan server saw $SOCAT_PEERADDR"' >/dev/null 2>&1 &
	nft -f - <<EOF2 || die "the port forward's table"
table ip standfwd {
  chain pre {
    type nat hook prerouting priority dstnat; policy accept;
    ip daddr $ROUTER_INET_IP tcp dport $KS_FWD_TCP dnat to $CLIENT_IP:$KS_FWD_TCP
    ip daddr $ROUTER_INET_IP udp dport $KS_FWD_UDP dnat to $CLIENT_IP:$KS_FWD_UDP
  }
}
EOF2
	# A carve-out of the stand guard with its own counter, like the bypass one.
	nft add counter inet standguard portfwd || die "portfwd counter"
	nft insert rule inet standguard forward ip saddr "$KS_WAN_CLIENT" counter name '"portfwd"' accept
	nft insert rule inet standguard forward ip daddr "$KS_WAN_CLIENT" counter name '"portfwd"' accept
	info "port forward $ROUTER_INET_IP:$KS_FWD_TCP/tcp and :$KS_FWD_UDP/udp -> $CLIENT_IP, WAN client $KS_WAN_CLIENT (carved out of the stand guard)"
}
ks_fwd_tcp() { in_inet socat -T5 - "TCP4:$ROUTER_INET_IP:$KS_FWD_TCP,bind=$KS_WAN_CLIENT" 2>&1 < /dev/null | tr '\n' ' '; }
# One datagram each way: under load one may be lost, so a second try — a
# forward that is broken (the controls) fails both.
ks_fwd_udp() {
	for _try in 1 2; do
		_o=$(echo ping | in_inet socat -T4 - "UDP4:$ROUTER_INET_IP:$KS_FWD_UDP,bind=$KS_WAN_CLIENT" 2>&1 | tr '\n' ' ')
		case "$_o" in *"lan server saw"*) break ;; esac
	done
	echo "$_o"
}
# assert_ks_port_forward <name> <when>
assert_ks_port_forward() {
	_want="lan server saw $KS_WAN_CLIENT"
	_t=$(ks_fwd_tcp)
	_u=$(ks_fwd_udp)
	case "$_t|$_u" in
	*"$_want"*"|"*"$_want"*) record "$1" PASS "$2: a TCP and a UDP port forward from the WAN reached the LAN server and its answers came back" ;;
	*) record "$1" FAIL "$2: tcp '$(echo "$_t" | cut -c1-80)', udp '$(echo "$_u" | cut -c1-80)'" ;;
	esac
}

# The defect this mode was written after finding: the armed prerouting chain used
# to carry `policy drop` while its exemptions were spelled `return`. In an
# nftables base chain `return` falls through to the POLICY, so every exemption
# became a silent drop and the router black-holed itself. Put it back, and
# nothing else, so the assertions that cover it are shown to have teeth.
ks_restore_policy_drop() {
	nft chain inet vctl prerouting '{ type filter hook prerouting priority mangle; policy drop; }' \
		|| die "policydrop: could not set the prerouting policy back to drop"
	_p=$(nft list chain inet vctl prerouting | sed -n 's/.*policy \([a-z]*\);.*/\1/p' | head -1)
	[ "$_p" = drop ] || die "policydrop: policy is '$_p' after the injection"
	info "MODE=$MODE: prerouting policy set back to drop (the historical defect), every rule left untouched"
}

# The defect for killswitch-disarmed: the kill switch's terminal drops, and the
# prerouting policy, and NOTHING else. The drops are two per hook point since
# the counting was split — tcp/udp in vctl_killswitch_drops, the rest (a LAN
# ping) in vctl_unproxied_other — and all four go. Everything the switch shares
# with the fleet default — the TPROXY rule, the exemptions, the escape counter,
# the counters themselves — is left exactly where `vctl firewall apply` put it,
# so a failure afterwards can only be the fail-closed verdict going missing.
ks_disarm_killswitch() {
	for _chain in prerouting forward; do
		for _ctr in vctl_killswitch_drops vctl_unproxied_other; do
			_h=$(nft -a list chain inet vctl "$_chain" | awk -v rule="counter name \"$_ctr\" drop" 'index($0, rule) {print $NF; exit}')
			[ -n "$_h" ] || die "disarm: no '$_ctr drop' in chain $_chain to remove"
			nft delete rule inet vctl "$_chain" handle "$_h" || die "disarm: $_chain delete failed"
		done
	done
	nft chain inet vctl prerouting '{ type filter hook prerouting priority mangle; policy accept; }' \
		|| die "disarm: could not put the prerouting policy back to accept"
	_left=$(nft list table inet vctl | grep -cE 'counter name "(vctl_killswitch_drops|vctl_unproxied_other)" drop')
	[ "$_left" -eq 0 ] || die "disarm: $_left terminal drops survived the surgery"
	info "MODE=$MODE: all four terminal drops (vctl_killswitch_drops and vctl_unproxied_other, prerouting + forward) removed, prerouting policy back to accept"
}

# The fleet's real failure is xray being OOM-killed, so kill it the same way:
# SIGKILL, supervisor first so it cannot respawn what we just removed.
ks_stop_xray() {
	for _p in $(ps w 2>/dev/null | grep '[v]ctl supervise' | awk '{print $1}'); do
		kill -9 "$_p" 2>/dev/null
	done
	for _p in $(ps w 2>/dev/null | grep -E '[v]ctl-xray-wrapper|[x]ray run' | awk '{print $1}'); do
		kill -9 "$_p" 2>/dev/null
	done
	_i=0
	while [ "$_i" -lt 20 ]; do
		netstat -lnt 2>/dev/null | grep -q ":$TPROXY_PORT " || {
			info "xray SIGKILLed; nothing listens on :$TPROXY_PORT after ${_i}s"
			return 0
		}
		_i=$((_i + 1)); sleep 1
	done
	die "xray still listening on :$TPROXY_PORT after SIGKILL"
}

# ------------------------------------------------------------------- probes ---

# ONE control-plane probe, through the SHIPPED dialer, which conveniently
# carries its own control with it.
#
# `vctl agent -once` opens two different kinds of connection to the same host in
# the same second:
#
#   /api/router/register (or /check-in) goes through controlplane.Client, whose
#     net.Dialer.Control stamps SO_MARK = firewall.DefaultControlMark BEFORE
#     connect (internal/controlplane/sockmark_linux.go). This is the path the
#     nft output chain exempts.
#   /healthz and /api/health are evaluateHealth's reachability probes, built on a
#     bare &http.Client{} with NO mark (cmd/vctl/cmd_agent.go).
#
# Same process, same destination, same instant, differing only in the socket
# option. That is a far better control than anything the stand could stage — and
# it is why this does not use socat: socat's setsockopt-int is applied at
# phase=CONNECTED, i.e. AFTER connect(), so the SYN goes out unmarked and the
# probe measures the wrong thing. Measured: 4 marked / 8 unmarked packets for one
# request.
#
# Isolated paths keep the run inert: xrayRenderPath points at a file that does
# not exist, which is what stops daemon.run starting a second supervisor and
# re-programming the firewall out from under the stand. Verified: after a run the
# only tables present are the ones the stand put there.
KS_AGENT_CFG=/tmp/ks-agent/agent.json

ks_write_agent_cfg() {
	mkdir -p /tmp/ks-agent
	cat > "$KS_AGENT_CFG" <<EOF
{
  "controlUrl": "http://$PANEL_IP:$PANEL_PORT",
  "statePath": "/tmp/ks-agent/state.json",
  "statusPath": "/tmp/ks-agent/status.json",
  "xrayConfigPath": "/tmp/ks-agent/desired.json",
  "xrayRenderPath": "/tmp/ks-agent/does-not-exist.json",
  "pollIntervalSeconds": 2,
  "requestTimeoutSeconds": 5
}
EOF
}

# `grep -c` prints 0 AND exits 1 when there is no match, so `|| echo 0` would
# emit "0\n0" and every arithmetic expansion downstream would abort the run.
ks_count_in_panel_log() {
	_v=$(grep -cE "$1" "$KS_PANEL_LOG" 2>/dev/null)
	[ -n "$_v" ] || _v=0
	echo "$_v"
}
ks_panel_hits() { ks_count_in_panel_log '/api/router/'; }

# The control for ks_ctl_survives_killswitch: the SAME destination, over an
# ORDINARY socket, issued by the stand itself.
#
# It used to come free with the agent run — evaluateHealth probed /healthz and
# /api/health on a bare &http.Client{}, so one `vctl agent -once` produced both
# a marked and an unmarked request to the same host in the same second. That
# stopped being true on purpose: those probes now travel the control-plane path,
# because measuring "can this router reach its panel" on a socket the router's
# own output chain black-holes made serverReachable structurally false on every
# router with the data plane loaded.
#
# So the stand issues the unmarked request itself. curl from the router
# namespace needs no socket option to be unmarked, which is why this direction
# does not have socat's setsockopt-at-phase-CONNECTED problem.
KS_UNMARKED_HITS=0
ks_unmarked_probe() {
	_b=$(ks_count_in_panel_log '/ks-unmarked')
	curl -sS --max-time 5 "http://$PANEL_IP:$PANEL_PORT/ks-unmarked" >/dev/null 2>&1
	sleep 1
	KS_UNMARKED_HITS=$(( $(ks_count_in_panel_log '/ks-unmarked') - _b ))
}

# Runs the shipped dialer once and leaves the deltas in KS_AGENT_*.
# Returns 0 when a control-plane request actually reached the panel.
ks_agent_probe() { # <tag>
	_b=$(ks_panel_hits); _bm=$(ks_guard_named ctl_marked)
	vctl agent -once -config "$KS_AGENT_CFG" > "/tmp/ks-agent/run-$1.log" 2>&1
	KS_AGENT_HITS=$(( $(ks_panel_hits) - _b ))
	KS_AGENT_MARKPKTS=$(( $(ks_guard_named ctl_marked) - _bm ))
	[ "$KS_AGENT_HITS" -gt 0 ]
}

# Client traffic that the proxy is SUPPOSED to carry. Deliberately more than one
# request, so "the counter did not move" is measured against a window that
# actually had a LAN in it — and the successes are COUNTED, because the first
# version of this mode measured only the counter and reported PASS while the
# router was black-holing every packet. A policy drop increments nothing.
KS_BURST_N=8
KS_BURST_OK=0
ks_client_burst() {
	KS_BURST_OK=0
	_i=0
	while [ "$_i" -lt "$KS_BURST_N" ]; do
		if in_client curl -sS --max-time 8 "http://$ORIGIN_IP/hello" 2>/dev/null | grep -q "$EXPECT_BODY"; then
			KS_BURST_OK=$((KS_BURST_OK + 1))
		fi
		_i=$((_i + 1))
	done
	# Bidirectional (the same shape assert_udp uses), NOT the one-shot -u form of
	# ks_leak_burst. A one-shot sender exits the moment the datagram is away, so
	# the origin's echo lands on a closed port and the CLIENT answers with ICMP
	# port-unreachable — which the armed switch then drops, because ICMP is not
	# tcp/udp and TPROXY cannot carry it. Measured: +6 on vctl_killswitch_drops in
	# a healthy window, entirely from that artefact (since the counting was split
	# it lands in vctl_unproxied_other instead). Keeping the reply socket open
	# removes it, and the ICMP behaviour is measured deliberately instead
	# (ks_icmp_note) rather than smuggled into a tolerance.
	_i=0
	while [ "$_i" -lt "$KS_UDP_PKTS" ]; do
		in_client sh -c "echo ks-burst | socat -T4 - UDP4:$ORIGIN_IP:$UDP_PORT" >/dev/null 2>&1
		_i=$((_i + 1))
	done
}

# Not an assertion: a measured note. With the switch armed, ICMP from a LAN
# client is dropped by the terminal rule, because the TPROXY rule only matches
# tcp/udp and TPROXY has no way to carry ICMP anyway. That means `ping` from a
# LAN client stops working the moment an operator arms the switch. It is inherent
# to the design rather than a defect, so the stand reports the number and lets an
# operator decide, instead of asserting a requirement nobody has stated. The
# ICMP is counted in vctl_unproxied_other, apart from the tcp/udp instrument, so
# a ping no longer reads as a leak (or a kill-switch drop).
ks_icmp_note() {
	_k0=$(ks_kill_ctr); _o0=$(ctr vctl_unproxied_other)
	in_client ping -c 5 -W 1 "$ORIGIN_IP" >/dev/null 2>&1
	sleep 1
	info "ICMP note: 5 pings from the LAN with xray HEALTHY -> vctl_unproxied_other +$(( $(ctr vctl_unproxied_other) - _o0 )), $KILL_CTR +$(( $(ks_kill_ctr) - _k0 )). TPROXY matches only tcp/udp, so ICMP falls to the terminal rule for the rest."
}

# The same client, after xray has died: exactly KS_UDP_PKTS datagrams (no
# retransmit ambiguity) plus one HTTP request whose failure is what a user sees.
ks_leak_burst() {
	_i=0
	while [ "$_i" -lt "$KS_UDP_PKTS" ]; do
		in_client sh -c "echo ks-leak | socat -u - UDP4-SENDTO:$ORIGIN_IP:$UDP_PORT" 2>/dev/null
		_i=$((_i + 1))
	done
	KS_LEAK_CURL=$(in_client curl -sS --max-time 6 "http://$ORIGIN_IP/hello" 2>&1)
}

# --------------------------------------------------------------- assertions ---

# `vctl firewall apply` used to ignore the operator's killSwitch setting outright
# (see loadSpec in cmd/vctl/cmd_firewall.go). Before anything else, check that
# the ruleset on this box is the one the operator config asked for — otherwise
# every assertion below is about some other ruleset.
#
# BOTH modes require `policy accept` on every chain. The kill-switch's verdict is
# the terminal counted drop, never the policy, because in an nftables base chain
# a `return` falls through to the POLICY rather than accepting — so `policy drop`
# silently converts all five exemptions above it into uncounted drops. The stand
# measured exactly that (see the mode header); `killswitch-policydrop` is the
# control that keeps it measured.
assert_ks_ruleset_matches_config() {
	_want=$(sed -n 's/.*"killSwitch": *\(true\|false\).*/\1/p' "$CFG" | head -1)
	[ -n "$_want" ] || _want=false
	_pol=$(nft list chain inet vctl prerouting 2>/dev/null | sed -n 's/.*policy \([a-z]*\);.*/\1/p' | head -1)
	_polf=$(nft list chain inet vctl forward 2>/dev/null | sed -n 's/.*policy \([a-z]*\);.*/\1/p' | head -1)
	_drops=$(nft list table inet vctl 2>/dev/null | grep -c 'counter name "vctl_killswitch_drops" drop')
	_shadow=$(nft list table inet vctl 2>/dev/null | grep -c 'counter name "vctl_would_leak"')
	_shadowdrop=$(nft list table inet vctl 2>/dev/null | grep -c 'counter name "vctl_would_leak" drop')
	# Everything but tcp/udp is counted apart, with the same verdict; the escape
	# counter (captured, then forwarded) has no verdict in either mode.
	_other=$(nft list table inet vctl 2>/dev/null | grep -c 'counter name "vctl_unproxied_other"')
	_otherdrop=$(nft list table inet vctl 2>/dev/null | grep -c 'counter name "vctl_unproxied_other" drop')
	_escaped=$(nft list table inet vctl 2>/dev/null | grep -c 'counter name "vctl_tproxy_escaped"$')
	if [ "$_pol" != accept ] || [ "$_polf" != accept ]; then
		record ks_ruleset_matches_config FAIL \
			"chain policies are prerouting=$_pol forward=$_polf; both must be accept or every 'return' exemption becomes an uncounted drop"
		return
	fi
	if [ "$_want" = true ]; then
		if [ "$_drops" -eq 2 ] && [ "$_shadow" -eq 0 ] && [ "$_otherdrop" -eq 2 ] && [ "$_escaped" -eq 1 ]; then
			record ks_ruleset_matches_config PASS "config killSwitch=true -> 2 terminal 'vctl_killswitch_drops ... drop' + 2 'vctl_unproxied_other drop', verdict-free vctl_tproxy_escaped, policies accept, no shadow counter"
		else
			record ks_ruleset_matches_config FAIL "config killSwitch=true but terminal_drops=$_drops (want 2) other_drops=$_otherdrop (want 2) escaped_refs=$_escaped (want 1, no verdict) shadow_refs=$_shadow (want 0)"
		fi
	else
		if [ "$_shadow" -eq 2 ] && [ "$_shadowdrop" -eq 0 ] && [ "$_drops" -eq 0 ] && [ "$_other" -eq 2 ] && [ "$_otherdrop" -eq 0 ] && [ "$_escaped" -eq 1 ]; then
			record ks_ruleset_matches_config PASS "config killSwitch=false -> 2 verdict-free 'vctl_would_leak' + 2 'vctl_unproxied_other', verdict-free vctl_tproxy_escaped, policies accept, no drop counter"
		else
			record ks_ruleset_matches_config FAIL "config killSwitch=false but shadow_refs=$_shadow shadow_with_drop=$_shadowdrop drop_refs=$_drops other_refs=$_other other_with_drop=$_otherdrop escaped_refs=$_escaped"
		fi
	fi
}

# (d) THE FALSE-POSITIVE DIRECTION: with the switch armed and xray healthy the
# LAN must not be touched. A kill switch that drops working traffic is the
# failure mode that takes a customer's internet down with no defect to point at.
#
# IT IS NOT ENOUGH TO READ THE COUNTER. The first version of this assertion did
# exactly that and reported PASS while the armed ruleset was dropping every
# packet in the topology, because the drops were happening at the chain POLICY
# and a policy decision increments nothing. So the assertion requires DELIVERY:
# all KS_BURST_N requests in the window must have come back with the expected
# body. The counter check rides along on top of that.
#
# Armed three ways. Inside: delivery, plus a window that provably carried
# packets (tproxy_hits moved). Across modes: `killswitch-no-routing` and
# `killswitch-policydrop` both make it FAIL.
assert_ks_lan_not_dropped_when_healthy() {
	_tol=$(( KS_IDLE_HEALTHY * 3 ))
	[ "$_tol" -lt 2 ] && _tol=2
	if [ "$KS_BURST_OK" -eq "$KS_BURST_N" ] && [ "$KS_HEALTHY_HITS" -gt 0 ] \
		&& [ "$KS_HEALTHY_DROPS" -le "$_tol" ]; then
		record ks_lan_not_dropped_when_healthy PASS \
			"$KS_BURST_OK/$KS_BURST_N client requests delivered in ${KS_HEALTHY_SECS}s (tproxy_hits +$KS_HEALTHY_HITS); $KILL_CTR +$KS_HEALTHY_DROPS (<= $_tol; idle floor +$KS_IDLE_HEALTHY over ${KS_WINDOW}s)"
	else
		record ks_lan_not_dropped_when_healthy FAIL \
			"$KS_BURST_OK/$KS_BURST_N delivered (want all), tproxy_hits +$KS_HEALTHY_HITS (want >0), $KILL_CTR +$KS_HEALTHY_DROPS (want <= $_tol)"
	fi
}

# The exemptions the armed chain has to honour, driven with real packets.
#
# These two exist because the armed ruleset used to drop both. `return` in an
# nftables base chain falls through to the chain POLICY, so under the old
# `policy drop` the router stopped answering on its own address and stopped
# forwarding to bypass destinations — silently, with vctl_killswitch_drops at 0.
# On a real unit that is SSH, LuCI, dnsmasq, DHCP and LAN-to-LAN routing, all
# gone the moment an operator armed the switch.
#
# `killswitch-policydrop` puts that policy back and nothing else, so both of
# these are shown failing on the exact defect they were written for.
assert_ks_router_itself_reachable() {
	_out=$(in_client curl -sS --max-time 8 "http://$ROUTER_CLIENT_IP:$ROUTER_SVC_PORT/hello" 2>&1)
	if echo "$_out" | grep -q "$EXPECT_BODY"; then
		record ks_router_itself_reachable PASS \
			"a LAN client still reaches a service ON the router ($ROUTER_CLIENT_IP:$ROUTER_SVC_PORT) with the switch armed"
	else
		record ks_router_itself_reachable FAIL \
			"the router stopped answering on its own address with the switch armed — SSH/LuCI/DNS/DHCP would be gone: $(echo "$_out" | tr '\n' ' ' | cut -c1-110)"
	fi
}

assert_ks_bypass_forwarded() {
	_out=$(in_client curl -sS --max-time 8 "http://$BYPASS_IP/hello" 2>&1)
	if echo "$_out" | grep -q "$EXPECT_BODY"; then
		record ks_bypass_forwarded PASS \
			"traffic to a bypass4 destination ($BYPASS_IP) is still forwarded, not proxied and not dropped (bypass_fwd $(ks_guard_named bypass_fwd) pkts)"
	else
		record ks_bypass_forwarded FAIL \
			"a bypass4 destination became unreachable with the switch armed — LAN-to-LAN routing would be dead: $(echo "$_out" | tr '\n' ' ' | cut -c1-110)"
	fi
}

# (b) THE ASSERTION THAT JUSTIFIES THE FEATURE, part 1: the shipped counter an
# operator reads on a live router moves when xray is down.
#
# Threshold: three times the measured idle rate, floored at half the
# deterministic stimulus. Never a bare absolute count — the measured idle floor
# is printed alongside so a reader can see the margin rather than trust it.
assert_ks_drops_when_xray_down() {
	if [ "$(ctr vctl_killswitch_drops)" -lt 0 ]; then
		record ks_drops_when_xray_down FAIL \
			"counter vctl_killswitch_drops does not exist in table inet vctl — this ruleset cannot fail closed"
		return
	fi
	_need=$(( KS_IDLE_DROPS * 3 ))
	[ "$_need" -lt $(( KS_UDP_PKTS / 2 )) ] && _need=$(( KS_UDP_PKTS / 2 ))
	if [ "$KS_DOWN_DROPS" -ge "$_need" ]; then
		record ks_drops_when_xray_down PASS \
			"xray down, ${KS_UDP_PKTS} UDP + 1 GET from the LAN: vctl_killswitch_drops +$KS_DOWN_DROPS (>= $_need; idle floor +$KS_IDLE_DROPS over ${KS_WINDOW}s)"
	else
		record ks_drops_when_xray_down FAIL \
			"vctl_killswitch_drops +$KS_DOWN_DROPS, want >= $_need (idle floor +$KS_IDLE_DROPS)"
	fi
}

# (b) part 2, and the load-bearing one. It reads the STAND's guard, not a vctl
# counter: on a real router the client packet would have left over the WAN, and
# the only thing in this topology that can carry it there is plain forwarding —
# which the guard drops and counts. Zero here means the shipped ruleset killed
# the packet BEFORE it ever got a routing decision that could have leaked it.
#
# Its teeth are two whole modes: `killswitch-shadow` and `killswitch-disarmed`
# run the identical stimulus and this counter DOES move there.
assert_ks_leak_blocked_at_forward() {
	if [ "$KS_DOWN_LEAK" -eq 0 ] && [ "$KS_IDLE_GUARD" -eq 0 ]; then
		record ks_leak_blocked_at_forward PASS \
			"not one of the ${KS_UDP_PKTS}+ leaking packets reached plain forwarding (standguard leaked +0)"
	else
		record ks_leak_blocked_at_forward FAIL \
			"standguard leaked +$KS_DOWN_LEAK during the stimulus (+$KS_IDLE_GUARD while idle) — client traffic reached plain forwarding and would have left the WAN in the clear"
	fi
}

# The instrument itself, in whichever spelling this mode's ruleset emits. It is
# the same rule at the same two hook points; only the verdict differs. PASSing in
# both `killswitch` and `killswitch-shadow` is what makes the shadow counter
# usable as the documented rollout gate ("run a week, read this counter").
# `killswitch-disarmed` is where it fails, because there the rules are gone.
assert_ks_leak_instrument() {
	_need=$(( KS_UDP_PKTS / 2 ))
	if [ "$KS_DOWN_KILLCTR" -ge "$_need" ]; then
		record ks_leak_instrument PASS \
			"$KILL_CTR +$KS_DOWN_KILLCTR for a ${KS_UDP_PKTS}-datagram leak (>= $_need) — the exposure is measurable in this ruleset"
	else
		record ks_leak_instrument FAIL \
			"$KILL_CTR +$KS_DOWN_KILLCTR, want >= $_need — the leak passed through uncounted"
	fi
}

# What the user actually experiences. Armed by phase A: the identical request to
# the identical origin succeeded a minute earlier on the same box.
assert_ks_client_blocked_when_xray_down() {
	if echo "$KS_LEAK_CURL" | grep -q "$EXPECT_BODY"; then
		record ks_client_blocked_when_xray_down FAIL \
			"the client still fetched $EXPECT_BODY from the origin with xray dead — the traffic escaped"
	else
		record ks_client_blocked_when_xray_down PASS \
			"client GET failed with xray dead: $(echo "$KS_LEAK_CURL" | tr '\n' ' ' | cut -c1-90)"
	fi
}

# (c) THE RECOVERABILITY ASSERTION. Worst case on purpose: the switch is armed,
# xray is dead and the LAN is being dropped packet by packet. If the router
# cannot reach the panel in that state, an operator cannot undo it remotely and
# the unit needs a site visit.
assert_ks_ctl_survives_killswitch() {
	if ks_agent_probe survive && [ "$KS_AGENT_MARKPKTS" -gt 0 ]; then
		record ks_ctl_survives_killswitch PASS \
			"vctl agent delivered $KS_AGENT_HITS /api/router/ request(s) on CtlMark ($KS_AGENT_MARKPKTS packets, mark $CTL_MARK) with the switch armed, xray dead and the LAN dropping"
	else
		record ks_ctl_survives_killswitch FAIL \
			"panel saw $KS_AGENT_HITS control-plane request(s), $KS_AGENT_MARKPKTS CtlMark packets — a remote router would be unrecoverable: $(tail -n 2 /tmp/ks-agent/run-survive.log 2>/dev/null | tr '\n' ' ' | cut -c1-120)"
	fi
}

# The control for the assertion above: the identical destination with the one
# socket option removed. It must NOT arrive — if an ordinary socket reaches the
# panel too, the marked request's success proves nothing about the exemption,
# because everything is simply getting out of this box.
#
# This used to ride along on the agent run itself, because evaluateHealth probed
# /healthz and /api/health on a bare http.Client. That was changed deliberately:
# measuring "can this router reach its panel" on a socket the router's own output
# chain black-holes made serverReachable structurally false on every router with
# the data plane loaded. The control is now the stand's own request, which is
# where a harness control belongs anyway.
assert_ks_ctl_unmarked_blocked() {
	ks_unmarked_probe
	if [ "$KS_UNMARKED_HITS" -eq 0 ]; then
		record ks_ctl_unmarked_blocked PASS \
			"an UNMARKED request from the router to the same panel did not arrive — only the CtlMark path gets out"
	else
		record ks_ctl_unmarked_blocked FAIL \
			"$KS_UNMARKED_HITS unmarked request(s) also reached the panel — the CtlMark exemption is not what carried the control plane here"
	fi
}

# The teeth for ks_ctl_survives_killswitch. Sever exactly that mark in a
# stand-local chain and require the identical probe to stop arriving. If it still
# arrived, either the probe cannot detect a severed control plane or its packets
# never carried CtlMark — and in both cases the assertion above was worthless.
assert_ks_ctl_mark_load_bearing() {
	_s0=$(ks_guard_named ctl_severed)
	ks_sever_ctl
	if ks_agent_probe severed; then
		ks_restore_ctl
		record ks_ctl_mark_load_bearing FAIL \
			"the probe reached the panel with 'meta mark $CTL_MARK drop' in the output path — it is not measuring the CtlMark path"
		return
	fi
	_s1=$(ks_guard_named ctl_severed)
	ks_restore_ctl
	if [ "$_s1" -gt "$_s0" ]; then
		record ks_ctl_mark_load_bearing PASS \
			"severing $CTL_MARK stopped the control plane (ctl_severed $_s0 -> $_s1) — the probe really is the CtlMark path, and it can report failure"
	else
		record ks_ctl_mark_load_bearing FAIL \
			"the probe failed but the sever rule counted nothing ($_s0 -> $_s1) — it failed for some other reason"
	fi
}

# --------------------------------------------------------------------- main ---

run_killswitch_mode() {
	CFG="$STAND/operator-config-killswitch.json"
	[ "$MODE" = killswitch-shadow ] && CFG="$STAND/operator-config.json"
	PROVIDER="$STAND/provider-marked.json"
	info "operator config: $CFG (killSwitch $(sed -n 's/.*"killSwitch": *\(true\|false\).*/\1/p' "$CFG" | head -1))"

	setup_kernel
	setup_topology
	setup_guard
	start_origins
	ks_start_panel
	ks_start_local_services
	ks_start_port_forward

	say "apply the ruleset with the SHIPPED code"
	apply_firewall
	case "$MODE" in
	killswitch-disarmed)  ks_disarm_killswitch ;;
	killswitch-policydrop) ks_restore_policy_drop ;;
	esac
	ks_detect_marks
	ks_setup_ctl_guard
	nft list ruleset | sed -n '/table inet vctl/,/^}/p'
	echo "--- ip rule ---"; ip rule list

	say "the ruleset is the one the operator config asked for"
	assert_ks_ruleset_matches_config

	say "start xray through vctl supervise + vctl-xray-wrapper"
	start_xray

	# ---------------------------------------------- phase A: healthy LAN ----
	say "phase A — the LAN with the switch armed and xray HEALTHY"
	_k0=$(ks_kill_ctr)
	sleep "$KS_WINDOW"
	KS_IDLE_HEALTHY=$(( $(ks_kill_ctr) - _k0 ))
	info "noise floor, LAN idle / xray up, ${KS_WINDOW}s: $KILL_CTR +$KS_IDLE_HEALTHY"

	_t0=$(date +%s)
	_k0=$(ks_kill_ctr); _h0=$(ctr vctl_tproxy_hits)
	assert_tcp
	assert_tls
	assert_dns
	assert_udp
	ks_client_burst
	sleep 1
	KS_HEALTHY_DROPS=$(( $(ks_kill_ctr) - _k0 ))
	KS_HEALTHY_HITS=$(( $(ctr vctl_tproxy_hits) - _h0 ))
	KS_HEALTHY_SECS=$(( $(date +%s) - _t0 ))
	assert_tproxy_counter
	assert_tproxy_delivered
	assert_ks_lan_not_dropped_when_healthy
	assert_ks_router_itself_reachable
	assert_ks_bypass_forwarded
	assert_ks_port_forward ks_port_forward_healthy "xray up"
	assert_no_leak
	# Last in the phase: it deliberately produces packets the armed switch drops
	# (and, with the switch off, packets that reach the stand guard), so it must
	# not run before the two absolute-counter assertions above.
	ks_icmp_note

	say "phase B — the control plane while everything is healthy"
	ks_write_agent_cfg
	if ks_agent_probe healthy; then
		record ks_ctl_reaches_panel PASS \
			"vctl agent reached the fake panel with the ruleset loaded ($KS_AGENT_HITS control-plane request(s), $KS_AGENT_MARKPKTS CtlMark packets)"
	else
		record ks_ctl_reaches_panel FAIL \
			"the router could not reach the panel even with xray up: $(tail -n 2 /tmp/ks-agent/run-healthy.log 2>/dev/null | tr '\n' ' ' | cut -c1-120)"
	fi

	# ------------------------------------------------- phase C: xray dead ----
	say "phase C — xray SIGKILLed: does the leak get dropped or does it escape?"
	ks_stop_xray

	_k0=$(ks_kill_ctr); _g0=$(guard_ctr); _d0=$(ctr vctl_killswitch_drops)
	sleep "$KS_WINDOW"
	KS_IDLE_KILLCTR=$(( $(ks_kill_ctr) - _k0 ))
	KS_IDLE_GUARD=$(( $(guard_ctr) - _g0 ))
	KS_IDLE_DROPS=$(( $(ctr vctl_killswitch_drops) - _d0 ))
	[ "$KS_IDLE_DROPS" -lt 0 ] && KS_IDLE_DROPS=0
	info "noise floor, LAN idle / xray down, ${KS_WINDOW}s: $KILL_CTR +$KS_IDLE_KILLCTR, standguard leaked +$KS_IDLE_GUARD"

	_k0=$(ks_kill_ctr); _g0=$(guard_ctr); _d0=$(ctr vctl_killswitch_drops)
	ks_leak_burst
	sleep 2
	KS_DOWN_KILLCTR=$(( $(ks_kill_ctr) - _k0 ))
	KS_DOWN_LEAK=$(( $(guard_ctr) - _g0 ))
	KS_DOWN_DROPS=$(( $(ctr vctl_killswitch_drops) - _d0 ))
	[ "$KS_DOWN_DROPS" -lt 0 ] && KS_DOWN_DROPS=0
	info "stimulus: $KS_UDP_PKTS UDP datagrams + 1 GET -> $KILL_CTR +$KS_DOWN_KILLCTR, standguard leaked +$KS_DOWN_LEAK"

	assert_ks_drops_when_xray_down
	assert_ks_leak_blocked_at_forward
	assert_ks_leak_instrument
	assert_ks_client_blocked_when_xray_down
	assert_ks_port_forward ks_port_forward_xray_down "xray dead"

	# ------------------------------- phase D: the management channel ---------
	say "phase D — the control plane with the switch holding and the LAN dropping"
	assert_ks_ctl_survives_killswitch
	assert_ks_ctl_unmarked_blocked
	assert_ks_ctl_mark_load_bearing

	say "nft counters (table inet vctl)"
	nft list counters table inet vctl
	echo "--- standguard ---"
	nft list counters table inet standguard
	echo "--- panel request log ---"
	cat "$KS_PANEL_LOG" 2>/dev/null

	say "xray / supervisor log (tail)"
	tail -n 15 "$WORK/supervise.log" 2>/dev/null
}
