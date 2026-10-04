package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/connectactions"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/uci"
)

// Synthetic fixtures only: a fixed readback key and the requests that set the
// fixture wireless config's values.
var (
	wifiTestKey = bytes.Repeat([]byte{0x5a}, 32)
	wifi5g      = connectactions.WiFi{SSID: "Home-5G", Password: "old-5g-key"}
	wifi5gOnly  = connectactions.WiFi{SSID: "Home-5G", Password: "old-5g-key", Band: "5g"}
	wifi2gOnly  = connectactions.WiFi{SSID: "Home-2G", Password: "old-2g-key", Band: "2g"}
)

func wifiSecretFixture(t *testing.T) *wizardRouter {
	t.Helper()
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, wizardWireless)
	w.write(t, w.env.WifiApply, lastApply)
	oldEnv, oldRead := connectWifiSecretEnv, connectWifiSecretReadFile
	connectWifiSecretEnv = func() setup.Env { return w.env }
	connectWifiSecretReadFile = os.ReadFile
	t.Cleanup(func() { connectWifiSecretEnv, connectWifiSecretReadFile = oldEnv, oldRead })
	return w
}

func TestWifiSecretNeedsVerifiedOwnerMarker(t *testing.T) {
	w := wifiSecretFixture(t)
	reads := 0
	connectWifiSecretReadFile = func(path string) ([]byte, error) {
		if path == w.env.WirelessConfig {
			reads++
		}
		return os.ReadFile(path)
	}
	if got := connectReadOwnerWifi(w.cfg, wifiTestKey, "router-fixture", "owner-fixture"); len(got) != 0 || reads != 0 {
		t.Fatal("read preexisting key without marker")
	}
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "router-fixture", "owner-fixture", wifi5g, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(w.cfg.StatePath + ".wifi-owner.json")
	if err != nil || strings.Contains(string(raw), "old-5g-key") || strings.Contains(string(raw), "Home-5G") {
		t.Fatal("marker contains secret/network data")
	}
	st, _ := os.Lstat(w.cfg.StatePath + ".wifi-owner.json")
	if st.Mode().Perm() != 0600 {
		t.Fatal("marker not private")
	}
	got := connectReadOwnerWifi(w.cfg, wifiTestKey, "router-fixture", "owner-fixture")
	if len(got) != 1 || got[0].Band != "5g" || got[0].SSID != "Home-5G" || got[0].Password != "old-5g-key" {
		t.Fatal("incorrect fixture readback")
	}
	// Marker is durable: no runtime apply file after restart.
	os.Remove(w.env.WifiApply)
	if len(connectReadOwnerWifi(w.cfg, wifiTestKey, "router-fixture", "owner-fixture")) != 1 {
		t.Fatal("marker did not survive restart")
	}
	reads = 0
	if len(connectReadOwnerWifi(w.cfg, wifiTestKey, "router-fixture", "other-owner")) != 0 || reads != 0 {
		t.Fatal("old binding exposed credential")
	}
	if len(connectReadOwnerWifi(w.cfg, wifiTestKey, "other-router", "owner-fixture")) != 0 {
		t.Fatal("wrong router exposed credential")
	}
}

func TestWifiSecretRefusesUnsafeState(t *testing.T) {
	for _, kind := range []string{"pending", "job", "applying", "failed", "permissions", "symlink", "changed-ap", "invalid-key"} {
		t.Run(kind, func(t *testing.T) {
			w := wifiSecretFixture(t)
			if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi5g, nil); err != nil {
				t.Fatal(err)
			}
			marker := w.cfg.StatePath + ".wifi-owner.json"
			switch kind {
			case "pending":
				w.write(t, filepath.Join(w.env.UCISaveDir, "wireless"), "owner delta")
			case "job":
				w.write(t, w.env.WifiJob, "owner rollback")
			case "applying", "failed":
				w.write(t, w.env.WifiApply, `{"state":"`+kind+`"}`)
			case "permissions":
				os.Chmod(marker, 0644)
			case "symlink":
				os.Rename(marker, marker+"-target")
				os.Symlink(marker+"-target", marker)
			case "changed-ap":
				w.write(t, w.env.WirelessConfig, strings.ReplaceAll(wizardWireless, "'ap1'", "'replacement'"))
			case "invalid-key":
				w.write(t, w.env.WirelessConfig, strings.ReplaceAll(wizardWireless, "old-5g-key", "bad"))
			}
			if len(connectReadOwnerWifi(w.cfg, wifiTestKey, "r-fixture", "o-fixture")) != 0 {
				t.Fatal("unsafe state exposed key")
			}
		})
	}
}

