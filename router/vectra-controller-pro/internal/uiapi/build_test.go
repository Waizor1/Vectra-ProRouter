package uiapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/xrayview"
)

func check(t *testing.T, d Diagnostics, id string) *Check {
	t.Helper()
	for i := range d.Checks {
		if d.Checks[i].ID == id {
			return &d.Checks[i]
		}
	}
	return nil
}

func paramsJSON(t *testing.T, c *Check) string {
	t.Helper()
	b, err := json.Marshal(c.Params)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bal is a balancer as xrayview resolves it. fallbackBalancer is where its
// fallback outbound leads through a loopback, "" for a plain outbound.
func bal(tag, role, strategy string, members []string, fallbackTag, fallbackBalancer string) xrayview.Balancer {
	return xrayview.Balancer{Tag: tag, Role: role, Strategy: strategy, Members: members,
		FallbackTag: fallbackTag, FallbackBalancer: fallbackBalancer}
}

func node(tag string) xrayview.Outbound {
	return xrayview.Outbound{Tag: tag, Protocol: "vless", Dials: true}
}

var (
	freedom   = xrayview.Outbound{Tag: "DIRECT", Protocol: "freedom"}
	blackhole = xrayview.Outbound{Tag: "BLOCK", Protocol: "blackhole"}
)

func picks(tags ...string) api.BalancerInfo { return api.BalancerInfo{Principle: tags} }

var (
	alive = api.Observation{Alive: true, DelayMs: 90}
	dead  = api.Observation{Alive: false}
)

const okFallback = `{"balancers":[],"blocked":false,"blockedBalancers":[],"main":false,"mainBlocked":false,"mainDirect":false,"warming":false}`

func TestBalancerFallbackFollowsTheTrafficAsXrayDoes(t *testing.T) {
	main := func(strategy string, fallbackTag, fallbackBalancer string) xrayview.Balancer {
		return bal("BL-MAIN", "main", strategy, []string{"a", "b"}, fallbackTag, fallbackBalancer)
	}
	backup := bal("BL-BACKUP", "reserve", "leastLoad", []string{"c"}, "", "")
	probed := map[string]api.Observation{"a": dead, "b": dead, "c": alive}

	for _, tc := range []struct {
		name      string
		balancers []xrayview.Balancer
		outbounds []xrayview.Outbound
		def       *xrayview.Outbound
		info      map[string]api.BalancerInfo
		obs       map[string]api.Observation
		status    string
		params    string
	}{{
		name: "an empty reserve that no traffic reaches",
		balancers: []xrayview.Balancer{main("leastLoad", "stage-wl", "BL-WL-LV1"),
			bal("BL-WL-LV1", "reserve", "leastPing", nil, "", "")},
		info:   map[string]api.BalancerInfo{"BL-MAIN": picks("a"), "BL-WL-LV1": picks()},
		obs:    map[string]api.Observation{"a": alive, "b": dead},
		status: "ok", params: okFallback,
	}, {
		name:      "leastPing with no live member passes the traffic on",
		balancers: []xrayview.Balancer{main("LeastPing", "stage-backup", "BL-BACKUP"), backup},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks(), "BL-BACKUP": picks("c")},
		obs:       probed,
		status:    "warn", params: `{"balancers":["BL-MAIN"],"blocked":false,"blockedBalancers":[],"main":true,"mainBlocked":false,"mainDirect":false,"warming":false}`,
	}, {
		name:      "random carries while any member is alive",
		balancers: []xrayview.Balancer{main("random", "stage-backup", "BL-BACKUP"), backup},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks("a", "b"), "BL-BACKUP": picks("c")},
		obs:       map[string]api.Observation{"a": dead, "b": alive, "c": alive},
		status:    "ok", params: okFallback,
	}, {
		name:      "random with a fallback skips members the observatory saw dead",
		balancers: []xrayview.Balancer{main("random", "stage-backup", "BL-BACKUP"), backup},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks("a", "b"), "BL-BACKUP": picks("c")},
		obs:       probed,
		status:    "warn", params: `{"balancers":["BL-MAIN"],"blocked":false,"blockedBalancers":[],"main":true,"mainBlocked":false,"mainDirect":false,"warming":false}`,
	}, {
		name:      "roundRobin without a fallback is stuck on dead members",
		balancers: []xrayview.Balancer{main("roundRobin", "", "")},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks("a", "b")},
		obs:       probed,
		status:    "fail", params: `{"balancers":[],"blocked":true,"blockedBalancers":["BL-MAIN"],"main":false,"mainBlocked":true,"mainDirect":false,"warming":false}`,
	}, {
		name: "random over members the observatory has no record of: cannot tell",
		balancers: []xrayview.Balancer{main("leastLoad", "", ""),
			bal("BL-RU", "routed", "roundRobin", []string{"ru1", "ru2"}, "stage-main", "BL-MAIN")},
		info:   map[string]api.BalancerInfo{"BL-MAIN": picks("a"), "BL-RU": picks("ru1", "ru2")},
		obs:    map[string]api.Observation{"a": alive, "ru1": dead},
		status: "ok", params: okFallback,
	}, {
		name:      "a pin carries, dead or not (pinned_node_dead says the rest)",
		balancers: []xrayview.Balancer{main("leastLoad", "", "")},
		info:      map[string]api.BalancerInfo{"BL-MAIN": {Override: "a"}},
		obs:       probed,
		status:    "ok", params: okFallback,
	}, {
		name: "a balancer with no members passes the traffic on",
		balancers: []xrayview.Balancer{main("leastLoad", "", ""),
			bal("BL-RU", "routed", "random", nil, "stage-main", "BL-MAIN")},
		info:   map[string]api.BalancerInfo{"BL-MAIN": picks("a"), "BL-RU": picks()},
		obs:    map[string]api.Observation{"a": alive},
		status: "warn", params: `{"balancers":["BL-RU"],"blocked":false,"blockedBalancers":[],"main":false,"mainBlocked":false,"mainDirect":false,"warming":false}`,
	}, {
		name:      "a plain fallback node the observatory saw alive carries",
		balancers: []xrayview.Balancer{main("leastLoad", "node-z", "")},
		outbounds: []xrayview.Outbound{node("node-z")},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       map[string]api.Observation{"a": dead, "b": dead, "node-z": alive},
		status:    "warn", params: `{"balancers":["BL-MAIN"],"blocked":false,"blockedBalancers":[],"main":true,"mainBlocked":false,"mainDirect":false,"warming":false}`,
	}, {
		name:      "a plain fallback node it has no record of carries",
		balancers: []xrayview.Balancer{main("leastLoad", "node-z", "")},
		outbounds: []xrayview.Outbound{node("node-z")},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       probed,
		status:    "warn", params: `{"balancers":["BL-MAIN"],"blocked":false,"blockedBalancers":[],"main":true,"mainBlocked":false,"mainDirect":false,"warming":false}`,
	}, {
		name:      "a plain fallback node it saw dead: blocked",
		balancers: []xrayview.Balancer{main("leastLoad", "node-z", "")},
		outbounds: []xrayview.Outbound{node("node-z")},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       map[string]api.Observation{"a": dead, "b": dead, "node-z": dead},
		status:    "fail", params: `{"balancers":["BL-MAIN"],"blocked":true,"blockedBalancers":["BL-MAIN"],"main":true,"mainBlocked":true,"mainDirect":false,"warming":false}`,
	}, {
		name:      "a fallback tag the config does not have: xray closes the connection",
		balancers: []xrayview.Balancer{main("leastLoad", "gone", "")},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       probed,
		status:    "fail", params: `{"balancers":["BL-MAIN"],"blocked":true,"blockedBalancers":["BL-MAIN"],"main":true,"mainBlocked":true,"mainDirect":false,"warming":false}`,
	}, {
		name:      "a blackhole fallback: blocked",
		balancers: []xrayview.Balancer{main("leastLoad", "BLOCK", "")},
		outbounds: []xrayview.Outbound{blackhole},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       probed,
		status:    "fail", params: `{"balancers":["BL-MAIN"],"blocked":true,"blockedBalancers":["BL-MAIN"],"main":true,"mainBlocked":true,"mainDirect":false,"warming":false}`,
	}, {
		name:      "a freedom fallback on the main chain: out WITHOUT the VPN",
		balancers: []xrayview.Balancer{main("leastLoad", "DIRECT", "")},
		outbounds: []xrayview.Outbound{freedom},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       probed,
		status:    "fail", params: `{"balancers":["BL-MAIN"],"blocked":false,"blockedBalancers":[],"main":true,"mainBlocked":false,"mainDirect":true,"warming":false}`,
	}, {
		name: "a freedom fallback on a routed chain is only a fallback",
		balancers: []xrayview.Balancer{main("leastLoad", "", ""),
			bal("BL-RU", "routed", "leastLoad", []string{"ru1"}, "DIRECT", "")},
		outbounds: []xrayview.Outbound{freedom},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks("a"), "BL-RU": picks()},
		obs:       map[string]api.Observation{"a": alive, "ru1": dead},
		status:    "warn", params: `{"balancers":["BL-RU"],"blocked":false,"blockedBalancers":[],"main":false,"mainBlocked":false,"mainDirect":false,"warming":false}`,
	}, {
		name:      "no fallback: xray's default outbound, the config's first, takes it",
		balancers: []xrayview.Balancer{main("leastLoad", "", "")},
		def:       &xrayview.Outbound{Tag: "bridge-pl5", Protocol: "vless", Dials: true},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       probed,
		status:    "warn", params: `{"balancers":["BL-MAIN"],"blocked":false,"blockedBalancers":[],"main":true,"mainBlocked":false,"mainDirect":false,"warming":false}`,
	}, {
		name:      "no fallback, and the first outbound is freedom",
		balancers: []xrayview.Balancer{main("leastLoad", "", "")},
		def:       &freedom,
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       probed,
		status:    "fail", params: `{"balancers":["BL-MAIN"],"blocked":false,"blockedBalancers":[],"main":true,"mainBlocked":false,"mainDirect":true,"warming":false}`,
	}, {
		name:      "no fallback and no outbound at all",
		balancers: []xrayview.Balancer{main("leastLoad", "", "")},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       probed,
		status:    "fail", params: `{"balancers":["BL-MAIN"],"blocked":true,"blockedBalancers":["BL-MAIN"],"main":true,"mainBlocked":true,"mainDirect":false,"warming":false}`,
	}, {
		name: "a loop none of which carries",
		balancers: []xrayview.Balancer{main("leastLoad", "stage-b", "BL-B"),
			bal("BL-B", "reserve", "leastLoad", []string{"c"}, "stage-main", "BL-MAIN")},
		info:   map[string]api.BalancerInfo{"BL-MAIN": picks(), "BL-B": picks()},
		obs:    probed,
		status: "fail", params: `{"balancers":["BL-MAIN","BL-B"],"blocked":true,"blockedBalancers":["BL-MAIN"],"main":true,"mainBlocked":true,"mainDirect":false,"warming":false}`,
	}, {
		name: "xray refused a link: nothing past it is judged",
		balancers: []xrayview.Balancer{main("leastLoad", "stage-backup", "BL-BACKUP"), backup,
			bal("BL-WL", "reserve", "leastPing", []string{"w"}, "", "")},
		info:   map[string]api.BalancerInfo{"BL-MAIN": picks(), "BL-BACKUP": {Err: errors.New("refused")}, "BL-WL": picks()},
		obs:    probed,
		status: "warn", params: `{"balancers":["BL-MAIN"],"blocked":false,"blockedBalancers":[],"main":true,"mainBlocked":false,"mainDirect":false,"warming":false}`,
	}, {
		name: "the main chain first, then routed chains in config order, each balancer once",
		balancers: []xrayview.Balancer{
			bal("BL-RU", "routed", "leastLoad", []string{"ru1"}, "stage-main", "BL-MAIN"),
			main("leastLoad", "gone", ""),
			bal("BL-TK", "routed", "leastPing", []string{"tk1"}, "stage-main", "BL-MAIN"),
			bal("BL-OLD", "unused", "leastLoad", nil, "", ""),
		},
		info:   map[string]api.BalancerInfo{"BL-RU": picks(), "BL-MAIN": picks(), "BL-TK": picks(), "BL-OLD": picks()},
		obs:    probed,
		status: "fail", params: `{"balancers":["BL-MAIN","BL-RU","BL-TK"],"blocked":true,"blockedBalancers":["BL-MAIN","BL-RU","BL-TK"],"main":true,"mainBlocked":true,"mainDirect":false,"warming":false}`,
	}, {
		name:      "right after an xray start the observatory has probed no main member yet",
		balancers: []xrayview.Balancer{main("leastLoad", "gone", "")},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       map[string]api.Observation{"c": alive},
		status:    "unknown", params: `{"balancers":[],"blocked":false,"blockedBalancers":[],"main":false,"mainBlocked":false,"mainDirect":false,"warming":true}`,
	}, {
		name:      "no main balancer: the routed chains alone",
		balancers: []xrayview.Balancer{bal("BL-RU", "routed", "leastLoad", []string{"ru1"}, "node-z", "")},
		outbounds: []xrayview.Outbound{node("node-z")},
		info:      map[string]api.BalancerInfo{"BL-RU": picks()},
		obs:       map[string]api.Observation{},
		status:    "warn", params: `{"balancers":["BL-RU"],"blocked":false,"blockedBalancers":[],"main":false,"mainBlocked":false,"mainDirect":false,"warming":false}`,
	}, {
		name:      "a strategy the router does not know: cannot tell",
		balancers: []xrayview.Balancer{main("fastestEver", "gone", "")},
		info:      map[string]api.BalancerInfo{"BL-MAIN": picks()},
		obs:       probed,
		status:    "ok", params: okFallback,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			v := &xrayview.View{Balancers: tc.balancers, Outbounds: tc.outbounds, Default: tc.def}
			in := Inputs{Now: time.Now(), View: v, Balancer: tc.info, Metrics: &api.Metrics{Observatory: tc.obs}}
			c := check(t, BuildDiagnostics(in), "balancer_fallback")
			if c == nil {
				t.Fatal("no balancer_fallback check")
			}
			if got := paramsJSON(t, c); c.Status != tc.status || got != tc.params {
				t.Fatalf("balancer_fallback = %s %s\n                   want %s %s", c.Status, got, tc.status, tc.params)
			}
		})
	}
}

