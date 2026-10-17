package xray_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/coreengine/xray"
)

// The «Авто» entry's shape as the provider served it on 2026-09-30, cut down:
// BL-MAIN one German node, BL-TK (TikTok, Telegram) one Belarusian node
// with a loopback stage behind it, BL-RU (YouTube) a Russian bridge.
const svcDoc = `{
 "outbounds":[
  {"tag":"sticky-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443,"users":[{"id":"u"}]}]}},
  {"tag":"bridge-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.6","port":40052,"users":[{"id":"u"}]}]}},
  {"tag":"sticky-by5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":443,"users":[{"id":"u"}]}]}},
  {"tag":"bridge-ru-tcp","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.8","port":40051,"users":[{"id":"u"}]}]}},
  {"tag":"whitelist-lv1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u"}]}]}},
  {"tag":"stage-tk","protocol":"loopback","settings":{"inboundTag":"STAGE_TK"}},
  {"tag":"DIRECT","protocol":"freedom"},
  {"tag":"BLOCK","protocol":"blackhole"}],
 "routing":{"rules":[
   {"inboundTag":["STAGE_TK"],"balancerTag":"BL-MAIN"},
   {"domain":["geosite:tiktok"],"balancerTag":"BL-TK"},
   {"domain":["geosite:telegram"],"balancerTag":"BL-TK"},
   {"ip":["geoip:telegram"],"balancerTag":"BL-TK"},
   {"domain":["geosite:youtube"],"balancerTag":"BL-RU"},
   {"network":"tcp,udp","balancerTag":"BL-MAIN"}],
  "balancers":[
   {"tag":"BL-MAIN","selector":["sticky-de5"],"strategy":{"type":"leastPing"},"fallbackTag":"DIRECT"},
   {"tag":"BL-TK","selector":["sticky-by5"],"strategy":{"type":"leastPing"},"fallbackTag":"stage-tk"},
   {"tag":"BL-RU","selector":["bridge-ru-tcp"],"strategy":{"type":"leastPing"},"fallbackTag":"DIRECT"}]},
 "burstObservatory":{"subjectSelector":["sticky-","bridge-ru"],"pingConfig":{"destination":"https://cp.cloudflare.com/generate_204","interval":"300s"}}
}`

type svcRender struct {
	Outbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
		Settings struct {
			InboundTag string `json:"inboundTag"`
		} `json:"settings"`
	} `json:"outbounds"`
	Routing struct {
		Rules []struct {
			InboundTag  []string `json:"inboundTag"`
			Domain      []string `json:"domain"`
			IP          []string `json:"ip"`
			BalancerTag string   `json:"balancerTag"`
			OutboundTag string   `json:"outboundTag"`
		} `json:"rules"`
		Balancers []struct {
			Tag      string   `json:"tag"`
			Selector []string `json:"selector"`
			Strategy struct {
				Type string `json:"type"`
			} `json:"strategy"`
			FallbackTag string `json:"fallbackTag"`
		} `json:"balancers"`
	} `json:"routing"`
	BurstObservatory struct {
		SubjectSelector []string `json:"subjectSelector"`
	} `json:"burstObservatory"`
}

func spliceServices(t *testing.T, opts xray.SpliceOptions) ([]byte, svcRender, xray.SpliceResult) {
	t.Helper()
	return spliceServicesDoc(t, svcDoc, opts)
}

