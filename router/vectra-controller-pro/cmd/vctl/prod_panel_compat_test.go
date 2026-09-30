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

// WHAT THIS FILE IS FOR
//
// The panel in production has not been upgraded to the xray-direct contract.
// The init script hands the router over and starts `vctl agent`, and that agent
// then talks to a panel that predates everything this controller is for. What
// it does there was, until this file, an assumption.
//
// So the stub below answers exactly as the deployed panel does. Every
// difference is read off packages/contracts/src/schemas.ts and
// apps/web/src/server/vectra/router-control.ts on `main`:
//
//   - desiredRevisionSummarySchema has NO engineMode field, and its config is
//     `passwallDesiredConfigSchema` — the deployed panel can only ever serve a
//     PassWall revision.
//   - resolveDesiredRevision has no engine guard (the guard arrives with
//     fleet.setEngineMode), so a router is handed whatever revision it points
//     at regardless of which controller is running on it.
//   - jobTypeSchema has no apply_xray_config / refresh_xray_subscriptions /
//     update_xray_assets / reload_xray_outbound. The deployed panel cannot
//     queue an xray job at all; what it queues is PassWall work.
//   - VECTRA_PROTOCOL_VERSION is the SAME string ("2026-04-v1") on both, and
//     routerInventorySchema on main accepts this controller's inventory
//     verbatim, dropping engineMode/xrayEnabled/xrayVersion as undeclared keys.
//     So there is no protocol-level rejection to design around — the router
//     enrolls and checks in normally, and the panel simply cannot see that it
//     is an xray router.
//
// The behaviour that follows is DEGRADATION, not failure, and these tests pin
// each half of it: what still works, and what must be refused rather than
// half-applied.

// passwallRevisionConfig is a real PassWall desired config — the AX3000T global
// rollout template the panel seeds routers from — not a hand-written shape.
func passwallRevisionConfig(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "apps", "web", "src", "app",
		"enrollment", "__fixtures__", "ax3000t-global-rollout-template.seed.json"))
	if err != nil {
		t.Fatalf("read the passwall rollout template: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("the passwall rollout template is not valid JSON")
	}
	return json.RawMessage(raw)
}

// prodPanelStub is the DEPLOYED panel: a revision with no engineMode carrying a
// PassWall config, and PassWall job types.
type prodPanelStub struct {
	*httptest.Server
	mu      sync.Mutex
	results map[string][]controlplane.JobResultRequest
	jobs    []controlplane.Job
}

