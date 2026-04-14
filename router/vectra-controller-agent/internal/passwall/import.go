package passwall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Importer struct {
	Backend UCIBackend
}

func NewImporter(backend UCIBackend) Importer {
	return Importer{Backend: backend}
}

func (i Importer) Import(ctx context.Context, source string) (ImportedState, error) {
	if i.Backend == nil {
		i.Backend = ExecBackend{}
	}

	lines, err := i.Backend.Show(ctx, "passwall2")
	if err != nil {
		return ImportedState{}, err
	}

	sections, err := ParseUCILines(lines)
	if err != nil {
		return ImportedState{}, err
	}

	config := importDesiredConfig(sections)
	digest, err := computeConfigDigest(config)
	if err != nil {
		return ImportedState{}, err
	}

	return ImportedState{
		Config:       config,
		ConfigDigest: digest,
		ImportedAt:   time.Now().UTC().Format(time.RFC3339),
		Source:       source,
		RawSnapshot: map[string]any{
			"uciLines": lines,
			"sections": snapshotSections(sections),
		},
	}, nil
}

func importDesiredConfig(sections []UCISection) DesiredConfig {
	config := DesiredConfig{
		SchemaVersion: 1,
		BasicSettings: BasicSettingsConfig{
			Main: MainSettings{
				LocalhostProxy:     true,
				ClientProxy:        true,
				NodeSocksPort:      1070,
				NodeSocksBindLocal: true,
			},
			DNS: DNSSettings{
				DirectQueryStrategy:    "UseIP",
				RemoteDNSProtocol:      "tcp",
				RemoteDNS:              "1.1.1.1",
				RemoteDNSDOH:           "https://1.1.1.1/dns-query",
				RemoteDNSDetour:        "remote",
				RemoteDNSQueryStrategy: "UseIPv4",
				DNSRedirect:            true,
			},
			Log: LogSettings{
				EnableNodeLog: true,
				Level:         "error",
			},
			Maintenance: MaintenanceSettings{
				BackupPaths: []string{
					"/etc/config/passwall2",
					"/etc/config/passwall2_server",
					"/usr/share/passwall2/domains_excluded",
				},
			},
		},
		Subscriptions: SubscriptionSettings{
			FilterKeywordMode: "0",
			TypePreferences:   SubscriptionTypes{},
			DomainStrategy:    "auto",
		},
		AppUpdate: AppUpdateConfig{
			BinaryPaths: BinaryPathConfig{
				Xray:     "/usr/bin/xray",
				SingBox:  "/usr/bin/sing-box",
				Hysteria: "/usr/bin/hysteria",
				Geoview:  "/usr/bin/geoview",
			},
			UpdateStrategy: "package-preferred",
			TargetVersions: TargetVersionConfig{},
		},
		RuleManage: RuleManageConfig{
			GeoIPURL:       "https://github.com/Loyalsoldier/geoip/releases/latest/download/geoip.dat",
			GeoSiteURL:     "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
			AssetDirectory: "/usr/share/v2ray/",
			ScheduleMode:   "daily",
			EnabledAssets:  []string{"geoip", "geosite"},
		},
	}

	sectionsByType := make(map[string][]UCISection)
	sectionByName := make(map[string]UCISection, len(sections))
	for _, section := range sections {
		sectionsByType[section.Type] = append(sectionsByType[section.Type], section)
		sectionByName[section.Name] = section
	}

	if globals := sectionsByType["global"]; len(globals) > 0 {
		global := globals[0]
		config.BasicSettings.Main.MainSwitch = optionBool(global, "enabled", false)
		config.BasicSettings.Main.SelectedNodeID = optionString(global, "node")
		config.BasicSettings.Main.LocalhostProxy = optionBool(global, "localhost_proxy", true)
		config.BasicSettings.Main.ClientProxy = optionBool(global, "client_proxy", true)
		config.BasicSettings.Main.NodeSocksPort = optionInt(global, "node_socks_port", 1070)
		config.BasicSettings.Main.NodeSocksBindLocal = optionBool(global, "node_socks_bind_local", true)
		config.BasicSettings.Main.SocksMainSwitch = optionBool(global, "socks_enabled", false)
		config.BasicSettings.Main.Extras = collectExtras(global, map[string]struct{}{
			"enabled": {}, "node": {}, "localhost_proxy": {}, "client_proxy": {},
			"node_socks_port": {}, "node_socks_bind_local": {}, "socks_enabled": {},
			"direct_dns_query_strategy": {}, "remote_dns_protocol": {}, "remote_dns": {},
			"remote_dns_doh": {}, "remote_dns_client_ip": {}, "remote_dns_detour": {},
			"remote_fakedns": {}, "remote_dns_query_strategy": {}, "dns_hosts": {},
			"dns_redirect": {}, "timestamp": {}, "log_node": {}, "loglevel": {},
		})
		config.BasicSettings.DNS.DirectQueryStrategy = optionStringDefault(global, "direct_dns_query_strategy", "UseIP")
		config.BasicSettings.DNS.RemoteDNSProtocol = optionStringDefault(global, "remote_dns_protocol", "tcp")
		config.BasicSettings.DNS.RemoteDNS = optionStringDefault(global, "remote_dns", "1.1.1.1")
		config.BasicSettings.DNS.RemoteDNSDOH = optionStringDefault(global, "remote_dns_doh", "https://1.1.1.1/dns-query")
		config.BasicSettings.DNS.RemoteDNSClientIP = optionString(global, "remote_dns_client_ip")
		config.BasicSettings.DNS.RemoteDNSDetour = optionStringDefault(global, "remote_dns_detour", "remote")
		config.BasicSettings.DNS.RemoteFakeDNS = optionBool(global, "remote_fakedns", false)
		config.BasicSettings.DNS.RemoteDNSQueryStrategy = optionStringDefault(global, "remote_dns_query_strategy", "UseIPv4")
		config.BasicSettings.DNS.DNSHosts = splitMultiline(optionString(global, "dns_hosts"))
		config.BasicSettings.DNS.DNSRedirect = optionBool(global, "dns_redirect", true)
		config.BasicSettings.Log.EnableNodeLog = optionBool(global, "log_node", true)
		config.BasicSettings.Log.Level = optionStringDefault(global, "loglevel", "error")
	}

	if rules := sectionsByType["global_rules"]; len(rules) > 0 {
		ruleSection := rules[0]
		config.RuleManage.GeoIPURL = optionStringDefault(ruleSection, "geoip_url", config.RuleManage.GeoIPURL)
		config.RuleManage.GeoSiteURL = optionStringDefault(ruleSection, "geosite_url", config.RuleManage.GeoSiteURL)
		config.RuleManage.AssetDirectory = optionStringDefault(ruleSection, "v2ray_location_asset", config.RuleManage.AssetDirectory)
		config.RuleManage.AutoUpdate = optionBool(ruleSection, "auto_update", false)
		config.RuleManage.EnabledAssets = enabledAssetsFromSection(ruleSection)
		config.RuleManage.Extras = collectExtras(ruleSection, map[string]struct{}{
			"geoip_url": {}, "geosite_url": {}, "v2ray_location_asset": {}, "auto_update": {}, "geoip_update": {}, "geosite_update": {},
		})
	}

	if apps := sectionsByType["global_app"]; len(apps) > 0 {
		appSection := apps[0]
		config.AppUpdate.BinaryPaths = BinaryPathConfig{
			Xray:     optionStringDefault(appSection, "xray_file", config.AppUpdate.BinaryPaths.Xray),
			SingBox:  optionStringDefault(appSection, "sing_box_file", config.AppUpdate.BinaryPaths.SingBox),
			Hysteria: optionStringDefault(appSection, "hysteria_file", config.AppUpdate.BinaryPaths.Hysteria),
			Geoview:  optionStringDefault(appSection, "geoview_file", config.AppUpdate.BinaryPaths.Geoview),
		}
		config.AppUpdate.Extras = collectExtras(appSection, map[string]struct{}{
			"xray_file": {}, "sing_box_file": {}, "hysteria_file": {}, "geoview_file": {},
		})
	}

	if subs := sectionsByType["global_subscribe"]; len(subs) > 0 {
		subSection := subs[0]
		config.Subscriptions.FilterKeywordMode = optionStringDefault(subSection, "filter_keyword_mode", "0")
		config.Subscriptions.DiscardList = optionList(subSection, "filter_discard_list")
		config.Subscriptions.KeepList = optionList(subSection, "filter_keep_list")
		config.Subscriptions.TypePreferences = SubscriptionTypes{
			Shadowsocks: optionString(subSection, "ss_type"),
			Trojan:      optionString(subSection, "trojan_type"),
			Vmess:       optionString(subSection, "vmess_type"),
			Vless:       optionString(subSection, "vless_type"),
			Hysteria2:   optionString(subSection, "hysteria2_type"),
		}
		config.Subscriptions.DomainStrategy = mapSubscriptionDomainStrategy(optionStringDefault(subSection, "domain_strategy", ""))
	}

	shuntNode := findSelectedShuntNode(sectionByName, config.BasicSettings.Main.SelectedNodeID)
	for _, section := range sectionsByType["shunt_rules"] {
		rule := ShuntRule{
			ID:          section.Name,
			Label:       optionStringDefault(section, "remarks", section.Name),
			DomainRules: splitMultiline(optionString(section, "domain_list")),
			IPRules:     splitMultiline(optionString(section, "ip_list")),
			Extras:      collectExtras(section, map[string]struct{}{"remarks": {}, "domain_list": {}, "ip_list": {}}),
		}
		if shuntNode.Name != "" {
			rule.OutboundNodeID = optionString(shuntNode, section.Name)
		}
		config.BasicSettings.ShuntRules = append(config.BasicSettings.ShuntRules, rule)
		config.RuleManage.ShuntRules = append(config.RuleManage.ShuntRules, rule)
	}

	for _, section := range sectionsByType["socks"] {
		config.BasicSettings.Socks = append(config.BasicSettings.Socks, SocksConfig{
			ID:                      section.Name,
			Enabled:                 !optionExists(section, "enabled") || optionBool(section, "enabled", true),
			NodeID:                  optionString(section, "node"),
			Port:                    optionInt(section, "port", 0),
			HTTPPort:                optionInt(section, "http_port", 0),
			BindLocal:               optionBool(section, "bind_local", true),
			AutoswitchBackupNodeIDs: optionList(section, "autoswitch_backup_node"),
			Extras: collectExtras(section, map[string]struct{}{
				"enabled": {}, "node": {}, "port": {}, "http_port": {}, "bind_local": {}, "autoswitch_backup_node": {},
			}),
		})
	}

	for _, section := range sectionsByType["nodes"] {
		node := NodeConfig{
			ID:        section.Name,
			Label:     optionStringDefault(section, "remarks", section.Name),
			Protocol:  importNodeProtocol(section),
			Enabled:   !optionExists(section, "enabled") || optionBool(section, "enabled", true),
			Group:     optionStringDefault(section, "group", "default"),
			Address:   optionString(section, "address"),
			Port:      optionInt(section, "port", 0),
			Username:  optionString(section, "username"),
			Password:  optionString(section, "password"),
			Transport: importNodeTransport(section),
			TLS:       boolPointerFromOption(section, "tls"),
			Tags:      append(optionList(section, "tag"), optionList(section, "tags")...),
			Extras: collectExtras(section, map[string]struct{}{
				"remarks": {}, "type": {}, "protocol": {}, "enabled": {}, "group": {}, "address": {}, "port": {},
				"username": {}, "password": {}, "transport": {}, "tls": {}, "tag": {}, "tags": {},
			}),
		}
		config.Nodes = append(config.Nodes, node)
	}

	for _, section := range sectionsByType["subscribe_list"] {
		config.Subscriptions.Items = append(config.Subscriptions.Items, SubscriptionEntry{
			ID:      section.Name,
			Remark:  optionStringDefault(section, "remark", section.Name),
			URL:     optionString(section, "url"),
			Enabled: !optionExists(section, "enabled") || optionBool(section, "enabled", true),
			AddMode: optionStringDefault(section, "add_mode", "2"),
			Metadata: SubscriptionMetadata{
				RemainingTraffic: optionString(section, "rem_traffic"),
				ExpiresAt:        optionString(section, "expired_date"),
			},
			Extras: collectExtras(section, map[string]struct{}{
				"remark": {}, "url": {}, "enabled": {}, "add_mode": {}, "rem_traffic": {}, "expired_date": {},
			}),
		})
	}

	return config
}

