#!/bin/sh

set -eu

ACTION="${1:-status}"
RUN_DIR="/var/run/vectra-controller"
AGENT_STATUS_FILE="$RUN_DIR/status.json"
LUCI_STATUS_FILE="$RUN_DIR/luci-status.json"
CONFIG_JSON="$RUN_DIR/config.json"
RENDERER="/usr/libexec/vectra-controller/render-config.sh"
STATE_FILE="/etc/vectra-controller/state.json"

mkdir -p "$RUN_DIR"

json_escape() {
	printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

json_file_field() {
	local file_path="$1"
	local expression="$2"
	[ -f "$file_path" ] || return 0
	jsonfilter -i "$file_path" -e "$expression" 2>/dev/null || true
}

json_file_bool() {
	local value
	value="$(json_file_field "$1" "$2")"
	case "$value" in
		true|1)
			printf 'true'
			;;
		*)
			printf 'false'
			;;
	esac
}

json_file_number() {
	local value
	value="$(json_file_field "$1" "$2")"
	case "$value" in
		''|*[!0-9-]*)
			printf '0'
			;;
		*)
			printf '%s' "$value"
			;;
	esac
}

json_file_string() {
	local value
	value="$(json_file_field "$1" "$2")"
	printf '%s' "$value"
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

state_rescue_mutate() {
	local mode="$1"
	local reason="$2"
	local happened_at="$3"
	local last_transition_at="$4"
	local tmp_file

	[ -f "$STATE_FILE" ] || return 0

	tmp_file="$(mktemp "$RUN_DIR/state-rescue.XXXXXX")"

	awk \
		-v mode="$mode" \
		-v reason="$reason" \
		-v happened_at="$happened_at" \
		-v last_transition_at="$last_transition_at" \
		'
		function print_rescue_block() {
			print "  \"rescue\": {"
			print "    \"state\": {"
			print "      \"mode\": \"" mode "\","
			print "      \"proxy_failure_count\": 0,"
			print "      \"direct_success_count\": 0,"
			print "      \"proxy_success_count\": 0,"
			print "      \"last_transition_at\": \"" last_transition_at "\""
			print "    },"
			if (mode == "direct") {
				print "    \"last_mode\": \"direct\","
				print "    \"last_reason\": \"" reason "\","
				print "    \"happened_at\": \"" happened_at "\""
			} else {
				print "    \"last_mode\": \"\","
				print "    \"last_reason\": \"\","
				print "    \"happened_at\": \"\""
			}
			print "  },"
		}
		BEGIN {
			in_rescue = 0
			replaced = 0
		}
		/^  "rescue": \{/ {
			print_rescue_block()
			in_rescue = 1
			replaced = 1
			next
		}
		in_rescue && /^  "current_job":/ {
			in_rescue = 0
			print
			next
		}
		!in_rescue {
			print
		}
		END {
			if (!replaced) {
				exit 1
			}
		}
		' "$STATE_FILE" >"$tmp_file"

	chmod 600 "$tmp_file"
	mv "$tmp_file" "$STATE_FILE"
}

sync_direct_rescue_state() {
	local reason="${1:-Оператор принудительно включил прямой режим из LuCI}"
	local now
	now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	state_rescue_mutate "direct" "$reason" "$now" "$now"
}

clear_rescue_state() {
	local now
	now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	state_rescue_mutate "proxy" "" "" "$now"
}

service_state() {
	local service_name="$1"
	if [ -x "/etc/init.d/$service_name" ] && /etc/init.d/"$service_name" running >/dev/null 2>&1; then
		printf 'running'
	else
		printf 'stopped'
	fi
}

selected_node_label() {
	local node_id="$1"
	local label protocol address port

	[ -n "$node_id" ] || return 0

	label="$(uci -q get "passwall2.$node_id.remarks" || true)"
	if [ -n "$label" ]; then
		printf '%s' "$label"
		return 0
	fi

	protocol="$(uci -q get "passwall2.$node_id.protocol" || true)"
	address="$(uci -q get "passwall2.$node_id.address" || uci -q get "passwall2.$node_id.server" || true)"
	port="$(uci -q get "passwall2.$node_id.port" || true)"

	if [ -n "$address" ] && [ -n "$port" ]; then
		printf '%s:%s' "$address" "$port"
		return 0
	fi

	if [ -n "$address" ]; then
		printf '%s' "$address"
		return 0
	fi

	if [ -n "$protocol" ]; then
		printf '%s' "$protocol"
		return 0
	fi

	printf '%s' "$node_id"
}

write_status() {
	local controller_service_state passwall_service_state passwall_state last_reason controller_version luci_version control_url panel_url
	local router_id rescue_mode selected_node selected_node_name import_state config_digest applied_revision_id
	local last_register_at last_check_in_at last_operator_message last_error pending_approval jobs_available
	local last_rescue_at server_reachable public_reachable proxy_failure_count proxy_success_count direct_success_count
	local last_server_error last_public_error
	controller_service_state="$(service_state vectra-controller)"
	passwall_service_state="$(service_state passwall2)"

	passwall_state="$(uci -q get passwall2.@global[0].enabled || printf 0)"
	last_reason="$(uci -q get vectra-controller.main.last_rescue_reason || true)"
	if [ -z "$last_reason" ]; then
		last_reason="$(json_file_string "$STATE_FILE" '@.rescue.last_reason')"
	fi
	control_url="$(uci -q get vectra-controller.main.control_url || true)"
	panel_url="$(uci -q get vectra-controller.main.panel_url || true)"
	if [ -z "$control_url" ]; then
		control_url="$(json_file_field "$AGENT_STATUS_FILE" '@.control_url')"
	fi
	if [ -z "$control_url" ]; then
		control_url="${panel_url:-}"
	fi
	controller_version="$(package_version vectra-controller-agent)"
	luci_version="$(package_version luci-app-vectra-controller)"
	router_id="$(json_file_field "$AGENT_STATUS_FILE" '@.router_id')"
	rescue_mode="$(json_file_field "$AGENT_STATUS_FILE" '@.rescue_mode')"
	if [ -z "$rescue_mode" ]; then
		rescue_mode="$(json_file_string "$STATE_FILE" '@.rescue.state.mode')"
	fi
	if [ -z "$rescue_mode" ]; then
		if [ "$passwall_state" = "1" ]; then
			rescue_mode="proxy"
		else
			rescue_mode="direct"
		fi
	fi
	if [ "$passwall_state" != "1" ] && [ "$rescue_mode" != "direct" ]; then
		rescue_mode="direct"
	fi
	selected_node="$(json_file_field "$AGENT_STATUS_FILE" '@.selected_node_id')"
	if [ -z "$selected_node" ]; then
		selected_node="$(uci -q get passwall2.@global[0].node || true)"
	fi
	selected_node_name="$(selected_node_label "$selected_node")"
	import_state="$(json_file_field "$AGENT_STATUS_FILE" '@.import_state')"
	config_digest="$(json_file_field "$AGENT_STATUS_FILE" '@.config_digest')"
	applied_revision_id="$(json_file_field "$AGENT_STATUS_FILE" '@.applied_revision_id')"
	last_register_at="$(json_file_field "$AGENT_STATUS_FILE" '@.last_register_at')"
	last_check_in_at="$(json_file_field "$AGENT_STATUS_FILE" '@.last_check_in_at')"
	last_operator_message="$(json_file_field "$AGENT_STATUS_FILE" '@.last_operator_message')"
	last_error="$(json_file_field "$AGENT_STATUS_FILE" '@.last_error')"
	last_rescue_at="$(json_file_field "$AGENT_STATUS_FILE" '@.last_rescue_at')"
	if [ -z "$last_rescue_at" ]; then
		last_rescue_at="$(json_file_string "$STATE_FILE" '@.rescue.happened_at')"
	fi
	server_reachable="$(json_file_bool "$AGENT_STATUS_FILE" '@.server_reachable')"
	public_reachable="$(json_file_bool "$AGENT_STATUS_FILE" '@.public_reachable')"
	proxy_failure_count="$(json_file_number "$AGENT_STATUS_FILE" '@.proxy_failure_count')"
	proxy_success_count="$(json_file_number "$AGENT_STATUS_FILE" '@.proxy_success_count')"
	direct_success_count="$(json_file_number "$AGENT_STATUS_FILE" '@.direct_success_count')"
	if [ "$proxy_failure_count" = "0" ]; then
		proxy_failure_count="$(json_file_number "$STATE_FILE" '@.rescue.state.proxy_failure_count')"
	fi
	if [ "$proxy_success_count" = "0" ]; then
		proxy_success_count="$(json_file_number "$STATE_FILE" '@.rescue.state.proxy_success_count')"
	fi
	if [ "$direct_success_count" = "0" ]; then
		direct_success_count="$(json_file_number "$STATE_FILE" '@.rescue.state.direct_success_count')"
	fi
	last_server_error="$(json_file_field "$AGENT_STATUS_FILE" '@.last_server_error')"
	last_public_error="$(json_file_field "$AGENT_STATUS_FILE" '@.last_public_error')"
	pending_approval="$(json_file_bool "$AGENT_STATUS_FILE" '@.pending_approval')"
	jobs_available="$(json_file_number "$AGENT_STATUS_FILE" '@.jobs_available')"

	if [ -z "$controller_version" ]; then
		controller_version="$(json_file_field "$AGENT_STATUS_FILE" '@.controller_version')"
	fi

	cat >"$LUCI_STATUS_FILE" <<EOF
{
  "serviceState": "$controller_service_state",
  "passwallServiceState": "$passwall_service_state",
  "passwallEnabled": "$passwall_state",
  "lastRescueReason": "$(json_escape "${last_reason:-}")",
  "controlUrl": "$(json_escape "${control_url:-}")",
  "panelUrl": "$(json_escape "${panel_url:-}")",
  "controllerVersion": "$(json_escape "${controller_version:-}")",
  "luciVersion": "$(json_escape "${luci_version:-}")",
  "routerId": "$(json_escape "${router_id:-}")",
  "rescueMode": "$(json_escape "${rescue_mode:-}")",
  "selectedNodeId": "$(json_escape "${selected_node:-}")",
  "selectedNodeLabel": "$(json_escape "${selected_node_name:-}")",
  "importState": "$(json_escape "${import_state:-}")",
  "configDigest": "$(json_escape "${config_digest:-}")",
  "appliedRevisionId": "$(json_escape "${applied_revision_id:-}")",
  "lastRegisterAt": "$(json_escape "${last_register_at:-}")",
  "lastCheckInAt": "$(json_escape "${last_check_in_at:-}")",
  "lastOperatorMessage": "$(json_escape "${last_operator_message:-}")",
  "lastRescueAt": "$(json_escape "${last_rescue_at:-}")",
  "serverReachable": ${server_reachable},
  "publicReachable": ${public_reachable},
  "proxyFailureCount": ${proxy_failure_count},
  "proxySuccessCount": ${proxy_success_count},
  "directSuccessCount": ${direct_success_count},
  "lastServerError": "$(json_escape "${last_server_error:-}")",
  "lastPublicError": "$(json_escape "${last_public_error:-}")",
  "lastError": "$(json_escape "${last_error:-}")",
  "pendingApproval": ${pending_approval},
  "jobsAvailable": ${jobs_available}
}
EOF
}

case "$ACTION" in
	render)
		[ -x "$RENDERER" ] && "$RENDERER" "$CONFIG_JSON"
		write_status
		;;
	reconnect)
		touch "$RUN_DIR/force-reconnect"
		/etc/init.d/vectra-controller restart >/dev/null 2>&1 || true
		write_status
		;;
	direct)
		uci -q set passwall2.@global[0].enabled='0'
		uci commit passwall2
		/etc/init.d/passwall2 restart >/dev/null 2>&1 || true
		uci -q set vectra-controller.main.last_rescue_reason='Оператор принудительно включил прямой режим из LuCI'
		uci commit vectra-controller
		sync_direct_rescue_state 'Оператор принудительно включил прямой режим из LuCI'
		/etc/init.d/vectra-controller restart >/dev/null 2>&1 || true
		write_status
		;;
	resume)
		uci -q set passwall2.@global[0].enabled='1'
		uci commit passwall2
		uci -q set vectra-controller.main.last_rescue_reason=''
		uci commit vectra-controller
		clear_rescue_state
		/etc/init.d/passwall2 restart >/dev/null 2>&1 || true
		/etc/init.d/vectra-controller restart >/dev/null 2>&1 || true
		write_status
		;;
	status|*)
		write_status
		cat "$LUCI_STATUS_FILE"
		;;
esac
