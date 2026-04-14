package passwall

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestBuildApplyPlanIncludesTouchedAreasAndRuntimeSteps(t *testing.T) {
	plan := BuildApplyPlan(DesiredConfig{
		SchemaVersion: 1,
		BasicSettings: BasicSettingsConfig{
			Main: MainSettings{
				MainSwitch:         true,
				SelectedNodeID:     "node-1",
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
			},
			Log: LogSettings{EnableNodeLog: true, Level: "error"},
		},
		Nodes: []NodeConfig{{ID: "node-1", Label: "Node 1", Protocol: "vmess", Enabled: true}},
		Subscriptions: SubscriptionSettings{
			Items: []SubscriptionEntry{{ID: "sub-1", Remark: "Main", URL: "https://example.com/sub", Enabled: true}},
		},
		AppUpdate: AppUpdateConfig{
			BinaryPaths: BinaryPathConfig{Xray: "/usr/bin/xray"},
			TargetVersions: TargetVersionConfig{
				Xray: "1.2.3",
			},
		},
		RuleManage: RuleManageConfig{
			GeoIPURL:      "https://example.com/geoip.dat",
			GeoSiteURL:    "https://example.com/geosite.dat",
			EnabledAssets: []string{"geoip", "geosite"},
		},
	}, ApplyOptions{
		RefreshSubscriptions: true,
		RefreshRules:         true,
		RestartService:       true,
	})

	if len(plan.Operations) != 6 {
		t.Fatalf("expected 6 operations, got %d", len(plan.Operations))
	}
	if !plan.RequiresRestart {
		t.Fatal("expected restart requirement")
	}
	if !plan.RefreshSubscriptions {
		t.Fatal("expected subscription refresh")
	}
	if !plan.RefreshRules {
		t.Fatal("expected rule refresh")
	}
	if !plan.PackageInstall {
		t.Fatal("expected package workflow")
	}
	if plan.Operations[0].Kind != "uci_apply" {
		t.Fatalf("expected first operation to be uci_apply, got %s", plan.Operations[0].Kind)
	}
	if plan.Operations[len(plan.Operations)-1].Kind != "service_restart" {
		t.Fatalf("expected last operation to be service_restart, got %s", plan.Operations[len(plan.Operations)-1].Kind)
	}
}

func TestBuildApplyPlanDoesNotRefreshOrRestartWithoutOptions(t *testing.T) {
	plan := BuildApplyPlan(DesiredConfig{
		SchemaVersion: 1,
		BasicSettings: BasicSettingsConfig{
			Main: MainSettings{
				MainSwitch:         true,
				SelectedNodeID:     "node-1",
				LocalhostProxy:     true,
				ClientProxy:        true,
				NodeSocksPort:      1070,
				NodeSocksBindLocal: true,
			},
		},
		Nodes: []NodeConfig{{ID: "node-1", Label: "Node 1", Protocol: "vmess", Enabled: true}},
		Subscriptions: SubscriptionSettings{
			Items: []SubscriptionEntry{{ID: "sub-1", Remark: "Main", URL: "https://example.com/sub", Enabled: true}},
		},
		RuleManage: RuleManageConfig{
			GeoIPURL:      "https://example.com/geoip.dat",
			GeoSiteURL:    "https://example.com/geosite.dat",
			EnabledAssets: []string{"geoip", "geosite"},
		},
	}, ApplyOptions{})

	if plan.RefreshSubscriptions {
		t.Fatal("expected subscription refresh to stay disabled")
	}
	if plan.RefreshRules {
		t.Fatal("expected rule refresh to stay disabled")
	}
	if plan.RequiresRestart {
		t.Fatal("expected restart to stay disabled")
	}
	for _, operation := range plan.Operations {
		if operation.Kind == "rule_refresh" {
			t.Fatal("did not expect rule_refresh operation without explicit option")
		}
		if operation.Kind == "service_restart" {
			t.Fatal("did not expect service_restart operation without explicit option")
		}
	}
}

