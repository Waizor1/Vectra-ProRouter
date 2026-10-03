package main

import (
 "vectra-controller-pro/internal/vault"
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"

	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/xrayview"
)

// The router's own check of its foreign exits (internal/exitcheck,
// coreengine/xray/exit_check.go): every round each exit is asked, through the
// exit probe in xray, for the observatory's neutral URL and for blocked
// sites; an exit that answers the one and none of the others while another
// exit carries them leaves every balancer until it passes two rounds again.

const (
	// exitCheckFirst waits for the observatory's first look after xray starts.
	exitCheckFirst = 2 * time.Minute
	// exitCheckEvery: a filtered exit hands out broken connections for at
	// most this long; each round is ~2 requests per exit.
	exitCheckEvery   = 10 * time.Minute
	exitCheckTimeout = 8 * time.Second
)

var (
	// exitCheckControl is the neutral URL when the entry's observatory names
	// no https one.
	exitCheckControl = "https://cp.cloudflare.com/generate_204"
	// exitCheckSites are blocked by Russia's filter by name, belong to three
	// owners — one site's outage never looks like a filter — and answer in
	// a few kilobytes (only the headers are read). Only the first is asked
	// of an exit that carries it.
	exitCheckSites = []string{"https://www.facebook.com/favicon.ico", "https://discord.com/favicon.ico", "https://x.com/favicon.ico"}
	// exitProbeRound asks the exits; tests replace it.
	exitProbeRound = func(ctx context.Context, p exitcheck.Prober, exits []string) []exitcheck.Result {
		return p.Round(ctx, exits)
	}
	// exitAskWait is how long a render is waited for at the loop's door (it
	// may be in a terminal job); tests shorten it.
	exitAskWait = time.Minute
	// exitWhere names the country a request comes from ("loc=PL"): where an
	// exit really leaves (1111, 2026-09-30: «Турция» and «ОАЭ» left in Poland).
	exitWhere = "https://www.cloudflare.com/cdn-cgi/trace"
	// exitLocate asks it through each exit; tests replace it.
	exitLocate = func(ctx context.Context, p exitcheck.Prober, exits []string) map[string]string {
		return p.Locate(ctx, exits)
	}
)

// exitWhereEvery: an exit's country rarely moves; a look costs a request.
const exitWhereEvery = 24 * time.Hour

func (d *daemon) exitCheckOn() bool { return !d.cfg.NoExitCheck && !d.passwallMode() }

// withExits installs the exit probe and leaves the exits found unfit out of
// a render of providerRaw.
func (d *daemon) withExits(opts xray.SpliceOptions, providerRaw []byte) xray.SpliceOptions {
	if !d.exitCheckOn() {
		return opts
	}
	opts.ExitProbeListen = xray.DefaultExitProbeListen
	unfit := d.exits.Unfit()
	if len(unfit) == 0 {
		return opts
	}
	checked := map[string]bool{}
	for _, t := range xray.ExitsToCheck(providerRaw) {
		checked[t] = true
	}
	var cand []string
	for _, t := range unfit {
		if checked[t] {
			cand = append(cand, t)
		}
	}
	// Only the exits a render would move: one no balancer can lose would
	// change the key and restart xray on the same config.
	opts.LeaveOut = xray.Leavable(providerRaw, opts.Services, cand)
	return opts
}

// exitWatch is the check's own bookkeeping between rounds: the set of unfit
// exits a render was made for that still does not leave them all out (done)
// — a balancer would be empty — so it is not asked for again.
type exitWatch struct {
	asked []string
	done  bool
	said  string
}

func (d *daemon) watchExits(ctx context.Context) {
	if !d.exitCheckOn() {
		return
	}
	first, every := exitCheckFirst, exitCheckEvery
	if d.cfg.ExitCheckFirstSec > 0 {
		first = time.Duration(d.cfg.ExitCheckFirstSec) * time.Second
	}
	if d.cfg.ExitCheckEverySec > 0 {
		every = time.Duration(d.cfg.ExitCheckEverySec) * time.Second
	}
	w := &exitWatch{}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			d.exitRound(ctx, w, now)
			t.Reset(every)
		}
	}
}

