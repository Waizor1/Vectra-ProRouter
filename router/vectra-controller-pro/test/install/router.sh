#!/bin/sh
# One scenario of the universal installer, inside a fresh OpenWrt userland: boot
# what a router boots (ubusd, procd, rpcd, uhttpd, dnsmasq), set up the world the
# scenario needs, run the installer as a person would, and check the router it
# leaves behind. Every check prints `RESULT <name> PASS|FAIL <detail>`.
#
# The installer, the feed and the fixtures come from run.sh:
#   /fixtures/install.sh   the installer, baked for the stand's feed and key
#   /fixtures/*.ipk        fake packages that stand in for other stacks
#   /fixtures/rogue.pub    a key the feed is NOT signed with
#   /fixtures/operator-config.json, provider-config.json
#                          the data-plane stand's operator config and provider
#                          document (a freedom outbound), for passwall-retire
#   /stub/                 PassWall2 and legacy-agent service stubs (test/dataplane)
#
# SCENARIO names the path; see the case at the bottom.

INSTALLER=/fixtures/install.sh
PKG=vectra-controller-pro
FAILS=0

record() { # <name> PASS|FAIL <detail>
	echo "RESULT $1 $2 $3"
	[ "$2" = PASS ] || FAILS=$((FAILS + 1))
}
check() { # <name> <detail> <command...>
	n="$1"
	d="$2"
	shift 2
	if "$@" > /dev/null 2>&1; then record "$n" PASS "$d"; else record "$n" FAIL "$d"; fi
}
info() { echo "  .. $*"; }
die() {
	echo "STAND-FATAL $*"
	echo "INSTALL-EXIT 1"
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

installed() { opkg list-installed "$1" 2>/dev/null | grep -q "^$1 - "; }
version_of() { opkg list-installed "$1" 2>/dev/null | sed -n "s/^$1 - //p"; }
running() { [ -x "/etc/init.d/$1" ] && "/etc/init.d/$1" running; }
enabled() { [ -x "/etc/init.d/$1" ] && "/etc/init.d/$1" enabled; }
dns_answers() { nslookup localhost 127.0.0.1 2>/dev/null | grep -q '^Name:'; }
# DNS=runs (qemu-mips, see run.sh): the process is all that can be observed —
# by name (pidof): BusyBox's pgrep -x matches argv[0], /usr/sbin/dnsmasq, and
# finds only procd's jail wrapper, none at all with the jail off (JAIL=0).
dns_ok() {
	if [ "${DNS:-answers}" = runs ]; then pidof dnsmasq; else dns_answers; fi
}
not() { ! "$@"; }
# /etc/config/dhcp as it was before the install, byte for byte, but for the
# two lines the package adds (its uci-defaults): vectra.lan and
# my.vectra-pro.net on the LAN's dhcp section. `dhcp_as_before named` wants
# each line there exactly once, on the section whose interface is lan;
# `dhcp_as_before` wants them gone.
VECTRA_LAN_LINE="$(printf "\tlist interface_name 'vectra.lan'")"
VECTRA_NET_LINE="$(printf "\tlist interface_name 'my.vectra-pro.net'")"
dhcp_as_before() { # [named]
	vl_count="$(grep -cxF "$VECTRA_LAN_LINE" /etc/config/dhcp)"
	vn_count="$(grep -cxF "$VECTRA_NET_LINE" /etc/config/dhcp)"
	if [ "$1" = named ]; then
		[ "$vl_count" = 1 ] && [ "$vn_count" = 1 ] || return 1
		vl_lan="$(uci -q -X show dhcp | sed -n "s/^dhcp\.\([^.=]*\)\.interface='lan'\$/\1/p" | head -n 1)"
		vl_names="$(uci -q get "dhcp.$vl_lan.interface_name" | tr ' ' '\n')"
		printf '%s\n' "$vl_names" | grep -qx 'vectra\.lan' || return 1
		printf '%s\n' "$vl_names" | grep -qx 'my\.vectra-pro\.net' || return 1
	else
		[ "$vl_count" = 0 ] && [ "$vn_count" = 0 ] || return 1
	fi
	grep -vxF -e "$VECTRA_LAN_LINE" -e "$VECTRA_NET_LINE" /etc/config/dhcp | cmp -s - /tmp/dhcp.before
}

# ------------------------------------------------------------------ boot ----

boot() {
	mkdir -p /var/lock /var/run /var/state /tmp/lock /tmp/resolv.conf.d
	# Vectra's control plane is production: a router installed here must not
	# register with it (a fresh vctl does at its first start). The feed comes
	# from the stand's own server, the geo data inside the package.
	echo "127.0.0.1 api.vectra-pro.net router.vectra-pro.net" >> /etc/hosts
	# What netifd writes from the WAN lease: the upstream dnsmasq forwards to.
	grep '^nameserver' /etc/resolv.conf > /tmp/resolv.conf.d/resolv.conf.auto
	ubusd &
	sleep 1
	procd &
	wait_for 15 sh -c 'ubus list | grep -q "^service$"' || die "procd/ubusd never came up"
	# Under qemu-user (a foreign architecture) procd's ujail cannot set up its
	# namespaces, and every jailed service (dnsmasq) dies at start. That is the
	# emulator, not the router: run.sh sets JAIL=0 only there, and services run
	# unjailed, as on a router built without procd-ujail.
	if [ "${JAIL:-1}" = 0 ] && [ -e /sbin/ujail ]; then
		mv /sbin/ujail /sbin/ujail.qemu-off
		info "qemu: procd's jail off (JAIL=0)"
	fi
	# logd, as /etc/init.d/log starts it at boot: jailed services (dnsmasq) log
	# through it and do not start without it.
	/sbin/logd -S 64 &
	wait_for 10 sh -c 'ubus list | grep -q "^log$"' || die "logd never came up"
	# A container never boots, so its first-boot uci-defaults never ran: run
	# them as /etc/init.d/boot does. Some probe hardware and fail; that is the
	# same on a router with no such hardware.
	for f in /etc/uci-defaults/*; do
		# shellcheck disable=SC1090
		[ -f "$f" ] && (. "$f") > /dev/null 2>&1 && rm -f "$f"
	done
	/etc/init.d/rpcd start
	wait_for 15 sh -c 'ubus list | grep -q "^session$"' || die "rpcd never came up"
	/etc/init.d/uhttpd start
	wait_for 15 wget -q -O /dev/null http://127.0.0.1/ || die "uhttpd never came up"
	/etc/init.d/dnsmasq start
	wait_for 15 dns_ok || die "dnsmasq never answered on 127.0.0.1 (DNS=${DNS:-answers})"
	info "booted: procd, ubusd, rpcd, uhttpd, dnsmasq ($(version_of dnsmasq))"
}

# Everything a refused run must leave exactly as it found it.
snapshot() { # <file>
	{
		echo "## installed"
		opkg list-installed | sort
		echo "## customfeeds"
		cat /etc/opkg/customfeeds.conf 2>/dev/null
		echo "## distfeeds"
		cat /etc/opkg/distfeeds.conf
		echo "## keys"
		ls /etc/opkg/keys
		echo "## config"
		md5sum /etc/config/* 2>/dev/null
		echo "## services"
		ls /etc/rc.d
	} > "$1"
}

stubs() { # PassWall2 and the legacy agent as services, as the dataplane stand has them
	mkdir -p /stand
	cp -R /stub /stand/legacy-stub
	chmod +x /stand/legacy-stub/*
	cp /stand/legacy-stub/passwall2 /etc/init.d/passwall2
	cp /stand/legacy-stub/vectra-controller /etc/init.d/vectra-controller
	# PassWall's own switch, on: the stub, like the real one, starts nothing
	# with it off.
	cp /stand/legacy-stub/passwall2.config /etc/config/passwall2
	chmod +x /etc/init.d/passwall2 /etc/init.d/vectra-controller
}

# feed_retry <command...>: a setup step that downloads from OpenWrt's feeds,
# tried three times, 10 s then 30 s apart, as the installer tries its own:
# downloads.openwrt.org goes away for a minute now and then (2026-10-04), and
# no scenario is about the stand's own setup.
feed_retry() {
	for pause in 10 30 -; do
		"$@" > /dev/null 2>&1 && return 0
		[ "$pause" != - ] || return 1
		info "OpenWrt's feeds did not answer ($*): again in ${pause}s"
		sleep "$pause"
	done
}

install_fixture() { # <package>
	opkg install "/fixtures/$1.ipk" > /dev/null 2>&1 || die "fixture $1 did not install"
}

# The installer as a person runs it: no terminal on stdin (a pipe or ssh -T).
# A run that changed the router and failed (or whose end's checks found it
# broken) leaves what the router looked like right then in the scenario log.
installer() { # <args...> -> INSTALL_RC, output in /tmp/installer.out
	sh "$INSTALLER" "$@" < /dev/null > /tmp/installer.out 2>&1
	INSTALL_RC=$?
	sed 's/^/     | /' /tmp/installer.out
	if grep -q 'ОШИБКА\|но не работает' /tmp/installer.out; then
		diagnose "the installer failed (exit $INSTALL_RC): $(sed -n 's/^Итог: //p' /tmp/installer.out | tail -n 1)"
	fi
}
said() { grep -q -- "$1" /tmp/installer.out; }

# What the router looks like, into the scenario log (DIAG| lines): DNS and
# DHCP first — what an owner loses when the dnsmasq swap goes wrong — then the
# system log and the installer's own.
diagnose() { # <why>
	echo "DIAG ==== $1"
	{
		echo "## opkg list-installed dnsmasq*"
		opkg list-installed 'dnsmasq*'
		echo "## processes"
		ps w
		echo "## listening on :53"
		netstat -lnup | grep ':53 '
		netstat -lntp | grep ':53 '
		echo "## procd: the dnsmasq service"
		ubus call service list '{"name":"dnsmasq"}'
		echo "## nslookup localhost 127.0.0.1"
		nslookup localhost 127.0.0.1
		echo "## /etc/config/dhcp vs before"
		diff /tmp/dhcp.before /etc/config/dhcp
		echo "## logread (last 120)"
		logread | tail -n 120
		echo "## /tmp/vectra-install.log (last 200)"
		tail -n 200 /tmp/vectra-install.log
	} 2>&1 | sed 's/^/DIAG| /'
}
# --------------------------------------------------------------- asserts ----

# The directory vctl reads its geo files from: uci geo_asset_dir, where the
# fleet's old /usr/share/v2ray (and nothing) reads as Vectra's own.
geo_read() {
	d="$(uci -q get $PKG.main.geo_asset_dir)"
	case "${d%/}" in
	"" | /usr/share/v2ray) echo /usr/share/vectra-controller-pro/geo ;;
	*) echo "${d%/}" ;;
	esac
}

assert_installed_and_on() { # <prefix>
	p="$1"
	check "${p}_package" "$PKG $(version_of $PKG)" installed "$PKG"
	xv="$(xray version 2>/dev/null | awk 'NR == 1 { print $2 }')"
	check "${p}_xray" "xray-core $(version_of xray-core), runs as $xv" test "$xv" = "26.3.27"
	geo="$(geo_read)"
	check "${p}_geo_accepted" "xray accepts every category the provider routes by, from $geo" \
		env XRAY_LOCATION_ASSET="$geo" xray run -test -c /usr/share/vectra-controller-pro/geo/check.json
	check "${p}_dnsmasq_full" "dnsmasq-full $(version_of dnsmasq-full) replaced dnsmasq and answers" \
		sh -c "! opkg list-installed dnsmasq | grep -q '^dnsmasq - ' && pidof dnsmasq && { [ \"\${DNS:-answers}\" = runs ] || nslookup localhost 127.0.0.1 | grep -q '^Name:'; }"
	check "${p}_kmods" "kmod-nft-tproxy, kmod-nft-socket, kmod-nft-nat installed" \
		sh -c 'for k in kmod-nft-tproxy kmod-nft-socket kmod-nft-nat rpcd-mod-iwinfo; do opkg list-installed $k | grep -q "^$k - " || exit 1; done'
	check "${p}_feed" "src/gz vectra_pro in customfeeds.conf, key in /etc/opkg/keys" \
		sh -c "grep -q '^src/gz vectra_pro ' /etc/opkg/customfeeds.conf && ls /etc/opkg/keys | grep -q ."
	check "${p}_service" "the service is enabled and procd runs it" sh -c "/etc/init.d/$PKG enabled && /etc/init.d/$PKG running"
	check "${p}_ubus" "ubus call vectra status answers" sh -c 'ubus call vectra status | grep -q "\"version\""'
	check "${p}_luci" "uhttpd serves the Vectra bundle" wget -q -O /dev/null http://127.0.0.1/luci-static/vectra/vectra-app.js
	check "${p}_distfeeds" "OpenWrt's distfeeds.conf left as it was" cmp -s /etc/opkg/distfeeds.conf /tmp/distfeeds.before
	check "${p}_dhcp_config" "/etc/config/dhcp kept (the swap restores it), plus vectra.lan and my.vectra-pro.net on the LAN" dhcp_as_before named
}

# The dnsmasq the router had before the install, back: the same version,
# running, answering, and nothing of dnsmasq-full or Vectra left.
assert_old_dnsmasq_back() { # <prefix>
	check "$1_dnsmasq" "dnsmasq $(version_of dnsmasq) back (was $(cat /tmp/dnsmasq.version)), running, answering" \
		sh -c "[ \"\$(opkg list-installed dnsmasq | sed -n 's/^dnsmasq - //p')\" = \"$(cat /tmp/dnsmasq.version)\" ] && ! opkg list-installed dnsmasq-full | grep -q . && pidof dnsmasq && { [ \"\${DNS:-answers}\" = runs ] || nslookup localhost 127.0.0.1 | grep -q '^Name:'; }"
	check "$1_enabled" "dnsmasq enabled: it starts again after a reboot" /etc/init.d/dnsmasq enabled
	check "$1_no_vectra" "Vectra not installed" not installed "$PKG"
	check "$1_dhcp_config" "/etc/config/dhcp as before" cmp -s /etc/config/dhcp /tmp/dhcp.before
}

# From the moment dnsmasq-full is on the router, no download goes out: each
# attempt fails, and is written to /tmp/offline.calls (every one before it, to
# /tmp/wget.calls: the wrapper is the one opkg runs).
offline_after_swap() {
	real="$(readlink -f /usr/bin/wget)"
	[ -x "$real" ] || die "no wget to wrap ($real)"
	rm -f /usr/bin/wget
	cat > /usr/bin/wget <<-EOF
		#!/bin/sh
		for a; do url="\$a"; done
		[ -f /usr/lib/opkg/info/dnsmasq-full.control ] && touch /tmp/offline
		if [ -e /tmp/offline ]; then
			echo "\$url" >> /tmp/offline.calls
			exit 4
		fi
		echo "\$url" >> /tmp/wget.calls
		exec $real "\$@"
	EOF
	chmod 0755 /usr/bin/wget
}

assert_unchanged() { # <name> <what>
	snapshot /tmp/after
	if cmp -s /tmp/before /tmp/after; then
		record "$1" PASS "$2: the router is exactly as before"
	else
		record "$1" FAIL "$2: changed — $(diff /tmp/before /tmp/after | grep '^[-+][^-+]' | head -5 | tr '\n' ' ')"
	fi
}

refused() { # <name> <expected text> -> the installer refused, said why, changed nothing
	check "$1_refused" "exit 1 (refused before any change), got $INSTALL_RC" test "$INSTALL_RC" = 1
	check "$1_says_why" "says: $2" said "$2"
	assert_unchanged "$1_unchanged" "$1"
}

# ------------------------------------------------- PassWall2's retirement ----

# The PassWall2 of passwall-retire, as opkg has it: the package, its
# translation and the helpers it brought.
PW2_PACKAGES="luci-app-passwall2 luci-i18n-passwall2-ru geoview tcping chinadns-ng"
VCTL_ETC=/etc/vectra-controller-pro

# One field of `ubus call vectra status` (a jsonfilter path).
status_of() { ubus call vectra status 2> /dev/null | jsonfilter -e "$1" 2> /dev/null; }
# vctl carries the traffic as the retirement asks it to: the holder is Vectra
# (its table loaded), xray runs, its policy rule and route are in the kernel.
carrying() {
	[ "$(status_of '@.power.holder')" = vectra ] && [ "$(status_of '@.engine.state')" = running ] &&
		ip rule show | grep -q 'fwmark 0x1 lookup 100' && ip route show table 100 | grep -q '^local default'
}
all_installed() { for p in "$@"; do installed "$p" || return 1; done; }
none_installed() { for p in "$@"; do ! installed "$p" || return 1; done; }
# PassWall2's configuration kept: one archive, only root reads it, in a
# directory only root enters, with /etc/config/passwall2 in it. (ls for the
# modes: OpenWrt's busybox has no stat.)
# shellcheck disable=SC2010
# Sealed in vctl's vault since 0.7.0-r4 (passwall2-*.tar.gz.vault): opened
# here only by the explicit recovery, into a new file.
backup_kept() {
	set -- "$VCTL_ETC"/backup/passwall2-*.tar.gz.vault
	[ $# = 1 ] && [ -f "$1" ] || return 1
	ls -l "$1" | grep -q '^-rw-------' || return 1
	ls -ld "$VCTL_ETC/backup" | grep -q '^drwx------' || return 1
	head -c 10 "$1" | grep -q '^VCTLVAULT1' || return 1
	rm -f /tmp/passwall2-backup.tar.gz
	/usr/sbin/vctl vault-restore -in "$1" -out /tmp/passwall2-backup.tar.gz > /dev/null 2>&1 || return 1
	tar -tzf /tmp/passwall2-backup.tar.gz | grep -qx 'etc/config/passwall2'
}
# Nothing on the router says PassWall2 is owed back, and the record says why.
nothing_owed() {
	[ ! -e "$VCTL_ETC/.passwall-disabled-by-vctl" ] && [ ! -e "$VCTL_ETC/.passwall-switch-off-by-vctl" ] &&
		grep -q '^removed=.*luci-app-passwall2' "$VCTL_ETC/.passwall-retired-by-vctl"
}
retired_in_status() {
	[ "$(status_of '@.legacy.passwall')" = retired ] && [ -n "$(status_of '@.legacy.passwallRetiredAt')" ] &&
		[ "$(status_of '@.power.handBack')" = direct ]
}
# PassWall2 nowhere: no init script, no process (bracketed: a pgrep -f run
# from a shell whose command line names it would find that shell).
no_passwall() { [ ! -e /etc/init.d/passwall2 ] && ! pgrep -f '[p]asswall-loop.sh' > /dev/null; }
# Vectra off for good, the traffic straight out: no table, no policy rule.
vectra_off() {
	! running "$PKG" && ! enabled "$PKG" && [ "$(uci -q get "$PKG.main.enabled")" = 0 ] &&
		! nft list table inet vctl > /dev/null 2>&1 && ! ip rule show | grep -q 'fwmark 0x1 lookup 100'
}
# `vctl retire-passwall [--now]` -> RETIRE_RC, its output in /tmp/retire.out
retire_passwall() {
	vctl retire-passwall "$@" > /tmp/retire.out 2>&1
	RETIRE_RC=$?
	sed 's/^/     | /' /tmp/retire.out
}
# The dead-man, as cron runs it every minute (the stand runs no cron); not
# held off by the uptime of a docker host that has just booted.
deadman() { BOOT_SETTLE=0 /usr/libexec/vectra-controller-pro/deadman.sh; }

# ------------------------------------------------------------- scenarios ----

cp /etc/opkg/distfeeds.conf /tmp/distfeeds.before
ls /etc/opkg/keys > /tmp/keys.before
# nojail: a router built without procd-ujail runs dnsmasq as it is, no jail.
[ "$SCENARIO" = nojail ] && JAIL=0
boot
cp /etc/config/dhcp /tmp/dhcp.before
version_of dnsmasq > /tmp/dnsmasq.version
# STRESS (run.sh: INSTALL_STRESS): busy loops beside the scenario, for the
# races a router under load runs into and an idle stand does not.
i=0
while [ "$i" -lt "${STRESS:-0}" ]; do
	(while :; do :; done) &
	i=$((i + 1))
done
[ "${STRESS:-0}" = 0 ] || info "stress: $STRESS busy loops"

case "$SCENARIO" in
lifecycle)
	# A router straight out of the box: install, run again, remove.
	installer
	check fresh_exit "exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check fresh_sized "sizes the install from the feed's own list (xray alone is ~34 MB)" \
		sh -c "grep -q 'нужно около [1-9][0-9] МБ' /tmp/installer.out"
	assert_installed_and_on fresh
	check fresh_shell_on "a new router: the support shell is on, the owner can switch it off (remote_shell '$(uci -q get $PKG.main.remote_shell)')" \
		test "$(uci -q get $PKG.main.remote_shell)" = 1
	check fresh_geodata "vectra-geodata $(version_of vectra-geodata) keeps its data in Vectra's own directory, nothing of it in /usr/share/v2ray" \
		sh -c '[ -s /usr/share/vectra-controller-pro/geo/geoip.dat ] && [ -s /usr/share/vectra-controller-pro/geo/geosite.dat ] && [ ! -L /usr/share/v2ray/geoip.dat ] && [ ! -L /usr/share/v2ray/geosite.dat ]'
	check fresh_reporter "vectra-reporter $(version_of vectra-reporter): cron runs it every minute, and it answers" \
		sh -c 'grep -qxF "* * * * * /usr/sbin/vectra-reporter run >/dev/null 2>&1" /etc/crontabs/root && /usr/sbin/vectra-reporter status >/dev/null'
	check fresh_says_where "tells where the wizard is" said "/cgi-bin/luci/admin/vectra"
	check fresh_self_copy "keeps a copy of itself for --uninstall" test -s /etc/vectra-controller-pro/vectra-install.sh
	pid1="$(pgrep -f '/usr/sbin/vctl agent' | head -n 1)"

	installer
	check rerun_exit "a second run: exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check rerun_same_daemon "a second run leaves the running daemon alone (pid ${pid1:-none})" \
		test "$(pgrep -f '/usr/sbin/vctl agent' | head -n 1)" = "$pid1"
	check rerun_still_on "still enabled and running" sh -c "/etc/init.d/$PKG enabled && /etc/init.d/$PKG running"

	sh /etc/vectra-controller-pro/vectra-install.sh --uninstall < /dev/null > /tmp/installer.out 2>&1
	INSTALL_RC=$?
	sed 's/^/     | /' /tmp/installer.out
	check uninstall_exit "exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check uninstall_package "vectra-controller-pro and vectra-geodata removed" sh -c "! opkg list-installed | grep -qE '^(vectra-controller-pro|vectra-geodata) '"
	check uninstall_reporter "vectra-reporter removed, its cron block and its spool with it" \
		sh -c "! opkg list-installed | grep -q '^vectra-reporter ' && ! grep -q vectra-reporter /etc/crontabs/root && [ ! -d /etc/vectra-reporter ]"
	check uninstall_xray "xray-core, pulled in for Vectra, removed with it" not installed xray-core
	check uninstall_daemon "no vctl process left" not pgrep -f '/usr/sbin/vctl agent'
	check uninstall_ubus "the vectra ubus object is gone" not ubus call vectra status
	check uninstall_feed "the feed line and the key are gone" \
		sh -c "! grep -q '^src/gz vectra_pro ' /etc/opkg/customfeeds.conf && ls /etc/opkg/keys | cmp -s - /tmp/keys.before"
	check uninstall_dns "dnsmasq-full stays and still answers" sh -c "opkg list-installed dnsmasq-full | grep -q . && pidof dnsmasq && { [ \"\${DNS:-answers}\" = runs ] || nslookup localhost 127.0.0.1 | grep -q '^Name:'; }"
	check uninstall_dhcp_config "/etc/config/dhcp as before the install: vectra.lan and my.vectra-pro.net gone with the package" dhcp_as_before
	check uninstall_keeps_identity "without --purge the router's Vectra identity stays" test -d /etc/vectra-controller-pro
	sh /etc/vectra-controller-pro/vectra-install.sh --uninstall --purge < /dev/null > /dev/null 2>&1
	check uninstall_purge "--purge removes /etc/vectra-controller-pro" test ! -e /etc/vectra-controller-pro
	;;

geodata-links)
	# vectra-geodata before 2026.9.28-r2 linked its files into an empty
	# /usr/share/v2ray. vctl's install (0.6.0-r18: its uci-defaults) takes
	# those links away, and the directory with them when nothing else is in it.
	mkdir -p /usr/share/v2ray
	for f in geoip.dat geosite.dat; do ln -s "/usr/share/vectra-controller-pro/geo/$f" "/usr/share/v2ray/$f"; done
	installer
	check links_exit "exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	assert_installed_and_on links
	check links_gone "an earlier vectra-geodata's links in /usr/share/v2ray are gone, and the directory with them" \
		test ! -e /usr/share/v2ray
	# Another package's file stays, and the directory with it: only Vectra's
	# own links go — with vctl's install, not vectra-geodata's (upgraded
	# alone, it would pull them from under an older vctl that reads them).
	mkdir -p /usr/share/v2ray
	echo foreign > /usr/share/v2ray/geoip.dat
	ln -s /usr/share/vectra-controller-pro/geo/geosite.dat /usr/share/v2ray/geosite.dat
	opkg install --force-reinstall "$PKG" > /tmp/reinstall.out 2>&1
	sed 's/^/     | /' /tmp/reinstall.out
	check links_only_ours "vctl's re-install takes Vectra's link away and leaves another package's file" \
		sh -c '[ ! -e /usr/share/v2ray/geosite.dat ] && [ ! -L /usr/share/v2ray/geosite.dat ] && [ "$(cat /usr/share/v2ray/geoip.dat)" = foreign ]'
	;;

check)
	snapshot /tmp/before
	installer --check
	check check_exit "--check on a fresh router: exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check check_says_ok "says the router passed" said "Проверки пройдены"
	assert_unchanged check_unchanged "--check"
	;;

standby)
	# A fleet router: the old agent and PassWall2 carry the traffic, and keep it.
	stubs
	install_fixture vectra-controller-agent
	install_fixture luci-app-passwall2
	for s in passwall2 vectra-controller; do "/etc/init.d/$s" enable; "/etc/init.d/$s" start; done
	wait_for 10 running passwall2 || die "the PassWall2 stub never started"
	installer --standby
	check standby_exit "--standby next to the agent and PassWall2: exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check standby_package "$PKG $(version_of $PKG) installed" installed "$PKG"
	check standby_off "not running, not enabled, UCI enabled=0" \
		sh -c "! /etc/init.d/$PKG running && ! /etc/init.d/$PKG enabled && [ \"\$(uci -q get $PKG.main.enabled)\" = 0 ]"
	check standby_passwall_kept "PassWall2 still enabled and running" sh -c '/etc/init.d/passwall2 running && /etc/init.d/passwall2 enabled'
	check standby_agent_kept "the old agent still enabled and running" sh -c '/etc/init.d/vectra-controller running && /etc/init.d/vectra-controller enabled'
	check standby_ubus "the UI's ubus object answers with Vectra off" sh -c 'ubus call vectra status | grep -q "\"version\""'
	check standby_says_how "tells how to switch it on" sh -c "grep -q 'vectra on' /tmp/installer.out || grep -q 'enabled=1' /tmp/installer.out"
	;;

standby-upgrade)
	# The owner's router: PassWall2 (dnsmasq-full, xray-core, its own geo data),
	# the old agent, and an old vctl installed with its postinst held back and
	# UCI enabled=0 — off. The upgrade must leave it off and PassWall2 running.
	stubs
	install_fixture vectra-controller-agent
	# The old agent has run here: its state file is there, as on every router
	# it ran (a stand-in with no identity in it, so nothing ever adopts one).
	mkdir -p /etc/vectra-controller
	echo '{"device_identifier":"install-stand"}' > /etc/vectra-controller/state.json
	install_fixture luci-app-passwall2
	for s in passwall2 vectra-controller; do "/etc/init.d/$s" enable; "/etc/init.d/$s" start; done
	feed_retry opkg update
	opkg remove dnsmasq > /dev/null 2>&1
	feed_retry opkg install dnsmasq-full || die "dnsmasq-full did not install"
	/etc/init.d/dnsmasq restart
	opkg install /fixtures/xray-core-passwall.ipk > /dev/null 2>&1 || die "PassWall's xray-core did not install"
	export VECTRA_SKIP_POSTINST_RESTART=1
	opkg install /fixtures/vectra-controller-pro-old.ipk > /dev/null 2>&1 || die "the old vctl did not install"
	unset VECTRA_SKIP_POSTINST_RESTART
	uci set $PKG.main.enabled=0
	# The old vctl is one from before the support shell's switch (r36 and
	# earlier): no remote_shell, for the upgrade to decide.
	uci -q delete $PKG.main.remote_shell
	uci commit $PKG
	/etc/init.d/$PKG disable
	wait_for 10 running passwall2 || die "the PassWall2 stub never started"
	# PassWall's transparent proxy as older PassWall (and v1) route it: the same
	# fwmark and table vctl's own data plane uses.
	ip rule add fwmark 0x1 lookup 100
	ip route add local 0.0.0.0/0 dev lo table 100
	pw_pid="$(pgrep -f passwall-loop.sh | head -n 1)"
	agent_pid="$(pgrep -f legacy-agent-loop.sh | head -n 1)"
	xray_before="$(version_of xray-core)"
	info "old vctl $(version_of $PKG) off; PassWall2 pid $pw_pid, agent pid $agent_pid, xray-core $xray_before"
	# The feed also offers an xray-core newer than PassWall's.
	export VECTRA_FEED="${FEED_BASE}/newer"
	installer --standby
	check upgrade_exit "--standby over the old vctl: exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check upgrade_version "vctl upgraded 0.2.3-r1 -> $(version_of $PKG)" sh -c "[ \"\$(opkg list-installed $PKG | sed -n 's/^$PKG - //p')\" != 0.2.3-r1 ]"
	check upgrade_still_off "still off: not running, not enabled, UCI enabled=0" \
		sh -c "! /etc/init.d/$PKG running && ! /etc/init.d/$PKG enabled && [ \"\$(uci -q get $PKG.main.enabled)\" = 0 ] && ! ps w | grep -q '[/]usr/sbin/vctl agent'"
	check upgrade_passwall_uninterrupted "PassWall2 ran throughout: same process $pw_pid, still enabled" \
		sh -c "[ \"\$(pgrep -f passwall-loop.sh | head -n 1)\" = '$pw_pid' ] && /etc/init.d/passwall2 enabled"
	check upgrade_agent_uninterrupted "the old agent ran throughout: same process $agent_pid, still enabled" \
		sh -c "[ \"\$(pgrep -f legacy-agent-loop.sh | head -n 1)\" = '$agent_pid' ] && /etc/init.d/vectra-controller enabled"
	check upgrade_no_markers "no hand-over markers written" sh -c "! ls /etc/vectra-controller-pro/.*-by-vctl > /dev/null 2>&1"
	check upgrade_no_wizard "a working router: setup_done=1, the wizard does not open by itself" test "$(uci -q get $PKG.main.setup_done)" = 1
	check upgrade_shell_kept "the old agent ran here: the support shell stays on (remote_shell '$(uci -q get $PKG.main.remote_shell)')" \
		test "$(uci -q get $PKG.main.remote_shell)" = 1
	check upgrade_ubus "the UI answers with Vectra off" sh -c 'ubus call vectra status | grep -q "\"version\""'
	check upgrade_xray_untouched "xray-core left to its owner: $xray_before before, $(version_of xray-core) after" \
		test "$(version_of xray-core)" = "$xray_before"
	check upgrade_passwall_routes "PassWall's fwmark 0x1 / table 100 route survived the old copy's stop" \
		sh -c "ip rule show | grep -q 'fwmark 0x1 lookup 100' && ip route show table 100 | grep -q '^local default'"
	sh /etc/vectra-controller-pro/vectra-install.sh --uninstall < /dev/null > /tmp/installer.out 2>&1
	check upgrade_uninstall_keeps_routes "removing a Vectra that never ran leaves PassWall's route: exit $?" \
		sh -c "ip rule show | grep -q 'fwmark 0x1 lookup 100' && ip route show table 100 | grep -q '^local default' && ! opkg list-installed | grep -q '^vectra-controller-pro '"
	;;

passwall-upgrade)
	# A router Vectra took from PassWall2, upgraded: the upgrade restarts vctl
	# in place. An upgrade is no hand-back — PassWall2 may not run for the
	# seconds of the file swap (opkg runs the installed package's prerm on an
	# upgrade too). (No old agent here: the installer takes a router it runs
	# only with --standby.)
	stubs
	install_fixture luci-app-passwall2
	/etc/init.d/passwall2 enable
	/etc/init.d/passwall2 start
	wait_for 10 running passwall2 || die "the PassWall2 stub never started"
	installer --yes
	check pwup_taken "taken over first: exit $INSTALL_RC, vctl runs, PassWall2 stopped" \
		sh -c "[ '$INSTALL_RC' = 0 ] && /etc/init.d/$PKG running && ! /etc/init.d/passwall2 running"
	check pwup_shell_new "a new router, PassWall2 or not: the support shell is on (remote_shell '$(uci -q get $PKG.main.remote_shell)')" \
		test "$(uci -q get $PKG.main.remote_shell)" = 1
	# The vctl this upgrade replaces is one from before the support shell's
	# switch (r36 and earlier): no remote_shell. It has run here, so its state
	# is there.
	uci -q delete $PKG.main.remote_shell
	uci commit $PKG
	was="$(version_of $PKG)"
	pid1="$(pgrep -f '[/]usr/sbin/vctl agent' | head -n 1)"
	rm -f /tmp/upgrade.seen /tmp/upgrade.done /tmp/upgrade.looks
	(
		while [ ! -f /tmp/upgrade.done ]; do
			pgrep -f passwall-loop.sh > /dev/null && echo "PassWall2 ran" >> /tmp/upgrade.seen
			pgrep -f legacy-agent-loop.sh > /dev/null && echo "the agent ran" >> /tmp/upgrade.seen
			[ "$(uci -q get passwall2.@global[0].enabled)" = 1 ] && echo "PassWall's switch on" >> /tmp/upgrade.seen
			echo >> /tmp/upgrade.looks
			usleep 100000 2> /dev/null || sleep 1
		done
	) &
	# The same package relabelled 9.9.9-r1: the installed one's prerm runs.
	export VECTRA_FEED="${FEED_BASE}/upgrade" VECTRA_VERSION=9.9.9-r1
	installer --yes
	touch /tmp/upgrade.done
	sleep 1
	check pwup_exit "the upgrade: exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check pwup_version "vctl upgraded $was -> $(version_of $PKG)" test "$(version_of $PKG)" = 9.9.9-r1
	check pwup_restarted "vctl runs again, a new process (was ${pid1:-none})" \
		sh -c "/etc/init.d/$PKG running && p=\"\$(pgrep -f '[/]usr/sbin/vctl agent' | head -n 1)\" && [ -n \"\$p\" ] && [ \"\$p\" != '$pid1' ]"
	looks="$(wc -l < /tmp/upgrade.looks 2> /dev/null || echo 0)"
	check pwup_watched "the watcher looked throughout the upgrade: $looks looks" test "$looks" -ge 3
	check pwup_no_handback "no hand-back for the upgrade: $(sort -u /tmp/upgrade.seen 2> /dev/null | tr '\n' ';')" test ! -s /tmp/upgrade.seen
	check pwup_still_taken "PassWall2 still off, the marker to give it back kept" \
		sh -c "! /etc/init.d/passwall2 running && ! /etc/init.d/passwall2 enabled && [ -f /etc/vectra-controller-pro/.passwall-disabled-by-vctl ]"
	check pwup_shell_kept "upgraded from a vctl without the switch that ran here: the support shell stays on (remote_shell '$(uci -q get $PKG.main.remote_shell)')" \
		sh -c "[ -s /etc/vectra-controller-pro/state.json ] && [ \"\$(uci -q get $PKG.main.remote_shell)\" = 1 ]"
	;;

passwall)
	# A router with PassWall2 and the official, older xray-core.
	stubs
	install_fixture luci-app-passwall2
	/etc/init.d/passwall2 enable
	/etc/init.d/passwall2 start
	feed_retry opkg update
	feed_retry opkg install xray-core || die "the official xray-core did not install"
	info "official xray-core $(version_of xray-core) installed, as PassWall2 has it"
	# PassWall's own geo data: a valid geosite that has PRIVATE and nothing else,
	# so it lacks what the provider routes by (xray: code not found).
	mkdir -p /usr/share/v2ray
	printf '\012\030\012\007PRIVATE\022\015\010\002\022\011localhost' > /usr/share/v2ray/geosite.dat
	echo "not the provider's geoip" > /usr/share/v2ray/geoip.dat
	pw_geo="$(md5sum /usr/share/v2ray/geosite.dat /usr/share/v2ray/geoip.dat)"
	snapshot /tmp/before
	installer
	refused passwall_needs_yes "--yes"
	installer --yes
	check passwall_exit "with --yes: exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	assert_installed_and_on passwall
	check passwall_xray_upgraded "the older xray-core was upgraded to Vectra's" test "$(version_of xray-core)" = "26.3.27-r1"
	check passwall_geo_own "PassWall's geo data lacks the provider's categories: Vectra reads its own" \
		test "$(geo_read)" = /usr/share/vectra-controller-pro/geo
	check passwall_geo_untouched "PassWall's geo files left byte for byte" \
		sh -c "echo '$pw_geo' | md5sum -c -s -"
	check passwall_taken_over "PassWall2 stopped and disabled, with the marker to give it back" \
		sh -c '! /etc/init.d/passwall2 running && ! /etc/init.d/passwall2 enabled && [ -f /etc/vectra-controller-pro/.passwall-disabled-by-vctl ]'
	# `vectra off`, not the init script's stop: from 0.6.0 the dead-man
	# (cron, every minute) starts a vctl that should run and does not, so a
	# bare stop with Vectra on is undone within a minute.
	vectra off --foreground > /tmp/vectra-off.out 2>&1
	check passwall_given_back "vectra off starts PassWall2 again (exit $?)" wait_for 30 running passwall2
	check passwall_off_stays "a minute later Vectra is still off and PassWall2 still runs" \
		sh -c "sleep 65; ! /etc/init.d/$PKG running && ! /etc/init.d/$PKG enabled && /etc/init.d/passwall2 running"
	;;

passwall-retire)
	# PassWall2 as opkg has it on a fleet router (run.sh: its package owns its
	# init script and its configuration), with the official xray-core. Vectra
	# takes it with --yes and, once vctl carries the traffic, retires it — as
	# the daemon does by itself after a day; `vctl retire-passwall --now`
	# skips only the day. From then on `vectra off` leaves plain internet.
	mkdir -p /stand
	cp -R /stub /stand/legacy-stub
	chmod +x /stand/legacy-stub/*
	feed_retry opkg update
	feed_retry opkg install xray-core || die "the official xray-core did not install"
	for p in geoview tcping chinadns-ng luci-app-passwall2-full luci-i18n-passwall2-ru; do install_fixture "$p"; done
	/etc/init.d/passwall2 enable
	/etc/init.d/passwall2 start
	wait_for 10 running passwall2 || die "the PassWall2 stub never started"
	installer --yes
	check retire_taken_over "taken over with --yes: exit $INSTALL_RC, vctl runs, PassWall2 stopped and disabled" \
		sh -c "[ '$INSTALL_RC' = 0 ] && /etc/init.d/$PKG running && ! /etc/init.d/passwall2 running && ! /etc/init.d/passwall2 enabled"
	check retire_installer_says "the installer names vectra off as the way back, says PassWall2 goes after a day and where its configuration is kept" \
		sh -c "grep -q 'vectra off' /tmp/installer.out && grep -q 'сутки' /tmp/installer.out && grep -q '$VCTL_ETC/backup' /tmp/installer.out && ! grep -q 'init.d/$PKG stop' /tmp/installer.out"
	check retire_owed_at_first "PassWall2 installed, and vectra off would give it the traffic back (status: $(status_of '@.legacy.passwall'), hand-back $(status_of '@.power.handBack'))" \
		test "$(status_of '@.legacy.passwall')/$(status_of '@.power.handBack')" = installed/passwall2

	# Not bound to an account, vctl carries nothing: PassWall2 stays, and the
	# command says why.
	retire_passwall --now
	check retire_refused_unconfigured "no operator config yet: refused (exit $RETIRE_RC), PassWall2 kept" \
		sh -c "[ $RETIRE_RC != 0 ] && grep -q 'operator config' /tmp/retire.out && [ -x /etc/init.d/passwall2 ]"
	# UCI retire_passwall '0' keeps it, --now or not.
	uci set "$PKG.main.retire_passwall=0"
	uci commit "$PKG"
	/etc/init.d/$PKG restart
	wait_for 30 running "$PKG" || die "vctl did not come back after its restart"
	wait_for 30 sh -c "vctl retire-passwall --now > /tmp/retire.out 2>&1; ! grep -q 'does not run' /tmp/retire.out"
	sed 's/^/     | /' /tmp/retire.out
	check retire_refused_switch "UCI retire_passwall '0': refused, PassWall2 kept" \
		sh -c "grep -q \"retire_passwall is '0'\" /tmp/retire.out && [ -x /etc/init.d/passwall2 ]"
	# shellcheck disable=SC2086
	check retire_refusals_kept_packages "after both refusals: $PW2_PACKAGES all installed" all_installed $PW2_PACKAGES
	check retire_refusals_kept_owed "after both refusals: the takeover's breadcrumbs and PassWall2's configuration there, no record" \
		sh -c "[ -e $VCTL_ETC/.passwall-disabled-by-vctl ] && [ -e /etc/config/passwall2 ] && [ ! -e $VCTL_ETC/.passwall-retired-by-vctl ]"
	uci delete "$PKG.main.retire_passwall"
	uci commit "$PKG"

	# A router bound to an account has the operator config and the provider's
	# document on /etc; `vctl apply-local` and a restart make them live. The
	# panel is out of reach here, so nothing would confirm the firewall's
	# commit and it would revert after 90 s: the sentinel is kept, as a
	# reachable panel's check-in keeps it.
	cp /fixtures/operator-config.json "$VCTL_ETC/xray-desired.json"
	cp /fixtures/provider-config.json "$VCTL_ETC/provider-config.json"
	chmod 0600 "$VCTL_ETC/xray-desired.json" "$VCTL_ETC/provider-config.json"
	vctl apply-local -config /var/run/vectra-controller-pro/agent.json > /tmp/apply-local.out 2>&1 || apply_rc=$?
	sed 's/^/     | /' /tmp/apply-local.out
	[ -z "${apply_rc:-}" ] || die "vctl apply-local did not install the operator config (exit $apply_rc)"
	(while :; do touch /var/run/vectra-controller-pro/fw-confirm 2> /dev/null; sleep 2; done) &
	CONFIRMER=$!
	/etc/init.d/$PKG restart
	if ! wait_for 90 carrying; then
		info "status: $(ubus call vectra status 2>&1 | tr -s ' \n' ' ' | cut -c1-600)"
		info "nft: $(nft list tables 2>&1 | tr '\n' ' '); ip rule: $(ip rule show 2>&1 | tr '\n' ' ')"
		logread -e vctl 2> /dev/null | tail -n 30 | sed 's/^/     | /'
		die "vctl never carried the traffic: the retirement cannot be shown"
	fi
	check retire_carrying "vctl carries the traffic: holder vectra, xray running, fwmark 0x1 / table 100 in the kernel" carrying

	retire_passwall --now
	check retire_done "vctl retire-passwall --now: exit $RETIRE_RC, PassWall2 retired, and it says what vectra off does now" \
		sh -c "[ $RETIRE_RC = 0 ] && grep -q 'PassWall2 retired' /tmp/retire.out && grep -q 'vectra off' /tmp/retire.out"
	# shellcheck disable=SC2086
	check retire_packages_gone "removed: $PW2_PACKAGES" none_installed $PW2_PACKAGES
	check retire_router_kept "kept: xray-core $(version_of xray-core), dnsmasq-full $(version_of dnsmasq-full), $PKG" \
		all_installed xray-core dnsmasq-full "$PKG"
	check retire_backup "its configuration in one archive, 0600, in a 0700 $VCTL_ETC/backup: $(ls "$VCTL_ETC"/backup 2> /dev/null | tr '\n' ' ')" backup_kept
	check retire_config_gone "/etc/config/passwall2, the conffile opkg kept, is gone: it is in the backup" test ! -e /etc/config/passwall2
	check retire_nothing_owed "the takeover's breadcrumbs gone, the record there: $(tr '\n' ' ' 2> /dev/null < "$VCTL_ETC/.passwall-retired-by-vctl")" nothing_owed
	check retire_no_passwall "PassWall2's init script went with its package; nothing of it runs" no_passwall
	check retire_still_carrying "vctl still runs and carries the traffic" carrying
	check retire_status "ubus: legacy.passwall $(status_of '@.legacy.passwall') at $(status_of '@.legacy.passwallRetiredAt'), hand-back $(status_of '@.power.handBack')" \
		retired_in_status

	# A bare stop with Vectra on is no way out: the dead-man starts vctl
	# again. With PassWall2 retired nothing is owed it — no hand-back to it.
	/etc/init.d/$PKG stop
	wait_for 30 not running "$PKG"
	deadman
	check retire_deadman_restarts "a bare stop: the dead-man starts vctl again, Vectra stays switched on, PassWall2 stays gone" \
		sh -c "/etc/init.d/$PKG enabled && [ \"\$(uci -q get $PKG.main.enabled)\" != 0 ] && [ ! -e /etc/init.d/passwall2 ]"
	check retire_deadman_running "vctl runs again" wait_for 30 running "$PKG"

	vectra off --foreground > /tmp/vectra-off.out 2>&1
	off_rc=$?
	sed 's/^/     | /' /tmp/vectra-off.out
	kill "$CONFIRMER" 2> /dev/null
	check retire_off "vectra off (exit $off_rc) says the traffic goes directly, without a VPN" \
		sh -c "[ $off_rc = 0 ] && grep -q 'directly' /tmp/vectra-off.out"
	check retire_off_plain "off and disabled, the router on plain internet: no vctl table, no fwmark 0x1 rule" vectra_off
	check retire_off_no_passwall "PassWall2 does not come back" no_passwall
	deadman
	sleep 60
	deadman
	check retire_off_stays "a minute later, the dead-man run as cron runs it: Vectra still off, PassWall2 still gone" \
		sh -c "! /etc/init.d/$PKG running && ! /etc/init.d/$PKG enabled && [ ! -e /etc/init.d/passwall2 ] && ! pgrep -f '[p]asswall-loop.sh'"
	;;

refuse-arch)
	snapshot /tmp/before
	export VECTRA_ARCHS=mipsel_24kc
	installer
	refused arch "не поддерживается"
	;;
refuse-apk)
	mv /bin/opkg /bin/opkg.hidden
	printf '#!/bin/sh\nexit 1\n' > /usr/bin/apk
	chmod +x /usr/bin/apk
	installer
	check apk_refused "exit 1, got $INSTALL_RC" test "$INSTALL_RC" = 1
	check apk_says_why "names apk" said "apk"
	rm -f /usr/bin/apk
	mv /bin/opkg.hidden /bin/opkg
	;;
refuse-release)
	cp /etc/openwrt_release /tmp/openwrt_release
	sed -i "s/^DISTRIB_RELEASE=.*/DISTRIB_RELEASE='22.03.7'/" /etc/openwrt_release
	snapshot /tmp/before
	installer
	refused release "22.03"
	cp /tmp/openwrt_release /etc/openwrt_release
	;;
refuse-memory)
	snapshot /tmp/before
	export VECTRA_MIN_RAM_KB=999999999
	installer
	refused memory "оперативной памяти"
	;;
refuse-storage)
	mkdir -p /overlay
	mount -t tmpfs -o size=12m tmpfs /overlay || die "cannot mount a small /overlay"
	snapshot /tmp/before
	installer
	refused storage "свободно"
	umount /overlay
	;;