func spliceServicesDoc(t *testing.T, doc string, opts xray.SpliceOptions) ([]byte, svcRender, xray.SpliceResult) {
	t.Helper()
	out, res, err := xray.Splice([]byte(doc), testTproxy(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var r svcRender
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	return out, r, res
}

func TestNoServiceChoiceLeavesTheEntryAlone(t *testing.T) {
	plain, _, _ := spliceServices(t, xray.SpliceOptions{})
	empty, _, _ := spliceServices(t, xray.SpliceOptions{Services: map[string]string{}})
	if string(plain) != string(empty) {
		t.Fatal("an empty choice changed the render")
	}
	if (xray.SpliceOptions{}).Key() != (xray.SpliceOptions{Services: map[string]string{}}).Key() {
		t.Fatal("an empty choice changed the render key")
	}
}

// TikTok through Germany: a balancer over the entry's German outbounds,
// backed by TikTok's own path in the entry (BL-TK) through a loopback; its
// rule for the LAN's traffic alone, so the provider's stages re-entering the
// routing never meet it again.
func TestAServiceCountryIsABalancerOverItsOutboundsBackedByTheEntrysOwnPath(t *testing.T) {
	_, r, res := spliceServices(t, xray.SpliceOptions{Services: map[string]string{"tiktok": "DE"}})
	var lb string
	for _, o := range r.Outbounds {
		if o.Tag == "vctl-svc-tiktok" {
			lb = o.Protocol + ":" + o.Settings.InboundTag
		}
	}
	if lb != "loopback:vctl-svc-tiktok" {
		t.Fatalf("loopback outbound %q", lb)
	}
	found := false
	for _, b := range r.Routing.Balancers {
		if b.Tag != "VCTL-SVC-TIKTOK" {
			continue
		}
		found = true
		if !reflect.DeepEqual(b.Selector, []string{"sticky-de5", "bridge-de5"}) || b.Strategy.Type != "leastPing" || b.FallbackTag != "vctl-svc-tiktok" {
			t.Fatalf("balancer %+v", b)
		}
	}
	if !found {
		t.Fatal("no VCTL-SVC-TIKTOK balancer")
	}
	rules := r.Routing.Rules
	if len(rules) < 3 {
		t.Fatalf("rules %+v", rules)
	}
	if !reflect.DeepEqual(rules[0].InboundTag, []string{"vctl-svc-tiktok"}) || rules[0].BalancerTag != "BL-TK" {
		t.Fatalf("loopback rule %+v", rules[0])
	}
	if !reflect.DeepEqual(rules[1].InboundTag, []string{"tproxy-in"}) || !reflect.DeepEqual(rules[1].Domain, []string{"geosite:tiktok"}) || rules[1].BalancerTag != "VCTL-SVC-TIKTOK" {
		t.Fatalf("service rule %+v", rules[1])
	}
	if !reflect.DeepEqual(rules[2].InboundTag, []string{"STAGE_TK"}) {
		t.Fatalf("the provider's rules no longer follow: %+v", rules[2])
	}
	if !reflect.DeepEqual(r.BurstObservatory.SubjectSelector, []string{"sticky-", "bridge-ru", "bridge-de5"}) {
		t.Fatalf("observatory %v", r.BurstObservatory.SubjectSelector)
	}
	if !reflect.DeepEqual(res.Services.Applied, []string{"tiktok=DE"}) || len(res.Services.Stale) != 0 {
		t.Fatalf("result %+v", res.Services)
	}
}

func TestTelegramGetsItsAddressesToo(t *testing.T) {
	_, r, _ := spliceServices(t, xray.SpliceOptions{Services: map[string]string{"telegram": "DE"}})
	var got []string
	for _, rule := range r.Routing.Rules {
		if rule.BalancerTag == "VCTL-SVC-TELEGRAM" {
			got = append(got, strings.Join(append(append([]string{}, rule.Domain...), rule.IP...), ","))
		}
	}
	if !reflect.DeepEqual(got, []string{"geosite:telegram", "geoip:telegram"}) {
		t.Fatalf("telegram rules %v", got)
	}
}

// The entry has no Dutch outbound: nothing is rendered for the choice, and it
// is said to be stale — the service stays on its own path.
func TestACountryTheEntryLacksIsNotRenderedAndSaidStale(t *testing.T) {
	out, _, res := spliceServices(t, xray.SpliceOptions{Services: map[string]string{"tiktok": "NL", "youtube": ""}})
	if strings.Contains(string(out), "VCTL-SVC") || strings.Contains(string(out), "vctl-svc") {
		t.Fatal("rendered a country the entry does not have")
	}
	if !reflect.DeepEqual(res.Services.Stale, []string{"tiktok"}) || len(res.Services.Applied) != 0 {
		t.Fatalf("result %+v", res.Services)
	}
}

// The owner's own sites come first: a TikTok domain they send direct stays
// direct whatever TikTok's country.
func TestTheOwnersSitesStayAboveAServiceChoice(t *testing.T) {
	_, r, _ := spliceServices(t, xray.SpliceOptions{
		Services: map[string]string{"tiktok": "DE"},
		Rules:    xray.UserRules{Direct: []string{"tiktok.com"}},
	})
	own, svc := -1, -1
	for i, rule := range r.Routing.Rules {
		if own < 0 && len(rule.Domain) > 0 && strings.Contains(strings.Join(rule.Domain, ","), "tiktok.com") && rule.BalancerTag == "" {
			own = i
		}
		if rule.BalancerTag == "VCTL-SVC-TIKTOK" {
			svc = i
		}
	}
	if own < 0 || svc < 0 || own > svc {
		t.Fatalf("own site at %d, service at %d", own, svc)
	}
}

func TestTheRenderKeyFollowsTheChoice(t *testing.T) {
	a := xray.SpliceOptions{Services: map[string]string{"tiktok": "DE"}}.Key()
	b := xray.SpliceOptions{Services: map[string]string{"tiktok": "BY"}}.Key()
	c := xray.SpliceOptions{Services: map[string]string{"tiktok": "DE", "youtube": ""}}.Key()
	if a == b || a == (xray.SpliceOptions{}).Key() || a != c {
		t.Fatalf("keys %q %q %q", a, b, c)
	}
}

// What the UI offers: the countries the entry's outbounds name (a whitelist
// level is no country), and each service's own country where its path is one.
func TestServiceCountriesComeFromTheEntry(t *testing.T) {
	sc := xray.ServiceCountries([]byte(svcDoc))
	if !reflect.DeepEqual(sc.Countries, []string{"BY", "DE", "RU"}) {
		t.Fatalf("countries %v", sc.Countries)
	}
	want := map[string]string{"tiktok": "BY", "telegram": "BY", "youtube": "RU"}
	if !reflect.DeepEqual(sc.Defaults, want) {
		t.Fatalf("defaults %v", sc.Defaults)
	}
}

// svcDocWith is svcDoc with one rule replaced (the old rule must be there).
func svcDocWith(t *testing.T, old, new string) string {
	t.Helper()
	if !strings.Contains(svcDoc, old) {
		t.Fatalf("svcDoc has no %s", old)
	}
	return strings.Replace(svcDoc, old, new, 1)
}

func svcRulesTo(r svcRender, balancer string) [][]string {
	var got [][]string
	for _, rule := range r.Routing.Rules {
		if rule.BalancerTag == balancer {
			got = append(got, append(append([]string{}, rule.Domain...), rule.IP...))
		}
	}
	return got
}

// The provider names a service by more than its geosite category (the «Авто»
// entry's TikTok rule lists byteimg.com, tiktokrow-cdn.com… beside it, which
// the router's geo file does not carry): a chosen country takes all of it, or
// part of the app would still leave through the default country.
func TestTheOverlayMatchesWhatTheEntryCallsTheService(t *testing.T) {
	doc := svcDocWith(t, `{"domain":["geosite:tiktok"],"balancerTag":"BL-TK"}`,
		`{"domain":["geosite:tiktok","domain:byteimg.com","full:p16-tiktokcdn-com.akamaized.net"],"balancerTag":"BL-TK"}`)
	doc = strings.Replace(doc, `{"ip":["geoip:telegram"],"balancerTag":"BL-TK"}`,
		`{"ip":["geoip:telegram","91.108.0.0/16","2001:b28:f23c::/48"],"balancerTag":"BL-TK"}`, 1)
	_, r, res := spliceServicesDoc(t, doc, xray.SpliceOptions{Services: map[string]string{"tiktok": "DE", "telegram": "DE"}})
	if got, want := svcRulesTo(r, "VCTL-SVC-TIKTOK"), [][]string{{"geosite:tiktok", "domain:byteimg.com", "full:p16-tiktokcdn-com.akamaized.net"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tiktok overlay matches %v, want %v", got, want)
	}
	if got, want := svcRulesTo(r, "VCTL-SVC-TELEGRAM"), [][]string{{"geosite:telegram"}, {"geoip:telegram", "91.108.0.0/16", "2001:b28:f23c::/48"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("telegram overlay matches %v, want %v", got, want)
	}
	if !reflect.DeepEqual(res.Services.Applied, []string{"tiktok=DE", "telegram=DE"}) {
		t.Fatalf("result %+v", res.Services)
	}
}

// A rule with a condition beside the service's name ("YouTube over UDP →
// BLOCK") is not where the service goes, and a blackhole is no path: a dead
// chosen country must fall back to the service's own path, never to nothing.
func TestAConditionedOrBlockingRuleIsNotTheServicesOwnPath(t *testing.T) {
	yt := `{"domain":["geosite:youtube"],"balancerTag":"BL-RU"}`
	doc := svcDocWith(t, yt, `{"domain":["geosite:youtube"],"network":"udp","outboundTag":"BLOCK"},`+yt)
	_, r, _ := spliceServicesDoc(t, doc, xray.SpliceOptions{Services: map[string]string{"youtube": "DE"}})
	fb := ""
	for _, b := range r.Routing.Balancers {
		if b.Tag == "VCTL-SVC-YOUTUBE" {
			fb = b.FallbackTag
		}
	}
	back := ""
	for _, rule := range r.Routing.Rules {
		if reflect.DeepEqual(rule.InboundTag, []string{"vctl-svc-youtube"}) {
			back = rule.BalancerTag + rule.OutboundTag
		}
	}
	if fb != "vctl-svc-youtube" || back != "BL-RU" {
		t.Fatalf("YouTube's overlay falls back to %q, then %q; want its own path BL-RU", fb, back)
	}
	if got := xray.ServiceCountries([]byte(doc)).Defaults["youtube"]; got != "RU" {
		t.Fatalf("YouTube's own country %q, want RU", got)
	}

	blocked := svcDocWith(t, yt, `{"domain":["geosite:youtube"],"outboundTag":"BLOCK"}`)
	out, _, res := spliceServicesDoc(t, blocked, xray.SpliceOptions{Services: map[string]string{"youtube": "DE"}})
	if strings.Contains(string(out), "VCTL-SVC-YOUTUBE") || !reflect.DeepEqual(res.Services.Stale, []string{"youtube"}) {
		t.Fatalf("a blocked service got an overlay (stale %v)", res.Services.Stale)
	}
	if xray.ServiceCountries([]byte(blocked)).Offered["youtube"] {
		t.Fatal("a service the entry blocks is offered a country")
	}
}

// An entry with no rule of its own for a service has no path to fall back
// to: the service is not offered, and a choice made earlier is stale.
func TestAServiceTheEntryDoesNotRouteIsNotOffered(t *testing.T) {
	doc := svcDocWith(t, `{"domain":["geosite:tiktok"],"balancerTag":"BL-TK"},`, ``)
	sc := xray.ServiceCountries([]byte(doc))
	if sc.Offered["tiktok"] || !sc.Offered["telegram"] || !sc.Offered["youtube"] {
		t.Fatalf("offered %v", sc.Offered)
	}
	_, _, res := spliceServicesDoc(t, doc, xray.SpliceOptions{Services: map[string]string{"tiktok": "DE"}})
	if !reflect.DeepEqual(res.Services.Stale, []string{"tiktok"}) {
		t.Fatalf("result %+v", res.Services)
	}
}

// The provider's AI rule as every location carried it on 2026-10-17: AI
// services and games in one list to BL-MAIN. A country for «Нейросети» takes
// the AI names; the games keep the provider's rule — BL-MAIN — untouched.
const providerAIAndGamesRule = `{"domain":["domain:chatgpt.com","domain:openai.com","domain:oaistatic.com","domain:oaiusercontent.com","domain:claude.ai","domain:anthropic.com","domain:claudeusercontent.com","domain:perplexity.ai","domain:x.ai","domain:grok.com","domain:gemini.google.com","domain:bard.google.com","domain:makersuite.google.com","domain:aistudio.google.com","domain:generativelanguage.googleapis.com","domain:labs.google.com","domain:ai.google.dev","domain:deepmind.com","domain:deepmind.google","domain:steampowered.com","domain:steamcommunity.com","domain:steamstatic.com","domain:steamcontent.com","domain:steamusercontent.com","domain:steamserver.net","domain:epicgames.com","domain:fortnite.com","domain:unrealengine.com","domain:easyanticheat.net","domain:easy.ac","domain:riotgames.com","domain:riotcdn.net","domain:leagueoflegends.com","domain:playvalorant.com","domain:battle.net","domain:blizzard.com","domain:blizzardcdn.com","domain:robertsspaceindustries.com","domain:cloudimperiumgames.com","domain:rsilauncher.com","domain:spectrum.chat"],"balancerTag":"BL-MAIN"}`

func TestTheAIOverlayLeavesTheProvidersGamesOnTheirPath(t *testing.T) {
	doc := svcDocWith(t, `{"domain":["geosite:youtube"],"balancerTag":"BL-RU"},`,
		providerAIAndGamesRule+`,{"domain":["geosite:youtube"],"balancerTag":"BL-RU"},`)
	_, r, res := spliceServicesDoc(t, doc, xray.SpliceOptions{Services: map[string]string{"ai": "DE"}})
	if !reflect.DeepEqual(res.Services.Applied, []string{"ai=DE"}) {
		t.Fatalf("result %+v", res.Services)
	}
	overlay := svcRulesTo(r, "VCTL-SVC-AI")
	if len(overlay) != 1 {
		t.Fatalf("AI overlay rules %v", overlay)
	}
	names := strings.Join(overlay[0], " ")
	for _, game := range []string{"steam", "epicgames", "fortnite", "riot", "valorant", "battle.net", "blizzard", "robertsspace", "cloudimperium", "rsilauncher", "spectrum.chat", "easyanticheat", "easy.ac", "unrealengine", "leagueoflegends"} {
		if strings.Contains(names, game) {
			t.Fatalf("%s goes through the AI country: %s", game, names)
		}
	}
	for _, ai := range []string{"domain:chatgpt.com", "domain:claude.ai", "domain:gemini.google.com", "domain:labs.google.com", "domain:deepmind.google", "domain:x.ai"} {
		if !strings.Contains(" "+names+" ", " "+ai+" ") {
			t.Fatalf("%s missing from the AI overlay: %s", ai, names)
		}
	}
	// The provider's own rule stays whole, so the games still reach BL-MAIN.
	main := svcRulesTo(r, "BL-MAIN")
	found := false
	for _, m := range main {
		if strings.Contains(strings.Join(m, " "), "domain:robertsspaceindustries.com") && strings.Contains(strings.Join(m, " "), "domain:chatgpt.com") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the provider's rule changed: %v", main)
	}
}

// What the overlay keeps is the catalog's: a catalog that trusts the
// provider's AI rule takes it whole again, one that adds a name adds it.
func TestTheCatalogDecidesWhatTheAIOverlayTakes(t *testing.T) {
	t.Cleanup(func() { xray.UseCatalog(nil) })
	doc := svcDocWith(t, `{"domain":["geosite:youtube"],"balancerTag":"BL-RU"},`,
		providerAIAndGamesRule+`,{"domain":["geosite:youtube"],"balancerTag":"BL-RU"},`)
	cat, err := catalog.Parse([]byte(`{"schema":1,"serial":9999999999,"services":{"ai":{"names":["domain:example-ai.test"]}}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	xray.UseCatalog(cat)
	_, r, _ := spliceServicesDoc(t, doc, xray.SpliceOptions{Services: map[string]string{"ai": "DE"}})
	names := strings.Join(svcRulesTo(r, "VCTL-SVC-AI")[0], " ")
	if !strings.Contains(names, "domain:steampowered.com") || !strings.HasSuffix(names, "domain:example-ai.test") {
		t.Fatalf("overlay %s", names)
	}
}

// The catalog's sites come after the owner's: a site the owner lists either
// way stays the owner's, the rest go where the catalog says.
func TestTheCatalogsSitesComeAfterTheOwnersAndNeverOverrideThem(t *testing.T) {
	t.Cleanup(func() { xray.UseCatalog(nil) })
	cat, err := catalog.Parse([]byte(`{"schema":1,"serial":9999999999,"sites":{"direct":["example.ru","shop.example"],"proxy":["steampowered.com"]}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	xray.UseCatalog(cat)
	_, r, res := spliceServicesDoc(t, svcDoc, xray.SpliceOptions{Rules: xray.UserRules{Proxy: []string{"example.ru"}}})
	if res.UserRules.Proxy != 1 || res.UserRules.FleetDirect != 1 || res.UserRules.FleetProxy != 1 {
		t.Fatalf("result %+v", res.UserRules)
	}
	var lan [][]string
	for _, rule := range r.Routing.Rules {
		if len(rule.InboundTag) == 1 && rule.InboundTag[0] == "tproxy-in" {
			lan = append(lan, append(append([]string{rule.OutboundTag + rule.BalancerTag}, rule.Domain...), rule.IP...))
		}
	}
	want := [][]string{{"BL-MAIN", "domain:example.ru"}, {"DIRECT", "domain:shop.example"}, {"BL-MAIN", "domain:steampowered.com"}}
	if !reflect.DeepEqual(lan, want) {
		t.Fatalf("LAN rules %v, want %v", lan, want)
	}
	// Without the owner's sites the catalog's still render.
	_, _, res = spliceServicesDoc(t, svcDoc, xray.SpliceOptions{})
	if res.UserRules.FleetDirect != 2 || res.UserRules.FleetProxy != 1 {
		t.Fatalf("catalog only: %+v", res.UserRules)
	}
}

// The catalog is one for every router: a geosite name the router's geo file
// lacks is left out of the render (xray would refuse it), and so is one
// while the file's categories are not known.
func TestACatalogGeositeTheRoutersFileLacksIsLeftOut(t *testing.T) {
	t.Cleanup(func() { xray.UseGeosites(nil) })
	doc := svcDocWith(t, `{"domain":["geosite:youtube"],"balancerTag":"BL-RU"},`,
		providerAIAndGamesRule+`,{"domain":["geosite:youtube"],"balancerTag":"BL-RU"},`)
	overlay := func() string {
		_, r, _ := spliceServicesDoc(t, doc, xray.SpliceOptions{Services: map[string]string{"ai": "DE"}})
		return strings.Join(svcRulesTo(r, "VCTL-SVC-AI")[0], " ")
	}
	if strings.Contains(overlay(), "geosite:google-ai") {
		t.Fatal("not known: the catalog's geosite is left out")
	}
	xray.UseGeosites([]string{"YOUTUBE", "Google-AI"})
	if !strings.Contains(overlay(), "geosite:google-ai") {
		t.Fatal("the file has it: kept")
	}
	xray.UseGeosites([]string{"youtube"})
	if strings.Contains(overlay(), "geosite:google-ai") {
		t.Fatal("the file lacks it: left out")
	}
}
