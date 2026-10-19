package main

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/supervisor"
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
	// Unsteady: the pin no longer keeps one address and no node does; it is
	// kept (no restart for nothing) and the UIs say there is no steady IP.
	Unsteady bool `json:"unsteady,omitempty"`
	// Ranked are the last round's steady nodes, the pin first, then the
	// fastest: where a pin that stops answering moves at once (r28,
	// stableFailover), without a probe and without a restart.
	Ranked []stableRank `json:"ranked,omitempty"`
}

// stableRank is a node a round found keeping one address.
type stableRank struct {
	Tag string `json:"tag"`
	IP  string `json:"ip"`
	Ms  int64  `json:"ms"`
}

type stableExitTracker struct {
	mu     sync.Mutex
	rec    stableExitRecord
	loaded bool
	// gen counts the record's saves: a round or a recheck that read an older
	// record never writes over a newer one (a move off a dead pin made while
	// it probed).
	gen      uint64
	misses   int
	nextFull time.Time
	// liveDue: the record changed and xray has not taken it yet (its API did
	// not answer): asked again at the next tick.
	liveDue bool
	// roundDue: a full round at the next tick — the pin died with no node to
	// move to, a new render has no room for it, or a round's result went
	// stale while it ran.
	roundDue bool
	// applied is the override the tracker last set on the running xray, ""
	// for none: it clears only its own, never the failover policy's.
	applied string
	// moveMu serialises the moves off a dead pin (the watchdog's, from the
	// connection table, and the tick's, from the observatory).
	moveMu sync.Mutex
	// refused is the last move xray refused: logged once.
	refused string
}

// Seams: tests never ask a real exit, nor a real xray.
var (
	stableSteady = func(ctx context.Context, p exitcheck.Prober, tag, url string, n int) ([]string, time.Duration, int) {
		return p.Steady(ctx, tag, url, n)
	}
	stableJitter   = func() float64 { return 0.9 + 0.2*rand.Float64() }
	stableOverride = api.OverrideBalancerTarget
	stableInfo     = api.GetBalancerInfo
	stableMetrics  = api.FetchMetrics
)

func (d *daemon) stableExitPath() string {
	return filepath.Join(filepath.Dir(d.cfg.StatePath), "stable-exit.json")
}

// stableExitNow is the record, read from disk once.
func (d *daemon) stableExitNow() stableExitRecord {
	r, _ := d.stableExitGen()
	return r
}

// stableExitGen is the record and its generation.
func (d *daemon) stableExitGen() (stableExitRecord, uint64) {
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
	return d.stable.rec, d.stable.gen
}

func (d *daemon) saveStableExit(r stableExitRecord) {
	d.saveStableExitIf(r, 0, false)
}

// saveStableExitIf saves r only while the record is still generation gen
// (when check): false when another save came first.
func (d *daemon) saveStableExitIf(r stableExitRecord, gen uint64, check bool) bool {
	d.stable.mu.Lock()
	if check && d.stable.gen != gen {
		d.stable.mu.Unlock()
		return false
	}
	d.stable.rec, d.stable.loaded = r, true
	d.stable.gen++
	d.stable.mu.Unlock()
	raw, err := json.Marshal(r)
	if err != nil {
		return true
	}
	if err := localctl.WriteFileAtomic(d.stableExitPath(), append(raw, '\n'), 0o600); err != nil {
		logging.L().Warn("stable exit: not saved", "err", err.Error())
	}
	return true
}

// The pin is xray's override of the stable balancer (r28): the render holds
// every node of the main path, the pin is set on it at run time — at every
// xray start (reapplyStablePin), and kept in line with the record by every
// tick and every look of the failover watchdog (stableReconcile). A move is
// no render and no restart: r26 rendered the pin, and its move at 23:19:19
// on 1111 (2026-10-19) restarted xray and dropped every connection in the
// house. While pinned, xray takes no fallback of its own (an override skips
// the strategy and the observatory): a pin that dies is moved by the
// tracker (stableMoveOff), on the connection table's word in seconds and on
// the observatory's at the next tick.

