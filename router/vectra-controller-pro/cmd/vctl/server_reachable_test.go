package main

import (
	"context"
	"encoding/json"
	"testing"

	"vectra-controller-pro/internal/controlplane"
)

// decodeCheckInHealth pulls the health block and panelReachability out of the
// last check-in BODY, so these assertions are about the bytes the panel
// receives and not about an in-memory struct.
func decodeCheckInHealth(t *testing.T, raw []byte) (controlplane.RouterHealth, *controlplane.RouterReachabilityProbe) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatal("no check-in request was captured")
	}
	var req struct {
		Health    controlplane.RouterHealth `json:"health"`
		Inventory struct {
			PanelReachability *controlplane.RouterReachabilityProbe `json:"panelReachability"`
		} `json:"inventory"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decode check-in body: %v", err)
	}
	return req.Health, req.Inventory.PanelReachability
}

// THE REGRESSION. serverReachable used to be measured with a bare
// &http.Client{} against /healthz and /api/health. Two things are wrong with
// that at once, and this panel reproduces both: it serves register and check-in
// and 404s everything else, exactly like a panel whose health endpoint is not
// where the router guesses.
//
//   - The socket is wrong. On a router with the data plane loaded, the output
//     chain stamps FwMark on unmarked local egress and the fwmark policy route
//     sends it to `local ... dev lo`, so the probe cannot leave the box at all.
//   - The endpoint is a guess. Even off a router, a panel that does not answer
//     those two paths makes the probe fail while the control plane works.
//
// Either way the field read false on a router that was talking to its panel
// perfectly well, every single loop.
func TestServerReachableComesFromTheControlPlaneItself(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyState(t, dir)
	panel := newCapturingPanel(t)
	d := newIdentityDaemon(t, dir, panel.URL, legacy)

	// Loop 1: nothing has been exchanged yet, so the answer is still the
	// fallback probe — and this panel 404s it. "Unknown" is reported as not
	// reachable, but panelReachability.status is left UNSET rather than
	// guessing "blocked" into an operator-facing column.
	if err := d.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce (first): %v", err)
	}
	health, reach := decodeCheckInHealth(t, panel.body("/api/router/check-in"))
	if reach == nil {
		t.Fatal("no panelReachability on the wire")
	}
	if reach.Status != "" {
		t.Errorf("first loop must not assert a panel status, got %q", reach.Status)
	}
	_ = health

	// Loop 2: the first check-in DID reach the panel, so that is what gets
	// reported — regardless of what /healthz does.
	if err := d.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce (second): %v", err)
	}
	health, reach = decodeCheckInHealth(t, panel.body("/api/router/check-in"))
	if !health.ServerReachable {
		t.Error("REGRESSION: serverReachable is false on the wire after a check-in that succeeded")
	}
	if reach == nil || !reach.Reachable {
		t.Errorf("panelReachability.reachable is false after a successful check-in: %+v", reach)
	}
	if reach.Status != "reachable" {
		t.Errorf("panelReachability.status = %q, want \"reachable\" — the panel reads exactly this field", reach.Status)
	}
}

// The other direction, or the field would just be a constant `true`.
func TestServerReachableIsFalseAfterTheControlPlaneFails(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyState(t, dir)
	// Nothing listens here: the check-in cannot complete.
	d := newIdentityDaemon(t, dir, "http://127.0.0.1:1", legacy)

	if err := d.runOnce(context.Background()); err == nil {
		t.Fatal("runOnce succeeded against a dead panel")
	}
	if d.lastControlPlaneOK == nil || *d.lastControlPlaneOK {
		t.Fatalf("a failed check-in was not recorded: %v", d.lastControlPlaneOK)
	}

	inv := controlplane.RouterInventory{}
	health, _ := d.evaluateHealth(context.Background(), &inv)
	if health.ServerReachable {
		t.Error("serverReachable is true after the check-in failed")
	}
	if inv.PanelReachability == nil || inv.PanelReachability.Status != "blocked" {
		t.Errorf("panelReachability.status = %+v, want \"blocked\"", inv.PanelReachability)
	}
}
