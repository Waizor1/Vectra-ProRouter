package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/conntrack"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/failover"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/supervisor"
	"vectra-controller-pro/internal/vault"
)

// deadTunnelDaemon: every probe through the tunnel fails, the line around it
// works — what a router with no tunnel at all sees too (released, or claimed
// and not given a config yet): its "tunnel" probe goes out the WAN.
func deadTunnelDaemon(t *testing.T) *daemon {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(srv.Close)
	d := confirmDaemon(t, srv.URL, filepath.Join(t.TempDir(), "fw-confirm"), &http.Client{Transport: toServer{srv}})
	d.rescuePolicy.HealthURLs = []string{"http://only-around-the-tunnel.invalid/generate_204"}
	d.rescuePolicy.TraceURLs = []string{"http://only-around-the-tunnel.invalid/cdn-cgi/trace"}
	d.rescuePolicy.Cooldown = 0
	d.rescuePolicy.MinFailSpan = 0
	d.client = controlplane.NewClient(controlplane.Options{BaseURL: srv.URL, HTTPClient: &http.Client{Transport: toServer{srv}}})
	return d
}

// A router released in the Vectra app, or claimed and not yet given the
// owner's config, has no tunnel: the rescue counts nothing against it and
// never sends it direct. On 2026-10-04 (vctl r17) it did, 20 s after a claim,
// and kept the owner's first config off the VPN for the two-minute cooldown.
func TestTheRescueJudgesNoTunnelWithoutAConfig(t *testing.T) {
	d := deadTunnelDaemon(t)
	// A run left from before the release: not this router's outage.
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy, ProxyFailureCount: 2, FirstFailureAt: time.Now().Add(-time.Minute)}, "")

	inv := controlplane.RouterInventory{}
	for i := 0; i < d.rescuePolicy.TriggerFailureCount+2; i++ {
		if _, dec := d.evaluateHealth(context.Background(), &inv); dec.ShouldTransition {
			t.Fatalf("no config, and the rescue went %s: %+v", dec.NextMode, dec)
		}
	}
	if st := d.rescueState(); st.Mode != rescue.ModeProxy || st.ProxyFailureCount != 0 || !st.FirstFailureAt.IsZero() {
		t.Fatalf("no config, and the rescue kept a failure run: %+v", st)
	}
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy, ProxyFailureCount: 1}, "")
	if d.rescueRecheckDue(time.Now()) {
		t.Fatal("no config, and the rescue looks again between the polls")
	}
}

// Right after an apply the rescue waits for the new config to settle, its
// failure window starting again; after that a dead tunnel still sends the
// router direct.
func TestTheRescueWaitsForANewConfigThenStillTrips(t *testing.T) {
	d := deadTunnelDaemon(t)
	d.desired = &config.Config{}
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy, ProxyFailureCount: 2, FirstFailureAt: time.Now().Add(-time.Minute)}, "")
	d.rescueAfresh(time.Now())
	if st := d.rescueState(); st.ProxyFailureCount != 0 || !st.FirstFailureAt.IsZero() {
		t.Fatalf("an apply kept the old failure window: %+v", st)
	}

	inv := controlplane.RouterInventory{}
	for i := 0; i < d.rescuePolicy.TriggerFailureCount+2; i++ {
		if _, dec := d.evaluateHealth(context.Background(), &inv); dec.ShouldTransition {
			t.Fatalf("the new config has not settled, and the rescue went %s: %+v", dec.NextMode, dec)
		}
	}
	if st := d.rescueState(); st.ProxyFailureCount != 0 {
		t.Fatalf("failures counted while the new config settles: %+v", st)
	}
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy, ProxyFailureCount: 1}, "")
	if d.rescueRecheckDue(time.Now()) {
		t.Fatal("the new config has not settled, and the rescue looks again between the polls")
	}
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy}, "")

	// Settled: the dead tunnel is a dead tunnel.
	d.reconfiguredAt = time.Now().Add(-xraySettle - time.Second)
	var last rescue.Decision
	for i := 0; i < d.rescuePolicy.TriggerFailureCount; i++ {
		_, last = d.evaluateHealth(context.Background(), &inv)
	}
	if !last.ShouldTransition || last.NextMode != rescue.ModeDirect {
		t.Fatalf("settled, the tunnel dead, the line up, and still proxy: %+v", last)
	}
}

