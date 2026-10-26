package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/supervisor"
	"vectra-controller-pro/internal/vault"
)

// QUIC through the Russian bridge (r31; internal/coreengine/xray/bridge_quic.go
// says why).
//
// The render gives the bridge's outbounds the Vision flow that carries
// UDP/443, and the table lets through QUIC to the FakeDNS addresses in
// firewall.SetQUICPass4 while it refuses the rest (Spec.QUICPass). This fills
// the set. dnsmasq adds the answers for the names whose route ends on the
// bridge — nftset lines in a fragment of its config directory — and which
// names those are, xray says itself (TestRoute), after every start of it: a
// name the owner sent elsewhere (a service's country, a site of theirs) does
// not land on the bridge and stays refused. A fragment that changed restarts
// dnsmasq (its config is read only at start) and empties the set.

// bridgeQUICFragment is vctl's file in dnsmasq's config directory.
const bridgeQUICFragment = "vctl-bridge-quic.conf"

// dnsmasqConfDirGlob finds the instances' config directories: OpenWrt's init
// script reads /tmp/dnsmasq.<section>.d (conf-dir) at every start.
var dnsmasqConfDirGlob = "/tmp/dnsmasq.*.d"

// dnsmasqInit is the resolver's init script.
var dnsmasqInit = "/etc/init.d/dnsmasq"

// bridgeQUICWithin bounds the question to xray, and how long the loop waits
// for dnsmasq's restart (the restart itself is never cut short).
const bridgeQUICWithin = 10 * time.Second

// resolverBackWithin is how long dnsmasq has to be running again after its
// restart before the fragment is taken for the reason it is not; it counts
// as running once seen as its own user twice, resolverSteadyAfter apart (one
// that dies on its config lives for milliseconds, and procd spawns it again).
var resolverBackWithin, resolverSteadyAfter = 5 * time.Second, time.Second

// resolverRestartGrace: for this long after vctl restarted dnsmasq, a
// dnsmasq missing from /proc, or running as root while it starts, is that
// restart — not a resolver that changed user (dnsRedirectStale).
const resolverRestartGrace = 30 * time.Second

// bridgeQUICSettle is how long xray runs before dnsmasq is changed under it.
// A fresh xray resolves its nodes' names through dnsmasq and probes them (the
// observatory's first round, the failover watchdog's re-probe): a dnsmasq
// restarted then fails those lookups, and the nodes stay dead until the
// watchdog's next restart, 10 min on — on the stand the rescue went direct.
const bridgeQUICSettle = 2 * time.Minute

// bridgeQUICRecheck is how often xray is asked again between its starts, while
// the names pass.
const bridgeQUICRecheck = 5 * time.Minute

// bridgeQUICRetry is how soon xray is asked again while its answer lets no
// name through or is not final yet (BL-RU on its fallback).
const bridgeQUICRetry = 30 * time.Second

// bridgeQUICHold is how long BL-RU may stay on its fallback before its names
// lose their pass. xray sends them to the fallback while it knows no member
// alive: a fresh start before the observatory's first round, a bridge the
// observatory missed once. Meanwhile the fragment, and dnsmasq, stay as they
// are — every change restarts dnsmasq, a moment without DNS for the LAN —
// and QUIC to the names reaches the fallback (the main path, plain Vision),
// which drops it without an answer: a browser goes on over TCP, as before
// r25. The hold runs on across xray's starts; a bridge down for longer gives
// its names up, and they come back with it.
const bridgeQUICHold = 30 * time.Minute

// carryBridgeQUIC lets the table pass QUIC to the set (Spec.QUICPass) when
// the running render has a Russian bridge that carries UDP/443 and names
// routed to it, and the table carries FakeDNS (no pool, no refusal and no
// set: carryFakeDNS runs first). Which of those names reach the set is
// syncBridgeQUIC's.
func (d *daemon) carryBridgeQUIC(spec *firewall.Spec) {
	if !spec.RefuseQUIC || len(spec.CarriedV4) == 0 || d.cfg.NoQUICBridge || d.bridgeQUICBroken {
		return
	}
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return
	}
	bq := xray.RenderBridgeQUIC(raw)
	spec.QUICPass = len(bq.Members) > 0 && len(bq.Names) > 0
}

