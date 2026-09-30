package firewall

import (
	"strings"
	"testing"
)

// dnsmasq's upstream queries — by its uid, to public addresses, port 53 —
// go to xray's DNS inbound; over IPv6 they are refused so it asks over IPv4.
// Part of the table, so every teardown of the data plane takes it along.
func TestRenderSteersDnsmasqUpstreamIntoTheTunnel(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.DNSRedirectPort = 10053
	s.DNSResolverUIDs = []int{453}
	s.DNSRejectV6 = true
	out, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		"counter " + CounterDNSRedirected + " { }",
		"chain dns_steer {",
		"type nat hook output priority dstnat; policy accept;",
		`meta skuid { 453 } ip daddr != @bypass4 meta l4proto { tcp, udp } th dport 53 counter name "` + CounterDNSRedirected + `" redirect to :10053`,
		"chain dns_steer6 {",
		"meta skuid { 453 } ip6 daddr != @bypass6 meta l4proto { tcp, udp } th dport 53 reject",
	} {
		if !strings.Contains(out, must) {
			t.Errorf("missing %q in:\n%s", must, out)
		}
	}
	s.DNSRejectV6 = false
	out, _ = Render(s)
	if strings.Contains(out, "dns_steer6") || !strings.Contains(out, "chain dns_steer {") {
		t.Errorf("no IPv4 upstream to fall back to: IPv6 must not be refused:\n%s", out)
	}
	s.DNSRejectV6 = true
	s.IPv6Enabled = false
	out, _ = Render(s)
	if strings.Contains(out, "dns_steer6") || !strings.Contains(out, "chain dns_steer {") {
		t.Errorf("IPv6 off: want the v4 redirect alone:\n%s", out)
	}
}

// Nothing is redirected unless the caller names a port AND dnsmasq's uid —
// and never root: xray's and vctl's own lookups are root's, and redirected
// they would come back to xray.
func TestRenderNeverSteersWithoutAPortOrAsRoot(t *testing.T) {
	for name, set := range map[string]func(*Spec){
		"nothing":  func(*Spec) {},
		"no uid":   func(s *Spec) { s.DNSRedirectPort = 10053 },
		"no port":  func(s *Spec) { s.DNSResolverUIDs = []int{453} },
		"root":     func(s *Spec) { s.DNSRedirectPort = 10053; s.DNSResolverUIDs = []int{0} },
		"root too": func(s *Spec) { s.DNSRedirectPort = 10053; s.DNSResolverUIDs = []int{453, 0} },
		"bad port": func(s *Spec) { s.DNSRedirectPort = 70000; s.DNSResolverUIDs = []int{453} },
	} {
		t.Run(name, func(t *testing.T) {
			s := DefaultSpec(12345, 1)
			set(&s)
			out, err := Render(s)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "dns_steer") || strings.Contains(out, CounterDNSRedirected) {
				t.Fatalf("steered DNS:\n%s", out)
			}
		})
	}
}

// A reprogram REPLACES the table. Declared into a table that exists, an nft
// block adds its rules to the chains already there and removes nothing: the
// old redirect stayed in force behind a render that no longer served it. The
// declare+delete before the block make the whole script one replacement.
func TestRenderReplacesTheTable(t *testing.T) {
	out, err := Render(DefaultSpec(12345, 1))
	if err != nil {
		t.Fatal(err)
	}
	decl := strings.Index(out, "\ntable inet vctl\n")
	del := strings.Index(out, "\ndelete table inet vctl\n")
	block := strings.Index(out, "\ntable inet vctl {")
	if decl < 0 || del < 0 || block < 0 || !(decl < del && del < block) {
		t.Fatalf("want 'table inet vctl', 'delete table inet vctl', then the block; got positions %d %d %d:\n%s", decl, del, block, out)
	}
	if strings.Count(out, "delete table") != 1 {
		t.Fatalf("more than one delete:\n%s", out)
	}
}

