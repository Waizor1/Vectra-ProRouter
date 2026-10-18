package catalog

import (
	"reflect"
	"strings"
	"testing"
)

// The built-in catalog carries the four r25 services in their order, then
// the games under «Игры», each a game on the stable exit by default.
func TestTheBuiltinCatalogHasTheGames(t *testing.T) {
	c := Builtin()
	want := []string{"youtube", "tiktok", "telegram", "ai", "steam", "epic", "riot", "battlenet", "starcitizen"}
	if got := c.ServiceIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("order %v", got)
	}
	for _, id := range want[4:] {
		s := c.Services[id]
		modes, def := s.EffectiveModes(id)
		if s.Group != "games" || def != ModeStable || !reflect.DeepEqual(modes, []string{ModeStable, ModeDirect, ModeMain}) || !s.OnlyKnown || s.Title["ru"] == "" || !s.Knows(s.Marker) {
			t.Errorf("%s: %+v", id, s)
		}
	}
	if _, def := c.Services["ai"].EffectiveModes("ai"); def != ModeLocation {
		t.Fatalf("«Нейросети» default %s", def)
	}
	if c.StableExitSettings() != (StableExit{Samples: 6, EveryHours: 6, RecheckMinutes: 30, URL: DefaultStableURL}) {
		t.Fatalf("stable exit %+v", c.StableExitSettings())
	}
}