// stableTarget is what the stable balancer of view should be pinned to now
// ("" = nothing: its own choice among the main path's nodes). ok is false
// when the render has no stable balancer, or the owner pinned it: then it is
// not the tracker's to touch.
func (d *daemon) stableTarget(view *xrayview.View, ov localctl.Overrides) (target string, ok bool) {
	b := view.Balancer(xray.StableBalancerTag)
	if b == nil || ov.Pins[xray.StableBalancerTag] != "" {
		return "", false
	}
	r := d.stableExitNow()
	if r.State == stableStatePinned && r.Tag != "" && contains(b.Members, r.Tag) && !contains(d.exits.Unfit(), r.Tag) {
		// A kept unsteady pin too: one node still beats the balancer's
		// rotation. An exit the exit check found filtered carries nothing
		// stable: the balancer chooses until a round moves the pin.
		return r.Tag, true
	}
	return "", true
}

// stableReconcile brings the running xray's stable balancer in line with the
// record: the pin set when it is not (current, when known, is what xray
// holds now); with no pin, the tracker's own override cleared — a move the
// failover policy made while nothing was pinned stays its own. It reports
// false when xray did not take it.
func (d *daemon) stableReconcile(ctx context.Context, view *xrayview.View, ov localctl.Overrides, current string, known bool) bool {
	target, ok := d.stableTarget(view, ov)
	if !ok || !xrayview.Loopback(view.APIListen) {
		return true
	}
	d.stable.mu.Lock()
	applied := d.stable.applied
	d.stable.mu.Unlock()
	switch {
	case target != "" && known && current == target:
		d.stable.mu.Lock()
		d.stable.applied = target
		d.stable.mu.Unlock()
		return true
	case target == "" && (applied == "" || (known && current != applied)):
		return true // nothing of the tracker's to clear
	}
	octx, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := stableOverride(octx, view.APIListen, xray.StableBalancerTag, target)
	cancel()
	if err != nil {
		logging.L().Warn("stable exit: xray did not take the pin; asked again", "exit", target, "err", err.Error())
		return false
	}
	d.stable.mu.Lock()
	d.stable.applied = target
	d.stable.mu.Unlock()
	if target != applied {
		if target == "" {
			logging.L().Info("stable exit: no pin; the balancer chooses among the main path's nodes")
		} else {
			logging.L().Info("stable exit: pinned without a restart", "exit", target)
		}
	}
	return true
}

// stableApplyLive reconciles from the installed render; false when xray did
// not take it.
func (d *daemon) stableApplyLive(ctx context.Context) bool {
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return false
	}
	view, err := xrayview.Parse(raw)
	if err != nil {
		return true
	}
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	current, known := "", false
	if xrayview.Loopback(view.APIListen) && view.Balancer(xray.StableBalancerTag) != nil {
		ictx, cancel := context.WithTimeout(ctx, 3*time.Second)
		infos, err := stableInfo(ictx, view.APIListen, []string{xray.StableBalancerTag})
		cancel()
		if info, ok := infos[xray.StableBalancerTag]; err == nil && ok && info.Err == nil {
			current, known = info.Override, true
		}
	}
	return d.stableReconcile(ctx, view, ov, current, known)
}

// stableMoved has xray take the record, asked again at the next tick while
// it does not.
func (d *daemon) stableMoved(ctx context.Context) {
	ok := d.stableApplyLive(ctx)
	d.stable.mu.Lock()
	d.stable.liveDue = !ok
	d.stable.mu.Unlock()
}

