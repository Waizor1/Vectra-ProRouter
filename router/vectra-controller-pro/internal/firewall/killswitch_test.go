package firewall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chainNamed returns the body of a `chain <name> { ... }` block, or "" when it
// was not emitted at all.
func chainNamed(t *testing.T, script, name string) string {
	t.Helper()
	i := strings.Index(script, "chain "+name+" {")
	if i < 0 {
		return ""
	}
	rest := script[i:]
	j := strings.Index(rest, "\n  }")
	if j < 0 {
		t.Fatalf("unterminated %s chain", name)
	}
	return rest[:j]
}

// forwardChain returns the body of the `chain forward { ... }` block, or "" when
// the guard was not emitted at all.
func forwardChain(t *testing.T, script string) string {
	t.Helper()
	return chainNamed(t, script, "forward")
}

// chainPolicy reads the policy off a chain's `type ... hook ...` line. Tests
// used to look for the substring "policy drop" anywhere in the render, which
// also matches prose in a comment; this reads the declaration itself.
func chainPolicy(chain string) string {
	for _, line := range strings.Split(chain, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "type ") {
			continue
		}
		if i := strings.Index(line, "policy "); i >= 0 {
			return strings.TrimSuffix(strings.TrimSpace(line[i+len("policy "):]), ";")
		}
	}
	return ""
}

// rulesOf strips comments, the chain header and the `type ... hook ...` line,
// leaving the ordered rule list.
func rulesOf(chain string) []string {
	var rules []string
	for _, line := range strings.Split(chain, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") ||
			strings.HasPrefix(line, "chain ") || strings.HasPrefix(line, "type ") {
			continue
		}
		rules = append(rules, line)
	}
	return rules
}

func mustRender(t *testing.T, s Spec) string {
	t.Helper()
	out, err := Render(s)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

func killSwitchSpec() Spec {
	s := DefaultSpec(12345, 1)
	s.KillSwitch = true
	return s
}

func TestKillSwitchPrereroutingPolicy(t *testing.T) {
	spec := DefaultSpec(12345, 1)

	// Default (off): fail-open — prerouting accepts.
	off := mustRender(t, spec)
	if !strings.Contains(off, "hook prerouting priority mangle; policy accept;") {
		t.Errorf("default prerouting should be 'policy accept':\n%s", off)
	}

	// On: prerouting STILL accepts, and the fail-closed verdict is the explicit
	// counted drop at the bottom of the chain. This test used to require `policy
	// drop` here and passed while the armed ruleset black-holed the router — see
	// TestKillSwitchNeverReliesOnADropPolicy for why the two are not equivalent.
	spec.KillSwitch = true
	on := mustRender(t, spec)
	if !strings.Contains(on, "hook prerouting priority mangle; policy accept;") {
		t.Errorf("kill-switch prerouting must stay 'policy accept' (the counted drop is the verdict):\n%s", on)
	}
	if !strings.Contains(on, "hook output priority mangle; policy accept;") {
		t.Errorf("kill-switch must NOT change the output chain (router self-traffic):\n%s", on)
	}
	// Local/LAN/bypass returns must still precede the drop so the router and LAN
	// stay reachable for clients.
	if !strings.Contains(on, "fib daddr type { local, broadcast, multicast } return") {
		t.Error("kill-switch must keep local/broadcast returns before the drop")
	}
}

// THE REGRESSION THIS FILE MISSED. In an nftables base chain, `return` with an
// empty jump stack does not accept — nft_do_chain falls through to the chain
// POLICY. So a chain that mixes `return` exemptions with `policy drop` drops
// every one of those exemptions, and does it invisibly, because a policy
// decision increments no counter.
//
// That is what the armed ruleset did: `fib daddr type local return` dropped the
// router's own inbound traffic, `@bypass4/@bypass6 return` dropped the LAN, and
// `@vctl_direct4/6 return` dropped every split-DNS direct destination — while
// vctl_killswitch_drops read 0. The data-plane stand measured it end to end
// (test/dataplane, MODE=killswitch): xray captured and dialled out, and not one
// reply survived prerouting.
//
// Every assertion above was satisfied throughout. This one is stated as an
// invariant over ALL chains and both modes so no future chain can reintroduce it.
func TestKillSwitchNeverReliesOnADropPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec Spec
	}{
		{"armed", killSwitchSpec()},
		{"shadow", DefaultSpec(12345, 1)},
	} {
		out := mustRender(t, tc.spec)
		for _, name := range []string{"prerouting", "forward", "output"} {
			chain := chainNamed(t, out, name)
			if chain == "" {
				t.Fatalf("%s: chain %s not emitted:\n%s", tc.name, name, out)
			}
			policyDrop := chainPolicy(chain) == "drop"
			var returns int
			for _, r := range rulesOf(chain) {
				if strings.HasSuffix(r, " return") {
					returns++
				}
			}
			if policyDrop && returns > 0 {
				t.Errorf("%s/%s: %d `return` exemptions under `policy drop` — in a base chain "+
					"`return` falls through to the policy, so each of those is an UNCOUNTED drop:\n%s",
					tc.name, name, returns, chain)
			}
		}
	}
}

