package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/subscription"
	"vectra-controller-pro/internal/vault"
)

// Production device vector (see internal/subscription/device_test.go).
const (
	e2eMAC   = "cc:d8:43:b1:bd:0c"
	e2eModel = "Xiaomi Mi Router AX3000T"
	e2eHWID  = "760386f8b139baf471f566a22efed5a4cd24a2241636d84524bd30bc28c08b4a"
)

func operatorExample(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "operator-config.json"))
	if err != nil {
		t.Fatalf("read example operator config: %v", err)
	}
	return raw
}

// providerEntry returns the fixture trimmed of surrounding whitespace: that is
// exactly what a json.RawMessage element of the provider array contains, and
// therefore what must land on disk byte-for-byte.
func providerEntry(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "internal", "coreengine", "xray", "testdata", "provider", "entry-00.json"))
	if err != nil {
		t.Fatalf("read provider fixture: %v", err)
	}
	return bytes.TrimSpace(raw)
}

// providerStub serves the JSON config array to JSON user agents, and to any
// agent on a path ending in /json (Remnawave's explicit JSON variant), and the
// degraded base64 link list to everything else, recording every request.
type providerStub struct {
	*httptest.Server
	mu      sync.Mutex
	Reqs    []http.Header
	Targets []string // each request's target: path and query
	entries []byte
}

func newProviderStub(t *testing.T, entry []byte) *providerStub {
	t.Helper()
	p := &providerStub{entries: append(append([]byte("["), entry...), ']')}
	p.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.Reqs = append(p.Reqs, r.Header.Clone())
		p.Targets = append(p.Targets, r.URL.RequestURI())
		p.mu.Unlock()
		ua := r.Header.Get("User-Agent")
		if strings.HasPrefix(ua, "Happ/") || strings.HasPrefix(ua, "v2rayNG/") || strings.HasSuffix(r.URL.Path, "/json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(p.entries)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("dmxlc3M6Ly91QGg6NDQzP3R5cGU9dGNwI2E=")) // base64 vless:// link
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *providerStub) headers() []http.Header {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]http.Header(nil), p.Reqs...)
}

// operatorConfigPointingAt rewrites the example config's subscription URL.
func operatorConfigPointingAt(t *testing.T, url string) []byte {
	t.Helper()
	c, err := config.Unmarshal(operatorExample(t))
	if err != nil {
		t.Fatalf("parse example operator config: %v", err)
	}
	c.Subscriptions[0].URL = url
	raw, err := config.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// fakeXray answers `version`, accepts `run -test` (exit 0) and stays up for
// `run -c`, like a real supervised daemon.
func writeFakeXray(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "fake-xray")
	stub := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  version) echo 'Xray 26.7.28 (fake)'; exit 0;;\n" +
		"  run)\n" +
		"    shift\n" +
		"    case \" $* \" in *\" stdin: \"*) cat >/dev/null;; *) exit 43;; esac\n" +
		"    for a in \"$@\"; do [ \"$a\" = '-test' ] && exit 0; done\n" +
		"    exec sleep 300;;\n" +
		"  *) exec sleep 300;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

type panelStub struct {
	*httptest.Server
	mu         sync.Mutex
	results    map[string][]controlplane.JobResultRequest
	registered bool
}

func newPanelStub(t *testing.T, desired []byte) *panelStub {
	t.Helper()
	p := &panelStub{results: map[string][]controlplane.JobResultRequest{}}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/router/register":
			p.mu.Lock()
			p.registered = true
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{
				RouterID: "r-e2e", IssuedToken: "tok-e2e", Status: "approved",
			})
		case "/api/router/check-in":
			rev := controlplane.DesiredRevisionSummary{
				ID: "rev-1", RevisionNumber: 1, Status: "approved",
				EngineMode: controlplane.EngineModeXrayDirect,
				Config:     json.RawMessage(desired),
			}
			revRaw, _ := json.Marshal(rev)
			_ = json.NewEncoder(w).Encode(controlplane.CheckInResponse{
				Status:          "ok",
				Jobs:            []controlplane.Job{{ID: "j1", Type: "apply_xray_config", State: "queued"}},
				DesiredRevision: revRaw,
			})
		case "/api/router/job-result":
			var req controlplane.JobResultRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			p.mu.Lock()
			p.results[req.JobID] = append(p.results[req.JobID], req)
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(controlplane.JobResultResponse{Acknowledged: true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *panelStub) resultsFor(id string) []controlplane.JobResultRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]controlplane.JobResultRequest(nil), p.results[id]...)
}

func newTestDaemon(t *testing.T, dir string, panel *panelStub, provider *providerStub) *daemon {
	t.Helper()
	return newTestDaemonWith(t, dir, panel, provider, nil)
}

