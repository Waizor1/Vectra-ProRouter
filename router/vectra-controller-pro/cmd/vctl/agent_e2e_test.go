package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
)

// TestAgentEndToEnd drives a full loop against a mock control plane:
// register -> check-in (delivering an apply_xray_config job + desired config)
// -> apply (render + write xray.json) -> job-result success.
func TestAgentEndToEnd(t *testing.T) {
	example, err := os.ReadFile(filepath.Join("..", "..", "examples", "p0-vless-reality.json"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}

	var mu sync.Mutex
	results := map[string][]controlplane.JobResultRequest{}
	registered := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/router/register":
			mu.Lock()
			registered = true
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{
				RouterID: "r-e2e", IssuedToken: "tok-e2e", Status: "approved",
			})
		case "/api/router/check-in":
			rev := controlplane.DesiredRevisionSummary{
				ID: "rev-1", RevisionNumber: 1, Status: "approved",
				EngineMode: controlplane.EngineModeXrayDirect,
				Config:     json.RawMessage(example),
			}
			revRaw, _ := json.Marshal(rev)
			_ = json.NewEncoder(w).Encode(controlplane.CheckInResponse{
				Status:          "ok",
				Jobs:            []controlplane.Job{{ID: "j1", Type: "apply_xray_config", State: "queued"}},
				DesiredRevision: revRaw,
			})
		case "/api/router/job-result":
			var req controlplane.JobResultRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			results[req.JobID] = append(results[req.JobID], req)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(controlplane.JobResultResponse{Acknowledged: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	// A stub "xray" that ignores its args and stays up, so the supervisor's
	// start succeeds without a real Xray binary.
	fakeXray := filepath.Join(dir, "fake-xray")
	// Answers `version` instantly; for `run` it stays up like a real daemon.
	stub := "#!/bin/sh\ncase \"$1\" in\n  version) echo 'Xray 1.8.24 (fake)';;\n  *) exec sleep 300;;\nesac\n"
	if err := os.WriteFile(fakeXray, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	agentJSON := map[string]any{
		"controlUrl":      srv.URL,
		"statePath":       filepath.Join(dir, "state.json"),
		"statusPath":      filepath.Join(dir, "status.json"),
		"xrayConfigPath":  filepath.Join(dir, "xray-desired.json"),
		"xrayRenderPath":  filepath.Join(dir, "xray.json"),
		"xrayBinary":      fakeXray,
		"legacyStatePath": filepath.Join(dir, "no-legacy.json"),
	}
	agentPath := filepath.Join(dir, "agent.json")
	raw, _ := json.Marshal(agentJSON)
	if err := os.WriteFile(agentPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := agentcfg.Load(agentPath)
	if err != nil {
		t.Fatalf("load agent cfg: %v", err)
	}
	d, err := newDaemon(cfg)
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	// Bound the firewall deadman so the test never leaves a long-lived process.
	d.confirmer = firewall.NewCommitConfirmer(filepath.Join(dir, "fw-confirm"), time.Second)
	// Keep connectivity probes hermetic (no real internet) and fast.
	d.rescuePolicy.HealthURLs = []string{srv.URL}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1) register
	if err := d.runOnce(ctx); err != nil {
		t.Fatalf("runOnce (register): %v", err)
	}
	mu.Lock()
	reg := registered
	mu.Unlock()
	if !reg || d.st.RouterID != "r-e2e" || d.st.AgentToken != "tok-e2e" {
		t.Fatalf("register failed: registered=%v state=%+v", reg, d.st)
	}

	// 2) check-in + apply job
	if err := d.runOnce(ctx); err != nil {
		t.Fatalf("runOnce (check-in): %v", err)
	}

	// xray.json must have been rendered + written.
	rendered, err := os.ReadFile(filepath.Join(dir, "xray.json"))
	if err != nil || !json.Valid(rendered) {
		t.Fatalf("xray.json not written/invalid: err=%v bytes=%d", err, len(rendered))
	}

	// A success job-result must have been submitted for j1.
	mu.Lock()
	got := results["j1"]
	mu.Unlock()
	var success *controlplane.JobResultRequest
	for i := range got {
		if got[i].Status == "success" {
			success = &got[i]
		}
	}
	if success == nil {
		t.Fatalf("no success job-result for j1; got %d results: %+v", len(got), got)
	}
	if success.AppliedRevisionID != "rev-1" {
		t.Errorf("appliedRevisionId = %q, want rev-1", success.AppliedRevisionID)
	}
	if success.ConfigDigest == "" {
		t.Error("missing configDigest in success result")
	}
	if d.st.AppliedRevisionID != "rev-1" {
		t.Errorf("state appliedRevisionId = %q", d.st.AppliedRevisionID)
	}
}
