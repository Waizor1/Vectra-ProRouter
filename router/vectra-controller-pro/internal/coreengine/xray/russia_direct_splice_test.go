package xray_test

import (
	"strings"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
)

const ruSpliceDoc = `{"outbounds":[{"tag":"bridge-pl5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000","encryption":"none"}]}]}},{"tag":"DIRECT","protocol":"freedom"},{"tag":"BLOCK","protocol":"blackhole"}],
"routing":{"rules":[{"domain":["geosite:youtube"],"balancerTag":"BL-RU"},{"ip":["geoip:ru"],"balancerTag":"BL-RU"},{"network":"tcp,udp","balancerTag":"BL-MAIN"}],
"balancers":[{"tag":"BL-MAIN","selector":["bridge-"]},{"tag":"BL-RU","selector":["bridge-"]}]}}`

func TestSpliceSendsRussiaDirectAndSaysHowMany(t *testing.T) {
	out, res, err := xray.Splice([]byte(ruSpliceDoc), testTproxy(), xray.SpliceOptions{RussiaDirect: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.RussiaDirectRules != 1 {
		t.Fatalf("res %d", res.RussiaDirectRules)
	}
	if !strings.Contains(string(out), `"ip":["geoip:ru"],"outboundTag":"DIRECT"`) {
		t.Fatalf("render: %s", out)
	}
	if !strings.Contains(string(out), `"domain":["geosite:youtube"],"balancerTag":"BL-RU"`) {
		t.Fatalf("YouTube moved: %s", out)
	}
	if k := (xray.SpliceOptions{RussiaDirect: true}).Key(); !strings.Contains(k, ";ru=direct") {
		t.Fatalf("key %q", k)
	}
	plain, res0, err := xray.Splice([]byte(ruSpliceDoc), testTproxy(), xray.SpliceOptions{})
	if err != nil || res0.RussiaDirectRules != 0 || !strings.Contains(string(plain), `"ip":["geoip:ru"],"balancerTag":"BL-RU"`) {
		t.Fatalf("without the option the provider's rule changed: %v %s", err, plain)
	}
}
