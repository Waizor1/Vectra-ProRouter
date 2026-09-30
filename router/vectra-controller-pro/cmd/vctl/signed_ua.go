package main

import (
	"time"

	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/routersign"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uaguard"
)

// A subscription that names no User-Agent is fetched with the router's own,
// VectraRouter/<version> vr1.<token> (internal/uatoken): made afresh for every
// request, signed with the device key and sealed to Vectra's key, so no other
// software can pass for a Vectra router.

// uaSigner makes the router's own User-Agent for one request from st
// (internal/routersign, which the reporter signs with too).
func uaSigner(st state.PersistedState, version string, now func() time.Time) func(hwid, url string) (string, error) {
	return routersign.Signer(st, version, now)
}

// uaKey is Vectra's key the agent is sealed to (routersign.Key).
func uaKey(st state.PersistedState) (claim.Key, error) { return routersign.Key(st) }

// ownAgent reports whether sub leaves the User-Agent to the router: it names
// none, not even through its headers.
func ownAgent(sub config.Subscription) bool {
	if sub.UserAgent != "" {
		return false
	}
	_, set := uaguard.FromHeaders(sub.Headers)
	return !set
}
