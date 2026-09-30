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

// UCI dns_rate sets the DNS door's rate, queries a second from one device
// ('0' switches it off); its burst is twenty seconds of it — DNS comes in
// page loads.
func TestTheDNSDoorFollowsTheOwnersSwitch(t *testing.T) {
	base := firewall.DefaultSpec(12345, 1)
	if s := withLoadGuards(base, agentcfg.Config{}); s.DNSRate == 0 || s.DNSRate != base.DNSRate || s.DNSBurst != base.DNSBurst {
		t.Fatalf("the default door changed: %d/%d", s.DNSRate, s.DNSBurst)
	}
	zero, hundred := 0, 100
	if s := withLoadGuards(base, agentcfg.Config{DNSRate: &zero}); s.DNSRate != 0 {
		t.Fatalf("dns_rate '0' left the door at %d", s.DNSRate)
	}
	if s := withLoadGuards(base, agentcfg.Config{DNSRate: &hundred}); s.DNSRate != 100 || s.DNSBurst != 2000 {
		t.Fatalf("dns_rate '100': %d/%d, want 100/2000", s.DNSRate, s.DNSBurst)
	}
}

// UCI admit_total_rate opens the router's whole door, new connections a
// second into xray from every device together; its burst is four seconds of
// it. Unset, it stays off.
func TestTheWholeDoorFollowsTheOwnersSwitch(t *testing.T) {
	base := firewall.DefaultSpec(12345, 1)
	if s := withLoadGuards(base, agentcfg.Config{}); s.AdmitTotalRate != 0 {
		t.Fatalf("the whole door is on unset: %d", s.AdmitTotalRate)
	}
	forty := 40
	if s := withLoadGuards(base, agentcfg.Config{AdmitTotalRate: &forty}); s.AdmitTotalRate != 40 || s.AdmitTotalBurst != 160 {
		t.Fatalf("admit_total_rate '40': %d/%d, want 40/160", s.AdmitTotalRate, s.AdmitTotalBurst)
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
