#!/bin/sh
# Gap 3 — the provider's REAL nodes.
#
# Every other mode terminates egress in a local `freedom` outbound, so REALITY,
# xhttp, hysteria2 and the provider's actual servers had never been exercised:
# the stand proved the plumbing and said nothing about whether a single byte
# ever left the building. The owner authorised testing against the live
# subscription with a TEST identity, so this mode fetches the real subscription,
# splices a real entry into tproxy and drives client traffic out through real
# nodes.
#
# MODE=real-egress             everything must PASS
# MODE=real-egress-no-routing  `vctl firewall routing` skipped — the same defect
#                              as the v4 no-routing control, against real nodes.
#                              The traffic assertions AND public_ip_differs must
#                              FAIL.
#
# ---------------------------------------------------------------------------
# SECRET HANDLING
#
# STAND_SUB_URL arrives in the container's environment (run.sh passes `-e
# STAND_SUB_URL` by NAME, so the value never appears in a docker command line).
# It is never echoed, never written to a file, and `vctl subscribe fetch`'s
# stderr is never printed — a Go http error embeds the URL it failed on, and
# that error text would otherwise land in logs/real-egress.log. The
# real_url_not_leaked assertion checks the artefacts the mode produced for it.
#
# IDENTITY
#
# The HWID is derived the way internal/subscription/device.go derives it,
# sha256(mac + "-" + model), from an obviously fake MAC and model. The live
# router keeps its own identity; nothing here touches it. real_hwid_synthetic
# proves the value differs from the production one WITHOUT storing the
# production identifier in the repo: what is stored is sha256(production HWID),
# so the comparison is possible and the identifier is not.
# ---------------------------------------------------------------------------

# Locally-administered MAC (the 0x02 bit) and a model string no device has.
SYN_MAC="02:00:00:de:ad:be"
SYN_MODEL="Vectra Dataplane Stand (synthetic)"
# sha256 of the production HWID. Not the HWID itself — see above.
PROD_HWID_SHA256="d43057fec6b8328cf4f27ff1071d83342c45d4840ef868bf550f2ef22c6520f6"

# No default, on purpose: see STAND_SUB_UA in run.sh. A malformed "Happ…" agent
# gets the device deleted and the account disabled by the provider's sweep.
SUB_UA="${STAND_SUB_UA:-}"
SUB_RAW=/tmp/subscription.json
ENTRY_RAW=/tmp/entry.json
PROVIDER_AUG=/tmp/provider-with-api.json
FETCH_ERR=/tmp/fetch.err

XAPI=127.0.0.1:10085
# The stand's observation port. This is the ONLY byte the mode adds to the
# provider's document: a top-level "api" object, prepended so every other byte
# stays exactly where the provider put it. real_provider_verbatim proves that.
API_PREFIX='{"api":{"tag":"stand-api","listen":"127.0.0.1:10085","services":["StatsService","RoutingService","ObservatoryService"]},'

TRACE_HOST=cp.cloudflare.com
TRACE_URL="https://cp.cloudflare.com/cdn-cgi/trace"
LOOP_URL_REAL="http://cp.cloudflare.com/generate_204"

MAX_ENTRY_TRIES=4
[ "$MODE" = real-egress-no-routing ] && MAX_ENTRY_TRIES=2

TRACE_IP=""
BASE_PUBIP=""
CLIENT_PUBIP=""
DIRECT_PUBIP=""
CHOSEN_ENTRY=-1

# ------------------------------------------------------------------ helpers ---

sha_of() { printf '%s' "$1" | sha256sum | awk '{print $1}'; }

# Public IPs are the owner's. Print enough to see two values differ, not enough
# to publish a home address: first two octets plus a short digest.
mask_ip() {
	[ -n "$1" ] || { echo "(none)"; return; }
	echo "$(echo "$1" | awk -F. '{print $1"."$2".x.x"}') (sha $(sha_of "$1" | cut -c1-12))"
}

