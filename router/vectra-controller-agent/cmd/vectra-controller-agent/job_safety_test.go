package main

import (
	"strings"
	"testing"
	"time"

	"vectra-controller-agent/internal/controlplane"
)

func TestEvaluateJobSafetyBlocksHeavyJobUnderLowMemory(t *testing.T) {
	decision := evaluateJobSafety(
		controlplane.Job{ID: "job-1", Type: "refresh_rules"},
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 37,
			MemoryTotalMB:     234,
			OverlayFreeMB:     40,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)

	if !decision.Blocked {
		t.Fatalf("expected low-memory refresh_rules job to be blocked")
	}
	if decision.Code != "router_resource_guard" {
		t.Fatalf("unexpected decision code %q", decision.Code)
	}
	if !strings.Contains(decision.Message, "available RAM 37 MB") {
		t.Fatalf("expected low-memory reason, got %q", decision.Message)
	}
}

func TestEvaluateJobSafetyAllowsHeavyJobAtOperationalLowMemory(t *testing.T) {
	decision := evaluateJobSafety(
		controlplane.Job{ID: "job-1", Type: "refresh_subscriptions"},
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 48,
			MemoryTotalMB:     234,
			OverlayFreeMB:     40,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)

	if decision.Blocked {
		t.Fatalf("expected subscription refresh to stay allowed at 48 MB, got %#v", decision)
	}
}

func TestEvaluateJobSafetyKeepsStorageJobMemoryFloorConservative(t *testing.T) {
	decision := evaluateJobSafety(
		controlplane.Job{ID: "job-1", Type: "update_passwall_packages"},
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 52,
			MemoryTotalMB:     234,
			OverlayFreeMB:     40,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)

	if !decision.Blocked {
		t.Fatalf("expected storage job to keep blocking below 64 MB")
	}
	if !strings.Contains(decision.Message, "available RAM 52 MB is below 64 MB floor") {
		t.Fatalf("expected storage memory floor reason, got %q", decision.Message)
	}
}

func TestClassifyEnsurePasswallRuntimeAsStorageJob(t *testing.T) {
	job := controlplane.Job{ID: "job-ensure-runtime", Type: "ensure_passwall_runtime"}

	if got := classifyJobSafety(job, nil); got != jobSafetyClassStorage {
		t.Fatalf("ensure_passwall_runtime class = %q, want storage", got)
	}
}

func TestClassifyOptimizationBaselineAsDiagnosticJob(t *testing.T) {
	job := controlplane.Job{ID: "job-optimization-baseline", Type: "collect_optimization_baseline"}

	if got := classifyJobSafety(job, nil); got != jobSafetyClassDiagnostic {
		t.Fatalf("collect_optimization_baseline class = %q, want diagnostic", got)
	}
}

func TestEvaluateJobSafetyBlocksStorageJobWhenSpaceUnknown(t *testing.T) {
	decision := evaluateJobSafety(
		controlplane.Job{ID: "job-1", Type: "update_passwall_packages"},
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 96,
			MemoryTotalMB:     234,
			OverlayFreeMB:     0,
			TMPFreeMB:         64,
		},
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)

	if !decision.Blocked {
		t.Fatalf("expected storage job with unknown /overlay to be blocked")
	}
	if got, want := decision.Class, jobSafetyClassStorage; got != want {
		t.Fatalf("decision class = %q, want %q", got, want)
	}
	if !strings.Contains(strings.Join(decision.Reasons, "; "), "/overlay free space is unknown") {
		t.Fatalf("expected overlay reason, got %#v", decision.Reasons)
	}
}

func TestEvaluateJobSafetyAllowsRouterRebootTerminalUnderLowMemory(t *testing.T) {
	decision := evaluateJobSafety(
		controlplane.Job{
			ID:   "job-1",
			Type: "run_terminal_command",
			Payload: map[string]interface{}{
				"purpose": "router-reboot",
			},
		},
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 12,
			OverlayFreeMB:     0,
			TMPFreeMB:         0,
		},
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)

	if decision.Blocked {
		t.Fatalf("expected explicit router reboot terminal job to stay allowed, got %#v", decision)
	}
}