refuse-conflict)
	install_fixture podkop
	snapshot /tmp/before
	installer
	refused conflict "podkop"
	;;
refuse-fleet)
	install_fixture vectra-controller-agent
	snapshot /tmp/before
	installer
	refused fleet "--standby"
	;;
refuse-agent-old)
	# An old agent that does not know Vectra (before 0.1.13-r45): not even
	# next to it, off — update the agent first.
	install_fixture vectra-controller-agent-r43
	snapshot /tmp/before
	installer --standby
	refused agent_old "0.1.13-r45"
	;;
check-json)
	# --check --json next to the old agent, without --standby: why it is
	# needed, the rest checked as with it — JSON lines, ASCII only, exit 1.
	install_fixture vectra-controller-agent
	snapshot /tmp/before
	installer --check --json
	check json_exit "exit 1, got $INSTALL_RC" test "$INSTALL_RC" = 1
	check json_ascii "every line a JSON object, ASCII only" sh -c '! LC_ALL=C grep -q "[^ -~]" /tmp/installer.out && ! grep -qv "^{.*}$" /tmp/installer.out'
	check json_code "names LEGACY_AGENT_NEEDS_STANDBY, and ends refused" sh -c 'grep -q "\"check\":\"LEGACY_AGENT_NEEDS_STANDBY\"" /tmp/installer.out && tail -n 1 /tmp/installer.out | grep -q "\"result\":\"refused\""'
	check json_rest "the rest checked as with --standby: the storage too" sh -c 'grep -q "\"check\":\"STORAGE\"" /tmp/installer.out'
	check json_log "the words are in the log" grep -q -- "--standby" /tmp/vectra-install.log
	assert_unchanged json_unchanged "--check --json"
	;;
