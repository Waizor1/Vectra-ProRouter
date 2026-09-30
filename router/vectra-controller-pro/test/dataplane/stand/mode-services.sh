# shellcheck shell=sh
# A server per service, end to end (docs/superpowers/specs/2026-09-30-vctl-
# service-routing-design.md, decision 3; plan 2026-09-30-vctl-service-choice).
#
# Sourced by stand.sh. The ui mode's stand — the package installed with opkg,
# procd, rpcd, the daemon running xray, real xray "provider nodes" in the
# inet ns — with an entry whose outbounds name countries: sticky-de1 (node a,
# :2001), sticky-by1 (node b, :2002), sticky-nl1 (node c, :2004). BL-MAIN is
# Germany; TikTok (geosite:tiktok) goes to BL-TK, Belarus.
#
# Through the router's own UI methods (ubus vectra services / set_service),
# proven on the packets: which node's inbound carried a TikTok request, and
# that the rest stays where it was.

. "$STAND/mode-ui.sh"

SV_DIR=/stand/services
SV_TIKTOK=www.tiktok.com

sv_ms() { awk '{printf "%d\n", $1 * 1000}' /proc/uptime; }

sv_fixtures() {
	UI_DIR=/tmp/services-fixtures
	rm -rf "$UI_DIR"
	mkdir -p "$UI_DIR"
	cp /stand/ui/panel-ok.sh /stand/ui/operator-config.json /stand/ui/nodes-server.json "$UI_DIR/"
	cp "$SV_DIR/subscription.json" "$UI_DIR/subscription.json"
}

sv_seed_and_apply() {
	mkdir -p /tmp/sysinfo /etc/vectra-controller-pro
	echo "Vectra Services Stand (synthetic)" > /tmp/sysinfo/model
	cp "$UI_DIR/operator-config.json" /etc/vectra-controller-pro/xray-desired.json
	if ! vctl apply-local -config "$UI_AGENT" > /tmp/sv-apply.log 2>&1; then
		tail -n 20 /tmp/sv-apply.log
		die "vctl apply-local failed"
	fi
	/etc/init.d/vectra-controller-pro restart
	ui_wait 40 engine_running || { ui_dump; die "xray never came up under the daemon"; }
	ui_wait 40 panel_checked_in || info "no check-in reached the stub panel yet"
	for n in sticky-de1 sticky-by1 sticky-nl1; do
		ui_wait 30 node_alive "$n" || { ui_dump; die "the observatory never held $n alive"; }
	done
	info "daemon up; xray running (pid $(xray_pid)); the three nodes alive"
}

# sv_row <id> <expr>: one field of a service's row in the services answer.
sv_row() { ubus -t 20 call vectra services 2>/dev/null | jsonfilter -e "@.services[@.id='$1']$2" 2>/dev/null; }
sv_active() { [ "$(sv_row "$1" .active)" = true ]; }
sv_default() { [ -z "$(sv_row "$1" .choice)" ] && [ "$(sv_row "$1" .active)" = false ]; }

# sv_tiktok: the node inbound(s) that carried a TikTok request (TLS, the name
# in the SNI), "" when none did.
sv_tiktok() { site_via https "$SV_TIKTOK"; }
sv_tiktok_is() { [ "$(sv_tiktok)" = "$1" ]; }

# sv_set <params>: set_service, then wait until the router runs it — a new
# xray, running, its nodes probed again (the render is written before xray
# restarts; a request in that window would meet no proxy at all).
sv_set() {
	_sp=$(xray_pid)
	_sc=$(jf "$(vcall set_service "$1")" '@.code')
	case "$_sc" in
	service_set | pending) ;;
	*) echo "answered '$_sc'"; return 1 ;;
	esac
	ui_wait 30 xray_pid_changed "$_sp" || { echo "$_sc, but xray was not restarted"; return 1; }
	ui_wait 30 engine_running
	for _n in sticky-de1 sticky-by1 sticky-nl1; do
		ui_wait 20 node_alive "$_n" || true
	done
	echo "$_sc"
}

sv_drop() { # <port>: the node's port stops answering — its SYNs vanish
	in_inet nft -f - <<EOF
table inet svstand {
  chain input {
    type filter hook input priority filter; policy accept;
    tcp dport $1 counter drop
  }
}
EOF
}
sv_undrop() { in_inet nft delete table inet svstand 2>/dev/null; }

