package firewall

import (
	"fmt"
	"strings"
	"testing"
)

func TestRender_DefaultSpec(t *testing.T) {
	s := DefaultSpec(12345, 1)
	out, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: must mention the table, tproxy port, both sets, and use the
	// block form (a single `table inet vctl { ... }` declaration so nft -f -
	// commits the whole thing atomically).
	for _, must := range []string{
		"table inet vctl {",
		"tproxy to :12345",
		"meta mark set 0x1",
		"@bypass4",
		"@vctl_direct4",
		"@vctl_direct6",
		"hook prerouting",
		"hook output",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("expected %q in output\n%s", must, out)
		}
	}
}

// Every named counter must be both declared as an object and referenced by a
// rule. A declared-but-unreferenced counter reads 0 forever, which is
// indistinguishable from "the data plane carried nothing" — the exact false
// negative this instrumentation exists to remove.
func TestRender_NamedCountersDeclaredAndReferenced(t *testing.T) {
	out, err := Render(DefaultSpec(12345, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{CounterTproxyHits, CounterEgressExempt, CounterOutputMarked,
		CounterKillSwitchShadow, CounterUnproxiedOther, CounterTproxyEscaped} {
		if !strings.Contains(out, "counter "+name+" { }") {
			t.Errorf("counter object %q not declared\n%s", name, out)
		}
		if !strings.Contains(out, `counter name "`+name+`"`) {
			t.Errorf("counter %q declared but never referenced by a rule\n%s", name, out)
		}
	}
}

func TestRender_NoIPv6(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.IPv6Enabled = false
	out, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "ip6 daddr") {
		t.Errorf("ipv6 disabled but ip6 rules emitted:\n%s", out)
	}
}

func TestRoutingAndRevertCommands(t *testing.T) {
	s := DefaultSpec(12345, 1)
	r := RoutingCommands(s)
	if len(r) < 2 {
		t.Fatalf("expected at least 2 routing cmds, got %d", len(r))
	}
	rev := RevertCommands(s)
	if !strings.HasPrefix(rev[0], "nft flush") {
		t.Errorf("unexpected revert order: %v", rev)
	}
}

// A REPLY IS NOT EGRESS, and the output chain has to know that.
//
// Anything the router sends on an existing connection is an answer to something
// that came IN — a LAN client talking to SSH, LuCI or dnsmasq — and must go back
// the way it arrived. Only connections the router ORIGINATES belong on the
// provider's routing. Without the `ct state established,related return` those
// replies fall through to the marking rule, get the tproxy FwMark, and the
// fwmark policy route sends them to `local ::/0 dev lo`.
//
// It hid on IPv4 because every RFC1918 LAN is inside @bypass4 and the reply
// returns two rules further down. @bypass6 covers only ::1/128, fc00::/7,
// fe80::/10 and ff00::/8, so a LAN with ISP-DELEGATED GLOBAL v6 addresses is not
// covered — and the router stops answering its own clients over v6. Measured on
// the stand (MODE=ipv6-killswitch): request in, no reply out,
// vctl_output_marked +7 while vctl_killswitch_drops stayed 0.
//
// Asserted as ORDER, not presence: after the rule that marks, it would do
// nothing.
func TestOutputChainReturnsRepliesBeforeMarkingThem(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec Spec
	}{
		{"armed", killSwitchSpec()},
		{"shadow", DefaultSpec(12345, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rules := rulesOf(chainNamed(t, mustRender(t, tc.spec), "output"))

			ct, mark := -1, -1
			for i, r := range rules {
				if strings.HasPrefix(r, "ct state ") && strings.HasSuffix(r, " return") {
					ct = i
				}
				if strings.Contains(r, "meta mark set 0x") {
					mark = i
				}
			}
			if ct < 0 {
				t.Fatalf("the output chain never returns on an established connection:\n  %s",
					strings.Join(rules, "\n  "))
			}
			if mark < 0 {
				t.Fatalf("no marking rule in the output chain:\n  %s", strings.Join(rules, "\n  "))
			}
			if ct > mark {
				t.Errorf("the established-connection return is AFTER the marking rule (%d > %d), so it never fires:\n  %s",
					ct, mark, strings.Join(rules, "\n  "))
			}
			if !strings.Contains(rules[ct], "established") || !strings.Contains(rules[ct], "related") {
				t.Errorf("the return covers %q; both established and related are needed (an ICMP error for a proxied flow is `related`)", rules[ct])
			}
		})
	}
}

