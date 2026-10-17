package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
)

// catalogStand is the signed feed stand with the channel's catalog: the
// router's catalog client trusts the stand's server, its catalog path is the
// test's.
func catalogStand(t *testing.T) (*signedFeedStand, agentcfg.Config) {
	t.Helper()
	s := newSignedFeedStand(t)
	prev := catalogHTTPClient
	catalogHTTPClient = func() *http.Client { return s.srv.Client() }
	t.Cleanup(func() { catalogHTTPClient = prev; xray.UseCatalog(nil) })
	return s, agentcfg.Config{CatalogPath: filepath.Join(t.TempDir(), "catalog.json")}
}

func catalogDoc(serial int64, exits string) []byte {
	return []byte(fmt.Sprintf(`{"schema":1,"serial":%d,"services":{"ai":{"onlyKnown":true,"names":["domain:openai.com"]}},"aiDefault":{"exits":%s},"sites":{},"refreshHours":2}`, serial, exits))
}

func (s *signedFeedStand) publishCatalog(raw, sig []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalog, s.catalogSig = raw, sig
}

func TestTheCatalogURLIsTheFeedsChannel(t *testing.T) {
	got, err := catalogURL("https://api.vectra-pro.net/artifacts/openwrt/pro-canary/aarch64_cortex-a53/")
	if err != nil || got != "https://api.vectra-pro.net/artifacts/openwrt/pro-canary/catalog.json" {
		t.Fatalf("%q %v", got, err)
	}
}

// A catalog the feed's key signed, newer than the one in use, is stored; the
// router takes it from disk.
func TestANewerCatalogSignedByTheFeedsKeyIsStored(t *testing.T) {
	s, cfg := catalogStand(t)
	raw := catalogDoc(catalog.Builtin().Serial+1, `[["KZ"]]`)
	s.publishCatalog(raw, s.key.sign(raw))
	next, stored, err := refreshCatalog(context.Background(), cfg, catalogHTTPClient())
	if err != nil || !stored || next.Serial != catalog.Builtin().Serial+1 {
		t.Fatalf("next %+v stored %v err %v", next, stored, err)
	}
	if l := catalog.Load(cfg.CatalogPath); l.Source != catalog.SourceDownloaded {
		t.Fatalf("loaded %+v", l)
	}
	// Asked again: the same catalog, nothing to store, no error.
	if _, stored, err := refreshCatalog(context.Background(), cfg, catalogHTTPClient()); err != nil || stored {
		t.Fatalf("again: stored %v err %v", stored, err)
	}
}

// Every key in /etc/opkg/keys vouches for packages; only the key that signs
// the Vectra feed vouches for routing. A changed catalog, an older one and
// a replay of the serial in use are not taken either.
func TestACatalogTheFeedsKeyDidNotSignIsRefused(t *testing.T) {
	s, cfg := catalogStand(t)
	openwrt := newFeedKey(9)
	writeFeedFile(t, filepath.Join(s.keys, openwrt.id()), openwrt.pubFile())
	raw := catalogDoc(catalog.Builtin().Serial+1, `[]`)

	s.publishCatalog(raw, openwrt.sign(raw))
	if _, stored, err := refreshCatalog(context.Background(), cfg, catalogHTTPClient()); !errors.Is(err, catalog.ErrNotFeedKey) || stored {
		t.Fatalf("an OpenWrt key: stored %v err %v", stored, err)
	}
	s.publishCatalog(append([]byte(nil), raw...), s.key.sign(catalogDoc(catalog.Builtin().Serial+2, `[]`)))
	if _, stored, err := refreshCatalog(context.Background(), cfg, catalogHTTPClient()); err == nil || stored {
		t.Fatalf("a signature of another document: stored %v err %v", stored, err)
	}
	if _, err := os.Stat(cfg.CatalogPath); !os.IsNotExist(err) {
		t.Fatal("a refused catalog reached the disk")
	}

	s.publishCatalog(raw, s.key.sign(raw))
	if _, stored, err := refreshCatalog(context.Background(), cfg, catalogHTTPClient()); err != nil || !stored {
		t.Fatalf("the real one: stored %v err %v", stored, err)
	}
	replay := catalogDoc(catalog.Builtin().Serial+1, `[["AE"]]`)
	s.publishCatalog(replay, s.key.sign(replay))
	if _, stored, err := refreshCatalog(context.Background(), cfg, catalogHTTPClient()); !errors.Is(err, catalog.ErrReplay) || stored {
		t.Fatalf("same serial, other content: stored %v err %v", stored, err)
	}
	older := catalogDoc(catalog.Builtin().Serial, `[]`)
	s.publishCatalog(older, s.key.sign(older))
	if _, stored, err := refreshCatalog(context.Background(), cfg, catalogHTTPClient()); err != nil || stored {
		t.Fatalf("an older one: stored %v err %v", stored, err)
	}
	if l := catalog.Load(cfg.CatalogPath); l.Catalog.Serial != catalog.Builtin().Serial+1 || len(l.Catalog.AIExits()) != 0 {
		t.Fatalf("in use %+v", l.Catalog)
	}
}