# ----------------------------------------------------------------- asserts ---

assert_sv_answer() {
	_c=$(sv_row tiktok '.countries[*]' | tr '\n' ',' | sed 's/,$//')
	_d=$(sv_row tiktok .defaultCountry)
	_a=$(ubus -t 20 call vectra services 2>/dev/null | jsonfilter -e '@.available' 2>/dev/null)
	if [ "$_a" = true ] && [ "$_c" = "BY,DE,NL" ] && [ "$_d" = BY ] && sv_default tiktok; then
		record svc_answer PASS "available; TikTok on its own path (BY); the entry offers $_c"
	else
		record svc_answer FAIL "available=$_a countries=$_c default=$_d choice=$(sv_row tiktok .choice) active=$(sv_row tiktok .active)"
	fi
}

assert_sv_default_path() {
	_via=$(sv_tiktok)
	if [ "$_via" = b-in ]; then
		record svc_default_path PASS "a TikTok request went through BL-TK's node (Belarus, b-in)"
	else
		record svc_default_path FAIL "a TikTok request went through '${_via:-no node}', want b-in"
	fi
}

assert_sv_choice_steers() {
	_code=$(sv_set '{"id":"tiktok","country":"NL"}') || {
		record svc_choice_steers FAIL "set_service tiktok NL: $_code"
		return
	}
	if ! ui_wait 40 sv_active tiktok; then
		record svc_choice_steers FAIL "set_service answered '$_code'; the choice never ran (active=$(sv_row tiktok .active))"
		return
	fi
	ui_wait 20 node_alive sticky-nl1 || true
	_via=$(sv_tiktok)
	_rest=$(client_via)
	if [ "$_via" = c-in ] && [ "$_rest" = a-in ] && grep -q '"VCTL-SVC-TIKTOK"' "$UI_RENDER"; then
		record svc_choice_steers PASS "set_service tiktok NL ($_code): TikTok through the Dutch node (c-in); the rest still through BL-MAIN (a-in)"
	else
		record svc_choice_steers FAIL "TikTok via '${_via:-none}' (want c-in), the rest via '${_rest:-none}' (want a-in)"
	fi
}

assert_sv_refuses() {
	_c1=$(jf "$(vcall set_service '{"id":"tiktok","country":"FR"}')" '@.code')
	_c2=$(jf "$(vcall set_service '{"id":"netflix","country":"NL"}')" '@.code')
	if [ "$_c1" = unknown_country ] && [ "$_c2" = unknown_service ] && [ "$(sv_row tiktok .choice)" = NL ]; then
		record svc_refuses PASS "a country the entry lacks: unknown_country; a service without a choice: unknown_service; the choice kept"
	else
		record svc_refuses FAIL "FR: '$_c1' (want unknown_country), netflix: '$_c2' (want unknown_service), choice now '$(sv_row tiktok .choice)'"
	fi
}

# The chosen country dies: TikTok falls back to its own path, not to nothing.
assert_sv_dead_country_falls_back() {
	sv_drop 2004 || die "could not drop the Dutch node's port"
	_t0=$(sv_ms)
	_t1=""
	while [ $(($(sv_ms) - _t0)) -lt 30000 ]; do
		_s=$(sv_ms)
		if in_client curl -sk --max-time 1 -o /dev/null --resolve "$SV_TIKTOK:443:$ORIGIN_IP" "https://$SV_TIKTOK/hello" 2>/dev/null; then
			_t1=$(sv_ms)
			break
		fi
		[ $(($(sv_ms) - _s)) -lt 500 ] && sleep 1
	done
	_via=$(sv_tiktok)
	sv_undrop
	if [ -n "$_t1" ] && [ $((_t1 - _t0)) -le 10000 ] && [ "$_via" = b-in ]; then
		record svc_dead_country PASS "the Dutch node dropped: TikTok back on its own path (b-in) after $((_t1 - _t0)) ms"
	else
		record svc_dead_country FAIL "TikTok ${_t1:+through after $((_t1 - _t0)) ms}${_t1:-never through in 30 s}, then via '${_via:-none}' (want b-in within 10 s)"
	fi
}