// The corollary, spelled out where an operator would look for it: with the
// switch armed, every packet the kill-switch refuses must pass through a rule
// that counts it — tcp/udp in vctl_killswitch_drops, the rest (a LAN ping) in
// vctl_unproxied_other. If a drop can happen without touching either, the
// counters are not the diagnostic the package doc says they are.
func TestKillSwitchEveryDropIsCounted(t *testing.T) {
	out := mustRender(t, killSwitchSpec())
	for _, name := range []string{"prerouting", "forward", "output"} {
		chain := chainNamed(t, out, name)
		if chainPolicy(chain) == "drop" {
			t.Errorf("chain %s drops via its policy, which increments no counter:\n%s", name, chain)
		}
		for _, r := range rulesOf(chain) {
			// The door (Spec.AdmitRate) drops too, into a counter of its own.
			if strings.HasSuffix(r, " drop") && !strings.Contains(r, `counter name "`+CounterKillSwitchDrops+`"`) &&
				!strings.Contains(r, `counter name "`+CounterUnproxiedOther+`"`) &&
				!strings.Contains(r, `counter name "`+CounterAdmitHeld+`"`) {
				t.Errorf("chain %s has an uncounted drop: %q", name, r)
			}
		}
	}
}

// With the switch OFF the guard is still emitted, in SHADOW form.
//
// The OFF default was justified by "the guard counts what it would have
// dropped, so exposure is measurable per router before committing fleet-wide".
// That was false as written: with the switch off there was no counter, no
// forward chain and a prerouting policy of accept, so measuring the exposure
// required first taking the risk being measured, and the rollout gate ("run a
// week, read the counter, decide") was not executable.
func TestKillSwitchShadowCounterPresentWhenDisabled(t *testing.T) {
	out := mustRender(t, DefaultSpec(12345, 1))

	if !strings.Contains(out, "counter "+CounterKillSwitchShadow+" { }") {
		t.Errorf("shadow counter object %q not declared with the switch off:\n%s", CounterKillSwitchShadow, out)
	}
	if n := strings.Count(out, `counter name "`+CounterKillSwitchShadow+`"`); n != 2 {
		t.Errorf("shadow counter referenced %d times, want 2 (prerouting fall-through + forward guard)\n%s", n, out)
	}
	if n := strings.Count(out, `counter name "`+CounterUnproxiedOther+`"`); n != 2 {
		t.Errorf("%s referenced %d times, want 2 (the same two points)\n%s", CounterUnproxiedOther, n, out)
	}
	if c := forwardChain(t, out); c == "" {
		t.Fatalf("no forward chain with the switch off — nothing would be counted:\n%s", out)
	}
	// The drop-counter name stays reserved for the armed switch: a number in
	// vctl_killswitch_drops must be proof the switch is ON.
	//
	// Matched as a DECLARATION and as a REFERENCE, not as a substring of the
	// whole render. A bare Contains also matches the counter's name written in
	// prose — this file already learned that once with `policy drop`, which is
	// why chainPolicy exists — and it broke the moment a rule comment cited a
	// measurement that named the counter.
	if strings.Contains(out, "counter "+CounterKillSwitchDrops+" { }") {
		t.Errorf("counter object %q must not be declared with the kill-switch off:\n%s", CounterKillSwitchDrops, out)
	}
	if n := strings.Count(out, `counter name "`+CounterKillSwitchDrops+`"`); n != 0 {
		t.Errorf("counter %q referenced %d times with the kill-switch off, want 0:\n%s", CounterKillSwitchDrops, n, out)
	}
}

