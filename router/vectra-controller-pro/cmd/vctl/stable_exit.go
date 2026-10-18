package main

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/vault"
	"vectra-controller-pro/internal/xrayview"
)

// The stable exit (r26, docs/superpowers/specs/2026-10-18-vctl-services-as-
// data.md §3): one node of the main path that keeps one address, for the
// services on «VPN · постоянный IP». Measured on 1111 (2026-10-18): the main
// path gave 4 addresses in 8 requests, and most nodes rotate theirs at their
// end (nl5 3 of 6, de5 3, fin5 2, pl5 2, fr5 1) — a launcher that ties its
// login to the address (Star Citizen's) logs out. A full round asks every
// candidate several times, each on a new connection; a pin stays while it
// keeps its address, and is rechecked between full rounds.

const (
	stableExitFirst    = 2 * time.Minute
	stableExitTick     = time.Minute
	stableRecheckAsk   = 3
	stableExitTimeout  = 8 * time.Second
	stableRoundTimeout = 5 * time.Minute
	stableMissesToMove = 2
)

// The states the UIs show (uiapi.StableExitView).
const (
	stableStatePinned   = "pinned"
	stableStateNone     = "none"
	stableStateChecking = "checking"
	stableStateOff      = "off"
)

// stableExitRecord is what the router keeps across restarts, so a restart
// keeps the same address. The address never leaves the router.
type stableExitRecord struct {
	State     string    `json:"state"`
	Tag       string    `json:"tag,omitempty"`
	IP        string    `json:"ip,omitempty"`
	Country   string    `json:"country,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitempty"`
	FullAt    time.Time `json:"fullAt,omitempty"`
}

type stableExitTracker struct {
	mu       sync.Mutex
	rec      stableExitRecord
	loaded   bool
	misses   int
	nextFull time.Time
	// renderDue: the record changed and the loop has not rendered it yet
	// (busy, or a render refused for now): asked again at the next tick.
	renderDue bool
}

// Seams: tests never ask a real exit.
var (
	stableSteady = func(ctx context.Context, p exitcheck.Prober, tag, url string, n int) ([]string, time.Duration, int) {
		return p.Steady(ctx, tag, url, n)
	}
	stableJitter = func() float64 { return 0.9 + 0.2*rand.Float64() }
)

func (d *daemon) stableExitPath() string {
	return filepath.Join(filepath.Dir(d.cfg.StatePath), "stable-exit.json")
}

// stableExitNow is the record, read from disk once.
func (d *daemon) stableExitNow() stableExitRecord {
	d.stable.mu.Lock()
	defer d.stable.mu.Unlock()
	if !d.stable.loaded {
		d.stable.loaded = true
		if raw, err := os.ReadFile(d.stableExitPath()); err == nil {
			var r stableExitRecord
			if json.Unmarshal(raw, &r) == nil {
				d.stable.rec = r
			}
		}
	}
	return d.stable.rec
}

func (d *daemon) saveStableExit(r stableExitRecord) {
	d.stable.mu.Lock()
	d.stable.rec, d.stable.loaded = r, true
	d.stable.mu.Unlock()
	raw, err := json.Marshal(r)
	if err != nil {
		return
	}
	if err := localctl.WriteFileAtomic(d.stableExitPath(), append(raw, '\n'), 0o600); err != nil {
		logging.L().Warn("stable exit: not saved", "err", err.Error())
	}
}

// withStableExit renders the pin when the document still has it.
func (d *daemon) withStableExit(opts xray.SpliceOptions, providerRaw []byte) xray.SpliceOptions {
	if !d.exitCheckOn() {
		return opts
	}
	r := d.stableExitNow()
	if r.State != stableStatePinned || r.Tag == "" {
		return opts
	}
	if v, err := xrayview.Parse(providerRaw); err == nil {
		if o := v.Outbound(r.Tag); o != nil && o.Dials {
			opts.StablePin = r.Tag
		}
	}
	return opts
}

// stableExitView is what the UIs show: the state and the country, never the
// address.
func (d *daemon) stableExitView(ov localctl.Overrides) (state, country string, checkedAt *time.Time) {
	c := xray.ActiveCatalog()
	if !d.exitCheckOn() {
		return stableStateOff, "", nil
	}
	r := d.stableExitNow()
	if r.State == "" {
		if ov.NeedsStableExit(c) {
			return stableStateChecking, "", nil
		}
		return stableStateNone, "", nil
	}
	if !r.CheckedAt.IsZero() {
		t := r.CheckedAt
		checkedAt = &t
	}
	if r.State == stableStatePinned {
		return r.State, r.Country, checkedAt
	}
	return r.State, "", checkedAt
}

