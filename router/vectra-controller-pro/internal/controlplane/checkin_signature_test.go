// SPDX-License-Identifier: LicenseRef-Vectra-Proprietary
// Copyright (c) 2026 Vectra. All rights reserved.

package controlplane

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

//go:embed testdata/router-auth-vectors.json
var routerAuthVectorsJSON []byte

type routerAuthVectors struct {
	CheckInSignature struct {
		Body                   string `json:"body"`
		BodySha256Hex          string `json:"bodySha256Hex"`
		DevicePrivateKeyBase64 string `json:"devicePrivateKeyBase64"`
		DevicePublicKeyBase64  string `json:"devicePublicKeyBase64"`
		Message                string `json:"message"`
		Nonce                  string `json:"nonce"`
		RouterID               string `json:"routerId"`
		SignatureBase64        string `json:"signatureBase64"`
	} `json:"checkInSignature"`
	DeviceBinding struct {
		Binding   string `json:"binding"`
		BoardName string `json:"boardName"`
		MAC       string `json:"mac"`
	} `json:"deviceBinding"`
}

func loadRouterAuthVectors(t *testing.T) routerAuthVectors {
	t.Helper()
	var vectors routerAuthVectors
	if err := json.Unmarshal(routerAuthVectorsJSON, &vectors); err != nil {
		t.Fatalf("decode embedded vectors: %v", err)
	}
	return vectors
}

// The embedded copy must stay byte-identical to the shared fixture the panel
// and the legacy agent use, or the sides can silently drift. Skipped when the
// shared tree is not present on this branch (origin/main carries only
// job-contract-fixtures.json; the shared router-auth fixture lands with the
// panel verifier), in which case the embedded copy — itself a byte-identical
// copy of that fixture — is authoritative here.
func TestRouterAuthVectorsMatchSharedFixture(t *testing.T) {
	sharedPath := filepath.Join("..", "..", "..", "..", "packages", "contracts", "fixtures", "router-auth-vectors.json")
	shared, err := os.ReadFile(sharedPath)
	if err != nil {
		t.Skipf("shared fixture not available (%v); embedded copy is authoritative here", err)
	}
	if string(shared) != string(routerAuthVectorsJSON) {
		t.Fatalf("embedded testdata/router-auth-vectors.json drifted from %s; re-copy it", sharedPath)
	}
}

func TestBodySha256HexMatchesVector(t *testing.T) {
	v := loadRouterAuthVectors(t)
	if got := BodySha256Hex([]byte(v.CheckInSignature.Body)); got != v.CheckInSignature.BodySha256Hex {
		t.Fatalf("BodySha256Hex = %q, want %q", got, v.CheckInSignature.BodySha256Hex)
	}
}

func TestBuildCheckInSignatureMessageMatchesVector(t *testing.T) {
	v := loadRouterAuthVectors(t)
	got := BuildCheckInSignatureMessage(
		v.CheckInSignature.Nonce,
		v.CheckInSignature.RouterID,
		v.CheckInSignature.BodySha256Hex,
	)
	if got != v.CheckInSignature.Message {
		t.Fatalf("BuildCheckInSignatureMessage =\n%q\nwant\n%q", got, v.CheckInSignature.Message)
	}
}

// vctl's produced signature MUST equal the vector the panel verifier accepts:
// this is the proof vctl's wire bytes verify under router-auth-integrity.ts
// verifyCheckInSignature, the same the legacy agent is held to.
func TestSignCheckInMatchesVector(t *testing.T) {
	v := loadRouterAuthVectors(t)
	sig, nonceHeader, err := SignCheckIn(
		v.CheckInSignature.DevicePrivateKeyBase64,
		v.CheckInSignature.Nonce,
		v.CheckInSignature.RouterID,
		[]byte(v.CheckInSignature.Body),
	)
	if err != nil {
		t.Fatalf("SignCheckIn: %v", err)
	}
	if sig != v.CheckInSignature.SignatureBase64 {
		t.Fatalf("signature = %q, want %q", sig, v.CheckInSignature.SignatureBase64)
	}
	if nonceHeader != v.CheckInSignature.Nonce {
		t.Fatalf("nonce header = %q, want %q", nonceHeader, v.CheckInSignature.Nonce)
	}
}

func TestSignCheckInRejectsBadKey(t *testing.T) {
	if _, _, err := SignCheckIn("not-base64!!", "vcn1.x", "r", []byte("{}")); err == nil {
		t.Fatal("expected an error for an undecodable private key")
	}
	if _, _, err := SignCheckIn("c2hvcnQ=", "vcn1.x", "r", []byte("{}")); err == nil {
		t.Fatal("expected an error for a wrong-length private key")
	}
}