func localGuardChain(t *testing.T, ruleset string) string {
	t.Helper()
	i := strings.Index(ruleset, "chain local_guard {")
	if i < 0 {
		return ""
	}
	j := strings.Index(ruleset[i:], "\n  }")
	return ruleset[i : i+j]
}

// xray's API, metrics and exit probe listen on loopback, the first two without
// authentication, the probe with a password anyone can read. The guard
// refuses xray's OWN sockets (a LAN client can make xray dial loopback through
// sniffing + a DIRECT rule) and non-root processes — and nothing else.
func TestLocalGuardRefusesXraysOwnSocketsAndNonRootOnTheControlPorts(t *testing.T) {
	out, err := Render(DefaultSpec(12345, 1))
	if err != nil {
		t.Fatal(err)
	}
	chain := localGuardChain(t, out)
	if chain == "" {
		t.Fatalf("no local_guard chain:\n%s", out)
	}
	for _, want := range []string{
		"type filter hook output priority filter; policy accept;",
		`meta mark 0x5644 ip daddr 127.0.0.0/8 tcp dport { 10085, 10086, 10087 } counter name "vctl_local_guard" reject with tcp reset`,
		`meta mark 0x5644 ip6 daddr ::1 tcp dport { 10085, 10086, 10087 } counter name "vctl_local_guard" reject with tcp reset`,
		`oifname "lo" tcp dport { 10085, 10086, 10087 } meta skuid != 0 counter name "vctl_local_guard" reject with tcp reset`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("local_guard lacks:\n  %s\nchain:\n%s", want, chain)
		}
	}
	// Every rule is scoped to the guarded ports: the chain must never become a
	// general output filter.
	for _, line := range strings.Split(chain, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "reject") && !strings.Contains(line, "tcp dport { 10085, 10086, 10087 }") {
			t.Errorf("unscoped rule in local_guard: %s", line)
		}
	}
	if !strings.Contains(out, "counter "+CounterLocalGuard+" { }") {
		t.Error("the guard's counter is not declared")
	}
}

func TestLocalGuardAbsentWithoutGuardPorts(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.GuardPorts = nil
	out, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if localGuardChain(t, out) != "" || strings.Contains(out, CounterLocalGuard) {
		t.Fatal("a guard was emitted with no ports to guard")
	}
}

// A connection the router opens goes into xray with every packet: the marking
// rule tags the connection too, and the rest of it — established, original
// direction — is marked and sent the same way BEFORE the established return.
// Replies to connections that came IN (SSH, LuCI, dnsmasq) return first.
func TestOutputChainCarriesTheRoutersOwnFlowsWhole(t *testing.T) {
	for _, spec := range []Spec{killSwitchSpec(), DefaultSpec(12345, 1)} {
		rules := rulesOf(chainNamed(t, mustRender(t, spec), "output"))
		at := func(want string) int {
			for i, r := range rules {
				if r == want {
					return i
				}
			}
			return -1
		}
		reply := at("ct direction reply return")
		follow := at("ct mark 0x1 meta mark set 0x1 return")
		est := at("ct state established,related return")
		mark := -1
		for i, r := range rules {
			if strings.Contains(r, "meta mark set 0x1 ct mark set 0x1") {
				mark = i
			}
		}
		if reply < 0 || follow < 0 || est < 0 || mark < 0 || !(reply < follow && follow < est && est < mark) {
			t.Fatalf("want reply return < ct-mark follow < established return < marking rule that tags the connection:\n  %s",
				strings.Join(rules, "\n  "))
		}
	}
}