// A shadow count must not change one packet's fate. No verdict anywhere: the
// prerouting policy stays accept, the forward chain's policy stays accept, and
// its last rule is a bare counter that falls through.
func TestKillSwitchShadowIssuesNoVerdict(t *testing.T) {
	out := mustRender(t, DefaultSpec(12345, 1))

	// No rule in any chain may carry a drop verdict, and no chain policy may be
	// drop: with the switch off the ruleset must be forwarding-neutral.
	for _, name := range []string{"prerouting", "forward", "output"} {
		chain := chainNamed(t, out, name)
		for _, r := range rulesOf(chain) {
			// The door (Spec.AdmitRate) is not the kill switch: a storm
			// waits there whatever the switch.
			if strings.HasSuffix(r, " drop") && !strings.Contains(r, `counter name "`+CounterAdmitHeld+`"`) {
				t.Errorf("chain %s has a drop verdict with the switch off: %q", name, r)
			}
		}
		if p := chainPolicy(chain); p != "accept" {
			t.Errorf("chain %s policy = %q with the switch off, want accept:\n%s", name, p, chain)
		}
	}
	if !strings.Contains(out, "hook prerouting priority mangle; policy accept;") {
		t.Errorf("prerouting policy must stay accept with the switch off:\n%s", out)
	}

	chain := forwardChain(t, out)
	if !strings.Contains(chain, "type filter hook forward priority mangle; policy accept;") {
		t.Errorf("shadow forward chain must stay accept:\n%s", chain)
	}
	rules := rulesOf(chain)
	tail := []string{
		`meta mark 0x1 counter name "` + CounterTproxyEscaped + `"`,
		`meta l4proto { tcp, udp } counter name "` + CounterKillSwitchShadow + `"`,
		`counter name "` + CounterUnproxiedOther + `"`,
	}
	if len(rules) < len(tail) || strings.Join(rules[len(rules)-len(tail):], "\n") != strings.Join(tail, "\n") {
		t.Fatalf("shadow forward chain ends\n  %s\nwant the bare counters\n  %s", strings.Join(rules, "\n  "), strings.Join(tail, "\n  "))
	}
	for i, r := range rules[:len(rules)-len(tail)] {
		if !strings.HasSuffix(r, "return") {
			t.Errorf("shadow rule %d = %q: everything before the counters must be an exemption", i, r)
		}
	}
}

