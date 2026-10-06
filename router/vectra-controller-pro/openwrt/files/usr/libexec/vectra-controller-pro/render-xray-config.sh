#!/bin/sh

set -e

# OpenWrt helper libraries rely on several optional globals and are not nounset-safe.
IPKG_INSTROOT="${IPKG_INSTROOT-}"

. /lib/functions.sh
. /usr/share/libubox/jshn.sh

# Renders the vctl DAEMON config (agentcfg.Config JSON) from UCI + board state.
# Field names and types MUST match internal/agentcfg/agentcfg.go exactly:
#   controlUrl, panelUrl, statePath, statusPath, xrayConfigPath,
#   providerConfigPath, xrayRenderPath, xrayBinary, geoAssetDir,
#   legacyStatePath (strings), controlFallbackIps (strings, UCI list control_ip),
#   pollIntervalSeconds, requestTimeoutSeconds, claimRotateSeconds (ints),
#   jobSafety{ heavyMemoryFloorMb, ... (ints), preDropCaches (bool) }.
OUTPUT_PATH="${1:-/var/run/vectra-controller-pro/agent.json}"
CONFIG_NAME="vectra-controller-pro"
SECTION="main"
BOARD_JSON="$(ubus call system board 2>/dev/null || true)"

uci_get_or_default() {
	local option="$1"
	local fallback="${2:-}"
	local value
	config_get value "$SECTION" "$option" "$fallback"
	printf '%s' "$value"
}

# Normalize a UCI value to a non-negative integer; empty/garbage -> default.
int_or_default() {
	local value="$1"
	local fallback="$2"
	case "$value" in
	'' | *[!0-9]*)
		printf '%s' "$fallback"
		;;
	*)
		printf '%s' "$value"
		;;
	esac
}

config_load "$CONFIG_NAME"

control_url="$(uci_get_or_default control_url https://api.vectra-pro.net)"
panel_url="$(uci_get_or_default panel_url https://router.vectra-pro.net)"
if [ -z "$control_url" ]; then
	control_url="${panel_url:-https://api.vectra-pro.net}"
fi
if [ -z "$panel_url" ]; then
	panel_url="$control_url"
fi

control_ips=""
config_get control_ips "$SECTION" control_ip ""

poll_interval="$(int_or_default "$(uci_get_or_default poll_interval 45)" 45)"
request_timeout="$(int_or_default "$(uci_get_or_default request_timeout 10)" 10)"

state_path="$(uci_get_or_default state_path /etc/vectra-controller-pro/state.json)"
status_path="$(uci_get_or_default status_path /var/run/vectra-controller-pro/status.json)"
xray_config_path="$(uci_get_or_default xray_config_path /etc/vectra-controller-pro/xray-desired.json)"
# The PROVIDER document lives in its own file: it is a complete Xray config
# fetched by the router, not an operator config, and is stored byte-for-byte.
provider_config_path="$(uci_get_or_default provider_config_path /etc/vectra-controller-pro/provider-config.json)"
xray_render_path="$(uci_get_or_default xray_render_path /var/run/vectra-controller-pro/xray.json)"
xray_binary="$(uci_get_or_default xray_binary /usr/sbin/vctl-xray-wrapper)"
# Exported to xray as XRAY_LOCATION_ASSET: vctl's own geo data
# (vectra-geodata) unless another directory is named. The fleet's old
# /usr/share/v2ray, PassWall's packages', reads as vctl's own too.
geo_asset_dir="$(uci_get_or_default geo_asset_dir /usr/share/vectra-controller-pro/geo)"
legacy_state_path="$(uci_get_or_default legacy_state_path /etc/vectra-controller/state.json)"

