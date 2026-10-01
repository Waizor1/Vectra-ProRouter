package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/vault"
)

func TestSecretMigrationPreservesIdentityAndRejectsDowngrade(t *testing.T) {
	dir := t.TempDir()
	c := agentcfg.Config{StatePath: filepath.Join(dir, "state.json"), XrayConfigPath: filepath.Join(dir, "operator.json"), ProviderConfigPath: filepath.Join(dir, "provider.json"), XrayRenderPath: filepath.Join(dir, "run", "xray.json"), LegacyStatePath: filepath.Join(dir, "legacy.json"), EntriesPath: filepath.Join(dir, "entries.gz")}
	c.Defaults()
	token := "SYNTHETIC_ROUTER_TOKEN_0123456789abcdef"
	st, _ := json.Marshal(state.PersistedState{RouterID: "test-router", AgentToken: token, DeviceIdentifier: "synthetic-device"})
	operator := []byte(`{"schema":1,"inbounds":{"tproxy":{"listenIP":"0.0.0.0","port":12345}},"subscriptions":[{"id":"test","enabled":true,"url":"https://node.invalid/SYNTHETIC_SUB_TOKEN"}]}`)
	provider := []byte(`{"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"node.invalid","port":443,"users":[{"id":"11111111-2222-4333-8444-555555555555"}]}]}}]}`)
	for p, b := range map[string][]byte{c.StatePath: st, c.StatePath + ".last-good": st, c.XrayConfigPath: operator, c.ProviderConfigPath: provider, c.XrayRenderPath: provider, c.StatePath + ".corrupt-old": st, c.ProviderConfigPath + ".tmp": provider} {
		os.MkdirAll(filepath.Dir(p), 0700)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := migrateSecrets(c); e != nil {
		t.Fatal(e)
	}
	if e := migrateSecrets(c); e != nil {
		t.Fatal(e)
	}
	got, e := state.Load(c.StatePath)
	if e != nil || got.AgentToken != token {
		t.Fatalf("identity lost: %v", e)
	}
	for _, p := range []string{c.StatePath, c.StatePath + ".last-good", c.StatePath + ".corrupt-old", c.XrayConfigPath, c.ProviderConfigPath, c.XrayRenderPath, c.ProviderConfigPath + ".tmp"} {
		b, e := os.ReadFile(p)
		if e != nil || json.Valid(b) || bytes.Contains(b, []byte(token)) || bytes.Contains(b, []byte("11111111-2222-4333-8444-555555555555")) {
			t.Fatalf("plaintext artifact %s", filepath.Base(p))
		}
		if _, e := vault.ReadFile(p); e != nil {
			t.Fatal(e)
		}
	}
	if e := config.SaveRaw(c.ProviderConfigPath, provider); e == nil {
		t.Fatal("plaintext helper downgraded sealed path")
	}
	if e := os.WriteFile(c.ProviderConfigPath, provider, 0600); e != nil {
		t.Fatal(e)
	}
	if e := migrateSecrets(c); e == nil {
		t.Fatal("plaintext downgrade accepted")
	}
}

func TestVaultIntegrityFailurePrecedesIdentityOrNetwork(t *testing.T) {
	dir := t.TempDir()
	c := agentcfg.Config{StatePath: filepath.Join(dir, "state.json"), XrayConfigPath: filepath.Join(dir, "operator.json"), ProviderConfigPath: filepath.Join(dir, "provider.json"), XrayRenderPath: filepath.Join(dir, "run", "xray.json"), LegacyStatePath: filepath.Join(dir, "legacy.json"), EntriesPath: filepath.Join(dir, "entries.gz")}
	c.Defaults()
	if e := vault.WriteFile(c.ProviderConfigPath, []byte(`{"outbounds":[]}`)); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(c.ProviderConfigPath)
	b[len(b)-1] ^= 1
	os.WriteFile(c.ProviderConfigPath, b, 0600)
	if _, e := newDaemon(c); e == nil {
		t.Fatal("corrupted provider accepted")
	}
	if _, e := os.Stat(c.StatePath); !os.IsNotExist(e) {
		t.Fatal("identity created before integrity failure")
	}
}

// Optional synthetic OUTPUT tree for independent artifact scanning; source
// fixtures and intentionally live in-memory input are never copied here.
func TestHardeningSyntheticArtifactExport(t *testing.T) {
	out := os.Getenv("VECTRA_HARDENING_ARTIFACT_DIR")
	if out == "" {
		t.Skip("artifact capture opt-in")
	}
	dir := t.TempDir()
	const token = "SYNTHETIC-AUDIT-AGENTTOKEN-174f9c31"
	const deviceKey = "SYNTHETIC-AUDIT-DEVICEKEY-cf824611"
	const sub = "SYNTHETIC-AUDIT-SUBTOKEN-7eb91da7"
	const id = "b1f69cd4-79ac-4a36-a808-721813c62a58"
	st := state.PersistedState{RouterID: "synthetic-router", AgentToken: token, DevicePrivateKey: deviceKey, DeviceIdentifier: "synthetic-device"}
	statePath := filepath.Join(dir, "state.json")
	if e := state.Save(statePath, st); e != nil {
		t.Fatal(e)
	}
	provider := []byte(`{"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"node.invalid","port":443,"users":[{"id":"` + id + `"}]}]}}]}`)
	for _, name := range []string{"provider-config.json", "xray.json"} {
		if e := vault.WriteFile(filepath.Join(dir, name), provider); e != nil {
			t.Fatal(e)
		}
	}
	cachePath := filepath.Join(dir, "provider-entries.json.gz")
	indexPath := filepath.Join(dir, "provider-entries.index.json")
	if _, e := localctl.SaveEntries(cachePath, indexPath, &localctl.EntriesCache{SubscriptionID: "synthetic", Remarks: []string{"synthetic location"}, Entries: []json.RawMessage{provider}}); e != nil {
		t.Fatal(e)
	}
	if e := vault.WriteFile(filepath.Join(dir, "xray-desired.json"), []byte(`{"subscription":"https://node.invalid/`+sub+`"}`)); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(out, 0700); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"state.json", "state.json.last-good", "provider-config.json", "xray.json", "provider-entries.json.gz", "provider-entries.index.json", "xray-desired.json"} {
		b, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		for _, marker := range []string{token, deviceKey, sub, id} {
			if bytes.Contains(b, []byte(marker)) {
				t.Fatal("output contains canary")
			}
		}
		if e := os.WriteFile(filepath.Join(out, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(out, ".synthetic-audit-artifacts"), []byte("synthetic encrypted-only export; local keys intentionally excluded\n"), 0600); e != nil {
		t.Fatal(e)
	}
}
