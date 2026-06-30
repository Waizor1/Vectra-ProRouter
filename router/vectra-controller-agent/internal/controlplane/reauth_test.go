package controlplane

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func TestSignReauthProducesVerifiableSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	privB64 := base64.StdEncoding.EncodeToString(priv)
	signedAt := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

	proof, err := SignReauth(privB64, "vectra-abc123", signedAt)
	if err != nil {
		t.Fatalf("sign reauth: %v", err)
	}

	if got, want := proof.SignedAt, "2026-06-29T12:00:00Z"; got != want {
		t.Fatalf("signedAt = %q, want %q", got, want)
	}

	sig, err := base64.StdEncoding.DecodeString(proof.Signature)
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}

	// The canonical message MUST match the panel verifier byte-for-byte.
	message := []byte("vectra-router-reauth:v1\nvectra-abc123\n2026-06-29T12:00:00Z")
	if !ed25519.Verify(pub, message, sig) {
		t.Fatal("signature does not verify against the canonical reauth message")
	}
}

func TestSignReauthRejectsInvalidPrivateKey(t *testing.T) {
	if _, err := SignReauth("not-valid-base64-!!!", "vectra-abc", time.Now()); err == nil {
		t.Fatal("expected error for undecodable private key")
	}
	if _, err := SignReauth(base64.StdEncoding.EncodeToString([]byte("too-short")), "vectra-abc", time.Now()); err == nil {
		t.Fatal("expected error for wrong-length private key")
	}
}
