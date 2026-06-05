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
	if !strings.Contains(err.Error(), "retry 1/") {
		t.Fatalf("flushPendingJobResult error should include retry counter, got %v", err)
	}
	if persisted.PendingJobResultRetryCount != 1 {
		t.Fatalf("expected retry counter = 1, got %d", persisted.PendingJobResultRetryCount)
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
	if persisted.PendingJobResultRetryCount != 2 {
		t.Fatalf("expected retry counter = 2, got %d", persisted.PendingJobResultRetryCount)
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
	if persisted.PendingJobResultRetryCount != 0 {
		t.Fatalf("retry counter should reset on successful flush, got %d", persisted.PendingJobResultRetryCount)
	}
}

// TestFlushPendingControllerUpdateBailsOutAfterMaxRetries verifies the
// post-totchto guard: when the runtime version never reaches the expected
// value after pendingJobResultMaxRetries cycles, the controller submits a
// failure result instead of looping forever, then clears the journal so
// check-ins resume.
func TestFlushPendingControllerUpdateBailsOutAfterMaxRetries(t *testing.T) {
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
		RouterID:  "router-stuck",
		StatePath: filepath.Join(t.TempDir(), "state.json"),
	}
	client := controlplane.NewClient(controlplane.Options{
		BaseURL:    server.URL,
		RouterID:   cfg.RouterID,
		AgentToken: "token",
	})
	persisted := &state.PersistedState{
		CurrentJob: state.CurrentJob{
			JobID:                     "job-stuck-r27",
			JobType:                   "run_terminal_command",
			ExpectedControllerVersion: "0.1.13-r27",
		},
		PendingJobResult: &controlplane.JobResultRequest{
			JobID:  "job-stuck-r27",
			Status: "success",
		},
	}

	// First pendingJobResultMaxRetries-1 cycles should keep returning the
	// retry error and incrementing the counter without submitting anything.
	for i := 1; i < pendingJobResultMaxRetries; i++ {
		err := flushPendingJobResult(context.Background(), cfg, client, persisted, controlplane.RouterInventory{
			ControllerRuntimeVersion: "0.1.13-r29",
		})
		if err == nil {
			t.Fatalf("expected retry error on cycle %d, got nil", i)
		}
		if !strings.Contains(err.Error(), "controller restart confirmation pending") {
			t.Fatalf("cycle %d error = %v, want retry error", i, err)
		}
		if persisted.PendingJobResultRetryCount != i {
			t.Fatalf("cycle %d: counter = %d, want %d", i, persisted.PendingJobResultRetryCount, i)
		}
		if len(submitted) != 0 {
			t.Fatalf("cycle %d: submitted %d, want 0", i, len(submitted))
		}
	}

	// On the Nth cycle the controller should bail out, submit as failure,
	// and clear the journal so the next check-in can proceed.
	err := flushPendingJobResult(context.Background(), cfg, client, persisted, controlplane.RouterInventory{
		ControllerRuntimeVersion: "0.1.13-r29",
	})
	if err != nil {
		t.Fatalf("bail-out cycle should NOT return error, got %v", err)
	}
	if len(submitted) != 1 {
		t.Fatalf("expected exactly 1 submitted result, got %d", len(submitted))
	}
	if submitted[0].Status != "failure" {
		t.Fatalf("submitted status = %q, want failure", submitted[0].Status)
	}
	if submitted[0].JobID != "job-stuck-r27" {
		t.Fatalf("submitted job_id = %q, want job-stuck-r27", submitted[0].JobID)
	}
	if got, _ := submitted[0].Result["error"].(string); !strings.Contains(got, "abandoned to restore connectivity") {
		t.Fatalf("expected abandon explanation in result.error, got %#v", submitted[0].Result["error"])
	}
	if got, _ := submitted[0].Result["abandonedAfterRetries"].(float64); got != float64(pendingJobResultMaxRetries) {
		t.Fatalf("abandonedAfterRetries = %v, want %d", got, pendingJobResultMaxRetries)
	}
	if persisted.PendingJobResult != nil {
		t.Fatal("pending job result should be cleared after bail-out")
	}
	if persisted.CurrentJob.JobID != "" {
		t.Fatal("current job should be cleared after bail-out")
	}
	if persisted.PendingJobResultRetryCount != 0 {
		t.Fatalf("retry counter should reset after bail-out, got %d", persisted.PendingJobResultRetryCount)
	}
}

// TestPersistPendingJobResultResetsRetryCounterForNewJob verifies that a
// transient retry counter from a previous job does NOT leak into a new
// pending result — without this, a one-off transient failure on one job
// could prematurely trip the bail-out on the next unrelated job.
func TestPersistPendingJobResultResetsRetryCounterForNewJob(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	persisted := &state.PersistedState{
		PendingJobResult: &controlplane.JobResultRequest{
			JobID:  "job-old",
			Status: "success",
		},
		PendingJobResultRetryCount: 7,
	}

	// Same job-id: counter must NOT reset (we're still trying the same flush).
	err := persistPendingJobResult(statePath, persisted, controlplane.JobResultRequest{
		JobID:  "job-old",
		Status: "success",
	})
	if err != nil {
		t.Fatalf("persistPendingJobResult: %v", err)
	}
	if persisted.PendingJobResultRetryCount != 7 {
		t.Fatalf("counter changed on same-job re-persist: %d, want 7", persisted.PendingJobResultRetryCount)
	}

	// Different job-id: counter resets to zero so the new flush gets a
	// fresh budget.
	err = persistPendingJobResult(statePath, persisted, controlplane.JobResultRequest{
		JobID:  "job-new",
		Status: "success",
	})
	if err != nil {
		t.Fatalf("persistPendingJobResult new job: %v", err)
	}
	if persisted.PendingJobResultRetryCount != 0 {
		t.Fatalf("counter not reset for new job: %d, want 0", persisted.PendingJobResultRetryCount)
	}
}