func TestWifiSecretMarkerRefusesUnverifiedAndSymlink(t *testing.T) {
	w := wifiSecretFixture(t)
	w.write(t, w.env.WifiApply, `{"state":"unverified"}`)
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi5g, nil); err == nil {
		t.Fatal("marked unverified config")
	}
	w.write(t, w.env.WifiApply, lastApply)
	marker := w.cfg.StatePath + ".wifi-owner.json"
	w.write(t, marker+"-target", "owner data")
	os.Symlink(marker+"-target", marker)
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi5g, nil); err == nil {
		t.Fatal("replaced symlink marker")
	}
	raw, _ := os.ReadFile(marker + "-target")
	if string(raw) != "owner data" {
		t.Fatal("overwrote symlink target")
	}
}

func TestWifiSecretWireHasOnlyConfidentialFields(t *testing.T) {
	raw, err := json.Marshal(connectConfidentialWifi{Band: "5g", SSID: "Fixture", Password: "fixture-key"})
	if err != nil || string(raw) != `{"band":"5g","ssid":"Fixture","password":"fixture-key"}` {
		t.Fatal("unexpected wire schema")
	}
}

func TestWifiSecretForgetClearsEligibilityOnFailureOrInterruption(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed", true: "interrupted"}[interrupted], func(t *testing.T) {
			w := wifiSecretFixture(t)
			if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi5g, nil); err != nil {
				t.Fatal(err)
			}
			if err := connectForgetWifiOwner(w.cfg); err != nil {
				t.Fatal(err)
			}
			if err := connectForgetWifiOwner(w.cfg); err != nil {
				t.Fatal("forget not idempotent")
			}
			// This models the dispatcher sequence: invalidate eligibility before
			// the action, and restore it only after verified owner-bound success.
			oldEnv := connectWifiEnv
			connectWifiEnv = func() setup.Env { return w.env }
			t.Cleanup(func() { connectWifiEnv = oldEnv })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if interrupted {
				cancel()
			} else {
				w.fail = func(cmd string) bool { return strings.HasPrefix(cmd, "uci set ") }
			}
			code, applied := connectApplyWifi(ctx, w.cfg, json.RawMessage(`{"ssid":"Fixture","password":"fixture-key"}`))
			if applied || (code != "interrupted" && code != "apply_failed") {
				t.Fatal("unexpected action result")
			}
			if got := connectReadOwnerWifi(w.cfg, wifiTestKey, "r-fixture", "o-fixture"); len(got) != 0 {
				t.Fatal("failed action retained old eligibility")
			}
		})
	}
}

func TestWifiSecretForgetRejectsOtherFilesWithoutDisclosure(t *testing.T) {
	for _, kind := range []string{"symlink", "directory", "permissions", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			w := wifiSecretFixture(t)
			path := w.cfg.StatePath + ".wifi-owner.json"
			switch kind {
			case "symlink":
				w.write(t, path+"-target", "fixture-key")
				os.Symlink(path+"-target", path)
			case "directory":
				os.Mkdir(path, 0700)
			case "permissions":
				w.write(t, path, "fixture-key")
				os.Chmod(path, 0644)
			case "invalid":
				w.write(t, path, "fixture-key")
				os.Chmod(path, 0600)
			}
			err := connectForgetWifiOwner(w.cfg)
			if err == nil || strings.Contains(err.Error(), "fixture-key") || strings.Contains(err.Error(), path) {
				t.Fatal("unsafe or disclosing invalidation")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatal("removed non-marker file")
			}
		})
	}
}

// A one-band change makes that band readable, plus only what the same owner's
// previous marker covered: never a band someone else set.
func TestWifiSecretMarkerScopedToBandAndOwnersOwn(t *testing.T) {
	w := wifiSecretFixture(t)
	both := strings.Replace(wizardWireless, "\toption disabled '1'\n", "", 1)
	both = strings.Replace(both, "option ssid 'OpenWrt'\n\toption encryption 'none'\n", "option ssid 'Home-2G'\n\toption encryption 'psk2'\n\toption key 'old-2g-key'\n", 1)
	w.write(t, w.env.WirelessConfig, both)
	bands := func(owner string) string {
		var out []string
		for _, c := range connectReadOwnerWifi(w.cfg, wifiTestKey, "r-fixture", owner) {
			out = append(out, c.Band)
		}
		return strings.Join(out, ",")
	}
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi5gOnly, nil); err != nil {
		t.Fatal(err)
	}
	if got := bands("o-fixture"); got != "5g" {
		t.Fatalf("5g change reads back %q", got)
	}
	kept := connectWifiOwnerAPs(w.cfg, "r-fixture", "o-fixture")
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi2gOnly, kept); err != nil {
		t.Fatal(err)
	}
	if got := bands("o-fixture"); got != "2g,5g" {
		t.Fatalf("same owner's two changes read back %q", got)
	}
	// Another owner's first one-band change inherits nothing.
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-other", wifi2gOnly, connectWifiOwnerAPs(w.cfg, "r-fixture", "o-other")); err != nil {
		t.Fatal(err)
	}
	if got := bands("o-other"); got != "2g" {
		t.Fatalf("new owner reads back %q", got)
	}
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-other", connectactions.WiFi{SSID: "Home-6G", Password: "fixture-6g-key", Band: "6g"}, nil); err == nil {
		t.Fatal("marked a band the router lacks")
	}
}