func TestEvaluateJobSafetyWithResourceCollectorSkipsUnguardedJob(t *testing.T) {
	called := false
	decision := evaluateJobSafetyWithResourceCollector(
		controlplane.Job{
			ID:   "job-1",
			Type: "run_terminal_command",
			Payload: map[string]interface{}{
				"purpose": "router-reboot",
			},
		},
		nil,
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		func() controlplane.RouterResources {
			called = true
			return controlplane.RouterResources{
				MemoryAvailableMB: 12,
				OverlayFreeMB:     0,
				TMPFreeMB:         0,
			}
		},
		JobSafetyTuning{},
	)

	if called {
		t.Fatalf("expected unguarded job to skip resource collection")
	}
	if decision.Blocked {
		t.Fatalf("expected explicit router reboot terminal job to stay allowed, got %#v", decision)
	}
	if got, want := decision.Class, jobSafetyClassNone; got != want {
		t.Fatalf("decision class = %q, want %q", got, want)
	}
}

func TestEvaluateJobSafetyUsesControllerOverlayFloorForTerminalSelfUpdate(t *testing.T) {
	selfUpdateJobs := []controlplane.Job{
		{
			ID:   "job-controller-self-update",
			Type: "run_terminal_command",
			Payload: map[string]interface{}{
				"purpose": controllerSelfUpdateTerminalPurpose,
			},
		},
		{
			ID:   "job-controller-self-update-compat",
			Type: "run_terminal_command",
			Payload: map[string]interface{}{
				"purpose": controllerSelfUpdateCompatTerminalPurpose,
			},
		},
	}

	for _, job := range selfUpdateJobs {
		t.Run(payloadString(job.Payload, "purpose"), func(t *testing.T) {
			if got := classifyJobSafety(job, nil); got != jobSafetyClassStorage {
				t.Fatalf("terminal controller self-update class = %q, want storage", got)
			}

			allowed := evaluateJobSafety(
				job,
				nil,
				controlplane.RouterResources{
					MemoryAvailableMB: 96,
					OverlayFreeMB:     12,
					TMPFreeMB:         64,
				},
				time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
				JobSafetyTuning{},
			)
			if allowed.Blocked {
				t.Fatalf("expected terminal controller self-update to use controller overlay floor, got %#v", allowed)
			}

			blocked := evaluateJobSafety(
				job,
				nil,
				controlplane.RouterResources{
					MemoryAvailableMB: 96,
					OverlayFreeMB:     7,
					TMPFreeMB:         64,
				},
				time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
				JobSafetyTuning{},
			)
			if !blocked.Blocked {
				t.Fatalf("expected terminal controller self-update below controller overlay floor to be blocked")
			}
			if !strings.Contains(strings.Join(blocked.Reasons, "; "), "/overlay free 7 MB is below 8 MB floor") {
				t.Fatalf("expected controller overlay floor reason, got %#v", blocked.Reasons)
			}
		})
	}
}

func TestEvaluateJobSafetyHonorsStorageMemoryFloorOverride(t *testing.T) {
	// Default storage floor is 64 MB; an operator lowering it to 40 MB should
	// let an update_controller job through at 48 MB even though it sits below
	// the compile-time default. This is what 91_vectra_low_mem_profile uses to
	// let r26+ self-update reach AX3000T-class boxes where MemAvailable
	// hovers in the 40-60 MB band.
	decision := evaluateJobSafety(
		controlplane.Job{ID: "job-1", Type: "update_controller"},
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 48,
			MemoryTotalMB:     234,
			OverlayFreeMB:     40,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		JobSafetyTuning{StorageMemoryFloorMB: 40},
	)

	if decision.Blocked {
		t.Fatalf("expected update_controller at 48 MB to pass with lowered 40 MB floor, got %#v", decision)
	}
}

