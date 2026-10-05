package controlplane

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// steadyStateCheckIn is the check-in the legacy agent sends between jobs: a
// PassWall router that applied a panel revision and reports nothing to import.
// Built from the agent's own wire types, so the fixture carries exactly the
// field names and omitempty behaviour the agent puts on the wire.
func steadyStateCheckIn() CheckInRequest {
	return CheckInRequest{
		ProtocolVersion: ProtocolVersion,
		RouterID:        "bdfdb919-5e06-4344-ad8b-67a16f3b6fcf",
		Inventory: RouterInventory{
			ProtocolVersion:          ProtocolVersion,
			DeviceIdentifier:         "vectra-07bf0887f662",
			DevicePublicKey:          "bGVnYWN5LWRldmljZS1wdWJsaWMta2V5",
			ControllerVersion:        "0.1.13-r42",
			ControllerRuntimeVersion: "0.1.13-r42",
			Hostname:                 "fleet-router",
			PanelDomain:              "https://router.vectra-pro.net",
			Model:                    "Xiaomi Mi Router AX3000T",
			BoardName:                "xiaomi,mi-router-ax3000t",
			LayoutFamily:             "ubootmod",
			Target:                   "mediatek/filogic",
			Architecture:             "aarch64_cortex-a53",
			OpenWrtRelease:           "24.10.6",
			PasswallEnabled:          true,
			SelectedNodeID:           "myshunt",
			SelectedNodeLabel:        "myshunt",
			NodeCount:                42,
			SubscriptionCount:        1,
			SubscriptionHealth:       RouterSubscriptionHealth{HwidEnabled: true, ScheduleEnabled: true},
			PackageVersions:          map[string]string{"luci-app-passwall2": "26.8.10-r1", "xray-core": "26.3.27-r1"},
			BinaryVersions:           map[string]string{"xray": "26.3.27"},
			RulesAssets:              RouterRulesAssets{AssetDirectory: "/usr/share/v2ray/"},
			Resources:                RouterResources{MemoryTotalMB: 234, MemoryAvailableMB: 92, SwapTotalMB: 117, SwapFreeMB: 100, OverlayFreeMB: 18, TMPFreeMB: 60},
			ServiceHealth:            RouterServiceHealth{Controller: "running", Passwall: "running", PasswallServer: "stopped", DNSMasq: "running"},
			TelegramReachability:     &RouterReachabilityProbe{Reachable: true, CheckedAt: "2026-10-05T09:00:00Z", Status: "reachable"},
			ConfigDigest:             "3f1c0d9a4b6e5f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a",
			AppliedRevisionID:        "1a2b3c4d-0000-4000-8000-000000000042",
		},
		Health: RouterHealth{
			CurrentMode:                "proxy",
			ProxyConnectivitySuccesses: 2,
			ServerReachable:            true,
			RecoveryPhase:              "idle",
		},
	}
}

// The panel half (apps/web/src/server/vectra/router-control.check-in-fast-path.test.ts)
// feeds this fixture to the real check-in handler; regenerate with
//
//	AGENT_UPDATE_CONTRACT_FIXTURE=1 go test ./internal/controlplane -run ContractFixture
func TestCheckInRequestMatchesPanelContractFixture(t *testing.T) {
	live, err := json.MarshalIndent(steadyStateCheckIn(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	live = append(live, '\n')

	fixturePath := filepath.Join("..", "..", "testdata", "contract", "check-in-request.json")
	if os.Getenv("AGENT_UPDATE_CONTRACT_FIXTURE") == "1" {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, live, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", fixturePath)
		return
	}

	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read contract fixture: %v (regenerate with AGENT_UPDATE_CONTRACT_FIXTURE=1)", err)
	}
	if !bytes.Equal(live, fixture) {
		t.Errorf("check-in payload drifted from the panel contract fixture %s; regenerate it and re-run the apps/web tests", fixturePath)
	}
}

// The agent never reports an engine mode: that absence is how the panel tells
// it from vctl and keeps sending it the full desired revision every check-in.
func TestCheckInRequestCarriesNoEngineMode(t *testing.T) {
	raw, err := json.Marshal(steadyStateCheckIn())
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Inventory map[string]any `json:"inventory"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.Inventory["engineMode"]; ok {
		t.Fatal("legacy agent inventory must not carry engineMode")
	}
}