# Optional operator overrides for the resource guard. Zero/empty means
# "use the compile-time default in jobsafety". pre_drop_caches opts into a
# one-shot vm.drop_caches=3 before the guard fails on RAM.
job_safety_heavy_memory_floor_mb="$(int_or_default "$(uci_get_or_default job_safety_heavy_memory_floor_mb 0)" 0)"
job_safety_storage_memory_floor_mb="$(int_or_default "$(uci_get_or_default job_safety_storage_memory_floor_mb 0)" 0)"
job_safety_diagnostic_memory_floor_mb="$(int_or_default "$(uci_get_or_default job_safety_diagnostic_memory_floor_mb 0)" 0)"
job_safety_heavy_overlay_floor_mb="$(int_or_default "$(uci_get_or_default job_safety_heavy_overlay_floor_mb 0)" 0)"
job_safety_storage_overlay_floor_mb="$(int_or_default "$(uci_get_or_default job_safety_storage_overlay_floor_mb 0)" 0)"
job_safety_heavy_tmp_floor_mb="$(int_or_default "$(uci_get_or_default job_safety_heavy_tmp_floor_mb 0)" 0)"
job_safety_storage_tmp_floor_mb="$(int_or_default "$(uci_get_or_default job_safety_storage_tmp_floor_mb 0)" 0)"
job_safety_diagnostic_tmp_floor_mb="$(int_or_default "$(uci_get_or_default job_safety_diagnostic_tmp_floor_mb 0)" 0)"
job_safety_pre_drop_caches="$(uci_get_or_default job_safety_pre_drop_caches 0)"
# How often the claim code changes (ADR-0006); 0/empty = the default, 20 min
# (each code is taken for 10 min more).
# Shorter only for tests.
claim_rotate_sec="$(int_or_default "$(uci_get_or_default claim_rotate_sec 0)" 0)"
# The router's resolver asks through the tunnel unless dns_tunnel is '0' (the
# ISP forges answers for blocked names even for queries to public resolvers).
dns_tunnel="$(uci_get_or_default dns_tunnel 1)"
# While it does, the LAN's queries to public resolvers (a device with 8.8.8.8
# set by hand) are answered by the router's resolver, unless dns_hijack is '0'.
dns_hijack="$(uci_get_or_default dns_hijack 1)"
# The addresses the routing sends straight out are routed by the kernel and
# never enter xray, unless direct_bypass is '0' (then xray carries them all).
direct_bypass="$(uci_get_or_default direct_bypass 1)"
# The load guards (firewall.Spec): a P2P host's peers by the kernel, a
# device's new connections a second into xray (and every device's together,
# admit_total_rate: off unless set), xray's to one node, a device's DNS
# queries a second to the router's resolver.
p2p_bypass="$(uci_get_or_default p2p_bypass 1)"
admit_rate="$(uci_get_or_default admit_rate "")"
admit_total_rate="$(uci_get_or_default admit_total_rate "")"
pace_rate="$(uci_get_or_default pace_rate "")"
dns_rate="$(uci_get_or_default dns_rate "")"
# The provider's nodes carry no IPv6: the LAN's IPv6 is refused at once, the
# devices take IPv4 — unless ipv6 is '1' (nodes that carry it).
ipv6="$(uci_get_or_default ipv6 0)"
# Russian sites go direct rather than through the provider's Russian bridge
# (BL-RU; YouTube keeps it), unless russia_direct is '0'.
russia_direct="$(uci_get_or_default russia_direct 1)"
# A node that stops answering loses new connections within seconds (the
# failover watchdog), unless failover_watchdog is '0'.
failover_watchdog="$(uci_get_or_default failover_watchdog 1)"
# An exit that answers the neutral URL and no blocked site leaves the
# balancers until it carries them again (the exit check), unless exit_check
# is '0'. exit_check_site (a list), exit_check_control, exit_check_first and
# exit_check_every replace the product's sites, neutral URL and timing.
exit_check="$(uci_get_or_default exit_check 1)"
exit_check_sites="$(uci_get_or_default exit_check_site "")"
exit_check_control="$(uci_get_or_default exit_check_control "")"
exit_check_first="$(uci_get_or_default exit_check_first "")"
exit_check_every="$(uci_get_or_default exit_check_every "")"
# What the router routes by: the provider's xray-JSON (default), 'passwall' —
# PassWall2's own configuration (the operator's route policy) through
# PassWall2's generator — or 'native': the same policy in vctl's own store
# (/etc/config/vectra_route, imported from PassWall2's once), generated and
# kept current by vctl, with no PassWall2 on the router.
route_source="$(uci_get_or_default route_source provider)"
# PassWall2 goes from the router once vctl has carried its traffic, switched
# on for good, for passwall_retire_after seconds (a day by default), unless
# retire_passwall is '0' (internal/retire).
retire_passwall="$(uci_get_or_default retire_passwall 1)"
passwall_retire_after="$(uci_get_or_default passwall_retire_after "")"

mkdir -p "$(dirname "$OUTPUT_PATH")"

json_init
json_add_string controlUrl "$control_url"
json_add_string panelUrl "$panel_url"
if [ -n "$control_ips" ]; then
	json_add_array controlFallbackIps
	for ip in $control_ips; do
		case "$ip" in *[!0-9.]* | '') ;; *) json_add_string "" "$ip" ;; esac
	done
	json_close_array