// An apply that loads the data plane while the rescue holds direct mode puts
// the router back on the tunnel: the rescue says proxy and judges it afresh,
// not after its cooldown. With no data plane loaded it stays direct.
func TestAnApplyInDirectModeJudgesTheNewTunnel(t *testing.T) {
	d := &daemon{rescuePolicy: rescue.DefaultPolicy(), desired: &config.Config{}}
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: time.Now()}, "")
	d.rescueAfresh(time.Now())
	if st := d.rescueState(); st.Mode != rescue.ModeDirect {
		t.Fatalf("no data plane loaded, and the rescue left direct mode: %+v", st)
	}
	key := ""
	d.fwProgrammed = &key
	d.rescueAfresh(time.Now())
	if st := d.rescueState(); st.Mode != rescue.ModeProxy || st.ProxyFailureCount != 0 {
		t.Fatalf("the apply loaded the data plane, and the rescue still says %+v", st)
	}
}

// Back from direct mode, the resolver's cache is emptied once the DNS
// redirect is in again: what it answered over the open path (real addresses,
// the ISP's forged ones) must not keep sites off the tunnel. A reprogram that
// keeps the redirect does not empty it again.
func TestReturningToTheProxyEmptiesTheResolverCache(t *testing.T) {
	d, _, _ := shutdownDaemon(t)
	if err := vault.WriteFile(d.cfg.XrayRenderPath, []byte(renderWithDNS)); err != nil {
		t.Fatal(err)
	}
	d.procDir = fakeProc(t, dnsmasqAs453)
	d.rootDir = t.TempDir()
	d.dnsAnswers = func(context.Context, int) bool { return true }
	d.ownsAddr = func(ip net.IP) bool { return ip.Equal(net.IPv4(192, 168, 1, 1)) }
	var applied int
	d.applyRuleset = func(string, firewall.Spec) error { applied++; return nil }
	var hup []int
	d.hupResolver = func(pid int) error { hup = append(hup, pid); return nil }

	// Direct mode: no data plane, nothing redirected.
	d.fwProgrammed = nil
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: time.Now()}, "")
	d.applyRescueTransition(context.Background(), rescue.Decision{ShouldTransition: true, NextMode: rescue.ModeProxy, Reason: "direct path stable; retrying proxy"})
	if applied != 1 || d.fwProgrammed == nil {
		t.Fatalf("the data plane was not loaded again (applied %d)", applied)
	}
	if _, in := redirectPort(*d.fwProgrammed); !in {
		t.Fatalf("the DNS redirect is not back: %q", *d.fwProgrammed)
	}
	if !reflect.DeepEqual(hup, []int{7}) {
		t.Fatalf("back on the proxy, the resolver got %v, want one flush of dnsmasq [7]", hup)
	}

	d.reapplyFirewall(context.Background())
	if applied != 2 || len(hup) != 1 {
		t.Fatalf("a reprogram that kept the redirect emptied the cache again: applied %d, flushes %v", applied, hup)
	}
}

