# shellcheck shell=sh
# real-egress with the router's OWN agent.
#
# Sourced by stand.sh after mode-real-egress.sh: the same topology, the same
# real nodes and every assertion of MODE=real-egress, with one change — the
# subscription is fetched the way a router whose config names no agent fetches
# it: `vctl subscribe fetch -signed`, the User-Agent the router makes itself
# (VectraRouter/<version> vr1.<token>, signed per request with its device key),
# from $STAND_SUB_URL/json, which Remnawave serves as xray-JSON whatever the
# agent. STAND_SUB_UA is not used.
#
# The device key is the stand vctl's own: `vctl agent -once` makes state.json
# at its first start, against a panel that is a dead port (127.0.0.1:1), with
# api/router.vectra-pro.net sinkholed besides. Its public probes
# (gstatic/cloudflare generate_204) are the only other thing it sends.
#
# Safety, as verified in the backend before this mode was written: the
# provider's anti-fraud never deletes a device whose platform header says
# OpenWrt, and vctl sends x-device-os: OpenWrt; the HWID is real-egress's
# synthetic one; the subscription is the test account's (README, Secrets).
#
# What goes on the wire to the provider is inside TLS. So own_ua_on_wire reads
# the agent off the IDENTICAL fetch — the same binary, flags, state.json and
# HWID — made against a TLS listener on the stand's own address that records
# the request's headers: it must start with VectraRouter/ and carry " vr1.", with
# x-device-os OpenWrt and x-hwid the synthetic one. The log shows the prefix
# and the token's length; the token itself never.

OWN_DIR=/tmp/own
OWN_STATE=$OWN_DIR/state.json
OWN_CAPTURE_PORT=18081

own_first_start() {
	printf '%s api.vectra-pro.net router.vectra-pro.net\n' 127.0.0.99 >> /etc/hosts
	mkdir -p "$OWN_DIR"
	cat > "$OWN_DIR/agent.json" <<EOF
{
  "controlUrl": "http://127.0.0.1:1",
  "panelUrl": "http://127.0.0.1:1",
  "statePath": "$OWN_STATE",
  "statusPath": "$OWN_DIR/status.json",
  "xrayConfigPath": "$OWN_DIR/no-operator-config.json",
  "providerConfigPath": "$OWN_DIR/provider-config.json",
  "xrayRenderPath": "$OWN_DIR/xray.json",
  "xrayBinary": "/usr/sbin/vctl-xray-wrapper",
  "geoAssetDir": "/usr/share/v2ray",
  "legacyStatePath": "$OWN_DIR/no-legacy-state.json",
  "pollIntervalSeconds": 5,
  "requestTimeoutSeconds": 3
}
EOF
	vctl agent -config "$OWN_DIR/agent.json" -once > "$OWN_DIR/first-start.log" 2>&1
	_id=$(jsonfilter -i "$OWN_STATE" -e '@.device_identifier' 2>/dev/null)
	_key=$(jsonfilter -i "$OWN_STATE" -e '@.device_private_key' 2>/dev/null)
	if [ -n "$_id" ] && [ -n "$_key" ]; then
		record own_state_first_start PASS "vctl's first start (agent -once, dead panel) made $OWN_STATE: a device id and key (neither printed)"
	else
		record own_state_first_start FAIL "no device id/key in $OWN_STATE after the first start: $(tail -n 3 "$OWN_DIR/first-start.log" | tr '\n' ' ' | cut -c1-140)"
		die "no device key: the router's own agent cannot be signed"
	fi
}