// A connection to the router's own address on xray's TPROXY port is xray's
// listener spoken to directly: dokodemo-door takes the router itself for the
// destination and proxies into itself until it runs out of descriptors. The
// guard drops what TPROXY did not deliver — and only that.
func TestRender_InboundGuardDropsDirectConnectionsToTheTproxyPort(t *testing.T) {
	out, err := Render(DefaultSpec(12345, 1))
	if err != nil {
		t.Fatal(err)
	}
	chain := chainBody(t, out, "inbound_guard")
	for _, must := range []string{
		"type filter hook input priority filter - 10; policy accept;",
		`ct direction original meta l4proto { tcp, udp } th dport 12345 fib daddr type local meta mark != 0x1 counter name "` + CounterInboundGuard + `" drop`,
	} {
		if !strings.Contains(chain, must) {
			t.Errorf("inbound_guard lacks %q:\n%s", must, chain)
		}
	}
	if !strings.Contains(out, "counter "+CounterInboundGuard+" { }") {
		t.Errorf("counter %s not declared", CounterInboundGuard)
	}
	// What TPROXY delivers is untouched: the capture rule still marks and
	// accepts, and the guard is the only rule naming the port as a dport.
	if !strings.Contains(out, "tproxy to :12345 meta mark set 0x1 accept") {
		t.Error("the TPROXY capture changed")
	}
	if n := strings.Count(out, "th dport 12345"); n != 1 {
		t.Errorf("th dport 12345 appears %d times, want the guard's once", n)
	}
	// Only a connection's original direction: an answer to the router's own
	// socket on that port (dnsmasq's random source ports) is not dropped.
	if !strings.HasPrefix(strings.TrimSpace(strings.SplitN(chain, "\n", 3)[2]), "ct direction original ") {
		t.Errorf("inbound_guard judges replies too:\n%s", chain)
	}
	// Another port and mark are followed.
	out, _ = Render(DefaultSpec(7000, 0x2))
	if !strings.Contains(chainBody(t, out, "inbound_guard"), "th dport 7000 fib daddr type local meta mark != 0x2 ") {
		t.Errorf("inbound_guard does not follow the spec:\n%s", out)
	}
}

// xray dialling into the LAN — a sniffed "Host: 192.168.1.1", a provider's
// connection sent there — is dropped; its answers to the LAN's own
// connections leave from the address the client asked for and pass.
func TestRender_LANEgressGuardDropsXrayDialsIntoTheLAN(t *testing.T) {
	s := DefaultSpec(12345, 1)
	out, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	mark := fmt.Sprintf("0x%x", s.SockMark)
	want := `meta mark ` + mark + ` oifname { "br-lan" } fib saddr type local counter name "` + CounterLANDial + `" drop`
	if chain := chainBody(t, out, "lan_egress_guard"); !strings.Contains(chain, want) || !strings.Contains(chain, "type filter hook output priority filter; policy accept;") {
		t.Errorf("lan_egress_guard without devices:\n%s", chain)
	}
	if !strings.Contains(out, "counter "+CounterLANDial+" { }") {
		t.Errorf("counter %s not declared", CounterLANDial)
	}
	// The LAN's devices as netifd reports them, a guest bridge among them;
	// a name no interface can have is left out.
	s.LANDevices = []string{"br-lan", "br-guest", `x"; drop`}
	out, _ = Render(s)
	if chain := chainBody(t, out, "lan_egress_guard"); !strings.Contains(chain, `oifname { "br-lan", "br-guest" } fib saddr type local`) {
		t.Errorf("lan_egress_guard with devices:\n%s", chain)
	}
	// No SockMark, no way to tell xray's sockets: no guard.
	s.SockMark = 0
	out, _ = Render(s)
	if strings.Contains(out, "lan_egress_guard") {
		t.Error("lan_egress_guard rendered without a SockMark")
	}
}
