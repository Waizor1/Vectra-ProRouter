package firewall

import (
	"strings"
	"testing"
)

// Peak load (1111, 2026-09-30 03:42–04:20): a torrent client's tracker
// storm through xray to one node got the router cut off from that node, and
// its peers — thousands of connections the provider sends DIRECT anyway —
// through xray took the router to 15 MB of free memory: two sockets and
// their buffers each. The kernel carries a P2P host's peers itself, a
// device's storm waits at the door, and xray's own dials to one node are
// paced.

func indexOf(rules []string, prefix string) int {
	for i, r := range rules {
		if strings.HasPrefix(r, prefix) {
			return i
		}
	}
	return -1
}

func TestAP2PHostsPeersGoByTheKernelNotThroughXray(t *testing.T) {
	out := mustRender(t, DefaultSpec(12345, 1))
	rules := rulesOf(chainNamed(t, out, "prerouting"))
	tproxy := indexOf(rules, "meta l4proto { tcp, udp } counter name \"vctl_tproxy_hits\" tproxy")
	for _, want := range []string{
		`iif != "lo" ct state new ct original packets 1 ip saddr @vctl_p2p4 meta l4proto tcp tcp dport >= 1024 tcp dport != { 4244, 5222-5223, 5228, 5242, 8080, 8443 } update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x10000000 counter name "vctl_p2p_direct" return`,
		`iif != "lo" ct state new ct original packets 1 ip saddr @vctl_p2p4 meta l4proto udp udp dport >= 1024 udp dport != { 3478-3497, 5222-5223, 8801-8810, 16384-16402, 19302-19309, 50000-65535 } update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x10000000 counter name "vctl_p2p_direct" return`,
		`iif != "lo" ct state new ct original packets 1 meta l4proto tcp tcp dport >= 1024 tcp dport != { 4244, 5222-5223, 5228, 5242, 8080, 8443 } update @vctl_p2p_rate4 { ip saddr limit rate over 4/second burst 24 packets } update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x10000000 counter name "vctl_p2p_direct" return`,
		`iif != "lo" ct state new ct original packets 1 meta l4proto udp udp dport >= 1024 udp dport != { 3478-3497, 5222-5223, 8801-8810, 16384-16402, 19302-19309, 50000-65535 } update @vctl_p2p_rate4 { ip saddr limit rate over 4/second burst 24 packets } update @vctl_p2p4 { ip saddr timeout 5m } ct mark set ct mark or 0x10000000 counter name "vctl_p2p_direct" return`,
		`iif != "lo" ct state new ct original packets 1 ip6 saddr @vctl_p2p6 meta l4proto tcp tcp dport >= 1024 tcp dport != { 4244, 5222-5223, 5228, 5242, 8080, 8443 } update @vctl_p2p6 { ip6 saddr timeout 5m } ct mark set ct mark or 0x10000000 counter name "vctl_p2p_direct" return`,
	} {
		i := indexOf(rules, want)
		if i < 0 {
			t.Errorf("prerouting lacks:\n  %s\nrules:\n%s", want, strings.Join(rules, "\n"))
			continue
		}
		// After the kernel's own direct decisions, before xray takes it.
		anchor := indexOf(rules, "ct mark and 0x10000000 == 0x10000000 return")
		if anchor < 0 || tproxy < 0 || i > tproxy || i < anchor {
			t.Errorf("out of place (%d, tproxy %d): %s", i, tproxy, want)
		}
	}
	for _, decl := range []string{
		"set vctl_p2p4 { type ipv4_addr; flags dynamic, timeout; timeout 5m; size 1024; }",
		"set vctl_p2p_rate4 { type ipv4_addr; flags dynamic, timeout; timeout 1m; size 1024; }",
		"set vctl_p2p6 { type ipv6_addr; flags dynamic, timeout; timeout 5m; size 1024; }",
		"counter vctl_p2p_direct { }",
	} {
		if !strings.Contains(out, decl) {
			t.Errorf("missing %q", decl)
		}
	}
}