refuse-signature)
	snapshot /tmp/before
	VECTRA_FEED_KEY="$(sed -n 2p /fixtures/rogue.pub)"
	VECTRA_FEED_KEY_ID="$(cat /fixtures/rogue.id)"
	export VECTRA_FEED_KEY VECTRA_FEED_KEY_ID
	installer
	refused signature "подпис"
	;;
refuse-feed-down)
	snapshot /tmp/before
	export VECTRA_FEED="${FEED_BASE}/nope"
	installer
	refused feed_down "недоступен"
	;;

dnsmasq-rollback)
	# dnsmasq-full that installs and never runs: the old dnsmasq must come back
	# — with no network: from the moment dnsmasq-full is on the router every
	# download fails, and is counted.
	export VECTRA_FEED="${FEED_BASE}/broken"
	offline_after_swap
	installer
	check rollback_exit "exit 1, got $INSTALL_RC" test "$INSTALL_RC" = 1
	check rollback_says "says the old dnsmasq is back" said "прежний dnsmasq возвращён"
	assert_old_dnsmasq_back rollback
	check rollback_offline "the way back downloaded nothing ($(wc -l < /tmp/offline.calls 2> /dev/null || echo 0) attempts after the swap began)" \
		sh -c '[ -s /tmp/wget.calls ] && [ ! -s /tmp/offline.calls ]'
	check rollback_saved_gone "what was kept for the way back is gone once it is not needed" test ! -e /tmp/vectra-dnsmasq-saved
	;;

