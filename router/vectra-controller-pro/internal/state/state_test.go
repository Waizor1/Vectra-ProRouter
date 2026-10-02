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

// 1111, 2026-10-02: after a planned restart vctl 0.7.0-r1 never started again
// — "secret storage migration: vault: invalid or unauthenticated envelope".
// The old vectra-reporter read the sealed state.json every minute, could not
// parse it and kept a byte copy as state.json.corrupt-<time>; Migrate then
// treated that ciphertext copy as a crash artifact it could not open.
func TestMigrateSurvivesAnOldReadersCorruptCopy(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	want := PersistedState{RouterID: "router-synthetic", AgentToken: "token-synthetic"}
	if err := Save(p, want); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if err := os.WriteFile(p+".corrupt-20261002T021300Z", raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(p); err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	got, err := Load(p)
	if err != nil || got.RouterID != want.RouterID || got.AgentToken != want.AgentToken {
		t.Fatalf("Load = %+v %v", got, err)
	}
}

// The reporter only reads the router's state: it must never write it (Load
// restores from last-good and saves), and it reads both a sealed state (vctl
// 0.7) and a plaintext one (a router rolled back to 0.6.0-r36).
func TestLoadReadOnlyReadsSealedAndPlaintextAndNeverWrites(t *testing.T) {
	dir := t.TempDir()
	sealed := filepath.Join(dir, "sealed", "state.json")
	want := PersistedState{RouterID: "router-synthetic", AgentToken: "token-synthetic"}
	if err := Save(sealed, want); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadReadOnly(sealed); err != nil || got.RouterID != want.RouterID {
		t.Fatalf("sealed: %+v %v", got, err)
	}
	plain := filepath.Join(dir, "plain", "state.json")
	_ = os.MkdirAll(filepath.Dir(plain), 0o700)
	if err := os.WriteFile(plain, []byte(`{"router_id":"router-plain","agent_token":"t"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadDir(filepath.Dir(plain))
	got, err := LoadReadOnly(plain)
	if err != nil || got.RouterID != "router-plain" {
		t.Fatalf("plaintext: %+v %v", got, err)
	}
	after, _ := os.ReadDir(filepath.Dir(plain))
	if len(after) != len(before) {
		t.Fatalf("a read wrote files: %d -> %d", len(before), len(after))
	}
	if b, _ := os.ReadFile(plain); !bytes.HasPrefix(b, []byte(`{"router_id"`)) {
		t.Fatal("a read changed the plaintext state")
	}
	// A sealed file under a marker that is not there to open: an error, never a write.
	broken := filepath.Join(dir, "sealed", "copy.json")
	raw, _ := os.ReadFile(sealed)
	_ = os.WriteFile(broken, raw, 0o600)
	if _, err := LoadReadOnly(broken); err == nil {
		t.Fatal("an unopenable copy loaded")
	}
}

// A hand-back starts the old Vectra agent, which cannot read the sealed legacy
// state: it keeps a copy and writes its state back as plaintext over the
// sealed path. vctl's next start must not refuse that (it is the old agent's
// own file, legitimately rewritten): it seals it again.
func TestMigrateLegacyResealsWhatTheOldAgentWroteBack(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "legacy", "state.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, []byte(`{"router_id":"r-legacy","agent_token":"t"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(p); err != nil {
		t.Fatal(err)
	}
	// The old agent, after a hand-back: a copy, then plaintext over both paths.
	raw, _ := os.ReadFile(p)
	_ = os.WriteFile(p+".corrupt-20261002T022100Z", raw, 0o600)
	for _, f := range []string{p, p + ".last-good"} {
		if err := os.WriteFile(f, []byte(`{"router_id":"r-legacy","agent_token":"t2"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateLegacy(p); err != nil {
		t.Fatalf("MigrateLegacy after the old agent wrote back: %v", err)
	}
	if b, _ := os.ReadFile(p); !bytes.HasPrefix(b, []byte("VCTLVAULT")) {
		t.Fatal("the legacy state was not sealed again")
	}
	if got, err := LoadReadOnly(p); err != nil || got.AgentToken != "t2" {
		t.Fatalf("LoadReadOnly: token %q, %v", got.AgentToken, err)
	}
	if left, _ := filepath.Glob(p + ".corrupt-*"); len(left) != 0 {
		t.Fatalf("the old agent's copy of the sealed state is still there: %v", left)
	}
}
