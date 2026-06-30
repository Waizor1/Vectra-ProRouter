package rescue

import (
	"testing"
	"time"
)

// A non-zero fwmark must still produce a usable client. (Whether a custom
// transport is wired is platform-dependent — SO_MARK is Linux-only — so the
// transport assertion lives in probe_mark_linux_test.go.)
func TestNewHTTPProberWithFwmarkBuildsUsableClient(t *testing.T) {
	prober := NewHTTPProberWithFwmark(2*time.Second, 0x564354)
	if prober.Client == nil {
		t.Fatal("expected a usable http client when a fwmark is configured")
	}
}

// A zero fwmark (marking disabled) must behave exactly like the plain prober:
// the default Transport, no custom dial hook. This is what the RU/foreign/
// Telegram/YouTube/Instagram reachability probes rely on so they keep testing
// the real proxied client path.
func TestNewHTTPProberWithFwmarkZeroLeavesDefaultTransport(t *testing.T) {
	prober := NewHTTPProberWithFwmark(2*time.Second, 0)
	if prober.Client == nil {
		t.Fatal("expected a usable http client without a fwmark")
	}
	if prober.Client.Transport != nil {
		t.Fatalf("fwmark 0 must not install a custom transport, got %T", prober.Client.Transport)
	}
}

// The plain constructor used by the unmarked reachability probes must never
// stamp a mark, on any platform.
func TestNewHTTPProberLeavesDefaultTransport(t *testing.T) {
	prober := NewHTTPProber(2 * time.Second)
	if prober.Client == nil {
		t.Fatal("expected a usable http client")
	}
	if prober.Client.Transport != nil {
		t.Fatalf("the unmarked prober must not install a custom transport, got %T", prober.Client.Transport)
	}
}