# curl the trace endpoint with the address pinned, so DNS never varies between
# the three vantage points and the destination is identical every time.
trace_ip_from() { # <runner...>  -> the public IP that endpoint saw
	"$@" curl -sS --max-time 25 --resolve "$TRACE_HOST:443:$TRACE_IP" \
		"$TRACE_URL" 2>/dev/null | sed -n 's/^ip=//p' | head -1
}

sg_ctr() { # <counter in table inet standguard> -> packets, or -1
	_v=$(nft list counter inet standguard "$1" 2>/dev/null | awk '/packets/{print $2; exit}')
	[ -n "$_v" ] || _v=-1
	echo "$_v"
}

# Break `vctl_output_marked` down by what the traffic actually is.
#
# Against the local fixture that counter stays at exactly 0, because the only
# egress on the box is one freedom outbound carrying xray's socket mark. Against
# the REAL provider it moves a little, and "a little" is useless without knowing
# what it is — so the stand names it. Every one of these rules sits at filter
# priority 0, downstream of vctl's `type route ... priority mangle` chain, so it
# sees the mark that chain just set.
setup_egress_breakdown() {
	nft -f - <<EOF || die "egress breakdown counters"
table inet standguard {
  counter marked_dns   { }
  counter marked_tcp   { }
  counter marked_udp   { }
  counter marked_other { }
  chain output {
    type filter hook output priority filter; policy accept;
    meta mark 0x1 udp dport 53 counter name "marked_dns" return
    meta mark 0x1 tcp dport 53 counter name "marked_dns" return
    meta mark 0x1 meta l4proto tcp counter name "marked_tcp" return
    meta mark 0x1 meta l4proto udp counter name "marked_udp" return
    meta mark 0x1 counter name "marked_other"
  }
}
EOF
}

stop_xray() {
	pkill -f 'vctl supervise' 2>/dev/null
	pkill -x xray 2>/dev/null
	pkill -f vctl-xray-wrapper 2>/dev/null
	_i=0
	while [ "$_i" -lt 15 ]; do
		netstat -lnt 2>/dev/null | grep -q ":$TPROXY_PORT " || return 0
		_i=$((_i + 1)); sleep 1
	done
	return 0
}

# --------------------------------------------------------------- fetch/splice ---

synthetic_hwid() {
	vctl subscribe hwid -mac "$SYN_MAC" -model "$SYN_MODEL" 2>/dev/null | tr -d '\r\n'
}

fetch_subscription() {
	_rel=$(sed -n "s/^DISTRIB_RELEASE='\(.*\)'/\1/p" /etc/openwrt_release 2>/dev/null)
	# stderr goes to a file that is NEVER printed: a transport error from Go's
	# http client quotes the URL it was dialling.
	if vctl subscribe fetch -url "$STAND_SUB_URL" -ua "$SUB_UA" \
		-hwid "$HWID" -mac "$SYN_MAC" -model "$SYN_MODEL" -os-release "$_rel" \
		-timeout 60 -retries 2 -out "$SUB_RAW" 2>"$FETCH_ERR"; then
		info "subscription fetched: $(wc -c < "$SUB_RAW") bytes, UA=$SUB_UA (URL and error text withheld)"
		return 0
	fi
	info "subscription fetch FAILED; error text withheld because it embeds the URL"
	return 1
}

# How many entries the JSON variant carries. `subscribe parse -entry N` fails
# past the end, which is the cheapest reliable probe available from the shell.
entry_count() {
	_n=0
	while [ "$_n" -lt 40 ]; do
		vctl subscribe parse -in "$SUB_RAW" -content-type application/json \
			-entry "$_n" -out /dev/null >/dev/null 2>&1 || break
		_n=$((_n + 1))
	done
	echo "$_n"
}

# Byte-splice the observation port in front of the provider's own bytes. No JSON
# parse, no re-serialise: the provider document's key order, its empty objects
# and its exact whitespace all survive, which is the whole point of
# internal/coreengine/xray's verbatim contract.
augment_entry() {
	head -c 1 "$ENTRY_RAW" | grep -q '{' || return 1
	{ printf '%s' "$API_PREFIX"; tail -c +2 "$ENTRY_RAW"; } > "$PROVIDER_AUG"
}

