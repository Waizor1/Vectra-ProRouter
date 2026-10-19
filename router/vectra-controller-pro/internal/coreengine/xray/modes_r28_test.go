package xray_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
)

// r28 (docs/superpowers/specs/2026-10-19-vctl-games-stable-ip.md): the pin
// is xray's balancer override, not the render; the games' own traffic rides
// it by class.

type r28Rule struct {
	InboundTag  []string        `json:"inboundTag"`
	Domain      []string        `json:"domain"`
	IP          []string        `json:"ip"`
	Network     string          `json:"network"`
	Port        json.RawMessage `json:"port"`
	Protocol    []string        `json:"protocol"`
	RuleTag     string          `json:"ruleTag"`
	BalancerTag string          `json:"balancerTag"`
	OutboundTag string          `json:"outboundTag"`
}

type r28Render struct {
	Routing struct {
		Rules     []r28Rule `json:"rules"`
		Balancers []struct {
			Tag         string   `json:"tag"`
			Selector    []string `json:"selector"`
			FallbackTag string   `json:"fallbackTag"`
			Strategy    struct {
				Type string `json:"type"`
			} `json:"strategy"`
		} `json:"balancers"`
	} `json:"routing"`
	BurstObservatory struct {
		SubjectSelector []string `json:"subjectSelector"`
	} `json:"burstObservatory"`
}

