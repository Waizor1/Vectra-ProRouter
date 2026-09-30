package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/routepolicy"
)

// geoFile is a geo file with these categories and nothing in them: an entry
// (field 1) per code, named by its field 1.
func geoFile(codes ...string) []byte {
	var out []byte
	for _, c := range codes {
		name := append([]byte{0x0a, byte(len(c))}, c...)
		out = append(out, 0x0a, byte(len(name)))
		out = append(out, name...)
	}
	return out
}

type geoServer struct {
	*httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  int
	asked map[string]int
}

func newGeoServer(t *testing.T) *geoServer {
	t.Helper()
	g := &geoServer{files: map[string][]byte{}, asked: map[string]int{}}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.hits++
		g.asked[r.URL.Path]++
		b, ok := g.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(g.Close)
	return g
}

func (g *geoServer) set(path string, b []byte) { g.mu.Lock(); g.files[path] = b; g.mu.Unlock() }

// geoPolicy is a policy whose rules name geoip:DIRECT, geosite:YOUTUBE and
// geosite:russia-outside, fetched from the server.
func geoPolicy(t *testing.T, base string) []routepolicy.Section {
	t.Helper()
	secs, err := routepolicy.ParseUCI(`
config global_rules 'r'
	option geoip_url '` + base + `/geoip.dat'
	option geosite_url '` + base + `/geosite.dat'
	option auto_update '1'
	option week_update '7'
	option time_update '6'

config shunt_rules 'direct'
	option domain_list 'domain:mos.ru
geosite:russia-outside'
	option ip_list 'geoip:DIRECT'

config shunt_rules 'YouTube'
	option domain_list 'geosite:YOUTUBE@ads'
`)
	if err != nil {
		t.Fatal(err)
	}
	return secs
}

func TestPolicyGeoCodes(t *testing.T) {
	ip, site := policyGeoCodes(geoPolicy(t, "http://x"))
	if !reflect.DeepEqual(ip, []string{"DIRECT"}) || !reflect.DeepEqual(site, []string{"RUSSIA-OUTSIDE", "YOUTUBE"}) {
		t.Fatalf("geoip %v, geosite %v", ip, site)
	}
}

