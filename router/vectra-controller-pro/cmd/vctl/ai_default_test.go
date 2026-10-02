package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/uiapi"
	"vectra-controller-pro/internal/vault"
)

func aiTestCache(t *testing.T, remarks ...string) (*daemon, *localctl.EntriesCache) {
	t.Helper()
	docs := make([]string, len(remarks))
	for i := range remarks {
		docs[i] = `{"outbounds":[{"tag":"n` + string(rune('a'+i)) + `","protocol":"vless"}]}`
	}
	return aiTestCacheOf(t, remarks, docs)
}

func aiTestCacheOf(t *testing.T, remarks, docs []string) (*daemon, *localctl.EntriesCache) {
	t.Helper()
	dir := t.TempDir()
	cfg := agentcfg.Config{StatePath: filepath.Join(dir, "state.json")}
	cfg.EntriesPath = filepath.Join(dir, "provider-entries.json.gz")
	cfg.EntriesIndexPath = filepath.Join(dir, "provider-entries.index.json")
	c := &localctl.EntriesCache{}
	for i, r := range remarks {
		c.Remarks = append(c.Remarks, r)
		c.Entries = append(c.Entries, json.RawMessage(docs[i]))
	}
	if _, err := localctl.SaveEntries(cfg.EntriesPath, cfg.EntriesIndexPath, c); err != nil {
		t.Fatal(err)
	}
	return &daemon{cfg: cfg}, c
}

// «Нейросети» go through Kazakhstan unless the owner chose otherwise: the
// provider's RU→KZ cascade first, its KZ location else, nothing without one.
// The last flag is the exit: «🇰🇿🇷🇺» leaves in Russia.
func TestTheAIServiceDefaultsToTheKazakhCascade(t *testing.T) {
	for name, tc := range map[string]struct {
		remarks []string
		want    int
	}{
		"cascade first":       {[]string{"🇷🇺🇪🇺 Авто", "🇰🇿 Казахстан", "🇷🇺🇰🇿 Казахстан", "🇰🇿 Gemini · Google"}, 2},
		"plain cascade first": {[]string{"🇷🇺🇰🇿 Gemini · Google", "🇷🇺🇰🇿 Казахстан"}, 1},
		"KZ without one":      {[]string{"🇷🇺🇪🇺 Авто", "🇰🇿 Gemini · Google", "🇰🇿 Казахстан"}, 2},
		"exit in Russia":      {[]string{"🇰🇿🇷🇺 Обратный", "🇩🇪 Германия"}, -1},
		"no KZ, nothing":      {[]string{"🇷🇺🇪🇺 Авто", "🇩🇪 Германия"}, -1},
	} {
		t.Run(name, func(t *testing.T) {
			d, c := aiTestCache(t, tc.remarks...)
			id, _, ok := aiDefault(localctl.Overrides{}, d.cfg.RouteSource, c, []byte(`{"outbounds":[{"tag":"main","protocol":"vless"}]}`))
			if tc.want < 0 {
				if ok {
					t.Fatalf("picked %s with no Kazakh location", id)
				}
				return
			}
			if !ok || id != localctl.Summarize(c)[tc.want].Digest {
				t.Fatalf("picked %q, want entry %d", id, tc.want)
			}
		})
	}
}

func TestTheAIDefaultYieldsToTheOwner(t *testing.T) {
	d, c := aiTestCache(t, "🇷🇺🇪🇺 Авто", "🇷🇺🇰🇿 Казахстан", "🇩🇪 Германия")
	sum := localctl.Summarize(c)
	running := c.Entries[0]
	got, err := d.connectServiceOptionsFor(localctl.Overrides{}, running)
	if err != nil || string(got["ai"]) != string(c.Entries[1]) {
		t.Fatalf("no default: %v %v", got, err)
	}
	// The owner's own location, their «as the main VPN», their country.
	for name, ov := range map[string]localctl.Overrides{
		"own location": {ServiceEntries: map[string]string{"ai": sum[2].Digest}},
		"main VPN":     {ServiceEntries: map[string]string{"ai": localctl.ServiceMainPath}},
		"country":      {Services: map[string]string{"ai": "DE"}},
	} {
		got, err := d.connectServiceOptionsFor(ov, running)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		switch name {
		case "own location":
			if string(got["ai"]) != string(c.Entries[2]) {
				t.Fatalf("%s: %v", name, got)
			}
		default:
			if _, has := got["ai"]; has {
				t.Fatalf("%s: the default overrode the owner", name)
			}
		}
	}
	// «As the main VPN» needs no location, so no cache either.
	if err := os.Remove(d.cfg.EntriesPath); err != nil {
		t.Fatal(err)
	}
	if got, err := d.connectServiceOptionsFor(localctl.Overrides{ServiceEntries: map[string]string{"ai": localctl.ServiceMainPath}}, running); err != nil || got != nil {
		t.Fatalf("main VPN without a cache: %v %v", got, err)
	}
	if _, err := localctl.SaveEntries(d.cfg.EntriesPath, d.cfg.EntriesIndexPath, c); err != nil {
		t.Fatal(err)
	}
	// A router that runs the cascade itself needs no second copy of it.
	if got, _ := d.connectServiceOptionsFor(localctl.Overrides{}, c.Entries[1]); got["ai"] != nil {
		t.Fatal("overlaid the running location on itself")
	}
}