func TestEvaluateJobSafetyDefaultStorageFloorStillBlocks(t *testing.T) {
	// Sanity-check the inverse: without an override, the default 64 MB floor
	// still blocks the same job at 48 MB. Guards the regression that override
	// is the only path to bypass.
	decision := evaluateJobSafety(
		controlplane.Job{ID: "job-1", Type: "update_controller"},
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 48,
			MemoryTotalMB:     234,
			OverlayFreeMB:     40,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)

	if !decision.Blocked {
		t.Fatalf("expected default storage floor to block 48 MB, got %#v", decision)
	}
	if !strings.Contains(decision.Message, "is below 64 MB floor") {
		t.Fatalf("expected 64 MB floor reason, got %q", decision.Message)
	}
}

func TestEvaluateJobSafetyWithResourceCollectorRetriesAfterDropCaches(t *testing.T) {
	// Simulate the AX3000T-class scenario: first read shows 48 MB (below the
	// 64 MB storage floor), pre_drop_caches kicks in, second read shows 80 MB
	// (above floor) and the guard lets the job through.
	//
	// We can't actually write to /proc/sys/vm/drop_caches in unit tests, so
	// attemptDropCaches will fail silently on hosts that lack the file; the
	// retry path still re-reads via the collector, which is what we verify.
	calls := 0
	collector := func() controlplane.RouterResources {
		calls++
		if calls == 1 {
			return controlplane.RouterResources{
				MemoryAvailableMB: 48, OverlayFreeMB: 40, TMPFreeMB: 80,
			}
		}
		return controlplane.RouterResources{
			MemoryAvailableMB: 80, OverlayFreeMB: 40, TMPFreeMB: 80,
		}
	}

	decision := evaluateJobSafetyWithResourceCollector(
		controlplane.Job{ID: "job-1", Type: "update_controller"},
		nil,
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		collector,
		JobSafetyTuning{PreDropCaches: true},
	)

	if calls < 2 {
		t.Fatalf("expected collector to be called twice (pre + post drop_caches), got %d", calls)
	}
	if decision.Blocked {
		t.Fatalf("expected post-drop_caches reading 80 MB to clear the floor, got %#v", decision)
	}
}

func TestEvaluateJobSafetyWithResourceCollectorSkipsDropCachesWhenDisabled(t *testing.T) {
	// Inverse: when PreDropCaches=false, the collector is called exactly once
	// regardless of the first reading.
	calls := 0
	collector := func() controlplane.RouterResources {
		calls++
		return controlplane.RouterResources{
			MemoryAvailableMB: 48, OverlayFreeMB: 40, TMPFreeMB: 80,
		}
	}

	evaluateJobSafetyWithResourceCollector(
		controlplane.Job{ID: "job-1", Type: "update_controller"},
		nil,
		time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		collector,
		JobSafetyTuning{},
	)

	if calls != 1 {
		t.Fatalf("expected collector to be called exactly once when PreDropCaches is off, got %d", calls)
	}
}

func TestClassifyApplyPasswallConfigOnlyGuardsHeavyImpact(t *testing.T) {
	job := controlplane.Job{ID: "job-1", Type: "apply_passwall_config"}

	if got := classifyJobSafety(job, &controlplane.DesiredRevisionSummary{}); got != jobSafetyClassNone {
		t.Fatalf("light apply class = %q, want none", got)
	}

	if got := classifyJobSafety(job, &controlplane.DesiredRevisionSummary{
		Impact: controlplane.DesiredRevisionImpact{
			RequiresRestart: true,
		},
	}); got != jobSafetyClassHeavy {
		t.Fatalf("restart apply class = %q, want heavy", got)
	}
}

