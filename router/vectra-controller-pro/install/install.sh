#!/bin/sh
# Vectra on an OpenWrt router: one installer for every architecture.
#
#   wget -O /tmp/vectra https://router.vectra-pro.net/install && sh /tmp/vectra
#
#   sh /tmp/vectra            install, or update what is installed
#   sh /tmp/vectra --standby  install, but leave Vectra off: whatever carries the
#                             traffic now (PassWall2, the old Vectra agent) keeps
#                             it until `vectra on`
#   sh /tmp/vectra --check    only the checks: nothing on the router changes
#   sh /tmp/vectra --uninstall remove Vectra (add --purge to forget the router's
#                             Vectra identity and settings too)
#   --yes                     do not ask (a router with PassWall2: Vectra takes
#                             over the traffic, `vectra off` gives it back; after
#                             a day of Vectra's VPN, Vectra removes PassWall2)
#   --force                   go on below the memory floor (tests only: the
#                             router may run out of memory)
#   --json                    say it as JSON lines, ASCII codes only (with
#                             --check: what an operator reads through the panel)
#
# Exit code: 0 done, nothing to warn of (--check: also with advice, named);
# 2 done, with warnings (said in the summary, by code); 1 not done — refused
# before any change, failed, or Vectra installed to carry the traffic and not
# running; 3 an option it does not know. (Before 0.7.0-r14 a refusal was 2.)
#
# OpenWrt 23.05 or 24.10 with opkg. OpenWrt 25 (apk) is not supported yet.
#
# Nothing changes before every check has passed: the router, its memory and
# storage, the clock, the network, the feeds, other proxy stacks. Then: the
# signed Vectra feed is added (its key is baked into this file), dnsmasq is
# replaced by dnsmasq-full without a moment the network has no DHCP or DNS
# (both packages are downloaded first; if dnsmasq-full does not come up, the old
# dnsmasq is put back), and vectra-controller-pro is installed with xray-core
# and the geo data (an xray put on the router by hand becomes the xray-core
# package of its own version first: nothing replaces it with another). The
# end is checked, not assumed: the service runs, the
# "vectra" ubus object answers, xray accepts the geo data, LuCI serves the page.
# A download from the feeds that fails is tried again, three times in all.
# Everything opkg says goes to /tmp/vectra-install.log.
#
# This file is a template until scripts/sign-pro-feed.sh bakes the block below
# (the feed, its key, the architectures and versions it holds). For tests the
# VECTRA_FEED, VECTRA_FEED_KEY, VECTRA_FEED_KEY_ID, VECTRA_ARCHS,
# VECTRA_VERSION and VECTRA_XRAY_MIN variables stand in for it.

# >>> baked by sign-pro-feed.sh
BAKED_FEED_URL=''
BAKED_FEED_KEY=''
BAKED_FEED_KEY_ID=''
BAKED_ARCHS=''
BAKED_VERSION=''
BAKED_XRAY_MIN=''
# <<< baked

FEED_URL="${VECTRA_FEED:-$BAKED_FEED_URL}"
FEED_KEY="${VECTRA_FEED_KEY:-$BAKED_FEED_KEY}"
FEED_KEY_ID="${VECTRA_FEED_KEY_ID:-$BAKED_FEED_KEY_ID}"
ARCHS="${VECTRA_ARCHS:-$BAKED_ARCHS}"
VERSION="${VECTRA_VERSION:-$BAKED_VERSION}"
XRAY_MIN="${VECTRA_XRAY_MIN:-$BAKED_XRAY_MIN}"
# OpenWrt's own feeds through Vectra's caching proxy, for a router that cannot
# reach downloads.openwrt.org (a broken ISP IPv6 is the usual cause).
OPENWRT_MIRROR="${VECTRA_OPENWRT_MIRROR:-}"

# 256 MB routers report 230-250 MB. Below that xray and vctl run the router out
# of memory, and the owner is left without internet.
MIN_RAM_KB="${VECTRA_MIN_RAM_KB:-196608}"
# opkg downloads into /tmp (RAM): the largest package, xray, is about 10 MB.
MIN_TMP_KB=24576
# Storage left over after the install, for configs, logs and updates.
MARGIN_KB=4096
# What Vectra's own update asks to find free on /overlay
# (internal/jobsafety, StorageOverlayFloorMB): below it, Connect's «Обновить»
# is refused until room is made.
UPDATE_FLOOR_KB=16384
# Official indexes carry no Installed-Size; an unpacked package takes about
# three times its download (a gzipped tar).
UNPACK_FACTOR=3
# TLS needs a clock later than this (2026-01-01).
CLOCK_FLOOR=1767225600
# The feeds (downloads.openwrt.org above all) go away for a minute now and
# then: on 2026-10-04 for about 40 s, and a router whose installer was fine was
# left with DNSMASQ_DEPENDENCY. A step that downloads is tried three times,
# this many seconds apart (40 s in all: the outage of that day). Whole
# seconds, and nothing else: anything else is the default ("0 0" for tests).
RETRY_DELAYS="$(printf '%s\n' "${VECTRA_RETRY_DELAYS:-10 30}" | awk 'NR > 1 { exit 1 } { for (i = 1; i <= NF; i++) { if ($i !~ /^[0-9]+$/) exit 1; printf "%s%d", (i > 1 ? " " : ""), $i } }')" && [ -n "$RETRY_DELAYS" ] || RETRY_DELAYS="10 30"
# All the pauses of one run together: a router whose feeds stay away is
# refused (or failed) as before, after two minutes at most, not after ten.
RETRY_BUDGET=120
# The longest the dnsmasq swap may leave the network without a dnsmasq that
# answers, in seconds: from the old one's stop to dnsmasq-full answering, or,
# if it does not, to the old one back (a third for dnsmasq-full, then the way
# back from the package, from the files, and dnsmasq run by hand).
DNS_DEADLINE="$(printf '%s\n' "${VECTRA_DNS_DEADLINE:-90}" | awk 'NR == 1 && /^[0-9]+$/ && $1 >= 15 { print $1 + 0 }')"
[ -n "$DNS_DEADLINE" ] || DNS_DEADLINE=90
DNS_T0=0

PKG=vectra-controller-pro
FEED_NAME=vectra_pro
CUSTOMFEEDS=/etc/opkg/customfeeds.conf
DISTFEEDS="${VECTRA_DISTFEEDS:-/etc/opkg/distfeeds.conf}"
LOG=/tmp/vectra-install.log
WORK=/tmp/vectra-install
KEYS=/etc/opkg/keys
SELF_COPY=/etc/vectra-controller-pro/vectra-install.sh
GEO_OWN=/usr/share/vectra-controller-pro/geo
# The dnsmasq the swap replaces, kept for the way back: on flash when there is
# room (a reboot does not lose it), in RAM otherwise. On a router the swap left
# broken it stays there for the owner.
SAVED_FLASH=/root/vectra-dnsmasq-saved
SAVED_RAM=/tmp/vectra-dnsmasq-saved
SAVED="$SAVED_RAM"
OPKG_INFO=/usr/lib/opkg/info
OPKG_STATUS=/usr/lib/opkg/status
# Where opkg keeps the feeds' lists, and its feed configuration.
LISTS="${VECTRA_OPKG_LISTS:-/var/opkg-lists}"
OPKG_CONFS="${VECTRA_OPKG_CONFS:-/etc/opkg.conf /etc/opkg/*.conf}"
# The old Vectra agent Vectra goes next to: one that knows it — it reads the
# router's identity Vectra seals, and does not start beside a running Vectra —
# from this release on, which installs this marker.
LEGACY_AGENT_MIN=0.1.13-r45
LEGACY_AGENT_MARKER="${VECTRA_LEGACY_AGENT_MARKER:-/usr/share/vectra-controller/vault-read-v1}"

# Other proxy stacks: two transparent proxies fight over the same traffic.
# PassWall2 is the one vctl knows how to take over (and give back).
CONFLICTS="podkop luci-app-podkop openclash luci-app-openclash homeproxy luci-app-homeproxy v2raya luci-app-v2raya nikki luci-app-nikki mihomo luci-app-mihomo ssclash luci-app-ssclash luci-app-passwall"
# DPI helpers do not proxy, but they rewrite the same traffic.
DPI_TOOLS="zapret luci-app-zapret youtubeUnblock luci-app-youtubeUnblock byedpi"

MODE=install
STANDBY=0
YES=0
FORCE=0
PURGE=0
JSON=0
# The warnings of this run, by code: an install that ends with any exits 2
# (--check: 0, they are advice). FAILURES: what the end's checks found
# broken where Vectra was to carry the traffic — the install exits 1.
WARNINGS=""
FAILURES=""
# In --check, the refusal --standby lifts (the old agent): said, and the rest
# checked as with --standby — one run says everything.
BLOCKER=""

# ----------------------------------------------------------------- output ----