// The shadow chains must count the SAME packets the armed ones would drop, or
// the measurement does not predict what enabling the switch would do. Only the
// verdicts, and the name of the tcp/udp counter, may differ.
func TestKillSwitchShadowMatchesTheArmedGuardRuleForRule(t *testing.T) {
	armedAsShadow := strings.NewReplacer(`"`+CounterKillSwitchDrops+`"`, `"`+CounterKillSwitchShadow+`"`)
	for _, name := range []string{"prerouting", "forward"} {
		on := rulesOf(chainNamed(t, mustRender(t, killSwitchSpec()), name))
		// A P2P host's peers go by the kernel only with the switch off
		// (Spec.P2PBypass): those rules have no armed twin.
		var off []string
		for _, r := range rulesOf(chainNamed(t, mustRender(t, DefaultSpec(12345, 1)), name)) {
			if !strings.Contains(r, `counter name "`+CounterP2PDirect+`"`) {
				off = append(off, r)
			}
		}
		if len(on) != len(off) {
			t.Fatalf("%s: rule count differs: armed %d, shadow %d\n armed: %v\nshadow: %v", name, len(on), len(off), on, off)
		}
		drops := 0
		for i := range on {
			a := on[i]
			// The door (Spec.AdmitRate) drops in both, the same rule.
			if strings.HasSuffix(a, " drop") && !strings.Contains(a, `counter name "`+CounterAdmitHeld+`"`) {
				drops++
				a = strings.TrimSuffix(a, " drop")
			}
			if a = armedAsShadow.Replace(a); a != off[i] {
				t.Errorf("%s rule %d differs beyond its verdict:\n armed: %s\nshadow: %s", name, i, on[i], off[i])
			}
		}
		// The two counted drops, and nothing else, carry the verdict.
		if drops != 2 || on[len(on)-1] != `counter name "`+CounterUnproxiedOther+`" drop` ||
			on[len(on)-2] != `meta l4proto { tcp, udp } counter name "`+CounterKillSwitchDrops+`" drop` {
			t.Errorf("%s: armed chain ends %q / %q with %d drops; want the tcp/udp drop, then the drop of the rest",
				name, on[len(on)-2], on[len(on)-1], drops)
		}
	}
}

// The kill-switch instrument counts only what TPROXY could have carried:
// tcp/udp. A LAN ping is never proxied, by design, so it lands in its own
// counter — and meets the same verdict, so what passes and what is dropped
// does not change with the split: the last rule of both chains still matches
// everything that got that far, and drops it exactly when the switch is on.
func TestKillCounterCountsOnlyTcpUdpAndTheRestApart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		spec    Spec
		kill    string
		verdict string
	}{
		{"armed", killSwitchSpec(), CounterKillSwitchDrops, " drop"},
		{"shadow", DefaultSpec(12345, 1), CounterKillSwitchShadow, ""},
	} {
		out := mustRender(t, tc.spec)
		for _, name := range []string{"prerouting", "forward"} {
			rules := rulesOf(chainNamed(t, out, name))
			if got, want := rules[len(rules)-2], `meta l4proto { tcp, udp } counter name "`+tc.kill+`"`+tc.verdict; got != want {
				t.Errorf("%s/%s: the instrument = %q, want %q", tc.name, name, got, want)
			}
			if got, want := rules[len(rules)-1], `counter name "`+CounterUnproxiedOther+`"`+tc.verdict; got != want {
				t.Errorf("%s/%s: last rule = %q, want %q (unconditional, the same verdict)", tc.name, name, got, want)
			}
		}
		if n := strings.Count(out, `counter name "`+tc.kill+`"`); n != 2 || strings.Count(out, `counter name "`+tc.kill+`"`+tc.verdict) != 2 {
			t.Errorf("%s: %s referenced %d times, want 2, both tcp/udp only", tc.name, tc.kill, n)
		}
		if !strings.Contains(out, "counter "+CounterUnproxiedOther+" { }") {
			t.Errorf("%s: %s is not declared", tc.name, CounterUnproxiedOther)
		}
	}
}