fi
json_add_string statePath "$state_path"
json_add_string statusPath "$status_path"
json_add_string xrayConfigPath "$xray_config_path"
json_add_string providerConfigPath "$provider_config_path"
json_add_string xrayRenderPath "$xray_render_path"
json_add_string xrayBinary "$xray_binary"
json_add_string geoAssetDir "$geo_asset_dir"
json_add_string legacyStatePath "$legacy_state_path"
json_add_int pollIntervalSeconds "$poll_interval"
json_add_int requestTimeoutSeconds "$request_timeout"
if [ "$claim_rotate_sec" -gt 0 ]; then
	json_add_int claimRotateSeconds "$claim_rotate_sec"
fi
if [ "$dns_tunnel" = "0" ] || [ "$dns_tunnel" = "false" ]; then
	json_add_boolean noDnsTunnel 1
fi
if [ "$dns_hijack" = "0" ] || [ "$dns_hijack" = "false" ]; then
	json_add_boolean noDnsHijack 1
fi
if [ "$direct_bypass" = "0" ] || [ "$direct_bypass" = "false" ]; then
	json_add_boolean noDirectBypass 1
fi
if [ "$p2p_bypass" = "0" ] || [ "$p2p_bypass" = "false" ]; then
	json_add_boolean noP2PBypass 1
fi
case "$admit_rate" in '' | *[!0-9]*) ;; *) json_add_int admitRate "$admit_rate" ;; esac
case "$admit_total_rate" in '' | *[!0-9]*) ;; *) json_add_int admitTotalRate "$admit_total_rate" ;; esac
case "$dns_rate" in '' | *[!0-9]*) ;; *) json_add_int dnsRate "$dns_rate" ;; esac
case "$pace_rate" in '' | *[!0-9]*) ;; *) json_add_int paceRate "$pace_rate" ;; esac
if [ "$ipv6" = "1" ] || [ "$ipv6" = "true" ]; then
	json_add_boolean ipv6 1
fi
if [ "$russia_direct" = "0" ] || [ "$russia_direct" = "false" ]; then
	json_add_boolean noRussiaDirect 1
fi
if [ "$failover_watchdog" = "0" ] || [ "$failover_watchdog" = "false" ]; then
	json_add_boolean noFailoverWatchdog 1
fi
if [ "$exit_check" = "0" ] || [ "$exit_check" = "false" ]; then
	json_add_boolean noExitCheck 1
fi
if [ -n "$exit_check_sites" ]; then
	json_add_array exitCheckSites
	for site in $exit_check_sites; do
		case "$site" in https://*) json_add_string "" "$site" ;; esac
	done
	json_close_array
fi
case "$exit_check_control" in https://*) json_add_string exitCheckControl "$exit_check_control" ;; esac
case "$exit_check_first" in '' | *[!0-9]*) ;; *) json_add_int exitCheckFirstSec "$exit_check_first" ;; esac
case "$exit_check_every" in '' | *[!0-9]*) ;; *) json_add_int exitCheckEverySec "$exit_check_every" ;; esac
case "$route_source" in
passwall|native) json_add_string routeSource "$route_source" ;;
esac
if [ "$retire_passwall" = "0" ] || [ "$retire_passwall" = "false" ]; then
	json_add_boolean noRetirePassWall 1
fi
case "$passwall_retire_after" in '' | *[!0-9]*) ;; *) json_add_int passWallRetireAfterSec "$passwall_retire_after" ;; esac

json_add_object jobSafety
json_add_int heavyMemoryFloorMb "$job_safety_heavy_memory_floor_mb"
json_add_int storageMemoryFloorMb "$job_safety_storage_memory_floor_mb"
json_add_int diagnosticMemoryFloorMb "$job_safety_diagnostic_memory_floor_mb"
json_add_int heavyOverlayFloorMb "$job_safety_heavy_overlay_floor_mb"
json_add_int storageOverlayFloorMb "$job_safety_storage_overlay_floor_mb"
json_add_int heavyTmpFloorMb "$job_safety_heavy_tmp_floor_mb"
json_add_int storageTmpFloorMb "$job_safety_storage_tmp_floor_mb"
json_add_int diagnosticTmpFloorMb "$job_safety_diagnostic_tmp_floor_mb"
if [ "$job_safety_pre_drop_caches" = "1" ] || [ "$job_safety_pre_drop_caches" = "true" ]; then
	json_add_boolean preDropCaches 1
else
	json_add_boolean preDropCaches 0
fi
json_close_object

json_dump >"$OUTPUT_PATH"