// The daemon asks at its first poll and then every refreshHours; a catalog
// stored by `vctl catalog refresh` or removed by `vctl catalog reset` is taken
// at the next poll. A failure keeps what runs and tries again soon.
func TestTheDaemonTakesTheCatalogAndAsksOnSchedule(t *testing.T) {
	s, cfg := catalogStand(t)
	d := &daemon{cfg: cfg}
	d.catalogSerial = useCatalog(cfg).Catalog.Serial
	now := time.Date(2026, 10, 17, 21, 0, 0, 0, time.UTC)

	d.maybeRefreshCatalog(context.Background(), now) // nothing published
	if !d.nextCatalogRefresh.Equal(now.Add(catalogRetryAfter)) || d.catalogSerial != catalog.Builtin().Serial {
		t.Fatalf("a failed ask: next %v serial %d", d.nextCatalogRefresh, d.catalogSerial)
	}
	raw := catalogDoc(catalog.Builtin().Serial+5, `[]`)
	s.publishCatalog(raw, s.key.sign(raw))
	d.maybeRefreshCatalog(context.Background(), now.Add(time.Minute)) // not due yet
	if d.catalogSerial != catalog.Builtin().Serial {
		t.Fatal("asked before it was due")
	}
	later := now.Add(catalogRetryAfter)
	d.maybeRefreshCatalog(context.Background(), later)
	if d.catalogSerial != catalog.Builtin().Serial+5 || xray.ActiveCatalog().Serial != d.catalogSerial {
		t.Fatalf("serial %d active %d", d.catalogSerial, xray.ActiveCatalog().Serial)
	}
	if lo, hi := later.Add(108*time.Minute), later.Add(132*time.Minute); d.nextCatalogRefresh.Before(lo) || d.nextCatalogRefresh.After(hi) {
		t.Fatalf("next ask %v, want 2 h ±10%% after %v", d.nextCatalogRefresh, later)
	}
	if err := catalog.Remove(cfg.CatalogPath); err != nil {
		t.Fatal(err)
	}
	d.maybeRefreshCatalog(context.Background(), later.Add(time.Minute))
	if d.catalogSerial != catalog.Builtin().Serial || xray.ActiveCatalog().Serial != catalog.Builtin().Serial {
		t.Fatalf("reset: serial %d", d.catalogSerial)
	}
}

// The catalog's aiDefault exits pick the locations «Нейросети» default to,
// in its order; none: no default.
func TestTheCatalogPicksTheAIDefaultLocations(t *testing.T) {
	t.Cleanup(func() { xray.UseCatalog(nil) })
	entries := []localctl.EntrySummary{
		{Digest: "kz-gemini", Remark: "🇰🇿 Gemini · Google"},
		{Digest: "kz", Remark: "🇰🇿 Казахстан "},
		{Digest: "ru-kz", Remark: "🇷🇺🇰🇿 Казахстан "},
		{Digest: "kz-ru", Remark: "🇰🇿🇷🇺 Россия"},
		{Digest: "ae", Remark: "🇦🇪 ОАЭ"},
	}
	if got := strings.Join(aiCandidates(entries), ","); got != "ru-kz,kz,kz-gemini" {
		t.Fatalf("built-in: %s", got)
	}
	for exits, want := range map[string]string{`[["AE"],["KZ"]]`: "ae,kz,kz-gemini", `[]`: ""} {
		c, err := catalog.Parse(catalogDoc(catalog.Builtin().Serial+1, exits), true)
		if err != nil {
			t.Fatal(err)
		}
		xray.UseCatalog(c)
		if got := strings.Join(aiCandidates(entries), ","); got != want {
			t.Fatalf("%s: %s, want %s", exits, got, want)
		}
	}
}
