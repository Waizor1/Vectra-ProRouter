package passwall

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

type Executor struct {
	Backend UCIBackend
}

func NewExecutor(backend UCIBackend) Executor {
	return Executor{Backend: backend}
}

func (e Executor) Apply(ctx context.Context, config DesiredConfig, options ApplyOptions) (ApplyResult, error) {
	if e.Backend == nil {
		e.Backend = ExecBackend{}
	}

	currentLines, err := e.Backend.Show(ctx, "passwall2")
	if err != nil {
		return ApplyResult{}, err
	}
	currentSections, err := ParseUCILines(currentLines)
	if err != nil {
		return ApplyResult{}, err
	}

	desiredDigest, err := computeConfigDigest(config)
	if err != nil {
		return ApplyResult{}, err
	}
	currentDigest, err := computeConfigDigest(importDesiredConfig(currentSections))
	if err != nil {
		return ApplyResult{}, err
	}
	if currentDigest == desiredDigest {
		return ApplyResult{
			Plan: ApplyPlan{
				Operations: []Operation{},
			},
			ConfigDigest: desiredDigest,
		}, nil
	}

	plan := BuildApplyPlan(config, options)

	uciCommands := buildBatchCommands(currentSections, config)
	if len(uciCommands) > 0 {
		if err := e.Backend.Batch(ctx, uciCommands); err != nil {
			return ApplyResult{}, err
		}
	}

	commandResults := make([]CommandResult, 0, 3)
	if plan.RefreshSubscriptions {
		result, runErr := e.Backend.Run(ctx, "lua", "/usr/share/passwall2/subscribe.lua", "start", "all")
		result = NormalizeCommandResult(result)
		commandResults = append(commandResults, result)
		if runErr != nil {
			return ApplyResult{}, runErr
		}
	}
	if plan.RefreshRules {
		assets := strings.Join(config.RuleManage.EnabledAssets, ",")
		if assets == "" {
			assets = "geoip,geosite"
		}
		result, runErr := e.Backend.Run(ctx, "lua", "/usr/share/passwall2/rule_update.lua", "log", assets)
		commandResults = append(commandResults, result)
		if runErr != nil {
			return ApplyResult{}, runErr
		}
	}
	if plan.RequiresRestart {
		result, runErr := e.Backend.Run(ctx, "/etc/init.d/passwall2", "restart")
		commandResults = append(commandResults, result)
		if runErr != nil {
			return ApplyResult{}, runErr
		}
	}

	return ApplyResult{
		Plan:           plan,
		ConfigDigest:   desiredDigest,
		UCICommands:    uciCommands,
		CommandResults: commandResults,
	}, nil
}

func buildBatchCommands(current []UCISection, config DesiredConfig) []string {
	commands := renderDeleteManagedSections(current)
	commands = append(commands, renderGlobalCommands(config)...)
	commands = append(commands, renderNodeCommands(config)...)
	commands = append(commands, renderSubscriptionCommands(config)...)
	commands = append(commands, "commit passwall2")
	return stripEmpty(commands)
}

func renderDeleteManagedSections(current []UCISection) []string {
	managedTypes := map[string]struct{}{
		"global": {}, "global_rules": {}, "global_app": {}, "global_subscribe": {},
		"socks": {}, "shunt_rules": {}, "nodes": {}, "subscribe_list": {},
	}
	commands := []string{}
	for _, section := range current {
		if _, ok := managedTypes[section.Type]; ok {
			commands = append(commands, fmt.Sprintf("delete passwall2.%s", section.Name))
		}
	}
	return commands
}

