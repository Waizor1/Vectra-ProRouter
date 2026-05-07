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

func containsCommand(commands []string, expected string) bool {
	for _, command := range commands {
		if command == expected {
			return true
		}
	}
	return false
}
