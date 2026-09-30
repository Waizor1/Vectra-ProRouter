package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterSendsHeadersAndProtocol(t *testing.T) {
	var gotPath, gotID, gotToken, gotProto string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotID = r.Header.Get("x-vectra-router-id")
		gotToken = r.Header.Get("x-vectra-router-token")
		var req RegisterRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotProto = req.ProtocolVersion
		_ = json.NewEncoder(w).Encode(RegisterResponse{RouterID: "r-123", IssuedToken: "tok"})
	}))
	defer srv.Close()

	c := NewClient(Options{BaseURL: srv.URL})
	resp, err := c.Register(context.Background(), RegisterRequest{
		Inventory: RouterInventory{EngineMode: EngineModeXrayDirect},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if gotPath != "/api/router/register" {
		t.Errorf("path = %q", gotPath)
	}
	if gotProto != ProtocolVersion {
		t.Errorf("protocol default not applied: %q", gotProto)
	}
	if resp.RouterID != "r-123" || resp.IssuedToken != "tok" {
		t.Errorf("decode response: %+v", resp)
	}
	// Register is unauthenticated until credentials are set.
	if gotID != "" || gotToken != "" {
		t.Errorf("unexpected auth headers on register: id=%q token=%q", gotID, gotToken)
	}
}

func TestCheckInAuthHeadersAfterSetCredentials(t *testing.T) {
	var gotID, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = r.Header.Get("x-vectra-router-id")
		gotToken = r.Header.Get("x-vectra-router-token")
		_ = json.NewEncoder(w).Encode(CheckInResponse{Status: "ok", Jobs: []Job{{ID: "j1", Type: "apply_xray_config"}}})
	}))
	defer srv.Close()

	c := NewClient(Options{BaseURL: srv.URL})
	c.SetCredentials("r-9", "secret")
	resp, err := c.CheckIn(context.Background(), CheckInRequest{RouterID: "r-9"})
	if err != nil {
		t.Fatalf("CheckIn: %v", err)
	}
	if gotID != "r-9" || gotToken != "secret" {
		t.Errorf("auth headers id=%q token=%q", gotID, gotToken)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].Type != "apply_xray_config" {
		t.Errorf("jobs decode: %+v", resp.Jobs)
	}
}

func TestNonOKStatusSurfacesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("router not approved"))
	}))
	defer srv.Close()

	c := NewClient(Options{BaseURL: srv.URL})
	_, err := c.SubmitJobResult(context.Background(), JobResultRequest{JobID: "j1", Status: "success"})
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if got := err.Error(); !contains(got, "403") || !contains(got, "router not approved") {
		t.Errorf("error missing status/body: %q", got)
	}
}

// A redirect is no answer: following it would hand the router's token to
// wherever it points — another host, or plain http.
func TestTheClientFollowsNoRedirect(t *testing.T) {
	var reached []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.Header.Get("x-vectra-router-token"))
		_ = json.NewEncoder(w).Encode(CheckInResponse{Status: "ok"})
	}))
	defer other.Close()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer panel.Close()

	c := NewClient(Options{BaseURL: panel.URL})
	c.SetCredentials("r-9", "secret")
	_, err := c.CheckIn(context.Background(), CheckInRequest{RouterID: "r-9"})
	if err == nil || !contains(err.Error(), "307") {
		t.Fatalf("a redirected check-in = %v, want the 307 as an error", err)
	}
	if len(reached) > 0 {
		t.Fatalf("the redirect was followed, with the token %q", reached)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
