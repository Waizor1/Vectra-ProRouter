package state

import (
	"path/filepath"
	"testing"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/recovery"
)

func TestSaveAndLoadPreservesJobJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	original := PersistedState{
		RouterID:         "router-123",
		AgentToken:       "token-abc",
		DeviceIdentifier: "vectra-test",
		ControlPlaneRecovery: recovery.State{
			LastSuccessfulControlPlaneAt: "2026-04-22T09:00:00Z",
			OutageStartedAt:              "2026-04-22T10:00:00Z",
			Phase:                        recovery.PhasePostRebootCheck,
			LastControllerRestartAt:      "2026-04-22T10:05:00Z",
			LastAutoRebootAt:             "2026-04-22T10:15:00Z",
			LastPasswallRetryAt:          "2026-04-22T10:20:00Z",
			AwaitingOperator:             true,
			LastPanelStatus:              recovery.StatusBlocked,
			LastRUStatus:                 recovery.StatusReachable,
			LastForeignStatus:            recovery.StatusBlocked,
			LastActionReason:             "waiting for operator",
		},
		CurrentJob: CurrentJob{
			JobID:                     "job-123",
			JobType:                   "update_controller",
			AcceptedAt:                "2026-04-06T00:00:00Z",
			ExpectedControllerVersion: "0.1.3-r1",
		},
		PendingJobResult: &controlplane.JobResultRequest{
			ProtocolVersion: controlplane.ProtocolVersion,
			RouterID:        "router-123",
			JobID:           "job-123",
			Status:          "success",
			Result: map[string]interface{}{
				"packages": []interface{}{
					"vectra-controller-agent",
					"luci-app-vectra-controller",
				},
			},
		},
	}

	if err := Save(path, original); err != nil {
		t.Fatalf("save state: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}

	if got, want := loaded.CurrentJob.JobID, original.CurrentJob.JobID; got != want {
		t.Fatalf("current job id = %q, want %q", got, want)
	}
	if loaded.PendingJobResult == nil {
		t.Fatal("expected pending job result to survive round-trip")
	}
	if got, want := loaded.PendingJobResult.JobID, "job-123"; got != want {
		t.Fatalf("pending job result job id = %q, want %q", got, want)
	}
	if got, want := loaded.PendingJobResult.Status, "success"; got != want {
		t.Fatalf("pending job result status = %q, want %q", got, want)
	}
	if got, want := loaded.ControlPlaneRecovery.Phase, recovery.PhasePostRebootCheck; got != want {
		t.Fatalf("control plane recovery phase = %q, want %q", got, want)
	}
	if !loaded.ControlPlaneRecovery.AwaitingOperator {
		t.Fatal("expected awaiting operator flag to survive round-trip")
	}
}
