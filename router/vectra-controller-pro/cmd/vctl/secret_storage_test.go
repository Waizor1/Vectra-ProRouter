package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestLegacyVaultUpgradeRecoveryAndKeyFreeExport(t *testing.T) {
	manifest, err := os.ReadFile("../../openwrt/files/lib/upgrade/keep.d/vectra-controller-pro")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/etc/vectra-controller/", "/etc/vectra-controller.vault-keys/", "/etc/vectra-controller-pro/", "/etc/vectra-controller-pro-vault-keys/"} {
		if !strings.Contains("\n"+string(manifest), "\n"+p+"\n") {
			t.Fatalf("recovery manifest lacks %s", p)
		}
	}
	key, _ := vault.KeyPath("/etc/vectra-controller/state.json")
	if key != "/etc/vectra-controller.vault-keys/key" {
		t.Fatal("legacy manifest/key mapping changed")
	}
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy", "state.json")
	c := agentcfg.Config{StatePath: filepath.Join(root, "pro", "state.json"), LegacyStatePath: legacy, XrayConfigPath: filepath.Join(root, "pro", "operator"), ProviderConfigPath: filepath.Join(root, "pro", "provider"), EntriesPath: filepath.Join(root, "pro", "entries"), XrayRenderPath: filepath.Join(root, "run", "xray")}
	c.Defaults()
	const token = "SYNTHETIC-LEGACY-RECOVERY-TOKEN"
	os.MkdirAll(filepath.Dir(legacy), 0700)
	os.WriteFile(legacy, []byte(`{"router_id":"synthetic-router","agent_token":"`+token+`","device_identifier":"synthetic-device"}`), 0600)
	if err := migrateSecrets(c); err != nil {
		t.Fatal(err)
	}
	var st state.PersistedState
	if imported, err := state.ImportLegacyIdentity(&st, legacy); err != nil || !imported || st.AgentToken != token {
		t.Fatalf("legacy import: %v", err)
	}
	if err := state.Save(c.StatePath, st); err != nil {
		t.Fatal(err)
	}
	if err := vault.WriteFile(c.ProviderConfigPath, []byte(`{"synthetic":"`+token+`"}`)); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "ordinary.tgz")
	if err := exportConfigSnapshot(c, out); err != nil {
		t.Fatal(err)
	}

	exported, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(exported))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(zr)
	zr.Close()
	if err != nil {
		t.Fatal(err)
	}
	legacyKey, _ := vault.KeyPath(legacy)
	keyBytes, err := os.ReadFile(legacyKey)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(decoded, []byte(token)) || bytes.Contains(decoded, keyBytes) || bytes.Contains(decoded, []byte("state.json")) {
		t.Fatal("ordinary export leaked legacy secret/key/identity")
	}
	// Simulate preservation/restoration at original absolute paths. Full recovery
	// intentionally holds keys in memory here; ordinary export is tested separately.
	saved := map[string][]byte{}
	dirs := []string{filepath.Dir(legacy), filepath.Dir(c.StatePath)}
	for _, base := range append(append([]string{}, dirs...), dirs[0]+".vault-keys", dirs[1]+".vault-keys") {
		if err := filepath.WalkDir(base, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if !d.IsDir() {
				b, e := os.ReadFile(p)
				if e != nil {
					return e
				}
				saved[p] = b
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(base); err != nil {
			t.Fatal(err)
		}
	}
	for p, b := range saved {
		os.MkdirAll(filepath.Dir(p), 0700)
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateSecrets(c); err != nil {
		t.Fatal(err)
	}
	got, err := state.Load(c.StatePath)
	if err != nil || got.AgentToken != token {
		t.Fatalf("new state recovery: %v", err)
	}
	var recovered state.PersistedState
	if imported, err := state.ImportLegacyIdentity(&recovered, legacy); err != nil || !imported || recovered.AgentToken != token {
		t.Fatalf("legacy recovery: %v", err)
	}
}

// 1111, 2026-10-02: vctl 0.7.0-r1 migrated and ran; after a planned restart it
// never started again. The old vectra-reporter (1.0.0-r2) read the sealed
// state every minute, failed to parse it and kept byte copies of it as
// state.json.corrupt-<time>; the next start refused them as crash artifacts
// it could not open. Copies of every sealed secret under foreign names — the
// legacy agent does the same with its state — must never refuse a start.
func TestTheNextStartSurvivesAnOldReadersCopiesOfSealedFiles(t *testing.T) {
	dir := t.TempDir()
	c := agentcfg.Config{StatePath: filepath.Join(dir, "state.json"), XrayConfigPath: filepath.Join(dir, "operator.json"), ProviderConfigPath: filepath.Join(dir, "provider.json"), XrayRenderPath: filepath.Join(dir, "run", "xray.json"), LegacyStatePath: filepath.Join(dir, "legacy", "state.json"), EntriesPath: filepath.Join(dir, "entries.gz")}
	c.Defaults()
	token := "SYNTHETIC_ROUTER_TOKEN_0123456789abcdef"
	st, _ := json.Marshal(state.PersistedState{RouterID: "test-router", AgentToken: token, DeviceIdentifier: "synthetic-device"})
	operator := []byte(`{"schema":1,"inbounds":{"tproxy":{"listenIP":"0.0.0.0","port":12345}},"subscriptions":[{"id":"test","enabled":true,"url":"https://node.invalid/SYNTHETIC_SUB_TOKEN"}]}`)
	provider := []byte(`{"outbounds":[]}`)
	for p, b := range map[string][]byte{c.StatePath: st, c.StatePath + ".last-good": st, c.LegacyStatePath: st, c.XrayConfigPath: operator, c.ProviderConfigPath: provider, c.XrayRenderPath: provider} {
		os.MkdirAll(filepath.Dir(p), 0700)
		if e := os.WriteFile(p, b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := migrateSecrets(c); e != nil {
		t.Fatal(e)
	}
	// What the old readers leave between two starts.
	for _, p := range []string{c.StatePath, c.LegacyStatePath} {
		raw, _ := os.ReadFile(p)
		if e := os.WriteFile(p+".corrupt-20261002T021300Z", raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	raw, _ := os.ReadFile(c.ProviderConfigPath)
	_ = os.WriteFile(c.ProviderConfigPath+".tmp", raw, 0600)
	if e := migrateSecrets(c); e != nil {
		t.Fatalf("the next start: %v", e)
	}
	for _, p := range []string{c.StatePath + ".corrupt-20261002T021300Z", c.LegacyStatePath + ".corrupt-20261002T021300Z", c.ProviderConfigPath + ".tmp"} {
		if _, e := os.Stat(p); !os.IsNotExist(e) {
			t.Fatalf("%s left: %v", filepath.Base(p), e)
		}
	}
	got, e := state.Load(c.StatePath)
	if e != nil || got.AgentToken != token {
		t.Fatalf("identity lost: %v", e)
	}
}

// A hand-back (dead-man) runs the old agent, which saves its own state as
// plaintext over the sealed copy, and may leave garbage there. Neither may
// refuse vctl's next start.
func TestTheNextStartSurvivesAHandBackToTheOldAgent(t *testing.T) {
	dir := t.TempDir()
	c := agentcfg.Config{StatePath: filepath.Join(dir, "state.json"), XrayConfigPath: filepath.Join(dir, "operator.json"), ProviderConfigPath: filepath.Join(dir, "provider.json"), XrayRenderPath: filepath.Join(dir, "run", "xray.json"), LegacyStatePath: filepath.Join(dir, "legacy", "state.json"), EntriesPath: filepath.Join(dir, "entries.gz")}
	c.Defaults()
	st, _ := json.Marshal(state.PersistedState{RouterID: "test-router", AgentToken: "SYNTHETIC_ROUTER_TOKEN_0123456789abcdef"})
	for _, p := range []string{c.StatePath, c.LegacyStatePath} {
		os.MkdirAll(filepath.Dir(p), 0700)
		if e := os.WriteFile(p, st, 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := migrateSecrets(c); e != nil {
		t.Fatal(e)
	}
	for _, rewrite := range [][]byte{st, []byte("not json at all")} {
		if e := os.WriteFile(c.LegacyStatePath, rewrite, 0600); e != nil {
			t.Fatal(e)
		}
		if e := migrateSecrets(c); e != nil {
			t.Fatalf("start after the old agent wrote %q: %v", rewrite[:8], e)
		}
	}
}
