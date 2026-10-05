package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"os"
	"slices"
	"sort"
	"sync"
	"time"
	"vectra-controller-pro/internal/connecttelemetry"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/uiapi"
)

// Seams gather only local state. Tests replace these with fixture readers;
// no external country/VPN checks are triggered by sending inventory.
var connectGather = func(ctx context.Context, d *daemon) uiapi.Inputs {
	need := uiapi.NeedDiagnostics
	need.Runtime = false
	need.Tune = false
	in := uiapi.Gather(ctx, uiapi.RouterEnv(d.cfg, controllerVersion()), need)
	if d.sup != nil {
		in.Runtime = d.liveRuntime()
	}
	penv := power.RouterEnv()
	penv.Loaded = func(context.Context) bool { return in.TableLoaded }
	in.Power = power.Read(ctx, penv, true)
	if _, err := os.Stat(setup.RouterEnv().VectraConfig); err != nil {
		in.Runtime = nil
	}
	return in
}
var connectSetup = func(ctx context.Context) setup.Facts { return setup.Read(ctx, setup.RouterEnv()) }

// publishConnectTelemetry is called on the daemon loop before Collect.
// Feature flags must correspond to handlers actually enabled for this owner;
// advertised support is never assumed from version or heartbeat.
func (d *daemon) publishConnectTelemetry(ctx context.Context, features map[string]bool) {
	if d.collector == nil {
		return
	}
	in := connectGather(ctx, d)
	// Read errors must not look like a confirmed empty owner configuration.
	overrides, settingsErr := localctl.LoadOverrides(d.cfg.OverridesPath)
	if settingsErr == nil {
		in.Overrides = overrides
	}
	t := connectSettings(in, connectSetup(ctx))
	if t.Services != nil {
		markConnectServiceCarriers(*t.Services, connectServiceCarriers(d.cfg.EntriesPath, in.Index))
	}
	// A stale «Нейросети» choice is skipped by the render: they run through
	// their default meanwhile, and that is the entry reported.
	aiOv := overrides
	if t.Services != nil && connectServiceStale(*t.Services, "ai") {
		aiOv.ServiceEntries = maps.Clone(overrides.ServiceEntries)
		delete(aiOv.ServiceEntries, "ai")
	}
	if id, ok := aiDefaultApplied(d.cfg, aiOv, d.st.SpliceKey); ok && settingsErr == nil && t.Services != nil && id != overrides.ServiceEntries["ai"] {
		setConnectServiceEntry(*t.Services, "ai", id, t.Entries) // «Нейросети» through their default
	}
	if settingsErr != nil {
		t.Sites = nil
		t.Services = nil
	}
	if d.st.ClaimOwner != nil {
		support := remoteShellAllowed()
		t.SupportAccess = &support
	}
	t.Verdict, t.ExitCountry = connectVerdict(in, d.exits.EgressSnapshot())
	t.Verdict, t.ExitCountry = d.holdConnectVerdict(in, t.Verdict, t.ExitCountry)
	connectOwnerCapabilities(&t, d.st.ClaimOwner, features)
	d.collector.SetConnect(connecttelemetry.Snapshot{Telemetry: t, ObservedAt: in.Now})
}

