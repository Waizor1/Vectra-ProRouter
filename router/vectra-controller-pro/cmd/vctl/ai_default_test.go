package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/uiapi"
)

func aiTestCache(t *testing.T, remarks ...string) (*daemon, *localctl.EntriesCache) {
	t.Helper()
	dir := t.TempDir()
	cfg := agentcfg.Config{StatePath: filepath.Join(dir, "state.json")}
	cfg.EntriesPath = filepath.Join(dir, "provider-entries.json.gz")
	cfg.EntriesIndexPath = filepath.Join(dir, "provider-entries.index.json")
	c := &localctl.EntriesCache{}
	for i, r := range remarks {
		c.Remarks = append(c.Remarks, r)
		c.Entries = append(c.Entries, json.RawMessage(`{"outbounds":[{"tag":"n`+string(rune('a'+i))+`","protocol":"vless"}]}`))
	}
	if _, err := localctl.SaveEntries(cfg.EntriesPath, cfg.EntriesIndexPath, c); err != nil {
		t.Fatal(err)
	}
	return &daemon{cfg: cfg}, c
}

// «Нейросети» go through Kazakhstan unless the owner chose otherwise: the
// provider's RU→KZ cascade first, its KZ location else, nothing without one.
func TestTheAIServiceDefaultsToTheKazakhCascade(t *testing.T) {
	for name, tc := range map[string]struct {
		remarks []string
		want    int
	}{
		"cascade first":  {[]string{"🇷🇺🇪🇺 Авто", "🇰🇿 Казахстан", "🇷🇺🇰🇿 Казахстан", "🇰🇿 Gemini · Google"}, 2},
		"KZ without one": {[]string{"🇷🇺🇪🇺 Авто", "🇰🇿 Gemini · Google", "🇰🇿 Казахстан"}, 2},
		"no KZ, nothing": {[]string{"🇷🇺🇪🇺 Авто", "🇩🇪 Германия"}, -1},
	} {
		t.Run(name, func(t *testing.T) {
			_, c := aiTestCache(t, tc.remarks...)
			id, ok := defaultAIEntry(c)
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

func TestTheRouterUIShowsKazakhstanAsTheAIDefault(t *testing.T) {
	_, c := aiTestCache(t, "🇷🇺🇪🇺 Авто", "🇷🇺🇰🇿 Казахстан")
	res := uiapi.Services{Services: []uiapi.ServiceInfo{{ID: "youtube"}, {ID: "ai"}}}
	markAIDefault(&res, localctl.Overrides{}, localctl.Summarize(c))
	if res.Services[1].DefaultCountry == nil || *res.Services[1].DefaultCountry != "KZ" || res.Services[0].DefaultCountry != nil {
		t.Fatalf("%+v", res.Services)
	}
	res = uiapi.Services{Services: []uiapi.ServiceInfo{{ID: "ai"}}}
	markAIDefault(&res, localctl.Overrides{ServiceEntries: map[string]string{"ai": localctl.ServiceMainPath}}, localctl.Summarize(c))
	if res.Services[0].DefaultCountry != nil {
		t.Fatal("Kazakhstan shown for an owner who chose the main VPN")
	}
}
