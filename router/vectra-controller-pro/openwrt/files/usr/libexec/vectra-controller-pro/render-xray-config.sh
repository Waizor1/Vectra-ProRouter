#!/bin/sh

set -e

# OpenWrt helper libraries rely on several optional globals and are not nounset-safe.
IPKG_INSTROOT="${IPKG_INSTROOT-}"

. /lib/functions.sh
. /usr/share/libubox/jshn.sh

# Renders the vctl DAEMON config (agentcfg.Config JSON) from UCI + board state.
# Field names and types MUST match internal/agentcfg/agentcfg.go exactly:
#   controlUrl, panelUrl, statePath, statusPath, xrayConfigPath,
#   xrayRenderPath, xrayBinary, legacyStatePath (strings),
#   pollIntervalSeconds, requestTimeoutSeconds (ints),
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

poll_interval="$(int_or_default "$(uci_get_or_default poll_interval 45)" 45)"
request_timeout="$(int_or_default "$(uci_get_or_default request_timeout 10)" 10)"

state_path="$(uci_get_or_default state_path /etc/vectra-controller-pro/state.json)"
status_path="$(uci_get_or_default status_path /var/run/vectra-controller-pro/status.json)"
xray_config_path="$(uci_get_or_default xray_config_path /etc/vectra-controller-pro/xray-desired.json)"
xray_render_path="$(uci_get_or_default xray_render_path /var/run/vectra-controller-pro/xray.json)"
xray_binary="$(uci_get_or_default xray_binary /usr/sbin/vctl-xray-wrapper)"
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

mkdir -p "$(dirname "$OUTPUT_PATH")"

json_init
json_add_string controlUrl "$control_url"
json_add_string panelUrl "$panel_url"
json_add_string statePath "$state_path"
json_add_string statusPath "$status_path"
json_add_string xrayConfigPath "$xray_config_path"
json_add_string xrayRenderPath "$xray_render_path"
json_add_string xrayBinary "$xray_binary"
json_add_string legacyStatePath "$legacy_state_path"
json_add_int pollIntervalSeconds "$poll_interval"
json_add_int requestTimeoutSeconds "$request_timeout"

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
