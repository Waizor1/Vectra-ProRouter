package firewall

import (
	"strings"
	"testing"
)

// The owner, 2026-09-30: «IPv6 заблокирован у нас на серверах — выключи его
// и у нас». The provider's nodes carry no IPv6: a LAN connection over IPv6
// captured by TPROXY is accepted by xray at once and then hangs at the exit
// — a page that never loads, no fallback, since TCP connected. Let past, it
// would leave unproxied. Refused, the device gets a reset at once and takes
// IPv4. The LAN's own IPv6 (bypass6) stays.
func TestIPv6FromTheLANIsRefusedNotCapturedNorLetPast(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.RefuseIPv6 = true
	out := mustRender(t, s)
	pre := rulesOf(chainNamed(t, out, "prerouting"))
	bypass := indexOf(pre, "ip6 daddr @bypass6 return")
	skip := indexOf(pre, "meta nfproto ipv6 return")
	tproxy := indexOf(pre, "meta l4proto { tcp, udp } counter name \"vctl_tproxy_hits\" tproxy")
	direct6 := indexOf(pre, "ct state new ip6 daddr @")
	if bypass < 0 || skip < 0 || skip < bypass || skip > tproxy || (direct6 >= 0 && skip > direct6) {
		t.Fatalf("prerouting: bypass6 %d, ipv6 return %d, direct v6 %d, tproxy %d:\n%s", bypass, skip, direct6, tproxy, strings.Join(pre, "\n"))
	}
	fwd := rulesOf(chainNamed(t, out, "forward"))
	fb := indexOf(fwd, "ip6 daddr @bypass6 return")
	tcp := indexOf(fwd, `meta nfproto ipv6 meta l4proto tcp counter name "vctl_ipv6_refused" reject with tcp reset`)
	other := indexOf(fwd, `meta nfproto ipv6 counter name "vctl_ipv6_refused" reject with icmpv6 type admin-prohibited`)
	ctdirect := indexOf(fwd, "ct mark and 0x")
	if fb < 0 || tcp < 0 || other < 0 || tcp < fb || other < tcp || (ctdirect >= 0 && other > ctdirect) {
		t.Fatalf("forward: bypass6 %d, tcp reset %d, icmpv6 %d, direct return %d:\n%s", fb, tcp, other, ctdirect, strings.Join(fwd, "\n"))
	}
	if !strings.Contains(out, "counter vctl_ipv6_refused { }") {
		t.Fatal("the refusal's counter is not declared")
	}
}

// Carried (UCI ipv6 '1', servers that take IPv6): as before, nothing refused.
func TestIPv6CarriedIsCapturedAsBefore(t *testing.T) {
	out := mustRender(t, DefaultSpec(12345, 1))
	if strings.Contains(out, "vctl_ipv6_refused") || strings.Contains(out, "meta nfproto ipv6 return") {
		t.Fatalf("IPv6 refused without being asked:\n%s", out)
	}
}

// The review of r29–r31: the refusal had no direction — it also refused new
// IPv6 from the internet to a LAN device and routed IPv6 between the LAN's
// own networks, ahead of fw4. Into the LAN's own devices it is left to fw4 as
// without vctl; everything else — to the WAN, a tunnel, anything unknown — is
// still refused. Known LAN devices only: with none known the refusal stays
// whole (a device misread as the LAN's would let IPv6 out unproxied).
func TestIPv6IntoTheLANsOwnDevicesIsLeftToTheFirewall(t *testing.T) {
	s := DefaultSpec(12345, 1)
	s.RefuseIPv6 = true
	s.LANDevices = []string{"br-lan", "br-guest"}
	fwd := rulesOf(chainNamed(t, mustRender(t, s), "forward"))
	lan := indexOf(fwd, `meta nfproto ipv6 oifname { "br-lan", "br-guest" } return`)
	tcp := indexOf(fwd, `meta nfproto ipv6 meta l4proto tcp counter name "vctl_ipv6_refused" reject with tcp reset`)
	if lan < 0 || tcp < 0 || lan > tcp {
		t.Fatalf("forward: LAN return %d, tcp reset %d:\n%s", lan, tcp, strings.Join(fwd, "\n"))
	}
	s.LANDevices = nil
	if out := chainNamed(t, mustRender(t, s), "forward"); strings.Contains(out, "oifname {") {
		t.Fatalf("a LAN return with no LAN device known:\n%s", out)
	}
	s.RefuseIPv6, s.LANDevices = false, []string{"br-lan"}
	if out := chainNamed(t, mustRender(t, s), "forward"); strings.Contains(out, "oifname {") {
		t.Fatalf("a LAN return while IPv6 is carried:\n%s", out)
	}
}