// renderExits reads the running render: the exits its probe reaches, and
// those of them no balancer selects — the ones it leaves out.
func renderExits(raw []byte) (probed, leftOut []string, control string) {
	var doc struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Settings struct {
				Accounts []struct {
					User string `json:"user"`
				} `json:"accounts"`
			} `json:"settings"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil, nil, ""
	}
	users := map[string]bool{}
	for _, ib := range doc.Inbounds {
		if ib.Tag == xray.ExitProbeTag {
			for _, a := range ib.Settings.Accounts {
				users[a.User] = true
			}
		}
	}
	v, err := xrayview.Parse(raw)
	if err != nil || len(users) == 0 {
		return nil, nil, ""
	}
	for _, o := range v.Outbounds {
		if !users[xray.ExitProbeUser(o.Tag)] {
			continue
		}
		probed = append(probed, o.Tag)
		if len(v.BalancersOf(o.Tag)) == 0 {
			leftOut = append(leftOut, o.Tag)
		}
	}
	sort.Strings(probed)
	sort.Strings(leftOut)
	return probed, leftOut, v.ProbeDestination
}

// exitRound is one round: ask, judge, and have the loop render again when
// what the render leaves out is not what the check found.
func (d *daemon) exitRound(ctx context.Context, w *exitWatch, now time.Time) {
	if !d.exitCheckOn() {
		return
	}
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return
	}
	exits, leftOut, dest := renderExits(raw)
	if len(exits) < 2 {
		// No probe in the running render yet, or nothing to compare against.
		return
	}
	// Countries of exits the render no longer has are nobody's to show.
	d.exits.KeepEgress(append(append([]string(nil), exits...), leftOut...))
	control := d.cfg.ExitCheckControl
	if control == "" {
		control = exitCheckControl
		if strings.HasPrefix(dest, "https://") {
			control = dest
		}
	}
	sites := d.cfg.ExitCheckSites
	if len(sites) == 0 {
		sites = exitCheckSites
	}
	p := exitcheck.Prober{Listen: xray.DefaultExitProbeListen, Control: control, Blocked: sites,
		Timeout: exitCheckTimeout, Attempts: 2, Parallel: 3, Where: exitWhere}
	// Enough for every exit's worst case (all timeouts, 3 at a time); an
	// exit the deadline cuts short is unjudged anyway (exitcheck.Prober).
	rctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	results := exitProbeRound(rctx, p, exits)
	cancel()
	before := d.exits.Unfit()
	d.exits.Round(results, now)
	after := d.exits.Unfit()
	// Where each exit really leaves, once a day each: the card and the
	// services' countries say when it is not where its name says. Only
	// exits this round found up: a dead one would cost a timeout for nothing.
	var up []string
	for _, r := range results {
		if r.Control {
			up = append(up, r.Tag)
		}
	}
	if due := d.exits.EgressDue(up, now, exitWhereEvery); len(due) > 0 {
		lctx, lcancel := context.WithTimeout(ctx, 2*time.Minute)
		d.exits.SetEgress(due, exitLocate(lctx, p, due), now)
		lcancel()
	}
	fit, filtered, dead := 0, 0, 0
	for _, r := range results {
		switch {
		case !r.Control:
			dead++
		case len(r.Blocked) > 0 && r.Blocked[len(r.Blocked)-1]:
			fit++
		default:
			filtered++
		}
	}
	logging.L().Info("exit check: round", "exits", len(exits), "fit", fit, "filtered", filtered, "dead", dead, "unfit", strings.Join(after, ","))
	if said := strings.Join(after, ","); said != w.said || !reflect.DeepEqual(before, after) {
		w.said = said
		for _, t := range after {
			if !contains(before, t) {
				logging.L().Warn("exit check: this exit answers the neutral URL and no blocked site; leaving it out of the balancers", "exit", t)
			}
		}
		for _, t := range before {
			if !contains(after, t) {
				logging.L().Info("exit check: this exit carries blocked sites again; back in the balancers", "exit", t)
			}
		}
	}
	want := intersect(after, exits)
	if reflect.DeepEqual(want, leftOut) {
		// The running render is right. Forget the last ask: a render made
		// later without it (vctl apply-local, run by hand, knows nothing of
		// the check) is corrected at the next round.
		w.asked, w.done = nil, false
		return
	}
	if w.done && reflect.DeepEqual(want, w.asked) {
		// Rendered for exactly this set, and still not left out: a balancer
		// would be empty. Asking again would change nothing.
		return
	}
	// Remembered only once the loop rendered it: a render refused, or a loop
	// busy with a job, is asked for again at the next round.
	w.asked, w.done = want, d.askReapply(ctx, "exit check")
}

// opRerender is the loop's own operation for the check: never on the UI
// socket (serveUI takes only the UI's operations).
const opRerender = "rerender"

// rerenderRunning renders the running document again, as it is, under the
// current options — the check's leave-outs — as reconcileRender does. Never
// the router UI's way: that resolves a location from the entries cache under
// the owner's choice, and a choice gone stale refuses the render, while a
// newer cached entry would replace the running one.
func (d *daemon) rerenderRunning(ctx context.Context) localctl.SocketResponse {
	if d.desired == nil || d.applier == nil || d.applier.Tproxy == nil {
		return localctl.SocketResponse{Code: "apply_failed", Detail: "this router has no operator config yet"}
	}
	raw, err := vault.ReadFile(d.documentPath())
	if err != nil || len(raw) == 0 {
		return localctl.SocketResponse{Code: "apply_failed", Detail: "no running document to render again"}
	}
	res, err := d.applyProvider(ctx, raw, false)
	if err != nil {
		return localctl.SocketResponse{Code: "apply_failed", Detail: err.Error()}
	}
	d.reloadAfterApply(ctx, res, raw)
	if err := d.persist(); err != nil {
		logging.L().Warn("persist state after the exit check's render", "err", err.Error())
	}
	return localctl.SocketResponse{OK: true}
}

// askReapply has the loop render the running document again under the
// current options; it waits for the loop's answer so two renders never race,
// and says whether the render was made.
func (d *daemon) askReapply(ctx context.Context, why string) bool {
	reply := make(chan localctl.SocketResponse, 1)
	select {
	case d.uiReqs <- uiRequest{op: opRerender, reply: reply}:
	case <-ctx.Done():
		return false
	case <-time.After(exitAskWait):
		logging.L().Warn(why + ": the loop is busy; asking again at the next round")
		return false
	}
	select {
	case resp := <-reply:
		if !resp.OK {
			logging.L().Warn(why+": the render was refused; asking again at the next round", "code", resp.Code, "detail", resp.Detail)
		}
		return resp.OK
	case <-ctx.Done():
	case <-time.After(3 * time.Minute):
	}
	return false
}

// unfitStamps and unfitSince carry the unfit exits through state.json.
func unfitStamps(m map[string]time.Time) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for t, since := range m {
		out[t] = since.UTC().Format(time.RFC3339)
	}
	return out
}

func unfitSince(m map[string]string) map[string]time.Time {
	out := make(map[string]time.Time, len(m))
	for t, s := range m {
		since, err := time.Parse(time.RFC3339, s)
		if err != nil {
			since = time.Now()
		}
		out[t] = since
	}
	return out
}

// egressStamps and egressFrom carry where each exit leaves through
// state.json.
func egressStamps(m map[string]exitcheck.Located) map[string]state.ExitEgress {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]state.ExitEgress, len(m))
	for t, l := range m {
		out[t] = state.ExitEgress{CC: l.CC, At: l.At.UTC().Format(time.RFC3339)}
	}
	return out
}

func egressFrom(m map[string]state.ExitEgress) map[string]exitcheck.Located {
	out := make(map[string]exitcheck.Located, len(m))
	for t, e := range m {
		at, err := time.Parse(time.RFC3339, e.At)
		if err != nil || e.CC == "" {
			continue // unreadable: asked again, as if never located
		}
		out[t] = exitcheck.Located{CC: e.CC, At: at}
	}
	return out
}

func intersect(a, b []string) []string {
	in := map[string]bool{}
	for _, x := range b {
		in[x] = true
	}
	var out []string
	for _, x := range a {
		if in[x] {
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
