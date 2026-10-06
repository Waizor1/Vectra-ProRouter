package firewall

import (
	"net/netip"
	"strings"
	"testing"
)

// The port forwards' «past the VPN» devices: a rule in prerouting after every
// exemption and the direct sets, ahead of the P2P and admission guards and
// TPROXY, marking the connection with the direct bit; its set and counter
// declared. (With the kill switch armed it is not rendered at all:
// TestPortForwardDirectIsNeverPastTheKillSwitch.)
func TestPortForwardDirectRuleSitsBeforeTheGuardsAndTproxy(t *testing.T) {
	for _, ks := range []bool{false} {
		s := DefaultSpec(12345, 1)
		s.KillSwitch = ks
		out := mustRender(t, s)
		pre := chainNamed(t, out, "prerouting")
		pf := ruleIndex(pre, "ct state new ip saddr @vctl_pf_direct4", "ct mark set ct mark or 0x10000000", `counter name "vctl_pf_direct"`, "return")
		bypass := ruleIndex(pre, "ip  daddr @bypass4 return")
		direct6 := ruleIndex(pre, "ct state new ip6 daddr @vctl_direct6")
		admit := ruleIndex(pre, "@vctl_admit4")
		tproxy := ruleIndex(pre, "tproxy to :12345")
		if pf < 0 || bypass < 0 || direct6 < 0 || admit < 0 || tproxy < 0 {
			t.Fatalf("killswitch=%v: prerouting lacks a rule (pf %d bypass %d direct6 %d admit %d tproxy %d):\n%s", ks, pf, bypass, direct6, admit, tproxy, pre)
		}
		if !(bypass < direct6 && direct6 < pf && pf < admit && admit < tproxy) {
			t.Errorf("killswitch=%v: order bypass %d < direct %d < pf %d < admit %d < tproxy %d broken:\n%s", ks, bypass, direct6, pf, admit, tproxy, pre)
		}
		if p2p := ruleIndex(pre, "@vctl_p2p4"); p2p >= 0 && p2p < pf {
			t.Errorf("killswitch=%v: the P2P guard precedes the port forwards' rule:\n%s", ks, pre)
		}
		for _, decl := range []string{"counter vctl_pf_direct { }", "set vctl_pf_direct4 { type ipv4_addr; }"} {
			if !strings.Contains(out, decl) {
				t.Errorf("killswitch=%v: %q is not declared", ks, decl)
			}
		}
		// No FakeDNS pool carried: no address clause at all.
		if strings.Contains(pre, "@vctl_pf_direct4 ip daddr") {
			t.Errorf("killswitch=%v: an address clause without pools:\n%s", ks, pre)
		}
	}
}

// The FakeDNS pools are never sent out by the kernel: they stand for domains
// only xray knows. Only well-formed IPv4 prefixes reach the script.
func TestPortForwardDirectLeavesFakeDNSToXray(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.CarriedV4 = []string{"198.18.0.0/16", " 198.19.1.7/16 ", "198.18.0.0/16", "fc00::/7", "1.2.3.4/33; flush ruleset", "garbage"}
	pre := chainNamed(t, mustRender(t, s), "prerouting")
	for _, want := range []string{
		"ct state new ip saddr @vctl_pf_direct4 ip daddr != { 198.18.0.0/16, 198.19.0.0/16 } meta l4proto { tcp, udp } th dport != { 53, 853 } ct mark set ct mark or 0x10000000 counter name \"vctl_pf_direct\" return",
		"ct state new ip saddr @vctl_pf_direct4 ip daddr != { 198.18.0.0/16, 198.19.0.0/16 } meta l4proto != { tcp, udp } ct mark set ct mark or 0x10000000 counter name \"vctl_pf_direct\" return",
	} {
		if !strings.Contains(pre, want) {
			t.Fatalf("want %q in:\n%s", want, pre)
		}
	}
	if strings.Contains(pre, "flush ruleset") || strings.Contains(pre, "garbage") || strings.Contains(pre, "fc00") {
		t.Fatalf("a value that is not an IPv4 prefix reached the script:\n%s", pre)
	}
}

// Without the direct bit nothing of it is rendered: the forward guard would
// have no bit to let through.
func TestPortForwardDirectNeedsTheBit(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.DirectCtMark = 0
	if out := mustRender(t, s); strings.Contains(out, "vctl_pf_direct") {
		t.Fatalf("DirectCtMark 0 still renders the port forwards' rule:\n%s", chainNamed(t, out, "prerouting"))
	}
}

