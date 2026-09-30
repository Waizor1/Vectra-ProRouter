package main

import (
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/firewall"
)

// The load guards have the owner's switches (UCI p2p_bypass '0', admit_rate,
// pace_rate; '0' switches a rate off): a guard that misjudges a household is
// switched off, not a reason to downgrade (review of r23).
func TestTheLoadGuardsFollowTheOwnersSwitches(t *testing.T) {
	base := firewall.DefaultSpec(12345, 1)
	if s := withLoadGuards(base, agentcfg.Config{}); s.P2PBypass != base.P2PBypass || s.AdmitRate != base.AdmitRate || s.PaceRate != base.PaceRate {
		t.Fatalf("no switch changed the defaults: %+v", s)
	}
	zero, ten := 0, 10
	s := withLoadGuards(base, agentcfg.Config{NoP2PBypass: true, AdmitRate: &zero, PaceRate: &ten})
	if s.P2PBypass || s.AdmitRate != 0 || s.PaceRate != 10 || s.PaceBurst != 40 {
		t.Fatalf("switches not applied: p2p %v admit %d pace %d/%d", s.P2PBypass, s.AdmitRate, s.PaceRate, s.PaceBurst)
	}
}

// The owner, 2026-09-30: the provider's nodes carry no IPv6 — the router
// refuses the LAN's IPv6 unless told the nodes carry it (UCI ipv6 '1').
func TestTheLANsIPv6IsRefusedUnlessTheNodesCarryIt(t *testing.T) {
	base := firewall.DefaultSpec(12345, 1)
	if s := withLoadGuards(base, agentcfg.Config{}); !s.RefuseIPv6 {
		t.Fatal("IPv6 not refused by default")
	}
	if s := withLoadGuards(base, agentcfg.Config{IPv6: true}); s.RefuseIPv6 {
		t.Fatal("IPv6 refused though the nodes carry it")
	}
}
