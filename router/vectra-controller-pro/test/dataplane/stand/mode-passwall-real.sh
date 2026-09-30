# shellcheck shell=sh
# The REAL PassWall2 next to vctl: `vectra on --trial`, off, keep, the deadman,
# "midnight", a WAN ifup, the legacy agent's cron watchdog and reboots.
#
# Sourced by stand.sh. Every other mode stands a sleep loop in for PassWall;
# this one installs the real thing — luci-app-passwall2 26.8.10 with its own
# xray-core 26.7.28, chinadns-ng, geoview and geo data, the bundle in
# build/passwall2-26.8.10, the build the owner's test router runs — lets it
# carry the LAN, and puts vctl next to it the way the installer's --standby
# does: installed, off, with an operator config from `vctl apply-local`.
#
#   MODE=passwall-real            every assertion must PASS
#   MODE=passwall-real-no-switch  the takeover with PassWall's own switch left
#                                 alone — the switch-off stripped from the init
#                                 script, in the payload, before the .ipk is
#                                 built. "Midnight" must then bring PassWall's
#                                 whole stack back next to vctl's:
#                                 pw_trial_switch_off and the pw_midnight_*
#                                 assertions MUST FAIL.
#
# WHO CARRIES THE LAN IS READ OFF THE PACKETS, never off process names. The
# inet namespace runs one xray as four "provider nodes" and logs the inbound
# every connection arrived on: vctl's provider (ui/subscription.json) dials
# node-a/b/c on :2001/:2002/:2004, PassWall's one node is :2005 (pw-in), a node
# vctl does not know. Each probe is a TCP request, a UDP echo and a DNS query
# for a fresh name — REDIRECT, TPROXY and PassWall's DNS hijack are three
# different mechanisms, and a leftover of any one of them would take its own
# protocol only — and each lands in that log under the stack that carried it.
# stand.sh's forward guard drops whatever would leave without a proxy.
#
# THE ROUTER'S WORLD. OpenWrt's feeds are reached once, for PassWall's LuCI and
# lua dependencies, with api/router.vectra-pro.net already sinkholed; then the
# default route is deleted and nothing leaves the container again (pw_isolated
# counts it). vctl's panel is the ui stand's stub, its subscription and
# PassWall's are served in the inet namespace, and the legacy agent is the
# stand's stub — installed as /usr/sbin/vectra-controller-agent so that the
# agent's REAL cron watchdog (openwrt/testdata/vectra-controller-watchdog.main,
# main's copy, the live router's) finds it by the path it looks for.
#
# A REBOOT, as far as a container can have one (pw_power_cycle, pw_boot):
# every process in the router's network namespace is SIGKILLed — no stop runs;
# the kernel forgets the router's nft tables, policy rules and their tables;
# tmpfs comes back empty; then procd and ubusd, config_generate, /etc/uci-defaults/*
# sourced in a subshell from /etc/uci-defaults and deleted on exit 0 exactly as
# /etc/init.d/boot's uci_apply_defaults does, and /etc/rc.d/S* in order with
# `boot` — except the scripts that would drive what the container does not
# have (PW_BOOT_SKIP): the host's clock and kernel parameters, flash and LEDs,
# and netifd with fw4, whose zones would have no devices without it and would
# reject the LAN. PassWall's rules live in their own table and do not need fw4;
# what fw4's reload would run of PassWall's — its include — is run by hand.
#
# Two product defects have stand-side ways past them, announced as KNOWN
# DEFECT where they bite, so that on a build that still has them the steps
# after them test their own subject: H2 (a data plane that did not come back
# after a reboot, fixed in 9bb567c) and vctl's init lock left held by
# PassWall's daemons after a hand-back (fixed in 0.6.0-r5). See their sections
# below.

PW_BUNDLE=/stand/passwall2
PW_WATCHDOG_SRC=/stand/legacy-watchdog
# The world's files live outside /tmp, which a reboot empties.
PW_WORLD=/srv/world
PW_NODES_LOG=$PW_WORLD/nodes-access.log
# The origin's resolver logs every query with who asked (pw_world).
PW_DNS_LOG=$PW_WORLD/origin-dns.log
PW_SUBSRV=$PW_WORLD/subsrv
PW_PANEL_IP=44.44.44.81
PW_PANEL_PORT=8080
PW_PROBE_PORT=8081
PW_SUB_PORT=8443
PW_UUID=5f0c2b4e-7d1a-4c3b-9e8f-0a1b2c3d4e5f
PW_SUB_ID=sub_stand
PW_PKG=vectra-controller-pro
PW_PKG_DATA=/tmp/pw-pkg
# The operator's copies of the package, on flash: step 10 installs them again
# after two reboots.
PW_IPK=/root/$PW_PKG.ipk
PW_IPK_NEWER=/root/$PW_PKG-newer.ipk
PW_VCTL_INIT=/etc/init.d/$PW_PKG
PW_AGENT_INIT=/etc/init.d/vectra-controller
PW_AGENT_BIN=/usr/sbin/vectra-controller-agent
PW_WATCHDOG=/usr/sbin/vectra-controller-watchdog
PW_WD_STAMP=/var/run/vectra-controller-watchdog.last-restart
# 0.6.0 holds the agent's watchdog off with its own throttle stamp 10 minutes
# ahead (AGENT_WATCHDOG_HOLD), set again every minute by the dead-man while
# vctl runs. Before 0.6.0 the stamp was 4102444800, for good.
PW_WD_AHEAD_MAX=700
PW_INIT=/etc/init.d/passwall2
PW_SNIPPET=/etc/uci-defaults/99-vectra-trial-passwall-switch
PW_TRIAL_FILE=/tmp/vectra-trial.json
PW_TRIAL_DIR=/tmp/vectra-trial.d
PW_MARKER_DIR=/etc/vectra-controller-pro
PW_LOG=/tmp/log/passwall2.log
PW_POWER_LOG=/tmp/vectra-power.log
PW_AGENT_JSON=/var/run/vectra-controller-pro/agent.json
# /etc/rc.d/S* the stand's boot does not run, and why: sysfixtime and sysntpd
# set the clock (the VM's), sysctl its kernel parameters, boot mounts, loads
# modules and runs uci-defaults (done by pw_boot itself); network (netifd)
# would take over the stand's interfaces and firewall (fw4) has no zone device
# without it; dropbear, odhcpd, packet_steering, gpio_switch, led and done
# (mount_root, LEDs) have nothing to do here.
PW_BOOT_SKIP="sysfixtime boot sysctl dropbear firewall network packet_steering odhcpd gpio_switch led done sysntpd"

PW_Q=0
PW_SUB_N=0
PW_PROBE=""
PW_PROBE_OK=""
PW_TCP=""
PW_NEWER_VER=""
PW_UDP=""
PW_DNS=""

pw_wait() { # <seconds> <command...>: poll once a second until it succeeds
	_t=$1
	shift
	_i=0
	while [ "$_i" -lt "$_t" ]; do
		"$@" >/dev/null 2>&1 && return 0
		_i=$((_i + 1))
		sleep 1
	done
	return 1
}

# A fraction of a second: busybox's sleep takes whole seconds only here, and
# coreutils' timeout came in with PassWall.
# (timeout 0 would never end: 0 is no nap at all.)
pw_nap() { [ "$1" = 0 ] || timeout "$1" tail -f /dev/null >/dev/null 2>&1; }

# The memory sampler (pw_start_sampler) tags each sample with this.
pw_phase() { echo "$1" > "$PW_WORLD/phase"; }

# ------------------------------------------------------------------ probes ---
#
# NB: no pattern here may contain the literal "passwall2/". PassWall's own stop
# SIGKILLs every process whose command line does (app.sh: `pgrep -af
# "passwall2/"`), and a probe it killed would read as "not running".

# PassWall's proxies run from its temp bin directory; app.sh asks the same.
pw_proxy_pids() { ps w 2>/dev/null | grep '[/]tmp/etc/passwall[2]/bin/' | awk '{print $1}' | tr '\n' ' ' | sed 's/ $//'; }
pw_xray_pid() { ps w 2>/dev/null | grep '[/]tmp/etc/passwall[2]/bin/xray' | awk '{print $1}' | head -n 1; }
pw_up() { [ -n "$(pw_xray_pid)" ]; }
pw_down() { [ -z "$(pw_proxy_pids)" ]; }
pw_switch() { uci -q get passwall2.@global[0].enabled; }
pw_linked() { ls /etc/rc.d/S[0-9][0-9]passwall2 >/dev/null 2>&1; }
pw_runs() { _c=$(grep -c 'Running complete!' "$PW_LOG" 2>/dev/null); echo "${_c:-0}"; }
pw_ran_since() { [ "$(pw_runs)" -gt "$1" ]; } # <count>: a start of PassWall's finished since
pw_xray_changed() { _p=$(pw_xray_pid); [ -n "$_p" ] && [ "$_p" != "$1" ]; }
pw_rules() { # what of PassWall's own is left in the kernel
	_ch=$(nft list table inet passwall2 2>/dev/null | grep -c 'PSW2')
	_ru=$(ip rule show 2>/dev/null | grep -c '0x50535732')
	_rt=$(ip route show table 999 2>/dev/null | grep -c .)
	_tb=no
	nft list table inet passwall2 >/dev/null 2>&1 && _tb=yes
	echo "psw2_refs=$_ch fwmark_rules=$_ru table999_routes=$_rt table_present=$_tb"
}
pw_rules_gone() { pw_rules | grep -q '^psw2_refs=0 fwmark_rules=0 table999_routes=0 '; }

agent_pid() { ps w 2>/dev/null | grep '[/]usr/sbin/vectra-controller-agent' | awk '{print $1}' | head -n 1; }
agent_up() { [ -n "$(agent_pid)" ]; }
agent_down() { [ -z "$(agent_pid)" ]; }
agent_linked() { ls /etc/rc.d/S[0-9][0-9]vectra-controller >/dev/null 2>&1; }

vctl_pid() { ps w 2>/dev/null | grep '[/]usr/sbin/vctl agent' | awk '{print $1}' | head -n 1; }
vctl_procd() { [ "$(ubus -t 5 call service list "{\"name\":\"$PW_PKG\"}" 2>/dev/null | jsonfilter -e "@['$PW_PKG'].instances[*].running" 2>/dev/null | head -n 1)" = true ]; }
vctl_linked() { ls /etc/rc.d/S[0-9][0-9]$PW_PKG >/dev/null 2>&1; }
vctl_down() { ! vctl_procd && [ -z "$(vctl_pid)" ]; }
trial_over() { [ ! -e "$PW_TRIAL_FILE" ] && [ -z "$(vctl_pid)" ]; }
nodes_listening() { ip netns exec "$INET_NS" netstat -lnt 2>/dev/null | grep -q '10.44.0.2:2005 '; }
vctl_dataplane() { nft list table inet vctl >/dev/null 2>&1 && ip rule show | grep -q 'fwmark 0x1 lookup 100'; }

# shellcheck disable=SC2010 # breadcrumb and rc.d names are the init script's own
crumbs_etc() { ls -A "$PW_MARKER_DIR" 2>/dev/null | grep -- '-by-vctl$' | tr '\n' ' ' | sed 's/ $//'; }
crumbs_tmp() { ls -A "$PW_TRIAL_DIR" 2>/dev/null | tr '\n' ' ' | sed 's/ $//'; }
wd_stamp() { cat "$PW_WD_STAMP" 2>/dev/null; }
# wd_held: the stamp is ahead of the clock, by no more than the hold and a
# minute (a stamp for good would be the pre-0.6.0 hold).
wd_held() {
	_ws=$(wd_stamp)
	case "$_ws" in '' | *[!0-9]*) return 1 ;; esac
	_wn=$(date +%s)
	[ "$_ws" -gt "$_wn" ] && [ "$_ws" -le $((_wn + PW_WD_AHEAD_MAX)) ]
}
# wd_ahead: how far ahead of the clock the stamp is, for the log.
wd_ahead() {
	_ws=$(wd_stamp)
	case "$_ws" in '' | *[!0-9]*) echo "stamp '$_ws'"; return ;; esac
	echo "stamp $((_ws - $(date +%s))) s ahead"
}
holder() { vectra status --json 2>/dev/null | jsonfilter -e '@.holder' 2>/dev/null; }

guard_leaked() { nft list counter inet standguard leaked 2>/dev/null | awk '/packets/{print $2; exit}'; }

# The node inbound(s) of the access-log lines matching <fixed string>: "pw",
# "a b", ... (the tags without their -in).
pw_tags() {
	grep -F "$1" "$PW_NODES_LOG" 2>/dev/null | grep -o '\[[a-z]*-in' | sed 's/^\[//; s/-in$//' | sort -u | tr '\n' ' ' | sed 's/ $//'
}
pw_stack_of() { # <tags> -> passwall | vctl | direct | none | mixed
	case "$1" in
	"") echo none ;;
	direct) echo direct ;;
	pw) echo passwall ;;
	*pw*) echo mixed ;;
	*) echo vctl ;;
	esac
}

# pw_probe: a TCP request, a UDP echo and a DNS query for a name no cache has
# seen, from the LAN client; PW_PROBE says what answered and which node carried
# each, PW_PROBE_OK is yes/no three times.
pw_probe() {
	: > "$PW_NODES_LOG"
	_g0=$(guard_leaked)
	_t=no
	_u=no
	_d=no
	rm -f "$PW_WORLD/probe.body"
	in_client curl -sS --max-time 10 -o "$PW_WORLD/probe.body" "http://$ORIGIN_IP/hello" 2>/dev/null
	grep -q "$EXPECT_BODY" "$PW_WORLD/probe.body" 2>/dev/null && _t=yes
	in_client sh -c "echo udp-$EXPECT_BODY | socat -T5 - UDP4:$ORIGIN_IP:$UDP_PORT" 2>/dev/null | grep -q "udp-$EXPECT_BODY" && _u=yes
	PW_Q=$((PW_Q + 1))
	_qn="q$PW_Q-$(date +%s).origin.stand"
	_r0=$(pw_dns_redirected)
	in_client nslookup "$_qn" "$ROUTER_CLIENT_IP" 2>/dev/null | grep -q "$ORIGIN_IP" && _d=yes
	PW_DNS_INTO_VCTL=no
	[ "$(pw_dns_redirected)" -gt "$_r0" ] && PW_DNS_INTO_VCTL=yes
	sleep 1
	PW_TCP=$(pw_tags "tcp:$ORIGIN_IP:80 ")
	PW_UDP=$(pw_tags "udp:$ORIGIN_IP:$UDP_PORT ")
	PW_DNS=$(pw_tags "$ORIGIN_DNS_IP:53 ")
	# No node saw it: did the router's dnsmasq ask the origin itself, from
	# its WAN address?
	[ -n "$PW_DNS" ] || ! grep -qF "$_qn from $ROUTER_INET_IP" "$PW_DNS_LOG" 2>/dev/null || PW_DNS=direct
	_g1=$(guard_leaked)
	PW_PROBE_OK="$_t$_u$_d"
	PW_PROBE="tcp=$_t via[${PW_TCP:-none}] udp=$_u via[${PW_UDP:-none}] dns=$_d via[${PW_DNS:-none}] into-vctl=$PW_DNS_INTO_VCTL guard ${_g0:-?}->${_g1:-?}"
	[ "${_g0:-x}" = "${_g1:-y}" ] || PW_PROBE_OK="${PW_PROBE_OK}leak"
}
# The last probe: all three answered, each carried by <passwall|vctl>.
pw_carried_by() {
	[ "$PW_PROBE_OK" = yesyesyes ] && [ "$(pw_stack_of "$PW_TCP")" = "$1" ] \
		&& [ "$(pw_stack_of "$PW_UDP")" = "$1" ] && pw_dns_by "$1"
}
# The probe's DNS goes to the router's dnsmasq, whose upstream query is the
# router's own traffic. PassWall hijacks it into its own DNS path (pw). vctl
# carried it through its xray before 0.6.0-r2, let it out directly from r2 to
# r7, and since r8 redirects it into xray's DNS inbound (dns_steer): its
# built-in DNS then asks the provider's server through the provider's routing,
# a connection the nodes' log does not show as the client's own. The
# redirect's counter moving during the probe is that path's evidence. A LAN
# client's own DNS is pw_trial_client_dns.
pw_dns_by() {
	_ds=$(pw_stack_of "$PW_DNS")
	[ "$_ds" = "$1" ] || { [ "$1" = vctl ] && { [ "$_ds" = direct ] || [ "$PW_DNS_INTO_VCTL" = yes ]; }; }
}
# vctl's DNS redirect counter (dnsmasq's upstream queries sent into xray), 0
# without one.
pw_dns_redirected() {
	nft list counter inet vctl vctl_dns_redirected 2>/dev/null | sed -n 's/.*packets \([0-9]*\).*/\1/p' | head -n 1 | grep . || echo 0
}
pw_carries() { pw_probe; pw_carried_by "$1"; } # <stack>, once
pw_wait_carrier() { # <stack> <seconds>: probe until it carries; PW_PROBE is the last probe
	_end=$(($(date +%s) + $2))
	while :; do
		pw_carries "$1" && return 0
		[ "$(date +%s)" -lt "$_end" ] || return 1
		sleep 2
	done
}
# The LAN's DNS alone, through the router, for a fresh name.
pw_dns_answers() {
	PW_Q=$((PW_Q + 1))
	in_client nslookup "d$PW_Q-$(date +%s).origin.stand" "$ROUTER_CLIENT_IP" 2>/dev/null | grep -q "$ORIGIN_IP"
}

# ------------------------------------------------------------------ setup ----

# FIRST, before anything touches the network: Vectra's control plane is
# production, and nothing in this stand may reach it.
pw_sinkhole() {
	printf '%s api.vectra-pro.net router.vectra-pro.net\n' 127.0.0.99 >> /etc/hosts
	info "api/router.vectra-pro.net -> 127.0.0.99 (nothing listens there), before any download"
}

