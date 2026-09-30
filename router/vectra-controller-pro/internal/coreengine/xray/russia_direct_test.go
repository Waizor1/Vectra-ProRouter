package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

const ruDoc = `{"outbounds":[{"tag":"bridge-pl5","protocol":"vless"},{"tag":"DIRECT","protocol":"freedom"},{"tag":"BLOCK","protocol":"blackhole"}],
"routing":{"domainStrategy":"IPIfNonMatch","rules":[
{"protocol":["bittorrent"],"outboundTag":"DIRECT"},
{"domain":["geosite:youtube","domain:googlevideo.com"],"balancerTag":"BL-RU"},
{"domain":["domain:gosuslugi.ru","domain:vk.com"],"outboundTag":"DIRECT"},
{"domain":["geosite:category-gov-ru","domain:gu-st.ru"],"balancerTag":"BL-RU"},
{"domain":["domain:ru","domain:xn--p1ai","domain:by"],"balancerTag":"BL-RU"},
{"ip":["geoip:ru"],"balancerTag":"BL-RU"},
{"domain":["domain:chatgpt.com"],"balancerTag":"BL-MAIN"},
{"network":"tcp,udp","balancerTag":"BL-MAIN"}],
"balancers":[{"tag":"BL-MAIN","selector":["bridge-"]},{"tag":"BL-RU","selector":["bridge-"]}]}}`

func rulesOf(t *testing.T, routing []byte) []map[string]any {
	t.Helper()
	var r struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(routing, &r); err != nil {
		t.Fatal(err)
	}
	return r.Rules
}

func TestRussianRulesGoDirectAndYouTubeKeepsItsBridge(t *testing.T) {
	var doc struct {
		Routing json.RawMessage `json:"routing"`
	}
	if err := json.Unmarshal([]byte(ruDoc), &doc); err != nil {
		t.Fatal(err)
	}
	out, n, err := rewriteRussianRules(doc.Routing, "DIRECT")
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rules := rulesOf(t, out)
	if len(rules) != 8 {
		t.Fatalf("rules %d", len(rules))
	}
	if rules[1]["balancerTag"] != "BL-RU" {
		t.Fatalf("YouTube moved: %v", rules[1])
	}
	for _, i := range []int{3, 4, 5} {
		if rules[i]["outboundTag"] != "DIRECT" || rules[i]["balancerTag"] != nil {
			t.Fatalf("rule %d not direct: %v", i, rules[i])
		}
	}
	if rules[6]["balancerTag"] != "BL-MAIN" || rules[7]["balancerTag"] != "BL-MAIN" {
		t.Fatalf("main rules touched: %v %v", rules[6], rules[7])
	}
	if s := string(out); !strings.Contains(s, `"domainStrategy":"IPIfNonMatch"`) {
		t.Fatalf("the rest of routing lost: %s", s)
	}
}

func TestNoDirectOutboundLeavesTheRulesAlone(t *testing.T) {
	if got := directOutboundTag([]byte(strings.Replace(ruDoc, `"protocol":"freedom"`, `"protocol":"blackhole"`, 1))); got != "" {
		t.Fatalf("direct tag %q", got)
	}
	if got := directOutboundTag([]byte(ruDoc)); got != "DIRECT" {
		t.Fatalf("direct tag %q", got)
	}
}

// 1111, 2026-09-30: «NNM club не открывается — бесконечная загрузка». The
// provider's Russian rule names Russian TLDs AND sites it sends through its
// own Russian server on purpose — nnmclub.to, kinozal.tv, anime sites: they
// want a Russian address and are blocked by the filter at home. The rule
// went direct whole, and with it those sites into the filter. It is split:
// the Russian destinations go direct, the rest keep the provider's Russian
// bridge — first, so a keyword of it wins over a Russian TLD.
func TestAMixedRussianRuleKeepsItsBlockedSitesOnTheBridge(t *testing.T) {
	routing := []byte(`{"rules":[
	{"domain":["domain:ru","domain:xn--p1ai","domain:by","domain:nnmclub.to","domain:kinozal.tv","keyword:nnmclub","domain:animego.org","full:lk.gosuslugi.ru"],"balancerTag":"BL-RU"},
	{"network":"tcp,udp","balancerTag":"BL-MAIN"}]}`)
	out, n, err := rewriteRussianRules(routing, "DIRECT")
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rules := rulesOf(t, out)
	if len(rules) != 3 {
		t.Fatalf("rules %v", rules)
	}
	want0 := []any{"domain:nnmclub.to", "domain:kinozal.tv", "keyword:nnmclub", "domain:animego.org"}
	want1 := []any{"domain:ru", "domain:xn--p1ai", "domain:by", "full:lk.gosuslugi.ru"}
	if rules[0]["balancerTag"] != "BL-RU" || rules[0]["outboundTag"] != nil || !equalAny(rules[0]["domain"], want0) {
		t.Fatalf("the bridge's part: %v", rules[0])
	}
	if rules[1]["outboundTag"] != "DIRECT" || rules[1]["balancerTag"] != nil || !equalAny(rules[1]["domain"], want1) {
		t.Fatalf("the direct part: %v", rules[1])
	}
	if rules[2]["balancerTag"] != "BL-MAIN" {
		t.Fatalf("the catch-all moved: %v", rules[2])
	}
}