// A forwarded packet carrying the tproxy mark was captured by TPROXY and then
// routed out anyway: the fwmark policy route is gone (leak path 2). It is
// counted on its own, before the instrument, with no verdict — so it still
// reaches the instrument, which leaks it or drops it as before.
func TestForwardCountsWhatTproxyCapturedAndStillForwarded(t *testing.T) {
	escaped := `meta mark 0x1 counter name "` + CounterTproxyEscaped + `"`
	for _, spec := range []Spec{killSwitchSpec(), DefaultSpec(12345, 1)} {
		out := mustRender(t, spec)
		rules := rulesOf(forwardChain(t, out))
		if rules[len(rules)-3] != escaped {
			t.Errorf("killSwitch=%v: forward rule before the instrument = %q, want %q", spec.KillSwitch, rules[len(rules)-3], escaped)
		}
		if strings.Count(out, `counter name "`+CounterTproxyEscaped+`"`) != 1 || !strings.Contains(out, "counter "+CounterTproxyEscaped+" { }") {
			t.Errorf("killSwitch=%v: %s must be declared and referenced once, in forward", spec.KillSwitch, CounterTproxyEscaped)
		}
		if strings.Contains(chainNamed(t, out, "prerouting"), CounterTproxyEscaped) {
			t.Error("prerouting counts escapes; only forward can see them")
		}
	}
	// No mark, no rule: `meta mark 0x0` would count every unmarked packet.
	s := DefaultSpec(12345, 1)
	s.FwMark = 0
	if out := mustRender(t, s); strings.Contains(out, CounterTproxyEscaped) {
		t.Errorf("a zero fwmark still emitted the escape counter:\n%s", forwardChain(t, out))
	}
}

// The armed ruleset is what an operator who has already enabled the switch is
// running, so it does not change by accident: the golden file has to be
// regenerated deliberately and the diff read.
//
// It has been regenerated five times: for the prerouting policy fix (drop ->
// accept, with the counted drop as the only verdict; the behavioural diff is in
// TestKillSwitchNeverReliesOnADropPolicy), for the loopback guard — one
// counter and the local_guard chain added, nothing else touched (see
// TestLocalGuard* in nft_test.go) — and for the leak counters: the terminal
// drop split into tcp/udp and the rest, the same verdict on both, plus the
// verdict-free escape counter in forward (TestKillCounterCountsOnlyTcpUdp*,
// TestForwardCountsWhatTproxyCaptured*) — and for the router's own DNS and
// NTP leaving the output chain directly, two rules and nothing else
// (TestOutputChainLetsTheRoutersOwnDNSAndNTPOutDirectly) — and for the
// router's own flows carried whole: a reply-direction return, the ct-mark
// follow rule, and the marking rule tagging the connection
// (TestOutputChainCarriesTheRoutersOwnFlowsWhole) — and for the script
// replacing the table instead of adding to it: a declare and a delete before
// the block, nothing inside it touched (TestRenderReplacesTheTable) — and for
// direct connections kept in the kernel for their whole life: in prerouting a
// conntrack-bit follow rule and a new-connection set rule replace the
// per-packet test of the direct sets, in forward the same bit returns, one
// counter (TestDirectConnectionsStickToTheKernel) — and for the direct sets
// flushed before the table is replaced, so nft does not read a loaded set
// back (TestReprogramFlushesTheDirectSetsFirst) — and for a connection's
// reply direction left alone in prerouting and forward, so a port forward's
// server can answer (TestRenderLeavesRepliesAlone) — and for the load guards
// of 2026-09-30: a P2P host's peers sent out by the kernel, a device's storm
// held at the door, xray's dials to one node paced (load_test.go) — and for
// the DNS door of r35: one counter, two sets and the dns_guard chain
// (TestADevicesDNSStormWaitsAtTheRoutersDoor). With the switch, what is
// dropped is unchanged.
func TestKillSwitchArmedRulesetIsUnchanged(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "killswitch-on.nft"))
	if err != nil {
		t.Fatal(err)
	}
	if got := mustRender(t, killSwitchSpec()); got != string(want) {
		t.Errorf("armed ruleset changed.\n--- want\n%s\n--- got\n%s", want, got)
	}
}

