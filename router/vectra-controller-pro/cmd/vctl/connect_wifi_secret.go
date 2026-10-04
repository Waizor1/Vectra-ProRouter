package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/connectactions"
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

// connectWifiOwnedAP is a marked access point plus FP, the fingerprint of the
// SSID and key this owner set on it (connectWifiFingerprint, hex). A marker
// entry without one (written before fingerprints existed) is never readable.
type connectWifiOwnedAP struct {
	connectWifiAP
	FP string `json:"fp,omitempty"`
}
type connectWifiOwnerMarker struct {
	RouterID string               `json:"routerId"`
	OwnerRef string               `json:"ownerRef"`
	APs      []connectWifiOwnedAP `json:"aps"`
}

var errConnectWifiSecret = errors.New("owner-bound Wi-Fi readback is unavailable")

// connectWifiReadbackKey is the marker fingerprint key:
// HMAC-SHA256(device Ed25519 seed, "vctl-wifi-readback-v1"); nil without a
// device key. The seed is router-local: only its public half ever leaves the
// router, and it is stored in the vault-sealed state file (whose key lives
// outside the state directory), never next to the marker in usable form. The
// distinct label separates this use from the claim derivations of the same
// seed. A new device key leaves every existing marker unreadable (fail safe).
func connectWifiReadbackKey(devicePrivateKey string) []byte {
	device, err := claim.DeviceKey(devicePrivateKey)
	if err != nil {
		return nil
	}
	m := hmac.New(sha256.New, device.Seed())
	m.Write([]byte("vctl-wifi-readback-v1"))
	return m.Sum(nil)
}

// connectWifiFingerprint is HMAC-SHA256(k, ssid || 0x00 || key). It and k
// must never be logged or leave the router.
func connectWifiFingerprint(k []byte, ssid, key string) []byte {
	m := hmac.New(sha256.New, k)
	m.Write([]byte(ssid))
	m.Write([]byte{0})
	m.Write([]byte(key))
	return m.Sum(nil)
}

// connectWifiFingerprintMatches reports whether an access point's current
// ssid and key are exactly what a marker entry's fingerprint recorded.
func connectWifiFingerprintMatches(k []byte, fp, ssid, key string) bool {
	want, err := hex.DecodeString(fp)
	if err != nil || len(want) != sha256.Size || len(k) != sha256.Size {
		return false
	}
	return hmac.Equal(connectWifiFingerprint(k, ssid, key), want)
}

// The caller must pass the current adopted binding and k, and call this with
// the requested set_wifi only after connectApplyWifi returned applied=true;
// runtime verification is also checked. want.Band "" targets every access
// point, otherwise only that band's; a target is marked only while it holds
// exactly want's SSID and password (a local change since the apply is not this
// owner's). Any other access point stays marked only if kept — this same
// owner's previous marker — has its fingerprint and it still matches. A want
// without an SSID re-marks only kept (nil when none still matches).
func connectMarkWifiOwner(cfg agentcfg.Config, k []byte, routerID, ownerRef string, want connectactions.WiFi, kept []connectWifiOwnedAP) error {
	if cfg.StatePath == "" || routerID == "" || ownerRef == "" || len(routerID) > 256 || len(ownerRef) > 256 || len(k) != sha256.Size {
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
	var requested []byte
	if want.SSID != "" {
		requested = connectWifiFingerprint(k, want.SSID, want.Password)
	}
	targets := 0
	for _, ap := range connectWifiFirstAPs(f) {
		iface := connectWifiSection(f, "wifi-iface", ap.Interface)
		if iface == nil {
			continue
		}
		fp := connectWifiFingerprint(k, iface.Get("ssid"), iface.Get("key"))
		if requested != nil && (want.Band == "" || connectWifiRadioBand(f, ap.Radio) == want.Band) {
			if hmac.Equal(fp, requested) {
				marker.APs = append(marker.APs, connectWifiOwnedAP{ap, hex.EncodeToString(fp)})
				targets++
			}
			continue
		}
		for _, prev := range kept {
			if prev.connectWifiAP == ap && connectWifiFingerprintMatches(k, prev.FP, iface.Get("ssid"), iface.Get("key")) {
				marker.APs = append(marker.APs, connectWifiOwnedAP{ap, prev.FP})
				break
			}
		}
	}
	if requested == nil && len(marker.APs) == 0 {
		return nil // nothing of this owner's is left to restore
	}
	if (requested != nil && targets == 0) || len(marker.APs) == 0 || len(marker.APs) > 16 {
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
// An access point is read back only while it holds exactly the SSID and key
// this owner set (the marker fingerprint, under k): a local change since —
// vctl UI, LuCI, raw uci — or an entry without a fingerprint is skipped.
func connectReadOwnerWifi(cfg agentcfg.Config, k []byte, routerID, ownerRef string) []connectConfidentialWifi {
	if cfg.StatePath == "" || routerID == "" || ownerRef == "" || len(k) != sha256.Size {
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
			if have == want.connectWifiAP {
				matched = true
				break
			}
		}
		if !matched {
			return nil
		}
		radio, ap := connectWifiSection(f, "wifi-device", want.Radio), connectWifiSection(f, "wifi-iface", want.Interface)
		if radio == nil || ap == nil {
			return nil
		}
		if !connectWifiFingerprintMatches(k, want.FP, ap.Get("ssid"), ap.Get("key")) {
			continue
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
func connectWifiOwnerAPs(cfg agentcfg.Config, routerID, ownerRef string) []connectWifiOwnedAP {
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

// connectWifiRadioBand is a wifi-device's band as setup resolves it (the
// same band set_wifi targets); "" when unknown.
func connectWifiRadioBand(f *uci.File, ref string) string {
	if radio := connectWifiSection(f, "wifi-device", ref); radio != nil {
		return setup.RadioBand(*radio)
	}
	return ""
}

// connectWifiSection is the last section of type typ named ref; nil if none.
func connectWifiSection(f *uci.File, typ, ref string) *uci.Section {
	var out *uci.Section
	for i := range f.Sections {
		if s := &f.Sections[i]; s.Type == typ && s.Ref() == ref {
			out = s
		}
	}
	return out
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
