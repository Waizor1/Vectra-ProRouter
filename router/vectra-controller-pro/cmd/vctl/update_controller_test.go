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
	"sync/atomic"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
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

// The artifact is fetched over https only, redirects included: a redirect to
// plain http is refused before anything is asked of it.
func TestTheArtifactDownloadRefusesARedirectToPlainHTTP(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plainHits.Add(1)
		_, _ = w.Write([]byte("not an ipk"))
	}))
	defer plain.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	prev := updateHTTPClient
	updateHTTPClient = func() *http.Client { return srv.Client() }
	t.Cleanup(func() { updateHTTPClient = prev })

	_, err := downloadFile(context.Background(), srv.URL+"/vectra-controller-pro_0.6.0-r37_aarch64_cortex-a53.ipk", filepath.Join(t.TempDir(), "u.ipk"))
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("a redirect to plain http = %v, want a refusal", err)
	}
	if n := plainHits.Load(); n > 0 {
		t.Fatalf("the redirect was followed %d time(s)", n)
	}
}

// The panel's geo update writes into vctl's own geo directory only. The
// directory is the operator config's to name, and the files are written as
// root: an operator config naming /etc/crontabs, with an asset called
// "root", would be a shell on the router for whoever sent it.
func TestTheGeoUpdateWritesOnlyIntoVctlsOwnDirectory(t *testing.T) {
	results := map[string][]controlplane.JobResultRequest{}
	mu := &sync.Mutex{}
	d := buildGuardTestDaemon(t, results, mu)
	crontabs := filepath.Join(t.TempDir(), "crontabs")
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "testdata", "panel", "operator-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Geo.AssetDir = crontabs
	cfg.Geo.ExtraAssets = []config.GeoFile{{Filename: "root", URL: "https://evil.invalid/cron"}}
	if err := config.Save(d.cfg.XrayConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	d.desired = cfg
	// ...and xray runs a render that passed its -test there.
	d.sup.SetAssetDir(crontabs)

	_ = d.executeJob(context.Background(), controlplane.Job{ID: "g1", Type: "update_xray_assets"}, controlplane.CheckInResponse{})
	fail, ok := lastResult(results, mu, "g1")
	if !ok {
		t.Fatalf("the geo update into another directory was not refused: %+v", results["g1"])
	}
	if msg, _ := fail.Result["error"].(string); !strings.Contains(msg, crontabs) || !strings.Contains(msg, config.DefaultGeoAssetDir) {
		t.Errorf("the refusal does not say where and why: %v", fail.Result)
	}
	if _, err := os.Stat(crontabs); !os.IsNotExist(err) {
		t.Fatalf("the refused directory was made: %v", err)
	}
}

// The self-update is an upgrade, never a re-install: opkg's --force-reinstall
// removes the installed package first, its prerm sees a removal (not
// "upgrade"), stops vctl, disables it and hands the router back to the old
// agent — killing the opkg, so the new version never lands (1111, Connect
// update_now, 2026-10-02 09:13). Both update lanes only ever install a newer
// version (the signed feed's floor), so a plain install is an upgrade.
func TestTheSelfUpdateIsAnUpgradeNotAReinstall(t *testing.T) {
	cmd := controllerInstallCommand(context.Background(), "/tmp/vectra-controller-pro-update.ipk")
	if strings.Contains(strings.Join(cmd.Args, " "), "--force-reinstall") {
		t.Fatalf("a re-install runs the old package's removal: %v", cmd.Args)
	}
}
