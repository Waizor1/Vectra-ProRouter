package xray_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
)

func dnsOptions() *xray.DNSOptions {
	return &xray.DNSOptions{
		Listen:          xray.DefaultDNSListen,
		DirectResolvers: xray.DefaultDirectResolvers,
		DirectDomains:   []string{"domain:vectra-pro.net", "full:sub.example.org"},
	}
}

type splicedDNSDoc struct {
	Inbounds []struct {
		Tag      string `json:"tag"`
		Listen   string `json:"listen"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
		Settings struct {
			Address string `json:"address"`
			Port    int    `json:"port"`
			Network string `json:"network"`
		} `json:"settings"`
	} `json:"inbounds"`
	Outbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	} `json:"outbounds"`
	Routing struct {
		Rules []map[string]json.RawMessage `json:"rules"`
	} `json:"routing"`
	DNS map[string]json.RawMessage `json:"dns"`
}

func decodeDNSDoc(t *testing.T, raw []byte) splicedDNSDoc {
	t.Helper()
	var d splicedDNSDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("decode spliced: %v", err)
	}
	return d
}

type dnsServer struct {
	Address      string   `json:"address"`
	Domains      []string `json:"domains"`
	SkipFallback bool     `json:"skipFallback"`
}

func dnsServers(t *testing.T, d splicedDNSDoc) []json.RawMessage {
	t.Helper()
	var servers []json.RawMessage
	if err := json.Unmarshal(d.DNS["servers"], &servers); err != nil {
		t.Fatalf("dns.servers: %v", err)
	}
	return servers
}

// The router's resolver asks through the tunnel: a loopback DNS inbound, a
// rule that hands it to xray's built-in DNS, and the provider's own servers
// behind it — with the nodes' and the control plane's names answered first,
// directly, so resolving a node never needs the tunnel it is part of.
func TestDNSThroughTheTunnelOnTheRealDocument(t *testing.T) {
	provider := providerFixture(t)
	spliced, res, err := xray.Splice(provider, testTproxy(), xray.SpliceOptions{DNS: dnsOptions()})
	if err != nil {
		t.Fatalf("splice: %v", err)
	}
	d := decodeDNSDoc(t, spliced)

	if len(d.Inbounds) != 2 || d.Inbounds[0].Tag != "tproxy-in" || d.Inbounds[1].Tag != xray.DNSInboundTag {
		t.Fatalf("inbounds = %+v, want the tproxy one and the DNS one", d.Inbounds)
	}
	in := d.Inbounds[1]
	if in.Listen != "127.0.0.1" || in.Port != 10053 || in.Protocol != "dokodemo-door" || in.Settings.Network != "tcp,udp" || in.Settings.Port != 53 {
		t.Fatalf("DNS inbound = %+v", in)
	}
	if port, ok := xray.RenderDNSListen(spliced); !ok || port != 10053 {
		t.Fatalf("RenderDNSListen = %d, %v", port, ok)
	}

	last := d.Outbounds[len(d.Outbounds)-1]
	if last.Tag != xray.DNSOutboundTag || last.Protocol != "dns" {
		t.Fatalf("last outbound = %+v, want the DNS one", last)
	}
	if d.Outbounds[0].Tag != "bridge-pl5" {
		t.Fatalf("first outbound (xray's default) changed to %q", d.Outbounds[0].Tag)
	}

	first := d.Routing.Rules[0]
	if string(first["inboundTag"]) != `["`+xray.DNSInboundTag+`"]` || string(first["outboundTag"]) != `"`+xray.DNSOutboundTag+`"` {
		t.Fatalf("first rule = %v, want the DNS inbound to the DNS outbound", first)
	}
	if string(d.Routing.Rules[1]["outboundTag"]) != `"BLOCK"` {
		t.Fatalf("the provider's first rule moved: rule 1 = %v", d.Routing.Rules[1])
	}
	var providerDoc splicedDNSDoc
	if err := json.Unmarshal(provider, &providerDoc); err != nil {
		t.Fatal(err)
	}
	if len(d.Routing.Rules) != len(providerDoc.Routing.Rules)+1 {
		t.Fatalf("rules %d, want the provider's %d plus one", len(d.Routing.Rules), len(providerDoc.Routing.Rules))
	}

	servers := dnsServers(t, d)
	wantHosts := []string{"full:ru10.provider.invalid", "full:ru11.provider.invalid", "full:ru12.provider.invalid", "full:ru4.provider.invalid", "full:ru8.provider.invalid", "full:ru9.provider.invalid"}
	for i, r := range []string{"8.8.8.8", "77.88.8.8"} {
		var s dnsServer
		if err := json.Unmarshal(servers[i], &s); err != nil {
			t.Fatalf("server %d: %v", i, err)
		}
		if s.Address != "tcp+local://"+r || !s.SkipFallback {
			t.Fatalf("server %d = %+v, want tcp+local://%s skipping fallback", i, s, r)
		}
		want := append(append([]string(nil), wantHosts...), "domain:vectra-pro.net", "full:sub.example.org")
		if !reflect.DeepEqual(s.Domains, want) {
			t.Fatalf("server %d domains = %v, want %v", i, s.Domains, want)
		}
	}
	providerServers := dnsServers(t, providerDoc)
	if len(servers) != len(providerServers)+2 {
		t.Fatalf("dns.servers = %d, want the provider's %d after the two direct ones", len(servers), len(providerServers))
	}
	for i, s := range providerServers {
		if string(servers[i+2]) != string(s) {
			t.Fatalf("provider server %d changed: %s -> %s", i, s, servers[i+2])
		}
	}
	for k, v := range providerDoc.DNS {
		if k == "servers" {
			continue
		}
		if string(d.DNS[k]) != string(v) {
			t.Fatalf("dns.%s changed: %s -> %s", k, v, d.DNS[k])
		}
	}
	if res.DNS.Listen != xray.DefaultDNSListen || res.DNS.NodeHosts != 6 || res.DNS.AddedDNS || res.DNS.Skipped != "" {
		t.Fatalf("result = %+v", res.DNS)
	}
}

// Without DNS options nothing of this exists, and the render key is what it
// was before DNS through the tunnel did.
func TestNoDNSOptionsNoDNSInbound(t *testing.T) {
	spliced, res, err := xray.Splice(providerFixture(t), testTproxy(), xray.SpliceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := xray.RenderDNSListen(spliced); ok {
		t.Fatal("a DNS inbound without DNS options")
	}
	if !reflect.DeepEqual(res.DNS, xray.DNSResult{}) {
		t.Fatalf("result = %+v", res.DNS)
	}
	base := xray.SpliceOptions{APIListen: xray.DefaultAPIListen}
	with := base
	with.DNS = dnsOptions()
	if base.Key() == with.Key() {
		t.Fatal("the render key does not tell DNS through the tunnel apart")
	}
	if strings.Contains(base.Key(), "dns") {
		t.Fatalf("the key without DNS changed: %s", base.Key())
	}
}

// A document with no "dns" of its own asks the system resolver — here, the
// router's own dnsmasq, redirected back into xray. It gets one: the direct
// servers and a general one.
func TestDNSAddedWhenTheProviderHasNone(t *testing.T) {
	doc := `{"outbounds":[{"tag":"node","protocol":"vless","settings":{"address":"n1.example.net","port":443}}],` +
		`"routing":{"rules":[{"network":"tcp,udp","outboundTag":"node"}]}}`
	spliced, res, err := xray.Splice([]byte(doc), testTproxy(), xray.SpliceOptions{DNS: dnsOptions()})
	if err != nil {
		t.Fatal(err)
	}
	d := decodeDNSDoc(t, spliced)
	servers := dnsServers(t, d)
	if len(servers) != 3 || string(servers[2]) != `"1.1.1.1"` {
		t.Fatalf("dns.servers = %s", d.DNS["servers"])
	}
	var s dnsServer
	_ = json.Unmarshal(servers[0], &s)
	if s.Domains[0] != "full:n1.example.net" {
		t.Fatalf("the node's flat address is not resolved directly: %+v", s)
	}
	if !res.DNS.AddedDNS || res.DNS.NodeHosts != 1 {
		t.Fatalf("result = %+v", res.DNS)
	}
}

// A document the router cannot resolve for safely is installed as it was:
// no DNS inbound, so the firewall redirects nothing.
func TestDNSSkippedWhenTheProviderCannotAnswerTheLAN(t *testing.T) {
	for name, dns := range map[string]string{
		"fakedns":            `{"servers":["fakedns","1.1.1.1"]}`,
		"localhost fallback": `{"servers":["1.1.1.1","localhost"]}`,
		"localhost domains":  `{"servers":[{"address":"localhost","domains":["geosite:private"],"skipFallback":true}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			doc := `{"dns":` + dns + `,"outbounds":[{"tag":"node","protocol":"vless","settings":{"address":"n1.example.net"}}]}`
			spliced, res, err := xray.Splice([]byte(doc), testTproxy(), xray.SpliceOptions{DNS: dnsOptions()})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := xray.RenderDNSListen(spliced); ok || res.DNS.Skipped == "" || res.DNS.Listen != "" {
				t.Fatalf("steered anyway: %+v", res.DNS)
			}
			if !strings.Contains(string(spliced), dns) {
				t.Fatalf("the provider's dns changed:\n%s", spliced)
			}
		})
	}
	// A localhost xray never falls back to (the real document's) is fine.
	doc := `{"dns":{"servers":["1.1.1.1",{"address":"localhost","skipFallback":true}]},"outbounds":[{"tag":"node","protocol":"vless","settings":{"address":"n1.example.net"}}]}`
	if _, res, err := xray.Splice([]byte(doc), testTproxy(), xray.SpliceOptions{DNS: dnsOptions()}); err != nil || res.DNS.Listen == "" {
		t.Fatalf("a skipFallback localhost blocked DNS: %+v %v", res.DNS, err)
	}
}

