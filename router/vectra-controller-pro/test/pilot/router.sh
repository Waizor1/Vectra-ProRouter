#!/bin/sh
# One boot of the pilot stand's router (test/pilot/run.sh): boot what a router
# boots, in procd's order, then the phase /out/phase names. `router.sh
# shutdown` is procd's shutdown order, run before the container is stopped.
# Every check prints `RESULT <name> PASS|FAIL <detail>` into /out/<phase>.log.
#
#   /out      this router's results, and what one phase leaves the next
#   /wanctl   the WAN side's control files and logs (test/pilot/wan): what the
#             router sent over its WAN is read there, never from the router
#
# SCENARIO, WAN_IP, ROUTER_IP come from run.sh; for the upgrades, OLD_FEED
# (the WAN side's directory of the older release) and OLD_VERSION.

PKG=vectra-controller-pro
WANCTL=/wanctl
# Vectra's production addresses (api, router: the control plane; api-app:
# Connect). Every packet the router sends there is dropped and counted.
PROD_IPS="72.56.14.52, 38.244.208.144"
PILOT_PASSWORD="pilot-stand-root-1"
# What the WAN side tries on the router's WAN address: SSH, LuCI/uhttpd,
# DNS, the support terminal's port and xray's transparent inbound.
WAN_PORTS="tcp:22,tcp:80,tcp:443,tcp:53,udp:53,tcp:7681,tcp:12345"
FAILS=0

trap 'exit 0' TERM

record() { # <name> PASS|FAIL <detail>
	echo "RESULT $1 $2 $3"
	[ "$2" = PASS ] || FAILS=$((FAILS + 1))
}
# check <name> <detail> <command...>: the command runs in this shell, so it
# may be one of the functions below.
check() {
	n="$1"
	d="$2"
	shift 2
	if "$@" > /dev/null 2>&1; then record "$n" PASS "$d"; else record "$n" FAIL "$d"; fi
}
info() { echo "  .. $*"; }
die() {
	echo "STAND-FATAL $*"
	exit 1
}
wait_for() { # <seconds> <command...>
	w="$1"
	shift
	while [ "$w" -gt 0 ]; do
		"$@" > /dev/null 2>&1 && return 0
		sleep 1
		w=$((w - 1))
	done
	return 1
}
not() { ! "$@"; }
installed() { opkg list-installed "$1" 2> /dev/null | grep -q "^$1 - "; }
version_of() { opkg list-installed "$1" 2> /dev/null | sed -n "s/^$1 - //p"; }
running() { [ -x "/etc/init.d/$1" ] && "/etc/init.d/$1" running; }
vctl_pid() { pgrep -f '^/usr/sbin/vctl agent' | head -n 1; }
vctl_up() { [ -n "$(vctl_pid)" ]; }
file_sha() { sha256sum "$1" 2> /dev/null | cut -d' ' -f1; }
# vctl's companions are on the router and carry no Auto-Installed mark: opkg
# removes an auto-installed package as an orphan as soon as a vctl that does
# not depend on it replaces one that did (0.7.0-r29 as published).
companions_user() {
	for c in vectra-geodata vectra-reporter; do
		installed "$c" || return 1
		awk -v c="$c" '/^Package: / { p = substr($0, 10) } /^$/ { p = "" } p == c && $0 == "Auto-Installed: yes" { m = 1 } END { exit m }' /usr/lib/opkg/status || return 1
	done
}
# The companions marked as an opkg leaves what it installed as a vctl's
# dependency — as an older vctl's self-update (r29's keeps nothing) leaves
# them. No opkg runs meanwhile.
mark_companions() {
	awk '/^Package: / { p = substr($0, 10) } /^$/ { p = "" }
		c = (p == "vectra-geodata" || p == "vectra-reporter") {}
		c && $0 == "Auto-Installed: yes" { next }
		{ print }
		c && /^Status: / { print "Auto-Installed: yes" }' /usr/lib/opkg/status > /tmp/status.marked &&
		cat /tmp/status.marked > /usr/lib/opkg/status && rm -f /tmp/status.marked
}

# ------------------------------------------------------ the WAN's records ----

