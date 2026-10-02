package state

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"os"
	"path/filepath"
)

// The new Vectra controller (vctl) keeps this agent's state sealed while it
// runs the router: AES-256-GCM, "VCTLVAULT1\n" | nonce | ciphertext, the
// header and the file's absolute path authenticated, the key in a sibling
// directory (<dir>.vault-keys/key) outside this one. After a hand-back this
// agent must still read its own credentials there — the identity mirror
// included — or it would mint a new identity and the panel would see a second
// router. It only reads sealed files; it writes plaintext as before, and vctl
// seals them again when it takes the router back.
const sealedMagic = "VCTLVAULT1\n"

var errSealed = errors.New("state: sealed file cannot be opened")

// readStateFile reads path, opening it when vctl sealed it.
func readStateFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(raw, []byte(sealedMagic)) {
		return raw, err
	}
	target, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	key, err := os.ReadFile(filepath.Join(filepath.Dir(target)+".vault-keys", "key"))
	if err != nil || len(key) != 32 {
		return nil, errSealed
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errSealed
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errSealed
	}
	body := raw[len(sealedMagic):]
	if len(body) < aead.NonceSize()+aead.Overhead() {
		return nil, errSealed
	}
	plain, err := aead.Open(nil, body[:aead.NonceSize()], body[aead.NonceSize():], []byte(sealedMagic+target))
	if err != nil {
		return nil, errSealed
	}
	return plain, nil
}
