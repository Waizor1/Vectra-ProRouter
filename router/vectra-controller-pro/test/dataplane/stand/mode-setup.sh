# shellcheck shell=sh
# Gap 7 — the setup wizard and the claim side of ADR-0006, end to end.
#
# Sourced by stand.sh. A router a customer has just unboxed: the package
# installed with the real opkg, procd/ubusd/rpcd running from their own init
# scripts, the daemon up WITHOUT an operator config (so it is not linked and
# offers a claim), and a panel stub that accepts it and answers, as the real
# panel may, with Vectra's claim key and bot — and, when the stand says so,
# with an owner, or with "released" (stand/setup/panel-claim.sh). The stub
# logs every request WITH its body, so what a check-in carries is read, not
# assumed.
#
#   MODE=setup       every assertion must PASS
#   MODE=setup-hold  the unboxed router, then the container stays up for a
#                    browser: run.sh publishes LuCI on
#                    127.0.0.1:${STAND_UI_PORT:-8080} (root, empty password —
#                    a fresh box)
#
# The router sets its internet connection up itself: the wizard only shows it
# and checks it. There is no netifd and no radio in the container, so the WAN
# is a UCI section on the stand's uplink (vinet) and the Wi-Fi a dummy
# wireless config; the wizard's changes are proven where they land — UCI and
# /etc/shadow.
#
# Then the owner's router in service, and its release: linked with a config
# (the kill switch ARMED; stand/setup/link.sh), the LAN carried by xray through
# the ui stand's nodes, a location, a pin and My sites chosen on it — until the
# owner unbinds it in the Vectra app. What is left must be a router as it came
# out of the box, proven on the packets: a LAN client reaches the origin by
# plain forwarding, carried by no node.

. "$STAND/mode-ui.sh" # ui_wait, ui_lock, vcall, jf, ui_boot_services, ui_install_package

SETUP_PANEL_IP=44.44.44.82
SETUP_PANEL_PORT=8080
SETUP_PANEL_LOG=/tmp/setup-panel.log
# Once this file exists, the panel stub names the router's owner.
SETUP_PANEL_OWNER=/tmp/setup-panel-owner
SETUP_OWNER='Stand Owner'
# ...and once this one exists, it says the owner released the router.
SETUP_PANEL_RELEASED=/tmp/setup-panel-released
# What a release must remove (the daemon's paths on a router).
SETUP_OWNER_FILES="/etc/vectra-controller-pro/xray-desired.json /etc/vectra-controller-pro/provider-config.json
/etc/vectra-controller-pro/provider-entries.json.gz /etc/vectra-controller-pro/provider-entries.index.json
/etc/vectra-controller-pro/local-overrides.json /var/run/vectra-controller-pro/xray.json"
SETUP_STATE=/etc/vectra-controller-pro/state.json
# Seconds between claim codes: the router's knob (uci claim_rotate_sec), short
# so the stand sees a rotation.
SETUP_ROTATE=20
SETUP_BOT=VectraStandBot

setup_isolate() {
	ip route del default 2>/dev/null || true
	ip -6 route del default 2>/dev/null || true
	ip -n "$INET_NS" addr add "$SETUP_PANEL_IP/32" dev lo
	ip route add "$SETUP_PANEL_IP/32" via "$INET_PEER_IP" dev vinet
	: > "$SETUP_PANEL_LOG"
	rm -f "$SETUP_PANEL_OWNER" "$SETUP_PANEL_RELEASED"
	in_inet socat "TCP-LISTEN:18080,bind=127.0.0.1,fork,reuseaddr" \
		"SYSTEM:sh $STAND/setup/panel-claim.sh" >/dev/null 2>&1 &
	in_inet /usr/bin/stand-tlsproxy -listen "$SETUP_PANEL_IP:$SETUP_PANEL_PORT" \
		-upstream http://127.0.0.1:18080 >/tmp/stand-panel-tls.log 2>&1 &
	uci set vectra-controller-pro.main.control_url="https://$SETUP_PANEL_IP:$SETUP_PANEL_PORT"
	uci set vectra-controller-pro.main.panel_url="https://$SETUP_PANEL_IP:$SETUP_PANEL_PORT"
	uci set vectra-controller-pro.main.poll_interval=5
	# A person at the browser gets the real 10 minutes.
	[ "$MODE" = setup ] && uci set vectra-controller-pro.main.claim_rotate_sec="$SETUP_ROTATE"
	uci commit vectra-controller-pro
	rm -f /etc/vectra-controller-pro/xray-desired.json # not linked
	info "default route removed; the panel is a claim-aware stub at $SETUP_PANEL_IP:$SETUP_PANEL_PORT; claim codes rotate every $(uci -q get vectra-controller-pro.main.claim_rotate_sec || echo 600)s"
}

# The WAN as a router would have it: DHCP on the uplink. Nothing runs netifd
# here, so this only tells the wizard which device the WAN is.
setup_dummy_network() {
	[ -f /etc/config/network ] || : > /etc/config/network
	uci -q delete network.wan
	uci set network.wan=interface
	uci set network.wan.device=vinet
	uci set network.wan.proto=dhcp
	uci commit network
}

# The container has no radio: two of them, disabled, with open access points —
# a fresh OpenWrt's wireless config, on its own rules: 2.4 GHz on auto at
# 17 dBm under the US's, 5 GHz on 36 with the driver's power and no country.
setup_dummy_wireless() {
	cat > /etc/config/wireless <<'EOF'
config wifi-device 'radio0'
	option type 'mac80211'
	option band '2g'
	option channel 'auto'
	option htmode 'HE20'
	option txpower '17'
	option country 'US'
	option disabled '1'

config wifi-device 'radio1'
	option type 'mac80211'
	option band '5g'
	option channel '36'
	option htmode 'HE80'
	option disabled '1'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option network 'lan'
	option mode 'ap'
	option ssid 'OpenWrt'
	option encryption 'none'

config wifi-iface 'default_radio1'
	option device 'radio1'
	option network 'lan'
	option mode 'ap'
	option ssid 'OpenWrt'
	option encryption 'none'
EOF
}

