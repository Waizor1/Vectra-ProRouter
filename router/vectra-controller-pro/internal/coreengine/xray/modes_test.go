package xray_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
)

// The provider's shared rule (2026-10-17): the AI services and the games,
// one list to BL-MAIN (testdata/provider/entry-00.json, rule 9).

func modesRender(t *testing.T, opts xray.SpliceOptions) (svcRender, xray.SpliceResult) {
	t.Helper()
	_, r, res := spliceServicesDoc(t, string(providerFixture(t)), opts)
	return r, res
}

func rulesWithInbound(r svcRender, inbound string) []int {
	var out []int
	for i, rule := range r.Routing.Rules {
		if slices.Contains(rule.InboundTag, inbound) {
			out = append(out, i)
		}
	}
	return out
}

// «VPN · постоянный IP»: every stable service goes to one balancer — the
// main path's nodes, the pin set on it at run time — whose fallback leads
// back into the main path: a launcher and its CDN leave from one address; a
// dead pin falls back to the provider's path, never to nothing.
func TestStableGamesShareOnePinnedExit(t *testing.T) {
	r, res := modesRender(t, xray.SpliceOptions{
		ServiceModes: map[string]string{"steam": "stable", "starcitizen": "stable", "youtube": "main"},
	})
	if !reflect.DeepEqual(res.Modes.Stable, []string{"steam", "starcitizen"}) || !reflect.DeepEqual(res.Modes.Members, mainMembers) || len(res.Modes.Skipped) != 0 {
		t.Fatalf("modes result %+v", res.Modes)
	}
	var bal *struct {
		Tag      string   `json:"tag"`
		Selector []string `json:"selector"`
		Strategy struct {
			Type string `json:"type"`
		} `json:"strategy"`
		FallbackTag string `json:"fallbackTag"`
	}
	for i := range r.Routing.Balancers {
		if r.Routing.Balancers[i].Tag == xray.StableBalancerTag {
			bal = &r.Routing.Balancers[i]
		}
	}
	if bal == nil || !reflect.DeepEqual(bal.Selector, mainMembers) || bal.Strategy.Type != "leastPing" || bal.FallbackTag != "vctl-stable-back" {
		t.Fatalf("stable balancer %+v", bal)
	}
	steam := svcRulesTo(r, xray.StableBalancerTag)
	if len(steam) != 2 || !slices.Contains(steam[0], "domain:steampowered.com") || !slices.Contains(steam[1], "domain:robertsspaceindustries.com") {
		t.Fatalf("stable rules %v", steam)
	}
	for _, list := range steam {
		if slices.Contains(list, "domain:chatgpt.com") || slices.Contains(list, "domain:epicgames.com") {
			t.Fatalf("a game took another service's name: %v", list)
		}
	}
	back := rulesWithInbound(r, "vctl-stable-back")
	if len(back) != 1 || r.Routing.Rules[back[0]].BalancerTag != "BL-MAIN" {
		t.Fatalf("the stable fallback leads to %v", back)
	}
	hasLoop := false
	for _, o := range r.Outbounds {
		if o.Tag == "vctl-stable-back" && o.Protocol == "loopback" && o.Settings.InboundTag == "vctl-stable-back" {
			hasLoop = true
		}
	}
	if !hasLoop {
		t.Fatal("no loopback back into the main path")
	}
}

// «Напрямую»: the service's names leave by the provider's plain freedom,
// before the stable services and above the provider's shared rule.
func TestDirectGamesLeaveStraightBeforeStableOnes(t *testing.T) {
	r, res := modesRender(t, xray.SpliceOptions{
		ServiceModes: map[string]string{"steam": "direct", "starcitizen": "stable"},
	})
	if !reflect.DeepEqual(res.Modes.Direct, []string{"steam"}) {
		t.Fatalf("modes result %+v", res.Modes)
	}
	direct, stable := -1, -1
	for i, rule := range r.Routing.Rules {
		switch {
		case rule.OutboundTag == "DIRECT" && slices.Contains(rule.Domain, "domain:steampowered.com"):
			direct = i
		case rule.BalancerTag == xray.StableBalancerTag && stable < 0:
			stable = i
		}
	}
	shared := -1
	for i, rule := range r.Routing.Rules {
		if slices.Contains(rule.Domain, "domain:chatgpt.com") && len(rule.InboundTag) == 0 {
			shared = i
		}
	}
	if direct < 0 || stable < 0 || shared < 0 || !(direct < stable && stable < shared) {
		t.Fatalf("direct at %d, stable at %d, the provider's shared rule at %d", direct, stable, shared)
	}
}

// A balancer selector is a prefix: a main path that is one node whose tag
// begins another tag ("bridge-fr5", "bridge-fr5-old") would bring that one
// into the stable exit too — no member, no stable exit; alone, it is the
// one member.
func TestAMemberThatIsAPrefixIsLeftOut(t *testing.T) {
	one := strings.Replace(string(providerFixture(t)), `{"network":"tcp,udp","balancerTag":"BL-MAIN"}`, `{"network":"tcp,udp","outboundTag":"bridge-fr5"}`, 1)
	if one == string(providerFixture(t)) {
		t.Fatal("the fixture's catch-all moved; update the test")
	}
	_, _, res := spliceServicesDoc(t, one, xray.SpliceOptions{ServiceModes: map[string]string{"steam": "stable"}})
	if !reflect.DeepEqual(res.Modes.Members, []string{"bridge-fr5"}) {
		t.Fatalf("members %v", res.Modes.Members)
	}
	both := strings.Replace(one, `"outbounds":[`, `"outbounds":[{"tag":"bridge-fr5-old","protocol":"vless","settings":{"vnext":[{"address":"192.0.2.9","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000","encryption":"none"}]}]}},`, 1)
	_, _, res = spliceServicesDoc(t, both, xray.SpliceOptions{ServiceModes: map[string]string{"steam": "stable"}})
	if len(res.Modes.Members) != 0 || !strings.Contains(strings.Join(res.Modes.Skipped, ";"), "no node to pin") {
		t.Fatalf("modes %+v", res.Modes)
	}
}

