package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/uci"
)

var connectWifiSecretEnv = setup.RouterEnv
var connectWifiSecretReadFile = os.ReadFile

// This payload belongs only to the authenticated, owner-bound check-in clone.
// It must never enter generic diagnostics, action results, or logs.
type connectConfidentialWifi struct {
	Band     string `json:"band"`
	SSID     string `json:"ssid"`
	Password string `json:"password"`
}

type connectWifiAP struct {
	Radio     string `json:"radio"`
	Interface string `json:"interface"`
}
type connectWifiOwnerMarker struct {
	RouterID string          `json:"routerId"`
	OwnerRef string          `json:"ownerRef"`
	APs      []connectWifiAP `json:"aps"`
}

var errConnectWifiSecret = errors.New("owner-bound Wi-Fi readback is unavailable")

// The caller must pass the current adopted binding, and call this only after
// connectApplyWifi returned applied=true. Runtime verification is also checked.
// band "" marks every access point; otherwise only that band's, plus kept: the
// access points this same owner's previous marker already covered.
func connectMarkWifiOwner(cfg agentcfg.Config, routerID, ownerRef, band string, kept []connectWifiAP) error {
	if cfg.StatePath == "" || routerID == "" || ownerRef == "" || len(routerID) > 256 || len(ownerRef) > 256 {
		return errConnectWifiSecret
	}
	env := connectWifiSecretEnv()
	lock, err := setup.LockWifi(env)
	if err != nil {
		return errConnectWifiSecret
	}
	defer lock.Close()
	if !connectWifiSecretStateSafe(env, false) {
		return errConnectWifiSecret
	}
	path := cfg.StatePath + ".wifi-owner.json"
	if st, err := os.Lstat(path); err == nil {
		if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
			return errConnectWifiSecret
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errConnectWifiSecret
	}
	f := connectWifiSecretConfig(env)
	if f == nil {
		return errConnectWifiSecret
	}
	marker := connectWifiOwnerMarker{RouterID: routerID, OwnerRef: ownerRef}
	for _, ap := range connectWifiFirstAPs(f) {
		if band == "" || connectWifiRadioBand(f, ap.Radio) == band || slices.Contains(kept, ap) {
			marker.APs = append(marker.APs, ap)
		}
	}
	if len(marker.APs) == 0 || len(marker.APs) > 16 {
		return errConnectWifiSecret
	}
	raw, err := json.Marshal(marker)
	if err != nil {
		return errConnectWifiSecret
	}
	if localctl.WriteFileAtomic(path, raw, 0600) != nil {
		return errConnectWifiSecret
	}
	return nil
}

