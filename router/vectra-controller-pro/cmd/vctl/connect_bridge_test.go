package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/connectactions"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/feedverify"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/xrayview"
)

// A test-binary bridge only: never linked into vctl, and never enabled by an
// ordinary test run. All device operations are synthetic fixture seams.
func TestConnectBridgeWorker(t *testing.T) {
	inputPath, outputPath := os.Getenv("VCTL_CONNECT_BRIDGE_INPUT"), os.Getenv("VCTL_CONNECT_BRIDGE_OUTPUT")
	if inputPath == "" || outputPath == "" {
		t.Skip("cross-process fixture bridge not requested")
	}
	var input struct {
		RouterID         string             `json:"routerId"`
		OwnerRef         string             `json:"ownerRef"`
		Job              *controlplane.Job  `json:"job"`
		Jobs             []controlplane.Job `json:"jobs"`
		ConfirmReboot    bool               `json:"confirmReboot"`
		ReportWifi       bool               `json:"reportWifi"`
		ResponseRouterID *string            `json:"responseRouterId"`
	}
	raw, err := os.ReadFile(inputPath)
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &input) != nil || input.RouterID == "" || !connectOwnerPattern.MatchString(input.OwnerRef) {
		t.Fatal("invalid bridge fixture input")
	}
	jobs := input.Jobs
	if input.Job != nil {
		jobs = append(jobs, *input.Job)
	}
	if len(jobs) > 128 {
		t.Fatal("too many fixture jobs")
	}
	for _, j := range jobs {
		if j.Type != connectactions.JobType {
			t.Fatal("bridge permits only typed Connect fixture jobs")
		}
	}
	d, _ := connectTestDaemon(t)
	d.st.RouterID = input.RouterID
	d.st.ClaimOwner = &controlplane.ClaimOwner{OwnerRef: input.OwnerRef, Label: "fixture"}
	results := []controlplane.JobResultRequest{}
	authenticated := true
	var resultMu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resultMu.Lock()
		defer resultMu.Unlock()
		if r.Header.Get("x-vectra-router-id") != input.RouterID || r.Header.Get("x-vectra-router-token") != "bridge-fixture-token" {
			authenticated = false
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/api/router/job-result" {
			var result controlplane.JobResultRequest
			if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&result) != nil {
				t.Error("invalid fixture job result")
			} else {
				results = append(results, result)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"acknowledged":true}`)
	}))
	defer server.Close()
	d.client = controlplane.NewClient(controlplane.Options{BaseURL: server.URL, HTTPClient: server.Client(), RouterID: input.RouterID, AgentToken: "bridge-fixture-token"})
	d.st.AgentToken = "bridge-fixture-token"
	oldRoute, oldGate, oldWifi, oldForget, oldMark, oldRead := connectRouteExecute, connectResourceBlocked, connectWifiExecute, connectWifiForget, connectWifiMark, connectWifiRead
	oldCommand, oldFeed, oldBoot, oldSetup := maintenanceCommand, maintenanceFeed, maintenanceBootID, connectSetup
	t.Cleanup(func() {
		connectRouteExecute, connectResourceBlocked, connectWifiExecute, connectWifiForget, connectWifiMark, connectWifiRead = oldRoute, oldGate, oldWifi, oldForget, oldMark, oldRead
		maintenanceCommand, maintenanceFeed, maintenanceBootID, connectSetup = oldCommand, oldFeed, oldBoot, oldSetup
	})
	mutations := 0
	var overrides localctl.Overrides
	entryRaw := json.RawMessage(`{"outbounds":[{"tag":"bridge-pl5","protocol":"vless"}],"routing":{"rules":[{"domain":["geosite:youtube"],"outboundTag":"bridge-pl5"}]}}`)
	cache := &localctl.EntriesCache{Remarks: []string{"Bridge fixture"}, Entries: []json.RawMessage{entryRaw}}
	summary := localctl.Summarize(cache)
	connectResourceBlocked = func(*daemon, string) bool { return false }
	connectRouteExecute = func(_ *daemon, _ context.Context, action string, params json.RawMessage) localctl.SocketResponse {
		change, code := connectRouteChange(action, params, cache)
		if code != "" {
			return localctl.SocketResponse{Code: code}
		}
		change.ApplyTo(&overrides)
		mutations++
		return localctl.SocketResponse{OK: true, Code: "applied"}
	}
	wifiSSID := "Bridge fixture"
	connectWifiExecute = func(_ context.Context, _ agentcfg.Config, p json.RawMessage) (string, bool) {
		var v struct {
			SSID string `json:"ssid"`
		}
		if json.Unmarshal(p, &v) != nil {
			return "invalid_params", false
		}
		wifiSSID = v.SSID
		mutations++
		return "applied", true
	}
	connectWifiForget = func(agentcfg.Config) error { return nil }
	connectWifiMark = func(agentcfg.Config, string, string, string, []connectWifiAP) error { return nil }
	connectWifiRead = func(agentcfg.Config, string, string) []connectConfidentialWifi { return nil }
	var executedWifiSecret string
	if input.ReportWifi {
		fixture := wifiSecretFixture(t)
		connectWifiForget = connectForgetWifiOwner
		connectWifiMark = connectMarkWifiOwner
		connectWifiRead = connectReadOwnerWifi
		connectWifiExecute = func(_ context.Context, _ agentcfg.Config, params json.RawMessage) (string, bool) {
			var change struct {
				SSID     string `json:"ssid"`
				Password string `json:"password"`
			}
			if json.Unmarshal(params, &change) != nil || !connectWifiParamsValid(change.SSID, change.Password) {
				return "invalid_params", false
			}
			// Fixture-only UCI literals, never a shell command. All paths are temp.
			text := "config wifi-device 'radio0'\n option band '2g'\nconfig wifi-iface 'ap0'\n option device 'radio0'\n option mode 'ap'\n option encryption 'psk2'\n option ssid " + strconv.Quote(change.SSID) + "\n option key " + strconv.Quote(change.Password) + "\n"
			fixture.write(t, fixture.env.WirelessConfig, text)
			fixture.write(t, fixture.env.WifiApply, lastApply)
			wifiSSID, executedWifiSecret = change.SSID, change.Password
			mutations++
			return "applied", true
		}
	}
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "opkg" && len(args) == 2 && args[0] == "status" {
			return []byte("Version: 0.6.0-r37\n"), nil
		}
		return nil, errors.New("fixture forbids command execution")
	}
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return "aarch64_generic", feedverify.Feed{URL: "https://fixture.invalid"}, nil, nil
	}
	boot := "bridge-fixture-boot"
	maintenanceBootID = func() (string, error) { return boot, nil }
	connectSetup = func(context.Context) setup.Facts {
		return setup.Facts{Wifi: setup.Wifi{Radios: []setup.Radio{{Device: "radio0", Band: "2g", AP: true, SSID: wifiSSID}}}}
	}
	responseRouter := input.RouterID
	if input.ResponseRouterID != nil {
		responseRouter = *input.ResponseRouterID
	}
	for _, j := range jobs {
		if err := d.executeJob(context.Background(), j, controlplane.CheckInResponse{RouterID: responseRouter}); err != nil && !errors.Is(err, errMaintenanceActionPending) {
			t.Fatal("fixture dispatcher failed")
		}
	}
	if input.ConfirmReboot {
		boot = "bridge-fixture-next-boot"
		if err := d.connectMaintenanceAfterCheckin(context.Background(), input.OwnerRef); err != nil {
			t.Fatal("fixture boot proof failed")
		}
	}
	// Actual parsers/settings/verdict run over a measured synthetic observation.
	now := time.Now()
	in := measuredConnectInputs(now)
	in.View, err = xrayview.Parse([]byte(`{"outbounds":[{"tag":"bridge-pl5","protocol":"vless"}],"routing":{"balancers":[{"tag":"main","selector":["bridge-pl5"],"strategy":{"type":"leastLoad"}}]}}`))
	if err != nil {
		t.Fatal("invalid synthetic view fixture")
	}
	in.Index = &localctl.EntriesIndex{Entries: summary}
	in.Overrides = overrides
	in.Runtime.Entry = &localctl.Entry{Index: 0, Remark: summary[0].Remark, Local: true}
	wire := connectSettings(in, connectSetup(context.Background()))
	wire.Verdict, wire.ExitCountry = connectVerdict(in, map[string]exitcheck.Located{"bridge-pl5": {CC: "DE", At: now}})
	capabilities := map[string]bool{"select_entry": true, "set_rules": true, "set_service": true, "set_wifi": true, "reboot": true, "update_now": true, "set_auto_update": true}
	connectOwnerCapabilities(&wire, d.st.ClaimOwner, capabilities)
	auto, available, err := d.maintenanceSnapshot(input.OwnerRef)
	if err == nil {
		wire.AutoUpdate = &auto
		wire.AvailableVersion = available
	}
	sharedWire := wire
	if input.ReportWifi {
		inv := controlplane.RouterInventory{Connect: &wire}
		d.enrichConnectCheckin(&inv)
		wire = *inv.Connect
		if sharedWire.Wifi != nil {
			for _, wifi := range *sharedWire.Wifi {
				if wifi.Password != "" {
					t.Fatal("confidential enrichment mutated shared snapshot")
				}
			}
		}
		if executedWifiSecret != "" {
			if wire.Wifi == nil || len(*wire.Wifi) != 1 || (*wire.Wifi)[0].Password != executedWifiSecret {
				t.Fatal("fixture confidential readback failed")
			}
		}
	}
	persisted := map[string]json.RawMessage{}
	for label, path := range map[string]string{"journal": d.cfg.StatePath + ".connect-actions.json", "maintenance": d.maintenancePath(), "wifiMarker": d.cfg.StatePath + ".wifi-owner.json"} {
		if raw, err := os.ReadFile(path); err == nil {
			persisted[label] = raw
		}
	}
	// Inputs are fake, nevertheless assert password separation in the durable
	// journal and ordinary result/shared-snapshot output before writing it.
	resultMu.Lock()
	defer resultMu.Unlock()
	output := struct {
		Results       []controlplane.JobResultRequest     `json:"results"`
		Capabilities  map[string]bool                     `json:"capabilities"`
		WireConnect   controlplane.RouterConnectTelemetry `json:"wireConnect"`
		SafePersisted map[string]json.RawMessage          `json:"safePersisted"`
		Authenticated bool                                `json:"authenticated"`
		Mutations     int                                 `json:"mutations"`
	}{results, capabilities, wire, persisted, authenticated, mutations}
	out, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		t.Fatal("fixture output serialization failed")
	}
	safeOutput, err := json.Marshal(struct {
		Results   []controlplane.JobResultRequest
		Shared    controlplane.RouterConnectTelemetry
		Persisted map[string]json.RawMessage
	}{results, sharedWire, persisted})
	if err != nil {
		t.Fatal("safe fixture output serialization failed")
	}
	for _, j := range jobs {
		p, _ := j.Payload["params"].(map[string]interface{})
		if secret, _ := p["password"].(string); secret != "" && (strings.Contains(string(safeOutput), secret) || (!input.ReportWifi && strings.Contains(string(out), secret))) {
			t.Fatal("fixture output disclosed password")
		}
	}
	if !authenticated {
		t.Fatal("fixture authentication failed")
	}
	if os.WriteFile(outputPath, out, 0600) != nil || os.Chmod(outputPath, 0600) != nil {
		t.Fatal("fixture output write failed")
	}
}
