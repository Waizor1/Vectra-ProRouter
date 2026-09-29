package main

import (
	"reflect"
	"strings"
	"testing"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/inventory"
)

func TestRescueRepairRefusesReconnectOverMissingXray(t *testing.T) {
	missing := &controlplane.RouterInventory{SafetyEvents: []controlplane.RouterSafetyEvent{{
		Type:    inventory.ProxyRuntimeUnusableEventType,
		Source:  "filesystem",
		Message: "xray binary /usr/bin/xray missing",
	}}}

	kept, refused := rescueRepairActionsForRuntime(
		[]string{rescueRepairActionRestartDNSMasq, rescueRepairActionReconnectProxy},
		missing,
	)
	if !reflect.DeepEqual(kept, []string{rescueRepairActionRestartDNSMasq}) {
		t.Fatalf("kept = %#v, want only the dnsmasq restart", kept)
	}
	if !strings.Contains(refused, "xray binary /usr/bin/xray missing") || !strings.Contains(refused, "update xray-runtime") {
		t.Fatalf("refusal must name the evidence and the fix, got %q", refused)
	}

	kept, refused = rescueRepairActionsForRuntime([]string{rescueRepairActionReconnectProxy}, missing)
	if len(kept) != 0 || refused == "" {
		t.Fatalf("a reconnect-only repair over a missing binary must be refused entirely, got %#v %q", kept, refused)
	}
}

func TestRescueRepairKeepsReconnectWhenBinaryIsPresent(t *testing.T) {
	actions := []string{rescueRepairActionReconnectProxy}
	for name, collected := range map[string]*controlplane.RouterInventory{
		"healthy": {},
		"failed start": {SafetyEvents: []controlplane.RouterSafetyEvent{{
			Type:   inventory.ProxyRuntimeUnusableEventType,
			Source: inventory.ProxyRuntimeStartFailureSource,
		}}},
		"no inventory": nil,
	} {
		t.Run(name, func(t *testing.T) {
			kept, refused := rescueRepairActionsForRuntime(actions, collected)
			if !reflect.DeepEqual(kept, actions) || refused != "" {
				t.Fatalf("got %#v %q, want the reconnect kept", kept, refused)
			}
		})
	}
}