# The claim of the newest check-in the stub saw.
last_checkin_hash() {
	_b=$(grep 'check-in' "$SETUP_PANEL_LOG" 2>/dev/null | tail -1 | sed 's/^[^{]*//')
	[ -n "$_b" ] && jf "$_b" '@.claim.codeHash'
}
code_hash() { printf 'vectra-claim-codehash/v1:%s' "$1" | sha256sum | cut -d' ' -f1; }
claim_code() { jf "$(vcall setup)" '@.vectra.claim.code'; }
checkin_has_claim() { [ -n "$(last_checkin_hash)" ]; }
# The newest check-in carries the hash of the code on screen now. Both are
# read afresh on every try: the code may rotate while the stand waits.
checkin_matches_screen() {
	_c=$(claim_code)
	[ -n "$_c" ] && [ "$(last_checkin_hash)" = "$(code_hash "$_c")" ]
}
code_changed() { _c=$(claim_code); [ -n "$_c" ] && [ "$_c" != "$1" ]; }
owner_shown() { [ "$(jf "$(vcall setup)" '@.vectra.owner.label')" = "$1" ]; }
linked_is() { [ "$(jf "$(vcall setup)" '@.vectra.linked')" = "$1" ]; }
checkins() { grep -c 'check-in' "$SETUP_PANEL_LOG" 2>/dev/null || echo 0; }
checked_in_since() { [ "$(checkins)" -ge $(($1 + ${2:-1})) ]; } # <count before> [how many more]
vctl_table_loaded() { nft list table inet vctl >/dev/null 2>&1; }
overrides_have() { grep -q "$1" /etc/vectra-controller-pro/local-overrides.json 2>/dev/null; }
router_xray_procs() { ps w 2>/dev/null | grep -v grep | grep -c 'run -c /var/run/vectra-controller-pro/xray.json'; }

# Counts the LAN client's packets that the router FORWARDS to the origin: plain
# routing. A proxied connection never reaches the forward hook — TPROXY
# delivers it to xray on the router — so a count that moves is traffic that
# went out directly.
setup_forward_counter() {
	nft -f - <<EOF || die "setupfwd table"
table inet setupfwd {
  counter lanfwd { }
  chain forward {
    type filter hook forward priority filter - 10; policy accept;
    ip saddr $CLIENT_NET ip daddr $ORIGIN_IP tcp dport 80 counter name "lanfwd"
  }
}
EOF
}
lan_forwarded() { nft list counter inet setupfwd lanfwd 2>/dev/null | awk '/packets/{print $2; exit}'; }

setup_start() {
	ui_install_package
	ui_wait 15 ubus list vectra || die "the package installed but rpcd never exposed 'vectra'"
	/etc/init.d/vectra-controller-pro restart
	ui_wait 45 checkin_has_claim || {
		tail -n 5 "$SETUP_PANEL_LOG" 2>/dev/null | cut -c1-200
		logread 2>/dev/null | grep -E 'vctl|vectra' | tail -n 20
		die "no check-in with a claim reached the panel stub"
	}
	info "the daemon checks in, unlinked, with a claim"
}

# ----------------------------------------------------------------- asserts ---

# A fresh router: the wizard is not done (installing the package on a router
# that does not work yet must not mark it done), nothing is linked, and there is
# a code, a QR sealed to the key the panel sent, and the bot's link.
assert_setup_shape() {
	_s=$(vcall setup)
	_done=$(jf "$_s" '@.done')
	_linked=$(jf "$_s" '@.vectra.linked')
	_bot=$(jf "$_s" '@.vectra.botUsername')
	_state=$(jf "$_s" '@.vectra.claim.state')
	_code=$(jf "$_s" '@.vectra.claim.code')
	_qr=$(jf "$_s" '@.vectra.claim.qr')
	_url=$(jf "$_s" '@.vectra.claim.botUrl')
	_exp=$(jf "$_s" '@.vectra.claim.expiresAt')
	_owner=$(jf "$_s" '@.vectra.owner.label')$(jf "$_s" '@.vectra.claim.owner.label')
	if [ "$_done" = false ] && [ "$_linked" = false ] && [ "$_bot" = "$SETUP_BOT" ] && [ "$_state" = unclaimed ] \
		&& [ "${#_code}" = 8 ] && [ -z "$(printf '%s' "$_code" | tr -d '0-9ABCDEFGHJKMNPQRSTVWXYZ')" ] \
		&& [ "${_qr#VECTRA:R1:}" != "$_qr" ] && [ "$_url" = "https://t.me/$SETUP_BOT?start=rt_$_code" ] \
		&& [ -n "$_exp" ] && [ -z "$_owner" ] && [ -z "$(jf "$_s" '@.admin')" ]; then
		record setup_shape PASS "done=false, unlinked, no owner, code $_code, QR ${#_qr} chars (VECTRA:R1:), $_url, expires $_exp"
	else
		record setup_shape FAIL "done=$_done linked=$_linked bot=$_bot state=$_state code='$_code' qr='$(printf '%s' "$_qr" | cut -c1-16)' url=$_url exp=$_exp owner='$_owner' admin='$(jf "$_s" '@.admin')'"
	fi
}