// Without the observatory the router cannot tell a random balancer's live
// members from its dead ones, nor a warm-up from a failure: no verdict.
func TestBalancerFallbackNeedsTheObservatory(t *testing.T) {
	v := &xrayview.View{Balancers: []xrayview.Balancer{bal("BL-MAIN", "main", "leastLoad", []string{"a"}, "", "")}}
	in := Inputs{Now: time.Now(), View: v, Balancer: map[string]api.BalancerInfo{"BL-MAIN": picks()}}
	if c := check(t, BuildDiagnostics(in), "balancer_fallback"); c != nil {
		t.Fatalf("judged without the observatory: %+v", c)
	}
}

func TestBalancerFallbackListsAtMostTen(t *testing.T) {
	bs := []xrayview.Balancer{bal("BL-MAIN", "main", "leastLoad", []string{"a"}, "", "")}
	info := map[string]api.BalancerInfo{"BL-MAIN": picks("a")}
	for i := 0; i < 12; i++ {
		tag := fmt.Sprintf("BL-R%02d", i)
		bs = append(bs, bal(tag, "routed", "leastLoad", []string{"r"}, "stage-main", "BL-MAIN"))
		info[tag] = picks()
	}
	in := Inputs{Now: time.Now(), View: &xrayview.View{Balancers: bs}, Balancer: info,
		Metrics: &api.Metrics{Observatory: map[string]api.Observation{"a": alive}}}
	c := check(t, BuildDiagnostics(in), "balancer_fallback")
	if c == nil || c.Status != "warn" || len(c.Params["balancers"].([]string)) != 10 {
		t.Fatalf("balancer_fallback = %+v", c)
	}
}

