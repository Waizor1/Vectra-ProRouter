package state

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// seal writes plain at path as vectra-controller-pro's vault does: the key in
// <dir>.vault-keys/key, "VCTLVAULT1\n" | nonce | AES-256-GCM, the header and
// the absolute path authenticated.
func seal(t *testing.T, path string, plain []byte) {
	t.Helper()
	target, _ := filepath.Abs(path)
	keyPath := filepath.Join(filepath.Dir(target)+".vault-keys", "key")
	key, err := os.ReadFile(keyPath)
	if err != nil {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
		_ = os.MkdirAll(filepath.Dir(keyPath), 0o700)
		if err := os.WriteFile(keyPath, key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, aead.NonceSize())
	_, _ = rand.Read(nonce)
	out := append([]byte(sealedMagic), nonce...)
	out = aead.Seal(out, nonce, plain, []byte(sealedMagic+target))
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// After a hand-back from vctl this agent finds its state, last-good and
// identity mirror sealed. It must read them, and keep its identity: minting
// a new one would show the panel a second router.
func TestLoadReadsStateSealedByVctl(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vectra-controller")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "state.json")
	creds := []byte(`{"router_id":"r-1","agent_token":"synthetic-token","device_identifier":"vectra-1","device_private_key":"synthetic-key"}`)
	seal(t, path, creds)
	seal(t, lastGoodPath(path), creds)
	seal(t, identityMirrorPath(path), creds)
	got, err := Load(path)
	if err != nil || got.RouterID != "r-1" || got.AgentToken != "synthetic-token" || got.DevicePrivateKey != "synthetic-key" {
		t.Fatalf("sealed state: %+v %v", got, err)
	}
	if left, _ := filepath.Glob(path + ".corrupt-*"); len(left) != 0 {
		t.Fatalf("copied the ciphertext as corrupt: %v", left)
	}
	// Only the mirror holds credentials, sealed: still this router.
	q := filepath.Join(t.TempDir(), "vectra-controller", "state.json")
	_ = os.MkdirAll(filepath.Dir(q), 0o755)
	seal(t, identityMirrorPath(q), creds)
	if err := os.WriteFile(q, []byte(`{"mode":"proxy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(q); err != nil || got.AgentToken != "synthetic-token" {
		t.Fatalf("sealed mirror: %+v %v", got, err)
	}
}

// A sealed file whose key is gone is not corrupt: no ciphertext copy, and the
// credentials come from whatever copy can still be read.
func TestLoadFallsBackPastASealedFileWithoutItsKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vectra-controller")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, "state.json")
	seal(t, path, []byte(`{"router_id":"r-1","agent_token":"synthetic-token"}`))
	if err := os.RemoveAll(dir + ".vault-keys"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identityMirrorPath(path), []byte(`{"router_id":"r-1","agent_token":"synthetic-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.AgentToken != "synthetic-token" {
		t.Fatalf("fallback: %+v %v", got, err)
	}
	if left, _ := filepath.Glob(path + ".corrupt-*"); len(left) != 0 {
		t.Fatalf("copied the ciphertext as corrupt: %v", left)
	}
	// A tampered envelope (another file's ciphertext) is refused.
	other := filepath.Join(dir, "other.json")
	seal(t, other, []byte(`{"router_id":"r-x","agent_token":"x"}`))
	raw, _ := os.ReadFile(other)
	moved := filepath.Join(dir, "moved.json")
	_ = os.WriteFile(moved, raw, 0o600)
	if _, err := readStateFile(moved); err == nil {
		t.Fatal("opened ciphertext moved from another path")
	}
}
