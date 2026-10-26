package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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
// bridge — an nftset line in a fragment of its config directory — and which
// names those are, xray says itself (TestRoute), after every start of it: a
// name the owner sent elsewhere (a service's country, a site of theirs) does
// not land on the bridge and stays refused. A fragment that changed restarts
// dnsmasq (its config is read only at start) and empties the set.

// bridgeQUICFragment is vctl's file in dnsmasq's config directory.
const bridgeQUICFragment = "vctl-bridge-quic.conf"

// dnsmasqConfDirGlob finds the instances' config directories: OpenWrt's init
// script reads /tmp/dnsmasq.<section>.d (conf-dir) at every start.
var dnsmasqConfDirGlob = "/tmp/dnsmasq.*.d"

// bridgeQUICWithin bounds the question to xray and the resolver's restart.
const bridgeQUICWithin = 10 * time.Second

// bridgeQUICRecheck is how often xray is asked again between its starts. With
// no live member, BL-RU falls back to the main path, which is plain Vision and
// drops UDP/443 without an answer: TestRoute then names the fallback, the
// names leave and their QUIC is refused again — and comes back with the
// bridge.
const bridgeQUICRecheck = 5 * time.Minute

// bridgeQUICRetry is how soon xray is asked again while none of the names
// lands on the bridge: right after xray starts its balancer may still send
// them to its fallback, until the observatory has seen the bridge alive.
const bridgeQUICRetry = 30 * time.Second

// carryBridgeQUIC lets the table pass QUIC to the set (Spec.QUICPass) when
// the running render has a Russian bridge that carries UDP/443 and names
// routed to it. Which of those names reach the set is syncBridgeQUIC's.
func (d *daemon) carryBridgeQUIC(spec *firewall.Spec) {
	if !spec.RefuseQUIC || d.cfg.NoQUICBridge {
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
	pass := d.fwSpec != nil && d.fwSpec.QUICPass && d.rescueState().Mode != rescue.ModeDirect
	key := st.StartedAt.String()
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
	var names []string
	waiting := ""
	if pass {
		c, cancel := context.WithTimeout(ctx, bridgeQUICWithin)
		got, elsewhere, err := d.bridgeQUICNames(c)
		cancel()
		if err != nil {
			// xray's API not answering yet: the next look asks again; the
			// set keeps what it has meanwhile.
			logging.L().Debug("QUIC through the Russian bridge: xray did not answer where the names go", "err", err.Error())
			return
		}
		names = got
		if len(names) == 0 {
			waiting = elsewhere
			if waiting == "" {
				waiting = "none"
			}
			if waiting != d.bridgeQUICWaiting {
				logging.L().Info("QUIC through the Russian bridge waits: xray sends the bridge's names elsewhere", "route", waiting)
			}
		}
	}
	table := "vctl"
	if d.fwSpec != nil && d.fwSpec.TableName != "" {
		table = d.fwSpec.TableName
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
	d.restartResolverForBridgeQUIC(ctx, table)
}

// bridgeQUICNames are the running render's bridge names xray routes, for
// QUIC from the LAN, to a bridge outbound that carries UDP/443; elsewhere is
// where it routed the first name that did not land there.
func (d *daemon) bridgeQUICNames(ctx context.Context) (names []string, elsewhere string, err error) {
	raw, err := vault.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return nil, "", err
	}
	bq := xray.RenderBridgeQUIC(raw)
	if len(bq.Names) == 0 {
		return nil, "", nil
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
		return nil, "", err
	}
	members := map[string]bool{}
	for _, m := range bq.Members {
		members[m] = true
	}
	for i, n := range bq.Names {
		switch {
		case i < len(tags) && members[tags[i]]:
			names = append(names, n)
		case elsewhere == "" && i < len(tags):
			elsewhere = tags[i]
		}
	}
	return names, elsewhere, nil
}

// bridgeQUICLine is the fragment for names ("" for none): dnsmasq adds every
// IPv4 answer for them and their subdomains to the table's set.
func bridgeQUICLine(names []string, table string) string {
	if len(names) == 0 {
		return ""
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	return "# Written by vectra-controller-pro: QUIC to these names goes through the Russian bridge.\n" +
		"nftset=/" + strings.Join(sorted, "/") + "/4#inet#" + table + "#" + firewall.SetQUICPass4 + "\n"
}

// writeBridgeQUICFragment puts the fragment for names into every dnsmasq
// config directory there is, or takes it out for none; changed says whether
// any file changed.
func writeBridgeQUICFragment(names []string, table string) (bool, error) {
	want := bridgeQUICLine(names, table)
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
			if err := os.Remove(path); err != nil {
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
// no pass.
func (d *daemon) restartResolverForBridgeQUIC(ctx context.Context, table string) {
	c, cancel := context.WithTimeout(ctx, bridgeQUICWithin)
	defer cancel()
	restart := restartDnsmasq
	if d.restartResolver != nil {
		restart = d.restartResolver
	}
	if err := restart(c); err != nil {
		logging.L().Warn("dnsmasq not restarted; its QUIC set follows at its next start", "err", err.Error())
	}
	_ = d.runFirewallCmd(c, "nft", "flush", "set", "inet", table, firewall.SetQUICPass4)
}

func restartDnsmasq(ctx context.Context) error {
	return exec.CommandContext(ctx, "/etc/init.d/dnsmasq", "restart").Run()
}

// dropBridgeQUIC takes the fragment out with the data plane.
func (d *daemon) dropBridgeQUIC(ctx context.Context) {
	changed, err := writeBridgeQUICFragment(nil, "")
	d.bridgeQUICSynced = ""
	d.bridgeQUICWaiting = ""
	if err != nil {
		logging.L().Warn("dnsmasq's QUIC fragment not removed", "err", err.Error())
		return
	}
	if !changed {
		return
	}
	c, cancel := context.WithTimeout(ctx, bridgeQUICWithin)
	defer cancel()
	restart := restartDnsmasq
	if d.restartResolver != nil {
		restart = d.restartResolver
	}
	if err := restart(c); err != nil {
		logging.L().Warn("dnsmasq not restarted after its QUIC fragment left", "err", err.Error())
	}
}