func TestPinnedNodeDeadFailsOnTheMainBalancer(t *testing.T) {
	v := &xrayview.View{Balancers: []xrayview.Balancer{
		bal("BL-A", "routed", "leastLoad", []string{"a"}, "", ""),
		bal("BL-MAIN", "main", "leastLoad", []string{"m"}, "", ""),
	}}
	obs := map[string]api.Observation{"a": dead, "m": dead, "ok": alive}
	for _, tc := range []struct {
		name   string
		pins   map[string]string
		view   *xrayview.View
		status string
		params string
	}{
		{"a dead pin on a routed balancer", map[string]string{"BL-A": "a"}, v, "warn", `{"balancer":"BL-A","main":false,"node":"a"}`},
		{"a dead pin on the main balancer", map[string]string{"BL-MAIN": "m"}, v, "fail", `{"balancer":"BL-MAIN","main":true,"node":"m"}`},
		{"both: the main one is reported", map[string]string{"BL-A": "a", "BL-MAIN": "m"}, v, "fail", `{"balancer":"BL-MAIN","main":true,"node":"m"}`},
		{"a live pin", map[string]string{"BL-MAIN": "ok"}, v, "ok", `{}`},
		{"a pin the observatory has no record of", map[string]string{"BL-MAIN": "never-probed"}, v, "ok", `{}`},
		{"no config to tell the main balancer by", map[string]string{"BL-MAIN": "m"}, nil, "warn", `{"balancer":"BL-MAIN","main":false,"node":"m"}`},
	} {
		in := Inputs{Now: time.Now(), View: tc.view, Metrics: &api.Metrics{Observatory: obs}, Overrides: localctl.Overrides{Pins: tc.pins}}
		c := check(t, BuildDiagnostics(in), "pinned_node_dead")
		if c == nil {
			t.Fatalf("%s: no pinned_node_dead check", tc.name)
		}
		if got := paramsJSON(t, c); c.Status != tc.status || got != tc.params {
			t.Errorf("%s: pinned_node_dead = %s %s, want %s %s", tc.name, c.Status, got, tc.status, tc.params)
		}
	}
}