func connectSettings(in uiapi.Inputs, f setup.Facts) controlplane.RouterConnectTelemetry {
	t := controlplane.RouterConnectTelemetry{RouterPasswordSet: f.Password}
	entries := []controlplane.ConnectEntry{}
	valid := map[string]bool{}
	if in.Index != nil && len(in.Index.Entries) <= 300 {
		for _, entry := range in.Index.Entries {
			if validConnectEntryID(entry.Digest) {
				entries = append(entries, controlplane.ConnectEntry{ID: entry.Digest, Name: entry.Remark})
				valid[entry.Digest] = true
			}
		}
		t.Entries = &entries
		if rt := in.Runtime; rt != nil && rt.Entry != nil && !rt.Entry.Stale {
			mode := "auto"
			if rt.Entry.Local {
				mode = "entry"
			}
			for _, entry := range in.Index.Entries {
				if entry.Index == rt.Entry.Index && entry.Remark == rt.Entry.Remark && valid[entry.Digest] {
					id := entry.Digest
					t.Location = &controlplane.ConnectLocation{Mode: mode, EntryID: &id}
					break
				}
			}
		}
	}
	direct := append([]string{}, in.Overrides.Direct...)
	vpn := append([]string{}, in.Overrides.Proxy...)
	if len(direct) <= 300 && len(vpn) <= 300 {
		t.Sites = &controlplane.ConnectSites{Direct: direct, VPN: vpn}
	}
	services := []controlplane.ConnectService{}
	ids := map[string]bool{}
	// The exact same authoritative catalogue drives typed ServiceByID.
	// An unconfigured service uses the main path, represented by null.
	for _, service := range xray.Services {
		ids[service.ID] = true
	}
	ordered := []string{}
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		s := controlplane.ConnectService{ID: id}
		entry := in.Overrides.ServiceEntries[id]
		// No location, no «as the main VPN», no country: the default. A
		// country chosen in the router's own UI is a choice the Connect
		// contract has no field for: auto false, entryId null.
		auto := entry == "" && in.Overrides.Services[id] == ""
		s.Auto = &auto
		switch {
		case valid[entry]:
			e := entry
			s.EntryID = &e
		case entry != "" && entry != localctl.ServiceMainPath && t.Entries != nil:
			s.Stale = true // the owner's location left the cache
		}
		services = append(services, s)
	}
	if len(services) <= 100 {
		t.Services = &services
	}
	wifi := []controlplane.ConnectWifi{}
	for _, r := range f.Wifi.Radios {
		if r.AP && r.SSID != "" {
			wifi = append(wifi, controlplane.ConnectWifi{Band: r.Band, SSID: r.SSID})
		}
	}
	if len(f.Wifi.Radios) > 0 && len(wifi) <= 8 {
		t.Wifi = &wifi
	}
	return t
}

// connectValidateServiceEntry, connectServicesCarried and connectLoadEntries
// are seams tests count validations and cache reads through.
var connectValidateServiceEntry = xray.ValidateConnectServiceEntry
var connectServicesCarried = xray.ConnectServicesCarried
var connectLoadEntries = localctl.LoadEntries

// connectCarriersMemo keeps the last answer of connectServiceCarriers: the
// check-in asks every minute, the answer changes only with the cached
// locations, and working it out parses every one of them.
var connectCarriersMemo struct {
	sync.Mutex
	key      string
	carriers map[string][]string
}

// connectServiceCarriers is, per service, the cached locations (digests, in
// the index's order) that carry it — those a set_service would accept. Nil
// when not known: no index, or a cache that does not match it. A mismatch is
// memoized like an answer, and clears on the next index change (the next
// subscription refresh rewrites the cache, then the index).
func connectServiceCarriers(entriesPath string, idx *localctl.EntriesIndex) map[string][]string {
	if idx == nil || len(idx.Entries) > 300 {
		return nil
	}
	// The digests are sha256 of each location's bytes: they name the cache.
	h := sha256.New()
	h.Write([]byte(entriesPath))
	for _, e := range idx.Entries {
		h.Write([]byte{0})
		h.Write([]byte(e.Digest))
	}
	key := hex.EncodeToString(h.Sum(nil))
	connectCarriersMemo.Lock()
	if connectCarriersMemo.key == key {
		carriers := connectCarriersMemo.carriers
		connectCarriersMemo.Unlock()
		return carriers
	}
	connectCarriersMemo.Unlock()
	cache, err := connectLoadEntries(entriesPath)
	if err != nil {
		return nil // unreadable now: tried again on the next check-in
	}
	carriers := serviceCarriersOf(cache, idx)
	connectCarriersMemo.Lock()
	connectCarriersMemo.key, connectCarriersMemo.carriers = key, carriers
	connectCarriersMemo.Unlock()
	return carriers
}