# PassWall's LuCI and lua dependencies from OpenWrt's feeds, the rest from the
# bundle. The bundle is built for the fleet's aarch64_cortex-a53; this image is
# aarch64_generic, and opkg takes an explicit list once any arch is named.
pw_install_bundle() {
	[ -d "$PW_BUNDLE" ] || die "no PassWall2 bundle at $PW_BUNDLE (run.sh mounts build/passwall2-26.8.10)"
	grep -q '^arch aarch64_cortex-a53' /etc/opkg.conf \
		|| printf 'arch all 1\narch noarch 1\narch aarch64_generic 10\narch aarch64_cortex-a53 20\n' >> /etc/opkg.conf
	_ol=$PW_WORLD/opkg.log
	opkg update > "$_ol" 2>&1 || { tail -n 5 "$_ol"; die "opkg update failed: OpenWrt's feeds are unreachable"; }
	# dnsmasq-full: PassWall's nftables mode needs dnsmasq's nftset, and the
	# fleet runs it. The kmods are what app.sh's environment check looks for.
	opkg remove dnsmasq >> "$_ol" 2>&1
	opkg install dnsmasq-full kmod-nft-socket kmod-nft-tproxy >> "$_ol" 2>&1 \
		|| { tail -n 5 "$_ol"; die "dnsmasq-full / kmods did not install"; }
	# /usr/bin/xray is the image's bare XTLS 26.3.27 binary, which no package
	# owns; the bundle's xray-core replaces it. vctl then runs the same xray
	# 26.7.28 the live router does.
	rm -f /usr/bin/xray
	for _p in tcping geoview v2ray-geoip v2ray-geosite chinadns-ng xray-core; do
		opkg install "$PW_BUNDLE/${_p}_"*.ipk >> "$_ol" 2>&1 || { tail -n 5 "$_ol"; die "bundle: $_p did not install"; }
	done
	opkg install "$PW_BUNDLE"/luci-app-passwall2_*.ipk >> "$_ol" 2>&1 \
		|| { tail -n 8 "$_ol"; die "luci-app-passwall2 did not install"; }
	_v=$(opkg list-installed luci-app-passwall2 | awk '{print $3}')
	_x=$(opkg list-installed xray-core | awk '{print $3}')
	_xv=$(xray version 2>/dev/null | awk 'NR == 1 {print $2}')
	if [ "$_v" = 26.8.10-r1 ] && [ "$_x" = 26.7.28-r1 ] && [ "$_xv" = 26.7.28 ] && [ -x "$PW_INIT" ] \
		&& dnsmasq -v 2>/dev/null | grep -qw nftset && command -v fw4 >/dev/null; then
		record pw_bundle_installed PASS "luci-app-passwall2 $_v, xray-core $_x (xray $_xv), $(opkg list-installed chinadns-ng | awk '{print $1" "$3}'), dnsmasq-full with nftset, fw4 present"
	else
		record pw_bundle_installed FAIL "luci-app-passwall2='$_v' xray-core='$_x' xray='$_xv'"
		die "the real PassWall2 is not what this mode is about to test"
	fi
}

# The feeds are done with: from here on nothing leaves the container. The
# guard counts what would still go out of eth0, the container's only uplink.
pw_isolate() {
	ip route del default 2>/dev/null || true
	ip -6 route del default 2>/dev/null || true
	nft -f - <<EOF || die "pwguard table"
table inet pwguard {
  counter escape { }
  chain output_eth0 {
    type filter hook output priority filter; policy accept;
    oifname "eth0" meta l4proto { tcp, udp } counter name "escape"
  }
  chain forward_eth0 {
    type filter hook forward priority filter; policy accept;
    oifname "eth0" meta l4proto { tcp, udp } counter name "escape"
  }
}
EOF
	info "default route deleted; from here on eth0 (the container's uplink) is counted"
}

pw_sub_publish() { # PassWall's subscription: one vless link, a new name each time (a new md5)
	PW_SUB_N=$((PW_SUB_N + 1))
	_b64=$(printf 'vless://%s@%s:2005?encryption=none&type=tcp&security=none#stand-sub-%s\n' "$PW_UUID" "$INET_PEER_IP" "$PW_SUB_N" | base64 | tr -d '\n')
	mkdir -p "$PW_SUBSRV/pw"
	printf 'HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %s\r\n\r\n%s' "${#_b64}" "$_b64" > "$PW_SUBSRV/pw/sub"
}

# The inet namespace's side: nodes, both subscriptions, the panel stub, the
# observatory's probe target. Nothing here is the router's; a reboot keeps it.
pw_world() {
	mkdir -p "$PW_WORLD" "$PW_SUBSRV/api/sub"
	: > "$PW_NODES_LOG"
	in_inet /usr/bin/xray run -c "$STAND/passwall-real/nodes-server.json" > "$PW_WORLD/nodes.log" 2>&1 &
	pw_wait 20 nodes_listening || { cat "$PW_WORLD/nodes.log"; die "the stand's nodes never listened"; }
	# The origin's resolver again, logging who asks: the router's WAN address
	# (its own DNS, straight out) or a node (through a proxy).
	# shellcheck disable=SC2046 # its pid(s); OpenWrt's busybox has no pkill
	kill $(pgrep -f "dnsmasq -k -u root -p 53 -a $ORIGIN_DNS_IP") 2>/dev/null
	pw_wait 5 sh -c "! ps w | grep -q '[d]nsmasq -k -u root -p 53 -a $ORIGIN_DNS_IP'" \
		|| die "the stand's origin resolver did not stop"
	: > "$PW_DNS_LOG"
	in_inet dnsmasq -k -u root -p 53 -a "$ORIGIN_DNS_IP" --bind-interfaces \
		--no-hosts --no-resolv "--address=/origin.stand/$ORIGIN_IP" \
		--log-queries "--log-facility=$PW_DNS_LOG" >/dev/null 2>&1 &
	pw_wait 10 ip netns exec "$INET_NS" sh -c "nslookup origin.stand $ORIGIN_DNS_IP 2>/dev/null | grep -q $ORIGIN_IP" \
		|| die "the origin's resolver did not come back with its query log"
	# vctl's provider (the ui stand's two locations) and PassWall's link.
	_len=$(wc -c < "$STAND/ui/subscription.json" | tr -d ' ')
	{ printf 'HTTP/1.0 200 OK\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: %s\r\n\r\n' "$_len"
	  cat "$STAND/ui/subscription.json"; } > "$PW_SUBSRV/api/sub/stand"
	pw_sub_publish
	printf 'HTTP/1.0 200 OK\r\nContent-Length: 2\r\n\r\nok' > "$PW_SUBSRV/ready"
	cat /stand/tls/cert.pem >> /etc/ssl/certs/ca-certificates.crt
	in_inet sh -c "cd $PW_SUBSRV && exec openssl s_server -accept $PW_SUB_PORT \
		-cert /stand/tls/cert.pem -key /stand/tls/key.pem -HTTP -quiet" >/dev/null 2>&1 &
	in_inet socat "TCP-LISTEN:$PW_PROBE_PORT,bind=$ORIGIN_IP,fork,reuseaddr" "SYSTEM:$STAND/http-reply.sh" >/dev/null 2>&1 &
	ip -n "$INET_NS" addr add "$PW_PANEL_IP/32" dev lo
	ip route add "$PW_PANEL_IP/32" via "$INET_PEER_IP" dev vinet
	: > /tmp/ui-panel.log
	in_inet socat "TCP-LISTEN:$PW_PANEL_PORT,bind=$PW_PANEL_IP,fork,reuseaddr" "SYSTEM:$STAND/ui/panel-ok.sh" >/dev/null 2>&1 &
	pw_wait 20 in_inet curl -sf --max-time 3 -o /dev/null "https://$ORIGIN_IP:$PW_SUB_PORT/ready" \
		|| die "the stand's subscription server never came up"
	info "nodes a/b/c (vctl's provider) and pw (PassWall's) on 10.44.0.2; subscriptions on :$PW_SUB_PORT; panel stub $PW_PANEL_IP:$PW_PANEL_PORT"
}

# The legacy agent: the stand's stub (a sleep loop with PassWall's watchdog),
# at the path the agent's REAL cron watchdog looks for, and that watchdog on
# its real cron line.
pw_legacy_agent() {
	[ -f "$PW_WATCHDOG_SRC" ] || die "no legacy watchdog at $PW_WATCHDOG_SRC (run.sh mounts openwrt/testdata/vectra-controller-watchdog.main)"
	cp /stand/legacy-stub/legacy-agent-loop.sh "$PW_AGENT_BIN"
	sed "s#/stand/legacy-stub/legacy-agent-loop.sh#$PW_AGENT_BIN#" /stand/legacy-stub/vectra-controller > "$PW_AGENT_INIT"
	cp "$PW_WATCHDOG_SRC" "$PW_WATCHDOG"
	chmod +x "$PW_AGENT_BIN" "$PW_AGENT_INIT" "$PW_WATCHDOG"
	"$PW_AGENT_INIT" enable
	# What the agent package's uci-defaults (92_vectra_controller_watchdog) writes.
	mkdir -p /etc/crontabs
	{
		echo '# >>> vectra-controller-watchdog (managed) >>>'
		echo "*/5 * * * * $PW_WATCHDOG >/dev/null 2>&1"
		echo '# <<< vectra-controller-watchdog (managed) <<<'
	} >> /etc/crontabs/root
	info "legacy agent stub at $PW_AGENT_BIN (enabled); the real watchdog at $PW_WATCHDOG, */5 in root's crontab"
}

# PassWall as the fleet has it: one node — the stand's pw-in — as the global
# node, its switch on, the subscription on the nightly cron with hwid, DNS the
# stand's resolver (44.44.44.53, the only one there is), and a start delay of
# 1 s instead of 60 so a reboot does not wait a minute.
#
# Its subscription is served from 10.44.0.2, an address in vctl's bypass, so
# the nightly fetch reaches it under vctl as well: vctl does not carry the
# router's own TCP (pw_trial_router_tcp), and a fetch that fails never gets to
# subscribe.lua's restart — which is what step 3 has to exercise.
pw_passwall_config() {
	# The WAN lease's resolver, as netifd would write it.
	mkdir -p /tmp/resolv.conf.d
	echo "nameserver $ORIGIN_DNS_IP" > /tmp/resolv.conf.d/resolv.conf.auto
	uci -q batch <<EOF || die "PassWall's config"
set passwall2.pwnode=nodes
set passwall2.pwnode.remarks='stand-pw'
set passwall2.pwnode.type='Xray'
set passwall2.pwnode.protocol='vless'
set passwall2.pwnode.address='$INET_PEER_IP'
set passwall2.pwnode.port='2005'
set passwall2.pwnode.uuid='$PW_UUID'
set passwall2.pwnode.encryption='none'
set passwall2.pwnode.tls='0'
set passwall2.pwnode.transport='raw'
set passwall2.pwnode.tcp_guise='none'
set passwall2.@global[0].node='pwnode'
set passwall2.@global[0].enabled='1'
set passwall2.@global[0].remote_dns='$ORIGIN_DNS_IP'
set passwall2.@global[0].remote_dns_protocol='tcp'
set passwall2.@global_delay[0].start_delay='1'
set passwall2.$PW_SUB_ID=subscribe_list
set passwall2.$PW_SUB_ID.remark='stand'
set passwall2.$PW_SUB_ID.url='https://$INET_PEER_IP:$PW_SUB_PORT/pw/sub'
set passwall2.$PW_SUB_ID.hwid='1'
set passwall2.$PW_SUB_ID.update_week_mode='7'
set passwall2.$PW_SUB_ID.update_time_mode='0:00'
commit passwall2
EOF
	mkdir -p /tmp/sysinfo
	echo "Vectra PassWall Stand (synthetic)" > /tmp/sysinfo/model
}

# A power cycle, as far as a container can have one: see the header.
pw_power_cycle() {
	_net=$(readlink /proc/1/ns/net)
	_victims=
	for _d in /proc/[0-9]*; do
		_p=${_d#/proc/}
		{ [ "$_p" = 1 ] || [ "$_p" = "$$" ]; } && continue
		[ "$(readlink "$_d/ns/net" 2>/dev/null)" = "$_net" ] || continue
		_victims="$_victims $_p"
	done
	# shellcheck disable=SC2086
	kill -9 $_victims 2>/dev/null
	sleep 1
	nft list tables 2>/dev/null | while read -r _k _fam _name; do
		case "$_name" in standguard | pwguard) ;; *) nft delete table "$_fam" "$_name" 2>/dev/null ;; esac
	done
	for _v in -4 -6; do
		ip "$_v" rule show 2>/dev/null | awk -F: '$1 != 0 && $1 != 32766 && $1 != 32767 {print $1}' \
			| while read -r _prio; do ip "$_v" rule del priority "$_prio" 2>/dev/null; done
		ip "$_v" route flush table 100 2>/dev/null
		ip "$_v" route flush table 999 2>/dev/null
	done
	# tmpfs: all of it but the network namespaces' handles, the board's
	# identity, the WAN lease (netifd's, and there is no netifd to write it
	# again) and the panel stub's log (the world's).
	for _e in /tmp/* /tmp/.[!.]*; do
		[ -e "$_e" ] || [ -L "$_e" ] || continue
		case "${_e#/tmp/}" in run | sysinfo | resolv.conf.d | opkg-lists | ui-panel.log) continue ;; esac
		rm -rf "$_e"
	done
	for _e in /tmp/run/* /tmp/run/.[!.]*; do
		[ -e "$_e" ] || [ -L "$_e" ] || continue
		[ "${_e#/tmp/run/}" = netns ] && continue
		rm -rf "$_e"
	done
	info "power cycle: $(echo "$_victims" | wc -w | tr -d ' ') processes SIGKILLed, the router's nft tables and policy routes gone, tmpfs empty"
}

pw_boot() {
	# /etc/init.d/boot's boot(), what of it a container can have.
	mkdir -p /var/lock /var/log /var/run /var/state /var/tmp /tmp/.uci /tmp/resolv.conf.d
	# ...its config_generate, which writes what a first boot finds missing. The
	# image has no /etc/config/system: without a `system` section S12log starts
	# no logd, and procd's jailed dnsmasq does not start without logd — the LAN
	# would have no DNS on the router at all, whoever carries it.
	/bin/config_generate >/dev/null 2>&1
	chmod 1777 /var/lock
	chmod 0700 /tmp/.uci
	touch /var/log/wtmp /var/log/lastlog
	[ -s /tmp/resolv.conf.d/resolv.conf.auto ] || echo "nameserver $ORIGIN_DNS_IP" > /tmp/resolv.conf.d/resolv.conf.auto
	ln -sf /tmp/resolv.conf.d/resolv.conf.auto /tmp/resolv.conf
	# procd with ubusd: PID 1 on a router.
	ubusd &
	sleep 1
	procd &
	pw_wait 15 sh -c 'ubus list | grep -q "^service$"' || die "procd/ubusd never came up"
	# uci_apply_defaults, as boot() runs it.
	# stand.sh runs under set -u; a router's boot does not, and OpenWrt's
	# scripts are not nounset-safe.
	PW_DEFAULTS=$(
		set +u
		cd /etc/uci-defaults 2>/dev/null || exit 0
		. /lib/functions/system.sh
		# shellcheck disable=SC2045,SC1090 # boot()'s own idiom: files="$(ls)", each sourced
		for _f in $(ls); do
			if ( . "./$_f" ) >/dev/null 2>&1; then
				rm -f "$_f"
				printf '%s:ran ' "$_f"
			else
				printf '%s:exit=%s ' "$_f" "$?"
			fi
		done
		uci commit
	)
	info "uci-defaults: ${PW_DEFAULTS:-none}"
	# rc.d, in order.
	_ran=
	for _s in /etc/rc.d/S*; do
		_n=${_s##*/}
		_n=${_n#S[0-9][0-9]}
		case " $PW_BOOT_SKIP " in *" $_n "*) continue ;; esac
		"$_s" boot >/dev/null 2>&1
		_ran="$_ran ${_s##*/}"
	done
	info "rc.d boot:$_ran"
}

# The installer's --standby, step by step: the package with its postinst held
# back, off in UCI, no boot link, marked set up.
pw_vctl_stage() {
	rm -rf "$PW_PKG_DATA"
	mkdir -p "$PW_PKG_DATA"
	cp -R /stand/pkg/. "$PW_PKG_DATA/"
	if [ "$MODE" = passwall-real-no-switch ]; then
		# The takeover without its hold on PassWall's own switch: the whole `if`
		# in stop_passwall_stack that notes the switch, writes the trial's
		# snippet and turns it off. Its opening line is one tab in and unique
		# (restore_passwall_stack's is two); the block ends at the first line
		# that is exactly one tab and `fi`.
		_init="$PW_PKG_DATA/etc/init.d/$PW_PKG"
		sed -i -e '/^\tif \[ "\$(uci -q get "\$cfg\.@global\[0\]\.enabled")" = 1 \]; then$/i\
	: # DEFECT INJECTED by the stand: PassWall'"'"'s own switch left on' "$_init"
		sed -i -e '/^\tif \[ "\$(uci -q get "\$cfg\.@global\[0\]\.enabled")" = 1 \]; then$/,/^\tfi$/d' "$_init"
		if ! grep -q 'DEFECT INJECTED' "$_init" || grep -q '@global\[0\]\.enabled=0' "$_init"; then
			die "no-switch: defect injection missed its anchor"
		fi
		info "MODE=$MODE: the switch-off stripped from stop_passwall_stack in the payload"
	fi
	# The image bakes the package's files in at their real paths: gone first,
	# so opkg putting them back is what installs them.
	for _p in $(cd /stand/pkg && find . -type f | sed 's|^\.||'); do rm -f "$_p"; done
	/stand/build-ipk.sh "$PW_PKG_DATA" /stand/pkg-control "$PW_IPK"
	# ...and the same payload one version up, for step 10's real upgrade.
	rm -rf /tmp/pw-ctl
	cp -R /stand/pkg-control /tmp/pw-ctl
	sed -i 's/^Version: \(.*\)$/Version: \1.1/' /tmp/pw-ctl/control
	PW_NEWER_VER=$(sed -n 's/^Version: //p' /tmp/pw-ctl/control)
	/stand/build-ipk.sh "$PW_PKG_DATA" /tmp/pw-ctl "$PW_IPK_NEWER"
	info "built $PW_IPK and $PW_IPK_NEWER ($PW_NEWER_VER) from the shipped payload"
}

