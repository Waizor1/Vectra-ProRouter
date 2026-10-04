package main

import (
 "vectra-controller-pro/internal/vault"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"time"

	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/conntrack"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/failover"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/xrayview"
)

// The failover watchdog (docs/superpowers/specs/2026-09-30-vctl-service-
// routing-design.md, decision 5). The provider's observatory probes each node
// every few minutes; a node that dies in between keeps getting new
// connections until then. The watchdog reads what xray already does — its
// attempts to the nodes in conntrack — and, when the node a balancer prefers
// stops answering, re-points that balancer through xray's API at the
// fastest node the observatory holds alive, and confirms with one request.
// No probe traffic. Its own goroutine: the daemon's loop can be busy for
// minutes (a terminal job runs in it).
var (
	failoverEvery          = 2 * time.Second
	failoverEndpointsEvery = 5 * time.Minute
	failoverDownAfter      = 60 * time.Second
	// While some node's name does not resolve, a few of those names — never
	// the ones that resolve — are asked again this soon, then half as often
	// each time none comes back, up to the endpoints' pace (reprobe).
	failoverRemapEvery = 15 * time.Second
	failoverFastBatch  = 4
	// Each lookup's own budget: a name that hangs does not use up the rest's.
	failoverLookupEach = 2 * time.Second
	// reprobeGap is the least time between two restarts of xray for names
	// that came back.
	reprobeGap = 10 * time.Minute

	failoverConntrack = conntrack.Read
	failoverBalancers = api.GetBalancerInfo
	failoverOverride  = api.OverrideBalancerTarget
	failoverMetrics   = api.FetchMetrics
	failoverConfirm   = confirmThroughTheTunnel
	// Both families: a node with an AAAA record may be dialled over IPv6.
	failoverLookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		return controlplane.MarkedResolver(firewall.DefaultControlMark).LookupNetIP(ctx, "ip", host)
	}
	failoverLocalAddrs = localAddrs
)

// localAddrs are the router's own addresses: what xray's dials leave from.
func localAddrs() (map[netip.Addr]bool, error) {
	ifas, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	out := map[netip.Addr]bool{}
	for _, a := range ifas {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out[p.Addr().Unmap()] = true
		}
	}
	return out, nil
}

// failoverProbeURL is what xray's own observatory asks when the render names
// no destination.
const failoverProbeURL = "https://www.google.com/generate_204"

