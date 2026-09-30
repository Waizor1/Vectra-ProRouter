package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/rescue"
)

// refreshRouter is a daemon running a data plane from an operator config on
// disk, its stored provider document last written `age` ago.
func refreshRouter(t *testing.T, providerURL string, age time.Duration) *daemon {
	t.Helper()
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	if err := os.WriteFile(d.cfg.XrayConfigPath, operatorConfigPointingAt(t, providerURL), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		t.Fatal(err)
	}
	d.desired = cfg
	d.rebuildApplier()
	if _, err := d.applyProvider(context.Background(), providerEntry(t), false); err != nil {
		t.Fatalf("the first apply: %v", err)
	}
	then := time.Now().Add(-age)
	if err := os.Chtimes(d.cfg.ProviderConfigPath, then, then); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTheDaemonRefreshesItsSubscriptionByItself(t *testing.T) {
	provider := newProviderStub(t, providerEntry(t))
	d := refreshRouter(t, provider.URL, 7*time.Hour)
	d.subClient = provider.Client()
	ctx, now := context.Background(), time.Now()

	// A router that was off for longer than a period refreshes at once.
	d.maybeRefreshSubscription(ctx, now)
	if n := len(provider.headers()); n != 1 {
		t.Fatalf("%d fetches at the first poll after a start, want 1", n)
	}
	d.maybeRefreshSubscription(ctx, now.Add(time.Minute))
	if n := len(provider.headers()); n != 1 {
		t.Fatalf("%d fetches a minute later, want still 1", n)
	}
	d.maybeRefreshSubscription(ctx, now.Add(subscriptionRefreshEvery+time.Second))
	if n := len(provider.headers()); n != 2 {
		t.Fatalf("%d fetches a period later, want 2", n)
	}
}

// A document written less than a period ago is not fetched again at start.
func TestAFreshDocumentWaitsItsPeriod(t *testing.T) {
	provider := newProviderStub(t, providerEntry(t))
	d := refreshRouter(t, provider.URL, time.Hour)
	d.subClient = provider.Client()
	now := time.Now()
	d.maybeRefreshSubscription(context.Background(), now)
	if n := len(provider.headers()); n != 0 {
		t.Fatalf("%d fetches for a document an hour old, want 0", n)
	}
	d.maybeRefreshSubscription(context.Background(), now.Add(subscriptionRefreshEvery))
	if n := len(provider.headers()); n != 1 {
		t.Fatalf("%d fetches once its period is up, want 1", n)
	}
}

// A failed refresh installs nothing, and is tried again after the retry pause.
func TestAFailedRefreshKeepsWhatRuns(t *testing.T) {
	var hits int
	down := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer down.Close()
	d := refreshRouter(t, down.URL, 7*time.Hour)
	d.subClient = down.Client()
	render, err := os.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	d.maybeRefreshSubscription(context.Background(), now)
	if hits == 0 {
		t.Fatal("the due refresh fetched nothing")
	}
	if after, _ := os.ReadFile(d.cfg.XrayRenderPath); string(after) != string(render) {
		t.Fatal("a failed refresh changed the render")
	}
	before := hits
	d.maybeRefreshSubscription(context.Background(), now.Add(subscriptionRetryAfter-time.Second))
	if hits != before {
		t.Fatal("a failed refresh was retried before the retry pause")
	}
	d.maybeRefreshSubscription(context.Background(), now.Add(subscriptionRetryAfter))
	if hits == before {
		t.Fatal("a failed refresh was not retried after the pause")
	}
}

// No data plane, no refresh: a router that runs none has nothing to keep fresh.
func TestNoRefreshWithoutARender(t *testing.T) {
	provider := newProviderStub(t, providerEntry(t))
	d := refreshRouter(t, provider.URL, 7*time.Hour)
	d.subClient = provider.Client()
	if err := os.Remove(d.cfg.XrayRenderPath); err != nil {
		t.Fatal(err)
	}
	d.maybeRefreshSubscription(context.Background(), time.Now())
	if n := len(provider.headers()); n != 0 {
		t.Fatalf("%d fetches with no render, want 0", n)
	}
}

// The refresh job and the daemon's own refresh are one path.
func TestTheRefreshJobErrorsNameTheirStep(t *testing.T) {
	d := refreshRouter(t, "https://127.0.0.1:1/sub", 7*time.Hour)
	_, _, err := d.refreshSubscription(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "refresh: ") {
		t.Fatalf("err = %v, want a refresh: error", err)
	}
}

// The data plane goes back in when it should be loaded and is not — a
// commit-confirm revert, a flush — and only then.
func TestAMissingDataPlaneIsLoadedAgain(t *testing.T) {
	provider := newProviderStub(t, providerEntry(t))
	d := refreshRouter(t, provider.URL, time.Hour)
	d.supStarted = true
	loaded := false
	d.tableLoaded = func(context.Context, string) bool { return loaded }
	ctx := context.Background()

	if !d.dataPlaneMissing(ctx) {
		t.Fatal("a configured router in proxy mode, xray running, no table: not seen as missing")
	}
	loaded = true
	if d.dataPlaneMissing(ctx) {
		t.Fatal("a loaded table seen as missing")
	}
	loaded = false

	// Direct mode — the rescue's or the operator's — takes it down on purpose.
	d.st.Rescue.Mode = string(rescue.ModeDirect)
	if d.dataPlaneMissing(ctx) {
		t.Fatal("direct mode's missing table would be loaded again")
	}
	d.st.Rescue.Mode = string(rescue.ModeProxy)

	// No xray, no render, no config: nothing to steer traffic into.
	d.supStarted = false
	if d.dataPlaneMissing(ctx) {
		t.Fatal("loaded again with xray not running")
	}
	d.supStarted = true
	if err := os.Remove(d.cfg.XrayRenderPath); err != nil {
		t.Fatal(err)
	}
	if d.dataPlaneMissing(ctx) {
		t.Fatal("loaded again with no render")
	}
}
