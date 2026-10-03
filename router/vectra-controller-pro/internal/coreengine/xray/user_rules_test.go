package xray_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/xrayview"
)

func withRules(direct, proxy []string) xray.SpliceOptions {
	o := routerOptions()
	o.Rules = xray.UserRules{Direct: direct, Proxy: proxy}
	return o
}

// ruleView is a routing rule as xray reads the fields the owner's use.
type ruleView struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag"`
	Domain      []string `json:"domain"`
	IP          []string `json:"ip"`
	OutboundTag string   `json:"outboundTag"`
	BalancerTag string   `json:"balancerTag"`
}

func routingRules(t *testing.T, doc []byte) ([]json.RawMessage, []ruleView) {
	t.Helper()
	var d struct {
		Routing struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	var views []ruleView
	for _, r := range d.Routing.Rules {
		var v ruleView
		_ = json.Unmarshal(r, &v)
		views = append(views, v)
	}
	return d.Routing.Rules, views
}

type sniffView struct {
	Enabled         bool     `json:"enabled"`
	DestOverride    []string `json:"destOverride"`
	RouteOnly       bool     `json:"routeOnly"`
	MetadataOnly    bool     `json:"metadataOnly"`
	DomainsExcluded []string `json:"domainsExcluded"`
}

func inboundSniffing(t *testing.T, doc []byte) sniffView {
	t.Helper()
	var d struct {
		Inbounds []struct {
			Sniffing sniffView `json:"sniffing"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(doc, &d); err != nil || len(d.Inbounds) != 1 {
		t.Fatalf("inbounds: %v", err)
	}
	return d.Inbounds[0].Sniffing
}

type outboundView struct {
	Tag            string `json:"tag"`
	Protocol       string `json:"protocol"`
	StreamSettings struct {
		Sockopt struct {
			Mark int `json:"mark"`
		} `json:"sockopt"`
	} `json:"streamSettings"`
}

func outbounds(t *testing.T, doc []byte) []outboundView {
	t.Helper()
	var d struct {
		Outbounds []outboundView `json:"outbounds"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	return d.Outbounds
}

func lan(r ruleView) bool { return len(r.InboundTag) == 1 && r.InboundTag[0] == "tproxy-in" }

// The real provider document with sites in both lists: four rules FIRST —
// direct domains, direct addresses, proxy domains, proxy addresses — for the
// LAN only, then every provider rule byte for byte. DIRECT is the provider's
// own freedom; BL-MAIN is where its catch-all sends the rest.
func TestTheOwnersSitesComeFirstForTheLANOnly(t *testing.T) {
	in := providerFixture(t)
	plain, _, err := xray.Splice(in, testTproxy(), routerOptions())
	if err != nil {
		t.Fatal(err)
	}
	out, res, err := xray.Splice(in, testTproxy(), withRules(
		[]string{"sberbank.ru", "госуслуги.рф", "1.2.3.0/24"},
		[]string{"example.org", "2001:db8::/32"}))
	if err != nil {
		t.Fatal(err)
	}
	raw, got := routingRules(t, out)
	want := []ruleView{
		{Type: "field", InboundTag: []string{"tproxy-in"}, Domain: []string{"domain:sberbank.ru", "domain:xn--c1aapkosapc.xn--p1ai"}, OutboundTag: "DIRECT"},
		{Type: "field", InboundTag: []string{"tproxy-in"}, IP: []string{"1.2.3.0/24"}, OutboundTag: "DIRECT"},
		{Type: "field", InboundTag: []string{"tproxy-in"}, Domain: []string{"domain:example.org"}, BalancerTag: "BL-MAIN"},
		{Type: "field", InboundTag: []string{"tproxy-in"}, IP: []string{"2001:db8::/32"}, BalancerTag: "BL-MAIN"},
	}
	if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("first rules = %+v\nwant %+v", got[:min(len(got), len(want))], want)
	}
	if s := string(raw[0]); s != `{"type":"field","inboundTag":["tproxy-in"],"domain":["domain:sberbank.ru","domain:xn--c1aapkosapc.xn--p1ai"],"outboundTag":"DIRECT"}` {
		t.Errorf("rule 0 = %s", s)
	}
	provRaw, _ := routingRules(t, in)
	if len(raw) != len(provRaw)+len(want) {
		t.Fatalf("%d rules, want the provider's %d plus %d", len(raw), len(provRaw), len(want))
	}
	for i := range provRaw {
		if !bytes.Equal(raw[len(want)+i], provRaw[i]) {
			t.Errorf("provider rule %d changed:\n got %s\nwant %s", i, raw[len(want)+i], provRaw[i])
		}
	}

	// Beyond the rules and the inbound's sniffing, nothing moved: the same
	// keys in the same order, every other value and every routing field.
	if strings.Join(orderedKeys(t, out), ",") != strings.Join(orderedKeys(t, plain), ",") {
		t.Errorf("key order %v, want %v", orderedKeys(t, out), orderedKeys(t, plain))
	}
	a, b := topLevel(t, plain), topLevel(t, out)
	for k := range a {
		if k != "routing" && k != "inbounds" && !bytes.Equal(a[k], b[k]) {
			t.Errorf("top-level %q changed", k)
		}
	}
	var ra, rb map[string]json.RawMessage
	_ = json.Unmarshal(a["routing"], &ra)
	_ = json.Unmarshal(b["routing"], &rb)
	for k := range ra {
		if k != "rules" && !bytes.Equal(ra[k], rb[k]) {
			t.Errorf("routing.%s changed", k)
		}
	}

	ur := res.UserRules
	if ur.Rules != 4 || ur.Direct != 3 || ur.Proxy != 2 || ur.Dropped != 0 || ur.DirectVia != "DIRECT" || ur.DirectAdded ||
		ur.ProxyVia != "BL-MAIN" || !ur.ProxyViaBalancer || len(ur.Skipped) != 0 || !ur.SniffingChanged {
		t.Errorf("result = %+v", ur)
	}
	if d := ur.Describe(); !strings.Contains(d, "4 routing rule(s)") || !strings.Contains(d, "via DIRECT") || !strings.Contains(d, "balancer BL-MAIN") {
		t.Errorf("describe = %s", d)
	}
}

// No sites, no change: the render is byte-for-byte what it was before sites
// existed, and so is the key every router already keeps in its state.
func TestNoSitesRenderExactlyAsBefore(t *testing.T) {
	in := providerFixture(t)
	plain, _, err := xray.Splice(in, testTproxy(), routerOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []xray.UserRules{{}, {Direct: []string{}, Proxy: nil}} {
		o := routerOptions()
		o.Rules = r
		got, res, err := xray.Splice(in, testTproxy(), o)
		if err != nil || !bytes.Equal(got, plain) || !reflect.DeepEqual(res.UserRules, xray.UserRulesResult{}) {
			t.Errorf("rules %+v changed the render (%v, %+v)", r, err, res.UserRules)
		}
		if o.Key() != routerOptions().Key() {
			t.Errorf("rules %+v changed the key", r)
		}
	}
	if k := routerOptions().Key(); k != "api=127.0.0.1:10085;metrics=127.0.0.1:10086;probe=10m0s;noaccess=false" {
		t.Errorf("the key without sites is %q; routers keep the old format in state.json", k)
	}
}

// A change to the sites is a change to the render: the key moves with the
// lists, and with the list a site is in.
func TestTheKeyFollowsTheSites(t *testing.T) {
	keys := map[string]string{}
	for name, o := range map[string]xray.SpliceOptions{
		"none":         routerOptions(),
		"direct":       withRules([]string{"sberbank.ru"}, nil),
		"proxy":        withRules(nil, []string{"sberbank.ru"}),
		"both":         withRules([]string{"sberbank.ru"}, []string{"example.org"}),
		"another site": withRules([]string{"sberbank.ru", "vtb.ru"}, nil),
	} {
		if other, dup := keys[o.Key()]; dup {
			t.Errorf("%s and %s share the key %s", name, other, o.Key())
		}
		keys[o.Key()] = name
	}
	if withRules([]string{"sberbank.ru"}, nil).Key() != withRules([]string{"sberbank.ru"}, []string{}).Key() {
		t.Error("the same sites gave two keys")
	}
}

// Domain rules match only a name xray sniffed, and IP rules only the
// destination address: with sites set, the inbound sniffs http/tls/quic with
// routeOnly whatever the operator's config says. Without sites it is exactly
// the operator's; the panel's config needs no change and gets none.
func TestSitesMakeTheInboundSniffForThem(t *testing.T) {
	in := providerFixture(t)
	check := func(name string, s sniffView) {
		t.Helper()
		if !s.Enabled || !s.RouteOnly || s.MetadataOnly {
			t.Errorf("%s: sniffing = %+v", name, s)
		}
		for _, p := range []string{"http", "tls", "quic"} {
			if !contains(s.DestOverride, p) {
				t.Errorf("%s: destOverride %v lacks %s", name, s.DestOverride, p)
			}
		}
	}

	// The stand's operator config: sniffing on, routeOnly off.
	out, res, err := xray.Splice(in, testTproxy(), withRules([]string{"sberbank.ru"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	check("routeOnly off", inboundSniffing(t, out))
	if !res.UserRules.SniffingChanged {
		t.Error("the change to the operator's sniffing was not reported")
	}
	if out, _, _ := xray.Splice(in, testTproxy(), routerOptions()); inboundSniffing(t, out).RouteOnly {
		t.Error("without sites, routeOnly is on although the operator's config has it off")
	}

	// Everything wrong: off, metadata only, one protocol. The operator's
	// exclusions stay, and the operator's config itself is not touched.
	tp := testTproxy()
	tp.Sniffing = config.Sniffing{Enabled: false, DestOverride: []string{"tls"}, MetadataOnly: true, DomainsExcluded: []string{"courier.push.apple.com"}}
	out, _, err = xray.Splice(in, tp, withRules(nil, []string{"example.org"}))
	if err != nil {
		t.Fatal(err)
	}
	s := inboundSniffing(t, out)
	check("sniffing off", s)
	if strings.Join(s.DestOverride, ",") != "tls,http,quic" || strings.Join(s.DomainsExcluded, ",") != "courier.push.apple.com" {
		t.Errorf("sniffing = %+v; the operator's order and exclusions stay", s)
	}
	if tp.Sniffing.Enabled || len(tp.Sniffing.DestOverride) != 1 || !tp.Sniffing.MetadataOnly {
		t.Errorf("the operator's config was modified: %+v", tp.Sniffing)
	}

	// The panel's config (golden fixture): already right, byte-identical.
	panel := testTproxy()
	panel.Sniffing.RouteOnly = true
	with, res, err := xray.Splice(in, panel, withRules([]string{"sberbank.ru"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	without, _, _ := xray.Splice(in, panel, routerOptions())
	if !bytes.Equal(topLevel(t, with)["inbounds"], topLevel(t, without)["inbounds"]) || res.UserRules.SniffingChanged {
		t.Errorf("the panel's sniffing was changed: %s", topLevel(t, with)["inbounds"])
	}
}

const nodeOutbound = `{"tag":"%s","protocol":"vless","settings":{"vnext":[{"address":"198.51.100.7","port":443,"users":[{"id":"u"}]}]}}`

// With no plain freedom in the document, one is added: LAST (the first
// outbound is xray's default and stays the provider's), and marked like
// every dialling outbound, or its packets would be captured by TPROXY again.
func TestADirectOutboundIsAddedMarkedAndLast(t *testing.T) {
	doc := []byte(`{"outbounds":[` + fmt.Sprintf(nodeOutbound, "node") + `,{"tag":"BLOCK","protocol":"blackhole"}],` +
		`"routing":{"rules":[{"network":"tcp,udp","outboundTag":"node"}]}}`)
	_, plainRes, err := xray.Splice(doc, testTproxy(), routerOptions())
	if err != nil {
		t.Fatal(err)
	}
	out, res, err := xray.Splice(doc, testTproxy(), withRules([]string{"sberbank.ru"}, []string{"example.org"}))
	if err != nil {
		t.Fatal(err)
	}
	obs := outbounds(t, out)
	last := obs[len(obs)-1]
	if len(obs) != 3 || obs[0].Tag != "node" || last.Tag != xray.DirectTag || last.Protocol != "freedom" ||
		last.StreamSettings.Sockopt.Mark != config.DefaultXraySockMark {
		t.Fatalf("outbounds = %+v", obs)
	}
	if res.OutboundsMarked != plainRes.OutboundsMarked+1 {
		t.Errorf("marked %d, want %d", res.OutboundsMarked, plainRes.OutboundsMarked+1)
	}
	_, rules := routingRules(t, out)
	if rules[0].OutboundTag != xray.DirectTag || rules[1].OutboundTag != "node" || rules[1].BalancerTag != "" {
		t.Errorf("rules = %+v", rules[:2])
	}
	if !res.UserRules.DirectAdded || res.UserRules.DirectVia != xray.DirectTag {
		t.Errorf("result = %+v", res.UserRules)
	}
	if v, err := xrayview.Parse(out); err != nil || v.Default == nil || v.Default.Tag != "node" {
		t.Errorf("xray's default outbound moved: %+v", v.Default)
	}
}

// Only a freedom that goes straight where it was asked carries a direct
// site: one that chains through another outbound or prefixes a PROXY
// protocol header does not. (One that redirects refuses the whole document:
// TestSpliceRefusesReverseAndRedirectInOutbounds.)
func TestDirectTakesOnlyAPlainFreedom(t *testing.T) {
	node := fmt.Sprintf(nodeOutbound, "node")
	for name, tc := range map[string]struct {
		freedoms string
		via      string
	}{
		"plain":                {`{"tag":"direct","protocol":"freedom","settings":{"domainStrategy":"ForceIPv4"}}`, "direct"},
		"capitalized protocol": {`{"tag":"direct","protocol":"Freedom"}`, "direct"},
		"the first plain one":  {`{"tag":"pp1","protocol":"freedom","settings":{"proxyProtocol":1}},{"tag":"direct","protocol":"freedom"}`, "direct"},
		"proxy protocol":       {`{"tag":"pp","protocol":"freedom","settings":{"proxyProtocol":2}}`, xray.DirectTag},
		"proxy protocol 0":     {`{"tag":"pp","protocol":"freedom","settings":{"proxyProtocol":0}}`, "pp"},
		"chained":              {`{"tag":"ch","protocol":"freedom","proxySettings":{"tag":"node"}}`, xray.DirectTag},
		"dialer proxy":         {`{"tag":"dp","protocol":"freedom","streamSettings":{"sockopt":{"dialerProxy":"node"}}}`, xray.DirectTag},
		"untagged":             {`{"protocol":"freedom"}`, xray.DirectTag},
	} {
		doc := []byte(`{"outbounds":[` + node + `,` + tc.freedoms + `],"routing":{"rules":[]}}`)
		out, res, err := xray.Splice(doc, testTproxy(), withRules([]string{"sberbank.ru"}, nil))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if _, rules := routingRules(t, out); rules[0].OutboundTag != tc.via || res.UserRules.DirectVia != tc.via ||
			res.UserRules.DirectAdded != (tc.via == xray.DirectTag) {
			t.Errorf("%s: direct via %q (%+v), want %q", name, rules[0].OutboundTag, res.UserRules, tc.via)
		}
	}
}

// Where the added freedom would itself leak — picked up by a balancer, a
// tag already taken, or the only outbound (xray's default) — the direct
// sites are left out, the rest rendered, and the document is still
// installed: the owner's choice never keeps the provider's config out.
func TestADirectOutboundThatWouldLeakIsNotAdded(t *testing.T) {
	node := fmt.Sprintf(nodeOutbound, "node")
	for name, doc := range map[string]string{
		"a selector picks it up": `{"outbounds":[` + node + `],"routing":{"rules":[{"network":"tcp,udp","balancerTag":"B"}],` +
			`"balancers":[{"tag":"B","selector":["node","vctl"]}]}}`,
		"the tag is taken": `{"outbounds":[` + node + `,` + fmt.Sprintf(nodeOutbound, xray.DirectTag) + `],"routing":{"rules":[]}}`,
		"no outbounds":     `{"routing":{"rules":[]}}`,
	} {
		out, res, err := xray.Splice([]byte(doc), testTproxy(), withRules([]string{"sberbank.ru"}, []string{"example.org"}))
		if err != nil {
			t.Errorf("%s: the document was refused: %v", name, err)
			continue
		}
		if res.UserRules.Direct != 0 || res.UserRules.DirectAdded || len(res.UserRules.Skipped) == 0 {
			t.Errorf("%s: result = %+v", name, res.UserRules)
		}
		for _, o := range outbounds(t, out) {
			if o.Tag == xray.DirectTag && o.Protocol == "freedom" {
				t.Errorf("%s: %s was added", name, xray.DirectTag)
			}
		}
		for _, r := range func() []ruleView { _, r := routingRules(t, out); return r }() {
			if contains(r.Domain, "domain:sberbank.ru") {
				t.Errorf("%s: a direct rule was rendered: %+v", name, r)
			}
		}
		if name != "no outbounds" && res.UserRules.Proxy != 1 {
			t.Errorf("%s: the proxy site was dropped with the direct ones: %+v", name, res.UserRules)
		}
	}
}

// A proxy site goes where the provider sends everything no rule names: its
// first catch-all rule's target — when that is a way through the VPN.
func TestProxyGoesWhereTheProviderSendsTheRest(t *testing.T) {
	obs := fmt.Sprintf(nodeOutbound, "node-a") + `,` + fmt.Sprintf(nodeOutbound, "node-b") +
		`,{"tag":"DIRECT","protocol":"freedom"},{"tag":"BLOCK","protocol":"blackhole"},` +
		`{"tag":"stage","protocol":"loopback","settings":{"inboundTag":"STAGE"}}`
	bal := `"balancers":[{"tag":"BL-MAIN","selector":["node-"]}]`
	for name, tc := range map[string]struct {
		rules    string
		target   string
		balancer bool
	}{
		"the catch-all balancer": {`[{"domain":["domain:x.org"],"outboundTag":"node-b"},{"network":"tcp,udp","balancerTag":"BL-MAIN"}]`, "BL-MAIN", true},
		"a catch-all node":       {`[{"network":"tcp","outboundTag":"node-b"}]`, "node-b", false},
		"no network at all":      {`[{"outboundTag":"node-b"}]`, "node-b", false},
		"a network list":         {`[{"network":["tcp","udp"],"balancerTag":"BL-MAIN"}]`, "BL-MAIN", true},
		"an empty condition":     {`[{"domain":[],"ip":null,"network":"tcp,udp","balancerTag":"BL-MAIN"}]`, "BL-MAIN", true},
		"a loopback into routing": {`[{"inboundTag":["STAGE"],"balancerTag":"BL-MAIN"},{"network":"tcp,udp","outboundTag":"stage"}]`,
			"stage", false},
		"a catch-all to freedom is no way through": {`[{"network":"tcp,udp","outboundTag":"DIRECT"}]`, "node-a", false},
		"nor to blackhole":                         {`[{"network":"tcp,udp","outboundTag":"BLOCK"}]`, "node-a", false},
		"a UDP-only catch-all":                     {`[{"network":"udp","balancerTag":"BL-MAIN"}]`, "node-a", false},
		"an inbound rule is not a catch-all":       {`[{"inboundTag":["x"],"balancerTag":"BL-MAIN"}]`, "node-a", false},
		"a port rule is not a catch-all":           {`[{"port":"443","balancerTag":"BL-MAIN"}]`, "node-a", false},
		"a balancer that does not exist":           {`[{"network":"tcp,udp","balancerTag":"BL-GONE"}]`, "node-a", false},
		"no rules":                                 {`[]`, "node-a", false},
	} {
		doc := []byte(`{"outbounds":[` + obs + `],"routing":{"rules":` + tc.rules + `,` + bal + `}}`)
		out, res, err := xray.Splice(doc, testTproxy(), withRules(nil, []string{"example.org"}))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		_, rules := routingRules(t, out)
		got, isBalancer := rules[0].OutboundTag, false
		if rules[0].BalancerTag != "" {
			got, isBalancer = rules[0].BalancerTag, true
		}
		if got != tc.target || isBalancer != tc.balancer || res.UserRules.ProxyVia != tc.target || !lan(rules[0]) {
			t.Errorf("%s: proxy via %q (balancer %v), want %q (%v)", name, got, isBalancer, tc.target, tc.balancer)
		}
	}

	// No node at all: nowhere to send them; left out, the rest installed.
	doc := []byte(`{"outbounds":[{"tag":"DIRECT","protocol":"freedom"}],"routing":{"rules":[{"network":"tcp,udp","outboundTag":"DIRECT"}]}}`)
	out, res, err := xray.Splice(doc, testTproxy(), withRules([]string{"sberbank.ru"}, []string{"example.org"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.UserRules.Proxy != 0 || res.UserRules.Direct != 1 || len(res.UserRules.Skipped) != 1 {
		t.Errorf("result = %+v", res.UserRules)
	}
	if _, rules := routingRules(t, out); len(rules) != 2 || rules[0].OutboundTag != "DIRECT" {
		t.Errorf("rules = %+v", rules)
	}
}

// firstMatch walks rules as xray does — the first that matches wins — for a
// LAN connection sniffed as domain, to ip.
func firstMatch(rules []ruleView, domain string, ip netip.Addr) string {
	for _, r := range rules {
		if !lan(r) {
			continue
		}
		for _, m := range r.Domain {
			d := strings.TrimPrefix(m, "domain:")
			if domain == d || strings.HasSuffix(domain, "."+d) {
				return r.OutboundTag + r.BalancerTag
			}
		}
		for _, m := range r.IP {
			if p, err := netip.ParsePrefix(m); err == nil && p.Contains(ip) {
				return r.OutboundTag + r.BalancerTag
			}
			if a, err := netip.ParseAddr(m); err == nil && a == ip {
				return r.OutboundTag + r.BalancerTag
			}
		}
	}
	return "provider"
}

// A site inside a site of the other list wins, however deep the nesting:
// "online.sberbank.ru" through the VPN inside "sberbank.ru" direct, and a
// direct "vip.online.sberbank.ru" inside that.
func TestTheMoreSpecificSiteWins(t *testing.T) {
	out, res, err := xray.Splice(providerFixture(t), testTproxy(), withRules(
		[]string{"sberbank.ru", "vip.online.sberbank.ru", "1.2.0.0/16", "unrelated.example"},
		[]string{"online.sberbank.ru", "1.2.3.0/24"}))
	if err != nil {
		t.Fatal(err)
	}
	_, rules := routingRules(t, out)
	rules = rules[:res.UserRules.Rules]
	for _, tc := range []struct {
		domain, ip, want string
	}{
		{"sberbank.ru", "203.0.113.1", "DIRECT"},
		{"www.sberbank.ru", "203.0.113.1", "DIRECT"},
		{"online.sberbank.ru", "203.0.113.1", "BL-MAIN"},
		{"x.online.sberbank.ru", "203.0.113.1", "BL-MAIN"},
		{"vip.online.sberbank.ru", "203.0.113.1", "DIRECT"},
		{"unrelated.example", "203.0.113.1", "DIRECT"},
		{"", "1.2.3.9", "BL-MAIN"},
		{"", "1.2.9.9", "DIRECT"},
		{"notsberbank.ru", "203.0.113.1", "provider"},
	} {
		if got := firstMatch(rules, tc.domain, netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Errorf("%s / %s goes %s, want %s (rules %+v)", tc.domain, tc.ip, got, tc.want, rules)
		}
	}
	// Three levels of domains, two of addresses: five rules, not one per site.
	if res.UserRules.Rules != 5 {
		t.Errorf("%d rules: %+v", res.UserRules.Rules, rules)
	}
}

// The overrides file is read again on every render; an entry that is not a
// site (edited by hand) never reaches xray as anything but a site.
func TestEntriesAreParsedAgainOnTheWayIn(t *testing.T) {
	var many []string
	for i := 0; i < 150; i++ {
		many = append(many, fmt.Sprintf("s%d.example", i))
	}
	out, res, err := xray.Splice(providerFixture(t), testTproxy(), withRules(
		append([]string{"exa mple.com", "SBERBANK.RU", "sberbank.ru", "geoip:ru"}, many...),
		[]string{"sberbank.ru", "regexp:.*", "ext:geosite.dat:ru"}))
	if err != nil {
		t.Fatal(err)
	}
	ur := res.UserRules
	// direct: 1 + 100 of the 150; dropped: a space, a repeat, "geoip:ru", 50
	// past the limit; proxy: already direct, and two matchers that are not sites.
	if ur.Direct != 101-1 || ur.Proxy != 0 || ur.Dropped != 3+51+3 {
		t.Errorf("result = %+v", ur)
	}
	_, rules := routingRules(t, out)
	for _, r := range rules[:ur.Rules] {
		for _, m := range append(append([]string{}, r.Domain...), r.IP...) {
			if !strings.HasPrefix(m, "domain:") || strings.ContainsAny(m[len("domain:"):], ":*") {
				t.Errorf("rendered matcher %q", m)
			}
		}
	}
}

// xray finds "routing" and its "rules" whatever their case, so the splice
// must too: a second, differently spelled key would win, and carry only the
// owner's rules. A document without routing gets one.
func TestRoutingIsFoundHoweverItIsSpelled(t *testing.T) {
	node := fmt.Sprintf(nodeOutbound, "n")
	doc := []byte(`{"outbounds":[` + node + `,{"tag":"DIRECT","protocol":"freedom"}],` +
		`"Routing":{"domainStrategy":"AsIs","Rules":[{"network":"tcp,udp","outboundTag":"n"}]}}`)
	out, _, err := xray.Splice(doc, testTproxy(), withRules([]string{"sberbank.ru"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if keys := strings.Join(orderedKeys(t, out), ","); keys != "outbounds,Routing,inbounds,api,metrics" {
		t.Errorf("keys = %s", keys)
	}
	_, rules := routingRules(t, out)
	if len(rules) != 2 || rules[0].OutboundTag != "DIRECT" || rules[1].OutboundTag != "n" {
		t.Errorf("rules = %+v", rules)
	}

	for name, doc := range map[string]string{
		"no routing":   `{"outbounds":[` + node + `]}`,
		"null routing": `{"outbounds":[` + node + `],"routing":null}`,
		"null rules":   `{"outbounds":[` + node + `],"routing":{"rules":null}}`,
	} {
		out, _, err := xray.Splice([]byte(doc), testTproxy(), withRules(nil, []string{"example.org"}))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if _, rules := routingRules(t, out); len(rules) != 1 || rules[0].OutboundTag != "n" || !lan(rules[0]) {
			t.Errorf("%s: rules = %+v", name, rules)
		}
	}
}

// The UI reads the render through xrayview: the owner's rules change neither
// a balancer's role nor its matchers, nor xray's default outbound.
func TestTheBalancerViewDoesNotChange(t *testing.T) {
	in := providerFixture(t)
	plain, _, _ := xray.Splice(in, testTproxy(), routerOptions())
	with, _, err := xray.Splice(in, testTproxy(), withRules([]string{"sberbank.ru", "1.2.3.0/24"}, []string{"example.org"}))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := xrayview.Parse(plain)
	b, err := xrayview.Parse(with)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Balancers, b.Balancers) || a.Default.Tag != b.Default.Tag || len(a.Outbounds) != len(b.Outbounds) {
		t.Errorf("the balancer view changed:\n%+v\n%+v", a.Balancers, b.Balancers)
	}
}

// «+ сервис» (spec decision 4): a service goes into its list as the geo
// category itself; a site the owner typed into the other list is more
// specific and goes first — discord.com direct stays direct with Discord
// through the VPN.
func TestAServiceIsRenderedAsItsCategoryAfterTheOwnersSites(t *testing.T) {
	out, res, err := xray.Splice(providerFixture(t), testTproxy(), withRules(
		[]string{"discord.com", "geosite:category-gov-ru"},
		[]string{"geosite:discord", "example.org"}))
	if err != nil {
		t.Fatal(err)
	}
	_, rules := routingRules(t, out)
	rules = rules[:res.UserRules.Rules]
	idx := func(m string) int {
		for i, r := range rules {
			for _, d := range r.Domain {
				if d == m {
					return i
				}
			}
		}
		return -1
	}
	dc, svc, gov := idx("domain:discord.com"), idx("geosite:discord"), idx("geosite:category-gov-ru")
	if dc < 0 || svc < 0 || gov < 0 || dc > svc {
		t.Fatalf("rules %+v", rules)
	}
	if rules[svc].BalancerTag != "BL-MAIN" || rules[gov].OutboundTag != "DIRECT" || rules[dc].OutboundTag != "DIRECT" {
		t.Fatalf("targets: %+v", rules)
	}
}

// The other way round: Meta direct, instagram.com through the VPN — the site
// the owner typed still wins, though direct rules come first at a level.
func TestASiteThroughTheVPNWinsOverAServiceDirect(t *testing.T) {
	out, res, err := xray.Splice(providerFixture(t), testTproxy(), withRules([]string{"geosite:meta"}, []string{"instagram.com"}))
	if err != nil {
		t.Fatal(err)
	}
	_, rules := routingRules(t, out)
	rules = rules[:res.UserRules.Rules]
	if len(rules) != 2 || len(rules[0].Domain) != 1 || rules[0].Domain[0] != "domain:instagram.com" || rules[0].BalancerTag != "BL-MAIN" ||
		len(rules[1].Domain) != 1 || rules[1].Domain[0] != "geosite:meta" || rules[1].OutboundTag != "DIRECT" {
		t.Fatalf("rules %+v", rules)
	}
}