// One change of path, one flush: the same reason again within
// resolverFlushDedupe is not repeated; another reason is.
func TestResolverFlushesAreNotRepeated(t *testing.T) {
	d := dnsDaemon(t, renderWithDNS, dnsmasqAs453)
	var n int
	d.hupResolver = func(int) error { n++; return nil }
	d.flushResolverCache("direct mode")
	d.flushResolverCache("direct mode")
	if n != 1 {
		t.Fatalf("the same flush twice in a row: %d", n)
	}
	d.flushResolverCache("the resolver asks through the tunnel again")
	if n != 2 {
		t.Fatalf("a flush for another change was dropped: %d", n)
	}
	d.flushedAt = time.Now().Add(-resolverFlushDedupe)
	d.flushResolverCache("the resolver asks through the tunnel again")
	if n != 3 {
		t.Fatalf("a flush after resolverFlushDedupe was dropped: %d", n)
	}
}

// steerDaemon: a daemon with an operator config on disk, a render whose DNS
// inbound answers, dnsmasq as its own user, and the firewall, the resolver's
// SIGHUP and xray stood in for.
type steer struct {
	d       *daemon
	applied int
	hup     int
	started time.Time // the running xray's start
	reloads int
}

func steerDaemon(t *testing.T, render string) *steer {
	t.Helper()
	d, _, _ := shutdownDaemon(t)
	if err := vault.WriteFile(d.cfg.XrayRenderPath, []byte(render)); err != nil {
		t.Fatal(err)
	}
	s := &steer{d: d, started: time.Now().Add(-time.Hour)}
	d.procDir = fakeProc(t, dnsmasqAs453)
	d.rootDir = t.TempDir()
	d.dnsAnswers = func(context.Context, int) bool { return true }
	d.ownsAddr = func(ip net.IP) bool { return ip.Equal(net.IPv4(192, 168, 1, 1)) }
	d.applyRuleset = func(string, firewall.Spec) error { s.applied++; return nil }
	d.hupResolver = func(int) error { s.hup++; return nil }
	d.runFirewallCmd = func(context.Context, string, ...string) error { return nil }
	d.xrayStatusFn = func() supervisor.Status {
		return supervisor.Status{State: supervisor.StateRunning, StartedAt: s.started}
	}
	return s
}

const renderWithDNSAndFakeDNS = `{"inbounds":[{"tag":"tproxy-in","listen":"0.0.0.0","port":12345,"protocol":"dokodemo-door"},` +
	`{"tag":"vctl-dns-in","listen":"127.0.0.1","port":10053,"protocol":"dokodemo-door"}],` +
	`"fakedns":[{"ipPool":"198.18.0.0/16","poolSize":65535}]}`

// The way back to the proxy reloads xray and loads the data plane only once
// the NEW xray runs: Reload only signals the old one, which answers on its
// DNS inbound a moment longer. An xray that does not come back leaves the
// router direct, the data plane unloaded.
func TestTheWayBackWaitsForTheNewXray(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	d.supStarted = true
	// The fake "new xray" starts on its own goroutine (like the real
	// supervisor's), so its start time is shared state: guard it.
	var startMu sync.Mutex
	var newStart time.Time
	getNewStart := func() time.Time {
		startMu.Lock()
		defer startMu.Unlock()
		return newStart
	}
	d.reloadXrayFn = func(context.Context) error {
		s.reloads++
		go func() {
			time.Sleep(300 * time.Millisecond)
			startMu.Lock()
			newStart = time.Now()
			startMu.Unlock()
		}()
		return nil
	}
	d.xrayStatusFn = func() supervisor.Status {
		if at := getNewStart(); !at.IsZero() {
			return supervisor.Status{State: supervisor.StateRunning, StartedAt: at}
		}
		return supervisor.Status{State: supervisor.StateRunning, StartedAt: s.started} // the old one, still up
	}
	var appliedAt time.Time
	d.applyRuleset = func(string, firewall.Spec) error { s.applied++; appliedAt = time.Now(); return nil }

	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy}, "")
	d.applyRescueTransition(context.Background(), rescue.Decision{ShouldTransition: true, NextMode: rescue.ModeProxy})
	if s.reloads != 1 || s.applied != 1 {
		t.Fatalf("reloads %d, data plane loads %d", s.reloads, s.applied)
	}
	if newStartAt := getNewStart(); newStartAt.IsZero() || appliedAt.Before(newStartAt) {
		t.Fatalf("the data plane went in at %v, before the new xray ran (%v)", appliedAt, newStartAt)
	}
	if s.hup != 1 {
		t.Fatalf("flushes %d, want 1", s.hup)
	}

	// No new xray within the bound: direct stays, nothing loaded.
	defer func(w time.Duration) { xrayRestartWait = w }(xrayRestartWait)
	xrayRestartWait = 400 * time.Millisecond
	d.reloadXrayFn = func(context.Context) error { return nil }
	d.xrayStatusFn = func() supervisor.Status {
		return supervisor.Status{State: supervisor.StateRunning, StartedAt: s.started}
	}
	d.fwProgrammed = nil
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy}, "")
	d.applyRescueTransition(context.Background(), rescue.Decision{ShouldTransition: true, NextMode: rescue.ModeProxy})
	if s.applied != 1 || d.fwProgrammed != nil {
		t.Fatalf("xray did not come back, and the data plane was loaded (%d)", s.applied)
	}
	if st := d.rescueState(); st.Mode != rescue.ModeDirect {
		t.Fatalf("xray did not come back, and the rescue says %+v", st)
	}
}

