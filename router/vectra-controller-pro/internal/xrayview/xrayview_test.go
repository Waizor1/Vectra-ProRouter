package xrayview

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../coreengine/xray/testdata/provider/entry-00.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseTheRealProviderDocument(t *testing.T) {
	v, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	dials := 0
	for _, o := range v.Outbounds {
		if o.Dials {
			dials++
		}
	}
	if dials != 22 {
		t.Errorf("%d dialling outbounds, want 22", dials)
	}
	pl5 := v.Outbound("bridge-pl5")
	if pl5 == nil || pl5.Protocol != "vless" || pl5.Transport != "tcp" || pl5.Security != "reality" || pl5.Address != "ru12.provider.invalid" || pl5.Port == 0 {
		t.Errorf("bridge-pl5 = %+v", pl5)
	}
	if hy := v.Outbound("hy2-de5"); hy == nil || hy.Transport != "hysteria" || hy.Address == "" {
		t.Errorf("hy2-de5 = %+v", hy)
	}
	if d := v.Outbound("DIRECT"); d == nil || d.Dials || d.Address != "" {
		t.Errorf("DIRECT = %+v", d)
	}

	type want struct {
		role, strategy, fallbackBalancer string
		members                          int
	}
	for tag, w := range map[string]want{
		"BL-MAIN":        {"main", "leastLoad", "BL-MAIN-BACKUP", 8},
		"BL-MAIN-BACKUP": {"reserve", "leastLoad", "BL-WL-LV1", 3},
		"BL-RU":          {"routed", "leastLoad", "BL-MAIN", 1},
		"BL-TK":          {"routed", "leastPing", "BL-MAIN", 1},
		"BL-WL-LV1":      {"reserve", "leastPing", "BL-WL-LV2", 0},
		"BL-WL-LV2":      {"reserve", "leastPing", "BL-WL-LV3", 1},
		// The prefix selector "whitelist-lv3" takes all eight lv3 nodes; its
		// fallback is a plain node, not another balancer.
		"BL-WL-LV3": {"reserve", "leastPing", "", 8},
	} {
		b := v.Balancer(tag)
		if b == nil {
			t.Errorf("%s missing", tag)
			continue
		}
		if b.Role != w.role || b.Strategy != w.strategy || b.FallbackBalancer != w.fallbackBalancer || len(b.Members) != w.members {
			t.Errorf("%s = role %s strategy %s fallback %q members %d; want %+v", tag, b.Role, b.Strategy, b.FallbackBalancer, len(b.Members), w)
		}
	}
	if b := v.Balancer("BL-MAIN"); b.Expected == nil || *b.Expected != 2 {
		t.Errorf("BL-MAIN expected = %v", b.Expected)
	}
	if b := v.Balancer("BL-WL-LV3"); b.FallbackTag != "whitelist-lv3" {
		t.Errorf("BL-WL-LV3 fallbackTag = %q", b.FallbackTag)
	}

	// BL-MAIN carries two named rule sets and the catch-all; BL-TK carries
	// TikTok and Telegram (domains and IPs).
	main := v.Balancer("BL-MAIN")
	kinds := []string{}
	for _, m := range main.Rules {
		kinds = append(kinds, m.Kind)
	}
	if strings.Join(kinds, ",") != "domain,domain,all" {
		t.Errorf("BL-MAIN rule kinds = %v", kinds)
	}
	tk := v.Balancer("BL-TK")
	if len(tk.Rules) != 3 || tk.Rules[0].Sample[0] != "geosite:tiktok" || tk.Rules[2].Kind != "ip" || tk.Rules[0].Total != 25 || len(tk.Rules[0].Sample) != SampleSize {
		t.Errorf("BL-TK rules = %+v", tk.Rules)
	}

	if !v.HasObservatory || v.ProbeInterval != 12*time.Hour || v.ProbeSampling != 2 || v.ProbeTimeout != 10*time.Second || v.ProbeDestination != "https://cp.cloudflare.com/generate_204" {
		t.Errorf("observatory = %v %s %d %s %q", v.HasObservatory, v.ProbeInterval, v.ProbeSampling, v.ProbeTimeout, v.ProbeDestination)
	}
	if got := strings.Join(v.BalancersOf("whitelist-lv3-2"), ","); got != "BL-WL-LV3" {
		t.Errorf("BalancersOf(whitelist-lv3-2) = %s", got)
	}
}

func TestCanPinRefusesAnythingXrayWouldSilentlyAccept(t *testing.T) {
	v, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.CanPin("BL-MAIN", "bridge-de5"); err != nil {
		t.Errorf("a real member refused: %v", err)
	}
	for _, c := range [][2]string{
		{"BL-NOPE", "bridge-de5"},    // no such balancer
		{"BL-MAIN", "bridge-ru-tcp"}, // a node of ANOTHER balancer
		{"BL-MAIN", "DIRECT"},        // not a member, and not a node
		{"BL-MAIN", "bridge-de"},     // a prefix, not a tag
		{"BL-MAIN", ""},
	} {
		if err := v.CanPin(c[0], c[1]); err == nil {
			t.Errorf("CanPin(%q, %q) accepted", c[0], c[1])
		}
	}
}

