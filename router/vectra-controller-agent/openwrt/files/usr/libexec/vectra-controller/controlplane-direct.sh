#!/bin/sh
#
# controlplane-direct.sh - force the Vectra controller agent's own traffic
# DIRECT, bypassing the PassWall2 transparent proxy (tproxy).
#
# WHY THIS EXISTS
# ---------------
# The controller agent runs ON the router. Its outbound HTTPS check-in to the
# panel (api.vectra-pro.net) is locally-generated OUTPUT-hook traffic. PassWall2,
# when local-proxy is enabled, hijacks that traffic exactly like a LAN client:
# it stamps the packet with its tproxy fwmark (0x50535732) in the `mangle_output`
# base chain, and `ip rule fwmark 0x50535732 table 999` then routes it to the
# loopback tproxy listener. If the active shunt's default_node is a dead proxy,
# the check-in is blackholed and the router strands permanently (NAT, no inbound
# path). The #1 operator requirement is that the control plane ALWAYS reaches the
# panel, so we carve control-plane traffic out of the tproxy unconditionally.
#
# HOW THE CARVE-OUT WORKS
# -----------------------
# The Go agent stamps SO_MARK = VECTRA_CONTROL_PLANE_FWMARK on its own sockets
# (it reads the value from config.json `control_plane_fwmark`, rendered by
# render-config.sh). This script installs a dedicated nftables table whose ONLY
# job is: at the `output` hook, BEFORE PassWall2's chain, re-stamp any packet
# carrying that mark with PassWall2's own local-output bypass mark (0xff).
#
#   table inet vectra_controlplane {
#     chain output_direct {
#       type route hook output priority -160; policy accept;
#       meta mark 0x564354 counter meta mark set 0xff accept
#     }
#   }
#
# WHY A HAND-OFF AND NOT A PLAIN `accept`:
#   * An `accept` verdict is NOT final across base chains. nftables keeps
#     evaluating every later-priority base chain registered on the same hook
#     ("If a packet is accepted and there is another chain, bearing the same
#     hook type and with a later priority, then the packet will subsequently
#     traverse this other chain" - wiki.nftables.org, Configuring chains). Only
#     `drop` is final. So accepting at -160 never kept PassWall2's
#     `mangle_output` (priority mangle - 1 = -151) from stamping its tproxy mark.
#   * PassWall2 itself exempts locally-generated packets carrying mark 0xff (the
#     mark xray stamps on its own outbound sockets) as the first non-address
#     rule of EVERY output chain, before any jump to PSW2_RULE or REDIRECT:
#       26.4.10 .. 26.8.10  `meta mark 255 counter return`
#       26.9.x              `meta mark and 0xff == 0xff counter return`
#     in PSW2_OUTPUT_MANGLE, PSW2_OUTPUT_MANGLE_V6 and (redirect mode)
#     PSW2_OUTPUT_NAT. Exactly 0xff satisfies both forms.
#   * Our chain runs first (-160 < -151), and because it is a `type route`
#     chain the kernel re-routes the packet after the mark changes. The packet
#     reaches PassWall2 already carrying 0xff, PassWall2 returns it unmarked by
#     its tproxy mark, no `ip rule fwmark` matches, and the MAIN table sends it
#     out the WAN = DIRECT egress - the same path xray's own traffic takes.
#   * We keep our own distinctive mark on the socket (and the counter on it) so
#     the agent never depends on the PassWall2 constant directly; the coupling
#     lives in this one rule (VECTRA_PASSWALL_BYPASS_MARK).
#
# DELIBERATELY a SEPARATE table (`inet vectra_controlplane`), NOT a rule grafted
# into `inet passwall2`:
#   * `passwall2 restart` runs `nftables.sh stop` (del_firewall_rule) then
#     `start` (add_firewall_rule). `del_firewall_rule` only flushes the
#     `inet passwall2` table / PSW2_* chains (nftables.sh:989-1023). Our separate
#     table is left intact, so the carve-out SURVIVES a PassWall2 restart with no
#     re-assertion needed. A grafted rule would be wiped on every restart.
#   * A full `fw4 reload` tears down ALL nftables tables. For that case the
#     uci-default registers this script as a fw4 firewall include (script type),
#     so fw4 re-runs `apply` on every reload and rebuilds our table - the same
#     persistence mechanism PassWall2 itself uses (firewall.passwall2 include).
#
# SAFETY
#   * Requires only `nft`, which is always present on OpenWrt 24.10 / fw4.
#   * Does NOT require PassWall2 to be installed or running; the table stands
#     alone and is harmless when no tproxy is active (a mark nobody sets just
#     never matches).
#   * Idempotent: `apply` atomically replaces the whole table, so repeated runs
#     never stack duplicate rules.
#
# POSIX/ash only. No bashisms.

set -u

# Mark MUST match render-config.sh `control_plane_fwmark` default and the value
# the Go agent passes to SO_MARK. 0x564354 = ASCII "VCT" (Vectra ConTroller).
# Chosen to NOT collide with PassWall2's marks: 0x50535732 (tproxy "PSW2") and
# 0xff/255 (PassWall2's local-output bypass sentinel). Distinctive high value in
# an otherwise unused range.
VECTRA_CONTROL_PLANE_FWMARK="${VECTRA_CONTROL_PLANE_FWMARK:-0x564354}"