func r28Splice(t *testing.T, doc string, opts xray.SpliceOptions) (r28Render, xray.SpliceResult) {
	t.Helper()
	out, res, err := xray.Splice([]byte(doc), testTproxy(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var r r28Render
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	return r, res
}

func (r r28Rule) port() string {
	var s string
	if json.Unmarshal(r.Port, &s) == nil {
		return s
	}
	return string(r.Port)
}

// The fixture's BL-MAIN, as xray resolves its selector.
var mainMembers = []string{"bridge-ae5", "bridge-de5", "bridge-fin5", "bridge-fr5", "bridge-nl5", "bridge-pl5", "bridge-tr5", "bridge-us5"}

// No pin is needed to render the stable exit: its balancer holds every node
// of the main path, chooses among them while nothing is pinned, and falls
// back into the main path. Which one is pinned is xray's override, set at
// run time — a move is no render and no restart.
func TestTheStableExitRendersWithoutAPin(t *testing.T) {
	r, res := r28Splice(t, string(providerFixture(t)), xray.SpliceOptions{
		ServiceModes: map[string]string{"steam": "stable", "starcitizen": "stable"}})
	if !reflect.DeepEqual(res.Modes.Stable, []string{"steam", "starcitizen"}) || len(res.Modes.Skipped) != 0 {
		t.Fatalf("modes %+v", res.Modes)
	}
	if !reflect.DeepEqual(res.Modes.Members, mainMembers) {
		t.Fatalf("members %v", res.Modes.Members)
	}
	found := false
	for _, b := range r.Routing.Balancers {
		if b.Tag == xray.StableBalancerTag {
			found = true
			if !reflect.DeepEqual(b.Selector, mainMembers) || b.FallbackTag != "vctl-stable-back" || b.Strategy.Type != "leastPing" {
				t.Fatalf("stable balancer %+v", b)
			}
		}
	}
	if !found {
		t.Fatal("no stable balancer")
	}
	for _, m := range mainMembers {
		if !slices.Contains(r.BurstObservatory.SubjectSelector, m) {
			t.Fatalf("%s is not observed: %v", m, r.BurstObservatory.SubjectSelector)
		}
	}
}

// The pin is not in the render key: two pins, one render.
func TestThePinIsNotInTheRenderKey(t *testing.T) {
	a := xray.SpliceOptions{ServiceModes: map[string]string{"steam": "stable"}}
	b := xray.SpliceOptions{ServiceModes: map[string]string{"steam": "stable"}, StableMain: true}
	if a.Key() == (xray.SpliceOptions{}).Key() || a.Key() == b.Key() {
		t.Fatalf("keys %q %q", a.Key(), b.Key())
	}
}

// «Игровые соединения»: what the main path carries that is not the web
// (UDP but DNS, NTP and QUIC; TCP but 80 and 443) rides the stable exit —
// a copy in front of every rule of the main path, the rule's own matchers
// kept, for the LAN's inbound alone. BitTorrent stays where the rule sent
// it. Rules to other paths keep everything.
func TestGameplayRidesTheStableExitByClass(t *testing.T) {
	r, res := r28Splice(t, string(providerFixture(t)), xray.SpliceOptions{
		ServiceModes: map[string]string{"gameplay": "stable"}})
	if !slices.Contains(res.Modes.Stable, "gameplay") || len(res.Modes.Skipped) != 0 {
		t.Fatalf("modes %+v", res.Modes)
	}
	const udp, tcp = "1-52,54-122,124-442,444-65535", "1-79,81-442,444-65535"
	rules := r.Routing.Rules
	// The provider's catch-all and its two lists to BL-MAIN each get, just
	// in front: BitTorrent to BL-MAIN, then UDP and TCP to the stable exit.
	checked := 0
	for i, rule := range rules {
		if rule.BalancerTag != "BL-MAIN" || len(rule.InboundTag) > 0 || len(rule.Protocol) > 0 {
			continue
		}
		if i < 3 {
			t.Fatalf("main rule %d has no room for its copies", i)
		}
		bt, u, c := rules[i-3], rules[i-2], rules[i-1]
		if !reflect.DeepEqual(bt.Protocol, []string{"bittorrent"}) || bt.BalancerTag != "BL-MAIN" || !reflect.DeepEqual(bt.Domain, rule.Domain) {
			t.Fatalf("rule %d: the BitTorrent pass-through %+v", i, bt)
		}
		for _, cp := range []struct {
			r    r28Rule
			net  string
			port string
		}{{u, "udp", udp}, {c, "tcp", tcp}} {
			if cp.r.BalancerTag != xray.StableBalancerTag || cp.r.Network != cp.net || cp.r.port() != cp.port ||
				!reflect.DeepEqual(cp.r.InboundTag, []string{"tproxy-in"}) || !reflect.DeepEqual(cp.r.Domain, rule.Domain) || cp.r.RuleTag != "" {
				t.Fatalf("rule %d: the %s copy %+v", i, cp.net, cp.r)
			}
		}
		checked++
	}
	if checked != 3 {
		t.Fatalf("%d main rules copied, want 3 (the AI list, the sites list, the catch-all)", checked)
	}
	// The STAGE_MAIN rule is a provider stage (its own inbound), not the LAN:
	// no copy. Nothing to BL-RU, BL-TK, DIRECT or BLOCK is copied.
	for i, rule := range rules {
		if rule.BalancerTag != xray.StableBalancerTag || rule.Network == "" {
			continue
		}
		next := rules[i+1]
		if next.BalancerTag == xray.StableBalancerTag {
			next = rules[i+2]
		}
		if next.BalancerTag != "BL-MAIN" {
			t.Fatalf("a copy at %d stands before a rule to %s%s", i, next.BalancerTag, next.OutboundTag)
		}
	}
}

// On «Как основной VPN» the class has no rule: the render is the plain one.
func TestGameplayOnTheMainPathChangesNothing(t *testing.T) {
	plain, _, _ := spliceServicesDoc(t, string(providerFixture(t)), xray.SpliceOptions{})
	out, _, _ := spliceServicesDoc(t, string(providerFixture(t)), xray.SpliceOptions{
		ServiceModes: map[string]string{"gameplay": "main"}})
	if string(out) != string(plain) {
		t.Fatal("gameplay on the main path changed the render")
	}
}

// A rule of the main path with a network or ports of its own: the copy is
// their meet; an empty meet is no copy. A ruleTag is never copied (xray
// ≥ 26.9.8 refuses a duplicate). A rule for another inbound, or one whose
// ports do not read, gets nothing.
func TestCopiesMeetTheRulesOwnNetworkAndPorts(t *testing.T) {
	doc := strings.Replace(string(providerFixture(t)),
		`{"network":"tcp,udp","balancerTag":"BL-MAIN"}`,
		`{"domain":["domain:game.example"],"network":"tcp","port":"443,8000-8020","ruleTag":"game","balancerTag":"BL-MAIN"},`+
			`{"domain":["domain:web.example"],"network":"tcp","port":443,"balancerTag":"BL-MAIN"},`+
			`{"inboundTag":["socks-in"],"balancerTag":"BL-MAIN"},`+
			`{"domain":["domain:odd.example"],"port":"eighty","balancerTag":"BL-MAIN"},`+
			`{"network":"tcp,udp","balancerTag":"BL-MAIN"}`, 1)
	if doc == string(providerFixture(t)) {
		t.Fatal("the fixture's catch-all moved; update the test")
	}
	r, _ := r28Splice(t, doc, xray.SpliceOptions{ServiceModes: map[string]string{"gameplay": "stable"}})
	var game, web, socks, odd []r28Rule
	for _, rule := range r.Routing.Rules {
		switch {
		case slices.Contains(rule.Domain, "domain:game.example") && rule.BalancerTag == xray.StableBalancerTag:
			game = append(game, rule)
		case slices.Contains(rule.Domain, "domain:web.example") && rule.BalancerTag == xray.StableBalancerTag:
			web = append(web, rule)
		case slices.Contains(rule.InboundTag, "socks-in") && rule.BalancerTag == xray.StableBalancerTag:
			socks = append(socks, rule)
		case slices.Contains(rule.Domain, "domain:odd.example") && rule.BalancerTag == xray.StableBalancerTag:
			odd = append(odd, rule)
		}
	}
	if len(game) != 1 || game[0].Network != "tcp" || game[0].port() != "8000-8020" || game[0].RuleTag != "" {
		t.Fatalf("game copies %+v", game)
	}
	if len(web) != 0 || len(socks) != 0 || len(odd) != 0 {
		t.Fatalf("copies that must not be: web %+v socks %+v odd %+v", web, socks, odd)
	}
	tags := 0
	for _, rule := range r.Routing.Rules {
		if rule.RuleTag == "game" {
			tags++
		}
	}
	if tags != 1 {
		t.Fatalf("ruleTag game appears %d times", tags)
	}
}

// «Постоянный IP для всего VPN» with the class on: the catch-all goes to the
// stable exit, and the provider's explicit main rules keep their copies.
func TestGameplayWithStableMainKeepsTheExplicitCopies(t *testing.T) {
	r, res := r28Splice(t, string(providerFixture(t)), xray.SpliceOptions{
		ServiceModes: map[string]string{"gameplay": "stable"}, StableMain: true})
	if !res.Modes.Main {
		t.Fatalf("modes %+v", res.Modes)
	}
	copies := 0
	for _, rule := range r.Routing.Rules {
		if rule.BalancerTag == xray.StableBalancerTag && slices.Contains(rule.Domain, "domain:canva.com") {
			copies++
		}
	}
	if copies != 2 {
		t.Fatalf("%d copies of the sites list", copies)
	}
}

// No observatory: no stable exit at all (a dead pin would never be noticed),
// the class included.
func TestGameplayNeedsTheObservatory(t *testing.T) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(providerFixture(t), &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "burstObservatory")
	raw, _ := json.Marshal(doc)
	_, res := r28Splice(t, string(raw), xray.SpliceOptions{ServiceModes: map[string]string{"gameplay": "stable", "steam": "stable"}})
	if len(res.Modes.Stable) != 0 || len(res.Modes.Skipped) != 2 || !strings.Contains(res.Modes.Skipped[0], "observatory") {
		t.Fatalf("modes %+v", res.Modes)
	}
}
