// Package happcrypt produces Happ "crypto links" — encrypted subscription deep
// links (happ://crypt{2,3,4,5}/<payload>) that only the Happ client app can
// decrypt. This protects a VPN provider's VLESS subscription URLs so end users
// of the app cannot view, edit, share, or extract the underlying server keys.
//
// crypt2/3/4 are produced fully OFFLINE here (RSA-4096, PKCS#1 v1.5, standard
// base64) using the official public keys published in @kastov/cryptohapp,
// embedded verbatim under keys/. crypt4 is the documented baseline (Happ marks
// it deprecated in favour of crypt5, but it is the strongest fully-offline,
// no-third-party option).
//
// crypt5 — the current Happ standard — has a closed algorithm and app-embedded
// keys; the only license-clean way to mint a real crypt5 link is Happ's
// official API (see api.go). We deliberately do NOT bundle the reverse-engineered
// crypt5 keys (they are extracted from the app and not licensed for
// redistribution) and we keep this package dependency-free (stdlib only).
package happcrypt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"embed"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"sync"
)

//go:embed keys/crypt2.pub.pem keys/crypt3.pub.pem keys/crypt4.pub.pem
var keyFS embed.FS

// Deep-link prefixes per Happ crypto-link version.
const (
	PrefixV2 = "happ://crypt2/"
	PrefixV3 = "happ://crypt3/"
	PrefixV4 = "happ://crypt4/"
	PrefixV5 = "happ://crypt5/"
)

var (
	keyOnce             sync.Once
	keyV2, keyV3, keyV4 *rsa.PublicKey
	keyErr              error
)

func loadKeys() {
	keyOnce.Do(func() {
		if keyV2, keyErr = loadEmbeddedKey("keys/crypt2.pub.pem"); keyErr != nil {
			return
		}
		if keyV3, keyErr = loadEmbeddedKey("keys/crypt3.pub.pem"); keyErr != nil {
			return
		}
		keyV4, keyErr = loadEmbeddedKey("keys/crypt4.pub.pem")
	})
}

func loadEmbeddedKey(name string) (*rsa.PublicKey, error) {
	b, err := keyFS.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("happcrypt: read embedded key %s: %w", name, err)
	}
	return ParsePublicKeyPEM(b)
}

// ParsePublicKeyPEM parses an SPKI/X.509 ("BEGIN PUBLIC KEY") RSA public key.
func ParsePublicKeyPEM(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("happcrypt: no PEM block found")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("happcrypt: parse public key: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("happcrypt: embedded key is not RSA")
	}
	return rsaPub, nil
}

// EncryptRSA encrypts content for pub using RSA PKCS#1 v1.5 and returns standard
// base64 — matching the canonical @kastov/cryptohapp encoder exactly. PKCS#1
// v1.5 is randomized, so the output differs per call (test via round-trip, not a
// fixed string).
func EncryptRSA(pub *rsa.PublicKey, content string) (string, error) {
	if pub == nil {
		return "", fmt.Errorf("happcrypt: nil public key")
	}
	if max := pub.Size() - 11; len(content) > max {
		return "", fmt.Errorf("happcrypt: content %d bytes exceeds RSA PKCS#1 v1.5 limit of %d bytes for this key — use crypt5 (no length limit) for long subscription URLs", len(content), max)
	}
	ct, err := rsa.EncryptPKCS1v15(rand.Reader, pub, []byte(content))
	if err != nil {
		return "", fmt.Errorf("happcrypt: rsa encrypt: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// LinkOffline builds a happ://crypt{2,3,4}/<base64> link entirely offline using
// the embedded official public key. version must be 2, 3, or 4. crypt5 has no
// public algorithm/key and must be minted via the API (APIClient.EncryptV5).
func LinkOffline(version int, content string) (string, error) {
	loadKeys()
	if keyErr != nil {
		return "", keyErr
	}
	var (
		pub    *rsa.PublicKey
		prefix string
	)
	switch version {
	case 2:
		pub, prefix = keyV2, PrefixV2
	case 4:
		pub, prefix = keyV4, PrefixV4
	case 3:
		pub, prefix = keyV3, PrefixV3
	default:
		return "", fmt.Errorf("happcrypt: offline encryption supports crypt2/3/4 only (got v%d); crypt5 requires APIClient.EncryptV5", version)
	}
	b64, err := EncryptRSA(pub, content)
	if err != nil {
		return "", err
	}
	return prefix + b64, nil
}

// PublicKey returns the embedded official RSA public key for crypt version 2,3,4.
func PublicKey(version int) (*rsa.PublicKey, error) {
	loadKeys()
	if keyErr != nil {
		return nil, keyErr
	}
	switch version {
	case 2:
		return keyV2, nil
	case 3:
		return keyV3, nil
	case 4:
		return keyV4, nil
	default:
		return nil, fmt.Errorf("happcrypt: no embedded key for crypt%d", version)
	}
}