func TestKillSwitchGuardPresentWhenEnabled(t *testing.T) {
	out := mustRender(t, killSwitchSpec())

	chain := forwardChain(t, out)
	if chain == "" {
		t.Fatalf("kill-switch on but no forward guard emitted:\n%s", out)
	}
	// The guard must run BEFORE fw4's forward chain (priority filter, 0), or
	// another table could accept the packet out from under the kill-switch and
	// the verdict would depend on hook registration order.
	if !strings.Contains(chain, "type filter hook forward priority mangle;") {
		t.Errorf("guard must sit at priority mangle, ahead of fw4's filter chain:\n%s", chain)
	}
	// Chain policy stays accept; the drop is an explicit, counted rule. A rule
	// typo must not black-hole the router via the policy (see the package doc).
	if !strings.Contains(chain, "policy accept;") {
		t.Errorf("guard chain policy must stay accept (explicit counted drop instead):\n%s", chain)
	}
}

// The counter is the contract with test/dataplane: it must be declared as an
// object AND referenced by a rule, exactly like the other three.
func TestKillSwitchCounterDeclaredAndReferenced(t *testing.T) {
	out := mustRender(t, killSwitchSpec())

	if !strings.Contains(out, "counter "+CounterKillSwitchDrops+" { }") {
		t.Errorf("counter object %q not declared\n%s", CounterKillSwitchDrops, out)
	}
	if n := strings.Count(out, `counter name "`+CounterKillSwitchDrops+`" drop`); n != 2 {
		t.Errorf("want the counted drop at both drop points (prerouting fall-through + "+
			"forward guard), found %d\n%s", n, out)
	}
}

// Requirement: the exemptions must PRECEDE the drop, and the drop must be last.
// If any exemption landed after the terminal drop it would be dead rule text.
func TestKillSwitchExemptionsPrecedeTheDrop(t *testing.T) {
	chain := forwardChain(t, mustRender(t, killSwitchSpec()))
	if chain == "" {
		t.Fatal("no forward guard")
	}
	rules := rulesOf(chain)
	if len(rules) < 3 {
		t.Fatalf("guard has too few rules to be meaningful: %v", rules)
	}

	// Control plane first, xray's own sockets second — same order as the output
	// chain, so the two chains read identically.
	if want := "meta mark 0x5643 return"; rules[0] != want {
		t.Errorf("first guard rule = %q, want %q (control plane)", rules[0], want)
	}
	if want := "meta mark 0x5644 return"; rules[1] != want {
		t.Errorf("second guard rule = %q, want %q (xray sockets)", rules[1], want)
	}

	tail := []string{
		`meta mark 0x1 counter name "` + CounterTproxyEscaped + `"`,
		`meta l4proto { tcp, udp } counter name "` + CounterKillSwitchDrops + `" drop`,
		`counter name "` + CounterUnproxiedOther + `" drop`,
	}
	if got := strings.Join(rules[len(rules)-len(tail):], "\n"); got != strings.Join(tail, "\n") {
		t.Fatalf("guard ends\n  %s\nwant the escape count, then the counted drops\n  %s", got, strings.Join(tail, "\n  "))
	}
	for i, r := range rules[:len(rules)-len(tail)] {
		if !strings.HasSuffix(r, "return") {
			t.Errorf("guard rule %d = %q: everything before the drops must be an exemption", i, r)
		}
	}
}

// Requirement 2, spelled out: the guard must never touch the control plane, xray,
// LAN-local traffic, DHCP, or the router's own services.
func TestKillSwitchGuardExemptsTheRouterAndTheLAN(t *testing.T) {
	chain := forwardChain(t, mustRender(t, killSwitchSpec()))
	if chain == "" {
		t.Fatal("no forward guard")
	}
	for _, must := range []string{
		"meta mark 0x5643 return",                               // controller -> panel
		"meta mark 0x5644 return",                               // xray's own sockets
		"fib daddr type { local, broadcast, multicast } return", // the router itself
		"udp dport { 67, 68, 546, 547 } return",                 // DHCP / DHCPv6
		"ip  daddr @bypass4 return",                             // LAN-local + reply path
		"ip6 daddr @bypass6 return",
		"ip  daddr @vctl_direct4 return", // split-DNS direct destinations
		"ip6 daddr @vctl_direct6 return",
	} {
		if !strings.Contains(chain, must) {
			t.Errorf("guard is missing the exemption %q:\n%s", must, chain)
		}
	}
}