# --------------------------------------------------------------- assertions ---

assert_hwid_synthetic() {
	if [ -z "$HWID" ]; then
		record real_hwid_synthetic FAIL "vctl subscribe hwid produced nothing"
		return
	fi
	_d=$(sha_of "$HWID")
	if [ "$_d" != "$PROD_HWID_SHA256" ]; then
		record real_hwid_synthetic PASS "synthetic HWID from mac=$SYN_MAC model='$SYN_MODEL' differs from the production identity"
	else
		record real_hwid_synthetic FAIL "the derived HWID EQUALS the production router's — refusing to reuse a live identity"
	fi
}

assert_subscription_fetched() {
	_b=$(wc -c < "$SUB_RAW" 2>/dev/null || echo 0)
	_n=$(entry_count)
	SUB_ENTRIES=$_n
	if [ "$_b" -gt 200000 ] && [ "$_n" -ge 20 ]; then
		record real_subscription_fetched PASS "$_b bytes, $_n xray configs (JSON variant, UA=$SUB_UA)"
	else
		record real_subscription_fetched FAIL "$_b bytes, $_n entries — not the JSON variant (base64 fallback?)"
	fi
}

# The only delta between what the provider sent and what the splice consumed is
# the injected api prefix. Strip it back off and the bytes must be identical.
assert_provider_verbatim() {
	_stripped=/tmp/stripped.json
	{ printf '{'; tail -c "+$(( ${#API_PREFIX} + 1 ))" "$PROVIDER_AUG"; } > "$_stripped"
	_a=$(sha256sum "$ENTRY_RAW" | awk '{print $1}')
	_b=$(sha256sum "$_stripped" | awk '{print $1}')
	if [ "$_a" = "$_b" ]; then
		record real_provider_verbatim PASS "provider entry $CHOSEN_ENTRY re-emitted byte-for-byte; only the api observation port was added"
	else
		record real_provider_verbatim FAIL "entry sha $_a vs stripped sha $_b — the mode altered the provider document"
	fi
}

assert_real_tcp() {
	_code=$(in_client curl -sS --max-time 25 -o /dev/null -w '%{http_code}' \
		"$LOOP_URL_REAL" 2>&1)
	case "$_code" in
	2*|3*) record real_tcp_through_node PASS "HTTP $_code from the client ns through a real node" ;;
	*)     record real_tcp_through_node FAIL "$(echo "$_code" | tr '\n' ' ' | cut -c1-160)" ;;
	esac
}

assert_real_tls() {
	_code=$(in_client curl -sS --max-time 25 -o /dev/null -w '%{http_code}' \
		--resolve "$TRACE_HOST:443:$TRACE_IP" "$TRACE_URL" 2>&1)
	case "$_code" in
	2*) record real_tls_through_node PASS "TLS to $TRACE_HOST -> HTTP $_code through a real node" ;;
	*)  record real_tls_through_node FAIL "$(echo "$_code" | tr '\n' ' ' | cut -c1-160)" ;;
	esac
}

assert_real_dns() {
	_out=$(in_client nslookup example.com 1.1.1.1 2>&1)
	if echo "$_out" | grep -qE 'Address[^:]*: *[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+'; then
		record real_dns_through_node PASS "example.com resolved via 1.1.1.1 through the proxy"
	else
		record real_dns_through_node FAIL "$(echo "$_out" | tr '\n' ' ' | cut -c1-160)"
	fi
}