dnsmasq-rollback-files)
	# The way back when opkg cannot put the old package back (a lock, a broken
	# status file): its files and opkg's record of them, kept before the swap,
	# go back by hand — still with no network.
	export VECTRA_FEED="${FEED_BASE}/broken"
	offline_after_swap
	mv /bin/opkg /bin/opkg.real
	cat > /bin/opkg <<-'EOF'
		#!/bin/sh
		case "$1 $2" in
		"install /tmp/vectra-dnsmasq-saved/dnsmasq_"*) echo "stand: opkg refuses to put dnsmasq back"; exit 255 ;;
		esac
		exec /bin/opkg.real "$@"
	EOF
	chmod 0755 /bin/opkg
	installer
	rm -f /bin/opkg
	mv /bin/opkg.real /bin/opkg
	check files_exit "exit 1, got $INSTALL_RC" test "$INSTALL_RC" = 1
	check files_says "says it put the old dnsmasq's files back, and that it works" \
		sh -c "grep -q 'восстанавливаю его файлы' /tmp/installer.out && grep -q 'прежний dnsmasq возвращён и работает' /tmp/installer.out"
	assert_old_dnsmasq_back files
	check files_opkg_record "opkg knows dnsmasq as installed, with its files: $(opkg files dnsmasq 2> /dev/null | grep -c /)" \
		sh -c "opkg files dnsmasq | grep -qx /usr/sbin/dnsmasq && opkg status dnsmasq | grep -q '^Status: install ok installed'"
	check files_offline "the way back downloaded nothing ($(wc -l < /tmp/offline.calls 2> /dev/null || echo 0) attempts)" \
		sh -c '[ -s /tmp/wget.calls ] && [ ! -s /tmp/offline.calls ]'
	;;