// reapplyStablePin sets the pin on a new xray (the supervisor's start hook):
// xray keeps an override in memory only. It waits for the API within the
// start budget, and gives up once that xray is gone — the next tick sets it
// then (liveDue).
func (d *daemon) reapplyStablePin(pid int) {
	d.stable.mu.Lock()
	d.stable.applied = "" // a new xray holds no override
	d.stable.mu.Unlock()
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return
	}
	view, err := xrayview.Parse(raw)
	if err != nil || !xrayview.Loopback(view.APIListen) {
		return
	}
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	target, ok := d.stableTarget(view, ov)
	if !ok {
		return
	}
	if target == "" {
		if r := d.stableExitNow(); r.State == stableStatePinned {
			// The new render lost the pin (the provider dropped its node, or
			// the exit check left it out): a round at the next tick, not at
			// the next recheck — meanwhile the games would rotate.
			d.stable.mu.Lock()
			d.stable.roundDue = true
			d.stable.mu.Unlock()
			logging.L().Info("stable exit: the new render has no room for the pin; looking again", "exit", r.Tag)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.startBudget())
	defer cancel()
	for {
		if d.sup != nil {
			if st := d.sup.Status(); st.State != supervisor.StateRunning || st.PID != pid {
				return // gone, or replaced: that start sets its own
			}
		}
		cctx, ccancel := context.WithTimeout(ctx, 3*time.Second)
		err := stableOverride(cctx, view.APIListen, xray.StableBalancerTag, target)
		ccancel()
		if err == nil {
			d.stable.mu.Lock()
			d.stable.applied = target
			d.stable.mu.Unlock()
			logging.L().Info("stable exit pin restored", "exit", target, "xrayPid", pid)
			return
		}
		if errors.Is(err, api.ErrUnknownBalancer) || ctx.Err() != nil {
			d.stable.mu.Lock()
			d.stable.liveDue = true
			d.stable.mu.Unlock()
			logging.L().Warn("could not restore the stable exit pin; asked again at the next tick", "exit", target, "err", err.Error())
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// stableMoveOff moves a pin that died to the next node of the last round's
// ranking at once — no probe, no restart — and has its address rechecked at
// the next tick; with none left, the override is cleared (the balancer
// chooses, and falls back into the main path) and a full round is due now.
// dead says a node is dead; alive that another node of the stable balancer
// answers now — without it the router's own way out is down (a WAN blip),
// and the pin stays: a move would change the game's address for good. The
// game on a dead node is lost either way; this lands its next session on a
// steady address in seconds.
func (d *daemon) stableMoveOff(ctx context.Context, view *xrayview.View, ov localctl.Overrides, pin string, dead func(string) bool, alive bool, why string) {
	d.stable.moveMu.Lock()
	defer d.stable.moveMu.Unlock()
	b := view.Balancer(xray.StableBalancerTag)
	r, gen := d.stableExitGen()
	if b == nil || ov.Pins[xray.StableBalancerTag] != "" || r.State != stableStatePinned || r.Tag != pin || !alive {
		return
	}
	unfit := d.exits.Unfit()
	var next stableRank
	var ranked []stableRank
	for _, c := range r.Ranked {
		if c.Tag == pin {
			continue // dead: never moved back to unchecked
		}
		ranked = append(ranked, c)
		if next.Tag == "" && !contains(unfit, c.Tag) && contains(b.Members, c.Tag) && !dead(c.Tag) {
			next = c
		}
	}
	octx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := stableOverride(octx, view.APIListen, xray.StableBalancerTag, next.Tag)
	cancel()
	if err != nil {
		if key := pin + ">" + next.Tag; d.stable.refused != key {
			d.stable.refused = key
			logging.L().Warn("stable exit: the pin died and xray did not take the move; asked again", "exit", pin, "err", err.Error())
		}
		return
	}
	d.stable.refused = ""
	d.stable.mu.Lock()
	d.stable.applied = next.Tag
	d.stable.mu.Unlock()
	rec := r
	rec.Ranked = ranked
	if next.Tag == "" {
		rec.State, rec.Tag, rec.IP, rec.Country, rec.Unsteady, rec.CheckedAt = stableStateNone, "", "", "", false, time.Now()
		d.saveStableExitIf(rec, gen, false)
		d.stable.mu.Lock()
		d.stable.roundDue, d.stable.misses = true, 0
		d.stable.mu.Unlock()
		logging.L().Warn("stable exit: the pin died and no other node keeps one address; a round now", "exit", pin, "by", why)
		return
	}
	rec.Tag, rec.IP, rec.Country, rec.Unsteady = next.Tag, next.IP, d.exitCountry(next.Tag), false
	rec.CheckedAt = time.Time{} // its address rechecked at the next tick
	d.saveStableExitIf(rec, gen, false)
	d.stable.mu.Lock()
	d.stable.misses = 0
	d.stable.mu.Unlock()
	logging.L().Warn("stable exit: the pin died; moved without a restart", "from", pin, "to", next.Tag, "by", why)
}

// stableFailover is the failover watchdog's word on the pin, each look: a
// pin xray's own connections to which go unanswered is moved off — when
// another node answered since (stableMoveOff: not a WAN blip) — and xray is
// brought in line with the record (stableReconcile).
func (d *daemon) stableFailover(ctx context.Context, w *failoverWatch, infos map[string]api.BalancerInfo, ov localctl.Overrides, now time.Time) {
	info, ok := infos[xray.StableBalancerTag]
	if !ok || info.Err != nil {
		return
	}
	r := d.stableExitNow()
	if r.State == stableStatePinned && info.Override == r.Tag && w.det.Failing(r.Tag, now) {
		since := w.det.FirstUnanswered(r.Tag)
		alive := false
		for _, o := range w.view.Outbounds {
			if o.Tag != r.Tag && o.Dials && w.det.AnsweredSince(o.Tag, since) {
				alive = true
				break
			}
		}
		d.stableMoveOff(ctx, w.view, ov, r.Tag, func(t string) bool { return w.det.Failing(t, now) }, alive, "connections")
	}
	d.stableReconcile(ctx, w.view, ov, info.Override, true)
}

// stableHealth is the observatory's word on the pin, each tick: a pin the
// observatory holds dead — one that takes connections and carries nothing,
// which the connection table cannot tell — is moved off when another node
// of the stable balancer is alive.
func (d *daemon) stableHealth(ctx context.Context) {
	r := d.stableExitNow()
	if r.State != stableStatePinned || r.Tag == "" {
		return
	}
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return
	}
	view, err := xrayview.Parse(raw)
	if err != nil || view.MetricsListen == "" || !xrayview.Loopback(view.APIListen) {
		return
	}
	b := view.Balancer(xray.StableBalancerTag)
	if b == nil {
		return
	}
	mctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	m, err := stableMetrics(mctx, view.MetricsListen)
	cancel()
	if err != nil || m == nil {
		return
	}
	o, seen := m.Observatory[r.Tag]
	if !seen || o.Alive {
		return
	}
	dead := func(t string) bool { h, ok := m.Observatory[t]; return ok && !h.Alive }
	alive := false
	for _, t := range b.Members {
		if h, ok := m.Observatory[t]; t != r.Tag && ok && h.Alive {
			alive = true
			break
		}
	}
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	d.stableMoveOff(ctx, view, ov, r.Tag, dead, alive, "observatory")
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
	if r.State == stableStatePinned && !r.Unsteady && !contains(d.exits.Unfit(), r.Tag) {
		return r.State, r.Country, checkedAt
	}
	if r.State == stableStatePinned {
		return stableStateNone, "", checkedAt
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
	// The observatory's word on the pin, then xray in line with the record
	// (also on a router whose failover watchdog is off).
	d.stableHealth(ctx)
	d.stableMoved(ctx)
	set := c.StableExitSettings()
	r, gen := d.stableExitGen()
	recheck := time.Duration(set.RecheckMinutes) * time.Minute
	full := r.FullAt.IsZero() || r.State == "" || (r.State != stableStatePinned && now.Sub(r.CheckedAt) >= recheck) ||
		(r.State == stableStatePinned && contains(d.exits.Unfit(), r.Tag)) // filtered: a new pin now
	d.stable.mu.Lock()
	if d.stable.nextFull.IsZero() && !r.FullAt.IsZero() {
		d.stable.nextFull = r.FullAt.Add(time.Duration(float64(time.Duration(set.EveryHours)*time.Hour) * stableJitter()))
	}
	if (!d.stable.nextFull.IsZero() && !now.Before(d.stable.nextFull)) || d.stable.roundDue {
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
		case len(ips) == 1 && ips[0] == r.IP && !r.Unsteady:
			d.stable.mu.Lock()
			d.stable.misses = 0
			d.stable.mu.Unlock()
			r.CheckedAt = now
			d.saveStableExitIf(r, gen, true) // a move made meanwhile stands
			return
		case len(ips) == 0 && failed > 0:
			d.stable.mu.Lock()
			d.stable.misses++
			misses := d.stable.misses
			d.stable.mu.Unlock()
			if misses < stableMissesToMove {
				return // one silent recheck is no verdict (a dead pin is the watchdog's and the observatory's to move)
			}
		}
		logging.L().Info("stable exit: the pin changed its address or stopped answering; looking again", "exit", r.Tag, "addresses", len(ips))
	}
	d.stableExitRound(ctx, now, cands, set, r, gen)
}

// stableExitRound asks every candidate and keeps or moves the pin.
func (d *daemon) stableExitRound(ctx context.Context, now time.Time, cands []string, set catalog.StableExit, prev stableExitRecord, gen uint64) {
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
	// The ranking a dead pin moves along (stableFailover): the pin first,
	// then the fastest. Sorted as a copy: pick points into stable.
	byMedian := append([]steady(nil), stable...)
	sort.SliceStable(byMedian, func(i, j int) bool { return byMedian[i].median < byMedian[j].median })
	var ranked []stableRank
	if pick != nil {
		ranked = append(ranked, stableRank{Tag: pick.tag, IP: pick.ip, Ms: pick.median.Milliseconds()})
	}
	for _, st := range byMedian {
		if pick == nil || st.tag != pick.tag {
			ranked = append(ranked, stableRank{Tag: st.tag, IP: st.ip, Ms: st.median.Milliseconds()})
		}
	}
	switch {
	case pick != nil:
		next.State, next.Tag, next.IP = stableStatePinned, pick.tag, pick.ip
		next.Country = d.exitCountry(pick.tag)
		next.Ranked = ranked
	case prev.State == stableStatePinned && pinAnswered && contains(cands, prev.Tag):
		// Nothing steadier, and the pin still answers: kept (one node still
		// beats the balancer's rotation) — a move only when a steady node
		// appears.
		next = prev
		next.CheckedAt, next.FullAt, next.Unsteady, next.Ranked = now, now, true, nil
		logging.L().Info("stable exit: no node keeps one address now; the pin is kept", "exit", prev.Tag)
	}
	if !d.saveStableExitIf(next, gen, true) {
		// The pin was moved off while the round asked (it judged the record
		// it started from): its answers are stale — another round now.
		d.stable.mu.Lock()
		d.stable.roundDue = true
		d.stable.mu.Unlock()
		logging.L().Info("stable exit: the pin moved during the round; looking again", "candidates", len(cands))
		return
	}
	d.stable.mu.Lock()
	d.stable.misses, d.stable.roundDue = 0, false
	d.stable.nextFull = now.Add(time.Duration(float64(time.Duration(set.EveryHours)*time.Hour) * stableJitter()))
	d.stable.mu.Unlock()
	logging.L().Info("stable exit: round", "candidates", len(cands), "steady", len(stable), "pin", next.Tag, "country", next.Country)
	d.stableMoved(ctx)
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
