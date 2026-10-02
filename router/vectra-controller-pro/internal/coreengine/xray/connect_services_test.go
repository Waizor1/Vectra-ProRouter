package xray_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"vectra-controller-pro/internal/coreengine/xray"
)

func TestConnectExactServiceGraph(t *testing.T) {
	original := []byte(svcDoc)
	opts := xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{"tiktok": json.RawMessage(svcDoc)}}
	a, r, res := spliceServices(t, opts)
	b, _, _ := spliceServices(t, opts)
	if string(a) != string(b) {
		t.Fatal("replay changed graph")
	}
	if string(original) != svcDoc {
		t.Fatal("provider mutated")
	}
	if !reflect.DeepEqual(res.Services.Applied, []string{"tiktok=entry"}) {
		t.Fatal(res.Services)
	}
	seen := false
	for _, rule := range r.Routing.Rules {
		if rule.BalancerTag == "vctl-connect-tiktok-BL-TK" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("selected entry exact path absent")
	}
	loop := false
	for _, out := range r.Outbounds {
		if out.Tag == "vctl-connect-tiktok-stage-tk" {
			loop = out.Settings.InboundTag == "vctl-connect-tiktok-STAGE_TK"
		}
	}
	if !loop {
		t.Fatal("loopback graph not namespaced")
	}
	for _, bal := range r.Routing.Balancers {
		if strings.HasPrefix(bal.Tag, "vctl-connect-") {
			for _, sel := range bal.Selector {
				if !strings.HasPrefix(sel, "vctl-connect-tiktok-") {
					t.Fatal("unscoped selector")
				}
			}
		}
	}
	if opts.Key() == (xray.SpliceOptions{}).Key() {
		t.Fatal("render key misses overlay")
	}
}
func TestConnectEntryGraphRefusesUnsafeReferences(t *testing.T) {
	for name, doc := range map[string]string{
		"unknown_service":   svcDoc,
		"missing_reference": strings.Replace(svcDoc, `"fallbackTag":"DIRECT"`, `"fallbackTag":"MISSING"`, 1),
		"proxy_reference":   strings.Replace(svcDoc, `"tag":"sticky-de5","protocol"`, `"tag":"sticky-de5","proxySettings":{"tag":"MISSING"},"protocol"`, 1),
		"no_observatory":    strings.Replace(svcDoc, `"burstObservatory"`, `"unusedObservatory"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			id := "tiktok"
			if name == "unknown_service" {
				id = "shell"
			}
			base := svcDoc
			if name == "no_observatory" {
				base = doc
			}
			_, _, err := xray.Splice([]byte(base), testTproxy(), xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{id: json.RawMessage(doc)}})
			if err == nil {
				t.Fatal("unsafe graph accepted")
			}
		})
	}
	collision := strings.Replace(svcDoc, `"selector":["sticky-de5"]`, `"selector":[""]`, 1)
	if _, _, err := xray.Splice([]byte(collision), testTproxy(), xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{"tiktok": json.RawMessage(svcDoc)}}); err == nil {
		t.Fatal("base selector captured imported outbounds")
	}
}

func TestConnectServiceGraphDuplicateCollisionAndBounds(t *testing.T) {
	cases := map[string]string{
		"duplicate_outbound":        strings.Replace(svcDoc, `"tag":"bridge-de5"`, `"tag":"sticky-de5"`, 1),
		"duplicate_balancer":        strings.Replace(svcDoc, `"tag":"BL-TK"`, `"tag":"BL-MAIN"`, 1),
		"balancer_outbound_overlap": strings.Replace(svcDoc, `"tag":"BL-MAIN"`, `"tag":"sticky-de5"`, 1),
		"oversized":                 svcDoc + strings.Repeat(" ", 1<<20),
		"negated_inbound":           strings.Replace(svcDoc, `"inboundTag":["STAGE_TK"]`, `"inboundTag":["!STAGE_TK"]`, 1),
		"unknown_dialer":            strings.Replace(svcDoc, `"tag":"sticky-de5","protocol"`, `"tag":"sticky-de5","streamSettings":{"sockopt":{"dialerProxy":"MISSING"}},"protocol"`, 1),
	}
	var excessive map[string]any
	if err := json.Unmarshal([]byte(svcDoc), &excessive); err != nil {
		t.Fatal(err)
	}
	outbounds := excessive["outbounds"].([]any)
	for i := 0; i < 257; i++ {
		outbounds = append(outbounds, map[string]any{"tag": fmt.Sprintf("extra%d", i), "protocol": "freedom"})
	}
	excessive["outbounds"] = outbounds
	raw, _ := json.Marshal(excessive)
	cases["graph_bound"] = string(raw)
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := xray.Splice([]byte(svcDoc), testTproxy(), xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{"tiktok": json.RawMessage(doc)}}); err == nil {
				t.Fatal("unsafe/oversized graph accepted")
			}
		})
	}
	base := strings.Replace(svcDoc, `"tag":"DIRECT"`, `"tag":"vctl-connect-tiktok-sticky-de5"`, 1)
	if _, _, err := xray.Splice([]byte(base), testTproxy(), xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{"tiktok": json.RawMessage(svcDoc)}}); err == nil {
		t.Fatal("tag collision accepted")
	}
}

func TestConnectDomainsRenderFullBound(t *testing.T) {
	var domains []string
	for i := 0; i < 299; i++ {
		domains = append(domains, fmt.Sprintf("site%d.example", i))
	}
	domains = append(domains, "_service.example")
	_, _, res := spliceServices(t, xray.SpliceOptions{Rules: xray.UserRules{Connect: true, Direct: domains}})
	if res.UserRules.Direct != 300 || res.UserRules.Dropped != 0 {
		t.Fatalf("truncated Connect rules: %+v", res.UserRules)
	}
}

func TestConnectImportedGraphCollisions(t *testing.T) {
	for name, base := range map[string]string{
		"balancer":        strings.Replace(svcDoc, `"tag":"BL-RU"`, `"tag":"vctl-connect-tiktok-BL-TK"`, 1),
		"loopbackInbound": strings.Replace(svcDoc, `"inboundTag":["STAGE_TK"]`, `"inboundTag":["vctl-connect-tiktok-STAGE_TK"]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := xray.Splice([]byte(base), testTproxy(), xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{"tiktok": json.RawMessage(svcDoc)}}); err == nil {
				t.Fatal("imported graph collision accepted")
			}
		})
	}
}