pw_vctl_standby() {
	VECTRA_SKIP_POSTINST_RESTART=1 opkg install "$PW_IPK" 2>&1 | sed 's/^/       opkg: /'
	uci set "$PW_PKG.main.enabled=0"
	uci set "$PW_PKG.main.setup_done=1"
	# Its panel is the stub: nothing may register with production.
	uci set "$PW_PKG.main.control_url=http://$PW_PANEL_IP:$PW_PANEL_PORT"
	uci set "$PW_PKG.main.panel_url=http://$PW_PANEL_IP:$PW_PANEL_PORT"
	uci set "$PW_PKG.main.poll_interval=10"
	uci commit "$PW_PKG"
	"$PW_VCTL_INIT" disable
	sleep 2
	_ver=$(opkg list-installed "$PW_PKG" | awk '{print $3}')
	if [ -n "$_ver" ] && ! vctl_linked && vctl_down && [ "$(uci -q get $PW_PKG.main.enabled)" = 0 ] \
		&& [ "$(uci -q get $PW_PKG.main.setup_done)" = 1 ] && pw_up && agent_up; then
		record pw_vctl_standby PASS "$PW_PKG $_ver installed off (uci enabled=0, no boot link, not running, setup_done=1); PassWall and the agent untouched"
	else
		record pw_vctl_standby FAIL "version='$_ver' linked=$(vctl_linked && echo yes || echo no) running=$(vctl_down && echo no || echo yes) enabled=$(uci -q get $PW_PKG.main.enabled) passwall=$(pw_up && echo up || echo DOWN) agent=$(agent_up && echo up || echo DOWN)"
	fi
}

# An operator config the owner would seed for `vectra on`: the ui stand's,
# against the stand's provider, installed with `vctl apply-local` — with the
# agent still owning the router, hence -ignore-legacy-agent. Its fetch goes
# through PassWall, which carries the router's own traffic too.
pw_vctl_seed() {
	mkdir -p "$PW_MARKER_DIR" "${PW_AGENT_JSON%/*}"
	cp "$STAND/ui/operator-config.json" "$PW_MARKER_DIR/xray-desired.json"
	/usr/libexec/vectra-controller-pro/render-xray-config.sh "$PW_AGENT_JSON" || die "render of $PW_AGENT_JSON failed"
	vctl apply-local -ignore-legacy-agent -config "$PW_AGENT_JSON" > "$PW_WORLD/apply-local.log" 2>&1
	_rc=$?
	sed 's/^/       /' "$PW_WORLD/apply-local.log" | tail -n 6
	[ "$_rc" = 0 ] && [ -s "$PW_MARKER_DIR/provider-config.json" ] || die "vctl apply-local failed (rc=$_rc)"
}

# The memory of the router's proxy processes (every xray, vctl, chinadns-ng,
# dnsmasq) and MemAvailable, twice a second, tagged with the stand's phase. It
# runs in the inet namespace, so no power cycle kills it.
pw_start_sampler() {
	pw_phase setup
	cat > "$PW_WORLD/mem-sampler.sh" <<'EOF'
net=$1
out=$2
phasef=$3
while :; do
	ph=$(cat "$phasef" 2>/dev/null)
	tot=0
	for c in $(grep -l -E 'xray|vctl|chinadns|dnsmasq' /proc/[0-9]*/cmdline 2>/dev/null); do
		d=${c%/cmdline}
		[ "$(readlink "$d/ns/net" 2>/dev/null)" = "$net" ] || continue
		r=$(awk '/^VmRSS:/{print $2}' "$d/status" 2>/dev/null)
		tot=$((tot + ${r:-0}))
	done
	echo "$ph $tot $(awk '/^MemAvailable:/{print $2}' /proc/meminfo)" >> "$out"
	timeout 0.5 tail -f /dev/null >/dev/null 2>&1
done
EOF
	in_inet sh "$PW_WORLD/mem-sampler.sh" "$(readlink /proc/1/ns/net)" "$PW_WORLD/mem.log" "$PW_WORLD/phase" >/dev/null 2>&1 &
}

pw_memory_report() {
	say "memory: the router's proxy processes (xray, vctl, chinadns-ng, dnsmasq), peak RSS per phase"
	awk '{ if ($2 > m[$1]) m[$1] = $2; if (!($1 in a) || $3 < a[$1]) a[$1] = $3; if (!($1 in o)) { o[$1] = ++n; k[n] = $1 } }
		END { for (i = 1; i <= n; i++) printf "     MEM %-22s peak %6.1f MiB   (container MemAvailable low %d MiB)\n", k[i], m[k[i]] / 1024, a[k[i]] / 1024 }' \
		"$PW_WORLD/mem.log" 2>/dev/null
}

pw_dump() {
	echo "--- vectra status ---"; vectra status 2>&1
	echo "--- processes ---"; ps w | grep -E '[x]ray|[v]ctl|[c]hinadns|[d]nsmasq|vectra-controller-[a]gent' | cut -c1-150
	echo "--- nft tables ---"; nft list tables 2>&1
	echo "--- ip rule ---"; ip rule show 2>&1
	echo "--- rc.d ---"; ls /etc/rc.d | tr '\n' ' '; echo
	echo "--- power log ---"; tail -n 25 "$PW_POWER_LOG" 2>/dev/null
	echo "--- logread (vectra/vctl) ---"; logread 2>/dev/null | grep -E 'vectra|vctl' | tail -n 25
	echo "--- PassWall log ---"; tail -n 15 "$PW_LOG" 2>/dev/null
}

# `vectra <args>`, its output kept and shown; PW_VRC is its exit code.
pw_vectra() { # <label> <args...>
	_l=$1
	shift
	vectra "$@" > "$PW_WORLD/vectra-$_l.out" 2>&1
	PW_VRC=$?
	sed 's/^/       vectra: /' "$PW_WORLD/vectra-$_l.out"
}

# The router's OWN TCP: a request from the router itself, and the node that
# carried it. PassWall carries it (its nat_output REDIRECT). Under vctl the
# connection's first packet is marked and TPROXY'd to xray, and every later
# one is not — vctl's output chain returns on `ct state established,related`
# before its marking rule — so the handshake completes with vctl's xray while
# the ACK goes out the WAN to the real destination, which answers RST (traced
# with nftrace on this stand).
pw_router_tcp() { # <assertion> <passwall|vctl>
	: > "$PW_NODES_LOG"
	_out=$(curl -sS --max-time 8 "http://$ORIGIN_IP/hello" 2>&1)
	sleep 1
	_via=$(pw_tags "tcp:$ORIGIN_IP:80 ")
	if echo "$_out" | grep -q "$EXPECT_BODY" && [ "$(pw_stack_of "$_via")" = "$2" ]; then
		record "$1" PASS "the router's own GET http://$ORIGIN_IP/hello answered, carried by $2 (via ${_via})"
	else
		record "$1" FAIL "the router's own GET http://$ORIGIN_IP/hello: '$(echo "$_out" | tr '\n' ' ' | cut -c1-90)', via [${_via:-none}] (want $2)"
	fi
}

# ----------------------------------------------------------- the baseline ---

assert_pw_baseline() {
	if pw_wait_carrier passwall 60; then
		record pw_baseline_carries PASS "PassWall2's own xray carries the LAN: $PW_PROBE"
	else
		record pw_baseline_carries FAIL "$PW_PROBE"
		pw_dump
		die "PassWall2 does not carry the LAN: nothing below would mean anything"
	fi
	info "holder per vectra status: $(holder); PassWall's proxies: $(pw_proxy_pids); $(pw_rules)"
	info "PassWall's cron: $(grep 'subscribe.lua' /etc/crontabs/root 2>/dev/null | head -n 1)"
	pw_router_tcp pw_baseline_router_tcp passwall
}

# Anti-vacuity for step 5: the agent's watchdog really restarts an agent that
# is not running — stopped, not disabled — when nothing holds it off.
assert_pw_ctl_watchdog() {
	"$PW_AGENT_INIT" stop >/dev/null 2>&1
	pw_wait 10 agent_down
	rm -f "$PW_WD_STAMP"
	"$PW_WATCHDOG"
	if pw_wait 10 agent_up && agent_linked; then
		record pw_ctl_watchdog_armed PASS "the agent stopped by hand came back by the watchdog alone ($(logread 2>/dev/null | grep vectra-controller-watchdog | tail -n 1 | sed 's/.*watchdog[^:]*: //' | cut -c1-80)); stamp $(wd_stamp)"
	else
		record pw_ctl_watchdog_armed FAIL "the watchdog did not restart a stopped agent: step 5 would prove nothing"
		"$PW_AGENT_INIT" start
	fi
}

# Anti-vacuity for step 3: a restart with the switch on brings a new stack.
assert_pw_ctl_restart() {
	_p0=$(pw_xray_pid)
	"$PW_INIT" restart >/dev/null 2>&1
	pw_wait 30 pw_xray_changed "$_p0"
	_p1=$(pw_xray_pid)
	if [ -n "$_p1" ] && [ "$_p1" != "$_p0" ] && pw_wait_carrier passwall 30; then
		record pw_ctl_restart_armed PASS "'passwall2 restart' with the switch on: xray $_p0 -> $_p1, carrying ($PW_PROBE)"
	else
		record pw_ctl_restart_armed FAIL "xray $_p0 -> ${_p1:-none}; $PW_PROBE"
	fi
}

# Anti-vacuity for step 3's second half: the real subscribe.lua, as the cron
# runs it, fetches, finds new nodes and restarts PassWall.
pw_subscribe() { # the cron's command, by hand
	pw_sub_publish
	_md0=$(uci -q get passwall2.$PW_SUB_ID.md5)
	PW_RUNS0=$(pw_runs)
	lua /usr/share/passwall2/subscribe.lua start $PW_SUB_ID cron >/dev/null 2>&1
	PW_SUB_RC=$?
	_md1=$(uci -q get passwall2.$PW_SUB_ID.md5)
	PW_SUB_FETCHED=no
	[ -n "$_md1" ] && [ "$_md1" != "$_md0" ] && PW_SUB_FETCHED=yes
	# Its last act is `/etc/init.d/passwall2 restart &`: wait for that start.
	pw_wait 40 pw_ran_since "$PW_RUNS0"
	PW_SUB_RESTARTED=no
	[ "$(pw_runs)" -gt "$PW_RUNS0" ] && PW_SUB_RESTARTED=yes
	PW_SUB_NODES=$(uci show passwall2 2>/dev/null | grep -c "\.group='stand'")
}

assert_pw_ctl_subscribe() {
	_p0=$(pw_xray_pid)
	pw_subscribe
	pw_wait 20 pw_up
	_p1=$(pw_xray_pid)
	if [ "$PW_SUB_FETCHED" = yes ] && [ "$PW_SUB_RESTARTED" = yes ] && [ -n "$_p1" ] && [ "$_p1" != "$_p0" ] && pw_wait_carrier passwall 30; then
		record pw_ctl_subscribe_armed PASS "subscribe.lua start $PW_SUB_ID cron: fetched (new md5, $PW_SUB_NODES node(s) of the group), its restart ran, xray $_p0 -> $_p1"
	else
		record pw_ctl_subscribe_armed FAIL "rc=$PW_SUB_RC fetched=$PW_SUB_FETCHED restarted=$PW_SUB_RESTARTED xray $_p0 -> ${_p1:-none}; $PW_PROBE"
	fi
}

# The WAN's ifup, as netifd hands it to hotplug-call: 98-passwall2 sourced in a
# subshell with ACTION/INTERFACE/DEVICE. Its gate wants the default route on
# DEVICE — only for the firing, into the stand's closed inet namespace — and
# PassWall's ready lock and cached ENABLED_DEFAULT_ACL=1. 26.8.10's app.sh
# never caches that variable, so as shipped the hotplug restarts nothing;
# `armed` writes it in, as a build that did cache it would have.
pw_fire_ifup() { # [armed]
	if [ "${1:-}" = armed ]; then
		mkdir -p /tmp/etc/passwall2
		echo 'ENABLED_DEFAULT_ACL="1"' >> /tmp/etc/passwall2/var
	fi
	PW_RUNS0=$(pw_runs)
	ip route replace default via "$INET_PEER_IP" dev vinet
	(
		set +u
		export ACTION=ifup INTERFACE=wan DEVICE=vinet
		. /etc/hotplug.d/iface/98-passwall2
	) >/dev/null 2>&1
	# A restart it started (`restart &`) ends with a start that logs.
	pw_wait 30 pw_ran_since "$PW_RUNS0"
	PW_IFUP_RESTARTED=no
	[ "$(pw_runs)" -gt "$PW_RUNS0" ] && PW_IFUP_RESTARTED=yes
	ip route del default via "$INET_PEER_IP" dev vinet 2>/dev/null
}

assert_pw_ctl_hotplug() {
	_p0=$(pw_xray_pid)
	pw_fire_ifup
	_shipped=$PW_IFUP_RESTARTED
	_p1=$(pw_xray_pid)
	pw_fire_ifup armed
	pw_wait 20 pw_up
	_p2=$(pw_xray_pid)
	if [ "$_shipped" = no ] && [ "$_p1" = "$_p0" ] && [ "$PW_IFUP_RESTARTED" = yes ] && [ -n "$_p2" ] && [ "$_p2" != "$_p1" ] && pw_wait_carrier passwall 30; then
		record pw_ctl_hotplug_armed PASS "as shipped the WAN ifup restarts nothing (xray $_p0 kept: 26.8.10 never caches ENABLED_DEFAULT_ACL); with the gate armed it restarts PassWall (xray $_p1 -> $_p2)"
	else
		record pw_ctl_hotplug_armed FAIL "as shipped: restarted=$_shipped xray $_p0 -> $_p1; armed: restarted=$PW_IFUP_RESTARTED xray -> ${_p2:-none}; $PW_PROBE"
	fi
}

# ----------------------------------------------------------- the trial ------

# What a trial's takeover must leave (step 2), each fact separately; step 10
# reads them as one.
pw_trial_facts() {
	_f=
	[ -z "$(pw_proxy_pids)" ] || _f="$_f passwall-procs=$(pw_proxy_pids)"
	pw_rules_gone || _f="$_f $(pw_rules)"
	[ "$(pw_switch)" = 0 ] || _f="$_f switch=$(pw_switch)"
	pw_linked || _f="$_f S99passwall2-gone"
	agent_up && _f="$_f agent-running"
	agent_linked || _f="$_f agent-link-gone"
	wd_held || _f="$_f watchdog-not-held($(wd_ahead))"
	[ -z "$(crumbs_etc)" ] || _f="$_f etc-crumbs=[$(crumbs_etc)]"
	[ -n "$(crumbs_tmp)" ] || _f="$_f no-tmp-crumbs"
	[ -f "$PW_SNIPPET" ] || _f="$_f no-snippet"
	[ -f "$PW_TRIAL_FILE" ] || _f="$_f no-trial-file"
	vctl_linked && _f="$_f vctl-boot-link"
	[ "$(uci -q get $PW_PKG.main.enabled)" = 0 ] || _f="$_f uci-enabled=$(uci -q get $PW_PKG.main.enabled)"
	echo "${_f# }"
}

pw_trial_on() { # <label>: vectra on --trial --minutes 10 --foreground; vctl must carry after
	pw_vectra "$1" on --trial --minutes 10 --foreground
	pw_wait_carrier vctl 60
}

step2_trial() {
	pw_phase trial-on
	_p0=$(pw_xray_pid)
	pw_vectra trial on --trial --minutes 10 --foreground
	_rc=$PW_VRC
	if [ "$_rc" = 0 ] && grep -q 'a trial of 10 min' "$PW_WORLD/vectra-trial.out" && vctl_procd; then
		record pw_trial_started PASS "vectra on --trial --minutes 10 --foreground: exit 0, procd runs vctl; status: $(vectra status 2>/dev/null | head -n 1)"
	else
		record pw_trial_started FAIL "exit $_rc; vctl running=$(vctl_procd && echo yes || echo no)"
	fi
	if pw_wait_carrier vctl 60; then
		record pw_trial_vctl_carries PASS "vctl's xray carries the LAN, PassWall's node sees nothing: $PW_PROBE"
	else
		record pw_trial_vctl_carries FAIL "$PW_PROBE (vctl data plane loaded: $(vctl_dataplane && echo yes || echo no))"
	fi
	# A LAN client's own DNS, to a public resolver of its choosing: since
	# 0.6.0-r17 the router answers it (dns_hijack) — its own resolver, which
	# asks through the tunnel (dns_steer) — so it never leaves the router's
	# WAN on the open path, where the ISP forges answers. (0.6.0-r2 to r16:
	# PREROUTING took it to vctl's xray as ordinary traffic.) Only the
	# router's own DNS goes straight out.
	: > "$PW_NODES_LOG"
	PW_Q=$((PW_Q + 1))
	_qn="f$PW_Q-$(date +%s).origin.stand"
	_a=no
	_h0=$(vctl_ctr vctl_dns_hijacked)
	in_client nslookup "$_qn" "$ORIGIN_DNS_IP" 2>/dev/null | grep -q "$ORIGIN_IP" && _a=yes
	_h1=$(vctl_ctr vctl_dns_hijacked)
	sleep 1
	_from=$(grep -F "$_qn from" "$PW_DNS_LOG" 2>/dev/null | head -n 1 | sed 's/.* from //')
	if [ "$_a" = yes ] && [ "${_h1:-0}" -gt "${_h0:-0}" ] && [ "$_from" != "$ROUTER_INET_IP" ]; then
		record pw_trial_client_dns PASS "a LAN client's own query to $ORIGIN_DNS_IP answered by the router (vctl_dns_hijacked ${_h0}->${_h1}), which asked through the tunnel (the origin saw it from ${_from:-a cache}, not the router's WAN); the LAN's DNS through the router: $(echo "$PW_PROBE" | grep -o 'dns=[a-z]* via\[[a-z ]*\]')"
	else
		record pw_trial_client_dns FAIL "answered=$_a vctl_dns_hijacked ${_h0:-?}->${_h1:-?}; the origin saw it from '${_from:-nobody}' (the router's WAN is $ROUTER_INET_IP)"
	fi
	_pp=$(pw_proxy_pids)
	if [ -z "$_pp" ]; then
		record pw_trial_no_pw_procs PASS "no process from /tmp/etc/passwall2/bin (xray was $_p0)"
	else
		record pw_trial_no_pw_procs FAIL "PassWall's proxies still run: $_pp"
	fi
	if pw_rules_gone; then
		record pw_trial_pw_rules_gone PASS "$(pw_rules) — no PSW2 chain or rule, no fwmark 0x50535732 rule, table 999 empty (PassWall's own stop leaves its table with four empty base chains)"
	else
		record pw_trial_pw_rules_gone FAIL "$(pw_rules)"
	fi
	if [ "$(pw_switch)" = 0 ]; then
		record pw_trial_switch_off PASS "passwall2.@global[0].enabled=0"
	else
		record pw_trial_switch_off FAIL "passwall2.@global[0].enabled=$(pw_switch): any restart brings PassWall back"
	fi
	if pw_linked; then
		# shellcheck disable=SC2010
		record pw_trial_pw_link_kept PASS "$(ls /etc/rc.d | grep 'passwall2$') still there: the next boot starts PassWall"
	else
		record pw_trial_pw_link_kept FAIL "PassWall's boot link is gone: a reboot would not give it back"
	fi
	if ! agent_up && agent_linked; then
		# shellcheck disable=SC2010
		record pw_trial_agent PASS "the agent stopped, its link $(ls /etc/rc.d | grep 'vectra-controller$') kept"
	else
		record pw_trial_agent FAIL "agent running=$(agent_up && echo yes || echo no) linked=$(agent_linked && echo yes || echo no)"
	fi
	if wd_held; then
		record pw_trial_wd_hold PASS "$PW_WD_STAMP: $(wd_ahead) (the hold, set again every minute)"
	else
		record pw_trial_wd_hold FAIL "$PW_WD_STAMP: $(wd_ahead)"
	fi
	_ct=$(crumbs_tmp)
	_ce=$(crumbs_etc)
	if [ -n "$_ct" ] && [ -z "$_ce" ]; then
		record pw_trial_crumbs_tmp PASS "breadcrumbs only in $PW_TRIAL_DIR: $_ct; none in $PW_MARKER_DIR"
	else
		record pw_trial_crumbs_tmp FAIL "$PW_TRIAL_DIR: [$_ct] $PW_MARKER_DIR: [$_ce]"
	fi
	if [ -f "$PW_SNIPPET" ] && grep -q "enabled=1" "$PW_SNIPPET"; then
		record pw_trial_snippet PASS "$PW_SNIPPET turns the switch on at the next boot: $(grep '^uci' "$PW_SNIPPET" | tr '\n' ';')"
	else
		record pw_trial_snippet FAIL "no snippet: after a reboot PassWall's switch would stay off"
	fi
	if ! vctl_linked && [ "$(uci -q get $PW_PKG.main.enabled)" = 0 ]; then
		record pw_trial_no_vctl_link PASS "vctl has no boot link and uci enabled=0: a reboot does not start it"
	else
		record pw_trial_no_vctl_link FAIL "vctl linked=$(vctl_linked && echo yes || echo no) uci enabled=$(uci -q get $PW_PKG.main.enabled)"
	fi
	pw_router_tcp pw_trial_router_tcp vctl
	info "holder per vectra status: $(holder)"
}

