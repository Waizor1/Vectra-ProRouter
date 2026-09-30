package main

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/rescue"
)

// A geoip.dat in protobuf wire format, for the loader.
func geoipDat(cats map[string][]string) []byte {
	varint := func(v uint64) []byte {
		var b []byte
		for v >= 0x80 {
			b = append(b, byte(v)|0x80)
			v >>= 7
		}
		return append(b, byte(v))
	}
	field := func(n int, v []byte) []byte {
		b := varint(uint64(n<<3 | 2))
		b = append(b, varint(uint64(len(v)))...)
		return append(b, v...)
	}
	var out []byte
	for code, cidrs := range cats {
		e := field(1, []byte(code))
		for _, c := range cidrs {
			p := netip.MustParsePrefix(c)
			cidr := field(1, p.Addr().AsSlice())
			cidr = append(cidr, varint(2<<3)...)
			cidr = append(cidr, varint(uint64(p.Bits()))...)
			e = append(e, field(2, cidr)...)
		}
		out = append(out, field(1, e)...)
	}
	return out
}

const directRender = `{
 "fakedns":[{"ipPool":"198.18.0.0/16","poolSize":65535}],
 "outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"WorldProxy","protocol":"vless"}],
 "routing":{"rules":[
  {"inboundTag":["vctl-dns-in"],"outboundTag":"vctl-dns-out"},
  {"inboundTag":["tproxy-in"],"network":"tcp,udp","ip":["geoip:DIRECT","1.2.3.0/24"],"outboundTag":"direct"},
  {"inboundTag":["tproxy-in"],"network":"tcp,udp","domains":["geosite:META"],"outboundTag":"WorldProxy"}
 ]}
}`

func directDaemon(t *testing.T) (*daemon, *[]string) {
	t.Helper()
	dir := t.TempDir()
	assets := filepath.Join(dir, "geo")
	if err := os.MkdirAll(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	dat := geoipDat(map[string][]string{
		"DIRECT": {"5.8.0.0/16", "5.9.0.0/16", "77.88.8.8/32", "198.18.5.0/24", "2a02:6b8::/32"},
		"OTHER":  {"9.9.9.0/24"},
	})
	if err := os.WriteFile(filepath.Join(assets, "geoip.dat"), dat, 0o644); err != nil {
		t.Fatal(err)
	}
	// IPv6 carried (UCI ipv6 '1'): both families; the refusal's own test sets it off.
	d := &daemon{cfg: agentcfg.Config{XrayRenderPath: filepath.Join(dir, "xray.json"), GeoAssetDir: assets, IPv6: true}}
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(directRender), 0o600); err != nil {
		t.Fatal(err)
	}
	d.desired = &config.Config{}
	d.desired.Inbounds.Tproxy = &config.TproxyInbound{Port: 12345}
	programmed := ""
	d.fwProgrammed = &programmed
	d.readMem = func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 60 * 1024}, nil }
	var loads []string
	d.directLoad = func(_ context.Context, script string) error {
		loads = append(loads, script)
		return nil
	}
	return d, &loads
}

// What the routing sends straight out by address is loaded into the direct
// sets in one transaction: merged, without FakeDNS addresses, both families.
func TestDirectSetsTakeTheRoutingsDirectAddresses(t *testing.T) {
	d, loads := directDaemon(t)
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 {
		t.Fatalf("%d loads, want 1", len(*loads))
	}
	s := (*loads)[0]
	for _, want := range []string{
		"flush set inet vctl vctl_direct4\n", "add element inet vctl vctl_direct4 {",
		"1.2.3.0/24", "5.8.0.0/15", "77.88.8.8",
		"flush set inet vctl vctl_direct6\n", "2a02:6b8::/32",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "198.18.") || strings.Contains(s, "9.9.9.0") {
		t.Errorf("a FakeDNS address or another category got in:\n%s", s)
	}
	if strings.Index(s, "flush set inet vctl vctl_direct4") > strings.Index(s, "add element inet vctl vctl_direct4") {
		t.Error("the flush must come before the load, in the same script")
	}

	// Nothing changed: nothing loaded again.
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 {
		t.Fatalf("reloaded an unchanged list: %d loads", len(*loads))
	}
	// A new geo file (the nightly update): loaded again.
	geo := filepath.Join(d.cfg.GeoAssetDir, "geoip.dat")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(geo, later, later); err != nil {
		t.Fatal(err)
	}
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 2 {
		t.Fatalf("a changed geo file was not loaded: %d loads", len(*loads))
	}
	// A reprogrammed table (programFirewall resets directLoaded): again.
	d.directLoaded = ""
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 3 {
		t.Fatalf("an empty new table was not loaded: %d loads", len(*loads))
	}
}