// The LAN's queries to public resolvers are answered by the router's own:
// past TPROXY in prerouting, redirected to :53 in a dstnat chain of the table
// (so every teardown takes it along). Queries to the router and to private
// addresses are left alone.
func TestRenderHijacksTheLANsQueriesToPublicResolvers(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.HijackDNS = true
	out, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	pre := chainBody(t, out, "prerouting")
	hijackReturn := "meta l4proto { tcp, udp } th dport 53 ip saddr @bypass4 ct state new return"
	tproxy := "tproxy to :12345"
	if i, j := strings.Index(pre, hijackReturn), strings.Index(pre, tproxy); i < 0 || j < 0 || i > j {
		t.Errorf("prerouting must let the LAN's DNS past TPROXY before it (return at %d, tproxy at %d):\n%s", i, j, pre)
	}
	for _, must := range []string{
		"meta l4proto { tcp, udp } th dport 53 ip saddr @bypass4 ct status dnat return",
		"meta l4proto { tcp, udp } th dport 53 ip6 saddr @bypass6 ct state new return",
		"meta l4proto { tcp, udp } th dport 53 ip6 saddr @bypass6 ct status dnat return",
	} {
		if i, j := strings.Index(pre, must), strings.Index(pre, tproxy); i < 0 || i > j {
			t.Errorf("prerouting lacks %q before TPROXY:\n%s", must, pre)
		}
	}
	if strings.Contains(pre, "th dport 53 ip saddr @bypass4 return") {
		t.Errorf("a flow older than the hijack must stay with TPROXY:\n%s", pre)
	}
	for _, must := range []string{
		"counter " + CounterDNSHijacked + " { }",
		"chain dns_hijack {",
		"type nat hook prerouting priority dstnat - 5; policy accept;",
		`meta l4proto { tcp, udp } th dport 53 ip saddr @bypass4 ip daddr != @bypass4 fib daddr type != local counter name "` + CounterDNSHijacked + `" redirect to :53`,
		`meta l4proto { tcp, udp } th dport 53 ip6 saddr @bypass6 ip6 daddr != @bypass6 fib daddr type != local counter name "` + CounterDNSHijacked + `" redirect to :53`,
	} {
		if !strings.Contains(out, must) {
			t.Errorf("missing %q in:\n%s", must, out)
		}
	}
	s.IPv6Enabled = false
	out, _ = Render(s)
	if strings.Contains(out, "ip6 saddr @bypass6") {
		t.Errorf("IPv6 off: no v6 hijack:\n%s", out)
	}
	s.HijackDNS = false
	out, _ = Render(s)
	if strings.Contains(out, "dns_hijack") || strings.Contains(out, CounterDNSHijacked) || strings.Contains(out, "th dport 53 ip saddr") {
		t.Errorf("hijacked with HijackDNS off:\n%s", out)
	}
}

// A connection's reply direction is never captured: a port forward's server
// answering the WAN (or a LAN host answering inbound IPv6) is not the LAN's
// traffic. In prerouting before TPROXY, in forward before the kill switch's
// drops — armed or not.
func TestRenderLeavesRepliesAlone(t *testing.T) {
	for _, ks := range []bool{false, true} {
		s := DefaultSpec(12345, 1)
		s.KillSwitch = ks
		out, err := Render(s)
		if err != nil {
			t.Fatal(err)
		}
		for chain, before := range map[string]string{
			"prerouting": "tproxy to :12345",
			"forward":    `meta l4proto { tcp, udp } counter name "` + killCounter(ks) + `"`,
		} {
			body := chainBody(t, out, chain)
			i, j := strings.Index(body, "ct direction reply return"), strings.Index(body, before)
			if i < 0 || j < 0 || i > j {
				t.Errorf("killSwitch=%v: %s must return on replies before %q (%d, %d):\n%s", ks, chain, before, i, j, body)
			}
		}
	}
}

// chainBody is the text of one chain of the rendered table.
func chainBody(t *testing.T, out, name string) string {
	t.Helper()
	i := strings.Index(out, "chain "+name+" {")
	if i < 0 {
		t.Fatalf("no chain %s in:\n%s", name, out)
	}
	j := strings.Index(out[i:], "\n  }")
	if j < 0 {
		t.Fatalf("chain %s never closes", name)
	}
	return out[i : i+j]
}

func killCounter(armed bool) string {
	if armed {
		return CounterKillSwitchDrops
	}
	return CounterKillSwitchShadow
}
