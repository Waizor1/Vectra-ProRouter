package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"vectra-controller-pro/internal/routepolicy"
)

const routeTestdata = "../../internal/routepolicy/testdata"

// nativeWorld points the native store, PassWall's configuration and both geo
// directories into a temp dir, with PassWall's config = the fleet fixture.
func nativeWorld(t *testing.T, uciFixture string) (pwGeo string) {
	t.Helper()
	dir := t.TempDir()
	oldStore, oldGeo, oldRefresh, oldPW, oldGet := nativeStorePath, nativeGeoDir, nativeRefreshPath, passwallUCIFile, uciGet
	t.Cleanup(func() {
		nativeStorePath, nativeGeoDir, nativeRefreshPath, passwallUCIFile, uciGet = oldStore, oldGeo, oldRefresh, oldPW, oldGet
	})
	nativeStorePath = filepath.Join(dir, "etc/config/vectra_route")
	nativeGeoDir = filepath.Join(dir, "usr/share/vectra-controller-pro/route-geo")
	nativeRefreshPath = filepath.Join(dir, "etc/vectra-controller-pro/route-refresh.json")
	passwallUCIFile = filepath.Join(dir, "etc/config/passwall2")
	pwGeo = filepath.Join(dir, "usr/share/v2ray")
	uciGet = func(key string) string {
		if key == "passwall2.@global_rules[0].v2ray_location_asset" {
			return pwGeo
		}
		return ""
	}
	b, err := os.ReadFile(filepath.Join(routeTestdata, uciFixture))
	if err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string][]byte{
		passwallUCIFile:                     b,
		filepath.Join(pwGeo, "geoip.dat"):   []byte("geoip bytes"),
		filepath.Join(pwGeo, "geosite.dat"): []byte("geosite bytes"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return pwGeo
}

// nativeDaemon is a test daemon routing natively from an operator config
// with a tproxy inbound.
func nativeDaemon(t *testing.T, feed *feedStub) *daemon {
	t.Helper()
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	if err := os.WriteFile(d.cfg.XrayConfigPath, operatorConfigPointingAt(t, provider.URL), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := d.loadDesiredConfig()
	if err != nil {
		t.Fatal(err)
	}
	d.desired = cfg
	d.cfg.RouteSource = routeSourceNative
	if feed != nil {
		d.subClient = feed.Client()
	}
	return d
}

func TestStoreGetterReadsPassWallsKeys(t *testing.T) {
	secs, err := routepolicy.ParseUCI("config global 'vectra_global'\n\toption node 'myshunt'\nconfig global_rules\n\toption v 'x'\nconfig global_rules\n\toption v 'y'\n")
	if err != nil {
		t.Fatal(err)
	}
	get := storeGetter(secs)
	for key, want := range map[string]string{
		"passwall2.@global[0].node":    "myshunt",
		"passwall2.vectra_global.node": "myshunt",
		"passwall2.@global_rules[1].v": "y",
		"passwall2.@global_rules[2].v": "",
		"passwall2.@global[0].missing": "",
		"passwall2.nothing":            "",
		"passwall2.@global[x].node":    "",
	} {
		if got := get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// The first native render imports PassWall's configuration and geo files as
// they are, and makes exactly what the generator makes of them for the xray
// the router runs; PassWall's own files are left alone.
func TestNativeRenderImportsPassWallsPolicyOnce(t *testing.T) {
	pwGeo := nativeWorld(t, "fleet.uci")
	d := nativeDaemon(t, nil)
	got, err := d.generateNative(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := os.ReadFile(passwallUCIFile)
	store, err := os.ReadFile(nativeStorePath)
	if err != nil || !bytes.Equal(store, pw) {
		t.Fatalf("the store is not PassWall's configuration as it was (err %v)", err)
	}
	if st, _ := os.Stat(nativeStorePath); st.Mode().Perm() != 0o600 {
		t.Fatalf("the store holds the subscription's secrets: mode %v", st.Mode().Perm())
	}
	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		a, _ := os.ReadFile(filepath.Join(pwGeo, f))
		b, err := os.ReadFile(filepath.Join(nativeGeoDir, f))
		if err != nil || !bytes.Equal(a, b) {
			t.Fatalf("%s not imported (err %v)", f, err)
		}
	}
	secs, _ := routepolicy.ParseUCI(string(pw))
	args, _ := passwallArgs(storeGetter(secs), d.desired.Inbounds.Tproxy.Port, 10053, wanResolverOr(d))
	want, err := routepolicy.Generate(secs, args, "26.7.28")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, got, want) {
		t.Fatal("the native render is not the generator's for this policy and xray 26.7.28")
	}
	// A later render reads the store, whatever PassWall's file says now.
	if err := os.WriteFile(passwallUCIFile, []byte("config global 'g'\n\toption node 'other'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := d.generateNative(context.Background())
	if err != nil || !jsonEqual(t, again, got) {
		t.Fatalf("a second render did not come from the store (err %v)", err)
	}
	if d.routeAssetDir() != nativeGeoDir || d.geoAssetDir() != nativeGeoDir {
		t.Fatalf("xray reads geo files from %s / %s, not the store's own", d.routeAssetDir(), d.geoAssetDir())
	}
}

// With no store and nothing to import, nothing is made: what runs keeps
// running.
func TestNativeRenderWithoutAPolicyFails(t *testing.T) {
	nativeWorld(t, "fleet.uci")
	_ = os.Remove(passwallUCIFile)
	d := nativeDaemon(t, nil)
	if _, err := d.generateNative(context.Background()); err == nil || !strings.Contains(err.Error(), "no route policy") {
		t.Fatalf("err = %v", err)
	}
	if fileExists(nativeStorePath) {
		t.Fatal("a store was written without a policy")
	}
}

func wanResolverOr(d *daemon) string {
	if r := wanResolver(d.etcRoot()); r != "" {
		return r
	}
	return "8.8.8.8"
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}

// feedStub is the provider's plain subscription: whatever body is set, and
// every request's headers.
type feedStub struct {
	*httptest.Server
	mu   sync.Mutex
	body []byte
	reqs []http.Header
}

func newFeedStub(t *testing.T, body []byte) *feedStub {
	t.Helper()
	f := &feedStub{body: body}
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, r.Header.Clone())
		b := f.body
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(b)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *feedStub) set(b []byte) { f.mu.Lock(); f.body = b; f.mu.Unlock() }
func (f *feedStub) requests() []http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]http.Header(nil), f.reqs...)
}

// storeWithSubscription imports the refresh fixture and points its
// subscription at the stub.
func storeWithSubscription(t *testing.T, url string) {
	t.Helper()
	nativeWorld(t, "refresh-before.uci")
	b, _ := os.ReadFile(passwallUCIFile)
	b = bytes.Replace(b, []byte("/tmp/refresh-feed.txt"), []byte(url), 1)
	if err := os.WriteFile(passwallUCIFile, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := importPassWallPolicy(); err != nil {
		t.Fatal(err)
	}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(routeTestdata, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The refresh asks as the router itself — its own signed User-Agent and the
// device headers PassWall sends — applies the feed to the store (the slots
// on the refreshed nodes), leaves an unchanged feed alone, refuses the
// placeholder, and never puts the URL in an error.
func TestNativeRefreshAsksAsTheRouterAndKeepsTheSlots(t *testing.T) {
	feed := newFeedStub(t, readTestdata(t, "refresh-feed-a.txt"))
	storeWithSubscription(t, feed.URL+"/api/sub/SECRETSHORT")
	d := nativeDaemon(t, feed)
	ctx := context.Background()

	rep, err := d.refreshNative(ctx, "sub1", false)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Nodes != 9 || len(rep.Rebound) != 5 {
		t.Fatalf("report: %+v", rep)
	}
	h := feed.requests()[0]
	if ua := h.Get("User-Agent"); !strings.HasPrefix(ua, "VectraRouter/") || !strings.Contains(ua, " vr1.") {
		t.Fatalf("User-Agent %q: not the router's own signed one", ua)
	}
	if h.Get("x-hwid") != d.device.HWID || h.Get("x-device-os") != "OpenWrt" || h.Get("x-device-model") != d.device.Model {
		t.Fatalf("device headers: %v", h)
	}
	store, _ := os.ReadFile(nativeStorePath)
	secs, err := routepolicy.ParseUCI(string(store))
	if err != nil {
		t.Fatal(err)
	}
	get := storeGetter(secs)
	nl := get("passwall2.myshunt.WorldProxy")
	if name := get("passwall2." + nl + ".remarks"); name != "🇷🇺🇳🇱 Нидерланды" || nl == "nodeNL01" {
		t.Fatalf("WorldProxy -> %s (%q)", nl, name)
	}
	if st, _ := os.Stat(nativeStorePath); st.Mode().Perm() != 0o600 {
		t.Fatalf("store mode %v", st.Mode().Perm())
	}

	// The same answer again: nothing written.
	before, _ := os.ReadFile(nativeStorePath)
	if _, err := d.refreshNative(ctx, "sub1", false); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(nativeStorePath); !bytes.Equal(before, after) {
		t.Fatal("an unchanged feed rewrote the store")
	}
	// Forced (the refresh job): applied again, the slots where they were.
	if _, err := d.refreshNative(ctx, "sub1", true); err != nil {
		t.Fatal(err)
	}
	store, _ = os.ReadFile(nativeStorePath)
	secs, _ = routepolicy.ParseUCI(string(store))
	if name := storeGetter(secs)("passwall2." + storeGetter(secs)("passwall2.myshunt.WorldProxy") + ".remarks"); name != "🇷🇺🇳🇱 Нидерланды" {
		t.Fatalf("after a forced refresh WorldProxy is on %q", name)
	}

	// The placeholder: refused, the store as it was.
	feed.set(readTestdata(t, "refresh-feed-stub.txt"))
	before, _ = os.ReadFile(nativeStorePath)
	if _, err := d.refreshNative(ctx, "sub1", true); !errors.Is(err, routepolicy.ErrEmptyFeed) {
		t.Fatalf("placeholder: err %v", err)
	}
	if after, _ := os.ReadFile(nativeStorePath); !bytes.Equal(before, after) {
		t.Fatal("the placeholder changed the store")
	}

	// A failing provider: the error names no URL.
	feed.Close()
	_, err = d.refreshNative(ctx, "sub1", true)
	if err == nil || strings.Contains(err.Error(), "SECRETSHORT") {
		t.Fatalf("a dead provider: %v", err)
	}
}

func TestNativeScheduleIsPassWalls(t *testing.T) {
	sec := func(opts map[string]string) routepolicy.Section {
		return routepolicy.Section{Type: "subscribe_list", Options: opts}
	}
	loc := time.FixedZone("MSK", 3*3600)
	at := func(day, h, m int) time.Time { return time.Date(2026, 9, day, h, m, 0, 0, loc) } // 2026-09-27 is a Sunday
	// 26.8.10 reads update_week_mode/update_time_mode alone: not auto_update.
	daily := scheduleOf(sec(map[string]string{"auto_update": "0", "update_week_mode": "7", "update_time_mode": "0:00", "week_update": "7", "time_update": "5"}))
	for _, c := range []struct {
		last, now time.Time
		want      bool
	}{
		{at(28, 23, 59), at(29, 0, 1), true}, // midnight passed since
		{at(29, 0, 0), at(29, 13, 0), false}, // done today
		{at(27, 12, 0), at(29, 13, 0), true}, // missed days: once, now
		{time.Time{}, at(29, 13, 0), false},  // no record: the caller starts one
	} {
		if got := daily.due(c.last, c.now); got != c.want {
			t.Errorf("daily 0:00 last %v now %v: %v, want %v", c.last, c.now, got, c.want)
		}
	}
	// The older format: scheduled by vctl when auto_update is on.
	legacy := scheduleOf(sec(map[string]string{"auto_update": "1", "week_update": "0", "time_update": "5"}))
	if !legacy.due(at(26, 12, 0), at(27, 5, 1)) || legacy.due(at(26, 12, 0), at(27, 4, 59)) || legacy.due(at(27, 5, 30), at(28, 6, 0)) {
		t.Error("Sundays at 5: wrong")
	}
	if scheduleOf(sec(map[string]string{"week_update": "7", "time_update": "5"})).on {
		t.Error("the older format without auto_update: scheduled")
	}
	every := scheduleOf(sec(map[string]string{"update_week_mode": "8", "update_interval_mode": "6"}))
	if every.due(at(29, 1, 0), at(29, 6, 59)) || !every.due(at(29, 1, 0), at(29, 7, 0)) {
		t.Error("every 6 hours: wrong")
	}
	for name, opts := range map[string]map[string]string{
		"nothing":        {},
		"no interval":    {"update_week_mode": "8"},
		"a week of nine": {"update_week_mode": "9", "update_time_mode": "1:00"},
		"no time":        {"update_week_mode": "7", "update_time_mode": "noon"},
		"hour 24":        {"update_week_mode": "7", "update_time_mode": "24:00"},
	} {
		if scheduleOf(sec(opts)).on {
			t.Errorf("%s: scheduled", name)
		}
	}
	if !scheduleOf(sec(map[string]string{"boot_update": "1"})).atBoot {
		t.Error("boot_update not read")
	}
}

// The daemon's own schedule: asks when due, and a failure waits the retry
// pause without touching the store.
func TestTheDaemonRefreshesThePolicyOnSchedule(t *testing.T) {
	feed := newFeedStub(t, readTestdata(t, "refresh-feed-a.txt"))
	storeWithSubscription(t, feed.URL)
	b, _ := os.ReadFile(nativeStorePath)
	b = bytes.Replace(b, []byte("option add_mode '2'\n\toption access_mode"), []byte("option add_mode '2'\n\toption auto_update '1'\n\toption update_week_mode '7'\n\toption update_time_mode '0:00'\n\toption access_mode"), 1)
	if err := os.WriteFile(nativeStorePath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	d := nativeDaemon(t, feed)
	ctx := context.Background()
	now := time.Now()
	yesterday := now.Add(-26 * time.Hour)
	if err := saveRefreshTimes(map[string]time.Time{"sub1": yesterday}); err != nil {
		t.Fatal(err)
	}
	d.maybeRefreshNative(ctx, now)
	if n := len(feed.requests()); n != 1 {
		t.Fatalf("%d requests when a midnight passed since the last, want 1", n)
	}
	d.maybeRefreshNative(ctx, now.Add(time.Minute))
	if n := len(feed.requests()); n != 1 {
		t.Fatalf("%d requests a minute later, want still 1", n)
	}
	if last := loadRefreshTimes()["sub1"]; last.Before(now.Add(-time.Minute)) {
		t.Fatalf("the refresh was not remembered: %v", last)
	}
}

// withSchedule adds scheduling options to the store's subscription.
func withSchedule(t *testing.T, opts string) {
	t.Helper()
	b, _ := os.ReadFile(nativeStorePath)
	b = bytes.Replace(b, []byte("option add_mode '2'\n\toption access_mode"), []byte("option add_mode '2'\n"+opts+"\toption access_mode"), 1)
	if err := os.WriteFile(nativeStorePath, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A router that takes native routing with no record asks at the next
// scheduled time, not at once; one with boot_update asks at its first loop;
// a refused answer (the placeholder) waits nativeRefusedRetry, not a day.
func TestNativeRefreshStartsItsRecordAndRetries(t *testing.T) {
	feed := newFeedStub(t, readTestdata(t, "refresh-feed-a.txt"))
	storeWithSubscription(t, feed.URL)
	withSchedule(t, "\toption update_week_mode '7'\n\toption update_time_mode '0:00'\n")
	d := nativeDaemon(t, feed)
	ctx := context.Background()
	now := time.Now()
	d.maybeRefreshNative(ctx, now)
	if n := len(feed.requests()); n != 0 {
		t.Fatalf("%d requests at the first loop with no record and no boot_update", n)
	}
	if loadRefreshTimes()["sub1"].IsZero() {
		t.Fatal("the record was not started")
	}
	d.maybeRefreshNative(ctx, now.Add(25*time.Hour))
	if n := len(feed.requests()); n != 1 {
		t.Fatalf("%d requests a day later, want 1", n)
	}

	// The placeholder, due: refused, retried after nativeRefusedRetry.
	feed.set(readTestdata(t, "refresh-feed-stub.txt"))
	later := now.Add(49 * time.Hour)
	d.maybeRefreshNative(ctx, later)
	if n := len(feed.requests()); n != 2 {
		t.Fatalf("%d requests, want 2", n)
	}
	d.maybeRefreshNative(ctx, later.Add(nativeRefreshRetry+time.Minute))
	if n := len(feed.requests()); n != 2 {
		t.Fatal("a refused answer asked for again after half an hour")
	}
	d.maybeRefreshNative(ctx, later.Add(nativeRefusedRetry+time.Minute))
	if n := len(feed.requests()); n != 3 {
		t.Fatalf("%d requests after the refusal's pause, want 3", n)
	}
}

func TestNativeBootUpdateAsksAtTheFirstLoop(t *testing.T) {
	feed := newFeedStub(t, readTestdata(t, "refresh-feed-a.txt"))
	storeWithSubscription(t, feed.URL)
	withSchedule(t, "\toption boot_update '1'\n")
	d := nativeDaemon(t, feed)
	d.maybeRefreshNative(context.Background(), time.Now())
	if n := len(feed.requests()); n != 1 {
		t.Fatalf("%d requests at the first loop with boot_update, want 1", n)
	}
	d.maybeRefreshNative(context.Background(), time.Now().Add(time.Hour))
	if n := len(feed.requests()); n != 1 {
		t.Fatal("boot_update asked again at a later loop")
	}
}
