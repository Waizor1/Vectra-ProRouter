// SPDX-License-Identifier: LicenseRef-Vectra-Proprietary
// Copyright (c) 2026 Vectra. All rights reserved.

package controlplane

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// With no nonce and no signing key (the default: the panel is not issuing
// nonces), a check-in must carry NO signature headers and a body byte-identical
// to what a pre-A2 controller sent.
func TestCheckInUnsignedByDefault(t *testing.T) {
	var gotBody []byte
	var gotNonce, gotSig string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotNonce = r.Header.Get(CheckInNonceHeader)
		gotSig = r.Header.Get(CheckInSignatureHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CheckInResponse{RouterID: "router-123", Status: "active"})
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client(), RouterID: "router-123", AgentToken: "token-abc"})
	req := CheckInRequest{
		ProtocolVersion: ProtocolVersion,
		RouterID:        "router-123",
		Inventory:       RouterInventory{ProtocolVersion: ProtocolVersion, DeviceIdentifier: "vectra-test"},
		Health:          RouterHealth{CurrentMode: "proxy"},
	}
	if _, err := client.CheckIn(context.Background(), req); err != nil {
		t.Fatalf("check-in failed: %v", err)
	}

	if gotNonce != "" || gotSig != "" {
		t.Fatalf("default check-in must send no signature headers; got nonce=%q sig=%q", gotNonce, gotSig)
	}
	want, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotBody) != string(want) {
		t.Fatalf("default check-in body changed.\n got: %s\nwant: %s", gotBody, want)
	}
}

// checkInSigner is inert unless BOTH a nonce and a private key are held.
func TestCheckInSignerInertWhenEitherMissing(t *testing.T) {
	c := NewClient(Options{BaseURL: "https://example.invalid"})
	if c.checkInSigner(CheckInRequest{SigningPrivateKey: "k"}, "") != nil {
		t.Fatal("signer must be nil with no nonce")
	}
	if c.checkInSigner(CheckInRequest{}, "vcn1.x") != nil {
		t.Fatal("signer must be nil with no private key")
	}
	if c.checkInSigner(CheckInRequest{SigningPrivateKey: "k"}, "vcn1.x") == nil {
		t.Fatal("signer must be present when both nonce and key are held")
	}
}

// When the controller holds a nonce and its device key, the check-in carries
// the signature headers; the nonce header echoes the held nonce verbatim.
func TestCheckInSignsWhenNonceHeld(t *testing.T) {
	v := loadRouterAuthVectors(t)
	var gotNonce, gotSig string
	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotNonce = r.Header.Get(CheckInNonceHeader)
		gotSig = r.Header.Get(CheckInSignatureHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CheckInResponse{RouterID: v.CheckInSignature.RouterID, Status: "active"})
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client(), RouterID: v.CheckInSignature.RouterID, AgentToken: "token-abc"})
	if _, err := client.CheckIn(context.Background(), CheckInRequest{
		RouterID:          v.CheckInSignature.RouterID,
		Inventory:         RouterInventory{ProtocolVersion: ProtocolVersion},
		SigningNonce:      v.CheckInSignature.Nonce,
		SigningPrivateKey: v.CheckInSignature.DevicePrivateKeyBase64,
	}); err != nil {
		t.Fatalf("check-in failed: %v", err)
	}

	if gotNonce != v.CheckInSignature.Nonce {
		t.Fatalf("nonce header = %q, want %q", gotNonce, v.CheckInSignature.Nonce)
	}
	wantSig, _, err := SignCheckIn(v.CheckInSignature.DevicePrivateKeyBase64, v.CheckInSignature.Nonce, v.CheckInSignature.RouterID, gotBody)
	if err != nil {
		t.Fatal(err)
	}
	if gotSig != wantSig {
		t.Fatalf("signature header = %q, want %q", gotSig, wantSig)
	}
}

// A 401 carrying code=checkin_signature_required and a fresh nonce makes the
// client re-sign with that nonce and retry exactly once.
func TestCheckInResignsOnceOn401(t *testing.T) {
	v := loadRouterAuthVectors(t)
	const freshNonce = "vcn1.1791460801.AAECAwQFBgcICQoLDA0ODw.ZZZ"
	var attempts int
	var secondNonce, secondSig string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Check-in signature required.","code":"checkin_signature_required","checkInNonce":"` + freshNonce + `"}`))
			return
		}
		secondNonce = r.Header.Get(CheckInNonceHeader)
		secondSig = r.Header.Get(CheckInSignatureHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CheckInResponse{RouterID: v.CheckInSignature.RouterID, Status: "active"})
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client(), RouterID: v.CheckInSignature.RouterID, AgentToken: "token-abc"})
	// Start with no nonce held: the first attempt is unsigned, the panel asks,
	// and the client re-signs with the nonce the 401 carried.
	if _, err := client.CheckIn(context.Background(), CheckInRequest{
		RouterID:          v.CheckInSignature.RouterID,
		Inventory:         RouterInventory{ProtocolVersion: ProtocolVersion},
		SigningPrivateKey: v.CheckInSignature.DevicePrivateKeyBase64,
	}); err != nil {
		t.Fatalf("check-in should succeed after one re-sign: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", attempts)
	}
	if secondNonce != freshNonce {
		t.Fatalf("retry nonce = %q, want the 401's %q", secondNonce, freshNonce)
	}
	if secondSig == "" {
		t.Fatal("retry must be signed")
	}
}

// A 401 without the signature-required code (e.g. a bad bearer token) must not
// trigger a re-sign.
func TestCheckInDoesNotResignOnPlain401(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client(), RouterID: "router-123", AgentToken: "bad"})
	if _, err := client.CheckIn(context.Background(), CheckInRequest{
		RouterID:          "router-123",
		SigningPrivateKey: "irrelevant",
	}); err == nil {
		t.Fatal("expected the plain 401 to surface as an error")
	}
	if attempts != 1 {
		t.Fatalf("expected no retry on a plain 401, got %d attempts", attempts)
	}
}

func TestRegisterRequestEnrollmentTokenOmitEmpty(t *testing.T) {
	withToken, err := json.Marshal(RegisterRequest{EnrollmentToken: "vpt1.123.abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withToken), `"enrollmentToken":"vpt1.123.abc"`) {
		t.Fatalf("expected enrollmentToken in %s", withToken)
	}
	without, err := json.Marshal(RegisterRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "enrollmentToken") {
		t.Fatalf("expected enrollmentToken omitted in %s", without)
	}
}

func TestCheckInSigningFieldsNeverSerialized(t *testing.T) {
	raw, err := json.Marshal(CheckInRequest{
		RouterID:          "router-123",
		SigningNonce:      "vcn1.secret",
		SigningPrivateKey: "private",
		SigningRouterID:   "router-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"SigningNonce", "signingNonce", "SigningPrivateKey", "private", "vcn1.secret"} {
		if strings.Contains(string(raw), needle) {
			t.Fatalf("signing material leaked into request body (%q): %s", needle, raw)
		}
	}
}

// DeviceBinding is omitted whenever it cannot be computed, so a box that cannot
// derive it reports no field at all.
func TestRouterInventoryDeviceBindingOmitEmpty(t *testing.T) {
	without, err := json.Marshal(RouterInventory{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "deviceBinding") {
		t.Fatalf("expected deviceBinding omitted in %s", without)
	}
	with, err := json.Marshal(RouterInventory{DeviceBinding: "v1:abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(with), `"deviceBinding":"v1:abc"`) {
		t.Fatalf("expected deviceBinding present in %s", with)
	}
}
