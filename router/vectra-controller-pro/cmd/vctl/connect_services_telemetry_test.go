package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/supervisor"
	"vectra-controller-pro/internal/uiapi"
)

// Three locations: one carries everything, one sends YouTube to a blackhole
// (it does not carry YouTube), and the provider's 🇷🇺🇰🇿 cascade.
const (
	svcTestAll   = `{"outbounds":[{"tag":"de-1","protocol":"vless"}]}`
	svcTestNoYT  = `{"outbounds":[{"tag":"nl-1","protocol":"vless"},{"tag":"block","protocol":"blackhole"}],"routing":{"rules":[{"domain":["geosite:youtube"],"outboundTag":"block"}]}}`
	svcTestKazak = `{"outbounds":[{"tag":"kz-1","protocol":"vless"}]}`
)

// servicesTestDaemon is an owned router with the three locations cached and
// the check-in's local reads replaced by the cache and ov.
func servicesTestDaemon(t *testing.T, ov localctl.Overrides) (*daemon, []string) {
	t.Helper()
	d, _ := connectTestDaemon(t)
	c := &localctl.EntriesCache{Remarks: []string{"🇩🇪 Германия", "🇳🇱 Нидерланды", "🇷🇺🇰🇿 Казахстан"},
		Entries: []json.RawMessage{json.RawMessage(svcTestAll), json.RawMessage(svcTestNoYT), json.RawMessage(svcTestKazak)}}
	if _, err := localctl.SaveEntries(d.cfg.EntriesPath, d.cfg.EntriesIndexPath, c); err != nil {
		t.Fatal(err)
	}
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error { *o = ov; return nil }); err != nil {
		t.Fatal(err)
	}
	oldGather, oldSetup := connectGather, connectSetup
	t.Cleanup(func() { connectGather, connectSetup = oldGather, oldSetup })
	connectGather = func(context.Context, *daemon) uiapi.Inputs {
		idx, err := localctl.LoadEntriesIndex(d.cfg.EntriesIndexPath)
		if err != nil {
			t.Fatal(err)
		}
		return uiapi.Inputs{Now: time.Now(), Index: idx}
	}
	connectSetup = func(context.Context) setup.Facts { return setup.Facts{} }
	var ids []string
	for _, e := range localctl.Summarize(c) {
		ids = append(ids, e.Digest)
	}
	return d, ids
}

// checkInServices is connect.services as the check-in puts it on the wire.
func checkInServices(t *testing.T, d *daemon) (map[string]controlplane.ConnectService, string) {
	t.Helper()
	d.publishConnectTelemetry(context.Background(), map[string]bool{"set_service": true})
	inv := d.collector.Collect(context.Background(), supervisor.Status{}, 0, 0)
	if inv.Connect == nil || inv.Connect.Services == nil {
		t.Fatalf("no services reported: %+v", inv.Connect)
	}
	raw, err := json.Marshal(*inv.Connect.Services)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]controlplane.ConnectService{}
	for _, s := range *inv.Connect.Services {
		out[s.ID] = s
	}
	return out, string(raw)
}

// The owner's choice, «as the main VPN», no choice at all, and «Нейросети»
// on their Kazakh default: entryId is where each runs now, auto says nobody
// chose, entries the locations that carry it.
func TestCheckInReportsServicesAutoAndCarriers(t *testing.T) {
	d, ids := servicesTestDaemon(t, localctl.Overrides{})
	ov := localctl.Overrides{ServiceEntries: map[string]string{"youtube": ids[0], "telegram": localctl.ServiceMainPath}}
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error { *o = ov; return nil }); err != nil {
		t.Fatal(err)
	}
	d.st.SpliceKey = "v1;svcEntries=ai=" + ids[2] + ";exitprobe=x" // the render carries the default
	got, raw := checkInServices(t, d)
	all := fmt.Sprintf("[%q,%q,%q]", ids[0], ids[1], ids[2])
	want := fmt.Sprintf(`[{"id":"ai","entryId":%q,"auto":true,"entries":%s},`+
		`{"id":"telegram","entryId":null,"auto":false,"entries":%s},`+
		`{"id":"tiktok","entryId":null,"auto":true,"entries":%s},`+
		`{"id":"youtube","entryId":%q,"auto":false,"entries":[%q,%q]}]`,
		ids[2], all, all, all, ids[0], ids[0], ids[2])
	if raw != want {
		t.Fatalf("services on the wire:\n got  %s\n want %s", raw, want)
	}
	if got["youtube"].Stale {
		t.Fatal("a running choice reported stale")
	}
}

// A choice that does not run — its location left the cache, or no longer
// carries the service — is stale, and the service is on its default.
func TestCheckInMarksAChoiceThatDoesNotRunStale(t *testing.T) {
	gone := strings.Repeat("f", 64)
	d, ids := servicesTestDaemon(t, localctl.Overrides{})
	ov := localctl.Overrides{ServiceEntries: map[string]string{"youtube": ids[1], "tiktok": gone, "telegram": ids[1]}}
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error { *o = ov; return nil }); err != nil {
		t.Fatal(err)
	}
	got, _ := checkInServices(t, d)
	for _, id := range []string{"youtube", "tiktok"} {
		s := got[id]
		if !s.Stale || s.EntryID != nil || s.Auto == nil || *s.Auto {
			t.Fatalf("%s: %+v", id, s)
		}
	}
	if s := got["telegram"]; s.Stale || s.EntryID == nil || *s.EntryID != ids[1] {
		t.Fatalf("telegram runs through its location: %+v", s)
	}
	if s := got["ai"]; s.Stale || s.Auto == nil || !*s.Auto {
		t.Fatalf("ai: %+v", s)
	}
}

