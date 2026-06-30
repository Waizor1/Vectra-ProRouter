//go:build linux

package rescue

import (
	"net/http"
	"testing"
	"time"
)

// On linux a configured fwmark must wire a custom transport whose DialContext
// stamps SO_MARK, so the panel-reachability probe egresses directly past the
// PassWall2 tproxy — the SAME path the control-plane check-in uses. Without this
// the probe would traverse the (possibly dead) proxy and falsely report the
// panel as blocked.
func TestNewHTTPProberWithFwmarkInstallsMarkedTransportOnLinux(t *testing.T) {
	prober := NewHTTPProberWithFwmark(2*time.Second, 0x564354)
	transport, ok := prober.Client.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected a *http.Transport carrying the mark dial hook, got %T", prober.Client.Transport)
	}
	if transport.DialContext == nil {
		t.Fatal("expected the marked transport to override DialContext so SO_MARK is stamped")
	}
}