# With --json the text goes to the log only, and stdout gets JSON lines.
say() {
	[ "$JSON" = 1 ] || printf '%s\n' "$*"
	printf '%s\n' "$*" >> "$LOG"
}
step() { say ""; say "==> $*"; }
ok() { say "    + $*"; }
note() { say "    - $*"; }
warn() { # <CODE> <message>: the run goes on, and ends with exit 2
	WARNINGS="$WARNINGS $1"
	say "    ! $2"
	jcheck warn "$1"
}
refuse() { # <CODE> <message>: nothing was changed
	say ""
	say "Установка не выполнена: $2"
	say "На роутере ничего не изменено. Подробности: $LOG"
	say "Итог: не установлено, $1 (код 1)."
	jcheck refuse "$1"
	result refused 1 "$1"
	cleanup
	exit 1
}
fail() { # <CODE> <message>: something was changed; say what state the router is in
	say ""
	say "ОШИБКА: $2"
	say "Подробности: $LOG"
	say "Итог: не установлено, $1 (код 1)."
	jcheck fail "$1"
	result failed 1 "$1"
	cleanup
	exit 1
}
broken() { # <CODE> <message>: the end's check failed — the run exits 1
	FAILURES="$FAILURES $1"
	say "    ! $2"
	jcheck fail "$1"
}
# conclude <what was done>: the summary, and the exit code it means — 1
# something the end's checks found broken; 2 with warnings (named by code);
# 0 nothing to warn of. --check's warnings are advice: it exits 0, naming
# them.
conclude() {
	say ""
	if [ -n "$FAILURES" ]; then
		say "Итог: $1, но не работает:$FAILURES${WARNINGS:+; предупреждения:$WARNINGS} (код 1). Что это значит — выше; подробности: $LOG"
		result failed 1 "${FAILURES# }"
		exit 1
	fi
	if [ -z "$WARNINGS" ]; then
		say "Итог: $1, без предупреждений (код 0)."
		result ok 0
		exit 0
	fi
	if [ "$MODE" = check ]; then
		say "Итог: $1, с замечаниями:$WARNINGS (код 0). Что они значат — выше."
		result ok 0
		exit 0
	fi
	say "Итог: $1, с предупреждениями:$WARNINGS (код 2). Что они значат — выше; подробности: $LOG"
	result warnings 2
	exit 2
}

# --json: one JSON object a line, ASCII only and short, so that each passes
# the panel's output filter whole — {"check":CODE,"level":ok|warn|refuse|fail}
# (with "value" where there is one) for what was found, and last
# {"result":ok|warnings|refused|failed,"exit":N,...}. The codes are the
# contract; the Russian text is in the log.
jline() {
	[ "$JSON" = 1 ] && printf '{%s}\n' "$1"
	return 0
}
jval() { printf '%s' "$1" | tr -cd ' -~' | cut -c 1-120 | sed 's/[\\"]/\\&/g'; }
jcheck() { # <level> <CODE> [value]
	if [ -n "${3:-}" ]; then
		jline "\"check\":\"$2\",\"level\":\"$1\",\"value\":\"$(jval "$3")\""
	else
		jline "\"check\":\"$2\",\"level\":\"$1\""
	fi
}
result() { # <result> <exit> [CODE]: the last line of --json
	w=""
	for c in $WARNINGS; do w="$w${w:+,}\"$c\""; done
	jline "\"result\":\"$1\",\"exit\":$2,\"mode\":\"$MODE\"${3:+,\"code\":\"$3\"},\"warnings\":[$w]"
}
run() { # a command whose output goes to the log only
	printf '$ %s\n' "$*" >> "$LOG"
	"$@" >> "$LOG" 2>&1
}
# retry <command...>: run (its output to the log), and run again (RETRY_DELAYS)
# while it fails on a download. Only for steps a second run takes up where the
# first stopped — opkg update, download, install, upgrade; fetch — never for
# one that changes the router otherwise (the dnsmasq swap: its remove, its
# installs and the way back, which need no network). opkg fails for many
# reasons, and only its "Failed to download" is the network: a signature, a
# package that does not unpack or a postinst that fails is said at once, not
# hidden behind a second try. opkg runs wget quiet, so its 404 and a CDN's 503
# read the same ("wget returned 8"): both are tried again — a package in a list
# just downloaded is rarely missing, and an outage often answers 5xx. fetch
# says the status: a 4xx is final, anything else (no connection, TLS, a
# timeout, a 5xx) is tried again.
# RT_UNTIL: a command that says the step got what this run needs from it even
# though it failed — opkg update fails whenever any feed does (a third party's
# that is down for good, too), and that is no reason to wait.
RT_UNTIL=""
RT_SPENT=0
retry() {
	rt_of=1
	for rt_wait in $RETRY_DELAYS; do rt_of=$((rt_of + 1)); done
	rt_n=1
	for rt_wait in $RETRY_DELAYS -; do
		rt_at=$(($(cat "$LOG" 2> /dev/null | wc -c)))
		printf '$ %s\n' "$*" >> "$LOG"
		"$@" >> "$LOG" 2>&1
		rt_rc=$?
		[ "$rt_rc" = 0 ] && return 0
		[ "$rt_wait" != - ] || return "$rt_rc"
		tail -c "+$((rt_at + 1))" "$LOG" > "$WORK/attempt.log"
		if [ "$1" = fetch ]; then
			grep -q -e 'HTTP error 4' "$WORK/attempt.log" && return "$rt_rc"
		else
			grep -q -e 'Failed to download' -e 'wget returned' "$WORK/attempt.log" || return "$rt_rc"
		fi
		[ -n "$RT_UNTIL" ] && $RT_UNTIL && return "$rt_rc"
		if [ $((RT_SPENT + rt_wait)) -gt "$RETRY_BUDGET" ]; then
			printf '# a download failed (exit %s); the %s s of retries are spent\n' "$rt_rc" "$RETRY_BUDGET" >> "$LOG"
			return "$rt_rc"
		fi
		RT_SPENT=$((RT_SPENT + rt_wait))
		rt_n=$((rt_n + 1))
		printf '# a download failed (exit %s): attempt %s/%s in %s s\n' "$rt_rc" "$rt_n" "$rt_of" "$rt_wait" >> "$LOG"
		note "Сервер пакетов не ответил — повторяю ($rt_n/$rt_of)…"
		sleep "$rt_wait"
	done
}

# ---------------------------------------------------------------- helpers ----

# a.b.c[-rN] compared numerically, part by part: 0 if $1 >= $2.
version_ge() {
	awk -v a="$1" -v b="$2" 'BEGIN {
		n = split(a, x, /[^0-9]+/); m = split(b, y, /[^0-9]+/)
		k = n > m ? n : m
		for (i = 1; i <= k; i++) {
			p = (i <= n && x[i] != "") ? x[i] + 0 : 0
			q = (i <= m && y[i] != "") ? y[i] + 0 : 0
			if (p > q) exit 0
			if (p < q) exit 1
		}
		exit 0
	}'
}

installed() { opkg list-installed "$1" 2>/dev/null | grep -q "^$1 - "; }
installed_version() { opkg list-installed "$1" 2>/dev/null | sed -n "s/^$1 - //p" | head -n 1; }
available() { opkg list "$1" 2>/dev/null | grep -q "^$1 - "; }
# have_all <pkg...>: each installed already, or in a feed's list.
have_all() {
	for hv in "$@"; do installed "$hv" || available "$hv" || return 1; done
}
field() { opkg info "$1" 2>/dev/null | sed -n "s/^$2: //p" | head -n 1; }
# A field of a package in the Vectra feed's own list (opkg info lists every
# feed's version of a package, in no order that says which is which). opkg keeps
# the list as it downloaded it: gzipped.
feed_field() {
	l="$LISTS/$FEED_NAME"
	{ zcat "$l" 2> /dev/null || cat "$l" 2> /dev/null; } | awk -v p="$1" -v f="$2" '
		/^Package: / { cur = substr($0, 10) }
		cur == p && index($0, f ": ") == 1 { print substr($0, length(f) + 3); exit }
	'
}

# pkg_candidates <pkg>: "<feed> <version> <filename> <sha256>" for every
# feed list that carries it, as opkg update downloaded them.
pkg_candidates() {
	for l in "$LISTS"/*; do
		[ -f "$l" ] || continue
		case "$l" in *.sig) continue ;; esac
		{ zcat "$l" 2> /dev/null || cat "$l" 2> /dev/null; } | awk -v p="$1" -v feed="${l##*/}" '
			/^Package: / { cur = substr($0, 10); v = f = s = "" }
			cur == p && /^Version: / { v = substr($0, 10) }
			cur == p && /^Filename: / { f = substr($0, 11) }
			cur == p && /^SHA256sum: / { s = substr($0, 12) }
			/^$/ { if (cur == p && v != "" && f != "") print feed, v, f, s; cur = "" }
			END { if (cur == p && v != "" && f != "") print feed, v, f, s }
		'
	done
}
# feed_url <name>: the URL of the feed opkg knows by that name.
# shellcheck disable=SC2086 # OPKG_CONFS is a list, and a glob
feed_url() { cat $OPKG_CONFS 2> /dev/null | awk -v n="$1" '($1 == "src/gz" || $1 == "src") && $2 == n { print $3; exit }'; }

free_kb() { df -k "$1" 2>/dev/null | awk 'NR == 2 { print $4; exit }'; }
# Where packages land: /overlay on a squashfs router, / otherwise.
storage_path() {
	if grep -q ' /overlay ' /proc/mounts 2>/dev/null; then echo /overlay; else echo /; fi
}

service_running() { [ -x "/etc/init.d/$1" ] && "/etc/init.d/$1" running > /dev/null 2>&1; }

wait_for() { # <seconds> <command...>
	n="$1"
	shift
	while [ "$n" -gt 0 ]; do
		"$@" > /dev/null 2>&1 && return 0
		sleep 1
		n=$((n - 1))
	done
	return 1
}

