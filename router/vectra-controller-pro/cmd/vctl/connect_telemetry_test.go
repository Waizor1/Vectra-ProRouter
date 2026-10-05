package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/connecttelemetry"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/uiapi"
	"vectra-controller-pro/internal/xrayview"
)

func measuredConnectInputs(now time.Time) uiapi.Inputs {
	return uiapi.Inputs{Now: now, Power: power.Facts{UCI: true, Boot: true, Running: true, Carrying: true}, TableLoaded: true, Counters: map[string]int64{"vctl_would_leak": 0}, Runtime: &localctl.Runtime{Engine: localctl.Engine{State: "running"}, Route: &localctl.Route{Nodes: []string{"bridge-pl5"}, At: now}}, View: &xrayview.View{Balancers: []xrayview.Balancer{{Tag: "main", Role: "main", Strategy: "leastLoad", Members: []string{"bridge-pl5"}}}}, Balancer: map[string]api.BalancerInfo{"main": {Principle: []string{"bridge-pl5"}}}, Metrics: &api.Metrics{Observatory: map[string]api.Observation{"bridge-pl5": {Alive: true, LastTry: now.Unix()}}}}
}
func TestConnectVerdictRequiresActualFreshMeasurements(t *testing.T) {
	now := time.Now()
	if v, c := connectVerdict(uiapi.Inputs{Now: now}, nil); v != "" || c != nil {
		t.Fatal("unavailable power/runtime fabricated verdict")
	}
	in := measuredConnectInputs(now)
	v, c := connectVerdict(in, map[string]exitcheck.Located{"bridge-pl5": {CC: "DE", At: now}})
	if v != "ok" || c == nil || *c != "DE" {
		t.Fatalf("%s %v", v, c)
	}
	_, c = connectVerdict(in, nil)
	if c != nil {
		t.Fatal("inferred node-label country")
	}
	_, c = connectVerdict(in, map[string]exitcheck.Located{"bridge-pl5": {CC: "DE", At: now.Add(-25 * time.Hour)}})
	if c != nil {
		t.Fatal("stale measured country")
	}
	in.Metrics.Observatory["bridge-pl5"] = api.Observation{Alive: true, LastTry: now.Add(-3 * time.Minute).Unix()}
	v, _ = connectVerdict(in, nil)
	if v != "" {
		t.Fatal("stale probe verdict")
	}
	in = measuredConnectInputs(now)
	in.Metrics = nil
	v, _ = connectVerdict(in, nil)
	if v != "" {
		t.Fatal("guessed verdict without metrics")
	}
	in = measuredConnectInputs(now)
	in.Runtime.Route.At = now.Add(time.Minute)
	v, _ = connectVerdict(in, nil)
	if v != "" {
		t.Fatal("future route verdict")
	}
}
func TestConnectSettingsUseCanonicalDigestsAndNoPasswords(t *testing.T) {
	id := strings.Repeat("a", 64)
	off := false
	in := uiapi.Inputs{Index: &localctl.EntriesIndex{Entries: []localctl.EntrySummary{{Index: 0, Remark: "Entry", Digest: id}}}, Runtime: &localctl.Runtime{Entry: &localctl.Entry{Index: 0, Remark: "Entry", Local: true}}, Overrides: localctl.Overrides{Direct: []string{"example.org"}, ServiceEntries: map[string]string{"youtube": id}, Services: map[string]string{"telegram": "PL"}}}
	out := connectSettings(in, setup.Facts{Password: &off, Wifi: setup.Wifi{Radios: []setup.Radio{{AP: true, Band: "2g", SSID: "Home"}}}})
	if out.Location == nil || out.Location.EntryID == nil || *out.Location.EntryID != id || out.RouterPasswordSet == nil || *out.RouterPasswordSet {
		t.Fatalf("%+v", out)
	}
	if out.Entries == nil || (*out.Entries)[0].Country != nil {
		t.Fatal("country inferred")
	}
	if out.Wifi == nil || (*out.Wifi)[0].Password != "" {
		t.Fatal("password telemetry")
	}
	byID := map[string]*string{}
	if out.Services != nil {
		for _, s := range *out.Services {
			byID[s.ID] = s.EntryID
		}
	}
	// «Нейросети» are a fourth service; with no Kazakh location in the index
	// they run on the main path (null), like any service without a choice.
	if len(byID) != 4 || byID["telegram"] != nil || byID["youtube"] == nil || byID["ai"] != nil {
		t.Fatal("service country converted into entry identity")
	}
}
func TestPublishConnectCapabilitiesAreOwnerBoundAndExplicit(t *testing.T) {
	out := controlplane.RouterConnectTelemetry{}
	features := map[string]bool{"location": true, "wifi": false}
	connectOwnerCapabilities(&out, nil, features)
	if out.OwnerRef != "" || out.Capabilities != nil {
		t.Fatal("unbound capabilities advertised")
	}
	connectOwnerCapabilities(&out, &controlplane.ClaimOwner{OwnerRef: "owner-test"}, features)
	if out.OwnerRef != "owner-test" || out.Capabilities == nil || len(*out.Capabilities) != 1 || (*out.Capabilities)[0] != "location" {
		t.Fatalf("%+v", out)
	}
}
func TestConnectInitialServiceCatalogueMatchesExecutor(t *testing.T) {
	out := connectSettings(uiapi.Inputs{}, setup.Facts{})
	if out.Services == nil || len(*out.Services) != len(xray.Services) {
		t.Fatal("fresh router lacks authoritative executor catalogue")
	}
	for _, s := range *out.Services {
		if _, ok := xray.ServiceByID(s.ID); !ok || s.EntryID != nil {
			t.Fatal("invented service or default binding")
		}
	}
	cache := &localctl.EntriesCache{Entries: []json.RawMessage{json.RawMessage(`{"remarks":"fake","outbounds":[{"tag":"proxy","protocol":"freedom"}]}`)}, Remarks: []string{"fake"}}
	for _, s := range *out.Services {
		change, code := connectRouteChange("set_service", json.RawMessage(fmt.Sprintf(`{"service":%q,"entryId":null}`, s.ID)), cache)
		if code != "" || change == nil {
			t.Fatalf("initial service refused: %s", code)
		}
	}
}