# Every register proves the router holds its device key: the stub logged the
# request, and openssl checks the ed25519 signature against the
# devicePublicKey the same request carries — and refuses it over one byte more.
assert_setup_register_proof() {
	_b=$(grep '/api/router/register' "$SETUP_PANEL_LOG" 2>/dev/null | head -1 | sed 's/^[^{]*//')
	_did=$(jf "$_b" '@.inventory.deviceIdentifier')
	_pub=$(jf "$_b" '@.inventory.devicePublicKey')
	_ts=$(jf "$_b" '@.proof.timestamp')
	_sig=$(jf "$_b" '@.proof.signature')
	# SubjectPublicKeyInfo for Ed25519 is a fixed 12-byte prefix (MCowBQYDK2VwAyEA
	# in base64) and the raw key: the PEM is the two base64 strings joined.
	printf -- '-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA%s\n-----END PUBLIC KEY-----\n' "$_pub" > /tmp/reg-pub.pem
	printf 'vectra-register/v1\n%s\n%s' "$_did" "$_ts" > /tmp/reg-msg
	printf '%s' "$_sig" | openssl base64 -d -A > /tmp/reg-sig 2>/dev/null
	_ok=$(openssl pkeyutl -verify -pubin -inkey /tmp/reg-pub.pem -rawin -in /tmp/reg-msg -sigfile /tmp/reg-sig 2>&1)
	printf 'x' >> /tmp/reg-msg
	_more=$(openssl pkeyutl -verify -pubin -inkey /tmp/reg-pub.pem -rawin -in /tmp/reg-msg -sigfile /tmp/reg-sig 2>&1)
	_age=$(($(date +%s) - ${_ts:-0}))
	if echo "$_ok" | grep -q 'Verified Successfully' && ! echo "$_more" | grep -q 'Verified Successfully' \
		&& [ "$_did" = "$(jsonfilter -i "$SETUP_STATE" -e '@.device_identifier')" ] && [ "$_age" -ge 0 ] && [ "$_age" -lt 600 ]; then
		record setup_register_proof PASS "register proof for $_did at $_ts verifies with its devicePublicKey (openssl), and not over one byte more"
	else
		record setup_register_proof FAIL "device '$_did' ts '$_ts' (age ${_age}s) sig ${#_sig} chars; verify: $(echo "$_ok" | tail -1 | cut -c1-80); one byte more: $(echo "$_more" | tail -1 | cut -c1-60)"
	fi
	rm -f /tmp/reg-pub.pem /tmp/reg-msg /tmp/reg-sig
}

# The internet connection is shown, never changed: what the router has, the
# router's own network name from the WAN's MAC, a check that finds the panel —
# and no method that would change the WAN.
assert_setup_wan_readonly() {
	_s=$(vcall setup)
	_proto=$(jf "$_s" '@.wan.proto')
	_link=$(jf "$_s" '@.wan.link')
	_extra=$(jf "$_s" '@.wan.job')$(jf "$_s" '@.wan.username')
	_sugg=$(jf "$_s" '@.wifi.suggested')
	_want="Vectra-$(awk -F: '{print toupper($5 $6)}' /sys/class/net/vinet/address)"
	_c=$(vcall wan_check)
	_panel=$(jf "$_c" '@.panel')
	_clink=$(jf "$_c" '@.link')
	_at=$(jf "$_c" '@.checkedAt')
	_methods=$(ubus -v list vectra 2>/dev/null)
	if [ "$_proto" = dhcp ] && [ "$_link" = true ] && [ -z "$_extra" ] && [ "$_sugg" = "$_want" ] \
		&& [ "$_panel" = true ] && [ "$_clink" = true ] && [ -n "$_at" ] \
		&& echo "$_methods" | grep -q '"wan_check"' && ! echo "$_methods" | grep -q '"set_wan"'; then
		record setup_wan_readonly PASS "wan dhcp on vinet, link up; suggested $_sugg (vinet's MAC); wan_check reached the panel; no set_wan"
	else
		record setup_wan_readonly FAIL "proto=$_proto link=$_link extra='$_extra' suggested='$_sugg' (want $_want) wan_check panel=$_panel link=$_clink at='$_at' set_wan offered: $(echo "$_methods" | grep -c '"set_wan"')"
	fi
}

# The check-in carries the hash of the code on screen — never the code.
assert_setup_checkin_codehash() {
	ui_wait 20 checkin_matches_screen
	_code=$(claim_code)
	_h=$(last_checkin_hash)
	if [ -n "$_code" ] && [ "$_h" = "$(code_hash "$_code")" ] && ! grep -q "$_code" "$SETUP_PANEL_LOG"; then
		record setup_checkin_codehash PASS "check-in claim.codeHash = sha256(\"vectra-claim-codehash/v1:$_code\"); the code itself never reached the panel"
	else
		record setup_checkin_codehash FAIL "code '$_code' hash '$(code_hash "$_code")', check-in carries '$_h'; code in the panel log: $(grep -c "$_code" "$SETUP_PANEL_LOG" 2>/dev/null)"
	fi
}

# The code changes on schedule, and the next check-ins carry the new one.
assert_setup_claim_rotates() {
	_c1=$(claim_code)
	ui_wait $((SETUP_ROTATE + 15)) code_changed "$_c1"
	ui_wait 15 checkin_matches_screen
	_c2=$(claim_code)
	if [ -n "$_c1" ] && [ -n "$_c2" ] && [ "$_c1" != "$_c2" ] && [ "$(last_checkin_hash)" = "$(code_hash "$_c2")" ]; then
		record setup_claim_rotates PASS "$_c1 -> $_c2 within ${SETUP_ROTATE}s (+ slack); the check-ins moved to the new hash"
	else
		record setup_claim_rotates FAIL "codes '$_c1' -> '$_c2'; check-in carries '$(last_checkin_hash)'"
	fi
}

