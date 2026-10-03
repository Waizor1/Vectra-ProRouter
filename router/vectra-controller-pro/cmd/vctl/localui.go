package main

import (
 "vectra-controller-pro/internal/vault"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"time"

	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/subscription"
	"vectra-controller-pro/internal/supervisor"
	"vectra-controller-pro/internal/uiapi"
	"vectra-controller-pro/internal/xrayview"
)

// The router UI's side of the daemon.
//
// The daemon is the single owner of xray, the firewall and the check-in loop,
// and that does not change: `vctl rpcd` (one short process per UI call) asks
// through the UI socket, and the daemon does the work on its own loop
// goroutine, between check-ins, exactly like a panel job. Nothing here runs
// concurrently with a job.
//
// Four local choices exist, all in localctl.Overrides:
//
//   - a location other than the panel's, honoured by REMARK on every apply —
//     panel jobs and nightly refreshes included — until reset on the router;
//   - balancer pins, applied live by rpcd and re-applied here after every
//     xray start (xray forgets them when it restarts);
//   - the observatory probe interval, part of every render;
//   - the owner's own sites ("My sites": always direct / always through the
//     VPN), part of every render too.

// uiRequest is a mutating UI operation queued to the loop goroutine.
type uiRequest struct {
	op     string
	change *localctl.Change
	reply  chan localctl.SocketResponse
}

// uiReplyWait is how long the socket handler waits for the loop to finish an
// operation before answering "pending". rpcd's own budget is below rpcd's
// exec timeout (30 s by default).
const uiReplyWait = 12 * time.Second

// spliceOptions are the router-side options for rendering providerRaw, and
// the probe state they imply.
func (d *daemon) spliceOptions(providerRaw []byte) (xray.SpliceOptions, localctl.Probe) {
	opts, probe, _ := d.spliceOptionsOv(providerRaw)
	return opts, probe
}

// spliceOptionsOv is spliceOptions and the overrides they were made from.
func (d *daemon) spliceOptionsOv(providerRaw []byte) (xray.SpliceOptions, localctl.Probe, localctl.Overrides) {
	ov, err := localctl.LoadOverrides(d.cfg.OverridesPath)
	if err != nil {
		logging.L().Warn("local overrides unreadable; rendering with defaults", "err", err.Error())
	}
	opts, probe := spliceOptionsFor(providerRaw, ov, !d.cfg.NoRussiaDirect)
	opts.ServiceEntries, err = d.connectServiceOptionsFor(ov, providerRaw)
	if err != nil {
		opts.ServiceEntries = map[string]json.RawMessage{"stale": json.RawMessage(`{}`)}
	}
	opts = d.withRuntime(opts, providerRaw)
	return opts, probe, ov
}

// spliceOptionsFor is spliceOptions under the given overrides.
func spliceOptionsFor(providerRaw []byte, ov localctl.Overrides, russiaDirect bool) (xray.SpliceOptions, localctl.Probe) {
	opts := xray.SpliceOptions{APIListen: xray.DefaultAPIListen, MetricsListen: xray.DefaultMetricsListen, NoAccessLog: true,
		Rules: xray.UserRules{Direct: ov.Direct, Proxy: ov.Proxy, Connect: ov.ConnectRules}, Services: ov.Services,
		// Only a document that has the provider's Russian bridge: the others
		// keep their splice key, so an upgrade re-renders nothing there.
		RussiaDirect: russiaDirect && xray.HasRussianBalancer(providerRaw)}
	provider, sampling, has := xray.ProviderProbeInterval(providerRaw)
	probe := localctl.Probe{ProviderIntervalSec: int(provider / time.Second), Sampling: sampling}
	switch {
	case !has:
		probe.Source = "provider"
	case ov.ProbeIntervalSec > 0:
		opts.ProbeInterval = clampProbe(time.Duration(ov.ProbeIntervalSec) * time.Second)
		probe.Source = "local"
	case provider > xray.DefaultRouterProbeInterval:
		opts.ProbeInterval = xray.DefaultRouterProbeInterval
		probe.Source = "default"
	default:
		// The provider already probes at least as often as the router would.
		probe.Source = "provider"
	}
	probe.IntervalSec = probe.ProviderIntervalSec
	if opts.ProbeInterval > 0 {
		probe.IntervalSec = int(opts.ProbeInterval / time.Second)
	}
	return opts, probe
}

