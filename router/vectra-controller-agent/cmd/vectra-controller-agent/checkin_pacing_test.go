package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-agent/internal/recovery"
	"vectra-controller-agent/internal/state"
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

// The fresh contact is on disk before jobs run, so the cron watchdog does not
// read a stale contact and probe alongside a long job.
func TestPersistContactBeforeJobsWritesTheFreshContact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	before := state.PersistedState{ControlPlaneRecovery: recovery.State{LastSuccessfulControlPlaneAt: "2026-10-06T10:00:00Z"}}
	current := before
	current.ControlPlaneRecovery.LastSuccessfulControlPlaneAt = "2026-10-06T10:20:00Z"

	baseline, err := persistContactBeforeJobs(path, before, &current)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "2026-10-06T10:20:00Z") {
		t.Fatalf("state on disk lacks the fresh contact: %s", raw)
	}
	if baseline != current {
		t.Fatal("the returned baseline must be the saved state")
	}
}