func TestConnectBalancerDefaultStaysSelectedEntry(t *testing.T) {
	selected := strings.Replace(svcDoc, `,"fallbackTag":"stage-tk"`, "", 1)
	_, r, _ := spliceServices(t, xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{"tiktok": json.RawMessage(selected)}})
	for _, bal := range r.Routing.Balancers {
		if bal.Tag == "vctl-connect-tiktok-BL-TK" {
			if bal.FallbackTag != "vctl-connect-tiktok-sticky-de5" {
				t.Fatal("fallback escaped selected entry")
			}
			return
		}
	}
	t.Fatal("selected balancer missing")
}

// A one-country location (the provider's «🇩🇪 Германия») has no rule of its
// own for YouTube, TikTok or Telegram: everything that is not Russian goes
// down its main path. Choosing it for a service in the Vectra app means that
// path (1111, 2026-10-02: every choice was refused service_path_unavailable).
const oneCountryEntry = `{
 "outbounds":[
  {"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u"}]}]}},
  {"tag":"DIRECT","protocol":"freedom"},
  {"tag":"BLOCK","protocol":"blackhole"}
 ],
 "routing":{"rules":[
  {"domain":["geosite:category-ru"],"outboundTag":"DIRECT"},
  {"network":"tcp,udp","outboundTag":"de-1"}
 ]}
}`