# The wizard is the simple view: the operator's lock does not refuse it.
assert_setup_under_the_lock() {
	ui_lock 1
	_s=$(vcall setup)
	_n=$(jf "$(vcall nodes)" '@.code')
	ui_lock 0
	if [ -n "$(jf "$_s" '@.vectra.claim.code')" ] && [ -z "$(jf "$_s" '@.code')" ] && [ "$_n" = locked ]; then
		record setup_under_the_lock PASS "ui_lock=1: nodes refused 'locked', setup still served"
	else
		record setup_under_the_lock FAIL "under the lock: setup code='$(jf "$_s" '@.code')' nodes='$_n'"
	fi
}

# The panel names an owner: the router keeps it (state.json) and shows it in
# the claim and at vectra.owner — where it stays once the router is linked.
assert_setup_claimed() {
	echo "$SETUP_OWNER" > "$SETUP_PANEL_OWNER"
	ui_wait 20 owner_shown "$SETUP_OWNER"
	_s=$(vcall setup)
	_state=$(jf "$_s" '@.vectra.claim.state')
	_co=$(jf "$_s" '@.vectra.claim.owner.label')
	_vo=$(jf "$_s" '@.vectra.owner.label')
	_kept=$(jsonfilter -i /etc/vectra-controller-pro/state.json -e '@.claim_owner.label' 2>/dev/null)
	if [ "$_state" = claimed ] && [ "$_co" = "$SETUP_OWNER" ] && [ "$_vo" = "$SETUP_OWNER" ] && [ "$_kept" = "$SETUP_OWNER" ]; then
		record setup_claimed PASS "the panel named '$SETUP_OWNER': claim.state claimed, claim.owner and vectra.owner show it, state.json keeps it"
	else
		record setup_claimed FAIL "claim.state=$_state claim.owner='$_co' vectra.owner='$_vo' state.json claim_owner.label='$_kept'"
	fi
}

wifi_radio() { jf "$(vcall setup)" "@.wifi.radios[$1].$2"; } # <index> <field>
jn() { _v=$(jf "$1" "$2"); [ "$_v" = null ] && _v=; printf '%s' "$_v"; } # jf, a JSON null as nothing
uci_w() { uci -q get "wireless.$1"; }
# The last Wi-Fi change's restart (setup.wifi.apply).
wifi_apply() { jn "$(vcall setup)" "@.wifi.apply.$1"; } # <field>
wifi_settled() { _st=$(wifi_apply state); [ -n "$_st" ] && [ "$_st" != applying ]; }
# What an applied change leaves behind: no private save directory, no job (it
# holds the keys), nothing staged in uci's own save directory.
wifi_clean() {
	! ls -d /var/run/vectra-controller-pro/uci-wifi-* >/dev/null 2>&1 && [ ! -e /var/run/vectra-controller-pro/wifi-job.json ] \
		&& [ -z "$(uci changes wireless)" ]
}
# The restart after a change, as the stand can judge it: there is no netifd
# here, so the helper cannot check the radios came up — honestly unverified,
# and nothing rolled back.
wifi_restart_honest() { # <name of the change>
	ui_wait 30 wifi_settled
	_st=$(wifi_apply state)
	_det=$(wifi_apply detail)
	if [ "$_st" = unverified ] && echo "$_det" | grep -q 'netifd did not answer' && wifi_clean; then
		return 0
	fi
	info "$1: restart state '$_st' ('$_det'); left behind: $(ls -d /var/run/vectra-controller-pro/uci-wifi-* /var/run/vectra-controller-pro/wifi-job.json 2>/dev/null | tr '\n' ' ') staged: $(uci changes wireless | head -2 | tr '\n' ' ')"
	return 1
}

# What the wizard shows of the Wi-Fi: one entry per radio, its own rules as
# they are, never a key. No netifd: whether a radio is up is unknown.
assert_setup_wifi_radios() {
	setup_dummy_wireless
	uci set wireless.default_radio1.key=never-shown-key && uci commit wireless
	_s=$(vcall setup)
	_r=""
	for _i in 0 1; do
		_x="@.wifi.radios[$_i]"
		_r="$_r|$(jn "$_s" "$_x.device") $(jn "$_s" "$_x.band") ch=$(jn "$_s" "$_x.channel") auto=$(jn "$_s" "$_x.auto")"
		_r="$_r $(jn "$_s" "$_x.htmode") w=$(jn "$_s" "$_x.width") $(jn "$_s" "$_x.country") tx=$(jn "$_s" "$_x.txpower") max=$(jn "$_s" "$_x.maxPower")"
		_r="$_r $(jn "$_s" "$_x.ssid") sec=$(jn "$_s" "$_x.secured") on=$(jn "$_s" "$_x.enabled") ap=$(jn "$_s" "$_x.ap") mesh=$(jn "$_s" "$_x.mesh") up=$(jn "$_s" "$_x.up")"
	done
	uci -q delete wireless.default_radio1.key && uci commit wireless
	_want="|radio0 2g ch= auto=true HE20 w=20 US tx=17 max=false OpenWrt sec=false on=false ap=true mesh=false up="
	_want="$_want|radio1 5g ch=36 auto=false HE80 w=80  tx= max=true OpenWrt sec=false on=false ap=true mesh=false up="
	if [ "$_r" = "$_want" ] && [ "$(jf "$_s" '@.wifi.tuned')" = true ] && [ "$(jf "$_s" '@.wifi.tunable')" = true ] \
		&& [ -z "$(jn "$_s" '@.wifi.apply')" ] && [ -z "$(jn "$_s" '@.wifi.radios[2].device')" ] && ! echo "$_s" | grep -q never-shown-key; then
		record setup_wifi_radios PASS "${_r#|}; tunable, tuned (nothing on); no restart yet; no key in the answer"
	else
		record setup_wifi_radios FAIL "'$_r' tuned=$(jf "$_s" '@.wifi.tuned') tunable=$(jf "$_s" '@.wifi.tunable') apply='$(jn "$_s" '@.wifi.apply')' key shown: $(echo "$_s" | grep -c never-shown-key)"
	fi
}

