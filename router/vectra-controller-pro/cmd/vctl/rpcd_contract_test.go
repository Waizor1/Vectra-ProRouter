package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/sites"
	"vectra-controller-pro/internal/uiapi"
	"vectra-controller-pro/internal/xrayview"
)

// The router UI and `vctl rpcd` are two halves of one contract, in two
// languages. ui/contract/ holds a fixture per answer; the UI is built against
// those exact files, and this test holds the Go side to them.
const contractDir = "../../ui/contract"

func readContract(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(contractDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func generic(t *testing.T, raw []byte) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Every fixture decodes into its Go type with no field left over, and
// re-encodes to the same JSON: a renamed, retyped or dropped field fails here.
func TestContractFixturesRoundTripThroughTheGoTypes(t *testing.T) {
	for name, target := range map[string]interface{}{
		"status.json":      &uiapi.Status{},
		"balancers.json":   &uiapi.Balancers{},
		"nodes.json":       &uiapi.Nodes{},
		"entries.json":     &uiapi.Entries{},
		"diagnostics.json": &uiapi.Diagnostics{},
		"logs.json":        &uiapi.Logs{},
		"rules.json":       &uiapi.Rules{},
		"services.json":    &uiapi.Services{},
		"action.json":      &uiapi.Action{},
	} {
		t.Run(name, func(t *testing.T) {
			raw := readContract(t, name)
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(target); err != nil {
				t.Fatalf("the Go type does not accept the fixture: %v", err)
			}
			back, err := json.Marshal(target)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(generic(t, raw), generic(t, back)) {
				t.Fatalf("re-encoded differently:\nfixture: %s\ngo:      %s", compactJSON(t, raw), back)
			}
		})
	}
}

// contractInputs are realistic inputs built from the REAL provider document:
// spliced exactly as the daemon renders it, with a plausible observation.
func contractInputs(t *testing.T) uiapi.Inputs {
	t.Helper()
	provider := providerEntry(t)
	opts := xray.SpliceOptions{APIListen: xray.DefaultAPIListen, MetricsListen: xray.DefaultMetricsListen, ProbeInterval: 10 * time.Minute}
	tproxy := &config.TproxyInbound{ListenIP: "0.0.0.0", Port: 12345, FwMark: 1, UDPEnabled: true, Tag: "tproxy-in",
		Sniffing: config.Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}}}
	rendered, _, err := xray.Splice(provider, tproxy, opts)
	if err != nil {
		t.Fatal(err)
	}
	view, err := xrayview.Parse(rendered)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	started := now.Add(-10 * time.Hour)
	checkIn := now.Add(-30 * time.Second)
	reach := true
	m := &api.Metrics{Observatory: map[string]api.Observation{
		"bridge-de5": {Alive: true, DelayMs: 96, HealthPing: &api.HealthPing{All: 2, Average: 96e6}},
		"bridge-nl5": {Alive: true, DelayMs: 112},
		"bridge-fr5": {Alive: false, HealthPing: &api.HealthPing{All: 2, Fail: 2}},
	}}
	m.Stats.Outbound = map[string]api.Traffic{"bridge-de5": {Uplink: 10, Downlink: 20}}
	mem, avail, total := 61.4, 88, 234
	limit := 80
	return uiapi.Inputs{
		Now: now, Version: "0.4.0-r1",
		Runtime: &localctl.Runtime{
			ControllerPID: 3012, StartedAt: started, Version: "0.4.0-r1", XrayVersion: "26.3.27", RouterID: "r-1",
			LastCheckIn: &checkIn, PanelReachable: &reach,
			Engine: localctl.Engine{State: "running", PID: 4127, StartedAt: started, Restarts: 1,
				LastExitAt: started.Add(-time.Minute), LastExitCode: -1, LastExitErr: "signal: killed"},
			Entry:        &localctl.Entry{Index: 0, Remark: "🇷🇺🇪🇺 Авто Самый стабильный", Count: 26, FetchedAt: now.Add(-12 * time.Hour)},
			Probe:        &localctl.Probe{IntervalSec: 600, ProviderIntervalSec: 43200, Sampling: 2, Source: "default"},
			LeakBaseline: &localctl.LeakBaseline{Packets: 0, At: started.Add(3 * time.Second), XrayPID: 4127},
		},
		View: view, APIReachable: true, MetricsReachable: true, Metrics: m,
		Balancer: map[string]api.BalancerInfo{
			"BL-MAIN":   {Principle: []string{"bridge-de5", "bridge-nl5"}, Override: "bridge-nl5"},
			"BL-WL-LV1": {Principle: nil},
			"BL-GONE":   {Err: fmt.Errorf("x")},
		},
		Overrides: localctl.Overrides{Pins: map[string]string{"BL-MAIN": "bridge-nl5"},
			Direct: []string{"sberbank.ru", "госуслуги.рф", "1.2.3.0/24"}, Proxy: []string{"example.org"}},
		Index: &localctl.EntriesIndex{FetchedAt: now.Add(-12 * time.Hour), Entries: []localctl.EntrySummary{
			{Index: 0, Remark: "🇷🇺🇪🇺 Авто Самый стабильный", NodeCount: 22, BalancerCount: 7},
			{Index: 1, Remark: "🇩🇪 Германия", NodeCount: 4, BalancerCount: 2},
		}},
		HasOperatorConfig: true,
		// A fleet router Vectra took from PassWall2: on, running, PassWall owed.
		Power:       power.Facts{UCI: true, Boot: true, Running: true, Carrying: true, Owed: power.PassWall},
		TableLoaded: true, Counters: map[string]int64{"vctl_tproxy_hits": 5, "vctl_would_leak": 0, "vctl_tproxy_escaped": 0, "vctl_unproxied_other": 12},
		Router: uiapi.RouterFacts{Hostname: "r", Model: "Xiaomi Mi Router AX3000T", Release: "OpenWrt 24.10.6",
			MemTotalMiB: &total, MemAvailableMiB: &avail, Load: []float64{0.3, 0.2, 0.1}},
		XrayRSSMiB: &mem, MemoryLimitMiB: &limit,
	}
}

