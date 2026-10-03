package vault

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTripScopePermissionsAndTamper(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret.json")
	secret := []byte("synthetic-vless-credential")
	if err := WriteFile(p, secret); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if bytes.Contains(raw, secret) {
		t.Fatal("plaintext persisted")
	}
	got, err := ReadFile(p)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("roundtrip: %v", err)
	}
	for _, f := range []string{p, testKeyPath(t, p)} {
		s, e := os.Stat(f)
		if e != nil || s.Mode().Perm() != 0o600 {
			t.Fatalf("permissions %s: %v", f, e)
		}
	}
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(other); !errors.Is(err, ErrInvalid) {
		t.Fatalf("transplant accepted: %v", err)
	}
	raw[len(raw)-1] ^= 1
	os.WriteFile(p, raw, 0o600)
	if _, err := ReadFile(p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tamper accepted: %v", err)
	}
}

func TestExplicitMigrationAndDowngrade(t *testing.T) {
	p := filepath.Join(t.TempDir(), "legacy.json")
	plain := []byte(`{"fake":"credential"}`)
	os.WriteFile(p, plain, 0o600)
	if _, err := ReadFile(p); err == nil {
		t.Fatal("implicit plaintext read")
	}
	if err := MigrateFile(p, func(raw []byte) error {
		if !bytes.Equal(raw, plain) {
			return errors.New("bad")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, plain, 0o600)
	if err := MigrateFile(p, func([]byte) error { return nil }); err == nil {
		t.Fatal("downgrade migrated")
	}
	if _, err := ReadFile(p); err == nil {
		t.Fatal("downgrade read")
	}
}

func TestInterruptedMigrationResumesEncryptedStage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "legacy.json")
	plain := []byte("synthetic-only")
	if err := WriteFile(p, plain); err != nil {
		t.Fatal(err)
	}
	target, _, _, stage, _ := paths(p)
	raw, _ := os.ReadFile(target)
	os.WriteFile(stage, raw, 0o600)
	os.WriteFile(target, plain, 0o600)
	got, err := ReadFile(p)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("resume: %v", err)
	}
	raw, _ = os.ReadFile(target)
	if !bytes.HasPrefix(raw, []byte(magic)) {
		t.Fatal("resume kept plaintext")
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("stage not consumed")
	}
}

func TestLostOrUnsafeKeyFailsClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state")
	if err := WriteFile(p, []byte("fake")); err != nil {
		t.Fatal(err)
	}
	k := testKeyPath(t, p)
	if err := os.Chmod(k, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(p); err == nil {
		t.Fatal("public key permissions accepted")
	}
	os.Remove(k)
	if err := WriteFile(p, []byte("new")); err == nil {
		t.Fatal("lost key silently replaced")
	}
	if _, err := os.Stat(k); !os.IsNotExist(err) {
		t.Fatal("replacement key created")
	}
}

func TestFailedValidationLeavesLegacyUntouched(t *testing.T) {
	p := filepath.Join(t.TempDir(), "legacy")
	plain := []byte("bad legacy")
	os.WriteFile(p, plain, 0o600)
	if err := MigrateFile(p, func([]byte) error { return errors.New("bad") }); err == nil {
		t.Fatal("invalid migrated")
	}
	raw, _ := os.ReadFile(p)
	if !bytes.Equal(raw, plain) {
		t.Fatal("failed validation changed original")
	}
}

func TestLostDirectoryKeyCannotBeResetByNewFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "one")
	if err := WriteFile(p, []byte("fake")); err != nil {
		t.Fatal(err)
	}
	os.Remove(testKeyPath(t, p))
	if err := WriteFile(filepath.Join(dir, "two"), []byte("fake-new")); err == nil {
		t.Fatal("new path replaced lost directory key")
	}
}

