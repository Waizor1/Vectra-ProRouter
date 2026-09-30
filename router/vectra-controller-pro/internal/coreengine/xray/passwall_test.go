package xray_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
)

func passwallFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/passwall/global.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// PassWall's own config, made into a document the splice takes: its
// inbounds gone, its marks gone, its rules naming the tproxy inbound, no
// system-resolver DNS; the routing, FakeDNS and the direct DNS left as they
// were.
func TestAdaptPassWall(t *testing.T) {
	out, res, err := xray.AdaptPassWall(passwallFixture(t), "tproxy-in")
	if err != nil {
		t.Fatal(err)
	}
	want := config.Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic", "fakedns"}, RouteOnly: true}
	if !reflect.DeepEqual(res.Sniffing, want) {
		t.Fatalf("sniffing = %+v", res.Sniffing)
	}
	if !reflect.DeepEqual(res.FakeDNSPools, []string{"198.18.0.0/16"}) || res.RemovedLocalhost != 1 || res.RewrittenMarks != 3 || res.Rules != 5 {
		t.Fatalf("result = %+v", res)
	}
	s := string(out)
	if strings.Contains(s, `"mark"`) || strings.Contains(s, "tcp_redir") || strings.Contains(s, "udp_redir") || strings.Contains(s, `"localhost"`) {
		t.Fatalf("PassWall's marks, inbound tags or localhost DNS survived:\n%s", s)
	}
	for _, must := range []string{`"inbounds":[]`, `"fakedns":[`, `"domains":["geosite:META","domain:instagram.com"]`, `"domainStrategy":"IPOnDemand"`, `"tcpFastOpen":true`, `"address":"fakedns"`, `"77.37.251.33"`} {
		if !strings.Contains(s, must) {
			t.Errorf("missing %s", must)
		}
	}
	// Stable: the same input makes the same bytes.
	again, _, _ := xray.AdaptPassWall(passwallFixture(t), "tproxy-in")
	if string(again) != s {
		t.Fatal("not deterministic")
	}
}

