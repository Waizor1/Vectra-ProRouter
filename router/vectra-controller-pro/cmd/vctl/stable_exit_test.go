package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/vault"
)

// stableStand is a daemon with a real render of the provider fixture (its
// exit probe in) and a fake exit: answers[tag] are the addresses it gives,
// one per question ("" = no answer), cycling.
type stableStand struct {
	d       *daemon
	answers map[string][]string
	asked   map[string]int
	slow    map[string]time.Duration
}

func newStableStand(t *testing.T, opts xray.SpliceOptions) *stableStand {
	t.Helper()
	dir := t.TempDir()
	provider, err := os.ReadFile(filepath.Join("..", "..", "internal", "coreengine", "xray", "testdata", "provider", "entry-00.json"))
	if err != nil {
		t.Fatal(err)
	}
	opts.ExitProbeListen = xray.DefaultExitProbeListen
	tp := &config.TproxyInbound{ListenIP: "0.0.0.0", Port: 12345, FwMark: 1, UDPEnabled: true, Tag: "tproxy-in",
		Sniffing: config.Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}}}
	render, _, err := xray.Splice(provider, tp, opts)
	if err != nil {
		t.Fatal(err)
	}
	d := &daemon{cfg: agentcfg.Config{
		StatePath:      filepath.Join(dir, "state.json"),
		OverridesPath:  filepath.Join(dir, "local-overrides.json"),
		XrayRenderPath: filepath.Join(dir, "xray.json"),
	}}
	if err := vault.WriteFile(d.cfg.XrayRenderPath, render); err != nil {
		t.Fatal(err)
	}
	s := &stableStand{d: d, answers: map[string][]string{}, asked: map[string]int{}, slow: map[string]time.Duration{}}
	oldSteady, oldJitter, oldWait := stableSteady, stableJitter, exitAskWait
	t.Cleanup(func() { stableSteady, stableJitter, exitAskWait = oldSteady, oldJitter, oldWait })
	exitAskWait = 10 * time.Millisecond
	stableJitter = func() float64 { return 1 }
	stableSteady = func(_ context.Context, _ exitcheck.Prober, tag, _ string, n int) ([]string, time.Duration, int) {
		var ips []string
		seen := map[string]bool{}
		failed := 0
		for i := 0; i < n; i++ {
			list := s.answers[tag]
			ip := ""
			if len(list) > 0 {
				ip = list[s.asked[tag]%len(list)]
			}
			s.asked[tag]++
			if ip == "" {
				failed++
				continue
			}
			if !seen[ip] {
				seen[ip] = true
				ips = append(ips, ip)
			}
		}
		median := 100 * time.Millisecond
		if d, ok := s.slow[tag]; ok {
			median = d
		}
		return ips, median, failed
	}
	return s
}

func (s *stableStand) totalAsked() int {
	n := 0
	for _, v := range s.asked {
		n += v
	}
	return n
}

// The games are on their stable default: a full round asks every member of
// the main path the probe reaches, and pins the fastest one that kept one
// address — fr5, not de5 (slower) and not nl5 (three addresses).
func TestTheStableExitPinsTheFastestSteadyNode(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.answers["bridge-de5"] = []string{"5.175.180.50"}
	s.slow["bridge-de5"] = 300 * time.Millisecond
	s.answers["bridge-nl5"] = []string{"5.255.121.68", "89.124.99.206", "216.57.105.110"}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	r := s.d.stableExitNow()
	if r.State != stableStatePinned || r.Tag != "bridge-fr5" || r.IP != "38.180.154.151" || r.Country != "FR" {
		t.Fatalf("record %+v", r)
	}
	for _, tag := range []string{"bridge-pl5", "bridge-de5", "bridge-fin5", "bridge-fr5", "bridge-nl5", "bridge-ae5"} {
		if s.asked[tag] != catalog.DefaultStableSamples {
			t.Fatalf("%s asked %d times, want %d (asked: %v)", tag, s.asked[tag], catalog.DefaultStableSamples, s.asked)
		}
	}
	if s.asked["hy2-nl5"] != 0 || s.asked["bridge-ru-tcp"] != 0 {
		t.Fatalf("asked a node off the main path: %v", s.asked)
	}
	// Kept on disk: a restart keeps the address, and renders with it.
	fi, err := os.Stat(s.d.stableExitPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("record file %v %v", fi, err)
	}
	again := &daemon{cfg: s.d.cfg}
	provider, _ := os.ReadFile(filepath.Join("..", "..", "internal", "coreengine", "xray", "testdata", "provider", "entry-00.json"))
	if opts := again.withStableExit(xray.SpliceOptions{}, provider); opts.StablePin != "bridge-fr5" {
		t.Fatalf("after a restart the pin is %q", opts.StablePin)
	}
	state, country, checked := again.stableExitView(localctl.Overrides{})
	if state != stableStatePinned || country != "FR" || checked == nil {
		t.Fatalf("view %s %s %v", state, country, checked)
	}
}

