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
