package firewall

import (
	"strings"
	"testing"
)

func TestKillSwitchPrereroutingPolicy(t *testing.T) {
	spec := DefaultSpec(12345, 1)

	// Default (off): fail-open — prerouting accepts.
	off, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(off, "hook prerouting priority mangle; policy accept;") {
		t.Errorf("default prerouting should be 'policy accept':\n%s", off)
	}

	// On: prerouting fails CLOSED (drop), but OUTPUT (router's own traffic,
	// incl. control plane + DNS) MUST stay accept so the router is never cut off.
	spec.KillSwitch = true
	on, err := Render(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(on, "hook prerouting priority mangle; policy drop;") {
		t.Errorf("kill-switch prerouting should be 'policy drop':\n%s", on)
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
