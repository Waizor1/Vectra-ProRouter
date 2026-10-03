package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/power"
)

// passwallRouter gives the test a PassWall2 to route by, as the takeover
// leaves it: its configuration naming a global node it has, its own switch
// turned off by the takeover and noted in its breadcrumb, its generator,
// and a `lua` that runs it — answering what PassWall2's generator made on a
// real router (the xray package's fixture). It returns where the takeover's
// breadcrumbs are.
func passwallRouter(t *testing.T) (markers string) {
	t.Helper()
	dir := t.TempDir()
	oldUCI, oldGen, oldLua, oldGet, oldEnv := passwallUCIFile, passwallGenerator, passwallLua, uciGet, powerEnv
	t.Cleanup(func() {
		passwallUCIFile, passwallGenerator, passwallLua, uciGet, powerEnv = oldUCI, oldGen, oldLua, oldGet, oldEnv
	})
	passwallUCIFile = filepath.Join(dir, "passwall2")
	if err := os.WriteFile(passwallUCIFile, []byte("config global\n\toption enabled '0'\n\toption node 'myshunt'\n\nconfig nodes 'myshunt'\n\toption protocol '_shunt'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	markers = filepath.Join(dir, "etc-vectra")
	if err := os.MkdirAll(markers, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markers, ".passwall-switch-off-by-vctl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	powerEnv = func() power.Env {
		env := power.RouterEnv()
		env.MarkerDir, env.TrialMarkers = markers, filepath.Join(dir, "trial.d")
		return env
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
	return markers
}

// passwallFails makes PassWall2's generator fail, as a broken configuration
// or a router out of memory would.
func passwallFails(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(passwallLua, []byte("#!/bin/sh\necho 'lua: util_xray.lua:1: attempt to index a nil value' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
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

// An owner's PassWall2, switched off before vctl came (no breadcrumb of the
// takeover, its switch off): it carried nothing, so vctl routes by nothing
// of it — as r13, waiting for its setup.
func TestAPassWallItsOwnerSwitchedOffIsNotRoutedBy(t *testing.T) {
	markers := passwallRouter(t)
	if err := os.Remove(filepath.Join(markers, ".passwall-switch-off-by-vctl")); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	if d.autoRoute || d.desired != nil || d.cfg.RouteSource != "" {
		t.Fatalf("auto %v, operator config %v, route source %q", d.autoRoute, d.desired != nil, d.cfg.RouteSource)
	}
}

// The rc.d link's breadcrumb alone — an enabled link, PassWall2's own switch
// off (LuCI's main switch): the takeover notes the link, and that PassWall2
// carried nothing. No auto route.
func TestTheRcdBreadcrumbAloneIsNoProof(t *testing.T) {
	markers := passwallRouter(t)
	if err := os.Remove(filepath.Join(markers, ".passwall-switch-off-by-vctl")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(markers, ".passwall-disabled-by-vctl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	d := newTestDaemon(t, dir, newPanelStub(t, operatorConfigPointingAt(t, provider.URL)), provider)
	if d.autoRoute || d.desired != nil || d.cfg.RouteSource != "" {
		t.Fatalf("auto %v, operator config %v, route source %q", d.autoRoute, d.desired != nil, d.cfg.RouteSource)
	}
}

// startAuto starts a daemon that routes by PassWall2 of itself (one loop:
// its render, xray on it) against a panel whose operator config points at
// subscription.
func startAuto(t *testing.T, dir, subscription string) (*daemon, *panelStub) {
	t.Helper()
	provider := newProviderStub(t, providerEntry(t))
	if subscription == "" {
		subscription = provider.URL
	}
	panel := newPanelStub(t, operatorConfigPointingAt(t, subscription))
	d := newTestDaemon(t, dir, panel, provider)
	t.Cleanup(func() { d.stopXray(context.Background()) })
	if !d.autoRoute {
		t.Fatal("not routing by PassWall2 of itself")
	}
	if err := d.run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, out := renderedTags(t, dir); !hasTag(out, "WorldProxy:NL") || !d.supStarted {
		t.Fatalf("not running PassWall2's render: %v, xray %v", out, d.supStarted)
	}
	return d, panel
}

// The claim's apply comes, and the provider cannot be fetched: the job
// fails, and the PassWall render keeps running — the LAN keeps its VPN; the
// daemon's own refresh tries the provider again.
func TestAnApplyWhoseProviderFailsKeepsThePassWallRender(t *testing.T) {
	passwallRouter(t)
	dead := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	t.Cleanup(dead.Close)
	dir := t.TempDir()
	d, panel := startAuto(t, dir, dead.URL)
	if err := d.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := panel.resultsFor("j1"); len(got) == 0 || got[len(got)-1].Status != "failure" {
		t.Fatalf("apply_xray_config: %+v", got)
	}
	if _, out := renderedTags(t, dir); !hasTag(out, "WorldProxy:NL") || !d.supStarted {
		t.Fatalf("the render that ran is gone: %v, xray %v", out, d.supStarted)
	}
	if d.autoRoute || d.cfg.RouteSource != "" || !d.linked() {
		t.Fatalf("auto %v, route source %q, linked %v: the panel's config is the router's now", d.autoRoute, d.cfg.RouteSource, d.linked())
	}
}

// A reboot while vctl routes by PassWall2 of itself: no render on tmpfs, and
// PassWall2's generator failing at the start — the render is rebuilt from
// PassWall's document on /etc (resumeRender), and runs.
func TestARebootInAutoModeResumesThePassWallRender(t *testing.T) {
	passwallRouter(t)
	dir := t.TempDir()
	d, _ := startAuto(t, dir, "")
	d.stopXray(context.Background())
	if err := os.Remove(filepath.Join(dir, "xray.json")); err != nil {
		t.Fatal(err)
	}
	passwallFails(t)
	// The panel out of reach after the reboot: what runs is the router's own.
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	panel.Close()
	again := newTestDaemon(t, dir, panel, provider)
	defer again.stopXray(context.Background())
	if !again.autoRoute {
		t.Fatal("after the reboot, not routing by PassWall2")
	}
	_ = again.run(context.Background(), true) // its check-in fails: the panel is down
	if _, out := renderedTags(t, dir); !hasTag(out, "WorldProxy:NL") || !again.supStarted {
		t.Fatalf("not resumed: %v, xray %v", out, again.supStarted)
	}
}

// PassWall2's generator failing on the first start: no render, no xray — no
// data plane, which `vectra on` sees and answers by giving the router back
// to PassWall2 (internal/power). The daemon stays up and tries again.
func TestAGeneratorFailureInAutoModeLoadsNothing(t *testing.T) {
	passwallRouter(t)
	passwallFails(t)
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	d := newTestDaemon(t, dir, newPanelStub(t, operatorConfigPointingAt(t, provider.URL)), provider)
	defer d.stopXray(context.Background())
	if err := d.run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "xray.json")); !os.IsNotExist(err) || d.supStarted {
		t.Fatalf("a render or xray without PassWall2's generator: %v, xray %v", err, d.supStarted)
	}
	if !d.autoRoute || d.passwallCfgStamp != "" {
		t.Fatalf("auto %v, stamp %q: the next loop would not try again", d.autoRoute, d.passwallCfgStamp)
	}
}

// apply-local installs the provider's document from the operator's config;
// routing by PassWall2 of itself there is neither: refused, and nothing is
// written — PassWall's document least of all.
func TestApplyLocalRefusesTheAutoRoute(t *testing.T) {
	passwallRouter(t)
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	d := newTestDaemon(t, dir, newPanelStub(t, operatorConfigPointingAt(t, provider.URL)), provider)
	if !d.autoRoute {
		t.Fatal("not routing by PassWall2")
	}
	doc := filepath.Join(dir, "doc.json")
	if err := os.WriteFile(doc, providerEntry(t), 0o600); err != nil {
		t.Fatal(err)
	}
	err := cmdApplyLocal([]string{"-config", filepath.Join(dir, "agent.json"), "-provider", doc, "-ignore-legacy-agent"})
	if err == nil || !strings.Contains(err.Error(), "no operator config") {
		t.Fatalf("err = %v", err)
	}
	for _, f := range []string{"passwall-config.json", "provider-config.json", "xray.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s written", f)
		}
	}
}
