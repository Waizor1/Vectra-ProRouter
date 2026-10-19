// SPDX-License-Identifier: LicenseRef-Vectra-Proprietary
// Copyright (c) 2026 Vectra. All rights reserved.

package controlplane

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// A2 check-in signature. These constants and the message layout MUST stay
// byte-for-byte identical to the panel verifier in
// apps/web/src/server/vectra/router-auth-integrity.ts; the shared golden
// vectors in packages/contracts/fixtures/router-auth-vectors.json pin them.
const (
	// CheckInSignatureMessagePrefix already ends in a newline, so the nonce
	// follows it directly with no separator of its own.
	CheckInSignatureMessagePrefix = "vectra-router-checkin:v1\n"
	CheckInNonceHeader            = "x-vectra-checkin-nonce"
	CheckInSignatureHeader        = "x-vectra-checkin-signature"
)

// BodySha256Hex is the lower-case hex sha256 of the exact request body bytes,
// which is what the check-in signature covers. The caller MUST hash the same
// bytes it sends on the wire.
func BodySha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// BuildCheckInSignatureMessage composes the canonical message the device key
// signs. Mirrors buildCheckInSignatureMessage on the panel: the prefix ends in
// a newline, then nonce, routerId and the body digest, each on its own line.
func BuildCheckInSignatureMessage(nonce, routerID, bodySha256Hex string) string {
	return CheckInSignatureMessagePrefix + nonce + "\n" + routerID + "\n" + bodySha256Hex
}

// SignCheckIn signs the canonical check-in message with the base64-std ed25519
// device private key (the same encoding the device identity is stored with). It
// returns the values for the x-vectra-checkin-signature and
// x-vectra-checkin-nonce headers: the standard (padded) base64 of the
// signature, and the nonce verbatim.
//
// The padded standard-base64 encoding is mandatory: the panel's
// ED25519_SIGNATURE_B64_PATTERN is /^[A-Za-z0-9+/]{86}==$/ and
// decodeCanonicalEd25519Signature re-encodes and REJECTS any base64url,
// unpadded or whitespace variant.
func SignCheckIn(privateKeyBase64, nonce, routerID string, body []byte) (signatureHeader, nonceHeader string, err error) {
	rawKey, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return "", "", fmt.Errorf("decode device private key: %w", err)
	}
	if len(rawKey) != ed25519.PrivateKeySize {
		return "", "", fmt.Errorf("device private key has unexpected length %d (want %d)", len(rawKey), ed25519.PrivateKeySize)
	}

	message := BuildCheckInSignatureMessage(nonce, routerID, BodySha256Hex(body))
	signature := ed25519.Sign(ed25519.PrivateKey(rawKey), []byte(message))
	return base64.StdEncoding.EncodeToString(signature), nonce, nil
}