// syncBridgeQUIC brings dnsmasq's fragment to the names that pass now: at
// every xray start, whenever the table's QUICPass changes, and every
// bridgeQUICRecheck. The fragment, and dnsmasq, change only when the names do.
func (d *daemon) syncBridgeQUIC(ctx context.Context) {
	if !d.supStarted {
		return
	}
	st := d.xrayStatus()
	if st.State != supervisor.StateRunning || st.StartedAt.IsZero() {
		return
	}
	pass := d.fwSpec != nil && d.fwSpec.QUICPass && !d.bridgeQUICBroken && d.rescueState().Mode != rescue.ModeDirect
	started := st.StartedAt.String()
	table := "vctl"
	if d.fwSpec != nil && d.fwSpec.TableName != "" {
		table = d.fwSpec.TableName
	}
	if started != d.bridgeQUICStart {
		// A new xray hands its FakeDNS addresses out anew: one the set still
		// holds may stand for another name now.
		if d.bridgeQUICStart != "" && pass {
			d.flushBridgeQUICSet(ctx, table)
		}
		d.bridgeQUICStart = started
	}
	key := started
	if pass {
		every := bridgeQUICRecheck
		if d.bridgeQUICWaiting != "" {
			every = bridgeQUICRetry
		}
		key += ";pass@" + d.now().Truncate(every).String()
	}
	if key == d.bridgeQUICSynced {
		return
	}
	if time.Since(st.StartedAt) < bridgeQUICSettle {
		return
	}
	var names []string
	waiting := ""
	if pass {
		c, cancel := context.WithTimeout(ctx, bridgeQUICWithin)
		ans, err := d.bridgeQUICNames(c)
		cancel()
		if err != nil {
			// xray's API not answering yet: the next look asks again; the
			// set keeps what it has meanwhile.
			logging.L().Debug("QUIC through the Russian bridge: xray did not answer where the names go", "err", err.Error())
			return
		}
		if !ans.fallback {
			d.bridgeQUICHeldSince = time.Time{}
		} else {
			if d.bridgeQUICHeldSince.IsZero() {
				d.bridgeQUICHeldSince = d.now()
			}
			if d.now().Sub(d.bridgeQUICHeldSince) < bridgeQUICHold {
				if d.bridgeQUICWaiting != bridgeQUICOnFallback {
					logging.L().Info("QUIC through the Russian bridge waits: BL-RU is on its fallback, the names keep their pass", "route", ans.elsewhere)
				}
				d.bridgeQUICSynced, d.bridgeQUICWaiting = key, bridgeQUICOnFallback
				return
			}
		}
		names = ans.names
		if len(names) == 0 {
			waiting = ans.elsewhere
			if waiting == "" {
				waiting = "none"
			}
			if waiting != d.bridgeQUICWaiting {
				logging.L().Info("QUIC through the Russian bridge waits: xray sends the bridge's names elsewhere", "route", waiting)
			}
		}
	}
	if len(names) > 0 {
		accepts := dnsmasqAccepts
		if d.resolverAccepts != nil {
			accepts = d.resolverAccepts
		}
		if err := accepts(ctx, bridgeQUICLines(names, table)); err != nil {
			logging.L().Error("dnsmasq does not take the QUIC fragment: QUIC to every proxied name stays refused until vctl starts again", "err", err.Error())
			d.bridgeQUICBroken = true
			names = nil
		}
	}
	changed, err := writeBridgeQUICFragment(names, table)
	if err != nil {
		logging.L().Warn("QUIC through the Russian bridge: dnsmasq's fragment not written", "err", err.Error())
		return
	}
	d.bridgeQUICSynced = key
	d.bridgeQUICWaiting = waiting
	if !changed {
		return
	}
	if len(names) > 0 {
		logging.L().Info("QUIC to the Russian bridge's names goes through it", "names", strings.Join(names, ","))
	} else {
		logging.L().Info("QUIC to every proxied name is refused again")
	}
	d.restartResolverForBridgeQUIC(ctx, table, len(names) > 0)
}

// bridgeQUICOnFallback is bridgeQUICWaiting while BL-RU is on its fallback.
const bridgeQUICOnFallback = "BL-RU fallback"

// bridgeQUICAnswer is xray's word on the bridge's names: those it routes to a
// member that carries UDP/443; whether it sent any to BL-RU's fallback (or
// could not pick for it); and where the first name that missed the bridge went.
type bridgeQUICAnswer struct {
	names     []string
	fallback  bool
	elsewhere string
}