// Every change of the resolver's path is its own flush, however close:
// in, out and in again within resolverFlushDedupe empties the cache twice.
func TestEveryReturnOfTheRedirectFlushes(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	ctx := context.Background()
	d.programFirewall(ctx, d.desired)
	d.tearDownFirewall(ctx)
	d.programFirewall(ctx, d.desired)
	if s.applied != 2 || s.hup != 2 {
		t.Fatalf("in, out, in: %d loads, %d flushes; want 2 and 2", s.applied, s.hup)
	}
}

// xray started anew while the DNS redirect hands out its FakeDNS addresses
// (the reload after a changed subscription, a location change, a crash): the
// cache is emptied once the new xray answers, once per start. The first look
// only notes the start.
func TestARestartedXrayEmptiesTheResolverCache(t *testing.T) {
	s := steerDaemon(t, renderWithDNSAndFakeDNS)
	d := s.d
	ctx := context.Background()
	d.supStarted = true
	d.programFirewall(ctx, d.desired) // the start's own flush
	if s.hup != 1 {
		t.Fatalf("flushes at the start: %d", s.hup)
	}
	d.flushAfterXrayRestart(ctx)
	if s.hup != 1 {
		t.Fatalf("the start the programming already flushed for was flushed again: %d", s.hup)
	}

	// A subscription refresh changed the render: xray reloads.
	s.started = time.Now()
	answering := false
	d.dnsAnswers = func(context.Context, int) bool { return answering }
	d.flushAfterXrayRestart(ctx)
	if s.hup != 1 {
		t.Fatal("flushed before the new xray answered")
	}
	answering = true
	d.flushAfterXrayRestart(ctx)
	d.flushAfterXrayRestart(ctx)
	if s.hup != 2 {
		t.Fatalf("a restarted xray: %d flushes, want one more", s.hup-1)
	}

	// Without FakeDNS the cached answers are real addresses: nothing to do.
	if err := vault.WriteFile(d.cfg.XrayRenderPath, []byte(renderWithDNS)); err != nil {
		t.Fatal(err)
	}
	s.started = time.Now().Add(time.Second)
	d.flushAfterXrayRestart(ctx)
	if s.hup != 2 {
		t.Fatal("flushed for a restart that handed out no FakeDNS addresses")
	}
}