// A rule that ANDs a domain list with an address list cannot be split
// without changing what it matches: mixed, it keeps the bridge whole.
func TestAMixedRuleOfTwoConditionsKeepsTheBridge(t *testing.T) {
	routing := []byte(`{"rules":[{"domain":["domain:ru","domain:nnmclub.to"],"ip":["geoip:ru"],"balancerTag":"BL-RU"}]}`)
	out, n, err := rewriteRussianRules(routing, "DIRECT")
	if err != nil || n != 0 || rulesOf(t, out)[0]["balancerTag"] != "BL-RU" {
		t.Fatalf("n=%d err=%v rules %v", n, err, rulesOf(t, out))
	}
}

func equalAny(got any, want []any) bool {
	l, ok := got.([]any)
	if !ok || len(l) != len(want) {
		return false
	}
	for i := range l {
		if l[i] != want[i] {
			return false
		}
	}
	return true
}

// Wholly Russian, two conditions or one, it goes direct as before.
func TestAWhollyRussianRuleOfTwoConditionsGoesDirect(t *testing.T) {
	routing := []byte(`{"rules":[{"domain":["domain:ru","full:x.su"],"ip":["geoip:ru"],"balancerTag":"BL-RU"}]}`)
	out, n, err := rewriteRussianRules(routing, "DIRECT")
	r := rulesOf(t, out)
	if err != nil || n != 1 || len(r) != 1 || r[0]["outboundTag"] != "DIRECT" || r[0]["balancerTag"] != nil {
		t.Fatalf("n=%d err=%v rules %v", n, err, r)
	}
}

// In the bridge's address rule only the Russian category goes direct. An
// address the provider wrote is sent through its Russian server on purpose,
// like nnmclub.to among the names (the review of r29–r31: direct, a foreign
// one would meet the filter at home; through the bridge, a Russian one still
// works). Another country's category keeps the bridge too.
func TestTheBridgesOwnAddressesKeepTheBridge(t *testing.T) {
	routing := []byte(`{"rules":[{"ip":["geoip:ru","203.0.113.0/24","198.51.100.7","2001:db8::/32","geoip:telegram"],"balancerTag":"BL-RU"}]}`)
	out, n, err := rewriteRussianRules(routing, "DIRECT")
	r := rulesOf(t, out)
	if err != nil || n != 1 || len(r) != 2 {
		t.Fatalf("n=%d err=%v rules %v", n, err, r)
	}
	if r[0]["balancerTag"] != "BL-RU" || !equalAny(r[0]["ip"], []any{"203.0.113.0/24", "198.51.100.7", "2001:db8::/32", "geoip:telegram"}) {
		t.Fatalf("the bridge's part: %v", r[0])
	}
	if r[1]["outboundTag"] != "DIRECT" || !equalAny(r[1]["ip"], []any{"geoip:ru"}) {
		t.Fatalf("the direct part: %v", r[1])
	}
}

// The review of r29–r31: the Russian reading was case-sensitive, and xray's
// is not past the prefix — "domain:Example.RU" and "geoip:RU" are Russian
// destinations to xray and were read as the bridge's.
func TestRussianDestinationsAreReadWhateverTheirCase(t *testing.T) {
	routing := []byte(`{"rules":[{"domain":["domain:Example.RU","full:WWW.YA.RU","domain:nnmclub.to"],"balancerTag":"BL-RU"},{"ip":["geoip:RU"],"balancerTag":"BL-RU"}]}`)
	out, n, err := rewriteRussianRules(routing, "DIRECT")
	r := rulesOf(t, out)
	if err != nil || n != 2 || len(r) != 3 {
		t.Fatalf("n=%d err=%v rules %v", n, err, r)
	}
	if r[0]["balancerTag"] != "BL-RU" || !equalAny(r[0]["domain"], []any{"domain:nnmclub.to"}) {
		t.Fatalf("the bridge's part: %v", r[0])
	}
	if r[1]["outboundTag"] != "DIRECT" || !equalAny(r[1]["domain"], []any{"domain:Example.RU", "full:WWW.YA.RU"}) {
		t.Fatalf("the direct part: %v", r[1])
	}
	if r[2]["outboundTag"] != "DIRECT" {
		t.Fatalf("geoip:RU kept the bridge: %v", r[2])
	}
}