// «Постоянный IP для всего VPN»: the provider's catch-all goes to the stable
// balancer; its old target is the stable fallback.
func TestStableMainSendsTheCatchAllThroughThePin(t *testing.T) {
	r, res := modesRender(t, xray.SpliceOptions{StableMain: true})
	if !res.Modes.Main {
		t.Fatalf("modes %+v", res.Modes)
	}
	var catchAll []string
	for _, rule := range r.Routing.Rules {
		if len(rule.Domain) == 0 && len(rule.IP) == 0 && len(rule.InboundTag) == 0 && rule.BalancerTag != "" {
			catchAll = append(catchAll, rule.BalancerTag)
		}
	}
	if !reflect.DeepEqual(catchAll, []string{xray.StableBalancerTag}) {
		t.Fatalf("catch-all rules go to %v", catchAll)
	}
	back := rulesWithInbound(r, "vctl-stable-back")
	if len(back) != 1 || r.Routing.Rules[back[0]].BalancerTag != "BL-MAIN" {
		t.Fatalf("the stable fallback leads to %v", back)
	}
}

// The render key follows the modes, and nothing else changes it.
func TestModesAreInTheRenderKey(t *testing.T) {
	base := xray.SpliceOptions{}
	if base.Key() != (xray.SpliceOptions{ServiceModes: map[string]string{"steam": "main"}}).Key() {
		t.Fatal("a main mode changed the key")
	}
	a := xray.SpliceOptions{ServiceModes: map[string]string{"steam": "stable"}}
	c := xray.SpliceOptions{ServiceModes: map[string]string{"steam": "direct"}}
	if a.Key() == c.Key() || a.Key() == base.Key() {
		t.Fatalf("keys %q %q", a.Key(), c.Key())
	}
}

// A service on «Напрямую» keeps its real DNS answers even though the
// provider proxies its names further down: the first rule that names it
// decides, as in xray.
func TestDirectNamesAreNotFakeDNSNames(t *testing.T) {
	doc := strings.Replace(fakeDoc, `{"domain":["domain:novayagazeta.ru","keyword:rutracker"],"balancerTag":"BL-MAIN"},`,
		`{"domain":["domain:novayagazeta.ru","keyword:rutracker"],"balancerTag":"BL-MAIN"},
   {"domain":["domain:chatgpt.com","domain:steampowered.com","domain:robertsspaceindustries.com"],"balancerTag":"BL-MAIN"},`, 1)
	opts := xray.SpliceOptions{RussiaDirect: true, FakeDNS: true,
		DNS:          &xray.DNSOptions{Listen: xray.DefaultDNSListen, DirectResolvers: xray.DefaultDirectResolvers},
		ServiceModes: map[string]string{"steam": "direct", "starcitizen": "stable"}}
	out, _, err := xray.Splice([]byte(doc), testTproxy(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var r fakeRender
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	fake := fakeServerDomains(t, r)
	if slices.Contains(fake, "domain:steampowered.com") {
		t.Fatalf("a direct service's name is a FakeDNS name: %v", fake)
	}
	for _, want := range []string{"domain:robertsspaceindustries.com", "domain:chatgpt.com"} {
		if !slices.Contains(fake, want) {
			t.Fatalf("%s is not a FakeDNS name: %v", want, fake)
		}
	}
}

// A direct rule with another condition (UDP only) does not take the name out
// of FakeDNS: its TCP still goes to the proxy rule after it.
func TestAConditionalDirectRuleKeepsTheNameFake(t *testing.T) {
	doc := strings.Replace(fakeDoc, `{"domain":["domain:novayagazeta.ru","keyword:rutracker"],"balancerTag":"BL-MAIN"},`,
		`{"domain":["domain:example.org"],"network":"udp","outboundTag":"DIRECT"},
   {"domain":["domain:novayagazeta.ru","keyword:rutracker","domain:example.org"],"balancerTag":"BL-MAIN"},`, 1)
	opts := xray.SpliceOptions{RussiaDirect: true, FakeDNS: true,
		DNS: &xray.DNSOptions{Listen: xray.DefaultDNSListen, DirectResolvers: xray.DefaultDirectResolvers}}
	out, _, err := xray.Splice([]byte(doc), testTproxy(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var r fakeRender
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	if fake := fakeServerDomains(t, r); !slices.Contains(fake, "domain:example.org") {
		t.Fatalf("a UDP-only direct rule took the name out of FakeDNS: %v", fake)
	}
}

// The stable exit's fallback must be a tunnel: a document whose main path
// goes straight out gets no stable exit — the stable services stay on the provider's
// routing rather than fall back to direct when the pin dies.
func TestNoPinWhenTheMainPathIsNotATunnel(t *testing.T) {
	doc := strings.Replace(string(providerFixture(t)), `{"network":"tcp,udp","balancerTag":"BL-MAIN"}`, `{"network":"tcp,udp","outboundTag":"DIRECT"}`, 1)
	if doc == string(providerFixture(t)) {
		t.Skip("the fixture's catch-all changed")
	}
	_, _, res := spliceServicesDoc(t, doc, xray.SpliceOptions{ServiceModes: map[string]string{"steam": "stable"}})
	if len(res.Modes.Stable) != 0 || !strings.Contains(strings.Join(res.Modes.Skipped, ";"), "not a tunnel") {
		t.Fatalf("modes %+v", res.Modes)
	}
}
