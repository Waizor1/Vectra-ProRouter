// Package rescue is a pragmatic connectivity-based mode evaluator for the
// xray-direct controller. It is a deliberately smaller subset of the legacy
// agent's rescue+recovery machine: enough to detect "proxy path is down" and
// recommend a fallback to direct (and recovery back to proxy), with a cooldown
// to prevent flapping. The deep auto-reboot recovery phases are deferred — on
// a canary, PassWall2 remains installed as the instant rollback.
//
// Evaluate is pure (no I/O) so the policy is fully unit-tested; the daemon
// performs the probes (ProbeAny) and acts on the Decision.
package rescue

import (
	"context"
	"net/http"
	"time"
)

// Mode is the controller's routing posture.
type Mode string

const (
	ModeProxy  Mode = "proxy"  // traffic flows through Xray
	ModeDirect Mode = "direct" // Xray routing bypassed; plain internet
)

// State is the rescue state carried across loops.
type State struct {
	Mode               Mode
	ProxyFailureCount  int
	DirectSuccessCount int
	ProxySuccessCount  int
	LastTransitionAt   time.Time
}

// Policy tunes the evaluator.
type Policy struct {
	HealthURLs           []string
	TriggerFailureCount  int           // consecutive proxy failures before going direct
	RecoverySuccessCount int           // consecutive direct successes before retrying proxy
	Cooldown             time.Duration // minimum gap between transitions
}

// DefaultPolicy returns sane defaults (mirrors the agent's thresholds).
func DefaultPolicy() Policy {
	return Policy{
		HealthURLs:           []string{"https://www.gstatic.com/generate_204", "https://cp.cloudflare.com/generate_204"},
		TriggerFailureCount:  3,
		RecoverySuccessCount: 2,
		Cooldown:             5 * time.Minute,
	}
}

// Input is the per-loop observation handed to Evaluate.
type Input struct {
	CurrentState    State
	PublicReachable bool // could we reach the public health URLs this loop?
	ProxyConclusive bool // in proxy mode, was the failure conclusive (not a transient probe error)?
	DirectReachable bool // is a direct path available (gate before switching to direct)?
	Now             time.Time
}

// Decision is the evaluator's recommendation.
type Decision struct {
	ShouldTransition bool
	NextMode         Mode
	NextState        State
	Reason           string
}

// Evaluate decides whether to transition modes given the current observation.
func Evaluate(in Input, p Policy) Decision {
	if p.TriggerFailureCount <= 0 {
		p.TriggerFailureCount = DefaultPolicy().TriggerFailureCount
	}
	if p.RecoverySuccessCount <= 0 {
		p.RecoverySuccessCount = DefaultPolicy().RecoverySuccessCount
	}
	st := in.CurrentState
	if st.Mode == "" {
		st.Mode = ModeProxy
	}
	d := Decision{NextMode: st.Mode, NextState: st}

	cooldownOK := st.LastTransitionAt.IsZero() || in.Now.Sub(st.LastTransitionAt) >= p.Cooldown

	switch st.Mode {
	case ModeProxy:
		if in.PublicReachable {
			d.NextState.ProxyFailureCount = 0
			d.NextState.ProxySuccessCount = st.ProxySuccessCount + 1
			return d
		}
		if !in.ProxyConclusive {
			return d // transient probe failure — don't count it
		}
		d.NextState.ProxyFailureCount = st.ProxyFailureCount + 1
		if d.NextState.ProxyFailureCount >= p.TriggerFailureCount && cooldownOK && in.DirectReachable {
			d.ShouldTransition = true
			d.NextMode = ModeDirect
			d.NextState.Mode = ModeDirect
			d.NextState.DirectSuccessCount = 0
			d.NextState.LastTransitionAt = in.Now
			d.Reason = "proxy path unreachable; falling back to direct"
		}
		return d

	case ModeDirect:
		if in.PublicReachable {
			d.NextState.DirectSuccessCount = st.DirectSuccessCount + 1
			if d.NextState.DirectSuccessCount >= p.RecoverySuccessCount && cooldownOK {
				d.ShouldTransition = true
				d.NextMode = ModeProxy
				d.NextState.Mode = ModeProxy
				d.NextState.ProxyFailureCount = 0
				d.NextState.LastTransitionAt = in.Now
				d.Reason = "direct path stable; retrying proxy"
			}
		}
		return d
	}
	return d
}

// ProbeAny returns true if ANY url answers with a < 400 status. A nil client
// uses a short-timeout default. Used by the daemon to fill Input.
func ProbeAny(ctx context.Context, client *http.Client, urls []string) bool {
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}
	for _, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode < 400 {
			return true
		}
	}
	return false
}