// Between rounds the pin alone is rechecked, three questions, once its
// interval is up; one that keeps its address stays without a round.
func TestThePinIsRecheckedAloneAndStays(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	before := s.totalAsked()
	s.d.stableExitStep(context.Background(), now.Add(10*time.Minute))
	if s.totalAsked() != before {
		t.Fatal("asked before the recheck was due")
	}
	s.d.stableExitStep(context.Background(), now.Add(31*time.Minute))
	if got := s.totalAsked() - before; got != stableRecheckAsk {
		t.Fatalf("a recheck asked %d questions, want %d", got, stableRecheckAsk)
	}
	if r := s.d.stableExitNow(); r.Tag != "bridge-fr5" || !r.CheckedAt.Equal(now.Add(31*time.Minute)) {
		t.Fatalf("record %+v", r)
	}
	// Six hours on: a full round, the pin kept while it keeps its address
	// even if another steady node is faster now.
	s.answers["bridge-de5"] = []string{"5.175.180.50"}
	s.slow["bridge-de5"] = time.Millisecond
	s.d.stableExitStep(context.Background(), now.Add(6*time.Hour+time.Minute))
	if r := s.d.stableExitNow(); r.Tag != "bridge-fr5" || !r.FullAt.Equal(now.Add(6*time.Hour+time.Minute)) {
		t.Fatalf("the pin moved without cause: %+v", r)
	}
}

// A pin that answers from another address moves at once; one that is silent
// moves only the second time (one silence is no verdict — the balancer falls
// back meanwhile).
func TestAPinThatChangesItsAddressMoves(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	s.answers["bridge-fr5"] = []string{"38.180.154.151", "38.180.154.152"}
	s.answers["bridge-de5"] = []string{"5.175.180.50"}
	s.d.stableExitStep(context.Background(), now.Add(31*time.Minute))
	if r := s.d.stableExitNow(); r.Tag != "bridge-de5" {
		t.Fatalf("record %+v", r)
	}
	s.answers["bridge-de5"] = []string{""}
	s.d.stableExitStep(context.Background(), now.Add(62*time.Minute))
	if r := s.d.stableExitNow(); r.Tag != "bridge-de5" {
		t.Fatalf("moved after one silence: %+v", r)
	}
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.d.stableExitStep(context.Background(), now.Add(93*time.Minute))
	if r := s.d.stableExitNow(); r.Tag != "bridge-fr5" {
		t.Fatalf("did not move after two silences: %+v", r)
	}
}

// No node keeps one address: none, nothing pinned — the stable services ride
// the main path — and another look after the recheck interval, not 6 h.
func TestNoSteadyNodeLeavesTheServicesOnTheMainPath(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-nl5"] = []string{"5.255.121.68", "89.124.99.206"}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	r := s.d.stableExitNow()
	if r.State != stableStateNone || r.Tag != "" {
		t.Fatalf("record %+v", r)
	}
	provider, _ := os.ReadFile(filepath.Join("..", "..", "internal", "coreengine", "xray", "testdata", "provider", "entry-00.json"))
	if opts := s.d.withStableExit(xray.SpliceOptions{}, provider); opts.StablePin != "" {
		t.Fatalf("pinned %q", opts.StablePin)
	}
	before := s.totalAsked()
	s.d.stableExitStep(context.Background(), now.Add(31*time.Minute))
	if s.totalAsked() == before {
		t.Fatal("no new look after the recheck interval")
	}
}

