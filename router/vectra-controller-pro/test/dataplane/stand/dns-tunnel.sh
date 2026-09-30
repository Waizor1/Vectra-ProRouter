#!/bin/sh
# DNS through the tunnel, end to end: the real subscription, the real nodes,
# real dnsmasq, real nftables, the real daemon.
#
#   docker run --rm --privileged -e STAND_SUB_URL --entrypoint sh <image> /stand/dns-tunnel.sh
#
# The ISP here is a dnsmasq on 5.5.5.5 that FORGES NXDOMAIN for
# www.instagram.com and www.youtube.com — what the test router's ISP does, for
# queries to 8.8.8.8 and 1.1.1.1 as much as to its own resolver (measured
# 2026-09-29). The router's dnsmasq (as uid dnsmasq, like on a router) asks that
# ISP. Without vctl those names do not resolve; with vctl carrying they must, and
# not one query for them may reach the ISP; with the data plane unloaded the
# ISP's forgery must be back — the redirect lives and dies with the table.
#
# STAND_SUB_URL is a SECRET (the test account's): never echoed, never written,
# fetch errors never printed (a Go error quotes the URL).

set -u
PASS=0
FAIL=0
record() { # <name> <PASS|FAIL> <detail>
	printf 'RESULT %s %s %s\n' "$1" "$2" "$3"
	if [ "$2" = PASS ]; then PASS=$((PASS + 1)); else FAIL=$((FAIL + 1)); fi
}
info() { printf '     %s\n' "$*"; }
die() { printf 'STAND-ABORT %s\n' "$*"; exit 90; }

[ -n "${STAND_SUB_URL:-}" ] || die "STAND_SUB_URL is not set"

D=/tmp/dt
mkdir -p "$D" /var/run/vectra-controller-pro /var/lock
ISP_IP=5.5.5.5
SYN_MAC="02:00:00:de:ad:be"
SYN_MODEL="Vectra Dataplane Stand (synthetic)"
HWID=$(printf '%s-%s' "$SYN_MAC" "$SYN_MODEL" | sha256sum | awk '{print $1}')

# resolved <server> <name>: the first IPv4 address, or nothing.
resolved() {
	nslookup "$2" "$1" 2>/dev/null | awk '/^Name:/{n=1} n && /^Address/ {print $NF; exit}' | grep -E '^[0-9]+(\.[0-9]+){3}$'
}

# ----------------------------------------------------------------- the ISP --
modprobe nft_redir 2>/dev/null
ip link add isp0 type dummy 2>/dev/null
ip addr add "$ISP_IP/32" dev isp0 2>/dev/null
ip link set isp0 up
# As root, so no redirect by dnsmasq's uid ever touches the ISP's own lookups.
dnsmasq --user=root --pid-file="$D/isp.pid" --listen-address="$ISP_IP" --bind-interfaces \
	--no-resolv --server=8.8.8.8 --no-hosts --cache-size=0 \
	--address=/www.instagram.com/ --address=/www.youtube.com/ \
	--log-queries --log-facility="$D/isp.log" || die "the fake ISP resolver did not start"

# ------------------------------------------------------- the router's dnsmasq --
cat > "$D/lan.conf" <<EOF
user=dnsmasq
pid-file=$D/lan.pid
listen-address=127.0.0.1
bind-interfaces
no-resolv
server=$ISP_IP
cache-size=0
address=/vectra.lan/192.168.1.1
log-queries
log-facility=$D/lan.log
EOF
dnsmasq -C "$D/lan.conf" || die "the router's dnsmasq did not start"
printf 'nameserver 127.0.0.1\n' > /etc/resolv.conf
sleep 1

# The ISP's first lookups go out cold; give it a few tries, reading each
# answer once.
i=0 ig="" ex=""
while [ "$i" -lt 5 ]; do
	ig=$(resolved 127.0.0.1 www.instagram.com)
	ex=$(resolved 127.0.0.1 example.com)
	[ -z "$ig" ] && [ -n "$ex" ] && break
	i=$((i + 1)); sleep 2
done
if [ -z "$ig" ] && [ -n "$ex" ]; then
	record isp_forges_without_vctl PASS "without vctl: www.instagram.com does not resolve (the ISP's forged NXDOMAIN); example.com -> $ex"
