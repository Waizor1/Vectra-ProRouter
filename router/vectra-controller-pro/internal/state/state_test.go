package state

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/vault"
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
	if err := MigrateLegacy(p, true); err != nil {
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
	if err := MigrateLegacy(p, true); err != nil {
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

// The old agent's identity mirror holds its token and device private key in
// plaintext. vctl seals it when the installed old agent reads sealed files,
// keeps it when it does not (a hand-back would otherwise mint a new
// identity), and still imports the identity from it either way.
func TestMigrateLegacySealsTheIdentityMirrorOnlyWhenTheOldAgentCanReadIt(t *testing.T) {
	const marker = "synthetic-mirror-private-key"
	mirror := []byte(`{"router_id":"r-legacy","agent_token":"synthetic-mirror-token","device_private_key":"` + marker + `"}`)
	for _, seal := range []bool{false, true} {
		p := filepath.Join(t.TempDir(), "legacy", "state.json")
		_ = os.MkdirAll(filepath.Dir(p), 0o700)
		_ = os.WriteFile(p, []byte(`{"router_id":"r-legacy","agent_token":"synthetic-mirror-token"}`), 0o600)
		_ = os.WriteFile(p+".identity", mirror, 0o600)
		_ = os.WriteFile(filepath.Join(filepath.Dir(p), ".vectra-state-1.tmp"), mirror, 0o600)
		if err := MigrateLegacy(p, seal); err != nil {
			t.Fatalf("seal=%v: %v", seal, err)
		}
		raw, _ := os.ReadFile(p + ".identity")
		if got := bytes.Contains(raw, []byte(marker)); got == seal {
			t.Fatalf("seal=%v: mirror plaintext=%v", seal, got)
		}
		tmp, _ := os.ReadFile(filepath.Join(filepath.Dir(p), ".vectra-state-1.tmp"))
		if seal && bytes.Contains(tmp, []byte(marker)) {
			t.Fatal("the old agent's crash-left temp file stayed plaintext")
		}
		// The old agent rewrites its mirror after a hand-back: sealed again.
		if seal {
			_ = os.WriteFile(p+".identity", mirror, 0o600)
			if err := MigrateLegacy(p, true); err != nil {
				t.Fatal(err)
			}
			if raw, _ := os.ReadFile(p + ".identity"); bytes.Contains(raw, []byte(marker)) {
				t.Fatal("the rewritten mirror stayed plaintext")
			}
		}
		var st PersistedState
		if ok, err := ImportLegacyIdentity(&st, p); err != nil || !ok || st.AgentToken != "synthetic-mirror-token" {
			t.Fatalf("seal=%v: import %v %v", seal, ok, err)
		}
	}
}

// vctl adopts the router's identity from whichever of the old agent's files
// holds it: plaintext the old agent wrote back over a sealed copy, or its
// mirror alone. A legacy file that exists but cannot be read is an error,
// never "no legacy identity" — that would mint a second router record.
func TestImportLegacyIdentityNeverMintsOverAnUnreadableLegacyIdentity(t *testing.T) {
	p := filepath.Join(t.TempDir(), "legacy", "state.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte(`{"router_id":"r-legacy","agent_token":"t1"}`), 0o600)
	if err := MigrateLegacy(p, true); err != nil {
		t.Fatal(err)
	}
	// Hand-back: plaintext over the sealed state (a vault "downgrade").
	_ = os.WriteFile(p, []byte(`{"router_id":"r-legacy","agent_token":"t2"}`), 0o600)
	var st PersistedState
	if ok, err := ImportLegacyIdentity(&st, p); err != nil || !ok || st.AgentToken != "t2" {
		t.Fatalf("plaintext written back by the old agent: %v %v %q", ok, err, st.AgentToken)
	}
	// Only the mirror holds credentials (the state was lost).
	q := filepath.Join(t.TempDir(), "legacy", "state.json")
	_ = os.MkdirAll(filepath.Dir(q), 0o700)
	_ = os.WriteFile(q+".identity", []byte(`{"router_id":"r-mirror","agent_token":"t3"}`), 0o600)
	st = PersistedState{}
	if ok, err := ImportLegacyIdentity(&st, q); err != nil || !ok || st.RouterID != "r-mirror" {
		t.Fatalf("mirror only: %v %v", ok, err)
	}
	// Sealed, and the key lost: unreadable, not absent.
	r := filepath.Join(t.TempDir(), "legacy", "state.json")
	_ = os.MkdirAll(filepath.Dir(r), 0o700)
	_ = os.WriteFile(r, []byte(`{"router_id":"r-sealed","agent_token":"t4"}`), 0o600)
	if err := MigrateLegacy(r, true); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(r) + ".vault-keys"); err != nil {
		t.Fatal(err)
	}
	st = PersistedState{}
	if ok, err := ImportLegacyIdentity(&st, r); err == nil || ok {
		t.Fatalf("an unreadable legacy identity was taken for none: %v %v", ok, err)
	}
	// Nothing there at all: a fresh enrollment.
	st = PersistedState{}
	if ok, err := ImportLegacyIdentity(&st, filepath.Join(t.TempDir(), "absent.json")); err != nil || ok {
		t.Fatalf("absent: %v %v", ok, err)
	}
}

// A damaged last-good copy must not block sealing a valid plaintext primary
// (it used to: the recovery read the primary through the vault, which refuses
// plaintext that was never sealed, and every start failed).
func TestMigrateSealsAValidPrimaryPastADamagedLastGood(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	_ = os.WriteFile(p, []byte(`{"router_id":"r-1","agent_token":"synthetic-primary-token"}`), 0o600)
	_ = os.WriteFile(p+".last-good", []byte(`{"router_id":`), 0o600)
	if err := Migrate(p); err != nil {
		t.Fatalf("migration blocked by a damaged backup: %v", err)
	}
	for _, f := range []string{p, p + ".last-good"} {
		if raw, _ := os.ReadFile(f); bytes.Contains(raw, []byte("synthetic-primary-token")) {
			t.Fatalf("%s left plaintext", filepath.Base(f))
		}
	}
	if got, err := Load(p); err != nil || got.RouterID != "r-1" {
		t.Fatalf("load: %v %v", got.RouterID, err)
	}
}

// The old agent downgraded below the vault-read release reads only plaintext:
// a mirror sealed while the newer agent was installed is unsealed for it.
func TestAMirrorSealedForANewAgentIsUnsealedForAnOldOne(t *testing.T) {
	p := filepath.Join(t.TempDir(), "legacy", "state.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	creds := []byte(`{"router_id":"r-legacy","agent_token":"synthetic-downgrade-token"}`)
	_ = os.WriteFile(p, creds, 0o600)
	_ = os.WriteFile(p+".identity", creds, 0o600)
	if err := MigrateLegacy(p, true); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(p + ".identity"); bytes.Contains(raw, []byte("synthetic-downgrade-token")) {
		t.Fatal("not sealed for the new agent")
	}
	if err := MigrateLegacy(p, false); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p + ".identity")
	if !bytes.Contains(raw, []byte("synthetic-downgrade-token")) || !vault.Unsealed(p+".identity") {
		t.Fatal("the old agent cannot read its mirror")
	}
	// Upgraded again: sealed again.
	if err := MigrateLegacy(p, true); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(p + ".identity"); bytes.Contains(raw, []byte("synthetic-downgrade-token")) {
		t.Fatal("not sealed again")
	}
}