nojail)
	# A router without procd's jail (no procd-ujail at the swap): its dnsmasq
	# runs as /usr/sbin/dnsmasq, which `pgrep -x dnsmasq` (the installer of
	# 0.7.0-r18 and before) never finds — it found the jail wrapper, or an
	# init script that happened to run. Found by name now, swapped, installed.
	check nojail_unjailed "dnsmasq runs without the jail: $(tr '\0' ' ' < "/proc/$(pidof dnsmasq | cut -d ' ' -f 1)/cmdline" 2> /dev/null)" \
		sh -c '[ ! -e /sbin/ujail ] && p="$(pidof dnsmasq)" && [ -n "$p" ] && ! grep -q ujail /proc/${p%% *}/cmdline'
	installer
	check nojail_exit "exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	assert_installed_and_on nojail
	;;

dnsmasq-stale)
	# A dnsmasq left holding port 53 while dnsmasq-full starts — one that
	# outlived procd's stop, frozen, answering nobody: every dnsmasq started
	# after it dies with "Address in use" (the installer of 0.7.0-r18 and before
	# then could not put the old one back either: DNSMASQ_FULL_BROKEN). It is
	# found and killed, and dnsmasq-full answers.
	mkdir -p /tmp/stale
	cp /usr/sbin/dnsmasq /tmp/stale/dnsmasq
	mv /bin/opkg /bin/opkg.real
	cat > /bin/opkg <<-'EOF'
		#!/bin/sh
		/bin/opkg.real "$@"
		rc=$?
		case "$1 $2" in
		"install /tmp/vectra-install/pkgs/dnsmasq-full_"*)
			if [ ! -e /tmp/stale.pid ]; then
				/tmp/stale/dnsmasq --conf-file=/dev/null --no-resolv --no-hosts --pid-file=/tmp/stale.pid
				sleep 1
				kill -STOP "$(cat /tmp/stale.pid)"
			fi
			;;
		esac
		exit "$rc"
	EOF
	chmod 0755 /bin/opkg
	installer
	rm -f /bin/opkg
	mv /bin/opkg.real /bin/opkg
	check stale_injected "a frozen dnsmasq held port 53 during the swap (pid $(cat /tmp/stale.pid 2> /dev/null))" test -s /tmp/stale.pid
	check stale_exit "exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check stale_killed "the frozen dnsmasq was found and killed (the install log says so)" \
		sh -c "case \"\$(cut -d ' ' -f 3 /proc/\$(cat /tmp/stale.pid)/stat 2> /dev/null)\" in '' | Z) ;; *) exit 1 ;; esac && grep -q '^# dnsmasq still runs after its stop' /tmp/vectra-install.log"
	assert_installed_and_on stale
	;;

