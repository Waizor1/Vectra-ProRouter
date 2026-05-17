package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/state"
)

func TestPersistCurrentJobTracksTerminalSelfUpdateRuntimeVersion(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	persisted := state.PersistedState{}

	err := persistCurrentJob(statePath, &persisted, controlplane.Job{
		ID:    "job-terminal-update",
		Type:  "run_terminal_command",
		State: "running",
		Payload: map[string]interface{}{
			"purpose":         controllerSelfUpdateCompatTerminalPurpose,
			"artifactVersion": "0.1.13-r23",
		},
	})
	if err != nil {
		t.Fatalf("persistCurrentJob returned error: %v", err)
	}

	if got := persisted.CurrentJob.ExpectedControllerVersion; got != "0.1.13-r23" {
		t.Fatalf("ExpectedControllerVersion = %q, want runtime target", got)
	}
}

func TestFlushPendingControllerUpdateRequiresRuntimeVersion(t *testing.T) {
	var submitted []controlplane.JobResultRequest
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/router/job-result" {
			http.NotFound(response, request)
			return
		}

		var payload controlplane.JobResultRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode job result: %v", err)
		}
		submitted = append(submitted, payload)
		response.Header().Set("content-type", "application/json")
		_, _ = response.Write([]byte(`{"protocolVersion":"1","acknowledged":true}`))
	}))
	t.Cleanup(server.Close)

	cfg := &config.Config{
		RouterID:  "router-123",
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}
	client := controlplane.NewClient(controlplane.Options{
		BaseURL:    server.URL,
		RouterID:   cfg.RouterID,
		AgentToken: "token",
	})
	persisted := &state.PersistedState{
		CurrentJob: state.CurrentJob{
			JobID:                     "job-update",
			JobType:                   "update_controller",
			ExpectedControllerVersion: "0.1.13-r23",
		},
		PendingJobResult: &controlplane.JobResultRequest{
			JobID:  "job-update",
			Status: "success",
			Result: map[string]interface{}{
				"artifactVersion": "0.1.13-r23",
			},
		},
	}

	err := flushPendingJobResult(context.Background(), cfg, client, persisted, controlplane.RouterInventory{
		ControllerVersion: "0.1.13-r23",
	})
	if err == nil || !strings.Contains(err.Error(), "running controller runtime version unavailable") {
		t.Fatalf("flushPendingJobResult error = %v, want missing runtime confirmation", err)
	}
	if len(submitted) != 0 {
		t.Fatalf("submitted %d job results before runtime confirmation", len(submitted))
	}
	if persisted.PendingJobResult == nil {
		t.Fatal("pending job result was cleared before runtime confirmation")
	}

	err = flushPendingJobResult(context.Background(), cfg, client, persisted, controlplane.RouterInventory{
		ControllerVersion:        "0.1.13-r23",
		ControllerRuntimeVersion: "0.1.13-r22",
	})
	if err == nil || !strings.Contains(err.Error(), "running runtime got 0.1.13-r22 want 0.1.13-r23") {
		t.Fatalf("flushPendingJobResult error = %v, want runtime mismatch", err)
	}
	if len(submitted) != 0 {
		t.Fatalf("submitted %d job results before matching runtime confirmation", len(submitted))
	}

	err = flushPendingJobResult(context.Background(), cfg, client, persisted, controlplane.RouterInventory{
		ControllerVersion:        "0.1.13-r23",
		ControllerRuntimeVersion: "0.1.13-r23",
	})
	if err != nil {
		t.Fatalf("flushPendingJobResult returned error after runtime confirmation: %v", err)
	}
	if len(submitted) != 1 {
		t.Fatalf("submitted %d job results, want 1", len(submitted))
	}
	if got := submitted[0].Result["confirmedControllerRuntimeVersion"]; got != "0.1.13-r23" {
		t.Fatalf("confirmedControllerRuntimeVersion = %#v, want 0.1.13-r23", got)
	}
	if got := submitted[0].Result["confirmedControllerVersion"]; got != "0.1.13-r23" {
		t.Fatalf("confirmedControllerVersion = %#v, want installed package version preserved", got)
	}
	if persisted.PendingJobResult != nil || persisted.CurrentJob.JobID != "" {
		t.Fatalf("job journal was not cleared after runtime-confirmed submit: %#v", persisted)
	}
}