// wifiTestDeviceKey is a synthetic device key (fixed seed), base64 as state
// holds it.
func wifiTestDeviceKey() string {
	return base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize)))
}

// wifiBothBands is the fixture with both bands secured, each its own network.
func wifiBothBands(t *testing.T, w *wizardRouter) {
	t.Helper()
	both := strings.Replace(wizardWireless, "\toption disabled '1'\n", "", 1)
	both = strings.Replace(both, "option ssid 'OpenWrt'\n\toption encryption 'none'\n", "option ssid 'Home-2G'\n\toption encryption 'psk2'\n\toption key 'old-2g-key'\n", 1)
	w.write(t, w.env.WirelessConfig, both)
}

func wifiReadBands(w *wizardRouter, k []byte, owner string) string {
	var out []string
	for _, c := range connectReadOwnerWifi(w.cfg, k, "r-fixture", owner) {
		out = append(out, c.Band)
	}
	return strings.Join(out, ",")
}

func TestWifiReadbackKeyDerivesFromDeviceKeyOnly(t *testing.T) {
	k := connectWifiReadbackKey(wifiTestDeviceKey())
	if len(k) != 32 || bytes.Equal(k, connectWifiReadbackKey(base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, 32))))) {
		t.Fatal("readback key not bound to the device key")
	}
	if !bytes.Equal(k, connectWifiReadbackKey(wifiTestDeviceKey())) {
		t.Fatal("readback key not stable")
	}
	for _, bad := range []string{"", "not-base64!", base64.StdEncoding.EncodeToString([]byte("short-fixture"))} {
		if connectWifiReadbackKey(bad) != nil {
			t.Fatal("readback key from an invalid device key")
		}
	}
}

// A password set locally (vctl UI, LuCI, raw uci) after the owner's change is
// never read back to that owner; the marker holds neither value nor the key.
func TestWifiSecretLocalChangeAfterMarkNotReadBack(t *testing.T) {
	for _, change := range []struct{ name, old, new string }{
		{"key", "old-5g-key", "local-5g-key"},
		{"ssid", "'Home-5G'", "'Local-5G'"},
	} {
		t.Run(change.name, func(t *testing.T) {
			w := wifiSecretFixture(t)
			if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi5g, nil); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(w.cfg.StatePath + ".wifi-owner.json")
			if bytes.Contains(raw, []byte("old-5g-key")) || bytes.Contains(raw, []byte("Home-5G")) || bytes.Contains(raw, wifiTestKey) {
				t.Fatal("marker holds network data or key")
			}
			if wifiReadBands(w, wifiTestKey, "o-fixture") != "5g" {
				t.Fatal("owner's own change not read back")
			}
			w.write(t, w.env.WirelessConfig, strings.Replace(wizardWireless, change.old, change.new, 1))
			if got := connectReadOwnerWifi(w.cfg, wifiTestKey, "r-fixture", "o-fixture"); len(got) != 0 {
				t.Fatal("local change read back to the owner")
			}
			// Another readback key (a new device key) never matches either.
			w.write(t, w.env.WirelessConfig, wizardWireless)
			if got := connectReadOwnerWifi(w.cfg, bytes.Repeat([]byte{0x6b}, 32), "r-fixture", "o-fixture"); len(got) != 0 {
				t.Fatal("fingerprint matched under another key")
			}
		})
	}
}