# "Midnight": what subscribe.lua does after its nightly run, then subscribe.lua
# itself as the cron runs it.
step3_midnight() {
	pw_phase midnight
	info "PassWall's cron after the takeover: [$(grep 'subscribe.lua' /etc/crontabs/root 2>/dev/null | head -n 1)] (its stop cleans its own lines)"
	PW_RUNS0=$(pw_runs)
	"$PW_INIT" restart >/dev/null 2>&1
	pw_wait 30 pw_ran_since "$PW_RUNS0"
	_ran=no
	[ "$(pw_runs)" -gt "$PW_RUNS0" ] && _ran=yes
	sleep 3
	_pp=$(pw_proxy_pids)
	pw_carries vctl
	_via=$?
	_dns=no
	pw_dns_answers && _dns=yes
	if [ "$_ran" = yes ] && [ -z "$_pp" ] && [ "$_via" = 0 ] && [ "$_dns" = yes ]; then
		record pw_midnight_restart PASS "'passwall2 restart' ran a full stop and start, which started no proxy (switch $(pw_switch)); LAN via vctl ($PW_PROBE); LAN DNS answers"
	else
		record pw_midnight_restart FAIL "restart ran=$_ran; PassWall proxies: [${_pp}] switch=$(pw_switch); $PW_PROBE; LAN DNS answers=$_dns"
	fi
	pw_subscribe
	sleep 3
	_pp=$(pw_proxy_pids)
	pw_carries vctl
	_via=$?
	_dns=no
	pw_dns_answers && _dns=yes
	if [ "$PW_SUB_FETCHED" = yes ] && [ "$PW_SUB_RESTARTED" = yes ] && [ -z "$_pp" ] && [ "$_via" = 0 ] && [ "$_dns" = yes ]; then
		record pw_midnight_subscribe PASS "subscribe.lua start $PW_SUB_ID cron: fetched through vctl (new md5), its 'restart &' ran and started no proxy; LAN via vctl ($PW_PROBE); LAN DNS answers"
	else
		record pw_midnight_subscribe FAIL "fetched=$PW_SUB_FETCHED restarted=$PW_SUB_RESTARTED; PassWall proxies: [${_pp}] switch=$(pw_switch); $PW_PROBE; LAN DNS answers=$_dns"
	fi
}

step4_ifup() {
	pw_phase wan-ifup
	pw_fire_ifup
	_shipped=$PW_IFUP_RESTARTED
	pw_fire_ifup armed
	_armed=$PW_IFUP_RESTARTED
	# ...and fw4's reload: what it runs of PassWall's, PassWall's include.
	( set +u; . /var/etc/passwall2.include ) >/dev/null 2>&1
	sleep 3
	_pp=$(pw_proxy_pids)
	pw_carries vctl
	_via=$?
	if [ "$_armed" = yes ] && [ -z "$_pp" ] && pw_rules_gone && [ "$_via" = 0 ]; then
		record pw_ifup_stays_down PASS "WAN ifup fired as shipped (restart: $_shipped) and with the gate armed (the restart it fired ran and started no proxy); PassWall's firewall include run by hand: $(pw_rules); LAN via vctl ($PW_PROBE)"
	else
		record pw_ifup_stays_down FAIL "shipped restart=$_shipped armed restart=$_armed; PassWall proxies: [${_pp}]; $(pw_rules); $PW_PROBE"
	fi
}

step5_watchdog() {
	pw_phase watchdog
	_out=$("$PW_WATCHDOG" 2>&1)
	_rc=$?
	sleep 4
	_msg=$(logread 2>/dev/null | grep vectra-controller-watchdog | tail -n 1 | sed 's/.*watchdog[^:]*: //' | cut -c1-110)
	if [ "$_rc" = 0 ] && ! agent_up && wd_held && pw_carries vctl; then
		record pw_watchdog_holds PASS "the agent's watchdog, by hand: the agent stays stopped ('$_msg'); $(wd_ahead); LAN via vctl"
	else
		record pw_watchdog_holds FAIL "rc=$_rc agent running=$(agent_up && echo yes || echo no) stamp='$(wd_stamp)' '$_msg' $_out; $PW_PROBE"
	fi
}

step6_reboot() {
	pw_phase reboot-trial
	pw_power_cycle
	pw_boot
	pw_wait 60 pw_up
	pw_wait_carrier passwall 60
	_via=$?
	if [ "$_via" = 0 ]; then
		record pw_reboot_pw_back PASS "after the reboot PassWall2 carries the LAN: $PW_PROBE"
	else
		record pw_reboot_pw_back FAIL "$PW_PROBE; PassWall proxies [$(pw_proxy_pids)] switch=$(pw_switch)"
	fi
	if [ "$(pw_switch)" = 1 ] && [ ! -f "$PW_SNIPPET" ]; then
		record pw_reboot_switch_on PASS "switch 1, the snippet ran and is gone (uci-defaults: $PW_DEFAULTS)"
	else
		record pw_reboot_switch_on FAIL "switch=$(pw_switch) snippet=$([ -f "$PW_SNIPPET" ] && echo present || echo gone) (uci-defaults: $PW_DEFAULTS)"
	fi
	pw_wait 15 agent_up
	if agent_up && agent_linked; then
		record pw_reboot_agent PASS "the agent runs (pid $(agent_pid)), enabled"
	else
		record pw_reboot_agent FAIL "agent running=$(agent_up && echo yes || echo no) linked=$(agent_linked && echo yes || echo no)"
	fi
	if vctl_down && ! vctl_linked && ! vctl_dataplane; then
		record pw_reboot_vctl_off PASS "vctl neither runs nor starts at boot; no vctl data plane"
	else
		record pw_reboot_vctl_off FAIL "vctl running=$(vctl_down && echo no || echo yes) linked=$(vctl_linked && echo yes || echo no) dataplane=$(vctl_dataplane && echo yes || echo no)"
	fi
	if [ -z "$(crumbs_etc)" ] && [ ! -e "$PW_TRIAL_DIR" ] && [ ! -e "$PW_TRIAL_FILE" ]; then
		record pw_reboot_no_crumbs PASS "no breadcrumb anywhere, no trial file"
	else
		record pw_reboot_no_crumbs FAIL "$PW_MARKER_DIR: [$(crumbs_etc)] $PW_TRIAL_DIR: [$(crumbs_tmp)] trial file: $([ -e "$PW_TRIAL_FILE" ] && echo present || echo none)"
	fi
	info "holder per vectra status: $(holder)"
}

# The end state of a hand-back (steps 7, 8, 9's off, 10's off): what fails, or
# nothing.
pw_handed_back() {
	_f=
	pw_carried_by passwall || _f="$_f carrier:{$PW_PROBE}"
	[ "$(pw_switch)" = 1 ] || _f="$_f switch=$(pw_switch)"
	pw_linked || _f="$_f passwall-unlinked"
	[ ! -f "$PW_SNIPPET" ] || _f="$_f snippet-left"
	agent_up || _f="$_f agent-down"
	agent_linked || _f="$_f agent-unlinked"
	vctl_down || _f="$_f vctl-running"
	vctl_linked && _f="$_f vctl-linked"
	vctl_dataplane && _f="$_f vctl-dataplane-left"
	[ -z "$(crumbs_etc)" ] || _f="$_f etc-crumbs=[$(crumbs_etc)]"
	[ -z "$(crumbs_tmp)" ] || _f="$_f tmp-crumbs=[$(crumbs_tmp)]"
	[ ! -e "$PW_TRIAL_FILE" ] || _f="$_f trial-file-left"
	wd_held && _f="$_f watchdog-held($(wd_ahead))"
	echo "${_f# }"
}


# ------------------------------------------------ the hand-back's overlap ---
#
# While a hand-back runs — `vectra off`, the deadman's — a sampler in the inet
# namespace (nothing the hand-back kills can stop it) looks every 0.1 s at the
# router's xray processes: vctl's (argv[0] vctl-xray-wrapper) and PassWall's
# (from its temp bin directory), how many of each and their RSS. Two at once
# is the hand-back giving PassWall its stack back while vctl's xray still
# runs.

pw_overlap_script() {
	cat > "$PW_WORLD/overlap.sh" <<'EOF'
net=$1
out=$2
flag=$3
while [ -e "$flag" ]; do
	t=$(cut -d' ' -f1 /proc/uptime)
	vn=0; vr=0; pn=0; pr=0
	for c in $(grep -l xray /proc/[0-9]*/cmdline 2>/dev/null); do
		d=${c%/cmdline}
		[ "$(readlink "$d/ns/net" 2>/dev/null)" = "$net" ] || continue
		r=$(awk '/^VmRSS:/{print $2}' "$d/status" 2>/dev/null)
		case "$(tr '\0' ' ' < "$c" 2>/dev/null)" in
		*vctl-xray-wrapper*) vn=$((vn + 1)); vr=$((vr + ${r:-0})) ;;
		*/tmp/etc/passwall*) pn=$((pn + 1)); pr=$((pr + ${r:-0})) ;;
		esac
	done
	echo "$t $vn $vr $pn $pr" >> "$out"
	timeout 0.1 tail -f /dev/null >/dev/null 2>&1
done
EOF
}

PW_OV_ALL=""
PW_OV_BAD=""

pw_overlap_start() { # <label>
	PW_OV_LABEL=$1
	PW_OV_FILE=$PW_WORLD/overlap-$1.log
	: > "$PW_OV_FILE"
	touch "$PW_WORLD/overlap.run"
	in_inet sh "$PW_WORLD/overlap.sh" "$(readlink /proc/1/ns/net)" "$PW_OV_FILE" "$PW_WORLD/overlap.run" >/dev/null 2>&1 &
	pw_nap 0.3
}

pw_overlap_stop() {
	rm -f "$PW_WORLD/overlap.run"
	pw_nap 0.5
	_s=$(awk '
		NR == 1 { t0 = $1 }
		$2 > 0 { lv = $1 }
		$4 > 0 && !fp { fp = $1 }
		$2 > 0 && $4 > 0 { if (!f) f = $1; l = $1; n++; s = $3 + $5; if (s > pk) pk = s }
		END {
			if (n) printf "BOTH xray for %.1f s (%d samples), %.1f MiB together at the peak; ", l - f + 0.1, n, pk / 1024
			else printf "never both xray; "
			printf "vctl xray last seen +%.1f s, PassWall xray first +%.1f s (%d samples, one every %.2f s)", (lv ? lv - t0 : -1), (fp ? fp - t0 : -1), NR, (NR > 1 ? (last - t0) / (NR - 1) : 0)
		}
		{ last = $1 }' "$PW_OV_FILE")
	info "MEASURE overlap $PW_OV_LABEL: $_s"
	PW_OV_ALL="$PW_OV_ALL $PW_OV_LABEL: $_s."
	case "$_s" in BOTH*) PW_OV_BAD="$PW_OV_BAD $PW_OV_LABEL" ;; esac
}

# ------------------------------------------------ vctl's init lock (a defect) ---
#
# Every invocation of a procd init script takes
# /var/lock/procd_vectra-controller-pro.lock on fd 1000 (procd.sh's
# _procd_wrapper calls procd_lock when rc.common sources it) and holds it until
# the script exits. The hand-back starts PassWall2 from
# inside vctl's `stop`, and luci-app-passwall2's init is not procd's: its
# daemons are children of that shell and inherit fd 1000 — xray,
# dnsmasq_default, monitor.sh, lease2hosts.sh and their sleeps. For as long as
# they live, every later /etc/init.d/vectra-controller-pro action (start, stop,
# restart, even status) waits on that lock: `vectra on|off|keep`, a trial and
# its deadman, the package's postinst. Measured on this stand. After every
# hand-back the stand records who holds it (pw_handback_frees_init), and — so
# the steps after it test their own subject — frees it the way an operator
# could: a PassWall restart from a shell that holds nothing of vctl's.

PW_LOCK_BAD=""
PW_LOCK_ALL=""

# shellcheck disable=SC2010 # one ls over every fd beats a readlink per fd in ash
# The locks vctl's own actions wait on: procd's for its init script, and the
# power lock (`vectra on|off|keep`, and 0.6.0's cron dead-man, which holds it
# on fd 9 for as long as it acts — its hand-back included).
PW_LOCKS_RE='procd_vectra-controller-pro\.lock|vectra-power\.lock'
pw_lock_holders() { # pid:command[lock] of every process with one of them open
	# shellcheck disable=SC2010 # the link targets are what is matched
	ls -l /proc/[0-9]*/fd/* 2>/dev/null | grep -E "$PW_LOCKS_RE" \
		| sed -n 's|.*/proc/\([0-9]*\)/fd/.*-> .*/\([^/]*\)\.lock$|\1 \2|p' | sort -u | while read -r _p _k; do
		echo "$_p:$(tr '\0' ' ' < "/proc/$_p/cmdline" 2>/dev/null | cut -c1-40 | sed 's/ *$//')[$_k]"
	done | tr '\n' ';' | sed 's/;$//'
}

# Its variables are its own (_ah_*): ash has no locals, and its callers
# still need their _rc and _left after it.
pw_after_handback() { # <label> [piles]
	# First the question an operator asks next: does the init script answer?
	_ah_s0=$(date +%s)
	timeout 10 "$PW_VCTL_INIT" status >/dev/null 2>&1
	_ah_src=$?
	_ah_st="status exit $_ah_src in $(($(date +%s) - _ah_s0)) s"
	_ah_h=$(pw_lock_holders)
	if [ -z "$_ah_h" ] && [ "$_ah_src" != 124 ]; then
		PW_LOCK_ALL="$PW_LOCK_ALL $1: free ($_ah_st)."
		info "after $1: vctl's locks free; $_ah_st"
		[ "${2:-}" = piles ] && record pw_deadman_not_blocked PASS "after $1 nothing holds vctl's locks ($_ah_st): the cron dead-man is not held up"
		return 0
	fi
	if [ "$_ah_src" != 124 ]; then
		# The file open, the lock not taken: descriptors inherited and
		# unlocked; not a hold.
		PW_LOCK_ALL="$PW_LOCK_ALL $1: open, not held, by [$_ah_h] ($_ah_st)."
		info "after $1: vctl's lock file open in [$_ah_h] but not held; $_ah_st"
		[ "${2:-}" = piles ] && record pw_deadman_not_blocked PASS "after $1 the lock is not held ($_ah_st): the cron dead-man is not held up"
		return 0
	fi
	PW_LOCK_BAD="$PW_LOCK_BAD $1"
	PW_LOCK_ALL="$PW_LOCK_ALL $1: held by [$_ah_h]."
	_ah_t0=$(date +%s)
	timeout 8 "$PW_VCTL_INIT" status >/dev/null 2>&1
	_ah_rc=$?
	if [ "${2:-}" = piles ]; then
		# 0.6.0's cron dead-man asks the init script first, every minute:
		# with the lock held, how many of them wait after two cron minutes?
		sleep 125
		_ah_dm=$(ps w | grep -c '[d]eadman.sh')
		if [ "$_ah_dm" = 0 ]; then
			record pw_deadman_not_blocked PASS "125 s after $1, with vctl's init lock held, no cron dead-man waits on it"
		else
			record pw_deadman_not_blocked FAIL "125 s after $1: $_ah_dm cron dead-man runs wait on vctl's init lock ($(ps w | grep -E '[d]eadman.sh|[f]lock 1000|[r]c.common /etc/init.d/vectra' | awk '{$1=$2=$3=$4=""; print}' | sed 's/^ *//' | tr '\n' ';' | cut -c1-200)); they pile up one a minute until PassWall's daemons go"
		fi
	fi
	info "KNOWN DEFECT (vctl's init lock) after $1: held by [$_ah_h]; '$PW_VCTL_INIT status' exit $_ah_rc after $(( $(date +%s) - _ah_t0 )) s. Freeing it: a PassWall restart from the stand's shell"
	"$PW_INIT" restart >/dev/null 2>&1
	pw_wait 30 pw_up
	# Its restart kills its own; a `sleep 60` of lease2hosts.sh may outlive it.
	pw_wait 70 sh -c "! ls -l /proc/[0-9]*/fd/* 2>/dev/null | grep -qE '$PW_LOCKS_RE'" \
		|| { _ah_left=$(pw_lock_holders); info "still held by [$_ah_left]: killed"; for _ah_x in $(echo "$_ah_left" | tr ';' '\n' | cut -d: -f1); do kill -9 "$_ah_x" 2>/dev/null; done; }
	pw_wait_carrier passwall 45 >/dev/null
}