# What this router sent to the WAN side: requests.jsonl lines from its address,
# from line <mark> on (0: all).
from_me() { tail -n "+$((${1:-0} + 1))" "$WANCTL/requests.jsonl" 2> /dev/null | grep -F "\"from\":\"$ROUTER_IP:"; }
reqs() { from_me "${2:-0}" | grep -F "\"path\":\"$1\""; }
count() { reqs "$1" | grep -c . || true; }
at_least() { [ "$(count "$1")" -ge "$2" ]; }
reports() { count /errors/router; }
more_reports_than() { [ "$(reports)" -gt "$1" ]; }
mark() { wc -l < "$WANCTL/requests.jsonl" 2> /dev/null || echo 0; }
# A field of a request line, by jsonfilter.
field() { echo "$1" | jsonfilter -e "@.$2" 2> /dev/null; }
last() { reqs "$1" "${2:-0}" | tail -n 1; }
# The sink's verdict on each report from this router: "OK VCTL_DOWN 1 …".
verdicts() { reqs /errors/router "${1:-0}" | while read -r l; do field "$l" sink; done; }
verdict_seen() { verdicts "${2:-0}" | grep -q "^$1"; }
job_result() { reqs /api/router/job-result | grep -F "\"jobId\":\"$1\"" | tail -n 1; }
# A job's final word (the router answers "accepted" first).
has_job_result() { case "$(field "$(job_result "$1")" jobStatus)" in failure | success) return 0 ;; esac; return 1; }
queue_job() { # <id> <command>
	mkdir -p "$WANCTL/jobs/$ROUTER_IP"
	printf '{"id":"%s","type":"run_terminal_command","payload":{"command":"%s","timeoutSeconds":20}}\n' "$1" "$2" > "$WANCTL/jobs/$ROUTER_IP/$1.json.tmp"
	mv "$WANCTL/jobs/$ROUTER_IP/$1.json.tmp" "$WANCTL/jobs/$ROUTER_IP/$1.json"
}
# What the WAN side serves this router: the feed (published, old, …), Connect up or down.
serve_feed() { echo "$1" > "$WANCTL/feed-$ROUTER_IP"; }
connect_is() { echo "$1" > "$WANCTL/sink-$ROUTER_IP"; }
checked_in_since() { [ -n "$(last /api/router/check-in "$1")" ]; }
checkin_says() { # <field> <value>: the last check-in says so
	[ "$(field "$(last /api/router/check-in)" "$1")" = "$2" ]
}

# ------------------------------------------------------------ the guard ----

# Before anything can talk: Vectra's names resolve to the stand. dnsmasq's
# upstream is the stand's resolver (what netifd writes from the WAN lease);
# vctl and the reporter ask 8.8.8.8 and 77.88.8.8 themselves, over TCP — the
# nat hook sends those to the stand. Anything addressed to production is
# dropped and counted: a fallback to a built-in address (KnownAddrs) would
# show up here.
guard() {
	nft -f - <<- EOF || die "the guard's nftables did not load"
		table inet pilot_guard {
			counter prod { }
			chain out { type filter hook output priority -400; policy accept;
				ip daddr { $PROD_IPS } counter name prod drop
			}
			chain through { type filter hook forward priority -400; policy accept;
				ip daddr { $PROD_IPS } counter name prod drop
			}
		}
		table ip pilot_dns {
			counter steered { }
			chain out { type nat hook output priority -110; policy accept;
				ip daddr { 8.8.8.8, 77.88.8.8 } meta l4proto { tcp, udp } th dport 53 counter name steered dnat to $WAN_IP:53
			}
		}
	EOF
}
prod_packets() { nft list counter inet pilot_guard prod 2> /dev/null | sed -n 's/.*packets \([0-9]*\).*/\1/p'; }
steered_packets() { nft list counter ip pilot_dns steered 2> /dev/null | sed -n 's/.*packets \([0-9]*\).*/\1/p'; }
# Reports from this router since the end of the last phase: none while the
# owner's switch is off, the boot and the shutdown between included.
quiet_since_last_phase() { [ "$(reports)" = "$(cat /out/reports.count 2> /dev/null || echo 0)" ]; }
check_quiet() { # <prefix>
	check "$1_quiet_across_boot" "no report since the last phase ended, its shutdown and this boot included ($(cat /out/reports.count 2> /dev/null || echo 0) before, $(reports) now)" quiet_since_last_phase
}
check_guard() { # <prefix>
	check "$1_no_production" "not one packet to Vectra's production addresses (dropped: $(prod_packets); lookups steered to the stand: $(steered_packets))" \
		test "$(prod_packets)" = 0
}

# ------------------------------------------------------------------ boot ----

