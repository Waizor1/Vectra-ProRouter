package xray_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
)

// Russian sites by the kernel (spec decision 8, the owner 2026-09-30: «ру
// сервисы … все в директ отправляй, иначе роутер сдохнет»). The provider's
// Russian rules send geoip:ru straight out, but its domain rules come first:
// a blocked site whose address is in a Russian network is proxied by name.
// The router answers every name a rule proxies with a FakeDNS address, so
// such a connection reaches xray whatever the name's real address; the rest
// of geoip:ru can then leave by the kernel.
const fakeDoc = `{"dns":{"servers":["1.1.1.1"],"queryStrategy":"UseIPv4"},
 "outbounds":[
  {"tag":"bridge-de5","protocol":"vless","settings":{"vnext":[{"address":"ru11.example","port":40052,"users":[{"id":"u"}]}]}},
  {"tag":"bridge-by-tcp","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":40059,"users":[{"id":"u"}]}]}},
  {"tag":"bridge-ru-tcp","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.8","port":40051,"users":[{"id":"u"}]}]}},
  {"tag":"DIRECT","protocol":"freedom"},
  {"tag":"BLOCK","protocol":"blackhole"}],
 "routing":{"rules":[
   {"domain":["geosite:meta"],"network":"udp","outboundTag":"BLOCK"},
   {"domain":["geosite:tiktok","domain:byteimg.com"],"balancerTag":"BL-TK"},
   {"ip":["geoip:telegram","91.108.0.0/16"],"balancerTag":"BL-TK"},
   {"domain":["geosite:youtube"],"balancerTag":"BL-RU"},
   {"domain":["domain:gosuslugi.ru","domain:vk.com"],"outboundTag":"DIRECT"},
   {"domain":["domain:novayagazeta.ru","keyword:rutracker"],"balancerTag":"BL-MAIN"},
   {"domain":["domain:ru","geosite:category-ru"],"balancerTag":"BL-RU"},
   {"ip":["geoip:ru"],"balancerTag":"BL-RU"},
   {"ip":["geoip:private"],"outboundTag":"DIRECT"},
   {"network":"tcp,udp","balancerTag":"BL-MAIN"},
   {"domain":["after.the.catch.all"],"balancerTag":"BL-TK"}],
  "balancers":[{"tag":"BL-MAIN","selector":["bridge-de5"]},{"tag":"BL-TK","selector":["bridge-by-tcp"]},{"tag":"BL-RU","selector":["bridge-ru-tcp"]}]}}`

type fakeRender struct {
	DNS struct {
		Servers []json.RawMessage `json:"servers"`
	} `json:"dns"`
	FakeDNS  []map[string]any `json:"fakedns"`
	Inbounds []struct {
		Tag      string `json:"tag"`
		Sniffing struct {
			DestOverride []string `json:"destOverride"`
			RouteOnly    bool     `json:"routeOnly"`
		} `json:"sniffing"`
	} `json:"inbounds"`
}