// A catalog published before r26 (no modes) keeps the r25 behaviour.
func TestACatalogWithoutModesKeepsR25(t *testing.T) {
	c, err := Parse([]byte(`{"schema":1,"serial":5,"services":{"youtube":{},"ai":{"onlyKnown":true}},"sites":{}}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if m, d := c.Services["youtube"].EffectiveModes("youtube"); d != ModeMain || !reflect.DeepEqual(m, []string{ModeMain, ModeCountry, ModeLocation}) {
		t.Fatalf("youtube %v %s", m, d)
	}
	if _, d := c.Services["ai"].EffectiveModes("ai"); d != ModeLocation {
		t.Fatalf("ai default %s", d)
	}
}

// Every new field is held to its form and bounds; one bad value refuses the
// whole catalog.
func TestR26FieldsAreValidated(t *testing.T) {
	base := func(svc, extra string) string {
		return `{"schema":1,"serial":5,"services":{"x":{` + svc + `}},"groups":{"games":{"title":{"ru":"Игры"}}},"sites":{}` + extra + `}`
	}
	good := []string{
		base(`"title":{"ru":"Икс","en":"X","zh":"X"},"group":"games","order":5,"icon":"steam","marker":"domain:x.com","ipMarker":"geoip:x","onlyKnown":true,"names":["domain:x.com"],"ips":["1.2.0.0/16","2001:db8::/32","9.9.9.9"],"modes":["stable","direct"],"default":"stable"`, ""),
		base(`"onlyKnown":true,"modes":["main","stable","direct"],"default":"main"`, `,"tunables":{"stableExit":{"samples":3,"everyHours":48,"recheckMinutes":10,"url":"https://cloudflare.com/cdn-cgi/trace","main":true},"door":{"rate":30,"burst":120},"quic":{"refuse":false}}`),
	}
	for _, raw := range good {
		if _, err := Parse([]byte(raw), true); err != nil {
			t.Errorf("refused %s: %v", raw, err)
		}
	}
	bad := map[string]string{
		"language":        base(`"title":{"de":"X"}`, ""),
		"empty title":     base(`"title":{"ru":""}`, ""),
		"long title":      base(`"title":{"ru":"`+strings.Repeat("я", 65)+`"}`, ""),
		"control char":    base(`"title":{"ru":"a\u0007"}`, ""),
		"unknown group":   base(`"group":"music"`, ""),
		"order":           base(`"order":5000`, ""),
		"icon":            base(`"icon":"Bad Icon"`, ""),
		"marker":          base(`"marker":"keyword:x"`, ""),
		"ip marker":       base(`"ipMarker":"x.com"`, ""),
		"ip":              base(`"ips":["1.2.3.4/33"]`, ""),
		"unmasked net":    base(`"ips":["1.2.3.4/24"]`, ""),
		"mode":            base(`"modes":["turbo"]`, ""),
		"repeated mode":   base(`"modes":["direct","direct"]`, ""),
		"default":         base(`"modes":["stable"],"default":"direct"`, ""),
		"country default": base(`"default":"country"`, ""),
		"samples":         base(``, `,"tunables":{"stableExit":{"samples":50}}`),
		"every":           base(``, `,"tunables":{"stableExit":{"everyHours":100}}`),
		"recheck":         base(``, `,"tunables":{"stableExit":{"recheckMinutes":1}}`),
		"url":             base(``, `,"tunables":{"stableExit":{"url":"https://evil.example/cdn-cgi/trace"}}`),
		"http url":        base(``, `,"tunables":{"stableExit":{"url":"http://www.cloudflare.com/cdn-cgi/trace"}}`),
		"door rate":       base(``, `,"tunables":{"door":{"rate":5,"burst":120}}`),
		"door burst":      base(``, `,"tunables":{"door":{"rate":60,"burst":40}}`),
		"group title":     `{"schema":1,"serial":5,"groups":{"g":{"title":{}}},"sites":{}}`,
		"direct default":  base(`"onlyKnown":true,"modes":["stable","direct"],"default":"direct"`, ""),
		"no onlyKnown":    base(`"marker":"domain:x.com","modes":["stable","direct"],"default":"stable"`, ""),
		"whole internet":  base(`"ips":["0.0.0.0/0"]`, ""),
		"wide v4":         base(`"ips":["10.0.0.0/8"]`, ""),
		"wide v6":         base(`"ips":["2000::/3"]`, ""),
		"geoip in ips":    base(`"ips":["geoip:telegram"]`, ""),
		"negated marker":  base(`"ipMarker":"geoip:!ru"`, ""),
		"bidi title":      base(`"title":{"ru":"Steam\u202e"}`, ""),
		"zero width":      base(`"title":{"ru":"St\u200beam"}`, ""),
	}
	for name, raw := range bad {
		if _, err := Parse([]byte(raw), true); err == nil {
			t.Errorf("%s: accepted %s", name, raw)
		}
	}
}

// What r24 and r25 make of an r26 catalog: they read schema 1 without strict
// and ignore the new fields — a catalog with the games stays usable to them.
func TestAnR26CatalogStaysReadableLeniently(t *testing.T) {
	raw := BuiltinRaw()
	type r25Service struct {
		OnlyKnown bool     `json:"onlyKnown,omitempty"`
		Names     []string `json:"names,omitempty"`
		Geosites  []string `json:"geosites,omitempty"`
	}
	c, err := Parse(raw, false)
	if err != nil || len(c.Services) > 16 {
		t.Fatalf("%v, %d services (r25 refuses more than 16)", err, len(c.Services))
	}
	_ = r25Service{}
}

// A quarantined serial is not loaded, and a higher one is again.
func TestAQuarantinedCatalogIsNotLoaded(t *testing.T) {
	path := t.TempDir() + "/catalog.json"
	raw := []byte(`{"schema":1,"serial":2099010101,"services":{},"sites":{}}`)
	if err := Store(path, raw, []byte("sig")); err != nil {
		t.Fatal(err)
	}
	if l := Load(path); l.Source != SourceDownloaded {
		t.Fatalf("not loaded before: %+v", l.Note)
	}
	if err := Quarantine(path, 2099010101); err != nil || Quarantined(path) != 2099010101 {
		t.Fatalf("quarantine %v %d", err, Quarantined(path))
	}
	if l := Load(path); l.Source != SourceBuiltin || !strings.Contains(l.Note, "quarantined") {
		t.Fatalf("loaded a quarantined catalog: %+v", l)
	}
	if err := Store(path, []byte(`{"schema":1,"serial":2099010102,"services":{},"sites":{}}`), []byte("sig")); err != nil {
		t.Fatal(err)
	}
	if l := Load(path); l.Source != SourceDownloaded || l.Catalog.Serial != 2099010102 {
		t.Fatalf("a newer one is not loaded: %+v", l)
	}
}
