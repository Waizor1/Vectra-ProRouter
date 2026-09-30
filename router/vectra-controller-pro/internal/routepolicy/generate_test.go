package routepolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// fleetArgs are what vctl's passwallArgs passes for the test router's UCI —
// and what the golden files were made with, by PassWall2 26.8.10's own
// generator (lua util_xray.lua gen_config) on the data-plane stand.
var fleetArgs = map[string]string{
	"loglevel": "error", "flag": "global", "node": "myshunt", "redir_port": "12345",
	"tcp_proxy_way": "tproxy", "dns_listen_port": "10053",
	"direct_dns_query_strategy": "UseIP", "remote_dns_query_strategy": "UseIPv4",
	"direct_dns_udp_server": "77.37.251.33", "direct_dns_udp_port": "53",
	"remote_dns_protocol": "doh", "remote_dns_detour": "direct",
	"remote_dns_doh_url": "https://dns.google/dns-query", "remote_dns_doh_host": "dns.google", "remote_dns_doh_port": "443",
}

func readUCI(t *testing.T, name string) []Section {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	secs, err := ParseUCI(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return secs
}

func jsonValue(t *testing.T, b []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return v
}

// diff lists the paths where two JSON values differ.
func diff(path string, a, b any, out *[]string) {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: object vs %T", path, b))
			return
		}
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			av, aok := x[k]
			bv, bok := y[k]
			switch {
			case !aok:
				*out = append(*out, fmt.Sprintf("%s.%s: missing in ours (golden %v)", path, k, short(bv)))
			case !bok:
				*out = append(*out, fmt.Sprintf("%s.%s: extra in ours (%v)", path, k, short(av)))
			default:
				diff(path+"."+k, av, bv, out)
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: array vs %T", path, b))
			return
		}
		if len(x) != len(y) {
			*out = append(*out, fmt.Sprintf("%s: %d items vs golden %d", path, len(x), len(y)))
		}
		for i := 0; i < len(x) && i < len(y); i++ {
			diff(fmt.Sprintf("%s[%d]", path, i), x[i], y[i], out)
		}
	default:
		if !reflect.DeepEqual(a, b) {
			*out = append(*out, fmt.Sprintf("%s: %v vs golden %v", path, short(a), short(b)))
		}
	}
}

func short(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// variantArgs go with fleet-variant.uci: remote FakeDNS, UseIP (both pools),
// the DNS cache off, remote DNS over TCP through the proxy, REDIRECT for TCP.
var variantArgs = map[string]string{
	"loglevel": "warn", "flag": "global", "node": "myshunt", "redir_port": "12345",
	"tcp_proxy_way": "redirect", "dns_listen_port": "10053",
	"direct_dns_query_strategy": "UseIPv4", "remote_dns_query_strategy": "UseIP", "dns_cache": "0",
	"direct_dns_udp_server": "192.0.2.53", "direct_dns_udp_port": "53",
	"remote_dns_protocol": "tcp", "remote_dns_tcp_server": "1.1.1.1", "remote_dns_tcp_port": "53",
	"remote_dns_fake": "1", "remote_dns_fake_strategy": "UseIP",
}

// The same UCI and arguments in, what PassWall2's own generator made out.
func TestGenerateMatchesPassWall2(t *testing.T) {
	for _, tc := range []struct {
		uci, golden, xray string
		args              map[string]string
	}{
		{"fleet.uci", "fleet.gen-26.7.28.json", "26.7.28", fleetArgs},
		{"fleet.uci", "fleet.gen-26.3.27.json", "26.3.27", fleetArgs},
		{"fleet-variant.uci", "fleet-variant.gen-26.7.28.json", "26.7.28", variantArgs},
		{"fleet-variant.uci", "fleet-variant.gen-26.3.27.json", "26.3.27", variantArgs},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			golden, err := os.ReadFile(filepath.Join("testdata", tc.golden))
			if err != nil {
				t.Fatalf("the golden file is the proof: %v", err)
			}
			out, err := Generate(readUCI(t, tc.uci), tc.args, tc.xray)
			if err != nil {
				t.Fatal(err)
			}
			var d []string
			diff("$", jsonValue(t, out), jsonValue(t, golden), &d)
			if len(d) > 0 {
				t.Fatalf("differs from PassWall2's own output in %d places:\n  %s", len(d), strings.Join(d, "\n  "))
			}
		})
	}
}

func TestGenerateRefusesWhatTheFleetDoesNotUse(t *testing.T) {
	base := readUCI(t, "fleet.uci")
	set := func(secs []Section, name, key, val string) []Section {
		out := make([]Section, len(secs))
		for i, s := range secs {
			c := Section{Type: s.Type, Name: s.Name, Anonymous: s.Anonymous, Options: map[string]string{}, Lists: s.Lists}
			for k, v := range s.Options {
				c.Options[k] = v
			}
			if s.Name == name {
				c.Options[key] = val
			}
			out[i] = c
		}
		return out
	}
	for name, secs := range map[string][]Section{
		"fragment":       set(base, "global_xray1", "fragment", "1"),
		"sniffing":       set(base, "global_xray1", "sniffing_override_dest", "1"),
		"pre-proxy":      set(base, "myshunt", "WorldProxy_proxy_tag", "nodeFR01"),
		"sing-box node":  set(base, "nodeNL01", "type", "sing-box"),
		"hysteria2 node": set(base, "nodeNL01", "protocol", "hysteria2"),
		"balancer":       set(base, "nodeNL01", "protocol", "_balancing"),
		"per-node DNS":   set(set(base, "nodeNL01", "domain_resolver", "udp"), "nodeNL01", "domain_resolver_dns", "1.1.1.1"),
		"not a shunt":    set(base, "myshunt", "protocol", "vless"),
		"kcp transport":  set(base, "nodeFR01", "transport", "mkcp"),
		"http tcp guise": set(base, "nodeBY01", "tcp_guise", "http"),
		"unknown node":   set(base, "vectra_global", "node", "nope"),
	} {
		args := fleetArgs
		if name == "unknown node" {
			args = map[string]string{}
			for k, v := range fleetArgs {
				args[k] = v
			}
			args["node"] = "nope"
		}
		if _, err := Generate(secs, args, "26.7.28"); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err %v, want ErrUnsupported", name, err)
		}
	}
}
