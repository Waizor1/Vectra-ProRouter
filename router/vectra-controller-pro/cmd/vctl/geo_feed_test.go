package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
)

// geoFeedStand is the signed feed stand publishing a geo set, geoFeedDir and
// the running files in the test's directories.
type geoFeedStand struct {
	*signedFeedStand
	current string // the directory the running xray reads
}

func newGeoFeedStand(t *testing.T) *geoFeedStand {
	t.Helper()
	s := newSignedFeedStand(t)
	prevDir, prevClient := geoFeedDir, catalogHTTPClient
	geoFeedDir = filepath.Join(t.TempDir(), "geo")
	catalogHTTPClient = func() *http.Client { return s.srv.Client() }
	t.Cleanup(func() { geoFeedDir, catalogHTTPClient = prevDir, prevClient })
	g := &geoFeedStand{signedFeedStand: s, current: filepath.Join(t.TempDir(), "pkg-geo")}
	writeFeedFile(t, filepath.Join(g.current, "geosite.dat"), string(geoFile("YOUTUBE", "CATEGORY-RU")))
	writeFeedFile(t, filepath.Join(g.current, "geoip.dat"), string(geoFile("RU", "PRIVATE")))
	s.mu.Lock()
	s.extra = map[string][]byte{}
	s.mu.Unlock()
	return g
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// publish puts a set on the channel: the files and geo.json, signed by sign
// (nil: the feed's key).
func (g *geoFeedStand) publish(t *testing.T, serial int64, site, ip []byte, mutate func(*geoManifest), sign func([]byte) []byte) []byte {
	t.Helper()
	root := path.Dir(feedPath)
	m := geoManifest{Schema: 1, Serial: serial, BuiltAt: "2026-10-17T22:00:00Z", Files: map[string]geoManifestFile{
		"geosite.dat": {Path: "geo/geosite-" + sha(site)[:12] + ".dat", SHA256: sha(site), Size: int64(len(site)), Codes: []string{"CATEGORY-RU", "YOUTUBE"}},
		"geoip.dat":   {Path: "geo/geoip-" + sha(ip)[:12] + ".dat", SHA256: sha(ip), Size: int64(len(ip)), Codes: []string{"PRIVATE", "RU"}},
	}}
	if mutate != nil {
		mutate(&m)
	}
	raw, _ := json.Marshal(m)
	if sign == nil {
		sign = g.key.sign
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.extra[root+"/"+m.Files["geosite.dat"].Path] = site
	g.extra[root+"/"+m.Files["geoip.dat"].Path] = ip
	g.extra[root+"/geo.json"] = raw
	g.extra[root+"/geo.json.sig"] = sign(raw)
	return raw
}

func (g *geoFeedStand) fetch(ctx context.Context, u string, max int64) ([]byte, error) {
	return fetchHTTPSWith(ctx, g.srv.Client(), u, max)
}

func TestTheGeoFeedPutsACheckedSetInPlace(t *testing.T) {
	g := newGeoFeedStand(t)
	site, ip := geoFile("YOUTUBE", "CATEGORY-RU", "GEOBLOCK"), geoFile("RU", "PRIVATE", "TELEGRAM")
	g.publish(t, 100, site, ip, nil, nil)
	var tested []string
	test := func(_ context.Context, dir string) error {
		for _, f := range geoFeedFiles {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				return err
			}
		}
		tested = append(tested, dir)
		return nil
	}
	serial, err := updateGeoFeed(context.Background(), g.fetch, g.current, test)
	if err != nil || serial != 100 {
		t.Fatalf("serial %d err %v", serial, err)
	}
	if len(tested) != 1 || filepath.Dir(tested[0]) != geoFeedDir {
		t.Fatalf("xray -test ran on %v, want a staging directory beside %s", tested, geoFeedDir)
	}
	for name, want := range map[string][]byte{"geosite.dat": site, "geoip.dat": ip} {
		if b, _ := os.ReadFile(filepath.Join(geoFeedDir, name)); !bytes.Equal(b, want) {
			t.Fatalf("%s not in place", name)
		}
	}
	if m, ok := storedGeoManifest(); !ok || m.Serial != 100 {
		t.Fatalf("stored manifest %+v %v", m, ok)
	}
	if _, err := os.Stat(filepath.Join(geoFeedDir, ".new")); !os.IsNotExist(err) {
		t.Fatal("the staging directory stayed")
	}
	// The same set again: nothing fetched, nothing changed.
	if serial, err := updateGeoFeed(context.Background(), g.fetch, g.current, test); err != nil || serial != 0 || len(tested) != 1 {
		t.Fatalf("again: serial %d err %v tests %d", serial, err, len(tested))
	}
}

// A set is taken only whole and checked: the feed key's, the files the
// manifest names, every category in use kept, and the running configuration
// accepted by xray with it. A refusal leaves the set in use as it was.
func TestTheGeoFeedRefusesASetThatFailsACheck(t *testing.T) {
	site, ip := geoFile("YOUTUBE", "CATEGORY-RU"), geoFile("RU", "PRIVATE")
	cases := map[string]func(g *geoFeedStand) (geoFeedTester, string){
		"another key": func(g *geoFeedStand) (geoFeedTester, string) {
			other := newFeedKey(9)
			writeFeedFile(t, filepath.Join(g.keys, other.id()), other.pubFile())
			g.publish(t, 100, site, ip, nil, other.sign)
			return nil, "signed by"
		},
		"wrong sha256": func(g *geoFeedStand) (geoFeedTester, string) {
			g.publish(t, 100, site, ip, func(m *geoManifest) {
				f := m.Files["geoip.dat"]
				f.SHA256 = strings.Repeat("0", 64)
				m.Files["geoip.dat"] = f
			}, nil)
			return nil, "sha256"
		},
		"a category in use gone": func(g *geoFeedStand) (geoFeedTester, string) {
			g.publish(t, 100, geoFile("YOUTUBE"), ip, func(m *geoManifest) {
				f := m.Files["geosite.dat"]
				f.Codes = []string{"YOUTUBE"}
				m.Files["geosite.dat"] = f
			}, nil)
			return nil, "CATEGORY-RU"
		},
		"xray refuses": func(g *geoFeedStand) (geoFeedTester, string) {
			g.publish(t, 100, site, ip, nil, nil)
			return func(context.Context, string) error { return errors.New("xray: failed to load geosite") }, "failed to load"
		},
		"a path out of the channel": func(g *geoFeedStand) (geoFeedTester, string) {
			g.publish(t, 100, site, ip, func(m *geoManifest) {
				f := m.Files["geosite.dat"]
				f.Path = "../../etc/passwd"
				m.Files["geosite.dat"] = f
			}, nil)
			return nil, "path"
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			g := newGeoFeedStand(t)
			test, want := setup(g)
			serial, err := updateGeoFeed(context.Background(), g.fetch, g.current, test)
			if err == nil || serial != 0 || !strings.Contains(err.Error(), want) {
				t.Fatalf("serial %d err %v, want an error with %q", serial, err, want)
			}
			if _, ok := storedGeoManifest(); ok {
				t.Fatal("a refused set counts as in place")
			}
			for _, f := range geoFeedFiles {
				if _, err := os.Stat(filepath.Join(geoFeedDir, f)); !os.IsNotExist(err) {
					t.Fatalf("%s of a refused set was put in place", f)
				}
			}
		})
	}
}