// The operator's direct mode lasts until the operator reconnects: neither an
// apply nor the rescue ends it. A state from before r18 says so by its reason.
func TestTheOperatorsDirectModeOutlivesAnApply(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: time.Now().Add(-time.Hour)}, operatorDirectReason)
	d.st.Rescue.Source = rescueSourceOperator
	if d.directMayEnd(time.Now(), true) || d.directMayEnd(time.Now(), false) {
		t.Fatal("an apply may end the operator's direct mode")
	}
	key := ""
	d.fwProgrammed = &key
	d.rescueAfresh(time.Now())
	if !d.operatorDirect() {
		t.Fatalf("an apply ended the operator's direct mode: %+v", d.st.Rescue)
	}
	if d.rescueRecheckDue(time.Now()) {
		t.Fatal("the rescue looks for the way back out of the operator's direct mode")
	}
	if dec := d.rescueStep(context.Background()); dec.ShouldTransition || dec.NextMode != rescue.ModeDirect {
		t.Fatalf("the rescue left the operator's direct mode: %+v", dec)
	}

	d.st.Rescue.Source = "" // a state file from before r18
	if !d.operatorDirect() {
		t.Fatal("an r17 state's operator direct mode reads as the rescue's")
	}
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: time.Now()}, "proxy path unreachable; falling back to direct")
	if d.operatorDirect() {
		t.Fatal("the rescue's direct mode reads as the operator's")
	}

	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect}, operatorDirectReason)
	d.st.Rescue.Source = rescueSourceOperator
	d.supStarted = false
	if err := d.jobReconnect(context.Background(), controlplane.Job{ID: "reconnect"}); err != nil {
		t.Fatal(err)
	}
	if d.operatorDirect() || d.st.Rescue.Source != "" || d.rescueState().Mode != rescue.ModeProxy {
		t.Fatalf("the operator's reconnect did not end direct mode: %+v", d.st.Rescue)
	}
}

// Applies in the rescue's direct mode while the tunnel is known dead do not
// load the data plane: the LAN keeps its internet, and several in a row do
// not swing it. New nodes may be tried at once — but after a failed return
// only when the backoff allows.
func TestAppliesInDirectModeDoNotSwingTheLAN(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	var applied int
	d.applyRuleset = func(string, firewall.Spec) error { applied++; return nil }
	d.hupResolver = func(int) error { return nil }
	d.runFirewallCmd = func(context.Context, string, ...string) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer d.stopXray(context.Background())
	for i := 0; i < 2; i++ { // register, then check in and apply
		if err := d.runOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if applied != 1 {
		t.Fatalf("the first apply loaded the data plane %d times", applied)
	}

	apply := func(id, url string, fetch bool) {
		t.Helper()
		if fetch { // the provider's document anew, not the one on disk
			if err := vault.RemoveFile(d.cfg.ProviderConfigPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
		rev := controlplane.DesiredRevisionSummary{ID: id, RevisionNumber: 2, Status: "approved",
			EngineMode: controlplane.EngineModeXrayDirect, Config: operatorConfigPointingAt(t, url)}
		raw, _ := json.Marshal(rev)
		if err := d.jobApplyXrayConfig(ctx, controlplane.Job{ID: id, Type: "apply_xray_config"}, controlplane.CheckInResponse{DesiredRevision: raw}); err != nil {
			t.Fatal(err)
		}
	}
	// The rescue went direct: the data plane is down, every node dead.
	d.tearDownFirewall(ctx)
	left := time.Now()
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: left}, "proxy path unreachable; falling back to direct")
	d.publishTunnel(map[string]failover.Health{"bridge-pl5": {}}, time.Now(), tunnelLook{}, []string{"bridge-pl5"})

	for i := 0; i < 3; i++ { // the same nodes, applied again and again
		apply(fmt.Sprintf("same-%d", i), fmt.Sprintf("%s/sub?n=%d", provider.URL, i), i == 0)
		if applied != 1 || d.rescueState().Mode != rescue.ModeDirect {
			t.Fatalf("apply %d of the same dead nodes: data plane loads %d, mode %s", i, applied, d.rescueState().Mode)
		}
	}

	// Other nodes, after a failed return: the backoff holds them.
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: left, FailedRetries: 1}, "")
	provider.mu.Lock()
	provider.entries = bytes.ReplaceAll(provider.entries, []byte("ru12.provider.invalid"), []byte("ru13.provider.invalid"))
	provider.mu.Unlock()
	before := d.renderNodesKey()
	apply("new-nodes-held", provider.URL+"/sub?n=held", true)
	if d.renderNodesKey() == before {
		t.Fatal("the test's new nodes did not reach the render")
	}
	if applied != 1 || d.rescueState().Mode != rescue.ModeDirect {
		t.Fatalf("new nodes within the backoff: data plane loads %d, mode %s", applied, d.rescueState().Mode)
	}

	// Other nodes, no failed return yet: tried at once.
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: left}, "")
	provider.mu.Lock()
	provider.entries = bytes.ReplaceAll(provider.entries, []byte("ru13.provider.invalid"), []byte("ru14.provider.invalid"))
	provider.mu.Unlock()
	apply("new-nodes", provider.URL+"/sub?n=new", true)
	if applied != 2 || d.rescueState().Mode != rescue.ModeProxy {
		t.Fatalf("new nodes: data plane loads %d, mode %s", applied, d.rescueState().Mode)
	}
}

