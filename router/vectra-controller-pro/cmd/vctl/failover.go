package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
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
		raw, err := os.ReadFile(d.cfg.XrayRenderPath)
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
	if w.eps == nil || now.Sub(w.epsAt) >= failoverEndpointsEvery {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		eps, unresolved := failover.MapEndpoints(lctx, w.view.Outbounds, failoverLookup)
		cancel()
		w.eps, w.epsAt = eps, now
		if said := fmt.Sprintf("%d/%d", len(eps), unresolved); said != w.epsSaid {
			w.epsSaid = said
			if unresolved > 0 {
				logging.L().Warn("failover watchdog: some nodes' names did not resolve; those nodes are not judged", "endpoints", len(eps), "unresolved", unresolved)
			} else {
				logging.L().Info("failover watchdog: watching the nodes' endpoints", "endpoints", len(eps))
			}
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
				health[tag] = failover.Health{Alive: o.Alive, DelayMs: o.DelayMs}
			}
		}
		cancel()
	}
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	unfit := d.exits.Unfit()
	main := ""
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
	}
	for _, a := range w.pol.Decide(now, bals, health, w.det) {
		d.applyFailover(ctx, w, a, now, main)
	}
	if main == "" {
		return
	}
	d.publishRoute(w, main, infos[main], unfit, now)
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
