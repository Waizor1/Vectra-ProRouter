package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/rescue"
)

// panelGone is the control plane's transport with the panel unreachable (its
// host refuses, as a blocked address or a VPS that is down does) and the
// rest of the internet there: what the router's marked sockets meet when
// only the panel is lost. Requests around the tunnel are marked so the trace
// server can answer them from the router's own WAN address.
type panelGone struct {
	panelHost string
	panelHits *int64
}

func (p panelGone) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == p.panelHost {
		atomic.AddInt64(p.panelHits, 1)
		return nil, errors.New("dial tcp: connect: connection refused")
	}
	r2 := r.Clone(r.Context())
	r2.Header.Set("X-Around", "1")
	return http.DefaultTransport.RoundTrip(r2)
}

// internetUp answers the rescue's health URLs (204) and its trace URLs
// (the tunnel's exit address through the tunnel, the WAN's around it). down
// makes it refuse everything: the internet gone.
func internetUp(t *testing.T, down *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down != nil && down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/cdn-cgi/trace" {
			if r.Header.Get("X-Around") != "" {
				_, _ = w.Write([]byte("ip=192.0.2.10\n"))
			} else {
				_, _ = w.Write([]byte("ip=198.51.100.7\n"))
			}
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withoutPanel points d at a panel that is gone and an internet that is
// there; returns the counter of requests the panel would have received.
func withoutPanel(t *testing.T, d *daemon, net *httptest.Server) *int64 {
	t.Helper()
	hits := new(int64)
	const panelURL = "http://panel.invalid"
	d.cfg.ControlURL = panelURL
	d.client = controlplane.NewClient(controlplane.Options{
		BaseURL:    panelURL,
		HTTPClient: &http.Client{Timeout: 2 * time.Second, Transport: panelGone{panelHost: "panel.invalid", panelHits: hits}},
	})
	d.rescuePolicy.HealthURLs = []string{net.URL + "/generate_204"}
	d.rescuePolicy.TraceURLs = []string{net.URL + "/cdn-cgi/trace"}
	return hits
}

// THE FINDING. A ruleset loaded while the panel is unreachable is confirmed
// by the router's own proof that it still reaches the internet — the probe
// on the control plane's marked client — and the panel is not needed.
func TestLocalProofConfirmsTheFirewallWithoutThePanel(t *testing.T) {
	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d := confirmDaemon(t, "http://127.0.0.1:1", confirmPath, &http.Client{})
	withoutPanel(t, d, internetUp(t, nil))

	if !d.confirmFirewall(context.Background()) {
		t.Fatal("the router reaches the internet and the ruleset was not confirmed: the deadman reverts it without the panel")
	}
	if !fileExists(confirmPath) {
		t.Fatal("no confirm sentinel written")
	}
	if !d.fwConfirmBy.IsZero() {
		t.Error("a confirmed ruleset still has tries pending")
	}
}

// No proof at all — the internet gone, the panel too — leaves the deadman
// armed: tried every fwConfirmEvery until shortly before it wakes, then no
// more. A ruleset that cut the router off is still reverted.
func TestNoProofLeavesTheDeadmanArmed(t *testing.T) {
	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d := confirmDaemon(t, "http://127.0.0.1:1", confirmPath, &http.Client{})
	var down atomic.Bool
	down.Store(true)
	withoutPanel(t, d, internetUp(t, &down))
	t0 := time.Date(2026, 10, 6, 4, 31, 0, 0, time.UTC)
	now := t0
	d.clock = func() time.Time { return now }

	if d.confirmFirewall(context.Background()) {
		t.Fatal("confirmed with no proof")
	}
	tries := 1
	for now = t0.Add(time.Second); now.Before(t0.Add(2 * time.Minute)); now = now.Add(time.Second) {
		before := d.fwConfirmNext
		if d.retryFirewallConfirm(context.Background(), now) {
			t.Fatalf("confirmed with no proof at +%s", now.Sub(t0))
		}
		if d.fwConfirmNext != before {
			tries++
		}
	}
	if fileExists(confirmPath) {
		t.Fatal("a sentinel was written with no proof: the deadman would keep a ruleset that cut the router off")
	}
	if want := int((d.confirmer.Timeout-fwConfirmMargin)/fwConfirmEvery) + 1; tries != want {
		t.Errorf("tries = %d, want %d (every %s until %s before the deadman)", tries, want, fwConfirmEvery, fwConfirmMargin)
	}
	if !d.fwConfirmBy.IsZero() {
		t.Error("tries still pending after the deadman's time")
	}

	// The internet back within the window: the next try confirms.
	down.Store(false)
	now = t0.Add(time.Hour)
	if !d.confirmFirewall(context.Background()) {
		t.Fatal("the internet is back and the ruleset was not confirmed")
	}
}

// A panel apply may still be confirmed by the panel alone (a WAN where only
// it answers), and the panel is not asked while it is known to be down: a
// blackholed panel would cost its timeout every try.
func TestThePanelStillConfirmsButIsNotWaitedFor(t *testing.T) {
	var hits int64
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer panel.Close()
	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d := confirmDaemon(t, panel.URL, confirmPath, &http.Client{Timeout: 2 * time.Second})
	d.rescuePolicy.HealthURLs = nil

	down := false
	d.lastControlPlaneOK = &down
	if d.confirmFirewall(context.Background()) || atomic.LoadInt64(&hits) != 0 {
		t.Fatalf("the panel was asked while known down (%d requests)", hits)
	}
	up := true
	d.lastControlPlaneOK = &up
	if !d.confirmFirewall(context.Background()) {
		t.Fatal("the panel answered and did not confirm")
	}
}

// The daemon with the panel gone for hours (a fake clock): the ruleset it
// loaded at the start is confirmed by the router itself, the deadman never
// reverts it, nothing loads it again or takes it down, the rescue keeps the
// proxy.
func TestHoursWithoutThePanel(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	withoutPanel(t, d, internetUp(t, nil))
	d.supStarted = true // xray runs (xrayStatusFn)

	t0 := time.Date(2026, 10, 6, 4, 31, 0, 0, time.UTC) // just after the nightly reboot
	now := t0
	d.clock = func() time.Time { return now }

	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d.confirmer = firewall.NewCommitConfirmer(confirmPath, 90*time.Second)
	// The deadman stood in for: armed by every apply, it reverts at its
	// timeout unless the sentinel is there by then.
	var armedAt time.Time
	armed, loaded, applies, reverts, teardowns := false, false, 0, 0, 0
	d.applyRuleset = func(string, firewall.Spec) error {
		_ = os.Remove(confirmPath)
		armedAt, armed, loaded = now, true, true
		applies++
		return nil
	}
	d.tableLoaded = func(context.Context, string) bool { return loaded }
	d.runFirewallCmd = func(context.Context, string, ...string) error { teardowns++; return nil }
	deadman := func() {
		if armed && !now.Before(armedAt.Add(d.confirmer.Timeout)) {
			armed = false
			if !fileExists(confirmPath) {
				loaded = false
				reverts++
			}
			_ = os.Remove(confirmPath)
		}
	}

	ctx := context.Background()
	d.restoreDataPlane(ctx) // the start after the reboot
	if applies != 1 {
		t.Fatalf("the start loaded the data plane %d times", applies)
	}

	const hours = 6
	poll := d.cfg.PollInterval()
	nextPoll := t0
	for ; now.Before(t0.Add(hours * time.Hour)); now = now.Add(dnsWatchEvery) {
		deadman()
		if !now.Before(nextPoll) {
			// The loop: runOnce (the rescue, a check-in when due), then
			// what follows it in run().
			_ = d.runOnce(ctx)
			d.ensureDataPlane(ctx)
			d.retryFirewallConfirm(ctx, now)
			nextPoll = now.Add(poll)
		}
		// The DNS watch's tick (waitForTick).
		d.retryFirewallConfirm(ctx, now)
	}

	if reverts != 0 {
		t.Errorf("the deadman reverted the ruleset %d times in %d h without the panel", reverts, hours)
	}
	if applies != 1 {
		t.Errorf("the data plane was loaded %d times: it flapped", applies)
	}
	if teardowns != 0 {
		t.Errorf("%d teardown commands ran", teardowns)
	}
	if !loaded || d.fwProgrammed == nil {
		t.Error("the data plane is not loaded at the end")
	}
	if m := d.rescueState().Mode; m != rescue.ModeProxy {
		t.Errorf("rescue mode %q, want proxy: the tunnel and the internet worked throughout", m)
	}
}

// The other side of the same deadman: a ruleset after which the router can
// prove nothing — the internet and the panel both gone — is still reverted.
func TestTheDeadmanStillRevertsWithoutAnyProof(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	var down atomic.Bool
	down.Store(true)
	withoutPanel(t, d, internetUp(t, &down))
	d.supStarted = true
	t0 := time.Date(2026, 10, 6, 4, 31, 0, 0, time.UTC)
	now := t0
	d.clock = func() time.Time { return now }
	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d.confirmer = firewall.NewCommitConfirmer(confirmPath, 90*time.Second)
	d.applyRuleset = func(string, firewall.Spec) error { _ = os.Remove(confirmPath); return nil }

	d.restoreDataPlane(context.Background())
	for now = t0; now.Before(t0.Add(90 * time.Second)); now = now.Add(dnsWatchEvery) {
		d.retryFirewallConfirm(context.Background(), now)
	}
	if fileExists(confirmPath) {
		t.Fatal("confirmed without any proof: a ruleset that cut the router off would stay")
	}
}