// No wireless file is read without a private matching ownership marker.
// The caller revalidates adoption and ownerRef before sending these records.
func connectReadOwnerWifi(cfg agentcfg.Config, routerID, ownerRef string) []connectConfidentialWifi {
	if cfg.StatePath == "" || routerID == "" || ownerRef == "" {
		return nil
	}
	aps := connectWifiOwnerAPs(cfg, routerID, ownerRef)
	if len(aps) == 0 {
		return nil
	}
	env := connectWifiSecretEnv()
	lock, err := setup.LockWifi(env)
	if err != nil {
		return nil
	}
	defer lock.Close()
	if !connectWifiSecretStateSafe(env, true) {
		return nil
	}
	f := connectWifiSecretConfig(env)
	if f == nil {
		return nil
	}
	current := connectWifiFirstAPs(f)
	var out []connectConfidentialWifi
	for _, want := range aps {
		matched := false
		for _, have := range current {
			if have == want {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
		var radio, ap *uci.Section
		for i := range f.Sections {
			s := &f.Sections[i]
			if s.Type == "wifi-device" && s.Ref() == want.Radio {
				radio = s
			}
			if s.Type == "wifi-iface" && s.Ref() == want.Interface {
				ap = s
			}
		}
		if radio == nil || ap == nil {
			return nil
		}
		if radio.Get("disabled") == "1" || radio.Get("disabled") == "true" || ap.Get("disabled") == "1" || ap.Get("disabled") == "true" {
			continue
		}
		enc, _, _ := strings.Cut(strings.ToLower(ap.Get("encryption")), "+")
		switch enc {
		case "psk", "psk2", "psk-mixed", "sae", "sae-mixed":
		default:
			continue
		}
		ssid, password := ap.Get("ssid"), ap.Get("key")
		if !connectWifiParamsValid(ssid, password) {
			return nil
		}
		band := connectWifiRadioBand(f, want.Radio)
		if band == "" {
			return nil
		}
		out = append(out, connectConfidentialWifi{Band: band, SSID: ssid, Password: password})
	}
	if !connectWifiSecretStateSafe(env, true) {
		return nil
	}
	return out
}

// connectWifiOwnerAPs is the access points a private marker for exactly this
// binding covers; nil without one. It reads no wireless configuration.
func connectWifiOwnerAPs(cfg agentcfg.Config, routerID, ownerRef string) []connectWifiAP {
	if cfg.StatePath == "" || routerID == "" || ownerRef == "" {
		return nil
	}
	path := cfg.StatePath + ".wifi-owner.json"
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 8192 {
		return nil
	}
	raw, err := connectWifiSecretReadFile(path)
	var marker connectWifiOwnerMarker
	if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &marker) != nil || marker.RouterID != routerID || marker.OwnerRef != ownerRef || len(marker.APs) == 0 || len(marker.APs) > 16 {
		return nil
	}
	return marker.APs
}

// connectWifiRadioBand is a wifi-device's band (2g/5g/6g/60g), from its band
// or legacy hwmode option; "" when unknown.
func connectWifiRadioBand(f *uci.File, ref string) string {
	for _, radio := range f.OfType("wifi-device") {
		if radio.Ref() != ref {
			continue
		}
		band := radio.Get("band")
		if band == "" {
			switch radio.Get("hwmode") {
			case "11b", "11g":
				band = "2g"
			case "11a":
				band = "5g"
			}
		}
		switch band {
		case "2g", "5g", "6g", "60g":
			return band
		}
		return ""
	}
	return ""
}

func connectWifiSecretFirstMissing(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

func connectWifiSecretStateSafe(env setup.Env, allowRestart bool) bool {
	if !connectWifiSecretFirstMissing(env.WifiJob) {
		return false
	}
	if st, err := os.Lstat(filepath.Join(env.UCISaveDir, "wireless")); err == nil {
		if !st.Mode().IsRegular() || st.Size() > 0 {
			return false
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}
	raw, err := connectWifiSecretReadFile(env.WifiApply)
	if errors.Is(err, os.ErrNotExist) {
		return allowRestart
	}
	var state setup.ApplyState
	return err == nil && len(raw) < 8192 && json.Unmarshal(raw, &state) == nil && state.State == setup.ApplyOK
}

func connectWifiSecretConfig(env setup.Env) *uci.File {
	st, err := os.Lstat(env.WirelessConfig)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil
	}
	raw, err := connectWifiSecretReadFile(env.WirelessConfig)
	if err != nil || len(raw) > 1<<20 {
		return nil
	}
	f, err := uci.Parse(string(raw))
	if err != nil {
		return nil
	}
	return f
}

func connectWifiFirstAPs(f *uci.File) []connectWifiAP {
	var out []connectWifiAP
	for _, radio := range f.OfType("wifi-device") {
		if radio.Name == "" {
			continue
		}
		for _, ap := range f.OfType("wifi-iface") {
			if ap.Get("device") == radio.Name && ap.Get("mode") == "ap" {
				out = append(out, connectWifiAP{Radio: radio.Ref(), Interface: ap.Ref()})
				break
			}
		}
	}
	return out
}

// connectForgetWifiOwner invalidates eligibility before an owner-bound Wi-Fi
// action or a binding transition. The caller must abort the mutation if this
// fails, and recreate eligibility only after verified success for that owner.
func connectForgetWifiOwner(cfg agentcfg.Config) error {
	if cfg.StatePath == "" {
		return errConnectWifiSecret
	}
	path := cfg.StatePath + ".wifi-owner.json"
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	env := connectWifiSecretEnv()
	lock, err := setup.LockWifi(env)
	if err != nil {
		return errConnectWifiSecret
	}
	defer lock.Close()
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 8192 {
		return errConnectWifiSecret
	}
	raw, err := connectWifiSecretReadFile(path)
	var marker connectWifiOwnerMarker
	if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &marker) != nil || marker.RouterID == "" || marker.OwnerRef == "" || len(marker.APs) == 0 || len(marker.APs) > 16 {
		return errConnectWifiSecret
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errConnectWifiSecret
	}
	// The marker is durable; make its invalidation survive restart too.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return errConnectWifiSecret
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return errConnectWifiSecret
	}
	return nil
}
