// Package uaguard refuses subscription User-Agents that get a customer's
// device deleted.
//
// The provider's panel records the last User-Agent seen per HWID, and an hourly
// fake-client sweep treats any "Happ…" agent that is not the real client's
// "Happ/<version>/<OS>/<build>" as malformed: the device is deleted on the
// FIRST strike (paid users included), the panel account is disabled and the
// customer is emailed "device disconnected". "Happ/1.0" — the value this
// codebase used to document as the way to get the JSON variant — is exactly
// such an agent. It happened to two customers on 2026-09-21.
//
// The check lives in its own leaf package so the operator-config validator and
// the fetcher enforce the same rule: a config carrying a malformed Happ agent
// is refused when it is adopted, and a fetch that would send one is refused
// before a single byte leaves the router.
package uaguard

import (
	"fmt"
	"regexp"
	"strings"
)

// wellFormedHapp is the shape the real client sends, e.g.
// "Happ/4.2.1/Windows/2609041405606": a dotted numeric version, an OS name, a
// numeric build of at least six digits. Stricter than "four slash-separated
// parts" on purpose: "Happ/1.0/OpenWrt/1" has four parts and is still nothing
// the real client ever sent, and the sweep's own pattern is not ours to see.
var wellFormedHapp = regexp.MustCompile(`^Happ/\d+(\.\d+){1,3}/[A-Za-z][A-Za-z0-9]*/\d{6,}$`)

// Check returns an error when ua would be flagged by the provider's sweep.
// Anything that does not claim to be Happ is not this guard's business.
func Check(ua string) error {
	trimmed := strings.TrimSpace(ua)
	if !strings.HasPrefix(strings.ToLower(trimmed), "happ") {
		return nil
	}
	if trimmed == ua && wellFormedHapp.MatchString(ua) {
		return nil
	}
	return fmt.Errorf("user-agent %q claims to be Happ but is not the real client's "+
		"\"Happ/<version>/<OS>/<build>\"; the provider's anti-fraud sweep deletes the device "+
		"and disables the customer's account on the first such request — refusing to send it", ua)
}

// routerProduct starts the router's own agent, VectraRouter/<version>
// vr1.<token> (internal/uatoken). Only the router makes it, afresh for every
// request; written into a config or a header it is a copy, and a copy is what
// a forger sends.
const routerProduct = "vectrarouter/"

// CheckConfigured is Check for an agent a config states: it also refuses the
// router's own agent written down. The router sends its own when userAgent is
// left empty.
func CheckConfigured(ua string) error {
	if err := Check(ua); err != nil {
		return err
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ua)), routerProduct) {
		return fmt.Errorf("user-agent %q copies the router's own, which the router makes itself for every request "+
			"(signed, with a token only it can make); leave userAgent empty instead", ua)
	}
	return nil
}

// FromHeaders returns the User-Agent a header map would set, matching the key
// case-insensitively the way net/http canonicalizes it. ok is false when the
// map sets none.
func FromHeaders(h map[string]string) (ua string, ok bool) {
	for k, v := range h {
		if strings.EqualFold(k, "User-Agent") {
			return v, true
		}
	}
	return "", false
}
