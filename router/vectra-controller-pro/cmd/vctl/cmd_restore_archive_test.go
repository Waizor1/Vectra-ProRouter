package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"vectra-controller-pro/internal/vault"
)

func recoveryFixture(t *testing.T, name string, typ byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	body := "fake-recovery-password"
	size := int64(len(body))
	if typ != tar.TypeReg {
		size = 0
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: typ, Mode: 0600, Size: size, Linkname: "/tmp/escape"}); err != nil {
		t.Fatal(err)
	}
	if size != 0 {
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestRestorePasswallArchiveAuthenticatesAndStreams(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/config"), 0700); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "passwall2-1.tar.gz.vault")
	raw := recoveryFixture(t, "etc/config/passwall2", tar.TypeReg)
	if err := vault.WriteFile(backup, raw); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(backup)
	if bytes.Contains(b, []byte("fake-recovery-password")) {
		t.Fatal("plaintext archive persisted")
	}
	if err := restorePasswallArchive(backup, root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "etc/config/passwall2"))
	if err != nil || string(b) != "fake-recovery-password" {
		t.Fatalf("restore %q %v", b, err)
	}
	b, _ = os.ReadFile(backup)
	b[len(b)-1] ^= 1
	_ = os.WriteFile(backup, b, 0600)
	_ = os.Remove(filepath.Join(root, "etc/config/passwall2"))
	if err := restorePasswallArchive(backup, root); err == nil {
		t.Fatal("corrupt archive accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "etc/config/passwall2")); !os.IsNotExist(err) {
		t.Fatal("corrupt archive wrote target")
	}
}
func TestRestorePasswallArchiveRejectsUnsafeEntries(t *testing.T) {
	for _, name := range []string{"../escape", "/etc/config/passwall2", "etc/config/other"} {
		if _, err := validatePasswallArchive(recoveryFixture(t, name, tar.TypeReg)); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if _, err := validatePasswallArchive(recoveryFixture(t, "etc/config/passwall2", tar.TypeSymlink)); err == nil {
		t.Fatal("accepted symlink")
	}
	root := t.TempDir()
	outside := t.TempDir()
	_ = os.Symlink(outside, filepath.Join(root, "etc"))
	backup := filepath.Join(t.TempDir(), "passwall2-2.tar.gz.vault")
	_ = vault.WriteFile(backup, recoveryFixture(t, "etc/config/passwall2", tar.TypeReg))
	if err := restorePasswallArchive(backup, root); err == nil {
		t.Fatal("accepted symlink parent")
	}
	if err := restorePasswallArchive(strings.TrimSuffix(backup, ".vault"), root); err == nil {
		t.Fatal("accepted downgrade")
	}
}