fetch() { # <url> <file>: uclient-fetch (wget) with TLS, as opkg itself fetches
	# Not quiet: its "HTTP error 404" in the log is what tells retry a 4xx.
	wget -T 20 -O "$2" "$1" >> "$LOG" 2>&1
}

xray_version() { xray version 2>/dev/null | awk 'NR == 1 { print $2; exit }'; }

lan_address() {
	a="$(uci -q get network.lan.ipaddr)"
	echo "${a%%/*}"
}

# --------------------------------------------------------------- cleanup ----

DISTFEEDS_SWAPPED=0
FEED_ADDED=0
KEY_ADDED=0
INSTALLED_SOMETHING=0
DNSMASQ_KEEP=0
SAVED_MADE=0
# The package that runs the router's dnsmasq (check_dnsmasq), the one the swap
# replaces: dnsmasq or dnsmasq-dhcpv6.
DNSMASQ_PKG=""
OLD=""

cleanup() {
	# OpenWrt's own feeds go back to their mirrors: the proxy was for this run.
	if [ "$DISTFEEDS_SWAPPED" = 1 ] && [ -f "$WORK/distfeeds.conf.orig" ]; then
		cp "$WORK/distfeeds.conf.orig" "$DISTFEEDS" && DISTFEEDS_SWAPPED=0
	fi
	# A run that installed nothing leaves no trace of the Vectra feed either.
	if [ "$INSTALLED_SOMETHING" = 0 ]; then
		[ "$FEED_ADDED" = 1 ] && remove_feed
		[ "$KEY_ADDED" = 1 ] && rm -f "$KEYS/$FEED_KEY_ID"
	fi
	rm -rf "$WORK/pkgs" "$WORK/attempt.log"
	# Only what this run kept: a copy an earlier run left for the owner stays.
	if [ "$SAVED_MADE" = 1 ] && [ "$DNSMASQ_KEEP" != 1 ]; then rm -rf "$SAVED_RAM" "$SAVED_FLASH"; fi
}

remove_feed() {
	[ -f "$CUSTOMFEEDS" ] || return 0
	grep -v "^src/gz $FEED_NAME " "$CUSTOMFEEDS" > "$WORK/customfeeds.conf" 2>/dev/null
	cat "$WORK/customfeeds.conf" > "$CUSTOMFEEDS"
}

# ---------------------------------------------------------------- checks ----

ARCH=""
RELEASE=""
STORE=""
TAKEOVER=""

check_router() {
	step "Роутер"
	[ "$(id -u 2>/dev/null)" = 0 ] || refuse NOT_ROOT "запустите установщик от root."
	[ -f /etc/openwrt_release ] || refuse NOT_OPENWRT "это не OpenWrt (нет /etc/openwrt_release)."
	# shellcheck disable=SC1091
	. /etc/openwrt_release
	RELEASE="${DISTRIB_RELEASE:-?}"
	ok "${DISTRIB_DESCRIPTION:-OpenWrt $RELEASE}, ${DISTRIB_TARGET:-?}"
	if ! command -v opkg > /dev/null 2>&1; then
		command -v apk > /dev/null 2>&1 && refuse OPENWRT_APK "OpenWrt $RELEASE использует apk. Эта версия установщика работает с opkg (OpenWrt 23.05 и 24.10); поддержка apk будет позже."
		refuse NO_OPKG "на роутере нет opkg."
	fi
	case "$RELEASE" in
	23.05* | 24.10*) ;;
	SNAPSHOT*) refuse OPENWRT_SNAPSHOT "это снапшот OpenWrt: поддерживаются выпуски 23.05 и 24.10." ;;
	*) refuse OPENWRT_RELEASE "OpenWrt $RELEASE не поддерживается: нужен 23.05 или 24.10 (nftables и dnsmasq с nftset)." ;;
	esac
	[ -x /sbin/fw4 ] || refuse NO_FIREWALL4 "нет firewall4 (nftables): Vectra работает только с ним."

	# The first architecture opkg prefers that the feed has.
	for a in $(opkg print-architecture 2>/dev/null | awk '$1 == "arch" && $2 != "all" && $2 != "noarch" { print $3, $2 }' | sort -rn | awk '{ print $2 }'); do
		for b in $ARCHS; do
			[ "$a" = "$b" ] && { ARCH="$a"; break 2; }
		done
	done
	[ -n "$ARCH" ] || refuse ARCH_UNSUPPORTED "архитектура роутера ($(opkg print-architecture 2>/dev/null | awk '$2 != "all" && $2 != "noarch" { printf "%s ", $2 }')) не поддерживается: для неё нет сборки Vectra."
	ok "архитектура $ARCH"

	mem="$(awk '/^MemTotal:/ { print $2; exit }' /proc/meminfo)"
	if [ "${mem:-0}" -lt "$MIN_RAM_KB" ]; then
		[ "$FORCE" = 1 ] || refuse RAM_TOO_LOW "оперативной памяти $((mem / 1024)) МБ, нужно не меньше $((MIN_RAM_KB / 1024)) МБ (роутер на 256 МБ): иначе xray и Vectra исчерпают память и интернет пропадёт."
		warn RAM_BELOW_FLOOR_FORCED "памяти $((mem / 1024)) МБ, ниже порога $((MIN_RAM_KB / 1024)) МБ — продолжаю по --force"
	else
		ok "память $((mem / 1024)) МБ"
	fi
	tmp="$(free_kb /tmp)"
	[ "${tmp:-0}" -ge "$MIN_TMP_KB" ] || refuse TMP_TOO_SMALL "в /tmp свободно $((${tmp:-0} / 1024)) МБ, нужно $((MIN_TMP_KB / 1024)) МБ для загрузки пакетов."
	STORE="$(storage_path)"
}

check_clock() {
	now="$(date +%s)"
	if [ "$now" -lt "$CLOCK_FLOOR" ]; then
		note "часы роутера отстают ($(date)); синхронизирую"
		for p in 0.openwrt.pool.ntp.org 1.openwrt.pool.ntp.org time.google.com; do
			run ntpd -n -q -p "$p" && break
		done
		now="$(date +%s)"
		[ "$now" -ge "$CLOCK_FLOOR" ] || refuse CLOCK_UNSYNCED "часы роутера показывают $(date) и не синхронизируются по NTP: без верного времени не работает TLS."
	fi
	ok "часы: $(date)"
}

# check_legacy_agent: the old Vectra agent (vectra-controller-agent) runs
# the router. Vectra goes next to it, off (--standby): two controllers would
# check in to the panel as the same router and take the same traffic; `vectra
# on` later stops and disables the agent and PassWall2, noting whom `vectra
# off` gives the traffic back to. And only next to an agent that knows Vectra
# (LEGACY_AGENT_MIN).
check_legacy_agent() {
	av="$(installed_version vectra-controller-agent)"
	if [ ! -f "$LEGACY_AGENT_MARKER" ] && ! version_ge "${av:-0}" "$LEGACY_AGENT_MIN"; then
		refuse LEGACY_AGENT_TOO_OLD "на роутере прежний агент Vectra vectra-controller-agent ${av:-?}, а Vectra встаёт только рядом с $LEGACY_AGENT_MIN или новее. Более старый не знает о Vectra: его сторож может запустить его рядом с работающей Vectra, и он не прочитает личность роутера, которую Vectra хранит запечатанной — после vectra off он не связался бы с панелью. Сначала обновите агента (панель: обновление контроллера, канал stable), затем запустите установщик снова."
	fi
	if [ "$STANDBY" = 1 ]; then
		ok "прежний агент Vectra $av: Vectra встанет рядом с ним выключенной"
		jcheck ok LEGACY_AGENT "$av"
		return 0
	fi
	why="на роутере работает прежний агент Vectra (vectra-controller-agent $av), поэтому нужен --standby: Vectra встанет рядом с ним выключенной, а он и PassWall2 продолжат работать. Без --standby на роутере оказались бы два контроллера: они отмечались бы в панели как один и тот же роутер и забирали бы один и тот же трафик. Переключение — потом, командой vectra on: она остановит агента и PassWall2 и запомнит, кому вернуть трафик (vectra off). Установка: sh $0 --standby"
	[ "$MODE" = check ] || refuse LEGACY_AGENT_NEEDS_STANDBY "$why"
	BLOCKER=LEGACY_AGENT_NEEDS_STANDBY
	say "    ! $why"
	jcheck refuse LEGACY_AGENT_NEEDS_STANDBY "$av"
	note "остальное проверяю так, как для --standby"
	STANDBY=1
}