func renderGlobalCommands(config DesiredConfig) []string {
	commands := []string{
		"set passwall2.vectra_global=global",
		setValue("passwall2.vectra_global.enabled", boolString(config.BasicSettings.Main.MainSwitch)),
		setValue("passwall2.vectra_global.node", config.BasicSettings.Main.SelectedNodeID),
		setValue("passwall2.vectra_global.localhost_proxy", boolString(config.BasicSettings.Main.LocalhostProxy)),
		setValue("passwall2.vectra_global.client_proxy", boolString(config.BasicSettings.Main.ClientProxy)),
		setValue("passwall2.vectra_global.node_socks_port", intString(config.BasicSettings.Main.NodeSocksPort)),
		setValue("passwall2.vectra_global.node_socks_bind_local", boolString(config.BasicSettings.Main.NodeSocksBindLocal)),
		setValue("passwall2.vectra_global.socks_enabled", boolString(config.BasicSettings.Main.SocksMainSwitch)),
		setValue("passwall2.vectra_global.direct_dns_query_strategy", config.BasicSettings.DNS.DirectQueryStrategy),
		setValue("passwall2.vectra_global.remote_dns_protocol", config.BasicSettings.DNS.RemoteDNSProtocol),
		setValue("passwall2.vectra_global.remote_dns", config.BasicSettings.DNS.RemoteDNS),
		setValue("passwall2.vectra_global.remote_dns_doh", config.BasicSettings.DNS.RemoteDNSDOH),
		setValue("passwall2.vectra_global.remote_dns_client_ip", config.BasicSettings.DNS.RemoteDNSClientIP),
		setValue("passwall2.vectra_global.remote_dns_detour", config.BasicSettings.DNS.RemoteDNSDetour),
		setValue("passwall2.vectra_global.remote_fakedns", boolString(config.BasicSettings.DNS.RemoteFakeDNS)),
		setValue("passwall2.vectra_global.remote_dns_query_strategy", config.BasicSettings.DNS.RemoteDNSQueryStrategy),
		setValue("passwall2.vectra_global.dns_hosts", strings.Join(config.BasicSettings.DNS.DNSHosts, "\n")),
		setValue("passwall2.vectra_global.dns_redirect", boolString(config.BasicSettings.DNS.DNSRedirect)),
		setValue("passwall2.vectra_global.log_node", boolString(config.BasicSettings.Log.EnableNodeLog)),
		setValue("passwall2.vectra_global.loglevel", defaultString(config.BasicSettings.Log.Level, "error")),
		"set passwall2.vectra_global_rules=global_rules",
		setValue("passwall2.vectra_global_rules.geoip_url", config.RuleManage.GeoIPURL),
		setValue("passwall2.vectra_global_rules.geosite_url", config.RuleManage.GeoSiteURL),
		setValue("passwall2.vectra_global_rules.v2ray_location_asset", config.RuleManage.AssetDirectory),
		setValue("passwall2.vectra_global_rules.auto_update", boolString(config.RuleManage.AutoUpdate)),
		setValue("passwall2.vectra_global_rules.geoip_update", boolString(assetEnabled(config.RuleManage.EnabledAssets, "geoip"))),
		setValue("passwall2.vectra_global_rules.geosite_update", boolString(assetEnabled(config.RuleManage.EnabledAssets, "geosite"))),
		"set passwall2.vectra_global_app=global_app",
		setValue("passwall2.vectra_global_app.xray_file", config.AppUpdate.BinaryPaths.Xray),
		setValue("passwall2.vectra_global_app.sing_box_file", config.AppUpdate.BinaryPaths.SingBox),
		setValue("passwall2.vectra_global_app.hysteria_file", config.AppUpdate.BinaryPaths.Hysteria),
		setValue("passwall2.vectra_global_app.geoview_file", config.AppUpdate.BinaryPaths.Geoview),
		"set passwall2.vectra_global_subscribe=global_subscribe",
		setValue("passwall2.vectra_global_subscribe.filter_keyword_mode", defaultString(config.Subscriptions.FilterKeywordMode, "0")),
	}

	commands = append(commands, setList("passwall2.vectra_global_subscribe.filter_discard_list", config.Subscriptions.DiscardList)...)
	commands = append(commands, setList("passwall2.vectra_global_subscribe.filter_keep_list", config.Subscriptions.KeepList)...)
	commands = append(commands, maybeSet("passwall2.vectra_global_subscribe.ss_type", config.Subscriptions.TypePreferences.Shadowsocks)...)
	commands = append(commands, maybeSet("passwall2.vectra_global_subscribe.trojan_type", config.Subscriptions.TypePreferences.Trojan)...)
	commands = append(commands, maybeSet("passwall2.vectra_global_subscribe.vmess_type", config.Subscriptions.TypePreferences.Vmess)...)
	commands = append(commands, maybeSet("passwall2.vectra_global_subscribe.vless_type", config.Subscriptions.TypePreferences.Vless)...)
	commands = append(commands, maybeSet("passwall2.vectra_global_subscribe.hysteria2_type", config.Subscriptions.TypePreferences.Hysteria2)...)
	commands = append(commands, maybeSet("passwall2.vectra_global_subscribe.domain_strategy", encodeSubscriptionDomainStrategy(config.Subscriptions.DomainStrategy))...)
	commands = append(commands, renderExtras("passwall2.vectra_global", config.BasicSettings.Main.Extras)...)
	commands = append(commands, renderExtras("passwall2.vectra_global_rules", config.RuleManage.Extras)...)
	commands = append(commands, renderExtras("passwall2.vectra_global_app", config.AppUpdate.Extras)...)
	return stripEmpty(commands)
}

