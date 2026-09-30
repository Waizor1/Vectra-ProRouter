package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/firewall"
)

// The arguments are what PassWall2's own run_global passes, read from the
// same UCI — here the test router's.
func TestPassWallArgsAreRunGlobals(t *testing.T) {
	uci := map[string]string{
		"passwall2.@global[0].node":                      "myshunt",
		"passwall2.@global[0].remote_dns_protocol":       "doh",
		"passwall2.@global[0].remote_dns_doh":            "https://dns.google/dns-query",
		"passwall2.@global[0].remote_dns_detour":         "direct",
		"passwall2.@global[0].remote_dns_query_strategy": "UseIPv4",
		"passwall2.@global[0].direct_dns_query_strategy": "UseIP",
		"passwall2.@global[0].loglevel":                  "error",
	}
	get := func(k string) string { return uci[k] }
	a, err := passwallArgs(get, 12345, 10053, "77.37.251.33")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"loglevel": "error", "flag": "global", "node": "myshunt", "redir_port": "12345", "tcp_proxy_way": "tproxy",
		"dns_listen_port": "10053", "direct_dns_query_strategy": "UseIP", "remote_dns_query_strategy": "UseIPv4",
		"direct_dns_udp_server": "77.37.251.33", "direct_dns_udp_port": "53",
		"remote_dns_protocol": "doh", "remote_dns_detour": "direct",
		"remote_dns_doh_url": "https://dns.google/dns-query", "remote_dns_doh_host": "dns.google", "remote_dns_doh_port": "443",
	}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("args =\n%v\nwant\n%v", a, want)
	}

	uci["passwall2.@global[0].remote_dns_doh"] = "https://1.1.1.1:8443/dns-query"
	uci["passwall2.@global[0].remote_fakedns"] = "1"
	a, _ = passwallArgs(get, 12345, 10053, "")
	if a["remote_dns_doh_ip"] != "1.1.1.1" || a["remote_dns_doh_port"] != "8443" || a["remote_dns_fake"] != "1" || a["remote_dns_fake_strategy"] != "UseIPv4" {
		t.Fatalf("doh by address / fakedns: %v", a)
	}
	if _, ok := a["direct_dns_udp_server"]; ok {
		t.Fatal("a direct DNS without a WAN resolver")
	}

	uci["passwall2.@global[0].remote_dns_protocol"] = "tcp"
	uci["passwall2.@global[0].remote_dns"] = "8.8.4.4#5353"
	a, _ = passwallArgs(get, 12345, 10053, "")
	if a["remote_dns_tcp_server"] != "8.8.4.4" || a["remote_dns_tcp_port"] != "5353" {
		t.Fatalf("tcp remote DNS: %v", a)
	}

	delete(uci, "passwall2.@global[0].node")
	if _, err := passwallArgs(get, 12345, 10053, ""); err == nil {
		t.Fatal("no global node, and yet arguments")
	}
}

// A render with FakeDNS pools: the pools are carried into xray — taken out of
// the bypass set, which holds 198.18.0.0/15 — and the firewall key says so.
func TestFakeDNSPoolsAreCarried(t *testing.T) {
	d := dnsDaemon(t, `{"inbounds":[],"fakedns":[{"ipPool":"198.18.0.0/16","poolSize":65535}]}`, nil)
	spec := firewall.DefaultSpec(12345, 1)
	d.carryFakeDNS(&spec)
	for _, b := range spec.BypassV4 {
		if b == "198.18.0.0/15" {
			t.Fatal("the FakeDNS pool is still bypassed")
		}
	}
	if len(spec.BypassV4) != len(firewall.DefaultSpec(12345, 1).BypassV4)-1 {
		t.Fatalf("more than the pool's range left the bypass set: %v", spec.BypassV4)
	}
	if fakeDNSKey(spec) != ";fakedns=198.18.0.0/16" {
		t.Fatalf("key = %q", fakeDNSKey(spec))
	}

	plain := dnsDaemon(t, renderWithDNS, nil)
	spec = firewall.DefaultSpec(12345, 1)
	plain.carryFakeDNS(&spec)
	if len(spec.BypassV4) != len(firewall.DefaultSpec(12345, 1).BypassV4) || fakeDNSKey(spec) != "" {
		t.Fatal("a render without FakeDNS changed the bypass set")
	}
}

func TestWanResolverIsTheFirstIPv4(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "tmp", "resolv.conf.d"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "tmp", "resolv.conf.d", "resolv.conf.auto"), []byte("# Interface wan6\nnameserver 2001:db8::1\n# Interface wan\nnameserver 77.37.251.33\nnameserver 77.37.255.30\n"), 0o644)
	if got := wanResolver(root); got != "77.37.251.33" {
		t.Fatalf("resolver = %q", got)
	}
	if got := wanResolver(t.TempDir()); got != "" {
		t.Fatalf("no resolv.conf.auto: %q", got)
	}
}

// A sync refused because the router was too short of memory to check it
// waits; any other failure is tried again at the next loop.
func TestPassWallSyncBacksOffOnLowMemory(t *testing.T) {
	low := fmt.Errorf("apply: refusing to install (previous config left in place): %w",
		fmt.Errorf("xray validate: %w (20 MiB free, 24 MiB wanted)", xray.ErrLowMemory))
	if got := passwallSyncBackoff(low); got != directRetry {
		t.Fatalf("low memory: backoff %v, want %v", got, directRetry)
	}
	if got := passwallSyncBackoff(errors.New("PassWall2's generator failed")); got != 0 {
		t.Fatalf("other failure: backoff %v, want 0", got)
	}
}