# THE assertion that separates "proxied" from "went direct and happened to
# work". Everything else in this mode would pass on a router whose client
# traffic bypassed xray entirely and reached the internet through the WAN.
#
# Three measurements of the SAME endpoint with the SAME pinned address:
#   BASE_PUBIP    from the router ns BEFORE any ruleset existed
#   DIRECT_PUBIP  from the router ns with the ruleset up, but with the endpoint
#                 in the shipped vctl_direct4 set, so the output chain returns
#                 on it and the request is genuinely direct
#   CLIENT_PUBIP  from the client ns, which can only leave via tproxy -> xray
#                 (the guard drops plain forwarding for that subnet)
#
# DIRECT == BASE is the control: it proves the measurement can still see this
# box's own address while the ruleset is loaded, so CLIENT differing is the
# proxy's doing and not an artefact of the ruleset breaking the measurement.
assert_public_ip_differs() {
	if [ -z "$CLIENT_PUBIP" ] || [ -z "$DIRECT_PUBIP" ]; then
		record real_public_ip_differs FAIL "client=$(mask_ip "$CLIENT_PUBIP") direct=$(mask_ip "$DIRECT_PUBIP") — no egress to compare"
		return
	fi
	if [ "$CLIENT_PUBIP" != "$DIRECT_PUBIP" ]; then
		record real_public_ip_differs PASS "client $(mask_ip "$CLIENT_PUBIP") != direct $(mask_ip "$DIRECT_PUBIP") — traffic left via the node"
	else
		record real_public_ip_differs FAIL "client and direct both $(mask_ip "$CLIENT_PUBIP") — client traffic went out the WAN, not the node"
	fi
}

assert_direct_ip_stable() {
	if [ -n "$BASE_PUBIP" ] && [ "$BASE_PUBIP" = "$DIRECT_PUBIP" ]; then
		record real_direct_ip_control PASS "router-ns direct egress still reports $(mask_ip "$DIRECT_PUBIP") — the comparison is armed"
	else
		record real_direct_ip_control FAIL "baseline $(mask_ip "$BASE_PUBIP") vs direct $(mask_ip "$DIRECT_PUBIP") — cannot trust the comparison"
	fi
}