// Nothing rides the stable exit (every game the owner sent direct or to the
// main path): the router asks nothing.
func TestNoStableServiceAsksNothing(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	modes := map[string]string{}
	for _, id := range xray.ActiveCatalog().ServiceIDs() {
		if m, _, _ := (localctl.Overrides{}).ServiceMode(id, xray.ActiveCatalog()); m == catalog.ModeStable {
			modes[id] = catalog.ModeDirect
		}
	}
	consent := map[string]string{}
	for id := range modes {
		consent[id] = xray.ActiveCatalog().Service(id).MatchersHash()
	}
	if _, err := localctl.UpdateOverrides(s.d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.ServiceModes, o.DirectConsent = modes, consent
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.d.stableExitStep(context.Background(), time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	if s.totalAsked() != 0 {
		t.Fatalf("asked %v", s.asked)
	}
}

// With the main path itself on the stable exit, the candidates are the
// stable fallback's: the main path as the provider made it.
func TestCandidatesFollowTheStableFallback(t *testing.T) {
	plain := newStableStand(t, xray.SpliceOptions{})
	main := newStableStand(t, xray.SpliceOptions{StablePin: "bridge-fr5", StableMain: true})
	raw1, _ := vault.ReadFile(plain.d.cfg.XrayRenderPath)
	raw2, _ := vault.ReadFile(main.d.cfg.XrayRenderPath)
	a, b := stableCandidates(raw1, nil), stableCandidates(raw2, []string{"bridge-ae5"})
	if len(a) == 0 || !reflect.DeepEqual(b, without(a, "bridge-ae5")) {
		t.Fatalf("candidates %v and, through the fallback and without the unfit ae5, %v", a, b)
	}
}

// A pin the exit check found filtered is not rendered, and a new round runs
// at once; a kept pin that no longer keeps one address is shown as none.
func TestAFilteredOrUnsteadyPinIsNotShownAsStable(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	provider, _ := os.ReadFile(filepath.Join("..", "..", "internal", "coreengine", "xray", "testdata", "provider", "entry-00.json"))
	s.d.exits.Round([]exitcheck.Result{{Tag: "bridge-fr5", Control: true, Blocked: []bool{false}}}, now)
	s.d.exits.Round([]exitcheck.Result{{Tag: "bridge-fr5", Control: true, Blocked: []bool{false}}}, now.Add(time.Minute))
	if !contains(s.d.exits.Unfit(), "bridge-fr5") {
		t.Skip("the exit check needs other evidence to call an exit unfit")
	}
	if opts := s.d.withStableExit(xray.SpliceOptions{}, provider); opts.StablePin != "" {
		t.Fatalf("a filtered pin rendered: %q", opts.StablePin)
	}
	if state, _, _ := s.d.stableExitView(localctl.Overrides{}); state == stableStatePinned {
		t.Fatal("a filtered pin shown as pinned")
	}
}

func TestAKeptUnsteadyPinReadsAsNone(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	s.answers["bridge-fr5"] = []string{"38.180.154.151", "38.180.154.152"}
	s.d.stableExitStep(context.Background(), now.Add(31*time.Minute))
	r := s.d.stableExitNow()
	if r.Tag != "bridge-fr5" || !r.Unsteady || r.State != stableStatePinned {
		t.Fatalf("record %+v", r)
	}
	if state, country, _ := s.d.stableExitView(localctl.Overrides{}); state != stableStateNone || country != "" {
		t.Fatalf("view %s %s", state, country)
	}
	provider, _ := os.ReadFile(filepath.Join("..", "..", "internal", "coreengine", "xray", "testdata", "provider", "entry-00.json"))
	if opts := s.d.withStableExit(xray.SpliceOptions{}, provider); opts.StablePin != "bridge-fr5" {
		t.Fatalf("the kept pin is not rendered (a restart for nothing): %q", opts.StablePin)
	}
}
