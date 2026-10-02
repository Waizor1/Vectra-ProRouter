package main

import (
	"context"
	"encoding/hex"
	"os"
	"sort"
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
	if settingsErr != nil {
		t.Sites = nil
		t.Services = nil
	}
	if d.st.ClaimOwner != nil {
		support := remoteShellAllowed()
		t.SupportAccess = &support
	}
	t.Verdict, t.ExitCountry = connectVerdict(in, d.exits.EgressSnapshot())
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
		if entry := in.Overrides.ServiceEntries[id]; valid[entry] {
			e := entry
			s.EntryID = &e
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

// Verdict uses the same diagnostics decisions as the local UI, but requires
// fresh selected-node observatory data. Missing data remains unknown.
func connectVerdict(in uiapi.Inputs, egress map[string]exitcheck.Located) (string, *string) {
	if !freshConnect(in.Now, time.Now(), connecttelemetry.MaxObservationAge) {
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
	if !in.TableLoaded || in.Counters == nil || rt.Route == nil || !freshConnect(rt.Route.At, in.Now, connecttelemetry.MaxObservationAge) {
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
		if ob.LastTry > 0 && !freshConnect(time.Unix(ob.LastTry, 0), in.Now, connecttelemetry.MaxObservationAge) {
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
		if !ok || !freshConnect(e.At, in.Now, 24*time.Hour) {
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