func TestClassifyRescueRepairGuardsOnlyHeavyActions(t *testing.T) {
	lightJob := controlplane.Job{
		ID:   "job-1",
		Type: "run_rescue_repair",
		Payload: map[string]interface{}{
			"actions": []interface{}{"restart_passwall", "restart_dnsmasq", "reconnect_proxy"},
		},
	}
	if got := classifyJobSafety(lightJob, nil); got != jobSafetyClassNone {
		t.Fatalf("light rescue repair class = %q, want none", got)
	}

	heavyJob := controlplane.Job{
		ID:   "job-2",
		Type: "run_rescue_repair",
		Payload: map[string]interface{}{
			"actions": []interface{}{"restart_passwall", "refresh_rules"},
		},
	}
	if got := classifyJobSafety(heavyJob, nil); got != jobSafetyClassHeavy {
		t.Fatalf("heavy rescue repair class = %q, want heavy", got)
	}
}

// makePasswallUpdateJobWithManifest builds a job payload that mirrors the
// real shape the panel emits for update_passwall_packages, with one
// packageArtifacts entry per package and installedSizeBytes set. Used by
// the manifest-aware overlay-floor tests below to avoid duplicating the
// inline JSON structure.
func makePasswallUpdateJobWithManifest(installedSizeBytesPerPackage []int64) controlplane.Job {
	artifacts := make([]interface{}, 0, len(installedSizeBytesPerPackage))
	for i, size := range installedSizeBytesPerPackage {
		artifacts = append(artifacts, map[string]interface{}{
			"name":               "pkg-" + string(rune('a'+i)),
			"artifactUrl":        "https://example.test/pkg.ipk",
			"installedSizeBytes": size,
		})
	}
	return controlplane.Job{
		ID:   "job-manifest",
		Type: "update_passwall_packages",
		Payload: map[string]interface{}{
			"packageArtifacts": artifacts,
		},
	}
}

func TestManifestOverlayFloorRelaxesStaticFloorForSmallUpgrade(t *testing.T) {
	// Real-world AX3000T case: PassWall2 upgrade payload sums to ~67 MB
	// installedSizeBytes (xray-core 30 + v2ray-geoip 19 + v2ray-geosite 10 +
	// geoview 7 + chinadns-ng 0.5 + tcping 0.07 + luci-app-passwall2 1.3).
	// /3 ≈ 22 MB UBIFS compressed worst case, +2 MB headroom = 24 MB.
	// Static storage floor is 16 MB — manifest is HIGHER, so we use the
	// manifest value and the router with 12 MB free still blocks.
	job := makePasswallUpdateJobWithManifest([]int64{
		30 * 1024 * 1024, // xray-core
		19 * 1024 * 1024, // v2ray-geoip
		10 * 1024 * 1024, // v2ray-geosite
		7 * 1024 * 1024,  // geoview
		512 * 1024,       // chinadns-ng
		70 * 1024,        // tcping
		1300 * 1024,      // luci-app-passwall2
	})

	// Verify the manifest computation: 67_882_982 / 3 = 22_627_660 bytes ≈ 21 MB
	// Plus 2 MB headroom = 23 MB. (Integer division loses a bit; assert >= 21.)
	got := manifestOverlayFloorMB(job)
	if got < 21 || got > 25 {
		t.Fatalf("manifestOverlayFloorMB returned %d MB, expected 21-25 MB for ~68 MB total payload", got)
	}

	// Now the inverse case: a small upgrade (just luci-app-passwall2, 1.3 MB)
	// should produce a manifest floor BELOW the static 16 MB floor, letting
	// the router with 12 MB free pass.
	smallJob := makePasswallUpdateJobWithManifest([]int64{
		1300 * 1024, // luci-app-passwall2 only
	})
	if got := manifestOverlayFloorMB(smallJob); got >= 16 {
		t.Fatalf("small upgrade manifest floor = %d MB, expected < 16 MB", got)
	}

	decision := evaluateJobSafety(
		smallJob,
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 80,
			OverlayFreeMB:     12, // below static 16 MB but above manifest-derived ~5 MB
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)
	if decision.Blocked {
		t.Fatalf("expected small-payload manifest-aware floor to let 12 MB pass, got %#v", decision)
	}
}

