package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/state"
)

func testVectraKey(t *testing.T) (*ecdh.PrivateKey, controlplane.ClaimKey) {
	t.Helper()
	seed := sha256.Sum256([]byte("vctl-claim-test/vectra"))
	priv, err := ecdh.X25519().NewPrivateKey(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	return priv, controlplane.ClaimKey{Kid: 7, PublicKey: base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())}
}

// openQR is Vectra's side, reduced to what these tests check: the QR opens
// with Vectra's key, and its plaintext names this router.
func openQR(t *testing.T, qr string, vectra *ecdh.PrivateKey) (plain []byte) {
	t.Helper()
	env, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(qr, claim.QRPrefix))
	if err != nil || !strings.HasPrefix(qr, claim.QRPrefix) || len(env) < 45 {
		t.Fatalf("not a claim QR: %q", qr)
	}
	eph, err := ecdh.X25519().NewPublicKey(env[1:33])
	if err != nil {
		t.Fatal(err)
	}
	shared, err := vectra.ECDH(eph)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(claim.DeriveKey(shared, env[1:33], vectra.PublicKey().Bytes()))
	gcm, _ := cipher.NewGCM(block)
	plain, err = gcm.Open(nil, env[33:45], env[45:], claim.AAD(env[0]))
	if err != nil {
		t.Fatalf("the QR does not open with Vectra's key: %v", err)
	}
	return plain
}

func TestTheClaimerFollowsTheRules(t *testing.T) {
	st := state.PersistedState{}
	if err := state.EnsureIdentity(&st); err != nil {
		t.Fatal(err)
	}
	c := newClaimer(st, "Xiaomi Mi Router AX3000T", time.Minute)
	t0 := time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)

	v := c.view(t0)
	if v == nil || v.State != "unclaimed" || len(v.Code) != 8 || v.QR != "" || !v.ExpiresAt.Equal(t0.Add(time.Minute+claim.Grace)) || v.Owner != nil {
		t.Fatalf("view = %+v", v)
	}
	a := c.announcement(t0.Add(30 * time.Second))
	// The panel is told the expiry the code is taken until, the grace in it
	// (router-claim-state.ts keeps it as it is): the same the UI shows.
	if a == nil || a.CodeHash != claim.CodeHash(v.Code) || a.ExpiresAt != v.ExpiresAt.Format(time.RFC3339) || a.ExpiresAt != "2026-09-28T07:11:00Z" {
		t.Fatalf("announcement = %+v for code %s", a, v.Code)
	}
	if v2 := c.view(t0.Add(time.Minute)); v2.Code == v.Code {
		t.Fatal("the code did not rotate")
	}

	vectra, key := testVectraKey(t)
	k, _ := claim.ParseKey(key.Kid, key.PublicKey)
	c.setKey(k)
	v = c.view(t0.Add(time.Minute))
	if v.QR == "" || c.view(t0.Add(time.Minute+time.Second)).QR != v.QR {
		t.Fatalf("no stable QR once the key is known: %+v", v)
	}
	plain := openQR(t, v.QR, vectra)
	if !strings.Contains(string(plain), st.DeviceIdentifier) || !strings.Contains(string(plain), "AX3000T") {
		t.Fatal("the QR does not name this router")
	}

	c.setOwner(&controlplane.ClaimOwner{Label: "Иван П."})
	if v := c.view(t0); v.State != "claimed" || v.Owner == nil || v.Owner.Label != "Иван П." {
		t.Fatalf("claimed view = %+v", v)
	}
	c.setLinked(true)
	if c.view(t0) != nil || c.announcement(t0) != nil {
		t.Fatal("a linked router still offers a claim")
	}
}

// claimPanel accepts the router and answers its check-ins with what the
// panel may say about claiming it; it keeps every check-in body.
type claimPanel struct {
	*httptest.Server
	mu     sync.Mutex
	bodies [][]byte
	info   controlplane.ClaimInfo
}