// confirmThroughTheTunnel sends one request the way the LAN's go — the
// router's own unmarked socket, which the output chain hands to xray, whose
// catch-all rule is the main balancer — to what the provider's observatory
// asks of its nodes. Any answer is the path working, as the observatory
// counts it; a redirect is an answer too.
func confirmThroughTheTunnel(ctx context.Context, url string) bool {
	c := &http.Client{
		Timeout:       4 * time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

type failoverWatch struct {
	det          *failover.Detector
	pol          *failover.Policy
	view         *xrayview.View
	viewStamp    string
	eps          failover.Endpoints
	epsAt        time.Time
	epsSaid      string
	downReported map[string]bool
	// trouble is what keeps the watchdog from seeing, said once when it
	// changes; refused is the moves xray refused, said once each.
	trouble string
	refused map[string]bool
	// The nodes' names (reprobe): which did not resolve when last asked; when
	// each was last seen resolving; which the observatory held alive when
	// their name went (lost: gone at this look, to be judged); which went in
	// an outage — more than half of the render's named nodes gone at once;
	// which came back since cameBackAt, and whether the observatory's verdict
	// on each predates its name (stale). named: how many the render has.
	unresolved   map[string]bool
	seenResolved map[string]time.Time
	aliveLost    map[string]bool
	lostInOutage map[string]bool
	lost         []string
	cameBack     map[string]bool
	cameBackAt   time.Time
	named        int
	// The fast look at the names that failed: when, how soon again, where in
	// them it goes on.
	fastAt    time.Time
	fastDelay time.Duration
	fastNext  int
	// xrayStarted is when the running xray started (startSeen: as last
	// seen); restart restarts it.
	lastReprobe time.Time
	restart     func(ctx context.Context) error
	xrayStarted func() time.Time
	startSeen   time.Time
}

func newFailoverWatch() *failoverWatch {
	return &failoverWatch{det: failover.NewDetector(), pol: failover.NewPolicy(), downReported: map[string]bool{}, refused: map[string]bool{}}
}

// blind says, once for each cause, why the watchdog cannot see; a look that
// gets through says it sees again. Nothing else would: the watchdog runs
// every 2 s for the life of the daemon.
func (w *failoverWatch) blind(cause string, err error) {
	if w.trouble == cause {
		return
	}
	w.trouble = cause
	logging.L().Warn("failover watchdog: "+cause+"; a node that stops answering is not moved off until this clears", "err", err.Error())
}

func (w *failoverWatch) sees() {
	if w.trouble != "" {
		logging.L().Info("failover watchdog sees again", "was", w.trouble)
		w.trouble = ""
	}
}

// watchFailover runs the watchdog until ctx ends.
func (d *daemon) watchFailover(ctx context.Context) {
	if d.cfg.NoFailoverWatchdog {
		return
	}
	w := newFailoverWatch()
	if d.sup != nil {
		w.restart = func(context.Context) error { return d.sup.Reload(d.supCtx) }
		w.xrayStarted = func() time.Time { return d.sup.Status().StartedAt }
	}
	t := time.NewTicker(failoverEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			d.failoverTick(ctx, w, now)
		}
	}
}

// failoverTick is one look. It reads only what is safe to share: the config,
// files, the kernel's table and xray's API.
func (d *daemon) failoverTick(ctx context.Context, w *failoverWatch, now time.Time) {
	if d.cfg.NoFailoverWatchdog || d.passwallMode() {
		return
	}
	fi, err := os.Stat(d.cfg.XrayRenderPath)
	if err != nil {
		w.blind("cannot read the render", err)
		return
	}
	if stamp := fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), fi.Size()); stamp != w.viewStamp {
		raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
		if err != nil {
			w.blind("cannot read the render", err)
			return
		}
		v, err := xrayview.Parse(raw)
		if err != nil {
			w.blind("cannot parse the render", err)
			return
		}
		keep := map[string]bool{}
		for _, b := range v.Balancers {
			keep[b.Tag] = true
		}
		w.pol.Forget(keep)
		w.view, w.viewStamp, w.eps = v, stamp, nil
	}
	if w.view == nil || w.view.APIListen == "" || len(w.view.Balancers) == 0 {
		return
	}
	if w.xrayStarted != nil {
		if st := w.xrayStarted(); !st.Equal(w.startSeen) {
			// Another xray: its first round is under way. Its nodes' names
			// are asked now, so what it probes with is what is seen here.
			w.startSeen, w.eps = st, nil
		}
	}
	if w.eps == nil || now.Sub(w.epsAt) >= failoverEndpointsEvery {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		eps, unresolved := failover.MapEndpoints(lctx, w.view.Outbounds, lookupEach)
		cancel()
		w.eps, w.epsAt = eps, now
		w.namesResolved(namedTags(w.view.Outbounds), unresolved, now, true)
		w.det.Keep(viewOutboundTags(w.view.Outbounds))
		if said := fmt.Sprintf("%d/%d", len(eps), len(unresolved)); said != w.epsSaid {
			w.epsSaid = said
			if len(unresolved) > 0 {
				logging.L().Warn("failover watchdog: some nodes' names did not resolve; those nodes are not judged", "endpoints", len(eps), "unresolved", len(unresolved))
			} else {
				logging.L().Info("failover watchdog: watching the nodes' endpoints", "endpoints", len(eps))
			}
		}
	} else if len(w.unresolved) > 0 && now.Sub(w.fastAt) >= w.fastDelay {
		batch := w.fastBatch()
		var outs []xrayview.Outbound
		for _, t := range batch {
			if o := w.view.Outbound(t); o != nil {
				outs = append(outs, *o)
			}
		}
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		eps, unresolved := failover.MapEndpoints(lctx, outs, lookupEach)
		cancel()
		for ep, tags := range eps {
			for _, t := range tags {
				if !contains(w.eps[ep], t) {
					w.eps[ep] = append(w.eps[ep], t)
				}
			}
		}
		w.fastAt = now
		if w.namesResolved(batch, unresolved, now, false) == 0 {
			w.fastDelay *= 2
			if w.fastDelay > failoverEndpointsEvery {
				w.fastDelay = failoverEndpointsEvery
			}
		} else {
			w.fastDelay = failoverRemapEvery
		}
	}
	local, err := failoverLocalAddrs()
	if err != nil {
		w.blind("cannot read the router's own addresses", err)
		return
	}
	w.det.Local = func(a netip.Addr) bool { return local[a] }
	entries, err := failoverConntrack()
	if err != nil {
		w.blind("cannot read the connection table", err)
		return
	}
	w.det.Observe(now, entries, w.eps)

	tags := make([]string, len(w.view.Balancers))
	for i, b := range w.view.Balancers {
		tags[i] = b.Tag
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	infos, err := failoverBalancers(cctx, w.view.APIListen, tags)
	cancel()
	if err != nil {
		w.blind("xray's API does not answer", err)
		return
	}
	w.sees()
	health := map[string]failover.Health{}
	if w.view.MetricsListen != "" {
		mctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if m, err := failoverMetrics(mctx, w.view.MetricsListen); err == nil {
			for tag, o := range m.Observatory {
				h := failover.Health{Alive: o.Alive, DelayMs: o.DelayMs}
				if o.LastSeen > 0 {
					h.LastSeen = time.Unix(o.LastSeen, 0)
				}
				health[tag] = h
			}
		}
		cancel()
	}
	w.reprobe(ctx, health, now)
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	unfit := d.exits.Unfit()
	main := ""
	var mainGroups [][]string
	var bals []failover.Balancer
	for _, b := range w.view.Balancers {
		if b.Role == "main" {
			main = b.Tag
		}
		info, ok := infos[b.Tag]
		if !ok || info.Err != nil {
			continue
		}
		chain := fallbackChain(w.view, b)
		var borrow []string
		if b.Role == "main" || b.Role == "routed" {
			// Never an exit the exit check found unable to carry blocked
			// sites, however fast it answers the observatory.
			for _, t := range borrowPool(w.view, b, chain) {
				if !contains(unfit, t) {
					borrow = append(borrow, t)
				}
			}
		}
		bals = append(bals, failover.Balancer{Tag: b.Tag, Members: b.Members, Principle: info.Principle,
			Override: info.Override, OwnerPin: ov.Pins[b.Tag], Fallback: nodeFallback(w.view, b), Chain: chain, Borrow: borrow})
		if b.Role == "main" {
			mainGroups = [][]string{b.Members, chain, borrow}
		}
	}
	for _, a := range w.pol.Decide(now, bals, health, w.det) {
		d.applyFailover(ctx, w, a, now, main)
	}
	if main == "" {
		return
	}
	d.publishRoute(w, main, infos[main], unfit, now)
	base := tunnelLook{MainDown: w.pol.DownFor(main, now) > 0}
	if len(w.eps) > 0 {
		// The connection table's word too: xray's own dials to the nodes —
		// the observatory's, at the least — answered or not. It needs no
		// last_seen_time from the observatory (burstObservatory gives none).
		base.Watching = true
		for _, g := range mainGroups {
			for _, t := range g {
				if a := w.det.LastAnswered(t); a.After(base.Answered) {
					base.Answered = a
				}
			}
		}
	}
	d.publishTunnel(health, now, base, mainGroups...)
	if down := w.pol.DownFor(main, now); down >= failoverDownAfter {
		if !w.downReported[main] {
			w.downReported[main] = true
			logging.L().Error("no node of the main balancer answers, and none is alive to move to", "balancer", main, "seconds", int(down.Seconds()))
			d.incident("PROXY_DOWN", main, "no node of "+main+" answers and none is alive to move to",
				map[string]any{"seconds": int(down.Seconds())})
		}
	} else {
		delete(w.downReported, main)
	}
}

