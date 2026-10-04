package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/failover"
	"vectra-controller-pro/internal/rescue"
)

// toServer sends every request to srv: what the control plane's marked path
// reaches while the tunnel does not.
type toServer struct{ srv *httptest.Server }

func (s toServer) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(s.srv.URL)
	r2 := r.Clone(r.Context())
	r2.URL.Scheme, r2.URL.Host, r2.Host = u.Scheme, u.Host, u.Host
	return http.DefaultTransport.RoundTrip(r2)
}

// The tunnel dead, the line alive: the rescue leaves proxy mode by itself.
// Its "can we go direct" was the tunnel's own answer again, false exactly
// when the tunnel was dead, so this never happened.
func TestRescueGoesDirectWhenOnlyTheTunnelIsDead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	d := confirmDaemon(t, srv.URL, filepath.Join(t.TempDir(), "fw-confirm"), &http.Client{Transport: toServer{srv}})
	// Only the marked client reaches it; the tunnel's plain client cannot
	// even resolve the name.
	d.rescuePolicy.HealthURLs = []string{"http://only-around-the-tunnel.invalid/generate_204"}
	d.rescuePolicy.TraceURLs = []string{"http://only-around-the-tunnel.invalid/cdn-cgi/trace"}
	d.rescuePolicy.Cooldown = 0
	d.rescuePolicy.MinFailSpan = 0
	d.client = controlplane.NewClient(controlplane.Options{BaseURL: srv.URL, HTTPClient: &http.Client{Transport: toServer{srv}}})

	inv := controlplane.RouterInventory{}
	var last rescue.Decision
	for i := 0; i < d.rescuePolicy.TriggerFailureCount; i++ {
		_, last = d.evaluateHealth(context.Background(), &inv)
	}
	if !last.ShouldTransition || last.NextMode != rescue.ModeDirect {
		t.Fatalf("after %d dead-tunnel loops with the line up: %+v", d.rescuePolicy.TriggerFailureCount, last)
	}
}

// The tunnel dead AND the line dead: nothing to gain by going direct.
func TestRescueStaysWhenTheLineIsDeadToo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer srv.Close()
	d := confirmDaemon(t, srv.URL, filepath.Join(t.TempDir(), "fw-confirm"), &http.Client{Transport: toServer{srv}})
	d.rescuePolicy.HealthURLs = []string{"http://only-around-the-tunnel.invalid/generate_204"}
	d.rescuePolicy.TraceURLs = []string{"http://only-around-the-tunnel.invalid/cdn-cgi/trace"}
	d.rescuePolicy.Cooldown = 0
	d.client = controlplane.NewClient(controlplane.Options{BaseURL: srv.URL, HTTPClient: &http.Client{Transport: toServer{srv}}})

	inv := controlplane.RouterInventory{}
	for i := 0; i < d.rescuePolicy.TriggerFailureCount+1; i++ {
		if _, dec := d.evaluateHealth(context.Background(), &inv); dec.ShouldTransition {
			t.Fatalf("went %s with the line dead too: %+v", dec.NextMode, dec)
		}
	}
}

// Under the kill switch the rescue never opens the router: fail closed.
func TestRescueNeverGoesDirectUnderTheKillSwitch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	d := confirmDaemon(t, srv.URL, filepath.Join(t.TempDir(), "fw-confirm"), &http.Client{Transport: toServer{srv}})
	d.rescuePolicy.HealthURLs = []string{"http://only-around-the-tunnel.invalid/generate_204"}
	d.rescuePolicy.TraceURLs = []string{"http://only-around-the-tunnel.invalid/cdn-cgi/trace"}
	d.rescuePolicy.Cooldown = 0
	d.client = controlplane.NewClient(controlplane.Options{BaseURL: srv.URL, HTTPClient: &http.Client{Transport: toServer{srv}}})
	d.desired = &config.Config{Inbounds: config.Inbounds{Tproxy: &config.TproxyInbound{Port: 12345, FwMark: 1, KillSwitch: true}}}

	inv := controlplane.RouterInventory{}
	for i := 0; i < d.rescuePolicy.TriggerFailureCount+2; i++ {
		if _, dec := d.evaluateHealth(context.Background(), &inv); dec.ShouldTransition {
			t.Fatalf("the kill switch is armed and the rescue went %s: %+v", dec.NextMode, dec)
		}
	}
}

