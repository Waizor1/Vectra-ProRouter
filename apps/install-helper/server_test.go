package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeGatewayResolver struct {
	ip string
}

func (f fakeGatewayResolver) DefaultGatewayIP() (string, error) {
	return f.ip, nil
}

type fakeFingerprinter struct{}

func (fakeFingerprinter) ProbeFingerprint(targetIP string) (string, bool, error) {
	return "SHA256:test", targetIP != "", nil
}

type fakeInstaller struct {
	invocations int
}

func (f *fakeInstaller) RunInstall(session *installSession, _ string, _ installRequest) {
	f.invocations++
	session.emitStage("completed", "success", "ok", "", nil)
}

func newTestServer(t *testing.T) *helperServer {
	t.Helper()

	state, err := newHelperStateStore(t.TempDir(), newMemorySecretStore())
	if err != nil {
		t.Fatalf("newHelperStateStore: %v", err)
	}

	return &helperServer{
		allowedOrigins: map[string]struct{}{
			"https://router.vectra-pro.net": {},
		},
		state:         state,
		sessions:      newInstallSessionStore(),
		sessionTokens: newSessionTokenStore(),
		resolver:      fakeGatewayResolver{ip: "192.168.99.1"},
		fingerprinter: fakeFingerprinter{},
		installer:     &fakeInstaller{},
	}
}

func TestHandleHealthIssuesSessionToken(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Origin", "https://router.vectra-pro.net")
	response := httptest.NewRecorder()

	server.handleHealth(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.Code)
	}

	var payload healthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if payload.SessionToken == "" {
		t.Fatal("expected session token")
	}
}

func TestScanRequiresIssuedToken(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/scan", bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Origin", "https://router.vectra-pro.net")
	response := httptest.NewRecorder()

	server.handleScan(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.Code)
	}
}

func TestInstallRejectsDisallowedOrigin(t *testing.T) {
	server := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.Header.Set("Origin", "https://router.vectra-pro.net")
	response := httptest.NewRecorder()
	server.handleHealth(response, request)

	var payload healthResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode health response: %v", err)
	}

	body := bytes.NewReader([]byte(`{"targetIp":"192.168.99.1"}`))
	installRequest := httptest.NewRequest(http.MethodPost, "/install", body)
	installRequest.Header.Set("Origin", "https://evil.example")
	installRequest.Header.Set("X-Vectra-Install-Session", payload.SessionToken)
	installResponse := httptest.NewRecorder()

	server.handleInstall(installResponse, installRequest)

	if installResponse.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", installResponse.Code)
	}
}