func clampProbe(d time.Duration) time.Duration {
	if d < xray.MinProbeInterval {
		return xray.MinProbeInterval
	}
	if d > xray.MaxProbeInterval {
		return xray.MaxProbeInterval
	}
	return d
}

// applyProvider installs providerRaw under the current router options. The
// render is redone when the provider bytes changed, when the options it was
// made with changed (SpliceKey), when force is set, or when there is none.
func (d *daemon) applyProvider(ctx context.Context, providerRaw []byte, force bool) (apply.ApplyResult, error) {
	opts, probe, ov := d.spliceOptionsOv(providerRaw)
	return d.applyRendering(ctx, providerRaw, force, opts, probe, ov)
}

func (d *daemon) applyProviderWith(ctx context.Context, providerRaw []byte, force bool, opts xray.SpliceOptions, probe localctl.Probe) (apply.ApplyResult, error) {
	d.applier.Splice = opts
	fresh := !force && fileExists(d.cfg.XrayRenderPath) && d.st.SpliceKey == d.renderKey(opts)
	res, err := d.applier.Apply(ctx, providerRaw, d.st.ConfigDigest, fresh)
	if err != nil {
		d.lastApplyErr = err.Error()
		d.noteApplyErr(err)
		if errors.Is(err, xray.ErrProviderRefused) {
			d.incident("PROVIDER_REFUSED", reKeyNumber.ReplaceAllString(clipText(err.Error(), 200), "N"),
				"the router refused the provider's document; the running render stays", map[string]any{"error": clipText(err.Error(), 300)})
		}
		return res, err
	}
	if len(res.DroppedKeys) > 0 || len(res.DroppedHosts) > 0 {
		// One line for all of it; the rest of the document is applied.
		logging.L().Warn("left out of the provider's document what the router does not take",
			"keys", strings.Join(res.DroppedKeys, ","), "dnsHosts", strings.Join(res.DroppedHosts, ","))
		d.incident("PROVIDER_PARTS_DROPPED", strings.Join(res.DroppedKeys, ",")+"|"+strings.Join(res.DroppedHosts, ","),
			"the router left out parts of the provider's document it does not take",
			map[string]any{"keys": res.DroppedKeys, "dnsHosts": res.DroppedHosts})
	}
	d.lastApplyErr = ""
	d.keepAIRefusedFor(providerRaw)
	d.st.ConfigDigest = res.AppliedDigest
	d.st.SpliceKey = d.renderKey(opts)
	// Every unfit exit, not only those the render moved: after a restart the
	// card still names the ones a balancer had to keep.
	d.st.UnfitExits = unfitStamps(d.exits.Snapshot(d.exits.Unfit()))
	d.probe = &probe
	return res, nil
}

// applyRendering is applyProviderWith under ov, the overrides opts were made
// from. TrialConnectService cannot run xray -test: a render xray refuses
// while it carries «Нейросети» only as the router's own default (ov names no
// choice for them) is made again without them, and that default is not tried
// again on this document (aiRefused). An owner's choice is never dropped.
func (d *daemon) applyRendering(ctx context.Context, providerRaw []byte, force bool, opts xray.SpliceOptions, probe localctl.Probe, ov localctl.Overrides) (apply.ApplyResult, error) {
	res, err := d.applyProviderWith(ctx, providerRaw, force, opts, probe)
	if err == nil || !errors.Is(err, apply.ErrRefused) {
		return res, err
	}
	without, ok := withoutAIDefault(opts, ov)
	if !ok {
		return res, err
	}
	logging.L().Warn("xray refused the render with the «Нейросети» default; rendering without it", "err", err.Error())
	res, err = d.applyProviderWith(ctx, providerRaw, force, without, probe)
	if err == nil {
		// Only now is the default the reason: without it xray took the render.
		d.keepAIRefusedFor(providerRaw)
		if d.aiRefused == nil {
			d.aiRefused = map[string]bool{}
		}
		d.aiRefused[aiRefusedKey(opts.ServiceEntries["ai"], providerRaw)] = true
	}
	return res, err
}

// keepAIRefusedFor forgets the «Нейросети» defaults refused on any document
// but this one: a refusal holds only on the document it joined
// (aiRefusedKey), and every document the provider ever sent stayed a key.
func (d *daemon) keepAIRefusedFor(document []byte) {
	_, doc, _ := strings.Cut(aiRefusedKey(nil, document), ":")
	for k := range d.aiRefused {
		if _, kd, _ := strings.Cut(k, ":"); kd != doc {
			delete(d.aiRefused, k)
		}
	}
}