check_conflicts() {
	step "Другие прокси на роутере"
	installed vectra-controller-agent && check_legacy_agent
	found=""
	for p in $CONFLICTS; do installed "$p" && found="$found $p"; done
	for s in sing-box xray; do
		# The package alone is harmless: OpenWrt's xray-core and sing-box enable
		# an init script that starts nothing until configured (PassWall and
		# podkop run the binary themselves). A service that runs is not.
		service_running "$s" && found="$found $s(служба)"
	done
	[ -z "$found" ] || refuse PROXY_CONFLICT "установлено:$found. Два прозрачных прокси на одном роутере мешают друг другу. Удалите или выключите их и запустите установщик снова."
	for p in $DPI_TOOLS; do
		installed "$p" && warn DPI_TOOL "$p установлен: он меняет тот же трафик; если сайты перестанут открываться, выключите его"
	done
	if [ "$STANDBY" = 1 ]; then
		ok "режим --standby: Vectra встанет выключенной, трафик никто не забирает"
	elif installed luci-app-passwall2 || [ -x /etc/init.d/passwall2 ]; then
		TAKEOVER=passwall2
		jcheck ok PASSWALL_TAKEOVER
		say "    PassWall2: Vectra заберёт у него трафик (PassWall2 остановится)."
		say "    Вернуть ему трафик: vectra off."
		say "    До привязки к аккаунту Vectra она поведёт трафик по маршрутам"
		say "    PassWall2, через VPN; после привязки — по настройкам Vectra."
		say "    Через сутки работы VPN Vectra удалит PassWall2 сама"
		say "    (настройки сохранятся в /etc/vectra-controller-pro/backup);"
		say "    после этого vectra off оставит интернет без VPN."
		if [ "$MODE" = check ]; then
			note "--check ничего не меняет; установка спросит, забирать ли трафик (или --yes)"
		elif [ "$YES" != 1 ]; then
			[ "$JSON" != 1 ] && [ -t 0 ] || refuse PASSWALL_NEEDS_YES "на роутере PassWall2. Запустите с --yes, если Vectra должна забрать у него трафик."
			printf '    Продолжить? [y/N] '
			read -r answer
			case "$answer" in y | Y | yes | д | Д | да) ;; *) refuse CANCELLED "отменено." ;; esac
		fi
	else
		ok "не найдено"
	fi
}

# check_dnsmasq: the dnsmasq the swap will replace, before anything changes —
# which package runs it (dnsmasq or dnsmasq-dhcpv6; another one the installer
# does not take apart), and that the owner runs it: enabled and running. A
# router whose DNS and DHCP another program does (AdGuard Home, dnsmasq
# switched off) is refused, not given a dnsmasq the owner turned off.
check_dnsmasq() {
	installed dnsmasq-full && return 0
	step "DNS и DHCP роутера"
	DNSMASQ_PKG=""
	for p in dnsmasq dnsmasq-dhcpv6; do
		installed "$p" && { DNSMASQ_PKG="$p"; break; }
	done
	if [ -z "$DNSMASQ_PKG" ]; then
		owner="$(opkg search /usr/sbin/dnsmasq 2> /dev/null | sed -n 's/ - .*//p' | head -n 1)"
		[ -n "$owner" ] && refuse DNSMASQ_UNKNOWN_PACKAGE "dnsmasq на роутере из пакета $owner: установщик заменяет на dnsmasq-full только dnsmasq и dnsmasq-dhcpv6. Замените его на dnsmasq-full сами и запустите установщик снова."
		[ -e /usr/sbin/dnsmasq ] && refuse DNSMASQ_UNKNOWN_PACKAGE "dnsmasq на роутере поставлен не пакетом: установщик не знает, как вернуть его, если dnsmasq-full не заработает. Поставьте dnsmasq-full сами и запустите установщик снова."
		refuse DNSMASQ_ABSENT "на роутере нет dnsmasq: DNS и DHCP делает другая программа. Vectra работает через dnsmasq-full как DNS роутера, а ставить его рядом с чужим DNS/DHCP установщик не будет."
	fi
	if ! /etc/init.d/dnsmasq enabled > /dev/null 2>&1 || ! /etc/init.d/dnsmasq running > /dev/null 2>&1; then
		refuse DNSMASQ_NOT_ACTIVE "$DNSMASQ_PKG выключен (не запущен или убран из автозапуска): DNS и DHCP, видимо, делает другая программа (например, AdGuard Home). Vectra работает через dnsmasq-full как DNS роутера, а установщик не включает то, что выключил владелец."
	fi
	ok "$DNSMASQ_PKG $(installed_version "$DNSMASQ_PKG") работает; его заменит dnsmasq-full"
	jcheck ok DNSMASQ "$DNSMASQ_PKG"
}

# ----------------------------------------------------------------- feeds ----

add_feed() {
	step "Фиды"
	[ -n "$FEED_URL" ] && [ -n "$FEED_KEY" ] && [ -n "$FEED_KEY_ID" ] || refuse NO_FEED_KEY "этот файл — шаблон без ключа фида. Скачайте установщик: wget -O /tmp/vectra https://router.vectra-pro.net/install && sh /tmp/vectra"
	mkdir -p "$KEYS" "$WORK"
	if [ -f "$KEYS/$FEED_KEY_ID" ]; then
		[ "$(sed -n 2p "$KEYS/$FEED_KEY_ID")" = "$FEED_KEY" ] || refuse FEED_KEY_CLASH "в $KEYS уже лежит другой ключ с номером $FEED_KEY_ID."
	else
		printf 'untrusted comment: Vectra Pro feed\n%s\n' "$FEED_KEY" > "$KEYS/$FEED_KEY_ID"
		KEY_ADDED=1
	fi
	line="src/gz $FEED_NAME $FEED_URL/$ARCH"
	touch "$CUSTOMFEEDS"
	if ! grep -qx "$line" "$CUSTOMFEEDS"; then
		remove_feed
		echo "$line" >> "$CUSTOMFEEDS"
		FEED_ADDED=1
	fi
	fetch "$FEED_URL/$ARCH/Packages.sig" "$WORK/Packages.sig" || refuse FEED_UNREACHABLE "фид Vectra недоступен ($FEED_URL). Проверьте интернет и DNS роутера: nslookup ${FEED_URL#*://}"

	note "opkg update (до минуты)"
	# What this run takes from OpenWrt's feeds.
	OPENWRT_NEED="dnsmasq-full kmod-nft-tproxy kmod-nft-socket kmod-nft-nat"
	[ "$LUCI" = 1 ] && OPENWRT_NEED="$OPENWRT_NEED luci"
	RT_UNTIL=feeds_ready
	retry opkg update
	RT_UNTIL=""
	# Vectra's feed must be there, and verified: opkg drops a list whose
	# signature does not check out.
	available "$PKG" || refuse FEED_UNVERIFIED "фид Vectra не прошёл проверку подписи или пуст (opkg update, см. лог)."
	ok "фид Vectra $FEED_URL/$ARCH, подпись ключом $FEED_KEY_ID"
	# shellcheck disable=SC2086 # a list of packages
	if ! have_all $OPENWRT_NEED; then
		# Every OpenWrt feed answered and a package is still missing: the
		# mirror would not have it either.
		[ -n "$(openwrt_feeds_down)" ] || openwrt_missing
		use_openwrt_mirror || { [ -n "$(openwrt_feeds_down)" ] || openwrt_missing; refuse OPENWRT_FEEDS_UNREACHABLE "фиды OpenWrt недоступны (не скачались списки:$(openwrt_feeds_down)) и прокси Vectra тоже. Проверьте DNS и IPv6 на WAN."; }
	fi
	ok "фиды OpenWrt"
}

use_openwrt_mirror() {
	[ -f "$DISTFEEDS" ] && grep -q 'downloads.openwrt.org' "$DISTFEEDS" || return 1
	mirror="$OPENWRT_MIRROR"
	[ -n "$mirror" ] || mirror="$(echo "$FEED_URL" | sed -n 's#^\(https\{0,1\}://[^/]*\)/.*#\1#p')/openwrt-cache"
	note "downloads.openwrt.org недоступен; беру фиды OpenWrt через $mirror"
	cp "$DISTFEEDS" "$WORK/distfeeds.conf.orig" || return 1
	DISTFEEDS_SWAPPED=1
	sed -e "s#https\{0,1\}://downloads.openwrt.org#$mirror#g" "$WORK/distfeeds.conf.orig" > "$DISTFEEDS" || return 1
	RT_UNTIL=openwrt_ready
	retry opkg update
	RT_UNTIL=""
	openwrt_ready
}

# After an opkg update that failed: is what this run needs there? Vectra's
# feed, and OpenWrt's either whole or not there at all — none of it is what a
# router that cannot reach downloads.openwrt.org sees, and the mirror is the
# next step for that, not a wait (a Cudy behind a broken IPv6).
# shellcheck disable=SC2086 # a list of packages
openwrt_ready() { have_all $OPENWRT_NEED; }
# The OpenWrt feeds (distfeeds.conf) whose list opkg update did not get.
openwrt_feeds_down() {
	[ -f "$DISTFEEDS" ] || return 0
	for fd in $(awk '($1 == "src/gz" || $1 == "src") { print $2 }' "$DISTFEEDS"); do
		[ -s "$LISTS/$fd" ] || printf ' %s' "$fd"
	done
}
# openwrt_missing: OpenWrt's feeds answered, and what this run needs is not in
# them — a kmod built for another kernel (a firmware built by hand, a
# snapshot), not an outage.
# shellcheck disable=SC2086 # a list of packages
openwrt_missing() {
	om=""
	for p in $OPENWRT_NEED; do installed "$p" || available "$p" || om="$om $p"; done
	refuse OPENWRT_PACKAGE_MISSING "в фидах OpenWrt нет нужных Vectra пакетов:$om. Фиды ответили — пакетов в них нет; обычно это модули ядра (kmod) для прошивки, собранной не официально, или снапшота. Нужна официальная прошивка OpenWrt 23.05 или 24.10."
}
feeds_ready() {
	have_all "$PKG" || return 1
	have_all dnsmasq-full || return 0
	openwrt_ready
}

