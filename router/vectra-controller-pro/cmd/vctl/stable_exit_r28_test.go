package main

import (
	"bytes"
	"context"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"vectra-controller-pro/internal/api"
	"vectra-controller-pro/internal/conntrack"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/failover"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/vault"
	"vectra-controller-pro/internal/xrayview"
)

// r28 (docs/superpowers/specs/2026-10-19-vctl-games-stable-ip.md): a pin
// move is xray's override, never a render — 1111 on 2026-10-19 lost every
// connection of the house at 23:19:19 to r26's render of a new pin.

// A round that moves the pin points the running xray at it and renders
// nothing: the render's bytes stay, so xray is not restarted.
func TestAPinMoveIsLiveNotARender(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	before, _ := vault.ReadFile(s.d.cfg.XrayRenderPath)
	s.answers["bridge-fr5"] = []string{"38.180.154.151", "38.180.154.152"}
	s.answers["bridge-de5"] = []string{"5.175.180.50"}
	s.d.stableExitStep(context.Background(), now.Add(31*time.Minute))
	if r := s.d.stableExitNow(); r.Tag != "bridge-de5" {
		t.Fatalf("record %+v", r)
	}
	if !reflect.DeepEqual(s.overrides, []string{"bridge-fr5", "bridge-de5"}) {
		t.Fatalf("overrides %v", s.overrides)
	}
	after, _ := vault.ReadFile(s.d.cfg.XrayRenderPath)
	if !bytes.Equal(before, after) {
		t.Fatal("a pin move changed the render")
	}
}

// xray's API did not answer: the move is asked again at the next tick.
func TestALiveMoveXrayMissedIsAskedAgain(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	fail := true
	stableOverride = func(_ context.Context, _, _, target string) error {
		if fail {
			return context.DeadlineExceeded
		}
		s.overrides = append(s.overrides, target)
		return nil
	}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	if len(s.overrides) != 0 {
		t.Fatalf("overrides %v", s.overrides)
	}
	fail = false
	s.d.stableExitStep(context.Background(), now.Add(time.Minute))
	if !reflect.DeepEqual(s.overrides, []string{"bridge-fr5"}) {
		t.Fatalf("not asked again: %v", s.overrides)
	}
}

