package passwall

import (
	"strings"
	"testing"
)

// The fleet's compact geo sources must be the itdoginfo/roscomvpn pair, never the
// ~10.5MB Loyalsoldier upstream that lacks the RUSSIA-OUTSIDE category the shunt
// references (which drops xray into "no proxy mode"). This locks the canonical
// values against an accidental regression.
func TestDefaultCompactGeoSourcesAreFleetCanonical(t *testing.T) {
	if got, want := DefaultCompactGeoSiteURL, "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat"; got != want {
		t.Fatalf("geosite default drifted: got %q want %q", got, want)
	}
	if got, want := DefaultCompactGeoIPURL, "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat"; got != want {
		t.Fatalf("geoip default drifted: got %q want %q", got, want)
	}
	for _, u := range []string{DefaultCompactGeoSiteURL, DefaultCompactGeoIPURL} {
		if strings.Contains(u, "Loyalsoldier") {
			t.Fatalf("compact geo source must not be the Loyalsoldier upstream: %s", u)
		}
	}
}

// A fresh unit whose live UCI carries no geo URLs must fall back to the compact
// fleet sources, because apply.go writes RuleManage.Geo*URL back into UCI and the
// daily rule_update cron then fetches whatever URL is stored. Falling back to the
// Loyalsoldier upstream here is exactly what bricked a customer's VPN on arrival.
func TestImportDesiredConfigFallsBackToCompactGeoSources(t *testing.T) {
	config := importDesiredConfig(nil)
	if got := config.RuleManage.GeoSiteURL; got != DefaultCompactGeoSiteURL {
		t.Fatalf("geosite fallback = %q, want %q", got, DefaultCompactGeoSiteURL)
	}
	if got := config.RuleManage.GeoIPURL; got != DefaultCompactGeoIPURL {
		t.Fatalf("geoip fallback = %q, want %q", got, DefaultCompactGeoIPURL)
	}
}

// A router that already carries explicit geo URLs in its global_rules UCI must
// keep them — the compact defaults are only a fallback, never an override.
func TestImportDesiredConfigKeepsExplicitGeoURLs(t *testing.T) {
	sections := []UCISection{
		{
			Type: "global_rules",
			Name: "myrules",
			Options: map[string][]string{
				"geoip_url":   {"https://example.com/custom-geoip.dat"},
				"geosite_url": {"https://example.com/custom-geosite.dat"},
			},
		},
	}
	config := importDesiredConfig(sections)
	if got := config.RuleManage.GeoSiteURL; got != "https://example.com/custom-geosite.dat" {
		t.Fatalf("explicit geosite overridden: got %q", got)
	}
	if got := config.RuleManage.GeoIPURL; got != "https://example.com/custom-geoip.dat" {
		t.Fatalf("explicit geoip overridden: got %q", got)
	}
}
