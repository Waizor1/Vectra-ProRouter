package supervisor

import (
	"testing"
	"time"
)

func TestBackoff_Exponential(t *testing.T) {
	b := NewBackoff(500, 8000, 2.0, 60*time.Second)
	got := []int{}
	for i := 0; i < 6; i++ {
		got = append(got, int(b.Next()/time.Millisecond))
	}
	want := []int{500, 1000, 2000, 4000, 8000, 8000}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("step %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestBackoff_ResetAfterStableUptime(t *testing.T) {
	b := NewBackoff(500, 8000, 2.0, 60*time.Second)
	_ = b.Next()
	_ = b.Next()
	if !b.MaybeResetAfter(120 * time.Second) {
		t.Fatalf("should reset")
	}
	if b.CurrentMs() != 500 {
		t.Fatalf("expected reset to 500, got %d", b.CurrentMs())
	}
}

func TestBackoff_NoResetOnShortUptime(t *testing.T) {
	b := NewBackoff(500, 8000, 2.0, 60*time.Second)
	_ = b.Next()
	_ = b.Next()
	if b.MaybeResetAfter(10 * time.Second) {
		t.Fatalf("should not reset")
	}
}