assert_pw_init_lock() {
	if [ -z "$PW_LOCK_BAD" ]; then
		record pw_handback_frees_init PASS "after every hand-back nothing held vctl's procd lock:$PW_LOCK_ALL"
	else
		record pw_handback_frees_init FAIL "PassWall's daemons, started by the hand-back, kept vctl's procd lock (fd 1000) and every later vctl init action waited on it, after:$PW_LOCK_BAD —$PW_LOCK_ALL"
	fi
}

# ------------------------------------------------------- H2 (a known defect) ---
#
# The render vctl's xray runs lives on tmpfs, and until 9bb567c the daemon
# brought xray up only from an existing one: after a reboot vctl held the
# router with no data plane. The stand records that where it bites
# (pw_trial_after_reboot, pw_keep_reboot_vctl) and then gets past it the way an
# operator would — apply-local and a restart — so the steps after it test
# their own subject.
pw_h2_workaround() { # <where>
	info "KNOWN DEFECT H2 at $1: vctl runs with no render (render $([ -s /var/run/vectra-controller-pro/xray.json ] && echo present || echo ABSENT), table inet vctl $(nft list table inet vctl >/dev/null 2>&1 && echo present || echo ABSENT)); re-seeding with 'vctl apply-local' and a restart"
	vctl apply-local -ignore-legacy-agent -config "$PW_AGENT_JSON" > "$PW_WORLD/apply-local-$1.log" 2>&1 \
		|| sed 's/^/       /' "$PW_WORLD/apply-local-$1.log" | tail -n 4
	timeout 120 "$PW_VCTL_INIT" restart >/dev/null 2>&1
	pw_wait_carrier vctl 60 || info "still not carrying after the workaround: $PW_PROBE"
}

# ---------------------------------------------------------- after the reboot ---

step7_off() {
	pw_phase trial-then-off
	_p0=$(pw_xray_pid)
	pw_vectra trial2 on --trial --minutes 10 --foreground
	if pw_wait_carrier vctl 60; then
		record pw_trial_after_reboot PASS "the first trial after a reboot carries the LAN on its own (the render rebuilt from /etc): $PW_PROBE"
	else
		_r=$([ -s /var/run/vectra-controller-pro/xray.json ] && echo present || echo ABSENT)
		if [ "$_r" = ABSENT ] || ! vctl_dataplane; then
			record pw_trial_after_reboot FAIL "KNOWN DEFECT H2: after a reboot vctl runs (procd: $(vctl_procd && echo yes || echo no)) with render $_r, data plane $(vctl_dataplane && echo loaded || echo ABSENT): $PW_PROBE"
			pw_h2_workaround trial2
		else
			record pw_trial_after_reboot FAIL "render present and data plane loaded after the reboot, but the LAN is not carried by vctl: $PW_PROBE"
		fi
	fi
	pw_phase off
	pw_overlap_start off
	pw_vectra off off --foreground
	_rc=$PW_VRC
	pw_wait 30 pw_up
	pw_wait_carrier passwall 45
	pw_wait 15 agent_up
	pw_wait 15 vctl_down
	pw_overlap_stop
	_p1=$(pw_xray_pid)
	_a1=$(agent_pid)
	_left=$(pw_handed_back)
	if [ "$_rc" = 0 ] && [ -z "$_left" ] && [ -n "$_p1" ] && [ "$_p1" != "$_p0" ]; then
		record pw_off_gives_back PASS "vectra off: PassWall2 carries again with a new xray ($_p0 -> $_p1), switch 1, snippet gone, agent back (pid $_a1), vctl off and unlinked, no breadcrumbs"
	else
		record pw_off_gives_back FAIL "off exit $_rc; xray $_p0 -> ${_p1:-none}; left: $_left"
	fi
	pw_after_handback off piles
	_p1=$(pw_xray_pid)
	pw_vectra off2 off --foreground
	_rc2=$PW_VRC
	sleep 3
	_p2=$(pw_xray_pid)
	_a2=$(agent_pid)
	pw_carries passwall
	_via=$?
	if [ "$_rc2" = 0 ] && [ "$_p2" = "$_p1" ] && [ "$_a2" = "$_a1" ] && [ "$_via" = 0 ] && [ "$(pw_switch)" = 1 ] \
		&& grep -q 'Vectra is off' "$PW_WORLD/vectra-off2.out"; then
		record pw_off_idempotent PASS "a second vectra off changed nothing: PassWall xray $_p2, agent $_a2, switch 1, still carrying"
	else
		record pw_off_idempotent FAIL "exit $_rc2; xray $_p1 -> ${_p2:-none}; agent $_a1 -> ${_a2:-none}; switch=$(pw_switch); $PW_PROBE"
	fi
}

step8_deadman() {
	pw_phase trial-1min
	_p0=$(pw_xray_pid)
	_t0=$(date +%s)
	pw_vectra trial1 on --trial --minutes 1
	_rc=$PW_VRC
	pw_wait_carrier vctl 60
	_during=$?
	_dprobe=$PW_PROBE
	_dm=$(jsonfilter -i "$PW_TRIAL_FILE" -e '@.deadman' 2>/dev/null)
	if [ "$_rc" = 0 ] && [ "$_during" = 0 ]; then
		record pw_deadman_trial_carries PASS "vectra on --trial --minutes 1, in the background: vctl carries ($_dprobe); deadman pid ${_dm:-?}"
	else
		record pw_deadman_trial_carries FAIL "exit $_rc; $_dprobe"
	fi
	pw_phase deadman-off
	pw_overlap_start deadman
	# One minute from the trial's start, the deadman's poll, and its off.
	pw_wait 150 trial_over
	_t1=$(date +%s)
	pw_wait 30 pw_up
	pw_wait_carrier passwall 45
	pw_wait 15 agent_up
	pw_overlap_stop
	_p1=$(pw_xray_pid)
	_left=$(pw_handed_back)
	_line=$(grep -E 'min are up|trial ends|Vectra is off' "$PW_POWER_LOG" 2>/dev/null | tail -n 2 | tr '\n' ' ' | cut -c1-140)
	pw_after_handback deadman
	if [ -z "$_left" ] && [ -n "$_p1" ] && [ "$_p1" != "$_p0" ]; then
		record pw_deadman_ends_trial PASS "nobody ran vectra off: $((_t1 - _t0)) s after the trial began it was over, PassWall2 carries (xray $_p0 -> $_p1), switch 1, snippet gone, agent back; power log: $_line"
	else
		record pw_deadman_ends_trial FAIL "after $((_t1 - _t0)) s: xray $_p0 -> ${_p1:-none}; left: $_left; power log: $_line"
		tail -n 12 "$PW_POWER_LOG" 2>/dev/null | sed 's/^/       power: /'
	fi
}

step9_keep() {
	pw_phase trial-keep
	pw_vectra trial3 on --trial --minutes 10 --foreground
	pw_wait_carrier vctl 60 || info "the trial before keep: vctl did not carry ($PW_PROBE)"
	pw_vectra keep keep --foreground
	_rc=$PW_VRC
	sleep 3
	_ce=$(crumbs_etc)
	_f=
	[ "$_rc" = 0 ] || _f="$_f exit=$_rc"
	[ "$(uci -q get $PW_PKG.main.enabled)" = 1 ] || _f="$_f uci-enabled=$(uci -q get $PW_PKG.main.enabled)"
	vctl_linked || _f="$_f vctl-unlinked"
	pw_linked && _f="$_f passwall-linked"
	[ "$(pw_switch)" = 0 ] || _f="$_f switch=$(pw_switch)"
	pw_down || _f="$_f passwall-procs"
	agent_linked && _f="$_f agent-linked"
	agent_up && _f="$_f agent-running"
	[ "$(echo "$_ce" | wc -w)" = 3 ] || _f="$_f etc-crumbs=[$_ce]"
	[ ! -f "$PW_SNIPPET" ] || _f="$_f snippet-left"
	[ ! -e "$PW_TRIAL_FILE" ] || _f="$_f trial-file-left"
	pw_wait_carrier vctl 30 || _f="$_f carrier:{$PW_PROBE}"
	if [ -z "$_f" ]; then
		record pw_keep PASS "vectra keep: uci enabled=1 + boot link; PassWall unlinked, switch 0; agent unlinked, stopped; on /etc: $_ce; snippet and trial file gone; LAN via vctl"
	else
		record pw_keep FAIL "${_f# }"
	fi

	pw_phase reboot-kept
	pw_power_cycle
	pw_boot
	pw_wait 30 vctl_procd
	pw_wait_carrier vctl 90
	_via=$?
	_f=
	[ "$_via" = 0 ] || _f="$_f carrier:{$PW_PROBE} vctl-running=$(vctl_procd && echo yes || echo no) dataplane=$(vctl_dataplane && echo yes || echo no) render=$([ -s /var/run/vectra-controller-pro/xray.json ] && echo yes || echo NONE)"
	vctl_linked || _f="$_f vctl-unlinked"
	pw_linked && _f="$_f passwall-linked"
	[ "$(pw_switch)" = 0 ] || _f="$_f switch=$(pw_switch)"
	pw_down || _f="$_f passwall-procs=[$(pw_proxy_pids)]"
	agent_linked && _f="$_f agent-linked"
	agent_up && _f="$_f agent-running"
	[ -n "$(crumbs_etc)" ] || _f="$_f no-etc-crumbs"
	if [ -z "$_f" ]; then
		record pw_keep_reboot_vctl PASS "after the reboot vctl carries the LAN ($PW_PROBE); PassWall unlinked with switch 0, agent unlinked, both down; breadcrumbs on /etc: $(crumbs_etc)"
	else
		record pw_keep_reboot_vctl FAIL "${_f# }$(vctl_dataplane || echo ' — KNOWN DEFECT H2 if the render is NONE')"
		pw_dump
	fi

	pw_phase midnight-kept
	PW_RUNS0=$(pw_runs)
	"$PW_INIT" restart >/dev/null 2>&1
	pw_wait 30 pw_ran_since "$PW_RUNS0"
	sleep 3
	_pp=$(pw_proxy_pids)
	pw_carries vctl
	_via=$?
	if [ -z "$_pp" ] && pw_rules_gone; then
		record pw_keep_midnight PASS "'passwall2 restart' on the kept router: nothing came up ($(pw_rules)); the LAN via $([ "$_via" = 0 ] && echo vctl || echo "?: $PW_PROBE")"
	else
		record pw_keep_midnight FAIL "PassWall proxies: [$_pp] switch=$(pw_switch) $(pw_rules); $PW_PROBE"
	fi

	pw_phase off-kept
	pw_overlap_start off-kept
	pw_vectra offk off --foreground
	_rc=$PW_VRC
	pw_wait 30 pw_up
	pw_wait_carrier passwall 45
	pw_wait 15 agent_up
	pw_wait 15 vctl_down
	pw_overlap_stop
	_left=$(pw_handed_back)
	if [ "$_rc" = 0 ] && [ -z "$_left" ]; then
		record pw_keep_off PASS "vectra off: PassWall2 back and carrying ($PW_PROBE), switch 1, linked; agent back; vctl off and unlinked; no breadcrumbs"
	else
		record pw_keep_off FAIL "exit $_rc ($(tail -n 1 "$PW_WORLD/vectra-offk.out" | cut -c1-90)); left: $_left"
	fi
	pw_after_handback off-kept
	# Its last step, the boot link's `disable`, may have waited on the lock
	# too: an operator's second `vectra off` finishes it.
	if vctl_linked; then
		info "vectra off left vctl's boot link: a second vectra off, as an operator would"
		pw_vectra offk2 off --foreground
	fi
}

# ------------------------------------------------------ a crash (0.6.0) ------
#
# 0.6.0's cron dead-man (deadman.sh, every minute from the package's managed
# crontab block — not the trial's own deadman of step 8): while Vectra should
# run and vctl does not, it unloads vctl's data plane at once, starts vctl
# again with a backoff, and after 10 minutes down, where PassWall or the agent
# is owed, runs `vectra off` in shell. Its minutes are its runs: the stand runs
# it by hand, one "minute" at a time, as cron would (cron runs it besides).
PW_DEADMAN=/usr/libexec/$PW_PKG/deadman.sh
PW_DM_STATE=/var/run/vectra-deadman.state
PW_VCTL_BIN=/usr/sbin/vctl

pw_deadman_minute() { "$PW_DEADMAN" >/dev/null 2>&1; }
pw_dm_state() { cat "$PW_DM_STATE" 2>/dev/null || echo none; }
pw_dm_log() { logread 2>/dev/null | grep 'vectra-controller-pro-deadman' | tail -n "${1:-1}" | sed 's/.*deadman[^:]*: //' | cut -c1-120 | tr '\n' '|'; }
vctl_xray_pids() { ps w 2>/dev/null | grep '[v]ctl-xray-wrapper' | awk '{print $1}' | tr '\n' ' ' | sed 's/ $//'; }

step10_crash() {
	pw_phase crash
	info "the cron dead-man's line: $(grep -F deadman.sh /etc/crontabs/root 2>/dev/null | head -n 1); crond $(pgrep crond >/dev/null && echo runs || echo 'DOES NOT RUN')"
	pw_vectra crash on --trial --minutes 30 --foreground
	if ! pw_wait_carrier vctl 60; then
		record pw_crash_unloaded FAIL "the trial before the crash never carried: $PW_PROBE"
		return
	fi

	# 1. vctl SIGKILLed with its data plane loaded; the dead-man's minute
	# before procd's respawn (5 s) can come.
	_v=$(vctl_pid)
	_x0=$(vctl_xray_pids)
	kill -9 $_v
	_t0=$(date +%s)
	vctl_dataplane && _left=loaded || _left=gone
	timeout 60 "$PW_DEADMAN" >/dev/null 2>&1
	_dmrc=$?
	vctl_dataplane && _after=loaded || _after=gone
	_st=$(pw_dm_state)
	_msg=$(pw_dm_log 2)
	pw_wait 60 sh -c "[ -n \"\$(ps w | grep '[/]usr/sbin/vctl agent')\" ]"
	pw_wait_carrier vctl 60
	_via=$?
	_t1=$(date +%s)
	sleep 5
	_x1=$(vctl_xray_pids)
	_x255=$(logread 2>/dev/null | grep -c 'xray exited; will restart.*exit status 255')
	_unl=no
	{ [ "$_after" = gone ] || echo "$_msg" | grep -q 'unloading it'; } && _unl=yes
	if [ "$_left" = loaded ] && [ "$_unl" = yes ] && [ "$_dmrc" != 124 ] && [ "$_via" = 0 ]; then
		record pw_crash_unloaded PASS "kill -9 vctl (pid $_v): its data plane was still loaded; the dead-man's first minute unloaded it ('$_msg' state '$_st'); vctl back (pid $(vctl_pid)) and the LAN carried $((_t1 - _t0)) s after the kill: $PW_PROBE"
	else
		record pw_crash_unloaded FAIL "after the kill: data plane $_left; the dead-man's minute: exit $_dmrc, data plane $_after ('$_msg' state '$_st'); carrier after $((_t1 - _t0)) s: $PW_PROBE"
	fi
	# The killed vctl's xray is its own process group: does it outlive it,
	# and does the new vctl's xray start next to it?
	case " $_x1 " in
	*" $_x0 "*) _orphan=yes ;;
	*) _orphan=no ;;
	esac
	if [ "$_orphan" = no ] && [ "$(echo "$_x1" | wc -w)" = 1 ]; then
		record pw_crash_one_xray PASS "the killed vctl's xray ($_x0) is gone; one xray, the new vctl's ($_x1)"
	else
		record pw_crash_one_xray FAIL "vctl xray before the kill: $_x0; after the restart: [$_x1] (the old one still runs: $_orphan); the new vctl's xray exited with status 255 $_x255 times: $(tail -n 2 /var/log/$PW_PKG/xray.log 2>/dev/null | tr '\n' ' ' | cut -c1-160)"
		# The stand's way on: the orphan goes, as a reboot would take it.
		[ "$_orphan" = yes ] && kill $_x0 2>/dev/null
		pw_wait_carrier vctl 60 >/dev/null
	fi

	# 2. vctl cannot start at all. Each dead-man minute is run by hand under a
	# timeout (a minute that hangs is a finding, not the stand's end) until
	# someone gives the router back: the init script's own guard (a
	# controller binary that is not there), or the dead-man after 10 minutes.
	pw_phase crash-unstartable
	chmod -x "$PW_VCTL_BIN"
	_v=$(vctl_pid)
	rm -f "$PW_DM_STATE"
	kill -9 $_v
	_t0=$(date +%s)
	pw_overlap_start crash
	_n=0
	_trace=
	_stuck=
	while [ "$_n" -lt 14 ]; do
		_n=$((_n + 1))
		timeout 60 "$PW_DEADMAN" >/dev/null 2>&1
		_dmrc=$?
		[ "$_dmrc" = 124 ] && _stuck="$_stuck $_n"
		_trace="$_trace $_n:[$(pw_dm_state)]$(vctl_dataplane && echo L)$([ "$_dmrc" = 124 ] && echo HUNG)"
		pw_up && break
		[ -e "$PW_TRIAL_FILE" ] || break
		sleep 2
	done
	pw_wait 60 pw_up
	pw_wait_carrier passwall 60
	pw_wait 20 agent_up
	pw_overlap_stop
	_t1=$(date +%s)
	info "the dead-man's minutes (down attempts next idle; L = data plane loaded, HUNG = killed after 60 s):$_trace"
	info "its log: $(pw_dm_log 4)"
	_guard=$(logread 2>/dev/null | grep -o 'controller binary missing at [^;]*; handing the router back[^|]*' | tail -n 1)
	_gave=$(logread 2>/dev/null | grep 'vectra-controller-pro-deadman' | grep -o 'has not run for [0-9]* minutes after [0-9]* starts' | tail -n 1)
	_left=$(pw_handed_back)
	[ -f "$PW_SNIPPET" ] && info "left behind: the trial's snippet $PW_SNIPPET (its next boot runs it; harmless: the switch is on already)"
	_by=
	[ -z "$_gave" ] || _by=" the dead-man ($_gave)"
	[ -z "$_guard" ] || _by="$_by the init script guard ($_guard)"
	if pw_carried_by passwall && [ -z "$_stuck" ]; then
		record pw_crash_handback PASS "vctl unstartable (chmod -x): PassWall2 carries again after $_n dead-man minutes by hand ($((_t1 - _t0)) s), given back by${_by:- ?}; still owed: ${_left:-nothing}; trial file $([ -e "$PW_TRIAL_FILE" ] && echo 'still there' || echo gone)"
	else
		record pw_crash_handback FAIL "after $_n dead-man minutes ($((_t1 - _t0)) s): hung in minute(s)[${_stuck# }]; given back by${_by:- nobody}; left: ${_left:-nothing}; $PW_PROBE"
	fi
	# Does the dead-man still run to its end, or does what the hand-back
	# started hold its locks?
	timeout 60 "$PW_DEADMAN" >/dev/null 2>&1
	_dmrc=$?
	if [ "$_dmrc" != 124 ]; then
		record pw_crash_deadman_after PASS "the dead-man's next minute after the hand-back ran to its end (exit $_dmrc); lock holders: [$(pw_lock_holders)]"
	else
		record pw_crash_deadman_after FAIL "the dead-man's next minute after the hand-back hung for 60 s: vctl's locks held by [$(pw_lock_holders)]"
	fi
	chmod +x "$PW_VCTL_BIN"
	pw_after_handback crash
	if [ -e "$PW_TRIAL_FILE" ] || vctl_linked || vctl_procd; then
		info "after the crash's hand-back: trial file $([ -e "$PW_TRIAL_FILE" ] && echo there || echo gone), vctl linked $(vctl_linked && echo yes || echo no), procd runs it $(vctl_procd && echo yes || echo no): vectra off, as an operator would"
		pw_vectra crash-off off --foreground
		pw_wait 30 pw_up
		pw_wait_carrier passwall 45 >/dev/null
		pw_after_handback crash-off
	fi
}