func newClaimPanel(t *testing.T, info controlplane.ClaimInfo) *claimPanel {
	t.Helper()
	p := &claimPanel{info: info}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/router/register":
			_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{RouterID: "r-claim", IssuedToken: "tok-claim", Status: "approved"})
		case "/api/router/check-in":
			var body json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.mu.Lock()
			p.bodies = append(p.bodies, body)
			info := p.info
			p.mu.Unlock()
			_ = json.NewEncoder(w).Encode(controlplane.CheckInResponse{Status: "ok", ClaimInfo: info})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *claimPanel) lastClaim(t *testing.T) *controlplane.ClaimAnnouncement {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bodies) == 0 {
		t.Fatal("no check-in reached the panel")
	}
	var req struct {
		Claim *controlplane.ClaimAnnouncement `json:"claim"`
	}
	if err := json.Unmarshal(p.bodies[len(p.bodies)-1], &req); err != nil {
		t.Fatal(err)
	}
	return req.Claim
}

func newClaimDaemon(t *testing.T, dir, panelURL string) *daemon {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"controlUrl": panelURL, "statePath": filepath.Join(dir, "state.json"), "statusPath": filepath.Join(dir, "status.json"),
		"xrayConfigPath": filepath.Join(dir, "operator.json"), "providerConfigPath": filepath.Join(dir, "provider-config.json"),
		"xrayRenderPath": filepath.Join(dir, "xray.json"), "xrayBinary": writeFakeXray(t, dir),
		"legacyStatePath": filepath.Join(dir, "no-legacy.json"),
	})
	agent := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(agent, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadAgentConfigForTest(t, agent)
	if err != nil {
		t.Fatal(err)
	}
	d, err := newDaemon(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d.rescuePolicy.HealthURLs = []string{panelURL}
	d.rescuePolicy.TraceURLs = nil // the panel stands in for the internet: no trace
	return d
}

// Unlinked, the router tells the panel the hash of the code it shows, keeps
// what the panel answers, and its QR then opens with Vectra's key.
func TestCheckInAnnouncesTheCodeAndKeepsWhatThePanelSays(t *testing.T) {
	vectra, key := testVectraKey(t)
	panel := newClaimPanel(t, controlplane.ClaimInfo{ClaimKey: &key, BotUsername: "VectraBot"})
	dir := t.TempDir()
	d := newClaimDaemon(t, dir, panel.URL)
	ctx := context.Background()
	if err := d.runOnce(ctx); err != nil { // register
		t.Fatal(err)
	}
	if err := d.runOnce(ctx); err != nil { // check-in
		t.Fatal(err)
	}
	ann := panel.lastClaim(t)
	view := d.liveRuntime().Claim
	if ann == nil || view == nil || ann.CodeHash != claim.CodeHash(view.Code) || ann.ExpiresAt != view.ExpiresAt.Format(time.RFC3339) {
		t.Fatalf("announced %+v for the view %+v", ann, view)
	}
	for _, b := range panel.bodies {
		if strings.Contains(string(b), view.Code) {
			t.Fatal("the code itself reached the panel")
		}
	}
	if view.QR == "" || !strings.Contains(string(openQR(t, view.QR, vectra)), d.st.DeviceIdentifier) {
		t.Fatalf("QR %q", view.QR)
	}
	if d.st.ClaimKey == nil || d.st.ClaimKey.Kid != 7 || d.st.BotUsername != "VectraBot" {
		t.Fatalf("not kept: %+v %q", d.st.ClaimKey, d.st.BotUsername)
	}

	// Claimed: the owner shows; a restart remembers it and the key.
	panel.mu.Lock()
	panel.info = controlplane.ClaimInfo{Owner: json.RawMessage(`{"label":"Иван П."}`)}
	panel.mu.Unlock()
	if err := d.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if v := d.liveRuntime().Claim; v.State != "claimed" || v.Owner.Label != "Иван П." || v.QR == "" {
		t.Fatalf("claimed = %+v", v)
	}
	d2 := newClaimDaemon(t, dir, panel.URL)
	if v := d2.liveRuntime().Claim; v == nil || v.State != "claimed" || v.QR == "" {
		t.Fatalf("after a restart = %+v", v)
	}

	// "owner": null — nobody owns it any more; an absent owner changes nothing.
	panel.mu.Lock()
	panel.info = controlplane.ClaimInfo{}
	panel.mu.Unlock()
	_ = d.runOnce(ctx)
	if v := d.liveRuntime().Claim; v.State != "claimed" {
		t.Fatalf("an absent owner changed the claim: %+v", v)
	}
	panel.mu.Lock()
	panel.info = controlplane.ClaimInfo{Owner: json.RawMessage(`null`)}
	panel.mu.Unlock()
	_ = d.runOnce(ctx)
	if v := d.liveRuntime().Claim; v.State != "unclaimed" || v.Owner != nil || d.st.ClaimOwner != nil {
		t.Fatalf("owner null did not unclaim: %+v", v)
	}
}

func TestWhatTheRouterCannotUseIsDropped(t *testing.T) {
	panel := newClaimPanel(t, controlplane.ClaimInfo{})
	d := newClaimDaemon(t, t.TempDir(), panel.URL)
	d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{
		ClaimKey:    &controlplane.ClaimKey{Kid: 1, PublicKey: base64.StdEncoding.EncodeToString([]byte("short"))},
		BotUsername: "evil.example/phish?x=",
		Owner:       json.RawMessage(`{"label":` + strings.Repeat(`"`, 1) + strings.Repeat("я", 100) + `"}`),
	})
	if d.st.ClaimKey != nil || d.st.BotUsername != "" {
		t.Fatalf("kept an unusable key or bot: %+v %q", d.st.ClaimKey, d.st.BotUsername)
	}
	if d.st.ClaimOwner == nil || len([]rune(d.st.ClaimOwner.Label)) != 64 {
		t.Fatalf("owner label not cut to 64 characters: %+v", d.st.ClaimOwner)
	}
	d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{Owner: json.RawMessage(`"not an object"`)})
	if d.st.ClaimOwner == nil {
		t.Fatal("an unreadable owner cleared the owner")
	}
}