# PassWall2's local-output bypass mark (see "WHY A HAND-OFF" above). Must stay
# exactly 0xff: 26.4-26.8 compare `meta mark 255`, 26.9 compares the low byte.
VECTRA_PASSWALL_BYPASS_MARK="${VECTRA_PASSWALL_BYPASS_MARK:-0xff}"

DISABLED_PATH="${DISABLED_PATH:-/etc/vectra-controller/controlplane-direct.disabled}"

NFT_TABLE="inet vectra_controlplane"
NFT_BIN="${NFT_BIN:-nft}"
LOG_TAG="vectra-controlplane-direct"
# fw4 firewall include target. fw4 sources this on every (re)load.
FW_INCLUDE_PATH="${FW_INCLUDE_PATH:-/var/etc/vectra-controlplane.include}"
# Canonical install path of THIS script, baked into the generated fw4 include so
# the include re-invokes the carve-out regardless of how fw4 sources it.
SELF_PATH="${SELF_PATH:-/usr/libexec/vectra-controller/controlplane-direct.sh}"

log() {
	if command -v logger >/dev/null 2>&1; then
		logger -t "$LOG_TAG" "$1"
	fi
}

have_nft() {
	command -v "$NFT_BIN" >/dev/null 2>&1
}

# Emit the full table definition. `flush table` inside the same atomic ruleset
# makes `apply` idempotent: if the table already exists it is emptied first; if
# not, the `add table` line creates it and the `flush` is a no-op. Everything is
# fed to a single `nft -f -` so it lands atomically.
render_ruleset() {
	cat <<-EOF
	add table $NFT_TABLE
	flush table $NFT_TABLE
	table $NFT_TABLE {
		chain output_direct {
			type route hook output priority -160; policy accept;
			meta mark $VECTRA_CONTROL_PLANE_FWMARK counter meta mark set $VECTRA_PASSWALL_BYPASS_MARK accept
		}
	}
	EOF
}

apply() {
	if ! have_nft; then
		log "nft not found; skipping control-plane direct carve-out"
		return 0
	fi
	if render_ruleset | "$NFT_BIN" -f - 2>/dev/null; then
		log "applied control-plane direct carve-out (mark $VECTRA_CONTROL_PLANE_FWMARK -> $VECTRA_PASSWALL_BYPASS_MARK)"
		return 0
	fi
	log "failed to apply control-plane direct carve-out"
	return 1
}

remove() {
	have_nft || return 0
	"$NFT_BIN" delete table $NFT_TABLE 2>/dev/null || true
	log "removed control-plane direct carve-out"
	return 0
}

# `remove` is temporary: boot/reload hooks intentionally rebuild the table.
# `disable` is the persistent rollback. Keep package-owned hooks in place;
# automatic apply becomes a no-op until uci-defaults/reinstall calls enable.
disable() {
	mkdir -p "$(dirname "$DISABLED_PATH")" || return 1
	(umask 077; : > "$DISABLED_PATH") || return 1
	if have_nft && "$NFT_BIN" list table $NFT_TABLE >/dev/null 2>&1; then
		"$NFT_BIN" delete table $NFT_TABLE || return 1
	fi
	if command -v uci >/dev/null 2>&1 &&
		[ "$(uci -q get firewall.vectra_controlplane.type)" = "script" ] &&
		[ "$(uci -q get firewall.vectra_controlplane.path)" = "$FW_INCLUDE_PATH" ]; then
		uci -q delete firewall.vectra_controlplane || return 1
		uci -q commit firewall || return 1
	fi
	# Never remove a file the owner replaced with their own include.
	if [ -f "$FW_INCLUDE_PATH" ] &&
		grep -qF "# Auto-generated by $LOG_TAG." "$FW_INCLUDE_PATH"; then
		rm -f "$FW_INCLUDE_PATH" || return 1
	fi
	log "disabled control-plane direct carve-out persistently"
}

enable() {
	rm -f "$DISABLED_PATH" || return 1
	write_fw_include
	apply
}

status() {
	have_nft || { echo "nft-unavailable"; return 0; }
	if "$NFT_BIN" list table $NFT_TABLE >/dev/null 2>&1; then
		"$NFT_BIN" list table $NFT_TABLE
		return 0
	fi
	echo "absent"
	return 1
}

# Write the fw4 include script that re-applies the carve-out on every firewall
# reload. The include simply re-invokes this script's apply. Writing it is cheap
# and idempotent.
write_fw_include() {
	mkdir -p "$(dirname "$FW_INCLUDE_PATH")" 2>/dev/null || true
	cat <<-EOF >"$FW_INCLUDE_PATH"
	#!/bin/sh
	# Auto-generated by $LOG_TAG. Re-applies the control-plane DIRECT carve-out
	# whenever fw4 reloads the ruleset. Do not edit by hand.
	[ -x "$SELF_PATH" ] && "$SELF_PATH" apply >/dev/null 2>&1
	exit 0
	EOF
	chmod 0755 "$FW_INCLUDE_PATH" 2>/dev/null || true
}

arg="${1:-apply}"
case "$arg" in
	apply)
		[ ! -f "$DISABLED_PATH" ] || exit 0
		write_fw_include
		apply
		;;
	disable)
		disable
		;;
	enable)
		enable
		;;
	remove)
		remove
		;;
	status)
		status
		;;
	write_include)
		write_fw_include
		;;
	*)
		echo "usage: $0 {apply|remove|disable|enable|status|write_include}" >&2
		exit 2
		;;
esac
