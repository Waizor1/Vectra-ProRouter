package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheckInUsesVectraHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-vectra-router-id"); got != "router-123" {
			t.Fatalf("expected router id header, got %q", got)
		}
		if got := r.Header.Get("x-vectra-router-token"); got != "token-abc" {
			t.Fatalf("expected router token header, got %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(CheckInResponse{
			ProtocolVersion:        ProtocolVersion,
			RouterID:               "router-123",
			Status:                 "active",
			PollingIntervalSeconds: 45,
			Jobs:                   []Job{},
			OperatorMessage:        "",
		})
	}))
	defer server.Close()

	client := NewClient(Options{
		BaseURL:    server.URL,
		RouterID:   "router-123",
		AgentToken: "token-abc",
	})

	_, err := client.CheckIn(context.Background(), CheckInRequest{
		RouterID: "router-123",
		Inventory: RouterInventory{
			ProtocolVersion:  ProtocolVersion,
			DeviceIdentifier: "vectra-test",
			DevicePublicKey:  "pub",
			Model:            "AX3000T",
			BoardName:        "xiaomi,mi-router-ax3000t",
			Target:           "mediatek/filogic",
			Architecture:     "aarch64_cortex-a53",
			OpenWrtRelease:   "24.10.4",
		},
		Health: RouterHealth{CurrentMode: "proxy"},
	})
	if err != nil {
		t.Fatalf("check-in failed: %v", err)
	}
}

func TestCheckInIncludesResponseBodyForNon2xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"router import validation failed"}`))
	}))
	defer server.Close()

	client := NewClient(Options{
		BaseURL:    server.URL,
		RouterID:   "router-123",
		AgentToken: "token-abc",
	})

	_, err := client.CheckIn(context.Background(), CheckInRequest{
		RouterID: "router-123",
		Inventory: RouterInventory{
			ProtocolVersion:  ProtocolVersion,
			DeviceIdentifier: "vectra-test",
			DevicePublicKey:  "pub",
			Model:            "AX3000T",
			BoardName:        "xiaomi,mi-router-ax3000t",
			Target:           "mediatek/filogic",
			Architecture:     "aarch64_cortex-a53",
			OpenWrtRelease:   "24.10.4",
		},
		Health: RouterHealth{CurrentMode: "proxy"},
	})
	if err == nil {
		t.Fatal("expected check-in to fail on 400 response")
	}

	message := err.Error()
	if !strings.Contains(message, "unexpected status 400") {
		t.Fatalf("expected status code in error, got %q", message)
	}
	if !strings.Contains(message, "router import validation failed") {
		t.Fatalf("expected response body in error, got %q", message)
	}
}