// Linked (an operator config), the router offers no claim at all.
func TestALinkedRouterOffersNoClaim(t *testing.T) {
	panel := newClaimPanel(t, controlplane.ClaimInfo{})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "operator.json"), operatorExample(t), 0o600); err != nil {
		t.Fatal(err)
	}
	d := newClaimDaemon(t, dir, panel.URL)
	ctx := context.Background()
	_ = d.runOnce(ctx) // register
	if err := d.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	panel.mu.Lock()
	body := string(panel.bodies[len(panel.bodies)-1])
	panel.mu.Unlock()
	if !strings.Contains(body, `"claim":null`) || d.liveRuntime().Claim != nil {
		t.Fatalf("a linked router's check-in carries a claim: %s", body)
	}
}

// The panel must hear of a new code the moment the UI shows it, not at the
// next check-in: until then a person typing the code on screen would be told
// it is unknown. The UI's view wakes the loop once per new code, never for the
// first code or a repeat of the same one.
func TestANewCodeOnScreenWakesTheLoop(t *testing.T) {
	st := state.PersistedState{}
	if err := state.EnsureIdentity(&st); err != nil {
		t.Fatal(err)
	}
	c := newClaimer(st, "Xiaomi Mi Router AX3000T", time.Minute)
	t0 := time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)
	woken := func() bool {
		select {
		case <-c.rotated:
			return true
		default:
			return false
		}
	}
	first := c.view(t0).Code
	if woken() {
		t.Fatal("the first code woke the loop")
	}
	if c.view(t0.Add(30*time.Second)).Code != first || woken() {
		t.Fatal("the same code woke the loop")
	}
	if c.view(t0.Add(time.Minute+time.Second)).Code == first {
		t.Fatal("the code did not rotate")
	}
	if !woken() {
		t.Fatal("a new code on screen did not wake the loop")
	}
	// A wake-up already pending is not piled up.
	c.view(t0.Add(2*time.Minute + 2*time.Second))
	c.view(t0.Add(3*time.Minute + 3*time.Second))
	if !woken() || woken() {
		t.Fatal("wake-ups piled up instead of one pending")
	}

	d := &daemon{claim: c, uiReqs: make(chan uiRequest)}
	c.view(t0.Add(4*time.Minute + 4*time.Second))
	done := make(chan bool, 1)
	go func() { done <- d.waitForTick(context.Background(), nil) }()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("waitForTick returned false for a new code")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForTick did not return for a new code")
	}
}