// newTestDaemonWith is newTestDaemon with more of agent.json (UCI rendered by
// render-xray-config.sh), such as the owner's routeSource.
func newTestDaemonWith(t *testing.T, dir string, panel *panelStub, provider *providerStub, extra map[string]any) *daemon {
	t.Helper()
	agentJSON := map[string]any{
		"controlUrl":         panel.URL,
		"statePath":          filepath.Join(dir, "state.json"),
		"statusPath":         filepath.Join(dir, "status.json"),
		"xrayConfigPath":     filepath.Join(dir, "operator.json"),
		"providerConfigPath": filepath.Join(dir, "provider-config.json"),
		"xrayRenderPath":     filepath.Join(dir, "xray.json"),
		"xrayBinary":         writeFakeXray(t, dir),
		"geoAssetDir":        filepath.Join(dir, "assets"),
		"legacyStatePath":    filepath.Join(dir, "no-legacy.json"),
	}
	for k, v := range extra {
		agentJSON[k] = v
	}
	agentPath := filepath.Join(dir, "agent.json")
	raw, _ := json.Marshal(agentJSON)
	if err := os.WriteFile(agentPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := agentcfg.Load(agentPath)
	if err != nil {
		t.Fatalf("load agent cfg: %v", err)
	}
	d, err := newDaemon(cfg)
	if err != nil {
		t.Fatalf("new daemon: %v", err)
	}
	// Bound the firewall deadman so the test never leaves a long-lived process.
	d.confirmer = firewall.NewCommitConfirmer(filepath.Join(dir, "fw-confirm"), time.Second)
	// Keep connectivity probes hermetic (no real internet) and fast.
	d.rescuePolicy.HealthURLs = []string{panel.URL}
	d.rescuePolicy.TraceURLs = nil // the panel stands in for the internet: no trace
	// The dev host has no /sys/class/net/eth0/address or /tmp/sysinfo/model:
	// inject the production identity so the provider path is exercised.
	d.device = subscription.DeviceFacts{
		MAC: e2eMAC, Model: e2eModel, OSRelease: "24.10.6",
		HWID: subscription.ComputeHWID(e2eMAC, e2eModel),
	}
	d.subClient = provider.Client()
	return d
}

// TestAgentEndToEnd drives a full loop against a mock control plane AND a mock
// provider: register -> check-in (delivering the operator config + an
// apply_xray_config job) -> fetch the provider document -> splice -> xray -test
// -> write xray.json -> job-result success.
func TestAgentEndToEnd(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1) register
	if err := d.runOnce(ctx); err != nil {
		t.Fatalf("runOnce (register): %v", err)
	}
	panel.mu.Lock()
	reg := panel.registered
	panel.mu.Unlock()
	if !reg || d.st.RouterID != "r-e2e" || d.st.AgentToken != "tok-e2e" {
		t.Fatalf("register failed: registered=%v state=%+v", reg, d.st)
	}

	// 2) check-in + apply job
	if err := d.runOnce(ctx); err != nil {
		t.Fatalf("runOnce (check-in): %v", err)
	}

	// xray.json must exist and be the SPLICED provider document.
	rendered, err := readEncryptedTestFile(t, filepath.Join(dir, "xray.json"))
	if err != nil || !json.Valid(rendered) {
		t.Fatalf("xray.json not written/invalid: err=%v bytes=%d", err, len(rendered))
	}
	var doc struct {
		Inbounds []struct {
			Protocol string `json:"protocol"`
			Listen   string `json:"listen"`
		} `json:"inbounds"`
		Outbounds []json.RawMessage `json:"outbounds"`
		Routing   json.RawMessage   `json:"routing"`
	}
	if err := json.Unmarshal(rendered, &doc); err != nil {
		t.Fatalf("unmarshal xray.json: %v", err)
	}
	// The tproxy inbound, the loopback DNS inbound the router's resolver
	// asks through (DNS through the tunnel, dns_steer.go) and the loopback
	// exit probe (exitcheck.go) — nothing else.
	if len(doc.Inbounds) != 3 || doc.Inbounds[0].Protocol != "dokodemo-door" ||
		doc.Inbounds[1].Protocol != "dokodemo-door" || doc.Inbounds[1].Listen != "127.0.0.1" ||
		doc.Inbounds[2].Protocol != "http" || doc.Inbounds[2].Listen != "127.0.0.1" {
		t.Fatalf("expected the tproxy inbound, the loopback DNS inbound and the loopback exit probe, got %+v", doc.Inbounds)
	}
	if len(doc.Outbounds) == 0 || len(doc.Routing) == 0 {
		t.Fatal("provider outbounds/routing did not survive into xray.json")
	}
	// Whitespace-tolerant: the provider's own formatting is preserved verbatim.
	compactRendered := new(bytes.Buffer)
	if err := json.Compact(compactRendered, rendered); err != nil {
		t.Fatalf("compact xray.json: %v", err)
	}
	if !bytes.Contains(compactRendered.Bytes(), []byte(`"tcpSettings":{}`)) {
		t.Error("empty tcpSettings objects were corrupted on the way to xray.json")
	}
	if bytes.Contains(compactRendered.Bytes(), []byte(`"tcpSettings":[]`)) {
		t.Error("REGRESSION: tcpSettings became an empty array in xray.json")
	}

	// The provider document must be persisted verbatim, and NOT in the
	// operator-config file.
	persisted, err := readEncryptedTestFile(t, filepath.Join(dir, "provider-config.json"))
	if err != nil {
		t.Fatalf("provider config not persisted: %v", err)
	}
	if string(persisted) != string(providerEntry(t)) {
		t.Error("persisted provider document is not byte-identical to what the provider served")
	}
	opRaw, opErr := readEncryptedTestFile(t, filepath.Join(dir, "operator.json"))
	if opErr != nil {
		t.Fatal(opErr)
	}
	if _, err := config.Unmarshal(opRaw); err != nil {
		t.Errorf("operator config should still be a valid operator config: %v", err)
	}

	// A success job-result must have been submitted for j1.
	var success *controlplane.JobResultRequest
	got := panel.resultsFor("j1")
	for i := range got {
		if got[i].Status == "success" {
			success = &got[i]
		}
	}
	if success == nil {
		t.Fatalf("no success job-result for j1; got %d results: %+v", len(got), got)
	}
	if success.AppliedRevisionID != "rev-1" {
		t.Errorf("appliedRevisionId = %q, want rev-1", success.AppliedRevisionID)
	}
	if success.ConfigDigest == "" {
		t.Error("missing configDigest in success result")
	}
	if d.st.AppliedRevisionID != "rev-1" {
		t.Errorf("state appliedRevisionId = %q", d.st.AppliedRevisionID)
	}
}