// withoutAIDefault is opts without «Нейросети», when they carry them only as
// the router's own default: ov, the overrides opts were made from, names no
// choice for them.
func withoutAIDefault(opts xray.SpliceOptions, ov localctl.Overrides) (xray.SpliceOptions, bool) {
	if opts.ServiceEntries["ai"] == nil {
		return opts, false
	}
	if _, chosen := ov.ServiceEntries["ai"]; chosen || ov.Services["ai"] != "" {
		return opts, false
	}
	entries := map[string]json.RawMessage{}
	for id, raw := range opts.ServiceEntries {
		if id != "ai" {
			entries[id] = raw
		}
	}
	if len(entries) == 0 {
		entries = nil
	}
	opts.ServiceEntries = entries
	return opts, true
}

// reloadAfterApply brings xray onto a freshly written render.
func (d *daemon) reloadAfterApply(ctx context.Context, res apply.ApplyResult, providerRaw []byte) {
	if !res.Changed {
		return
	}
	d.nodeCount = countProviderOutbounds(providerRaw)
	if !d.supStarted {
		d.ensureSupervisor(ctx)
	} else if err := d.sup.Reload(d.supCtx); err != nil {
		logging.L().Warn("xray reload after apply failed", "err", err.Error())
	}
}

// cacheEntries keeps the whole provider array, so the router can switch
// locations without fetching again. Best effort: a fetch that cannot be cached
// is still a good fetch.
func (d *daemon) cacheEntries(subID string, fr *subscription.FetchResult) {
	wrote, err := localctl.SaveEntries(d.cfg.EntriesPath, d.cfg.EntriesIndexPath, &localctl.EntriesCache{
		SubscriptionID: subID,
		FetchedAt:      time.Now().UTC(),
		Remarks:        fr.Remarks,
		Entries:        fr.Entries,
	})
	if err != nil {
		logging.L().Warn("could not cache the provider's locations; switching locations on the router needs another fetch", "err", err.Error())
		return
	}
	if wrote {
		logging.L().Info("cached the provider's locations", "entries", len(fr.Entries))
	}
}

// resolveEntry picks the location to run among remarks: the router's own
// choice when it is still offered, else the panel's.
func (d *daemon) resolveEntry(remarks []string, sub config.Subscription) (idx int, local, stale bool, err error) {
	ov, oerr := localctl.LoadOverrides(d.cfg.OverridesPath)
	if oerr != nil {
		logging.L().Warn("local overrides unreadable; using the panel's location", "err", oerr.Error())
	}
	idx, local, stale, err = localctl.Resolve(remarks, ov, sub.EntryRemark, sub.EntryIndex)
	if stale {
		logging.L().Warn("the location chosen on the router is no longer in the subscription; running the panel's",
			"chosen", ov.EntryRemark)
	}
	return idx, local, stale, err
}

// localReapply makes a change made on the router true: a location switch, a
// reset to the panel's location, a new probe interval, the owner's sites. It
// renders from the cached array — never a fetch — under the overrides AS THEY
// WILL BE, and persists the change only once that render is installed. A
// change that fails leaves both the router and the overrides file as they
// were.
func (d *daemon) localReapply(ctx context.Context, change *localctl.Change) localctl.SocketResponse {
	resp := d.localReapplyOnce(ctx, change)
	if !resp.OK {
		d.lastApplyErr = strings.TrimSpace(resp.Code + ": " + resp.Detail)
		return resp
	}
	if change != nil {
		if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
			change.ApplyTo(o)
			return nil
		}); err != nil {
			// Running, but not remembered: the next apply would undo it.
			d.lastApplyErr = "persist the change: " + err.Error()
			return localctl.SocketResponse{Code: "internal", Detail: d.lastApplyErr}
		}
	}
	return resp
}

