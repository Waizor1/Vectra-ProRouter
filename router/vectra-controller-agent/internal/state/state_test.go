package state

import (
	"path/filepath"
	"testing"

	"vectra-controller-agent/internal/controlplane"
)

func TestSaveAndLoadPreservesJobJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	original := PersistedState{
		RouterID:         "router-123",
		AgentToken:       "token-abc",
		DeviceIdentifier: "vectra-test",
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
}