// The WAN's resolvers changing under a redirect already in force empties the
// resolver's cache once: a resolver the redirect did not take until now may
// have answered over the open path. The same resolvers again do not.
func TestNewWANResolversEmptyTheResolverCache(t *testing.T) {
	d, _, _ := shutdownDaemon(t)
	if err := vault.WriteFile(d.cfg.XrayRenderPath, []byte(renderWithDNS)); err != nil {
		t.Fatal(err)
	}
	d.procDir = fakeProc(t, dnsmasqAs453)
	d.rootDir = t.TempDir()
	d.dnsAnswers = func(context.Context, int) bool { return true }
	d.ownsAddr = func(ip net.IP) bool { return ip.Equal(net.IPv4(192, 168, 1, 1)) }
	d.applyRuleset = func(string, firewall.Spec) error { return nil }
	var hup int
	d.hupResolver = func(int) error { hup++; return nil }

	withWANResolver(t, d, "77.37.1.2")
	d.reapplyFirewall(context.Background())
	if d.fwProgrammed == nil || hup != 1 {
		t.Fatalf("first redirect: programmed %v, flushes %d", d.fwProgrammed, hup)
	}
	d.flushedAt = time.Time{}
	d.reapplyFirewall(context.Background())
	if hup != 1 {
		t.Fatalf("the same resolvers again emptied the cache: %d", hup)
	}
	withWANResolver(t, d, "192.168.1.254")
	d.reapplyFirewall(context.Background())
	if hup != 2 || redirectUpstreams(*d.fwProgrammed) != "192.168.1.254" {
		t.Fatalf("new WAN resolvers: flushes %d, redirect %q", hup, *d.fwProgrammed)
	}
}