# The first boot of a router out of the box: what its image would have.
first_boot() {
	# An AX3000T: opkg takes aarch64_cortex-a53 packages (Vectra's) next to
	# this userland's own aarch64_generic ones; the release says so too.
	cat >> /etc/opkg.conf <<- EOF
		arch all 1
		arch noarch 1
		arch aarch64_generic 10
		arch aarch64_cortex-a53 20
	EOF
	sed -i -e "s/^DISTRIB_ARCH=.*/DISTRIB_ARCH='aarch64_cortex-a53'/" -e "s#^DISTRIB_TARGET=.*#DISTRIB_TARGET='mediatek/filogic'#" /etc/openwrt_release
	# fw4's wan zone on the uplink: netifd does not run here, so by device.
	uci -q add_list firewall.@zone[1].device=eth0
	uci commit firewall
	# The stand's CA, as if it were one the bundle already had.
	cat "$WANCTL/ca.pem" >> /etc/ssl/certs/ca-certificates.crt
	# A cron line of the owner's own: proof that crond runs the minutes.
	mkdir -p /etc/crontabs
	echo '* * * * * date +%s >> /out/cron.ticks' >> /etc/crontabs/root
	touch /etc/pilot-first-boot-done
}

boot() {
	[ -f /etc/pilot-first-boot-done ] || first_boot
	# What preinit leaves in /tmp on the router (board detection).
	mkdir -p /tmp/sysinfo /var/lock /var/run /tmp/resolv.conf.d
	echo "Xiaomi Mi Router AX3000T" > /tmp/sysinfo/model
	echo "xiaomi,mi-router-ax3000t" > /tmp/sysinfo/board_name
	# The WAN lease: the ISP's resolver is the stand's. The router itself asks
	# its dnsmasq, as on OpenWrt.
	echo "nameserver $WAN_IP" > /tmp/resolv.conf.d/resolv.conf.auto
	echo "nameserver 127.0.0.1" > /etc/resolv.conf
	guard
	ubusd &
	sleep 1
	procd &
	wait_for 15 sh -c 'ubus list | grep -q "^service$"' || die "procd/ubusd never came up"
	# procd's rcS S boot: each start script in order. netifd stays out (docker
	# owns eth0), and what needs it with it.
	for s in /etc/rc.d/S*; do
		case "${s##*/}" in S20network | S25packet_steering | S35odhcpd) continue ;; esac
		"$s" boot > /dev/null 2>&1
	done
	wait_for 20 sh -c 'nslookup localhost 127.0.0.1 | grep -q "^Name:"' || info "dnsmasq does not answer yet"
	info "booted: $(sed -n "s/^DISTRIB_DESCRIPTION='\(.*\)'/\1/p" /etc/openwrt_release), $(cd /etc/rc.d && echo S*)"
}

# procd's rcS K shutdown: each stop script in order.
shutdown_router() {
	for s in /etc/rc.d/K*; do
		"$s" shutdown > /dev/null 2>&1
	done
	sync
	info "shutdown: $(cd /etc/rc.d && echo K*)"
	check_guard shutdown
}

# ---------------------------------------------------- the pilot's switches ----

shell_off() { [ "$(uci -q get $PKG.main.remote_shell)" = 0 ]; }
# 0.7.0 brought the vault (state.json sealed) and the owner's support-shell
# switch: 0.6.0-r36 has neither.
since_07() { case "$(version_of $PKG)" in 0.6.*) return 1 ;; esac; }
reporter_off() { [ "$(uci -q get vectra-reporter.main.enabled)" = 0 ]; }
sealed() { head -c 10 /etc/vectra-controller-pro/state.json 2> /dev/null | grep -q '^VCTLVAULT1'; }
cron_runs() { pidof crond > /dev/null && grep -qF '/usr/sbin/vectra-reporter run' /etc/crontabs/root; }
ticks() { wc -l < /out/cron.ticks 2> /dev/null || echo 0; }
spool_has() { /usr/sbin/vectra-reporter status 2> /dev/null | grep -q "$1"; }
incident_waits() { grep -rqs "$1" /var/run/vectra-reporter/inbox; }
# What the reporter keeps on flash: its files, by name and content.
reporter_files() { (cd /etc/vectra-reporter 2> /dev/null && find . -type f | sort | while read -r f; do echo "$f $(file_sha "$f" | cut -c1-12)"; done); }