// xray's burst observatory (healthCheck) reports alive/delay per outbound but
// no probe time: every verdict on 1111 stayed unknown and the Vectra app
// showed the router's VPN as "unknown" (2026-10-02). With no probe time, the
// live scrape (Inputs.Now) is the observation.
func TestConnectVerdictTakesABurstObservationWithoutAProbeTime(t *testing.T) {
	now := time.Now()
	in := measuredConnectInputs(now)
	in.Metrics.Observatory["bridge-pl5"] = api.Observation{Alive: true}
	if v, _ := connectVerdict(in, nil); v != "ok" {
		t.Fatalf("alive burst observation: %q", v)
	}
	in.Metrics.Observatory["bridge-pl5"] = api.Observation{Alive: false}
	if v, _ := connectVerdict(in, nil); v != "down" {
		t.Fatalf("dead burst observation: %q", v)
	}
}

// The failover watchdog publishes the route every 2 s, beside the gather; a
// route (or a probe, an egress) published while the gather ran is later
// than Inputs.Now and is the freshest word, not a future one. 1111 after
// its reboot (2026-10-05): every other check-in carried no verdict, and
// Connect showed "unknown" with the VPN working.
func TestConnectVerdictTakesARoutePublishedDuringTheGather(t *testing.T) {
	gatherStart := time.Now().Add(-2 * time.Second)
	in := measuredConnectInputs(gatherStart)
	in.Runtime.Route.At = gatherStart.Add(time.Second)
	in.Metrics.Observatory["bridge-pl5"] = api.Observation{Alive: true, LastTry: gatherStart.Add(time.Second).Unix()}
	v, c := connectVerdict(in, map[string]exitcheck.Located{"bridge-pl5": {CC: "PL", At: gatherStart.Add(time.Second)}})
	if v != "ok" || c == nil || *c != "PL" {
		t.Fatalf("route published during the gather: %q %v", v, c)
	}
	// Later than the judgement itself is still no observation.
	in.Runtime.Route.At = time.Now().Add(time.Minute)
	if v, _ := connectVerdict(in, nil); v != "" {
		t.Fatalf("future route judged: %q", v)
	}
}

