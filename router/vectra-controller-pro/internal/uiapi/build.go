package uiapi

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"vectra-controller-pro/internal/memguard"

	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/brand"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/sites"
	"vectra-controller-pro/internal/tune"
	"vectra-controller-pro/internal/xrayview"
)

// Inputs is everything the answers are built from. Gathering is separate
// (gather.go) so the building is pure and tested against fixed inputs.
// A nil pointer means "could not be read" and becomes null in the answer.
type Inputs struct {
	Now     time.Time
	Version string

	// Runtime is the daemon's live state; nil when the daemon is down.
	Runtime *localctl.Runtime

	// View is the RENDERED config xray runs; nil before the first install.
	View *xrayview.View
	// ProviderProbeInterval is what the provider asked for (0 = unknown).
	ProviderProbeInterval time.Duration

	APIReachable     bool
	MetricsReachable bool
	// Metrics is the observatory + traffic; nil when unreachable.
	Metrics *api.Metrics
	// Balancer is xray's current selection per balancer; nil when the API
	// could not be asked.
	Balancer map[string]api.BalancerInfo

	Overrides localctl.Overrides
	Index     *localctl.EntriesIndex
	// PanelRemark/PanelIndex are the operator config's entry choice;
	// HasOperatorConfig is false when the router has none yet.
	HasOperatorConfig bool
	PanelRemark       string
	PanelIndex        int
	// UserAgentProblem is the uaguard refusal for the configured agent, "".
	UserAgentProblem string

	TableLoaded bool
	// Counters are the nft named counters (packets); nil when unreadable.
	Counters map[string]int64

	AgentEnabled    bool
	PasswallRunning bool
	// PassWall is PassWall2 on the router — installed, retired or absent,
	// "" when not looked at — and PassWallRetiredAt when it was retired.
	PassWall          string
	PassWallRetiredAt time.Time

	Router         RouterFacts
	XrayRSSMiB     *float64
	MemoryLimitMiB *int

	// UILocked is the operator's lock on the router UI (UCI ui_lock); rpcd
	// reads it at every call.
	UILocked bool
	// RemoteShell is the owner's switch for the panel's support shell (UCI
	// remote_shell); rpcd reads it at every status call.
	RemoteShell bool

	// Power is whether Vectra is switched on and who carries the traffic;
	// rpcd reads it at every status call, the daemon up or not.
	Power power.Facts

	// Tune is the router's tune as it is now (tune.Inspect); nil when not read.
	Tune *tune.Plan

	// Brand is whose router this is (BuildBrand); rpcd resolves it at every
	// status call. The zero value is neutral.
	Brand BrandView
}

// RouterFacts are /proc and friends.
type RouterFacts struct {
	Hostname        string
	Model           string
	Release         string
	MemTotalMiB     *int
	MemAvailableMiB *int
	OverlayFreeMiB  *int
	TmpFreeMiB      *int
	Load            []float64
	UptimeSec       *int
}

// Well-known named counters of the vctl nft table (internal/firewall).
var knownCounters = []string{"vctl_tproxy_hits", "vctl_egress_exempt", "vctl_output_marked", "vctl_killswitch_drops", "vctl_would_leak"}

