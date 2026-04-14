package rescue

import (
	"testing"
	"time"
)

func TestEvaluateTransitionsToDirect(t *testing.T) {
	now := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	decision := Evaluate(EvaluationInput{
		Now: now,
		Policy: Policy{
			TriggerFailureCount:      3,
			RecoverySuccessCount:     2,
			Cooldown:                 5 * time.Minute,
			RequireDirectPathSuccess: true,
		},
		State: State{
			Mode:               ModeProxy,
			ProxyFailureCount:  2,
			DirectSuccessCount: 1,
			LastTransitionAt:   now.Add(-10 * time.Minute),
		},
		ProxyFailureIncrement: 1,
	})

	if !decision.ShouldTransition {
		t.Fatalf("expected transition to direct mode")
	}
	if decision.NextMode != ModeDirect {
		t.Fatalf("expected direct mode, got %s", decision.NextMode)
	}
	if decision.NextState.LastTransitionAt != now {
		t.Fatalf("expected transition timestamp to be updated")
	}
}

func TestEvaluateStaysInCooldown(t *testing.T) {
	now := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	decision := Evaluate(EvaluationInput{
		Now: now,
		Policy: Policy{
			TriggerFailureCount:      3,
			Cooldown:                 5 * time.Minute,
			RequireDirectPathSuccess: false,
		},
		State: State{
			Mode:              ModeProxy,
			ProxyFailureCount: 2,
			LastTransitionAt:  now.Add(-2 * time.Minute),
		},
		ProxyFailureIncrement: 1,
	})

	if decision.ShouldTransition {
		t.Fatalf("expected cooldown to block transition")
	}
	if decision.NextMode != ModeProxy {
		t.Fatalf("expected to remain in proxy mode, got %s", decision.NextMode)
	}
}

func TestEvaluateRecoversProxy(t *testing.T) {
	now := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	decision := Evaluate(EvaluationInput{
		Now: now,
		Policy: Policy{
			RecoverySuccessCount:     2,
			Cooldown:                 5 * time.Minute,
			RequireDirectPathSuccess: true,
		},
		State: State{
			Mode:              ModeDirect,
			ProxySuccessCount: 1,
			LastTransitionAt:  now.Add(-10 * time.Minute),
		},
		ProxySuccessIncrement: 1,
	})

	if !decision.ShouldTransition {
		t.Fatalf("expected recovery transition")
	}
	if decision.NextMode != ModeProxy {
		t.Fatalf("expected proxy mode, got %s", decision.NextMode)
	}
}
