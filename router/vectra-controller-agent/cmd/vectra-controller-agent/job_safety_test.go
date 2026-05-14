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
	)

	if !decision.Blocked {
		t.Fatalf("expected storage job to keep blocking below 64 MB")
	}
	if !strings.Contains(decision.Message, "available RAM 52 MB is below 64 MB floor") {
		t.Fatalf("expected storage memory floor reason, got %q", decision.Message)
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
	job := controlplane.Job{
		ID:   "job-1",
		Type: "run_terminal_command",
		Payload: map[string]interface{}{
			"purpose": controllerSelfUpdateTerminalPurpose,
		},
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
	)
	if !blocked.Blocked {
		t.Fatalf("expected terminal controller self-update below controller overlay floor to be blocked")
	}
	if !strings.Contains(strings.Join(blocked.Reasons, "; "), "/overlay free 7 MB is below 8 MB floor") {
		t.Fatalf("expected controller overlay floor reason, got %#v", blocked.Reasons)
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