# wifi_scan never scans: before the first optimize_wifi there is nothing.
assert_setup_wifi_scan_empty() {
	_t0=$(date +%s)
	_c=$(vcall wifi_scan)
	_took=$(($(date +%s) - _t0))
	if [ "$(echo "$_c" | tr -d ' \n\t')" = '{"radios":[],"scannedAt":null}' ] && [ "$_took" -le 2 ]; then
		record setup_wifi_scan_empty PASS "before any optimize_wifi: {radios: [], scannedAt: null}, in ${_took}s (no scan)"
	else
		record setup_wifi_scan_empty FAIL "answer: $(echo "$_c" | tr -d '\n\t ' | cut -c1-200) (${_took}s)"
	fi
}

# A 6 GHz radio: the country is router-wide and Panama's would switch it off,
# so the router is not tunable and optimize_wifi refuses before touching
# anything.
assert_setup_wifi_unsupported() {
	uci set wireless.radio2=wifi-device && uci set wireless.radio2.band=6g && uci set wireless.radio2.htmode=HE80 && uci commit wireless
	_before=$(md5sum < /etc/config/wireless)
	_s=$(vcall setup)
	_a=$(vcall optimize_wifi '{}')
	_after=$(md5sum < /etc/config/wireless)
	uci -q delete wireless.radio2 && uci commit wireless
	if [ "$(jf "$_s" '@.wifi.tunable')" = false ] && [ -z "$(jn "$_s" '@.wifi.tuned')" ] && [ "$(jf "$_a" '@.code')" = unsupported ] \
		&& [ "$(jf "$_a" '@.detail')" = "radio2: a 6 GHz radio — Panama's rules would switch it off" ] && [ "$_before" = "$_after" ] \
		&& [ -z "$(jn "$(vcall setup)" '@.wifi.apply')" ]; then
		record setup_wifi_unsupported PASS "tunable=false, tuned=null; optimize_wifi: unsupported '$(jf "$_a" '@.detail')'; nothing changed"
	else
		record setup_wifi_unsupported FAIL "tunable=$(jf "$_s" '@.wifi.tunable') tuned='$(jn "$_s" '@.wifi.tuned')' answer=$(echo "$_a" | tr -d '\n\t' | cut -c1-160) changed=$([ "$_before" = "$_after" ] && echo no || echo YES)"
	fi
}

# The tuning on radios whose networks are open and off: Panama, no txpower,
# the channels asked for, htmode untouched — and neither network switched on,
# which the answer says. A DFS channel, and a name without a key for an open
# network, are refused and change nothing. The answer comes with the restart
# "applying"; a second change meanwhile is busy; then the helper's verdict.
assert_setup_wifi_optimize_open() {
	_dfs=$(jf "$(vcall optimize_wifi '{"channels":{"radio1":100}}')" '@.code')
	_nokey=$(jf "$(vcall set_wifi '{"radios":{"radio0":{"ssid":"Stand-Home"}}}')" '@.code')
	_ch1=$(uci_w radio1.channel)
	_a=$(vcall optimize_wifi '{"channels":{"radio0":11,"radio1":149}}')
	_now=$(wifi_apply state)
	_busy=$(jf "$(vcall set_wifi '{"radios":{"radio1":{"ssid":"Stand-Busy","key":"stand-key-000"}}}')" '@.code')
	_code=$(jf "$_a" '@.code')
	_detail=$(jf "$_a" '@.detail')
	_uci="$(uci_w radio0.country) $(uci_w radio1.country) tx=$(uci_w radio0.txpower)$(uci_w radio1.txpower) $(uci_w radio0.channel) $(uci_w radio1.channel)"
	_uci="$_uci $(uci_w radio0.htmode) $(uci_w radio1.htmode) off=$(uci_w radio0.disabled)$(uci_w radio1.disabled)"
	_honest=no
	wifi_restart_honest optimize_open && _honest=yes
	_kept="$(uci_w radio0.country) $(uci_w radio1.channel)" # not rolled back
	if [ "$_dfs" = invalid_params ] && [ "$_nokey" = invalid_params ] && [ "$_ch1" = 36 ] && [ "$_code" = wifi_optimized ] \
		&& [ "$_uci" = "PA PA tx= 11 149 HE20 HE80 off=11" ] && [ "$_now" = applying ] && [ "$_busy" = busy ] \
		&& [ "$_honest" = yes ] && [ "$_kept" = "PA 149" ] \
		&& echo "$_detail" | grep -q 'radio0: open network left disabled' && echo "$_detail" | grep -q 'radio1: open network left disabled'; then
		record setup_wifi_optimize_open PASS "wifi_optimized: $_uci; '$_detail'; answered with the restart applying, a second change busy, then unverified (no netifd), nothing rolled back; DFS 100 and an open network without a key refused"
	else
		record setup_wifi_optimize_open FAIL "dfs=$_dfs nokey=$_nokey ch1 before='$_ch1' answer=$_code uci='$_uci' detail='$_detail' after the answer=$_now then=$_busy verdict ok=$_honest kept='$_kept'"
	fi
}

