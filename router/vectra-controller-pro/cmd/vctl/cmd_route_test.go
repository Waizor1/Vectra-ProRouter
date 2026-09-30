package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/localctl"
)

// A native store as PassWall2's configuration has it: the owner's slots, each
// a shunt rule with its lists. YouTube's .ru site is not the main VPN's and
// stays where the provider sends it.
const routeStore = `
config global 'global'
	option enabled '1'

config shunt_rules 'WorldProxy'
	option remarks 'WorldProxy'
	option domain_list 'geosite:ANIME
domain:kinopoisk.ru
domain:example.com'
	option ip_list '5.255.255.5
1.1.1.1'

config shunt_rules 'Special'
	option remarks 'Special'
	option domain_list 'domain:pornhub.org
rutracker.ru'

config shunt_rules 'YouTube'
	option remarks 'YouTube'
	option domain_list 'domain:youtube.ru'
`

const routeStable = "🇷🇺🇪🇺 Авто Самый стабильный"

type routeFixture struct {
	cfg      agentcfg.Config
	uci      map[string]string
	sets     []string
	checked  [][]string // the proxy list each render check was made with
	checkErr error
}

func newRouteFixture(t *testing.T, ov *localctl.Overrides) *routeFixture {
	t.Helper()
	dir := t.TempDir()
	prevStore, prevGet, prevSet, prevCheck := nativeStorePath, routeUCIGet, routeUCISet, routeCheck
	t.Cleanup(func() { nativeStorePath, routeUCIGet, routeUCISet, routeCheck = prevStore, prevGet, prevSet, prevCheck })
	nativeStorePath = filepath.Join(dir, "vectra_route")
	if err := os.WriteFile(nativeStorePath, []byte(routeStore), 0o600); err != nil {
		t.Fatal(err)
	}
	geo := filepath.Join(dir, "geo")
	if err := os.MkdirAll(geo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(geo, "geoip.dat"), geoipDat(map[string][]string{"RU": {"5.255.255.0/24"}}), 0o644); err != nil {
		t.Fatal(err)
	}
	idx := localctl.EntriesIndex{Entries: []localctl.EntrySummary{{Index: 0, Remark: "🇪🇺 Авто Самый быстрый"}, {Index: 1, Remark: routeStable}}}
	raw, _ := json.Marshal(idx)
	index := filepath.Join(dir, "index.json")
	if err := os.WriteFile(index, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &routeFixture{
		cfg: agentcfg.Config{GeoAssetDir: geo, OverridesPath: filepath.Join(dir, "overrides.json"), EntriesIndexPath: index},
		uci: map[string]string{"vectra-controller-pro.main.route_source": "native"},
	}
	if ov != nil {
		if _, err := localctl.UpdateOverrides(f.cfg.OverridesPath, func(o *localctl.Overrides) error { *o = *ov; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	routeCheck = func(_ context.Context, _ agentcfg.Config, entryIndex int, ov localctl.Overrides) (string, error) {
		if entryIndex != 1 {
			t.Errorf("render check of entry #%d", entryIndex)
		}
		f.checked = append(f.checked, append([]string(nil), ov.Proxy...))
		return "the stub's render", f.checkErr
	}
	routeUCIGet = func(key string) string { return f.uci[key] }
	routeUCISet = func(key, value string) error {
		f.uci[key] = value
		f.sets = append(f.sets, key+"="+value)
		return nil
	}
	return f
}

func (f *routeFixture) run(t *testing.T, entry string, apply bool) (string, error) {
	t.Helper()
	var out strings.Builder
	err := routeProvider(&out, f.cfg, entry, apply)
	return out.String(), err
}

func TestRouteProviderDryRunSaysWhatItWouldCarryAndChangesNothing(t *testing.T) {
	f := newRouteFixture(t, nil)
	out, err := f.run(t, routeStable, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "carry 3 (2 domains, 1 address)") {
		t.Fatalf("output:\n%s", out)
	}
	if strings.Contains(out, "kinopoisk") || strings.Contains(out, "5.255.255.5") {
		t.Fatalf("a dry run names what it carries:\n%s", out)
	}
	if _, err := os.Stat(f.cfg.OverridesPath); !os.IsNotExist(err) {
		t.Fatalf("overrides written on a dry run: %v", err)
	}
	if len(f.sets) != 0 {
		t.Fatalf("UCI changed on a dry run: %v", f.sets)
	}
	// The render is checked as it will run: with the sites carried.
	if want := [][]string{{"kinopoisk.ru", "rutracker.ru", "5.255.255.5"}}; !reflect.DeepEqual(f.checked, want) || !strings.Contains(out, "the stub's render") {
		t.Fatalf("render checked with %v\n%s", f.checked, out)
	}
}

// A render xray refuses is no switch: nothing is written, the router keeps
// what runs.
func TestRouteProviderRefusesToSwitchToARenderXrayRefuses(t *testing.T) {
	f := newRouteFixture(t, nil)
	f.checkErr = errors.New("xray -test: failed to load geosite")
	out, err := f.run(t, routeStable, true)
	if err == nil || !strings.Contains(err.Error(), "render") {
		t.Fatalf("err %v\n%s", err, out)
	}
	if _, err := os.Stat(f.cfg.OverridesPath); !os.IsNotExist(err) || len(f.sets) != 0 {
		t.Fatalf("switched anyway: %v %v", err, f.sets)
	}
}

func TestRouteProviderApplyCarriesTheRussianSitesAndSwitches(t *testing.T) {
	f := newRouteFixture(t, &localctl.Overrides{Proxy: []string{"kinopoisk.ru"}})
	if _, err := f.run(t, routeStable, true); err != nil {
		t.Fatal(err)
	}
	ov, err := localctl.LoadOverrides(f.cfg.OverridesPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"kinopoisk.ru", "rutracker.ru", "5.255.255.5"}; !reflect.DeepEqual(ov.Proxy, want) {
		t.Fatalf("proxy %v, want %v", ov.Proxy, want)
	}
	if ov.EntryRemark != routeStable || ov.EntryIndex == nil || *ov.EntryIndex != 1 {
		t.Fatalf("entry %q %v", ov.EntryRemark, ov.EntryIndex)
	}
	if want := []string{"vectra-controller-pro.main.route_source=provider"}; !reflect.DeepEqual(f.sets, want) {
		t.Fatalf("uci %v", f.sets)
	}
}

// The owner's "always without the VPN" is theirs: a site already there is
// left there, and the move goes on.
func TestRouteProviderLeavesASiteTheOwnerSendsDirect(t *testing.T) {
	f := newRouteFixture(t, &localctl.Overrides{Direct: []string{"rutracker.ru"}})
	out, err := f.run(t, routeStable, true)
	if err != nil {
		t.Fatal(err)
	}
	ov, _ := localctl.LoadOverrides(f.cfg.OverridesPath)
	if want := []string{"kinopoisk.ru", "5.255.255.5"}; !reflect.DeepEqual(ov.Proxy, want) || !reflect.DeepEqual(ov.Direct, []string{"rutracker.ru"}) {
		t.Fatalf("proxy %v direct %v\n%s", ov.Proxy, ov.Direct, out)
	}
}

func TestRouteProviderRefusesAnEntryTheSubscriptionHasNot(t *testing.T) {
	f := newRouteFixture(t, nil)
	if _, err := f.run(t, "🇳🇱 Нидерланды", true); err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(f.cfg.OverridesPath); !os.IsNotExist(err) || len(f.sets) != 0 {
		t.Fatalf("changed something: %v %v", err, f.sets)
	}
}

// A native router may not have the provider's geo files yet: geoip:ru comes
// from the route policy's own.
func TestRouteProviderReadsGeoipFromTheRoutePolicysFilesWhenTheProvidersAreMissing(t *testing.T) {
	f := newRouteFixture(t, nil)
	prev := nativeGeoDir
	t.Cleanup(func() { nativeGeoDir = prev })
	nativeGeoDir = filepath.Join(t.TempDir(), "route-geo")
	if err := os.MkdirAll(nativeGeoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.cfg.GeoAssetDir, "geoip.dat"), filepath.Join(nativeGeoDir, "geoip.dat")); err != nil {
		t.Fatal(err)
	}
	out, err := f.run(t, routeStable, false)
	if err != nil || !strings.Contains(out, "carry 3 (2 domains, 1 address)") {
		t.Fatalf("err %v\n%s", err, out)
	}
}
