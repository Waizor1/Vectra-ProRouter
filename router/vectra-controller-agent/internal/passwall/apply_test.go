package passwall

import "testing"

func TestRenderNodeCommandsPrefersShuntRuleTargetsOverStaleNodeExtras(t *testing.T) {
	config := DesiredConfig{
		BasicSettings: BasicSettingsConfig{
			ShuntRules: []ShuntRule{
				{
					ID:             "WorldProxy",
					Label:          "WorldProxy",
					OutboundNodeID: "node-new",
				},
			},
		},
		Nodes: []NodeConfig{
			{
				ID:       "myshunt",
				Label:    "Main shunt",
				Protocol: "shunt",
				Enabled:  true,
				Group:    "default",
				Extras: map[string]any{
					"WorldProxy":   "node-old",
					"default_node": "_direct",
				},
			},
			{
				ID:       "node-new",
				Label:    "New node",
				Protocol: "vless",
				Enabled:  true,
				Group:    "default",
			},
		},
	}

	commands := renderNodeCommands(config)

	if !containsCommand(commands, "set passwall2.myshunt.WorldProxy='node-new'") {
		t.Fatalf("expected shunt rule target command, commands=%v", commands)
	}
	if containsCommand(commands, "set passwall2.myshunt.WorldProxy='node-old'") {
		t.Fatalf("stale node extra overrode shunt rule target, commands=%v", commands)
	}
	if !containsCommand(commands, "set passwall2.myshunt.default_node='_direct'") {
		t.Fatalf("expected unrelated shunt node extras to be preserved, commands=%v", commands)
	}
}

func TestRenderNodeCommandsPreservesLatestPasswallNodeExtras(t *testing.T) {
	config := DesiredConfig{
		Nodes: []NodeConfig{
			{
				ID:       "node-main",
				Label:    "Main node",
				Protocol: "vless",
				Enabled:  true,
				Group:    "default",
				Extras: map[string]any{
					"mkcp_mtu":       1400,
					"tls_pinSHA256": "sha256-fingerprint",
				},
			},
		},
	}

	commands := renderNodeCommands(config)

	if !containsCommand(commands, "set passwall2.node_main.mkcp_mtu='1400'") {
		t.Fatalf("expected mkcp_mtu extra to be rendered, commands=%v", commands)
	}
	if !containsCommand(commands, "set passwall2.node_main.tls_pinSHA256='sha256-fingerprint'") {
		t.Fatalf("expected tls_pinSHA256 extra to be rendered, commands=%v", commands)
	}
}

func TestRenderSubscriptionCommandsPreservesDomainResolverExtras(t *testing.T) {
	config := DesiredConfig{
		Subscriptions: SubscriptionSettings{
			Items: []SubscriptionEntry{
				{
					ID:      "sub-main",
					Remark:  "Main sub",
					URL:     "https://example.com/sub",
					Enabled: true,
					AddMode: "2",
					Extras: map[string]any{
						"domain_resolver":           "https",
						"domain_resolver_dns_https": "https://dns.example/dns-query",
						"domain_strategy":           "UseIPv4",
					},
				},
			},
		},
	}

	commands := renderSubscriptionCommands(config)

	if !containsCommand(commands, "set passwall2.vectra_sub_sub_main.domain_resolver='https'") {
		t.Fatalf("expected domain_resolver extra to be rendered, commands=%v", commands)
	}
	if !containsCommand(commands, "set passwall2.vectra_sub_sub_main.domain_resolver_dns_https='https://dns.example/dns-query'") {
		t.Fatalf("expected domain_resolver_dns_https extra to be rendered, commands=%v", commands)
	}
	if !containsCommand(commands, "set passwall2.vectra_sub_sub_main.domain_strategy='UseIPv4'") {
		t.Fatalf("expected domain_strategy extra to be rendered, commands=%v", commands)
	}
}

func containsCommand(commands []string, expected string) bool {
	for _, command := range commands {
		if command == expected {
			return true
		}
	}
	return false
}