// «As the main VPN» for a service with a default is a choice, kept as such;
// for the others it is simply no choice.
func TestChoosingTheMainVPNForTheAIServiceIsKept(t *testing.T) {
	var ov localctl.Overrides
	// The Vectra app's «as the main VPN» (entryId null).
	c, code := connectRouteChange("set_service", json.RawMessage(`{"service":"ai","entryId":null}`), &localctl.EntriesCache{})
	if code != "" {
		t.Fatal(code)
	}
	c.ApplyTo(&ov)
	if ov.ServiceEntries["ai"] != localctl.ServiceMainPath {
		t.Fatalf("ai: %v", ov.ServiceEntries)
	}
	// The router UI's «default» clears the choice: Kazakhstan again.
	(&localctl.Change{SetService: &localctl.ServiceChoice{ID: "ai"}}).ApplyTo(&ov)
	if _, has := ov.ServiceEntries["ai"]; has {
		t.Fatalf("cleared ai: %v", ov.ServiceEntries)
	}
	(&localctl.Change{SetService: &localctl.ServiceChoice{ID: "youtube", MainPath: true}}).ApplyTo(&ov)
	if _, has := ov.ServiceEntries["youtube"]; has {
		t.Fatalf("youtube: %v", ov.ServiceEntries)
	}
}

// An unchosen default never refuses a render and never unproxies the AI
// sites: a location whose AI path ends DIRECT, or that would not import (no
// path that tunnels — the provider's whitelist chain without the cascade's
// own AI rule), is skipped for the next, and with none there is no default.
func TestAnAIDefaultThatWouldGoDirectOrNotImportIsSkipped(t *testing.T) {
	const direct = `{"outbounds":[{"tag":"kz-1","protocol":"vless"},{"tag":"DIRECT","protocol":"freedom"}],
 "routing":{"balancers":[{"tag":"BL-MAIN","selector":["kz-"],"fallbackTag":"DIRECT"}],
  "rules":[{"domain":["domain:chatgpt.com"],"balancerTag":"BL-MAIN"}]}}`
	const unimportable = `{"outbounds":[{"tag":"kz-2","protocol":"vless"},{"tag":"stage-wl","protocol":"loopback","settings":{"inboundTag":"STAGE_WL"}},{"tag":"DIRECT","protocol":"freedom"}],
 "routing":{"balancers":[{"tag":"BL-MAIN","selector":["kz-"],"fallbackTag":"stage-wl"},{"tag":"BL-WL","selector":["whitelist-"],"fallbackTag":"whitelist-lv3"}],
  "rules":[{"inboundTag":["STAGE_WL"],"balancerTag":"BL-WL"},{"network":"tcp,udp","balancerTag":"BL-MAIN"}]}}`
	const chain = `{"outbounds":[{"tag":"kz-3","protocol":"vless"},{"tag":"stage-wl","protocol":"loopback","settings":{"inboundTag":"STAGE_WL"}},{"tag":"DIRECT","protocol":"freedom"}],
 "routing":{"balancers":[{"tag":"BL-MAIN","selector":["kz-"],"fallbackTag":"stage-wl"},{"tag":"BL-WL","selector":["whitelist-"],"fallbackTag":"whitelist-lv3"}],
  "rules":[{"inboundTag":["STAGE_WL"],"balancerTag":"BL-WL"},{"domain":["domain:chatgpt.com"],"balancerTag":"BL-MAIN"},{"network":"tcp,udp","balancerTag":"BL-MAIN"}]}}`
	const main = `{"outbounds":[{"tag":"main","protocol":"vless"}]}`
	d, c := aiTestCacheOf(t,
		[]string{"🇷🇺🇪🇺 Авто", "🇷🇺🇰🇿 Казахстан", "🇷🇺🇰🇿 Казахстан 2", "🇰🇿 Казахстан"},
		[]string{main, direct, unimportable, chain})
	got, err := d.connectServiceOptionsFor(localctl.Overrides{}, c.Entries[0])
	if err != nil || string(got["ai"]) != chain {
		t.Fatalf("took %s (%v), want the whitelist chain that ends closed", got["ai"], err)
	}
	d, c = aiTestCacheOf(t, []string{"🇷🇺🇪🇺 Авто", "🇷🇺🇰🇿 Казахстан", "🇰🇿 Казахстан"}, []string{main, direct, unimportable})
	if got, err := d.connectServiceOptionsFor(localctl.Overrides{}, c.Entries[0]); err != nil || got != nil {
		t.Fatalf("a default that goes direct or does not import: %v %v", got, err)
	}
}

