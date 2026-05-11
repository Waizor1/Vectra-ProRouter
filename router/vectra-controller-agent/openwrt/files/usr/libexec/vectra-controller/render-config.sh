#!/bin/sh

set -e

# OpenWrt helper libraries rely on several optional globals and are not nounset-safe.
IPKG_INSTROOT="${IPKG_INSTROOT-}"

. /lib/functions.sh
. /usr/share/libubox/jshn.sh

OUTPUT_PATH="${1:-/var/run/vectra-controller/config.json}"
CONFIG_NAME="vectra-controller"
SECTION="main"
BOARD_JSON="$(ubus call system board 2>/dev/null || true)"
LOW_MEMORY_EXPENSIVE_PROBE_FLOOR_MB="${VECTRA_LOW_MEMORY_EXPENSIVE_PROBE_FLOOR_MB:-64}"

uci_get_or_default() {
	local option="$1"
	local fallback="${2:-}"
	local value
	config_get value "$SECTION" "$option" "$fallback"
	printf '%s' "$value"
}

json_field() {
	local expression="$1"
	jsonfilter -s "$BOARD_JSON" -e "$expression" 2>/dev/null || true
}

detect_layout_family() {
	local board_name="$1"
	case "$board_name" in
	xiaomi,mi-router-ax3000t)
		printf 'stock-layout'
		;;
	*ubootmod*)
		printf 'ubootmod'
		;;
	esac
}

service_state() {
	local service_name="$1"
	if [ -x "/etc/init.d/$service_name" ] && "/etc/init.d/$service_name" enabled >/dev/null 2>&1; then
		if "/etc/init.d/$service_name" running >/dev/null 2>&1; then
			printf 'running'
		else
			printf 'stopped'
		fi
		return
	fi
	printf 'unknown'
}

package_version() {
	local package_name="$1"
	local version=""
	version="$(awk -F': ' '/^Version:/ { print $2; exit }' "/usr/lib/opkg/info/${package_name}.control" 2>/dev/null || true)"
	if [ -n "$version" ]; then
		printf '%s' "$version"
		return 0
	fi

	awk -F': ' -v package_name="$package_name" '
		/^Package:/ { current = ($2 == package_name); next }
		current && /^Version:/ { print $2; exit }
	' /usr/lib/opkg/status 2>/dev/null || true
}

binary_version() {
	local binary_path="$1"
	shift
	[ -x "$binary_path" ] || return 0
	if [ "${memory_available_mb:-0}" -gt 0 ] 2>/dev/null && \
		[ "${memory_available_mb:-0}" -lt "$LOW_MEMORY_EXPENSIVE_PROBE_FLOOR_MB" ] 2>/dev/null; then
		return 0
	fi
	"$binary_path" "$@" 2>&1 | awk 'NF && $0 ~ /[0-9]+\.[0-9]+/ { print; exit }'
}

meminfo_mb() {
	local key="$1"
	awk -v name="$key" '$1 == name ":" { print int($2 / 1024); exit }' /proc/meminfo 2>/dev/null
}

df_free_mb() {
	local mount_path="$1"
	df -kP "$mount_path" 2>/dev/null | awk 'NR == 2 { print int($4 / 1024) }'
}

resolve_selected_node_label() {
	local node_id="$1"
	local remark address port protocol

	[ -n "$node_id" ] || return 0

	remark="$(uci -q get "passwall2.${node_id}.remarks" || true)"
	if [ -n "$remark" ]; then
		printf '%s' "$remark"
		return 0
	fi

	address="$(uci -q get "passwall2.${node_id}.address" || true)"
	port="$(uci -q get "passwall2.${node_id}.port" || true)"
	if [ -n "$address" ] && [ -n "$port" ]; then
		printf '%s:%s' "$address" "$port"
		return 0
	fi
	if [ -n "$address" ]; then
		printf '%s' "$address"
		return 0
	fi

	protocol="$(uci -q get "passwall2.${node_id}.protocol" || true)"
	if [ -n "$protocol" ]; then
		printf '%s' "$protocol"
		return 0
	fi

	printf '%s' "$node_id"
}

