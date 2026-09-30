package jobsafety

import (
	"testing"

	"vectra-controller-pro/internal/controlplane"
)

func TestClassify(t *testing.T) {
	cases := map[string]Class{
		"apply_xray_config":          ClassHeavy,
		"refresh_xray_subscriptions": ClassHeavy,
		"update_xray_assets":         ClassHeavy,
		"update_controller":          ClassStorage,
		"collect_router_logs":        ClassDiagnostic,
		"reload_xray_outbound":       ClassNone,
		"reconnect":                  ClassNone,
		"totally_unknown":            ClassNone,
	}
	for jt, want := range cases {
		if got := Classify(jt); got != want {
			t.Errorf("Classify(%q) = %q, want %q", jt, got, want)
		}
	}
}

func TestEvaluateBlocksLowMemoryHeavy(t *testing.T) {
	res := controlplane.RouterResources{MemoryAvailableMB: 20, OverlayFreeMB: 50, TMPFreeMB: 50}
	d := Evaluate("apply_xray_config", res, DefaultConfig())
	if !d.Blocked || d.Code != "router_resource_guard" {
		t.Fatalf("expected block, got %+v", d)
	}
	if len(d.Reasons) == 0 {
		t.Error("expected a reason")
	}
}

func TestEvaluateUnknownReadingDoesNotBlock(t *testing.T) {
	// 0 means "unknown" (e.g. dev host) — must not block.
	res := controlplane.RouterResources{MemoryAvailableMB: 0, OverlayFreeMB: 0, TMPFreeMB: 0}
	if d := Evaluate("apply_xray_config", res, DefaultConfig()); d.Blocked {
		t.Errorf("unknown readings should not block: %+v", d)
	}
}

func TestEvaluateNoneClassNeverBlocks(t *testing.T) {
	res := controlplane.RouterResources{MemoryAvailableMB: 1, OverlayFreeMB: 1, TMPFreeMB: 1}
	if d := Evaluate("reload_xray_outbound", res, DefaultConfig()); d.Blocked {
		t.Errorf("none-class blocked: %+v", d)
	}
}

func TestEvaluateStorageOverlayFloor(t *testing.T) {
	res := controlplane.RouterResources{MemoryAvailableMB: 128, OverlayFreeMB: 4, TMPFreeMB: 128}
	d := Evaluate("update_controller", res, DefaultConfig())
	if !d.Blocked {
		t.Fatalf("expected overlay block for storage class, got %+v", d)
	}
}

func TestWithDefaultsFillsZeros(t *testing.T) {
	c := Config{HeavyMemoryFloorMB: 99}.WithDefaults()
	if c.HeavyMemoryFloorMB != 99 {
		t.Error("explicit floor overwritten")
	}
	if c.StorageMemoryFloorMB != 64 {
		t.Errorf("default not filled: %d", c.StorageMemoryFloorMB)
	}
}
