package supervisor

import (
	"math"
	"time"
)

// BackoffState computes the next restart delay using an exponential policy
// with a "stable-uptime reset" rule: if the previous run stayed up longer
// than ResetAfter, backoff is reset to InitialMs.
type BackoffState struct {
	InitialMs int
	MaxMs     int
	Factor    float64
	ResetAfter time.Duration

	currentMs int
	attempt   int
}

// NewBackoff returns a fresh policy at the initial delay.
func NewBackoff(initialMs, maxMs int, factor float64, resetAfter time.Duration) *BackoffState {
	if initialMs <= 0 {
		initialMs = 500
	}
	if maxMs < initialMs {
		maxMs = initialMs
	}
	if factor < 1 {
		factor = 2
	}
	return &BackoffState{
		InitialMs:  initialMs,
		MaxMs:      maxMs,
		Factor:     factor,
		ResetAfter: resetAfter,
		currentMs:  initialMs,
	}
}

// Next returns the current delay and prepares the next one.
func (b *BackoffState) Next() time.Duration {
	d := time.Duration(b.currentMs) * time.Millisecond
	// Prepare next
	nextMs := int(math.Min(float64(b.MaxMs), float64(b.currentMs)*b.Factor))
	b.currentMs = nextMs
	b.attempt++
	return d
}

// Reset resets the backoff back to InitialMs (call after a stable run).
func (b *BackoffState) Reset() {
	b.currentMs = b.InitialMs
	b.attempt = 0
}

// Attempt returns the number of times Next() has been called.
func (b *BackoffState) Attempt() int { return b.attempt }

// CurrentMs returns the delay that the next Next() call will return.
func (b *BackoffState) CurrentMs() int { return b.currentMs }

// MaybeResetAfter checks whether a run that lasted `uptime` should reset
// the backoff. Returns true if it did.
func (b *BackoffState) MaybeResetAfter(uptime time.Duration) bool {
	if b.ResetAfter > 0 && uptime >= b.ResetAfter {
		b.Reset()
		return true
	}
	return false
}