# A listener on the stand's own address that records one request and answers
# an empty JSON array. It has to be TLS: the router signs the request target of
# an https:// subscription only (uatoken.RequestTarget) and refuses to send
# anything for plain http. The stand's xray terminates TLS with the stand's
# certificate (for 44.44.44.44, which lives on the router's loopback for as
# long as this runs) and hands the plain request to a recorder.
OWN_CAPTURE_IP=44.44.44.44
OWN_RECORDER_PORT=18082
own_capture() {
	cat > "$OWN_DIR/capture.sh" <<'EOF'
#!/bin/sh
: > /tmp/own/request.txt
while IFS= read -r l; do
	l=$(printf '%s' "$l" | tr -d '\r')
	[ -z "$l" ] && break
	printf '%s\n' "$l" >> /tmp/own/request.txt
done
printf 'HTTP/1.0 200 OK\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n[]'
EOF
	chmod +x "$OWN_DIR/capture.sh"
	cat > "$OWN_DIR/tls-front.json" <<EOF
{
  "log": { "loglevel": "warning" },
  "inbounds": [{
    "listen": "$OWN_CAPTURE_IP", "port": $OWN_CAPTURE_PORT, "protocol": "dokodemo-door",
    "settings": { "address": "127.0.0.1", "port": $OWN_RECORDER_PORT, "network": "tcp" },
    "streamSettings": { "network": "raw", "security": "tls", "tlsSettings": {
      "certificates": [{ "certificateFile": "/stand/tls/cert.pem", "keyFile": "/stand/tls/key.pem" }] } }
  }],
  "outbounds": [{ "protocol": "freedom" }]
}
EOF
	cp /etc/ssl/certs/ca-certificates.crt "$OWN_DIR/ca.orig" 2>/dev/null
	cat /stand/tls/cert.pem >> /etc/ssl/certs/ca-certificates.crt
	ip addr add "$OWN_CAPTURE_IP/32" dev lo 2>/dev/null
	socat "TCP-LISTEN:$OWN_RECORDER_PORT,bind=127.0.0.1,reuseaddr,fork" "SYSTEM:$OWN_DIR/capture.sh" >/dev/null 2>&1 &
	_rec=$!
	/usr/bin/xray run -c "$OWN_DIR/tls-front.json" > "$OWN_DIR/tls-front.log" 2>&1 &
	_front=$!
	sleep 2
	_rel=$(sed -n "s/^DISTRIB_RELEASE='\(.*\)'/\1/p" /etc/openwrt_release 2>/dev/null)
	vctl subscribe fetch -url "https://$OWN_CAPTURE_IP:$OWN_CAPTURE_PORT/json" -signed -state "$OWN_STATE" \
		-hwid "$HWID" -mac "$SYN_MAC" -model "$SYN_MODEL" -os-release "$_rel" \
		-timeout 20 -retries 0 > /dev/null 2> "$OWN_DIR/capture-fetch.err"
	_rc=$?
	sleep 1
	kill "$_front" "$_rec" 2>/dev/null
	ip addr del "$OWN_CAPTURE_IP/32" dev lo 2>/dev/null
	[ -f "$OWN_DIR/ca.orig" ] && cp "$OWN_DIR/ca.orig" /etc/ssl/certs/ca-certificates.crt
	_ua=$(sed -n 's/^[Uu]ser-[Aa]gent: //p' "$OWN_DIR/request.txt" 2>/dev/null)
	_os=$(sed -n 's/^[Xx]-[Dd]evice-[Oo][Ss]: //p' "$OWN_DIR/request.txt" 2>/dev/null)
	_hw=$(sed -n 's/^[Xx]-[Hh][Ww][Ii][Dd]: //p' "$OWN_DIR/request.txt" 2>/dev/null)
	_tok=${_ua#* vr1.}
	_shown="${_ua%% vr1.*} vr1.<${#_tok} characters, withheld>"
	case "$_ua" in
	VectraRouter/*" vr1."?*)
		if [ "$_os" = OpenWrt ] && [ "$_hw" = "$HWID" ]; then
			record own_ua_on_wire PASS "the agent on the wire: '$_shown'; x-device-os: $_os; x-hwid: the synthetic one"
		else
			record own_ua_on_wire FAIL "agent '$_shown', but x-device-os='$_os' x-hwid synthetic=$([ "$_hw" = "$HWID" ] && echo yes || echo no)"
		fi
		;;
	*)
		# The local listener's URL is the stand's own: its error text may be shown.
		record own_ua_on_wire FAIL "fetch exit $_rc ($(head -c 160 "$OWN_DIR/capture-fetch.err" | tr '\n' ' ')); recorded $(wc -l < "$OWN_DIR/request.txt" 2>/dev/null | tr -d ' ') header line(s); the agent on the wire is not the router's own: '$(printf '%s' "$_ua" | cut -c1-24)…' $(tail -n 2 "$OWN_DIR/tls-front.log" | tr '\n' ' ' | cut -c1-120)"
		die "refusing to send the provider an agent that is not the router's own"
		;;
	esac
}

# The real fetch, with the router's own agent. stderr is never printed: a Go
# transport error quotes the URL.
fetch_subscription() {
	_rel=$(sed -n "s/^DISTRIB_RELEASE='\(.*\)'/\1/p" /etc/openwrt_release 2>/dev/null)
	if vctl subscribe fetch -url "$STAND_SUB_URL/json" -signed -state "$OWN_STATE" \
		-hwid "$HWID" -mac "$SYN_MAC" -model "$SYN_MODEL" -os-release "$_rel" \
		-timeout 60 -retries 2 -out "$SUB_RAW" 2>"$FETCH_ERR"; then
		info "subscription fetched with the router's own agent: $(wc -c < "$SUB_RAW") bytes (URL and error text withheld)"
		return 0
	fi
	info "subscription fetch FAILED; error text withheld because it embeds the URL"
	return 1
}

run_real_egress_own_mode() {
	[ -n "${STAND_SUB_URL:-}" ] || die "STAND_SUB_URL is not set; this mode needs the live subscription"
	# The agent real-egress's messages name.
	# shellcheck disable=SC2034 # read by mode-real-egress.sh
	SUB_UA="the router's own (VectraRouter/<version> vr1.<signed>)"

	say "the stand vctl's first start: its own state.json and device key"
	own_first_start

	say "the router's own agent, on the wire (an identical fetch against the stand's loopback)"
	HWID=$(synthetic_hwid)
	[ -n "$HWID" ] || die "no synthetic HWID"
	own_capture

	run_real_egress_mode
}