// The owner's sites and DNS together: the DNS rule first, the owner's next,
// the provider's after — each for its own inbound only.
func TestDNSAndTheOwnersSitesTogether(t *testing.T) {
	opts := xray.SpliceOptions{DNS: dnsOptions(), Rules: xray.UserRules{Direct: []string{"sberbank.ru"}, Proxy: []string{"example.com"}}}
	spliced, res, err := xray.Splice(providerFixture(t), testTproxy(), opts)
	if err != nil {
		t.Fatal(err)
	}
	d := decodeDNSDoc(t, spliced)
	if string(d.Routing.Rules[0]["inboundTag"]) != `["`+xray.DNSInboundTag+`"]` {
		t.Fatalf("rule 0 = %v", d.Routing.Rules[0])
	}
	for i := 1; i <= res.UserRules.Rules; i++ {
		if string(d.Routing.Rules[i]["inboundTag"]) != `["tproxy-in"]` {
			t.Fatalf("rule %d = %v, want the owner's", i, d.Routing.Rules[i])
		}
	}
	if res.UserRules.Rules != 2 {
		t.Fatalf("owner's rules = %d", res.UserRules.Rules)
	}
}

func TestDNSOptionsAreChecked(t *testing.T) {
	for name, o := range map[string]*xray.DNSOptions{
		"not loopback":    {Listen: "0.0.0.0:10053", DirectResolvers: []string{"8.8.8.8"}},
		"no port":         {Listen: "127.0.0.1", DirectResolvers: []string{"8.8.8.8"}},
		"v6 resolver":     {Listen: xray.DefaultDNSListen, DirectResolvers: []string{"2001:4860:4860::8888"}},
		"no resolver":     {Listen: xray.DefaultDNSListen},
		"a quoted domain": {Listen: xray.DefaultDNSListen, DirectResolvers: []string{"8.8.8.8"}, DirectDomains: []string{`full:a"b`}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := xray.Splice(providerFixture(t), testTproxy(), xray.SpliceOptions{DNS: o}); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// Every way a document names a node's host: vnext, servers, the flat address
// newer documents use, WireGuard peers. Addresses and non-dialling outbounds
// are not names to resolve.
func TestNodeHostsFromEveryShape(t *testing.T) {
	doc := `{"outbounds":[
		{"tag":"a","protocol":"vmess","settings":{"vnext":[{"address":"A.example.net."},{"address":"203.0.113.5"}]}},
		{"tag":"b","protocol":"trojan","settings":{"servers":[{"address":"b.example.net"}]}},
		{"tag":"c","protocol":"hysteria","settings":{"address":"c.example.net"}},
		{"tag":"d","protocol":"wireguard","settings":{"peers":[{"endpoint":"d.example.net:51820"},{"endpoint":"[2001:db8::1]:51820"}]}},
		{"tag":"e","protocol":"freedom","settings":{"domainStrategy":"UseIPv4"}},
		{"tag":"f","protocol":"vless","settings":{"address":"a.example.net"}}
	]}`
	spliced, res, err := xray.Splice([]byte(doc), testTproxy(), xray.SpliceOptions{DNS: dnsOptions()})
	if err != nil {
		t.Fatal(err)
	}
	var s dnsServer
	_ = json.Unmarshal(dnsServers(t, decodeDNSDoc(t, spliced))[0], &s)
	want := []string{"full:a.example.net", "full:b.example.net", "full:c.example.net", "full:d.example.net", "domain:vectra-pro.net", "full:sub.example.org"}
	if !reflect.DeepEqual(s.Domains, want) || res.DNS.NodeHosts != 4 {
		t.Fatalf("domains = %v (%d hosts)", s.Domains, res.DNS.NodeHosts)
	}
}

// A DNS server named by its host is dialled after resolving that host through
// the system resolver — back here — so its name is resolved directly too.
func TestDNSServerHostsAreResolvedDirectly(t *testing.T) {
	doc := `{"dns":{"servers":["https://dns.example/dns-query",{"address":"tcp://Resolver.Example:53"},"1.1.1.1"]},` +
		`"outbounds":[{"tag":"node","protocol":"vless","settings":{"address":"n1.example.net"}}]}`
	spliced, res, err := xray.Splice([]byte(doc), testTproxy(), xray.SpliceOptions{DNS: dnsOptions()})
	if err != nil {
		t.Fatal(err)
	}
	var s dnsServer
	_ = json.Unmarshal(dnsServers(t, decodeDNSDoc(t, spliced))[0], &s)
	want := []string{"full:dns.example", "full:n1.example.net", "full:resolver.example", "domain:vectra-pro.net", "full:sub.example.org"}
	if !reflect.DeepEqual(s.Domains, want) || res.DNS.NodeHosts != 3 {
		t.Fatalf("domains = %v (%d)", s.Domains, res.DNS.NodeHosts)
	}
}

// The owner, 2026-09-30: the nodes carry no IPv6, and the router refuses the
// LAN's. Its resolver answers IPv4 only, so a device never tries IPv6 first
// — whatever the provider's document says, with a dns object or without.
func TestTheResolverAnswersIPv4OnlyWhenIPv6IsRefused(t *testing.T) {
	for name, doc := range map[string]string{
		"provider asks both": `{"dns":{"servers":["1.1.1.1"],"queryStrategy":"UseIP"},"outbounds":[{"tag":"n1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443,"users":[{"id":"u"}]}]}},{"tag":"DIRECT","protocol":"freedom"}],"routing":{"rules":[{"network":"tcp,udp","outboundTag":"n1"}]}}`,
		"no dns at all":      `{"outbounds":[{"tag":"n1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443,"users":[{"id":"u"}]}]}},{"tag":"DIRECT","protocol":"freedom"}],"routing":{"rules":[{"network":"tcp,udp","outboundTag":"n1"}]}}`,
	} {
		for _, v4 := range []bool{true, false} {
			opts := xray.SpliceOptions{DNS: &xray.DNSOptions{Listen: xray.DefaultDNSListen, DirectResolvers: xray.DefaultDirectResolvers, IPv4Only: v4}}
			out, _, err := xray.Splice([]byte(doc), testTproxy(), opts)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			var d struct {
				DNS struct {
					QueryStrategy string `json:"queryStrategy"`
				} `json:"dns"`
			}
			if err := json.Unmarshal(out, &d); err != nil {
				t.Fatal(err)
			}
			if v4 && d.DNS.QueryStrategy != "UseIPv4" {
				t.Fatalf("%s: IPv6 refused, queryStrategy %q", name, d.DNS.QueryStrategy)
			}
			if !v4 && name == "provider asks both" && d.DNS.QueryStrategy != "UseIP" {
				t.Fatalf("%s: carried, the provider's %q changed", name, d.DNS.QueryStrategy)
			}
		}
	}
	a := xray.SpliceOptions{DNS: &xray.DNSOptions{Listen: xray.DefaultDNSListen, DirectResolvers: xray.DefaultDirectResolvers}}.Key()
	b := xray.SpliceOptions{DNS: &xray.DNSOptions{Listen: xray.DefaultDNSListen, DirectResolvers: xray.DefaultDirectResolvers, IPv4Only: true}}.Key()
	if a == b {
		t.Fatal("the splice key ignores IPv4Only")
	}
}

// dnsLevelDoc is what the DNS session level touches in a spliced document.
type dnsLevelDoc struct {
	Inbounds []struct {
		Tag      string                     `json:"tag"`
		Settings map[string]json.RawMessage `json:"settings"`
	} `json:"inbounds"`
	Outbounds []struct {
		Tag      string                     `json:"tag"`
		Settings map[string]json.RawMessage `json:"settings"`
	} `json:"outbounds"`
	Policy struct {
		Levels map[string]map[string]json.RawMessage `json:"levels"`
		System json.RawMessage                       `json:"system"`
	} `json:"policy"`
}

func dnsLevelOf(t *testing.T, raw []byte) (in, out string, d dnsLevelDoc) {
	t.Helper()
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	for _, ib := range d.Inbounds {
		if ib.Tag == xray.DNSInboundTag {
			in = string(ib.Settings["userLevel"])
		}
	}
	for _, ob := range d.Outbounds {
		if ob.Tag == xray.DNSOutboundTag {
			out = string(ob.Settings["userLevel"])
		}
	}
	return in, out, d
}

// 1111, 2026-09-30 (the stress test): every query dnsmasq forwards comes from
// a port of its own, so each is a session of the DNS inbound — and it lived
// the provider's connIdle, 120 s. 600 lookups left 2136 sessions and 6400
// goroutines; an ordinary browsing burst left hundreds (AAAA answers, empty
// under IPv4Only, are never cached by dnsmasq). The DNS inbound and outbound
// get a level of their own: idle 8 s — above a slow answer's 4 s, far below
// 120 — closed as soon as a side is done, no buffer.
func TestDNSSessionsLiveSecondsNotTheProvidersMinutes(t *testing.T) {
	spliced, _, err := xray.Splice(providerFixture(t), testTproxy(), xray.SpliceOptions{DNS: dnsOptions()})
	if err != nil {
		t.Fatal(err)
	}
	in, out, d := dnsLevelOf(t, spliced)
	if in == "" || in != out {
		t.Fatalf("DNS inbound level %q, outbound level %q: want the same level on both", in, out)
	}
	lvl := d.Policy.Levels[in]
	want := map[string]string{"connIdle": "8", "handshake": "4", "uplinkOnly": "0", "downlinkOnly": "0", "bufferSize": "0"}
	for k, v := range want {
		if string(lvl[k]) != v {
			t.Fatalf("level %s = %v, want %v", in, lvl, want)
		}
	}
	// The provider's own levels and system stay as they were: level 0, and 8
	// its users name, are not ours to take.
	if in == "0" || in == "8" {
		t.Fatalf("the DNS level %s is one the provider uses", in)
	}
	if string(d.Policy.Levels["0"]["connIdle"]) != "120" || len(d.Policy.System) == 0 {
		t.Fatalf("the provider's policy changed: %+v", d.Policy)
	}
}

// A provider with no policy at all gets one holding the DNS level alone —
// every other level keeps xray's defaults.
func TestDNSLevelWhenTheProviderHasNoPolicy(t *testing.T) {
	provider := []byte(`{"outbounds":[{"tag":"node","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"00000000-0000-4000-8000-000000000000","level":16}]}]}}],
"routing":{"rules":[{"network":"tcp,udp","outboundTag":"node"}]},"dns":{"servers":["1.1.1.1"]}}`)
	spliced, _, err := xray.Splice(provider, testTproxy(), xray.SpliceOptions{DNS: dnsOptions()})
	if err != nil {
		t.Fatal(err)
	}
	in, out, d := dnsLevelOf(t, spliced)
	if in == "" || in != out || in == "16" {
		t.Fatalf("DNS level in %q out %q (16 is the provider's users')", in, out)
	}
	if len(d.Policy.Levels) != 1 || string(d.Policy.Levels[in]["connIdle"]) != "8" {
		t.Fatalf("policy levels %v", d.Policy.Levels)
	}
}