// 1111, 2026-10-05, drill dr2 (xray killed every 3 s): the DNS watch took the
// redirect out of the table within seconds, and yet the LAN resolved nothing
// for ~40 s more. The resolver's flows to the WAN's resolvers kept the
// redirect's NAT — answered from 127.0.0.1:10053, three minutes from their
// last packet — and dnsmasq reuses its source ports. Taking the redirect out
// forgets those flows before the cache is emptied; putting it back forgets
// the router's own flows that went out unredirected; unloading the data
// plane forgets the redirected ones too.
func TestTheRedirectsFlowsLeaveWithIt(t *testing.T) {
	d, _, _ := shutdownDaemon(t)
	if err := vault.WriteFile(d.cfg.XrayRenderPath, []byte(renderWithDNS)); err != nil {
		t.Fatal(err)
	}
	d.procDir = fakeProc(t, dnsmasqAs453)
	d.rootDir = t.TempDir()
	answering := true
	d.dnsAnswers = func(context.Context, int) bool { return answering }
	d.ownsAddr = func(ip net.IP) bool { return ip.Equal(net.IPv4(192, 168, 0, 2)) }
	d.applyRuleset = func(string, firewall.Spec) error { return nil }
	var events []string
	d.hupResolver = func(int) error { events = append(events, "flush"); return nil }
	withWANResolver(t, d, "77.37.251.33")

	const table = `ipv4     2 udp      17 178 src=192.168.0.2 dst=77.37.251.33 sport=41234 dport=53 src=127.0.0.1 dst=192.168.0.2 sport=10053 dport=41234 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 178 src=192.168.0.2 dst=77.37.255.30 sport=41235 dport=53 src=127.0.0.1 dst=192.168.0.2 sport=10053 dport=41235 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 170 src=192.168.0.2 dst=77.37.255.30 sport=41300 dport=53 src=77.37.255.30 dst=192.168.0.2 sport=53 dport=41300 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 170 src=192.168.1.141 dst=8.8.8.8 sport=5353 dport=53 src=192.168.1.1 dst=192.168.1.141 sport=53 dport=5353 [ASSURED] mark=0 zone=0 use=2`
	prevRead, prevForget := dnsFlowsRead, dnsFlowsForget
	t.Cleanup(func() { dnsFlowsRead, dnsFlowsForget = prevRead, prevForget })
	dnsFlowsRead = func() ([]conntrack.Entry, error) { return conntrack.Parse(strings.NewReader(table)), nil }
	var forgotten [][]uint16
	dnsFlowsForget = func(es []conntrack.Entry) (int, error) {
		var ports []uint16
		for _, e := range es {
			ports = append(ports, e.SPort)
		}
		forgotten = append(forgotten, ports)
		events = append(events, "forget")
		return len(es), nil
	}
	ctx := context.Background()

	// In: the router's own unredirected DNS flow is forgotten, then the cache
	// emptied.
	d.reapplyFirewall(ctx)
	if _, in := redirectPort(*d.fwProgrammed); !in {
		t.Fatalf("redirect not in: %q", *d.fwProgrammed)
	}
	if !reflect.DeepEqual(forgotten, [][]uint16{{41300}}) || !reflect.DeepEqual(events, []string{"forget", "flush"}) {
		t.Fatalf("redirect in: forgotten %v, events %v", forgotten, events)
	}

	// xray stops answering; the DNS watch takes the redirect out, as the
	// loop does (waitForTick): the redirected flows go before the flush.
	answering = false
	forgotten, events = nil, nil
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		t.Fatal(err)
	}
	d.programFirewallWithin(ctx, cfg, 0)
	d.flushResolverCache("xray stopped answering on its DNS inbound")
	if _, in := redirectPort(*d.fwProgrammed); in {
		t.Fatalf("redirect still in: %q", *d.fwProgrammed)
	}
	if !reflect.DeepEqual(forgotten, [][]uint16{{41234, 41235}}) || !reflect.DeepEqual(events, []string{"forget", "flush"}) {
		t.Fatalf("redirect out: forgotten %v, events %v; want the two flows into 10053 forgotten, then the flush", forgotten, events)
	}

	// The same table again: nothing to forget.
	forgotten = nil
	d.programFirewallWithin(ctx, cfg, 0)
	if forgotten != nil {
		t.Fatalf("no change of the redirect, yet flows forgotten: %v", forgotten)
	}

	// Back in, then the data plane unloaded: the redirected flows go with it.
	answering = true
	d.reapplyFirewall(ctx)
	forgotten = nil
	d.unloadDataPlane(ctx, cfg)
	if !reflect.DeepEqual(forgotten, [][]uint16{{41234, 41235}}) {
		t.Fatalf("data plane unloaded: forgotten %v", forgotten)
	}
}
