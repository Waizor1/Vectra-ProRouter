// Package routersign makes the router's own User-Agent,
// VectraRouter/<version> vr1.<token> (internal/uatoken), from what vctl keeps
// in state.json: the device id and key, and Vectra's key — the one the panel
// named in check-in (claimKey), else the built-in one. vctl signs its
// subscription fetches with it; the reporter (vectra-reporter) its reports.
package routersign

import (
	"crypto/rand"
	"fmt"
	"time"

	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uatoken"
)

// Signer makes the User-Agent for one request from st. Without a device key,
// or with a clock that is not set yet, it fails, and the request then
// carries none.
func Signer(st state.PersistedState, version string, now func() time.Time) func(hwid, rawURL string) (string, error) {
	return func(hwid, rawURL string) (string, error) {
		dev, err := claim.DeviceKey(st.DevicePrivateKey)
		if err != nil {
			return "", fmt.Errorf("no device key in state.json: %w", err)
		}
		key, err := Key(st)
		if err != nil {
			return "", err
		}
		return uatoken.UserAgent(uatoken.Request{
			Version:   version,
			DeviceID:  st.DeviceIdentifier,
			DeviceKey: dev,
			HWID:      hwid,
			URL:       rawURL,
			Kid:       key.Kid,
			ServerPub: key.PublicKey,
			Now:       now(),
		}, rand.Reader)
	}
}

// Key is Vectra's key a token is sealed to: the one the panel named in
// check-in (claimKey), else the built-in one.
func Key(st state.PersistedState) (claim.Key, error) {
	if k := st.ClaimKey; k != nil {
		if key, err := claim.ParseKey(k.Kid, k.PublicKey); err == nil {
			return key, nil
		}
	}
	return claim.ParseKey(uatoken.BuiltinKid, uatoken.BuiltinKey)
}