// tunnelLook is the word on the main traffic's way out, as the watchdog last
// read it: from xray's observatory, whether a node of the main balancer, its
// reserve or the entry's other countries is alive (Dead: none is, of those it
// watches) and when one last answered its probe (LastAlive; Seen: the
// observatory says when at all); from the watchdog's own policy, whether the
// main balancer has been down (MainDown: failing, nothing to move to).
type tunnelLook struct {
	Dead      bool
	MainDown  bool
	Seen      bool
	LastAlive time.Time
	// Watching: the watchdog maps the nodes' endpoints, so Answered — when a
	// connection of xray's to one of them was last answered — is a word.
	Watching bool
	Answered time.Time
	At       time.Time
}

// publishTunnel records the word on the main traffic's nodes.
// base carries the watchdog's own word (MainDown, Watching, Answered); the
// look is complete before it is stored: the loop reads it.
func (d *daemon) publishTunnel(health map[string]failover.Health, now time.Time, base tunnelLook, groups ...[]string) {
	l := &base
	l.At = now
	observed, alive := false, false
	for _, g := range groups {
		for _, t := range g {
			h, ok := health[t]
			observed = observed || ok
			alive = alive || h.Alive
			if !h.LastSeen.IsZero() {
				l.Seen = true
				if h.Alive && h.LastSeen.After(l.LastAlive) {
					l.LastAlive = h.LastSeen
				}
			}
		}
	}
	// A node the observatory does not watch says nothing: with none of them
	// watched it is no word on the nodes, only the policy's.
	l.Dead = observed && !alive
	d.tunnel.Store(l)
}