assert_sv_back_to_default() {
	_code=$(sv_set '{"id":"tiktok","country":""}') || {
		record svc_back_to_default FAIL "set_service tiktok \"\": $_code"
		return
	}
	if ! ui_wait 40 sv_default tiktok; then
		record svc_back_to_default FAIL "set_service tiktok \"\" answered '$_code'; still choice=$(sv_row tiktok .choice) active=$(sv_row tiktok .active)"
		return
	fi
	_via=$(sv_tiktok)
	if [ "$_via" = b-in ] && ! grep -q '"VCTL-SVC-TIKTOK"' "$UI_RENDER"; then
		record svc_back_to_default PASS "back to the entry's own path: TikTok through b-in, no overlay in the render"
	else
		record svc_back_to_default FAIL "TikTok via '${_via:-none}' (want b-in); overlay in the render: $(grep -c VCTL-SVC-TIKTOK "$UI_RENDER")"
	fi
}

# «+ сервис» (spec decision 4): the router offers its geo file's categories
# (never "private"), keeps one only when that file has it, and a service in
# «Без VPN» leaves by xray's freedom — no node carries it — where before a
# node did.
assert_sv_plus_service() {
	_cat=$(ubus -t 20 call vectra rules 2>/dev/null | jsonfilter -e '@.catalog[*]' 2>/dev/null | tr '\n' ',')
	_before=$(site_via https discord.com)
	_sp=$(xray_pid)
	_c1=$(jf "$(vcall set_rules '{"direct":["geosite:discord"],"proxy":[]}')" '@.code')
	ui_wait 40 rules_are "geosite:discord" "" || true
	ui_wait 30 xray_pid_changed "$_sp" || true
	ui_wait 30 engine_running
	ui_wait 20 node_alive sticky-de1 || true
	_after=$(site_via https discord.com)
	_ok=no
	site_ok && _ok=yes
	_c2=$(jf "$(vcall set_rules '{"direct":["geosite:nosuch"],"proxy":[]}')" '@.code')
	_rendered=no
	grep -q '"geosite:discord"' "$UI_RENDER" && _rendered=yes
	_sp=$(xray_pid)
	vcall set_rules '{"direct":[],"proxy":[]}' >/dev/null
	ui_wait 40 rules_are "" "" || true
	ui_wait 30 xray_pid_changed "$_sp" || true
	ui_wait 30 engine_running
	ui_wait 20 node_alive sticky-de1 || true
	case ",$_cat" in *,discord,*) _has=yes ;; *) _has=no ;; esac
	case ",$_cat" in *,private,*) _priv=yes ;; *) _priv=no ;; esac
	if [ "$_has" = yes ] && [ "$_priv" = no ] && [ "$_before" = a-in ] && [ -z "$_after" ] && [ "$_ok" = yes ] &&
		[ "$_rendered" = yes ] && { [ "$_c1" = rules_set ] || [ "$_c1" = pending ]; } && [ "$_c2" = unknown_service ]; then
		record svc_plus_service PASS "catalog has discord (not private); Discord through node a before, then with geosite:discord in «Без VPN» ($_c1) straight out, no node; an unknown category: $_c2"
	else
		record svc_plus_service FAIL "catalog discord=$_has private=$_priv; before '$_before' (want a-in), after '$_after' ok=$_ok (want none, yes); set $_c1, rendered $_rendered; unknown '$_c2'"
	fi
}

run_services_mode() {
	sv_fixtures
	setup_kernel
	setup_topology
	setup_guard
	start_origins
	ui_isolate
	ui_start_nodes
	ui_start_subscription
	ui_boot_services
	/sbin/logd -S 256 >/dev/null 2>&1 &
	ui_wait 10 logread || die "logd never came up"

	say "install the package; an entry whose outbounds name countries"
	ui_install_package
	ui_wait 15 ubus list vectra || die "the package installed but rpcd never exposed 'vectra'"
	sv_seed_and_apply

	say "what the router offers, and TikTok on its own path"
	assert_sv_answer
	assert_sv_default_path

	say "TikTok through the Netherlands, chosen in the router's UI"
	assert_sv_choice_steers
	assert_sv_refuses
	assert_sv_dead_country_falls_back

	say "back to the default"
	assert_sv_back_to_default

	say "«+ сервис»: a whole service around the VPN"
	assert_sv_plus_service

	say "data plane integrity"
	assert_tcp
	assert_no_leak
	[ "$FAILURES" -gt 0 ] && { say "diagnostics for the failures above"; ui_dump; }
}