# off_window <prefix> <what>: the router makes something to say (VCTL_DOWN,
# from the dead-man), then two of cron's minutes and more go by; the reporter
# sends nothing in them, while cron runs (the owner's line ticks).
off_window() {
	check "$1_deadman" "vctl stopped while switched on: the dead-man (cron) started it again" vctl_down_once
	check "$1_incident_waits" "the dead-man's VCTL_DOWN waits in the reporter's inbox" incident_waits VCTL_DOWN
	r0="$(reports)"
	t0="$(ticks)"
	sleep 135
	t1="$(ticks)"
	r1="$(reports)"
	check "$1_cron_ticked" "crond ran its minutes meanwhile: $((t1 - t0)) ticks" test "$((t1 - t0))" -ge 2
	check "$1_nothing_reported" "$2: no report reached Connect ($((r1 - r0)) sent in $((t1 - t0)) minutes)" test "$r1" = "$r0"
}

# The support shell asked for over the panel: refused, with the router's own words.
refusal_said() {
	l="$(job_result "$1")"
	[ "$(field "$l" jobStatus)" = failure ] && echo "$l" | grep -qF 'support shell access is off'
}
shell_refused() { # <prefix> <job id>
	queue_job "$2" id
	if wait_for 90 has_job_result "$2"; then
		check "$1_shell_refused" "run_terminal_command refused: failure, «support shell access is off»" refusal_said "$2"
		refusal_said "$2" || info "the answer: $(job_result "$2" | cut -c1-400)"
	else
		record "$1_shell_refused" FAIL "no job-result for $2 in 90 s"
	fi
}

# The identity the panel knows this router by: kept, and no second registration.
LINE=
line_is() { [ "$(field "$LINE" "$1")" = "$2" ]; }
identity_kept() { # <prefix>
	set -- "$1" "$(cut -d' ' -f1 /out/identity)" "$(cut -d' ' -f2 /out/identity)" "$(mark)"
	wait_for 90 checked_in_since "$4"
	LINE="$(last /api/router/check-in "$4")"
	check "$1_checks_in" "checks in over the WAN as $(field "$LINE" routerId), its token accepted" line_is tokenOK true
	check "$1_same_identity" "the same identity: $2 (device key $3)" sh -c "[ '$(field "$LINE" deviceIdentifier)' = '$2' ] && [ '$(field "$LINE" devicePublicKeySha)' = '$3' ]"
	check "$1_no_new_register" "no second registration (registrations: $(count /api/router/register))" test "$(count /api/router/register)" = 1
	if since_07; then
		check "$1_state_sealed" "state.json sealed in the vault (VCTLVAULT1)" sealed
	else
		check "$1_state_plain" "$(version_of $PKG) has no vault: state.json still plaintext" not sealed
	fi
}
save_identity() {
	LINE="$(last /api/router/register)"
	echo "$(field "$LINE" deviceIdentifier) $(field "$LINE" devicePublicKeySha)" > /out/identity
	info "identity: $(cat /out/identity), router id $(field "$LINE" routerId)"
}

# probe_count <file> <open|closed>: how many ports the probe found so.
probe_count() { jsonfilter -i "$1" -e '@.*' 2> /dev/null | grep -c "^$2\$" || true; }
# The router's WAN address, tried from the WAN side.
wan_closed() { # <prefix>
	if wget -q -O /out/wan-probe-"$PHASE".json "https://api.vectra-pro.net/stand/probe?ports=$WAN_PORTS"; then
		open="$(probe_count /out/wan-probe-"$PHASE".json open)"
		closed="$(probe_count /out/wan-probe-"$PHASE".json closed)"
		check "$1_wan_closed" "nothing answers on the WAN address ($(tr -d '\n' < /out/wan-probe-"$PHASE".json))" \
			test "$open:$closed" = "0:$(echo "$WAN_PORTS" | tr ',' '\n' | grep -c .)"
	else
		record "$1_wan_closed" FAIL "the WAN side's probe did not answer"
	fi
}

# The published installer, as the pilot's guide says: wget, then sh.
fetch_installer() {
	rm -f /tmp/vectra
	wget -q -O /tmp/vectra https://router.vectra-pro.net/install
}
installer() { # <args...> -> INSTALL_RC, output in /out/installer-<phase>.out
	sh /tmp/vectra "$@" < /dev/null > "/out/installer-$PHASE.out" 2>&1
	INSTALL_RC=$?
	sed 's/^/     | /' "/out/installer-$PHASE.out"
}

# Something for the reporter to say, made by the router itself: vctl stopped
# while it is switched on; the dead-man (cron, every minute) finds it down,
# records VCTL_DOWN and starts it again. (vctl 0.7 writes no crash output on
# purpose — cmd/vctl/crash_go123.go — so a crash is no report any more.)
vctl_down_once() {
	/etc/init.d/$PKG stop > /dev/null 2>&1
	wait_for 150 vctl_up
}