# The scan optimize_wifi made, kept for wifi_scan: no netifd, and both radios
# off in UCI — not scanned; each keeps a channel its width allows.
assert_setup_wifi_scan_cached() {
	_c=$(vcall wifi_scan)
	_r="$(jn "$_c" '@.radios[0].device') $(jn "$_c" '@.radios[0].error') cur=$(jn "$_c" '@.radios[0].current') rec=$(jn "$_c" '@.radios[0].recommended') n=$(jn "$_c" '@.radios[0].networks')"
	_r="$_r; $(jn "$_c" '@.radios[1].device') $(jn "$_c" '@.radios[1].error') cur=$(jn "$_c" '@.radios[1].current') rec=$(jn "$_c" '@.radios[1].recommended') n=$(jn "$_c" '@.radios[1].networks')"
	if [ "$_r" = "radio0 off cur= rec=6 n=0; radio1 off cur=36 rec=36 n=0" ] && [ -n "$(jn "$_c" '@.scannedAt')" ]; then
		record setup_wifi_scan_cached PASS "optimize_wifi's scan, kept: $_r; scannedAt $(jn "$_c" '@.scannedAt')"
	else
		record setup_wifi_scan_cached FAIL "'$_r' answer: $(echo "$_c" | tr -d '\n\t ' | cut -c1-240)"
	fi
}

# Named and keyed in the same call that tunes them: one transaction, both
# radios on and secured, and the Wi-Fi tuned.
assert_setup_wifi_optimize_named() {
	_a=$(vcall optimize_wifi '{"channels":{"radio0":6,"radio1":36},"radios":{"radio0":{"ssid":"Stand-Home","key":"stand-key-123"},"radio1":{"ssid":"Stand-Home-5G","key":"stand-key-555"}}}')
	_uci="$(uci_w default_radio0.ssid) $(uci_w default_radio0.encryption) $(uci_w default_radio0.key) $(uci_w default_radio1.ssid)"
	_uci="$_uci $(uci_w default_radio1.encryption) $(uci_w default_radio1.key) on=$(uci_w radio0.disabled)$(uci_w radio1.disabled) $(uci_w radio0.channel) $(uci_w radio1.channel)"
	_honest=no
	wifi_restart_honest optimize_named && _honest=yes
	_s=$(vcall setup)
	if [ "$(jf "$_a" '@.code')" = wifi_optimized ] && [ -z "$(jf "$_a" '@.detail')" ] \
		&& [ "$_uci" = "Stand-Home psk2 stand-key-123 Stand-Home-5G psk2 stand-key-555 on=00 6 36" ] \
		&& [ "$(jf "$_s" '@.wifi.tuned')" = true ] && [ "$(jf "$_s" '@.wifi.radios[0].enabled')" = true ] \
		&& [ "$(jf "$_s" '@.wifi.radios[1].secured')" = true ] && [ "$_honest" = yes ]; then
		record setup_wifi_optimize_named PASS "renamed, keyed and tuned in one call: $_uci; setup says tuned; the restart unverified, nothing rolled back"
	else
		record setup_wifi_optimize_named FAIL "answer=$(jf "$_a" '@.code') detail='$(jf "$_a" '@.detail')' uci='$_uci' tuned=$(jf "$_s" '@.wifi.tuned') verdict ok=$_honest"
	fi
}

# set_wifi per radio: renaming one band keeps its key (secured, none given)
# and leaves the other band as it is. A Wi-Fi change someone left
# uncommitted in uci's own save directory — which every uci commit reads —
# refuses it until it is reverted.
assert_setup_wifi_set() {
	uci set wireless.default_radio1.ssid=Stand-Pending
	_pending=$(vcall set_wifi '{"radios":{"radio1":{"ssid":"Stand-5G"}}}')
	uci revert wireless
	_r=$(jf "$(vcall set_wifi '{"radios":{"radio1":{"ssid":"Stand-5G"}}}')" '@.code')
	_uci="$(uci_w default_radio1.ssid) $(uci_w default_radio1.key) $(uci_w default_radio0.ssid) $(uci_w default_radio0.key)"
	_honest=no
	wifi_restart_honest set_wifi && _honest=yes
	if [ "$(jf "$_pending" '@.code')" = busy ] && jf "$_pending" '@.detail' | grep -q 'uncommitted Wi-Fi changes are waiting in uci' \
		&& [ "$_r" = wifi_set ] && [ "$_uci" = "Stand-5G stand-key-555 Stand-Home stand-key-123" ] \
		&& [ "$(wifi_radio 1 ssid)" = Stand-5G ] && [ "$(wifi_radio 1 secured)" = true ] && [ "$(wifi_radio 1 enabled)" = true ] \
		&& [ "$_honest" = yes ]; then
		record setup_wifi_set PASS "a change pending in uci refused (busy); then wifi_set: radio1 renamed, its key kept, still on; radio0 untouched ($_uci)"
	else
		record setup_wifi_set FAIL "pending: $(echo "$_pending" | tr -d '\n\t' | cut -c1-160); answer=$_r uci='$_uci' verdict ok=$_honest"
	fi
}

assert_setup_finish() {
	_r=$(jf "$(vcall finish_setup)" '@.code')
	_done=$(jf "$(vcall setup)" '@.done')
	if [ "$_r" = setup_finished ] && [ "$_done" = true ] && [ "$(uci -q get vectra-controller-pro.main.setup_done)" = 1 ]; then
		record setup_finish PASS "setup_finished: setup.done true, uci setup_done=1"
	else
		record setup_finish FAIL "answer=$_r done=$_done uci=$(uci -q get vectra-controller-pro.main.setup_done)"
	fi
}