# ------------------------------------------------- a revert (0.6.0-r2) ------
#
# vctl loads its ruleset behind a commit-confirm: a shell that sleeps 90 s and
# takes the ruleset down unless a check-in confirmed it. Before 0.6.0-r2 a
# revert was for good: on the test router one in the first minute of a trial
# left the rest of it direct. From 0.6.0-r2 every poll loads a missing table
# again (ensureDataPlane), behind the same commit-confirm. The stand's poll is
# PW_POLL seconds (poll_interval, pw_vctl_seed).
PW_POLL=10
pw_table() { nft list table inet vctl >/dev/null 2>&1; }
pw_reverts() { logread 2>/dev/null | grep -c 'commit-confirm: auto-reverted'; }
# The panel unreachable: its packets dropped where they arrive.
pw_panel_block() {
	in_inet nft -f - <<EOT
table inet pwpanel {
	chain input {
		type filter hook input priority filter; policy accept;
		ip daddr $PW_PANEL_IP tcp dport $PW_PANEL_PORT counter drop
	}
}
EOT
}
pw_panel_unblock() { in_inet nft delete table inet pwpanel 2>/dev/null; }
pw_vctl_log() { logread 2>/dev/null | grep -E "$1" | tail -n "${2:-1}" | sed 's/^[A-Z][a-z]* [A-Z][a-z]* *[0-9]* //' | cut -c1-150 | tr '\n' '|'; }

step11_revert() {
	pw_phase revert
	pw_vectra revert on --trial --minutes 30 --foreground
	if ! pw_wait_carrier vctl 60; then
		record pw_revert_table_back FAIL "the trial before it never carried: $PW_PROBE"
		record pw_revert_panel_down FAIL "not reached"
		return
	fi
	# Past the takeover's own commit-confirm, so what follows is ours.
	pw_wait 100 sh -c '! pgrep -f "[/]fw-confirm" >/dev/null'

	# 1. The table flushed from under a vctl in proxy mode.
	nft delete table inet vctl
	_t0=$(date +%s)
	pw_wait 120 pw_table
	_t1=$(date +%s)
	if pw_table && pw_wait_carrier vctl 30; then
		record pw_revert_table_back PASS "nft delete table inet vctl: back $((_t1 - _t0)) s later (poll ${PW_POLL} s; '$(pw_vctl_log 'data plane should be loaded')'), the LAN through vctl again: $PW_PROBE"
	else
		record pw_revert_table_back FAIL "no table $((_t1 - _t0)) s after the delete (poll ${PW_POLL} s); $PW_PROBE"
	fi
	pw_wait 100 sh -c '! pgrep -f "[/]fw-confirm" >/dev/null'

	# 2. The same with the panel unreachable: the reload's commit-confirm
	# finds no check-in and reverts it; once the panel answers, a reload
	# stays.
	pw_panel_block
	_rv0=$(pw_reverts)
	nft delete table inet vctl
	_t0=$(date +%s)
	pw_wait 60 pw_table
	_tr=$(($(date +%s) - _t0))
	_rl=no
	pw_table && _rl=yes
	# The revert is read off its own log line: the next poll loads the table
	# again, within the second the stand samples at.
	pw_wait 150 sh -c "[ \"\$(logread 2>/dev/null | grep -c 'commit-confirm: auto-reverted')\" -gt $_rv0 ]"
	_tv=$(($(date +%s) - _t0))
	_rv=no
	[ "$(pw_reverts)" -gt "$_rv0" ] && _rv=yes
	_revlog=$(pw_vctl_log 'commit-confirm: auto-reverted')
	pw_wait 30 pw_table
	_again=no
	pw_table && _again=yes
	pw_probe
	_during=$PW_PROBE
	pw_panel_unblock
	_t2=$(date +%s)
	pw_wait 60 pw_table
	_tb=$(($(date +%s) - _t2))
	# It has to outlive its commit-confirm this time.
	_stay=yes
	_i=0
	while [ "$_i" -lt 100 ]; do
		pw_table || _stay="no (gone $_i s after it came back)"
		[ "$_stay" = yes ] || break
		_i=$((_i + 1))
		sleep 1
	done
	pw_wait_carrier vctl 30
	_via=$?
	if [ "$_rl" = yes ] && [ "$_rv" = yes ] && [ "$_again" = yes ] && [ "$_stay" = yes ] && [ "$_via" = 0 ]; then
		record pw_revert_panel_down PASS "panel unreachable: the deleted table reloaded after ${_tr} s, reverted by its commit-confirm ${_tv} s after the delete ('$_revlog') and loaded again at once, the panel still down ($_during); panel back: there ${_tb} s later and still there 100 s on; the LAN through vctl: $PW_PROBE"
	else
		record pw_revert_panel_down FAIL "reloaded=$_rl (${_tr} s) reverted=$_rv (${_tv} s, '$_revlog') loaded-again=$_again after-the-panel: there in ${_tb} s, stays=$_stay; $PW_PROBE"
	fi
	pw_panel_unblock

	pw_phase revert-off
	pw_vectra revert-off off --foreground
	pw_wait 30 pw_up
	pw_wait_carrier passwall 45 >/dev/null
	pw_after_handback revert-off
}

pw_upgrade_check() { # <assertion> <opkg install args> <what>
	_dm0=$(jsonfilter -i "$PW_TRIAL_FILE" -e '@.deadman' 2>/dev/null)
	_p0=$(vctl_pid)
	# shellcheck disable=SC2086 # the arguments are words
	timeout 150 opkg install $2 > "$PW_WORLD/opkg-$1.out" 2>&1
	_rc=$?
	if [ "$_rc" = 124 ]; then
		info "opkg did not return within 150 s (killed); what it waited on: $(ps w | grep -E '[f]lock 1000|[r]c.common /etc/init.d/vectra' | awk '{$1=$2=$3=$4=""; print}' | tr '\n' ';' | cut -c1-160); vctl's init lock held by [$(pw_lock_holders)]"
	fi
	sed 's/^/       opkg: /' "$PW_WORLD/opkg-$1.out" | tail -n 6
	pw_wait 40 vctl_procd
	pw_wait_carrier vctl 60
	_via=$?
	_facts=$(pw_trial_facts)
	_dm=$(jsonfilter -i "$PW_TRIAL_FILE" -e '@.deadman' 2>/dev/null)
	_alive=no
	[ -n "$_dm" ] && grep -q deadman "/proc/$_dm/cmdline" 2>/dev/null && _alive=yes
	if [ "$_rc" = 0 ] && [ "$_via" = 0 ] && [ -z "$_facts" ] && [ "$_alive" = yes ]; then
		record "$1" PASS "$3 during a trial: no vctl boot link, uci enabled=0, the trial and its deadman (pid $_dm) intact, every step-2 fact holds; vctl (pid ${_p0:-none} -> $(vctl_pid)) carries: $PW_PROBE"
	else
		record "$1" FAIL "$3: opkg exit $_rc; carrier: $PW_PROBE; broken: ${_facts:-none}; deadman $_dm0 -> ${_dm:-none} alive=$_alive"
	fi
}

step10_upgrade() {
	pw_phase trial-upgrade
	# 30 minutes: an install that hangs must not let a deadman end the trial
	# in the middle of this step.
	pw_vectra trial4 on --trial --minutes 30 --foreground
	if ! pw_wait_carrier vctl 60; then
		if [ ! -s /var/run/vectra-controller-pro/xray.json ] || ! vctl_dataplane; then
			pw_h2_workaround trial4
		else
			info "the trial before the upgrade: vctl's data plane is loaded but did not carry: $PW_PROBE"
		fi
	fi
	pw_upgrade_check pw_upgrade_same "--force-reinstall $PW_IPK" "opkg install --force-reinstall of the same ipk ($(opkg list-installed $PW_PKG | awk '{print $3}'))"
	# A reinstall runs the old package's prerm — stop, a hand-back — first. If
	# that left the router out of the trial or vctl's init locked, the next
	# check starts from a clean trial of its own.
	if ! vctl_procd || [ ! -e "$PW_TRIAL_FILE" ] || [ -n "$(pw_lock_holders)" ]; then
		pw_after_handback upgrade-same
		pw_vectra reset-off off --foreground
		pw_after_handback reset-off
		pw_vectra trial5 on --trial --minutes 30 --foreground
		pw_wait_carrier vctl 60 || info "the trial before the next install: vctl did not carry ($PW_PROBE)"
	fi
	pw_upgrade_check pw_upgrade_newer "$PW_IPK_NEWER" "opkg install of the same payload one version up ($(opkg list-installed $PW_PKG | awk '{print $3}') -> $PW_NEWER_VER)"
	pw_phase off-upgrade
	pw_overlap_start off-upgrade
	pw_vectra offu off --foreground
	_rc=$PW_VRC
	pw_wait 30 pw_up
	pw_wait_carrier passwall 45
	pw_wait 15 agent_up
	pw_wait 15 vctl_down
	pw_overlap_stop
	_left=$(pw_handed_back)
	pw_after_handback off-upgrade
	if [ "$_rc" = 0 ] && [ -z "$_left" ]; then
		record pw_upgrade_then_off PASS "vectra off after the upgrades: PassWall2 carries again ($PW_PROBE), switch 1, agent back, no vctl link"
	else
		record pw_upgrade_then_off FAIL "exit $_rc; left: $_left"
	fi
}

# The takeover while PassWall restarts. PassWall's init serialises its own
# start, stop and restart with a lock (/var/lock/passwall2.lock); the
# takeover's `stop` meets a restart in flight, and its check that PassWall is
# down is a sample. Each attempt: a restart in the background, the trial
# after a delay, then 20 s of watching for PassWall's proxies next to vctl.
step11_race() {
	pw_phase race
	_res=
	_bad=
	for _delay in 0 0.5 1.5 3; do
		pw_wait_carrier passwall 45 >/dev/null
		"$PW_INIT" restart >/dev/null 2>&1 &
		pw_nap "$_delay"
		pw_vectra race on --trial --minutes 10 --foreground
		_rc=$PW_VRC
		_seen=
		_i=0
		while [ "$_i" -lt 20 ]; do
			_pp=$(pw_proxy_pids)
			[ -n "$_pp" ] && _seen="$_seen ${_i}s:[$_pp]"
			_i=$((_i + 1))
			sleep 1
		done
		pw_carries vctl
		_via=$?
		_one="delay ${_delay}s: vectra exit $_rc, PassWall proxies after the takeover: ${_seen:-none}, switch $(pw_switch), LAN $([ "$_via" = 0 ] && echo 'via vctl' || echo "{$PW_PROBE}")"
		info "$_one"
		_res="$_res [$_one]"
		{ [ "$_rc" = 0 ] && [ -z "$_seen" ] && [ "$_via" = 0 ]; } || _bad=1
		pw_overlap_start "race-off-$_delay"
		pw_vectra race-off off --foreground
		pw_wait 30 pw_up
		pw_wait 15 vctl_down
		pw_overlap_stop
		pw_after_handback "race-off-$_delay"
	done
	pw_wait_carrier passwall 45 >/dev/null
	if [ -z "$_bad" ]; then
		record pw_race_restart PASS "a PassWall restart in flight never outlived the takeover:$_res"
	else
		record pw_race_restart FAIL "PassWall's stack next to vctl after a takeover that met its restart:$_res"
	fi
}

# The operator config and a provider document on /etc, but nothing ever
# applied: state.json carries no config digest. After a reboot vctl must not
# build a data plane of its own accord from them — it waits for its first
# apply (a job, the router UI, apply-local). Intended.
step12_unapplied() {
	pw_phase unapplied
	sed -i -e '/"config_digest":/d' -e '/"splice_key":/d' "$PW_MARKER_DIR/state.json"
	if ! jsonfilter -i "$PW_MARKER_DIR/state.json" -e '@.router_id' >/dev/null 2>&1; then
		record pw_unapplied_no_dataplane FAIL "could not take the digest out of state.json cleanly"
		return
	fi
	pw_power_cycle
	pw_boot
	pw_wait 60 pw_up
	pw_wait_carrier passwall 60 >/dev/null
	_t0=$(date +%s)
	pw_vectra unapplied on --trial --minutes 10 --foreground
	_rc=$PW_VRC
	_t1=$(date +%s)
	sleep 15
	pw_probe
	_render=absent
	[ -e /var/run/vectra-controller-pro/xray.json ] && _render=PRESENT
	_x=$(ps w 2>/dev/null | grep -c '[v]ctl-xray-wrapper')
	_docs=yes
	[ -s "$PW_MARKER_DIR/xray-desired.json" ] && [ -s "$PW_MARKER_DIR/provider-config.json" ] || _docs=no
	_dig=$(jsonfilter -i "$PW_MARKER_DIR/state.json" -e '@.config_digest' 2>/dev/null)
	# Nothing applied, so nothing to carry. Before 0.6.0 vctl stayed up
	# without a data plane; from 0.6.0 the trial sees that and gives the
	# router back ("vctl runs without its data plane") — either keeps the LAN
	# off a render nobody applied. How long the trial held it is in the log.
	if [ "$_docs" = yes ] && [ -z "$_dig" ] && vctl_procd && [ "$_render" = absent ] && [ "$_x" = 0 ] && ! vctl_dataplane; then
		record pw_unapplied_no_dataplane PASS "documents on /etc, no digest in state.json: vctl runs (trial exit $_rc) with no render, no xray and no data plane, as intended; the LAN meanwhile: $PW_PROBE"
	elif [ "$_docs" = yes ] && [ -z "$_dig" ] && [ "$_render" = absent ] && [ "$_x" = 0 ] && ! vctl_dataplane \
		&& grep -q 'runs without its data plane' "$PW_WORLD/vectra-unapplied.out" && pw_carried_by passwall; then
		record pw_unapplied_no_dataplane PASS "documents on /etc, no digest in state.json: no render, no xray, no data plane; the trial gave the router back by itself $((_t1 - _t0)) s after 'vectra on' (exit $_rc: '$(grep -m1 'runs without its data plane' "$PW_WORLD/vectra-unapplied.out" | sed 's/^.*Z //' | cut -c1-80)'); PassWall2 carries: $PW_PROBE"
	else
		record pw_unapplied_no_dataplane FAIL "documents=$_docs digest='${_dig}' vctl running=$(vctl_procd && echo yes || echo no) render=$_render xray=$_x dataplane=$(vctl_dataplane && echo yes || echo no)"
	fi
	pw_vectra unapplied-off off --foreground
	pw_wait 30 pw_up
	pw_wait_carrier passwall 45 >/dev/null
	pw_after_handback unapplied-off
}

assert_pw_one_xray() {
	if [ -z "$PW_OV_BAD" ]; then
		record pw_handback_one_xray PASS "no hand-back ran vctl's xray and PassWall's at once:$PW_OV_ALL"
	else
		record pw_handback_one_xray FAIL "both xray at once during:$PW_OV_BAD —$PW_OV_ALL"
	fi
}

assert_pw_isolated() {
	_esc=$(nft list counter inet pwguard escape 2>/dev/null | awk '/packets/{print $2; exit}')
	_def4=$(ip route show default 2>/dev/null)
	_def6=$(ip -6 route show default 2>/dev/null)
	_ci=$(grep -c 'check-in\|register' /tmp/ui-panel.log 2>/dev/null)
	if [ "$_esc" = 0 ] && [ -z "$_def4" ] && [ -z "$_def6" ] && [ "${_ci:-0}" -gt 0 ] && grep -q '^127.0.0.99 api.vectra-pro.net' /etc/hosts; then
		record pw_isolated PASS "after the downloads nothing left through eth0 (0 packets), no default route, api/router.vectra-pro.net sinkholed; vctl's $_ci panel exchanges went to the stand's stub"
	else
		record pw_isolated FAIL "eth0 escapes=$_esc default v4='$_def4' v6='$_def6' panel exchanges=$_ci"
	fi
}

