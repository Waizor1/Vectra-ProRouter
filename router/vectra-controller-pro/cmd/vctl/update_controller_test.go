package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/controlplane"
)

// buildGuardTestDaemon wires a daemon to a mock control plane that only needs
// to capture job-results.
func buildGuardTestDaemon(t *testing.T, results map[string][]controlplane.JobResultRequest, mu *sync.Mutex) *daemon {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/router/job-result" {
			var req controlplane.JobResultRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			results[req.JobID] = append(results[req.JobID], req)
			mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"acknowledged": true})
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	fakeXray := filepath.Join(dir, "fake-xray")
	if err := os.WriteFile(fakeXray, []byte("#!/bin/sh\ncase \"$1\" in version) echo 'Xray 1 (fake)';; *) exec sleep 1;; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agentJSON, _ := json.Marshal(map[string]any{
		"controlUrl":      srv.URL,
		"statePath":       filepath.Join(dir, "state.json"),
		"xrayConfigPath":  filepath.Join(dir, "xray-desired.json"),
		"xrayRenderPath":  filepath.Join(dir, "xray.json"),
		"xrayBinary":      fakeXray,
		"legacyStatePath": filepath.Join(dir, "no-legacy.json"),
	})
	cfgPath := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(cfgPath, agentJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := agentcfg.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDaemon(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d.st.RouterID = "r"
	d.st.AgentToken = "tok"
	d.client.SetCredentials("r", "tok")
	return d
}

func lastResult(results map[string][]controlplane.JobResultRequest, mu *sync.Mutex, id string) (controlplane.JobResultRequest, bool) {
	mu.Lock()
	defer mu.Unlock()
	for i := len(results[id]) - 1; i >= 0; i-- {
		if results[id][i].Status == "failure" {
			return results[id][i], true
		}
	}
	return controlplane.JobResultRequest{}, false
}

func TestUpdateControllerRefusesForeignArtifact(t *testing.T) {
	results := map[string][]controlplane.JobResultRequest{}
	mu := &sync.Mutex{}
	d := buildGuardTestDaemon(t, results, mu)

	// A legacy-agent artifact handed to the pro controller must be refused
	// BEFORE any download/opkg.
	job := controlplane.Job{ID: "j1", Type: "update_controller", Payload: map[string]any{
		"artifactUrl": "https://api.vectra-pro.net/x/vectra-controller-agent_0.1.13.ipk",
		"sha256":      "deadbeef",
		"name":        "vectra-controller-agent",
	}}
	_ = d.executeJob(context.Background(), job, controlplane.CheckInResponse{})

	fail, ok := lastResult(results, mu, "j1")
	if !ok {
		t.Fatal("expected a failure result for foreign artifact")
	}
	if msg, _ := fail.Result["error"].(string); !strings.Contains(msg, "refusing artifact") {
		t.Errorf("expected refusal, got %v", fail.Result)
	}
}

func TestUpdateControllerRequiresChecksum(t *testing.T) {
	results := map[string][]controlplane.JobResultRequest{}
	mu := &sync.Mutex{}
	d := buildGuardTestDaemon(t, results, mu)

	// A pro artifact with no checksum must be refused (fail closed) before install.
	job := controlplane.Job{ID: "j2", Type: "update_controller", Payload: map[string]any{
		"artifactUrl": "https://api.vectra-pro.net/x/vectra-controller-pro_0.2.0.ipk",
		"name":        "vectra-controller-pro",
	}}
	_ = d.executeJob(context.Background(), job, controlplane.CheckInResponse{})

	fail, ok := lastResult(results, mu, "j2")
	if !ok {
		t.Fatal("expected a failure result for missing checksum")
	}
	if msg, _ := fail.Result["error"].(string); !strings.Contains(msg, "missing sha256") {
		t.Errorf("expected missing-sha256 refusal, got %v", fail.Result)
	}
}

// The package's postinst restarts a running vctl — and this job runs that
// opkg as vctl's own child: the restart's stop ends vctl, vctl's context kills
// the opkg before it writes its status, and the job's result is never
// journaled. The job holds the postinst's restart back and restarts vctl
// itself once opkg is done.
func TestUpdateControllerHoldsThePostinstRestartBack(t *testing.T) {
	cmd := controllerInstallCommand(context.Background(), "/tmp/vectra-controller-pro-update.ipk")
	held := false
	for _, e := range cmd.Env {
		held = held || e == "VECTRA_SKIP_POSTINST_RESTART=1"
	}
	if !held || cmd.Args[len(cmd.Args)-1] != "/tmp/vectra-controller-pro-update.ipk" {
		t.Fatalf("env %v, args %v", cmd.Env, cmd.Args)
	}
	if !strings.Contains(strings.Join(cmd.Args, " "), "opkg install") {
		t.Fatalf("not an opkg install: %v", cmd.Args)
	}
}

// The restart outlives the vctl it stops: a session of its own, so nothing
// that ends vctl's process group or session ends the restart with it before
// its start.
func TestTheSelfUpdateRestartOutlivesVctl(t *testing.T) {
	cmd := controllerRestartCommand()
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("no session of its own: %+v", cmd.SysProcAttr)
	}
	if !strings.Contains(strings.Join(cmd.Args, " "), "/etc/init.d/vectra-controller-pro restart") {
		t.Fatalf("args %v", cmd.Args)
	}
}