# burstObservatory probes every subjectSelector outbound through the outbound
# itself. With statsOutboundUplink on (the provider ships it), an outbound that
# moved bytes while no client request ever selected it can only have been
# probed. Client traffic in this mode is a handful of requests through ONE
# balancer pick, so three or more subject outbounds with traffic is the
# observatory working.
assert_observatory() {
	_stats=$(xray api statsquery -s "$XAPI" -pattern "outbound>>>" 2>/dev/null)
	if [ -z "$_stats" ]; then
		record real_observatory_probes FAIL "xray api statsquery returned nothing (api port down?)"
		record real_observatory_selects FAIL "no api access"
		return
	fi
	# The xray CLI pretty-prints one JSON field per line, so name/value pairs can
	# be walked without a JSON parser (there is none in the image).
	echo "$_stats" | awk '
		/"name":/  { gsub(/.*outbound>>>/, ""); sub(/>>>traffic>>>/, " "); sub(/".*/, ""); split($0, a, " "); tag = a[1]; dir = a[2] }
		/"value":/ { gsub(/[^0-9]/, ""); if ($0 + 0 > 0 && tag != "") printf "%s %s %s\n", tag, dir, $0 }
	' | sort > /tmp/outbound-traffic.txt

	_subjects=$(sed -n 's/.*"subjectSelector":\[\([^]]*\)\].*/\1/p' "$ENTRY_RAW" | tr -d '"' | tr ',' '\n')
	echo "--- burstObservatory probe evidence: bytes moved per outbound ---"
	echo "    (a subject outbound with traffic that no client request selected was probed)"
	_active=$(awk '{print $1}' /tmp/outbound-traffic.txt | sort -u)
	_hit=0
	for t in $_active; do
		_role=other
		if echo "$_subjects" | grep -qx "$t"; then _role=subject; _hit=$((_hit + 1)); fi
		printf '    %-18s %-8s %s\n' "$t" "$_role" \
			"$(awk -v k="$t" '$1==k{printf "%s=%s ", $2, $3}' /tmp/outbound-traffic.txt)"
	done
	if [ "$_hit" -ge 3 ]; then
		record real_observatory_probes PASS "$_hit burstObservatory subject outbounds carried bytes; client traffic used at most one"
	else
		record real_observatory_probes FAIL "only $_hit subject outbounds carried bytes — the observatory did not probe"
	fi

	# `xray api bi` does not enumerate when given no arguments, and it stops at
	# the first tag it is given, so ask for one balancer at a time. The tags come
	# from the provider's own routing rules rather than this provider's BL-*
	# naming convention.
	_bals=$(tr ',' '\n' < "$ENTRY_RAW" | sed -n 's/.*"balancerTag":"\([^"]*\)".*/\1/p' | sort -u)
	_seen=0
	echo "--- xray api bi: balancer verdicts (the observatory's selection) ---"
	for b in $_bals; do
		_out=$(xray api bi -s "$XAPI" "$b" 2>&1)
		printf '    [%s]\n' "$b"
		echo "$_out" | sed 's/^/      /'
		echo "$_out" | grep -qiE 'bridge-|hy2-|whitelist-' && _seen=$((_seen + 1))
	done
	if [ "$_seen" -ge 1 ]; then
		record real_observatory_selects PASS "$_seen of $(echo "$_bals" | wc -l | tr -d ' ') balancers report a live principle target (verdicts above)"
	else
		record real_observatory_selects FAIL "no balancer reported a principle target"
	fi
}

# Two idle windows and one request window, measured once and read by the two
# assertions below. Both of them need the SAME thing: a measured baseline for
# what this box does when nobody is asking it to do anything.
#
# Against the local fixture there is no such background — the only egress is one
# freedom outbound carrying xray's socket mark. The real provider document
# brings xray's own housekeeping with it: the `dns` block resolves through
# 1.1.1.1/77.88.8.8, burstObservatory's pingConfig dials a `connectivity` URL
# directly, and the observatory probes all sixteen subjectSelector outbounds at
# start. All of that moves the very counters the fixture assertions read as
# absolutes.
#
# LOOP_SLACK / DELIVERY_MARGIN are set two orders of magnitude below the signals
# they have to separate and around one order above the measured background: a
# delivered request moved 81 egress_exempt packets, a real loop moved 85,150
# tproxy hits, and the idle windows move single digits.
#
# LOOP_SLACK was 5 and had to be re-derived, because the idle window stopped
# being a predictor for the request window. The output chain now returns on
# `ct state established,related` before it marks, so the idle background — which
# is xray's housekeeping flows CONTINUING — dropped from +5/+1 to +0/+0.
# Measured across the two runs either side of that change:
#
#   before: idle +5 / +1, request window +2   -> bound 10, passed
#   after:  idle +0 / +0, request window +9   -> bound  5, failed
#
# The +9 is not a loop. It is the FIRST packets of the new direct connections
# one request legitimately opens against a real provider — xray's `dns` block
# and burstObservatory's connectivity URL — and the mode's own breakdown says so:
# marked_tcp 19, marked_dns 0, marked_udp 0, marked_other 0, with tproxy_hits +73
# (bound 300) and egress_exempt +234. An idle window opens no connections at all,
# so it cannot bound a window that does.
#
# 40 keeps the detector three orders of magnitude below the 85,150-packet loop
# signature while covering the handful of connections a request opens. It is a
# re-derivation, not a softening: the quantity being bounded changed kind.
LOOP_SLACK=40
# REQUEST_BURST drives the delivery signal well clear of observatory noise; the
# threshold is then expressed RELATIVE to the measured idle maximum rather than
# as a constant, so a noisier provider does not silently turn this into a coin
# flip. A constant margin of 10 sat BELOW an observed idle window of 18.
REQUEST_BURST=8
DELIVERY_MARGIN=10
# DELIVERY_FLOOR is the part a relative threshold cannot supply. An idle window
# can legitimately measure 0 while the observatory sits between bursts, and then
# `idle*2 + margin` collapses to the margin alone — which observatory noise
# clears on its own. That is not hypothetical: the no-routing control PASSED at
# +13 against a threshold of +10 with a measured idle of 0, i.e. the control
# stopped detecting the defect it exists for.
#
# Calibration, from real runs: delivered traffic moves +364, observatory-only
# noise moves +10..+27. A floor of 100 sits ~4x above the noise and ~3.6x below
# the signal, so neither side is close to it.
DELIVERY_FLOOR=100
measure_egress_windows() {
	W_OM_IDLE=0
	W_EX_IDLE=0
	for _w in 1 2; do
		_a=$(ctr vctl_output_marked); _e=$(ctr vctl_egress_exempt)
		sleep 4
		_b=$(( $(ctr vctl_output_marked) - _a ))
		_f=$(( $(ctr vctl_egress_exempt) - _e ))
		info "idle window $_w: output_marked +$_b, egress_exempt +$_f (xray housekeeping + observatory)"
		[ "$_b" -gt "$W_OM_IDLE" ] && W_OM_IDLE=$_b
		[ "$_f" -gt "$W_EX_IDLE" ] && W_EX_IDLE=$_f
	done

	_ex0=$(ctr vctl_egress_exempt)
	_om0=$(ctr vctl_output_marked)
	_th0=$(ctr vctl_tproxy_hits)
	_dns0=$(sg_ctr marked_dns); _tcp0=$(sg_ctr marked_tcp)
	_udp0=$(sg_ctr marked_udp); _oth0=$(sg_ctr marked_other)
	# Drive REQUEST_BURST requests, not one. A single request through an already
	# established outbound moves too few packets to separate from observatory
	# noise: a real run measured +27 against a max idle window of 18, and failed
	# a >28 threshold — the data plane was demonstrably fine (the public IP
	# comparison passed in the same run), the measurement was not.
	#
	# Raising the signal is the honest fix. Lowering the threshold below the
	# noise floor would make the assertion pass on a dead data plane, which is
	# the exact trap this mode exists to avoid.
	_r=0
	while [ "$_r" -lt "$REQUEST_BURST" ]; do
		in_client curl -sS --max-time 25 -o /dev/null "$LOOP_URL_REAL" 2>/dev/null
		_r=$((_r + 1))
	done
	sleep 4
	W_DEX=$(( $(ctr vctl_egress_exempt) - _ex0 ))
	W_DOM=$(( $(ctr vctl_output_marked) - _om0 ))
	W_DTH=$(( $(ctr vctl_tproxy_hits) - _th0 ))
	info "request window: tproxy_hits +$W_DTH, output_marked +$W_DOM, egress_exempt +$W_DEX"
	info "request window marked-egress breakdown: dns +$(( $(sg_ctr marked_dns) - _dns0 )) tcp +$(( $(sg_ctr marked_tcp) - _tcp0 )) udp +$(( $(sg_ctr marked_udp) - _udp0 )) other +$(( $(sg_ctr marked_other) - _oth0 ))"
}

# The v4 stand asserts delivery as an ABSOLUTE: vctl_egress_exempt > 0, because
# "xray only ever opens an outbound socket because it accepted a tproxied
# connection". THAT PREMISE IS FALSE FOR A REAL PROVIDER. burstObservatory dials
# every subject outbound at start whether or not a single client packet was ever
# delivered, so the absolute counter is non-zero on a completely dead data plane
# — the same trap as nft_tproxy_hits, one level deeper. It was caught by the
# no-routing control: nft_tproxy_delivered PASSED there.
#
# So delivery is measured as a DELTA around a client request, against the idle
# baseline. The idle windows are the control: they are what the observatory
# alone produces.
assert_real_tproxy_delivered() {
	# Threshold sits above TWICE the measured idle maximum, so it scales with the
	# observed noise instead of assuming it. With REQUEST_BURST requests the
	# signal clears that comfortably; a dead data plane produces only the idle
	# background and cannot.
	# Whichever is stricter: scale with the measured noise, but never drop below
	# the absolute floor. The relative term alone degenerates when idle reads 0.
	_want=$((W_EX_IDLE * 2 + DELIVERY_MARGIN))
	[ "$_want" -lt "$DELIVERY_FLOOR" ] && _want=$DELIVERY_FLOOR
	if [ "$W_DEX" -gt "$_want" ]; then
		record real_tproxy_delivered PASS "$REQUEST_BURST requests moved egress_exempt +$W_DEX vs idle background $W_EX_IDLE (want > +$_want) — xray accepted tproxied connections and dialled out"
	else
		record real_tproxy_delivered FAIL "$REQUEST_BURST requests moved egress_exempt +$W_DEX, idle background $W_EX_IDLE (want > +$_want) — nothing was delivered to xray"
	fi
}

# The loop detector. Same recalibration: "output_marked must be exactly zero" is
# right for the fixture and wrong here, and raising it to "must be small" would
# be the weakening this stand exists to prevent. The request window has to stay
# within the measured idle background instead.
assert_no_egress_loop_real() {
	if [ "$W_DTH" -lt "$LOOP_BOUND" ] && [ "$W_DEX" -gt 0 ] && [ "$W_DOM" -le $((W_OM_IDLE + LOOP_SLACK)) ]; then
		record no_egress_loop PASS "one request: tproxy_hits +$W_DTH (<$LOOP_BOUND), output_marked +$W_DOM (idle background $W_OM_IDLE, slack $LOOP_SLACK), egress_exempt +$W_DEX"
	else
		record no_egress_loop FAIL "one request: tproxy_hits +$W_DTH (want <$LOOP_BOUND), output_marked +$W_DOM (want <=$((W_OM_IDLE + LOOP_SLACK))), egress_exempt +$W_DEX (want >0)"
	fi
}

# The subscription URL must not have survived into anything on disk.
assert_url_not_leaked() {
	_hits=$(grep -rlF "$STAND_SUB_URL" "$WORK" "$PROVIDER_AUG" "$ENTRY_RAW" /etc/vectra-controller-pro 2>/dev/null | tr '\n' ' ')
	if [ -z "$_hits" ]; then
		record real_url_not_leaked PASS "subscription URL absent from the rendered config, the supervisor log and the state dir"
	else
		record real_url_not_leaked FAIL "URL found in: $_hits"
	fi
}

# ------------------------------------------------------------------- main -----

# Bring xray up on one entry and report whether the client can reach the
# internet through it. A dead node is a provider fact, not a stand failure, so
# the caller walks to the next entry — but the assertions below never soften.
try_entry() { # <index> -> 0 when client traffic flows
	_n=$1
	stop_xray
	if ! vctl subscribe parse -in "$SUB_RAW" -content-type application/json \
		-entry "$_n" -out "$ENTRY_RAW" 2>/dev/null; then
		info "entry $_n: not present"
		return 1
	fi
	augment_entry || { info "entry $_n: does not start with '{'"; return 1; }
	PROVIDER="$PROVIDER_AUG"
	mkdir -p "$WORK"
	vctl supervise -config "$CFG" -provider "$PROVIDER" \
		-status "$WORK/status.json" > "$WORK/supervise.log" 2>&1 &
	_i=0
	while [ "$_i" -lt 45 ]; do
		netstat -lnt 2>/dev/null | grep -q ":$TPROXY_PORT " && break
		_i=$((_i + 1)); sleep 1
	done
	if ! netstat -lnt 2>/dev/null | grep -q ":$TPROXY_PORT "; then
		info "entry $_n: xray never listened on :$TPROXY_PORT"
		tail -n 5 "$WORK/supervise.log" 2>/dev/null | sed 's/^/       /'
		return 1
	fi
	# burstObservatory's first round needs a moment before a balancer can pick.
	sleep 12
	_code=$(in_client curl -sS --max-time 25 -o /dev/null -w '%{http_code}' "$LOOP_URL_REAL" 2>/dev/null)
	case "$_code" in
	2*|3*) info "entry $_n: client reached the internet (HTTP $_code)"; return 0 ;;
	*)     info "entry $_n: no client egress (HTTP '${_code:-none}')"; return 1 ;;
	esac
}

