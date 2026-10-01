package state

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	in := PersistedState{
		RouterID:          "r-1",
		AgentToken:        "tok",
		AppliedRevisionID: "rev-7",
		ConfigDigest:      "abc",
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path + ".last-good"); err != nil {
		t.Errorf("last-good not written: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.RouterID != "r-1" || got.AgentToken != "tok" || got.AppliedRevisionID != "rev-7" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestLoadMissingReturnsEmpty(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if got.RouterID != "" {
		t.Errorf("expected empty state, got %+v", got)
	}
}

func TestCorruptRecoversFromLastGood(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Save(path, PersistedState{RouterID: "good"}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the primary file; last-good still holds the good copy.
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.RouterID != "good" {
		t.Errorf("expected recovery from last-good, got %+v", got)
	}
}

func TestEnsureIdentityIdempotent(t *testing.T) {
	var s PersistedState
	if err := EnsureIdentity(&s); err != nil {
		t.Fatal(err)
	}
	if s.DeviceIdentifier == "" || s.DevicePublicKey == "" || s.DevicePrivateKey == "" {
		t.Fatalf("identity not populated: %+v", s)
	}
	id, pub := s.DeviceIdentifier, s.DevicePublicKey
	if err := EnsureIdentity(&s); err != nil {
		t.Fatal(err)
	}
	if s.DeviceIdentifier != id || s.DevicePublicKey != pub {
		t.Error("EnsureIdentity mutated existing identity")
	}
}

func TestImportLegacyIdentity(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(legacy, []byte(`{
		"router_id":"legacy-r","agent_token":"legacy-tok",
		"device_identifier":"vectra-aaa","device_public_key":"pk","device_private_key":"sk"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var fresh PersistedState
	if err := Migrate(legacy); err != nil {
		t.Fatal(err)
	}
	imported, err := ImportLegacyIdentity(&fresh, legacy)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !imported || fresh.RouterID != "legacy-r" || fresh.AgentToken != "legacy-tok" {
		t.Errorf("expected import, got %+v (imported=%v)", fresh, imported)
	}

	// Already-identified state must NOT be overwritten.
	existing := PersistedState{RouterID: "mine", AgentToken: "mytok"}
	imported, err = ImportLegacyIdentity(&existing, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if imported || existing.RouterID != "mine" {
		t.Errorf("import overwrote existing identity: %+v (imported=%v)", existing, imported)
	}

	// Missing legacy file is not an error.
	var f2 PersistedState
	if imported, err = ImportLegacyIdentity(&f2, filepath.Join(dir, "absent.json")); err != nil || imported {
		t.Errorf("missing legacy: imported=%v err=%v", imported, err)
	}
}

func TestEncryptedStateFailClosedWithoutBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, PersistedState{AgentToken: "synthetic-secret-token"}); err != nil {
		t.Fatal(err)
	}
	os.Remove(path + ".last-good")
	os.WriteFile(path, []byte(`{"agent_token":"attacker-plaintext"}`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("corrupt or downgraded state silently enrolled")
	}
}

func TestMigrateLegacyAndEncryptedRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	raw := []byte(`{"router_id":"fake-router","agent_token":"synthetic-secret-token"}`)
	os.WriteFile(path, raw, 0o600)
	os.WriteFile(path+".last-good", raw, 0o600)
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{path, path + ".last-good"} {
		b, _ := os.ReadFile(p)
		if bytes.Contains(b, []byte("synthetic-secret-token")) {
			t.Fatal("plaintext state")
		}
	}
	os.WriteFile(path, []byte("corrupt ciphertext"), 0o600)
	if err := Migrate(path); err != nil {
		t.Fatalf("startup migration blocked recovery: %v", err)
	}
	got, err := Load(path)
	if err != nil || got.RouterID != "fake-router" {
		t.Fatalf("recovery %v", err)
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 0 {
		t.Fatal("plaintext corrupt backup created")
	}
}

func TestMigrationSealsHistoricalCorruptArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	artifact := path + ".corrupt-20200101T000000Z"
	secret := []byte(`{"agent_token":"synthetic-artifact-token","partial`)
	os.WriteFile(artifact, secret, 0o600)
	os.WriteFile(path+".tmp", secret, 0o600)
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{artifact, path + ".tmp"} {
		raw, _ := os.ReadFile(p)
		if bytes.Contains(raw, []byte("synthetic-artifact-token")) {
			t.Fatal("old artifact leaked")
		}
	}
}

func TestMissingSealedStateCannotReenroll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, PersistedState{RouterID: "fake"}); err != nil {
		t.Fatal(err)
	}
	os.Remove(path)
	os.Remove(path + ".last-good")
	if _, err := Load(path); err == nil {
		t.Fatal("deleted sealed identity silently reset")
	}
}

func TestCorruptBackupRepairsFromAuthenticatedPrimary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, PersistedState{RouterID: "fake"}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path+".last-good", []byte("corrupt backup"), 0o600)
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.RouterID != "fake" {
		t.Fatalf("primary recovery: %v", err)
	}
}
