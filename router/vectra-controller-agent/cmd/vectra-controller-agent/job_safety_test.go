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