# ---------------------------------------------------------------- phases ----

# pilot: a new router, the pilot's steps one by one.
phase_install() {
	check install_no_vectra "a router out of the box: no Vectra package" not installed $PKG
	# 1. The reporter off before it is installed: the pilot's own file.
	printf "config reporter 'main'\n\toption enabled '0'\n" > /etc/config/vectra-reporter
	file_sha /etc/config/vectra-reporter > /out/reporter-config.sha256
	# 2. The installer, with --standby, for a BloopCat router.
	check install_fetched "wget of the published installer" fetch_installer
	info "installer $(file_sha /tmp/vectra | cut -c1-16)"
	installer --standby --brand bloopcat
	check install_exit "--standby --brand bloopcat: exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check install_version "$PKG $(version_of $PKG), the version the published feed names" test "$(version_of $PKG)" = "$(cat /out/expected.version)"
	check install_vctl_bytes "/usr/sbin/vctl is the published package's ($(file_sha /usr/sbin/vctl | cut -c1-16))" test "$(file_sha /usr/sbin/vctl)" = "$(cat /out/expected.vctl.sha256)"
	check install_reporter_bytes "vectra-reporter $(version_of vectra-reporter) is the published package's" test "$(file_sha /usr/sbin/vectra-reporter)" = "$(cat /out/expected.reporter.sha256)"
	check install_feed_key "the feed line and the installer's key $(cat /out/expected.keyid)" \
		sh -c "grep -q '^src/gz vectra_pro https://api.vectra-pro.net/artifacts/openwrt/pro-canary' /etc/opkg/customfeeds.conf && [ -s /out/expected.keyid ] && [ -f /etc/opkg/keys/\$(cat /out/expected.keyid) ]"
	check install_companions_user "vectra-geodata and vectra-reporter installed, neither Auto-Installed" companions_user
	check install_reporter_kept_off "the owner's /etc/config/vectra-reporter kept as written (enabled '0')" test "$(file_sha /etc/config/vectra-reporter)" = "$(cat /out/reporter-config.sha256)"
	check install_reporter_package_copy "the package's own copy beside it, as opkg does with a conffile (vectra-reporter-opkg)" test -f /etc/config/vectra-reporter-opkg
	check install_cron "cron runs the reporter every minute: the line in root's crontab, crond running" cron_runs
	check install_standby_off "Vectra left off: not running, not enabled, UCI enabled=0" \
		sh -c "! /etc/init.d/$PKG running && ! /etc/init.d/$PKG enabled && [ \"\$(uci -q get $PKG.main.enabled)\" = 0 ]"
	check install_brand "brand bloopcat in UCI" test "$(uci -q get $PKG.main.brand)" = bloopcat
	check install_shell_default "a new router: the support shell is on until the owner turns it off (remote_shell '$(uci -q get $PKG.main.remote_shell)')" \
		test "$(uci -q get $PKG.main.remote_shell)" = 1
	check install_no_contact "nothing reached the panel or Connect yet (register $(count /api/router/register), check-in $(count /api/router/check-in), reports $(reports))" \
		test "$(count /api/router/register)$(count /api/router/check-in)$(reports)" = 000
	wan_closed install
	# 3. passwd.
	printf '%s\n%s\n' "$PILOT_PASSWORD" "$PILOT_PASSWORD" | passwd root > /dev/null 2>&1
	check install_passwd "root has a password now" sh -c "grep '^root:' /etc/shadow | cut -d: -f2 | grep -q '^[\$]'"
	# 4. The support shell off.
	uci set $PKG.main.remote_shell=0 && uci commit $PKG
	# 5. Vectra on.
	vectra on --force > /out/vectra-on.out 2>&1
	rc=$?
	sed 's/^/     | /' /out/vectra-on.out
	check install_on "vectra on --force: exit 0, got $rc" test "$rc" = 0
	check install_running "procd runs vctl, and the service is enabled for the next boot" \
		wait_for 30 sh -c "/etc/init.d/$PKG running && /etc/init.d/$PKG enabled"
	check install_registered "registered over the WAN with the stub panel" wait_for 90 at_least /api/router/register 1
	save_identity
	wait_for 60 at_least /api/router/check-in 2
	check install_checkin_shell_off "check-in tells the panel the support shell is off (remoteShell $(field "$(last /api/router/check-in)" remoteShell))" \
		checkin_says remoteShell false
	check install_checkin_password "check-in tells the panel a router password is set (routerPasswordSet $(field "$(last /api/router/check-in)" connect_routerPasswordSet))" \
		checkin_says connect_routerPasswordSet true
	check install_state_sealed "the router's identity is sealed in the vault (state.json: VCTLVAULT1)" sealed
	shell_refused install pilot-shell-install
	wan_closed install_on
	# The reporter off with something to say.
	off_window install "the reporter off, VCTL_DOWN on the router"
	check_guard install
}

