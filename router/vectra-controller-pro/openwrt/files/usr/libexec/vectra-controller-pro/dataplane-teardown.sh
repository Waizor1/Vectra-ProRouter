#!/bin/sh
#
# Unload the vctl data plane: the `inet vctl` nftables table and the fwmark
# policy route/rule pair that `vctl firewall routing` installs.
#
# WHY THIS EXISTS
#
# Nothing used to run firewall.RevertCommands when vctl stopped. The table and
# the policy route stayed loaded with no xray listening behind them, which is
# strictly worse than having never installed them:
#
#   1. The router's OWN egress dies. The output chain still ends in
#      `meta l4proto { tcp, udp } ... meta mark set 0x<fwmark>`, and the policy
#      route resolves that mark to `local 0.0.0.0/0 dev lo table 100`, so
#      locally-generated packets never leave the box. The legacy
#      vectra-controller-agent we hand the router back to sets no SO_MARK
#      (nothing in router/vectra-controller-agent references SO_MARK/SocketMark),
#      so the CtlMark exemption does not cover it: the controller we just
#      restored cannot reach the panel.
#   2. With the kill switch on, the forward chain's terminal drop survives and
#      the LAN goes dark.
#
# WHY IT IS SHELL
#
# The stop path has to work when the Go binary is missing, mid-upgrade, or
# broken — an `opkg remove` deletes /usr/sbin/vctl, and a crash-looping vctl is
# exactly when an operator reaches for `stop`. Nothing here needs vctl.
#
# It is idempotent: every command is best-effort, so running it on an already
# clean router is a no-op.

set -u

# The table name and routing table id are vctl constants (firewall.DefaultSpec):
# they are not operator-settable anywhere, so hardcoding them cannot drift.
TABLE="vctl"
RT_TABLE="100"

# The fwmark IS operator-settable — it comes from inbounds.tproxy.fwmark of the
# desired config the panel delivers — so it must be read, not assumed.
DEFAULT_DESIRED_CONFIG="/etc/vectra-controller-pro/xray-desired.json"

desired_config_path() {
	local path=""
	if command -v uci >/dev/null 2>&1; then
		path="$(uci -q get vectra-controller-pro.main.xray_config_path 2>/dev/null)" || path=""
	fi
	[ -n "$path" ] || path="$DEFAULT_DESIRED_CONFIG"
	printf '%s' "$path"
}

# 1 is the fallback everywhere else in the controller when the operator left it
# unset (config.ApplyDefaults and firewallSpecFromConfig both use it), so an
# unreadable or absent config tears down what the controller would have built.
tproxy_fwmark() {
	local config mark=""
	config="$(desired_config_path)"
	if [ -r "$config" ] && command -v jsonfilter >/dev/null 2>&1; then
		mark="$(jsonfilter -i "$config" -e '@.inbounds.tproxy.fwmark' 2>/dev/null)" || mark=""
	fi
	case "$mark" in
	'' | 0 | *[!0-9]*) mark=1 ;;
	esac
	printf '%s' "$mark"
}

# `vctl firewall routing` is best-effort and re-runs on every apply, so
# duplicate ip rules can accumulate. Delete until the kernel says there are no
# more, bounded so a misbehaving `ip` cannot spin here forever.
delete_rules() {
	local family="$1" mark="$2" i=0
	while [ "$i" -lt 8 ]; do
		ip $family rule del fwmark "$mark" lookup "$RT_TABLE" 2>/dev/null || break
		i=$((i + 1))
	done
}

MARK="$(tproxy_fwmark)"
HEXMARK="$(printf '0x%x' "$MARK")"

# The policy route is ours only while our table is. PassWall before its own
# fwmark (and PassWall v1) install the very same pair — `fwmark 0x1 lookup 100`
# and `local 0.0.0.0/0 dev lo table 100` — and a vctl that never loaded its
# data plane (installed next to them, off) is stopped on every upgrade and
# removal: deleting the pair then took THEIR transparent proxy down. Without
# our table nothing marks a packet for that route, so leaving it is harmless.
if ! nft -t list table inet "$TABLE" > /dev/null 2>&1; then
	logger -t vectra-controller-pro "no table inet $TABLE: no data plane of ours to unload; fwmark $HEXMARK / table $RT_TABLE left as they are" 2>/dev/null || true
	exit 0
fi

nft flush table inet "$TABLE" 2>/dev/null || true
nft delete table inet "$TABLE" 2>/dev/null || true

delete_rules "" "$HEXMARK"
ip route del local 0.0.0.0/0 dev lo table "$RT_TABLE" 2>/dev/null || true

delete_rules "-6" "$HEXMARK"
ip -6 route del local ::/0 dev lo table "$RT_TABLE" 2>/dev/null || true

logger -t vectra-controller-pro "data plane unloaded (table inet $TABLE, fwmark $HEXMARK, table $RT_TABLE)" 2>/dev/null || true

exit 0
