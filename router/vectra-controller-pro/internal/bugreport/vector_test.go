package bugreport

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"

	"vectra-controller-pro/internal/uatoken"
)

var update = flag.Bool("update", false, "rewrite ui/contract/report-vector.json")

// The contract's vector: a report and the exact request a router makes of it
// — fixed keys, fixed randomness — for Vectra Connect's tests.
func TestReportVector(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, 32)
	dev := ed25519.NewKeyFromSeed(seed)
	vectraPriv, _ := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{9}, 32))
	at := time.Date(2026, 9, 29, 21, 3, 11, 0, time.UTC)
	r := Report{Schema: 1, ReportID: "0f8d3c2e-6a51-4c1e-9d0b-7f3e2a1c9b44", Code: "VCTL_PANIC", Severity: "critical",
		Fingerprint: Fingerprint("VCTL_PANIC", "panic: runtime error: index out of range [N] with length N"),
		Title:       "vctl crashed: panic: runtime error: index out of range [3] with length 3", FirstAt: at, LastAt: at, Count: 1,
		Router:   Router{DeviceID: "vectra-0123456789ab", Model: "Xiaomi Mi Router AX3000T", Arch: "aarch64_cortex-a53", OpenWrt: "24.10.6", Vctl: "0.6.0-r19"},
		Evidence: Evidence{Stack: "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:\n"}}
	body, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	url := "https://api-app.vectra-pro.net/errors/router?b=" + hex.EncodeToString(sum[:])
	hwid := "4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"
	ua, err := uatoken.UserAgent(uatoken.Request{Version: "0.6.0-r19", DeviceID: r.Router.DeviceID, DeviceKey: dev, HWID: hwid, URL: url,
		Kid: 1, ServerPub: vectraPriv.PublicKey().Bytes(), Now: at}, bytes.NewReader(bytes.Repeat([]byte{5}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	vector := map[string]any{
		"comment":          "A router's report request (docs/CONNECT-BUG-REPORTS.md), made with fixed keys: open the token with vectraPrivateKey (kid 1) and devicePublicKey for target, and check b = sha256(body).",
		"vectraPrivateKey": base64.StdEncoding.EncodeToString(vectraPriv.Bytes()),
		"devicePublicKey":  base64.StdEncoding.EncodeToString(dev.Public().(ed25519.PublicKey)),
		"now":              at.Format(time.RFC3339),
		"method":           "POST",
		"url":              url,
		"target":           url[len("https://api-app.vectra-pro.net"):],
		"headers": map[string]string{"User-Agent": ua, "x-hwid": hwid, "x-device-os": "OpenWrt", "x-device-model": r.Router.Model,
			"x-ver-os": r.Router.OpenWrt, "x-vectra-report-id": r.ReportID, "Content-Type": "application/json"},
		"body": string(body),
	}
	keys := func(kid byte) (*ecdh.PrivateKey, bool) { return vectraPriv, kid == 1 }
	devices := func(id string) (ed25519.PublicKey, bool) {
		return dev.Public().(ed25519.PublicKey), id == r.Router.DeviceID
	}
	if _, err := uatoken.Open(ua, keys, devices, hwid, vector["target"].(string), at, time.Minute); err != nil {
		t.Fatalf("the vector does not open: %v", err)
	}
	out, _ := json.MarshalIndent(vector, "", "  ")
	const path = "../../ui/contract/report-vector.json"
	if *update {
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if old, err := os.ReadFile(path); err != nil || !bytes.Equal(bytes.TrimSpace(old), bytes.TrimSpace(out)) {
		t.Fatalf("ui/contract/report-vector.json is not this vector (run with -update): %v", err)
	}
}