func (d *daemon) watchStableExit(ctx context.Context) {
	if !d.exitCheckOn() {
		return
	}
	t := time.NewTimer(stableExitFirst)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			d.stableExitStep(ctx, now)
			t.Reset(stableExitTick)
		}
	}
}

// stableExitStep does what is due: nothing while no service rides the stable
// exit, a render the last change still waits for, a recheck of the pin, or a
// full round. What is due is decided before the render is read.
func (d *daemon) stableExitStep(ctx context.Context, now time.Time) {
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	c := xray.ActiveCatalog()
	if !ov.NeedsStableExit(c) {
		return
	}
	d.stable.mu.Lock()
	renderDue := d.stable.renderDue
	d.stable.mu.Unlock()
	if renderDue {
		d.stableAskRender(ctx)
	}
	set := c.StableExitSettings()
	r := d.stableExitNow()
	recheck := time.Duration(set.RecheckMinutes) * time.Minute
	full := r.FullAt.IsZero() || r.State == "" || (r.State != stableStatePinned && now.Sub(r.CheckedAt) >= recheck)
	d.stable.mu.Lock()
	if d.stable.nextFull.IsZero() && !r.FullAt.IsZero() {
		d.stable.nextFull = r.FullAt.Add(time.Duration(float64(time.Duration(set.EveryHours)*time.Hour) * stableJitter()))
	}
	if !d.stable.nextFull.IsZero() && !now.Before(d.stable.nextFull) {
		full = true
	}
	d.stable.mu.Unlock()
	if !full && now.Sub(r.CheckedAt) < recheck {
		return // nothing due: the render is not even read
	}
	render, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return
	}
	cands := stableCandidates(render, d.exits.Unfit())
	if len(cands) == 0 {
		return // no probe in the running render yet
	}
	if r.State == stableStatePinned && !contains(cands, r.Tag) {
		full = true
	}
	p := exitcheck.Prober{Listen: xray.DefaultExitProbeListen, Timeout: stableExitTimeout}
	if !full {
		rctx, cancel := context.WithTimeout(ctx, time.Minute)
		ips, _, failed := stableSteady(rctx, p, r.Tag, set.URL, stableRecheckAsk)
		cancel()
		switch {
		case len(ips) == 1 && ips[0] == r.IP:
			d.stable.mu.Lock()
			d.stable.misses = 0
			d.stable.mu.Unlock()
			r.CheckedAt = now
			d.saveStableExit(r)
			return
		case len(ips) == 0 && failed > 0:
			d.stable.mu.Lock()
			d.stable.misses++
			misses := d.stable.misses
			d.stable.mu.Unlock()
			if misses < stableMissesToMove {
				return // one silent recheck is no verdict; the balancer falls back meanwhile
			}
		}
		logging.L().Info("stable exit: the pin changed its address or stopped answering; looking again", "exit", r.Tag, "addresses", len(ips))
	}
	d.stableExitRound(ctx, now, cands, set, r)
}

// stableAskRender has the loop render the record; remembered while the loop
// does not.
func (d *daemon) stableAskRender(ctx context.Context) {
	ok := d.askReapply(ctx, "stable exit")
	d.stable.mu.Lock()
	d.stable.renderDue = !ok
	d.stable.mu.Unlock()
}

// stableExitRound asks every candidate and keeps or moves the pin.
func (d *daemon) stableExitRound(ctx context.Context, now time.Time, cands []string, set catalog.StableExit, prev stableExitRecord) {
	p := exitcheck.Prober{Listen: xray.DefaultExitProbeListen, Timeout: stableExitTimeout}
	rctx, cancel := context.WithTimeout(ctx, stableRoundTimeout)
	defer cancel()
	type steady struct {
		tag    string
		ip     string
		median time.Duration
	}
	var stable []steady
	// The current pin first: a round the deadline cuts short still judges it.
	order := append([]string(nil), cands...)
	if prev.Tag != "" && contains(order, prev.Tag) {
		order = append([]string{prev.Tag}, without(order, prev.Tag)...)
	}
	pinAnswered := false
	for _, tag := range order {
		ips, median, failed := stableSteady(rctx, p, tag, set.URL, set.Samples)
		if tag == prev.Tag && len(ips) > 0 {
			pinAnswered = true
		}
		if len(ips) == 1 && failed == 0 {
			stable = append(stable, steady{tag, ips[0], median})
		}
	}
	if rctx.Err() != nil && ctx.Err() != nil {
		return
	}
	next := stableExitRecord{State: stableStateNone, CheckedAt: now, FullAt: now}
	var pick *steady
	for i := range stable {
		if stable[i].tag == prev.Tag && stable[i].ip == prev.IP {
			pick = &stable[i] // the pin stays while it keeps its address
			break
		}
	}
	if pick == nil {
		for i := range stable {
			if pick == nil || stable[i].median < pick.median {
				pick = &stable[i]
			}
		}
	}
	switch {
	case pick != nil:
		next.State, next.Tag, next.IP = stableStatePinned, pick.tag, pick.ip
		next.Country = d.exitCountry(pick.tag)
	case prev.State == stableStatePinned && pinAnswered && contains(cands, prev.Tag):
		// Nothing steadier, and the pin still answers: kept, so the render
		// and xray stay as they are (one node still beats the balancer's
		// rotation) — a move only when a steady node appears.
		next = prev
		next.CheckedAt, next.FullAt = now, now
		logging.L().Info("stable exit: no node keeps one address now; the pin is kept", "exit", prev.Tag)
	}
	d.stable.mu.Lock()
	d.stable.misses = 0
	d.stable.nextFull = now.Add(time.Duration(float64(time.Duration(set.EveryHours)*time.Hour) * stableJitter()))
	d.stable.mu.Unlock()
	d.saveStableExit(next)
	logging.L().Info("stable exit: round", "candidates", len(cands), "steady", len(stable), "pin", next.Tag, "country", next.Country)
	if next.Tag != prev.Tag || next.State != prev.State {
		d.stableAskRender(ctx)
	}
}