// The owner pinned the stable balancer in the router UI: theirs, never
// touched by the tracker — neither by a round nor at an xray start.
func TestTheOwnersPinOnTheStableBalancerWins(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	if _, err := localctl.UpdateOverrides(s.d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.Pins = map[string]string{xray.StableBalancerTag: "bridge-de5"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.d.stableExitStep(context.Background(), time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	s.d.reapplyStablePin(0)
	if len(s.overrides) != 0 {
		t.Fatalf("the owner's pin was overridden: %v", s.overrides)
	}
}

// stableWatch is a failover watch over the stand's render whose detector
// holds the failing nodes' endpoints unanswered for 3 s, while the answering
// ones had a connection answered meanwhile (the router's way out is up).
func stableWatch(t *testing.T, s *stableStand, failing, answering []string) (*failoverWatch, time.Time) {
	t.Helper()
	raw, err := vault.ReadFile(s.d.cfg.XrayRenderPath)
	if err != nil {
		t.Fatal(err)
	}
	view, err := xrayview.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	w := newFailoverWatch()
	w.view = view
	eps := failover.Endpoints{}
	var entries []conntrack.Entry
	for i, tag := range failing {
		ep := netip.AddrPortFrom(netip.AddrFrom4([4]byte{203, 0, 113, byte(10 + i)}), 443)
		eps[failover.Endpoint{Proto: "tcp", Addr: ep}] = []string{tag}
		entries = append(entries, unanswered(uint16(100+2*i), ep.String()), unanswered(uint16(101+2*i), ep.String()))
	}
	t0 := time.Date(2026, 10, 18, 12, 40, 0, 0, time.UTC)
	w.det.Observe(t0, entries, eps)
	for i, tag := range answering {
		ep := netip.AddrPortFrom(netip.AddrFrom4([4]byte{203, 0, 113, byte(50 + i)}), 443)
		eps[failover.Endpoint{Proto: "tcp", Addr: ep}] = []string{tag}
		e := unanswered(uint16(200+i), ep.String())
		e.Replied, e.State = true, "ESTABLISHED"
		entries = append(entries, e)
	}
	w.det.Observe(t0.Add(3*time.Second), entries, eps)
	return w, t0.Add(3 * time.Second)
}

// A pin that stops answering moves along the last round's ranking in the
// watchdog's own seconds — no probe, no render — while the router's way out
// is up; the new pin's address is rechecked at the next tick, and the dead
// one leaves the ranking.
func TestADeadPinMovesAlongTheRanking(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.answers["bridge-de5"] = []string{"5.175.180.50"}
	s.slow["bridge-de5"] = 300 * time.Millisecond
	s.answers["bridge-pl5"] = []string{"45.1.1.1"}
	s.slow["bridge-pl5"] = 200 * time.Millisecond
	s.d.stableExitStep(context.Background(), time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	r := s.d.stableExitNow()
	if r.Tag != "bridge-fr5" || len(r.Ranked) != 3 || r.Ranked[0].Tag != "bridge-fr5" || r.Ranked[1].Tag != "bridge-pl5" || r.Ranked[2].Tag != "bridge-de5" {
		t.Fatalf("record %+v", r)
	}
	before, _ := vault.ReadFile(s.d.cfg.XrayRenderPath)
	// pl5 is failing too: the move skips it; nl5 answers: the way out is up.
	w, now := stableWatch(t, s, []string{"bridge-fr5", "bridge-pl5"}, []string{"bridge-nl5"})
	infos := map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-fr5"}}
	s.d.stableFailover(context.Background(), w, infos, localctl.Overrides{}, now)
	if s.current != "bridge-de5" {
		t.Fatalf("overrides %v", s.overrides)
	}
	r = s.d.stableExitNow()
	if r.Tag != "bridge-de5" || r.IP != "5.175.180.50" || !r.CheckedAt.IsZero() || r.State != stableStatePinned {
		t.Fatalf("record %+v", r)
	}
	for _, c := range r.Ranked {
		if c.Tag == "bridge-fr5" {
			t.Fatalf("the dead pin is still ranked: %+v", r.Ranked)
		}
	}
	if after, _ := vault.ReadFile(s.d.cfg.XrayRenderPath); !bytes.Equal(before, after) {
		t.Fatal("the move changed the render")
	}
	// The next tick rechecks de5's address and keeps it.
	asked := s.asked["bridge-de5"]
	s.d.stableExitStep(context.Background(), now.Add(time.Minute))
	if s.asked["bridge-de5"]-asked != stableRecheckAsk {
		t.Fatalf("de5 asked %d times at the next tick", s.asked["bridge-de5"]-asked)
	}
	if r := s.d.stableExitNow(); r.Tag != "bridge-de5" || r.CheckedAt.IsZero() {
		t.Fatalf("record %+v", r)
	}
	// A pin that answers is not moved.
	n := len(s.overrides)
	s.d.stableFailover(context.Background(), w, map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-de5"}}, localctl.Overrides{}, now)
	if len(s.overrides) != n {
		t.Fatalf("moved without cause: %v", s.overrides[n:])
	}
}

// Nothing else answers either: the router's own way out is down (a WAN
// blip), not the pin — it stays, and the game keeps its address.
func TestAWANBlipDoesNotMoveThePin(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.answers["bridge-de5"] = []string{"5.175.180.50"}
	s.slow["bridge-de5"] = 300 * time.Millisecond
	s.d.stableExitStep(context.Background(), time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	w, now := stableWatch(t, s, []string{"bridge-fr5"}, nil)
	n := len(s.overrides)
	s.d.stableFailover(context.Background(), w, map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-fr5"}}, localctl.Overrides{}, now)
	if len(s.overrides) != n || s.d.stableExitNow().Tag != "bridge-fr5" {
		t.Fatalf("moved on a blip: %v, record %+v", s.overrides[n:], s.d.stableExitNow())
	}
}

// The observatory holds the pin dead (it takes connections and carries
// nothing — the connection table cannot tell): the tick moves it, when
// another node is alive; every node dead is the way out, and nothing moves.
func TestTheObservatoryMovesADataDeadPin(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.answers["bridge-de5"] = []string{"5.175.180.50"}
	s.slow["bridge-de5"] = 300 * time.Millisecond
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	s.health = map[string]bool{}
	for _, m := range []string{"bridge-ae5", "bridge-de5", "bridge-fin5", "bridge-fr5", "bridge-nl5", "bridge-pl5", "bridge-tr5", "bridge-us5"} {
		s.health[m] = false
	}
	s.d.stableHealth(context.Background())
	if s.current != "bridge-fr5" {
		t.Fatalf("moved with every node dead: %v", s.overrides)
	}
	s.health["bridge-de5"] = true
	s.d.stableHealth(context.Background())
	if s.current != "bridge-de5" || s.d.stableExitNow().Tag != "bridge-de5" {
		t.Fatalf("overrides %v record %+v", s.overrides, s.d.stableExitNow())
	}
}

// xray is kept in line with the record, whatever moved it: the pin is set
// back on a look that finds another node; with no pin, the tracker clears
// only its own override, never one the failover policy set.
func TestXrayIsBroughtInLineWithTheRecord(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.d.stableExitStep(context.Background(), time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	w, now := stableWatch(t, s, nil, nil)
	s.d.stableFailover(context.Background(), w, map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-de5"}}, localctl.Overrides{}, now)
	if s.current != "bridge-fr5" {
		t.Fatalf("not set back: %v", s.overrides)
	}
	// No pin now: the tracker's own override (fr5) is cleared…
	r := s.d.stableExitNow()
	r.State, r.Tag = stableStateNone, ""
	s.d.saveStableExit(r)
	s.d.stableFailover(context.Background(), w, map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-fr5"}}, localctl.Overrides{}, now)
	if s.current != "" {
		t.Fatalf("its own override kept: %v", s.overrides)
	}
	// …and the policy's move is left alone.
	n := len(s.overrides)
	s.d.stableFailover(context.Background(), w, map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-nl5"}}, localctl.Overrides{}, now)
	if len(s.overrides) != n {
		t.Fatalf("cleared the policy's move: %v", s.overrides[n:])
	}
}

// A round that asked while the pin was moved off judged a record that no
// longer is: its pick is not saved over the move, and another round runs at
// the next tick.
func TestARoundThatRacedAMoveIsDiscarded(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.answers["bridge-de5"] = []string{"5.175.180.50"}
	s.slow["bridge-de5"] = 300 * time.Millisecond
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	steady := stableSteady
	moved := false
	stableSteady = func(ctx context.Context, p exitcheck.Prober, tag, url string, n int) ([]string, time.Duration, int) {
		if tag == "bridge-nl5" && !moved {
			moved = true // the watchdog moves the pin while the round asks
			r := s.d.stableExitNow()
			r.Tag, r.IP = "bridge-de5", "5.175.180.50"
			s.d.saveStableExit(r)
		}
		return steady(ctx, p, tag, url, n)
	}
	s.d.stableExitStep(context.Background(), now.Add(6*time.Hour+time.Minute))
	if r := s.d.stableExitNow(); r.Tag != "bridge-de5" {
		t.Fatalf("the round wrote over the move: %+v", r)
	}
	before := s.totalAsked()
	s.d.stableExitStep(context.Background(), now.Add(6*time.Hour+2*time.Minute))
	if s.totalAsked()-before < 8 {
		t.Fatalf("no round after a stale one (%d questions)", s.totalAsked()-before)
	}
}

// No other node of the ranking is left: the override is cleared (the
// balancer chooses and falls back into the main path) and a full round runs
// at the next tick, not at the next recheck.
func TestADeadPinWithNothingToMoveToReleasesAndLooksAgain(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.d.stableExitStep(context.Background(), time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	w, now := stableWatch(t, s, []string{"bridge-fr5"}, []string{"bridge-nl5"})
	s.d.stableFailover(context.Background(), w, map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-fr5"}}, localctl.Overrides{}, now)
	if got := s.overrides[len(s.overrides)-1]; got != "" {
		t.Fatalf("overrides %v", s.overrides)
	}
	if r := s.d.stableExitNow(); r.State != stableStateNone || r.Tag != "" {
		t.Fatalf("record %+v", r)
	}
	s.answers["bridge-fr5"] = []string{""}
	s.answers["bridge-de5"] = []string{"5.175.180.50"}
	before := s.totalAsked()
	s.d.stableExitStep(context.Background(), now.Add(time.Minute))
	if s.totalAsked()-before < 8 {
		t.Fatalf("no full round at the next tick (%d questions)", s.totalAsked()-before)
	}
	if r := s.d.stableExitNow(); r.Tag != "bridge-de5" || s.overrides[len(s.overrides)-1] != "bridge-de5" {
		t.Fatalf("record %+v overrides %v", r, s.overrides)
	}
}

// A new render without the pin's node (the provider dropped it): the start
// hook sets nothing and a full round runs at the next tick.
func TestARenderThatLostThePinLooksAgainAtOnce(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	s.d.stableExitStep(context.Background(), now)
	r := s.d.stableExitNow()
	r.Tag, r.IP = "bridge-gone", "198.51.100.1"
	s.d.saveStableExit(r)
	n := len(s.overrides)
	s.d.reapplyStablePin(0)
	if len(s.overrides) != n {
		t.Fatalf("pinned a node the render does not have: %v", s.overrides[n:])
	}
	before := s.totalAsked()
	s.d.stableExitStep(context.Background(), now.Add(2*time.Minute))
	if s.totalAsked()-before < 8 {
		t.Fatalf("no full round at the next tick (%d questions)", s.totalAsked()-before)
	}
	if r := s.d.stableExitNow(); r.Tag != "bridge-fr5" {
		t.Fatalf("record %+v", r)
	}
}