else
	record isp_forges_without_vctl FAIL "the simulated ISP is not forging: instagram='$ig' example.com='$ex'"
	die "no forging ISP to test against"
fi

# ---------------------------------------------- the real subscription, signed --
cat > "$D/agent.json" <<EOF
{
  "controlUrl": "http://127.0.0.1:1",
  "panelUrl": "https://router.vectra-pro.net",
  "statePath": "$D/state.json",
  "statusPath": "$D/status.json",
  "xrayConfigPath": "$D/operator.json",
  "providerConfigPath": "$D/provider-config.json",
  "xrayRenderPath": "/var/run/vectra-controller-pro/xray.json",
  "xrayBinary": "/usr/sbin/vctl-xray-wrapper",
  "geoAssetDir": "/usr/share/v2ray",
  "legacyStatePath": "$D/no-legacy.json",
  "pollIntervalSeconds": 5,
  "requestTimeoutSeconds": 3
}
EOF
vctl agent -config "$D/agent.json" -once > "$D/first-start.log" 2>&1
[ -n "$(jsonfilter -i "$D/state.json" -e '@.device_private_key' 2>/dev/null)" ] || die "no device key after the first start"
_rel=$(sed -n "s/^DISTRIB_RELEASE='\(.*\)'/\1/p" /etc/openwrt_release 2>/dev/null)
vctl subscribe fetch -url "$STAND_SUB_URL/json" -signed -state "$D/state.json" \
	-hwid "$HWID" -mac "$SYN_MAC" -model "$SYN_MODEL" -os-release "$_rel" \
	-timeout 60 -retries 2 -out "$D/sub.json" 2>/dev/null || die "subscription fetch failed (error withheld)"
info "subscription: $(wc -c < "$D/sub.json") bytes (URL withheld)"

cat > "$D/operator.json" <<'EOF'
{
  "schema": 1,
  "instance": { "name": "dns-tunnel-stand", "logLevel": "warning" },
  "process": {
    "xrayBinary": "/usr/sbin/vctl-xray-wrapper",
    "workDir": "/var/run/vectra-controller-pro",
    "restartBackoff": { "initialMs": 500, "factor": 2, "maxMs": 60000, "reset": "60s" },
    "reloadGrace": "5s",
    "startTimeout": "15s"
  },
  "inbounds": {
    "tproxy": {
      "listenIP": "0.0.0.0", "port": 12345, "fwmark": 1, "udpEnabled": true, "tag": "tproxy-in",
      "killSwitch": false,
      "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": false }
    }
  },
  "geo": { "assetDir": "/usr/share/v2ray", "updateOnStart": false }
}
EOF

carrying=""
n=0
while [ "$n" -lt 6 ] && [ -z "$carrying" ]; do
	vctl subscribe parse -in "$D/sub.json" -content-type application/json -entry "$n" -out "$D/provider-config.json" 2>/dev/null || break
	rm -f /var/run/vectra-controller-pro/xray.json "$D/state.json.render" 2>/dev/null
	if ! vctl apply-local -config "$D/agent.json" -ignore-legacy-agent > "$D/apply-$n.log" 2>&1; then
		info "entry $n: apply-local failed: $(tail -n 2 "$D/apply-$n.log" | sed 's#https://[^ "]*#<url>#g' | tr '\n' ' ' | cut -c1-200)"
		n=$((n + 1)); continue
	fi
	if ! xrayport=$(jsonfilter -i /var/run/vectra-controller-pro/xray.json -e '@.inbounds[@.tag="vctl-dns-in"].port' 2>/dev/null) || [ "$xrayport" != 10053 ]; then
		record render_has_dns_inbound FAIL "entry $n: the render has no DNS inbound on 10053 ('$xrayport')"
		break
	fi
	vctl agent -config "$D/agent.json" > "$D/agent-$n.log" 2>&1 &
	AGENT=$!
	i=0
	while [ "$i" -lt 40 ]; do
		nft list chain inet vctl dns_steer >/dev/null 2>&1 && break
		i=$((i + 1)); sleep 1
	done
	touch /var/run/vectra-controller-pro/fw-confirm
	sleep 15 # the balancers' first probe round
	code=$(curl -s -m 20 -o /dev/null -w '%{http_code}' https://www.google.com/generate_204 2>/dev/null)
	case "$code" in
	2*|3*) carrying=$n ;;
	*) info "entry $n: no egress (HTTP '$code')"; kill "$AGENT" 2>/dev/null; sleep 3; nft delete table inet vctl 2>/dev/null; n=$((n + 1)) ;;
	esac