# -------------------------------------------------------------------- main ---


# PassWall-compatible routing (UCI route_source 'passwall'): vctl renders from
# PassWall2's own configuration, generated by PassWall2's own code — here
# shaped like the fleet's: a shunt whose one rule sends origin.stand, with
# FakeDNS, through PassWall's node, and everything else straight out. vctl
# carries it: PassWall's own stack is down throughout (checked), so what is
# measured is vctl's data plane with PassWall's routing, not PassWall.
# The kernel's direct routing (cmd/vctl/direct_bypass.go). PW_DIRECT_IP is on
# the direct shunt's ip_list; its endpoints answer with the peer address they
# see, which says who carried the connection: the client's own (10.99.0.2) is
# the kernel forwarding it, the router's (10.44.0.1) is xray dialling out.
PW_DIRECT_IP=44.44.44.46
vctl_ctr() { nft list counter inet vctl "$1" 2>/dev/null | awk '/packets/{print $2; exit}'; }
pw_direct_world() {
	ip -n "$INET_NS" addr add "$PW_DIRECT_IP/32" dev lo 2>/dev/null
	ip route replace "$PW_DIRECT_IP/32" via "$INET_PEER_IP" dev vinet
	for _ip in "$PW_DIRECT_IP" "$ORIGIN_IP"; do
		in_inet socat "TCP-LISTEN:82,bind=$_ip,fork,reuseaddr" 'SYSTEM:echo "peer $SOCAT_PEERADDR"' >/dev/null 2>&1 &
		in_inet socat "TCP-LISTEN:81,bind=$_ip,fork,reuseaddr" 'SYSTEM:echo "peer $SOCAT_PEERADDR"; read x; echo "got $x"' >/dev/null 2>&1 &
	done
	# The guard drops every forwarded client packet; the kernel's direct
	# routes to PW_DIRECT_IP are forwarded by design.
	nft insert rule inet standguard forward ip daddr "$PW_DIRECT_IP" accept
	nft insert rule inet standguard forward ip saddr "$PW_DIRECT_IP" accept
	sleep 1
}
# pw_slow <ip> <mid-command>: a connection from the client that sends its
# request 4 seconds in, with <mid-command> run at 2 seconds; prints what the
# client received ("peer <addr> got second" when the connection lived). The
# request after the change is the point: it is the client's packets that a
# change of hands sends elsewhere. stdin stays open throughout: at an EOF
# socat half-closes and gives up 0.5 s later.
pw_slow() {
	in_client sh -c "(sleep 4; echo second; sleep 3) | socat -T12 - TCP4:$1:81" > "$PW_WORLD/slow.out" 2>&1 &
	_sp=$!
	sleep 2
	sh -c "$2"
	wait $_sp
	tr '\n' ' ' < "$PW_WORLD/slow.out"
}

pw_direct_checks() {
	_set=""
	_i=0
	while [ $_i -lt 30 ]; do
		_set=$(nft list set inet vctl vctl_direct4 2>/dev/null | tr -d '\n')
		echo "$_set" | grep -q "$PW_DIRECT_IP" && break
		sleep 1
		_i=$((_i + 1))
	done
	_n0=$(vctl_ctr vctl_direct_new)
	_peer=$(in_client socat -T5 - "TCP4:$PW_DIRECT_IP:82" 2>&1)
	_n1=$(vctl_ctr vctl_direct_new)
	if echo "$_set" | grep -q "$PW_DIRECT_IP" && [ "$_peer" = "peer $CLIENT_IP" ] && [ "${_n1:-0}" -gt "${_n0:-0}" ]; then
		record pw_direct_by_kernel PASS "the direct shunt's address is in vctl_direct4 and the kernel carried the client there ($_peer; vctl_direct_new ${_n0}->${_n1}), xray never saw it"
	else
		record pw_direct_by_kernel FAIL "set: $(echo "$_set" | grep -o 'elements = {[^}]*}' | cut -c1-120); endpoint saw '$_peer'; vctl_direct_new ${_n0:-?}->${_n1:-?}; $(logread -e vctl 2>/dev/null | grep -iE 'direct' | tail -2 | tr '\n' ' ' | cut -c1-240)"
	fi
	_peer=$(in_client socat -T5 - "TCP4:$ORIGIN_IP:82" 2>&1)
	if [ "$_peer" = "peer $ROUTER_INET_IP" ]; then
		record pw_direct_rest_via_xray PASS "an address on no direct list still goes through xray ($_peer)"
	else
		record pw_direct_rest_via_xray FAIL "the origin saw '$_peer', not the router"
	fi
	# A connection the kernel carries stays with the kernel when the set is
	# emptied under it (a reprogram does that)...
	_out=$(pw_slow "$PW_DIRECT_IP" "nft flush set inet vctl vctl_direct4")
	nft add element inet vctl vctl_direct4 "{ $PW_DIRECT_IP }" 2>/dev/null
	case "$_out" in
	"peer $CLIENT_IP got second"*) record pw_direct_sticky_kernel PASS "a kernel-carried connection lived through its set being flushed ($_out)" ;;
	*) record pw_direct_sticky_kernel FAIL "the client got '$_out'" ;;
	esac
	# ...and one xray carries stays with xray when its address enters it.
	# TCP early demux hands an established packet to its socket before the
	# kernel routes it, so it would keep xray's connection alive whatever the
	# rules said: off for this check and its control, so that what is
	# measured is the rules.
	_ed=$(cat /proc/sys/net/ipv4/tcp_early_demux 2>/dev/null)
	echo 0 > /proc/sys/net/ipv4/tcp_early_demux 2>/dev/null
	_out=$(pw_slow "$ORIGIN_IP" "nft add element inet vctl vctl_direct4 '{ $ORIGIN_IP }'")
	nft delete element inet vctl vctl_direct4 "{ $ORIGIN_IP }" 2>/dev/null
	case "$_out" in
	"peer $ROUTER_INET_IP got second"*) record pw_direct_sticky_xray PASS "an xray-carried connection lived through its address entering the direct set ($_out)" ;;
	*) record pw_direct_sticky_xray FAIL "the client got '$_out'" ;;
	esac
	# Control: the same, with the set tested per packet (what the rules would
	# be without the conntrack bit) — the connection must change hands and
	# die, or the two checks above prove nothing.
	nft insert rule inet vctl prerouting ip daddr @vctl_direct4 return
	_h=$(nft -a list chain inet vctl prerouting 2>/dev/null | grep 'ip daddr @vctl_direct4 return' | grep -v 'ct state' | awk '{print $NF}' | head -n 1)
	_out=$(pw_slow "$ORIGIN_IP" "nft add element inet vctl vctl_direct4 '{ $ORIGIN_IP }'")
	nft delete element inet vctl vctl_direct4 "{ $ORIGIN_IP }" 2>/dev/null
	[ -n "$_h" ] && nft delete rule inet vctl prerouting handle "$_h"
	[ -n "$_ed" ] && echo "$_ed" > /proc/sys/net/ipv4/tcp_early_demux 2>/dev/null
	case "$_out" in
	*"got second"*) record pw_direct_sticky_control FAIL "tested per packet the connection still lived ($_out): the sticky checks cannot tell" ;;
	*) record pw_direct_sticky_control PASS "control: with the set tested per packet the same connection changed hands and broke ($_out)" ;;
	esac
}

# What comes in from outside, and the LAN's DNS aimed past the router. A port
# forward from the WAN to a server on the LAN: its answers are the reply
# direction of a connection the WAN opened, and the server sees the WAN
# client's own address (no masquerade), so a public one — PW_WAN_CLIENT, on no
# bypass list. The LAN's DNS: a query from the client to the origin's resolver
# for origin.stand, which that resolver answers 44.44.44.44 and the router's
# pipeline (PassWall's routing, FakeDNS for StandProxy) with a 198.18 address.
PW_WAN_CLIENT=44.44.44.47
PW_FWD_TCP=8080
PW_FWD_UDP=8081
pw_lan_world() {
	ip -n "$INET_NS" addr add "$PW_WAN_CLIENT/32" dev lo 2>/dev/null
	ip route replace "$PW_WAN_CLIENT/32" via "$INET_PEER_IP" dev vinet
	in_client socat "TCP-LISTEN:$PW_FWD_TCP,bind=$CLIENT_IP,fork,reuseaddr" 'SYSTEM:echo "lan server saw $SOCAT_PEERADDR"' >/dev/null 2>&1 &
	in_client socat "UDP-RECVFROM:$PW_FWD_UDP,bind=$CLIENT_IP,fork" 'SYSTEM:echo "lan server saw $SOCAT_PEERADDR"' >/dev/null 2>&1 &
	nft -f - <<EOF2 || die "the port forward's table"
table ip standfwd {
  chain pre {
    type nat hook prerouting priority dstnat; policy accept;
    ip daddr $ROUTER_INET_IP tcp dport $PW_FWD_TCP dnat to $CLIENT_IP:$PW_FWD_TCP
    ip daddr $ROUTER_INET_IP udp dport $PW_FWD_UDP dnat to $CLIENT_IP:$PW_FWD_UDP
  }
}
EOF2
	# The guard drops every forwarded client packet; a port forward's are
	# forwarded by design.
	nft insert rule inet standguard forward ip daddr "$PW_WAN_CLIENT" accept
	nft insert rule inet standguard forward ip saddr "$PW_WAN_CLIENT" accept
	sleep 1
}
pw_fwd_tcp() { in_inet socat -T5 - "TCP4:$ROUTER_INET_IP:$PW_FWD_TCP,bind=$PW_WAN_CLIENT" 2>&1 < /dev/null | tr '\n' ' '; }
# One datagram each way: under load one may be lost, so a second try — a
# forward that is broken (the controls) fails both.
pw_fwd_udp() {
	for _try in 1 2; do
		_o=$(echo ping | in_inet socat -T4 - "UDP4:$ROUTER_INET_IP:$PW_FWD_UDP,bind=$PW_WAN_CLIENT" 2>&1 | tr '\n' ' ')
		case "$_o" in *"lan server saw"*) break ;; esac
	done
	echo "$_o"
}
# The first address a resolver gives for origin.stand, asked from the client.
pw_lan_asks() { in_client nslookup origin.stand "$1" 2>/dev/null | awk '/^Name:/{n=1} n && /^Address/ {print $NF; exit}'; }

pw_lan_checks() {
	pw_lan_world
	_want="lan server saw $PW_WAN_CLIENT"
	_t=$(pw_fwd_tcp)
	_u=$(pw_fwd_udp)
	case "$_t" in
	*"$_want"*) record pw_lan_port_forward_tcp PASS "a TCP port forward from the WAN reached the LAN server and its answer came back ($_t)" ;;
	*) record pw_lan_port_forward_tcp FAIL "the WAN client got '$_t'; tables: $(nft list tables 2>/dev/null | tr '\n' ' ')" ;;
	esac
	case "$_u" in
	*"$_want"*) record pw_lan_port_forward_udp PASS "a UDP port forward from the WAN reached the LAN server and its answer came back ($_u)" ;;
	*) record pw_lan_port_forward_udp FAIL "the WAN client got '$_u'" ;;
	esac
	# Control: without the reply-direction return in prerouting the same
	# forwards must break, or the two checks above prove nothing.
	_h=$(nft -t -a list chain inet vctl prerouting 2>/dev/null | grep 'ct direction reply return' | awk '{print $NF}' | head -n 1)
	[ -n "$_h" ] && nft delete rule inet vctl prerouting handle "$_h"
	_t=$(pw_fwd_tcp)
	_u=$(pw_fwd_udp)
	[ -n "$_h" ] && nft insert rule inet vctl prerouting ct direction reply return
	case "$_t$_u" in
	*"$_want"*) record pw_lan_port_forward_control FAIL "without the reply-direction return a forward still worked (tcp '$_t', udp '$_u'; rule handle '${_h:-none}'): the checks cannot tell" ;;
	*) record pw_lan_port_forward_control PASS "control: without the reply-direction return both forwards broke (tcp '$_t', udp '$_u')" ;;
	esac

	_h0=$(vctl_ctr vctl_dns_hijacked)
	_a=$(pw_lan_asks "$ORIGIN_DNS_IP")
	_h1=$(vctl_ctr vctl_dns_hijacked)
	_r=$(pw_lan_asks "$ROUTER_CLIENT_IP")
	case "$_a" in
	198.18.*)
		if [ "${_h1:-0}" -gt "${_h0:-0}" ] && [ -n "$_r" ]; then
			record pw_lan_dns_hijacked PASS "the client asked $ORIGIN_DNS_IP for origin.stand and the router answered ($_a, FakeDNS; vctl_dns_hijacked ${_h0}->${_h1}); asked directly the router still answers ($_r)"
		else
			record pw_lan_dns_hijacked FAIL "answer $_a, vctl_dns_hijacked ${_h0:-?}->${_h1:-?}, the router asked directly: '${_r}'"
		fi ;;
	*) record pw_lan_dns_hijacked FAIL "the client asked $ORIGIN_DNS_IP and got '$_a', not the router's FakeDNS answer; vctl_dns_hijacked ${_h0:-?}->${_h1:-?}; $(nft -t list chain inet vctl dns_hijack 2>&1 | grep -c redirect) hijack rule(s)" ;;
	esac
	# Control: the hijack taken out whole — its chain emptied and the
	# prerouting returns that let the queries past TPROXY for it removed — the
	# same question is ordinary traffic again: carried by xray, the routing's
	# default direct, and answered by $ORIGIN_DNS_IP itself (on a real line, the
	# ISP's forgery).
	nft flush chain inet vctl dns_hijack 2>/dev/null
	for _h in $(nft -t -a list chain inet vctl prerouting 2>/dev/null | grep 'th dport 53 ip6\{0,1\} saddr @bypass' | awk '{print $NF}'); do
		nft delete rule inet vctl prerouting handle "$_h"
	done
	_a=$(pw_lan_asks "$ORIGIN_DNS_IP")
	case "$_a" in
	"$ORIGIN_IP") record pw_lan_dns_hijack_control PASS "control: without the hijack the same question was answered by $ORIGIN_DNS_IP itself ($_a)" ;;
	*) record pw_lan_dns_hijack_control FAIL "without the hijack the client got '$_a', not $ORIGIN_DNS_IP's own $ORIGIN_IP: the check cannot tell the two apart" ;;
	esac
}

step13_passwall_routing() {
	pw_phase passwall-routing
	# The nodes resolve names as a real exit does: FakeDNS hands them the
	# domain, not an address.
	mkdir -p "/etc/netns/$INET_NS" "/etc/netns/$CLIENT_NS"
	echo "nameserver $ORIGIN_DNS_IP" > "/etc/netns/$INET_NS/resolv.conf"
	echo "nameserver $ROUTER_CLIENT_IP" > "/etc/netns/$CLIENT_NS/resolv.conf"
	kill $(pgrep -f "xray run -c $STAND/passwall-real/nodes-server.json") 2>/dev/null
	sleep 1
	in_inet /usr/bin/xray run -c "$STAND/passwall-real/nodes-server.json" >> "$PW_WORLD/nodes.log" 2>&1 &
	pw_wait 10 sh -c "ip netns exec $INET_NS netstat -lnt 2>/dev/null | grep -q ':2005 '" || sleep 3
	# The provider's documents again (step 12 left them unapplied), for the
	# way back at the end.
	pw_vctl_seed
	pw_direct_world
	uci batch <<EOF
set passwall2.standdirect=shunt_rules
set passwall2.standdirect.remarks='StandDirect'
set passwall2.standdirect.network='tcp,udp'
set passwall2.standdirect.ip_list='$PW_DIRECT_IP/32'
set passwall2.standproxy=shunt_rules
set passwall2.standproxy.remarks='StandProxy'
set passwall2.standproxy.network='tcp,udp'
set passwall2.standproxy.domain_list='origin.stand'
set passwall2.standshunt=nodes
set passwall2.standshunt.remarks='stand-shunt'
set passwall2.standshunt.type='Xray'
set passwall2.standshunt.protocol='_shunt'
set passwall2.standshunt.default_node='_direct'
set passwall2.standshunt.fakedns='1'
set passwall2.standshunt.standdirect='_direct'
set passwall2.standshunt.standproxy='pwnode'
set passwall2.standshunt.standproxy_fakedns='1'
set passwall2.@global[0].node='standshunt'
EOF
	uci commit passwall2
	uci set "$PW_PKG.main.route_source=passwall"
	uci commit "$PW_PKG"
	pw_vectra pwr-on on --foreground
	_rc=$PW_VRC
	pw_wait 60 vctl_dataplane
	sleep 8
	_render=$(jsonfilter -i "$PW_AGENT_JSON" -e '@.xrayRenderPath' 2>/dev/null)
	_render=${_render:-/var/run/vectra-controller-pro/xray.json}
	if [ "$_rc" = 0 ] && vctl_dataplane && pw_down && grep -q '"fakedns"' "$_render" 2>/dev/null && grep -q 'StandProxy' "$_render" && ! grep -q '"mark":255' "$_render" && ! grep -q XRAY_LOCATION_ASSET "$_render"; then
		record pw_routing_render PASS "vectra on with route_source passwall: vctl carries (PassWall down), its render is PassWall's — the StandProxy rule, FakeDNS, vctl's marks; sniffing $(jsonfilter -i "$_render" -e '@.inbounds[0].sniffing.destOverride' 2>/dev/null | tr -d ' \n')"
	else
		record pw_routing_render FAIL "vectra exit $_rc; dataplane=$(vctl_dataplane && echo yes || echo no) passwall-down=$(pw_down && echo yes || echo no) render($_render): fakedns=$(grep -c '"fakedns"' "$_render" 2>/dev/null) StandProxy=$(grep -c StandProxy "$_render" 2>/dev/null); $(logread -e vctl 2>/dev/null | grep -iE 'passwall|error' | tail -2 | tr '\n' ' ' | cut -c1-240)"
	fi
	_fake=$(in_client nslookup origin.stand "$ROUTER_CLIENT_IP" 2>/dev/null | awk '/^Name:/{n=1} n && /^Address/ {print $NF; exit}')
	: > "$PW_NODES_LOG"
	_body=$(in_client curl -sS --max-time 10 "http://origin.stand/hello" 2>&1)
	sleep 1
	_via=$(pw_tags "origin.stand:80 ")
	case "$_fake" in
	198.18.*)
		if echo "$_body" | grep -q "$EXPECT_BODY" && [ "$_via" = pw ] && pw_down; then
			record pw_routing_listed_via_node PASS "vctl: origin.stand -> FakeDNS $_fake; the client's request carried by PassWall's node (via $_via) by its domain, PassWall's own stack down"
		else
			record pw_routing_listed_via_node FAIL "FakeDNS $_fake, but body='$(echo "$_body" | head -c 80)' via='$_via' passwall-down=$(pw_down && echo yes || echo no)"
		fi ;;
	*) record pw_routing_listed_via_node FAIL "origin.stand resolved to '$_fake', not a FakeDNS address" ;;
	esac
	: > "$PW_NODES_LOG"
	_body=$(in_client curl -sS --max-time 10 "http://$ORIGIN_IP/hello" 2>&1)
	sleep 1
	_via=$(pw_tags "$ORIGIN_IP:80 ")
	if echo "$_body" | grep -q "$EXPECT_BODY" && [ -z "$_via" ] && vctl_dataplane; then
		record pw_routing_default_direct PASS "vctl: a destination on no list goes straight out, as PassWall's default_node _direct says (no node saw it)"
	else
		record pw_routing_default_direct FAIL "body='$(echo "$_body" | head -c 80)' via='$_via'"
	fi
	pw_direct_checks
	pw_lan_checks
	uci set "$PW_PKG.main.route_source=provider"
	uci commit "$PW_PKG"
	uci set passwall2.@global[0].node='pwnode'
	uci commit passwall2
	/etc/init.d/"$PW_PKG" restart >/dev/null 2>&1
	if pw_wait_carrier vctl 90; then
		record pw_routing_back_to_provider PASS "route_source back to the provider's: vctl carries by the provider's routing again, its document untouched by the PassWall one ($PW_PROBE)"
	else
		record pw_routing_back_to_provider FAIL "$PW_PROBE; render: $(grep -c '"fakedns"' "$_render" 2>/dev/null) fakedns"
	fi
	pw_vectra pwr-off off --foreground
}