// freshTunnel is the watchdog's word if it is under a minute old.
func (d *daemon) freshTunnel(now time.Time) *tunnelLook {
	l := d.tunnel.Load()
	if l == nil || now.Sub(l.At) >= time.Minute {
		return nil
	}
	return l
}

// tunnelDead: nothing says the main traffic's way out works again since
// since — the moment the router left it. Where the observatory says when its
// nodes last answered, a node must have answered after since: its "alive" is
// a verdict of its last round, which can predate the outage by its whole
// probe interval (1111, 2026-10-04: direct mode went back to a blocked tunnel
// on that word). Otherwise: every watched node dead, or the main balancer
// down. No word (no watchdog, no metrics, an old look) is not dead.
func (d *daemon) tunnelDead(now, since time.Time) bool {
	l := d.freshTunnel(now)
	if l == nil {
		return false
	}
	if !since.IsZero() {
		// Nothing has answered for long: one blind try, the cooldown's
		// doubling keeping the next ones apart — never direct for good on
		// a word that may not come (no observatory probing at all).
		if now.Sub(since) >= blindRetryAfter {
			return false
		}
		if l.Watching || l.Seen {
			return !l.Answered.After(since) && !l.LastAlive.After(since)
		}
	}
	return l.Dead || l.MainDown
}

// blindRetryAfter is how long direct mode waits for a node's answer before
// it tries the proxy anyway.
const blindRetryAfter = 10 * time.Minute

// tunnelFailing: the watchdog holds the main balancer down, or every watched
// node dead — the rescue need not wait for the next poll to look.
func (d *daemon) tunnelFailing(now time.Time) bool {
	l := d.freshTunnel(now)
	return l != nil && (l.MainDown || l.Dead)
}

// lookupEach looks a node's name up within its own budget.
func lookupEach(ctx context.Context, host string) ([]netip.Addr, error) {
	lctx, cancel := context.WithTimeout(ctx, failoverLookupEach)
	defer cancel()
	return failoverLookup(lctx, host)
}

// namedTags are the nodes a render dials by name.
func namedTags(outs []xrayview.Outbound) []string {
	var tags []string
	for _, o := range outs {
		if o.Dials && o.Address != "" {
			if _, err := netip.ParseAddr(o.Address); err != nil {
				tags = append(tags, o.Tag)
			}
		}
	}
	return tags
}

// viewOutboundTags are every outbound tag of the render: what the watchdog
// may still have to judge (failover.Detector.Keep).
func viewOutboundTags(outs []xrayview.Outbound) []string {
	tags := make([]string, 0, len(outs))
	for _, o := range outs {
		tags = append(tags, o.Tag)
	}
	return tags
}

// fastBatch is the next few names that did not resolve, in turn.
func (w *failoverWatch) fastBatch() []string {
	tags := make([]string, 0, len(w.unresolved))
	for t := range w.unresolved {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	n := failoverFastBatch
	if n > len(tags) {
		n = len(tags)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, tags[(w.fastNext+i)%len(tags)])
	}
	w.fastNext = (w.fastNext + n) % len(tags)
	return out
}