// The review of r29–r31: while IPv6 is refused (the default, UCI ipv6 unset)
// the IPv6 direct set buys nothing — the refusal comes before it in both
// chains — and cost the kernel and vctl's heap an eighth of the budget. It is
// emptied, and the budget goes to IPv4.
func TestDirectSetsCarryNoIPv6WhileIPv6IsRefused(t *testing.T) {
	d, loads := directDaemon(t)
	d.cfg.IPv6 = false
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 {
		t.Fatalf("%d loads, want 1", len(*loads))
	}
	s := (*loads)[0]
	if strings.Contains(s, "2a02:6b8::") || strings.Contains(s, "add element inet vctl vctl_direct6") {
		t.Fatalf("IPv6 ranges loaded while IPv6 is refused:\n%s", s)
	}
	if !strings.Contains(s, "flush set inet vctl vctl_direct6\n") || !strings.Contains(s, "77.88.8.8") {
		t.Fatalf("want the v6 set emptied and v4 loaded:\n%s", s)
	}
}

func TestDirectSetsOnlyWhenWanted(t *testing.T) {
	for name, mod := range map[string]func(d *daemon){
		"turned off":         func(d *daemon) { d.cfg.NoDirectBypass = true },
		"no data plane":      func(d *daemon) { d.fwProgrammed = nil },
		"rescue direct mode": func(d *daemon) { d.st.Rescue.Mode = string(rescue.ModeDirect) },
		"no operator config": func(d *daemon) { d.desired = nil },
		"no tproxy inbound":  func(d *daemon) { d.desired.Inbounds.Tproxy = nil },
		"no render":          func(d *daemon) { _ = os.Remove(d.cfg.XrayRenderPath) },
	} {
		d, loads := directDaemon(t)
		mod(d)
		d.maybeLoadDirect(context.Background())
		if len(*loads) != 0 {
			t.Errorf("%s: loaded %d times", name, len(*loads))
		}
	}
}

// Short of memory, or a failed load: nothing is loaded (xray carries those
// addresses, as before), and it is tried again after a while, not every loop.
func TestDirectSetsWaitForMemoryAndRetry(t *testing.T) {
	d, loads := directDaemon(t)
	d.readMem = func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 20 * 1024}, nil }
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 0 || d.directLoaded != "" || d.directFailKey == "" {
		t.Fatalf("20 MiB free: loads %d loaded %q failKey %q", len(*loads), d.directLoaded, d.directFailKey)
	}
	d.readMem = func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 60 * 1024}, nil }
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 0 {
		t.Fatal("retried within the retry interval")
	}
	d.directFailAt = time.Now().Add(-directRetry - time.Second)
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 || d.directLoaded == "" {
		t.Fatalf("after the interval: loads %d loaded %q", len(*loads), d.directLoaded)
	}

	d2, _ := directDaemon(t)
	d2.directLoad = func(context.Context, string) error { return errors.New("nft: out of memory") }
	d2.maybeLoadDirect(context.Background())
	if d2.directLoaded != "" || d2.directFailKey == "" {
		t.Fatal("a failed load counted as loaded")
	}
}

// Short of memory, part of the list is loaded — the largest ranges — and a
// fuller load is tried once memory allows, after a while.
func TestDirectSetsLoadInPartAndUpgradeLater(t *testing.T) {
	d, loads := directDaemon(t)
	var cidrs []string
	for i := 0; i < 3000; i++ {
		cidrs = append(cidrs, netip.AddrFrom4([4]byte{byte(40 + i/250), byte(i % 250), 0, 0}).String()+"/24")
	}
	cidrs = append(cidrs, "31.0.0.0/8") // the largest, kept first
	if err := os.WriteFile(filepath.Join(d.cfg.GeoAssetDir, "geoip.dat"), geoipDat(map[string][]string{"DIRECT": cidrs}), 0o644); err != nil {
		t.Fatal(err)
	}
	d.readMem = func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 28 * 1024}, nil }
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 || !d.directPartial {
		t.Fatalf("28 MiB free: loads %d partial %v, want one partial load", len(*loads), d.directPartial)
	}
	part := strings.Count((*loads)[0], ",\n") + 1
	if part >= 3002 || part < 500 || !strings.Contains((*loads)[0], "31.0.0.0/8") {
		t.Fatalf("partial load of %d elements (of 3002), the /8 kept: %v", part, strings.Contains((*loads)[0], "31.0.0.0/8"))
	}
	d.readMem = func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 60 * 1024}, nil }
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 {
		t.Fatal("upgraded before its time")
	}
	d.directLoadedAt = time.Now().Add(-directUpgrade - time.Second)
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 2 || d.directPartial {
		t.Fatalf("after %v with memory: loads %d partial %v, want a full second load", directUpgrade, len(*loads), d.directPartial)
	}
	if full := strings.Count((*loads)[1], ",\n") + 1; full != 3002 {
		t.Fatalf("full load has %d elements, want 3002", full)
	}
}

