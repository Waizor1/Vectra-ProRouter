package main

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/vault"
)

const quietDoc = `{"outbounds":[{"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u1"}]}]}}],"routing":{"rules":[{"network":"tcp,udp","outboundTag":"de-1"}]}}`

func TestSameNodesComparesOutboundsAndRules(t *testing.T) {
	reordered := `{"routing":{"rules":[{"outboundTag":"de-1","network":"tcp,udp"}],"balancers":[{"tag":"B"}]},"outbounds":[{"settings":{"vnext":[{"port":443,"address":"203.0.113.9","users":[{"id":"u1"}]}]},"protocol":"vless","tag":"de-1"}]}`
	if !sameNodes([]byte(quietDoc), []byte(reordered)) {
		t.Fatal("the same outbounds and rules, other balancers and key order: same")
	}
	if sameNodes([]byte(quietDoc), []byte(`{"outbounds":[{"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u1"}]}]}}],"routing":{"rules":[]}}`)) {
		t.Fatal("other rules are not the same")
	}
	rotated := `{"outbounds":[{"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u2"}]}]}}]}`
	if sameNodes([]byte(quietDoc), []byte(rotated)) {
		t.Fatal("a rotated account is another node")
	}
	if sameNodes([]byte(quietDoc), []byte(`{"outbounds":[]}`)) || sameNodes([]byte("x"), []byte("x")) {
		t.Fatal("no outbounds or no document is never the same")
	}
}

func quietDaemon(t *testing.T) *daemon {
	t.Helper()
	dir := t.TempDir()
	prev := tzPaths
	tzPaths = []string{filepath.Join(dir, "TZ")} // none: UTC
	t.Cleanup(func() { tzPaths = prev })
	d := &daemon{cfg: agentcfg.Config{ProviderConfigPath: filepath.Join(dir, "provider-config.json"), StatePath: filepath.Join(dir, "state.json")}}
	if err := vault.WriteFile(d.cfg.ProviderConfigPath, []byte(quietDoc)); err != nil {
		t.Fatal(err)
	}
	return d
}

// A refresh that changes no node is held outside the night window and
// taken in it; a node changed, or the router out of the tunnel, is taken at
// once.
func TestTheDaemonsRefreshHoldsANodeNeutralDocumentForTheNight(t *testing.T) {
	d := quietDaemon(t)
	day := time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC)
	routingOnly := []byte(`{"outbounds":[{"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u1"}]}]}}],"routing":{"rules":[{"network":"tcp,udp","outboundTag":"de-1"}],"balancers":[{"tag":"BL","selector":["de-"]}]},"burstObservatory":{"subjectSelector":["de-"]}}`)
	newRule := []byte(`{"outbounds":[{"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u1"}]}]}}],"routing":{"rules":[{"domain":["geosite:youtube"],"outboundTag":"de-1"},{"network":"tcp,udp","outboundTag":"de-1"}]}}`)
	if !d.holdForQuiet(routingOnly, day) || d.pendingDoc == nil || !d.pendingSince.Equal(day) {
		t.Fatal("a routing-only change by day was not held")
	}
	night := time.Date(2026, 10, 19, 3, 30, 0, 0, time.UTC)
	d.pendingDoc = nil
	if d.holdForQuiet(routingOnly, night) {
		t.Fatal("held in the night window")
	}
	rotated := []byte(`{"outbounds":[{"tag":"de-1","protocol":"vless","settings":{"vnext":[{"address":"203.0.113.9","port":443,"users":[{"id":"u2"}]}]}}]}`)
	if d.holdForQuiet(rotated, day) {
		t.Fatal("a node change was held")
	}
	if d.holdForQuiet(newRule, day) {
		t.Fatal("a rule change was held")
	}
	if d.holdForQuiet([]byte(quietDoc), day) {
		t.Fatal("the running document itself was held")
	}
	d.st.Rescue.Mode = string(rescue.ModeDirect)
	if d.holdForQuiet(routingOnly, day) {
		t.Fatal("held while the router is out of the tunnel")
	}
}

// A held document waits for the window or quietHoldMax, then is applied.
func TestAHeldDocumentWaitsForTheWindowOrTheMaximum(t *testing.T) {
	d := quietDaemon(t)
	applied := 0
	prev := applyHeldDoc
	applyHeldDoc = func(*daemon, context.Context, []byte) { applied++ }
	t.Cleanup(func() { applyHeldDoc = prev })
	day := time.Date(2026, 10, 18, 14, 0, 0, 0, time.UTC)
	base := sha256.Sum256([]byte(quietDoc))
	d.pendingDoc, d.pendingSince, d.pendingBase = []byte(quietDoc), day, base
	d.maybeApplyHeld(context.Background(), day.Add(time.Hour))
	if d.pendingDoc == nil {
		t.Fatal("applied an hour later by day")
	}
	d.maybeApplyHeld(context.Background(), day.Add(10*time.Hour)) // 00:00: not yet
	if d.pendingDoc == nil {
		t.Fatal("applied in the evening, before the window")
	}
	d.maybeApplyHeld(context.Background(), day.Add(quietHoldMax))
	if d.pendingDoc != nil || applied != 1 {
		t.Fatalf("still held after the maximum (applied %d)", applied)
	}
	d.pendingDoc, d.pendingSince, d.pendingBase = []byte(quietDoc), day, base
	d.maybeApplyHeld(context.Background(), time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC))
	if d.pendingDoc != nil || applied != 2 {
		t.Fatalf("still held in the night window (applied %d)", applied)
	}
	// Another document ran since it was held (the owner chose another
	// location): the held one is dropped, never applied over it.
	d.pendingDoc, d.pendingSince, d.pendingBase = []byte(quietDoc), day, base
	if err := vault.WriteFile(d.cfg.ProviderConfigPath, []byte(`{"outbounds":[{"tag":"fi-1","protocol":"vless"}]}`)); err != nil {
		t.Fatal(err)
	}
	d.maybeApplyHeld(context.Background(), time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC))
	if d.pendingDoc != nil || applied != 2 {
		t.Fatalf("a stale held document was applied (applied %d)", applied)
	}
}

func TestTheQuietWindowIsTheRoutersNight(t *testing.T) {
	prev := tzPaths
	tzPaths = []string{filepath.Join(t.TempDir(), "TZ")}
	t.Cleanup(func() { tzPaths = prev })
	for h, want := range map[int]bool{2: false, 3: true, 5: true, 6: false, 14: false} {
		if got := inQuietWindow(time.Date(2026, 10, 18, h, 30, 0, 0, time.UTC)); got != want {
			t.Errorf("%02d:30: %v", h, got)
		}
	}
}