// A start in direct mode leaves the data plane down: the mode was chosen on
// purpose and survives the restart; loading the table would run the LAN into
// the dead tunnel while the router says "direct".
func TestAStartInDirectModeLoadsNothing(t *testing.T) {
	dir := t.TempDir()
	confirm := filepath.Join(dir, "fw-confirm")
	d := confirmDaemon(t, "http://127.0.0.1:1", confirm, nil)
	d.desired = &config.Config{Inbounds: config.Inbounds{Tproxy: &config.TproxyInbound{Port: 12345, FwMark: 1}}}
	d.dnsAnswers = func(context.Context, int) bool { return false }

	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect}, "")
	if err := os.WriteFile(confirm, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	d.restoreDataPlane(context.Background())
	if _, err := os.Stat(confirm); err != nil {
		t.Fatal("direct mode, and the firewall was programmed anyway (the apply cleared the confirm sentinel)")
	}

	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy}, "")
	d.restoreDataPlane(context.Background())
	if _, err := os.Stat(confirm); err == nil {
		t.Fatal("proxy mode, and the firewall was not programmed")
	}
}

// Between the polls the rescue looks again only while it is unsure: after a
// failed probe through the tunnel, or in direct mode once the cooldown allows
// the way back and a node lives — never under the kill switch.
func TestTheRescueRechecksBetweenPollsOnlyWhenUnsure(t *testing.T) {
	d := &daemon{rescuePolicy: rescue.DefaultPolicy()}
	now := time.Now()
	if d.rescueRecheckDue(now) {
		t.Fatal("rechecked a healthy proxy between the polls")
	}
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy, ProxyFailureCount: 1}, "")
	if !d.rescueRecheckDue(now) {
		t.Fatal("a probe through the tunnel failed and nothing looks again before the next poll")
	}
	d.rescueCheckedAt = now
	if d.rescueRecheckDue(now.Add(rescueFailingEvery / 2)) {
		t.Fatal("looked again sooner than rescueFailingEvery")
	}
	if !d.rescueRecheckDue(now.Add(rescueFailingEvery)) {
		t.Fatal("a probe failed and the next waits longer than rescueFailingEvery")
	}

	d.rescueCheckedAt = time.Time{}
	d.storeRescueState(rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: now}, "")
	if d.rescueRecheckDue(now) {
		t.Fatal("direct mode looks for the way back within the cooldown")
	}
	later := now.Add(d.rescuePolicy.Cooldown + time.Second)
	if !d.rescueRecheckDue(later) {
		t.Fatal("the cooldown is over and nothing says the tunnel is dead, yet no recheck")
	}
	d.tunnel.Store(&tunnelLook{Dead: true, At: later})
	if d.rescueRecheckDue(later) {
		t.Fatal("every node is dead and the rescue still looks for the way back")
	}
	if !d.rescueRecheckDue(later.Add(2 * time.Minute)) {
		t.Fatal("an old word of the observatory still holds the way back")
	}

	d.desired = &config.Config{Inbounds: config.Inbounds{Tproxy: &config.TproxyInbound{Port: 12345, FwMark: 1, KillSwitch: true}}}
	d.storeRescueState(rescue.State{Mode: rescue.ModeProxy, ProxyFailureCount: 2}, "")
	if d.rescueRecheckDue(later) {
		t.Fatal("rechecked under the kill switch")
	}
}