// Markers written before fingerprints (or with a malformed one) are never
// readable and never carried forward as kept.
func TestWifiSecretLegacyMarkerNotReadBack(t *testing.T) {
	for _, fp := range []string{"", "zz", strings.Repeat("00", 32)} {
		w := wifiSecretFixture(t)
		wifiBothBands(t, w)
		marker := connectWifiOwnerMarker{RouterID: "r-fixture", OwnerRef: "o-fixture", APs: []connectWifiOwnedAP{
			{connectWifiAP{Radio: "radio0", Interface: "ap0"}, fp},
			{connectWifiAP{Radio: "radio1", Interface: "ap1"}, fp},
		}}
		raw, _ := json.Marshal(marker)
		if fp == "" && strings.Contains(string(raw), `"fp"`) {
			t.Fatal("legacy fixture not legacy")
		}
		w.write(t, w.cfg.StatePath+".wifi-owner.json", string(raw))
		os.Chmod(w.cfg.StatePath+".wifi-owner.json", 0600)
		if got := connectReadOwnerWifi(w.cfg, wifiTestKey, "r-fixture", "o-fixture"); len(got) != 0 {
			t.Fatalf("marker with fp %q read back", fp)
		}
		kept := connectWifiOwnerAPs(w.cfg, "r-fixture", "o-fixture")
		if len(kept) != 2 {
			t.Fatal("fixture marker unreadable")
		}
		if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi2gOnly, kept); err != nil {
			t.Fatal(err)
		}
		if got := wifiReadBands(w, wifiTestKey, "o-fixture"); got != "2g" {
			t.Fatalf("unverified kept entry carried forward: %q", got)
		}
	}
}

// A one-band change keeps the owner's other band only while it is unchanged.
func TestWifiSecretKeptOnlyWhileUnchanged(t *testing.T) {
	for _, changed := range []bool{false, true} {
		w := wifiSecretFixture(t)
		wifiBothBands(t, w)
		if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi5gOnly, nil); err != nil {
			t.Fatal(err)
		}
		kept := connectWifiOwnerAPs(w.cfg, "r-fixture", "o-fixture")
		if changed {
			raw, _ := os.ReadFile(w.env.WirelessConfig)
			w.write(t, w.env.WirelessConfig, strings.Replace(string(raw), "old-5g-key", "local-5g-key", 1))
		}
		if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi2gOnly, kept); err != nil {
			t.Fatal(err)
		}
		want := "2g,5g"
		if changed {
			want = "2g"
		}
		if got := wifiReadBands(w, wifiTestKey, "o-fixture"); got != want {
			t.Fatalf("changed=%v reads back %q", changed, got)
		}
		if n := len(connectWifiOwnerAPs(w.cfg, "r-fixture", "o-fixture")); n != len(strings.Split(want, ",")) {
			t.Fatalf("changed=%v marker holds %d entries", changed, n)
		}
	}
}

// A local change in the gap between the apply and the mark (the Wi-Fi lock is
// released in between) leaves that access point unmarked.
func TestWifiSecretMarkRequiresRequestedValues(t *testing.T) {
	w := wifiSecretFixture(t)
	raced := connectactions.WiFi{SSID: "Home-5G", Password: "app-5g-key"}
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", raced, nil); err == nil {
		t.Fatal("marked values the request did not set")
	}
	if _, err := os.Lstat(w.cfg.StatePath + ".wifi-owner.json"); err == nil {
		t.Fatal("marker written for raced values")
	}
	if got := connectReadOwnerWifi(w.cfg, wifiTestKey, "r-fixture", "o-fixture"); len(got) != 0 {
		t.Fatal("raced value read back")
	}
	// All bands requested, one changed locally meanwhile: only the other marked.
	wifiBothBands(t, w)
	raw, _ := os.ReadFile(w.env.WirelessConfig)
	same := strings.ReplaceAll(strings.ReplaceAll(string(raw), "Home-2G", "Home-5G"), "old-2g-key", "old-5g-key")
	w.write(t, w.env.WirelessConfig, strings.Replace(same, "'radio0'\n\toption mode 'ap'\n\toption ssid 'Home-5G'", "'radio0'\n\toption mode 'ap'\n\toption ssid 'Local-2G'", 1))
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi5g, nil); err != nil {
		t.Fatal(err)
	}
	if got := wifiReadBands(w, wifiTestKey, "o-fixture"); got != "5g" {
		t.Fatalf("raced band marked: %q", got)
	}
	// Re-marking only kept with nothing left to keep writes nothing.
	os.Remove(w.cfg.StatePath + ".wifi-owner.json")
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", connectactions.WiFi{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(w.cfg.StatePath + ".wifi-owner.json"); err == nil {
		t.Fatal("empty marker written")
	}
}

