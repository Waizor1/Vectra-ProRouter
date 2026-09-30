package xray

import (
	"net/netip"
	"strings"
	"testing"
)

// The shape of the test router's render in PassWall mode (2026-09-29): vctl's
// DNS rule, PassWall's DNS rules, the "direct" shunt (domains, then
// geoip:DIRECT), the proxied slots, the final direct.
const passwallShapedRender = `{
 "outbounds": [
  {"tag":"direct","protocol":"freedom","settings":{"finalRules":[{"action":"allow"}],"domainStrategy":"UseIP"},"streamSettings":{"sockopt":{"mark":22084}}},
  {"tag":"blackhole","protocol":"blackhole"},
  {"tag":"vctl-dns-out","protocol":"dns"},
  {"tag":"WorldProxy","protocol":"vless","settings":{"vnext":[]}}
 ],
 "routing": {"domainStrategy":"IPOnDemand","rules": [
  {"inboundTag":["vctl-dns-in"],"outboundTag":"vctl-dns-out"},
  {"inboundTag":["dns-in-default"],"outboundTag":"direct"},
  {"inboundTag":["tproxy-in"],"network":"tcp,udp","domains":["geosite:russia-outside"],"outboundTag":"direct"},
  {"inboundTag":["tproxy-in"],"network":"tcp,udp","ip":["geoip:DIRECT","1.2.3.0/24","5.6.7.8","geoip:private"],"outboundTag":"direct"},
  {"inboundTag":["tproxy-in"],"network":"tcp,udp","domains":["geosite:META"],"outboundTag":"WorldProxy"},
  {"inboundTag":["tproxy-in"],"network":"tcp,udp","ip":["9.9.9.0/24"],"outboundTag":"direct"},
  {"network":"tcp,udp","outboundTag":"direct"}
 ]}
}`

func TestDirectBypassTakesTheLeadingDirectAddresses(t *testing.T) {
	src, err := DirectBypass([]byte(passwallShapedRender), "tproxy-in")
	if err != nil {
		t.Fatal(err)
	}
	if len(src.GeoIP) != 1 || src.GeoIP[0] != (GeoRef{File: "geoip.dat", Code: "DIRECT"}) {
		t.Fatalf("geoip %v, want DIRECT only (private is the bypass set's already)", src.GeoIP)
	}
	want := []netip.Prefix{netip.MustParsePrefix("1.2.3.0/24"), netip.MustParsePrefix("5.6.7.8/32")}
	if len(src.Prefixes) != 2 || src.Prefixes[0] != want[0] || src.Prefixes[1] != want[1] {
		t.Fatalf("prefixes %v, want %v", src.Prefixes, want)
	}
	// 9.9.9.0/24 is direct too, but after a proxied rule: a connection to
	// it that sniffs as geosite:META is xray's to send to WorldProxy.
	if src.Rules != 1 || src.Stop != 4 || !strings.Contains(src.StopReason, "WorldProxy") {
		t.Fatalf("rules %d stop %d (%s), want 1 rule, stopped at rule 4 by WorldProxy", src.Rules, src.Stop, src.StopReason)
	}
	if src.Empty() || src.Key() == "" {
		t.Fatal("a source with addresses reads as empty")
	}
}

func TestDirectBypassStopsAtTheFirstRuleThatIsNotDirect(t *testing.T) {
	for name, rules := range map[string]string{
		"a proxied port rule first": `{"network":"udp","port":"50000-50100","outboundTag":"WorldProxy"},{"ip":["geoip:DIRECT"],"outboundTag":"direct"}`,
		"a balancer first":          `{"domains":["x.example"],"balancerTag":"B"},{"ip":["geoip:DIRECT"],"outboundTag":"direct"}`,
		"a blackhole first":         `{"ip":["6.6.6.6"],"outboundTag":"blackhole"},{"ip":["geoip:DIRECT"],"outboundTag":"direct"}`,
		"a catch-all direct first":  `{"network":"tcp,udp","outboundTag":"direct"},{"ip":["geoip:DIRECT"],"outboundTag":"direct"}`,
	} {
		render := `{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"blackhole","protocol":"blackhole"},{"tag":"WorldProxy","protocol":"vless"}],"routing":{"rules":[` + rules + `]}}`
		src, err := DirectBypass([]byte(render), "tproxy-in")
		if err != nil {
			t.Fatal(err)
		}
		if !src.Empty() || src.Stop != 0 {
			t.Errorf("%s: %+v, want nothing, stopped at rule 0", name, src)
		}
	}
}

func TestDirectBypassSkipsWhatTheKernelCannotSee(t *testing.T) {
	render := `{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[
	 {"ip":["geoip:!RU"],"outboundTag":"direct"},
	 {"network":"udp","ip":["7.7.7.0/24"],"outboundTag":"direct"},
	 {"ip":["8.8.8.8"],"port":"53","outboundTag":"direct"},
	 {"ip":["ext:vectra.dat:direct"],"outboundTag":"direct"},
	 {"ip":["ext:../etc/shadow:x"],"outboundTag":"direct"}
	]}}`
	src, err := DirectBypass([]byte(render), "tproxy-in")
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Prefixes) != 0 || len(src.GeoIP) != 1 || src.GeoIP[0] != (GeoRef{File: "vectra.dat", Code: "direct"}) || src.Stop != -1 {
		t.Fatalf("%+v: want only ext:vectra.dat:direct, scan to the end", src)
	}
}

func TestDirectBypassNeedsAPlainFreedom(t *testing.T) {
	for name, ob := range map[string]string{
		"fragment":     `{"tag":"direct","protocol":"freedom","settings":{"fragment":{"packets":"tlshello"}}}`,
		"redirect":     `{"tag":"direct","protocol":"freedom","settings":{"redirect":"1.1.1.1:443"}}`,
		"dialer proxy": `{"tag":"direct","protocol":"freedom","streamSettings":{"sockopt":{"dialerProxy":"x"}}}`,
		"interface":    `{"tag":"direct","protocol":"freedom","streamSettings":{"sockopt":{"interface":"wg0"}}}`,
		"a block rule": `{"tag":"direct","protocol":"freedom","settings":{"finalRules":[{"action":"block","ip":["geoip:private"]},{"action":"allow"}]}}`,
	} {
		render := `{"outbounds":[` + ob + `],"routing":{"rules":[{"ip":["geoip:DIRECT"],"outboundTag":"direct"}]}}`
		src, err := DirectBypass([]byte(render), "tproxy-in")
		if err != nil {
			t.Fatal(err)
		}
		if !src.Empty() {
			t.Errorf("%s: a freedom that alters the connection counted as direct: %+v", name, src)
		}
	}
}

func TestDirectBypassIgnoresOtherInbounds(t *testing.T) {
	render := `{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"P","protocol":"vless"}],"routing":{"rules":[
	 {"inboundTag":["socks-in"],"outboundTag":"P"},
	 {"inboundTag":["tproxy-in","socks-in"],"ip":["10.9.0.0/16"],"outboundTag":"direct"}
	]}}`
	src, err := DirectBypass([]byte(render), "tproxy-in")
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Prefixes) != 1 || src.Stop != -1 {
		t.Fatalf("%+v: a rule for another inbound must not stop the scan", src)
	}
	if _, err := DirectBypass([]byte("{"), "tproxy-in"); err == nil {
		t.Fatal("an unreadable render must be an error")
	}
}