// The observatory's word: dead only when no node of the main balancer, its
// reserve or the borrow pool is alive; and once it says when its nodes last
// answered, only a node that answered after the router left counts.
func TestTheTunnelIsDeadOnlyWithEveryNodeDead(t *testing.T) {
	d := &daemon{}
	now := time.Now()
	if d.tunnelDead(now, time.Time{}) {
		t.Fatal("dead with no word at all")
	}
	health := map[string]failover.Health{"a": {}, "b": {}, "c": {Alive: true}}
	d.publishTunnel(health, now, tunnelLook{}, []string{"a"}, []string{"b"}, []string{"c"})
	if d.tunnelDead(now, time.Time{}) {
		t.Fatal("a borrowable node lives, and yet dead")
	}
	d.publishTunnel(health, now, tunnelLook{}, []string{"a"}, []string{"b"})
	if !d.tunnelDead(now, time.Time{}) {
		t.Fatal("every node the main traffic can take is dead, and yet alive")
	}
	d.publishTunnel(map[string]failover.Health{"a": {Alive: true}}, now, tunnelLook{MainDown: true}, []string{"a"})
	if !d.tunnelDead(now, time.Time{}) || !d.tunnelFailing(now) {
		t.Fatal("the watchdog holds the main balancer down, and yet the tunnel lives")
	}
}

// "Alive" from a round before the outage is not a way back: a node must have
// answered after the router went direct (1111, 2026-10-04).
func TestOnlyAnAnswerAfterLeavingIsAWayBack(t *testing.T) {
	d := &daemon{}
	now := time.Now()
	left := now.Add(-2 * time.Minute)
	stale := map[string]failover.Health{"a": {Alive: true, LastSeen: left.Add(-time.Minute)}}
	d.publishTunnel(stale, now, tunnelLook{}, []string{"a"})
	if !d.tunnelDead(now, left) {
		t.Fatal("an answer from before the outage took the router back")
	}
	fresh := map[string]failover.Health{"a": {Alive: true, LastSeen: now.Add(-10 * time.Second)}, "b": {LastSeen: now}}
	d.publishTunnel(fresh, now, tunnelLook{}, []string{"a", "b"})
	if d.tunnelDead(now, left) {
		t.Fatal("a node answered after the router left, and yet no way back")
	}
}

// Nodes the observatory does not watch say nothing: with none of them watched
// there is no word, not a dead tunnel.
func TestUnobservedNodesAreNoWord(t *testing.T) {
	d := &daemon{}
	now := time.Now()
	d.publishTunnel(map[string]failover.Health{"other": {}}, now, tunnelLook{}, []string{"a"}, []string{"b"})
	if d.tunnelDead(now, time.Time{}) {
		t.Fatal("nothing it names is watched, and yet the tunnel is dead")
	}
}

// The connection table's word: a node xray dialled and that answered after
// the router left is a way back; none since, no way back — until the blind
// retry after blindRetryAfter.
func TestTheConnectionTableIsAWayBack(t *testing.T) {
	d := &daemon{}
	now := time.Now()
	left := now.Add(-3 * time.Minute)
	health := map[string]failover.Health{"a": {Alive: true}}
	d.publishTunnel(health, now, tunnelLook{Watching: true, Answered: left.Add(-time.Minute)}, []string{"a"})
	if !d.tunnelDead(now, left) {
		t.Fatal("no node answered since the router left, and yet a way back")
	}
	d.publishTunnel(health, now, tunnelLook{Watching: true, Answered: now.Add(-5 * time.Second)}, []string{"a"})
	if d.tunnelDead(now, left) {
		t.Fatal("a node answered since the router left, and yet no way back")
	}
	d.publishTunnel(health, now, tunnelLook{Watching: true}, []string{"a"})
	if d.tunnelDead(now, now.Add(-blindRetryAfter)) {
		t.Fatal("no blind try after blindRetryAfter")
	}
}