// The single rule that must NOT be there. A forwarded packet carrying the tproxy
// fwmark is the leak: TPROXY captured it, the rule accepted it, and then the
// missing fwmark policy route let the kernel forward it out the WAN anyway.
// Exempting the mark here would re-open exactly the hole the chain closes.
func TestKillSwitchGuardDoesNotExemptTheTproxyFwMark(t *testing.T) {
	s := killSwitchSpec()
	chain := forwardChain(t, mustRender(t, s))
	if chain == "" {
		t.Fatal("no forward guard")
	}
	leak := "meta mark 0x1 return"
	if strings.Contains(chain, leak) {
		t.Fatalf("guard returns on the tproxy fwmark (%q) — that IS the leak path "+
			"(tproxy accepted, policy route missing, kernel forwards it):\n%s", leak, chain)
	}
	// Guard the guard: the output chain still returns on it, so this test is
	// asserting an asymmetry rather than the absence of a rule everywhere.
	if !strings.Contains(outputChain(t, mustRender(t, s)), leak) {
		t.Error("the output chain must still return on the tproxy fwmark")
	}
}

// A zero mark must omit the rule, never emit `meta mark 0x0 return` — that
// matches every unmarked packet and would return the entire forwarded stream,
// silently disabling the kill-switch.
func TestKillSwitchGuardZeroMarksOmitTheirRules(t *testing.T) {
	s := killSwitchSpec()
	s.CtlMark = 0
	s.SockMark = 0
	chain := forwardChain(t, mustRender(t, s))
	if chain == "" {
		t.Fatal("no forward guard")
	}
	if strings.Contains(chain, "meta mark 0x0 return") {
		t.Fatalf("zero mark emitted a catch-all return, disabling the guard:\n%s", chain)
	}
	if !strings.Contains(chain, `counter name "`+CounterKillSwitchDrops+`" drop`) {
		t.Errorf("guard lost its drop when the marks were zeroed:\n%s", chain)
	}
}

func TestKillSwitchGuardNoIPv6(t *testing.T) {
	s := killSwitchSpec()
	s.IPv6Enabled = false
	chain := forwardChain(t, mustRender(t, s))
	if chain == "" {
		t.Fatal("no forward guard")
	}
	if strings.Contains(chain, "ip6 ") {
		t.Errorf("ipv6 disabled but the guard emitted ip6 rules:\n%s", chain)
	}
}

// The prerouting fall-through (xray down) has to be counted too, or the operator
// sees a dead internet and every counter at zero.
func TestKillSwitchPrereroutingDropIsCounted(t *testing.T) {
	on := mustRender(t, killSwitchSpec())
	pre := on[strings.Index(on, "chain prerouting {"):]
	pre = pre[:strings.Index(pre, "\n  }")]

	rules := rulesOf(pre)
	drop := `counter name "` + CounterKillSwitchDrops + `" drop`
	if got := rules[len(rules)-2]; got != `meta l4proto { tcp, udp } `+drop {
		t.Errorf("prerouting's tcp/udp fall-through rule = %q, want the counted drop %q\n%s", got, drop, pre)
	}
	if last, want := rules[len(rules)-1], `counter name "`+CounterUnproxiedOther+`" drop`; last != want {
		t.Errorf("last prerouting rule = %q, want %q\n%s", last, want, pre)
	}
	// It must come after the TPROXY rule, or nothing would ever be proxied.
	if i, j := strings.Index(pre, "tproxy to :"), strings.Index(pre, drop); i < 0 || j < 0 || i > j {
		t.Errorf("the counted drop must follow the tproxy rule (tproxy=%d drop=%d):\n%s", i, j, pre)
	}

	off := mustRender(t, DefaultSpec(12345, 1))
	if strings.Contains(off, drop) {
		t.Errorf("prerouting must not carry the counted drop with the kill-switch off:\n%s", off)
	}
}