// serviceCarriersOf works the carriers out from a cache that matches idx,
// each location parsed once; nil when it does not match.
func serviceCarriersOf(cache *localctl.EntriesCache, idx *localctl.EntriesIndex) map[string][]string {
	if len(cache.Entries) != len(idx.Entries) {
		return nil
	}
	digests := make([]string, len(cache.Entries))
	for i, raw := range cache.Entries {
		sum := sha256.Sum256(raw)
		digests[i] = hex.EncodeToString(sum[:])
		if digests[i] != idx.Entries[i].Digest {
			return nil // written between the two reads
		}
	}
	carriers := map[string][]string{}
	for _, svc := range xray.Services {
		carriers[svc.ID] = []string{}
	}
	seen := map[string]bool{}
	for i, raw := range cache.Entries {
		id := digests[i]
		if seen[id] || !validConnectEntryID(id) {
			continue
		}
		seen[id] = true
		for svc := range connectServicesCarried(raw) {
			if _, known := carriers[svc]; known {
				carriers[svc] = append(carriers[svc], id)
			}
		}
	}
	return carriers
}

// markConnectServiceCarriers lists each service's carriers, and marks an
// owner's location that no longer carries its service stale: it does not
// run, the service is on its default meanwhile.
func markConnectServiceCarriers(services []controlplane.ConnectService, carriers map[string][]string) {
	if carriers == nil {
		return
	}
	for i := range services {
		s := &services[i]
		list := carriers[s.ID]
		if len(list) > connecttelemetry.MaxServiceEntries {
			continue // not known rather than cut short
		}
		entries := append([]string{}, list...)
		s.Entries = &entries
		if s.EntryID != nil && (s.Auto == nil || !*s.Auto) && !slices.Contains(list, *s.EntryID) {
			s.EntryID, s.Stale = nil, true
		}
	}
}

func connectServiceStale(services []controlplane.ConnectService, id string) bool {
	for _, s := range services {
		if s.ID == id {
			return s.Stale
		}
	}
	return false
}

// setConnectServiceEntry reports service as running through entry, when the
// inventory lists that entry.
func setConnectServiceEntry(services []controlplane.ConnectService, service, entry string, entries *[]controlplane.ConnectEntry) {
	if entries == nil {
		return
	}
	for _, e := range *entries {
		if e.ID != entry {
			continue
		}
		for i := range services {
			if services[i].ID == service && services[i].EntryID == nil {
				id := entry
				services[i].EntryID = &id
			}
		}
		return
	}
}

func validConnectEntryID(id string) bool {
	if len(id) != 64 {
		return false
	}
	b, err := hex.DecodeString(id)
	return err == nil && hex.EncodeToString(b) == id
}
func freshConnect(at, now time.Time, age time.Duration) bool {
	return !at.IsZero() && !now.Before(at) && now.Sub(at) <= age
}

// freshObservation: at is no older than age at ref (when the gather began)
// and no later than now (when it is judged). The failover watchdog publishes
// the route every 2 s, the exit check its egress, beside the gather: one
// published while the gather ran is later than ref, and it is the freshest
// word there is, not a future one. Judged against ref alone it left the
// verdict out whenever a publish fell inside the gather — on 1111 after its
// reboot (2026-10-05) every other check-in, the 45 s poll against the 2 s
// watchdog alternating in and out of that window: Connect showed "unknown".
func freshObservation(at, ref, now time.Time, age time.Duration) bool {
	return !at.IsZero() && !at.After(now) && ref.Sub(at) <= age
}

// holdConnectVerdict: a check-in that cannot judge the tunnel right now (a
// probe still under way, an API slow to answer) reports the last verdict the
// router did judge, with its country, while that is no older than the
// verdict's own lifetime (connecttelemetry.MaxObservationAge): the owner's
// app does not flip to "unknown" for one unlucky read. Older, or with no
// configuration to judge at all, the verdict stays out: unknown.
func (d *daemon) holdConnectVerdict(in uiapi.Inputs, verdict string, country *string) (string, *string) {
	if verdict != "" {
		d.connectVerdict, d.connectVerdictAt = connectJudged{verdict: verdict, country: country}, in.Now
		return verdict, country
	}
	if in.Runtime == nil || d.connectVerdict.verdict == "" || !freshConnect(d.connectVerdictAt, in.Now, connecttelemetry.MaxObservationAge) {
		return "", nil
	}
	return d.connectVerdict.verdict, d.connectVerdict.country
}

