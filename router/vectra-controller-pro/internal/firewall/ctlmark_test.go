package firewall

import (
	"strings"
	"testing"

	"vectra-controller-pro/internal/config"
)

// outputChain returns the body of the `chain output { ... }` block.
func outputChain(t *testing.T, script string) string {
	t.Helper()
	i := strings.Index(script, "chain output {")
	if i < 0 {
		t.Fatal("no output chain in the rendered ruleset")
	}
	rest := script[i:]
	j := strings.Index(rest, "\n  }")
	if j < 0 {
		t.Fatal("unterminated output chain")
	}
	return rest[:j]
}

// The control-plane mark must be the FIRST rule of the output chain: vctl's own
// panel traffic has to escape TPROXY before any other rule can capture it.
// Otherwise a dead provider chain costs the router its remote management and
// the commit-confirm deadman flaps the ruleset every 90s.
func TestOutputChainReturnsOnControlMarkFirst(t *testing.T) {
	script, err := Render(DefaultSpec(12345, 1))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	chain := outputChain(t, script)

	var rules []string
	for _, line := range strings.Split(chain, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") ||
			strings.HasPrefix(line, "chain ") || strings.HasPrefix(line, "type ") {
			continue
		}
		rules = append(rules, line)
	}
	if len(rules) == 0 {
		t.Fatal("no rules parsed out of the output chain")
	}
	want := "meta mark 0x5643 return"
	if rules[0] != want {
		t.Fatalf("first output rule = %q, want %q\nchain:\n%s", rules[0], want, chain)
	}
	// Xray's own socket mark comes next, carrying the egress-exempt counter:
	// that counter moving is the only direct proof that xray's sockets are
	// marked and were let through.
	wantSecond := `meta mark 0x5644 counter name "` + CounterEgressExempt + `" return`
	if rules[1] != wantSecond {
		t.Errorf("second output rule = %q, want %q", rules[1], wantSecond)
	}
	// The tproxy mark return must still be present.
	if rules[2] != "meta mark 0x1 return" {
		t.Errorf("third output rule = %q, want the tproxy-mark return", rules[2])
	}
}

// The single most expensive lesson the data-plane stand taught: if xray's own
// sockets carry the tproxy fwmark, the `ip rule fwmark <FwMark> lookup 100`
// installed by `vctl firewall routing` resolves their egress to
// `local 0.0.0.0/0 dev lo`. Xray's packets then re-enter through PREROUTING,
// match the TPROXY rule again and come back to xray — an unbounded loop
// (measured: 85,150 packets and 381 MiB RSS for a handful of requests).
func TestSockMarkIsDistinctFromFwMark(t *testing.T) {
	s := DefaultSpec(12345, 1)
	if s.SockMark == s.FwMark {
		t.Fatalf("xray socket mark 0x%x must differ from the tproxy fwmark 0x%x "+
			"(the fwmark policy route would loop xray's own egress)", s.SockMark, s.FwMark)
	}
	if s.SockMark == s.CtlMark {
		t.Errorf("xray socket mark must be separable from the control-plane mark in counters")
	}
	if s.SockMark != config.DefaultXraySockMark {
		t.Fatalf("SockMark = 0x%x, want config.DefaultXraySockMark 0x%x "+
			"(the nft rule and the splice must agree or nothing is exempt)",
			s.SockMark, config.DefaultXraySockMark)
	}
}

// The sock-mark return has to come BEFORE the marking rule at the bottom of the
// output chain, or xray's egress gets marked with FwMark and steered into the
// local TPROXY socket anyway.
func TestSockMarkReturnPrecedesTheMarkingRule(t *testing.T) {
	script, err := Render(DefaultSpec(12345, 1))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	chain := outputChain(t, script)
	ret := strings.Index(chain, "meta mark 0x5644")
	set := strings.Index(chain, "meta mark set 0x1")
	if ret < 0 || set < 0 {
		t.Fatalf("output chain missing sock-mark return (%d) or marking rule (%d)\n%s", ret, set, chain)
	}
	if ret > set {
		t.Errorf("sock-mark return must precede the marking rule\n%s", chain)
	}
}

func TestControlMarkIsDistinctFromTproxyMark(t *testing.T) {
	s := DefaultSpec(12345, 1)
	if s.CtlMark == s.FwMark {
		t.Fatalf("control mark 0x%x must differ from the tproxy mark 0x%x", s.CtlMark, s.FwMark)
	}
	if s.CtlMark != DefaultControlMark {
		t.Fatalf("DefaultSpec.CtlMark = 0x%x, want 0x%x", s.CtlMark, DefaultControlMark)
	}
}

// A zero CtlMark omits the rule entirely rather than emitting `meta mark 0x0`,
// which would match unmarked packets and return everything.
func TestZeroControlMarkOmitsTheRule(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.CtlMark = 0
	script, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(script, "meta mark 0x0 return") {
		t.Fatal("a zero control mark must not emit a rule (it would return every unmarked packet)")
	}
}

// The kill-switch must never apply to the OUTPUT chain: the router's own
// traffic (control plane, DNS) always stays accept.
func TestControlMarkSurvivesKillSwitch(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.KillSwitch = true
	script, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	chain := outputChain(t, script)
	if !strings.Contains(chain, "policy accept") {
		t.Error("output chain policy must stay accept even with the kill-switch on")
	}
	if !strings.Contains(chain, "meta mark 0x5643 return") {
		t.Error("control-mark return must survive the kill-switch")
	}
}

// The router's own DNS and NTP must leave before the marking rule: dnsmasq's
// upstream, vctl's lookup of its panel and the clock may never depend on the
// proxy. (On the test router the provider's catch-all rule sent UDP 53 to a
// balancer still warming up; the check-in failed and the deadman reverted.)
func TestOutputChainLetsTheRoutersOwnDNSAndNTPOutDirectly(t *testing.T) {
	for _, ks := range []bool{false, true} {
		spec := DefaultSpec(12345, 1)
		spec.KillSwitch = ks
		script, err := Render(spec)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		chain := outputChain(t, script)
		mark := strings.Index(chain, "meta mark set 0x1 ct mark set 0x1")
		for _, rule := range []string{"udp dport { 53, 123 } return", "tcp dport 53 return"} {
			i := strings.Index(chain, rule)
			if i < 0 || mark < 0 || i > mark {
				t.Fatalf("killSwitch=%v: %q is missing or after the marking rule:\n%s", ks, rule, chain)
			}
		}
	}
	// The PREROUTING chain keeps capturing a LAN client's DNS: only the
	// router's own leaves directly.
	script, _ := Render(DefaultSpec(12345, 1))
	pre := script[strings.Index(script, "chain prerouting {"):]
	pre = pre[:strings.Index(pre, "\n  }")]
	if strings.Contains(pre, "dport { 53") || strings.Contains(pre, "dport 53") {
		t.Fatalf("PREROUTING exempts DNS; a LAN client's lookups must still be captured:\n%s", pre)
	}
}
