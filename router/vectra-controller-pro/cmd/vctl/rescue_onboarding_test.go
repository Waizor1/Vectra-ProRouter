package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/rescue"
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