func TestManifestOverlayFloorClampsToAbsoluteMinimum(t *testing.T) {
	// A trivially small upgrade (1 KB installed size) should still demand at
	// least jobSafetyAbsoluteMinimumOverlayMB to leave room for opkg's own
	// staging files. Otherwise the router could fill the overlay mid-install.
	job := makePasswallUpdateJobWithManifest([]int64{1024})

	if got := manifestOverlayFloorMB(job); got != jobSafetyAbsoluteMinimumOverlayMB {
		t.Fatalf("trivial-payload manifest floor = %d MB, want %d MB absolute minimum",
			got, jobSafetyAbsoluteMinimumOverlayMB)
	}

	decision := evaluateJobSafety(
		job,
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 80,
			OverlayFreeMB:     3, // below absolute minimum of 4
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)
	if !decision.Blocked {
		t.Fatalf("expected 3 MB overlay to be blocked even with tiny manifest, got %#v", decision)
	}
}

func TestManifestOverlayFloorIgnoredWhenNoManifest(t *testing.T) {
	// Legacy panel payloads or scoped-package single-binary jobs may not ship
	// packageArtifacts. The static class floor must apply unchanged in that
	// case — we never want to "relax" a job below the static default just
	// because the manifest is missing.
	legacyJob := controlplane.Job{
		ID:      "job-legacy",
		Type:    "update_passwall_packages",
		Payload: map[string]interface{}{
			// No packageArtifacts, only legacy fields.
			"artifactUrl":   "https://example.test/legacy.ipk",
			"targetVersion": "26.5.1-r1",
		},
	}

	if got := manifestOverlayFloorMB(legacyJob); got != 0 {
		t.Fatalf("legacy job without manifest returned floor %d, expected 0 (fall back to static)", got)
	}

	decision := evaluateJobSafety(
		legacyJob,
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 80,
			OverlayFreeMB:     12,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)
	if !decision.Blocked {
		t.Fatalf("expected legacy job at 12 MB overlay to hit static 16 MB floor, got %#v", decision)
	}
	if !strings.Contains(decision.Message, "/overlay free 12 MB is below 16 MB floor") {
		t.Fatalf("expected static 16 MB floor message, got %q", decision.Message)
	}
}

func TestManifestOverlayFloorOnlyAppliesToPasswallUpdate(t *testing.T) {
	// Other storage-class jobs (update_controller, validate_firmware,
	// ensure_passwall_runtime) keep using the static floor — they don't ship
	// per-package manifests in the same shape.
	for _, jobType := range []string{
		"update_controller",
		"validate_firmware",
		"ensure_passwall_runtime",
	} {
		job := controlplane.Job{
			ID:   "job-" + jobType,
			Type: jobType,
			Payload: map[string]interface{}{
				"packageArtifacts": []interface{}{
					map[string]interface{}{
						"name":               "anything",
						"artifactUrl":        "https://example.test/x.ipk",
						"installedSizeBytes": int64(1024),
					},
				},
			},
		}
		if got := manifestOverlayFloorMB(job); got != 0 {
			t.Fatalf("job %s returned manifest floor %d, expected 0 (only update_passwall_packages opts in)",
				jobType, got)
		}
	}
}