func TestParseUCILinesAndImportCurrentState(t *testing.T) {
	lines := []string{
		"passwall2.global=global",
		"passwall2.global.enabled='1'",
		"passwall2.global.node='rulenode'",
		"passwall2.global.localhost_proxy='1'",
		"passwall2.global.client_proxy='1'",
		"passwall2.global.node_socks_port='1080'",
		"passwall2.global.direct_dns_query_strategy='UseIPv4'",
		"passwall2.global.remote_dns_protocol='tcp'",
		"passwall2.global.remote_dns='8.8.8.8'",
		"passwall2.global.remote_dns_query_strategy='UseIPv4'",
		"passwall2.global.dns_hosts='dns.google.com 8.8.8.8\ncloudflare-dns.com 1.1.1.1'",
		"passwall2.global.log_node='1'",
		"passwall2.global.loglevel='warning'",
		"passwall2.rules=global_rules",
		"passwall2.rules.geoip_url='https://example.com/geoip.dat'",
		"passwall2.rules.geosite_url='https://example.com/geosite.dat'",
		"passwall2.rules.v2ray_location_asset='/usr/share/v2ray/'",
		"passwall2.rules.auto_update='1'",
		"passwall2.rules.geoip_update='1'",
		"passwall2.rules.geosite_update='0'",
		"passwall2.app=global_app",
		"passwall2.app.xray_file='/usr/bin/xray'",
		"passwall2.app.sing_box_file='/usr/bin/sing-box'",
		"passwall2.subscribe=global_subscribe",
		"passwall2.subscribe.filter_keyword_mode='3'",
		"passwall2.subscribe.filter_discard_list='RU'",
		"passwall2.subscribe.filter_keep_list='US'",
		"passwall2.subscribe.ss_type='xray'",
		"passwall2.subscribe.domain_strategy='prefer_ipv4'",
		"passwall2.socks_one=socks",
		"passwall2.socks_one.enabled='1'",
		"passwall2.socks_one.node='node_1'",
		"passwall2.socks_one.port='2080'",
		"passwall2.socks_one.http_port='2081'",
		"passwall2.socks_one.bind_local='1'",
		"passwall2.socks_one.autoswitch_backup_node='node_2'",
		"passwall2.rule_cn=shunt_rules",
		"passwall2.rule_cn.remarks='China'",
		"passwall2.rule_cn.protocol='http tls'",
		"passwall2.rule_cn.inbound='tproxy socks'",
		"passwall2.rule_cn.network='tcp'",
		"passwall2.rule_cn.source='geoip:private 192.168.1.0/24'",
		"passwall2.rule_cn.port='443'",
		"passwall2.rule_cn.domain_list='geosite:cn'",
		"passwall2.rule_cn.ip_list='geoip:cn'",
		"passwall2.rule_cn.invert='1'",
		"passwall2.node_1=nodes",
		"passwall2.node_1.remarks='Node 1'",
		"passwall2.node_1.type='Xray'",
		"passwall2.node_1.protocol='vmess'",
		"passwall2.node_1.address='example.com'",
		"passwall2.node_1.port='443'",
		"passwall2.node_1.tls='1'",
		"passwall2.rulenode=nodes",
		"passwall2.rulenode.remarks='Rule Node'",
		"passwall2.rulenode.type='Xray'",
		"passwall2.rulenode.protocol='_shunt'",
		"passwall2.rulenode.rule_cn='node_1'",
		"passwall2.sub_1=subscribe_list",
		"passwall2.sub_1.remark='Default'",
		"passwall2.sub_1.url='https://example.com/sub'",
		"passwall2.sub_1.add_mode='2'",
		"passwall2.sub_1.rem_traffic='10 GB'",
		"passwall2.sub_1.expired_date='2026-05-01'",
	}

	backend := &fakeBackend{lines: lines}
	imported, err := NewImporter(backend).Import(context.Background(), "check_in")
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if imported.Config.SchemaVersion != 1 {
		t.Fatalf("expected schemaVersion 1, got %d", imported.Config.SchemaVersion)
	}
	if imported.Config.BasicSettings.Main.SelectedNodeID != "rulenode" {
		t.Fatalf("expected selected node rulenode, got %s", imported.Config.BasicSettings.Main.SelectedNodeID)
	}
	if imported.Config.BasicSettings.DNS.RemoteDNS != "8.8.8.8" {
		t.Fatalf("expected remote dns 8.8.8.8, got %s", imported.Config.BasicSettings.DNS.RemoteDNS)
	}
	if got, want := imported.Config.Subscriptions.FilterKeywordMode, "3"; got != want {
		t.Fatalf("expected filter mode %s, got %s", want, got)
	}
	if len(imported.Config.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(imported.Config.Nodes))
	}
	if len(imported.Config.BasicSettings.Socks) != 1 {
		t.Fatalf("expected 1 socks config, got %d", len(imported.Config.BasicSettings.Socks))
	}
	if len(imported.Config.Subscriptions.Items) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(imported.Config.Subscriptions.Items))
	}
	if len(imported.Config.RuleManage.EnabledAssets) != 1 || imported.Config.RuleManage.EnabledAssets[0] != "geoip" {
		t.Fatalf("expected geoip-only enabled assets, got %#v", imported.Config.RuleManage.EnabledAssets)
	}
	if imported.Config.BasicSettings.ShuntRules[0].OutboundNodeID != "node_1" {
		t.Fatalf("expected shunt rule outbound node node_1, got %s", imported.Config.BasicSettings.ShuntRules[0].OutboundNodeID)
	}
	if got, want := imported.Config.BasicSettings.ShuntRules[0].Extras["protocol"], "http tls"; got != want {
		t.Fatalf("expected protocol extras %q, got %#v", want, got)
	}
	if got, want := imported.Config.BasicSettings.ShuntRules[0].Extras["inbound"], "tproxy socks"; got != want {
		t.Fatalf("expected inbound extras %q, got %#v", want, got)
	}
	if got, want := imported.Config.BasicSettings.ShuntRules[0].Extras["network"], "tcp"; got != want {
		t.Fatalf("expected network extras %q, got %#v", want, got)
	}
	if got, want := imported.Config.BasicSettings.ShuntRules[0].Extras["source"], "geoip:private 192.168.1.0/24"; got != want {
		t.Fatalf("expected source extras %q, got %#v", want, got)
	}
	if got, want := imported.Config.BasicSettings.ShuntRules[0].Extras["port"], "443"; got != want {
		t.Fatalf("expected port extras %q, got %#v", want, got)
	}
	if got, want := imported.Config.BasicSettings.ShuntRules[0].Extras["invert"], "1"; got != want {
		t.Fatalf("expected invert extras %q, got %#v", want, got)
	}
	if imported.ConfigDigest == "" {
		t.Fatal("expected config digest")
	}
}

