package rescue

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// The cooldown holds the way back to the proxy, never the way out: a tunnel
// that died minutes after the last switch must not keep the LAN offline.
func TestCooldownHoldsOnlyTheWayBack(t *testing.T) {
	p := DefaultPolicy()
	now := time.Now()
	d := Evaluate(Input{
		CurrentState:    State{Mode: ModeProxy, ProxyFailureCount: p.TriggerFailureCount - 1, LastTransitionAt: now},
		PublicReachable: false,
		ProxyConclusive: true,
		DirectReachable: true,
		Now:             now,
	}, p)
	if !d.ShouldTransition || d.NextMode != ModeDirect {
		t.Fatalf("the cooldown kept a dead tunnel: %+v", d)
	}
	d = Evaluate(Input{
		CurrentState:    State{Mode: ModeDirect, DirectSuccessCount: p.RecoverySuccessCount, LastTransitionAt: now},
		PublicReachable: true,
		Now:             now,
	}, p)
	if d.ShouldTransition {
		t.Fatalf("back to the proxy within the cooldown: %+v", d)
	}
}

// With the observatory holding every node dead, direct mode stays: the proxy
// retried then carries nothing.
func TestNoRetryWhileTheTunnelIsDead(t *testing.T) {
	p := DefaultPolicy()
	now := time.Now()
	st := State{Mode: ModeDirect, DirectSuccessCount: p.RecoverySuccessCount, LastTransitionAt: now.Add(-p.Cooldown - time.Second)}
	if d := Evaluate(Input{CurrentState: st, PublicReachable: true, TunnelDead: true, Now: now}, p); d.ShouldTransition {
		t.Fatalf("retried the proxy with every node dead: %+v", d)
	}
	if d := Evaluate(Input{CurrentState: st, PublicReachable: true, Now: now}, p); !d.ShouldTransition || d.NextMode != ModeProxy {
		t.Fatalf("a node lives and the proxy is not retried: %+v", d)
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

// Dead urls are waited on together, not in turn: two of them cost one timeout.
func TestProbeAnyAsksAllAtOnce(t *testing.T) {
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	defer close(block)
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer fast.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	start := time.Now()
	if !ProbeAny(context.Background(), client, []string{slow.URL, slow.URL + "/2", fast.URL}) {
		t.Fatal("a url that answers was not seen")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("the answering url waited on the dead ones: %s", time.Since(start))
	}
	start = time.Now()
	if ProbeAny(context.Background(), client, []string{slow.URL, slow.URL + "/2"}) {
		t.Fatal("dead urls answered")
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("two dead urls cost %s: asked in turn", el)
	}
	start = time.Now()
	if ProbeAnyWithin(context.Background(), &http.Client{}, []string{slow.URL}, 300*time.Millisecond) {
		t.Fatal("a dead url answered")
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("the budget was not kept: %s", el)
	}
}

// A return to the proxy that fails within RetryWindow doubles the next
// cooldown; a proxy that holds for StableAfter clears the count.
func TestFailedReturnsBackTheWayBackOff(t *testing.T) {
	p := DefaultPolicy()
	now := time.Now()
	// Back on the proxy a minute ago, and it fails again.
	st := State{Mode: ModeProxy, ProxyFailureCount: p.TriggerFailureCount - 1, LastTransitionAt: now.Add(-time.Minute)}
	d := Evaluate(Input{CurrentState: st, ProxyConclusive: true, DirectReachable: true, Now: now}, p)
	if !d.ShouldTransition || d.NextState.FailedRetries != 1 {
		t.Fatalf("a failed return not counted: %+v", d)
	}
	direct := d.NextState
	direct.DirectSuccessCount = p.RecoverySuccessCount
	if d := Evaluate(Input{CurrentState: direct, PublicReachable: true, Now: now.Add(p.Cooldown + time.Second)}, p); d.ShouldTransition {
		t.Fatalf("one failed return and the plain cooldown still lets it back: %+v", d)
	}
	if d := Evaluate(Input{CurrentState: direct, PublicReachable: true, Now: now.Add(2*p.Cooldown + time.Second)}, p); !d.ShouldTransition {
		t.Fatalf("the doubled cooldown is over and no return: %+v", d)
	}
	// Long on the proxy: the count goes.
	held := State{Mode: ModeProxy, FailedRetries: 3, LastTransitionAt: now.Add(-StableAfter)}
	if d := Evaluate(Input{CurrentState: held, PublicReachable: true, Now: now}, p); d.NextState.FailedRetries != 0 {
		t.Fatalf("a proxy that held kept the count: %+v", d)
	}
	// The first fall to direct after a long proxy is not a failed return.
	first := State{Mode: ModeProxy, ProxyFailureCount: p.TriggerFailureCount - 1, LastTransitionAt: now.Add(-time.Hour)}
	if d := Evaluate(Input{CurrentState: first, ProxyConclusive: true, DirectReachable: true, Now: now}, p); d.NextState.FailedRetries != 0 {
		t.Fatalf("counted as a failed return: %+v", d)
	}
}

// The trace's "ip=" is what the probe reports; a 4xx or a body without it is
// no answer.
func TestProbeTraceReadsTheAddress(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fl=1\nh=x\nip=203.0.113.7\nloc=PL\n"))
	}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	defer bad.Close()
	if ip, got := ProbeTrace(context.Background(), nil, []string{bad.URL, ok.URL}, 2*time.Second); !got || ip != "203.0.113.7" {
		t.Fatalf("got %q %v", ip, got)
	}
	if _, got := ProbeTrace(context.Background(), nil, []string{bad.URL}, 2*time.Second); got {
		t.Fatal("a 403 counted as an answer")
	}
}

// ProbeTraceAll keeps every answer: one url out by the kernel must not hide
// another that came through the tunnel.
func TestProbeTraceAllKeepsEveryAnswer(t *testing.T) {
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ip=198.51.100.1\n")) }))
	defer a.Close()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ip=203.0.113.9\n")) }))
	defer b.Close()
	ips := ProbeTraceAll(context.Background(), nil, []string{a.URL, b.URL}, 2*time.Second)
	if len(ips) != 2 {
		t.Fatalf("answers %v, want both", ips)
	}
}
