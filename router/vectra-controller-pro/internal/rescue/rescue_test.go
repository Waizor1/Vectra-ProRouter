package rescue

import (
	"testing"
	"time"
)

func TestProxyStaysWhenReachable(t *testing.T) {
	in := Input{
		CurrentState:    State{Mode: ModeProxy, ProxyFailureCount: 2},
		PublicReachable: true,
		Now:             time.Now(),
	}
	d := Evaluate(in, DefaultPolicy())
	if d.ShouldTransition {
		t.Fatalf("should not transition while reachable: %+v", d)
	}
	if d.NextState.ProxyFailureCount != 0 {
		t.Errorf("failure count not reset: %d", d.NextState.ProxyFailureCount)
	}
}

func TestProxyToDirectAfterFailures(t *testing.T) {
	p := DefaultPolicy()
	now := time.Now()
	st := State{Mode: ModeProxy, ProxyFailureCount: p.TriggerFailureCount - 1}
	d := Evaluate(Input{
		CurrentState:    st,
		PublicReachable: false,
		ProxyConclusive: true,
		DirectReachable: true,
		Now:             now,
	}, p)
	if !d.ShouldTransition || d.NextMode != ModeDirect {
		t.Fatalf("expected transition to direct: %+v", d)
	}
	if d.NextState.LastTransitionAt != now {
		t.Error("transition timestamp not set")
	}
}

func TestNoTransitionWhenDirectUnavailable(t *testing.T) {
	p := DefaultPolicy()
	st := State{Mode: ModeProxy, ProxyFailureCount: p.TriggerFailureCount - 1}
	d := Evaluate(Input{
		CurrentState:    st,
		PublicReachable: false,
		ProxyConclusive: true,
		DirectReachable: false, // can't verify direct works -> don't cut over
		Now:             time.Now(),
	}, p)
	if d.ShouldTransition {
		t.Fatalf("must not go direct when direct path unverified: %+v", d)
	}
	if d.NextState.ProxyFailureCount != p.TriggerFailureCount {
		t.Errorf("failure still counted: %d", d.NextState.ProxyFailureCount)
	}
}

func TestTransientFailureNotCounted(t *testing.T) {
	d := Evaluate(Input{
		CurrentState:    State{Mode: ModeProxy, ProxyFailureCount: 1},
		PublicReachable: false,
		ProxyConclusive: false, // inconclusive (e.g. our own probe broke)
		Now:             time.Now(),
	}, DefaultPolicy())
	if d.NextState.ProxyFailureCount != 1 {
		t.Errorf("transient failure should not increment count: %d", d.NextState.ProxyFailureCount)
	}
}

func TestCooldownBlocksTransition(t *testing.T) {
	p := DefaultPolicy()
	st := State{
		Mode:              ModeProxy,
		ProxyFailureCount: p.TriggerFailureCount - 1,
		LastTransitionAt:  time.Now(), // just transitioned
	}
	d := Evaluate(Input{
		CurrentState:    st,
		PublicReachable: false,
		ProxyConclusive: true,
		DirectReachable: true,
		Now:             time.Now(),
	}, p)
	if d.ShouldTransition {
		t.Fatalf("cooldown should block transition: %+v", d)
	}
}

func TestDirectRecoversToProxy(t *testing.T) {
	p := DefaultPolicy()
	now := time.Now()
	st := State{Mode: ModeDirect, DirectSuccessCount: p.RecoverySuccessCount - 1, LastTransitionAt: now.Add(-p.Cooldown - time.Second)}
	d := Evaluate(Input{CurrentState: st, PublicReachable: true, Now: now}, p)
	if !d.ShouldTransition || d.NextMode != ModeProxy {
		t.Fatalf("expected recovery to proxy: %+v", d)
	}
}
