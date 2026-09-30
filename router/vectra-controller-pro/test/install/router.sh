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
# DNS=runs (qemu-mips, see run.sh): the process is all that can be observed.
dns_ok() {
	if [ "${DNS:-answers}" = runs ]; then pgrep -x dnsmasq; else dns_answers; fi
}
not() { ! "$@"; }
# /etc/config/dhcp as it was before the install, byte for byte, but for the
# one line the package adds (its uci-defaults): vectra.lan on the LAN's dhcp
# section. `dhcp_as_before vectra.lan` wants that line there exactly once, on
# the section whose interface is lan; `dhcp_as_before` wants it gone.
VECTRA_LAN_LINE="$(printf "\tlist interface_name 'vectra.lan'")"
dhcp_as_before() { # [vectra.lan]
	vl_count="$(grep -cxF "$VECTRA_LAN_LINE" /etc/config/dhcp)"
	if [ "$1" = vectra.lan ]; then
		[ "$vl_count" = 1 ] || return 1
		vl_lan="$(uci -q -X show dhcp | sed -n "s/^dhcp\.\([^.=]*\)\.interface='lan'\$/\1/p" | head -n 1)"
		uci -q get "dhcp.$vl_lan.interface_name" | tr ' ' '\n' | grep -qx 'vectra\.lan' || return 1
	else
		[ "$vl_count" = 0 ] || return 1
	fi
	grep -vxF "$VECTRA_LAN_LINE" /etc/config/dhcp | cmp -s - /tmp/dhcp.before
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

install_fixture() { # <package>
	opkg install "/fixtures/$1.ipk" > /dev/null 2>&1 || die "fixture $1 did not install"
}

# The installer as a person runs it: no terminal on stdin (a pipe or ssh -T).
installer() { # <args...> -> INSTALL_RC, output in /tmp/installer.out
	sh "$INSTALLER" "$@" < /dev/null > /tmp/installer.out 2>&1
	INSTALL_RC=$?
	sed 's/^/     | /' /tmp/installer.out
}
said() { grep -q -- "$1" /tmp/installer.out; }

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
		sh -c "! opkg list-installed dnsmasq | grep -q '^dnsmasq - ' && pgrep -x dnsmasq && { [ \"\${DNS:-answers}\" = runs ] || nslookup localhost 127.0.0.1 | grep -q '^Name:'; }"
	check "${p}_kmods" "kmod-nft-tproxy, kmod-nft-socket, kmod-nft-nat installed" \
		sh -c 'for k in kmod-nft-tproxy kmod-nft-socket kmod-nft-nat rpcd-mod-iwinfo; do opkg list-installed $k | grep -q "^$k - " || exit 1; done'
	check "${p}_feed" "src/gz vectra_pro in customfeeds.conf, key in /etc/opkg/keys" \
		sh -c "grep -q '^src/gz vectra_pro ' /etc/opkg/customfeeds.conf && ls /etc/opkg/keys | grep -q ."
	check "${p}_service" "the service is enabled and procd runs it" sh -c "/etc/init.d/$PKG enabled && /etc/init.d/$PKG running"
	check "${p}_ubus" "ubus call vectra status answers" sh -c 'ubus call vectra status | grep -q "\"version\""'
	check "${p}_luci" "uhttpd serves the Vectra bundle" wget -q -O /dev/null http://127.0.0.1/luci-static/vectra/vectra-app.js
	check "${p}_distfeeds" "OpenWrt's distfeeds.conf left as it was" cmp -s /etc/opkg/distfeeds.conf /tmp/distfeeds.before
	check "${p}_dhcp_config" "/etc/config/dhcp kept (the swap restores it), plus vectra.lan on the LAN" dhcp_as_before vectra.lan
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
	check "$1_refused" "exit 2 (refused before any change), got $INSTALL_RC" test "$INSTALL_RC" = 2
	check "$1_says_why" "says: $2" said "$2"
	assert_unchanged "$1_unchanged" "$1"
}

# ------------------------------------------------------------- scenarios ----

cp /etc/opkg/distfeeds.conf /tmp/distfeeds.before
ls /etc/opkg/keys > /tmp/keys.before
boot
cp /etc/config/dhcp /tmp/dhcp.before

case "$SCENARIO" in
lifecycle)
	# A router straight out of the box: install, run again, remove.
	installer
	check fresh_exit "exit 0, got $INSTALL_RC" test "$INSTALL_RC" = 0
	check fresh_sized "sizes the install from the feed's own list (xray alone is ~34 MB)" \
		sh -c "grep -q 'нужно около [1-9][0-9] МБ' /tmp/installer.out"
	assert_installed_and_on fresh
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
	check uninstall_dns "dnsmasq-full stays and still answers" sh -c "opkg list-installed dnsmasq-full | grep -q . && pgrep -x dnsmasq && { [ \"\${DNS:-answers}\" = runs ] || nslookup localhost 127.0.0.1 | grep -q '^Name:'; }"
	check uninstall_dhcp_config "/etc/config/dhcp as before the install: vectra.lan gone with the package" dhcp_as_before
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
	install_fixture luci-app-passwall2
	for s in passwall2 vectra-controller; do "/etc/init.d/$s" enable; "/etc/init.d/$s" start; done
	opkg update > /dev/null 2>&1
	opkg remove dnsmasq > /dev/null 2>&1
	opkg install dnsmasq-full > /dev/null 2>&1 || die "dnsmasq-full did not install"
	/etc/init.d/dnsmasq restart
	opkg install /fixtures/xray-core-passwall.ipk > /dev/null 2>&1 || die "PassWall's xray-core did not install"
	export VECTRA_SKIP_POSTINST_RESTART=1
	opkg install /fixtures/vectra-controller-pro-old.ipk > /dev/null 2>&1 || die "the old vctl did not install"
	unset VECTRA_SKIP_POSTINST_RESTART
	uci set $PKG.main.enabled=0
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
	;;

passwall)
	# A router with PassWall2 and the official, older xray-core.
	stubs
	install_fixture luci-app-passwall2
	/etc/init.d/passwall2 enable
	/etc/init.d/passwall2 start
	opkg update > /dev/null 2>&1
	opkg install xray-core > /dev/null 2>&1 || die "the official xray-core did not install"
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
	check apk_refused "exit 2, got $INSTALL_RC" test "$INSTALL_RC" = 2
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
	# dnsmasq-full that installs and never runs: the old dnsmasq must come back.
	export VECTRA_FEED="${FEED_BASE}/broken"
	installer
	check rollback_exit "exit 1, got $INSTALL_RC" test "$INSTALL_RC" = 1
	check rollback_says "says the old dnsmasq is back" said "прежний dnsmasq возвращён"
	check rollback_dnsmasq "dnsmasq $(version_of dnsmasq) back, running, answering" \
		sh -c "opkg list-installed dnsmasq | grep -q '^dnsmasq - ' && ! opkg list-installed dnsmasq-full | grep -q . && pgrep -x dnsmasq && { [ \"\${DNS:-answers}\" = runs ] || nslookup localhost 127.0.0.1 | grep -q '^Name:'; }"
	check rollback_no_vectra "Vectra not installed" not installed "$PKG"
	check rollback_dhcp_config "/etc/config/dhcp as before" cmp -s /etc/config/dhcp /tmp/dhcp.before
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

echo "INSTALL-EXIT $FAILS"
exit "$FAILS"