// An older manifest than the set in use is never taken (a replay).
func TestTheGeoFeedNeverGoesBack(t *testing.T) {
	g := newGeoFeedStand(t)
	g.publish(t, 200, geoFile("YOUTUBE", "CATEGORY-RU"), geoFile("RU", "PRIVATE"), nil, nil)
	if s, err := updateGeoFeed(context.Background(), g.fetch, g.current, nil); err != nil || s != 200 {
		t.Fatalf("%d %v", s, err)
	}
	g.publish(t, 150, geoFile("YOUTUBE", "CATEGORY-RU", "OLD"), geoFile("RU", "PRIVATE"), nil, nil)
	if s, err := updateGeoFeed(context.Background(), g.fetch, g.current, nil); err != nil || s != 0 {
		t.Fatalf("an older set: %d %v", s, err)
	}
	if m, _ := storedGeoManifest(); m.Serial != 200 {
		t.Fatalf("in use %d", m.Serial)
	}
}

// The daemon asks after its start, takes a finished set at its next poll and
// renders from the feed's directory from then on; the job asks at once.
func TestTheDaemonTakesTheGeoFeedsSet(t *testing.T) {
	g := newGeoFeedStand(t)
	prevFiles := config.GeoFilesAt
	config.GeoFilesAt = func(dir string) bool { return dir == geoFeedDir || prevFiles(dir) }
	t.Cleanup(func() { config.GeoFilesAt = prevFiles })
	g.publish(t, 300, geoFile("YOUTUBE", "CATEGORY-RU"), geoFile("RU", "PRIVATE"), nil, nil)
	d := &daemon{cfg: g.cfgFor(t)}
	now := time.Now()
	d.maybeUpdateGeoFeed(context.Background(), now)
	if d.geoFeedBusy.Load() || !d.nextGeoFeed.Equal(now.Add(geoFeedFirstAfter)) {
		t.Fatal("asked at once after a start")
	}
	d.startGeoFeedNow()
	d.maybeUpdateGeoFeed(context.Background(), time.Now())
	deadline := time.Now().Add(10 * time.Second)
	for d.geoFeedBusy.Load() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	d.maybeUpdateGeoFeed(context.Background(), time.Now())
	if d.geoFeedSerial != 300 || d.geoAssetDir() != geoFeedDir || d.geoFeedKey() != ";geofeed=300" {
		t.Fatalf("serial %d dir %s key %q", d.geoFeedSerial, d.geoAssetDir(), d.geoFeedKey())
	}
}

func (g *geoFeedStand) cfgFor(t *testing.T) agentcfg.Config {
	return agentcfg.Config{XrayRenderPath: filepath.Join(t.TempDir(), "xray.json")}
}