// bridgeQUICNames asks xray where QUIC from the LAN to each of the running
// render's bridge names goes.
func (d *daemon) bridgeQUICNames(ctx context.Context) (bridgeQUICAnswer, error) {
	var ans bridgeQUICAnswer
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return ans, err
	}
	bq := xray.RenderBridgeQUIC(raw)
	if len(bq.Names) == 0 {
		return ans, nil
	}
	tag := "tproxy-in"
	if d.desired != nil && d.desired.Inbounds.Tproxy != nil && d.desired.Inbounds.Tproxy.Tag != "" {
		tag = d.desired.Inbounds.Tproxy.Tag
	}
	queries := make([]api.RouteQuery, len(bq.Names))
	for i, n := range bq.Names {
		queries[i] = api.RouteQuery{InboundTag: tag, UDP: true, Domain: n, Port: 443}
	}
	testRoute := api.TestRoute
	if d.testRoute != nil {
		testRoute = d.testRoute
	}
	tags, err := testRoute(ctx, api.DefaultAPIServer, queries)
	if err != nil {
		return ans, err
	}
	members := map[string]bool{}
	for _, m := range bq.Members {
		members[m] = true
	}
	for i, n := range bq.Names {
		got := ""
		if i < len(tags) {
			got = tags[i]
		}
		if members[got] {
			ans.names = append(ans.names, n)
			continue
		}
		// No pick (a balancer with no fallback and no member alive errs), or
		// the fallback's.
		if got == "" || got == bq.Fallback {
			ans.fallback = true
		}
		if ans.elsewhere == "" {
			ans.elsewhere = got
		}
	}
	return ans, nil
}

