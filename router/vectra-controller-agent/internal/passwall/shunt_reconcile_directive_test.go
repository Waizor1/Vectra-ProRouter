package passwall

import (
	"context"
	"strings"
	"testing"
)

// andrey-avito, 2026-09-28: the subscription re-minted every node ID, the last
// desired revision still remembered YouTube on the Russia exit, and the panel
// directive had moved YouTube to Finland. The shunt self-heal put YouTube back on
// the Russia label, the directive self-heal put it back on Finland, and the two
// traded four bindings (and a PassWall restart) on every check-in.
func andreyAvitoPingPongBackend() *fakeBackend {
	return &fakeBackend{
		lines: []string{
			"passwall2.myshunt=nodes",
			"passwall2.myshunt.remarks='Маршрутизатор BloopCat'",
			"passwall2.myshunt.type='Xray'",
			"passwall2.myshunt.protocol='_shunt'",
			"passwall2.myshunt.YouTube='fin_node'",
			"passwall2.myshunt.Tiktok='bad_tiktok'",
			"passwall2.YouTube=shunt_rules",
			"passwall2.YouTube.remarks='YouTube'",
			"passwall2.Tiktok=shunt_rules",
			"passwall2.Tiktok.remarks='Tiktok'",
			"passwall2.fin_node=nodes",
			"passwall2.fin_node.remarks='🇷🇺🇫🇮 ⚡Финляндия YouTube 🚫Ad🚫'",
			"passwall2.fin_node.type='Xray'",
			"passwall2.fin_node.protocol='vless'",
			"passwall2.fin_node.transport='grpc'",
			"passwall2.fin_node.address='ru5.nfnpx.online'",
			"passwall2.fin_node.port='50054'",
			"passwall2.rus_node=nodes",
			"passwall2.rus_node.remarks='🇷🇺⚡Россия YouTube 🚫Ad🚫'",
			"passwall2.rus_node.type='Xray'",
			"passwall2.rus_node.protocol='vless'",
			"passwall2.rus_node.transport='grpc'",
			"passwall2.rus_node.address='ru4.nfnpx.online'",
			"passwall2.rus_node.port='50051'",
			"passwall2.by_node=nodes",
			"passwall2.by_node.remarks='🇧🇾 Беларусь '",
			"passwall2.by_node.type='Xray'",
			"passwall2.by_node.protocol='vless'",
			"passwall2.by_node.transport='raw'",
			"passwall2.by_node.address='by2.nfnpx.online'",
			"passwall2.by_node.port='443'",
			"passwall2.bad_tiktok=nodes",
			"passwall2.bad_tiktok.remarks='🇸🇬 Сингапур'",
			"passwall2.bad_tiktok.type='Xray'",
			"passwall2.bad_tiktok.protocol='vless'",
			"passwall2.bad_tiktok.transport='raw'",
			"passwall2.bad_tiktok.address='sg1.nfnpx.online'",
			"passwall2.bad_tiktok.port='443'",
		},
	}
}

func andreyAvitoStaleDesired() DesiredConfig {
	return DesiredConfig{
		BasicSettings: BasicSettingsConfig{
			ShuntRules: []ShuntRule{
				{ID: "YouTube", Label: "YouTube", OutboundNodeID: "old_rus"},
				{ID: "Tiktok", Label: "Tiktok", OutboundNodeID: "old_by"},
			},
		},
		Nodes: []NodeConfig{
			{ID: "myshunt", Label: "Маршрутизатор BloopCat", Protocol: "shunt", Enabled: true},
			{
				ID:        "old_rus",
				Label:     "🇷🇺⚡Россия YouTube 🚫Ad🚫",
				Protocol:  "vless",
				Enabled:   true,
				Address:   "ru4.nfnpx.online",
				Port:      50051,
				Transport: "grpc",
			},
			{
				ID:        "old_by",
				Label:     "🇧🇾 Беларусь ",
				Protocol:  "vless",
				Enabled:   true,
				Address:   "by2.nfnpx.online",
				Port:      443,
				Transport: "raw",
			},
		},
	}
}