func (d *daemon) localReapplyOnce(ctx context.Context, change *localctl.Change) localctl.SocketResponse {
	if d.desired == nil || d.applier == nil || d.applier.Tproxy == nil {
		return localctl.SocketResponse{Code: "apply_failed", Detail: "this router has no operator config yet"}
	}
	if d.passwallMode() {
		return d.localReapplyPassWall(ctx, change)
	}
	sub, ok := enabledSubscription(d.desired)
	if !ok {
		return localctl.SocketResponse{Code: "apply_failed", Detail: "no enabled subscription in the operator config"}
	}
	ov, err := localctl.LoadOverrides(d.cfg.OverridesPath)
	if err != nil {
		return localctl.SocketResponse{Code: "internal", Detail: err.Error()}
	}
	if change != nil {
		change.ApplyTo(&ov)
	}

	var providerRaw []byte
	cache, cerr := localctl.LoadEntries(d.cfg.EntriesPath)
	switch {
	case cerr == nil:
		idx, _, stale, err := localctl.Resolve(cache.Remarks, ov, sub.EntryRemark, sub.EntryIndex)
		if err != nil {
			return localctl.SocketResponse{Code: "unknown_entry", Detail: err.Error()}
		}
		if stale {
			// The chosen location is not in the subscription: say so, rather
			// than install the panel's and report success.
			return localctl.SocketResponse{Code: "unknown_entry", Detail: "the chosen location is not in the cached subscription"}
		}
		if ov.EntryDigest != "" {
			found := false
			for _, e := range localctl.Summarize(cache) {
				if e.Digest == ov.EntryDigest {
					idx = e.Index
					found = true
					break
				}
			}
			if !found {
				return localctl.SocketResponse{Code: "unknown_entry"}
			}
		}
		providerRaw = cache.Entries[idx]
	case (change != nil && change.TouchesEntry()) || ov.HasEntry():
		// Without the array neither a location nor "back to the panel's" can
		// be found; the document on disk may be the very choice being undone.
		return localctl.SocketResponse{Code: "no_entries_cache", Detail: cerr.Error()}
	default:
		// No cache and no location involved (a probe interval): the document
		// on disk is the one running.
		raw, rerr := vault.ReadFile(d.cfg.ProviderConfigPath)
		if rerr != nil {
			return localctl.SocketResponse{Code: "no_entries_cache", Detail: rerr.Error()}
		}
		providerRaw = raw
	}

	opts, probe := spliceOptionsFor(providerRaw, ov, !d.cfg.NoRussiaDirect)
	serviceEntries, serviceErr := d.connectServiceOptionsFor(ov, providerRaw)
	if serviceErr != nil {
		return localctl.SocketResponse{Code: "unknown_entry"}
	}
	opts.ServiceEntries = serviceEntries
	opts = d.withRuntime(opts, providerRaw)
	res, err := d.applyRendering(ctx, providerRaw, false, opts, probe, ov)
	if err != nil {
		return localctl.SocketResponse{Code: "apply_failed", Detail: err.Error()}
	}
	d.reloadAfterApply(ctx, res, providerRaw)
	if err := d.persist(); err != nil {
		logging.L().Warn("persist state after a local apply", "err", err.Error())
	}
	logging.L().Info("applied a change made on the router", "changed", res.Changed, "digest", shortDigest(res.AppliedDigest))
	return localctl.SocketResponse{OK: true}
}

// restartXray restarts xray on the installed render. Pins come back through
// the supervisor's start hook.
func (d *daemon) restartXray(ctx context.Context) localctl.SocketResponse {
	if !d.supStarted {
		if !fileExists(d.cfg.XrayRenderPath) {
			return localctl.SocketResponse{Code: "apply_failed", Detail: "there is no installed xray config to start"}
		}
		d.ensureSupervisor(ctx)
		return localctl.SocketResponse{OK: true}
	}
	if err := d.sup.Reload(d.supCtx); err != nil {
		return localctl.SocketResponse{Code: "internal", Detail: err.Error()}
	}
	return localctl.SocketResponse{OK: true}
}

// handleUIRequest runs on the loop goroutine.
func (d *daemon) handleUIRequest(ctx context.Context, op string, change *localctl.Change) localctl.SocketResponse {
	d.busy = op
	d.publishRuntime()
	defer func() { d.busy = "" }()
	switch op {
	case localctl.OpReapply:
		return d.localReapply(ctx, change)
	case localctl.OpRestartXray:
		return d.restartXray(ctx)
	case opRerender:
		return d.rerenderRunning(ctx)
	case localctl.OpRetirePassWall, opRetirePassWallNow:
		return d.retirePassWall(ctx, retireEnv(), time.Now(), op == opRetirePassWallNow)
	}
	return localctl.SocketResponse{Code: "invalid_params", Detail: "unknown operation " + op}
}

