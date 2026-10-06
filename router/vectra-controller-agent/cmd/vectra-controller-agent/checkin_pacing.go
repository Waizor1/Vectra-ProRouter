package main

import (
	"sync"
	"time"

	"vectra-controller-agent/internal/recovery"
)

// Control-plane recovery asks runOnce to skip the check-in whenever the
// /api/health probe fails and through every settle/warmup phase. Taken
// literally that starves the panel: the probe has a 5-second budget while the
// check-in has the full request timeout, so on a slow or lossy path the probe
// keeps failing while a check-in would go through. The router then shows up
// once every ~20 minutes (whenever a probe happens to pass), recovery counts
// the silence as an outage, and the cron dead-man reads the stale contact as a
// strand (leonid-avito: /api/health every 45 s, check-ins every ~20 min, 32
// "unreachable over one hour" incidents in two days).
//
// The skip is therefore advisory. While it is requested a check-in is still
// attempted once per interval, doubling after each failed attempt up to a cap,
// so a dead panel costs one request timeout every few minutes and a live one
// always sees — and can rescue — the router.
const (
	recoveryCheckInBaseInterval = 2 * time.Minute
	recoveryCheckInMaxInterval  = 5 * time.Minute
)

type checkInPacer struct {
	mu                  sync.Mutex
	lastAttemptAt       time.Time
	consecutiveFailures int
}

var controlPlaneCheckInPacer = &checkInPacer{}

func recoveryCheckInInterval(consecutiveFailures int) time.Duration {
	interval := recoveryCheckInBaseInterval
	for i := 0; i < consecutiveFailures && interval < recoveryCheckInMaxInterval; i++ {
		interval *= 2
	}
	if interval > recoveryCheckInMaxInterval {
		interval = recoveryCheckInMaxInterval
	}
	return interval
}

// dueDuringRecovery reports whether a check-in should be attempted although
// recovery asked to skip control-plane work this cycle. Never right before
// the router reboots itself.
func (p *checkInPacer) dueDuringRecovery(now time.Time, phase recovery.Phase) bool {
	if phase == recovery.PhaseRebootWait {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.lastAttemptAt.IsZero() || now.Before(p.lastAttemptAt) {
		return true
	}
	return now.Sub(p.lastAttemptAt) >= recoveryCheckInInterval(p.consecutiveFailures)
}

// noteAttempt records a check-in attempt and counts it as failed until
// noteSuccess says otherwise, so every early return on the way counts.
func (p *checkInPacer) noteAttempt(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAttemptAt = now
	p.consecutiveFailures++
}

func (p *checkInPacer) noteSuccess() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.consecutiveFailures = 0
}