openwrt_description() {
	if [ -f /usr/lib/os-release ]; then
		awk -F= '/^PRETTY_NAME=/ { gsub(/^"|"$/, "", $2); print $2; exit }' /usr/lib/os-release
		return 0
	fi

	if [ -f /etc/openwrt_release ]; then
		awk -F= '/^DISTRIB_DESCRIPTION=/ { gsub(/^"|"$/, "", $2); print $2; exit }' /etc/openwrt_release
	fi
}

config_load "$CONFIG_NAME"
[ -f /etc/openwrt_release ] && . /etc/openwrt_release

control_url="$(uci_get_or_default control_url)"
panel_url="$(uci_get_or_default panel_url)"
if [ -z "$control_url" ]; then
	control_url="${panel_url:-https://api.vectra-pro.net}"
fi
if [ -z "$panel_url" ]; then
	panel_url="$control_url"
fi
poll_interval="$(uci_get_or_default poll_interval 45s)"
request_timeout="$(uci_get_or_default request_timeout 10s)"
state_path="$(uci_get_or_default state_path /etc/vectra-controller/state.json)"
status_path="$(uci_get_or_default status_path /var/run/vectra-controller/status.json)"
controller_version="$(package_version vectra-controller-agent)"
if [ -z "$controller_version" ]; then
	controller_version="$(uci_get_or_default controller_version)"
fi
if [ -z "$controller_version" ]; then
	controller_version="unknown"
fi
model="$(json_field '@.model')"
[ -n "$model" ] || model="$(uci_get_or_default model)"
board_name="$(json_field '@.board_name')"
[ -n "$board_name" ] || board_name="$(uci_get_or_default board_name)"
target="$(json_field '@.release.target')"
[ -n "$target" ] || target="$(uci_get_or_default target)"
architecture="${DISTRIB_ARCH:-}"
[ -n "$architecture" ] || architecture="$(uci_get_or_default architecture)"
openwrt_release="$(json_field '@.release.version')"
[ -n "$openwrt_release" ] || openwrt_release="${DISTRIB_RELEASE:-}"
[ -n "$openwrt_release" ] || openwrt_release="$(uci_get_or_default openwrt_release)"
layout_family="$(detect_layout_family "$board_name")"
[ -n "$layout_family" ] || layout_family="$(uci_get_or_default layout_family)"

hostname_value="$(uci -q get system.@system[0].hostname)"
passwall_enabled="$(uci -q get passwall2.@global[0].enabled || printf 0)"
selected_node="$(uci -q get passwall2.@global[0].node || true)"
selected_node_label="$(resolve_selected_node_label "$selected_node")"
node_count="$(uci show passwall2 2>/dev/null | grep -c '=nodes' || true)"
subscription_count="$(uci show passwall2 2>/dev/null | grep -c '=subscribe_list' || true)"
openwrt_description_value="$(openwrt_description)"
memory_total_mb="$(meminfo_mb MemTotal || printf 0)"
memory_available_mb="$(meminfo_mb MemAvailable || printf 0)"
swap_total_mb="$(meminfo_mb SwapTotal || printf 0)"
swap_free_mb="$(meminfo_mb SwapFree || printf 0)"
overlay_free_mb="$(df_free_mb /overlay || printf 0)"
tmp_free_mb="$(df_free_mb /tmp || printf 0)"

mkdir -p "$(dirname "$OUTPUT_PATH")"

json_init
json_add_string control_url "$control_url"
json_add_string panel_url "$panel_url"
json_add_string state_path "$state_path"
json_add_string status_path "$status_path"
json_add_string poll_interval "$poll_interval"
json_add_string request_timeout "$request_timeout"

