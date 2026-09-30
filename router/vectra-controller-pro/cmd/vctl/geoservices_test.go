package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/uiapi"
)

// writeGeosite writes a geosite.dat naming these categories (no domains:
// only the names are read).
func writeGeosite(t *testing.T, dir string, codes ...string) {
	t.Helper()
	pb := func(field int, v []byte) []byte {
		b := []byte{byte(field<<3 | 2)}
		n := uint64(len(v))
		for n >= 0x80 {
			b = append(b, byte(n)|0x80)
			n >>= 7
		}
		return append(append(b, byte(n)), v...)
	}
	var out []byte
	for _, c := range codes {
		out = append(out, pb(1, pb(1, []byte(c)))...)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"geosite.dat", "geoip.dat"} {
		if err := os.WriteFile(filepath.Join(dir, f), out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// «+ сервис» (spec decision 4): the router offers the categories of the geo
// file xray runs with — never "private", the LAN — and keeps a service only
// when that file has it: xray refuses a render naming a category it lacks.
func TestTheRouterOffersAndKeepsOnlyTheServicesItsGeoFileHas(t *testing.T) {
	s := newLockStand(t)
	geo := filepath.Join(filepath.Dir(s.cfg.StatePath), "geo")
	writeGeosite(t, geo, "DISCORD", "META", "PRIVATE", "CATEGORY-GOV-RU")
	s.cfg.GeoAssetDir = geo
	ctx := context.Background()

	r, ok := rpcdCall(ctx, s.cfg, "rules", nil).(uiapi.Rules)
	if !ok || !reflect.DeepEqual(r.Catalog, []string{"category-gov-ru", "discord", "meta"}) {
		t.Fatalf("rules %+v", r)
	}
	a, _ := rpcdCall(ctx, s.cfg, "set_rules", []byte(`{"direct":["geosite:Category-Gov-RU"],"proxy":["geosite:discord","example.org"]}`)).(uiapi.Action)
	if !a.OK || a.Code != "rules_set" {
		t.Fatalf("a known service: %+v", a)
	}
	reqs := s.daemonRequests()
	if n := len(reqs); n == 0 || reqs[n-1].Change == nil || reqs[n-1].Change.SetRules == nil ||
		!reflect.DeepEqual(reqs[n-1].Change.SetRules.Proxy, []string{"geosite:discord", "example.org"}) {
		t.Fatalf("the daemon was asked %+v", reqs)
	}
	for _, bad := range []string{`{"direct":[],"proxy":["geosite:nosuch"]}`, `{"direct":[],"proxy":["geosite:private"]}`} {
		a, _ := rpcdCall(ctx, s.cfg, "set_rules", []byte(bad)).(uiapi.Action)
		if a.OK || a.Code != "unknown_service" {
			t.Fatalf("%s: %+v", bad, a)
		}
	}
	if n := len(s.daemonRequests()); n != len(reqs) {
		t.Fatal("a refused service reached the daemon")
	}
	// No geo file to read: nothing is offered, and a service is not kept.
	s.cfg.GeoAssetDir = filepath.Join(geo, "none")
	if r, _ := rpcdCall(ctx, s.cfg, "rules", nil).(uiapi.Rules); len(r.Catalog) != 0 {
		t.Fatalf("catalog without a geo file: %v", r.Catalog)
	}
	b, _ := json.Marshal(uiapi.BuildRules(localctl.Overrides{}, nil))
	if string(b) != `{"direct":[],"proxy":[],"max":100,"catalog":[],"missing":[]}` {
		t.Fatalf("empty rules = %s", b)
	}
}

// A category the geo file lost since (an update) never reaches xray: the
// render drops it rather than fail and keep the router on its old one.
func TestARenderDropsAServiceTheGeoFileNoLongerHas(t *testing.T) {
	geo := filepath.Join(t.TempDir(), "geo")
	writeGeosite(t, geo, "DISCORD")
	d := &daemon{}
	d.cfg.GeoAssetDir = geo
	o := d.withKnownServices(xray.SpliceOptions{Rules: xray.UserRules{Direct: []string{"bank.example", "geosite:meta"}, Proxy: []string{"geosite:discord"}}})
	if !reflect.DeepEqual(o.Rules.Direct, []string{"bank.example"}) || !reflect.DeepEqual(o.Rules.Proxy, []string{"geosite:discord"}) {
		t.Fatalf("rules %+v", o.Rules)
	}
}

// Review of r27 (deferred minor, done): a service the geo file lost since
// (an update) is dropped from the render but stayed in the list as if it
// ran. The rules answer names it, so the list can say so.
func TestTheRulesAnswerNamesAServiceTheGeoFileLost(t *testing.T) {
	s := newLockStand(t)
	geo := filepath.Join(filepath.Dir(s.cfg.StatePath), "geo")
	writeGeosite(t, geo, "DISCORD")
	s.cfg.GeoAssetDir = geo
	_, _ = localctl.UpdateOverrides(s.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.Direct, o.Proxy = []string{"geosite:hdrezka", "bank.example"}, []string{"geosite:discord"}
		return nil
	})
	r, ok := rpcdCall(context.Background(), s.cfg, "rules", nil).(uiapi.Rules)
	if !ok || !reflect.DeepEqual(r.Missing, []string{"geosite:hdrezka"}) {
		t.Fatalf("rules %+v", r)
	}
	// No geo file to read: nothing is judged missing.
	s.cfg.GeoAssetDir = filepath.Join(geo, "none")
	if r, _ := rpcdCall(context.Background(), s.cfg, "rules", nil).(uiapi.Rules); len(r.Missing) != 0 {
		t.Fatalf("missing without a geo file: %v", r.Missing)
	}
}
