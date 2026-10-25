package xray_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
)

// flowsOf is every outbound's flow, flat or in vnext's users.
func flowsOf(t *testing.T, render []byte) map[string]string {
	t.Helper()
	var doc struct {
		Outbounds []struct {
			Tag      string `json:"tag"`
			Settings struct {
				Flow  string `json:"flow"`
				Vnext []struct {
					Users []struct {
						Flow string `json:"flow"`
					} `json:"users"`
				} `json:"vnext"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(render, &doc); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := map[string]string{}
	for _, ob := range doc.Outbounds {
		f := ob.Settings.Flow
		for _, v := range ob.Settings.Vnext {
			for _, u := range v.Users {
				if u.Flow != "" {
					f = u.Flow
				}
			}
		}
		out[ob.Tag] = f
	}
	return out
}

func TestQUICOverBridgeGivesOnlyTheBridgeTheUDP443Flow(t *testing.T) {
	provider := providerFixture(t)
	before := flowsOf(t, provider)
	if before["bridge-ru-tcp"] != xray.VisionFlow || before["bridge-pl5"] != xray.VisionFlow {
		t.Fatalf("fixture changed: %v", before)
	}

	out, res, err := xray.Splice(provider, testTproxy(), xray.SpliceOptions{QUICOverBridge: true, RussiaDirect: true})
	if err != nil {
		t.Fatal(err)
	}
	after := flowsOf(t, out)
	if after["bridge-ru-tcp"] != xray.VisionUDP443Flow {
		t.Fatalf("bridge-ru-tcp flow = %q, want %q", after["bridge-ru-tcp"], xray.VisionUDP443Flow)
	}
	if res.QUICBridgeOutbounds != 1 {
		t.Fatalf("QUICBridgeOutbounds = %d, want 1", res.QUICBridgeOutbounds)
	}
	for tag, f := range before {
		if tag != "bridge-ru-tcp" && after[tag] != f {
			t.Fatalf("%s flow %q -> %q: only the bridge's members may change", tag, f, after[tag])
		}
	}
}

func TestWithoutQUICOverBridgeTheFlowsStay(t *testing.T) {
	provider := providerFixture(t)
	out, res, err := xray.Splice(provider, testTproxy(), xray.SpliceOptions{RussiaDirect: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := flowsOf(t, out)["bridge-ru-tcp"]; got != xray.VisionFlow || res.QUICBridgeOutbounds != 0 {
		t.Fatalf("flow %q, rewritten %d: nothing may change without the option", got, res.QUICBridgeOutbounds)
	}
	if xray.RenderBridgeQUIC(out).Members != nil {
		t.Fatal("plain Vision carries no UDP/443: no member")
	}
}

func TestQUICOverBridgeRewritesTheFlatSettingsToo(t *testing.T) {
	doc := `{"outbounds":[
{"tag":"bridge-ru-tcp","protocol":"vless","settings":{"address":"ru.example","port":40051,"id":"x","flow":"xtls-rprx-vision","encryption":"none"}},
{"tag":"bridge-ru-tcp-2","protocol":"vless","settings":{"address":"ru2.example","port":40051,"id":"x","flow":"xtls-rprx-vision"}},
{"tag":"bridge-de5","protocol":"vless","settings":{"address":"de.example","port":443,"id":"x","flow":"xtls-rprx-vision"}},
{"tag":"DIRECT","protocol":"freedom"}],
"routing":{"rules":[{"domain":["domain:googlevideo.com"],"balancerTag":"BL-RU"}],
"balancers":[{"tag":"BL-RU","selector":["bridge-ru-tcp"]}]}}`
	out, res, err := xray.Splice([]byte(doc), testTproxy(), xray.SpliceOptions{QUICOverBridge: true})
	if err != nil {
		t.Fatal(err)
	}
	flows := flowsOf(t, out)
	if flows["bridge-ru-tcp"] != xray.VisionUDP443Flow || flows["bridge-ru-tcp-2"] != xray.VisionUDP443Flow {
		t.Fatalf("members not rewritten (the selector is a prefix): %v", flows)
	}
	if flows["bridge-de5"] != xray.VisionFlow {
		t.Fatalf("a non-member changed: %v", flows)
	}
	if res.QUICBridgeOutbounds != 2 {
		t.Fatalf("QUICBridgeOutbounds = %d, want 2", res.QUICBridgeOutbounds)
	}
	if !strings.Contains(string(out), `"encryption":"none"`) {
		t.Fatalf("the member's other settings were lost: %s", out)
	}
}

func TestRenderBridgeQUICNamesTheBridgesNamesAndMembers(t *testing.T) {
	out, _, err := xray.Splice(providerFixture(t), testTproxy(), xray.SpliceOptions{QUICOverBridge: true, RussiaDirect: true})
	if err != nil {
		t.Fatal(err)
	}
	bq := xray.RenderBridgeQUIC(out)
	if !reflect.DeepEqual(bq.Members, []string{"bridge-ru-tcp"}) {
		t.Fatalf("members = %v, want [bridge-ru-tcp]", bq.Members)
	}
	have := map[string]bool{}
	for _, n := range bq.Names {
		have[n] = true
		if !strings.Contains(n, ".") {
			t.Fatalf("a whole top-level domain %q is no name", n)
		}
	}
	for _, want := range []string{"googlevideo.com", "ytimg.com", "ggpht.com"} {
		if !have[want] {
			t.Fatalf("names %v lack %s (YouTube's BL-RU rule)", bq.Names, want)
		}
	}
	for _, never := range []string{"youtube", "geosite:youtube", "ru", "by", "nnmclub"} {
		if have[never] {
			t.Fatalf("names %v carry %q: not a name a resolver can be told", bq.Names, never)
		}
	}
}

func TestRenderBridgeQUICSkipsATCPOnlyRule(t *testing.T) {
	doc := `{"outbounds":[{"tag":"bridge-ru-tcp","protocol":"vless","settings":{"flow":"xtls-rprx-vision-udp443"}}],
"routing":{"rules":[{"domain":["domain:tcp-only.example"],"network":"tcp","balancerTag":"BL-RU"},
{"domain":["full:quic.example"],"network":"tcp,udp","balancerTag":"BL-RU"}],
"balancers":[{"tag":"BL-RU","selector":["bridge-ru-tcp"]}]}}`
	bq := xray.RenderBridgeQUIC([]byte(doc))
	if !reflect.DeepEqual(bq.Names, []string{"quic.example"}) {
		t.Fatalf("names = %v, want [quic.example]", bq.Names)
	}
}

func TestQUICOverBridgeIsPartOfTheRenderKey(t *testing.T) {
	off := xray.SpliceOptions{RussiaDirect: true}.Key()
	on := xray.SpliceOptions{RussiaDirect: true, QUICOverBridge: true}.Key()
	if off == on || !strings.Contains(on, ";ru=quic") {
		t.Fatalf("keys %q / %q: an upgrade must render again", off, on)
	}
}