// Nothing a View holds may carry a credential. Every string value under a
// credential-looking key in the fixture is collected and searched for in a
// dump of the whole View.
func TestTheViewCarriesNoCredentials(t *testing.T) {
	raw := fixture(t)
	v, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	dump := fmt.Sprintf("%+v", *v)
	var doc interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	secretKeys := map[string]bool{"id": true, "password": true, "publicKey": true, "shortId": true, "spiderX": true, "auth": true, "users": false}
	var secrets []string
	var walk func(x interface{})
	walk = func(x interface{}) {
		switch y := x.(type) {
		case map[string]interface{}:
			for k, val := range y {
				if s, ok := val.(string); ok && secretKeys[k] && len(s) >= 6 {
					secrets = append(secrets, s)
				}
				walk(val)
			}
		case []interface{}:
			for _, e := range y {
				walk(e)
			}
		}
	}
	walk(doc)
	if len(secrets) < 10 {
		t.Fatalf("found only %d credential values in the fixture; the scan is not looking where they are", len(secrets))
	}
	for _, s := range secrets {
		if strings.Contains(dump, s) {
			t.Errorf("the view contains a credential value (%d chars)", len(s))
		}
	}
}

func TestParseTheSplicedRuntimeAdditions(t *testing.T) {
	doc := []byte(`{"api":{"tag":"vctl-api","listen":"127.0.0.1:10085"},"metrics":{"tag":"vctl-metrics","listen":"127.0.0.1:10086"},
"outbounds":[{"tag":"vctl-x","protocol":"vless","settings":{"address":"a.example","port":443,"id":"secret-uuid-value"}}],
"routing":{"rules":[{"port":"443","balancerTag":"B"}],"balancers":[{"tag":"B","selector":["vctl-"]}]},
"observatory":{"probeUrl":"https://x.test/204","probeInterval":"5m"}}`)
	v, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if v.APIListen != "127.0.0.1:10085" || v.MetricsListen != "127.0.0.1:10086" {
		t.Errorf("listen = %q %q", v.APIListen, v.MetricsListen)
	}
	b := v.Balancer("B")
	// xray registers the metrics endpoint as an outbound, so a selector that
	// happens to match its tag picks it up — the view must show that, and
	// CanPin must still refuse it.
	if strings.Join(b.Members, ",") != "vctl-metrics,vctl-x" {
		t.Errorf("members = %v", b.Members)
	}
	if err := v.CanPin("B", "vctl-metrics"); err == nil {
		t.Error("the metrics endpoint was accepted as a pin target")
	}
	if b.Strategy != "random" || b.Role != "routed" || b.Rules[0].Kind != "other" || b.Rules[0].Sample[0] != "port:443" {
		t.Errorf("B = %+v", b)
	}
	if o := v.Outbound("vctl-x"); o.Address != "a.example" || o.Port != 443 {
		t.Errorf("flat vless settings: %+v", o)
	}
	if v.ProbeInterval != 5*time.Minute || v.ProbeSampling != 1 {
		t.Errorf("classic observatory: %s/%d", v.ProbeInterval, v.ProbeSampling)
	}
}

func TestLoopbackOnlyAcceptsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:10085": true, "[::1]:10085": true, "127.8.9.1:1": true,
		"0.0.0.0:10085": false, "192.168.1.1:10085": false, "[::]:10085": false,
		"localhost:10085": false, "10085": false, "": false,
	} {
		if got := Loopback(addr); got != want {
			t.Errorf("Loopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

// xray hands a connection whose balancer has neither a candidate nor a
// fallbackTag to its default handler: the FIRST outbound of the config,
// tagged or not.
func TestDefaultIsTheFirstOutboundTaggedOrNot(t *testing.T) {
	v, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if v.Default == nil || v.Default.Tag != "bridge-pl5" || !v.Default.Dials {
		t.Errorf("provider default = %+v, want bridge-pl5", v.Default)
	}
	v, err = Parse([]byte(`{"outbounds":[{"protocol":"freedom"},{"tag":"node-a","protocol":"vless","settings":{"vnext":[{"address":"a","port":1}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.Default == nil || v.Default.Tag != "" || v.Default.Protocol != "freedom" || len(v.Outbounds) != 1 {
		t.Errorf("untagged first outbound: default = %+v, outbounds %d", v.Default, len(v.Outbounds))
	}
	if v, _ := Parse([]byte(`{"outbounds":[]}`)); v.Default != nil {
		t.Errorf("no outbounds, default = %+v", v.Default)
	}
}