# The owner's router in service: linked with a config (kill switch armed),
# xray carrying the LAN through the stand's nodes, and choices made on it — a
# location, a pin, My sites.
setup_link_router() {
	sh "$STAND/setup/link.sh" > /tmp/setup-link.log 2>&1 || { tail -n 20 /tmp/setup-link.log; die "linking the stand's router failed"; }
	ui_wait 40 engine_running || die "xray never came up after linking"
	ui_wait 30 vctl_table_loaded || die "the data plane never loaded after linking"
	_n=$(checkins)
	ui_wait 20 checked_in_since "$_n" # the check-in that confirms the ruleset
	ui_wait 20 node_alive node-b || true
	_sel=$(jf "$(vcall select_entry '{"index":0}')" '@.code')
	_pin=$(jf "$(vcall pin_balancer '{"balancer":"BL-MAIN","node":"node-b"}')" '@.code')
	_rul=$(jf "$(vcall set_rules '{"direct":["example.org"],"proxy":["example.net"]}')" '@.code')
	ui_wait 40 overrides_have '"example.org"' || true
	ui_wait 40 override_is BL-MAIN node-b || true
	info "linked, kill switch armed; on the router: select_entry=$_sel pin_balancer=$_pin set_rules=$_rul"
}

assert_setup_in_service() {
	_s=$(vcall setup)
	_via=$(client_via)
	_fwd=$(lan_forwarded)
	_ov=/etc/vectra-controller-pro/local-overrides.json
	if [ "$(jf "$_s" '@.vectra.linked')" = true ] && [ -z "$(jf "$_s" '@.vectra.claim.code')" ] \
		&& [ "$(jf "$_s" '@.vectra.owner.label')" = "$SETUP_OWNER" ] && [ "$_via" = b-in ] && [ "$_fwd" = 0 ] \
		&& grep -q '"entryRemark"' "$_ov" && grep -q '"BL-MAIN": "node-b"' "$_ov" && grep -q '"example.org"' "$_ov" \
		&& vctl_table_loaded; then
		record setup_in_service PASS "linked, owner '$SETUP_OWNER', no claim; the LAN client carried by node-b (pinned), 0 packets forwarded; location, pin and My sites kept on the router"
	else
		record setup_in_service FAIL "linked=$(jf "$_s" '@.vectra.linked') owner='$(jf "$_s" '@.vectra.owner.label')' via='$_via' forwarded=$_fwd overrides: $(tr -d '\n ' < "$_ov" 2>/dev/null | cut -c1-160)"
	fi
}

# The owner unbinds the router in the Vectra app: from its next check-in on,
# the panel says "released": true, "owner": null.
setup_release() {
	# The owner set the router's password through LuCI's own banner; the
	# release must keep it.
	printf 'stand-pass-123\nstand-pass-123\n' | passwd root >/dev/null 2>&1
	SETUP_ROOT_HASH=$(awk -F: '$1=="root" {print $2}' /etc/shadow)
	SETUP_XRAY_PID=$(xray_pid)
	ui_lock 1 # kept across the release, like the rest of UCI
	touch "$SETUP_PANEL_RELEASED"
	ui_wait 30 linked_is false || info "setup still says linked 30 s after the release"
}

# Traffic, files, state and claim: as out of the box. UCI untouched.
assert_setup_released() {
	_s=$(vcall setup)
	_code=$(jf "$_s" '@.vectra.claim.code')
	_state=$(jf "$_s" '@.vectra.claim.state')
	_owner=$(jf "$_s" '@.vectra.owner.label')$(jf "$_s" '@.vectra.claim.owner.label')
	_engine=$(jf "$(vcall status)" '@.engine.state')
	_left=""
	for _f in $SETUP_OWNER_FILES; do [ -e "$_f" ] && _left="$_left $(basename "$_f")"; done
	_stleft=""
	for _k in claim_owner applied_revision_id config_digest splice_key last_desired_revision; do
		[ -n "$(jsonfilter -i "$SETUP_STATE" -e "@.$_k" 2>/dev/null)" ] && _stleft="$_stleft $_k"
	done
	_table=gone
	vctl_table_loaded && _table=loaded
	_rules=$(ip rule 2>/dev/null | grep -c 'lookup 100')
	_procs=$(router_xray_procs)
	_kept=yes
	[ "$(uci -q get vectra-controller-pro.main.ui_lock)" = 1 ] && [ "$(uci -q get vectra-controller-pro.main.setup_done)" = 1 ] \
		&& [ "$(uci -q get wireless.default_radio0.ssid)" = Stand-Home ] && [ "$(uci -q get wireless.default_radio0.key)" = stand-key-123 ] \
		&& [ -n "$SETUP_ROOT_HASH" ] && [ "$(awk -F: '$1=="root" {print $2}' /etc/shadow)" = "$SETUP_ROOT_HASH" ] || _kept=no
	if [ "$(jf "$_s" '@.vectra.linked')" = false ] && [ "$_state" = unclaimed ] && [ "${#_code}" = 8 ] && [ -z "$_owner" ] \
		&& [ "$_engine" != running ] && [ "$_procs" = 0 ] && [ -n "$SETUP_XRAY_PID" ] && [ ! -d "/proc/$SETUP_XRAY_PID" ] \
		&& [ -z "$_left" ] && [ -z "$_stleft" ] && [ "$_table" = gone ] && [ "$_rules" = 0 ] && [ "$_kept" = yes ]; then
		record setup_released PASS "linked=false, a new code $_code, no owner; xray $_engine (pid $SETUP_XRAY_PID gone), vctl table and fwmark rule gone; config, subscription, locations, render, choices and applied revision removed; Wi-Fi, root password, setup_done, ui_lock kept"
	else
		record setup_released FAIL "linked=$(jf "$_s" '@.vectra.linked') state=$_state code='$_code' owner='$_owner' engine=$_engine procs=$_procs pid '$SETUP_XRAY_PID' alive=$([ -d "/proc/$SETUP_XRAY_PID" ] && echo yes || echo no) files left:${_left:- none} state left:${_stleft:- none} table=$_table rules=$_rules uci/shadow kept=$_kept"
	fi
	ui_lock 0
}

