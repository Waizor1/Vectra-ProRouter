package main

import (
	"testing"
	"time"

	"vectra-controller-agent/internal/recovery"
)

func TestRecoveryCheckInPacing(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 2 * time.Minute},
		{1, 4 * time.Minute},
		{2, 5 * time.Minute},
		{10, 5 * time.Minute},
	}
	for _, tc := range cases {
		if got := recoveryCheckInInterval(tc.failures); got != tc.want {
			t.Errorf("recoveryCheckInInterval(%d) = %s, want %s", tc.failures, got, tc.want)
		}
	}

	now := time.Now()
	pacer := &checkInPacer{}
	if !pacer.dueDuringRecovery(now, recovery.PhaseMonitoring) {
		t.Fatal("a pacer that never attempted must be due")
	}
	if pacer.dueDuringRecovery(now, recovery.PhaseRebootWait) {
		t.Fatal("never check in right before a self-reboot")
	}

	// A failed attempt backs off to 4 minutes.
	pacer.noteAttempt(now)
	if pacer.dueDuringRecovery(now.Add(3*time.Minute), recovery.PhaseDirectSettle) {
		t.Fatal("expected backoff after a failed attempt")
	}
	if !pacer.dueDuringRecovery(now.Add(4*time.Minute), recovery.PhaseDirectSettle) {
		t.Fatal("expected the next attempt after the backoff")
	}

	// A success resets the backoff to the base interval.
	pacer.noteAttempt(now.Add(4 * time.Minute))
	pacer.noteSuccess()
	if !pacer.dueDuringRecovery(now.Add(6*time.Minute), recovery.PhaseOperatorAttention) {
		t.Fatal("expected the base interval after a success")
	}

	// Never longer than the cap, however long the panel stays down.
	for i := 0; i < 20; i++ {
		pacer.noteAttempt(now.Add(10 * time.Minute))
	}
	if !pacer.dueDuringRecovery(now.Add(15*time.Minute), recovery.PhaseMonitoring) {
		t.Fatal("expected an attempt at least every recoveryCheckInMaxInterval")
	}

	// A clock that jumped backwards never silences the router.
	if !pacer.dueDuringRecovery(now, recovery.PhaseMonitoring) {
		t.Fatal("expected a check-in after a backwards clock jump")
	}
}
