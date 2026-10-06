package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
	panelHits *int64 // check-ins and registrations it refused
	back      *atomic.Bool
	halfUp    *atomic.Bool // its health answers, its check-in does not
}

func (p panelGone) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == p.panelHost && p.halfUp != nil && p.halfUp.Load() {
		if strings.HasPrefix(r.URL.Path, "/api/router/") {
			atomic.AddInt64(p.panelHits, 1)
			return nil, errors.New("read: connection reset by peer")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
	}
	if r.URL.Host == p.panelHost {
		if p.back != nil && p.back.Load() {
			if strings.HasPrefix(r.URL.Path, "/api/router/") {
				atomic.AddInt64(p.panelHits, 1)
			}
			// Back, and with nothing to say: an empty answer.
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"status":"ok"}`)), Request: r}, nil
		}
		if strings.HasPrefix(r.URL.Path, "/api/router/") {
			atomic.AddInt64(p.panelHits, 1)
		}
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
	hits, _ := withoutPanelUntil(t, d, net)
	return hits
}

// withoutPanelUntil is withoutPanel with a switch that brings the panel back.
func withoutPanelUntil(t *testing.T, d *daemon, net *httptest.Server) (*int64, *atomic.Bool) {
	t.Helper()
	p := withoutPanelSwitches(t, d, net)
	return p.panelHits, p.back
}

// withoutPanelSwitches is withoutPanel with its switches: the panel back,
// or half up.
func withoutPanelSwitches(t *testing.T, d *daemon, net *httptest.Server) panelGone {
	t.Helper()
	p := panelGone{panelHost: "panel.invalid", panelHits: new(int64), back: new(atomic.Bool), halfUp: new(atomic.Bool)}
	const panelURL = "http://panel.invalid"
	d.cfg.ControlURL = panelURL
	d.client = controlplane.NewClient(controlplane.Options{
		BaseURL:    panelURL,
		HTTPClient: &http.Client{Timeout: 2 * time.Second, Transport: p},
	})
	d.rescuePolicy.HealthURLs = []string{net.URL + "/generate_204"}
	d.rescuePolicy.TraceURLs = []string{net.URL + "/cdn-cgi/trace"}
	return p
}

// THE FINDING. A ruleset loaded while the panel is unreachable is confirmed
// by the router's own proof that it still reaches the internet — the probe
// on the control plane's marked client — and the panel is not needed.
func TestLocalProofConfirmsTheFirewallWithoutThePanel(t *testing.T) {
	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d := confirmDaemon(t, "http://127.0.0.1:1", confirmPath, &http.Client{})
	withoutPanel(t, d, internetUp(t, nil))

	if !d.confirmFirewall(context.Background(), d.now()) {
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

	if d.confirmFirewall(context.Background(), d.now()) {
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
	if want := int((d.confirmer.Timeout-fwConfirmMargin-fwConfirmMaxTry)/fwConfirmEvery) + 1; tries != want {
		t.Errorf("tries = %d, want %d (every %s, the last starting %s before the deadman)", tries, want, fwConfirmEvery, fwConfirmMargin+fwConfirmMaxTry)
	}
	if !d.fwConfirmBy.IsZero() {
		t.Error("tries still pending after the deadman's time")
	}

	// The internet back within the window: the next try confirms.
	down.Store(false)
	now = t0.Add(time.Hour)
	if !d.confirmFirewall(context.Background(), d.now()) {
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
	if d.confirmFirewall(context.Background(), d.now()) || atomic.LoadInt64(&hits) != 0 {
		t.Fatalf("the panel was asked while known down (%d requests)", hits)
	}
	up := true
	d.lastControlPlaneOK = &up
	if !d.confirmFirewall(context.Background(), d.now()) {
		t.Fatal("the panel answered and did not confirm")
	}
}

// The check-in's backoff: none after one failure (the next poll asks
// again), then doubling from two polls, at most five minutes, drawn between
// half and the whole.
func TestCheckInBackoff(t *testing.T) {
	poll := 45 * time.Second
	for _, c := range []struct {
		n      int
		lo, hi time.Duration
	}{
		{0, 0, 0},
		{1, 0, 0},
		{2, poll, 2 * poll},
		{3, 2 * poll, 4 * poll},
		{4, 150 * time.Second, checkInMaxBackoff},
		{40, 150 * time.Second, checkInMaxBackoff},
	} {
		lo := checkInBackoff(poll, c.n, func() float64 { return 0 })
		hi := checkInBackoff(poll, c.n, func() float64 { return 0.999999 })
		if lo != c.lo || hi < c.hi-time.Second || hi > c.hi {
			if c.hi == 0 && hi == 0 && lo == 0 {
				continue
			}
			t.Errorf("failures %d: [%s, %s], want [%s, %s]", c.n, lo, hi, c.lo, c.hi)
		}
	}
}

// The daemon with the panel gone for hours (a fake clock): the ruleset it
// loaded at the start is confirmed by the router itself, the deadman never
// reverts it, nothing loads it again or takes it down, the rescue keeps the
// proxy, and the check-ins back off instead of hammering the panel.
func TestHoursWithoutThePanel(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	hits := withoutPanel(t, d, internetUp(t, nil))
	d.supStarted = true // xray runs (xrayStatusFn)
	fakeClientChain(t, d)

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
	polls := 0
	for ; now.Before(t0.Add(hours * time.Hour)); now = now.Add(dnsWatchEvery) {
		deadman()
		if !now.Before(nextPoll) {
			// The loop: runOnce (the rescue, a check-in when due), then
			// what follows it in run().
			_ = d.runOnce(ctx)
			d.ensureDataPlane(ctx)
			d.retryFirewallConfirm(ctx, now)
			nextPoll = now.Add(poll)
			polls++
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
	// Without backoff every poll would ask: polls of them. With it, after
	// the first few, one in about five minutes at most.
	maxAsks := int64(8 + hours*int(time.Hour/(checkInMaxBackoff/2)))
	if h := atomic.LoadInt64(hits); h == 0 || h > maxAsks || h >= int64(polls) {
		t.Errorf("the panel was asked %d times in %d polls; want backoff (at most %d)", h, polls, maxAsks)
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
	fakeClientChain(t, d)
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

// clientChain stands in for the kernel's view of the LAN clients' path (nft,
// ip, /proc/net/tcp, the counters), whole unless a field breaks it.
type clientChain struct {
	refresh                                 func()
	noTproxy, noRule, noRoute, notListening bool
	noRule6                                 bool
	escaping, dropping                      bool // the counter climbs
	escaped, drops                          int64
}

func fakeClientChain(t *testing.T, d *daemon) *clientChain {
	t.Helper()
	old := chainWindow
	chainWindow = time.Millisecond
	t.Cleanup(func() { chainWindow = old })
	spec, ok := firewallSpecFromConfig(d.desired)
	if !ok {
		t.Fatal("no tproxy inbound in the test config")
	}
	c := &clientChain{}
	d.nftOutput = func(_ context.Context, args ...string) ([]byte, error) {
		rule := fmt.Sprintf("\t\tmeta l4proto { tcp, udp } counter name \"vctl_tproxy_hits\" tproxy to :%d meta mark set 0x%08x accept\n", spec.TproxyPort, spec.FwMark)
		if c.noTproxy {
			rule = ""
		}
		return []byte("table inet vctl {\n\tchain prerouting {\n\t\ttype filter hook prerouting priority mangle; policy accept;\n" + rule + "\t}\n}\n"), nil
	}
	d.ipOutput = func(_ context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "rule show":
			out := "0:\tfrom all lookup local\n32766:\tfrom all lookup main\n"
			if !c.noRule {
				out = fmt.Sprintf("0:\tfrom all lookup local\n100:\tfrom all fwmark 0x%x lookup %d\n32766:\tfrom all lookup main\n", spec.FwMark, spec.RtTable)
			}
			return []byte(out), nil
		case fmt.Sprintf("route show table %d", spec.RtTable):
			if c.noRoute {
				return nil, nil
			}
			return []byte("local default dev lo scope host\n"), nil
		case "-6 rule show":
			if c.noRule6 {
				return []byte("0:\tfrom all lookup local\n"), nil
			}
			return []byte(fmt.Sprintf("100:\tfrom all fwmark 0x%x lookup %d\n", spec.FwMark, spec.RtTable)), nil
		case fmt.Sprintf("-6 route show table %d", spec.RtTable):
			return []byte("local default dev lo metric 1024 pref medium\n"), nil
		}
		return nil, fmt.Errorf("unexpected ip %v", args)
	}
	if d.procDir == "" {
		d.procDir = t.TempDir()
	}
	_ = os.MkdirAll(filepath.Join(d.procDir, "net"), 0o755)
	tcp := "  sl  local_address rem_address   st\n"
	tcp += fmt.Sprintf("   0: 0100007F:0035 00000000:0000 0A\n   1: 00000000:%04X 00000000:0000 0A\n", spec.TproxyPort)
	d.readCounters = func(context.Context) (map[string]int64, bool) {
		if c.escaping {
			c.escaped += 3
		}
		if c.dropping {
			c.drops += 5
		}
		return map[string]int64{"vctl_tproxy_escaped": c.escaped, "vctl_killswitch_drops": c.drops}, true
	}
	write := func() {
		body := tcp
		if c.notListening {
			body = "  sl  local_address rem_address   st\n   0: 0100007F:0035 00000000:0000 0A\n"
		}
		_ = os.WriteFile(filepath.Join(d.procDir, "net", "tcp"), []byte(body), 0o644)
	}
	write()
	c.refresh = write
	return c
}

// A ruleset that breaks the LAN clients' path — whatever the router's own
// marked sockets say, and the panel too — is not confirmed, and the deadman
// reverts it. A whole path with dead nodes (no tunnel answer) is confirmed:
// the tunnel is the rescue's business, not the deadman's.
func TestABrokenClientChainIsNotConfirmed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		break_ func(c *clientChain)
		ks     bool
	}{
		{"whole", func(*clientChain) {}, false},
		{"no TPROXY rule in prerouting", func(c *clientChain) { c.noTproxy = true }, false},
		{"no fwmark policy rule", func(c *clientChain) { c.noRule = true }, false},
		{"no local route", func(c *clientChain) { c.noRoute = true }, false},
		{"xray not listening on the TPROXY port", func(c *clientChain) { c.notListening = true }, false},
		{"captured packets escape to the WAN", func(c *clientChain) { c.escaping = true }, false},
		{"the kill switch drops the LAN", func(c *clientChain) { c.dropping = true }, true},
		{"kill-switch drops ignored with the switch off", func(c *clientChain) { c.dropping = true }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := steerDaemon(t, renderWithDNS)
			d := s.d
			// The internet around the tunnel answers, the tunnel does not
			// (dead nodes), the panel is gone.
			srv := internetUp(t, nil)
			withoutPanel(t, d, srv)
			d.rescuePolicy.TraceURLs = []string{"http://127.0.0.1:1/cdn-cgi/trace"}
			d.supStarted = true
			if tc.ks {
				d.desired.Inbounds.Tproxy.KillSwitch = true
			}
			c := fakeClientChain(t, d)
			tc.break_(c)
			c.refresh()
			confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
			d.confirmer = firewall.NewCommitConfirmer(confirmPath, 90*time.Second)
			t0 := time.Date(2026, 10, 6, 4, 31, 0, 0, time.UTC)
			now := t0
			d.clock = func() time.Time { return now }
			reverted := false
			d.applyRuleset = func(string, firewall.Spec) error { _ = os.Remove(confirmPath); return nil }

			d.programFirewall(context.Background(), d.desired)
			for ; now.Before(t0.Add(90 * time.Second)); now = now.Add(dnsWatchEvery) {
				d.retryFirewallConfirm(context.Background(), now)
			}
			reverted = !fileExists(confirmPath)
			whole := tc.name == "whole" || strings.HasPrefix(tc.name, "kill-switch drops ignored")
			if whole && reverted {
				t.Fatalf("a whole client path was not confirmed (fault %q): the deadman reverts it", d.fwConfirmFault)
			}
			if !whole && !reverted {
				t.Fatal("a broken client path was confirmed: the deadman keeps a ruleset that cuts the LAN off")
			}
			if !whole && d.fwConfirmFault == "" {
				t.Error("no fault recorded")
			}
		})
	}
}

// The deadline runs from the deadman's arming, taken before the apply, and a
// proof that comes back after it does not write the sentinel: a "confirmed"
// can never land after the revert. The last try starts fwConfirmMaxTry
// before it.
func TestALateProofDoesNotConfirm(t *testing.T) {
	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d := confirmDaemon(t, "http://127.0.0.1:1", confirmPath, &http.Client{})
	t0 := time.Date(2026, 10, 6, 4, 31, 0, 0, time.UTC)
	var nowNs atomic.Int64
	set := func(at time.Time) { nowNs.Store(at.UnixNano()) }
	d.clock = func() time.Time { return time.Unix(0, nowNs.Load()).UTC() }
	var slow atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if slow.Load() {
			nowNs.Add(int64(30 * time.Second)) // the try took that long
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	withoutPanel(t, d, srv)

	// Armed 70 s ago (the apply and the DNS wait took long): past the last
	// try's start, nothing is tried.
	set(t0.Add(70 * time.Second))
	if d.confirmFirewall(context.Background(), t0) || fileExists(confirmPath) {
		t.Fatal("tried and confirmed after the last try's start")
	}
	// Within the window, but the proof returns after the deadline.
	slow.Store(true)
	set(t0.Add(60 * time.Second))
	if d.confirmFirewall(context.Background(), t0) || fileExists(confirmPath) {
		t.Fatalf("a proof that came back at +%s confirmed; the deadman wakes at +90s", d.now().Sub(t0))
	}
	// In time: confirmed.
	slow.Store(false)
	set(t0.Add(10 * time.Second))
	if !d.confirmFirewall(context.Background(), t0) {
		t.Fatal("a proof in time did not confirm")
	}
}

// Backed off from a panel that was gone for an hour, the router checks in at
// the first poll after the panel's health answers again — not at the end of
// a five-minute wait — and the backoff is reset.
func TestCheckInsResumeAtTheFirstPollAfterThePanelIsBack(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	hits, back := withoutPanelUntil(t, d, internetUp(t, nil))
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	now := t0
	d.clock = func() time.Time { return now }
	ctx := context.Background()
	poll := d.cfg.PollInterval()
	for ; now.Before(t0.Add(time.Hour)); now = now.Add(poll) {
		_ = d.runOnce(ctx)
	}
	if h := atomic.LoadInt64(hits); h < 3 || h > 40 {
		t.Fatalf("%d check-ins in an hour of 45 s polls: want a backoff (3..40)", h)
	}
	// The next due check-in fails too; one poll later the router is deep
	// in its wait when the panel comes back.
	now = d.cpRetryAt
	_ = d.runOnce(ctx)
	now = now.Add(poll)
	if !now.Before(d.cpRetryAt) {
		t.Fatalf("not backed off: the next check-in is due %s from now", d.cpRetryAt.Sub(now))
	}
	before := atomic.LoadInt64(hits)
	back.Store(true)
	_ = d.runOnce(ctx)
	if atomic.LoadInt64(hits) == before {
		t.Fatal("the panel is back and the next poll did not check in: it waits out the backoff")
	}
	if d.cpFailures != 0 || !d.cpRetryAt.IsZero() {
		t.Errorf("the backoff was not reset after the check-in: failures %d", d.cpFailures)
	}
}

// One failure costs no poll: the next poll asks again.
func TestOneFailedCheckInDoesNotSkipAPoll(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	hits := withoutPanel(t, d, internetUp(t, nil))
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	now := t0
	d.clock = func() time.Time { return now }
	_ = d.runOnce(context.Background())
	now = now.Add(d.cfg.PollInterval())
	_ = d.runOnce(context.Background())
	if h := atomic.LoadInt64(hits); h != 2 {
		t.Fatalf("%d check-ins in two polls after one failure; want 2", h)
	}
}

// THE WINDOW. The tries end ~27 s before the deadman wakes; a check-in in
// that window must not write the sentinel for a ruleset whose LAN path
// failed every check — nor at all once the confirmation's deadline passed.
// After the deadman woke (the ruleset reverted), the check-in's usual
// rewrite is back.
func TestACheckInBeforeTheRevertDoesNotConfirmABrokenRuleset(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	withoutPanel(t, d, internetUp(t, nil))
	d.supStarted = true
	c := fakeClientChain(t, d)
	c.noRule = true
	confirmPath := filepath.Join(t.TempDir(), "fw-confirm")
	d.confirmer = firewall.NewCommitConfirmer(confirmPath, 90*time.Second)
	d.applyRuleset = func(string, firewall.Spec) error { _ = os.Remove(confirmPath); return nil }
	t0 := time.Date(2026, 10, 6, 4, 31, 0, 0, time.UTC)
	now := t0
	d.clock = func() time.Time { return now }
	ctx := context.Background()

	d.programFirewall(ctx, d.desired)
	for ; now.Before(t0.Add(73 * time.Second)); now = now.Add(dnsWatchEvery) {
		d.retryFirewallConfirm(ctx, now)
	}
	if !d.fwConfirmBy.IsZero() {
		t.Fatal("the tries did not end before the window")
	}
	for _, at := range []time.Duration{73 * time.Second, 84 * time.Second, 86 * time.Second, 89 * time.Second} {
		now = t0.Add(at)
		d.checkInConfirm(ctx)
		if fileExists(confirmPath) {
			t.Fatalf("a check-in at +%s confirmed a ruleset whose LAN path failed every check", at)
		}
	}
	// The LAN path whole again, but past the deadline: the deadman decides.
	c.noRule = false
	now = t0.Add(86 * time.Second)
	d.checkInConfirm(ctx)
	if fileExists(confirmPath) {
		t.Fatal("a check-in after the confirmation's deadline wrote the sentinel")
	}
	// In time and whole: the check-in confirms.
	d.programFirewall(ctx, d.desired)
	t1 := now
	now = t1.Add(70 * time.Second)
	d.fwConfirmBy = time.Time{} // the tries over
	d.checkInConfirm(ctx)
	if !fileExists(confirmPath) {
		t.Fatal("a check-in in time with the LAN path whole did not confirm")
	}
	if d.unconfirmed(now) {
		t.Error("still unconfirmed after the check-in confirmed")
	}
	// After the deadman woke, nothing is unconfirmed: the rewrite is back.
	_ = os.Remove(confirmPath)
	d.fwUnconfirmedUntil = now.Add(-time.Second)
	d.checkInConfirm(ctx)
	if !fileExists(confirmPath) {
		t.Error("with nothing unconfirmed the check-in no longer rewrites the sentinel")
	}
}

// With the LAN's IPv6 through TPROXY (not refused), its policy rule is part
// of the path; refused, it is not asked.
func TestTheIPv6PolicyRuleCountsWhenIPv6IsCarried(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	d.supStarted = true
	c := fakeClientChain(t, d)
	c.noRule6 = true
	spec, _ := firewallSpecFromConfig(d.desired)
	spec.IPv6Enabled, spec.RefuseIPv6 = true, true
	d.fwSpec = &spec
	if f := d.clientChainFault(context.Background()); f != "" {
		t.Fatalf("IPv6 refused, yet its policy rule was asked: %s", f)
	}
	spec.RefuseIPv6 = false
	if f := d.clientChainFault(context.Background()); !strings.Contains(f, "IPv6 policy rule") {
		t.Fatalf("IPv6 carried and its policy rule missing: fault %q", f)
	}
	c.noRule6 = false
	if f := d.clientChainFault(context.Background()); f != "" {
		t.Fatalf("IPv6 carried, its policy route whole: fault %q", f)
	}
}

// A panel half up — its health answers, its check-in fails — gets one early
// check-in per wait, not one every poll: the backoff holds.
func TestAHalfUpPanelDoesNotBypassTheBackoff(t *testing.T) {
	s := steerDaemon(t, renderWithDNS)
	d := s.d
	p := withoutPanelSwitches(t, d, internetUp(t, nil))
	p.halfUp.Store(true)
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	now := t0
	d.clock = func() time.Time { return now }
	poll := d.cfg.PollInterval()
	polls := 0
	for ; now.Before(t0.Add(time.Hour)); now = now.Add(poll) {
		_ = d.runOnce(context.Background())
		polls++
	}
	// Backoff alone: ~16 in an hour; one early try per wait at most doubles it.
	if h := atomic.LoadInt64(p.panelHits); h > 40 || h >= int64(polls)/2 {
		t.Fatalf("%d check-ins in %d polls to a half-up panel: the health answers bypass the backoff", h, polls)
	}
}