phase_after_reboot() { # pilot and upgrade: after a clean reboot
	check reboot_running "vctl back after the reboot (S95), $(version_of $PKG)" wait_for 60 running $PKG
	check reboot_shell_off "the support shell still off" shell_off
	check reboot_reporter_off "the reporter still off" reporter_off
	identity_kept reboot
	if since_07; then shell_refused reboot "$SCENARIO-shell-reboot"; else info "$(version_of $PKG) has no support-shell switch"; fi
	wan_closed reboot
	off_window reboot "the reporter off after a clean reboot"
	check_quiet reboot
	check_guard reboot
}

phase_after_powercut() { # pilot and upgrade: after the power went
	check powercut_running "vctl back after the power cut, $(version_of $PKG)" wait_for 60 running $PKG
	check powercut_shell_off "the support shell still off" shell_off
	check powercut_reporter_off "the reporter still off" reporter_off
	identity_kept powercut
	shell_refused powercut "$SCENARIO-shell-powercut"
	off_window powercut "the reporter off after a boot nobody shut down"
	check_quiet powercut
	check_guard powercut
}

# Each refusal above could have failed: the same router with the owner's
# switches on.
phase_controls() {
	check controls_running "vctl running" wait_for 60 running $PKG
	uci set $PKG.main.remote_shell=1 && uci commit $PKG
	queue_job "$SCENARIO-shell-on" "id -u"
	if wait_for 90 has_job_result "$SCENARIO-shell-on"; then
		LINE="$(job_result "$SCENARIO-shell-on")"
		check controls_shell_runs "with the switch on the same job runs: success ($(field "$LINE" result | cut -c1-120))" line_is jobStatus success
	else
		record controls_shell_runs FAIL "no job-result in 90 s"
	fi
	uci set $PKG.main.remote_shell=0 && uci commit $PKG
	m="$(mark)"
	r0="$(reports)"
	uci set vectra-reporter.main.enabled=1 && uci commit vectra-reporter
	vctl_down_once
	if wait_for 200 more_reports_than "$r0"; then
		wait_for 150 verdict_seen "OK VCTL_DOWN" "$m"
		check controls_reports_sent "switched on, cron sends what happened (no manual run): $(verdicts "$m" | tr '\n' ';')" verdict_seen OK "$m"
		check controls_reports_verified "every report verified by the sink: the body's hash, the token sealed to the panel's claimKey, signed by the registered device key" \
			not verdict_seen BAD "$m"
		check controls_vctl_down "the dead-man's VCTL_DOWN reported" verdict_seen "OK VCTL_DOWN" "$m"
		if [ "$SCENARIO" != pilot ]; then
			check controls_old_report "the report queued under $OLD_VERSION, held through the upgrade, delivered now (made before the upgrade at $(cat /out/upgrade-at))" \
				old_report_delivered "$m"
		fi
	else
		record controls_reports_sent FAIL "nothing reached the sink in 200 s after the switch went on (status: $(/usr/sbin/vectra-reporter status 2>&1 | tail -n 2 | tr '\n' ' '))"
	fi
	uci set vectra-reporter.main.enabled=0 && uci commit vectra-reporter
	# The WAN probe could have seen an open port: one accept rule at the top of
	# fw4's wan input, and dropbear and uhttpd answer on the WAN address. (Not
	# `firewall stop`: fw4 flush deletes every nftables table, vctl's and the
	# guard's too.)
	nft insert rule inet fw4 input_wan tcp dport '{ 22, 80 }' accept comment pilot-control
	if wget -q -O /out/wan-probe-open.json "https://api.vectra-pro.net/stand/probe?ports=tcp:22,tcp:80"; then
		check controls_probe_sees_open "with ssh and http let in on the WAN, the WAN side reaches them ($(tr -d '\n' < /out/wan-probe-open.json))" \
			test "$(probe_count /out/wan-probe-open.json open)" = 2
	else
		record controls_probe_sees_open FAIL "the WAN side's probe did not answer"
	fi
	h="$(nft -a list chain inet fw4 input_wan | sed -n 's/.*comment "pilot-control" # handle \([0-9]*\).*/\1/p')"
	[ -n "$h" ] && nft delete rule inet fw4 input_wan handle "$h"
	wan_closed controls_after
	check_guard controls
	# The guard could have counted one: a packet sent to the production panel's
	# address on purpose (port 9, discard) is dropped and counted.
	p0="$(prod_packets)"
	wget -q -T 3 -O /dev/null http://72.56.14.52:9/ > /dev/null 2>&1
	check controls_guard_counts "the guard drops and counts a packet sent to production on purpose ($p0 -> $(prod_packets))" \
		test "$(prod_packets)" -gt "$p0"
}