func newProdPanelStub(t *testing.T, config json.RawMessage, jobs []controlplane.Job) *prodPanelStub {
	t.Helper()
	p := &prodPanelStub{results: map[string][]controlplane.JobResultRequest{}, jobs: jobs}
	// Built as a map, not as DesiredRevisionSummary, precisely so `engineMode`
	// is ABSENT from the bytes rather than present-and-empty. The deployed
	// schema has no such field, and "absent" is the signal this controller keys
	// on.
	revRaw, err := json.Marshal(map[string]any{
		"id":             "8f14e45f-ceea-467a-9dc7-6b9d5a44a0e1",
		"revisionNumber": 41,
		"status":         "approved",
		"origin":         "operator_draft",
		"configDigest":   "sha256:passwall",
		"config":         config,
		"impact": map[string]any{
			"changedSections": []string{}, "requiresRestart": true,
			"refreshSubscriptions": false, "refreshRules": false,
			"packageInstall": false, "firmwareValidation": false,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/router/register":
			_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{
				RouterID: "r-prod", IssuedToken: "tok-prod", Status: "approved",
			})
		case "/api/router/check-in":
			_ = json.NewEncoder(w).Encode(controlplane.CheckInResponse{
				Status: "ok", Jobs: p.jobs, DesiredRevision: revRaw,
			})
		case "/api/router/job-result":
			var req controlplane.JobResultRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			p.mu.Lock()
			p.results[req.JobID] = append(p.results[req.JobID], req)
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(controlplane.JobResultResponse{Acknowledged: true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *prodPanelStub) resultsFor(id string) []controlplane.JobResultRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]controlplane.JobResultRequest(nil), p.results[id]...)
}

// newProdDaemon is newTestDaemon against the deployed-panel stub. It reuses the
// same agent config shape, so the only variable is what the panel answers.
func newProdDaemon(t *testing.T, dir string, panel *prodPanelStub) *daemon {
	t.Helper()
	agentJSON := map[string]any{
		"controlUrl":         panel.URL,
		"statePath":          filepath.Join(dir, "state.json"),
		"statusPath":         filepath.Join(dir, "status.json"),
		"xrayConfigPath":     filepath.Join(dir, "operator.json"),
		"providerConfigPath": filepath.Join(dir, "provider-config.json"),
		"xrayRenderPath":     filepath.Join(dir, "xray.json"),
		"xrayBinary":         writeFakeXray(t, dir),
		"geoAssetDir":        filepath.Join(dir, "assets"),
		"legacyStatePath":    filepath.Join(dir, "no-legacy.json"),
	}
	agentPath := filepath.Join(dir, "agent.json")
	raw, _ := json.Marshal(agentJSON)
	if err := os.WriteFile(agentPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadAgentConfigForTest(t, agentPath)
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDaemon(cfg)
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	d.rescuePolicy.HealthURLs = []string{panel.URL}
	return d
}

// THE HEADLINE: against the deployed panel the router ENROLLS AND CHECKS IN.
// There is no protocol-version mismatch and no schema rejection — the inventory
// this controller sends parses under the deployed routerInventorySchema, which
// simply drops engineMode/xrayEnabled/xrayVersion as fields it does not declare.
//
// This is the fact the whole degradation design rests on: the router stays
// MANAGEABLE (check-in, jobs, terminal, logs, controller update) while the
// panel cannot yet configure its data plane.
func TestAgainstDeployedPanelTheRouterStillEnrollsAndChecksIn(t *testing.T) {
	dir := t.TempDir()
	panel := newProdPanelStub(t, passwallRevisionConfig(t), nil)
	d := newProdDaemon(t, dir, panel)

	if err := d.runOnce(context.Background()); err != nil {
		t.Fatalf("register against the deployed panel failed: %v", err)
	}
	if d.st.RouterID == "" || d.st.AgentToken == "" {
		t.Fatalf("no identity after register: %+v", d.st)
	}
	if err := d.runOnce(context.Background()); err != nil {
		t.Fatalf("check-in against the deployed panel failed: %v", err)
	}
}

// The revision the deployed panel hands over is a PassWall one, and it must not
// be adopted. LastDesiredRevision is persisted to state.json and is what
// jobApplyXrayConfig falls back to, so adopting it would outlive the panel
// upgrade: the stale PassWall config would shadow the first real xray revision,
// and the failure would read as "the new panel sent something unparseable".
func TestAgainstDeployedPanelAPassWallRevisionIsNotAdopted(t *testing.T) {
	dir := t.TempDir()
	panel := newProdPanelStub(t, passwallRevisionConfig(t), nil)
	d := newProdDaemon(t, dir, panel)

	if err := d.runOnce(context.Background()); err != nil { // register
		t.Fatal(err)
	}
	if err := d.runOnce(context.Background()); err != nil { // check-in
		t.Fatal(err)
	}

	if d.st.LastDesiredRevision != nil {
		t.Fatalf("adopted a revision with no engineMode: id=%q engineMode=%q",
			d.st.LastDesiredRevision.ID, d.st.LastDesiredRevision.EngineMode)
	}
	// And it must not be on disk either — state.json survives restarts.
	raw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "last_desired_revision") {
		t.Errorf("a foreign revision was persisted to state.json:\n%s", firstBytes(raw, 400))
	}
}

// The deployed panel queues PassWall work. Each such job must be REFUSED —
// reported as a failure the operator can see — and must not touch the data
// plane. A controller that silently ignored them would leave jobs hanging in
// `delivered` forever, which is the panel-side version of the same silence.
func TestAgainstDeployedPanelPassWallJobsAreRefusedAndChangeNothing(t *testing.T) {
	passwallJobs := []controlplane.Job{
		{ID: "j-apply", Type: "apply_passwall_config", State: "queued"},
		{ID: "j-refresh", Type: "refresh_subscriptions", State: "queued"},
		{ID: "j-runtime", Type: "ensure_passwall_runtime", State: "queued"},
		{ID: "j-rules", Type: "refresh_rules", State: "queued"},
		{ID: "j-repair", Type: "run_rescue_repair", State: "queued"},
		{ID: "j-pkg", Type: "update_passwall_packages", State: "queued"},
	}
	dir := t.TempDir()
	panel := newProdPanelStub(t, passwallRevisionConfig(t), passwallJobs)
	d := newProdDaemon(t, dir, panel)

	if err := d.runOnce(context.Background()); err != nil { // register
		t.Fatal(err)
	}
	if err := d.runOnce(context.Background()); err != nil { // check-in + jobs
		t.Fatal(err)
	}

	for _, job := range passwallJobs {
		results := panel.resultsFor(job.ID)
		if len(results) == 0 {
			t.Errorf("%s (%s): no result reported — the panel would leave it hanging", job.ID, job.Type)
			continue
		}
		final := results[len(results)-1]
		if final.Status != "failure" {
			t.Errorf("%s (%s): status %q, want failure", job.ID, job.Type, final.Status)
		}
		if msg, _ := final.Result["error"].(string); !strings.Contains(msg, "unsupported job type") {
			t.Errorf("%s (%s): result %v, want an 'unsupported job type' error", job.ID, job.Type, final.Result)
		}
	}

	// Nothing was applied: no rendered xray config, no supervisor.
	if fileExists(filepath.Join(dir, "xray.json")) {
		t.Error("a PassWall job produced a rendered xray config")
	}
	if d.supStarted {
		t.Error("a PassWall job started the xray supervisor")
	}
}

// The direct test of the guard: even if a panel DID queue apply_xray_config
// while handing over a revision for another engine, the config must not be
// applied. Without this the document reaches config.Read, which rejects it
// field by field — a message that reads like a corrupt config rather than the
// wrong engine, on a router whose data plane is then half-configured.
func TestApplyXrayConfigRefusesAForeignEngineRevision(t *testing.T) {
	dir := t.TempDir()
	panel := newProdPanelStub(t, passwallRevisionConfig(t),
		[]controlplane.Job{{ID: "j-x", Type: "apply_xray_config", State: "queued"}})
	d := newProdDaemon(t, dir, panel)

	if err := d.runOnce(context.Background()); err != nil { // register
		t.Fatal(err)
	}
	if err := d.runOnce(context.Background()); err != nil { // check-in + the job
		t.Fatal(err)
	}

	results := panel.resultsFor("j-x")
	if len(results) == 0 {
		t.Fatal("no result reported for apply_xray_config")
	}
	final := results[len(results)-1]
	if final.Status != "failure" {
		t.Fatalf("status %q, want failure", final.Status)
	}
	msg, _ := final.Result["error"].(string)
	for _, want := range []string{"engineMode", "xray-direct", "another engine"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q: %s", want, msg)
		}
	}
	if fileExists(filepath.Join(dir, "operator.json")) {
		t.Error("a foreign-engine revision was persisted as the operator config")
	}
}

func firstBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// loadAgentConfigForTest keeps the agentcfg import local to the helper that
// needs it, so the stub above reads as the panel contract and nothing else.
func loadAgentConfigForTest(t *testing.T, path string) (agentcfg.Config, error) {
	t.Helper()
	return agentcfg.Load(path)
}