json_add_object rescue_policy
json_add_array health_urls
json_add_string "" "https://www.gstatic.com/generate_204"
json_add_string "" "https://cp.cloudflare.com/"
json_close_array
json_add_int trigger_failure_count 3
json_add_int recovery_success_count 2
json_add_int cooldown 300000000000
json_add_boolean require_direct_path_success 1
json_add_string direct_mode_reason "Subscription expired or upstream proxy unavailable"
json_add_string panel_outage_threshold "1h"
json_add_string probe_cache_ttl "5m"
json_add_string controller_restart_settle "90s"
json_add_string direct_settle "45s"
json_add_string post_reboot_settle "4m"
json_add_string passwall_warmup "75s"
json_add_string reboot_cooldown "12h"
json_close_object

json_add_object inventory
json_add_string protocolVersion "2026-04-v1"
json_add_string deviceIdentifier ""
json_add_string devicePublicKey ""
json_add_string controllerVersion "$controller_version"
json_add_string hostname "$hostname_value"
json_add_string panelDomain "$panel_url"
json_add_string model "$model"
json_add_string boardName "$board_name"
json_add_string layoutFamily "$layout_family"
json_add_string target "$target"
json_add_string architecture "$architecture"
json_add_string openwrtRelease "$openwrt_release"
json_add_string openwrtDescription "$openwrt_description_value"
if [ "$passwall_enabled" = "1" ]; then
	json_add_boolean passwallEnabled 1
else
	json_add_boolean passwallEnabled 0
fi
json_add_string selectedNodeId "$selected_node"
json_add_string selectedNodeLabel "$selected_node_label"
json_add_int nodeCount "${node_count:-0}"
json_add_int subscriptionCount "${subscription_count:-0}"

json_add_object packageVersions
json_add_string "luci-app-passwall2" "$(package_version luci-app-passwall2)"
json_add_string "xray-core" "$(package_version xray-core)"
json_add_string "sing-box" "$(package_version sing-box)"
json_add_string hysteria "$(package_version hysteria)"
json_add_string geoview "$(package_version geoview)"
json_add_string "v2ray-geoip" "$(package_version v2ray-geoip)"
json_add_string "v2ray-geosite" "$(package_version v2ray-geosite)"
json_add_string dnsmasq "$(package_version dnsmasq)"
json_add_string "dnsmasq-full" "$(package_version dnsmasq-full)"
json_add_string "chinadns-ng" "$(package_version chinadns-ng)"
json_add_string "kmod-nft-socket" "$(package_version kmod-nft-socket)"
json_add_string "kmod-nft-tproxy" "$(package_version kmod-nft-tproxy)"
json_add_string "kmod-nft-nat" "$(package_version kmod-nft-nat)"
json_close_object

json_add_object binaryVersions
json_add_string xray "$(binary_version /usr/bin/xray -version)"
json_add_string "sing-box" "$(binary_version /usr/bin/sing-box version)"
json_add_string hysteria "$(binary_version /usr/bin/hysteria version)"
json_add_string geoview "$(binary_version /usr/bin/geoview -version)"
json_add_string dnsmasq "$(binary_version /usr/sbin/dnsmasq -v)"
json_close_object

json_add_object resources
json_add_int memoryTotalMb "${memory_total_mb:-0}"
json_add_int memoryAvailableMb "${memory_available_mb:-0}"
json_add_int swapTotalMb "${swap_total_mb:-0}"
json_add_int swapFreeMb "${swap_free_mb:-0}"
json_add_int overlayFreeMb "${overlay_free_mb:-0}"
json_add_int tmpFreeMb "${tmp_free_mb:-0}"
json_close_object

json_add_object serviceHealth
json_add_string controller "$(service_state vectra-controller)"
json_add_string passwall "$(service_state passwall2)"
json_add_string passwallServer "unknown"
json_add_string dnsmasq "$(service_state dnsmasq)"
json_close_object

json_close_object
json_dump >"$OUTPUT_PATH"