# upgrade, upgrade06: a router on an older release, set up the default way.
phase_old() {
	serve_feed "$OLD_FEED"
	check old_fetched "wget of $OLD_VERSION's installer" fetch_installer
	installer
	check old_exit "$OLD_VERSION's installer: exit 0 or 2 (warnings), got $INSTALL_RC" sh -c "[ $INSTALL_RC = 0 ] || [ $INSTALL_RC = 2 ]"
	check old_version "$PKG $(version_of $PKG)" test "$(version_of $PKG)" = "$OLD_VERSION"
	version_of vectra-reporter > /out/reporter-old.version
	check old_running "vctl runs" wait_for 30 running $PKG
	check old_registered "registered over the WAN" wait_for 90 at_least /api/router/register 1
	save_identity
	wait_for 60 at_least /api/router/check-in 2
	check old_reporter_on "vectra-reporter $(version_of vectra-reporter) on, as the package ships it (enabled '1')" test "$(uci -q get vectra-reporter.main.enabled)" = 1
	check old_cron "cron runs the reporter" cron_runs
	if since_07; then
		check old_shell_default "the support shell on, as on every router installed the default way" checkin_says remoteShell true
		check old_state_sealed "state.json sealed in the vault" sealed
	else
		# Before the vault: the state is plaintext, sealed by the upgrade.
		check old_state_plain "$OLD_VERSION keeps state.json in plaintext (no vault yet)" \
			sh -c "! head -c 10 /etc/vectra-controller-pro/state.json | grep -q '^VCTLVAULT1' && grep -q device_public_key /etc/vectra-controller-pro/state.json"
	fi
	# A report the router has not delivered yet: made while Connect is down.
	connect_is down
	m="$(mark)"
	/usr/sbin/vectra-reporter test > /out/reporter-test-old.out 2>&1
	check old_report_tried "a report sent while Connect is down: it answered 503 ($(tail -n 1 /out/reporter-test-old.out))" wait_for 60 verdict_seen "DOWN 503" "$m"
	/usr/sbin/vectra-reporter status > /out/reporter-status-old.out 2>&1
	check old_report_queued "the report waits in the spool ($(grep -c TEST /out/reporter-status-old.out) TEST)" spool_has TEST
	# The owner's switches, the pilot's way, before cron's next minute.
	uci set $PKG.main.remote_shell=0 && uci commit $PKG
	uci set vectra-reporter.main.enabled=0 && uci commit vectra-reporter
	connect_is accept
	file_sha /etc/config/vectra-reporter > /out/reporter-config.sha256
	off_window old "$OLD_VERSION, the reporter switched off with a report waiting, Connect up again"
	if since_07; then
		shell_refused old "$SCENARIO-shell-old"
	else
		# A fact about the old release, not about the pilot's: the switch is
		# set but nothing reads it yet.
		queue_job "$SCENARIO-shell-old" id
		if wait_for 90 has_job_result "$SCENARIO-shell-old"; then
			LINE="$(job_result "$SCENARIO-shell-old")"
			check old_shell_no_switch "$OLD_VERSION has no switch: with remote_shell 0 the job still runs ($(field "$LINE" jobStatus))" line_is jobStatus success
		else
			record old_shell_no_switch FAIL "no job-result in 90 s"
		fi
	fi
	check_guard old
}

phase_old_after_reboot() { phase_after_reboot; }

# The TEST report made before the upgrade, delivered.
old_report_delivered() {
	at="$(cat /out/upgrade-at)"
	verdicts "$1" | while read -r v; do
		case "$v" in "OK TEST "*) f="${v##*first=}"; [ "$f" -lt "$at" ] && echo yes ;; esac
	done | grep -q yes
}

