# shellcheck shell=sh
# DNS sessions live seconds, not the provider's minutes (r35).
#
# Sourced by stand.sh. The ru-kernel mode's stand: the package installed with
# opkg, procd, the daemon running xray, real xray "provider nodes" in the inet
# ns, and the router's dnsmasq running as a user of its own, so its upstream
# queries go into xray's DNS inbound (dns_steer). Then a burst of unique names
# through that dnsmasq, from the LAN client.
#
# dnsmasq asks every upstream query from a port of its own, so each is a
# session of xray's DNS inbound and of its DNS outbound. Before r35 they lived
# the provider's connIdle: on the test router 600 lookups left 2136 sessions
# and 6400 goroutines behind for two minutes, each a buffer and stacks the
# garbage collector walks. Measured here on xray itself (its goroutines, read
# from pprof on the metrics listener; three per DNS session, about twenty for
# the rest of xray at rest): up during the burst, back down within 20 s, and
# every name answered.
#
# Then the door (firewall.Spec.DNSRate): one device floods the router's
# resolver, 1000 unique names a second for 10 s. Over its rate (100 a second,
# 2000 at once) the queries are dropped at the router's door, xray gets under
# a third of them, the router's own lookups are answered throughout, and the
# device's are again a few seconds after.
#
# Last, the router's whole door (firewall.Spec.AdmitTotalRate, off unless
# set): with UCI admit_total_rate '5', 40 connections at once from the LAN —
# within the device's own door — are held past the first 20 and still get
# through, TCP asking again.
#
# The controls: dns-sessions-provider-idle — the same run with xray given the
# render with the DNS sessions back on the provider's level (a shim in place
# of the wrapper, the router before r35): its sessions must still be alive
# 20 s after the burst. dns-door-off — UCI dns_rate '0': the storm reaches
# xray whole.

. "$STAND/mode-rukernel.sh"

DS_NAMES=240 # unique names in a burst, A and AAAA each: within the door's 2000
DS_PAR=12    # asked at once
# Seconds after the burst the sessions must be gone by. xray looks at a
# session's idleness once per connIdle, and the first look finds the session's
# own start: one ends 8-16 s after its last packet on the DNS level (8 s).
DS_SETTLE=20

ds_goroutines() {
	curl -s --max-time 5 "http://127.0.0.1:10086/debug/pprof/goroutine?debug=1" 2>/dev/null \
		| sed -n 's/^goroutine profile: total \([0-9]*\).*/\1/p'
}
ds_pprof_up() { [ -n "$(ds_goroutines)" ]; }
ds_held() { nft list counter inet vctl vctl_dns_held 2>/dev/null | sed -n 's/.*packets \([0-9]*\).*/\1/p' | head -n 1 | grep . || echo 0; }

