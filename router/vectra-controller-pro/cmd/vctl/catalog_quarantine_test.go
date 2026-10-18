package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/coreengine/xray"
)

// A downloaded catalog whose render the gate refuses twice in a row, while
// the built-in catalog's render passes, is set aside; a refusal for low
// memory never sets one aside; a document the built-in catalog cannot render
// either is no fault of the catalog.
func TestABrokenCatalogIsQuarantinedOnlyForRealRefusals(t *testing.T) {
	bad := &catalog.Catalog{Schema: 1, Serial: 2099010101, Services: map[string]catalog.Service{}}
	loaded := catalog.Loaded{Catalog: bad, Source: catalog.SourceDownloaded}
	refused := fmt.Errorf("apply: %w: %w", apply.ErrRefused, errors.New("xray: unknown geosite"))
	lowMem := fmt.Errorf("apply: %w: %w", apply.ErrRefused, xray.ErrLowMemory)
	builtinFails := false
	old := catalogRerender
	t.Cleanup(func() { catalogRerender = old; xray.UseCatalog(nil) })
	catalogRerender = func(context.Context, *daemon) error {
		if xray.ActiveCatalog().Serial == bad.Serial || builtinFails {
			return refused
		}
		return nil
	}
	newD := func() *daemon {
		xray.UseCatalog(bad)
		return &daemon{cfg: agentcfg.Config{CatalogPath: filepath.Join(t.TempDir(), "catalog.json")}}
	}

	d := newD()
	for i := 0; i < 5; i++ {
		if d.maybeQuarantine(context.Background(), loaded, lowMem) {
			t.Fatal("quarantined for low memory")
		}
	}
	if d.maybeQuarantine(context.Background(), loaded, refused) {
		t.Fatal("quarantined at the first refusal")
	}
	if !d.maybeQuarantine(context.Background(), loaded, refused) {
		t.Fatal("not quarantined at the second refusal")
	}
	if catalog.Quarantined(d.cfg.CatalogPath) != bad.Serial || xray.ActiveCatalog().Serial != catalog.Builtin().Serial || d.catalogSerial != catalog.Builtin().Serial {
		t.Fatalf("after quarantine: file %d, active %d, daemon %d", catalog.Quarantined(d.cfg.CatalogPath), xray.ActiveCatalog().Serial, d.catalogSerial)
	}

	// The built-in catalog's render fails too: the document's fault, the
	// downloaded catalog stays in use, nothing is set aside.
	d = newD()
	builtinFails = true
	d.maybeQuarantine(context.Background(), loaded, refused)
	if d.maybeQuarantine(context.Background(), loaded, refused) || catalog.Quarantined(d.cfg.CatalogPath) != 0 || xray.ActiveCatalog().Serial != bad.Serial {
		t.Fatalf("quarantined though the built-in fails too: file %d active %d", catalog.Quarantined(d.cfg.CatalogPath), xray.ActiveCatalog().Serial)
	}

	// A built-in catalog is never quarantined.
	d = newD()
	builtinFails = false
	b := catalog.Loaded{Catalog: catalog.Builtin(), Source: catalog.SourceBuiltin}
	d.maybeQuarantine(context.Background(), b, refused)
	if d.maybeQuarantine(context.Background(), b, refused) {
		t.Fatal("quarantined the built-in catalog")
	}
}