phase_upgrade() {
	date +%s > /out/upgrade-at
	wait_for 60 vctl_up
	pid0="$(vctl_pid)"
	check upgrade_running_before "vctl runs before the upgrade (pid ${pid0:-none})" test -n "$pid0"
	serve_feed published
	check upgrade_fetched "wget of the published installer" fetch_installer
	reporter_files > /out/reporter-files-before-upgrade
	installer
	reporter_files > /out/reporter-files-after-upgrade
	cp /tmp/vectra-install.log /out/vectra-install-upgrade.log 2> /dev/null
	check upgrade_companions_user "vectra-geodata and vectra-reporter installed, neither Auto-Installed any more: opkg removes neither with a vctl that does not depend on them" companions_user
	check upgrade_reporter_state_kept "vectra-reporter's own state and spool (/etc/vectra-reporter) kept through the upgrade: before $(grep -c . /out/reporter-files-before-upgrade) files, after: $(tr '\n' ' ' < /out/reporter-files-after-upgrade)" \
		sh -c "[ -s /out/reporter-files-before-upgrade ] && cmp -s /out/reporter-files-before-upgrade /out/reporter-files-after-upgrade"
	grep -E 'orphaned|Removing package vectra' /out/vectra-install-upgrade.log 2> /dev/null | sed 's/^/  .. opkg: /' 
	check upgrade_exit "the published installer over $OLD_VERSION: exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check upgrade_version "$PKG $OLD_VERSION -> $(version_of $PKG)" test "$(version_of $PKG)" = "$(cat /out/expected.version)"
	check upgrade_vctl_bytes "/usr/sbin/vctl is the published package's" test "$(file_sha /usr/sbin/vctl)" = "$(cat /out/expected.vctl.sha256)"
	check upgrade_restarted "vctl restarted in place (pid $pid0 -> $(vctl_pid))" \
		wait_for 60 sh -c "/etc/init.d/$PKG running && [ -n \"\$(pgrep -f '^/usr/sbin/vctl agent')\" ] && [ \"\$(pgrep -f '^/usr/sbin/vctl agent' | head -n 1)\" != '$pid0' ]"
	if [ "$(cat /out/reporter-old.version)" != "$(version_of vectra-reporter)" ]; then
		check upgrade_reporter_upgraded "vectra-reporter $(cat /out/reporter-old.version) -> $(version_of vectra-reporter), the published binary" \
			test "$(file_sha /usr/sbin/vectra-reporter)" = "$(cat /out/expected.reporter.sha256)"
		check upgrade_reporter_package_copy "the new package's own config beside the owner's (vectra-reporter-opkg)" test -f /etc/config/vectra-reporter-opkg
	else
		info "vectra-reporter $(version_of vectra-reporter) as before: opkg does not reinstall the same version (binary $(file_sha /usr/sbin/vectra-reporter | cut -c1-16), published $(cut -c1-16 /out/expected.reporter.sha256))"
	fi
	check upgrade_shell_off "the support shell still off" shell_off
	check upgrade_reporter_off "the owner's reporter file untouched (enabled '0')" test "$(file_sha /etc/config/vectra-reporter)" = "$(cat /out/reporter-config.sha256)"
	wait_for 60 checkin_says controllerVersion "$(cat /out/expected.version)"
	check upgrade_checkin_version "check-in says $(field "$(last /api/router/check-in)" controllerVersion)" checkin_says controllerVersion "$(cat /out/expected.version)"
	identity_kept upgrade
	check upgrade_report_kept "the report queued under $OLD_VERSION still waits in the spool" spool_has TEST
	shell_refused upgrade upgrade-shell-upgrade
	wan_closed upgrade
	off_window upgrade "upgraded, the old identity's report waiting, the reporter off"
	check_quiet upgrade
	check_guard upgrade
	# A vctl an older self-update brought in finds the companions marked: it
	# drops the marks once it starts (keepCompanionsAtStart).
	mark_companions
	"/etc/init.d/$PKG" restart > /dev/null 2>&1
	check upgrade_start_drops_marks "the companions marked as an older updater's opkg leaves them, vctl started again: the marks gone" wait_for 90 companions_user
}

# ------------------------------------------------------------------ main ----

if [ "${1:-}" = shutdown ]; then
	shutdown_router
	exit 0
fi
PHASE="$(cat /out/phase)"
boot >> "/out/$PHASE.log" 2>&1
fn="phase_$(echo "$PHASE" | tr - _)"
{
	echo "## $SCENARIO / $PHASE ($(date -u +%FT%TZ))"
	"$fn"
	reports > /out/reports.count
	echo "## $PHASE: $FAILS failed"
} >> "/out/$PHASE.log" 2>&1
touch "/out/$PHASE.done"
while :; do
	sleep 3600 &
	wait $!
done