# The flood: DS_CHUNKS files of DS_CHUNK queries (type A, 37 bytes each, a
# name of its own each, f00000..f09999) that socat sends one datagram a query.
# No subshell per query: printf is the shell's own.
DS_CHUNKS=10
DS_CHUNK=1000
ds_flood_files() {
	_c=0
	while [ "$_c" -lt "$DS_CHUNKS" ]; do
		_i=0
		while [ "$_i" -lt "$DS_CHUNK" ]; do
			_n=00000$((_c * DS_CHUNK + _i))
			_n=${_n#"${_n%?????}"}
			# shellcheck disable=SC2059
			printf "\001\002\001\000\000\001\000\000\000\000\000\000\006f${_n}\006origin\005stand\000\000\001\000\001"
			_i=$((_i + 1))
		done > "/tmp/ds-flood.$_c"
		[ "$(wc -c < "/tmp/ds-flood.$_c")" -eq $((DS_CHUNK * 37)) ] || die "flood chunk $_c is $(wc -c < "/tmp/ds-flood.$_c") bytes"
		_c=$((_c + 1))
	done
}
# ds_stacks: xray's goroutines grouped by where they wait, the biggest groups
# first (their first frames outside the runtime).
ds_stacks() {
	curl -s --max-time 5 "http://127.0.0.1:10086/debug/pprof/goroutine?debug=1" 2>/dev/null | awk '
		/^[0-9]+ @/ { if (n) print n "\t" f; n = $1; f = ""; k = 0; next }
		/^#/ { if (k < 3 && $3 !~ /^runtime\./) { sub(/\+0x[0-9a-f]+$/, "", $3); f = f " " $3; k++ } }
		END { if (n) print n "\t" f }' | sort -rn | head -n 8 | while IFS= read -r l; do info "  $l"; done
}
ds_rss() { awk '/^VmRSS/{print $2; exit}' "/proc/$(xray_pid)/status" 2>/dev/null; }

# ds_burst <tag>: DS_NAMES unique names under origin.stand (the stand's
# resolver answers every one with the origin's address), DS_PAR at a time,
# through the router's dnsmasq. Prints how many were answered right.
ds_burst() {
	_ok=/tmp/ds-ok.$1
	: > "$_ok"
	_pids=
	_p=0
	while [ "$_p" -lt "$DS_PAR" ]; do
		(
			_i=$_p
			while [ "$_i" -lt "$DS_NAMES" ]; do
				[ "$(rk_answer "n$_i-$1.origin.stand")" = "$ORIGIN_IP" ] && echo "$_i" >> "$_ok"
				_i=$((_i + DS_PAR))
			done
		) &
		_pids="$_pids $!"
		_p=$((_p + 1))
	done
	# Only the burst's own workers: dnsmasq and the origins run in the
	# background of this shell too.
	# shellcheck disable=SC2086
	wait $_pids
	wc -l < "$_ok" | tr -d ' '
}

# The control: xray runs the render with every userLevel 0 — the DNS inbound
# and outbound on the provider's level, as they were before r35. vctl's render
# itself is untouched.
ds_provider_idle_shim() {
	cat > /usr/sbin/vctl-xray-wrapper <<'EOF'
#!/bin/sh
export GOMEMLIMIT="${GOMEMLIMIT:-80MiB}" GOGC="${GOGC:-30}"
export XRAY_LOCATION_ASSET="${XRAY_LOCATION_ASSET:-/usr/share/vectra-controller-pro/geo}"
_n=$#
_prev=
while [ "$_n" -gt 0 ]; do
	_orig=$1
	shift
	_a=$_orig
	if [ "$_prev" = -c ]; then
		_a=/tmp/xray-provider-idle.$$.json
		sed 's/"userLevel":[0-9][0-9]*/"userLevel":0/g' "$_orig" > "$_a"
	fi
	set -- "$@" "$_a"
	_prev=$_orig
	_n=$((_n - 1))
done
exec -a "$0" /usr/bin/xray "$@"
EOF
	chmod 755 /usr/sbin/vctl-xray-wrapper
	info "control: xray runs the render with the DNS sessions on the provider's level"
}

# ----------------------------------------------------------------- asserts ---

assert_ds_level_rendered() {
	_lvl=$(jsonfilter -i "$UI_RENDER" -e '@.inbounds[@.tag="vctl-dns-in"].settings.userLevel' 2>/dev/null)
	_out=$(jsonfilter -i "$UI_RENDER" -e '@.outbounds[@.tag="vctl-dns-out"].settings.userLevel' 2>/dev/null)
	if [ -n "$_lvl" ] && [ "$_lvl" = "$_out" ] \
		&& grep -q "\"$_lvl\":{\"handshake\":4,\"connIdle\":8,\"uplinkOnly\":0,\"downlinkOnly\":0,\"bufferSize\":0}" "$UI_RENDER"; then
		record ds_level_rendered PASS "the DNS inbound and outbound run on level $_lvl: idle 8 s (gone 8-16 s after the last packet), closed as soon as a side is done"
	else
		record ds_level_rendered FAIL "inbound level '$_lvl', outbound level '$_out'; policy: $(grep -o '"policy":{[^}]*}[^}]*}' "$UI_RENDER" | head -1 | cut -c1-200)"
	fi
}

run_dnssessions_mode() {
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

	say "install the package; the router's dnsmasq asks through the tunnel"
	ui_install_package
	ui_wait 15 ubus list vectra || die "the package installed but rpcd never exposed 'vectra'"
	[ "$MODE" = dns-sessions-provider-idle ] && ds_provider_idle_shim
	if [ "$MODE" = dns-door-off ]; then
		uci set vectra-controller-pro.main.dns_rate=0
		uci commit vectra-controller-pro
		info "UCI dns_rate '0': the control, no door"
	fi
	rk_dnsmasq
	rk_seed_and_apply
	ui_wait 20 ds_pprof_up || die "xray's pprof on the metrics listener never answered"

	say "the render"
	assert_ds_level_rendered

	say "a burst of $DS_NAMES unique names, $DS_PAR at a time"
	# A first burst warms xray's resolver and the nodes' connections; what
	# it leaves must be gone before the baseline.
	ds_burst warm >/dev/null
	sleep "$DS_SETTLE"
	_g0=$(ds_goroutines) _r0=$(ds_rss)
	info "xray's goroutines at rest, by where they wait:"
	ds_stacks
	_t0=$(date +%s)
	_ans=$(ds_burst run)
	_t1=$(date +%s)
	_g1=$(ds_goroutines) _r1=$(ds_rss)
	info "right after the burst:"
	ds_stacks
	sleep "$DS_SETTLE"
	_g2=$(ds_goroutines) _r2=$(ds_rss)
	info "xray goroutines: before $_g0, after the burst $_g1, $DS_SETTLE s later $_g2 (RSS kB: $_r0 / $_r1 / $_r2); the burst took $((_t1 - _t0)) s"

	if [ "$_ans" = "$DS_NAMES" ]; then
		record ds_burst_answered PASS "every one of $DS_NAMES unique names answered with the origin's address"
	else
		record ds_burst_answered FAIL "$_ans of $DS_NAMES names answered right"
	fi
	# Anti-vacuity: the burst did leave sessions behind in xray.
	if [ -n "$_g0" ] && [ -n "$_g1" ] && [ "$((_g1 - _g0))" -ge "$DS_NAMES" ]; then
		record ds_burst_made_sessions PASS "the burst raised xray's goroutines by $((_g1 - _g0)) ($_g0 -> $_g1)"
	else
		record ds_burst_made_sessions FAIL "goroutines $_g0 -> $_g1: the burst left no sessions to watch"
	fi
	# Gone: back to the baseline, within a tenth of what the burst added.
	_slack=$(((_g1 - _g0) / 10))
	[ "$_slack" -lt 40 ] && _slack=40
	if [ -n "$_g2" ] && [ "$_g2" -le "$((_g0 + _slack))" ]; then
		record dns_sessions_die_in_seconds PASS "$DS_SETTLE s after the burst xray is back to $_g2 goroutines (baseline $_g0, peak $_g1)"
	else
		record dns_sessions_die_in_seconds FAIL "$DS_SETTLE s after the burst xray still runs $_g2 goroutines (baseline $_g0, peak $_g1, allowed $((_g0 + _slack)))"
	fi

	say "the door: one device floods the router's resolver, $DS_CHUNK names a second for $DS_CHUNKS s"
	ds_flood_files
	sleep "$DS_SETTLE"
	_h0=$(ds_held) _g0=$(ds_goroutines)
	_c=0
	while [ "$_c" -lt "$DS_CHUNKS" ]; do
		in_client socat -u -b 37 "OPEN:/tmp/ds-flood.$_c" "UDP4-SENDTO:$ROUTER_CLIENT_IP:53" 2>/dev/null
		_c=$((_c + 1))
		[ "$_c" -lt "$DS_CHUNKS" ] && sleep 1
	done
	_g1=$(ds_goroutines)
	_mine=$(nslookup "router-self.origin.stand" "$ROUTER_CLIENT_IP" 2>/dev/null | awk '/^Name:/{n=1} n && /^Address/ {print $NF; exit}')
	_h1=$(ds_held)
	_sent=$((DS_CHUNKS * DS_CHUNK))
	# What the door lets through: its burst, then its rate each second.
	_cap=$(((2000 + 100 * DS_CHUNKS) * 3 * 13 / 10))
	info "the flood: $_sent queries; held at the door $((_h1 - _h0)); xray goroutines $_g0 -> $_g1 (allowed rise $_cap)"
	if [ "$((_h1 - _h0))" -ge "$((_sent / 2))" ] && [ "$((_g1 - _g0))" -le "$_cap" ]; then
		record dns_door_holds_a_storm PASS "$((_h1 - _h0)) of $_sent queries held at the door; xray rose by $((_g1 - _g0)) goroutines (allowed $_cap)"
	else
		record dns_door_holds_a_storm FAIL "held $((_h1 - _h0)) of $_sent (want >= $((_sent / 2))); xray rose by $((_g1 - _g0)) goroutines (allowed $_cap)"
	fi
	if [ "$_mine" = "$ORIGIN_IP" ]; then
		record dns_door_spares_the_router PASS "the router's own lookup, right after the flood: answered ($_mine)"
	else
		record dns_door_spares_the_router FAIL "the router's own lookup right after the flood: '${_mine:-no answer}'"
	fi
	sleep 4
	_back=$(rk_answer "device-back.origin.stand")
	if [ "$_back" = "$ORIGIN_IP" ]; then
		record dns_door_lets_the_device_back PASS "the flooding device's lookup 4 s later: answered"
	else
		record dns_door_lets_the_device_back FAIL "the flooding device's lookup 4 s later: '${_back:-no answer}'"
	fi

	say "the router's whole door: UCI admit_total_rate '5', 40 connections at once"
	assert_ds_whole_door
	[ "$FAILURES" -gt 0 ] && { say "diagnostics for the failures above"; ui_dump; }
}

ds_total_held() { nft list counter inet vctl vctl_admit_total_held 2>/dev/null | sed -n 's/.*packets \([0-9]*\).*/\1/p' | head -n 1; }

assert_ds_whole_door() {
	if nft list counter inet vctl vctl_admit_total_held >/dev/null 2>&1; then
		record ds_whole_door FAIL "the whole door is loaded with admit_total_rate unset"
		return
	fi
	uci set vectra-controller-pro.main.admit_total_rate=5
	uci commit vectra-controller-pro
	/etc/init.d/vectra-controller-pro restart
	ui_wait 40 engine_running || { ui_dump; die "xray never came back after the restart"; }
	ui_wait 30 sh -c 'nft list counter inet vctl vctl_admit_total_held >/dev/null 2>&1' || die "the whole door never loaded"
	for n in sticky-de1 sticky-by1; do
		ui_wait 30 node_alive "$n" || { ui_dump; die "the observatory never held $n alive after the restart"; }
	done
	_h0=$(ds_total_held)
	: > /tmp/ds-door.ok
	_i=0 _pids=
	while [ "$_i" -lt 40 ]; do
		(in_client curl -s --max-time 15 "http://$ORIGIN_IP/hello" 2>/dev/null | grep -q "$EXPECT_BODY" && echo ok >> /tmp/ds-door.ok) &
		_pids="$_pids $!"
		_i=$((_i + 1))
	done
	# shellcheck disable=SC2086
	wait $_pids
	_h1=$(ds_total_held)
	_ok=$(wc -l < /tmp/ds-door.ok | tr -d ' ')
	if [ "$((_h1 - ${_h0:-0}))" -ge 10 ] && [ "$_ok" -ge 36 ]; then
		record ds_whole_door PASS "40 connections at once: $((_h1 - ${_h0:-0})) held at the whole door, $_ok answered within 15 s (TCP asked again)"
	else
		record ds_whole_door FAIL "40 connections at once: held $((_h1 - ${_h0:-0})) (want >= 10), answered $_ok of 40"
	fi
	uci delete vectra-controller-pro.main.admit_total_rate
	uci commit vectra-controller-pro
}
