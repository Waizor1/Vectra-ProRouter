package main

import (
	"reflect"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/firewall"
)

// netifd's `network.interface dump`, trimmed: the LAN and a guest network set
// up statically, the loopback, a DHCP WAN with its IPv6 default route learned
// from the ISP (source-specific), a static WAN of a second router with its own
// default route, and a static network that is down.
const netifdDump = `{"interface":[
{"interface":"lan","up":true,"proto":"static","l3_device":"br-lan","route":[]},
{"interface":"guest","up":true,"proto":"static","l3_device":"br-guest","route":[]},
{"interface":"loopback","up":true,"proto":"static","l3_device":"lo","route":[]},
{"interface":"wan","up":true,"proto":"dhcp","l3_device":"wan","route":[{"target":"0.0.0.0","mask":0,"nexthop":"192.0.2.1","source":"0.0.0.0/0"}]},
{"interface":"wan6","up":true,"proto":"dhcpv6","l3_device":"wan","route":[{"target":"::","mask":0,"nexthop":"fe80::1","source":"2001:db8:5::/56"}]},
{"interface":"uplink","up":true,"proto":"static","l3_device":"eth9","route":[{"target":"0.0.0.0","mask":0,"nexthop":"198.51.100.1"}]},
{"interface":"iot","up":false,"proto":"static","l3_device":"br-iot","route":[]}
]}`

// The IPv6 refusal's exemption (the review of r29–r31) goes to the LAN's own
// devices only: the statically set up networks that are up and carry no
// default route. A device a default route leaves by is never the LAN's.
func TestTheLANsDevicesAreTheStaticOnesNoDefaultRouteLeavesBy(t *testing.T) {
	if got := lanDevicesFrom([]byte(netifdDump)); !reflect.DeepEqual(got, []string{"br-guest", "br-lan"}) {
		t.Fatalf("LAN devices %v", got)
	}
	// A static interface sharing a device with a default route's is not the LAN's.
	shared := `{"interface":[{"interface":"lan","up":true,"proto":"static","l3_device":"eth0","route":[]},
{"interface":"wan6","up":true,"proto":"dhcpv6","l3_device":"eth0","route":[{"target":"::","mask":0,"nexthop":"fe80::1"}]}]}`
	if got := lanDevicesFrom([]byte(shared)); len(got) != 0 {
		t.Fatalf("a default route's device read as the LAN's: %v", got)
	}
	if got := lanDevicesFrom([]byte("not json")); got != nil {
		t.Fatalf("an unreadable dump gave %v", got)
	}
}

// The daemon's loaded ruleset carries them only while IPv6 is refused — and
// netifd is not asked on the every-loop checks (dataPlaneMissing).
func TestTheRefusalKnowsTheLANsDevices(t *testing.T) {
	prev := lanDevices
	t.Cleanup(func() { lanDevices = prev })
	asked := 0
	lanDevices = func() []string { asked++; return []string{"br-lan"} }
	base := firewall.DefaultSpec(12345, 1)
	if s := withLANDevices(withLoadGuards(base, agentcfg.Config{})); !reflect.DeepEqual(s.LANDevices, []string{"br-lan"}) {
		t.Fatalf("refused, LAN devices %v", s.LANDevices)
	}
	if s := withLANDevices(withLoadGuards(base, agentcfg.Config{IPv6: true})); s.LANDevices != nil {
		t.Fatalf("carried, LAN devices %v", s.LANDevices)
	}
	asked = 0
	_ = withLoadGuards(base, agentcfg.Config{})
	if asked != 0 {
		t.Fatal("the load guards alone asked netifd")
	}
}
