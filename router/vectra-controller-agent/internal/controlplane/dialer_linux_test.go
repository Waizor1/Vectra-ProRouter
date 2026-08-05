//go:build linux

package controlplane

import (
	"net/http"
	"testing"
)

// On linux a configured fwmark must wire a custom transport whose DialContext
// stamps SO_MARK, so the control-plane client egresses directly past the
// PassWall2 tproxy. (The shared marking primitive itself is unit-tested in
// internal/netmark; this asserts NewClient integrates it.)
func TestNewClientInstallsMarkedTransportOnLinux(t *testing.T) {
	c := NewClient(Options{BaseURL: "http://example", Fwmark: 0x564354})
	transport, ok := c.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected a *http.Transport carrying the mark dial hook, got %T", c.httpClient.Transport)
	}
	if transport.DialContext == nil {
		t.Fatal("expected the marked transport to override DialContext so SO_MARK is stamped")
	}
}