feed-outage)
	# downloads.openwrt.org away for a moment (2026-10-04: about 40 s), at
	# each step that needs it: one package list in opkg update, one package
	# before the dnsmasq swap (DNSMASQ_DEPENDENCY or DNSMASQ_DOWNLOAD) and one
	# after it (PKG_INSTALL) fail to download once, as opkg sees a feed that
	# does not answer. The installer tries again and installs — through
	# OpenWrt's own feeds, not the mirror.
	real="$(readlink -f /usr/bin/wget)"
	[ -x "$real" ] || die "no wget to wrap ($real)"
	rm -f /usr/bin/wget
	cat > /usr/bin/wget <<-EOF
		#!/bin/sh
		for a; do url="\$a"; done
		k=""
		case "\$url" in
		https://downloads.openwrt.org/*/Packages.gz) k=list ;;
		https://downloads.openwrt.org/*.ipk) [ -f /usr/lib/opkg/info/dnsmasq-full.control ] && k=after || k=before ;;
		esac
		if [ -n "\$k" ] && [ ! -e "/tmp/outage.\$k" ]; then
			echo "\$url" > "/tmp/outage.\$k"
			exit 4
		fi
		exec $real "\$@"
	EOF
	chmod 0755 /usr/bin/wget
	installer
	# What the retries met: opkg's own words, from the install log.
	sed "s/^/LOG| /" /tmp/vectra-install.log
	for k in list before after; do info "away once ($k): $(cat "/tmp/outage.$k" 2> /dev/null)"; done
	check outage_exit "exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check outage_injected "a package list, a package before the dnsmasq swap and one after it each failed to download once" \
		test -s /tmp/outage.list -a -s /tmp/outage.before -a -s /tmp/outage.after
	check outage_retried "says it tries again: $(grep -c 'повторяю (2/3)' /tmp/installer.out) times" \
		sh -c "[ \"\$(grep -c 'Сервер пакетов OpenWrt не ответил — повторяю (2/3)' /tmp/installer.out)\" -ge 3 ]"
	check outage_logged "each retry is in the install log" sh -c "[ \"\$(grep -c '^# a download failed' /tmp/vectra-install.log)\" -ge 3 ]"
	check outage_no_mirror "OpenWrt's own feeds, not the mirror" not said "беру фиды OpenWrt через"
	assert_installed_and_on outage
	;;

mirror)
	# downloads.openwrt.org unreachable: OpenWrt's feeds through Vectra's proxy.
	echo "127.0.0.1 downloads.openwrt.org" >> /etc/hosts
	export VECTRA_OPENWRT_MIRROR="${OPENWRT_MIRROR:?run.sh sets OPENWRT_MIRROR}"
	installer
	check mirror_exit "exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check mirror_used "says it took OpenWrt's feeds through the mirror" said "$OPENWRT_MIRROR"
	assert_installed_and_on mirror
	;;

*)
	die "unknown SCENARIO '$SCENARIO'"
	;;
esac

[ "$FAILS" = 0 ] || diagnose "the end: $FAILS check(s) failed"
echo "INSTALL-EXIT $FAILS"
exit "$FAILS"
