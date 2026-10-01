package vault

import (
	"bytes"
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