// serveUI answers the UI socket until ctx ends. Reads are served from the
// published snapshot; mutations are queued to the loop goroutine.
func (d *daemon) serveUI(ctx context.Context) {
	err := localctl.Serve(ctx, d.cfg.UISocketPath, func(hctx context.Context, req localctl.SocketRequest) localctl.SocketResponse {
		switch req.Op {
		case localctl.OpRuntime:
			return localctl.SocketResponse{OK: true, Runtime: d.liveRuntime()}
		case localctl.OpReapply, localctl.OpRestartXray, localctl.OpRetirePassWall:
			op, wait := req.Op, uiReplyWait
			if req.Op == localctl.OpRetirePassWall {
				// A person at the console waits for opkg and PassWall2's own
				// stop, not a UI poll.
				wait = retireReplyWait
				if req.Now {
					op = opRetirePassWallNow
				}
			}
			reply := make(chan localctl.SocketResponse, 1)
			select {
			case d.uiReqs <- uiRequest{op: op, change: req.Change, reply: reply}:
			default:
				return localctl.SocketResponse{Code: "busy", Detail: "another change made on the router is still being applied"}
			}
			select {
			case r := <-reply:
				return r
			case <-time.After(wait):
				return localctl.SocketResponse{OK: true, Code: "pending"}
			case <-hctx.Done():
				return localctl.SocketResponse{Code: "internal", Detail: "the controller is shutting down"}
			}
		}
		return localctl.SocketResponse{Code: "invalid_params", Detail: "unknown operation " + req.Op}
	})
	if err != nil {
		logging.L().Error("router UI socket stopped; the UI can read nothing from this controller", "err", err.Error())
	}
}

// publishRuntime snapshots the daemon's state for the socket goroutine. It
// runs on the loop goroutine, the only writer of these fields.
func (d *daemon) publishRuntime() {
	rt := &localctl.Runtime{
		ControllerPID:  os.Getpid(),
		StartedAt:      d.startedAt,
		Version:        controllerVersion(),
		RouterID:       d.st.RouterID,
		PanelReachable: d.lastControlPlaneOK,
		LastApplyError: d.lastApplyErr,
		Busy:           d.busy,
		Probe:          d.probe,
	}
	if d.collector != nil {
		rt.XrayVersion = d.collector.XrayVersion()
	}
	if !d.lastCheckIn.IsZero() {
		t := d.lastCheckIn
		rt.LastCheckIn = &t
	}
	if d.desired != nil && d.desired.Inbounds.Tproxy != nil {
		rt.KillSwitch = d.desired.Inbounds.Tproxy.KillSwitch
	}
	rt.Entry = d.runningEntry()
	if d.autoRoute {
		rt.AutoRouteSource = d.cfg.RouteSource
	}
	d.claim.setLinked(d.linked())
	d.runtime.Store(rt)
}

// liveRuntime is the published snapshot plus the supervisor's live state.
func (d *daemon) liveRuntime() *localctl.Runtime {
	var rt localctl.Runtime
	if p := d.runtime.Load(); p != nil {
		rt = *p
	}
	s := d.sup.Status()
	rt.Engine = localctl.Engine{
		State:        string(s.State),
		PID:          s.PID,
		StartedAt:    s.StartedAt,
		Restarts:     s.RestartCount,
		LastExitAt:   s.LastExitAt,
		LastExitCode: s.LastExitCode,
		LastExitErr:  s.LastExitErr,
	}
	if rt.Engine.State == "" {
		rt.Engine.State = "idle"
	}
	// Where each exit was seen leaving: the exit check's, live.
	rt.Egress = d.exits.Egress()
	// A baseline belongs to the xray it was taken for; any other has none yet.
	if b := d.leakBaseline.Load(); b != nil && s.State == supervisor.StateRunning && b.XrayPID == s.PID {
		rt.LeakBaseline = b
	}
	rt.Claim = d.claim.view(time.Now())
	rt.Route = d.route.Load()
	return &rt
}

// runningEntry identifies the running location by its bytes: the entry of
// the cached array whose digest is the applied config digest.
func (d *daemon) runningEntry() *localctl.Entry {
	idx, err := localctl.LoadEntriesIndex(d.cfg.EntriesIndexPath)
	if err != nil || d.st.ConfigDigest == "" {
		return nil
	}
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	for _, e := range idx.Entries {
		if e.Digest != d.st.ConfigDigest {
			continue
		}
		stale := ov.HasEntry() && ov.EntryRemark != e.Remark && !idx.HasRemark(ov.EntryRemark)
		return &localctl.Entry{
			Index:     e.Index,
			Remark:    e.Remark,
			Count:     len(idx.Entries),
			Local:     ov.HasEntry() && ov.EntryRemark == e.Remark,
			Stale:     stale,
			FetchedAt: idx.FetchedAt,
		}
	}
	return nil
}

