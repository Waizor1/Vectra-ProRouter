package main

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/uatoken"
)

// The router's own subscription User-Agent, from the daemon's fetch to the
// token the subscription backend opens (internal/uatoken.Open is its
// reference).

// vectorServerKey is the contract vector's throwaway Vectra key
// (ui/contract/ua-vector.json): its private half is public, so a test can
// open what is sealed to it.
func vectorServerKey(t *testing.T) (int, *ecdh.PrivateKey) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "ui", "contract", "ua-vector.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Kid              int    `json:"kid"`
		ServerPrivateKey string `json:"serverPrivateKey"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	b, err := base64.StdEncoding.DecodeString(v.ServerPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.Kid, k
}

// ownAgentConfig is the example operator config with its subscription on url
// and no User-Agent: the router's own.
func ownAgentConfig(t *testing.T, url string) *config.Config {
	t.Helper()
	cfg, err := config.Unmarshal(operatorConfigPointingAt(t, url))
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyDefaults(cfg)
	cfg.Subscriptions[0].UserAgent = ""
	cfg.Subscriptions[0].Headers = nil
	return cfg
}

func TestDaemonFetchSendsItsOwnSignedAgent(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	kid, server := vectorServerKey(t)
	// The key the panel names in check-in wins over the built-in one.
	d.st.ClaimKey = &controlplane.ClaimKey{Kid: kid, PublicKey: base64.StdEncoding.EncodeToString(server.PublicKey().Bytes())}

	const target = "/api/sub/TestSubPath/json"
	raw, meta, err := d.fetchProviderDocument(context.Background(), ownAgentConfig(t, provider.URL+target))
	if err != nil {
		t.Fatalf("fetchProviderDocument: %v", err)
	}
	if len(raw) == 0 || meta["ownUserAgent"] != true {
		t.Fatalf("raw %d bytes, ownUserAgent %v", len(raw), meta["ownUserAgent"])
	}
	reqs := provider.headers()
	if len(reqs) != 1 {
		t.Fatalf("%d provider requests, want 1", len(reqs))
	}
	ua := reqs[0].Get("User-Agent")
	if want := uatoken.Product + "/" + controllerVersion() + " " + uatoken.Scheme; !strings.HasPrefix(ua, want) {
		t.Fatalf("User-Agent %q, want the prefix %q", ua, want)
	}
	if len(ua) > uatoken.MaxLen {
		t.Fatalf("User-Agent of %d bytes, over %d", len(ua), uatoken.MaxLen)
	}
	if got := provider.Targets[0]; got != target {
		t.Fatalf("request target %q, want %q", got, target)
	}
	dev, err := claim.DeviceKey(d.st.DevicePrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keys := func(k byte) (*ecdh.PrivateKey, bool) { return server, int(k) == kid }
	devices := func(id string) (ed25519.PublicKey, bool) {
		return dev.Public().(ed25519.PublicKey), id == d.st.DeviceIdentifier
	}
	opened, err := uatoken.Open(ua, keys, devices, reqs[0].Get("x-hwid"), target, time.Now(), time.Minute)
	if err != nil {
		t.Fatalf("the backend could not open the agent: %v", err)
	}
	if opened.DeviceID != d.st.DeviceIdentifier {
		t.Fatalf("device %q, want %q", opened.DeviceID, d.st.DeviceIdentifier)
	}
	// Bound to this subscription and this HWID: anywhere else it is refused.
	if _, err := uatoken.Open(ua, keys, devices, reqs[0].Get("x-hwid"), "/api/sub/another/json", time.Now(), time.Minute); err == nil {
		t.Fatal("the agent opened for another subscription")
	}
}

// Without a key from the panel the agent is sealed to the built-in one.
func TestOwnAgentSealsToTheBuiltinKey(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	d.st.ClaimKey = nil

	if _, _, err := d.fetchProviderDocument(context.Background(), ownAgentConfig(t, provider.URL+"/api/sub/x/json")); err != nil {
		t.Fatalf("fetchProviderDocument: %v", err)
	}
	ua := provider.headers()[0].Get("User-Agent")
	tok, err := base64.RawURLEncoding.DecodeString(ua[strings.LastIndex(ua, " "+uatoken.Scheme)+1+len(uatoken.Scheme):])
	if err != nil {
		t.Fatal(err)
	}
	builtin, _ := base64.StdEncoding.DecodeString(uatoken.BuiltinKey)
	if tok[0] != uatoken.BuiltinKid {
		t.Fatalf("sealed to key %d, want the built-in %d", tok[0], uatoken.BuiltinKid)
	}
	// The ephemeral key follows the kid; the salt binds the built-in public
	// key, so a token for it cannot open under any other (the vector's here).
	kid, server := vectorServerKey(t)
	keys := func(k byte) (*ecdh.PrivateKey, bool) { return server, int(k) == kid }
	devices := func(string) (ed25519.PublicKey, bool) { return nil, false }
	if _, err := uatoken.Open(ua, keys, devices, e2eHWID, "/api/sub/x/json", time.Now(), time.Minute); err == nil {
		t.Fatal("a token for the built-in key opened under another key")
	}
	if len(builtin) != 32 {
		t.Fatalf("built-in key of %d bytes", len(builtin))
	}
}

// No device key: nothing is sent, not even an unsigned request.
func TestOwnAgentNeedsTheDeviceKey(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	d.st.DevicePrivateKey = ""

	_, _, err := d.fetchProviderDocument(context.Background(), ownAgentConfig(t, provider.URL+"/api/sub/x/json"))
	if err == nil || !strings.Contains(err.Error(), "device key") {
		t.Fatalf("err = %v, want a refusal naming the device key", err)
	}
	if n := len(provider.headers()); n != 0 {
		t.Fatalf("%d requests sent without the router's own agent", n)
	}
}

// An agent the config names — directly or through its headers — is sent as
// it is: the operator's choice.
func TestConfiguredAgentIsSentAsItIs(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	cfg := ownAgentConfig(t, provider.URL)
	cfg.Subscriptions[0].Headers = map[string]string{"user-agent": "v2rayNG/1.9.5"}

	_, meta, err := d.fetchProviderDocument(context.Background(), cfg)
	if err != nil {
		t.Fatalf("fetchProviderDocument: %v", err)
	}
	if got := provider.headers()[0].Get("User-Agent"); got != "v2rayNG/1.9.5" {
		t.Fatalf("User-Agent %q, want the configured one", got)
	}
	if meta["ownUserAgent"] != false {
		t.Fatalf("ownUserAgent %v, want false", meta["ownUserAgent"])
	}
}

// The link list answered to the router's own agent says how to get JSON.
func TestOwnAgentLinkListNamesTheJSONPath(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)

	_, _, err := d.fetchProviderDocument(context.Background(), ownAgentConfig(t, provider.URL+"/api/sub/x"))
	if err == nil || !strings.Contains(err.Error(), "/json") {
		t.Fatalf("err = %v, want advice naming the /json variant", err)
	}
}