func ptr[T any](v T) *T { return &v }

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func timePtr(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func secs(d time.Duration) int { return int(d / time.Second) }

// BuildStatus answers `status`.
func nodeRef(tag string, egress map[string]string) NodeRef {
	r := NodeRef{Tag: tag}
	cc := xrayview.CountryHint(tag)
	if cc != "" {
		r.Country = &cc
	}
	if e := egress[tag]; e != "" && e != cc {
		r.Egress = &e
	}
	return r
}

// buildRoute is the server card's line from the watchdog's last look.
func buildRoute(r *localctl.Route, egress map[string]string) *RouteView {
	if r == nil {
		return nil
	}
	v := &RouteView{Nodes: []NodeRef{}, Unfit: []NodeRef{}, UnfitKept: []NodeRef{}}
	for _, t := range r.Unfit {
		v.Unfit = append(v.Unfit, nodeRef(t, egress))
	}
	for _, t := range r.UnfitKept {
		v.UnfitKept = append(v.UnfitKept, nodeRef(t, egress))
	}
	if r.Override != "" {
		v.Nodes = append(v.Nodes, nodeRef(r.Override, egress))
	} else {
		for _, n := range r.Nodes {
			v.Nodes = append(v.Nodes, nodeRef(n, egress))
		}
	}
	if r.MovedFrom != "" {
		m := nodeRef(r.MovedFrom, egress)
		v.MovedFrom, v.MovedAt = &m, r.MovedAt
		reason := r.Reason
		v.Reason = &reason
	}
	return v
}

func BuildStatus(in Inputs) Status {
	st := Status{
		Version: in.Version,
		Engine:  Engine{State: "unknown"},
		Pins:    map[string]string{},
		Legacy: Legacy{AgentEnabled: in.AgentEnabled, PasswallRunning: in.PasswallRunning,
			Passwall: strPtr(in.PassWall), PasswallRetiredAt: timePtr(in.PassWallRetiredAt)},
		Router: Router{
			Hostname:        in.Router.Hostname,
			Model:           strPtr(in.Router.Model),
			Release:         strPtr(in.Router.Release),
			MemTotalMiB:     in.Router.MemTotalMiB,
			MemAvailableMiB: in.Router.MemAvailableMiB,
			OverlayFreeMiB:  in.Router.OverlayFreeMiB,
			TmpFreeMiB:      in.Router.TmpFreeMiB,
			Load:            in.Router.Load,
			UptimeSec:       in.Router.UptimeSec,
		},
		Subscription: SubscriptionState{Source: "panel"},
		Probe:        ProbeState{Source: "provider"},
		UI:           UIPolicy{Locked: in.UILocked},
		RemoteShell:  in.RemoteShell,
		Brand:        in.Brand,
		Power: Power{Enabled: in.Power.On(), Running: in.Power.Running, Holder: in.Power.Holder(),
			HandBack: strPtr(in.Power.HandBack()), WouldIdle: in.Power.WouldIdle},
	}
	if in.Brand.LANName == "" {
		st.Brand = BrandView{LANName: brand.NeutralLANName}
	}
	for b, n := range in.Overrides.Pins {
		st.Pins[b] = n
	}

	if in.Runtime != nil {
		st.Route = buildRoute(in.Runtime.Route, in.Runtime.Egress)
	}
	if rt := in.Runtime; rt != nil {
		st.Controller = Controller{Running: true, PID: ptr(rt.ControllerPID)}
		if !rt.StartedAt.IsZero() {
			st.Controller.UptimeSec = ptr(secs(in.Now.Sub(rt.StartedAt)))
		}
		e := rt.Engine
		st.Engine = Engine{State: e.State, XrayVersion: strPtr(rt.XrayVersion), Restarts: e.Restarts,
			RSSMiB: in.XrayRSSMiB, MemoryLimitMiB: in.MemoryLimitMiB}
		if e.State == "running" && e.PID > 0 {
			st.Engine.PID = ptr(e.PID)
			if !e.StartedAt.IsZero() {
				st.Engine.UptimeSec = ptr(secs(in.Now.Sub(e.StartedAt)))
			}
		}
		if !e.LastExitAt.IsZero() || e.LastExitErr != "" {
			st.Engine.LastExit = &LastExit{At: timePtr(e.LastExitAt), Code: e.LastExitCode, Error: e.LastExitErr}
		}
		st.Dataplane.KillSwitch = rt.KillSwitch
		st.ControlPlane = ControlPlane{Reachable: rt.PanelReachable, RouterID: strPtr(rt.RouterID)}
		if rt.LastCheckIn != nil {
			st.ControlPlane.LastCheckIn = timePtr(*rt.LastCheckIn)
		}
		if p := rt.Probe; p != nil {
			st.Probe = ProbeState{IntervalSec: ptr(p.IntervalSec), Source: p.Source}
		}
		if en := rt.Entry; en != nil {
			st.Subscription = SubscriptionState{
				EntryIndex: ptr(en.Index), EntryRemark: strPtr(en.Remark), EntryCount: ptr(en.Count),
				FetchedAt: timePtr(en.FetchedAt), Source: sourceOf(en.Local), OverrideStale: en.Stale,
			}
		}
	}
	if st.Subscription.EntryCount == nil && in.Index != nil {
		st.Subscription.EntryCount = ptr(len(in.Index.Entries))
		st.Subscription.FetchedAt = timePtr(in.Index.FetchedAt)
	}
	if st.Probe.IntervalSec == nil && in.View != nil && in.View.HasObservatory {
		st.Probe.IntervalSec = ptr(secs(in.View.ProbeInterval))
	}

	if in.View != nil {
		st.API.Listen = strPtr(in.View.APIListen)
		st.Metrics.Listen = strPtr(in.View.MetricsListen)
	}
	st.API.Reachable = in.APIReachable
	st.Metrics.Reachable = in.MetricsReachable

	st.Dataplane.Loaded = in.TableLoaded
	if in.Counters != nil {
		st.Dataplane.Counters = map[string]int64{}
		for k, v := range in.Counters {
			st.Dataplane.Counters[k] = v
		}
	}
	if p := in.Tune; p != nil {
		st.Tune = &Tune{Enabled: p.On, Profile: p.Profile, Items: make([]TuneItem, 0, len(p.Items))}
		for _, it := range p.Items {
			st.Tune.Items = append(st.Tune.Items, TuneItem{ID: it.ID, State: it.State, Value: it.Value, Target: it.Target,
				Reason: strPtr(it.Reason)})
		}
	}
	return st
}

func sourceOf(local bool) string {
	if local {
		return "local"
	}
	return "panel"
}

// BuildBalancers answers `balancers`.
func BuildBalancers(in Inputs) Balancers {
	out := Balancers{APIReachable: in.APIReachable && in.Balancer != nil, Balancers: []Balancer{}}
	out.Probe.Source = "provider"
	if rt := in.Runtime; rt != nil && rt.Probe != nil {
		out.Probe.Source = rt.Probe.Source
		out.Probe.ProviderIntervalSec = ptr(rt.Probe.ProviderIntervalSec)
	} else if in.ProviderProbeInterval > 0 {
		out.Probe.ProviderIntervalSec = ptr(secs(in.ProviderProbeInterval))
	}
	v := in.View
	if v == nil {
		return out
	}
	if v.Default != nil {
		out.DefaultOutbound = strPtr(v.Default.Tag)
	}
	if v.HasObservatory {
		out.Probe.IntervalSec = ptr(secs(v.ProbeInterval))
		out.Probe.Sampling = ptr(v.ProbeSampling)
		if v.ProbeTimeout > 0 {
			out.Probe.TimeoutSec = ptr(secs(v.ProbeTimeout))
		}
		out.Probe.Destination = strPtr(v.ProbeDestination)
	}
	for _, b := range v.Balancers {
		nb := Balancer{
			Tag: b.Tag, Role: b.Role, Strategy: b.Strategy, Expected: b.Expected,
			Selector: nonNil(b.Selector), Members: nonNil(b.Members), Matchers: []Matcher{},
		}
		if b.FallbackTag != "" {
			nb.Fallback = &Fallback{Tag: b.FallbackTag, Balancer: strPtr(b.FallbackBalancer)}
		}
		if in.Balancer != nil {
			if info, ok := in.Balancer[b.Tag]; ok && info.Err == nil {
				nb.Selected = nonNil(info.Principle)
				if info.Override != "" {
					nb.Pinned = strPtr(info.Override)
				}
			}
		}
		if nb.Pinned == nil {
			// xray not asked (or not up): what the router will re-apply.
			if p, ok := in.Overrides.Pins[b.Tag]; ok && in.Balancer == nil {
				nb.Pinned = strPtr(p)
			}
		}
		for _, m := range b.Rules {
			nb.Matchers = append(nb.Matchers, Matcher{Kind: m.Kind, Network: strPtr(m.Network), Sample: nonNil(m.Sample), Total: m.Total})
		}
		out.Balancers = append(out.Balancers, nb)
	}
	return out
}

// BuildNodes answers `nodes`.
func BuildNodes(in Inputs) Nodes {
	out := Nodes{Nodes: []Node{}}
	if in.Metrics != nil {
		out.ObservedAt = timePtr(in.Now)
	}
	v := in.View
	if v == nil {
		return out
	}
	for _, o := range v.Outbounds {
		if !o.Dials {
			continue
		}
		n := Node{
			Tag: o.Tag, Protocol: o.Protocol, Transport: o.Transport, Security: o.Security,
			Address: strPtr(o.Address), CountryHint: countryHint(o.Tag), Balancers: nonNil(v.BalancersOf(o.Tag)),
		}
		if o.Port > 0 {
			n.Port = ptr(o.Port)
		}
		if in.Metrics != nil {
			if ob, ok := in.Metrics.Observatory[o.Tag]; ok {
				n.Alive = ptr(ob.Alive)
				if ob.Alive {
					delay := ob.DelayMs
					if delay == 0 && ob.HealthPing != nil {
						delay = ob.HealthPing.Average / int64(time.Millisecond)
					}
					n.DelayMs = ptr(int(delay))
				}
				if ob.LastSeen > 0 {
					n.LastSeen = timePtr(time.Unix(ob.LastSeen, 0))
				}
				if ob.LastTry > 0 {
					n.LastTry = timePtr(time.Unix(ob.LastTry, 0))
				}
			}
			if tr, ok := in.Metrics.Stats.Outbound[o.Tag]; ok {
				n.Traffic = &Traffic{UpBytes: tr.Uplink, DownBytes: tr.Downlink}
			}
		}
		out.Nodes = append(out.Nodes, n)
	}
	return out
}

// BuildRules answers `rules` from the overrides: what the router keeps is
// what it runs, since a change is kept only once it is running.
func BuildRules(ov localctl.Overrides, catalog []string) Rules {
	r := Rules{Direct: nonNil(ov.Direct), Proxy: nonNil(ov.Proxy), Max: sites.Max, Catalog: nonNil(catalog), Missing: []string{}}
	if len(catalog) == 0 {
		// No geo file read: nothing is judged missing.
		return r
	}
	have := map[string]bool{}
	for _, c := range catalog {
		have[c] = true
	}
	for _, e := range append(append([]string(nil), r.Direct...), r.Proxy...) {
		if name, ok := strings.CutPrefix(e, sites.ServicePrefix); ok && !have[name] {
			r.Missing = append(r.Missing, e)
		}
	}
	return r
}

// BuildEntries answers `entries`.
func BuildEntries(in Inputs) Entries {
	out := Entries{Source: "panel", Entries: []Entry{}}
	if in.Overrides.HasEntry() {
		out.Source = "local"
	}
	if in.Index == nil {
		return out
	}
	out.Cached = true
	out.FetchedAt = timePtr(in.Index.FetchedAt)
	for _, e := range in.Index.Entries {
		out.Entries = append(out.Entries, Entry{Index: e.Index, Remark: e.Remark, NodeCount: e.NodeCount, BalancerCount: e.BalancerCount})
	}
	if in.HasOperatorConfig {
		if i, _, _, err := localctl.Resolve(remarksOf(in.Index), localctl.Overrides{}, in.PanelRemark, in.PanelIndex); err == nil {
			out.PanelIndex = ptr(i)
		}
	}
	if rt := in.Runtime; rt != nil && rt.Entry != nil {
		out.Active = ptr(rt.Entry.Index)
		out.Source = sourceOf(rt.Entry.Local)
		out.OverrideStale = rt.Entry.Stale
	} else if in.Overrides.HasEntry() && !in.Index.HasRemark(in.Overrides.EntryRemark) {
		out.OverrideStale = true
	}
	return out
}

func remarksOf(idx *localctl.EntriesIndex) []string {
	out := make([]string, len(idx.Entries))
	for i, e := range idx.Entries {
		out[i] = e.Remark
	}
	return out
}

// memoryThresholds are the memory check's lines, in MiB available: a failure
// only where the kernel is reclaiming the pages of running programs
// (memguard.CriticalKB: 5% of RAM, at least 12 MiB) — the router is about to
// kill — and a warning at twice that. A 234 MB router lives at 40-65 MB free
// under load, as the fleet did under PassWall2; a check that failed below 48
// told the owner «nothing works» while everything did (1111, 2026-09-30).
// Heavy jobs have their own floor (internal/jobsafety).
func memoryThresholds(totalMiB *int) (fail, warn int) {
	total := uint64(234)
	if totalMiB != nil && *totalMiB > 0 {
		total = uint64(*totalMiB)
	}
	fail = int(memguard.CriticalKB(total*1024) / 1024)
	return fail, 2 * fail
}

// BuildDiagnostics answers `diagnostics`: ids, statuses and params only.
func BuildDiagnostics(in Inputs) Diagnostics {
	d := Diagnostics{CheckedAt: in.Now.UTC().Format(time.RFC3339)}
	add := func(id, status string, params map[string]interface{}) {
		if params == nil {
			params = map[string]interface{}{}
		}
		d.Checks = append(d.Checks, Check{ID: id, Status: status, Params: params})
	}
	rt := in.Runtime

	if rt != nil {
		add("controller_running", "ok", map[string]interface{}{"pid": rt.ControllerPID})
	} else {
		add("controller_running", "fail", nil)
	}

	switch {
	case rt == nil:
		add("xray_running", "unknown", nil)
	case rt.Engine.State == "running":
		p := map[string]interface{}{"pid": rt.Engine.PID}
		if !rt.Engine.StartedAt.IsZero() {
			p["uptimeSec"] = secs(in.Now.Sub(rt.Engine.StartedAt))
		}
		add("xray_running", "ok", p)
	default:
		add("xray_running", "fail", map[string]interface{}{"state": rt.Engine.State})
	}

	switch {
	case in.View == nil:
		add("xray_api", "unknown", nil)
	case in.View.APIListen == "":
		add("xray_api", "warn", map[string]interface{}{"error": "the running config has no API"})
	case in.APIReachable:
		add("xray_api", "ok", map[string]interface{}{"listen": in.View.APIListen})
	default:
		add("xray_api", "fail", map[string]interface{}{"listen": in.View.APIListen, "error": "not answering"})
	}

	if in.TableLoaded {
		add("dataplane_loaded", "ok", map[string]interface{}{"tproxyHits": in.Counters["vctl_tproxy_hits"]})
	} else {
		add("dataplane_loaded", "fail", nil)
	}

	if in.Counters != nil {
		status, p := noLeak(in.Counters, rt)
		add("no_leak", status, p)
	}

	stack := map[string]interface{}{"passwallRunning": in.PasswallRunning, "agentEnabled": in.AgentEnabled}
	switch {
	case in.PasswallRunning:
		add("single_stack", "fail", stack)
	case in.AgentEnabled:
		add("single_stack", "warn", stack)
	default:
		add("single_stack", "ok", stack)
	}

	switch {
	case rt == nil || rt.PanelReachable == nil:
		add("panel_link", "unknown", nil)
	case !*rt.PanelReachable:
		p := map[string]interface{}{}
		if rt.LastCheckIn != nil {
			p["lastCheckInAgoSec"] = secs(in.Now.Sub(*rt.LastCheckIn))
		}
		add("panel_link", "fail", p)
	default:
		p := map[string]interface{}{}
		status := "ok"
		if rt.LastCheckIn != nil {
			ago := secs(in.Now.Sub(*rt.LastCheckIn))
			p["lastCheckInAgoSec"] = ago
			if ago > 10*60 {
				status = "warn"
			}
		}
		add("panel_link", status, p)
	}

	if in.HasOperatorConfig {
		if in.UserAgentProblem != "" {
			add("subscription_ua", "fail", map[string]interface{}{"reason": in.UserAgentProblem})
		} else {
			add("subscription_ua", "ok", nil)
		}
	}

	if avail := in.Router.MemAvailableMiB; avail != nil {
		p := map[string]interface{}{"availableMiB": *avail}
		if in.Router.MemTotalMiB != nil {
			p["totalMiB"] = *in.Router.MemTotalMiB
		}
		if in.XrayRSSMiB != nil {
			p["xrayMiB"] = int(*in.XrayRSSMiB + 0.5)
		}
		failMiB, warnMiB := memoryThresholds(in.Router.MemTotalMiB)
		switch {
		case *avail < failMiB:
			add("memory", "fail", p)
		case *avail < warnMiB:
			add("memory", "warn", p)
		default:
			add("memory", "ok", p)
		}
	}

	if p := in.Tune; p != nil {
		status, params := tuneCheck(p)
		add("tune", status, params)
	}

	if in.Metrics != nil && in.View != nil {
		var dead []string
		for _, o := range in.View.Outbounds {
			// A whitelist level is for a mobile network under a whitelist
			// regime: silent on any other connection, and that is no fault.
			if ob, ok := in.Metrics.Observatory[o.Tag]; ok && o.Dials && !ob.Alive && !xrayview.WhitelistLevel(o.Tag) {
				dead = append(dead, o.Tag)
			}
		}
		sort.Strings(dead)
		p := map[string]interface{}{"count": len(dead), "tags": capList(dead, 10)}
		if len(dead) > 0 {
			add("dead_nodes", "warn", p)
		} else {
			add("dead_nodes", "ok", p)
		}
	}

	// Judging where traffic goes takes both xray's picks (API) and its probes
	// (observatory): without either, no verdict at all rather than a guess.
	if in.Balancer != nil && in.View != nil && in.Metrics != nil {
		status, p := balancerFallback(in.View, in.Balancer, in.Metrics.Observatory)
		add("balancer_fallback", status, p)
	}

	if in.Metrics != nil && len(in.Overrides.Pins) > 0 {
		status, params := "ok", map[string]interface{}{}
		for _, b := range in.Overrides.PinnedBalancers() {
			n := in.Overrides.Pins[b]
			if ob, ok := in.Metrics.Observatory[n]; !ok || ob.Alive {
				continue
			}
			// xray sends a pinned balancer's traffic to the pin, dead or not; on
			// the main balancer that is everything no other rule took.
			main := false
			if in.View != nil {
				vb := in.View.Balancer(b)
				main = vb != nil && vb.Role == "main"
			}
			if status == "ok" || main {
				status, params = "warn", map[string]interface{}{"balancer": b, "node": n, "main": main}
			}
			if main {
				status = "fail"
				break
			}
		}
		add("pinned_node_dead", status, params)
	}

	// fail first, then warn, then the rest, stable within a status.
	rank := map[string]int{"fail": 0, "warn": 1, "unknown": 2, "ok": 3}
	sort.SliceStable(d.Checks, func(i, j int) bool { return rank[d.Checks[i].Status] < rank[d.Checks[j].Status] })
	return d
}

// tuneCheck is the tune's line: the items in place (set by the tune, or so
// before it), the zram swap's size, and a warning where a small router runs
// without its compressed swap — the memory guard and every measurement of it
// on the fleet assume one.
func tuneCheck(p *tune.Plan) (string, map[string]interface{}) {
	items := []string{}
	var zram interface{}
	status := "ok"
	for _, it := range p.Items {
		in := it.State == tune.Applied || it.State == tune.Already
		if in {
			items = append(items, it.ID)
		}
		if it.ID != tune.ItemZram {
			continue
		}
		if in && it.Value != nil {
			if mib, err := strconv.Atoi(*it.Value); err == nil {
				zram = mib
			}
		}
		if !in && p.Profile == tune.Lowmem {
			status = "warn"
		}
	}
	return status, map[string]interface{}{"enabled": p.On, "profile": p.Profile, "items": items, "zramMiB": zram}
}

// leakFailPackets is where packets past a running xray stop being a trickle:
// fewer warn, this many fail.
const leakFailPackets = 50

// noLeak judges the leak instruments of the loaded table (internal/firewall):
//   - vctl_would_leak (switch off) / vctl_killswitch_drops (switch on) count
//     the tcp/udp that TPROXY did not take. That includes the fall-through
//     while xray restarts — fail-open, or fail-closed, by design, on every
//     subscription refresh and every restart from the UI — so only what they
//     counted since the running xray had started (the daemon's baseline,
//     localctl.Runtime.LeakBaseline) is judged.
//   - vctl_tproxy_escaped counts packets TPROXY took that were then forwarded
//     out unproxied (the fwmark policy route is gone). Never by design.
//   - vctl_unproxied_other is everything but tcp/udp (a LAN ping): never
//     proxied by design, shown for information only.
func noLeak(counters map[string]int64, rt *localctl.Runtime) (string, map[string]interface{}) {
	leak := counters[firewall.CounterKillSwitchShadow]
	escaped, measuresEscape := counters[firewall.CounterTproxyEscaped]
	drops, armed := counters[firewall.CounterKillSwitchDrops]
	hits := counters[firewall.CounterTproxyHits]
	p := map[string]interface{}{"wouldLeak": leak, "sinceStart": nil, "escaped": nil, "killSwitch": armed, "other": nil}
	if other, ok := counters[firewall.CounterUnproxiedOther]; ok {
		p["other"] = other
	}
	if armed {
		p["drops"], p["dropsSinceStart"] = drops, nil
	}
	if rt == nil || rt.LeakBaseline == nil {
		// Without a baseline a restart window cannot be told from traffic that
		// got past a running xray.
		if leak > 0 || escaped > 0 || (armed && drops > 0) {
			return "unknown", p
		}
		return "ok", p
	}
	b := *rt.LeakBaseline
	if hits < b.TproxyHits || leak < b.Packets || escaped < b.Escaped || drops < b.Drops {
		// The ruleset was loaded again since: its counters began again at 0.
		b = localctl.LeakBaseline{}
	}
	since, escapedSince, dropsSince := leak-b.Packets, escaped-b.Escaped, drops-b.Drops
	p["sinceStart"] = since
	if measuresEscape {
		p["escaped"] = escapedSince
	}
	if armed {
		p["dropsSinceStart"] = dropsSince
	} else {
		dropsSince = 0
	}
	switch {
	case escapedSince > 0 || since >= leakFailPackets || dropsSince >= leakFailPackets:
		return "fail", p
	case since > 0 || dropsSince > 0:
		return "warn", p
	}
	return "ok", p
}

// maxFallbackSteps bounds one walk down a fallback chain, as the UI's rail
// does (ui/app/src/lib/balancing.ts).
const maxFallbackSteps = 32

// chainEnd is where a chain's traffic ends up.
type chainEnd int

const (
	endCarried chainEnd = iota // a balancer, a pin, or a live (or unprobed) node takes it
	endUnknown                 // the router cannot tell
	endBlocked                 // nothing takes it: no such outbound, a blackhole, only dead nodes, a loop
	endDirect                  // a freedom outbound: it leaves WITHOUT the VPN
)

// balancerDoes is what one balancer does with its traffic right now.
type balancerDoes int

const (
	doesCarry   balancerDoes = iota
	doesPassOn               // to its fallbackTag, or to xray's default outbound
	doesStick                // keeps it on members that are all dead
	doesUnknown              // the router cannot tell
)

// balancerVerdict is xray's own decision (v26.3.27, app/router/balancing.go
// and strategy_*.go):
//   - a pin (override) takes everything, healthy or not;
//   - leastLoad and leastPing pick among members the observatory saw alive and
//     report the pick as principle targets: none means none qualifies;
//   - random and roundRobin report EVERY member as a principle target, so their
//     health comes from the observatory. With a fallbackTag they skip members
//     it saw dead (one it has no record of counts as alive) and pass the
//     traffic on when none is left; without one they pick among all members,
//     dead ones included.
func balancerVerdict(b *xrayview.Balancer, info map[string]api.BalancerInfo, obs map[string]api.Observation) balancerDoes {
	bi, asked := info[b.Tag]
	switch {
	case !asked || bi.Err != nil:
		return doesUnknown
	case bi.Override != "":
		return doesCarry
	case len(b.Members) == 0:
		return doesPassOn
	}
	switch strings.ToLower(b.Strategy) {
	case "leastload", "leastping":
		if len(bi.Principle) > 0 {
			return doesCarry
		}
		return doesPassOn
	case "random", "roundrobin", "":
		alive, unprobed := 0, 0
		for _, m := range b.Members {
			if o, ok := obs[m]; !ok {
				unprobed++
			} else if o.Alive {
				alive++
			}
		}
		switch {
		case alive > 0:
			return doesCarry
		case unprobed > 0:
			return doesUnknown
		case b.FallbackTag == "":
			return doesStick
		}
		return doesPassOn
	}
	return doesUnknown
}

// outboundEnd is where traffic handed to outbound o ends; nil is a tag the
// config does not have, and xray closes such a connection.
func outboundEnd(o *xrayview.Outbound, obs map[string]api.Observation) chainEnd {
	switch {
	case o == nil, o.Protocol == "blackhole":
		return endBlocked
	case o.Protocol == "freedom":
		return endDirect
	case !o.Dials:
		return endUnknown // a loopback into rules the router does not follow, dns
	}
	if r, ok := obs[o.Tag]; ok && !r.Alive {
		return endBlocked
	}
	return endCarried
}

// walkChain follows the traffic of one chain from start, past every balancer
// that passes it on, to whatever takes it — or to where the router can no
// longer tell. passedOn are those balancers, in order.
func walkChain(v *xrayview.View, start *xrayview.Balancer, info map[string]api.BalancerInfo, obs map[string]api.Observation) (end chainEnd, passedOn []string) {
	seen := map[string]bool{}
	for b, steps := start, 0; steps < maxFallbackSteps; steps++ {
		if seen[b.Tag] {
			return endBlocked, passedOn // round a loop, and nothing on it takes the traffic
		}
		seen[b.Tag] = true
		switch balancerVerdict(b, info, obs) {
		case doesCarry:
			return endCarried, passedOn
		case doesUnknown:
			return endUnknown, passedOn
		case doesStick:
			return endBlocked, passedOn
		}
		passedOn = append(passedOn, b.Tag)
		switch {
		case b.FallbackBalancer != "":
			if b = v.Balancer(b.FallbackBalancer); b == nil {
				return endUnknown, passedOn
			}
		case b.FallbackTag != "":
			return outboundEnd(v.Outbound(b.FallbackTag), obs), passedOn
		default:
			// Neither a candidate nor a fallback: xray hands the connection to
			// its default handler, the config's first outbound.
			return outboundEnd(v.Default, obs), passedOn
		}
	}
	return endUnknown, passedOn
}

// balancerFallback follows the traffic of the main balancer, then of each
// routed one in config order (walkChain). A balancer no traffic reaches is
// never reported.
func balancerFallback(v *xrayview.View, info map[string]api.BalancerInfo, obs map[string]api.Observation) (string, map[string]interface{}) {
	p := map[string]interface{}{"balancers": []string{}, "main": false, "mainBlocked": false, "mainDirect": false,
		"blocked": false, "blockedBalancers": []string{}, "warming": false}
	var starts []*xrayview.Balancer
	for _, role := range []string{"main", "routed"} {
		for i := range v.Balancers {
			if b := &v.Balancers[i]; b.Role == role {
				starts = append(starts, b)
			}
		}
	}
	for _, s := range starts {
		// Right after an xray start the observatory has probed nothing yet and
		// leastLoad/leastPing have no target, so every balancer would look like
		// it passes its traffic on. Say that, not a guess.
		if s.Role == "main" && len(s.Members) > 0 && !anyProbed(s.Members, obs) {
			p["warming"] = true
			return "unknown", p
		}
	}
	var falling, blocked []string
	flagged := map[string]bool{}
	main, mainBlocked, mainDirect := false, false, false
	for _, s := range starts {
		end, passedOn := walkChain(v, s, info, obs)
		for _, t := range passedOn {
			if !flagged[t] {
				flagged[t] = true
				falling = append(falling, t)
			}
		}
		isMain := s.Role == "main"
		main = main || (isMain && len(passedOn) > 0)
		switch end {
		case endBlocked:
			blocked = append(blocked, s.Tag)
			mainBlocked = mainBlocked || isMain
		case endDirect:
			mainDirect = mainDirect || isMain
		}
	}
	p["balancers"], p["main"], p["mainBlocked"], p["mainDirect"] = capList(falling, 10), main, mainBlocked, mainDirect
	p["blocked"], p["blockedBalancers"] = len(blocked) > 0, capList(blocked, 10)
	switch {
	case len(blocked) > 0 || mainDirect:
		return "fail", p
	case len(falling) > 0:
		return "warn", p
	}
	return "ok", p
}

func anyProbed(tags []string, obs map[string]api.Observation) bool {
	for _, t := range tags {
		if _, ok := obs[t]; ok {
			return true
		}
	}
	return false
}

func capList(xs []string, n int) []string {
	if xs == nil {
		return []string{}
	}
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// countryHint is the node's country as its tag names it (xrayview.CountryHint).
func countryHint(tag string) *string {
	if cc := xrayview.CountryHint(tag); cc != "" {
		return &cc
	}
	return nil
}