// exitCountry is where the exit was seen leaving (the exit check), else its
// name's country.
func (d *daemon) exitCountry(tag string) string {
	if loc, ok := d.exits.EgressSnapshot()[tag]; ok && loc.CC != "" {
		return loc.CC
	}
	return xrayview.CountryHint(tag)
}

// stableCandidates are the main path's dialling members that the exit probe
// reaches, the unfit ones left out, in the main path's order. The main path
// is the stable fallback's target when the main path rides the stable exit,
// else the first catch-all rule's.
func stableCandidates(render []byte, unfit []string) []string {
	probed, _, _ := renderExits(render)
	if len(probed) == 0 {
		return nil
	}
	var doc struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
		Routing struct {
			Rules     []map[string]json.RawMessage `json:"rules"`
			Balancers []struct {
				Tag      string   `json:"tag"`
				Selector []string `json:"selector"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if json.Unmarshal(render, &doc) != nil {
		return nil
	}
	target, isBalancer := "", false
	takeTarget := func(r map[string]json.RawMessage) {
		var s string
		if json.Unmarshal(r["balancerTag"], &s) == nil && s != "" {
			target, isBalancer = s, true
			return
		}
		if json.Unmarshal(r["outboundTag"], &s) == nil {
			target = s
		}
	}
	for _, r := range doc.Routing.Rules {
		var inb []string
		if json.Unmarshal(r["inboundTag"], &inb) == nil && len(inb) == 1 && inb[0] == "vctl-stable-back" {
			takeTarget(r)
			break
		}
	}
	if target == "" {
		for _, r := range doc.Routing.Rules {
			if mainCatchAll(r) {
				takeTarget(r)
				break
			}
		}
	}
	if target == "" || target == xray.StableBalancerTag {
		return nil
	}
	var members []string
	if isBalancer {
		for _, b := range doc.Routing.Balancers {
			if b.Tag != target {
				continue
			}
			for _, o := range doc.Outbounds {
				for _, sel := range b.Selector {
					if strings.HasPrefix(o.Tag, sel) && !contains(members, o.Tag) {
						members = append(members, o.Tag)
					}
				}
			}
		}
	} else {
		members = []string{target}
	}
	var out []string
	for _, m := range members {
		if !contains(probed, m) || contains(unfit, m) {
			continue
		}
		prefix := false
		for _, o := range doc.Outbounds {
			if o.Tag != m && strings.HasPrefix(o.Tag, m) {
				prefix = true // a selector of it would pick that one too (xray.stablePinUsable)
			}
		}
		if !prefix {
			out = append(out, m)
		}
	}
	return out
}

// mainCatchAll: a rule for everything — no condition but a network naming
// tcp and udp (xray.isCatchAllRule, read from a render).
func mainCatchAll(r map[string]json.RawMessage) bool {
	network := ""
	for k, v := range r {
		switch strings.ToLower(k) {
		case "type", "outboundtag", "balancertag", "ruletag":
		case "network":
			var s string
			var list []string
			if json.Unmarshal(v, &s) != nil {
				_ = json.Unmarshal(v, &list)
				s = strings.Join(list, ",")
			}
			network = strings.ToLower(s)
		default:
			if t := strings.TrimSpace(string(v)); t != "" && t != "null" && t != `""` && t != "[]" && t != "{}" {
				return false
			}
		}
	}
	return strings.Contains(network, "tcp") && strings.Contains(network, "udp")
}

func without(xs []string, x string) []string {
	var out []string
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
