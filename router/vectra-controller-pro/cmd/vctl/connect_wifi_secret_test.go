package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/setup"
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
	if got := connectReadOwnerWifi(w.cfg, "router-fixture", "owner-fixture"); len(got) != 0 || reads != 0 {
		t.Fatal("read preexisting key without marker")
	}
	if err := connectMarkWifiOwner(w.cfg, "router-fixture", "owner-fixture", "", nil); err != nil {
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
	got := connectReadOwnerWifi(w.cfg, "router-fixture", "owner-fixture")
	if len(got) != 1 || got[0].Band != "5g" || got[0].SSID != "Home-5G" || got[0].Password != "old-5g-key" {
		t.Fatal("incorrect fixture readback")
	}
	// Marker is durable: no runtime apply file after restart.
	os.Remove(w.env.WifiApply)
	if len(connectReadOwnerWifi(w.cfg, "router-fixture", "owner-fixture")) != 1 {
		t.Fatal("marker did not survive restart")
	}
	reads = 0
	if len(connectReadOwnerWifi(w.cfg, "router-fixture", "other-owner")) != 0 || reads != 0 {
		t.Fatal("old binding exposed credential")
	}
	if len(connectReadOwnerWifi(w.cfg, "other-router", "owner-fixture")) != 0 {
		t.Fatal("wrong router exposed credential")
	}
}

func TestWifiSecretRefusesUnsafeState(t *testing.T) {
	for _, kind := range []string{"pending", "job", "applying", "failed", "permissions", "symlink", "changed-ap", "invalid-key"} {
		t.Run(kind, func(t *testing.T) {
			w := wifiSecretFixture(t)
			if err := connectMarkWifiOwner(w.cfg, "r-fixture", "o-fixture", "", nil); err != nil {
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
			if len(connectReadOwnerWifi(w.cfg, "r-fixture", "o-fixture")) != 0 {
				t.Fatal("unsafe state exposed key")
			}
		})
	}
}

func TestWifiSecretMarkerRefusesUnverifiedAndSymlink(t *testing.T) {
	w := wifiSecretFixture(t)
	w.write(t, w.env.WifiApply, `{"state":"unverified"}`)
	if err := connectMarkWifiOwner(w.cfg, "r-fixture", "o-fixture", "", nil); err == nil {
		t.Fatal("marked unverified config")
	}
	w.write(t, w.env.WifiApply, lastApply)
	marker := w.cfg.StatePath + ".wifi-owner.json"
	w.write(t, marker+"-target", "owner data")
	os.Symlink(marker+"-target", marker)
	if err := connectMarkWifiOwner(w.cfg, "r-fixture", "o-fixture", "", nil); err == nil {
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
			if err := connectMarkWifiOwner(w.cfg, "r-fixture", "o-fixture", "", nil); err != nil {
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
			if got := connectReadOwnerWifi(w.cfg, "r-fixture", "o-fixture"); len(got) != 0 {
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
		for _, c := range connectReadOwnerWifi(w.cfg, "r-fixture", owner) {
			out = append(out, c.Band)
		}
		return strings.Join(out, ",")
	}
	if err := connectMarkWifiOwner(w.cfg, "r-fixture", "o-fixture", "5g", nil); err != nil {
		t.Fatal(err)
	}
	if got := bands("o-fixture"); got != "5g" {
		t.Fatalf("5g change reads back %q", got)
	}
	kept := connectWifiOwnerAPs(w.cfg, "r-fixture", "o-fixture")
	if err := connectMarkWifiOwner(w.cfg, "r-fixture", "o-fixture", "2g", kept); err != nil {
		t.Fatal(err)
	}
	if got := bands("o-fixture"); got != "2g,5g" {
		t.Fatalf("same owner's two changes read back %q", got)
	}
	// Another owner's first one-band change inherits nothing.
	if err := connectMarkWifiOwner(w.cfg, "r-fixture", "o-other", "2g", connectWifiOwnerAPs(w.cfg, "r-fixture", "o-other")); err != nil {
		t.Fatal(err)
	}
	if got := bands("o-other"); got != "2g" {
		t.Fatalf("new owner reads back %q", got)
	}
	if err := connectMarkWifiOwner(w.cfg, "r-fixture", "o-other", "6g", nil); err == nil {
		t.Fatal("marked a band the router lacks")
	}
}