# ------------------------------------------------------------------ plan ----

# xray on the router that is not its package's — PassWall2's, swapped in by
# hand (the fleet's onboarding puts a newer xray there than any feed of its
# time): no xray-core package at all, or one whose version is not the
# binary's. Vectra's package depends on xray-core (>= minimum), and opkg
# satisfies that with an xray-core of any feed written over the binary
# PassWall2 runs — an older one on the router of 2026-10-03 (26.3.27 over
# 26.7.28). So, where opkg would write one — no package, or one older than
# the minimum — the xray-core of the binary's own version, from whichever feed
# has it, goes in first (install_xray_pin): the same xray, now its package.
# With none, nothing is installed, and the binary is left as it is. A package
# at the minimum or newer satisfies the dependency: opkg leaves it, and the
# binary over it, alone.
XRAY_PIN=""
XRAY_FREED_KB=0
plan_xray() {
	xb="$(command -v xray 2> /dev/null)"
	[ -n "$xb" ] || return 0
	pv="$(installed_version xray-core)"
	xv="$(xray_version)"
	if [ -n "$pv" ]; then
		# The package's own binary (or one that does not answer: verify says
		# so): opkg's business, as before.
		[ -z "$xv" ] || [ "$(echo "$pv" | sed 's/-r\{0,1\}[0-9]*$//')" = "$xv" ] && return 0
		step "xray $xv поверх пакета xray-core $pv"
		if version_ge "$pv" "$XRAY_MIN"; then
			note "xray заменён вручную; пакет xray-core $pv не старше нужного Vectra $XRAY_MIN, и opkg его не тронет: xray остаётся как есть"
			jcheck ok XRAY_HAND_SWAPPED_KEPT "$xv"
			return 0
		fi
	else
		step "xray без пакета"
	fi
	[ -n "$xv" ] || refuse XRAY_BINARY_BROKEN "на роутере есть $xb без пакета xray-core, и он не запускается (xray version). Установщик не заменяет xray, поставленный вручную: почините или удалите его и запустите установщик снова."
	version_ge "$xv" "$XRAY_MIN" || refuse XRAY_BINARY_TOO_OLD "на роутере $xb $xv${pv:+ (поверх пакета xray-core $pv)}, а Vectra нужен xray $XRAY_MIN или новее. Установщик не заменяет xray, поставленный вручную: обновите его пакетом xray-core и запустите установщик снова."
	XRAY_PIN="$(pkg_candidates xray-core | awk -v v="$xv" '{ u = $2; sub(/-r?[0-9]+$/, "", u); if (u == v) { print; exit } }')"
	[ -n "$XRAY_PIN" ] || refuse XRAY_NO_SAME_VERSION "на роутере $xb $xv, поставленный вручную (обычно для PassWall2)${pv:+ поверх пакета xray-core $pv, который старше нужного Vectra $XRAY_MIN}, а ни в одном фиде нет пакета xray-core $xv. Пакет Vectra зависит от xray-core, и opkg поставил бы поверх другую версию — установщик этого не делает, xray не тронут. Поставьте пакет xray-core $xv (opkg install ./xray-core_$xv-r1_$ARCH.ipk) и запустите установщик снова."
	# The package's binary takes the place of this one (with no package:
	# its space is the binary's).
	[ -n "$pv" ] || XRAY_FREED_KB="$(du -k "$xb" 2> /dev/null | awk '{ print $1; exit }')"
	# shellcheck disable=SC2086 # the plan's fields
	set -- $XRAY_PIN
	# Only what its feed's signed list vouches for: a list with no SHA256sum
	# for it leaves nothing to check the download against.
	[ -n "${4:-}" ] || refuse XRAY_PIN_CHECKSUM "в списке фида $1 нет SHA256 для xray-core $2: установщик не ставит то, что нечем проверить; xray не тронут."
	ok "xray $xv: сначала встанет xray-core $2 из фида $1 — та же версия"
	jcheck ok XRAY_PIN "$2"
}

check_storage() {
	step "Место"
	need=0
	# Vectra's own packages state their Installed-Size; the rest is estimated.
	for p in $PKG xray-core vectra-geodata vectra-reporter; do
		v="$(feed_field "$p" Version)"
		iv="$(installed_version "$p")"
		[ -n "$iv" ] && version_ge "$iv" "$v" && continue
		s="$(feed_field "$p" Installed-Size)"
		need=$((need + ${s:-0} / 1024))
	done
	for p in dnsmasq-full kmod-nft-tproxy kmod-nft-socket kmod-nft-nat rpcd-mod-iwinfo procd-ujail jsonfilter jshn ca-bundle luci-base; do
		installed "$p" && continue
		s="$(field "$p" Size)"
		need=$((need + ${s:-0} * UNPACK_FACTOR / 1024))
	done
	[ "$LUCI" = 1 ] && need=$((need + 2048))
	need=$((need - ${XRAY_FREED_KB:-0}))
	[ "$need" -ge 0 ] || need=0
	have="$(free_kb "$STORE")"
	if [ "${have:-0}" -lt $((need + MARGIN_KB)) ]; then
		refuse STORAGE_TOO_SMALL "на $STORE свободно $((${have:-0} / 1024)) МБ, нужно около $(((need + MARGIN_KB) / 1024)) МБ (xray — самый большой пакет, около 30 МБ)."
	fi
	ok "нужно около $((need / 1024)) МБ, свободно $((have / 1024)) МБ на $STORE"
	jcheck ok STORAGE "need_mb=$((need / 1024)) free_mb=$((have / 1024))"
	after=$((have - need))
	if [ "$after" -lt "$UPDATE_FLOOR_KB" ]; then
		warn OVERLAY_AFTER_BELOW_UPDATE_FLOOR "после установки на $STORE останется около $((after / 1024)) МБ — меньше $((UPDATE_FLOOR_KB / 1024)) МБ, которых требует обновление Vectra: «Обновить» будет отказывать, пока место не освободится (например, когда Vectra удалит PassWall2 — через сутки после включения)"
	fi
}

# --------------------------------------------------------------- install ----

# install_xray_pin installs the xray-core plan_xray chose. Downloaded and
# checked against its feed's list before anything changes: a download that
# fails changes nothing.
install_xray_pin() {
	[ -n "$XRAY_PIN" ] || return 0
	# shellcheck disable=SC2086 # the plan's fields
	set -- $XRAY_PIN
	step "xray-core $2: пакет той же версии, что уже стоит"
	base="$(feed_url "$1")"
	[ -n "$base" ] || refuse XRAY_PIN_DOWNLOAD "не найден адрес фида $1, где лежит xray-core $2."
	rm -rf "$WORK/pkgs"
	mkdir -p "$WORK/pkgs"
	f="$WORK/pkgs/${3##*/}"
	retry fetch "$base/$3" "$f" || refuse XRAY_PIN_DOWNLOAD "не удалось скачать xray-core $2 ($base/$3)."
	if [ "$(sha256sum "$f" 2> /dev/null | awk '{ print $1 }')" != "$4" ]; then
		refuse XRAY_PIN_CHECKSUM "xray-core $2 скачался не тот: sha256 не совпадает со списком фида $1."
	fi
	INSTALLED_SOMETHING=1
	retry opkg install "$f" || fail XRAY_PIN_INSTALL "opkg install xray-core $2 не прошёл (см. лог). Vectra не установлена."
	ok "xray-core $(installed_version xray-core): теперь xray — пакет, и его версия остаётся прежней"
}