# ---------------------------------------------- native routing (step 14) ----
# route_source 'native': the same route policy, imported once into vctl's own
# store (/etc/config/vectra_route) and generated by vctl; the slot's
# subscription asked by vctl itself, on the policy's own schedule, with the
# router's signed agent; then PassWall2 taken off the router altogether, and
# vctl carrying by the policy through a restart and a power cycle. Last in
# every run: nothing after it has a PassWall to work with.
PW_NATIVE_SUB=sub_native
PW_NATIVE_STORE=/etc/config/vectra_route
PW_NATIVE_GEO=/usr/share/vectra-controller-pro/route-geo
PW_NATIVE_REFRESH=/etc/vectra-controller-pro/route-refresh.json
# pw_native_feed <inbound port> | stub: the subscription's one node, stand-exit
# (served at the origin's address: the stand's certificate names that one).
pw_native_feed() {
	if [ "$1" = stub ]; then
		_l='vless://00000000-0000-4000-8000-000000000000@0.0.0.0:1?type=tcp&security=none#App%20not%20supported'
	else
		_l="vless://$PW_UUID@$INET_PEER_IP:$1?encryption=none&type=tcp&security=none#stand-exit"
	fi
	_b64=$(printf '%s\n' "$_l" | base64 | tr -d '\n')
	mkdir -p "$PW_SUBSRV/native"
	printf 'HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\nContent-Length: %s\r\n\r\n%s' "${#_b64}" "$_b64" > "$PW_SUBSRV/native/sub"
}
# The native subscription last asked a day and more ago: due at the next loop.
pw_native_due() {
	mkdir -p "${PW_NATIVE_REFRESH%/*}"
	printf '{"%s":%s}' "$PW_NATIVE_SUB" "$(($(date +%s) - 90000))" > "$PW_NATIVE_REFRESH"
}
pw_native_slot() { uci -q get vectra_route.standshunt.standproxy; }
pw_native_port() { uci -q get "vectra_route.$(pw_native_slot).port"; }
pw_native_port_is() { [ "$(pw_native_port)" = "$1" ]; }
# The node that carried the client's GET http://origin.stand/hello ("" when
# it failed).
pw_native_via() {
	: > "$PW_NODES_LOG"
	_nb=$(in_client curl -sS --max-time 10 "http://origin.stand/hello" 2>&1)
	sleep 1
	echo "$_nb" | grep -q "$EXPECT_BODY" && pw_tags "origin.stand:80 "
}
pw_native_carries() { [ "$(pw_native_via)" = "$1" ]; } # <node tag>

step14_native_routing() {
	pw_phase native-routing
	uci -q batch <<EOF || die "the native step's policy"
set passwall2.nsub1=nodes
set passwall2.nsub1.remarks='stand-exit'
set passwall2.nsub1.type='Xray'
set passwall2.nsub1.protocol='vless'
set passwall2.nsub1.address='$INET_PEER_IP'
set passwall2.nsub1.port='2004'
set passwall2.nsub1.uuid='$PW_UUID'
set passwall2.nsub1.encryption='none'
set passwall2.nsub1.tls='0'
set passwall2.nsub1.transport='raw'
set passwall2.nsub1.tcp_guise='none'
set passwall2.nsub1.timeout='60'
set passwall2.nsub1.add_mode='2'
set passwall2.nsub1.group='stand-native'
set passwall2.standshunt.standproxy='nsub1'
set passwall2.@global[0].node='standshunt'
set passwall2.$PW_NATIVE_SUB=subscribe_list
set passwall2.$PW_NATIVE_SUB.remark='stand-native'
set passwall2.$PW_NATIVE_SUB.url='https://$ORIGIN_IP:$PW_SUB_PORT/native/sub'
set passwall2.$PW_NATIVE_SUB.hwid='1'
set passwall2.$PW_NATIVE_SUB.add_mode='2'
set passwall2.$PW_NATIVE_SUB.auto_update='1'
set passwall2.$PW_NATIVE_SUB.update_week_mode='7'
set passwall2.$PW_NATIVE_SUB.update_time_mode='0:00'
commit passwall2
EOF
	pw_native_feed 2004
	rm -rf "$PW_NATIVE_STORE" "$PW_NATIVE_GEO" "$PW_NATIVE_REFRESH"
	pw_vctl_seed
	uci set "$PW_PKG.main.route_source=native"
	uci commit "$PW_PKG"
	pw_vectra nat-on on --foreground
	_rc=$PW_VRC
	pw_wait 60 vctl_dataplane
	sleep 8

	# The import: PassWall2's configuration as it is, and its geo files.
	_pwgeo=$(uci -q get passwall2.@global_rules[0].v2ray_location_asset)
	_pwgeo=${_pwgeo:-/usr/share/v2ray/}
	if [ -f "$PW_NATIVE_STORE" ] && cmp -s "$PW_NATIVE_STORE" /etc/config/passwall2 && [ "$(ls -l "$PW_NATIVE_STORE" | cut -c1-10)" = "-rw-------" ] \
		&& cmp -s "$PW_NATIVE_GEO/geoip.dat" "${_pwgeo%/}/geoip.dat" && cmp -s "$PW_NATIVE_GEO/geosite.dat" "${_pwgeo%/}/geosite.dat"; then
		record pw_native_imported PASS "vectra on with route_source native: PassWall2's configuration imported as it is into $PW_NATIVE_STORE (0600), its geo files into $PW_NATIVE_GEO"
	else
		record pw_native_imported FAIL "store: $(ls -l "$PW_NATIVE_STORE" 2>&1 | cut -c1-80); the same as PassWall's: $(cmp -s "$PW_NATIVE_STORE" /etc/config/passwall2 && echo yes || echo no); geo: $(ls "$PW_NATIVE_GEO" 2>&1 | tr '\n' ' '), the same as ${_pwgeo%/}'s: $(cmp -s "$PW_NATIVE_GEO/geoip.dat" "${_pwgeo%/}/geoip.dat" && cmp -s "$PW_NATIVE_GEO/geosite.dat" "${_pwgeo%/}/geosite.dat" && echo yes || echo no)"
	fi

	# vctl's generator, PassWall's stack down, the slot's node carrying.
	_render=$(jsonfilter -i "$PW_AGENT_JSON" -e '@.xrayRenderPath' 2>/dev/null)
	_render=${_render:-/var/run/vectra-controller-pro/xray.json}
	_fake=$(in_client nslookup origin.stand "$ROUTER_CLIENT_IP" 2>/dev/null | awk '/^Name:/{n=1} n && /^Address/ {print $NF; exit}')
	_via=$(pw_native_via)
	case "$_fake" in 198.18.*) _fk=yes ;; *) _fk=no ;; esac
	# The render names no geo directory of its own: PassWall's generator
	# writes one into the config's env, and a recent xray (26.7.28; not
	# this stand's 26.3.27) applies it over the one vctl pins — PassWall's, gone
	# with its packages (the test router, 2026-09-29).
	if [ "$_rc" = 0 ] && pw_down && [ "$_fk" = yes ] && [ "$_via" = c ] && grep -q StandProxy "$_render" \
		&& ! grep -q XRAY_LOCATION_ASSET "$_render" && logread 2>/dev/null | grep -q "vctl's generator"; then
		record pw_native_carries PASS "vctl's own generator: origin.stand -> FakeDNS $_fake, carried by the slot's subscription node (c-in, :2004); PassWall's stack down"
	else
		record pw_native_carries FAIL "vectra exit $_rc; passwall-down=$(pw_down && echo yes || echo no); FakeDNS '$_fake'; via '$_via'; StandProxy in the render: $(grep -c StandProxy "$_render" 2>/dev/null); a geo directory named in it: $(grep -c XRAY_LOCATION_ASSET "$_render" 2>/dev/null); $(logread 2>/dev/null | grep -iE 'route policy|generator|import' | tail -3 | tr '\n' ' ' | cut -c1-300)"
	fi

	# The two generators on this router's policy, while PassWall is here.
	_cmp=$(vctl passwall-render -compare -config "$PW_AGENT_JSON" 2>&1)
	_crc=$?
	if [ "$_crc" = 0 ] && echo "$_cmp" | grep -q '^identical'; then
		record pw_native_compare PASS "$(echo "$_cmp" | head -n 1 | cut -c1-200)"
	else
		record pw_native_compare FAIL "exit $_crc: $(echo "$_cmp" | tr '\n' ' ' | cut -c1-400)"
	fi

	# The provider moves the exit to another inbound (the host switched off
	# on its panel, the same name elsewhere): the scheduled refresh, and the
	# slot follows it by name.
	pw_native_feed 2002
	pw_native_due
	pw_wait 120 pw_native_port_is 2002
	pw_wait 60 pw_native_carries b
	_node=$(pw_native_slot)
	_via=$(pw_native_via)
	if [ "$(pw_native_port)" = 2002 ] && [ "$_node" != nsub1 ] && [ "$_via" = b ]; then
		record pw_native_refresh_follows PASS "the policy's schedule asked the subscription (the router's own agent); the exit moved to :2002, the slot followed it by name (node $_node) and b-in carries"
	else
		record pw_native_refresh_follows FAIL "slot -> ${_node:-?} port $(pw_native_port); via '$_via'; $(logread 2>/dev/null | grep -iE 'subscription|refresh|slot' | tail -3 | tr '\n' ' ' | cut -c1-300)"
	fi

	# PassWall2 off the router: its packages removed; vctl restarted.
	opkg remove luci-app-passwall2 > "$PW_WORLD/opkg-remove.log" 2>&1
	opkg remove chinadns-ng geoview tcping v2ray-geoip v2ray-geosite >> "$PW_WORLD/opkg-remove.log" 2>&1
	/etc/init.d/"$PW_PKG" restart >/dev/null 2>&1
	pw_wait 60 vctl_dataplane
	pw_wait 60 pw_native_carries b
	_via=$(pw_native_via)
	if [ ! -e /usr/lib/lua/luci/passwall2/util_xray.lua ] && [ ! -e /etc/init.d/passwall2 ] && [ ! -e /usr/share/v2ray/geoip.dat ] && [ "$_via" = b ]; then
		record pw_native_without_passwall PASS "PassWall2 removed (its generator, init script and geo packages gone); vctl restarted carries by the policy (b-in)"
	else
		record pw_native_without_passwall FAIL "generator $([ -e /usr/lib/lua/luci/passwall2/util_xray.lua ] && echo present || echo gone), init $([ -e /etc/init.d/passwall2 ] && echo present || echo gone), v2ray geo $([ -e /usr/share/v2ray/geoip.dat ] && echo present || echo gone); via '$_via'; $(tail -n 3 "$PW_WORLD/opkg-remove.log" | tr '\n' ' ' | cut -c1-200)"
	fi

	# A power cycle: vctl comes back at boot and carries by the policy.
	pw_power_cycle
	pw_boot
	pw_wait 90 vctl_dataplane
	pw_wait 60 pw_native_carries b
	_via=$(pw_native_via)
	if vctl_dataplane && [ "$_via" = b ]; then
		record pw_native_after_reboot PASS "after a power cycle vctl carries by the native policy (b-in), no PassWall on the router"
	else
		record pw_native_after_reboot FAIL "dataplane=$(vctl_dataplane && echo yes || echo no) via '$_via'; $(logread 2>/dev/null | grep -iE 'route policy|generator|error' | tail -3 | tr '\n' ' ' | cut -c1-300)"
	fi

	# The provider's placeholder at the next refresh: refused, nothing moves.
	pw_native_feed stub
	cp "$PW_NATIVE_STORE" "$PW_WORLD/store-before-stub"
	pw_native_due
	pw_wait 90 sh -c "logread 2>/dev/null | grep -q 'did not refresh'"
	_via=$(pw_native_via)
	if cmp -s "$PW_NATIVE_STORE" "$PW_WORLD/store-before-stub" && [ "$_via" = b ] && logread 2>/dev/null | grep -q 'did not refresh'; then
		record pw_native_refuses_placeholder PASS "the provider's placeholder ('App not supported') refused: the store unchanged, b-in still carries"
	else
		record pw_native_refuses_placeholder FAIL "store $(cmp -s "$PW_NATIVE_STORE" "$PW_WORLD/store-before-stub" && echo unchanged || echo CHANGED); via '$_via'"
	fi
}

run_passwall_real_mode() {
	pw_sinkhole
	mkdir -p "$PW_WORLD"

	say "the real PassWall2 26.8.10 from the bundle; its LuCI and lua deps from OpenWrt's feeds"
	pw_install_bundle
	pw_isolate

	say "topology, the world (nodes, subscriptions, panel stub), the legacy agent and its watchdog"
	setup_kernel
	setup_topology
	setup_guard
	start_origins
	pw_world
	pw_legacy_agent
	pw_passwall_config
	pw_overlap_script

	say "the router boots: procd, uci-defaults, rc.d — PassWall2 and the agent from their own links"
	pw_boot
	pw_wait 60 pw_up || { pw_dump; die "PassWall2 never started at boot"; }
	pw_wait 20 agent_up || die "the legacy agent stub never started at boot"
	pw_start_sampler

	say "vctl the way the installer's --standby leaves it, and an operator config from 'vctl apply-local'"
	pw_vctl_stage
	pw_vctl_standby
	pw_vctl_seed

	# STAND_PW_HOLD=1: stop here, the world up and PassWall carrying, for
	# `docker exec` by hand; nothing below runs.
	if [ -n "${STAND_PW_HOLD:-}" ]; then
		echo "STAND-HOLD ready"
		while :; do sleep 3600; done
	fi

	say "1. baseline: PassWall2 carries the LAN — and the controls for what follows"
	pw_phase baseline
	assert_pw_baseline
	assert_pw_ctl_watchdog
	assert_pw_ctl_restart
	assert_pw_ctl_subscribe
	assert_pw_ctl_hotplug

	# STAND_PW_ONLY=routing: the setup and the baseline, then straight to the
	# PassWall-compatible routing step (the steps between do not touch it).
	if [ "${STAND_PW_ONLY:-}" = routing ]; then
		say "15. PassWall-compatible routing: vctl renders from PassWall2's own configuration"
		step13_passwall_routing
		say "16. native routing: vctl's own route policy, generator and subscription; PassWall2 removed"
		step14_native_routing
		pw_phase end
		[ "$FAILURES" -gt 0 ] && { say "diagnostics for the failures above"; pw_dump; }
		return
	fi

	say "2. vectra on --trial --minutes 10 --foreground"
	step2_trial

	say "3. midnight: 'passwall2 restart', then the real subscribe.lua cron run"
	step3_midnight

	if [ "$MODE" = passwall-real-no-switch ]; then
		say "control: the defect is what step 3 had to see; give the router back and stop here"
		pw_vectra ctl-off off --foreground
		pw_after_handback ctl-off
		pw_memory_report
		[ "$FAILURES" -gt 0 ] && pw_dump
		return
	fi

	say "4. the WAN's ifup: PassWall's iface hotplug, as shipped and armed"
	step4_ifup

	say "5. the legacy agent's cron watchdog, by hand"
	step5_watchdog

	say "6. a reboot ends the trial"
	step6_reboot

	say "7. a new trial, then vectra off — twice"
	step7_off

	say "8. vectra on --trial --minutes 1: the deadman ends it"
	step8_deadman

	say "9. vectra keep, a reboot, midnight, vectra off"
	step9_keep

	say "10. a crash: vctl SIGKILLed, then unable to start — 0.6.0's cron dead-man"
	step10_crash

	say "11. a revert: the table deleted under a running vctl, with the panel answering and without"
	step11_revert

	say "12. the package installed again during a trial"
	step10_upgrade

	say "13. the takeover racing a PassWall restart in flight"
	step11_race

	say "14. documents on /etc, never applied: no data plane after a reboot"
	step12_unapplied

	say "the hand-backs, and the stand's isolation"
	assert_pw_one_xray
	assert_pw_init_lock
	assert_pw_isolated

	say "15. PassWall-compatible routing: vctl renders from PassWall2's own configuration"
	step13_passwall_routing

	say "16. native routing: vctl's own route policy, generator and subscription; PassWall2 removed"
	step14_native_routing

	pw_phase end
	pw_memory_report
	[ "$FAILURES" -gt 0 ] && { say "diagnostics for the failures above"; pw_dump; }
}