// A new pair of files is taken; a file lacking a category the policy routes
// by, or not matching its published sha256, is refused and nothing moves;
// the same files again change nothing.
func TestNativeGeoUpdate(t *testing.T) {
	nativeWorld(t, "fleet.uci")
	g := newGeoServer(t)
	secs := geoPolicy(t, g.URL)
	d := nativeDaemon(t, nil)
	ctx := context.Background()
	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		if err := os.MkdirAll(nativeGeoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nativeGeoDir, f), []byte("old "+f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(f string) []byte { b, _ := os.ReadFile(filepath.Join(nativeGeoDir, f)); return b }

	ipOK, siteOK := geoFile("PRIVATE", "DIRECT"), geoFile("YOUTUBE", "russia-outside", "META")
	g.set("/geoip.dat", ipOK)
	g.set("/geosite.dat", geoFile("YOUTUBE")) // no RUSSIA-OUTSIDE
	if _, err := d.updateNativeGeo(ctx, secs, false); err == nil || !strings.Contains(err.Error(), "RUSSIA-OUTSIDE") {
		t.Fatalf("a geosite without a routed category: %v", err)
	}
	if string(read("geoip.dat")) != "old geoip.dat" {
		t.Fatal("geoip.dat was replaced although geosite.dat was refused")
	}

	g.set("/geosite.dat", siteOK)
	sum := sha256.Sum256([]byte("something else"))
	g.set("/geosite.dat.sha256sum", []byte(hex.EncodeToString(sum[:])+"  geosite.dat\n"))
	if _, err := d.updateNativeGeo(ctx, secs, false); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("a file not matching its sha256: %v", err)
	}
	sum = sha256.Sum256(siteOK)
	g.set("/geosite.dat.sha256sum", []byte(hex.EncodeToString(sum[:])+"  geosite.dat\n"))

	changed, err := d.updateNativeGeo(ctx, secs, false)
	if err != nil || !reflect.DeepEqual(changed, []string{"geoip.dat", "geosite.dat"}) {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if !bytes.Equal(read("geoip.dat"), ipOK) || !bytes.Equal(read("geosite.dat"), siteOK) {
		t.Fatal("the new files are not in place")
	}
	if changed, err := d.updateNativeGeo(ctx, secs, false); err != nil || len(changed) != 0 {
		t.Fatalf("the same files again: changed %v, err %v", changed, err)
	}
}

// A file whose switch is off is not fetched, as PassWall's rule_update.lua
// does not; both off, nothing is.
func TestNativeGeoFollowsItsSwitches(t *testing.T) {
	nativeWorld(t, "fleet.uci")
	g := newGeoServer(t)
	g.set("/geoip.dat", geoFile("DIRECT"))
	g.set("/geosite.dat", geoFile("RUSSIA-OUTSIDE", "YOUTUBE"))
	secs := geoPolicy(t, g.URL)
	rules, _ := firstOfType(secs, "global_rules")
	rules.Options["geoip_update"] = "0"
	rules.Options["geosite_update"] = "1"
	d := nativeDaemon(t, nil)
	changed, err := d.updateNativeGeo(context.Background(), secs, false)
	if err != nil || !reflect.DeepEqual(changed, []string{"geosite.dat"}) {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if g.asked["/geoip.dat"] != 0 {
		t.Fatal("geoip.dat was fetched with geoip_update '0'")
	}
	rules.Options["geosite_update"] = "0"
	hits := g.hits
	if changed, err := d.updateNativeGeo(context.Background(), secs, false); err != nil || len(changed) != 0 || g.hits != hits {
		t.Fatalf("both switches off: changed %v, err %v, requests %d", changed, err, g.hits-hits)
	}
	// Asked for, both files are fetched whatever the switches say.
	if changed, err := d.updateNativeGeo(context.Background(), secs, true); err != nil || !reflect.DeepEqual(changed, []string{"geoip.dat"}) || g.asked["/geoip.dat"] == 0 {
		t.Fatalf("asked for, with both switches off: changed %v, err %v, geoip requests %d", changed, err, g.asked["/geoip.dat"])
	}
}

// The daemon's schedule: due once a 06:00 has passed since the last update,
// and remembered.
func TestNativeGeoFollowsThePolicysSchedule(t *testing.T) {
	nativeWorld(t, "fleet.uci")
	g := newGeoServer(t)
	g.set("/geoip.dat", geoFile("DIRECT"))
	g.set("/geosite.dat", geoFile("RUSSIA-OUTSIDE", "YOUTUBE"))
	if err := os.MkdirAll(filepath.Dir(nativeStorePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativeStorePath, []byte(routepolicy.Export(geoPolicy(t, g.URL))), 0o600); err != nil {
		t.Fatal(err)
	}
	d := nativeDaemon(t, nil)
	now := time.Now()
	if err := saveRefreshTimes(map[string]time.Time{"geo": now.Add(-25 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	d.maybeUpdateNativeGeo(context.Background(), now)
	waitGeo(t, d)
	if g.hits == 0 {
		t.Fatal("a 06:00 passed since the last update and nothing was fetched")
	}
	if b, _ := os.ReadFile(filepath.Join(nativeGeoDir, "geoip.dat")); !bytes.Equal(b, geoFile("DIRECT")) {
		t.Fatal("the new geoip.dat is not in place")
	}
	hits := g.hits
	d.maybeUpdateNativeGeo(context.Background(), now.Add(time.Minute))
	waitGeo(t, d)
	if g.hits != hits {
		t.Fatal("fetched again a minute later")
	}
}

// waitGeo waits for the geo update's goroutine.
func waitGeo(t *testing.T, d *daemon) {
	t.Helper()
	for i := 0; i < 500 && d.nativeGeoBusy.Load(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if d.nativeGeoBusy.Load() {
		t.Fatal("the geo update never finished")
	}
}

// Without a record nothing is fetched at once: the record starts now, and
// the next 06:00 is when.
func TestNativeGeoStartsItsRecordFirst(t *testing.T) {
	nativeWorld(t, "fleet.uci")
	g := newGeoServer(t)
	g.set("/geoip.dat", geoFile("DIRECT"))
	g.set("/geosite.dat", geoFile("RUSSIA-OUTSIDE", "YOUTUBE"))
	if err := os.MkdirAll(filepath.Dir(nativeStorePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativeStorePath, []byte(routepolicy.Export(geoPolicy(t, g.URL))), 0o600); err != nil {
		t.Fatal(err)
	}
	d := nativeDaemon(t, nil)
	now := time.Now()
	d.maybeUpdateNativeGeo(context.Background(), now)
	waitGeo(t, d)
	if g.hits != 0 {
		t.Fatal("fetched at once, with no record")
	}
	if loadRefreshTimes()["geo"].IsZero() {
		t.Fatal("the record was not started")
	}
	d.maybeUpdateNativeGeo(context.Background(), now.Add(25*time.Hour))
	waitGeo(t, d)
	if g.hits == 0 {
		t.Fatal("a day later, nothing fetched")
	}
}

type failSums struct{ next http.RoundTripper }

func (f failSums) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, ".sha256sum") {
		return nil, errors.New("connection reset")
	}
	return f.next.RoundTrip(r)
}

// A published sha256 that cannot be asked for is not "none published":
// nothing is taken unchecked. And a download cut short by a power cut is
// cleared before the next one.
func TestNativeGeoNeverTakesAFileUnchecked(t *testing.T) {
	nativeWorld(t, "fleet.uci")
	g := newGeoServer(t)
	g.set("/geoip.dat", geoFile("DIRECT"))
	g.set("/geosite.dat", geoFile("RUSSIA-OUTSIDE", "YOUTUBE"))
	old := nativeGeoClient
	t.Cleanup(func() { nativeGeoClient = old })
	nativeGeoClient = func() *http.Client { return &http.Client{Transport: failSums{http.DefaultTransport}} }
	d := nativeDaemon(t, nil)
	if _, err := d.updateNativeGeo(context.Background(), geoPolicy(t, g.URL), false); err == nil || !strings.Contains(err.Error(), "published sha256") {
		t.Fatalf("err = %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(nativeGeoDir, ".*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}

	nativeGeoClient = old
	if err := os.WriteFile(filepath.Join(nativeGeoDir, ".geosite.dat.new123"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(nativeStorePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativeStorePath, []byte(routepolicy.Export(geoPolicy(t, g.URL))), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveRefreshTimes(map[string]time.Time{"geo": time.Now().Add(-25 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	d.maybeUpdateNativeGeo(context.Background(), time.Now())
	waitGeo(t, d)
	if _, err := os.Stat(filepath.Join(nativeGeoDir, ".geosite.dat.new123")); !os.IsNotExist(err) {
		t.Fatal("a download cut short is still on the flash")
	}
}

// The panel's geo update job, in native routing, is the route policy's own
// update — its sources, its checks, its directory — not the operator
// config's URLs; in PassWall mode it refuses: those files are PassWall's.
func TestTheGeoJobFollowsTheRouteSource(t *testing.T) {
	nativeWorld(t, "fleet.uci")
	g := newGeoServer(t)
	g.set("/geoip.dat", geoFile("DIRECT"))
	g.set("/geosite.dat", geoFile("RUSSIA-OUTSIDE", "YOUTUBE"))
	if err := os.MkdirAll(filepath.Dir(nativeStorePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nativeStorePath, []byte(routepolicy.Export(geoPolicy(t, g.URL))), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, t.TempDir(), panel, provider)
	ctx := context.Background()

	d.cfg.RouteSource = routeSourceNative
	if err := d.jobUpdateAssets(ctx, controlplane.Job{ID: "geo-1", Type: "update_xray_assets"}); err != nil {
		t.Fatal(err)
	}
	// Answered at once: the update runs beside the loop.
	if res := panel.resultsFor("geo-1"); len(res) != 1 || res[0].Status != "success" || res[0].Result["started"] != true {
		t.Fatalf("results %+v", res)
	}
	waitGeo(t, d)
	if b, _ := os.ReadFile(filepath.Join(nativeGeoDir, "geosite.dat")); !bytes.Equal(b, geoFile("RUSSIA-OUTSIDE", "YOUTUBE")) {
		t.Fatal("the policy's geosite.dat is not in the route policy's directory")
	}
	if loadRefreshTimes()["geo"].IsZero() {
		t.Fatal("the update was not recorded: the schedule would fetch again at once")
	}
	// A request while an update runs is refused, not queued behind it.
	d.nativeGeoBusy.Store(true)
	if err := d.jobUpdateAssets(ctx, controlplane.Job{ID: "geo-busy", Type: "update_xray_assets"}); err != nil {
		t.Fatal(err)
	}
	d.nativeGeoBusy.Store(false)
	if res := panel.resultsFor("geo-busy"); len(res) != 1 || res[0].Status != "failure" {
		t.Fatalf("while one runs: results %+v", res)
	}

	d.cfg.RouteSource = routeSourcePassWall
	if err := d.jobUpdateAssets(ctx, controlplane.Job{ID: "geo-2", Type: "update_xray_assets"}); err != nil {
		t.Fatal(err)
	}
	if res := panel.resultsFor("geo-2"); len(res) != 1 || res[0].Status != "failure" {
		t.Fatalf("PassWall mode: results %+v", res)
	}
}