// One read that cannot judge keeps the last verdict (and its country) with
// the time it was judged; past the verdict's lifetime, with nothing to
// judge, for another owner or over another route, unknown.
func TestConnectVerdictHeldThroughAnUnjudgedCheckIn(t *testing.T) {
	d := &daemon{}
	now := time.Now()
	pl := "PL"
	in := measuredConnectInputs(now)
	if v, c, at := d.holdConnectVerdict(in, "o", "ok", &pl); v != "ok" || c == nil || *c != "PL" || !at.Equal(now) {
		t.Fatalf("judged verdict changed: %q %v %v", v, c, at)
	}
	in.Now = now.Add(45 * time.Second)
	v, c, at := d.holdConnectVerdict(in, "o", "", nil)
	if v != "ok" || c == nil || *c != "PL" {
		t.Fatalf("one unjudged check-in dropped the verdict: %q %v", v, c)
	}
	// Published with the judgement's own time, the check-in's freshness gate
	// ends it MaxObservationAge after the judgement, not after the hold.
	if !at.Equal(now) {
		t.Fatalf("held verdict restamped: %v, judged %v", at, now)
	}
	never := func(string) ([]byte, error) { return nil, os.ErrNotExist }
	snap := connecttelemetry.Snapshot{Telemetry: controlplane.RouterConnectTelemetry{Verdict: v, ExitCountry: c}, ObservedAt: at}
	if out := connecttelemetry.Build(never, now.Add(connecttelemetry.MaxObservationAge+time.Second), snap); out.Verdict != "" || out.ExitCountry != nil {
		t.Fatalf("held verdict outlived its judgement: %+v", out)
	}
	in.Now = now.Add(connecttelemetry.MaxObservationAge + time.Second)
	if v, c, _ := d.holdConnectVerdict(in, "o", "", nil); v != "" || c != nil {
		t.Fatalf("stale verdict held: %q %v", v, c)
	}
	in.Now = now.Add(time.Minute)
	d.holdConnectVerdict(in, "o", "down", nil)
	in.Now = now.Add(90 * time.Second)
	if v, c, _ := d.holdConnectVerdict(in, "o", "", nil); v != "down" || c != nil {
		t.Fatalf("held verdict is not the last judged: %q %v", v, c)
	}
	in.Runtime = nil
	if v, _, _ := d.holdConnectVerdict(in, "o", "", nil); v != "" {
		t.Fatalf("verdict held with no configuration: %q", v)
	}
}

// The hold is one owner's and one route's: a claim, a release or another
// route (a location switch) is unknown; a route not readable now keeps the
// verdict and drops the country, which may be the old route's.
func TestConnectVerdictHoldEndsWithTheOwnerOrTheRoute(t *testing.T) {
	now := time.Now()
	pl := "PL"
	judged := func() (*daemon, uiapi.Inputs) {
		d := &daemon{}
		in := measuredConnectInputs(now)
		d.holdConnectVerdict(in, "o", "ok", &pl)
		in.Now = now.Add(30 * time.Second)
		return d, in
	}
	d, in := judged()
	if v, _, _ := d.holdConnectVerdict(in, "other", "", nil); v != "" {
		t.Fatalf("held for another owner: %q", v)
	}
	if v, _, _ := d.holdConnectVerdict(in, "o", "", nil); v != "" {
		t.Fatalf("hold survived an owner change: %q", v)
	}
	d, in = judged()
	in.Runtime.Route = &localctl.Route{Nodes: []string{"bridge-de5"}, At: in.Now}
	if v, c, _ := d.holdConnectVerdict(in, "o", "", nil); v != "" || c != nil {
		t.Fatalf("held over another route: %q %v", v, c)
	}
	d, in = judged()
	in.Runtime.Route = &localctl.Route{Nodes: []string{"bridge-pl5"}, Override: "bridge-de5", At: in.Now}
	if v, _, _ := d.holdConnectVerdict(in, "o", "", nil); v != "" {
		t.Fatalf("held over the watchdog's move: %q", v)
	}
	d, in = judged()
	in.Runtime.Route = nil
	if v, c, _ := d.holdConnectVerdict(in, "o", "", nil); v != "ok" || c != nil {
		t.Fatalf("route not readable: %q %v", v, c)
	}
}