// The answers built from real inputs have the fixtures' shape: the same keys
// at every level. Values may be null where the contract allows it.
func TestBuiltAnswersHaveTheContractShape(t *testing.T) {
	in := contractInputs(t)
	for name, built := range map[string]interface{}{
		"status.json":      uiapi.BuildStatus(in),
		"balancers.json":   uiapi.BuildBalancers(in),
		"nodes.json":       uiapi.BuildNodes(in),
		"entries.json":     uiapi.BuildEntries(in),
		"diagnostics.json": uiapi.BuildDiagnostics(in),
		"rules.json":       uiapi.BuildRules(in.Overrides, []string{"discord", "meta"}),
		"services.json":    uiapi.BuildServices(true, []byte(svcRunning), localctl.Overrides{Services: map[string]string{"tiktok": "DE"}}, nil),
		"action.json":      action(true, "balancer_pinned", ""),
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(built)
			if err != nil {
				t.Fatal(err)
			}
			var problems []string
			sameShape("", generic(t, readContract(t, name)), generic(t, raw), &problems)
			if len(problems) > 0 {
				t.Fatalf("shape differs from the fixture:\n  %s", strings.Join(problems, "\n  "))
			}
		})
	}
}

// Dynamic-key maps: their keys are data, not shape.
var dynamicMaps = map[string]bool{".pins": true, ".dataplane.counters": true, ".checks[].params": true, ".wifi.apply.radios": true}

func sameShape(path string, want, got interface{}, problems *[]string) {
	if want == nil || got == nil {
		return
	}
	switch w := want.(type) {
	case map[string]interface{}:
		g, ok := got.(map[string]interface{})
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s: want an object, got %T", path, got))
			return
		}
		if dynamicMaps[path] {
			return
		}
		for k := range w {
			if _, ok := g[k]; !ok {
				*problems = append(*problems, fmt.Sprintf("%s.%s: missing", path, k))
			}
		}
		for k := range g {
			if _, ok := w[k]; !ok {
				*problems = append(*problems, fmt.Sprintf("%s.%s: not in the contract", path, k))
			}
		}
		for k := range w {
			sameShape(path+"."+k, w[k], g[k], problems)
		}
	case []interface{}:
		g, ok := got.([]interface{})
		if !ok {
			*problems = append(*problems, fmt.Sprintf("%s: want an array, got %T", path, got))
			return
		}
		if len(w) > 0 {
			for _, e := range g {
				sameShape(path+"[]", w[0], e, problems)
			}
		}
	default:
		if reflect.TypeOf(want) != reflect.TypeOf(got) {
			*problems = append(*problems, fmt.Sprintf("%s: want %T, got %T", path, want, got))
		}
	}
}