func renderNodeCommands(config DesiredConfig) []string {
	commands := make([]string, 0, len(config.BasicSettings.Socks)+len(config.BasicSettings.ShuntRules)+len(config.Nodes))

	for _, socks := range config.BasicSettings.Socks {
		ref := "passwall2." + safeID("vectra_socks_"+socks.ID)
		commands = append(commands, "set "+ref+"=socks")
		commands = append(commands, setValue(ref+".enabled", boolString(socks.Enabled)))
		commands = append(commands, setValue(ref+".node", socks.NodeID))
		commands = append(commands, maybeSet(ref+".port", intString(socks.Port))...)
		commands = append(commands, maybeSet(ref+".http_port", intString(socks.HTTPPort))...)
		commands = append(commands, setValue(ref+".bind_local", boolString(socks.BindLocal)))
		commands = append(commands, setList(ref+".autoswitch_backup_node", socks.AutoswitchBackupNodeIDs)...)
		commands = append(commands, renderExtras(ref, socks.Extras)...)
	}

	for _, rule := range config.BasicSettings.ShuntRules {
		ref := "passwall2." + safeID(rule.ID)
		commands = append(commands, "set "+ref+"=shunt_rules")
		commands = append(commands, setValue(ref+".remarks", rule.Label))
		commands = append(commands, setValue(ref+".domain_list", strings.Join(rule.DomainRules, "\n")))
		commands = append(commands, setValue(ref+".ip_list", strings.Join(rule.IPRules, "\n")))
		commands = append(commands, renderExtras(ref, rule.Extras)...)
	}

	for _, node := range config.Nodes {
		ref := "passwall2." + safeID(node.ID)
		commands = append(commands, "set "+ref+"=nodes")
		commands = append(commands, setValue(ref+".remarks", node.Label))
		commands = append(commands, setValue(ref+".enabled", boolString(node.Enabled)))
		commands = append(commands, maybeSet(ref+".group", defaultString(node.Group, "default"))...)
		commands = append(commands, renderNodeProtocol(ref, node)...)
		commands = append(commands, maybeSet(ref+".address", node.Address)...)
		commands = append(commands, maybeSet(ref+".port", intString(node.Port))...)
		commands = append(commands, maybeSet(ref+".username", node.Username)...)
		commands = append(commands, maybeSet(ref+".password", node.Password)...)
		if node.TLS != nil {
			commands = append(commands, setValue(ref+".tls", boolString(*node.TLS)))
		}
		commands = append(commands, setList(ref+".tag", node.Tags)...)
		commands = append(commands, renderShuntNodeBindings(ref, node, config.BasicSettings.ShuntRules)...)
		commands = append(commands, renderNodeExtras(ref, node, config.BasicSettings.ShuntRules)...)
	}

	return stripEmpty(commands)
}

func renderSubscriptionCommands(config DesiredConfig) []string {
	commands := make([]string, 0, len(config.Subscriptions.Items)*8)
	for _, item := range config.Subscriptions.Items {
		ref := "passwall2." + safeID("vectra_sub_"+item.ID)
		commands = append(commands, "set "+ref+"=subscribe_list")
		commands = append(commands, setValue(ref+".remark", item.Remark))
		commands = append(commands, setValue(ref+".url", item.URL))
		commands = append(commands, setValue(ref+".enabled", boolString(item.Enabled)))
		commands = append(commands, setValue(ref+".add_mode", defaultString(item.AddMode, "2")))
		commands = append(commands, maybeSet(ref+".rem_traffic", item.Metadata.RemainingTraffic)...)
		commands = append(commands, maybeSet(ref+".expired_date", item.Metadata.ExpiresAt)...)
		commands = append(commands, renderExtras(ref, item.Extras)...)
	}
	return stripEmpty(commands)
}

