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
// holds bridge-fr5's endpoint failing (xray's own connections to it
// unanswered for 2 s).
func stableWatch(t *testing.T, s *stableStand, failing ...string) (*failoverWatch, time.Time) {
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
	w.det.Observe(t0.Add(3*time.Second), entries, eps)
	return w, t0.Add(3 * time.Second)
}

// A pin that stops answering moves along the last round's ranking in the
// watchdog's own seconds — no probe, no render — and the new pin's address
// is rechecked at the next tick.
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
	// pl5 is failing too: the move skips it.
	w, now := stableWatch(t, s, "bridge-fr5", "bridge-pl5")
	infos := map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-fr5"}}
	s.d.stableFailover(context.Background(), w, infos, localctl.Overrides{}, now)
	if got := s.overrides[len(s.overrides)-1]; got != "bridge-de5" {
		t.Fatalf("overrides %v", s.overrides)
	}
	r = s.d.stableExitNow()
	if r.Tag != "bridge-de5" || r.IP != "5.175.180.50" || !r.CheckedAt.IsZero() || r.State != stableStatePinned {
		t.Fatalf("record %+v", r)
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
	// A pin that answers is not moved; nor one xray is not pinned to (the
	// owner's, or an older one).
	n := len(s.overrides)
	s.d.stableFailover(context.Background(), w, map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-de5"}}, localctl.Overrides{}, now)
	s.d.stableFailover(context.Background(), w, map[string]api.BalancerInfo{xray.StableBalancerTag: {Override: "bridge-fr5"}}, localctl.Overrides{}, now)
	if len(s.overrides) != n {
		t.Fatalf("moved without cause: %v", s.overrides[n:])
	}
}

// No other node of the ranking is left: the override is cleared (the
// balancer chooses and falls back into the main path) and a full round runs
// at the next tick, not at the next recheck.
func TestADeadPinWithNothingToMoveToReleasesAndLooksAgain(t *testing.T) {
	s := newStableStand(t, xray.SpliceOptions{})
	s.answers["bridge-fr5"] = []string{"38.180.154.151"}
	s.d.stableExitStep(context.Background(), time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	w, now := stableWatch(t, s, "bridge-fr5")
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