# The swap of dnsmasq for dnsmasq-full, and its way back, which needs no
# network. Before anything is removed, the dnsmasq that runs now is kept twice:
# - its package, with everything it depends on already on the router, so opkg
#   can put it back;
# - its files as they are on the router, with opkg's record of them, put back
#   by hand when opkg cannot.
# It goes on flash when there is room, so the way back survives a reboot.
# "Works" is checked, not assumed. Each start is of the system dnsmasq only,
# afresh, with no leftover of it holding port 53, and it must answer
# (DNS_CHECK). Otherwise the way back is taken and checked the same way. From
# the moment the old dnsmasq stops to a dnsmasq that answers, everything
# shares one deadline (DNS_DEADLINE): one try per stage, each with its share.
swap_dnsmasq() {
	installed dnsmasq-full && { ok "dnsmasq-full уже стоит"; return 0; }
	OLD="$DNSMASQ_PKG"
	[ -n "$OLD" ] || fail DNSMASQ_ABSENT "не найден пакет dnsmasq, который нужно заменить (проверка не выполнялась). dnsmasq не тронут."
	step "$OLD -> dnsmasq-full"
	rm -rf "$WORK/pkgs"
	mkdir -p "$WORK/pkgs"
	# Its libraries first: they do not clash with dnsmasq.
	for d in $(depends_of "$(field dnsmasq-full Depends)"); do
		installed "$d" || retry opkg install "$d" || fail DNSMASQ_DEPENDENCY "не удалось поставить $d (зависимость dnsmasq-full). $OLD не тронут."
	done
	INSTALLED_SOMETHING=1
	download dnsmasq-full || fail DNSMASQ_DOWNLOAD "не удалось скачать dnsmasq-full. $OLD не тронут."
	full=""
	for f in "$WORK"/pkgs/dnsmasq-full_*.ipk; do [ -f "$f" ] && full="$f"; done
	[ -n "$full" ] || fail DNSMASQ_DOWNLOAD "dnsmasq-full не скачался. $OLD не тронут."
	download "$OLD" || fail DNSMASQ_DOWNLOAD "не удалось скачать $OLD (для отката). $OLD не тронут."
	old=""
	for f in "$WORK/pkgs/${OLD}"_*.ipk; do [ -f "$f" ] && old="$f"; done
	[ -n "$old" ] || fail DNSMASQ_DOWNLOAD "$OLD (для отката) не скачался. $OLD не тронут."
	# Putting it back must not need the network: what it depends on is on
	# the router before anything is removed. A package whose control cannot
	# be read (or names no dependency: dnsmasq needs at least libc) is not
	# one to count on.
	deps="$(ipk_field "$old" Depends)"
	[ -n "$deps" ] || fail DNSMASQ_DOWNLOAD "скачанный $OLD (для отката) не читается или пуст (нет Depends): откат на него не гарантирован. $OLD не тронут."
	for d in $(depends_of "$deps"); do
		installed "$d" || retry opkg install "$d" || fail DNSMASQ_DEPENDENCY "не удалось поставить $d (зависимость $OLD, для отката). $OLD не тронут."
	done
	[ "$(ipk_field "$old" Version)" = "$(installed_version "$OLD")" ] ||
		printf '# the feed has %s %s, the router %s: the way back puts the feed'"'"'s back first, the router'"'"'s files if that fails\n' "$OLD" "$(ipk_field "$old" Version)" "$(installed_version "$OLD")" >> "$LOG"
	save_dnsmasq "$old" || fail DNSMASQ_DOWNLOAD "не удалось сохранить прежний $OLD для отката (место в /tmp?). $OLD не тронут."
	old="$SAVED/${old##*/}"
	note "прежний $OLD $(installed_version "$OLD") сохранён для отката: $SAVED"
	[ -f /etc/config/dhcp ] && cp /etc/config/dhcp "$WORK/dhcp.before"
	# What "works" means here is what worked before: a dnsmasq that answered
	# must answer again; one that only hands out addresses (another DNS server
	# on the router) must run again.
	DNS_CHECK=dnsmasq_runs
	dns_answers && DNS_CHECK=dns_answers

	DNS_T0="$(date +%s)"
	if ! run opkg remove "$OLD"; then
		installed "$OLD" && dnsmasq_up "$(dns_by 1 3)" && fail DNSMASQ_REMOVE "opkg remove $OLD не прошёл; $OLD работает как прежде."
		dnsmasq_back
	fi
	if run opkg install "$full" && restore_dhcp && dnsmasq_up "$(dns_by 1 3)"; then
		ok "dnsmasq-full $(installed_version dnsmasq-full): DHCP и DNS работают (без DNS $(($(date +%s) - DNS_T0)) с)"
		return 0
	fi
	say "    ! dnsmasq-full не поднялся; возвращаю прежний $OLD"
	dnsmasq_back
}

