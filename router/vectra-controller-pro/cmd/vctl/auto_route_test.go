package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// passwallRouter gives the test a PassWall2 to route by: its configuration
// naming a global node, its generator, and a `lua` that runs it — answering
// what PassWall2's generator made on a real router (the xray package's
// fixture).
func passwallRouter(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	oldUCI, oldGen, oldLua, oldGet := passwallUCIFile, passwallGenerator, passwallLua, uciGet
	t.Cleanup(func() { passwallUCIFile, passwallGenerator, passwallLua, uciGet = oldUCI, oldGen, oldLua, oldGet })
	passwallUCIFile = filepath.Join(dir, "passwall2")
	if err := os.WriteFile(passwallUCIFile, []byte("config global\n\toption enabled '0'\n\toption node 'myshunt'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	passwallGenerator = filepath.Join(dir, "util_xray.lua")
	if err := os.WriteFile(passwallGenerator, []byte("-- PassWall2's generator\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs(filepath.Join("..", "..", "internal", "coreengine", "xray", "testdata", "passwall", "global.json"))
	if err != nil {
		t.Fatal(err)
	}
	passwallLua = filepath.Join(dir, "lua")
	script := "#!/bin/sh\n[ \"$2\" = gen_config ] || exit 2\ncat '" + fixture + "'\n"
	if err := os.WriteFile(passwallLua, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	uciGet = func(key string) string {
		if key == "passwall2.@global[0].node" {
			return "myshunt"
		}
		return ""
	}
}

// renderedTags is the inbound and outbound tags of the render xray runs.
func renderedTags(t *testing.T, dir string) (inbounds, outbounds []string) {
	t.Helper()
	raw, err := readEncryptedTestFile(t, filepath.Join(dir, "xray.json"))
	if err != nil {
		t.Fatalf("no render: %v", err)
	}
	var doc struct {
		Inbounds  []struct{ Tag string } `json:"inbounds"`
		Outbounds []struct{ Tag string } `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, in := range doc.Inbounds {
		inbounds = append(inbounds, in.Tag)
	}
	for _, out := range doc.Outbounds {
		outbounds = append(outbounds, out.Tag)
	}
	return inbounds, outbounds
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// The migration of 2026-10-03, without its 17 minutes of LAN on plain
// internet: vctl takes a PassWall2 router over before the panel has sent it
// an operator config, and carries the traffic at once — by PassWall2's
// configuration, on the base operator config — while it still shows its
// claim code. The claim's apply then moves it to the provider: the operator
// config the panel sent, the route source back to the provider's.
func TestATakeoverFromPassWallRoutesByItUntilTheOperatorConfigArrives(t *testing.T) {
	passwallRouter(t)
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer d.stopXray(context.Background())

	if !d.autoRoute || d.cfg.RouteSource != routeSourcePassWall || d.desired == nil || d.linked() {
		t.Fatalf("not routing by PassWall2 of itself: auto %v, route source %q, operator config %v, linked %v",
			d.autoRoute, d.cfg.RouteSource, d.desired != nil, d.linked())
	}
	if d.claim.announcement(time.Now()) == nil {
		t.Fatal("routing by PassWall2, the router shows no claim code: nobody could link it")
	}
	if _, err := os.Stat(filepath.Join(dir, "operator.json")); !os.IsNotExist(err) {
		t.Fatalf("the base operator config was written to /etc (%v): the router would read as linked", err)
	}

	// The daemon's start (one loop): the render from PassWall2's
	// configuration, xray on it, the data plane's firewall from the base
	// config's TPROXY inbound — and the panel registered with.
	if err := d.run(ctx, true); err != nil {
		t.Fatal(err)
	}
	in, out := renderedTags(t, dir)
	if !hasTag(in, "tproxy-in") || !hasTag(out, "WorldProxy:NL") {
		t.Fatalf("not PassWall2's render: inbounds %v, outbounds %v", in, out)
	}
	if !d.supStarted {
		t.Fatal("xray does not run the render: the LAN would go out directly")
	}
	if spec, ok := firewallSpecFromConfig(d.desired); !ok || spec.TproxyPort != 12345 {
		t.Fatalf("no data plane to program from the base config: %+v %v", spec, ok)
	}
	if _, err := readEncryptedTestFile(t, filepath.Join(dir, "passwall-config.json")); err != nil {
		t.Fatalf("PassWall2's document is not where route_source 'passwall' keeps it: %v", err)
	}

	// The claim's apply: the panel's operator config, the provider's routes.
	if err := d.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := panel.resultsFor("j1"); len(got) == 0 || got[len(got)-1].Status != "success" {
		t.Fatalf("apply_xray_config: %+v", got)
	}
	if d.autoRoute || d.cfg.RouteSource != "" || !d.linked() {
		t.Fatalf("still routing by PassWall2: auto %v, route source %q, linked %v", d.autoRoute, d.cfg.RouteSource, d.linked())
	}
	d.publishRuntime() // the loop's next step, as after every iteration
	if d.claim.announcement(time.Now()) != nil {
		t.Fatal("linked, the router still announces a claim code")
	}
	in, out = renderedTags(t, dir)
	if !hasTag(in, "tproxy-in") || hasTag(out, "WorldProxy:NL") {
		t.Fatalf("the render is not the provider's: inbounds %v, outbounds %v", in, out)
	}
	if _, err := readEncryptedTestFile(t, filepath.Join(dir, "provider-config.json")); err != nil {
		t.Fatalf("the provider's document was not kept: %v", err)
	}
	if !d.supStarted {
		t.Fatal("xray stopped between the two renders")
	}
}

// The owner's route source is the owner's: set in UCI, vctl chooses none of
// its own, and the panel's operator config does not move it.
func TestAnOwnersRouteSourceIsKept(t *testing.T) {
	passwallRouter(t)
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemonWith(t, dir, panel, provider, map[string]any{"routeSource": routeSourcePassWall})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer d.stopXray(context.Background())
	if d.autoRoute || d.desired != nil {
		t.Fatalf("vctl chose for the owner: auto %v, operator config %v", d.autoRoute, d.desired != nil)
	}
	for i := 0; i < 2; i++ { // register, then the check-in and its apply
		if err := d.runOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if d.cfg.RouteSource != routeSourcePassWall || d.autoRoute || !d.linked() {
		t.Fatalf("route source %q, auto %v, linked %v", d.cfg.RouteSource, d.autoRoute, d.linked())
	}
}

// A router without PassWall2 — a fresh install — has nothing to route by:
// it waits for its setup, as before, and the operator config's absence is
// all it shows.
func TestWithoutPassWallVctlWaitsForItsSetup(t *testing.T) {
	passwallRouter(t)
	_ = os.Remove(passwallUCIFile)
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	if d.autoRoute || d.desired != nil || d.cfg.RouteSource != "" || d.linked() {
		t.Fatalf("auto %v, operator config %v, route source %q", d.autoRoute, d.desired != nil, d.cfg.RouteSource)
	}
}