// The kill switch wins over «past the VPN»: armed, the ruleset has neither
// the rule nor its set, so such a device's traffic meets TPROXY and the
// switch like every other's — no silent leak past it. The daemon reads the
// same predicate and reports directActive false (cmd/vctl), so the UI says
// the device goes through the VPN for now.
func TestPortForwardDirectIsNeverPastTheKillSwitch(t *testing.T) {
	out := mustRender(t, killSwitchSpec())
	if strings.Contains(out, "vctl_pf_direct") {
		t.Fatalf("the armed ruleset carries the «past the VPN» rule or set:\n%s", chainNamed(t, out, "prerouting"))
	}
	if PortForwardDirectInTable(killSwitchSpec()) {
		t.Fatal("PortForwardDirectInTable says the rule is in an armed ruleset")
	}
	off := DefaultSpec(12345, 1)
	if !PortForwardDirectInTable(off) || !strings.Contains(mustRender(t, off), "set vctl_pf_direct4 { type ipv4_addr; }") {
		t.Fatal("switch off: the rule must be in the table, and the predicate say so")
	}
	off.DirectCtMark = 0
	if PortForwardDirectInTable(off) {
		t.Fatal("no direct bit: PortForwardDirectInTable must be false")
	}
}

// Port forwards work through TPROXY because a forwarded server's answers —
// the reply direction — return at the top of prerouting, before the port
// forwards' own rule and before TPROXY. Captured, a server's SYN-ACK reached
// xray's listener and was reset: every port forward on the router was dead.
func TestPortForwardRepliesNeverReachTproxy(t *testing.T) {
	for _, ks := range []bool{false, true} {
		s := DefaultSpec(12345, 1)
		s.KillSwitch = ks
		s.CarriedV4 = []string{"198.18.0.0/15"}
		pre := chainNamed(t, mustRender(t, s), "prerouting")
		reply := ruleIndex(pre, "ct direction reply return")
		pf := ruleIndex(pre, "@vctl_pf_direct4")
		tproxy := ruleIndex(pre, "tproxy to :12345")
		if ks && pf < 0 {
			pf = reply + 1 // not rendered when armed; only the reply's place matters
		}
		if reply < 0 || !(reply < pf && pf < tproxy) {
			t.Fatalf("killswitch=%v: replies must return first (reply %d, pf %d, tproxy %d):\n%s", ks, reply, pf, tproxy, pre)
		}
	}
}

// The set's script replaces its elements in one transaction and touches
// nothing else; only IPv4 addresses, each once.
func TestPortForwardDirectScript(t *testing.T) {
	addrs := []netip.Addr{
		netip.MustParseAddr("192.168.1.50"), netip.MustParseAddr("::ffff:192.168.1.60"),
		netip.MustParseAddr("192.168.1.50"), netip.MustParseAddr("fd00::1"),
	}
	got := PortForwardDirectScript("vctl", addrs)
	want := "flush set inet vctl vctl_pf_direct4\nadd element inet vctl vctl_pf_direct4 { 192.168.1.50, 192.168.1.60 }\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := PortForwardDirectScript("", nil); got != "flush set inet vctl vctl_pf_direct4\n" {
		t.Fatalf("empty: %q", got)
	}
	for _, bad := range []string{"delete table", "flush ruleset", "add rule"} {
		if strings.Contains(got, bad) {
			t.Fatalf("the script does more than the set: %q", got)
		}
	}
}

// A device «past the VPN» never sends its DNS out by the kernel: the ISP
// forges answers even from public resolvers. Its queries to 53 and 853 are
// left to the hijack (when on) and TPROXY — with the hijack on and off.
func TestPortForwardDirectKeepsDNSInTheTunnel(t *testing.T) {
	for _, hijack := range []bool{false, true} {
		s := DefaultSpec(12345, 1)
		s.HijackDNS = hijack
		pre := chainNamed(t, mustRender(t, s), "prerouting")
		var pf []string
		for _, line := range strings.Split(pre, "\n") {
			if strings.Contains(line, "@vctl_pf_direct4") {
				pf = append(pf, strings.TrimSpace(line))
			}
		}
		if len(pf) != 2 {
			t.Fatalf("hijack=%v: %d port-forward rules, want 2:\n%s", hijack, len(pf), pre)
		}
		if !strings.Contains(pf[0], "meta l4proto { tcp, udp } th dport != { 53, 853 }") || !strings.Contains(pf[1], "meta l4proto != { tcp, udp }") {
			t.Fatalf("hijack=%v: DNS is not kept out of «past the VPN»:\n%s", hijack, strings.Join(pf, "\n"))
		}
		tproxy := ruleIndex(pre, "tproxy to :12345")
		first := ruleIndex(pre, "@vctl_pf_direct4")
		if first < 0 || first > tproxy {
			t.Fatalf("hijack=%v: the rule is not before TPROXY", hijack)
		}
		if hijack {
			if h := ruleIndex(pre, "th dport 53 ip saddr @bypass4 ct state new return"); h < 0 || h > first {
				t.Fatalf("hijack on: the hijack's return does not precede the port forwards' rule:\n%s", pre)
			}
		}
	}
}