func TestADevicesStormWaitsAtTheDoorBeforeXray(t *testing.T) {
	out := mustRender(t, DefaultSpec(12345, 1))
	rules := rulesOf(chainNamed(t, out, "prerouting"))
	tproxy := indexOf(rules, "meta l4proto { tcp, udp } counter name \"vctl_tproxy_hits\" tproxy")
	for _, want := range []string{
		`iif != "lo" ct state new ct original packets 1 meta l4proto { tcp, udp } update @vctl_admit4 { ip saddr limit rate over 15/second burst 60 packets } counter name "vctl_admit_held" drop`,
		`iif != "lo" ct state new ct original packets 1 meta l4proto { tcp, udp } update @vctl_admit6 { ip6 saddr limit rate over 15/second burst 60 packets } counter name "vctl_admit_held" drop`,
	} {
		i := indexOf(rules, want)
		if p2p := indexOf(rules, `iif != "lo" ct state new ct original packets 1 ip saddr @vctl_p2p4`); i < 0 || tproxy < 0 || p2p < 0 || i > tproxy || i < p2p {
			t.Errorf("admission rule at %d (tproxy %d):\n  %s\nrules:\n%s", i, tproxy, want, strings.Join(rules, "\n"))
		}
	}
	if !strings.Contains(out, "counter vctl_admit_held { }") || !strings.Contains(out, "set vctl_admit4 { type ipv4_addr; flags dynamic, timeout; timeout 1m; size 1024; }") {
		t.Error("admission counter or set not declared")
	}
}

func TestXraysDialsToOneNodeArePaced(t *testing.T) {
	out := mustRender(t, DefaultSpec(12345, 1))
	chain := chainNamed(t, out, "pace")
	if chain == "" {
		t.Fatalf("no pace chain:\n%s", out)
	}
	for _, want := range []string{
		"type filter hook output priority filter; policy accept;",
		`meta mark 0x5644 meta l4proto tcp tcp flags & (syn | ack) == syn tcp dport != 53 update @vctl_pace4 { ip daddr . tcp dport limit rate over 40/second burst 160 packets } counter name "vctl_paced" drop`,
		`meta mark 0x5644 meta l4proto tcp tcp flags & (syn | ack) == syn tcp dport != 53 update @vctl_pace6 { ip6 daddr . tcp dport limit rate over 40/second burst 160 packets } counter name "vctl_paced" drop`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("pace chain lacks:\n  %s\nchain:\n%s", want, chain)
		}
	}
	if !strings.Contains(out, "set vctl_pace4 { type ipv4_addr . inet_service; flags dynamic, timeout; timeout 30s; size 1024; }") {
		t.Error("pace set not declared")
	}
}

func TestNoLoadGuardsWhenSwitchedOff(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.AdmitRate, s.PaceRate, s.P2PBypass = 0, 0, false
	out := mustRender(t, s)
	for _, name := range []string{"vctl_admit", "vctl_pace", "vctl_p2p", "chain pace"} {
		if strings.Contains(out, name) {
			t.Errorf("%q rendered while switched off", name)
		}
	}
}

// The kill switch promises nothing leaves unproxied: a P2P host's peers are
// not let out by the kernel under it.
func TestNoP2PBypassUnderTheKillSwitch(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.KillSwitch = true
	if out := mustRender(t, s); strings.Contains(out, "vctl_p2p") {
		t.Fatal("P2P peers go by the kernel with the kill switch armed")
	}
}

// A device can never fill a node's pace bucket alone: the door lets a
// device less than a node takes (the watchdog's confirm and the rescue's
// probes dial the same node).
func TestADeviceCannotFillANodesPace(t *testing.T) {
	s := DefaultSpec(12345, 1)
	if s.AdmitRate >= s.PaceRate || s.AdmitBurst >= s.PaceBurst {
		t.Fatalf("admit %d/%d, pace %d/%d", s.AdmitRate, s.AdmitBurst, s.PaceRate, s.PaceBurst)
	}
}