func TestForceOverlayBypassDropsOverlayReasonButKeepsMemoryAndTmp(t *testing.T) {
	// Operator escape hatch: when forceOverlayBypass=true in the payload, the
	// overlay floor stops contributing to the block decision. Memory and TMP
	// floors still apply — those guard against hard failure modes that bypass
	// can't paper over (OOM, opkg staging IO error). The bypassed-overlay
	// case still emits a WARN log line for audit (asserted indirectly: the
	// decision message should NOT include the /overlay reason).
	job := controlplane.Job{
		ID:   "job-bypass",
		Type: "update_passwall_packages",
		Payload: map[string]interface{}{
			"forceOverlayBypass": true,
		},
	}

	decision := evaluateJobSafety(
		job,
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 80, // above 64 MB storage floor
			OverlayFreeMB:     3,  // far below static 16 MB
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)
	if decision.Blocked {
		t.Fatalf("expected forceOverlayBypass to let 3 MB overlay pass, got %#v", decision)
	}

	// Memory still blocks: bypass only covers overlay.
	memBlockedJob := controlplane.Job{
		ID:   "job-bypass-2",
		Type: "update_passwall_packages",
		Payload: map[string]interface{}{
			"forceOverlayBypass": true,
		},
	}
	memDecision := evaluateJobSafety(
		memBlockedJob,
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 32, // below 64 MB storage floor
			OverlayFreeMB:     3,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)
	if !memDecision.Blocked {
		t.Fatalf("expected memory floor to still block with forceOverlayBypass, got %#v", memDecision)
	}
	if !strings.Contains(memDecision.Message, "available RAM 32 MB") {
		t.Fatalf("expected memory reason to remain, got %q", memDecision.Message)
	}
	if strings.Contains(memDecision.Message, "/overlay free") {
		t.Fatalf("forceOverlayBypass should drop overlay reason entirely, got %q", memDecision.Message)
	}
}

func TestForceOverlayBypassDefaultsToFalseAndBlocks(t *testing.T) {
	// Sanity-check the inverse: without the flag, the static floor still blocks.
	// Also locks in that the bypass field defaults to false (no string
	// "true"/"1" coercion).
	job := controlplane.Job{
		ID:      "job-default",
		Type:    "update_passwall_packages",
		Payload: map[string]interface{}{
			// No forceOverlayBypass field.
		},
	}

	decision := evaluateJobSafety(
		job,
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 80,
			OverlayFreeMB:     3,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)
	if !decision.Blocked {
		t.Fatalf("expected default behavior to block 3 MB overlay, got %#v", decision)
	}
}

func TestOverlayGuardErrorMessageIncludesTuningKnobHint(t *testing.T) {
	// Operability check: when the overlay guard blocks, the error message
	// must surface (a) current free, (b) configured floor, and (c) the UCI
	// option name an operator can set to lower the floor. Without (c) the
	// recovery path requires reading source code — a real ops pain point on
	// AX3000T routers where this fires routinely.
	decision := evaluateJobSafety(
		controlplane.Job{ID: "job-1", Type: "update_passwall_packages"},
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 80,
			OverlayFreeMB:     5,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)
	if !decision.Blocked {
		t.Fatalf("expected 5 MB overlay to be blocked")
	}
	// Static substring kept verbatim — many downstream log scrapers and the
	// self-heal tests still grep for this exact pattern.
	if !strings.Contains(decision.Message, "/overlay free 5 MB is below 16 MB floor") {
		t.Fatalf("expected core overlay reason verbatim, got %q", decision.Message)
	}
	// Storage-class jobs get the storage-overlay knob hint.
	if !strings.Contains(decision.Message, "job_safety_storage_overlay_floor_mb") {
		t.Fatalf("expected storage-overlay UCI knob hint, got %q", decision.Message)
	}
	if !strings.Contains(decision.Message, "forceOverlayBypass=true") {
		t.Fatalf("expected forceOverlayBypass hint, got %q", decision.Message)
	}

	// Heavy-class jobs (refresh_subscriptions) get the heavy-overlay knob hint.
	heavyDecision := evaluateJobSafety(
		controlplane.Job{ID: "job-2", Type: "refresh_subscriptions"},
		nil,
		controlplane.RouterResources{
			MemoryAvailableMB: 80,
			OverlayFreeMB:     5,
			TMPFreeMB:         80,
		},
		time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
		JobSafetyTuning{},
	)
	if !heavyDecision.Blocked {
		t.Fatalf("expected 5 MB overlay to block refresh_subscriptions (default heavy floor is 8 MB)")
	}
	if !strings.Contains(heavyDecision.Message, "job_safety_heavy_overlay_floor_mb") {
		t.Fatalf("expected heavy-overlay UCI knob hint, got %q", heavyDecision.Message)
	}
}
