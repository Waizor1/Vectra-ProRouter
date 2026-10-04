package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/connectactions"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/feedverify"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/state"
)

func connectTestDaemon(t *testing.T) (*daemon, *[]controlplane.JobResultRequest) {
	t.Helper()
	results := []controlplane.JobResultRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-vectra-router-id") != legacyRouterID || r.Header.Get("x-vectra-router-token") != legacyToken {
			t.Error("unbound authenticated request")
		}
		if r.URL.Path == "/api/router/job-result" {
			var result controlplane.JobResultRequest
			_ = json.NewDecoder(r.Body).Decode(&result)
			results = append(results, result)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"acknowledged":true}`)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	d := newIdentityDaemon(t, dir, server.URL, writeLegacyState(t, dir))
	d.st.ClaimOwner = &controlplane.ClaimOwner{OwnerRef: "owner:fixture", Label: "fake"}
	return d, &results
}
func connectTestJob(id, action string, params map[string]interface{}) controlplane.Job {
	return controlplane.Job{ID: id, Type: connectactions.JobType, Payload: map[string]interface{}{"origin": "partner_action", "actionId": id, "ownerRef": "owner:fixture", "action": action, "params": params}}
}
func TestConnectDispatchBindingReplayAndFailure(t *testing.T) {
	d, results := connectTestDaemon(t)
	oldRoute, oldGate := connectRouteExecute, connectResourceBlocked
	t.Cleanup(func() { connectRouteExecute = oldRoute; connectResourceBlocked = oldGate })
	calls := 0
	connectResourceBlocked = func(*daemon, string) bool { return false }
	connectRouteExecute = func(*daemon, context.Context, string, json.RawMessage) localctl.SocketResponse {
		calls++
		return localctl.SocketResponse{OK: true}
	}
	job := connectTestJob("action1", "select_entry", map[string]interface{}{"entryId": nil})
	for _, target := range []string{"", "wrong-router"} {
		if err := d.executeJob(context.Background(), job, controlplane.CheckInResponse{RouterID: target}); err != nil {
			t.Fatal(err)
		}
	}
	bad := connectTestJob("actionbad", "select_entry", map[string]interface{}{"entryId": nil})
	bad.Payload["ownerRef"] = "foreign"
	_ = d.executeJob(context.Background(), bad, controlplane.CheckInResponse{RouterID: d.st.RouterID})
	if calls != 0 {
		t.Fatal("unauthorized mutation")
	}
	for n := 0; n < 2; n++ {
		if err := d.executeJob(context.Background(), job, controlplane.CheckInResponse{RouterID: d.st.RouterID}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("replay repeated mutation %d", calls)
	}
	last := (*results)[len(*results)-1]
	if last.Status != "success" || last.Result["code"] != "replayed" {
		t.Fatalf("replay outcome %+v", last)
	}
	conflict := job
	conflict.Payload = map[string]interface{}{"origin": "partner_action", "actionId": "action1", "ownerRef": "owner:fixture", "action": "select_entry", "params": map[string]interface{}{"entryId": "changed"}}
	_ = d.executeJob(context.Background(), conflict, controlplane.CheckInResponse{RouterID: d.st.RouterID})
	if calls != 1 {
		t.Fatal("conflict repeated mutation")
	}
	journal, _ := connectactions.OpenJournal(d.cfg.StatePath + ".connect-actions.json")
	e, _ := connectactions.Parse([]byte(`{"origin":"partner_action","actionId":"interrupted","ownerRef":"owner:fixture","action":"select_entry","params":{"entryId":null}}`))
	_, _, _ = journal.Begin(d.connectBinding(), e)
	_ = d.executeJob(context.Background(), connectTestJob("interrupted", "select_entry", map[string]interface{}{"entryId": nil}), controlplane.CheckInResponse{RouterID: d.st.RouterID})
	if calls != 1 {
		t.Fatal("interrupted repeated mutation")
	}
}
func TestConnectDispatchWiFiSecretNeverJournalled(t *testing.T) {
	d, results := connectTestDaemon(t)
	oldGate, oldWifi, oldForget, oldMark := connectResourceBlocked, connectWifiExecute, connectWifiForget, connectWifiMark
	t.Cleanup(func() {
		connectResourceBlocked = oldGate
		connectWifiExecute = oldWifi
		connectWifiForget = oldForget
		connectWifiMark = oldMark
	})
	connectResourceBlocked = func(*daemon, string) bool { return false }
	calls, forget, mark := 0, 0, 0
	connectWifiForget = func(agentcfg.Config) error { forget++; return nil }
	connectWifiMark = func(agentcfg.Config, []byte, string, string, connectactions.WiFi, []connectWifiOwnedAP) error { mark++; return nil }
	connectWifiExecute = func(context.Context, agentcfg.Config, json.RawMessage) (string, bool) {
		calls++
		return "applied", true
	}
	secret := "fake-fixture-password"
	j := connectTestJob("wifi1", "set_wifi", map[string]interface{}{"ssid": "fake-guest", "password": secret})
	for i := 0; i < 2; i++ {
		if err := d.executeJob(context.Background(), j, controlplane.CheckInResponse{RouterID: d.st.RouterID}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || forget != 1 || mark != 1 {
		t.Fatalf("wifi replay/invalidation %d/%d/%d", calls, forget, mark)
	}
	_ = filepath.Walk(filepath.Dir(d.cfg.StatePath), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			raw, _ := os.ReadFile(path)
			if strings.Contains(string(raw), secret) {
				t.Errorf("secret persisted in %s", filepath.Base(path))
			}
		}
		return nil
	})
	raw, _ := json.Marshal(results)
	if strings.Contains(string(raw), secret) {
		t.Fatal("secret in result")
	}
	connectWifiForget = func(agentcfg.Config) error { return errors.New("failure") }
	_ = d.executeJob(context.Background(), connectTestJob("wifi2", "set_wifi", map[string]interface{}{"ssid": "fake-guest", "password": secret}), controlplane.CheckInResponse{RouterID: d.st.RouterID})
	if calls != 1 {
		t.Fatal("mutation after failed invalidation")
	}
}
// A one-band change hands the marker its band and what this owner's previous
// marker covered — read before that marker is forgotten.
func TestConnectDispatchWiFiBandScopesTheOwnerMarker(t *testing.T) {
	d, _ := connectTestDaemon(t)
	oldGate, oldWifi, oldForget, oldMark := connectResourceBlocked, connectWifiExecute, connectWifiForget, connectWifiMark
	t.Cleanup(func() {
		connectResourceBlocked, connectWifiExecute, connectWifiForget, connectWifiMark = oldGate, oldWifi, oldForget, oldMark
	})
	connectResourceBlocked = func(*daemon, string) bool { return false }
	b := d.connectBinding()
	prev := connectWifiOwnedAP{connectWifiAP{Radio: "radio1", Interface: "ap1"}, "fixture-fp"}
	raw, _ := json.Marshal(connectWifiOwnerMarker{RouterID: b.RouterID, OwnerRef: b.OwnerRef, APs: []connectWifiOwnedAP{prev}})
	if err := os.WriteFile(d.cfg.StatePath+".wifi-owner.json", raw, 0600); err != nil {
		t.Fatal(err)
	}
	connectWifiForget = func(cfg agentcfg.Config) error {
		if err := os.Remove(cfg.StatePath + ".wifi-owner.json"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	var gotParams, gotBand string
	var gotKept []connectWifiOwnedAP
	connectWifiExecute = func(_ context.Context, _ agentcfg.Config, p json.RawMessage) (string, bool) {
		gotParams = string(p)
		return "applied", true
	}
	connectWifiMark = func(_ agentcfg.Config, _ []byte, _, _ string, want connectactions.WiFi, kept []connectWifiOwnedAP) error {
		gotBand, gotKept = want.Band, kept
		return nil
	}
	j := connectTestJob("wifi-band", "set_wifi", map[string]interface{}{"ssid": "fake-2g", "password": "fake-fixture-password", "band": "2g"})
	if err := d.executeJob(context.Background(), j, controlplane.CheckInResponse{RouterID: d.st.RouterID}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotParams, `"band":"2g"`) || gotBand != "2g" || len(gotKept) != 1 || gotKept[0] != prev {
		t.Fatalf("band %q kept %v params-band %v", gotBand, gotKept, strings.Contains(gotParams, `"band"`))
	}
	j = connectTestJob("wifi-all", "set_wifi", map[string]interface{}{"ssid": "fake-all", "password": "fake-fixture-password"})
	if err := d.executeJob(context.Background(), j, controlplane.CheckInResponse{RouterID: d.st.RouterID}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotParams, `"band"`) || gotBand != "" || gotKept != nil {
		t.Fatalf("all-band change scoped: %q %v", gotBand, gotKept)
	}
}
func TestConnectConfidentialEnrichmentClonesAndOwnerBinds(t *testing.T) {
	d, _ := connectTestDaemon(t)
	oldRead := connectWifiRead
	t.Cleanup(func() { connectWifiRead = oldRead })
	calls := 0
	connectWifiRead = func(agentcfg.Config, []byte, string, string) []connectConfidentialWifi {
		calls++
		return []connectConfidentialWifi{{Band: "2.4", SSID: "fake", Password: "fake-test-key"}}
	}
	original := &controlplane.RouterConnectTelemetry{OwnerRef: "foreign"}
	inv := controlplane.RouterInventory{Connect: original}
	d.enrichConnectCheckin(&inv)
	if calls != 0 {
		t.Fatal("foreign password read")
	}
	original.OwnerRef = d.st.ClaimOwner.OwnerRef
	d.enrichConnectCheckin(&inv)
	if calls != 1 || original.Wifi != nil || inv.Connect == original || (*inv.Connect.Wifi)[0].Password != "fake-test-key" {
		t.Fatal("confidential clone failed")
	}
}
func TestConnectClientNeverEchoesConfidentialCheckinErrors(t *testing.T) {
	secret := "fake-test-key"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400); io.WriteString(w, secret) }))
	defer server.Close()
	client := controlplane.NewClient(controlplane.Options{BaseURL: server.URL, HTTPClient: server.Client(), RouterID: "fixture", AgentToken: "fake"})
	_, err := client.CheckIn(context.Background(), controlplane.CheckInRequest{})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("confidential response reflected")
	}
}
func TestConnectAllSevenNativeActionsWithFakeDependencies(t *testing.T) {
	d, results := connectTestDaemon(t)
	oldRoute, oldGate, oldWifi, oldForget, oldMark := connectRouteExecute, connectResourceBlocked, connectWifiExecute, connectWifiForget, connectWifiMark
	oldCommand, oldFeed, oldBoot := maintenanceCommand, maintenanceFeed, maintenanceBootID
	t.Cleanup(func() {
		connectRouteExecute = oldRoute
		connectResourceBlocked = oldGate
		connectWifiExecute = oldWifi
		connectWifiForget = oldForget
		connectWifiMark = oldMark
		maintenanceCommand = oldCommand
		maintenanceFeed = oldFeed
		maintenanceBootID = oldBoot
	})
	connectRouteExecute = func(*daemon, context.Context, string, json.RawMessage) localctl.SocketResponse {
		return localctl.SocketResponse{OK: true}
	}
	connectResourceBlocked = func(*daemon, string) bool { return false }
	connectWifiExecute = func(context.Context, agentcfg.Config, json.RawMessage) (string, bool) { return "applied", true }
	connectWifiForget = func(agentcfg.Config) error { return nil }
	connectWifiMark = func(agentcfg.Config, []byte, string, string, connectactions.WiFi, []connectWifiOwnedAP) error { return nil }
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "opkg" && len(args) == 2 && args[0] == "status" {
			return []byte("Version: 0.6.0-r37\n"), nil
		}
		t.Fatalf("unexpected real command path %s", name)
		return nil, nil
	}
	maintenanceFeed = func(context.Context) (string, feedverify.Feed, []feedverify.Package, error) {
		return "aarch64_generic", feedverify.Feed{URL: "https://fake.invalid"}, nil, nil
	}
	maintenanceBootID = func() (string, error) { return "fake-boot", nil }
	for i, tc := range []struct {
		action string
		params map[string]interface{}
	}{
		{"select_entry", map[string]interface{}{"entryId": nil}}, {"set_rules", map[string]interface{}{"direct": []string{".пример.рф"}, "vpn": []string{}}}, {"set_service", map[string]interface{}{"service": "youtube", "entryId": nil}}, {"set_wifi", map[string]interface{}{"ssid": "fake", "password": "fake-key-only"}}, {"reboot", map[string]interface{}{}}, {"update_now", map[string]interface{}{}}, {"set_auto_update", map[string]interface{}{"enabled": true}},
	} {
		j := connectTestJob(fmt.Sprintf("typed%d", i), tc.action, tc.params)
		if err := d.executeJob(context.Background(), j, controlplane.CheckInResponse{RouterID: d.st.RouterID}); err != nil && !(tc.action == "reboot" && errors.Is(err, errMaintenanceActionPending)) {
			t.Fatalf("%s: %v", tc.action, err)
		}
		last := (*results)[len(*results)-1]
		want := "success"
		if tc.action == "reboot" {
			want = "accepted"
		}
		if last.Status != want {
			t.Fatalf("%s result %+v", tc.action, last)
		}
	}
	s, err := d.readMaintenance(d.st.ClaimOwner.OwnerRef)
	if err != nil || !s.AutoUpdate || s.Reboot == nil || !s.Reboot.Acknowledged || s.Reboot.Dispatched {
		t.Fatal("maintenance scheduling semantics")
	}
}
func TestConnectResourceGuardPrecedesMutation(t *testing.T) {
	d, _ := connectTestDaemon(t)
	oldGate, oldRoute := connectResourceBlocked, connectRouteExecute
	t.Cleanup(func() { connectResourceBlocked = oldGate; connectRouteExecute = oldRoute })
	connectResourceBlocked = func(*daemon, string) bool { return true }
	connectRouteExecute = func(*daemon, context.Context, string, json.RawMessage) localctl.SocketResponse {
		t.Fatal("mutation despite resource guard")
		return localctl.SocketResponse{}
	}
	if err := d.executeJob(context.Background(), connectTestJob("lowmem", "select_entry", map[string]interface{}{"entryId": nil}), controlplane.CheckInResponse{RouterID: d.st.RouterID}); err != nil {
		t.Fatal(err)
	}
}
func TestConnectOwnerReassignmentClearsOldSettings(t *testing.T) {
	d, _ := connectTestDaemon(t)
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.EntryRemark = "old owner"
		o.ServiceEntries = map[string]string{"youtube": strings.Repeat("a", 64)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	d.st.AppliedRevisionID = "old-revision"
	d.st.PendingJobResult = &controlplane.JobResultRequest{JobID: "old-job"}
	d.st.CurrentJob = state.CurrentJob{JobID: "old-job"}
	if !d.adoptClaimInfo(context.Background(), controlplane.ClaimInfo{Owner: json.RawMessage(`{"ownerRef":"new-owner","label":"new"}`)}) {
		t.Fatal("owner change did not stop old response execution")
	}
	if d.st.ClaimOwner == nil || d.st.ClaimOwner.OwnerRef != "new-owner" || d.desired != nil || d.st.AppliedRevisionID != "" || d.st.PendingJobResult != nil || d.st.CurrentJob.JobID != "" {
		t.Fatal("old binding state survived")
	}
	o, err := localctl.LoadOverrides(d.cfg.OverridesPath)
	if err != nil || o.EntryRemark != "" || len(o.ServiceEntries) != 0 {
		t.Fatal("old choices survived")
	}
}
func TestConnectCapabilityRequiresRealHardwareAndSignedFeed(t *testing.T) {
	d, _ := connectTestDaemon(t)
	old := connectSetup
	t.Cleanup(func() { connectSetup = old })
	connectSetup = func(context.Context) setup.Facts { return setup.Facts{} }
	c := d.connectCapabilities()
	if c["set_wifi"] || c["set_wifi_band"] || c["update_now"] || c["set_auto_update"] {
		t.Fatal("missing support advertised")
	}
	connectSetup = func(context.Context) setup.Facts {
		return setup.Facts{Wifi: setup.Wifi{Radios: []setup.Radio{{Device: "fake-radio", AP: true}}}}
	}
	s := maintenanceState{OwnerRef: d.st.ClaimOwner.OwnerRef, FeedVerified: true, FeedObservedAt: time.Now(), LastPoll: time.Now()}
	if d.saveMaintenance(s) != nil {
		t.Fatal("save")
	}
	c = d.connectCapabilities()
	if !c["set_wifi"] || !c["set_wifi_band"] || !c["update_now"] || !c["set_auto_update"] {
		t.Fatal("real support omitted")
	}
	d.st.ClaimOwner = nil
	c = d.connectCapabilities()
	if len(c) != 0 {
		t.Fatal("unowned advertised")
	}
}
func TestConnectRebootReplayWaitsForBootProof(t *testing.T) {
	d, results := connectTestDaemon(t)
	oldBoot, oldCommand := maintenanceBootID, maintenanceCommand
	t.Cleanup(func() { maintenanceBootID = oldBoot; maintenanceCommand = oldCommand })
	boot, calls := "boot-fixture-a", 0
	maintenanceBootID = func() (string, error) { return boot, nil }
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "/sbin/reboot" || len(args) != 0 {
			t.Fatalf("unexpected command %s", name)
		}
		calls++
		return nil, nil
	}
	j := connectTestJob("reboot-proof", "reboot", map[string]interface{}{})
	for n := 0; n < 2; n++ {
		if e := d.executeJob(context.Background(), j, controlplane.CheckInResponse{RouterID: d.st.RouterID}); !errors.Is(e, errMaintenanceActionPending) {
			t.Fatalf("pending %v", e)
		}
	}
	for _, r := range *results {
		if r.JobID == j.ID && r.Status != "accepted" {
			t.Fatal("premature terminal reboot")
		}
	}
	if e := d.connectMaintenanceAfterCheckin(context.Background(), d.connectBinding().OwnerRef); e != nil {
		t.Fatal(e)
	}
	if e := d.executeJob(context.Background(), j, controlplane.CheckInResponse{RouterID: d.st.RouterID}); !errors.Is(e, errMaintenanceActionPending) {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatalf("repeated reboot %d", calls)
	}
	boot = "boot-fixture-b"
	if e := d.connectMaintenanceAfterCheckin(context.Background(), d.connectBinding().OwnerRef); e != nil {
		t.Fatal(e)
	}
	last := (*results)[len(*results)-1]
	if last.Status != "success" || last.Result["rebootVerified"] != true {
		t.Fatal("missing physical proof")
	}
	if e := d.executeJob(context.Background(), j, controlplane.CheckInResponse{RouterID: d.st.RouterID}); e != nil {
		t.Fatal(e)
	}
	last = (*results)[len(*results)-1]
	if last.Status != "success" || last.Result["code"] != "replayed" || calls != 1 {
		t.Fatal("terminal replay unsafe")
	}
}
func TestConnectDeliveredReceiptFailureKeepsPending(t *testing.T) {
	d, _ := connectTestDaemon(t)
	old := connectReceiptSave
	t.Cleanup(func() { connectReceiptSave = old })
	connectReceiptSave = func(*daemon, string, string) error { return errors.New("fixture disk full") }
	d.st.PendingJobResult = &controlplane.JobResultRequest{ProtocolVersion: controlplane.ProtocolVersion, RouterID: d.st.RouterID, JobID: "pending-reboot", Status: "accepted"}
	if d.persist() != nil {
		t.Fatal("save")
	}
	d.recoverJournal(context.Background())
	if d.st.PendingJobResult == nil {
		t.Fatal("lost accepted after receipt failure")
	}
	connectReceiptSave = func(*daemon, string, string) error { return nil }
	d.recoverJournal(context.Background())
	if d.st.PendingJobResult != nil {
		t.Fatal("durable receipt retry did not clear pending")
	}
}
func TestConnectCrashBetweenTerminalAndPendingReconstructsTruth(t *testing.T) {
	d, results := connectTestDaemon(t)
	j := connectTestJob("crash-terminal", "select_entry", map[string]interface{}{"entryId": nil})
	raw, _ := json.Marshal(j.Payload)
	e, _ := connectactions.Parse(raw)
	journal, _ := connectactions.OpenJournal(d.cfg.StatePath + ".connect-actions.json")
	if _, _, err := journal.Begin(d.connectBinding(), e); err != nil {
		t.Fatal(err)
	}
	if journal.Complete(d.connectBinding(), j.ID, connectactions.Succeeded) != nil {
		t.Fatal("complete")
	}
	d.st.CurrentJob = state.CurrentJob{JobID: j.ID, JobType: j.Type}
	if d.persist() != nil {
		t.Fatal("save")
	}
	d.recoverJournal(context.Background())
	last := (*results)[len(*results)-1]
	if last.Status != "success" || d.st.PendingJobResult != nil || d.st.CurrentJob.JobID != "" {
		t.Fatal("terminal truth lost during crash recovery")
	}
	record, found, err := journal.Lookup(d.connectBinding(), j.ID)
	if err != nil || !found || record.Status != connectactions.Succeeded {
		t.Fatal("terminal truth corrupted")
	}
}