// The UI owns a sentence for every diagnostic id and action code, and learns
// them from the contract README. Anything the router can emit must be there.
func TestEveryCodeTheRouterEmitsIsInTheContract(t *testing.T) {
	readme := string(readContract(t, "README.md"))
	in := contractInputs(t)
	down := uiapi.Inputs{Now: in.Now, Router: in.Router, HasOperatorConfig: true, UserAgentProblem: "malformed_happ"}
	ids := map[string]bool{}
	for _, d := range []uiapi.Diagnostics{uiapi.BuildDiagnostics(in), uiapi.BuildDiagnostics(down)} {
		for _, c := range d.Checks {
			ids[c.ID] = true
		}
	}
	for id := range ids {
		if !strings.Contains(readme, "| `"+id+"` |") {
			t.Errorf("diagnostic id %q is not in the contract's Diagnostics table", id)
		}
	}
	src, err := os.ReadFile("cmd_rpcd.go")
	if err != nil {
		t.Fatal(err)
	}
	powerSrc, err := os.ReadFile("rpcd_power.go")
	if err != nil {
		t.Fatal(err)
	}
	src = append(src, powerSrc...)
	daemonSrc, _ := os.ReadFile("localui.go")
	codeRe := regexp.MustCompile(`action\((?:true|false), "([a-z_]+)"|Code: "([a-z_]+)"|okCode = "([a-z_]+)"|localctl\.Op\w+, "([a-z_]+)"`)
	codes := map[string]bool{}
	for _, m := range codeRe.FindAllStringSubmatch(string(src)+string(daemonSrc), -1) {
		for _, c := range m[1:] {
			if c != "" {
				codes[c] = true
			}
		}
	}
	if len(codes) < 10 {
		t.Fatalf("found only %d codes; the scan is not looking where they are", len(codes))
	}
	var missing []string
	for c := range codes {
		if !strings.Contains(readme, "`"+c+"`") {
			missing = append(missing, c)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("codes the router emits but the contract does not list: %v", missing)
	}
}

// No answer carries a credential from the provider document.
func TestAnswersCarryNoCredentials(t *testing.T) {
	in := contractInputs(t)
	var all bytes.Buffer
	for _, v := range []interface{}{uiapi.BuildStatus(in), uiapi.BuildBalancers(in), uiapi.BuildNodes(in),
		uiapi.BuildEntries(in), uiapi.BuildDiagnostics(in)} {
		b, _ := json.Marshal(v)
		all.Write(b)
	}
	var doc interface{}
	_ = json.Unmarshal(providerEntry(t), &doc)
	var secrets []string
	var walk func(x interface{})
	walk = func(x interface{}) {
		switch y := x.(type) {
		case map[string]interface{}:
			for k, v := range y {
				if s, ok := v.(string); ok && len(s) >= 6 && (k == "id" || k == "password" || k == "publicKey" || k == "shortId" || k == "auth") {
					secrets = append(secrets, s)
				}
				walk(v)
			}
		case []interface{}:
			for _, e := range y {
				walk(e)
			}
		}
	}
	walk(doc)
	if len(secrets) < 10 {
		t.Fatalf("found only %d credentials in the fixture; the scan is broken", len(secrets))
	}
	for _, s := range secrets {
		if bytes.Contains(all.Bytes(), []byte(s)) {
			t.Fatalf("an answer carries a credential (%d chars)", len(s))
		}
	}
}

func TestBuiltAnswersSayWhatTheInputsSay(t *testing.T) {
	in := contractInputs(t)
	b := uiapi.BuildBalancers(in)
	var main *uiapi.Balancer
	for i := range b.Balancers {
		if b.Balancers[i].Tag == "BL-MAIN" {
			main = &b.Balancers[i]
		}
	}
	if main == nil || main.Role != "main" || main.Pinned == nil || *main.Pinned != "bridge-nl5" || len(main.Selected) != 2 {
		t.Fatalf("BL-MAIN = %+v", main)
	}
	if b.Probe.IntervalSec == nil || *b.Probe.IntervalSec != 600 || *b.Probe.ProviderIntervalSec != 43200 || b.Probe.Source != "default" {
		t.Fatalf("probe = %+v", b.Probe)
	}
	// xray's default outbound is the rendered config's first: the provider's.
	if b.DefaultOutbound == nil || *b.DefaultOutbound != "bridge-pl5" {
		t.Fatalf("defaultOutbound = %v, want bridge-pl5", b.DefaultOutbound)
	}
	if nb := uiapi.BuildBalancers(uiapi.Inputs{Now: in.Now}); nb.DefaultOutbound != nil {
		t.Errorf("no render, defaultOutbound = %q", *nb.DefaultOutbound)
	}

	nodes := uiapi.BuildNodes(in)
	byTag := map[string]uiapi.Node{}
	for _, n := range nodes.Nodes {
		byTag[n.Tag] = n
	}
	if len(nodes.Nodes) != 22 {
		t.Errorf("%d nodes, want 22", len(nodes.Nodes))
	}
	if n := byTag["bridge-de5"]; n.Alive == nil || !*n.Alive || *n.DelayMs != 96 || n.Traffic == nil || *n.CountryHint != "DE" {
		t.Errorf("bridge-de5 = %+v", n)
	}
	if n := byTag["bridge-fr5"]; n.Alive == nil || *n.Alive || n.DelayMs != nil {
		t.Errorf("dead bridge-fr5 = %+v", n)
	}
	// Never probed: null, not "dead", not 0 ms.
	if n := byTag["bridge-pl5"]; n.Alive != nil || n.DelayMs != nil {
		t.Errorf("unprobed bridge-pl5 = %+v", n)
	}
	if n := byTag["whitelist-lv3"]; n.CountryHint != nil {
		t.Errorf("whitelist-lv3 got a flag: %v", *n.CountryHint)
	}

	d := uiapi.BuildDiagnostics(in)
	status := map[string]string{}
	for _, c := range d.Checks {
		status[c.ID] = c.Status
	}
	// BL-WL-LV1 has no targets, but it is a reserve that no traffic reaches:
	// BL-MAIN carries everything before it. Nothing is on a fallback.
	for id, want := range map[string]string{"xray_running": "ok", "dead_nodes": "warn", "balancer_fallback": "ok", "memory": "ok", "panel_link": "ok", "no_leak": "ok"} {
		if status[id] != want {
			t.Errorf("%s = %q, want %q", id, status[id], want)
		}
	}
	if d.Checks[0].Status != "warn" {
		t.Errorf("checks are not ordered worst first: %+v", d.Checks[0])
	}

	// The daemon down: an honest answer, not a crash and not zeros.
	down := uiapi.BuildStatus(uiapi.Inputs{Now: in.Now})
	if down.Controller.Running || down.Engine.State != "unknown" || down.Engine.PID != nil || down.ControlPlane.Reachable != nil {
		t.Errorf("down = %+v", down)
	}
	if e := uiapi.BuildEntries(uiapi.Inputs{Now: in.Now}); e.Cached || len(e.Entries) != 0 || e.Active != nil {
		t.Errorf("no cache = %+v", e)
	}
}

// A check's params are a free-form map, so the shape test cannot see a renamed
// or dropped key. For the checks the UI reads field by field, the router's
// answer to the realistic inputs must be the fixture, status and params.
func TestDiagnosticParamsTheUIReadsAreTheFixtures(t *testing.T) {
	var fixture uiapi.Diagnostics
	if err := json.Unmarshal(readContract(t, "diagnostics.json"), &fixture); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, c := range fixture.Checks {
		b, _ := json.Marshal(c.Params)
		want[c.ID] = c.Status + " " + string(b)
	}
	seen := 0
	for _, c := range uiapi.BuildDiagnostics(contractInputs(t)).Checks {
		if c.ID != "no_leak" && c.ID != "balancer_fallback" {
			continue
		}
		seen++
		b, _ := json.Marshal(c.Params)
		if got := c.Status + " " + string(b); got != want[c.ID] {
			t.Errorf("%s:\n  router  %s\n  fixture %s", c.ID, got, want[c.ID])
		}
	}
	if seen != 2 {
		t.Fatalf("built %d of the 2 checks", seen)
	}
}

// The rules fixture is what the router answers for those lists, and every
// entry in it is already in the form the router keeps: the UI is never built
// against a site the router would have written differently.
func TestRulesFixtureIsWhatTheRouterKeeps(t *testing.T) {
	var fixture uiapi.Rules
	if err := json.Unmarshal(readContract(t, "rules.json"), &fixture); err != nil {
		t.Fatal(err)
	}
	d, p, err := sites.NormalizeLists(fixture.Direct, fixture.Proxy)
	if err != nil {
		t.Fatal(err)
	}
	if built := uiapi.BuildRules(localctl.Overrides{Direct: d, Proxy: p}, fixture.Catalog); !reflect.DeepEqual(built, fixture) {
		t.Fatalf("router  %+v\nfixture %+v", built, fixture)
	}
	// No sites: empty lists, never null.
	raw, _ := json.Marshal(uiapi.BuildRules(localctl.Overrides{}, nil))
	if string(raw) != `{"direct":[],"proxy":[],"max":100,"catalog":[],"missing":[]}` {
		t.Errorf("no sites = %s", raw)
	}
}

func compactJSON(t *testing.T, raw []byte) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// The ACL must grant exactly the methods `vctl rpcd list` offers — reads in
// "read", changes in "write" — or LuCI gets "Access denied" for a method that
// exists, or can call one nobody meant to expose.
func TestRPCDACLMatchesTheMethods(t *testing.T) {
	raw, err := os.ReadFile("../../openwrt/files/usr/share/rpcd/acl.d/vectra-controller-pro.json")
	if err != nil {
		t.Fatal(err)
	}
	var acl map[string]struct {
		Read  struct{ Ubus map[string][]string } `json:"read"`
		Write struct{ Ubus map[string][]string } `json:"write"`
	}
	if err := json.Unmarshal(raw, &acl); err != nil {
		t.Fatal(err)
	}
	g := acl["vectra-controller-pro"]
	reads := map[string]bool{"status": true, "balancers": true, "nodes": true, "entries": true, "diagnostics": true, "logs": true, "rules": true, "services": true}
	reads["setup"], reads["wan_check"], reads["wifi_scan"] = true, true, true // the setup wizard's reads
	granted := map[string]string{}
	for _, m := range g.Read.Ubus["vectra"] {
		granted[m] = "read"
	}
	for _, m := range g.Write.Ubus["vectra"] {
		granted[m] = "write"
	}
	for m := range rpcdSignatures {
		want := "write"
		if reads[m] {
			want = "read"
		}
		if granted[m] != want {
			t.Errorf("method %s: ACL grants %q, want %q", m, granted[m], want)
		}
	}
	if len(granted) != len(rpcdSignatures) {
		t.Errorf("ACL lists %d methods, rpcd offers %d", len(granted), len(rpcdSignatures))
	}
	// And the contract README documents the same set.
	readme := string(readContract(t, "README.md"))
	for m := range rpcdSignatures {
		if !strings.Contains(readme, "| `"+m+"` |") {
			t.Errorf("method %s is not in the contract README", m)
		}
	}
}

// The firewall guards exactly the loopback ports the splice gives xray's API,
// metrics and exit probe; a port changed in one place only would leave it
// unguarded — the probe is an HTTP proxy onto every exit, and a LAN request
// xray is tricked into dialling on loopback must never reach it.
func TestTheFirewallGuardsTheSplicedControlPorts(t *testing.T) {
	guarded := map[int]bool{}
	for _, p := range firewall.DefaultSpec(12345, 1).GuardPorts {
		guarded[p] = true
	}
	for _, listen := range []string{xray.DefaultAPIListen, xray.DefaultMetricsListen, xray.DefaultExitProbeListen} {
		_, port, err := net.SplitHostPort(listen)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := strconv.Atoi(port)
		if !guarded[n] {
			t.Errorf("%s is spliced but port %d is not in firewall.DefaultSpec().GuardPorts", listen, n)
		}
	}
}