// bridgeQUICLines is the fragment for names ("" for none): dnsmasq adds every
// IPv4 answer for them and their subdomains to the table's set. One line per
// name: dnsmasq reads a config line in at most 1024 bytes, splits a longer
// one, and does not start with the rest of it — the provider's names on one
// line come to 0.9–1.6 KB.
func bridgeQUICLines(names []string, table string) string {
	if len(names) == 0 {
		return ""
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	var b strings.Builder
	b.WriteString("# Written by vectra-controller-pro: QUIC to these names goes through the Russian bridge.\n")
	for _, n := range sorted {
		b.WriteString("nftset=/" + n + "/4#inet#" + table + "#" + firewall.SetQUICPass4 + "\n")
	}
	return b.String()
}

// writeBridgeQUICFragment puts the fragment for names into every dnsmasq
// config directory there is, or takes it out for none; changed says whether
// any file changed.
func writeBridgeQUICFragment(names []string, table string) (bool, error) {
	want := bridgeQUICLines(names, table)
	dirs, err := filepath.Glob(dnsmasqConfDirGlob)
	if err != nil {
		return false, err
	}
	changed := false
	for _, dir := range dirs {
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		path := filepath.Join(dir, bridgeQUICFragment)
		have, err := os.ReadFile(path)
		switch {
		case want == "" && err == nil:
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return changed, err
			}
			changed = true
		case want != "" && (err != nil || string(have) != want):
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	if want != "" && len(dirs) == 0 {
		logging.L().Warn("QUIC through the Russian bridge: no dnsmasq config directory; QUIC stays refused", "glob", dnsmasqConfDirGlob)
	}
	return changed, nil
}

// restartResolverForBridgeQUIC starts dnsmasq again (it reads the fragment
// only at start) and empties the set: an address of a name that left keeps
// no pass. A dnsmasq that is not running is not started: it reads the
// fragment whenever it does. One that does not come back with names in the
// fragment loses the fragment and starts without it — QUIC to every proxied
// name refused, as in r30, until vctl starts again — rather than leave the LAN
// without DNS and DHCP.
func (d *daemon) restartResolverForBridgeQUIC(ctx context.Context, table string, withNames bool) {
	if !d.resolverRunning() {
		d.flushBridgeQUICSet(ctx, table)
		return
	}
	d.restartResolverNow(ctx)
	d.flushBridgeQUICSet(ctx, table)
	if !withNames || d.resolverBack(ctx) {
		return
	}
	logging.L().Error("dnsmasq did not come back with the QUIC fragment: it goes, and QUIC to every proxied name is refused again until vctl starts again")
	d.bridgeQUICBroken = true
	if _, err := writeBridgeQUICFragment(nil, ""); err != nil {
		logging.L().Warn("dnsmasq's QUIC fragment not removed", "err", err.Error())
	}
	d.restartResolverNow(ctx)
}

// restartResolverNow restarts dnsmasq and waits for it at most
// bridgeQUICWithin; the clock for dnsRedirectStale's grace starts here.
func (d *daemon) restartResolverNow(ctx context.Context) {
	d.resolverRestartedAt = d.now()
	c, cancel := context.WithTimeout(ctx, bridgeQUICWithin)
	defer cancel()
	restart := restartDnsmasq
	if d.restartResolver != nil {
		restart = d.restartResolver
	}
	if err := restart(c); err != nil {
		logging.L().Warn("dnsmasq's restart for the QUIC fragment", "err", err.Error())
	}
}

// resolverRunning: a dnsmasq process is there.
func (d *daemon) resolverRunning() bool {
	return len(resolverPIDs(d.procRoot())) > 0
}

// resolverBack waits up to resolverBackWithin for dnsmasq to be running as
// its own user, steadily.
func (d *daemon) resolverBack(ctx context.Context) bool {
	deadline := time.Now().Add(resolverBackWithin)
	var since time.Time
	for {
		if len(resolverUIDs(d.procRoot())) == 0 {
			since = time.Time{}
		} else if since.IsZero() {
			since = time.Now()
		} else if time.Since(since) >= resolverSteadyAfter {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(resolverSteadyAfter / 4):
		}
	}
}

// dnsmasqAccepts: dnsmasq takes the fragment — its syntax check on the
// fragment alone (a dnsmasq built without nftset, a line it would split).
// The resolver's restart is never risked on one it does not.
func dnsmasqAccepts(ctx context.Context, fragment string) error {
	f, err := os.CreateTemp("", "vctl-bridge-quic-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(fragment); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	c, cancel := context.WithTimeout(ctx, bridgeQUICWithin)
	defer cancel()
	out, err := exec.CommandContext(c, "dnsmasq", "--test", "-C", f.Name()).CombinedOutput()
	if err != nil {
		return errors.New(strings.TrimSpace(err.Error() + ": " + string(out)))
	}
	return nil
}

// resolverRestarting: vctl restarted dnsmasq less than resolverRestartGrace
// ago.
func (d *daemon) resolverRestarting() bool {
	return !d.resolverRestartedAt.IsZero() && d.now().Sub(d.resolverRestartedAt) < resolverRestartGrace
}

func (d *daemon) flushBridgeQUICSet(ctx context.Context, table string) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), bridgeQUICWithin)
	defer cancel()
	_ = d.runFirewallCmd(c, "nft", "flush", "set", "inet", table, firewall.SetQUICPass4)
}

// errStillRestarting: restartDnsmasq stopped waiting, the restart goes on.
var errStillRestarting = errors.New("still restarting; left to finish")

// restartDnsmasq runs dnsmasq's restart in a session of its own and waits for
// it while ctx lasts. It is never killed: a restart is a stop and a start,
// and one cut between them — a timeout, vctl's own stop — would leave the LAN
// without DNS and DHCP.
func restartDnsmasq(ctx context.Context) error {
	cmd := exec.Command(dnsmasqInit, "restart")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return errStillRestarting
	}
}

// dropBridgeQUIC takes the fragment out with the data plane, after the table:
// going direct does not wait for dnsmasq, whose restart is started and left
// to finish.
func (d *daemon) dropBridgeQUIC() {
	changed, err := writeBridgeQUICFragment(nil, "")
	d.bridgeQUICSynced, d.bridgeQUICWaiting, d.bridgeQUICStart = "", "", ""
	d.bridgeQUICHeldSince = time.Time{}
	if err != nil {
		logging.L().Warn("dnsmasq's QUIC fragment not removed", "err", err.Error())
		return
	}
	if !changed || !d.resolverRunning() {
		return
	}
	d.resolverRestartedAt = d.now()
	restart := restartDnsmasq
	if d.restartResolver != nil {
		restart = d.restartResolver
	}
	started, cancel := context.WithCancel(context.Background())
	cancel()
	if err := restart(started); err != nil && !errors.Is(err, errStillRestarting) {
		logging.L().Warn("dnsmasq not restarted after its QUIC fragment left", "err", err.Error())
	}
}