func TestShuntSelfHealYieldsSlotsPinnedByPanelDirective(t *testing.T) {
	backend := andreyAvitoPingPongBackend()
	directive := &FleetRoutePolicyDirective{
		Version: "2026-08-03-v4",
		Slots: []FleetRoutePolicyDirectiveSlot{
			{ID: "YouTube", NodeID: "fin_node"},
		},
	}

	result, err := ReconcileShuntBindingsYieldingTo(context.Background(), backend, andreyAvitoStaleDesired(), directive)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	batch := strings.Join(backend.batchCommands, "\n")
	if strings.Contains(batch, "myshunt.YouTube") {
		t.Fatalf("shunt self-heal must leave the directive's YouTube pin alone, got:\n%s", batch)
	}
	// The slot the directive is silent about is still the shunt self-heal's to fix.
	if !result.Changed || !strings.Contains(batch, "set passwall2.myshunt.Tiktok='by_node'") {
		t.Fatalf("expected the unpinned Tiktok slot to be restored, got result=%#v batch:\n%s", result, batch)
	}
}

func TestShuntSelfHealKeepsFullControlWithoutDirectiveBindings(t *testing.T) {
	for name, directive := range map[string]*FleetRoutePolicyDirective{
		"nil":    nil,
		"empty":  {Version: "2026-08-03-v4"},
		"exempt": {Version: "2026-08-03-v4", Exempt: true, Slots: []FleetRoutePolicyDirectiveSlot{{ID: "YouTube", NodeID: "fin_node"}}},
		"blank":  {Version: "2026-08-03-v4", Slots: []FleetRoutePolicyDirectiveSlot{{ID: "YouTube"}}},
	} {
		t.Run(name, func(t *testing.T) {
			backend := andreyAvitoPingPongBackend()
			if _, err := ReconcileShuntBindingsYieldingTo(context.Background(), backend, andreyAvitoStaleDesired(), directive); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			batch := strings.Join(backend.batchCommands, "\n")
			if !strings.Contains(batch, "set passwall2.myshunt.YouTube='rus_node'") {
				t.Fatalf("with no usable pin the desired revision still owns YouTube, got:\n%s", batch)
			}
		})
	}
}

// A directive computed before the subscription re-minted the IDs pins nodes
// that are no longer on the router. The directive self-heal cannot resolve
// them and skips the slot, so the shunt self-heal must keep it — otherwise the
// slot has no owner and its traffic falls to the shunt's default route.
func TestShuntSelfHealKeepsSlotsPinnedToNodesThatAreGone(t *testing.T) {
	for name, pin := range map[string]string{
		"re-minted id": "fin_node_before_refresh",
		"shunt node":   "myshunt",
	} {
		t.Run(name, func(t *testing.T) {
			backend := andreyAvitoPingPongBackend()
			directive := &FleetRoutePolicyDirective{
				Version: "2026-08-03-v4",
				Slots:   []FleetRoutePolicyDirectiveSlot{{ID: "YouTube", NodeID: pin}},
			}
			if _, err := ReconcileShuntBindingsYieldingTo(context.Background(), backend, andreyAvitoStaleDesired(), directive); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			batch := strings.Join(backend.batchCommands, "\n")
			if !strings.Contains(batch, "set passwall2.myshunt.YouTube='rus_node'") {
				t.Fatalf("a pin to a node that is not usable here must not orphan the slot, got:\n%s", batch)
			}
		})
	}
}

func TestShuntSelfHealKeepsSlotsPinnedToADisabledNode(t *testing.T) {
	backend := andreyAvitoPingPongBackend()
	backend.lines = append(backend.lines, "passwall2.fin_node.enabled='0'")
	directive := &FleetRoutePolicyDirective{
		Version: "2026-08-03-v4",
		Slots:   []FleetRoutePolicyDirectiveSlot{{ID: "YouTube", NodeID: "fin_node"}},
	}
	if _, err := ReconcileShuntBindingsYieldingTo(context.Background(), backend, andreyAvitoStaleDesired(), directive); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if batch := strings.Join(backend.batchCommands, "\n"); !strings.Contains(batch, "set passwall2.myshunt.YouTube='rus_node'") {
		t.Fatalf("a pin to a disabled node must not orphan the slot, got:\n%s", batch)
	}
}