done
[ -n "$carrying" ] || die "no entry carried traffic"
info "entry $carrying carries: $(jsonfilter -i "$D/provider-config.json" -e '@.remarks' 2>/dev/null)"
record render_has_dns_inbound PASS "the render has vctl-dns-in on 127.0.0.1:10053 and the rule to vctl-dns-out"

# ------------------------------------------------------------------ steered --
if nft list chain inet vctl dns_steer 2>/dev/null | grep -q 'meta skuid 453 .*redirect to :10053'; then
	record redirect_by_dnsmasq_uid PASS "the table redirects uid 453's (dnsmasq's) port-53 queries to :10053"
else
	record redirect_by_dnsmasq_uid FAIL "$(nft list chain inet vctl dns_steer 2>&1 | tr '\n' ' ' | cut -c1-220)"
fi
isp_before=$(wc -l < "$D/isp.log")
ig=$(resolved 127.0.0.1 www.instagram.com)
yt=$(resolved 127.0.0.1 www.youtube.com)
if [ -n "$ig" ] && [ -n "$yt" ]; then
	record blocked_names_resolve PASS "through the tunnel: www.instagram.com -> $ig, www.youtube.com -> $yt"
else
	record blocked_names_resolve FAIL "instagram='$ig' youtube='$yt'; agent: $(grep -iE 'dns|error' "$D/agent-$carrying.log" | tail -n 3 | tr '\n' ' ' | cut -c1-240)"
fi
if tail -n +"$((isp_before + 1))" "$D/isp.log" | grep -qE 'query\[.*\] www\.(instagram|youtube)\.com'; then
	record isp_never_asked FAIL "the ISP was asked: $(tail -n +"$((isp_before + 1))" "$D/isp.log" | grep -E 'instagram|youtube' | head -2 | tr '\n' ' ')"
else
	record isp_never_asked PASS "not one query for them reached the ISP"
fi
red=$(nft list counter inet vctl vctl_dns_redirected 2>/dev/null | sed -n 's/.*packets \([0-9]*\).*/\1/p')
if [ "${red:-0}" -gt 0 ]; then
	record dns_redirect_counted PASS "vctl_dns_redirected: $red packets"
else
	record dns_redirect_counted FAIL "vctl_dns_redirected: '${red}'"
fi
if [ "$(resolved 127.0.0.1 vectra.lan)" = 192.168.1.1 ]; then
	record local_names_kept PASS "vectra.lan still answered by dnsmasq itself"
else
	record local_names_kept FAIL "vectra.lan -> '$(resolved 127.0.0.1 vectra.lan)'"
fi
aaaa=$(nslookup -type=aaaa www.google.com 127.0.0.1 2>/dev/null | awk '/^Name:/{n=1} n && /^Address/ {print $NF}' | grep ':' | head -1)
if [ -z "$aaaa" ]; then
	record ipv4_only_answers PASS "no AAAA through the tunnel (the provider's queryStrategy UseIPv4)"
else
	record ipv4_only_answers FAIL "AAAA answered: $aaaa"
fi
t0=$(date +%s)
nslookup -type=txt example.com 127.0.0.1 >/dev/null 2>&1
t1=$(date +%s)
if [ $((t1 - t0)) -le 3 ]; then
	record non_ip_query_answered PASS "a TXT query was answered in $((t1 - t0)) s (not left to time out)"
else
	record non_ip_query_answered FAIL "a TXT query took $((t1 - t0)) s"