func TestConnectServiceTakesTheMainPathOfALocationWithoutItsOwnRule(t *testing.T) {
	noCatchAll := strings.Replace(oneCountryEntry, `,
  {"network":"tcp,udp","outboundTag":"de-1"}`, "", 1)
	for name, entry := range map[string]string{"catch-all rule": oneCountryEntry, "default outbound": noCatchAll} {
		t.Run(name, func(t *testing.T) {
			for _, id := range []string{"youtube", "telegram"} {
				if err := xray.ValidateConnectServiceEntry([]byte(entry), id); err != nil {
					t.Fatalf("%s refused: %v", id, err)
				}
			}
			_, r, res := spliceServices(t, xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{
				"youtube": json.RawMessage(entry), "telegram": json.RawMessage(entry)}})
			if len(res.Services.Applied) != 2 {
				t.Fatalf("applied %v", res.Services.Applied)
			}
			got := map[string]bool{}
			for _, rule := range r.Routing.Rules {
				if !strings.HasPrefix(rule.OutboundTag, "vctl-connect-") {
					continue
				}
				for _, d := range append(rule.Domain, rule.IP...) {
					got[d+"→"+rule.OutboundTag] = true
				}
			}
			for _, want := range []string{"geosite:youtube→vctl-connect-youtube-de-1", "geosite:telegram→vctl-connect-telegram-de-1", "geoip:telegram→vctl-connect-telegram-de-1"} {
				if !got[want] {
					t.Fatalf("missing %s in %v", want, got)
				}
			}
		})
	}
}

func TestConnectServiceTakesABalancedMainPathAndAnArrayNetwork(t *testing.T) {
	for name, entry := range map[string]string{
		"balancer with a tunnel fallback": strings.Replace(strings.Replace(oneCountryEntry, `{"network":"tcp,udp","outboundTag":"de-1"}`, `{"network":"tcp,udp","balancerTag":"BL"}`, 1), `"routing":{`, `"routing":{"balancers":[{"tag":"BL","selector":["de-"],"fallbackTag":"de-1"}],`, 1),
		"network as an array":             strings.Replace(strings.Replace(oneCountryEntry, `"tcp,udp"`, `["tcp","udp"]`, 1), `{"tag":"de-1","protocol":"vless"`, `{"tag":"DIRECT0","protocol":"freedom"},{"tag":"de-1","protocol":"vless"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := xray.ValidateConnectServiceEntry([]byte(entry), "youtube"); err != nil {
				t.Fatalf("refused: %v", err)
			}
		})
	}
}

func TestConnectServiceRefusesALocationWhoseMainPathIsNotATunnel(t *testing.T) {
	for name, entry := range map[string]string{
		"direct catch-all":  strings.Replace(oneCountryEntry, `{"network":"tcp,udp","outboundTag":"de-1"}`, `{"network":"tcp,udp","outboundTag":"DIRECT"}`, 1),
		"blocked catch-all": strings.Replace(oneCountryEntry, `{"network":"tcp,udp","outboundTag":"de-1"}`, `{"network":"tcp,udp","outboundTag":"BLOCK"}`, 1),
		"freedom default":   `{"outbounds":[{"tag":"DIRECT","protocol":"freedom"},{"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u"}]}]}}],"routing":{"rules":[{"domain":["geosite:google"],"outboundTag":"de-1"}]}}`,
		// The location's own rule decides even when it says "not through me".
		"own rule blocks it":            strings.Replace(oneCountryEntry, `{"domain":["geosite:category-ru"],"outboundTag":"DIRECT"},`, `{"domain":["geosite:category-ru"],"outboundTag":"DIRECT"},{"domain":["geosite:youtube"],"outboundTag":"BLOCK"},`, 1),
		"balancer of direct members":    strings.Replace(strings.Replace(oneCountryEntry, `{"network":"tcp,udp","outboundTag":"de-1"}`, `{"network":"tcp,udp","balancerTag":"BL"}`, 1), `"routing":{`, `"routing":{"balancers":[{"tag":"BL","selector":["DIRECT"],"fallbackTag":"DIRECT"}],`, 1),
		"balancer falling back direct":  `{"outbounds":[{"tag":"DIRECT","protocol":"freedom"},{"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u"}]}]}}],"routing":{"balancers":[{"tag":"BL","selector":["de-"]}],"rules":[{"network":"tcp,udp","balancerTag":"BL"}]}}`,
		"tcp-only rule, direct default": `{"outbounds":[{"tag":"DIRECT","protocol":"freedom"},{"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u"}]}]}}],"routing":{"rules":[{"network":"tcp","outboundTag":"de-1"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := xray.ValidateConnectServiceEntry([]byte(entry), "youtube"); err == nil {
				t.Fatal("a location whose main path is no tunnel was accepted")
			}
		})
	}
}