func TestCrossProcessWriters(t *testing.T) {
	if path := os.Getenv("VCTL_VAULT_TEST_CHILD"); path != "" {
		for i := 0; i < 30; i++ {
			if err := WriteFile(path, []byte("synthetic-child-secret")); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	p := filepath.Join(t.TempDir(), "shared")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var cmds []*exec.Cmd
	for i := 0; i < 2; i++ {
		cmd := exec.Command(exe, "-test.run=^TestCrossProcessWriters$")
		cmd.Env = append(os.Environ(), "VCTL_VAULT_TEST_CHILD="+p)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ReadFile(p)
	if err != nil || string(got) != "synthetic-child-secret" {
		t.Fatalf("concurrent write: %v", err)
	}
}

func TestExplicitRetirementAllowsNextOwnerButKeepsDeviceKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "provider.json")
	if err := WriteFile(p, []byte("fake-owner-one")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(testKeyPath(t, p))
	if err := RemoveFile(p); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(p); !os.IsNotExist(err) {
		t.Fatalf("explicitly retired file: %v", err)
	}
	if err := WriteFile(p, []byte("fake-owner-two")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(testKeyPath(t, p))
	if !bytes.Equal(before, after) {
		t.Fatal("retirement reset device key")
	}
}

func TestRetirementTombstoneRejectsDowngradeAndInterruptedDeletion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "provider")
	if err := WriteFile(p, []byte("fake-old-owner")); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	_, _, marker, stage, _ := paths(p)
	if err := atomic(marker, []byte("deleted-v1\n")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(stage, raw, 0o600)
	if _, err := ReadFile(p); !os.IsNotExist(err) {
		t.Fatalf("old owner after tombstone: %v", err)
	}
	os.WriteFile(p, []byte("fake-plaintext-downgrade"), 0o600)
	validated := false
	if err := MigrateFile(p, func([]byte) error { validated = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if validated {
		t.Fatal("migration reopened after release")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("downgraded file retained")
	}
}

func TestRetirementOfUnusedPathSupportsFutureEncryptedWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "never-used")
	if err := RemoveFile(p); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(p, []byte("fake-future-owner")); err != nil {
		t.Fatal(err)
	}
}

func testKeyPath(t *testing.T, path string) string {
	t.Helper()
	p, err := KeyPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestKeyPathDefaultRootsAndDescendants(t *testing.T) {
	for _, storage := range []string{"/etc/vectra-controller-pro", "/etc/vectra-controller-pro/archives", "/var/run/vectra-controller-pro", "/var/run/vectra-controller-pro/staging"} {
		base := "/etc/vectra-controller-pro"
		if strings.HasPrefix(storage, "/var/run/") {
			base = "/var/run/vectra-controller-pro"
		}
		hash := sha256.Sum256([]byte(storage))
		want := filepath.Join(base+"-vault-keys", hex.EncodeToString(hash[:]), "key")
		got, err := KeyPath(filepath.Join(storage, "provider.json"))
		if err != nil || got != want {
			t.Fatalf("mapping %s: %s %v", storage, got, err)
		}
	}
	for _, storage := range []string{"/etc/vectra-controller-pro-other", "/tmp/synthetic-storage"} {
		got, err := KeyPath(filepath.Join(storage, "config.json"))
		if err != nil || got != filepath.Join(storage+".vault-keys", "key") {
			t.Fatalf("fallback mapping: %s %v", got, err)
		}
	}
}

func TestWholeConfigurationDirectoryCopyContainsNoDecryptionKey(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "configuration")
	clone := filepath.Join(root, "copied-configuration")
	p := filepath.Join(source, "provider.json")
	if err := WriteFile(p, []byte("synthetic-config-copy-secret")); err != nil {
		t.Fatal(err)
	}
	keyPath := testKeyPath(t, p)
	localKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(keyPath, source+string(filepath.Separator)) {
		t.Fatal("key inside config directory")
	}
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		dest := filepath.Join(clone, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o700)
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if bytes.Equal(data, localKey) {
			t.Fatal("key included in config copy")
		}
		return os.WriteFile(dest, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	clonePath := filepath.Join(clone, "provider.json")
	if _, err := os.Stat(testKeyPath(t, clonePath)); !os.IsNotExist(err) {
		t.Fatal("key unexpectedly in clone")
	}
	if _, err := ReadFile(clonePath); err == nil {
		t.Fatal("config-only clone decrypted")
	}
}

// An old reader (vectra-reporter 1.0.0-r2, the legacy agent) that cannot parse
// a sealed file keeps a byte copy of it under another name (state.json.corrupt-
// <time>). That copy is ciphertext authenticated to the original path: it can
// never be opened, and it reveals nothing. Migrating crash artifacts must take
// it away, not fail the daemon's start on it (1111, 2026-10-02).
func TestMigrateArtifactRemovesASealedCopyUnderAnotherName(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if err := WriteFile(p, []byte(`{"synthetic":true}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	copyPath := p + ".corrupt-20261002T021300Z"
	if err := os.WriteFile(copyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateFile(copyPath, func([]byte) error { return nil }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("precondition: MigrateFile on the copy = %v, want ErrInvalid", err)
	}
	if err := MigrateArtifact(copyPath); err != nil {
		t.Fatalf("MigrateArtifact = %v", err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("the unopenable copy is still there: %v", err)
	}
	if got, err := ReadFile(p); err != nil || string(got) != `{"synthetic":true}` {
		t.Fatalf("the original suffered: %q %v", got, err)
	}
	// A plaintext artifact is still sealed, an openable sealed one kept.
	plain := p + ".tmp"
	if err := os.WriteFile(plain, []byte("partial synthetic secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateArtifact(plain); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(plain); bytes.Contains(b, []byte("synthetic secret")) {
		t.Fatal("plaintext artifact not sealed")
	}
	if err := MigrateArtifact(plain); err != nil {
		t.Fatalf("second pass over a sealed artifact: %v", err)
	}
	if _, err := ReadFile(plain); err != nil {
		t.Fatalf("sealed artifact lost: %v", err)
	}
	if err := MigrateArtifact(filepath.Join(dir, "absent")); err != nil {
		t.Fatal(err)
	}
}

// A wrong or replaced key makes every artifact fail to open; that is no proof
// it is a copy. Only an artifact that opens under the name it was copied from
// (its own name minus .corrupt-<time> or .tmp) is a proven copy and may go;
// anything else stays where it is, and does not refuse the start.
func TestMigrateArtifactDeletesOnlyProvenCopies(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if err := WriteFile(p, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	// A sealed artifact of its own (a .tmp sealed under its own name).
	own := p + ".tmp"
	if err := WriteFile(own, []byte("own sealed artifact")); err != nil {
		t.Fatal(err)
	}
	// Another sealed file, copied under an artifact name it was not derived from.
	other := filepath.Join(dir, "other.json")
	if err := WriteFile(other, []byte(`{"b":2}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(other)
	stray := p + ".corrupt-20261002T021300Z"
	if err := os.WriteFile(stray, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// Replace the key: nothing opens any more.
	kp := testKeyPath(t, p)
	if err := os.WriteFile(kp, bytes.Repeat([]byte{7}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{own, stray} {
		if err := MigrateArtifact(a); err != nil {
			t.Fatalf("%s: %v", filepath.Base(a), err)
		}
		if _, err := os.Stat(a); err != nil {
			t.Fatalf("%s deleted without proof it is a copy: %v", filepath.Base(a), err)
		}
	}
}

// The old Vectra agent (a separate module) opens what this vault seals with
// the standard library only: "VCTLVAULT1\n" | 12-byte nonce | AES-256-GCM,
// the header and the absolute path as additional data, the 32-byte key at
// <dir>.vault-keys/key. This pins that format.
func TestSealedFilesOpenWithTheStandardLibraryAsTheOldAgentDoes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vectra-controller")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.json.identity")
	if err := WriteFile(path, []byte(`{"agent_token":"synthetic"}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	key, err := os.ReadFile(filepath.Join(dir+".vault-keys", "key"))
	if err != nil || len(key) != 32 {
		t.Fatalf("key at <dir>.vault-keys/key: %v", err)
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	body := raw[len("VCTLVAULT1\n"):]
	plain, err := aead.Open(nil, body[:12], body[12:], []byte("VCTLVAULT1\n"+path))
	if err != nil || string(plain) != `{"agent_token":"synthetic"}` {
		t.Fatalf("format drifted from what the old agent reads: %v", err)
	}
}

// Unseal writes the plaintext first and drops the seal last: interrupted in
// between, the next Unseal finishes it instead of failing on the plaintext.
func TestUnsealFinishesAnInterruptedUnseal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy", "state.json.identity")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	plain := []byte(`{"router_id":"r","agent_token":"synthetic"}`)
	if err := WriteFile(path, plain); err != nil {
		t.Fatal(err)
	}
	if err := Unseal(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, plain) || !Unsealed(path) {
		t.Fatal("not unsealed")
	}
	// Sealed again, then interrupted after the plaintext was written.
	if err := MigrateFile(path, func([]byte) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, plain, 0o600)
	if err := Unseal(path); err != nil {
		t.Fatalf("interrupted unseal: %v", err)
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, plain) || !Unsealed(path) {
		t.Fatal("the interrupted unseal was not finished")
	}
}