func fakeSplice(t *testing.T, fake bool, rules xray.UserRules) ([]byte, fakeRender, xray.SpliceResult) {
	t.Helper()
	opts := xray.SpliceOptions{RussiaDirect: true, FakeDNS: fake, Rules: rules,
		DNS: &xray.DNSOptions{Listen: xray.DefaultDNSListen, DirectResolvers: xray.DefaultDirectResolvers}}
	out, res, err := xray.Splice([]byte(fakeDoc), testTproxy(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var r fakeRender
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	return out, r, res
}

func fakeServerDomains(t *testing.T, r fakeRender) []string {
	t.Helper()
	for _, s := range r.DNS.Servers {
		var o struct {
			Address      string   `json:"address"`
			Domains      []string `json:"domains"`
			SkipFallback bool     `json:"skipFallback"`
		}
		if json.Unmarshal(s, &o) == nil && o.Address == "fakedns" {
			// Only its own names: xray asks every server that does not skip
			// the fallback for a name no server lists, and a FakeDNS one
			// would hand every other name a fake address too (the stand,
			// 2026-09-30: plain.origin.stand got 198.18.214.67).
			if !o.SkipFallback {
				t.Errorf("the FakeDNS server answers names it does not list (no skipFallback)")
			}
			return o.Domains
		}
	}
	return nil
}

func TestTheRouterAnswersEveryProxiedNameWithAFakeAddress(t *testing.T) {
	out, r, res := fakeSplice(t, true, xray.UserRules{Proxy: []string{"chatgpt.com"}, Direct: []string{"sber.ru"}})
	got := fakeServerDomains(t, r)
	// Every name a rule sends anywhere but straight out, in order, up to the
	// rule that takes everything: the owner's «через VPN», Meta's UDP block,
	// TikTok, YouTube, the proxied Russian site and the keyword. Not the
	// direct rules, not the Russian rule the router sends direct, not what
	// no connection can reach after the catch-all.
	want := []string{"domain:chatgpt.com", "geosite:meta", "geosite:tiktok", "domain:byteimg.com", "geosite:youtube",
		"domain:novayagazeta.ru", "keyword:rutracker"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fakedns domains %v, want %v", got, want)
	}
	if len(r.FakeDNS) != 1 || r.FakeDNS[0]["ipPool"] != "198.18.0.0/16" {
		t.Fatalf("fakedns pool %v", r.FakeDNS)
	}
	sniff := r.Inbounds[0].Sniffing
	if !strings.Contains(strings.Join(sniff.DestOverride, ","), "fakedns") || !sniff.RouteOnly {
		t.Fatalf("tproxy sniffing %+v", sniff)
	}
	// The nodes' own names are asked for real, before the fake addresses.
	fi, ni := strings.Index(string(out), `"fakedns","domains"`), strings.Index(string(out), "tcp+local://")
	if fi < 0 || ni < 0 || ni > fi {
		t.Fatalf("node resolvers at %d, fakedns at %d", ni, fi)
	}
	if res.DNS.FakeDomains != len(want) {
		t.Fatalf("result says %d fake names", res.DNS.FakeDomains)
	}
}

func TestNoFakeAddressesUnlessAsked(t *testing.T) {
	out, r, _ := fakeSplice(t, false, xray.UserRules{})
	if fakeServerDomains(t, r) != nil || len(r.FakeDNS) != 0 || strings.Contains(string(out), `"fakedns"`) {
		t.Fatal("FakeDNS rendered without being asked")
	}
	if (xray.SpliceOptions{FakeDNS: true}).Key() == (xray.SpliceOptions{}).Key() {
		t.Fatal("the render key ignores FakeDNS")
	}
}

// With every proxied name answered by FakeDNS, the kernel may take the
// Russian addresses past the domain rules: what is left of geoip:ru after the
// proxied addresses (Telegram's) goes straight out.
func TestWithFakeAddressesTheRussianNetworksLeaveByTheKernel(t *testing.T) {
	out, _, _ := fakeSplice(t, true, xray.UserRules{})
	src, err := xray.DirectBypass(out, "tproxy-in")
	if err != nil {
		t.Fatal(err)
	}
	var geo []string
	for _, g := range src.GeoIP {
		geo = append(geo, g.Code)
	}
	var excl []string
	for _, g := range src.ExcludeGeoIP {
		excl = append(excl, g.Code)
	}
	for _, p := range src.ExcludePrefixes {
		excl = append(excl, p.String())
	}
	if !reflect.DeepEqual(geo, []string{"ru"}) || !reflect.DeepEqual(excl, []string{"telegram", "91.108.0.0/16"}) {
		t.Fatalf("direct %v minus %v (stop %d: %s)", geo, excl, src.Stop, src.StopReason)
	}
	// Without FakeDNS a proxied name could stand for a Russian address: the
	// scan stops at the first such rule, as before.
	plain, _, _ := fakeSplice(t, false, xray.UserRules{})
	if src, _ := xray.DirectBypass(plain, "tproxy-in"); !src.Empty() {
		t.Fatalf("direct without FakeDNS: %+v", src)
	}
}

// 1111, 2026-09-30: nnmclub.to sat in the provider's Russian rule beside
// domain:ru; the rule went direct whole and the name got its real address
// and the filter. Split, the bridge's part is proxied by name: FakeDNS
// answers it, xray sees the name and sends it through the Russian bridge.
func TestTheBridgesOwnSitesInAMixedRussianRuleGetFakeAnswers(t *testing.T) {
	doc := strings.Replace(fakeDoc, `{"domain":["domain:ru","geosite:category-ru"],"balancerTag":"BL-RU"}`,
		`{"domain":["domain:ru","geosite:category-ru","domain:nnmclub.to","keyword:kinozal"],"balancerTag":"BL-RU"}`, 1)
	if doc == fakeDoc {
		t.Fatal("precondition: the fixture's Russian rule was not found")
	}
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
	got := strings.Join(fakeServerDomains(t, r), " ")
	for _, want := range []string{"domain:nnmclub.to", "keyword:kinozal"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s gets no FakeDNS answer: %s", want, got)
		}
	}
	for _, not := range []string{"domain:ru", "geosite:category-ru"} {
		if strings.Contains(" "+got+" ", " "+not+" ") {
			t.Errorf("%s (direct) got a FakeDNS answer: %s", not, got)
		}
	}
}
