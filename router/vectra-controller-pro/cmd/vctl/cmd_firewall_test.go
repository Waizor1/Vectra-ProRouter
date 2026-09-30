package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/firewall"
)

// The review of r29–r31: the stand applies the data plane through `vctl
// firewall`, which carries IPv6, so the daemon's refusal (RefuseIPv6) had
// never met a packet. -refuse-ipv6 renders the daemon's ruleset for the
// stand to drive; without it the CLI still carries.
func TestFirewallRenderRefusesIPv6OnlyWhenAsked(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "operator.json")
	if err := os.WriteFile(cfg, []byte(`{"schema":1,"instance":{"name":"t"},"process":{"xrayBinary":"/usr/sbin/vctl-xray-wrapper"},
"inbounds":{"tproxy":{"listenIP":"::","port":12345,"fwmark":1,"udpEnabled":true,"tag":"tproxy-in"}},"geo":{"assetDir":"/usr/share/v2ray"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	render := func(extra ...string) string {
		t.Helper()
		out := filepath.Join(dir, "rules.nft")
		if err := cmdFirewall(append([]string{"render", "-config", cfg, "-out", out}, extra...)); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if carried := render(); strings.Contains(carried, firewall.CounterIPv6Refused+"\" reject") || strings.Contains(carried, "admin-prohibited") {
		t.Fatalf("the CLI refused IPv6 unasked:\n%s", carried)
	}
	refused := render("-refuse-ipv6")
	if !strings.Contains(refused, "admin-prohibited") || !strings.Contains(refused, "reject with tcp reset") {
		t.Fatalf("-refuse-ipv6 rendered no refusal:\n%s", refused)
	}
}

// -lan-devices names the LAN's devices for the stand, as netifd does for the
// daemon: IPv6 into them is left to the router's own firewall.
func TestFirewallRenderLeavesIPv6IntoTheLANToTheFirewall(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "operator.json")
	if err := os.WriteFile(cfg, []byte(`{"schema":1,"instance":{"name":"t"},"process":{"xrayBinary":"/usr/sbin/vctl-xray-wrapper"},
"inbounds":{"tproxy":{"listenIP":"::","port":12345,"fwmark":1,"udpEnabled":true,"tag":"tproxy-in"}},"geo":{"assetDir":"/usr/share/v2ray"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "rules.nft")
	if err := cmdFirewall([]string{"render", "-config", cfg, "-out", out, "-refuse-ipv6", "-lan-devices", "vclient,br-guest"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `meta nfproto ipv6 oifname { "vclient", "br-guest" } return`) {
		t.Fatalf("no LAN return:\n%s", b)
	}
}