// connectJudged is the last verdict the router judged for Connect.
type connectJudged struct {
	verdict string
	country *string
}

// Verdict uses the same diagnostics decisions as the local UI, but requires
// fresh selected-node observatory data. Missing data remains unknown.
func connectVerdict(in uiapi.Inputs, egress map[string]exitcheck.Located) (string, *string) {
	now := time.Now()
	if !freshConnect(in.Now, now, connecttelemetry.MaxObservationAge) {
		return "", nil
	}
	if in.Runtime == nil {
		return "", nil
	}
	if !in.Power.On() {
		return "stopped", nil
	}
	if in.Power.Running && !in.Power.Carrying && in.Power.Holder() == power.Direct {
		return "direct", nil
	}
	rt := in.Runtime
	if rt == nil {
		return "", nil
	}
	if rt.Engine.State != "running" {
		return "down", nil
	}
	if !in.TableLoaded || in.Counters == nil || rt.Route == nil || !freshObservation(rt.Route.At, in.Now, now, connecttelemetry.MaxObservationAge) {
		return "", nil
	}
	nodes := rt.Route.Nodes
	if rt.Route.Override != "" {
		nodes = []string{rt.Route.Override}
	}
	if len(nodes) == 0 || in.Metrics == nil {
		return "", nil
	}
	for _, tag := range nodes {
		ob, ok := in.Metrics.Observatory[tag]
		if !ok {
			return "", nil
		}
		// The classic observatory dates its probe; xray's burst observatory
		// (healthCheck) does not, and then this live scrape is the observation.
		if ob.LastTry > 0 && !freshObservation(time.Unix(ob.LastTry, 0), in.Now, now, connecttelemetry.MaxObservationAge) {
			return "", nil
		}
		if !ob.Alive {
			return "down", nil
		}
	}
	verdict := ""
	checks := uiapi.BuildDiagnostics(in).Checks
	for _, c := range checks {
		if c.ID == "no_leak" && c.Status == "unknown" {
			return "", nil
		}
		if c.ID == "pinned_node_dead" && c.Status == "fail" {
			return "down", nil
		}
		if c.ID == "no_leak" && (c.Status == "fail" || c.Status == "warn") {
			armed, _ := c.Params["killSwitch"].(bool)
			if !armed {
				return "leak", nil
			}
			return "down", nil
		}
	}
	for _, c := range checks {
		if c.ID == "balancer_fallback" {
			mainDirect, _ := c.Params["mainDirect"].(bool)
			mainBlocked, _ := c.Params["mainBlocked"].(bool)
			mainReserve, _ := c.Params["main"].(bool)
			switch {
			case mainDirect:
				verdict = "direct"
			case mainBlocked:
				verdict = "down"
			case c.Status == "unknown":
				return "", nil
			case mainReserve:
				verdict = "reserve"
			case c.Status == "ok":
				verdict = "ok"
			}
		}
	}
	if verdict == "" || verdict == "direct" || verdict == "down" {
		return verdict, nil
	}
	var country *string
	for _, tag := range nodes {
		e, ok := egress[tag]
		if !ok || !freshObservation(e.At, in.Now, now, 24*time.Hour) {
			return verdict, nil
		}
		if country != nil && *country != e.CC {
			return verdict, nil
		}
		cc := e.CC
		country = &cc
	}
	return verdict, country
}

func connectOwnerCapabilities(t *controlplane.RouterConnectTelemetry, owner *controlplane.ClaimOwner, features map[string]bool) {
	if owner == nil || owner.OwnerRef == "" {
		return
	}
	t.OwnerRef = owner.OwnerRef
	caps := []string{}
	for feature, on := range features {
		if on {
			caps = append(caps, feature)
		}
	}
	sort.Strings(caps)
	t.Capabilities = &caps
}
