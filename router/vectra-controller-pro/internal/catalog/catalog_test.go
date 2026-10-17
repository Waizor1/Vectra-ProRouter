package catalog_test

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/feedverify"
)

// testKey is a usign key as feedtool keygen makes one; obviously fake.
type testKey struct {
	number [8]byte
	priv   ed25519.PrivateKey
}

func newTestKey(seed byte) testKey {
	k := testKey{priv: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))}
	for i := range k.number {
		k.number[i] = seed*16 + byte(i)
	}
	return k
}

func (k testKey) id() string { return fmt.Sprintf("%016x", binary.BigEndian.Uint64(k.number[:])) }

func (k testKey) pub() []byte {
	blob := append(append([]byte("Ed"), k.number[:]...), k.priv.Public().(ed25519.PublicKey)...)
	return []byte("untrusted comment: test key\n" + base64.StdEncoding.EncodeToString(blob) + "\n")
}

func (k testKey) sign(msg []byte) []byte {
	blob := append(append([]byte("Ed"), k.number[:]...), ed25519.Sign(k.priv, msg)...)
	return []byte("untrusted comment: signed by key " + k.id() + "\n" + base64.StdEncoding.EncodeToString(blob) + "\n")
}

func keysWith(t *testing.T, keys ...testKey) string {
	t.Helper()
	dir := t.TempDir()
	for _, k := range keys {
		if err := os.WriteFile(filepath.Join(dir, k.id()), k.pub(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func doc(serial int64, extra string) []byte {
	return []byte(fmt.Sprintf(`{"schema":1,"serial":%d,"services":{"ai":{"onlyKnown":true,"names":["domain:openai.com","full:gemini.google.com"],"geosites":["openai"]}},"aiDefault":{"exits":[["RU","KZ"],["KZ"]]},"sites":{"direct":[],"proxy":[]}%s}`, serial, extra))
}

func TestTheBuiltinCatalogIsValidAndKnowsTheAIServices(t *testing.T) {
	c := catalog.Builtin()
	if _, err := catalog.Parse(catalog.BuiltinRaw(), true); err != nil {
		t.Fatal(err)
	}
	ai := c.Service("ai")
	if !ai.OnlyKnown {
		t.Fatal("«Нейросети» must not take the provider's whole list")
	}
	if got := c.AIExits(); len(got) != 2 || strings.Join(got[0], "") != "RUKZ" || strings.Join(got[1], "") != "KZ" {
		t.Fatalf("aiDefault exits %v", got)
	}
	if c.Refresh() != 6 {
		t.Fatalf("refresh %d", c.Refresh())
	}
}

// The provider's AI rule as every location carried it on 2026-10-17: the AI
// names and, in the same list, games. «Нейросети» know the AI names only.
var providerAIRule = []string{
	"domain:chatgpt.com", "domain:openai.com", "domain:oaistatic.com", "domain:oaiusercontent.com",
	"domain:claude.ai", "domain:anthropic.com", "domain:claudeusercontent.com", "domain:perplexity.ai",
	"domain:x.ai", "domain:grok.com", "domain:gemini.google.com", "domain:bard.google.com",
	"domain:makersuite.google.com", "domain:aistudio.google.com", "domain:generativelanguage.googleapis.com",
	"domain:labs.google.com", "domain:ai.google.dev", "domain:deepmind.com", "domain:deepmind.google",
}

var providerGames = []string{
	"domain:steampowered.com", "domain:steamcommunity.com", "domain:steamstatic.com", "domain:steamcontent.com",
	"domain:steamusercontent.com", "domain:steamserver.net", "domain:epicgames.com", "domain:fortnite.com",
	"domain:unrealengine.com", "domain:easyanticheat.net", "domain:easy.ac", "domain:riotgames.com",
	"domain:riotcdn.net", "domain:leagueoflegends.com", "domain:playvalorant.com", "domain:battle.net",
	"domain:blizzard.com", "domain:blizzardcdn.com", "domain:robertsspaceindustries.com",
	"domain:cloudimperiumgames.com", "domain:rsilauncher.com", "domain:spectrum.chat",
}

func TestTheAIServiceKnowsTheProvidersAINamesAndNoGame(t *testing.T) {
	ai := catalog.Builtin().Service("ai")
	for _, m := range providerAIRule {
		if !ai.Knows(m) {
			t.Errorf("%s is AI", m)
		}
	}
	for _, m := range providerGames {
		if ai.Knows(m) {
			t.Errorf("%s is not AI", m)
		}
	}
}

func TestKnowsTakesSubdomainsButNotParentsNorPatterns(t *testing.T) {
	s := catalog.Service{Names: []string{"full:gemini.google.com", "domain:x.ai", "geosite:anthropic"}, Geosites: []string{"openai"}}
	for m, want := range map[string]bool{
		"domain:gemini.google.com":   true,
		"full:gemini.google.com":     true,
		"gemini.google.com":          false, // plain: a substring match in xray
		"domain:a.gemini.google.com": true,
		"domain:google.com":          false, // the parent: all of Google
		"full:google.com":            false,
		"domain:max.ai":              false, // not under x.ai
		"domain:api.x.ai":            true,
		"keyword:gemini":             false,
		"regexp:.*gemini.*":          false,
		"ext:geosite.dat:openai":     false,
		"geosite:openai":             true,
		"geosite:openai@ads":         true,
		"geosite:anthropic":          true,
		"geosite:google":             false,
		"GeoSite:OpenAI":             true,
		"domain:":                    false,
		"dotless:gemini":             false,
	} {
		if got := s.Knows(m); got != want {
			t.Errorf("Knows(%q) = %v, want %v", m, got, want)
		}
	}
}

func TestParseRefusesWhatIsNotACatalog(t *testing.T) {
	for name, raw := range map[string]string{
		"schema 2":         `{"schema":2,"serial":1}`,
		"no serial":        `{"schema":1}`,
		"keyword name":     `{"schema":1,"serial":1,"services":{"ai":{"names":["keyword:gpt"]}}}`,
		"upper case host":  `{"schema":1,"serial":1,"services":{"ai":{"names":["domain:OpenAI.com"]}}}`,
		"bad geosite":      `{"schema":1,"serial":1,"services":{"ai":{"geosites":["open ai"]}}}`,
		"bad service id":   `{"schema":1,"serial":1,"services":{"A I":{}}}`,
		"bad country":      `{"schema":1,"serial":1,"aiDefault":{"exits":[["kz"]]}}`,
		"empty exit":       `{"schema":1,"serial":1,"aiDefault":{"exits":[[]]}}`,
		"bad site":         `{"schema":1,"serial":1,"sites":{"direct":["http://"]}}`,
		"refresh too long": `{"schema":1,"serial":1,"refreshHours":100}`,
		"two documents":    `{"schema":1,"serial":1}{}`,
		"not json":         `schema=1`,
	} {
		if _, err := catalog.Parse([]byte(raw), false); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	if _, err := catalog.Parse(bytes.Repeat([]byte(" "), catalog.MaxBytes+1), false); !errors.Is(err, catalog.ErrTooLarge) {
		t.Errorf("oversize: %v", err)
	}
}

// A router reads the fields it knows of a newer catalog; the publisher, which
// runs the current code, refuses a field it does not know (a typo).
func TestUnknownFieldsAreTheRoutersToIgnoreAndThePublishersToRefuse(t *testing.T) {
	raw := doc(5, `,"gamesExit":"BL-MAIN"`)
	if _, err := catalog.Parse(raw, false); err != nil {
		t.Fatalf("router: %v", err)
	}
	if _, err := catalog.Parse(raw, true); err == nil {
		t.Fatal("publisher took an unknown field")
	}
}

func TestVerifyTakesOnlyTheFeedsKey(t *testing.T) {
	vectra, openwrt := newTestKey(1), newTestKey(2)
	keys := keysWith(t, vectra, openwrt)
	raw := doc(7, "")
	if err := catalog.Verify(raw, vectra.sign(raw), keys, vectra.number); err != nil {
		t.Fatalf("the feed's key: %v", err)
	}
	if err := catalog.Verify(raw, openwrt.sign(raw), keys, vectra.number); !errors.Is(err, catalog.ErrNotFeedKey) {
		t.Fatalf("an OpenWrt key in /etc/opkg/keys: %v", err)
	}
	if err := catalog.Verify(append(raw, ' '), vectra.sign(raw), keys, vectra.number); !errors.Is(err, feedverify.ErrBadSignature) {
		t.Fatalf("a changed catalog: %v", err)
	}
	stranger := newTestKey(3)
	if err := catalog.Verify(raw, stranger.sign(raw), keys, vectra.number); !errors.Is(err, feedverify.ErrUnknownKey) {
		t.Fatalf("a key the router does not hold: %v", err)
	}
}

func TestNewerTakesOnlyAHigherSerial(t *testing.T) {
	cur, curRaw := mustParse(t, doc(10, "")), doc(10, "")
	for name, tc := range map[string]struct {
		raw  []byte
		want error
	}{
		"higher":         {doc(11, ""), nil},
		"lower":          {doc(9, ""), catalog.ErrOlder},
		"same":           {doc(10, ""), catalog.ErrSame},
		"same, reworded": {doc(10, `,"refreshHours":2`), catalog.ErrReplay},
	} {
		next := mustParse(t, tc.raw)
		err := catalog.Newer(cur, curRaw, next, tc.raw)
		if (tc.want == nil) != (err == nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}

func TestLoadPrefersANewerStoredCatalogAndNeverFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	builtin := catalog.Builtin()

	if l := catalog.Load(path); l.Source != catalog.SourceBuiltin || l.Note != "" {
		t.Fatalf("none stored: %+v", l)
	}
	newer := doc(builtin.Serial+1, "")
	if err := catalog.Store(path, newer, []byte("sig")); err != nil {
		t.Fatal(err)
	}
	if l := catalog.Load(path); l.Source != catalog.SourceDownloaded || l.Catalog.Serial != builtin.Serial+1 {
		t.Fatalf("newer stored: %+v", l)
	}
	if b, _ := os.ReadFile(path + ".sig"); string(b) != "sig" {
		t.Fatal("the signature is kept next to the catalog")
	}
	older := doc(builtin.Serial-1, "")
	if err := catalog.Store(path, older, []byte("sig")); err != nil {
		t.Fatal(err)
	}
	if l := catalog.Load(path); l.Source != catalog.SourceBuiltin || l.Note == "" || !l.Older {
		t.Fatalf("a stored catalog older than the release's: %+v", l)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	if l := catalog.Load(path); l.Source != catalog.SourceBuiltin || l.Note == "" {
		t.Fatalf("a broken stored catalog: %+v", l)
	}
	if err := catalog.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".sig"); !os.IsNotExist(err) {
		t.Fatal("reset leaves the signature")
	}
	if err := catalog.Remove(path); err != nil {
		t.Fatalf("reset twice: %v", err)
	}
}

func mustParse(t *testing.T, raw []byte) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Parse(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
