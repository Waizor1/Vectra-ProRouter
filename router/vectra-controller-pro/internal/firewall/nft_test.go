package firewall

import (
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
