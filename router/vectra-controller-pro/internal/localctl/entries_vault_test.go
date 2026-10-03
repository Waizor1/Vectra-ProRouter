package localctl

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEntriesVaultMigrationAndCorruption(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "entries.gz")
	c := &EntriesCache{SubscriptionID: "fake-subscription", Remarks: []string{"test-country"}, Entries: []json.RawMessage{json.RawMessage(`{"outbounds":[{"protocol":"vless","settings":{"id":"synthetic-credential"}}]}`)}}
	raw, err := encodeEntries(c)
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	z := gzip.NewWriter(&gz)
	z.Write(raw)
	z.Close()
	os.WriteFile(p, gz.Bytes(), 0o600)
	if _, err := LoadEntries(p); err == nil {
		t.Fatal("implicit plaintext gzip accepted")
	}
	if err := MigrateEntries(p); err != nil {
		t.Fatal(err)
	}
	encrypted, _ := os.ReadFile(p)
	if bytes.HasPrefix(encrypted, []byte{0x1f, 0x8b}) || bytes.Contains(encrypted, []byte("synthetic-credential")) {
		t.Fatal("unencrypted cache")
	}
	back, err := LoadEntries(p)
	if err != nil || !bytes.Equal(back.Entries[0], c.Entries[0]) {
		t.Fatalf("exact cache recovery: %v", err)
	}
	encrypted[len(encrypted)-1] ^= 1
	os.WriteFile(p, encrypted, 0o600)
	if _, err := LoadEntries(p); err == nil {
		t.Fatal("tampered cache accepted")
	}
	os.WriteFile(p, gz.Bytes(), 0o600)
	if err := MigrateEntries(p); err == nil {
		t.Fatal("plaintext downgrade accepted")
	}
}