# No VPN interception and no kill-switch black hole: the LAN client reaches
# the origin by plain forwarding (the counter moves), and no node carried it.
assert_setup_released_direct() {
	_f0=$(lan_forwarded)
	_via=$(client_via)
	_body=$(in_client curl -s --max-time 8 "http://$ORIGIN_IP/hello" 2>/dev/null)
	_f1=$(lan_forwarded)
	if echo "$_body" | grep -q "$EXPECT_BODY" && [ -z "$_via" ] && [ "${_f1:-0}" -gt "${_f0:-0}" ]; then
		record setup_released_direct PASS "the LAN client got the origin's answer directly: $((_f1 - _f0)) packets forwarded by the router, none carried by a node"
	else
		record setup_released_direct FAIL "body='$(echo "$_body" | cut -c1-30)' via='$_via' forwarded $_f0 -> $_f1"
	fi
}

# The panel keeps saying "released" until the router is claimed again. Once
# wiped there is no owner: the next answers change nothing — a choice made on
# the router since survives them.
assert_setup_released_once() {
	_ov=/etc/vectra-controller-pro/local-overrides.json
	printf '{\n  "probeIntervalSec": 300\n}\n' > "$_ov"
	_n=$(checkins)
	ui_wait 20 checked_in_since "$_n" 2
	if [ -s "$_ov" ] && grep -q 'probeIntervalSec' "$_ov" && linked_is false && [ -n "$(claim_code)" ] && [ "$(checkins)" -ge $((_n + 2)) ]; then
		record setup_released_once PASS "$(($(checkins) - _n)) more released answers: nothing removed again"
	else
		record setup_released_once FAIL "after $(($(checkins) - _n)) released answers: overrides $([ -e "$_ov" ] && echo kept || echo REMOVED), linked=$(jf "$(vcall setup)" '@.vectra.linked')"
	fi
	rm -f "$_ov" "$SETUP_PANEL_RELEASED" "$SETUP_PANEL_OWNER"
}

# -------------------------------------------------------------------- main ---

run_setup_mode() {
	setup_kernel
	setup_topology
	start_origins
	setup_isolate
	setup_dummy_network
	ui_start_nodes
	ui_start_subscription
	# The observatory's probe destination (the ui mode starts it in ui_isolate):
	# without it every node looks dead once the router is linked, and a held
	# router would honestly — but uselessly — say its traffic goes direct.
	in_inet socat "TCP-LISTEN:$UI_PROBE_PORT,bind=$ORIGIN_IP,fork,reuseaddr" "SYSTEM:$STAND/http-reply.sh" >/dev/null 2>&1 &
	ui_boot_services

	say "install the package on a fresh router; the daemon checks in unlinked"
	setup_start

	if [ "$MODE" = setup-hold ]; then
		setup_dummy_wireless
		say "HOLD: LuCI at http://127.0.0.1:${STAND_UI_PORT:-8080}/cgi-bin/luci/ (root, empty password)"
		info "name an owner:        docker exec vctl-setup-hold sh -c 'echo \"$SETUP_OWNER\" > $SETUP_PANEL_OWNER'"
		info "configure it (link):  docker exec vctl-setup-hold sh /stand/setup/link.sh"
		info "the owner unbinds it: docker exec vctl-setup-hold touch $SETUP_PANEL_RELEASED"
		printf 'STAND-HOLD ready\n'
		while :; do sleep 3600; done
	fi

	say "the wizard's view of a fresh router, and the claim"
	assert_setup_shape
	assert_setup_register_proof
	assert_setup_wan_readonly
	assert_setup_checkin_codehash
	assert_setup_claim_rotates
	assert_setup_under_the_lock
	assert_setup_claimed

	say "the wizard's changes"
	assert_setup_wifi_radios
	assert_setup_wifi_scan_empty
	assert_setup_wifi_unsupported
	assert_setup_wifi_optimize_open
	assert_setup_wifi_scan_cached
	assert_setup_wifi_optimize_named
	assert_setup_wifi_set
	assert_setup_finish

	say "the owner's router in service: linked, the kill switch armed, choices made on it"
	setup_forward_counter
	setup_link_router
	assert_setup_in_service

	say "the owner unbinds it in the Vectra app"
	setup_release
	assert_setup_released
	assert_setup_released_direct
	assert_setup_released_once

	[ "$FAILURES" -gt 0 ] && {
		say "diagnostics for the failures above"
		tail -n 5 "$SETUP_PANEL_LOG" 2>/dev/null | cut -c1-240
		echo "--- setup ---"; vcall setup | head -c 1200; echo
		echo "--- nft tables / ip rule ---"; nft list tables 2>&1; ip rule list 2>&1
		echo "--- xray ---"; ps w 2>/dev/null | grep -v grep | grep xray
		echo "--- files ---"; ls -la /etc/vectra-controller-pro /var/run/vectra-controller-pro 2>&1
		echo "--- link ---"; tail -n 15 /tmp/setup-link.log 2>/dev/null
		logread 2>/dev/null | grep -E 'vctl|vectra' | tail -n 30
	}
}