func TestNoLeakJudgesOnlyWhatGotPastARunningXray(t *testing.T) {
	base := func(b localctl.LeakBaseline) *localctl.Runtime {
		b.At, b.XrayPID = time.Now(), 7
		return &localctl.Runtime{LeakBaseline: &b}
	}
	// A loaded shadow ruleset (switch off), after some uptime.
	off := func(leak, escaped, other, hits int64) map[string]int64 {
		return map[string]int64{"vctl_would_leak": leak, "vctl_tproxy_escaped": escaped, "vctl_unproxied_other": other, "vctl_tproxy_hits": hits}
	}
	on := func(drops, escaped, other, hits int64) map[string]int64 {
		return map[string]int64{"vctl_killswitch_drops": drops, "vctl_tproxy_escaped": escaped, "vctl_unproxied_other": other, "vctl_tproxy_hits": hits}
	}
	for _, tc := range []struct {
		name     string
		counters map[string]int64
		rt       *localctl.Runtime
		status   string
		params   string
	}{
		{"nothing leaked, no baseline yet", off(0, 0, 3, 100), &localctl.Runtime{},
			"ok", `{"escaped":null,"killSwitch":false,"other":3,"sinceStart":null,"wouldLeak":0}`},
		{"leaked, and no baseline to tell which kind", off(5, 0, 0, 100), &localctl.Runtime{},
			"unknown", `{"escaped":null,"killSwitch":false,"other":0,"sinceStart":null,"wouldLeak":5}`},
		{"escaped, and no baseline", off(0, 2, 0, 100), nil,
			"unknown", `{"escaped":null,"killSwitch":false,"other":0,"sinceStart":null,"wouldLeak":0}`},
		{"only the restart windows leaked", off(5, 0, 9, 500), base(localctl.LeakBaseline{Packets: 5, TproxyHits: 100}),
			"ok", `{"escaped":0,"killSwitch":false,"other":9,"sinceStart":0,"wouldLeak":5}`},
		{"a trickle got past a running xray", off(8, 0, 0, 500), base(localctl.LeakBaseline{Packets: 5, TproxyHits: 100}),
			"warn", `{"escaped":0,"killSwitch":false,"other":0,"sinceStart":3,"wouldLeak":8}`},
		{"49 is still a trickle", off(54, 0, 0, 500), base(localctl.LeakBaseline{Packets: 5, TproxyHits: 100}),
			"warn", `{"escaped":0,"killSwitch":false,"other":0,"sinceStart":49,"wouldLeak":54}`},
		{"50 got past a running xray", off(55, 0, 0, 500), base(localctl.LeakBaseline{Packets: 5, TproxyHits: 100}),
			"fail", `{"escaped":0,"killSwitch":false,"other":0,"sinceStart":50,"wouldLeak":55}`},
		{"one packet escaped the policy route", off(6, 1, 0, 500), base(localctl.LeakBaseline{Packets: 5, TproxyHits: 100}),
			"fail", `{"escaped":1,"killSwitch":false,"other":0,"sinceStart":1,"wouldLeak":6}`},
		{"a ping is information, not a leak", off(0, 0, 400, 500), base(localctl.LeakBaseline{TproxyHits: 100}),
			"ok", `{"escaped":0,"killSwitch":false,"other":400,"sinceStart":0,"wouldLeak":0}`},
		{"reloaded since: tproxy_hits went down", off(4, 0, 0, 30), base(localctl.LeakBaseline{Packets: 1, TproxyHits: 100}),
			"warn", `{"escaped":0,"killSwitch":false,"other":0,"sinceStart":4,"wouldLeak":4}`},
		{"reloaded since: a counter below its baseline", off(2, 0, 0, 900), base(localctl.LeakBaseline{Packets: 9, TproxyHits: 100}),
			"warn", `{"escaped":0,"killSwitch":false,"other":0,"sinceStart":2,"wouldLeak":2}`},
		{"a ruleset from before the escape counter", map[string]int64{"vctl_would_leak": 0, "vctl_tproxy_hits": 9},
			base(localctl.LeakBaseline{TproxyHits: 1}),
			"ok", `{"escaped":null,"killSwitch":false,"other":null,"sinceStart":0,"wouldLeak":0}`},
		{"kill switch: drops before the start are the restart", on(40, 0, 7, 500), base(localctl.LeakBaseline{Drops: 40, TproxyHits: 100}),
			"ok", `{"drops":40,"dropsSinceStart":0,"escaped":0,"killSwitch":true,"other":7,"sinceStart":0,"wouldLeak":0}`},
		{"kill switch: drops while xray runs", on(50, 0, 0, 500), base(localctl.LeakBaseline{Drops: 40, TproxyHits: 100}),
			"warn", `{"drops":50,"dropsSinceStart":10,"escaped":0,"killSwitch":true,"other":0,"sinceStart":0,"wouldLeak":0}`},
		{"kill switch: 50 dropped while xray runs", on(90, 0, 0, 500), base(localctl.LeakBaseline{Drops: 40, TproxyHits: 100}),
			"fail", `{"drops":90,"dropsSinceStart":50,"escaped":0,"killSwitch":true,"other":0,"sinceStart":0,"wouldLeak":0}`},
		{"kill switch: escaped and dropped", on(41, 1, 0, 500), base(localctl.LeakBaseline{Drops: 40, TproxyHits: 100}),
			"fail", `{"drops":41,"dropsSinceStart":1,"escaped":1,"killSwitch":true,"other":0,"sinceStart":0,"wouldLeak":0}`},
		{"kill switch: drops and no baseline", on(3, 0, 0, 500), &localctl.Runtime{},
			"unknown", `{"drops":3,"dropsSinceStart":null,"escaped":null,"killSwitch":true,"other":0,"sinceStart":null,"wouldLeak":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := check(t, BuildDiagnostics(Inputs{Now: time.Now(), Counters: tc.counters, TableLoaded: true, Runtime: tc.rt}), "no_leak")
			if c == nil {
				t.Fatal("no no_leak check")
			}
			if got := paramsJSON(t, c); c.Status != tc.status || got != tc.params {
				t.Fatalf("no_leak = %s %s\n          want %s %s", c.Status, got, tc.status, tc.params)
			}
		})
	}
	// The table could not be read: no verdict at all, not a clean one.
	if c := check(t, BuildDiagnostics(Inputs{Now: time.Now()}), "no_leak"); c != nil {
		t.Fatalf("no_leak without counters = %+v", c)
	}
}

// A 234 MB router lives at 40-65 MB free under load — the fleet did under
// PassWall2. Only the kernel reclaiming running programs' pages is a failure
// (memguard.CriticalKB: 5% of RAM, at least 12 MiB), twice that a warning
// (1111, 2026-09-30, the owner: «при 50 мб пишет — мол всё не работает»).
func TestMemoryFailsOnlyWhereTheKernelReclaims(t *testing.T) {
	total := 234
	for avail, want := range map[int]string{11: "fail", 12: "warn", 23: "warn", 24: "ok", 40: "ok", 50: "ok", 88: "ok"} {
		a := avail
		c := check(t, BuildDiagnostics(Inputs{Now: time.Now(), Router: RouterFacts{MemAvailableMiB: &a, MemTotalMiB: &total}}), "memory")
		if c == nil || c.Status != want {
			t.Errorf("memory with %d of %d MiB available = %+v, want %s", avail, total, c, want)
		}
	}
	// A larger router: 5% of it.
	big, a := 1024, 40
	if c := check(t, BuildDiagnostics(Inputs{Now: time.Now(), Router: RouterFacts{MemAvailableMiB: &a, MemTotalMiB: &big}}), "memory"); c == nil || c.Status != "fail" {
		t.Errorf("40 of 1024 MiB = %+v, want fail (critical 51)", c)
	}
}

func TestStatusSaysWhetherTheUIIsLocked(t *testing.T) {
	for _, locked := range []bool{true, false} {
		st := BuildStatus(Inputs{Now: time.Now(), UILocked: locked})
		b, _ := json.Marshal(st)
		if want := fmt.Sprintf(`"ui":{"locked":%v}`, locked); st.UI.Locked != locked || !strings.Contains(string(b), want) {
			t.Errorf("status with the lock %v = %s", locked, b)
		}
	}
}

// The server card's line (spec decision 6): which countries the main
// traffic goes through now — the watchdog's move when it made one — and the
// node it moved off.
func TestStatusSaysWhereTheMainTrafficGoesAndWhatWasMovedOff(t *testing.T) {
	at := time.Unix(1790000000, 0).UTC()
	rt := &localctl.Runtime{Route: &localctl.Route{Balancer: "BL-MAIN", Nodes: []string{"bridge-fin5", "bridge-tr5"},
		Override: "sticky-by5", MovedFrom: "sticky-de5", MovedAt: &at, Reason: "borrowed"}}
	st := BuildStatus(Inputs{Now: at.Add(time.Minute), Runtime: rt})
	if st.Route == nil || len(st.Route.Nodes) != 1 || st.Route.Nodes[0].Tag != "sticky-by5" || *st.Route.Nodes[0].Country != "BY" ||
		st.Route.MovedFrom == nil || st.Route.MovedFrom.Tag != "sticky-de5" || *st.Route.MovedFrom.Country != "DE" ||
		st.Route.Reason == nil || *st.Route.Reason != "borrowed" || st.Route.MovedAt == nil {
		b, _ := json.Marshal(st.Route)
		t.Fatalf("route %s", b)
	}
	rt.Route = &localctl.Route{Balancer: "BL-MAIN", Nodes: []string{"bridge-fin5", "bridge-tr5"}}
	st = BuildStatus(Inputs{Now: at, Runtime: rt})
	if st.Route == nil || len(st.Route.Nodes) != 2 || *st.Route.Nodes[0].Country != "FI" || *st.Route.Nodes[1].Country != "TR" || st.Route.MovedFrom != nil {
		b, _ := json.Marshal(st.Route)
		t.Fatalf("route %s", b)
	}
	if st := BuildStatus(Inputs{Now: at, Runtime: &localctl.Runtime{}}); st.Route != nil {
		t.Fatal("a route with no word from the watchdog")
	}
}

// The exits the router's exit check left out (1111, 2026-09-30: the American
// one carried no blocked site) are named on the card with their countries;
// an empty list, never null, when there are none.
func TestStatusNamesTheExitsLeftOutForBlockedSites(t *testing.T) {
	rt := &localctl.Runtime{Route: &localctl.Route{Balancer: "BL-MAIN", Nodes: []string{"bridge-fin5"}, Unfit: []string{"bridge-us5", "stage-x"}}}
	st := BuildStatus(Inputs{Now: time.Now(), Runtime: rt})
	if st.Route == nil || len(st.Route.Unfit) != 2 || st.Route.Unfit[0].Tag != "bridge-us5" || *st.Route.Unfit[0].Country != "US" || st.Route.Unfit[1].Country != nil {
		b, _ := json.Marshal(st.Route)
		t.Fatalf("route %s", b)
	}
	rt.Route.Unfit = nil
	b, _ := json.Marshal(BuildStatus(Inputs{Now: time.Now(), Runtime: rt}).Route)
	if !strings.Contains(string(b), `"unfit":[]`) {
		t.Fatalf("route %s", b)
	}
}

// 1111, 2026-09-30: the owner — «он ругается что белые списки не доступны,
// так они и не работают с вайфай». Whitelist levels are for a mobile network
// under a whitelist regime; on a home connection they do not answer, and that
// is no fault. The check names only the nodes this connection should reach.
func TestDeadNodesLeavesOutTheWhitelistLevels(t *testing.T) {
	v, err := xrayview.Parse([]byte(`{"outbounds":[
	 {"tag":"bridge-de5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.5","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
	 {"tag":"bridge-us5","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.6","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
	 {"tag":"whitelist-lv1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.7","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
	 {"tag":"whitelist-lv2-3","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.8","port":443,"users":[{"id":"00000000-0000-0000-0000-000000000000"}]}]}},
	 {"tag":"DIRECT","protocol":"freedom"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	m := &api.Metrics{Observatory: map[string]api.Observation{
		"bridge-de5": {Alive: true}, "bridge-us5": {Alive: true},
		"whitelist-lv1": {Alive: false}, "whitelist-lv2-3": {Alive: false}}}
	c := check(t, BuildDiagnostics(Inputs{Now: time.Now(), View: v, Metrics: m}), "dead_nodes")
	if c.Status != "ok" || fmt.Sprint(c.Params["count"]) != "0" {
		t.Fatalf("dead_nodes %s %v", c.Status, c.Params)
	}
	m.Observatory["bridge-us5"] = api.Observation{Alive: false}
	c = check(t, BuildDiagnostics(Inputs{Now: time.Now(), View: v, Metrics: m}), "dead_nodes")
	if c.Status != "warn" || fmt.Sprint(c.Params["tags"]) != "[bridge-us5]" {
		t.Fatalf("dead_nodes %s %v", c.Status, c.Params)
	}
}

// 1111, 2026-09-30: «Турция» and «ОАЭ» left in Poland. Where the router saw
// an exit leave elsewhere than its name says, the card says so; where it
// leaves where the name says, or is not known yet, nothing is added.
func TestTheRouteSaysWhereAnExitReallyLeaves(t *testing.T) {
	rt := &localctl.Runtime{Route: &localctl.Route{Balancer: "BL-MAIN", Nodes: []string{"bridge-ae5", "bridge-de5", "bridge-fr5"}},
		Egress: map[string]string{"bridge-ae5": "PL", "bridge-de5": "DE"}}
	st := BuildStatus(Inputs{Now: time.Now(), Runtime: rt})
	if st.Route == nil || len(st.Route.Nodes) != 3 {
		t.Fatalf("route %+v", st.Route)
	}
	ae, de, fr := st.Route.Nodes[0], st.Route.Nodes[1], st.Route.Nodes[2]
	if ae.Egress == nil || *ae.Egress != "PL" || de.Egress != nil || fr.Egress != nil {
		b, _ := json.Marshal(st.Route)
		t.Fatalf("route %s", b)
	}
}

// PassWall2 on the router (internal/retire): installed — the takeover's way
// back — retired, with when, or absent; null when it was not looked at.
func TestStatusSaysWhatBecameOfPassWall(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	st := BuildStatus(Inputs{Now: time.Now(), PassWall: "retired", PassWallRetiredAt: at})
	b, _ := json.Marshal(st.Legacy)
	if want := `{"agentEnabled":false,"passwallRunning":false,"passwall":"retired","passwallRetiredAt":"2026-10-01T12:00:00Z"}`; string(b) != want {
		t.Fatalf("legacy = %s, want %s", b, want)
	}
	b, _ = json.Marshal(BuildStatus(Inputs{Now: time.Now()}).Legacy)
	if want := `{"agentEnabled":false,"passwallRunning":false,"passwall":null,"passwallRetiredAt":null}`; string(b) != want {
		t.Fatalf("legacy = %s, want %s", b, want)
	}
}
