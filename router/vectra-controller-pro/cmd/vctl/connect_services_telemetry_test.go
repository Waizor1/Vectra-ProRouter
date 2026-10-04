package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/supervisor"
	"vectra-controller-pro/internal/uiapi"
	"vectra-controller-pro/internal/vault"
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

// The carriers are worked out once per cache, each location parsed once:
// an unchanged cache is not read or parsed again on the next check-in, a
// new one is, and a cache that does not match its index is not re-read
// every minute either.
func TestServiceCarriersAreKeptUntilTheCacheChanges(t *testing.T) {
	d, ids := servicesTestDaemon(t, localctl.Overrides{})
	oldCarried, oldLoad := connectServicesCarried, connectLoadEntries
	t.Cleanup(func() { connectServicesCarried, connectLoadEntries = oldCarried, oldLoad })
	parses, reads := 0, 0
	connectServicesCarried = func(raw []byte) map[string]bool { parses++; return oldCarried(raw) }
	connectLoadEntries = func(p string) (*localctl.EntriesCache, error) { reads++; return oldLoad(p) }
	idx, err := localctl.LoadEntriesIndex(d.cfg.EntriesIndexPath)
	if err != nil {
		t.Fatal(err)
	}
	first := connectServiceCarriers(d.cfg.EntriesPath, idx)
	if parses != 3 || reads != 1 {
		t.Fatalf("parsed %d locations in %d reads, want 3 in 1", parses, reads)
	}
	if !slices.Equal(first["youtube"], []string{ids[0], ids[2]}) || len(first["telegram"]) != 3 {
		t.Fatalf("carriers: %v", first)
	}
	for range 3 {
		connectServiceCarriers(d.cfg.EntriesPath, idx)
	}
	if parses != 3 || reads != 1 {
		t.Fatalf("an unchanged cache was read again: %d parses, %d reads", parses, reads)
	}
	c := &localctl.EntriesCache{Remarks: []string{"🇩🇪 Германия"}, Entries: []json.RawMessage{json.RawMessage(svcTestNoYT)}}
	if _, err := localctl.SaveEntries(d.cfg.EntriesPath, d.cfg.EntriesIndexPath, c); err != nil {
		t.Fatal(err)
	}
	if idx, err = localctl.LoadEntriesIndex(d.cfg.EntriesIndexPath); err != nil {
		t.Fatal(err)
	}
	next := connectServiceCarriers(d.cfg.EntriesPath, idx)
	if parses != 4 || reads != 2 || len(next["youtube"]) != 0 || next["youtube"] == nil || len(next["tiktok"]) != 1 {
		t.Fatalf("a new cache: %d parses, %d reads, %v", parses, reads, next)
	}
	// The cache rewritten under an index that no longer describes it: not
	// known, and not read again while the index stays.
	other := &localctl.EntriesCache{Remarks: []string{"x"}, Entries: []json.RawMessage{json.RawMessage(svcTestAll)}}
	if _, err := localctl.SaveEntries(d.cfg.EntriesPath, filepath.Join(t.TempDir(), "other.index.json"), other); err != nil {
		t.Fatal(err)
	}
	idx.Entries = append(idx.Entries, localctl.EntrySummary{Digest: strings.Repeat("e", 64)})
	for range 3 {
		if got := connectServiceCarriers(d.cfg.EntriesPath, idx); got != nil {
			t.Fatalf("a mismatched cache: %v", got)
		}
	}
	if reads != 3 || parses != 4 {
		t.Fatalf("a lasting mismatch was re-read: %d reads, %d parses", reads, parses)
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

// withoutService is entry with the service's own rule sent to a tag the
// document lacks: the location no longer carries it.
func withoutService(t *testing.T, entry []byte, svc xray.Service) []byte {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(entry, &doc); err != nil {
		t.Fatal(err)
	}
	var routing map[string]json.RawMessage
	_ = json.Unmarshal(doc["routing"], &routing)
	if routing == nil {
		routing = map[string]json.RawMessage{}
	}
	var rules []json.RawMessage
	_ = json.Unmarshal(routing["rules"], &rules)
	rule, _ := json.Marshal(map[string]any{"domain": []string{svc.Domains[0]}, "outboundTag": "vctl-test-missing"})
	routing["rules"], _ = json.Marshal(append([]json.RawMessage{rule}, rules...))
	doc["routing"], _ = json.Marshal(routing)
	out, _ := json.Marshal(doc)
	if xray.ValidateConnectServiceEntry(out, svc.ID) == nil {
		t.Fatalf("%s still carried", svc.ID)
	}
	return out
}

// An owner's service location that left the cache, or no longer carries the
// service, never freezes the router: the render skips it (the service takes
// its default path), every other change still applies, the choice is kept,
// and it runs again when the location does.
func TestAStaleServiceChoiceIsSkippedNotRefused(t *testing.T) {
	d, _, entries, remarks := newLocalUIDaemon(t)
	ctx := context.Background()
	raw, _, err := d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.applyProvider(ctx, raw, false); err != nil {
		t.Fatal(err)
	}
	var svc xray.Service
	for _, s := range xray.Services {
		if s.ID != "ai" && xray.ValidateConnectServiceEntry(entries[1], s.ID) == nil {
			svc = s
			break
		}
	}
	if svc.ID == "" {
		t.Fatal("the fixture's second location carries no service")
	}
	overlay := "vctl-connect-" + svc.ID + "-"
	rendered := func() bool {
		b, err := vault.ReadFile(d.cfg.XrayRenderPath)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(string(b), overlay)
	}
	save := func(docs ...[]byte) {
		c := &localctl.EntriesCache{}
		for i, doc := range docs {
			c.Remarks = append(c.Remarks, remarks[0]+strings.Repeat(" ", i))
			c.Entries = append(c.Entries, doc)
		}
		c.Remarks[0] = remarks[0]
		if _, err := localctl.SaveEntries(d.cfg.EntriesPath, d.cfg.EntriesIndexPath, c); err != nil {
			t.Fatal(err)
		}
	}
	choose := func(id string) {
		if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
			o.ServiceEntries = map[string]string{svc.ID: id}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	rules := func(site string) localctl.SocketResponse {
		return d.localReapply(ctx, &localctl.Change{SetRules: &localctl.Rules{Direct: []string{site}, Proxy: []string{}}})
	}
	chosen := apply.Digest(entries[1])
	choose(chosen)
	if resp := rules("one.example"); !resp.OK || !rendered() {
		t.Fatalf("a running choice: %+v, overlay %v", resp, rendered())
	}

	// The location left the cache.
	save(entries[0])
	if resp := rules("two.example"); !resp.OK {
		t.Fatalf("a vanished location refused a change: %+v", resp)
	}
	if rendered() {
		t.Fatal("rendered a location the cache no longer has")
	}
	if _, err := d.applyProvider(ctx, entries[0], true); err != nil {
		t.Fatalf("a vanished location refused the render: %v", err)
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); ov.ServiceEntries[svc.ID] != chosen {
		t.Fatalf("the choice was dropped: %+v", ov.ServiceEntries)
	}
	if d.svcSkipped.m[svc.ID] != chosen {
		t.Fatalf("the skip was not noted: %v", d.svcSkipped.m)
	}

	// A location in the cache that no longer carries the service.
	lacking := withoutService(t, entries[1], svc)
	save(entries[0], lacking)
	choose(apply.Digest(lacking))
	if resp := rules("three.example"); !resp.OK || rendered() {
		t.Fatalf("a location without the service: %+v, overlay %v", resp, rendered())
	}

	// Choosing a location that does not carry the service now is refused.
	if resp := d.localReapply(ctx, &localctl.Change{SetService: &localctl.ServiceChoice{ID: svc.ID, EntryID: apply.Digest(lacking)}}); resp.OK {
		t.Fatal("a new choice that cannot run was reported applied")
	}

	// The location is back: the kept choice runs again, and is forgotten as skipped.
	choose(chosen)
	save(entries[0], entries[1])
	if resp := rules("four.example"); !resp.OK || !rendered() {
		t.Fatalf("the choice did not resume: %+v, overlay %v", resp, rendered())
	}
	if _, still := d.svcSkipped.m[svc.ID]; still {
		t.Fatal("a running choice still noted as skipped")
	}
}

// A stale «Нейросети» choice: they run through their Kazakh default
// meanwhile, and the check-in names that default, stale and not auto.
func TestAStaleAIChoiceReportsTheDefaultItRunsThrough(t *testing.T) {
	d, ids := servicesTestDaemon(t, localctl.Overrides{})
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.ServiceEntries = map[string]string{"ai": strings.Repeat("f", 64)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d.st.SpliceKey = "v1;svcEntries=ai=" + ids[2] + ";exitprobe=x" // the render took the default
	got, raw := checkInServices(t, d)
	s := got["ai"]
	if !s.Stale || s.Auto == nil || *s.Auto || s.EntryID == nil || *s.EntryID != ids[2] {
		t.Fatalf("ai: %s", raw)
	}
}

// A cache that is there but cannot be read now moves nothing: the render and
// the local change wait (the running render stays, overlay and all), and run
// as soon as the cache reads again. A cache that is gone — no locations at
// all — skips the choices instead.
func TestAnUnreadableCacheWaitsAMissingOneSkips(t *testing.T) {
	d, _, entries, remarks := newLocalUIDaemon(t)
	ctx := context.Background()
	raw, _, err := d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.applyProvider(ctx, raw, false); err != nil {
		t.Fatal(err)
	}
	var svc xray.Service
	for _, s := range xray.Services {
		if s.ID != "ai" && xray.ValidateConnectServiceEntry(entries[1], s.ID) == nil {
			svc = s
			break
		}
	}
	overlay := "vctl-connect-" + svc.ID + "-"
	rendered := func() bool {
		b, err := vault.ReadFile(d.cfg.XrayRenderPath)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(string(b), overlay)
	}
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.ServiceEntries = map[string]string{svc.ID: apply.Digest(entries[1])}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rules := func(site string) localctl.SocketResponse {
		return d.localReapply(ctx, &localctl.Change{SetRules: &localctl.Rules{Direct: []string{site}, Proxy: []string{}}})
	}
	if resp := rules("one.example"); !resp.OK || !rendered() {
		t.Fatalf("a running choice: %+v", resp)
	}
	saved := func() {
		c := &localctl.EntriesCache{Remarks: remarks, Entries: []json.RawMessage{entries[0], entries[1]}}
		if _, err := localctl.SaveEntries(d.cfg.EntriesPath, d.cfg.EntriesIndexPath, c); err != nil {
			t.Fatal(err)
		}
	}

	// Unreadable now: bytes the vault cannot open.
	if err := os.WriteFile(d.cfg.EntriesPath, []byte("not sealed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, lerr := localctl.LoadEntries(d.cfg.EntriesPath); lerr == nil || errors.Is(lerr, os.ErrNotExist) {
		t.Fatalf("the fixture is not an unreadable cache: %v", lerr)
	}
	if resp := rules("two.example"); resp.OK {
		t.Fatal("a change rendered without reading the services' locations")
	}
	if _, err := d.applyProvider(ctx, entries[0], true); err == nil {
		t.Fatal("a render went ahead without reading the services' locations")
	}
	if !rendered() {
		t.Fatal("the running render lost its service location")
	}
	if _, _, err := d.connectServiceOptionsFor(localctl.Overrides{}, entries[0]); err == nil {
		t.Fatal("the «Нейросети» default was given up on a read error")
	}
	if _, err := routePreviewOptions(d, entries[0], localctl.Overrides{ServiceEntries: map[string]string{svc.ID: apply.Digest(entries[1])}}); err == nil {
		t.Fatal("the preview went ahead without reading the services' locations")
	}
	_ = os.Remove(d.cfg.EntriesPath)
	saved()
	if resp := rules("three.example"); !resp.OK || !rendered() {
		t.Fatalf("readable again: %+v, overlay %v", resp, rendered())
	}

	// Gone: no locations at all — the choice is skipped and kept.
	if err := vault.RemoveFile(d.cfg.EntriesPath); err != nil {
		t.Fatal(err)
	}
	if _, err := d.applyProvider(ctx, entries[0], true); err != nil {
		t.Fatalf("a missing cache refused the render: %v", err)
	}
	if rendered() {
		t.Fatal("rendered a location with no cache")
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); ov.ServiceEntries[svc.ID] != apply.Digest(entries[1]) {
		t.Fatalf("the choice was dropped: %+v", ov.ServiceEntries)
	}
}
