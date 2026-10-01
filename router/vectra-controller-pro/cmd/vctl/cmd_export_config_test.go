package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/vault"
)

func TestOrdinaryConfigExportExcludesKeysAndUnmanagedArtifacts(t *testing.T) {
	dir := t.TempDir()
	c := agentcfg.Config{XrayConfigPath: filepath.Join(dir, "operator"), ProviderConfigPath: filepath.Join(dir, "provider"), EntriesPath: filepath.Join(dir, "entries")}
	out := filepath.Join(t.TempDir(), "export.tgz")
	const secret = "SYNTHETIC-AUDIT-AGENTTOKEN-174f9c31"
	for _, p := range []string{c.XrayConfigPath, c.ProviderConfigPath, c.EntriesPath} {
		if err := vault.WriteFile(p, []byte(secret)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"state.json", "xray.json", "password.log", "old.tar.gz", "secret.tmp"} {
		os.WriteFile(filepath.Join(dir, name), []byte(secret), 0600)
	}
	if err := exportConfigSnapshot(c, out); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(out)
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var names []string
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(b, []byte(secret)) || strings.Contains(h.Name, "key") || strings.Contains(h.Name, ".vault/") {
			t.Fatal("export leaked secret or key")
		}
		names = append(names, h.Name)
	}
	if len(names) != 4 {
		t.Fatalf("export not exact allowlist: %v", names)
	}
	st, _ := os.Stat(out)
	if st.Mode().Perm() != 0600 {
		t.Fatal("export permissions")
	}
	if capture := os.Getenv("VECTRA_HARDENING_ARTIFACT_DIR"); capture != "" {
		os.MkdirAll(capture, 0700)
		if e := os.WriteFile(filepath.Join(capture, "ordinary-config-export.tgz"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
}
func TestOrdinaryConfigExportRefusesCipherTamperAndPlaintext(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "provider")
	c := agentcfg.Config{ProviderConfigPath: p, XrayConfigPath: filepath.Join(dir, "missing1"), EntriesPath: filepath.Join(dir, "missing2")}
	vault.WriteFile(p, []byte("fake"))
	raw, _ := os.ReadFile(p)
	raw[len(raw)-1] ^= 1
	os.WriteFile(p, raw, 0600)
	out := filepath.Join(dir, "out")
	if exportConfigSnapshot(c, out) == nil {
		t.Fatal("tamper exported")
	}
	if _, e := os.Stat(out); !os.IsNotExist(e) {
		t.Fatal("failed export published")
	}
	os.WriteFile(p, []byte(`{"credential":"fake"}`), 0600)
	if exportConfigSnapshot(c, out) == nil {
		t.Fatal("plaintext exported")
	}
}