fi
code=$(curl -s -m 20 -o /dev/null -w '%{http_code}' https://www.instagram.com/ 2>/dev/null)
case "$code" in
2*|3*) record instagram_opens PASS "https://www.instagram.com/ HTTP $code by its name" ;;
*) record instagram_opens FAIL "https://www.instagram.com/ HTTP '$code'" ;;
esac
# A resolver loop — xray resolving a node through dnsmasq, which asks xray —
# shows in dnsmasq as its forwarding table filling up, or as the nodes' names
# forwarded over and over. The test itself asks for www.instagram.com a handful
# of times (cache-size=0, A and AAAA each).
fw=$(grep -c 'forwarded www.instagram.com' "$D/lan.log")
nodes=$(grep -cE 'forwarded [^ ]*provider\.invalid' "$D/lan.log")
if ! grep -q 'Maximum number of concurrent' "$D/lan.log" && [ "$fw" -le 12 ] && [ "$nodes" -le 150 ]; then
	record no_resolver_loop PASS "www.instagram.com forwarded $fw time(s), node names $nodes; dnsmasq's table never filled"
else
	record no_resolver_loop FAIL "www.instagram.com forwarded $fw times, node names $nodes; $(grep -i 'Maximum number of concurrent' "$D/lan.log" | head -1)"
fi

# ------------------------------------------------- the DNS inbound stops answering --
# xray's DNS inbound unreachable (dropped on loopback): within two loops the
# redirect must be out — the LAN resolving over the open path, alive — and
# back as soon as the inbound answers again. The tunnel itself keeps working,
# so this is the redirect's own liveness and not the rescue's. (The panel is a
# dead port here: the confirm sentinel is kept, as a reachable panel would.)
( while :; do touch /var/run/vectra-controller-pro/fw-confirm; sleep 3; done ) &
CONFIRMER=$!
nft add table inet stand_dnsblock
nft add chain inet stand_dnsblock out '{ type filter hook output priority 0; policy accept; }'
nft add rule inet stand_dnsblock out ip daddr 127.0.0.1 udp dport 10053 drop
nft add rule inet stand_dnsblock out ip daddr 127.0.0.1 tcp dport 10053 drop
i=0
while [ "$i" -lt 90 ] && nft list chain inet vctl dns_steer >/dev/null 2>&1; do i=$((i + 1)); sleep 1; done
# How long until the LAN resolves again once the redirect is out: flows
# dnsmasq already had NATed to the inbound keep their binding in conntrack.
j=0 ex=""
while [ "$j" -lt 60 ]; do
	ex=$(resolved 127.0.0.1 example.com)
	[ -n "$ex" ] && break
	j=$((j + 1)); sleep 1
done
info "after the redirect went out, the first answer came after ${j}s"
if ! nft list chain inet vctl dns_steer >/dev/null 2>&1 && nft list table inet vctl >/dev/null 2>&1 && [ -n "$ex" ] && [ "$j" -le 15 ]; then
	record dead_inbound_loses_redirect PASS "the DNS inbound unreachable: the redirect went out after ${i}s, the data plane stayed, the LAN resolves ${j}s later (example.com -> $ex)"
else
	record dead_inbound_loses_redirect FAIL "after ${i}s: dns_steer $(nft list chain inet vctl dns_steer >/dev/null 2>&1 && echo present || echo absent), table $(nft list table inet vctl >/dev/null 2>&1 && echo present || echo absent), example.com='$ex' after ${j}s; conntrack for 10053: $(grep -c 'dport=10053\|sport=10053' /proc/net/nf_conntrack 2>/dev/null)"
fi
nft delete table inet stand_dnsblock
i=0
while [ "$i" -lt 90 ] && ! nft list chain inet vctl dns_steer >/dev/null 2>&1; do i=$((i + 1)); sleep 1; done
sleep 2
ig=$(resolved 127.0.0.1 www.instagram.com)
if nft list chain inet vctl dns_steer >/dev/null 2>&1 && [ -n "$ig" ]; then
	record inbound_back_gets_redirect PASS "the inbound answers again: the redirect returned after ${i}s, www.instagram.com -> $ig"
else
	record inbound_back_gets_redirect FAIL "after ${i}s: dns_steer $(nft list chain inet vctl dns_steer >/dev/null 2>&1 && echo present || echo absent), instagram='$ig'"
fi
if [ "$(nft list chain inet vctl output 2>/dev/null | grep -c 'udp dport { 53, 123 } return')" = 1 ]; then
	record reprogram_replaces PASS "after the reprograms the output chain holds each rule once"
