package passwall

import (
	"context"
	"errors"
	"testing"
)

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

// TestExecutorApplyRevertsOnBatchFailure verifies the regression behaviour
// added alongside r25: when `uci batch` fails partway through, the Executor
// must best-effort call `uci revert` to discard staged-but-not-committed
// changes so the next caller doesn't silently inherit them.
func TestExecutorApplyRevertsOnBatchFailure(t *testing.T) {
	backend := &fakeBackend{
		lines: []string{
			"passwall2.oldglobal=global",
		},
		batchErr: errors.New("uci batch: syntax error near line 3"),
	}

	cfg := DesiredConfig{
		BasicSettings: BasicSettingsConfig{
			Main: MainSettings{MainSwitch: true, SelectedNodeID: "node-main"},
		},
		Nodes: []NodeConfig{{ID: "node-main", Label: "Main", Protocol: "vmess", Enabled: true}},
	}

	_, err := NewExecutor(backend).Apply(context.Background(), cfg, ApplyOptions{})
	if err == nil {
		t.Fatal("expected apply to return batch error")
	}

	if len(backend.revertedPackages) != 1 || backend.revertedPackages[0] != "passwall2" {
		t.Fatalf("expected exactly one revert of passwall2, got %v", backend.revertedPackages)
	}
}

// TestExecutorApplyDoesNotRevertOnSuccess verifies the revert path is gated on
// failure — successful applies must NOT call revert (which would discard the
// commit that just succeeded).
func TestExecutorApplyDoesNotRevertOnSuccess(t *testing.T) {
	backend := &fakeBackend{
		lines: []string{"passwall2.oldglobal=global"},
	}

	cfg := DesiredConfig{
		BasicSettings: BasicSettingsConfig{Main: MainSettings{MainSwitch: true, SelectedNodeID: "node-main"}},
		Nodes:         []NodeConfig{{ID: "node-main", Label: "Main", Protocol: "vmess", Enabled: true}},
	}

	if _, err := NewExecutor(backend).Apply(context.Background(), cfg, ApplyOptions{}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	if len(backend.revertedPackages) != 0 {
		t.Fatalf("expected no revert on success, got %v", backend.revertedPackages)
	}
}

// TestExecutorApplyPopulatesAppliedDigest verifies that after a successful
// apply the result includes a digest computed from the post-apply uci state.
// This is what the control plane uses to detect silent drift between the
// desired config and what passwall2 actually settled on (e.g. if subscribe.lua
// or rule_update.lua wrote new values that were not in the desired payload).
func TestExecutorApplyPopulatesAppliedDigest(t *testing.T) {
	backend := &fakeBackend{
		lines: []string{"passwall2.oldglobal=global"},
		// Simulate the post-apply state: the desired config rendered to a new
		// set of uci lines (we approximate it by setting appliedLines to the
		// batch-produced section names; the actual digest contents don't have
		// to match desired exactly — we just want AppliedDigest non-empty and
		// distinct from a deliberately-different baseline).
		appliedLines: []string{
			"passwall2.vectra_global=global",
			"passwall2.vectra_global.enabled='1'",
			"passwall2.vectra_global.node='node-main'",
			"passwall2.node_main=nodes",
			"passwall2.node_main.remarks='Main'",
		},
	}

	cfg := DesiredConfig{
		BasicSettings: BasicSettingsConfig{Main: MainSettings{MainSwitch: true, SelectedNodeID: "node-main"}},
		Nodes:         []NodeConfig{{ID: "node-main", Label: "Main", Protocol: "vmess", Enabled: true}},
	}

	result, err := NewExecutor(backend).Apply(context.Background(), cfg, ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	if result.ConfigDigest == "" {
		t.Fatal("expected ConfigDigest to be populated")
	}
	if result.AppliedDigest == "" {
		t.Fatal("expected AppliedDigest to be populated from post-apply Show")
	}
	if backend.showCount < 2 {
		t.Fatalf("expected at least 2 Show calls (before + after), got %d", backend.showCount)
	}
}