// A country chosen on the router is a choice too: not auto.
func TestAServiceCountryIsNotAuto(t *testing.T) {
	out := connectSettings(uiapi.Inputs{Overrides: localctl.Overrides{Services: map[string]string{"telegram": "PL"}}}, setup.Facts{})
	for _, s := range *out.Services {
		if s.Auto == nil || *s.Auto != (s.ID != "telegram") || s.Stale || s.Entries != nil {
			t.Fatalf("%+v", s)
		}
	}
}

// The carriers are worked out once per cache: an unchanged cache is not
// parsed again on the next check-in, a new one is.
func TestServiceCarriersAreKeptUntilTheCacheChanges(t *testing.T) {
	d, ids := servicesTestDaemon(t, localctl.Overrides{})
	old := connectValidateServiceEntry
	t.Cleanup(func() { connectValidateServiceEntry = old })
	calls := 0
	connectValidateServiceEntry = func(raw []byte, id string) error { calls++; return old(raw, id) }
	idx, err := localctl.LoadEntriesIndex(d.cfg.EntriesIndexPath)
	if err != nil {
		t.Fatal(err)
	}
	first := connectServiceCarriers(d.cfg.EntriesPath, idx)
	if calls != 3*len(xray.Services) {
		t.Fatalf("validated %d times, want %d", calls, 3*len(xray.Services))
	}
	if !slices.Equal(first["youtube"], []string{ids[0], ids[2]}) || len(first["telegram"]) != 3 {
		t.Fatalf("carriers: %v", first)
	}
	for range 3 {
		connectServiceCarriers(d.cfg.EntriesPath, idx)
	}
	if calls != 3*len(xray.Services) {
		t.Fatalf("an unchanged cache was validated again: %d", calls)
	}
	c := &localctl.EntriesCache{Remarks: []string{"🇩🇪 Германия"}, Entries: []json.RawMessage{json.RawMessage(svcTestNoYT)}}
	if _, err := localctl.SaveEntries(d.cfg.EntriesPath, d.cfg.EntriesIndexPath, c); err != nil {
		t.Fatal(err)
	}
	if idx, err = localctl.LoadEntriesIndex(d.cfg.EntriesIndexPath); err != nil {
		t.Fatal(err)
	}
	next := connectServiceCarriers(d.cfg.EntriesPath, idx)
	if calls != 4*len(xray.Services) || len(next["youtube"]) != 0 || next["youtube"] == nil || len(next["tiktok"]) != 1 {
		t.Fatalf("a new cache: %d calls, %v", calls, next)
	}
	// No index: not known, and nothing is listed.
	if connectServiceCarriers(d.cfg.EntriesPath, nil) != nil {
		t.Fatal("carriers without an index")
	}
}

// A service no cached location carries lists none ([]), never null; more
// than the wire allows is not known rather than cut short.
func TestServiceCarriersOnTheWire(t *testing.T) {
	auto := true
	services := []controlplane.ConnectService{{ID: "youtube", Auto: &auto}, {ID: "tiktok", Auto: &auto}}
	many := make([]string, 201)
	markConnectServiceCarriers(services, map[string][]string{"tiktok": many})
	raw, _ := json.Marshal(services)
	if string(raw) != `[{"id":"youtube","entryId":null,"auto":true,"entries":[]},{"id":"tiktok","entryId":null,"auto":true}]` {
		t.Fatal(string(raw))
	}
}

// ":auto" takes a service back to its default: the owner's location, their
// «as the main VPN» and their country all go, no cache needed.
func TestSetServiceAutoDeletesTheChoice(t *testing.T) {
	for _, svc := range []string{"ai", "youtube"} {
		ov := localctl.Overrides{ServiceEntries: map[string]string{svc: localctl.ServiceMainPath}, Services: map[string]string{svc: "DE"}}
		c, code := connectRouteChange("set_service", json.RawMessage(fmt.Sprintf(`{"service":%q,"entryId":":auto"}`, svc)), nil)
		if code != "" {
			t.Fatalf("%s: %s", svc, code)
		}
		c.ApplyTo(&ov)
		if _, has := ov.ServiceEntries[svc]; has || ov.Services[svc] != "" {
			t.Fatalf("%s: choice kept: %+v", svc, ov)
		}
		if out := connectSettings(uiapi.Inputs{Overrides: ov}, setup.Facts{}); !*(*out.Services)[slices.IndexFunc(*out.Services, func(s controlplane.ConnectService) bool { return s.ID == svc })].Auto {
			t.Fatalf("%s: not auto after :auto", svc)
		}
	}
	if _, code := connectRouteChange("set_service", json.RawMessage(`{"service":"shell","entryId":":auto"}`), nil); code != "unknown_service" {
		t.Fatalf("unknown service: %q", code)
	}
}

// The Vectra app offers «Авто» only to a router that understands ":auto".
func TestSetServiceAutoIsAdvertisedWithSetService(t *testing.T) {
	d, _ := servicesTestDaemon(t, localctl.Overrides{})
	d.desired = &config.Config{}
	c := d.connectCapabilities()
	if !c["set_service"] || !c["set_service_auto"] {
		t.Fatalf("capabilities: %v", c)
	}
	d.cfg.RouteSource = "passwall"
	if c := d.connectCapabilities(); c["set_service"] || c["set_service_auto"] {
		t.Fatalf("advertised on a router that renders no services: %v", c)
	}
}
