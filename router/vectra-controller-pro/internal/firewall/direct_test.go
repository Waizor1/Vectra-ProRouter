package firewall

import (
	"strings"
	"testing"
)

// ruleIndex is the index of the first line of chain that contains all of
// parts, -1 when none does.
func ruleIndex(chain string, parts ...string) int {
	for i, line := range strings.Split(chain, "\n") {
		ok := true
		for _, p := range parts {
			if !strings.Contains(line, p) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// A connection the kernel routes straight out is decided on its first packet
// and followed by its conntrack bit: reloading or emptying the direct sets
// must not move a live connection between the kernel and xray (a TCP
// connection that changes hands is reset).
func TestDirectConnectionsStickToTheKernel(t *testing.T) {
	for _, ks := range []bool{false, true} {
		s := DefaultSpec(12345, 1)
		s.KillSwitch = ks
		out := mustRender(t, s)
		pre := chainNamed(t, out, "prerouting")
		follow := ruleIndex(pre, "ct mark and 0x10000000 == 0x10000000 return")
		new4 := ruleIndex(pre, "ct state new ip  daddr @vctl_direct4", "ct mark set ct mark or 0x10000000", `counter name "vctl_direct_new"`, "return")
		new6 := ruleIndex(pre, "ct state new ip6 daddr @vctl_direct6", "ct mark set ct mark or 0x10000000", "return")
		tproxy := ruleIndex(pre, "tproxy to :12345")
		bypass := ruleIndex(pre, "ip  daddr @bypass4 return")
		if follow < 0 || new4 < 0 || new6 < 0 || tproxy < 0 {
			t.Fatalf("killswitch=%v: prerouting lacks the direct rules:\n%s", ks, pre)
		}
		if !(bypass < follow && follow < new4 && new4 < new6 && new6 < tproxy) {
			t.Errorf("killswitch=%v: order bypass %d < follow %d < new4 %d < new6 %d < tproxy %d broken:\n%s", ks, bypass, follow, new4, new6, tproxy, pre)
		}
		// The set alone must never decide for a connection that is not new:
		// it would pull a live xray connection out from under xray.
		if ruleIndex(pre, "ip  daddr @vctl_direct4 return") >= 0 {
			t.Errorf("killswitch=%v: prerouting still tests the direct set for every packet:\n%s", ks, pre)
		}
		fwd := forwardChain(t, out)
		if i, drop := ruleIndex(fwd, "ct mark and 0x10000000 == 0x10000000 return"), ruleIndex(fwd, `counter name "`+s.killCounter()+`"`); i < 0 || (drop >= 0 && i > drop) {
			t.Errorf("killswitch=%v: the forward guard does not let direct connections through before its verdict:\n%s", ks, fwd)
		}
		if !strings.Contains(out, "counter vctl_direct_new { }") {
			t.Errorf("killswitch=%v: the direct counter is not declared", ks)
		}
	}
}

// Without the bit, the sets are tested per packet as before.
func TestDirectSetsWithoutTheBit(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.DirectCtMark = 0
	out := mustRender(t, s)
	pre := chainNamed(t, out, "prerouting")
	if ruleIndex(pre, "ip  daddr @vctl_direct4 return") < 0 || strings.Contains(out, "vctl_direct_new") || strings.Contains(out, "ct mark and") {
		t.Fatalf("DirectCtMark 0 changed the direct rules:\n%s", pre)
	}
}

func (s Spec) killCounter() string {
	if s.KillSwitch {
		return CounterKillSwitchDrops
	}
	return CounterKillSwitchShadow
}

// A reprogram empties the direct sets before it replaces the table: nft
// reads back every interval set it is not told is being flushed, and a loaded
// direct set made every reprogram a 24 MB nft (3 MB with the flush).
func TestReprogramFlushesTheDirectSetsFirst(t *testing.T) {
	out := mustRender(t, DefaultSpec(12345, 1))
	del := strings.Index(out, "delete table inet vctl\n")
	for _, want := range []string{
		"add set inet vctl vctl_direct4 { type ipv4_addr; flags interval; auto-merge; }\nflush set inet vctl vctl_direct4\n",
		"add set inet vctl vctl_direct6 { type ipv6_addr; flags interval; auto-merge; }\nflush set inet vctl vctl_direct6\n",
	} {
		i := strings.Index(out, want)
		if i < 0 || del < 0 || i > del {
			t.Fatalf("the flush %q is not before the delete:\n%s", want, out[:strings.Index(out, "table inet vctl {")])
		}
	}
	s := DefaultSpec(12345, 1)
	s.IPv6Enabled = false
	if strings.Contains(mustRender(t, s), "vctl_direct6") {
		t.Fatal("an IPv4-only table still names the v6 direct set")
	}
}
