package uiapi

import (
	"reflect"
	"testing"

	"vectra-controller-pro/internal/localctl"
)

// A country offered for a service whose every exit leaves elsewhere, all in
// one other country, is named with it: TikTok «through Turkey» is Poland.
func TestAServiceCountryThatLeavesElsewhereIsNamed(t *testing.T) {
	render := []byte(`{"outbounds":[
	 {"tag":"bridge-tr5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443,"users":[{"id":"u"}]}]}},
	 {"tag":"bridge-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.6","port":443,"users":[{"id":"u"}]}]}},
	 {"tag":"bridge-de6","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":443,"users":[{"id":"u"}]}]}},
	 {"tag":"bridge-by-tcp","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.8","port":443,"users":[{"id":"u"}]}]}}],
	 "routing":{"rules":[{"domain":["geosite:tiktok"],"balancerTag":"BL-TK"},{"network":"tcp,udp","balancerTag":"BL-MAIN"}],
	 "balancers":[{"tag":"BL-TK","selector":["bridge-by-tcp"]},{"tag":"BL-MAIN","selector":["bridge-tr5","bridge-de"]}]}}`)
	s := BuildServices(true, render, localctl.Overrides{}, map[string]string{"bridge-tr5": "PL", "bridge-de5": "DE", "bridge-de6": "NL"})
	var tk *ServiceInfo
	for i := range s.Services {
		if s.Services[i].ID == "tiktok" {
			tk = &s.Services[i]
		}
	}
	if tk == nil || !reflect.DeepEqual(tk.Egress, map[string]string{"TR": "PL"}) {
		t.Fatalf("tiktok %+v", tk)
	}
}
