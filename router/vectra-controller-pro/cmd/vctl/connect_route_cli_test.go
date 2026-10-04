package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
)

func TestConnectChoiceThenCLIProviderApplyClearsDigest(t *testing.T) {
	a := strings.Repeat("a", 64)
	f := newRouteFixture(t, &localctl.Overrides{EntryDigest: a, EntryRemark: "old Connect entry", ConnectRules: true})
	if _, err := f.run(t, routeStable, true); err != nil {
		t.Fatal(err)
	}
	ov, err := localctl.LoadOverrides(f.cfg.OverridesPath)
	if err != nil {
		t.Fatal(err)
	}
	if ov.EntryDigest != "" {
		t.Fatal("CLI B retained Connect A digest")
	}
	if ov.ConnectRules {
		t.Fatal("CLI native IP carry retained domain-only parser")
	}
	i, local, stale, err := localctl.Resolve([]string{"A", routeStable}, ov, "A", 0)
	if err != nil || i != 1 || !local || stale {
		t.Fatalf("restart picked wrong entry: index=%d local=%t stale=%t err=%v", i, local, stale, err)
	}
	if !contains(ov.Proxy, "5.255.255.5") {
		t.Fatal("legacy IP carry lost")
	}
}

func TestConnectServicePreviewPreservesExactOverlayAndSkipsStale(t *testing.T) {
	d, _, entries, _ := newLocalUIDaemon(t)
	cache := &localctl.EntriesCache{Remarks: []string{"a", "b"}, Entries: []json.RawMessage{entries[0], entries[1]}}
	if _, err := localctl.SaveEntries(d.cfg.EntriesPath, d.cfg.EntriesIndexPath, cache); err != nil {
		t.Fatal(err)
	}
	ov := localctl.Overrides{ConnectRules: true, Direct: []string{"_service.example"}, ServiceEntries: map[string]string{"tiktok": apply.Digest(entries[1])}}
	opts := routePreviewOptions(d, entries[0], ov)
	if !opts.Rules.Connect || !bytes.Equal(opts.ServiceEntries["tiktok"], entries[1]) {
		t.Fatal("preview dropped Connect settings")
	}
	// A location gone from the cache is skipped, as the render skips it.
	ov.ServiceEntries["tiktok"] = strings.Repeat("a", 64)
	if opts := routePreviewOptions(d, entries[0], ov); opts.ServiceEntries["tiktok"] != nil || !opts.Rules.Connect {
		t.Fatal("preview kept a stale service entry")
	}
	// CLI applies the legacy parser so its carried IP/CIDR sites remain rules.
	ov = localctl.Overrides{Direct: []string{"5.255.255.5"}, Proxy: []string{"example.org"}}
	opts = routePreviewOptions(d, entries[0], ov)
	_, res, err := xray.Splice(entries[0], d.desired.Inbounds.Tproxy, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.UserRules.Direct != 1 || res.UserRules.Proxy != 1 {
		t.Fatal("preview dropped legacy IP rules", res.UserRules)
	}
}
