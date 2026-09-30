package state

import (
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
