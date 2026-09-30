package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
)

// localRouter seeds a router that has everything a data plane needs on disk and
// no panel worth talking to: the operator config the panel would have delivered,
// and the last-good provider document stored verbatim.
//
// controlUrl points at a port nothing listens on, which is the honest shape of
// the situation this command exists for — the panel is reachable but useless.
// Nothing in apply-local talks to it.
func localRouter(t *testing.T, withOperatorConfig, withProviderDoc bool) (dir, agentPath string) {
	t.Helper()
	dir = t.TempDir()

	// No legacy agent on this box, asserted rather than assumed: the guard must
	// not be what makes these tests pass or fail.
	old := legacyInitScript
	legacyInitScript = filepath.Join(dir, "no-legacy-init")
	t.Cleanup(func() { legacyInitScript = old })

	if withOperatorConfig {
		if err := os.WriteFile(filepath.Join(dir, "operator.json"),
			operatorConfigPointingAt(t, "https://sub.invalid/x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if withProviderDoc {
		if err := os.WriteFile(filepath.Join(dir, "provider-config.json"), providerEntry(t), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	agentJSON := map[string]any{
		"controlUrl":         "http://127.0.0.1:1",
		"statePath":          filepath.Join(dir, "state.json"),
		"statusPath":         filepath.Join(dir, "status.json"),
		"xrayConfigPath":     filepath.Join(dir, "operator.json"),
		"providerConfigPath": filepath.Join(dir, "provider-config.json"),
		"xrayRenderPath":     filepath.Join(dir, "xray.json"),
		"xrayBinary":         writeFakeXray(t, dir),
		"geoAssetDir":        filepath.Join(dir, "assets"),
		"legacyStatePath":    filepath.Join(dir, "no-legacy-state.json"),
	}
	agentPath = filepath.Join(dir, "agent.json")
	raw, _ := json.Marshal(agentJSON)
	if err := os.WriteFile(agentPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, agentPath
}

// THE DEGRADATION PATH, end to end and with no panel in it: local operator
// config + cached provider document -> spliced, `xray -test`ed, installed.
func TestApplyLocalInstallsFromTheCachedProviderDocument(t *testing.T) {
	dir, agentPath := localRouter(t, true, true)

	if err := cmdApplyLocal([]string{"-config", agentPath}); err != nil {
		t.Fatalf("apply-local: %v", err)
	}

	rendered, err := os.ReadFile(filepath.Join(dir, "xray.json"))
	if err != nil {
		t.Fatalf("no rendered xray config was installed: %v", err)
	}
	if !json.Valid(rendered) {
		t.Fatalf("the installed config is not valid JSON:\n%s", firstBytes(rendered, 300))
	}
	// The whole point of the splice: the controller's tproxy inbound replaced
	// the provider's, and the provider's own outbounds survived.
	if !bytes.Contains(rendered, []byte("\"tproxy\"")) {
		t.Errorf("the installed config has no tproxy inbound:\n%s", firstBytes(rendered, 400))
	}

	// The cached provider document must be untouched. It is the byte-exact
	// last-good document, and rewriting it through a decode/encode round trip is
	// the corruption the verbatim rule exists to prevent.
	after, err := os.ReadFile(filepath.Join(dir, "provider-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(after), bytes.TrimSpace(providerEntry(t))) {
		t.Error("apply-local rewrote the cached provider document instead of leaving it verbatim")
	}

	// And the digest is persisted, so the daemon's next apply is a no-op rather
	// than a redundant reinstall.
	stateRaw, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stateRaw), "config_digest") {
		t.Errorf("the applied digest was not persisted:\n%s", firstBytes(stateRaw, 400))
	}
}

// Without the operator config there is nothing to build a data plane FROM, and
// the message has to say which file and how to seed it — this command exists
// precisely for the operator who has no panel to ask.
func TestApplyLocalRefusesWithoutAnOperatorConfig(t *testing.T) {
	dir, agentPath := localRouter(t, false, true)

	err := cmdApplyLocal([]string{"-config", agentPath})
	if err == nil {
		t.Fatal("apply-local succeeded with no operator config")
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "operator.json")) {
		t.Errorf("the refusal does not name the missing file: %v", err)
	}
	if fileExists(filepath.Join(dir, "xray.json")) {
		t.Error("a config was installed despite the refusal")
	}
}

// -dry-run runs the same `xray -test` gate and installs nothing.
func TestApplyLocalDryRunInstallsNothing(t *testing.T) {
	dir, agentPath := localRouter(t, true, true)

	if err := cmdApplyLocal([]string{"-config", agentPath, "-dry-run"}); err != nil {
		t.Fatalf("apply-local -dry-run: %v", err)
	}
	if fileExists(filepath.Join(dir, "xray.json")) {
		t.Error("-dry-run installed a rendered config")
	}
}

// state.json belongs to the DAEMON, which is normally running while an operator
// types this. The digest is the one field apply-local owns; everything else in
// there is the daemon's live bookkeeping, and a pending job result thrown away
// here is a job the panel never hears about again. So apply-local re-reads the
// file and writes back that one field, rather than saving the whole struct it
// loaded at startup.
//
// HONEST LIMIT OF THIS TEST: with no concurrent writer, the whole-struct save
// preserves everything too, so this does NOT distinguish the two
// implementations — verified by forcing the old path and watching it still
// pass. What it does pin is that every field present before the command is
// present after it, which is what a future refactor writing a fresh
// PersistedState would break. The narrow write is what shrinks the race window
// against a running daemon; that window is not reproducible from here, because
// the command builds its own daemon and nothing in the test can write between
// its load and its save.
func TestApplyLocalPreservesTheDaemonsState(t *testing.T) {
	dir, agentPath := localRouter(t, true, true)
	statePath := filepath.Join(dir, "state.json")

	// What a running daemon would have in flight.
	seeded := `{"router_id":"r-1","agent_token":"t-1",` +
		`"device_identifier":"vectra-07bf0887f662","device_public_key":"k",` +
		`"applied_revision_id":"rev-9","config_digest":"stale",` +
		`"rescue":{"mode":"direct","proxy_failure_count":3},` +
		`"pending_job_result":{"jobId":"j-inflight","status":"success"}}`
	if err := os.WriteFile(statePath, []byte(seeded), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := cmdApplyLocal([]string{"-config", agentPath}); err != nil {
		t.Fatalf("apply-local: %v", err)
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	// Decoded, not grepped: state.json is written indented, so a substring check
	// would pass or fail on whitespace rather than on the value.
	var got struct {
		RouterID          string `json:"router_id"`
		AppliedRevisionID string `json:"applied_revision_id"`
		ConfigDigest      string `json:"config_digest"`
		Rescue            struct {
			Mode              string `json:"mode"`
			ProxyFailureCount int    `json:"proxy_failure_count"`
		} `json:"rescue"`
		PendingJobResult *struct {
			JobID string `json:"jobId"`
		} `json:"pending_job_result"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode state.json: %v\n%s", err, firstBytes(raw, 400))
	}

	if got.PendingJobResult == nil || got.PendingJobResult.JobID != "j-inflight" {
		t.Errorf("the daemon's pending job result was thrown away: %+v", got.PendingJobResult)
	}
	if got.Rescue.ProxyFailureCount != 3 || got.Rescue.Mode != "direct" {
		t.Errorf("the daemon's rescue state was clobbered: %+v", got.Rescue)
	}
	if got.AppliedRevisionID != "rev-9" || got.RouterID != "r-1" {
		t.Errorf("identity/revision fields were clobbered: routerId=%q appliedRevisionId=%q",
			got.RouterID, got.AppliedRevisionID)
	}
	if got.ConfigDigest == "stale" || got.ConfigDigest == "" {
		t.Errorf("the applied digest was not recorded: %q", got.ConfigDigest)
	}
}

// Same mutual exclusion as `supervise`: this command installs the config the
// router's xray will run, so a live legacy agent means two proxy stacks
// fighting over one router.
func TestApplyLocalRefusesWhileTheLegacyAgentOwnsTheRouter(t *testing.T) {
	_, agentPath := localRouter(t, true, true)
	realFakeInit(t) // installed, enabled

	err := cmdApplyLocal([]string{"-config", agentPath})
	if err == nil || !strings.Contains(err.Error(), "still owns this router") {
		t.Fatalf("apply-local must refuse while the legacy agent owns the router, got: %v", err)
	}
}

// restartedDaemon is the daemon a reboot starts: a new process over the same
// files.
func restartedDaemon(t *testing.T, agentPath string) *daemon {
	t.Helper()
	cfg, err := agentcfg.Load(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDaemon(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// After a reboot the render is gone — it lives on tmpfs — and the documents
// it was made from are not: the daemon's start puts back the very render the
// router ran before.
func TestTheDaemonResumesItsRenderAfterAReboot(t *testing.T) {
	dir, agentPath := localRouter(t, true, true)
	if err := cmdApplyLocal([]string{"-config", agentPath}); err != nil {
		t.Fatalf("apply-local: %v", err)
	}
	render := filepath.Join(dir, "xray.json")
	before, err := os.ReadFile(render)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(render); err != nil { // the reboot
		t.Fatal(err)
	}

	restartedDaemon(t, agentPath).resumeRender(context.Background())

	after, err := os.ReadFile(render)
	if err != nil {
		t.Fatalf("no render after the restart: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the rebuilt render is not the one the router ran before its restart")
	}
}

// A router that never applied has nothing to resume: its first render still
// comes from a job, the router UI or apply-local.
func TestNothingIsResumedWithoutAnEarlierApply(t *testing.T) {
	dir, agentPath := localRouter(t, true, true)
	restartedDaemon(t, agentPath).resumeRender(context.Background())
	if fileExists(filepath.Join(dir, "xray.json")) {
		t.Error("a render was made for a router that never applied one")
	}
}

// Without the stored document nothing is rebuilt, and nothing is fetched to
// make up for it at start.
func TestNothingIsResumedWithoutTheStoredProviderDocument(t *testing.T) {
	dir, agentPath := localRouter(t, true, true)
	if err := cmdApplyLocal([]string{"-config", agentPath}); err != nil {
		t.Fatalf("apply-local: %v", err)
	}
	for _, f := range []string{"xray.json", "provider-config.json"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	restartedDaemon(t, agentPath).resumeRender(context.Background())
	if fileExists(filepath.Join(dir, "xray.json")) {
		t.Error("a render was made without the stored provider document")
	}
}

// A render that is there is the one to run: resuming never overwrites it.
func TestAnExistingRenderIsNotRebuilt(t *testing.T) {
	dir, agentPath := localRouter(t, true, true)
	if err := cmdApplyLocal([]string{"-config", agentPath}); err != nil {
		t.Fatalf("apply-local: %v", err)
	}
	render := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(render, []byte(`{"marker":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	restartedDaemon(t, agentPath).resumeRender(context.Background())
	if got, _ := os.ReadFile(render); string(got) != `{"marker":true}` {
		t.Error("an existing render was rebuilt")
	}
}

// A changed tproxy inbound in the operator config is a changed render, even
// with the provider's document the same: it used to count as fresh, and the
// router kept running the old inbound (sniffing's routeOnly, measured on the
// test router) while its config said otherwise.
func TestAChangedInboundIsRenderedAgain(t *testing.T) {
	dir, agentPath := localRouter(t, true, true)
	if err := cmdApplyLocal([]string{"-config", agentPath}); err != nil {
		t.Fatalf("apply-local: %v", err)
	}
	render := filepath.Join(dir, "xray.json")
	before, err := os.ReadFile(render)
	if err != nil {
		t.Fatal(err)
	}
	op := filepath.Join(dir, "operator.json")
	raw, err := os.ReadFile(op)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	tp := cfg["inbounds"].(map[string]any)["tproxy"].(map[string]any)
	tp["port"] = float64(12399)
	out, _ := json.Marshal(cfg)
	if err := os.WriteFile(op, out, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdApplyLocal([]string{"-config", agentPath}); err != nil {
		t.Fatalf("apply-local again: %v", err)
	}
	after, err := os.ReadFile(render)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, after) || !bytes.Contains(after, []byte("12399")) {
		t.Fatalf("the render still carries the old inbound:\n%s", firstBytes(after, 400))
	}
}
