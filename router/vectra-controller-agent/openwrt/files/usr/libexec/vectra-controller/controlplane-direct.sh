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
# job is: at the `output` hook, BEFORE PassWall2's chain, `accept` any packet
# carrying that mark.
#
#   table inet vectra_controlplane {
#     chain output_direct {
#       type route hook output priority -160; policy accept;
#       meta mark 0x564354 counter accept
#     }
#   }
#
# ORDERING / WHY IT BEATS PASSWALL2 (verified against nftables.sh):
#   * PassWall2 creates `chain mangle_output { type route hook output
#     priority mangle - 1; ... }` i.e. priority -151
#     (passwall2/.../root/usr/share/passwall2/nftables.sh:139-141).
#   * nftables evaluates all base chains registered on the SAME hook in
#     ASCENDING priority order. Our chain at priority -160 runs strictly
#     BEFORE PassWall2's -151 chain.
#   * An `accept` verdict issued from a base chain TERMINATES evaluation of the
#     remaining base chains on that hook for this packet. So once we accept a
#     marked packet, PassWall2's `mangle_output` (which would `meta mark set
#     0x50535732` and divert it via `ip rule ... table 999`) never runs.
#   * The packet therefore keeps a clean mark, misses the tproxy `ip rule`, and
#     is routed by the MAIN table out the WAN = DIRECT egress. This is exactly
#     the same effect PassWall2 grants its own internal bypass sentinel
#     (`meta mark 255 counter return`, nftables.sh:781), but in our own table
#     and with our own mark so we never depend on a PassWall2 magic constant.
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
			meta mark $VECTRA_CONTROL_PLANE_FWMARK counter accept
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
		log "applied control-plane direct carve-out (mark $VECTRA_CONTROL_PLANE_FWMARK)"
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
		write_fw_include
		apply
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
		echo "usage: $0 {apply|remove|status|write_include}" >&2
		exit 2
		;;
esac
