package controlplane

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"time"
)

// RecoveryProof is a keypair-signed proof of device identity used to recover a
// router whose bearer token was lost. The agent signs a canonical message with
// its persistent ed25519 device private key; the panel verifies it against the
// device public key it stored at enrollment and, on success, issues a fresh
// token without requiring the (lost) current token. This makes token loss
// self-healing instead of a permanent brick.
type RecoveryProof struct {
	SignedAt  string `json:"signedAt"`
	Signature string `json:"signature"`
}

// reauthMessage is the exact byte sequence signed and verified by both sides.
// It MUST stay byte-for-byte identical to the panel verifier.
func reauthMessage(deviceIdentifier string, signedAt time.Time) []byte {
	return []byte("vectra-router-reauth:v1\n" + deviceIdentifier + "\n" + signedAt.UTC().Format(time.RFC3339))
}

// SignReauth signs the canonical reauth message for deviceIdentifier at
// signedAt using the base64-std-encoded ed25519 private key from persisted
// device identity.
func SignReauth(privateKeyBase64 string, deviceIdentifier string, signedAt time.Time) (*RecoveryProof, error) {
	rawKey, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode device private key: %w", err)
	}
	if len(rawKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("device private key has unexpected length %d (want %d)", len(rawKey), ed25519.PrivateKeySize)
	}

	signature := ed25519.Sign(ed25519.PrivateKey(rawKey), reauthMessage(deviceIdentifier, signedAt))
	return &RecoveryProof{
		SignedAt:  signedAt.UTC().Format(time.RFC3339),
		Signature: base64.StdEncoding.EncodeToString(signature),
	}, nil
}