# dnsmasq_back: the way back, and the end of the run. Said loudly when even it
# leaves the router without the dnsmasq it had.
dnsmasq_back() {
	if rollback_dnsmasq; then
		fail DNSMASQ_FULL_ROLLED_BACK "dnsmasq-full не заработал; прежний $OLD возвращён и работает (без DNS было $(($(date +%s) - DNS_T0)) с). Vectra не установлена."
	fi
	DNSMASQ_KEEP=1
	where="$SAVED"
	case "$SAVED" in /tmp/*) where="$SAVED — в памяти: после перезагрузки его не будет, сохраните заранее" ;; esac
	if dnsmasq_rescue; then
		fail DNSMASQ_FULL_BROKEN "dnsmasq-full не заработал, и прежний $OLD не запускается как служба. Запущен временный dnsmasq: DHCP и DNS работают, но только до перезагрузки роутера. Подключитесь кабелем и выполните: kill \$(cat /var/run/dnsmasq-rescue.pid); opkg install --force-reinstall $old && /etc/init.d/dnsmasq enable && /etc/init.d/dnsmasq restart (пакет и файлы прежнего dnsmasq: $where)"
	fi
	fail DNSMASQ_FULL_BROKEN "dnsmasq-full не заработал, и прежний $OLD не вернулся: у устройств в сети может не быть DHCP/DNS. Подключитесь кабелем и выполните: opkg install --force-reinstall $old && /etc/init.d/dnsmasq enable && /etc/init.d/dnsmasq restart (пакет и файлы прежнего dnsmasq: $where)"
}

# rollback_dnsmasq: dnsmasq-full gone, and the dnsmasq that was there back and
# answering, from its package or else from its files. No network. It starts
# again what it stops: the old dnsmasq is always there to put back (the
# checks refused a router without one).
rollback_dnsmasq() {
	dnsmasq_stop
	installed dnsmasq-full && { run opkg remove dnsmasq-full || run opkg remove --force-depends dnsmasq-full; }
	run opkg install "$old" && restore_dhcp && dnsmasq_up "$(dns_by 2 3)" && return 0
	say "    ! opkg не вернул прежний $OLD; восстанавливаю его файлы, сохранённые до замены"
	restore_saved_dnsmasq && restore_dhcp && dnsmasq_up "$(dns_by 5 6)"
}

restore_dhcp() {
	if [ -f "$WORK/dhcp.before" ]; then
		cp "$WORK/dhcp.before" /etc/config/dhcp
	fi
	rm -f /etc/config/dhcp-opkg
	return 0
}

# download <pkg>: opkg download into $WORK/pkgs. Run in this shell, not a
# subshell, so retry's pauses count against this run's budget. Then back to
# the directory it ran from ($0 may be relative to it).
download() {
	dl_from="$(pwd)"
	cd "$WORK/pkgs" || return 1
	retry opkg download "$1"
	dl_rc=$?
	cd "$dl_from" || cd /
	return "$dl_rc"
}

# The dependencies of a package, by name: "a, b (>= 1)" -> a b. libc is
# always there, and opkg lists it under another name on some releases.
depends_of() { echo "$1" | tr ',' '\n' | sed 's/ *(.*//; s/^ *//; s/ *$//' | grep -v -e '^libc$' -e '^$'; }
# A field of a downloaded package's control.
ipk_field() { tar -xzOf "$1" ./control.tar.gz 2> /dev/null | tar -xzOf - ./control 2> /dev/null | sed -n "s/^$2: //p" | head -n 1; }

# save_dnsmasq <ipk>: the package for the way back, and the installed $OLD's
# files as they are now with opkg's record of it. Built in /tmp, then moved to
# flash ($SAVED_FLASH) when there is room, so it survives a reboot.
save_dnsmasq() {
	SAVED_MADE=1
	rm -rf "$SAVED_RAM" "$SAVED_FLASH"
	SAVED="$SAVED_RAM"
	mkdir -p "$SAVED" && [ -f "$OPKG_INFO/$OLD.list" ] && cp "$1" "$SAVED/" || return 1
	{
		cat "$OPKG_INFO/$OLD.list"
		ls "$OPKG_INFO/$OLD".*
	} | while read -r f; do
		if [ -e "$f" ] || [ -L "$f" ]; then echo "${f#/}"; fi
	done > "$SAVED/files"
	grep -qx 'usr/sbin/dnsmasq' "$SAVED/files" && tar -czf "$SAVED/files.tar.gz" -C / -T "$SAVED/files" || return 1
	awk -v p="$OLD" '/^Package: / { k = ($2 == p) } k' "$OPKG_STATUS" > "$SAVED/status"
	grep -qx "Package: $OLD" "$SAVED/status" || return 1
	sv_kb="$(du -sk "$SAVED" | awk '{ print $1; exit }')"
	if [ "$(free_kb "${SAVED_FLASH%/*}")" -ge $((${sv_kb:-0} + MARGIN_KB)) ] 2> /dev/null &&
		mkdir -p "$SAVED_FLASH" && cp -a "$SAVED/." "$SAVED_FLASH/"; then
		rm -rf "$SAVED"
		SAVED="$SAVED_FLASH"
	else
		rm -rf "$SAVED_FLASH"
		printf '# no room on flash for the way back (%s KB): kept in RAM only\n' "$sv_kb" >> "$LOG"
	fi
	return 0
}

# restore_saved_dnsmasq: what save_dnsmasq kept, back in place, under opkg's
# own record of it. For when opkg cannot put the package back.
restore_saved_dnsmasq() {
	[ -s "$SAVED/files.tar.gz" ] && [ -s "$SAVED/status" ] || return 1
	dnsmasq_stop
	installed dnsmasq-full && run opkg remove --force-depends dnsmasq-full
	installed "$OLD" && run opkg remove --force-depends "$OLD"
	run tar -xzf "$SAVED/files.tar.gz" -C / || return 1
	grep -qx "Package: $OLD" "$OPKG_STATUS" || { echo; cat "$SAVED/status"; } >> "$OPKG_STATUS"
}

# The system dnsmasq's processes: the instances procd runs for /etc/config/dhcp
# (each reads the configuration its init script writes, -C
# /var/etc/dnsmasq.conf.<section>; the jail wrapper around one says so too),
# and the one dnsmasq_rescue starts with that file. Not PassWall2's copies,
# which read their own files under /tmp/etc/passwall2. By name and command
# line, not `pgrep -x dnsmasq`: BusyBox's pgrep -x matches argv[0],
# "/usr/sbin/dnsmasq" as procd starts it, and finds only the jail wrapper or
# an init script that happens to run. A process that has exited and is not
# yet reaped does not count.
dnsmasq_pids() {
	for p in $(pidof dnsmasq); do
		case "$(cut -d ' ' -f 3 "/proc/$p/stat" 2> /dev/null)" in "" | Z | X) continue ;; esac
		tr '\0' ' ' < "/proc/$p/cmdline" 2> /dev/null | grep -q -e ' -C /var/etc/dnsmasq\.conf\.' && echo "$p"
	done
}
dnsmasq_runs() { [ -n "$(dnsmasq_pids)" ]; }
no_dnsmasq() { ! dnsmasq_runs; }
# dnsmasq answers from /etc/hosts itself: no upstream (a WAN whose DNS is down)
# can make this fail. An answer has a Name: line; the Server: header alone,
# printed even when nothing answers, does not count. A second at most per try.
dns_answers() {
	dnsmasq_runs || return 1
	nslookup -timeout=1 -retry=1 localhost 127.0.0.1 2> /dev/null | grep -q '^Name:'
}

# dns_by <num> <den>: the time (date +%s) a stage of the swap has until: its
# share of DNS_DEADLINE from the moment the old dnsmasq stopped, and at least
# 5 s from now.
dns_by() {
	db=$((DNS_T0 + DNS_DEADLINE * $1 / $2))
	dn=$(($(date +%s) + 5))
	[ "$db" -ge "$dn" ] || db="$dn"
	echo "$db"
}
# until_time <time> <command...>: the command, once a second, until it holds or
# the time comes.
until_time() {
	ut="$1"
	shift
	while :; do
		"$@" > /dev/null 2>&1 && return 0
		[ "$(date +%s)" -lt "$ut" ] || return 1
		sleep 1
	done
}

# dnsmasq_stop: procd's dnsmasq stopped, and none of the system dnsmasq left
# holding port 53. One procd no longer tracks (its jail gone before it) would
# keep every dnsmasq started after it from answering.
dnsmasq_stop() {
	[ -x /etc/init.d/dnsmasq ] && run /etc/init.d/dnsmasq stop
	wait_for 5 no_dnsmasq && return 0
	printf '# dnsmasq still runs after its stop (%s): killed\n' "$(dnsmasq_pids | tr '\n' ' ')" >> "$LOG"
	for p in $(dnsmasq_pids); do kill "$p" 2> /dev/null; done
	wait_for 2 no_dnsmasq && return 0
	for p in $(dnsmasq_pids); do kill -9 "$p" 2> /dev/null; done
	wait_for 1 no_dnsmasq
}

# dnsmasq_up <time>: the dnsmasq on the router started afresh by its init
# script, and answering (DNS_CHECK) by that time. Enabled again: opkg's
# remove disabled it, and the checks refused a router whose owner had.
dnsmasq_up() {
	[ -x /etc/init.d/dnsmasq ] || { dns_diag "нет /etc/init.d/dnsmasq"; return 1; }
	run /etc/init.d/dnsmasq enable
	dnsmasq_stop
	run /etc/init.d/dnsmasq start
	until_time "$1" "$DNS_CHECK" && return 0
	dns_diag "dnsmasq не ответил ($DNS_CHECK) за $(($(date +%s) - DNS_T0)) с от остановки прежнего"
	return 1
}

# dnsmasq_rescue: the last resort, the dnsmasq binary run straight, outside
# procd and its jail, with the configuration its init script wrote last.
# Until a reboot.
dnsmasq_rescue() {
	[ -x /usr/sbin/dnsmasq ] || return 1
	c=""
	for f in /var/etc/dnsmasq.conf.*; do [ -f "$f" ] && c="$f" && break; done
	[ -n "$c" ] || return 1
	say "    ! запускаю dnsmasq вручную (до перезагрузки), чтобы в сети были DHCP и DNS"
	dnsmasq_stop
	run /usr/sbin/dnsmasq -C "$c" -x /var/run/dnsmasq-rescue.pid && until_time "$(dns_by 1 1)" "$DNS_CHECK"
}

# dns_diag <what>: DNS on the router as it is now, into the log. What an owner
# (and support) needs to see why dnsmasq does not answer.
dns_diag() {
	{
		printf '# ---- %s: dnsmasq now\n' "$1"
		opkg list-installed 'dnsmasq*'
		ps w | grep -e '[d]nsmasq' -e '[u]jail'
		netstat -lnup | grep ':53 '
		netstat -lntp | grep ':53 '
		ubus call service list '{"name":"dnsmasq"}'
		nslookup -timeout=1 -retry=1 localhost 127.0.0.1
		logread | tail -n 40
		printf '# ----\n'
	} >> "$LOG" 2>&1
}

LUCI=0
install_packages() {
	if [ "$LUCI" = 1 ]; then
		step "LuCI (веб-интерфейс роутера)"
		retry opkg install luci || fail LUCI_INSTALL "не удалось поставить LuCI."
		INSTALLED_SOMETHING=1
		ok "LuCI $(installed_version luci-base)"
	fi
	step "Vectra"
	was="$(installed_version "$PKG")"
	INSTALLED_SOMETHING=1
	# --standby: the package's postinst neither enables nor starts it — on an
	# upgrade of a copy already there as much as on a first install — and it is
	# off in UCI too, so neither a reboot nor procd's config trigger brings it up
	# before `vectra on`.
	[ "$STANDBY" = 1 ] && export VECTRA_SKIP_POSTINST_RESTART=1
	if [ -n "$was" ]; then
		note "обновляю $was -> $(feed_field "$PKG" Version)"
		# Not xray-core: on a router with PassWall2 it is PassWall's too (often
		# with a binary swapped in by hand), and `opkg upgrade` would take the
		# newest from ANY feed. The package's own Depends, xray-core (>= the
		# minimum), upgrades it only when it is too old.
		retry opkg upgrade "$PKG" vectra-geodata vectra-reporter
		installed vectra-geodata || retry opkg install vectra-geodata
		installed vectra-reporter || retry opkg install vectra-reporter
	fi
	pkgs="$PKG"
	if [ -z "$was" ]; then
		# vectra-geodata and vectra-reporter by name on a first install too,
		# not only through the package's Depends: that Depends is the pro
		# feed's (build-pro-feed.sh), not the SDK Makefile's — a feed whose
		# index predates it, or a package built otherwise, leaves the geo data
		# out, and xray refuses every route by country and service (the router
		# of 2026-10-03). Named where the feed has them.
		for p in vectra-geodata vectra-reporter; do available "$p" && pkgs="$pkgs $p"; done
	fi
	# shellcheck disable=SC2086 # a list of packages
	retry opkg install $pkgs || fail PKG_INSTALL "opkg install $pkgs не прошёл (см. лог). Интернет роутера работает как прежде."
	if [ "$STANDBY" = 1 ]; then
		unset VECTRA_SKIP_POSTINST_RESTART
		run uci set "$PKG.main.enabled=0"
		# A router that already runs (PassWall2, the old agent) is set up: the
		# wizard does not open by itself — its Wi-Fi boost would retune a home
		# network that works. It stays one click away in the page's footer.
		# (The package's own config ships setup_done '0'.)
		[ "$(uci -q get "$PKG.main.setup_done")" = 1 ] || run uci set "$PKG.main.setup_done=1"
		run uci commit "$PKG"
		run "/etc/init.d/$PKG" disable
	fi
	ok "$PKG $(installed_version "$PKG"), xray-core $(installed_version xray-core), vectra-geodata $(installed_version vectra-geodata)"
}

# The geo data xray reads: Vectra's own (vectra-geodata, $GEO_OWN) unless uci
# geo_asset_dir names another directory — the fleet's old /usr/share/v2ray,
# PassWall's packages', reads as Vectra's own, as vctl reads it. It must hold
# every category the provider routes by; another directory that does not
# gets Vectra's own instead, and its files stay untouched.
geo_dir() {
	d="$(uci -q get "$PKG.main.geo_asset_dir")"
	case "${d%/}" in
	"" | /usr/share/v2ray) echo "$GEO_OWN" ;;
	*) echo "${d%/}" ;;
	esac
}
geo_ok() { [ -f "$GEO_OWN/check.json" ] && run env XRAY_LOCATION_ASSET="$1" xray run -test -c "$GEO_OWN/check.json"; }
setup_geo() {
	d="$(geo_dir)"
	geo_ok "$d" && return 0
	[ "$d" != "$GEO_OWN" ] && geo_ok "$GEO_OWN" || return 0
	run uci set "$PKG.main.geo_asset_dir=$GEO_OWN"
	run uci commit "$PKG"
	note "гео-данные в $d не подходят маршрутам Vectra (их поставил другой пакет); Vectra берёт свои из $GEO_OWN"
}

# ----------------------------------------------------------------- check ----

# verify checks the end; what is wrong is a warning (the run exits 2).
verify() {
	step "Проверка"
	v="$(installed_version "$PKG")"
	# The version in the feed's own list, which opkg just installed from — not
	# the one this file was signed with (VERSION): a feed published since, or
	# an installer older than its feed, said "в фиде 0.6.0-r36" next to
	# 0.7.0-r13 on the router of 2026-10-03.
	fv="$(feed_field "$PKG" Version)"
	if [ -n "$fv" ] && [ "$v" != "$fv" ]; then
		warn PKG_VERSION_MISMATCH "установлена версия $v, а в фиде Vectra $fv"
	else
		ok "$PKG $v${fv:+ — последняя в фиде}"
		jcheck ok PKG_VERSION "$v"
	fi
	if [ -n "$VERSION" ] && [ -n "$fv" ] && [ "$VERSION" != "$fv" ]; then
		note "этот установщик собран при выпуске $VERSION; фид с тех пор отдаёт $fv — поставлено то, что в фиде"
	fi
	xv="$(xray_version)"
	if [ -n "$xv" ] && version_ge "$xv" "$XRAY_MIN"; then
		ok "xray $xv"
	else
		warn XRAY_TOO_OLD "xray ${xv:-не запускается}, нужен $XRAY_MIN или новее"
	fi
	if geo_ok "$(geo_dir)"; then
		ok "гео-данные ($(geo_dir)): все категории маршрутов на месте"
	else
		warn GEO_DATA_REJECTED "xray не принимает гео-данные в $(geo_dir): маршруты по странам и сервисам не заработают (opkg install vectra-geodata)"
	fi
	if [ "$STANDBY" = 1 ]; then
		if "/etc/init.d/$PKG" running > /dev/null 2>&1; then
			warn SERVICE_RUNNING_IN_STANDBY "служба $PKG запущена, хотя должна стоять выключенной"
		else
			ok "служба $PKG выключена, как и задумано"
		fi
	elif wait_for 30 "/etc/init.d/$PKG" running; then
		ok "служба $PKG запущена"
	else
		# Vectra was to carry the traffic (a takeover: what carried it is
		# stopped): not running is not done.
		broken SERVICE_NOT_RUNNING "служба $PKG не запустилась: logread -e vctl"
	fi
	if wait_for 20 ubus call vectra status; then
		ok "ubus: vectra отвечает"
	elif [ "$STANDBY" = 1 ]; then
		warn UBUS_NOT_ANSWERING "ubus-объект vectra не отвечает (rpcd): /etc/init.d/rpcd restart"
	else
		broken UBUS_NOT_ANSWERING "ubus-объект vectra не отвечает (rpcd): /etc/init.d/rpcd restart — ни vectra status, ни страница роутера не работают"
	fi
	if [ -f /www/luci-static/resources/view/vectra/app.js ] && wait_for 10 fetch http://127.0.0.1/luci-static/vectra/vectra-app.js "$WORK/app.js"; then
		ok "LuCI отдаёт страницу Vectra"
	else
		warn LUCI_PAGE_MISSING "LuCI не отдаёт страницу Vectra: /etc/init.d/uhttpd restart"
	fi
	have="$(free_kb "$STORE")"
	if [ "${have:-0}" -lt "$UPDATE_FLOOR_KB" ]; then
		warn OVERLAY_BELOW_UPDATE_FLOOR "на $STORE свободно $((${have:-0} / 1024)) МБ — меньше $((UPDATE_FLOOR_KB / 1024)) МБ, которых требует обновление Vectra: «Обновить» будет отказывать, пока место не освободится (например, когда Vectra удалит PassWall2 — через сутки после включения)"
	else
		ok "на $STORE свободно $((have / 1024)) МБ: обновлению Vectra хватит (нужно $((UPDATE_FLOOR_KB / 1024)) МБ)"
	fi
}

finish() {
	ip="$(lan_address)"
	say ""
	if [ "$STANDBY" = 1 ]; then
		say "Vectra установлена и выключена: трафик идёт как раньше."
		if [ -x /usr/sbin/vectra ]; then
			say "  Включить: vectra on    Выключить: vectra off    Состояние: vectra status"
		else
			say "  Включить: uci set $PKG.main.enabled=1; uci commit $PKG; /etc/init.d/$PKG enable; /etc/init.d/$PKG start"
			say "  Выключить: /etc/init.d/$PKG stop; /etc/init.d/$PKG disable"
		fi
		say "  Интерфейс: http://${ip:-192.168.1.1}/cgi-bin/luci/admin/vectra"
		if [ -f "$0" ] && mkdir -p /etc/vectra-controller-pro && cp "$0" "$SELF_COPY" 2>/dev/null; then
			say "  Удалить: sh $SELF_COPY --uninstall"
		fi
		return 0
	fi
	say "Vectra установлена."
	say "  Откройте http://${ip:-192.168.1.1}/cgi-bin/luci/admin/vectra — мастер настройки"
	say "  проверит интернет, прокачает Wi-Fi и привяжет роутер к аккаунту Vectra."
	# The claim code, when the router already reached Vectra.
	code=""
	i=0
	while [ $i -lt 20 ]; do
		code="$(ubus call vectra setup 2>/dev/null | jsonfilter -q -e '@.vectra.claim.code' 2>/dev/null)"
		[ -n "$code" ] && break
		sleep 1
		i=$((i + 1))
	done
	if [ -n "$code" ]; then
		say "  Код привязки: $code (в приложении Vectra: Роутер -> Привязать)"
		link="$(ubus call vectra setup 2>/dev/null | jsonfilter -q -e '@.vectra.claim.botUrl' 2>/dev/null)"
		[ -n "$link" ] && say "  Ссылка: $link"
		say "  Код и ссылка — в vectra status, пока роутер не привязан."
	fi
	if [ "$TAKEOVER" = passwall2 ]; then
		say "  PassWall2 остановлен. Вернуть ему трафик: vectra off."
		say "  До привязки Vectra ведёт трафик по маршрутам PassWall2, через VPN."
		say "  Через сутки работы VPN Vectra удалит PassWall2 сама"
		say "  (настройки сохранятся в /etc/vectra-controller-pro/backup);"
		say "  после этого vectra off оставит интернет без VPN."
	fi
	if [ -f "$0" ] && mkdir -p /etc/vectra-controller-pro && cp "$0" "$SELF_COPY" 2>/dev/null; then
		say "  Удалить: sh $SELF_COPY --uninstall"
	fi
}

# ------------------------------------------------------------- uninstall ----

uninstall() {
	step "Удаление Vectra"
	[ "$(id -u 2>/dev/null)" = 0 ] || refuse NOT_ROOT "запустите от root."
	installed "$PKG" || note "$PKG не установлен"
	if installed "$PKG"; then
		# prerm stops the service: the data plane is unloaded and PassWall2, if
		# Vectra took it over and has not removed it since, is given back.
		run opkg remove --autoremove "$PKG" || fail PKG_REMOVE "opkg remove $PKG не прошёл."
		ok "$PKG удалён; служба остановлена"
	fi
	installed vectra-geodata && run opkg remove vectra-geodata
	installed vectra-reporter && run opkg remove vectra-reporter
	installed xray-core && note "xray-core остаётся: он был на роутере до Vectra или нужен другому пакету"
	mkdir -p "$WORK"
	remove_feed
	[ -n "$FEED_KEY_ID" ] && rm -f "$KEYS/$FEED_KEY_ID"
	ok "фид Vectra убран из opkg"
	if [ "$PURGE" = 1 ]; then
		rm -rf /etc/vectra-controller-pro /etc/config/vectra-controller-pro
		ok "настройки и ключ устройства Vectra удалены"
	else
		note "настройки и ключ устройства остались в /etc/vectra-controller-pro (удалить: --uninstall --purge)"
	fi
	note "dnsmasq-full остаётся: он заменяет dnsmasq полностью"
	say ""
	say "Vectra удалена."
	result ok 0
}

# ------------------------------------------------------------------ main ----

main() {
	while [ $# -gt 0 ]; do
		case "$1" in
		--check) MODE=check ;;
		--standby) STANDBY=1 ;;
		--uninstall) MODE=uninstall ;;
		--purge) PURGE=1 ;;
		--yes | -y) YES=1 ;;
		--force) FORCE=1 ;;
		--json) JSON=1 ;;
		-h | --help)
			sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'
			exit 0
			;;
		*)
			echo "неизвестный параметр: $1 (см. --help)"
			exit 3
			;;
		esac
		shift
	done
	: > "$LOG"
	mkdir -p "$WORK"
	say "Vectra для OpenWrt${VERSION:+ $VERSION} — $(date)"
	jline "\"installer\":\"$(jval "${VERSION:-template}")\",\"mode\":\"$MODE\",\"standby\":$STANDBY"

	if [ "$MODE" = uninstall ]; then
		uninstall
		exit 0
	fi

	check_router
	check_clock
	check_conflicts
	check_dnsmasq
	installed luci-base || LUCI=1
	add_feed
	plan_xray
	check_storage

	if [ "$MODE" = check ]; then
		cleanup
		say ""
		if [ -n "$BLOCKER" ]; then
			say "Остальные проверки пройдены, но без --standby установка не пойдёт (почему — выше). Ничего не изменено."
			say "Итог: не установлено, $BLOCKER (код 1)."
			result refused 1 "$BLOCKER"
			exit 1
		fi
		say "Проверки пройдены: Vectra можно ставить на этот роутер. Ничего не изменено."
		conclude "проверки пройдены"
	fi

	install_xray_pin
	swap_dnsmasq
	install_packages
	setup_geo
	cleanup
	verify
	finish
	conclude "Vectra установлена"
}

# Sourced by the tests for its functions; run otherwise.
[ -n "${VECTRA_INSTALL_LIB:-}" ] || main "$@"