func renderNodeProtocol(ref string, node NodeConfig) []string {
	switch node.Protocol {
	case "shunt":
		return []string{
			setValue(ref+".type", "Xray"),
			setValue(ref+".protocol", "_shunt"),
		}
	case "balancing":
		return []string{
			setValue(ref+".type", "Xray"),
			setValue(ref+".protocol", "_balancing"),
		}
	case "urltest":
		return []string{
			setValue(ref+".type", "sing-box"),
			setValue(ref+".protocol", "_urltest"),
		}
	case "socks":
		return []string{
			setValue(ref+".type", "Xray"),
			setValue(ref+".protocol", "socks"),
			setValue(ref+".transport", normalizeTransport(node.Transport)),
		}
	case "shadowsocks":
		return []string{
			setValue(ref+".type", "Xray"),
			setValue(ref+".protocol", "shadowsocks"),
			setValue(ref+".transport", normalizeTransport(node.Transport)),
		}
	default:
		return []string{
			setValue(ref+".type", "Xray"),
			setValue(ref+".protocol", normalizeProtocol(node.Protocol)),
			setValue(ref+".transport", normalizeTransport(node.Transport)),
		}
	}
}

func renderShuntNodeBindings(ref string, node NodeConfig, rules []ShuntRule) []string {
	if node.Protocol != "shunt" {
		return nil
	}
	commands := []string{}
	for _, rule := range rules {
		if rule.OutboundNodeID == "" {
			continue
		}
		commands = append(commands, setValue(ref+"."+safeID(rule.ID), rule.OutboundNodeID))
	}
	return commands
}

func renderNodeExtras(ref string, node NodeConfig, rules []ShuntRule) []string {
	if node.Protocol != "shunt" {
		return renderExtras(ref, node.Extras)
	}

	ruleIDs := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		ruleIDs[rule.ID] = struct{}{}
	}

	return renderExtrasSkipping(ref, node.Extras, ruleIDs)
}

func renderExtras(ref string, extras map[string]any) []string {
	return renderExtrasSkipping(ref, extras, nil)
}

func renderExtrasSkipping(ref string, extras map[string]any, skipKeys map[string]struct{}) []string {
	if len(extras) == 0 {
		return nil
	}
	keys := sortedKeys(extras)
	commands := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, skip := skipKeys[key]; skip {
			continue
		}
		switch value := extras[key].(type) {
		case string:
			commands = append(commands, setValue(ref+"."+key, value))
		case []string:
			commands = append(commands, setList(ref+"."+key, value)...)
		case []any:
			values := make([]string, 0, len(value))
			for _, item := range value {
				values = append(values, fmt.Sprint(item))
			}
			commands = append(commands, setList(ref+"."+key, values)...)
		default:
			commands = append(commands, setValue(ref+"."+key, fmt.Sprint(value)))
		}
	}
	return commands
}

func stripEmpty(commands []string) []string {
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		if strings.TrimSpace(command) != "" {
			out = append(out, command)
		}
	}
	return out
}

func assetEnabled(assets []string, name string) bool {
	for _, asset := range assets {
		if asset == name {
			return true
		}
	}
	return false
}

func setValue(key string, value string) string {
	return "set " + key + "=" + encodeUCIValue(value)
}

func maybeSet(key string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return []string{setValue(key, value)}
}

func setList(key string, values []string) []string {
	if len(values) == 0 {
		return nil
	}
	commands := []string{"delete " + key}
	for _, value := range values {
		commands = append(commands, "add_list "+key+"="+encodeUCIValue(value))
	}
	return commands
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func intString(value int) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizeProtocol(protocol string) string {
	switch protocol {
	case "", "custom":
		return "vmess"
	default:
		return protocol
	}
}

func normalizeTransport(transport string) string {
	switch transport {
	case "":
		return "raw"
	case "tcp":
		return "raw"
	default:
		return transport
	}
}

func safeID(value string) string {
	if value == "" {
		return "section"
	}

	var builder strings.Builder
	builder.Grow(len(value))

	lastWasUnderscore := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			builder.WriteRune(r)
			lastWasUnderscore = r == '_'
			continue
		}

		if !lastWasUnderscore {
			builder.WriteByte('_')
			lastWasUnderscore = true
		}
	}

	sanitized := strings.Trim(builder.String(), "_")
	if sanitized == "" {
		return "section"
	}

	return sanitized
}

func encodeSubscriptionDomainStrategy(value string) string {
	switch value {
	case "prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only":
		return value
	default:
		return ""
	}
}