// The router UI and the Connect inventory say «Kazakhstan» only for the
// default the installed render took (its splice key names it by digest), or
// when the router runs that location itself.
func TestTheAIDefaultIsReportedOnlyWhenTheRenderCarriesIt(t *testing.T) {
	d, c := aiTestCache(t, "🇷🇺🇪🇺 Авто", "🇷🇺🇰🇿 Казахстан")
	sum := localctl.Summarize(c)
	d.cfg.ProviderConfigPath = filepath.Join(t.TempDir(), "provider.json")
	if err := vault.WriteFile(d.cfg.ProviderConfigPath, c.Entries[0]); err != nil {
		t.Fatal(err)
	}
	took := "v1;svcEntries=ai=" + sum[1].Digest + ";exitprobe=x"
	for _, tc := range []struct {
		key  string
		ov   localctl.Overrides
		want bool
	}{
		{took, localctl.Overrides{}, true},
		{"v1;svc=ai:KZ", localctl.Overrides{}, false}, // the render refused it: nothing taken
		{took, localctl.Overrides{ServiceEntries: map[string]string{"ai": localctl.ServiceMainPath}}, false},
	} {
		id, ok := aiDefaultApplied(d.cfg, tc.ov, tc.key)
		if ok != tc.want || (ok && id != sum[1].Digest) {
			t.Fatalf("key %s, %+v: %q %v", tc.key, tc.ov, id, ok)
		}
		res := uiapi.Services{Services: []uiapi.ServiceInfo{{ID: "youtube"}, {ID: "ai"}}}
		markAIDefault(&res, ok)
		if (res.Services[1].DefaultCountry != nil) != tc.want || res.Services[0].DefaultCountry != nil {
			t.Fatalf("UI: %+v", res.Services)
		}
	}
	// A router that runs the cascade itself takes it with no overlay.
	if err := vault.WriteFile(d.cfg.ProviderConfigPath, c.Entries[1]); err != nil {
		t.Fatal(err)
	}
	if id, ok := aiDefaultApplied(d.cfg, localctl.Overrides{}, "v1"); !ok || id != sum[1].Digest {
		t.Fatalf("running the cascade itself: %q %v", id, ok)
	}
	d.cfg.RouteSource = "passwall" // a router on another engine renders no services
	if _, ok := aiDefaultApplied(d.cfg, localctl.Overrides{}, took); ok {
		t.Fatal("reported a default on a router that renders no services")
	}
}

// A render the router's xray refuses with the «Нейросети» default is tried
// again without it; an owner's own choice is never dropped.
func TestARefusedRenderDropsOnlyTheUnchosenAIDefault(t *testing.T) {
	d, _ := aiTestCache(t, "🇷🇺🇪🇺 Авто")
	d.cfg.OverridesPath = filepath.Join(t.TempDir(), "overrides.json")
	opts := xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{"ai": json.RawMessage(`{}`), "youtube": json.RawMessage(`{}`)}}
	without, ok := d.withoutAIDefault(opts)
	if !ok || without.ServiceEntries["ai"] != nil || without.ServiceEntries["youtube"] == nil {
		t.Fatalf("default not dropped: %v %v", without.ServiceEntries, ok)
	}
	if _, ok := d.withoutAIDefault(xray.SpliceOptions{ServiceEntries: map[string]json.RawMessage{"youtube": json.RawMessage(`{}`)}}); ok {
		t.Fatal("dropped something with no AI default")
	}
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.ServiceEntries = map[string]string{"ai": "x"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.withoutAIDefault(opts); ok {
		t.Fatal("dropped the owner's own AI location")
	}
}