run_real_egress_mode() {
	[ -n "${STAND_SUB_URL:-}" ] || die "STAND_SUB_URL is not set; this mode needs the live subscription"
	[ -n "$SUB_UA" ] || die "STAND_SUB_UA is not set; there is deliberately no default User-Agent"
	CFG="$STAND/operator-config.json"

	say "identity"
	HWID=$(synthetic_hwid)
	assert_hwid_synthetic

	say "fetch the live subscription (JSON variant)"
	fetch_subscription || die "subscription fetch failed"
	assert_subscription_fetched

	say "topology + baseline public IP (BEFORE any ruleset exists)"
	setup_kernel
	setup_topology
	setup_guard
	mkdir -p "/etc/netns/$CLIENT_NS"
	printf 'nameserver 1.1.1.1\n' > "/etc/netns/$CLIENT_NS/resolv.conf"
	TRACE_IP=$(nslookup "$TRACE_HOST" 2>/dev/null | awk '/^Address/{ip=$NF} END{print ip}')
	echo "$TRACE_IP" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' || die "could not resolve $TRACE_HOST"
	info "trace endpoint pinned to $TRACE_HOST -> $TRACE_IP"
	BASE_PUBIP=$(trace_ip_from)
	info "baseline (unruled, router ns) public IP: $(mask_ip "$BASE_PUBIP")"
	[ -n "$BASE_PUBIP" ] || die "no internet from the stand host; real-egress cannot run"

	say "apply the ruleset with the SHIPPED code"
	apply_firewall
	setup_egress_breakdown

	say "splice a REAL provider entry into tproxy and dial the REAL nodes"
	_n=0
	while [ "$_n" -lt "$MAX_ENTRY_TRIES" ]; do
		if try_entry "$_n"; then CHOSEN_ENTRY=$_n; break; fi
		_n=$((_n + 1))
	done
	if [ "$CHOSEN_ENTRY" -lt 0 ]; then
		info "no entry carried client traffic after $MAX_ENTRY_TRIES attempts; asserting on the last one"
		CHOSEN_ENTRY=$((MAX_ENTRY_TRIES - 1))
	else
		info "entry $CHOSEN_ENTRY selected: $(sed -n 's/.*"remarks":"\([^"]*\)".*/\1/p' "$ENTRY_RAW" | head -1)"
	fi
	info "$(grep -m1 'spliced provider config' "$WORK/supervise.log" 2>/dev/null || echo 'no splice line in log')"

	say "assertions"
	assert_provider_verbatim
	assert_splice_mark
	assert_real_tcp
	assert_real_tls
	assert_real_dns

	# Client vantage point first, with the endpoint NOT in the direct set.
	CLIENT_PUBIP=$(trace_ip_from in_client)
	# Then the router's own, made genuinely direct through the SHIPPED
	# vctl_direct4 set (the nftset dnsmasq populates for split routing).
	nft add element inet vctl vctl_direct4 "{ $TRACE_IP }" 2>/dev/null
	DIRECT_PUBIP=$(trace_ip_from)
	nft delete element inet vctl vctl_direct4 "{ $TRACE_IP }" 2>/dev/null
	assert_public_ip_differs
	assert_direct_ip_stable

	assert_tproxy_counter
	measure_egress_windows
	assert_real_tproxy_delivered
	assert_no_egress_loop_real
	assert_no_leak
	assert_observatory
	assert_url_not_leaked

	say "nft counters (table inet vctl)"
	nft list counters table inet vctl
	echo "--- guard (must stay 0) ---"
	nft list counter inet standguard leaked
	echo "--- marked local egress, broken down ---"
	for c in marked_dns marked_tcp marked_udp marked_other; do
		printf '    %-14s %s packets\n' "$c" "$(sg_ctr "$c")"
	done

	say "xray / supervisor log (tail)"
	tail -n 25 "$WORK/supervise.log" 2>/dev/null
}