// A new list that cannot be loaded (memory short, here) does not leave the
// old one in the kernel: what left the list must go back to xray.
func TestDirectSetsNeverKeepAStaleList(t *testing.T) {
	d, loads := directDaemon(t)
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 || d.directLoaded == "" {
		t.Fatalf("first load: %d loads", len(*loads))
	}
	geo := filepath.Join(d.cfg.GeoAssetDir, "geoip.dat")
	if err := os.WriteFile(geo, geoipDat(map[string][]string{"DIRECT": {"5.8.0.0/16"}}), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(geo, later, later)
	d.readMem = func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 20 * 1024}, nil }
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 2 || strings.Contains((*loads)[1], "add element") || !strings.Contains((*loads)[1], "flush set inet vctl vctl_direct4") {
		t.Fatalf("a new list that could not load: loads %d, last %q — want the old list flushed", len(*loads), (*loads)[len(*loads)-1])
	}
	if d.directLoaded != "" {
		t.Fatal("the emptied sets still count as loaded")
	}
}

// A partial load is redone only when memory allows more than it holds.
func TestDirectSetsUpgradeOnlyToMore(t *testing.T) {
	d, loads := directDaemon(t)
	var cidrs []string
	for i := 0; i < 3000; i++ {
		cidrs = append(cidrs, netip.AddrFrom4([4]byte{byte(40 + i/250), byte(i % 250), 0, 0}).String()+"/24")
	}
	if err := os.WriteFile(filepath.Join(d.cfg.GeoAssetDir, "geoip.dat"), geoipDat(map[string][]string{"DIRECT": cidrs}), 0o644); err != nil {
		t.Fatal(err)
	}
	d.readMem = func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 28 * 1024}, nil }
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 || !d.directPartial {
		t.Fatalf("partial load: loads %d partial %v", len(*loads), d.directPartial)
	}
	d.directLoadedAt = time.Now().Add(-directUpgrade - time.Second)
	d.maybeLoadDirect(context.Background()) // same memory: no more to load
	if len(*loads) != 1 {
		t.Fatalf("reloaded with no more memory: %d loads", len(*loads))
	}
}

// An ext: geo file's update is a new list, like geoip.dat's.
func TestDirectSetsFollowExtGeoFiles(t *testing.T) {
	d, loads := directDaemon(t)
	ext := filepath.Join(d.cfg.GeoAssetDir, "vectra.dat")
	if err := os.WriteFile(ext, geoipDat(map[string][]string{"DIRECT": {"77.88.0.0/18"}}), 0o644); err != nil {
		t.Fatal(err)
	}
	render := strings.Replace(directRender, `"ip":["geoip:DIRECT","1.2.3.0/24"]`, `"ip":["ext:vectra.dat:direct"]`, 1)
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(render), 0o600); err != nil {
		t.Fatal(err)
	}
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 || !strings.Contains((*loads)[0], "77.88.0.0/18") {
		t.Fatalf("ext: list not loaded: %v", *loads)
	}
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 {
		t.Fatal("reloaded an unchanged ext: list")
	}
	later := time.Now().Add(time.Hour)
	_ = os.Chtimes(ext, later, later)
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 2 {
		t.Fatalf("an updated ext: file was not loaded again: %d loads", len(*loads))
	}
}

// With the names the rules proxy answered by FakeDNS, the Russian networks
// after the provider's domain rules leave by the kernel — less what an
// earlier rule sends elsewhere by address (spec decision 8).
const directRenderFake = `{
 "dns":{"servers":[{"address":"fakedns","domains":["geosite:META"]}]},
 "fakedns":[{"ipPool":"198.18.0.0/16","poolSize":65535}],
 "outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"WorldProxy","protocol":"vless"}],
 "routing":{"rules":[
  {"inboundTag":["vctl-dns-in"],"outboundTag":"vctl-dns-out"},
  {"inboundTag":["tproxy-in"],"domain":["geosite:META"],"balancerTag":"BL-MAIN"},
  {"inboundTag":["tproxy-in"],"ip":["5.9.0.0/16"],"outboundTag":"WorldProxy"},
  {"inboundTag":["tproxy-in"],"ip":["geoip:DIRECT"],"outboundTag":"direct"},
  {"network":"tcp,udp","balancerTag":"BL-MAIN"}
 ]}
}`

func TestDirectSetsReachPastProxiedNamesLessProxiedAddresses(t *testing.T) {
	d, loads := directDaemon(t)
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(directRenderFake), 0o600); err != nil {
		t.Fatal(err)
	}
	d.maybeLoadDirect(context.Background())
	if len(*loads) != 1 {
		t.Fatalf("%d loads, want 1", len(*loads))
	}
	s := (*loads)[0]
	for _, want := range []string{"5.8.0.0/16", "77.88.8.8", "2a02:6b8::/32"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "5.9.") || strings.Contains(s, "5.8.0.0/15") || strings.Contains(s, "198.18.") {
		t.Errorf("a proxied or FakeDNS address got in:\n%s", s)
	}
}