// TestDaemonFetchSendsFullDeviceHeaders is the regression test for the
// fleet-wide 403: the DAEMON path used to call subscription.Fetch with only
// URL/UserAgent/ExtraHeaders, so x-hwid, x-ver-os and x-device-model were never
// sent and the provider refused the device.
func TestDaemonFetchSendsFullDeviceHeaders(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)

	cfg, err := config.Unmarshal(operatorConfigPointingAt(t, provider.URL))
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyDefaults(cfg)

	raw, meta, err := d.fetchProviderDocument(context.Background(), cfg)
	if err != nil {
		t.Fatalf("fetchProviderDocument: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("no provider entry returned")
	}
	if meta["bodyFormat"] != subscription.FormatJSON {
		t.Errorf("bodyFormat = %v, want json", meta["bodyFormat"])
	}

	reqs := provider.headers()
	if len(reqs) != 1 {
		t.Fatalf("expected exactly 1 provider request, got %d", len(reqs))
	}
	h := reqs[0]
	want := map[string]string{
		"User-Agent":     "v2rayNG/1.9.5",
		"x-device-os":    "OpenWrt",
		"x-ver-os":       "24.10.6",
		"x-device-model": e2eModel,
		"x-hwid":         e2eHWID,
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
}

// A device without a readable identity must fail loudly rather than send a
// request the provider will 403.
func TestDaemonFetchRefusesWithoutDeviceIdentity(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	d.device = subscription.DeviceFacts{}

	cfg, err := config.Unmarshal(operatorConfigPointingAt(t, provider.URL))
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyDefaults(cfg)

	if _, _, err := d.fetchProviderDocument(context.Background(), cfg); err == nil {
		t.Fatal("expected a refusal when the device identity is unknown")
	}
	if n := len(provider.headers()); n != 0 {
		t.Errorf("no request should have been sent, got %d", n)
	}
}

// Asking for the degraded link-list variant must be an explicit, loud failure —
// there is no builder to turn URIs into a runnable config anymore.
func TestDaemonFetchRefusesDegradedLinkList(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)

	cfg, err := config.Unmarshal(operatorConfigPointingAt(t, provider.URL))
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyDefaults(cfg)
	cfg.Subscriptions[0].UserAgent = "passwall2/26.7.16"

	_, _, err = d.fetchProviderDocument(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected a refusal for the degraded payload")
	}
	if !strings.Contains(err.Error(), "v2rayNG/1.9.5") {
		t.Errorf("error should point at a JSON user agent: %v", err)
	}
}

// Assert at-rest ciphertext before inspecting semantic contents. Fixtures use
// only fake credentials; this helper must never silently accept plaintext.
func readEncryptedTestFile(t *testing.T, path string) ([]byte, error) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if json.Valid(raw) {
		t.Fatalf("plaintext JSON artifact at %s", path)
	}
	return vault.ReadFile(path)
}