else
	record reprogram_replaces FAIL "the output chain holds 'udp dport { 53, 123 } return' $(nft list chain inet vctl output 2>/dev/null | grep -c 'udp dport { 53, 123 } return') times"
fi

# ------------------------------------------------------------ tunnel dies --
# The tunnel dead (xray's own egress dropped), the line alive: the rescue must
# take the router direct by itself, and with the data plane the redirect.
nft add table inet stand_block
nft add chain inet stand_block out '{ type filter hook output priority 10; policy accept; }'
nft add rule inet stand_block out meta mark 0x5644 counter drop
i=0
while [ "$i" -lt 120 ] && nft list table inet vctl >/dev/null 2>&1; do i=$((i + 1)); sleep 1; done
# A lookup in flight at the moment of the switch can still be lost; the LAN
# must resolve within seconds of it.
j=0 ex=""
while [ "$j" -lt 30 ]; do
	ex=$(resolved 127.0.0.1 example.com)
	[ -n "$ex" ] && break
	j=$((j + 1)); sleep 1
done
info "after the switch to direct, the first answer came after ${j}s"
code=$(curl -s -m 15 -o /dev/null -w '%{http_code}' https://www.google.com/generate_204 2>/dev/null)
if ! nft list table inet vctl >/dev/null 2>&1 && [ -n "$ex" ] && [ "$j" -le 15 ] && [ "$code" = 204 ]; then
	record dead_tunnel_goes_direct PASS "tunnel dead, line up: direct after ${i}s — no table, no redirect; example.com -> $ex (${j}s after), google 204 straight out"
else
	record dead_tunnel_goes_direct FAIL "after ${i}s: table $(nft list table inet vctl >/dev/null 2>&1 && echo present || echo absent), example.com='$ex', google '$code'; $(grep -i rescue "$D/agent-$carrying.log" | tail -2 | tr '\n' ' ' | cut -c1-200)"
fi
nft delete table inet stand_block 2>/dev/null
kill "$CONFIRMER" 2>/dev/null

# --------------------------------------------------------------- fail-open --
kill "$AGENT" 2>/dev/null
i=0
while [ "$i" -lt 15 ] && kill -0 "$AGENT" 2>/dev/null; do i=$((i + 1)); sleep 1; done
if nft list table inet vctl >/dev/null 2>&1; then
	record stop_takes_redirect FAIL "the table (and the redirect) outlived the daemon"
	nft delete table inet vctl 2>/dev/null
else
	record stop_takes_redirect PASS "the daemon's stop took the table, and the redirect with it"
fi
# dnsmasq may still be waiting on a query the stop cut short; give it a few
# tries, and read each answer once.
i=0 ig="" ex=""
while [ "$i" -lt 5 ]; do
	ig=$(resolved 127.0.0.1 www.instagram.com)
	ex=$(resolved 127.0.0.1 example.com)
	[ -z "$ig" ] && [ -n "$ex" ] && break
	i=$((i + 1)); sleep 2
done
if [ -z "$ig" ] && [ -n "$ex" ]; then
	record isp_back_after_stop PASS "after the stop dnsmasq asks the ISP again: www.instagram.com forged NXDOMAIN, example.com -> $ex"
else
	record isp_back_after_stop FAIL "after the stop: instagram='$ig' example.com='$ex'"
fi
# A table programmed twice is the same table: each rule once.
vctl firewall apply -config "$D/operator.json" -yes >/dev/null 2>&1
vctl firewall apply -config "$D/operator.json" -yes >/dev/null 2>&1
n=$(nft list chain inet vctl prerouting 2>/dev/null | grep -c 'tproxy to :12345')
if [ "$n" = 1 ]; then
	record apply_twice_same_table PASS "two applies in a row: the tproxy rule is there once"
else
	record apply_twice_same_table FAIL "two applies in a row: the tproxy rule is there $n times"
fi
nft delete table inet vctl 2>/dev/null

if grep -rqF "$STAND_SUB_URL" "$D"/*.log /var/run/vectra-controller-pro 2>/dev/null; then
	record url_not_leaked FAIL "the subscription URL is in the stand's logs"
else
	record url_not_leaked PASS "the subscription URL is in no log and no render"
fi

echo "DNS-TUNNEL $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
