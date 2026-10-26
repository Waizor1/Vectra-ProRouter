package firewall

import (
	"strings"
	"testing"
)

// r31: QUIC to the names whose route carries UDP/443 (the Russian bridge's)
// is let through to xray; the rest is refused as in r25.
func TestQUICPassLetsTheSetThroughAndRefusesTheRest(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.CarriedV4 = []string{"198.18.0.0/15"}
	s.QUICPass = true
	out := mustRender(t, s)
	rules := rulesOf(chainNamed(t, out, "prerouting"))

	pass := indexOf(rules, `iif != "lo" ip daddr @vctl_quic_pass4 meta l4proto udp udp dport 443 counter name "vctl_quic_passed"`)
	// ct state new: a flow xray answered is not cut when its address leaves
	// the set (an hour on, a flush) — it goes on to tproxy.
	refuse := indexOf(rules, `iif != "lo" ip daddr { 198.18.0.0/15 } ip daddr != @vctl_quic_pass4 meta l4proto udp udp dport 443 ct state new counter name "vctl_quic_refused" reject with icmpx port-unreachable`)
	tproxy := indexOf(rules, "meta l4proto { tcp, udp } counter name \"vctl_tproxy_hits\" tproxy")
	if pass < 0 || refuse < 0 || tproxy < 0 || pass > refuse || refuse > tproxy {
		t.Fatalf("pass %d, refusal %d, tproxy %d: want pass, then refusal, then tproxy:\n%s", pass, refuse, tproxy, strings.Join(rules, "\n"))
	}
	if strings.Contains(rules[pass], "accept") || strings.Contains(rules[pass], "return") {
		t.Fatalf("the pass rule must only count, the packet goes on to tproxy: %s", rules[pass])
	}
	if indexOf(rules, `iif != "lo" ip daddr { 198.18.0.0/15 } meta l4proto udp udp dport 443`) >= 0 {
		t.Fatalf("a refusal without the set's exception is still there:\n%s", strings.Join(rules, "\n"))
	}
	for _, decl := range []string{
		"counter vctl_quic_passed { }",
		"set vctl_quic_pass4 { type ipv4_addr; flags timeout; timeout 1h; size 8192; }",
	} {
		if !strings.Contains(out, decl) {
			t.Errorf("missing %q", decl)
		}
	}
	// The ICMP tune follows the refusal, which is still there.
	if !RelaxesICMP(s) {
		t.Error("RelaxesICMP = false while QUIC is still refused for the rest")
	}
}

func TestWithoutQUICPassTheRefusalIsR25s(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.CarriedV4 = []string{"198.18.0.0/15"}
	out := mustRender(t, s)
	for _, gone := range []string{"vctl_quic_pass4", "vctl_quic_passed"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q without QUICPass", gone)
		}
	}
	// And QUICPass alone, with the refusal off, renders nothing of its own.
	s.RefuseQUIC, s.QUICPass = false, true
	if out := mustRender(t, s); strings.Contains(out, "vctl_quic") {
		t.Error("a QUIC rule with the refusal off")
	}
}