// onXrayStart is the supervisor's start hook: it runs after every xray start,
// in its own goroutine, never on the loop.
func (d *daemon) onXrayStart(pid int) {
	go d.takeLeakBaseline(pid)
	d.reapplyPins(pid)
}

// startBudget bounds how long the start hook waits for a new xray's API.
func (d *daemon) startBudget() time.Duration {
	if d.pinBudget > 0 {
		return d.pinBudget
	}
	return 45 * time.Second
}

// takeLeakBaseline records the leak counter for the xray started as pid, once
// that xray answers on its API. xray binds its inbounds — TPROXY included —
// before its API, so from then on nothing falls through by design: the
// counter's value at that moment is the restart window (and every earlier
// one), and what it adds later bypassed a RUNNING xray (diagnostics no_leak).
// The table may still be loading at a daemon start; it is asked again until
// the budget runs out. No API to ask, or an xray replaced first: no baseline,
// and no_leak says it cannot tell rather than guess.
func (d *daemon) takeLeakBaseline(pid int) {
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return
	}
	view, err := xrayview.Parse(raw)
	if err != nil || !xrayview.Loopback(view.APIListen) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), d.startBudget())
	defer cancel()
	for {
		if s := d.sup.Status(); s.State != supervisor.StateRunning || s.PID != pid {
			return // gone, or replaced: that start takes its own
		}
		if answers(ctx, view.APIListen) {
			if c, ok := d.leakCounters(ctx); ok {
				d.storeLeakBaseline(&localctl.LeakBaseline{
					Packets:    c[firewall.CounterKillSwitchShadow],
					Escaped:    c[firewall.CounterTproxyEscaped],
					Drops:      c[firewall.CounterKillSwitchDrops],
					TproxyHits: c[firewall.CounterTproxyHits],
					At:         time.Now().UTC(),
					XrayPID:    pid,
				})
				return
			}
		}
		select {
		case <-ctx.Done():
			logging.L().Debug("no leak baseline for this xray start", "xrayPid", pid)
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// storeLeakBaseline publishes b, taken for the xray b.XrayPID, unless that
// xray is no longer the one running — reading the counters takes time, and a
// restart meanwhile has a start hook of its own — or it already has one. It
// only ever replaces the baseline of a different xray, and reports whether it
// stored b.
func (d *daemon) storeLeakBaseline(b *localctl.LeakBaseline) bool {
	for {
		old := d.leakBaseline.Load()
		if old != nil && old.XrayPID == b.XrayPID {
			return false
		}
		if s := d.sup.Status(); s.State != supervisor.StateRunning || s.PID != b.XrayPID {
			return false
		}
		if d.leakBaseline.CompareAndSwap(old, b) {
			return true
		}
	}
}

// leakCounters reads the vctl table's counters; false when it is not loaded.
func (d *daemon) leakCounters(ctx context.Context) (map[string]int64, bool) {
	if d.readCounters != nil {
		return d.readCounters(ctx)
	}
	return uiapi.NftCounters(ctx, uiapi.RouterEnv(d.cfg, ""))
}

// answers reports whether something accepts a TCP connection at addr.
func answers(ctx context.Context, addr string) bool {
	c, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(c, "tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// reapplyPins restores the router's balancer pins after an xray start: xray
// keeps overrides in memory only. It runs in its own goroutine (the
// supervisor's start hook) and touches no daemon field but the exit check's
// (it locks) — only files and the API. A pin whose balancer or node is gone
// from the new config is dropped rather than applied to whatever now carries
// that tag; one whose node the exit check left out waits.
func (d *daemon) reapplyPins(pid int) {
	ov, err := localctl.LoadOverrides(d.cfg.OverridesPath)
	if err != nil || len(ov.Pins) == 0 {
		return
	}
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return
	}
	view, err := xrayview.Parse(raw)
	if err != nil || !xrayview.Loopback(view.APIListen) {
		logging.L().Warn("balancer pins not restored: the running config has no xray API", "pins", len(ov.Pins))
		return
	}
	var stale, parked []string
	unfit := d.exits.Unfit()
	for _, b := range ov.PinnedBalancers() {
		if err := view.CanPin(b, ov.Pins[b]); err != nil {
			// An exit the exit check left out is still in the config: the
			// owner's pin waits for it, unapplied — applied, it would steer
			// the balancer back onto the exit that fails blocked sites.
			if contains(unfit, ov.Pins[b]) && view.Outbound(ov.Pins[b]) != nil && view.Balancer(b) != nil {
				parked = append(parked, b)
				logging.L().Warn("a balancer pin waits: the exit check left its node out", "balancer", b, "node", ov.Pins[b])
				continue
			}
			stale = append(stale, b)
			logging.L().Warn("dropping a balancer pin the running config no longer supports", "balancer", b, "node", ov.Pins[b], "err", err.Error())
		}
	}
	if len(stale) > 0 {
		_, _ = localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
			for _, b := range stale {
				delete(o.Pins, b)
			}
			return nil
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), d.startBudget())
	defer cancel()
	for _, b := range ov.PinnedBalancers() {
		if contains(stale, b) || contains(parked, b) {
			continue
		}
		node := ov.Pins[b]
		for {
			cctx, ccancel := context.WithTimeout(ctx, 3*time.Second)
			err := api.OverrideBalancerTarget(cctx, view.APIListen, b, node)
			ccancel()
			if err == nil {
				logging.L().Info("balancer pin restored", "balancer", b, "node", node, "xrayPid", pid)
				break
			}
			if errors.Is(err, api.ErrUnknownBalancer) || ctx.Err() != nil {
				logging.L().Warn("could not restore a balancer pin", "balancer", b, "node", node, "err", err.Error())
				break
			}
			// xray is still coming up.
			select {
			case <-ctx.Done():
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
}

// reconcileRender re-renders the cached provider document when the options
// the render was made with are not the current ones — after an upgrade that
// added the API, or a probe interval changed while the daemon was down. It
// runs once at start, before xray is brought up, so it costs no restart.
func (d *daemon) reconcileRender(ctx context.Context) {
	if d.desired == nil || d.applier == nil || d.applier.Tproxy == nil {
		return
	}
	raw, err := vault.ReadFile(d.documentPath())
	if err != nil || len(raw) == 0 {
		return
	}
	opts, probe := d.spliceOptions(raw)
	if d.st.SpliceKey == d.renderKey(opts) {
		// Nothing to redo, but the UI must still learn what the running
		// render was made with.
		d.probe = &probe
		return
	}
	res, err := d.applyProvider(ctx, raw, false)
	if err != nil {
		logging.L().Warn("could not re-render the installed config under the current options; keeping the old render", "err", err.Error())
		return
	}
	logging.L().Info("re-rendered the installed config under the current router options", "changed", res.Changed)
	if err := d.persist(); err != nil {
		logging.L().Warn("persist state after re-render", "err", err.Error())
	}
}

// renderKey is what a render is made from besides the provider's document:
// the router's own splice options and the operator's tproxy inbound it
// splices in. A render made under another is redone. The inbound was not in
// it, so a changed inbound — sniffing's routeOnly on the test router — left
// the old render in place with the new config reporting otherwise.
func (d *daemon) renderKey(opts xray.SpliceOptions) string {
	k := opts.Key()
	if d.applier != nil && d.applier.Tproxy != nil {
		if raw, err := json.Marshal(d.applier.Tproxy); err == nil {
			k += ";tproxy=" + string(raw)
		}
	}
	// And the geo directory it is checked against: a render checked against
	// another is checked again (a new operator config's directory, vctl's
	// own once vectra-geodata is there).
	// And the provider guard the render passed (xray/provider_guard.go): a
	// render made before r12 kept the provider's own log block and api, so
	// it is redone once, at start, before xray comes up.
	return k + ";geo=" + d.geoAssetDir() + ";guard=1"
}

// resumeRender puts back, after a reboot, the render the router ran before it.
//
// The render lives on tmpfs; the operator config and the last-good provider
// document it was made from live on /etc. Without this a router switched on
// for good came up from every reboot — the fleet's is daily, at 04:30 — with
// vctl running and holding the router, and no xray and no data plane: the LAN
// out directly until a panel job or an operator's apply-local, while the
// router said Vectra carried it.
//
// It resumes; it does not adopt anything: only a router that applied before
// (state.json has its digest) gets its render back, from the document stored
// verbatim by that apply, through the same applier and `xray -test` gate. A
// router that never applied waits for its first render as before — a job,
// the router UI or apply-local.
func (d *daemon) resumeRender(ctx context.Context) {
	if fileExists(d.cfg.XrayRenderPath) || d.desired == nil || d.applier == nil || d.applier.Tproxy == nil || d.st.ConfigDigest == "" {
		return
	}
	raw, err := vault.ReadFile(d.documentPath())
	if err != nil || len(raw) == 0 {
		logging.L().Warn("no render to run and no last-good provider document to rebuild it from",
			"provider", d.documentPath())
		d.resumeLastGoodRender(ctx, "no last-good provider document")
		return
	}
	res, err := d.applyProvider(ctx, raw, false)
	if err != nil {
		// A document refused since (r12's guard) or a render that cannot be
		// made again: never left without a data plane — the last render
		// xray took runs (last_good_render.go).
		logging.L().Error("could not rebuild the render from the last-good provider document", "err", err.Error())
		d.resumeLastGoodRender(ctx, err.Error())
		return
	}
	d.nodeCount = countProviderOutbounds(raw)
	logging.L().Info("rebuilt the render the router ran before its restart", "digest", res.AppliedDigest)
	if err := d.persist(); err != nil {
		logging.L().Warn("persist state after rebuilding the render", "err", err.Error())
	}
}

// waitForTick blocks until the next poll, serving UI requests meanwhile.
// It returns false when ctx ends.
func (d *daemon) waitForTick(ctx context.Context, tick <-chan time.Time) bool {
	var rotated <-chan struct{} // nil: never ready
	if d.claim != nil {
		rotated = d.claim.rotated
	}
	watch := time.NewTicker(dnsWatchEvery)
	defer watch.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-tick:
			return true
		case <-watch.C:
			// xray down: the router's lookups leave the dead inbound within
			// seconds, not at the next poll (dnsWatchDue).
			if d.dnsWatchDue(ctx) {
				d.programFirewallWithin(ctx, d.desired, 0)
				d.publishRuntime()
			}
			// A probe through the tunnel just failed, or direct mode may go
			// back: the rescue looks again now, not at the next poll.
			if d.rescueRecheckDue(time.Now()) {
				if dec := d.rescueStep(ctx); dec.ShouldTransition {
					d.applyRescueTransition(ctx, dec)
					d.publishRuntime()
				}
			}
		case <-rotated:
			// The UI shows a new claim code: check in now, so the panel knows it.
			return true
		case req := <-d.uiReqs:
			resp := d.handleUIRequest(ctx, req.op, req.change)
			// Publish first: a status read right after the reply must already
			// see the new location / interval, not the busy flag.
			d.publishRuntime()
			req.reply <- resp
		}
	}
}

func controllerVersion() string {
	if runtimeVersion != "" && runtimeVersion != "dev" {
		return runtimeVersion
	}
	return Version
}

func shortDigest(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// localReapplyPassWall is localReapplyOnce under PassWall-compatible routing:
// the owner's sites and the probe interval apply on top of PassWall's
// routing; the location is not the router's to choose — it is the operator's
// route policy, PassWall2's own configuration.
func (d *daemon) localReapplyPassWall(ctx context.Context, change *localctl.Change) localctl.SocketResponse {
	if change != nil && change.TouchesEntry() {
		return localctl.SocketResponse{Code: "apply_failed", Detail: "locations follow the operator's route policy on this router (PassWall-compatible routing)"}
	}
	ov, err := localctl.LoadOverrides(d.cfg.OverridesPath)
	if err != nil {
		return localctl.SocketResponse{Code: "internal", Detail: err.Error()}
	}
	if change != nil {
		change.ApplyTo(&ov)
	}
	raw, err := vault.ReadFile(d.documentPath())
	if err != nil {
		return localctl.SocketResponse{Code: "apply_failed", Detail: err.Error()}
	}
	opts, probe := spliceOptionsFor(raw, ov, !d.cfg.NoRussiaDirect)
	opts = d.withRuntime(opts, raw)
	res, err := d.applyProviderWith(ctx, raw, false, opts, probe)
	if err != nil {
		return localctl.SocketResponse{Code: "apply_failed", Detail: err.Error()}
	}
	d.reloadAfterApply(ctx, res, raw)
	if err := d.persist(); err != nil {
		logging.L().Warn("persist state after a local apply", "err", err.Error())
	}
	return localctl.SocketResponse{OK: true}
}