func computeConfigDigest(config DesiredConfig) (string, error) {
	payload, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal config digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func snapshotSections(sections []UCISection) []map[string]any {
	out := make([]map[string]any, 0, len(sections))
	for _, section := range sections {
		out = append(out, map[string]any{
			"name":    section.Name,
			"type":    section.Type,
			"options": cloneOptions(section.Options),
		})
	}
	return out
}

func optionExists(section UCISection, key string) bool {
	_, ok := section.Options[key]
	return ok
}

func optionString(section UCISection, key string) string {
	values := section.Options[key]
	if len(values) == 0 {
		return ""
	}
	return values[len(values)-1]
}

func optionStringDefault(section UCISection, key string, fallback string) string {
	if value := optionString(section, key); value != "" {
		return value
	}
	return fallback
}

func optionList(section UCISection, key string) []string {
	values := section.Options[key]
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		for _, item := range splitMultiline(value) {
			if item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

func optionBool(section UCISection, key string, fallback bool) bool {
	value := optionString(section, key)
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func optionInt(section UCISection, key string, fallback int) int {
	value := optionString(section, key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func collectExtras(section UCISection, known map[string]struct{}) map[string]any {
	extras := map[string]any{}
	for _, key := range sortedKeys(section.Options) {
		if _, ok := known[key]; ok {
			continue
		}
		values := section.Options[key]
		if len(values) == 1 {
			extras[key] = values[0]
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		extras[key] = copied
	}
	if len(extras) == 0 {
		return nil
	}
	return extras
}

func splitMultiline(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func boolPointerFromOption(section UCISection, key string) *bool {
	if !optionExists(section, key) {
		return nil
	}
	value := optionBool(section, key, false)
	return &value
}

func importNodeProtocol(section UCISection) string {
	protocol := strings.ToLower(optionString(section, "protocol"))
	switch protocol {
	case "_shunt":
		return "shunt"
	case "_balancing":
		return "balancing"
	case "_urltest":
		return "urltest"
	case "shadowsocks", "trojan", "vmess", "vless", "socks":
		return protocol
	}

	nodeType := strings.ToLower(optionString(section, "type"))
	switch nodeType {
	case "ss":
		return "shadowsocks"
	case "ss-rust":
		return "shadowsocks-rust"
	}

	if protocol != "" {
		return protocol
	}
	if nodeType != "" {
		return nodeType
	}
	return "custom"
}

func importNodeTransport(section UCISection) string {
	transport := strings.ToLower(optionString(section, "transport"))
	switch transport {
	case "raw":
		return "tcp"
	default:
		return transport
	}
}

func mapSubscriptionDomainStrategy(value string) string {
	switch value {
	case "prefer_ipv4", "prefer_ipv6", "ipv4_only", "ipv6_only":
		return value
	default:
		return "auto"
	}
}

func enabledAssetsFromSection(section UCISection) []string {
	assets := make([]string, 0, 2)
	if optionBool(section, "geoip_update", true) {
		assets = append(assets, "geoip")
	}
	if optionBool(section, "geosite_update", true) {
		assets = append(assets, "geosite")
	}
	if len(assets) == 0 {
		return []string{}
	}
	return assets
}

func findSelectedShuntNode(byName map[string]UCISection, selectedNodeID string) UCISection {
	if selectedNodeID == "" {
		return UCISection{}
	}
	section, ok := byName[selectedNodeID]
	if !ok || optionString(section, "protocol") != "_shunt" {
		return UCISection{}
	}
	return section
}
