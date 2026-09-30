package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/config"
)

// The directory xray reads moves with a render written under it, never
// before: an operator config naming a directory the xray -test gate refuses
// leaves the running render — and every restart of it — its own.
func TestTheGeoDirectoryMovesOnlyWithAWrittenRender(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	// This xray's -test refuses a directory without geo files, as the real
	// one refuses a config naming categories it cannot find.
	stub := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  version) echo 'Xray 26.7.28 (fake)'; exit 0;;\n" +
		"  run)\n" +
		"    for a in \"$@\"; do [ \"$a\" = '-test' ] && { [ -s \"$XRAY_LOCATION_ASSET/geoip.dat\" ] || exit 1; exit 0; }; done\n" +
		"    exec sleep 300;;\n" +
		"  *) exec sleep 300;;\n" +
		"esac\n"
	strict := func() {
		if err := os.WriteFile(d.cfg.XrayBinary, []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	strict()
	withGeo := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"geoip.dat", "geosite.dat"} {
			if err := os.WriteFile(filepath.Join(p, f), []byte{0x0a}, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	a, b := withGeo("geo-a"), withGeo("geo-b")
	cfg, err := config.Unmarshal(operatorConfigPointingAt(t, provider.URL))
	if err != nil {
		t.Fatal(err)
	}
	// As jobApplyXrayConfig does: the operator config is adopted and saved
	// before the gate — refused or not.
	apply := func(assetDir string) error {
		c := *cfg
		c.Geo.AssetDir = assetDir
		d.desired = &c
		if err := config.Save(d.cfg.XrayConfigPath, &c); err != nil {
			t.Fatal(err)
		}
		d.rebuildApplier()
		_, err := d.applyProvider(context.Background(), providerEntry(t), true)
		if perr := d.persist(); perr != nil {
			t.Fatal(perr)
		}
		return err
	}

	if err := apply(a); err != nil {
		t.Fatal(err)
	}
	if got := d.sup.AssetDir(); got != a {
		t.Fatalf("after a render written under %s xray reads %s", a, got)
	}
	missing := filepath.Join(dir, "geo-missing")
	if err := apply(missing); err == nil {
		t.Fatal("the gate took a directory without geo files")
	}
	if got := d.sup.AssetDir(); got != a {
		t.Fatalf("a config the gate refused moved xray's geo directory to %s", got)
	}
	// Nor at vctl's next start: the render runs with the directory it was
	// checked against, and its re-render under the refused one is refused
	// again.
	d2 := newTestDaemon(t, dir, panel, provider)
	strict() // newTestDaemon wrote the stand-in that takes everything
	if got := d2.sup.AssetDir(); got != a {
		t.Fatalf("after a restart xray reads %s; its render was checked against %s", got, a)
	}
	d2.reconcileRender(context.Background())
	if got := d2.sup.AssetDir(); got != a {
		t.Fatalf("the restart's re-render moved xray to %s", got)
	}
	if err := apply(b); err != nil {
		t.Fatal(err)
	}
	if got := d.sup.AssetDir(); got != b {
		t.Fatalf("after a render written under %s xray reads %s", b, got)
	}
	if got := d.collector.AssetDir(); got != b {
		t.Fatalf("the inventory reports %s, xray reads %s", got, b)
	}
}
