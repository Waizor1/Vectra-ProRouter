package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/config"
)

// The panel and this decoder are the two halves of one contract, written in two
// languages, and nothing used to check them against each other.
//
// That gap shipped a real bug: the pre-pivot panel schema modelled dns/nodes/
// routing with zod .default(), so it emitted those keys unconditionally, while
// Load() runs with DisallowUnknownFields. Every xray apply job would have died
// at decode with `unknown field "dns"`. The Go e2e test did not catch it because
// it feeds a Go-authored config; the panel test did not catch it because it only
// checked its own zod schema. Neither side was wrong on its own terms.
//
// The files under testdata/panel are emitted BY THE PANEL from
// apps/web/src/server/vectra/xray-operator-config.ts. On this side we prove the
// decoder accepts them. On the panel side, xray-operator-config.test.ts asserts
// the builder still produces these exact bytes — so a drift in either half
// fails a test instead of a router.
//
// To regenerate after an intentional panel change, see
// testdata/panel/README.md.
func TestPanelAuthoredConfigDecodes(t *testing.T) {
	for _, name := range []string{
		"operator-config.json",
		"operator-config-profile.json",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("testdata", "panel", name)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if len(raw) == 0 {
				t.Fatal("golden is empty; regenerate it from the panel")
			}

			cfg, err := config.Load(path)
			if err != nil {
				t.Fatalf("panel-authored config rejected by the decoder: %v", err)
			}
			if err := config.Validate(cfg); err != nil {
				t.Fatalf("panel-authored config failed validation: %v", err)
			}

			if cfg.Schema != config.SchemaVersion {
				t.Errorf("schema = %d, want %d", cfg.Schema, config.SchemaVersion)
			}
			if cfg.Inbounds.Tproxy == nil {
				t.Fatal("panel must emit inbounds.tproxy; without it there is no transparent proxy")
			}
			// The fleet runs its geo assets here, and Xray only finds them
			// because XRAY_LOCATION_ASSET points at this directory.
			if got, want := cfg.Geo.AssetDir, "/usr/share/v2ray"; got != want {
				t.Errorf("geo.assetDir = %q, want %q", got, want)
			}
			if len(cfg.Subscriptions) == 0 {
				t.Fatal("panel must emit a subscription; the router fetches the provider document itself")
			}
		})
	}
}

// A config carrying any pre-pivot key must be refused rather than silently
// ignored — this is the exact shape that used to reach the router.
func TestPrePivotKeysAreRefused(t *testing.T) {
	for _, key := range []string{"dns", "nodes", "routing", "normalization"} {
		t.Run(key, func(t *testing.T) {
			raw := []byte(`{"schema":1,"` + key + `":{}}`)
			path := filepath.Join(t.TempDir(), "cfg.json")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(path); err == nil {
				t.Fatalf("key %q was accepted; DisallowUnknownFields must reject it", key)
			}
		})
	}
}