// The adapted document through the real splice: vctl's tproxy inbound with
// PassWall's sniffing, the DNS inbound, vctl's marks, PassWall's rules for
// the tproxy inbound, the nodes' names answered directly, FakeDNS kept.
func TestPassWallDocumentSplices(t *testing.T) {
	doc, res, err := xray.AdaptPassWall(passwallFixture(t), "tproxy-in")
	if err != nil {
		t.Fatal(err)
	}
	tp := testTproxy()
	tp.Sniffing = res.Sniffing
	opts := xray.SpliceOptions{DNS: &xray.DNSOptions{Listen: xray.DefaultDNSListen, DirectResolvers: xray.DefaultDirectResolvers, AllowFakeDNS: true}}
	spliced, sres, err := xray.Splice(doc, tp, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sres.DNS.Listen == "" {
		t.Fatalf("DNS not steered: %+v", sres.DNS)
	}
	var d struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Sniffing struct {
				DestOverride []string `json:"destOverride"`
				RouteOnly    bool     `json:"routeOnly"`
			} `json:"sniffing"`
		} `json:"inbounds"`
		Outbounds []struct {
			Tag            string `json:"tag"`
			Protocol       string `json:"protocol"`
			StreamSettings struct {
				Sockopt struct {
					Mark int `json:"mark"`
				} `json:"sockopt"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
		Routing struct {
			Rules []map[string]json.RawMessage `json:"rules"`
		} `json:"routing"`
		DNS struct {
			Servers []json.RawMessage `json:"servers"`
		} `json:"dns"`
	}
	if err := json.Unmarshal(spliced, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Inbounds) != 2 || d.Inbounds[0].Tag != "tproxy-in" || !d.Inbounds[0].Sniffing.RouteOnly ||
		strings.Join(d.Inbounds[0].Sniffing.DestOverride, ",") != "http,tls,quic,fakedns" {
		t.Fatalf("inbounds = %+v", d.Inbounds)
	}
	for _, o := range d.Outbounds {
		switch o.Protocol {
		case "vless", "freedom":
			if o.StreamSettings.Sockopt.Mark != config.DefaultXraySockMark {
				t.Errorf("%s mark = %d", o.Tag, o.StreamSettings.Sockopt.Mark)
			}
		}
	}
	if string(d.Routing.Rules[0]["outboundTag"]) != `"vctl-dns-out"` {
		t.Fatalf("first rule = %v", d.Routing.Rules[0])
	}
	var world map[string]json.RawMessage
	for _, r := range d.Routing.Rules {
		if string(r["ruleTag"]) == `"WorldProxy Domains"` {
			world = r
		}
	}
	if world == nil || string(world["inboundTag"]) != `["tproxy-in"]` {
		t.Fatalf("WorldProxy rule = %v", world)
	}
	var first struct {
		Address string   `json:"address"`
		Domains []string `json:"domains"`
	}
	_ = json.Unmarshal(d.DNS.Servers[0], &first)
	if first.Address != "tcp+local://8.8.8.8" || !reflect.DeepEqual(first.Domains, []string{"full:by2.provider.invalid", "full:ru17.provider.invalid"}) {
		t.Fatalf("first DNS server = %+v", first)
	}
	if !strings.Contains(string(spliced), `"address":"fakedns"`) {
		t.Fatal("FakeDNS servers lost")
	}
	if got := xray.RenderFakeDNSPools(spliced); !reflect.DeepEqual(got, []string{"198.18.0.0/16"}) {
		t.Fatalf("pools = %v", got)
	}

	// Without AllowFakeDNS the same document is left unsteered.
	opts.DNS.AllowFakeDNS = false
	if _, sres, _ := xray.Splice(doc, tp, opts); sres.DNS.Listen != "" {
		t.Fatal("FakeDNS steered without being allowed")
	}
}

func TestAdaptPassWallRefusesWhatIsNotAGlobalConfig(t *testing.T) {
	for name, doc := range map[string]string{
		"not json":     `[`,
		"no tproxy":    `{"inbounds":[{"tag":"socks","protocol":"socks"}],"outbounds":[{"tag":"direct","protocol":"freedom"}]}`,
		"no outbounds": `{"inbounds":[{"tag":"tcp_redir","protocol":"dokodemo-door"}]}`,
	} {
		if _, _, err := xray.AdaptPassWall([]byte(doc), "tproxy-in"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// PassWall's generator names its geo directory in the config's env, and a recent
// xray (26.7.28) applies that over the env it was started with: vctl's pinned
// directory (the supervisor's, the xray -test gate's) would be ignored. The
// adapter takes XRAY_LOCATION_ASSET out and leaves the rest of env.
func TestAdaptPassWallLeavesTheGeoDirectoryToVctl(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(passwallFixture(t), &doc); err != nil {
		t.Fatal(err)
	}
	for name, env := range map[string]map[string]any{
		"asset only": {"XRAY_LOCATION_ASSET": "/usr/share/v2ray/"},
		"and more":   {"XRAY_LOCATION_ASSET": "/usr/share/v2ray/", "XRAY_BUF_SPLICE": "enable"},
	} {
		doc["env"] = env
		raw, _ := json.Marshal(doc)
		out, res, err := xray.AdaptPassWall(raw, "tproxy-in")
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if !res.DroppedAssetDir || strings.Contains(string(out), "XRAY_LOCATION_ASSET") {
			t.Fatalf("%s: the config still names the geo directory: %v", name, got["env"])
		}
		e, has := got["env"].(map[string]any)
		if name == "asset only" && has {
			t.Fatalf("an env left empty stays: %v", e)
		}
		if name == "and more" && (!has || e["XRAY_BUF_SPLICE"] != "enable") {
			t.Fatalf("the rest of env was lost: %v", got["env"])
		}
	}
}
