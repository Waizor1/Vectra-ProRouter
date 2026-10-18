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

// «VPN · постоянный IP»: every stable service goes to one balancer whose
// only member is the stable exit, and whose fallback leads back into the
// main path — a launcher and its CDN leave from one address; a dead pin
// falls back to the provider's path, never to nothing.
func TestStableGamesShareOnePinnedExit(t *testing.T) {
	r, res := modesRender(t, xray.SpliceOptions{
		ServiceModes: map[string]string{"steam": "stable", "starcitizen": "stable", "youtube": "main"},
		StablePin:    "bridge-fr5",
	})
	if !reflect.DeepEqual(res.Modes.Stable, []string{"steam", "starcitizen"}) || res.Modes.Pin != "bridge-fr5" || len(res.Modes.Skipped) != 0 {
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
	if bal == nil || !reflect.DeepEqual(bal.Selector, []string{"bridge-fr5"}) || bal.Strategy.Type != "leastPing" || bal.FallbackTag != "vctl-stable-back" {
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
		StablePin:    "bridge-fr5",
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

// No stable exit yet: the stable services ride the provider's routing — the
// render is the one without modes.
func TestNoPinLeavesStableServicesToTheProvider(t *testing.T) {
	plain, _, _ := spliceServicesDoc(t, string(providerFixture(t)), xray.SpliceOptions{})
	out, _, res := spliceServicesDoc(t, string(providerFixture(t)), xray.SpliceOptions{
		ServiceModes: map[string]string{"steam": "stable"}})
	if string(out) != string(plain) {
		t.Fatal("a stable service without a pin changed the render")
	}
	if len(res.Modes.Skipped) != 1 || !strings.Contains(res.Modes.Skipped[0], "no stable exit") {
		t.Fatalf("skipped %v", res.Modes.Skipped)
	}
}

// A balancer selector is a prefix: a pin that is a prefix of another tag
// would pick that one too — refused, the services stay on the provider's
// routing.
func TestAPinThatIsAPrefixIsRefused(t *testing.T) {
	_, res := modesRender(t, xray.SpliceOptions{
		ServiceModes: map[string]string{"steam": "stable"}, StablePin: "whitelist-lv3"})
	if len(res.Modes.Stable) != 0 || len(res.Modes.Skipped) != 1 || !strings.Contains(res.Modes.Skipped[0], "prefix") {
		t.Fatalf("modes %+v", res.Modes)
	}
	_, res = modesRender(t, xray.SpliceOptions{
		ServiceModes: map[string]string{"steam": "stable"}, StablePin: "no-such-node"})
	if len(res.Modes.Stable) != 0 || !strings.Contains(strings.Join(res.Modes.Skipped, ";"), "not in this document") {
		t.Fatalf("modes %+v", res.Modes)
	}
}

// «Постоянный IP для всего VPN»: the provider's catch-all goes to the stable
// balancer; its old target is the stable fallback.
func TestStableMainSendsTheCatchAllThroughThePin(t *testing.T) {
	r, res := modesRender(t, xray.SpliceOptions{StablePin: "bridge-fr5", StableMain: true})
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
	// Without a pin nothing changes.
	plain, _, _ := spliceServicesDoc(t, string(providerFixture(t)), xray.SpliceOptions{})
	out, _, _ := spliceServicesDoc(t, string(providerFixture(t)), xray.SpliceOptions{StableMain: true})
	if string(out) != string(plain) {
		t.Fatal("StableMain without a pin changed the render")
	}
}

// The render key follows the modes and the pin, and nothing else changes it.
func TestModesAreInTheRenderKey(t *testing.T) {
	base := xray.SpliceOptions{}
	if base.Key() != (xray.SpliceOptions{ServiceModes: map[string]string{"steam": "main"}}).Key() {
		t.Fatal("a main mode changed the key")
	}
	a := xray.SpliceOptions{ServiceModes: map[string]string{"steam": "stable"}, StablePin: "bridge-fr5"}
	b := xray.SpliceOptions{ServiceModes: map[string]string{"steam": "stable"}, StablePin: "bridge-nl5"}
	c := xray.SpliceOptions{ServiceModes: map[string]string{"steam": "direct"}}
	if a.Key() == b.Key() || a.Key() == c.Key() || a.Key() == base.Key() {
		t.Fatalf("keys %q %q %q", a.Key(), b.Key(), c.Key())
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
		ServiceModes: map[string]string{"steam": "direct", "starcitizen": "stable"}, StablePin: "bridge-de5"}
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
