package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/state"
)

func TestRegisterWithRecoverySignsAndRetriesOn403(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	var calls int
	var gotProof *controlplane.RecoveryProof
	var gotDeviceID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req controlplane.RegisterRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.RecoveryProof == nil {
			// First attempt: reject like the panel anti-hijack guard does.
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"Existing router registration requires the current router token."}`))
			return
		}
		gotProof = req.RecoveryProof
		gotDeviceID = req.Inventory.DeviceIdentifier
		_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{
			RouterID:    "router-xyz",
			IssuedToken: "fresh-token",
		})
	}))
	defer srv.Close()

	client := controlplane.NewClient(controlplane.Options{BaseURL: srv.URL})
	persisted := &state.PersistedState{
		DeviceIdentifier: "vectra-dev",
		DevicePublicKey:  base64.StdEncoding.EncodeToString(pub),
		DevicePrivateKey: base64.StdEncoding.EncodeToString(priv),
	}

	resp, err := registerWithRecovery(context.Background(), client, persisted, controlplane.RegisterRequest{
		Inventory: controlplane.RouterInventory{DeviceIdentifier: "vectra-dev"},
	})
	if err != nil {
		t.Fatalf("registerWithRecovery: %v", err)
	}
	if resp.IssuedToken != "fresh-token" {
		t.Fatalf("issued token = %q, want fresh-token", resp.IssuedToken)
	}
	if calls != 2 {
		t.Fatalf("expected 2 register calls (403 then signed retry), got %d", calls)
	}
	if gotProof == nil {
		t.Fatal("the retry did not carry a recovery proof")
	}
	sig, err := base64.StdEncoding.DecodeString(gotProof.Signature)
	if err != nil {
		t.Fatalf("decode proof signature: %v", err)
	}
	message := []byte("vectra-router-reauth:v1\n" + gotDeviceID + "\n" + gotProof.SignedAt)
	if !ed25519.Verify(pub, message, sig) {
		t.Fatal("recovery proof signature does not verify against the device public key")
	}
}

func TestRegisterWithRecoveryDoesNotRetryWithoutPrivateKey(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()

	client := controlplane.NewClient(controlplane.Options{BaseURL: srv.URL})
	persisted := &state.PersistedState{DeviceIdentifier: "vectra-dev"} // no private key

	if _, err := registerWithRecovery(context.Background(), client, persisted, controlplane.RegisterRequest{
		Inventory: controlplane.RouterInventory{DeviceIdentifier: "vectra-dev"},
	}); err == nil {
		t.Fatal("expected error on 403 with no private key to sign with")
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt with no signing key, got %d", calls)
	}
}

func TestRegisterWithRecoveryDoesNotRetryOnNon403(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"validation failed"}`))
	}))
	defer srv.Close()

	client := controlplane.NewClient(controlplane.Options{BaseURL: srv.URL})
	persisted := &state.PersistedState{
		DeviceIdentifier: "vectra-dev",
		DevicePublicKey:  base64.StdEncoding.EncodeToString(pub),
		DevicePrivateKey: base64.StdEncoding.EncodeToString(priv),
	}

	if _, err := registerWithRecovery(context.Background(), client, persisted, controlplane.RegisterRequest{
		Inventory: controlplane.RouterInventory{DeviceIdentifier: "vectra-dev"},
	}); err == nil {
		t.Fatal("expected error to propagate on a non-403 failure")
	}
	if calls != 1 {
		t.Fatalf("expected no signed retry on a non-403 error, got %d calls", calls)
	}
}
