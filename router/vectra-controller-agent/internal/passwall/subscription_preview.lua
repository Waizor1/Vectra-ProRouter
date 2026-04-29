#!/usr/bin/lua

local json = require("luci.jsonc")

local function readfile(path)
  local handle = io.open(path, "r")
  if not handle then
    return nil
  end

  local content = handle:read("*a")
  handle:close()
  return content
end

local source = readfile("/usr/share/passwall2/subscribe.lua")
if not source then
  io.stderr:write("failed to read /usr/share/passwall2/subscribe.lua\n")
  os.exit(1)
end

local injected = [=[
log = function() end

local function vectra_preview_trim(value)
	if value == nil then return nil end
	value = tostring(value)
	value = api.trim(value)
	if value == "" then return nil end
	return value
end

local function vectra_preview_bool(value)
	if value == true or value == 1 or value == "1" then return true end
	if value == false or value == 0 or value == "0" then return false end
	return nil
end

local function vectra_preview_access_mode(value)
	if value == "direct" or value == "proxy" then
		return value
	end
	return "auto"
end

local function vectra_preview_reset_defaults()
	allowInsecure_default = true
	domain_strategy_default = uci:get(appname, "@global_subscribe[0]", "domain_strategy") or ""
	domain_strategy_node = ""
	filter_keyword_mode_default = uci:get(appname, "@global_subscribe[0]", "filter_keyword_mode") or "0"
	filter_keyword_discard_list_default = uci:get(appname, "@global_subscribe[0]", "filter_discard_list") or {}
	filter_keyword_keep_list_default = uci:get(appname, "@global_subscribe[0]", "filter_keep_list") or {}
	ss_type_default = api.get_core("ss_type", {{has_ss,"shadowsocks-libev"},{has_ss_rust,"shadowsocks-rust"},{has_singbox,"sing-box"},{has_xray,"xray"}})
	trojan_type_default = api.get_core("trojan_type", {{has_singbox,"sing-box"},{has_xray,"xray"}})
	vmess_type_default = api.get_core("vmess_type", {{has_xray,"xray"},{has_singbox,"sing-box"}})
	vless_type_default = api.get_core("vless_type", {{has_xray,"xray"},{has_singbox,"sing-box"}})
	hysteria2_type_default = api.get_core("hysteria2_type", {{has_hysteria2,"hysteria2"},{has_singbox,"sing-box"},{has_xray,"xray"}})
	preproxy_node_group, to_node_group, chain_node_type = "", "", ""
end

local function vectra_preview_apply_subscription_overrides(value)
	if value.allowInsecure and value.allowInsecure ~= "1" then
		allowInsecure_default = nil
	end
	local filter_keyword_mode = value.filter_keyword_mode or "5"
	if filter_keyword_mode == "0" then
		filter_keyword_mode_default = "0"
	elseif filter_keyword_mode == "1" then
		filter_keyword_mode_default = "1"
		filter_keyword_discard_list_default = value.filter_discard_list or {}
	elseif filter_keyword_mode == "2" then
		filter_keyword_mode_default = "2"
		filter_keyword_keep_list_default = value.filter_keep_list or {}
	elseif filter_keyword_mode == "3" then
		filter_keyword_mode_default = "3"
		filter_keyword_keep_list_default = value.filter_keep_list or {}
		filter_keyword_discard_list_default = value.filter_discard_list or {}
	elseif filter_keyword_mode == "4" then
		filter_keyword_mode_default = "4"
		filter_keyword_keep_list_default = value.filter_keep_list or {}
		filter_keyword_discard_list_default = value.filter_discard_list or {}
	end

	local ss_type = value.ss_type or "global"
	if ss_type ~= "global" and core_has[ss_type] then
		ss_type_default = ss_type
	end
	local trojan_type = value.trojan_type or "global"
	if trojan_type ~= "global" and core_has[trojan_type] then
		trojan_type_default = trojan_type
	end
	local vmess_type = value.vmess_type or "global"
	if vmess_type ~= "global" and core_has[vmess_type] then
		vmess_type_default = vmess_type
	end
	local vless_type = value.vless_type or "global"
	if vless_type ~= "global" and core_has[vless_type] then
		vless_type_default = vless_type
	end
	local hysteria2_type = value.hysteria2_type or "global"
	if hysteria2_type ~= "global" and core_has[hysteria2_type] then
		hysteria2_type_default = hysteria2_type
	end
	local domain_strategy = value.domain_strategy or "global"
	if domain_strategy ~= "global" then
		domain_strategy_node = domain_strategy
	else
		domain_strategy_node = domain_strategy_default
	end

	local function valid_chain_node(node)
		if not node then return "" end
		local cp = uci:get(appname, node, "chain_proxy") or ""
		local am = uci:get(appname, node, "add_mode") or "0"
		chain_node_type = (cp == "" and am ~= "2") and (uci:get(appname, node, "type") or "") or ""
		if chain_node_type ~= "Xray" and chain_node_type ~= "sing-box" then
			chain_node_type = ""
			return ""
		end
		return node
	end

	preproxy_node_group = (value.chain_proxy == "1") and valid_chain_node(value.preproxy_node) or ""
	to_node_group = (value.chain_proxy == "2") and valid_chain_node(value.to_node) or ""
end

local function vectra_preview_count_non_empty_lines(raw)
	local count = 0
	for _, line in ipairs(split((raw or ""):gsub("\r\n", "\n"), "\n")) do
		if line and not tostring(line):match("^%s*$") then
			count = count + 1
		end
	end
	return count
end

local function vectra_preview_detect_payload_mode(raw)
	raw = vectra_preview_trim(raw) or ""
	if raw == "" then
		return "unknown"
	end
	if raw:find("^ssd://") then
		return "ssd-json"
	end
	if raw:find("://") and not raw:find("\n") then
		return "single-link"
	end
	local decoded = base64Decode(raw)
	if decoded and decoded:find("://") then
		return "base64-lines"
	end
	if raw:find("://") then
		return "plain-lines"
	end
	return "unknown"
end

local function vectra_preview_count_payload_nodes(raw, payload_mode)
	raw = vectra_preview_trim(raw) or ""
	if raw == "" then
		return 0
	end
	if payload_mode == "single-link" then
		return 1
	end
	if payload_mode == "plain-lines" then
		return vectra_preview_count_non_empty_lines(raw)
	end
	if payload_mode == "base64-lines" then
		local decoded = base64Decode(raw)
		if not decoded then return nil end
		return vectra_preview_count_non_empty_lines(decoded)
	end
	if payload_mode == "ssd-json" then
		local payload = raw:gsub("^ssd://", "")
		local decoded = base64Decode(payload)
		if not decoded then return nil end
		local parsed = jsonParse(decoded)
		if type(parsed) == "table" and type(parsed.servers) == "table" then
			return #parsed.servers
		end
		return nil
	end
	return nil
end

local function vectra_preview_safe_extras(node)
	local extras = {}
	local reserved = {
		[".name"] = true,
		[".type"] = true,
		remarks = true,
		type = true,
		protocol = true,
		group = true,
		address = true,
		port = true,
		username = true,
		password = true,
		transport = true,
		tls = true
	}

	for key, value in pairs(node or {}) do
		if not reserved[key] then
			local value_type = type(value)
			if value_type == "string" or value_type == "number" or value_type == "boolean" then
				extras[key] = value
			elseif value_type == "table" then
				local list = {}
				local valid = true
				for _, entry in ipairs(value) do
					if type(entry) ~= "string" then
						valid = false
						break
					end
					list[#list + 1] = entry
				end
				if valid then
					extras[key] = list
				end
			end
		end
	end

	return extras
end

local function vectra_preview_collect_resolved_nodes()
	local resolved = {}
	for _, bucket in ipairs(nodeResult or {}) do
		for _, node in ipairs(bucket.list or {}) do
			resolved[#resolved + 1] = {
				label = vectra_preview_trim(node.remarks),
				protocol = vectra_preview_trim(node.protocol),
				address = vectra_preview_trim(node.address),
				port = tonumber(node.port),
				username = vectra_preview_trim(node.username),
				password = vectra_preview_trim(node.password),
				transport = vectra_preview_trim(node.transport),
				tls = vectra_preview_bool(node.tls),
				extras = vectra_preview_safe_extras(node)
			}
		end
	end
	return resolved
end

local checked_at = os.date("!%Y-%m-%dT%H:%M:%SZ")
local results = {}
local subscribe_list = {}

uci:foreach(appname, "subscribe_list", function(o)
	subscribe_list[#subscribe_list + 1] = o
end)

for _, value in ipairs(subscribe_list) do
	vectra_preview_reset_defaults()
	vectra_preview_apply_subscription_overrides(value)
	nodeResult = {}
	subscribe_info = {}

	local cfgid = value[".name"]
	local remark = vectra_preview_trim(value.remark) or cfgid or "subscription"
	local url = vectra_preview_trim(value.url) or ""
	local enabled = value.enabled == nil or tostring(value.enabled) == "1"
	local access_mode = vectra_preview_access_mode(value.access_mode)
	local user_agent = vectra_preview_trim(value.user_agent)
	local fetch_state = enabled and "network_error" or "disabled"
	local http_status = nil
	local payload_mode = "unknown"
	local payload_node_count = nil
	local resolved_nodes = {}
	local tmp_file = "/tmp/" .. tostring(cfgid or "subscription") .. ".vectra-preview"

	if enabled and url ~= "" then
		local code = curl(url, tmp_file, user_agent, value.access_mode)
		http_status = tonumber(code)

		if http_status == 200 then
			local raw_handle = io.open(tmp_file, "r")
			local raw_stdout = raw_handle and raw_handle:read("*a") or ""
			if raw_handle then raw_handle:close() end
			local raw_data = api.trim(raw_stdout or "")
			payload_mode = vectra_preview_detect_payload_mode(raw_data)
			payload_node_count = vectra_preview_count_payload_nodes(raw_data, payload_mode)
			fetch_state = "ok"
			local ok = pcall(function()
				if raw_data and #raw_data > 0 then
					parse_link(raw_data, "2", remark, cfgid)
				end
			end)
			if not ok then
				fetch_state = "parse_error"
				nodeResult = {}
			end
			resolved_nodes = vectra_preview_collect_resolved_nodes()
		elseif http_status and http_status > 0 then
			fetch_state = "http_error"
		else
			fetch_state = "network_error"
		end
	elseif enabled and url == "" then
		fetch_state = "http_error"
		http_status = 404
	end

	os.remove(tmp_file)

	results[#results + 1] = {
		subscriptionId = cfgid,
		remark = remark,
		url = url,
		enabled = enabled,
		accessMode = access_mode,
		userAgent = user_agent,
		fetchState = fetch_state,
		httpStatus = http_status,
		payloadMode = payload_mode,
		payloadNodeCount = payload_node_count,
		resolvedNodes = resolved_nodes,
		checkedAt = checked_at
	}
end

io.write(json.stringify({
	checkedAt = checked_at,
	entries = results
}))
]=]

local loader = loadstring or load
local chunk, err = loader("arg = {}\n" .. source .. "\n" .. injected, "@/usr/share/passwall2/subscribe.lua")
if not chunk then
  io.stderr:write((err or "failed to load subscription preview helper") .. "\n")
  os.exit(1)
end

local ok, runtimeErr = pcall(chunk)
if not ok then
  io.stderr:write(tostring(runtimeErr) .. "\n")
  os.exit(1)
end
