package localctl

import (
	"reflect"
	"testing"

	"vectra-controller-pro/internal/catalog"
)

// A mode chosen, a country, a location, «as the main VPN» and nothing at all:
// each service runs what the owner chose while the catalog offers it, else
// the catalog's default.
func TestServiceModeIsTheOwnersChoiceElseTheCatalogs(t *testing.T) {
	c := catalog.Builtin()
	ov := Overrides{
		ServiceModes:   map[string]string{"steam": "direct", "epic": "country"},
		DirectConsent:  map[string]string{"steam": c.Services["steam"].MatchersHash()},
		Services:       map[string]string{"tiktok": "DE"},
		ServiceEntries: map[string]string{"youtube": "abc", "ai": ServiceMainPath},
	}
	for id, want := range map[string]struct {
		mode          string
		chosen, stale bool
	}{
		"steam":       {"direct", true, false},
		"epic":        {"stable", true, true}, // a mode the catalog does not offer a game: its default meanwhile
		"tiktok":      {"country", true, false},
		"youtube":     {"location", true, false},
		"ai":          {"main", true, false},
		"telegram":    {"main", false, false},
		"starcitizen": {"stable", false, false},
	} {
		m, chosen, stale := ov.ServiceMode(id, c)
		if m != want.mode || chosen != want.chosen || stale != want.stale {
			t.Errorf("%s: %s chosen=%v stale=%v, want %+v", id, m, chosen, stale, want)
		}
	}
	modes := ov.RenderModes(c)
	if modes["steam"] != "direct" || modes["starcitizen"] != "stable" || modes["tiktok"] != "" || modes["epic"] != "stable" {
		t.Fatalf("render modes %v", modes)
	}
	if !ov.NeedsStableExit(c) {
		t.Fatal("games on the stable exit need one")
	}
}

// Choosing a mode clears the service's country and location; «Авто» clears
// every choice; a country clears the mode; «Нейросети»' «as the main VPN»
// keeps r25's form.
func TestSetServiceModeClearsTheOtherChoices(t *testing.T) {
	o := Overrides{Services: map[string]string{"steam": "DE"}, ServiceEntries: map[string]string{"steam": "abc"}}
	Change{SetServiceMode: &ServiceModeChoice{ID: "steam", Mode: "direct"}}.ApplyTo(&o)
	if o.Services["steam"] != "" || o.ServiceEntries["steam"] != "" || o.ServiceModes["steam"] != "direct" {
		t.Fatalf("after direct: %+v", o)
	}
	Change{SetService: &ServiceChoice{ID: "steam", Country: "NL"}}.ApplyTo(&o)
	if o.ServiceModes["steam"] != "" || o.Services["steam"] != "NL" {
		t.Fatalf("after a country: %+v", o)
	}
	Change{SetServiceMode: &ServiceModeChoice{ID: "steam"}}.ApplyTo(&o)
	if len(o.ServiceModes)+len(o.Services)+len(o.ServiceEntries) != 0 {
		t.Fatalf("after «Авто»: %+v", o)
	}
	Change{SetServiceMode: &ServiceModeChoice{ID: "ai", Mode: "main"}}.ApplyTo(&o)
	if o.ServiceEntries["ai"] != ServiceMainPath || o.ServiceModes["ai"] != "" {
		t.Fatalf("«Нейросети» as the main VPN: %+v", o)
	}
	on := true
	Change{SetStableMain: &on}.ApplyTo(&o)
	if o.StableMain == nil || !*o.StableMain || !o.StableMainOn(catalog.Builtin()) {
		t.Fatalf("stable main: %+v", o)
	}
	off := Overrides{}
	if off.StableMainOn(catalog.Builtin()) || !reflect.DeepEqual(off.RenderModes(&catalog.Catalog{Schema: 1, Serial: 1}), map[string]string(nil)) {
		t.Fatal("defaults")
	}
}

// «Напрямую» is bound to what the service matched when the owner chose it:
// a catalog that widens the service makes the choice stale.
func TestDirectConsentFollowsTheCatalog(t *testing.T) {
	c := catalog.Builtin()
	var o Overrides
	Change{SetServiceMode: &ServiceModeChoice{ID: "steam", Mode: "direct", Consent: c.Services["steam"].MatchersHash()}}.ApplyTo(&o)
	if m, _, stale := o.ServiceMode("steam", c); m != "direct" || stale {
		t.Fatalf("consented direct: %s stale=%v", m, stale)
	}
	wider := *c
	wider.Services = map[string]catalog.Service{}
	for k, v := range c.Services {
		wider.Services[k] = v
	}
	s := wider.Services["steam"]
	s.Names = append(append([]string(nil), s.Names...), "domain:example.com")
	wider.Services["steam"] = s
	if m, chosen, stale := o.ServiceMode("steam", &wider); m != "stable" || !chosen || !stale {
		t.Fatalf("a widened service kept the owner's direct: %s chosen=%v stale=%v", m, chosen, stale)
	}
	Change{SetServiceMode: &ServiceModeChoice{ID: "steam", Mode: "stable"}}.ApplyTo(&o)
	if len(o.DirectConsent) != 0 {
		t.Fatalf("consent kept after another mode: %v", o.DirectConsent)
	}
}