// namesResolved takes a look at the names asked (all the render's when
// full): those that failed and had not are lost; those that failed before and
// resolve now came back — stale when they went in an outage (more than half
// of the names at once: a reboot, the WAN down) and the running xray never
// saw them resolve (it started while they were gone) or the observatory held
// them alive when they went. One name that comes and goes on its own — the
// watchdog's resolvers disagree about it (1111, 30.09: 8.8.8.8 NXDOMAIN,
// 77.88.8.8 an address) — is no outage, and no reason to restart. It says how
// many came back.
func (w *failoverWatch) namesResolved(asked, unresolved []string, now time.Time, full bool) int {
	if w.unresolved == nil {
		w.unresolved, w.seenResolved, w.aliveLost, w.lostInOutage, w.cameBack = map[string]bool{}, map[string]time.Time{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	}
	failed := map[string]bool{}
	for _, t := range unresolved {
		failed[t] = true
	}
	if full {
		w.named = len(asked)
		// Names the render no longer has are nobody's to wait for.
		for t := range w.unresolved {
			if !contains(asked, t) {
				delete(w.unresolved, t)
				delete(w.aliveLost, t)
				delete(w.lostInOutage, t)
			}
		}
		for t := range w.seenResolved {
			if !contains(asked, t) {
				delete(w.seenResolved, t)
			}
		}
	}
	back := 0
	for _, t := range asked {
		if failed[t] {
			if !w.unresolved[t] {
				w.unresolved[t] = true
				w.lost = append(w.lost, t)
				w.fastAt, w.fastDelay = now, failoverRemapEvery
			}
			continue
		}
		if w.unresolved[t] {
			delete(w.unresolved, t)
			w.cameBack[t] = w.lostInOutage[t] && (w.seenResolved[t].Before(w.startSeen) || w.aliveLost[t])
			w.cameBackAt = now
			delete(w.aliveLost, t)
			delete(w.lostInOutage, t)
			back++
		}
		w.seenResolved[t] = now
	}
	if len(w.unresolved)*2 > w.named {
		for t := range w.unresolved {
			w.lostInOutage[t] = true
		}
	}
	return back
}

// reprobe: xray's observatory probes every node once as it starts, then a few
// times in a window of minutes (1111: twice in every ten). Started before the
// router's DNS worked — a reboot — its first round found the nodes whose names
// did not resolve dead, and holds them dead until the next random probe: 5-8
// minutes on 1111 after a reboot, their balancers on a fallback meanwhile; the
// same after an outage long enough to lose the names. When such a name
// resolves again and the observatory still holds its node dead on a verdict
// from before (stale: see namesResolved), xray is restarted once, so its first
// round runs with the names resolving. At most once in reprobeGap. A node dead
// before its name went, a name that never failed, a node held alive: left
// alone — a restart drops every proxied connection.
func (w *failoverWatch) reprobe(ctx context.Context, health map[string]failover.Health, now time.Time) {
	if len(w.lost) > 0 {
		// What the observatory held as the names went.
		for _, t := range w.lost {
			if h, ok := health[t]; ok && h.Alive {
				w.aliveLost[t] = true
			}
		}
		w.lost = nil
	}
	if len(w.cameBack) == 0 || w.restart == nil {
		return
	}
	if len(health) == 0 {
		// No observatory to read yet: ask again at the next look, for a while.
		if now.Sub(w.cameBackAt) > time.Minute {
			w.cameBack = map[string]bool{}
		}
		return
	}
	var dead []string
	for t, stale := range w.cameBack {
		if h, ok := health[t]; ok && !h.Alive && stale {
			dead = append(dead, t)
		}
	}
	sort.Strings(dead)
	w.cameBack = map[string]bool{}
	if len(dead) == 0 {
		return
	}
	if !w.lastReprobe.IsZero() && now.Sub(w.lastReprobe) < reprobeGap {
		logging.L().Info("failover watchdog: nodes' names resolve again and xray still holds them dead from before; restarted for this less than the gap ago, its own probes will find them", "nodes", dead, "gap", reprobeGap.String())
		return
	}
	logging.L().Warn("failover watchdog: the nodes' names resolve again and xray's observatory still holds them dead from before: restarting xray so it probes them now", "nodes", dead)
	if err := w.restart(ctx); err != nil {
		logging.L().Warn("failover watchdog: could not restart xray", "err", err.Error())
		return
	}
	w.lastReprobe = now
}

// publishRoute tells the owner's server card where the main traffic goes and
// what the watchdog moved it off (spec decision 6).
func (d *daemon) publishRoute(w *failoverWatch, main string, info api.BalancerInfo, unfit []string, now time.Time) {
	r := &localctl.Route{Balancer: main, Nodes: info.Principle, Override: info.Override, At: now}
	if m, ok := w.pol.Move(main); ok {
		at := m.At
		r.Override, r.MovedFrom, r.MovedAt, r.Reason = m.To, m.From, &at, m.Reason
	}
	for _, t := range unfit {
		if w.view.Outbound(t) == nil {
			continue
		}
		switch {
		case contains(w.view.BalancersOf(t), main):
			// What the main traffic goes through now: while it is held on one
			// server (the watchdog's move, the owner's pin), that one only.
			if r.Override == "" || r.Override == t {
				r.UnfitKept = append(r.UnfitKept, t)
			}
		case len(w.view.BalancersOf(t)) == 0:
			r.Unfit = append(r.Unfit, t)
		}
	}
	d.route.Store(r)
}

// nodeFallback is b's fallback when it carries traffic through a node — a
// stage into another balancer, or a server of its own — and "" otherwise.
// Direct or a black hole is xray's last resort once its observatory holds
// every node dead; forced on connection evidence it would send the LAN out
// unproxied (or nowhere) with nothing dialling the node to bring it back
// (the stand's PassWall race, 30.09).
func nodeFallback(v *xrayview.View, b xrayview.Balancer) string {
	if b.FallbackBalancer != "" {
		return b.FallbackTag
	}
	if ob := v.Outbound(b.FallbackTag); ob != nil && ob.Dials {
		return b.FallbackTag
	}
	return ""
}

// fallbackChain is the members of the balancers b's fallback leads to, stage
// by stage.
func fallbackChain(v *xrayview.View, b xrayview.Balancer) []string {
	var out []string
	seen := map[string]bool{b.Tag: true}
	for fb := b.FallbackBalancer; fb != "" && !seen[fb]; {
		seen[fb] = true
		nb := v.Balancer(fb)
		if nb == nil {
			break
		}
		out = append(out, nb.Members...)
		fb = nb.FallbackBalancer
	}
	return out
}

// borrowPool is what b may borrow when it and its chain are down: the entry's
// dialling outbounds of a country other than Russia (a Russian exit reaches
// nothing a VPN is for; a tag naming no country — a whitelist level — is not
// the router's to judge).
func borrowPool(v *xrayview.View, b xrayview.Balancer, chain []string) []string {
	own := map[string]bool{}
	for _, t := range append(append([]string{}, b.Members...), chain...) {
		own[t] = true
	}
	var out []string
	for _, o := range v.Outbounds {
		if !o.Dials || own[o.Tag] {
			continue
		}
		if cc := xrayview.CountryHint(o.Tag); cc != "" && cc != "RU" {
			out = append(out, o.Tag)
		}
	}
	return out
}

func (d *daemon) applyFailover(ctx context.Context, w *failoverWatch, a failover.Action, now time.Time, main string) {
	octx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err := failoverOverride(octx, w.view.APIListen, a.Balancer, a.Target)
	cancel()
	key := a.Balancer + "→" + a.Target
	if err != nil {
		// Nothing changed in xray, nothing is taken as done: the next look
		// asks again.
		if !w.refused[key] {
			w.refused[key] = true
			logging.L().Warn("xray refused to re-point a balancer; asked again at the next look", "balancer", a.Balancer, "to", a.Target, "err", err.Error())
		}
		return
	}
	delete(w.refused, key)
	w.pol.Applied(a, now)
	if a.Target == "" {
		if a.Reason == "exhausted" {
			logging.L().Warn("no node of the balancer is left to move to; back to its own choice and fallback", "balancer", a.Balancer, "node", a.From)
			return
		}
		logging.L().Info("balancer back to its own choice", "balancer", a.Balancer, "node", a.From, "reason", a.Reason)
		return
	}
	secs := 0
	if first := w.det.FirstUnanswered(a.From); !first.IsZero() {
		secs = int(now.Sub(first).Seconds())
	}
	if a.Reason == "borrowed" {
		logging.L().Warn("the balancer and its whole reserve are down; lent another country of the entry",
			"balancer", a.Balancer, "from", a.From, "to", a.Target)
	} else {
		logging.L().Warn("node failing; balancer moved", "balancer", a.Balancer, "from", a.From, "to", a.Target, "seconds", secs)
	}
	if a.Balancer == main {
		probe := w.view.ProbeDestination
		if probe == "" {
			probe = failoverProbeURL
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ok := failoverConfirm(cctx, probe)
		cancel()
		w.pol.Confirmed(a.Balancer, ok, time.Now())
		if !ok {
			logging.L().Warn("no answer through the moved balancer yet; the next node is tried", "balancer", a.Balancer, "to", a.Target)
			return
		}
	}
	d.incident("PROXY_FAILOVER", a.Balancer+" from "+a.From,
		fmt.Sprintf("node %s stopped answering; %s moved to %s", a.From, a.Balancer, a.Target),
		map[string]any{"to": a.Target, "seconds": secs})
}