// The marker resolves a radio's band exactly as setup (and set_wifi) does,
// including an invalid band option falling back to hwmode.
func TestWifiSecretBandResolutionMatchesSetup(t *testing.T) {
	text := "config wifi-device 'r2G'\n\toption band '2G'\n\toption hwmode '11g'\n" +
		"config wifi-device 'rbad'\n\toption band 'bogus'\n" +
		"config wifi-device 'rlegacy'\n\toption hwmode '11a'\n" +
		"config wifi-device 'r6'\n\toption band '6g'\n\toption hwmode '11a'\n" +
		"config wifi-device 'r60'\n\toption band '60g'\n"
	f, err := uci.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"r2G": "2g", "rbad": "", "rlegacy": "5g", "r6": "6g", "r60": "60g"}
	for _, radio := range f.OfType("wifi-device") {
		if got, sb := connectWifiRadioBand(f, radio.Ref()), setup.RadioBand(radio); got != sb || got != want[radio.Name] {
			t.Errorf("%s: marker %q setup %q want %q", radio.Name, got, sb, want[radio.Name])
		}
	}
	// End to end: a 2g change on a radio with band '2G' + hwmode marks it.
	w := wifiSecretFixture(t)
	wifiBothBands(t, w)
	raw, _ := os.ReadFile(w.env.WirelessConfig)
	w.write(t, w.env.WirelessConfig, strings.Replace(string(raw), "option band '2g'", "option band '2G'\n\toption hwmode '11g'", 1))
	if err := connectMarkWifiOwner(w.cfg, wifiTestKey, "r-fixture", "o-fixture", wifi2gOnly, nil); err != nil {
		t.Fatal(err)
	}
	if got := wifiReadBands(w, wifiTestKey, "o-fixture"); got != "2g" {
		t.Fatalf("hwmode-fallback radio reads back %q", got)
	}
}

// A failed one-band set_wifi restores this owner's earlier marks that still
// match; one changed meanwhile stays dropped.
func TestConnectDispatchWiFiFailureKeepsValidMarks(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		ok         bool
		local      bool
		want       string
	}{
		{"rolled-back", "rolled_back", false, false, "2g,5g"},
		{"rolled-back-local-change", "rolled_back", false, true, "2g"},
		{"applied-but-unverifiable", "applied", true, false, "2g,5g"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := connectTestDaemon(t)
			d.st.DevicePrivateKey = wifiTestDeviceKey()
			w := wifiSecretFixture(t)
			wifiBothBands(t, w)
			oldGate, oldWifi, oldForget, oldMark, oldRead := connectResourceBlocked, connectWifiExecute, connectWifiForget, connectWifiMark, connectWifiRead
			t.Cleanup(func() {
				connectResourceBlocked, connectWifiExecute, connectWifiForget, connectWifiMark, connectWifiRead = oldGate, oldWifi, oldForget, oldMark, oldRead
			})
			connectResourceBlocked = func(*daemon, string) bool { return false }
			connectWifiForget, connectWifiMark, connectWifiRead = connectForgetWifiOwner, connectMarkWifiOwner, connectReadOwnerWifi
			b := d.connectBinding()
			k := connectWifiReadbackKey(d.st.DevicePrivateKey)
			if err := connectMarkWifiOwner(d.cfg, k, b.RouterID, b.OwnerRef, wifi5g, nil); err != nil {
				t.Fatal(err)
			}
			if err := connectMarkWifiOwner(d.cfg, k, b.RouterID, b.OwnerRef, wifi2gOnly, connectWifiOwnerAPs(d.cfg, b.RouterID, b.OwnerRef)); err != nil {
				t.Fatal(err)
			}
			connectWifiExecute = func(context.Context, agentcfg.Config, json.RawMessage) (string, bool) {
				if tc.local {
					raw, _ := os.ReadFile(w.env.WirelessConfig)
					w.write(t, w.env.WirelessConfig, strings.Replace(string(raw), "old-5g-key", "local-5g-key", 1))
				}
				// "applied", yet 2g does not hold the requested values: unmarkable.
				return tc.code, tc.ok
			}
			j := connectTestJob("wifi-"+tc.name, "set_wifi", map[string]interface{}{"ssid": "fake-2g", "password": "fake-fixture-password", "band": "2g"})
			if err := d.executeJob(context.Background(), j, controlplane.CheckInResponse{RouterID: d.st.RouterID}); err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, c := range connectReadOwnerWifi(d.cfg, k, b.RouterID, b.OwnerRef) {
				out = append(out, c.Band)
			}
			if got := strings.Join(out, ","); got != tc.want {
				t.Fatalf("after failure reads back %q, want %q", got, tc.want)
			}
		})
	}
}