func TestParseUCILinesSupportsMultilineQuotedValues(t *testing.T) {
	lines := []string{
		"passwall2.direct=shunt_rules",
		"passwall2.direct.remarks='Direct'",
		"passwall2.direct.domain_list='domain:mos.ru",
		"domain:ozon.ru",
		"domain:avito.ru",
		"domain:sprintbox.ru'",
	}

	sections, err := ParseUCILines(lines)
	if err != nil {
		t.Fatalf("parse uci lines: %v", err)
	}

	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}

	got := optionString(sections[0], "domain_list")
	want := "domain:mos.ru\ndomain:ozon.ru\ndomain:avito.ru\ndomain:sprintbox.ru"
	if got != want {
		t.Fatalf("domain_list = %q, want %q", got, want)
	}

	items := splitMultiline(got)
	if !reflect.DeepEqual(items, []string{
		"domain:mos.ru",
		"domain:ozon.ru",
		"domain:avito.ru",
		"domain:sprintbox.ru",
	}) {
		t.Fatalf("unexpected multiline items: %#v", items)
	}
}

func TestExecutorApplyWritesBatchAndRunsScripts(t *testing.T) {
	backend := &fakeBackend{
		lines: []string{
			"passwall2.oldglobal=global",
			"passwall2.oldnode=nodes",
			"passwall2.oldsub=subscribe_list",
		},
	}
	executor := NewExecutor(backend)

	cfg := DesiredConfig{
		SchemaVersion: 1,
		BasicSettings: BasicSettingsConfig{
			Main: MainSettings{
				MainSwitch:         true,
				SelectedNodeID:     "node-main",
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
			},
			Log:        LogSettings{EnableNodeLog: true, Level: "error"},
			Socks:      []SocksConfig{{ID: "socks-main", Enabled: true, NodeID: "node-main", Port: 2080, BindLocal: true}},
			ShuntRules: []ShuntRule{{
				ID:             "China",
				Label:          "China",
				OutboundNodeID: "node-main",
				DomainRules:    []string{"geosite:cn"},
				IPRules:        []string{"geoip:cn"},
				Extras: map[string]any{
					"protocol": "http tls",
					"inbound":  "tproxy socks",
					"network":  "tcp",
					"source":   "geoip:private 192.168.1.0/24",
					"port":     "443",
					"invert":   "1",
				},
			}},
		},
		Nodes: []NodeConfig{
			{ID: "node-main", Label: "Main", Protocol: "vmess", Enabled: true, Address: "example.com", Port: 443, Transport: "tcp"},
			{ID: "rulenode", Label: "Rule Node", Protocol: "shunt", Enabled: true},
		},
		Subscriptions: SubscriptionSettings{
			FilterKeywordMode: "1",
			Items:             []SubscriptionEntry{{ID: "main", Remark: "Main", URL: "https://example.com/sub", Enabled: true}},
		},
		AppUpdate: AppUpdateConfig{
			BinaryPaths: BinaryPathConfig{
				Xray:     "/usr/bin/xray",
				SingBox:  "/usr/bin/sing-box",
				Hysteria: "/usr/bin/hysteria",
				Geoview:  "/usr/bin/geoview",
			},
			UpdateStrategy: "package-preferred",
		},
		RuleManage: RuleManageConfig{
			GeoIPURL:       "https://example.com/geoip.dat",
			GeoSiteURL:     "https://example.com/geosite.dat",
			AssetDirectory: "/usr/share/v2ray/",
			EnabledAssets:  []string{"geoip", "geosite"},
		},
	}

	result, err := executor.Apply(context.Background(), cfg, ApplyOptions{
		RefreshSubscriptions: true,
		RefreshRules:         true,
		RestartService:       true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(backend.batchCommands) == 0 {
		t.Fatal("expected uci batch commands")
	}
	joinedBatch := strings.Join(backend.batchCommands, "\n")
	for _, needle := range []string{
		"delete passwall2.oldglobal",
		"delete passwall2.oldnode",
		"set passwall2.vectra_global=global",
		"set passwall2.node_main=nodes",
		"set passwall2.rulenode=nodes",
		"set passwall2.vectra_sub_main=subscribe_list",
		"set passwall2.China.protocol='http tls'",
		"set passwall2.China.inbound='tproxy socks'",
		"set passwall2.China.network='tcp'",
		"set passwall2.China.source='geoip:private 192.168.1.0/24'",
		"set passwall2.China.port='443'",
		"set passwall2.China.invert='1'",
		"commit passwall2",
	} {
		if !strings.Contains(joinedBatch, needle) {
			t.Fatalf("expected batch to contain %q", needle)
		}
	}

	gotRuns := backend.runCommands
	wantRuns := []string{
		"lua /usr/share/passwall2/subscribe.lua start all",
		"lua /usr/share/passwall2/rule_update.lua log geoip,geosite",
		"/etc/init.d/passwall2 restart",
	}
	if !reflect.DeepEqual(gotRuns, wantRuns) {
		t.Fatalf("unexpected run commands:\n got %#v\nwant %#v", gotRuns, wantRuns)
	}
	if result.ConfigDigest == "" {
		t.Fatal("expected config digest")
	}
}

func TestExecutorApplyFiltersBenignSubscribeStderr(t *testing.T) {
	backend := &fakeBackend{
		runResults: map[string]CommandResult{
			"lua /usr/share/passwall2/subscribe.lua start all": {
				Command: "lua /usr/share/passwall2/subscribe.lua start all",
				Stderr: strings.Join([]string{
					"tr: write error: Broken pipe",
					"head: standard output: I/O error",
					"real subscribe warning",
				}, "\n"),
			},
		},
	}
	executor := NewExecutor(backend)

	result, err := executor.Apply(context.Background(), DesiredConfig{
		SchemaVersion: 1,
		BasicSettings: BasicSettingsConfig{
			Main: MainSettings{
				LocalhostProxy:     true,
				ClientProxy:        true,
				NodeSocksBindLocal: true,
			},
		},
		Subscriptions: SubscriptionSettings{
			Items: []SubscriptionEntry{{ID: "sub", Remark: "Sub", URL: "https://example.com/sub", Enabled: true}},
		},
	}, ApplyOptions{
		RefreshSubscriptions: true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(result.CommandResults) != 1 {
		t.Fatalf("expected one command result, got %#v", result.CommandResults)
	}
	if result.CommandResults[0].Stderr != "real subscribe warning" {
		t.Fatalf("unexpected stderr after filtering: %q", result.CommandResults[0].Stderr)
	}
}

func TestExecutorApplySanitizesImportedSubscriptionSectionIDs(t *testing.T) {
	backend := &fakeBackend{
		lines: []string{
			"passwall2.oldsub=subscribe_list",
		},
	}

	_, err := NewExecutor(backend).Apply(context.Background(), DesiredConfig{
		SchemaVersion: 1,
		BasicSettings: BasicSettingsConfig{
			Main: MainSettings{
				LocalhostProxy:     true,
				ClientProxy:        true,
				NodeSocksBindLocal: true,
			},
		},
		Subscriptions: SubscriptionSettings{
			Items: []SubscriptionEntry{{
				ID:      "@subscribe_list[0]",
				Remark:  "Imported",
				URL:     "https://example.com/sub",
				Enabled: true,
			}},
		},
	}, ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	joinedBatch := strings.Join(backend.batchCommands, "\n")
	if strings.Contains(joinedBatch, "vectra_sub_@subscribe_list[0]") {
		t.Fatalf("expected invalid raw imported subscription id to be sanitized, got batch:\n%s", joinedBatch)
	}
	if !strings.Contains(joinedBatch, "set passwall2.vectra_sub_subscribe_list_0=subscribe_list") {
		t.Fatalf("expected sanitized subscribe_list section name, got batch:\n%s", joinedBatch)
	}
}

func TestExecutorPropagatesCommandFailures(t *testing.T) {
	backend := &fakeBackend{
		runErrFor: map[string]error{
			"lua /usr/share/passwall2/subscribe.lua start all": errors.New("subscribe failed"),
		},
	}
	executor := NewExecutor(backend)

	_, err := executor.Apply(context.Background(), DesiredConfig{
		SchemaVersion: 1,
		BasicSettings: BasicSettingsConfig{
			Main: MainSettings{
				LocalhostProxy:     true,
				ClientProxy:        true,
				NodeSocksBindLocal: true,
			},
		},
		Subscriptions: SubscriptionSettings{
			Items: []SubscriptionEntry{{ID: "sub", Remark: "Sub", URL: "https://example.com/sub", Enabled: true}},
		},
	}, ApplyOptions{
		RefreshSubscriptions: true,
	})
	if err == nil {
		t.Fatal("expected apply error")
	}
	if !strings.Contains(err.Error(), "subscribe failed") {
		t.Fatalf("expected subscribe failure, got %v", err)
	}
}

func TestExecutorApplyNoopsWhenDesiredAlreadyMatchesCurrent(t *testing.T) {
	lines := []string{
		"passwall2.global=global",
		"passwall2.global.enabled='1'",
		"passwall2.global.node='node_1'",
		"passwall2.global.localhost_proxy='1'",
		"passwall2.global.client_proxy='1'",
		"passwall2.global.node_socks_port='1070'",
		"passwall2.global.node_socks_bind_local='1'",
		"passwall2.global.socks_enabled='0'",
		"passwall2.global.direct_dns_query_strategy='UseIP'",
		"passwall2.global.remote_dns_protocol='tcp'",
		"passwall2.global.remote_dns='1.1.1.1'",
		"passwall2.global.remote_dns_doh='https://1.1.1.1/dns-query'",
		"passwall2.global.remote_dns_detour='remote'",
		"passwall2.global.remote_dns_query_strategy='UseIPv4'",
		"passwall2.global.dns_redirect='1'",
		"passwall2.global.log_node='1'",
		"passwall2.global.loglevel='warning'",
		"passwall2.node_1=nodes",
		"passwall2.node_1.remarks='Node 1'",
		"passwall2.node_1.type='Xray'",
		"passwall2.node_1.protocol='vless'",
		"passwall2.node_1.transport='xhttp'",
		"passwall2.node_1.address='example.com'",
		"passwall2.node_1.port='443'",
		"passwall2.node_1.xhttp_mode='auto'",
		"passwall2.node_1.xhttp_path='/'",
		"passwall2.sub_1=subscribe_list",
		"passwall2.sub_1.remark='Default'",
		"passwall2.sub_1.url='https://example.com/sub'",
		"passwall2.sub_1.add_mode='2'",
	}

	backend := &fakeBackend{lines: lines}
	imported, err := NewImporter(backend).Import(context.Background(), "check_in")
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	result, err := NewExecutor(backend).Apply(context.Background(), imported.Config, ApplyOptions{
		RefreshSubscriptions: true,
		RefreshRules:         true,
		RestartService:       true,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(result.Plan.Operations) != 0 {
		t.Fatalf("expected no-op plan, got %#v", result.Plan.Operations)
	}
	if len(result.UCICommands) != 0 {
		t.Fatalf("expected no UCI commands, got %#v", result.UCICommands)
	}
	if len(result.CommandResults) != 0 {
		t.Fatalf("expected no command results, got %#v", result.CommandResults)
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no batch commands, got %#v", backend.batchCommands)
	}
	if len(backend.runCommands) != 0 {
		t.Fatalf("expected no runtime commands, got %#v", backend.runCommands)
	}
}

type fakeBackend struct {
	lines         []string
	batchCommands []string
	runCommands   []string
	runErrFor     map[string]error
	runResults    map[string]CommandResult
}

func (f fakeBackend) Show(_ context.Context, _ string) ([]string, error) {
	return append([]string(nil), f.lines...), nil
}

func (f *fakeBackend) Batch(_ context.Context, commands []string) error {
	f.batchCommands = append([]string(nil), commands...)
	return nil
}

func (f *fakeBackend) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := name
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}
	f.runCommands = append(f.runCommands, command)
	if f.runErrFor != nil {
		if err, ok := f.runErrFor[command]; ok {
			return CommandResult{Command: command, Stderr: err.Error()}, err
		}
	}
	if f.runResults != nil {
		if result, ok := f.runResults[command]; ok {
			if result.Command == "" {
				result.Command = command
			}
			return result, nil
		}
	}
	return CommandResult{Command: command, Stdout: "ok"}, nil
}
