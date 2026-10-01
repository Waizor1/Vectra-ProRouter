package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
	"vectra-controller-pro/internal/vault"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/logging"
)

// The legacy vectra-controller-agent identity that router 1111111111 already
// holds. vctl must adopt it verbatim rather than minting a new one.
const (
	legacyRouterID   = "bdfdb919-5e06-4344-ad8b-67a16f3b6fcf"
	legacyToken      = "legacy-agent-token"
	legacyDeviceID   = "vectra-07bf0887f662"
	legacyDevicePub  = "bGVnYWN5LWRldmljZS1wdWJsaWMta2V5"
	legacyDevicePriv = "bGVnYWN5LWRldmljZS1wcml2YXRlLWtleQ=="
)

func writeLegacyState(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "legacy-agent-state.json")
	raw := fmt.Sprintf(`{"router_id":%q,"agent_token":%q,"device_identifier":%q,`+
		`"device_public_key":%q,"device_private_key":%q}`,
		legacyRouterID, legacyToken, legacyDeviceID, legacyDevicePub, legacyDevicePriv)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// newIdentityDaemon builds a daemon over a FRESH state directory that has a
// legacy agent state file next to it — the exact canary-migration situation.
func newIdentityDaemon(t *testing.T, dir, controlURL, legacyStatePath string) *daemon {
	t.Helper()
	agentJSON := map[string]any{
		"controlUrl":         controlURL,
		"statePath":          filepath.Join(dir, "state.json"),
		"statusPath":         filepath.Join(dir, "status.json"),
		"xrayConfigPath":     filepath.Join(dir, "operator.json"),
		"providerConfigPath": filepath.Join(dir, "provider-config.json"),
		"xrayRenderPath":     filepath.Join(dir, "xray.json"),
		"xrayBinary":         writeFakeXray(t, dir),
		"geoAssetDir":        filepath.Join(dir, "assets"),
		"legacyStatePath":    legacyStatePath,
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
	d.rescuePolicy.HealthURLs = []string{controlURL}
	return d
}

// TestFreshInstallAdoptsLegacyDeviceIdentity is the regression test for the
// dead adopt branch: newDaemon used to call state.EnsureIdentity BEFORE
// state.ImportLegacyIdentity. EnsureIdentity mints a random identifier into
// any empty field, and every device-identity adopt branch in
// ImportLegacyIdentity is guarded by `== ""`. So router_id and agent_token
// (assigned unconditionally) were adopted while device_identifier /
// device_public_key silently were not — the router reported a device identity
// the panel had never seen.
//
// The pre-existing TestImportLegacyIdentity in internal/state passes either
// way: it calls ImportLegacyIdentity on a zero-value state directly and never
// exercises newDaemon's real call order. This test does.
func TestFreshInstallAdoptsLegacyDeviceIdentity(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyState(t, dir)
	d := newIdentityDaemon(t, dir, "http://127.0.0.1:1", legacy)

	if d.st.RouterID != legacyRouterID {
		t.Errorf("routerId = %q, want %q", d.st.RouterID, legacyRouterID)
	}
	if d.st.AgentToken != legacyToken {
		t.Errorf("agentToken = %q, want %q", d.st.AgentToken, legacyToken)
	}
	if d.st.DeviceIdentifier != legacyDeviceID {
		t.Errorf("REGRESSION: deviceIdentifier = %q, want the adopted %q "+
			"(a freshly minted vectra-* value means EnsureIdentity ran first)",
			d.st.DeviceIdentifier, legacyDeviceID)
	}
	if d.st.DevicePublicKey != legacyDevicePub {
		t.Errorf("REGRESSION: devicePublicKey = %q, want the adopted legacy key", d.st.DevicePublicKey)
	}
	if d.st.DevicePrivateKey != legacyDevicePriv {
		t.Errorf("REGRESSION: devicePrivateKey was not adopted; the keypair would no longer match the panel's record")
	}

	// The adopted identity must also survive to disk, or the next boot re-mints.
	raw, err := vault.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["device_identifier"] != legacyDeviceID {
		t.Errorf("persisted device_identifier = %v, want %q", persisted["device_identifier"], legacyDeviceID)
	}
}

// TestEnsureIdentityStillMintsWithoutLegacyState guards the other direction:
// reordering must not break plain enrollment on a router with no legacy agent.
func TestEnsureIdentityStillMintsWithoutLegacyState(t *testing.T) {
	dir := t.TempDir()
	d := newIdentityDaemon(t, dir, "http://127.0.0.1:1", filepath.Join(dir, "absent.json"))

	if d.st.DeviceIdentifier == "" || d.st.DevicePublicKey == "" || d.st.DevicePrivateKey == "" {
		t.Fatalf("fresh enrollment must mint an identity, got %+v", d.st)
	}
	if d.st.RouterID != "" || d.st.AgentToken != "" {
		t.Errorf("no legacy state: routerId/token must stay empty so register runs, got %q/%q",
			d.st.RouterID, d.st.AgentToken)
	}
}

// capturingPanel records the raw register/check-in request bodies so a test can
// assert on the BYTES vctl puts on the wire, not on an in-memory struct.
type capturingPanel struct {
	*httptest.Server
	mu     sync.Mutex
	bodies map[string][]byte
}

func newCapturingPanel(t *testing.T) *capturingPanel {
	t.Helper()
	p := &capturingPanel{bodies: map[string][]byte{}}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 0)
		if r.Body != nil {
			buf := make([]byte, 1<<20)
			n, _ := r.Body.Read(buf)
			for n > 0 {
				body = append(body, buf[:n]...)
				n, _ = r.Body.Read(buf)
			}
		}
		p.mu.Lock()
		p.bodies[r.URL.Path] = body
		p.mu.Unlock()

		switch r.URL.Path {
		case "/api/router/register":
			_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{
				RouterID: legacyRouterID, IssuedToken: legacyToken, Status: "approved",
			})
		case "/api/router/check-in":
			_ = json.NewEncoder(w).Encode(controlplane.CheckInResponse{Status: "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *capturingPanel) body(path string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.bodies[path]...)
}

// TestCheckInPayloadCarriesPanelRequiredStrings is the regression test for the
// live 400: vctl's check-in body shipped deviceIdentifier:"" and
// devicePublicKey:"" because nothing ever copied them out of the persisted
// state. The panel's routerInventorySchema declares both z.string().min(1),
// producing exactly the two "String must contain at least 1 character(s)"
// entries observed under fieldErrors.inventory.
//
// It asserts on the decoded WIRE body: neither Go tag changes nor a
// reintroduced gap in the assembly path can slip past it.
func TestCheckInPayloadCarriesPanelRequiredStrings(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyState(t, dir)
	panel := newCapturingPanel(t)
	d := newIdentityDaemon(t, dir, panel.URL, legacy)

	// Legacy adoption means the daemon already has routerId+token, so this
	// first iteration checks in rather than registering — exactly what the
	// live canary does.
	if err := d.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce (check-in): %v", err)
	}

	raw := panel.body("/api/router/check-in")
	if len(raw) == 0 {
		t.Fatal("no check-in request was captured")
	}
	var req struct {
		Inventory controlplane.RouterInventory `json:"inventory"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decode check-in body: %v", err)
	}

	if req.Inventory.DeviceIdentifier != legacyDeviceID {
		t.Errorf("REGRESSION: wire deviceIdentifier = %q, want %q "+
			"(empty => panel 400 'String must contain at least 1 character(s)')",
			req.Inventory.DeviceIdentifier, legacyDeviceID)
	}
	if req.Inventory.DevicePublicKey != legacyDevicePub {
		t.Errorf("REGRESSION: wire devicePublicKey = %q, want the adopted legacy key "+
			"(empty => panel 400 'String must contain at least 1 character(s)')",
			req.Inventory.DevicePublicKey)
	}

	// Belt and braces: no panel-required min(1) string may be empty on the wire.
	// On a dev host `ubus` is absent, so board facts are legitimately blank —
	// only assert the fields that do not depend on router hardware.
	hardwareIndependent := map[string]bool{
		"deviceIdentifier": true, "devicePublicKey": true, "controllerVersion": true,
	}
	for _, field := range req.Inventory.MissingRequiredFields() {
		if hardwareIndependent[field] {
			t.Errorf("inventory.%s is empty on the wire; the panel rejects this payload", field)
		}
	}
}

// Every register proves the router holds its device key: an ed25519 signature
// over "vectra-register/v1\n<deviceIdentifier>\n<timestamp>" that verifies
// with the devicePublicKey the same request carries. The signature is never
// logged.
func TestRegisterCarriesAProofOfTheDeviceKey(t *testing.T) {
	dir := t.TempDir()
	panel := newCapturingPanel(t)
	d := newIdentityDaemon(t, dir, panel.URL, filepath.Join(dir, "absent.json")) // no identity yet: it registers
	logs := captureLog(t)
	logging.SetDefault(logging.New("debug", logs, "text"))
	before := time.Now().Unix()
	if err := d.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce (register): %v", err)
	}
	var req struct {
		Inventory controlplane.RouterInventory `json:"inventory"`
		Proof     *struct {
			Timestamp *int64 `json:"timestamp"`
			Signature string `json:"signature"`
		} `json:"proof"`
	}
	raw := panel.body("/api/router/register")
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decode register body: %v", err)
	}
	if req.Proof == nil || req.Proof.Timestamp == nil || req.Proof.Signature == "" {
		t.Fatalf("no proof in the register request: %s", raw)
	}
	ts := *req.Proof.Timestamp
	if ts < before || ts > time.Now().Unix() {
		t.Errorf("timestamp %d is not the time of the request", ts)
	}
	pub, err := base64.StdEncoding.DecodeString(req.Inventory.DevicePublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("devicePublicKey %q: %v", req.Inventory.DevicePublicKey, err)
	}
	sig, err := base64.StdEncoding.DecodeString(req.Proof.Signature)
	msg := []byte("vectra-register/v1\n" + req.Inventory.DeviceIdentifier + "\n" + strconv.FormatInt(ts, 10))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
		t.Fatalf("the proof does not verify with the request's devicePublicKey over %q", msg)
	}
	if req.Inventory.DeviceIdentifier != d.st.DeviceIdentifier {
		t.Errorf("signed for %q, the router is %q", req.Inventory.DeviceIdentifier, d.st.DeviceIdentifier)
	}
	if n := len(logs.matching(req.Proof.Signature)); n != 0 {
		t.Errorf("the signature is in the log %d time(s)", n)
	}
}

// TestCheckInPayloadMatchesPanelContractFixture is the cheap cross-language
// guard. It pins the SHAPE of the check-in body vctl actually sends (recursive
// key set + JSON value kind, values ignored) to a committed fixture, which
// apps/web parses with the panel's real routerCheckInRequestSchema in
// tests/contract/vctl-check-in.contract.test.ts.
//
// Together the pair catches "vctl sends an inventory the panel would reject"
// for structural drift: a renamed, added, removed or retyped field. It does NOT
// catch value-level violations (an empty min(1) string, a malformed uuid, a bad
// enum member) — those are covered by MissingRequiredFields and the wire test
// above, because the fixture deliberately carries normalized placeholder values.
//
// Regenerate after an intentional payload change:
//
//	VCTL_UPDATE_CONTRACT_FIXTURE=1 go test ./cmd/vctl -run ContractFixture
func TestCheckInPayloadMatchesPanelContractFixture(t *testing.T) {
	dir := t.TempDir()
	legacy := writeLegacyState(t, dir)
	panel := newCapturingPanel(t)
	d := newIdentityDaemon(t, dir, panel.URL, legacy)

	if err := d.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce (check-in): %v", err)
	}
	raw := panel.body("/api/router/check-in")
	if len(raw) == 0 {
		t.Fatal("no check-in request was captured")
	}

	var live map[string]any
	if err := json.Unmarshal(raw, &live); err != nil {
		t.Fatalf("decode check-in body: %v", err)
	}

	fixturePath := filepath.Join("..", "..", "testdata", "contract", "check-in-request.json")
	if os.Getenv("VCTL_UPDATE_CONTRACT_FIXTURE") == "1" {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := json.MarshalIndent(normalizeForFixture(live), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", fixturePath)
		return
	}

	fixtureRaw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read contract fixture: %v (regenerate with VCTL_UPDATE_CONTRACT_FIXTURE=1)", err)
	}
	var fixture map[string]any
	if err := json.Unmarshal(fixtureRaw, &fixture); err != nil {
		t.Fatalf("decode contract fixture: %v", err)
	}

	if liveShape, fixtureShape := shapeOf(live), shapeOf(fixture); liveShape != fixtureShape {
		t.Errorf("check-in payload shape drifted from the panel contract fixture.\n"+
			" live:    %s\n fixture: %s\n"+
			"If the change is intentional, regenerate with "+
			"VCTL_UPDATE_CONTRACT_FIXTURE=1 go test ./cmd/vctl -run ContractFixture "+
			"and re-run the apps/web contract test so the panel schema still accepts it.",
			liveShape, fixtureShape)
	}
}

// shapeOf renders a JSON value as a deterministic type-only signature:
// object keys are sorted and every scalar collapses to its JSON kind, so the
// signature changes only when the payload's structure changes.
func shapeOf(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := "{"
		for i, k := range keys {
			if i > 0 {
				out += ","
			}
			out += k + ":" + shapeOf(t[k])
		}
		return out + "}"
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		return "[" + shapeOf(t[0]) + "]"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	default:
		return "null"
	}
}

// fixturePlaceholders are the values normalizeForFixture substitutes, by JSON
// key. Two different kinds of value are in here, for two different reasons:
//
//   - Host-dependent: timestamps, free space, this machine's hostname. Without
//     these the committed fixture churns on every run and on every machine.
//   - The board facts (model, boardName, target, architecture, openwrtRelease).
//     These are read from `ubus` and /etc/openwrt_release, so on ANY dev host
//     they come out "". All five are z.string().min(1) and NOT optional in
//     routerInventorySchema, so a fixture carrying "" is a payload the panel
//     answers with HTTP 400 — and the apps/web half of this guard would then be
//     pinning a rejection as if it were the contract. The values are router
//     1111111111's, verbatim, and match TestCollectReportsPanelRequiredFieldsOnFilogic.
//
// Only VALUES are substituted, never keys, so the shape the Go half compares is
// untouched.
var fixturePlaceholders = map[string]any{
	"checkedAt":         "2026-08-05T00:00:00Z",
	"hostname":          "1111111111",
	"assetDirectory":    "/usr/share/xray",
	"model":             "Xiaomi Mi Router AX3000T",
	"boardName":         "xiaomi,mi-router-ax3000t",
	"target":            "mediatek/filogic",
	"architecture":      "aarch64_cortex-a53",
	"openwrtRelease":    "24.10.6",
	"memoryTotalMb":     float64(234),
	"memoryAvailableMb": float64(96),
	"overlayFreeMb":     float64(18),
	"tmpFreeMb":         float64(58),
	"swapTotalMb":       float64(0),
	"swapFreeMb":        float64(0),
	// The claim's hash (ui/contract/claim-vector.json's code) and expiry.
	"codeHash":  "287cc1f914dceb7bdec32a2bbb72632815b82bcb4fe25046c8f4fd8fd1247c1b",
	"expiresAt": "2026-09-28T07:12:00Z",
}

// normalizeForFixture rewrites host-dependent and board-dependent values to the
// stable placeholders above, so the committed fixture does not churn on every
// run AND parses cleanly under the panel's real routerCheckInRequestSchema
// (apps/web/tests/contract/vctl-check-in.contract.test.ts).
//
// A placeholder is applied only when it is the SAME JSON kind as the live value.
// Substituting across kinds would quietly repair a retyped field into the
// fixture, which is exactly the drift the shape comparison exists to catch.
func normalizeForFixture(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for k, val := range m {
		if placeholder, ok := fixturePlaceholders[k]; ok && shapeOf(placeholder) == shapeOf(val) {
			out[k] = placeholder
			continue
		}
		switch typed := val.(type) {
		case map[string]any:
			out[k] = normalizeForFixture(typed)
		case []any:
			items := make([]any, 0, len(typed))
			for _, item := range typed {
				items = append(items, normalizeForFixture(item))
			}
			out[k] = items
		default:
			out[k] = val
		}
	}
	return out
}
