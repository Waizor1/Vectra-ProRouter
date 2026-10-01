package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"vectra-controller-pro/internal/controlplane"
)

// feedKey is a usign key as feedtool keygen makes one (tools/feedtool/usign.go).
// Obviously fake: its seed is one byte repeated.
type feedKey struct {
	number [8]byte
	priv   ed25519.PrivateKey
}

func newFeedKey(seed byte) feedKey {
	k := feedKey{priv: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))}
	for i := range k.number {
		k.number[i] = seed*16 + byte(i)
	}
	return k
}

func (k feedKey) id() string { return fmt.Sprintf("%016x", binary.BigEndian.Uint64(k.number[:])) }

// pubFile is the key as install/install.sh files it in /etc/opkg/keys.
func (k feedKey) pubFile() string {
	blob := append(append([]byte("Ed"), k.number[:]...), k.priv.Public().(ed25519.PublicKey)...)
	return "untrusted comment: Vectra Pro feed\n" + base64.StdEncoding.EncodeToString(blob) + "\n"
}

// sign is feedtool sign (usign -S); signAs a signature by k that names
// another key's number.
func (k feedKey) sign(msg []byte) []byte { return k.signAs(k.number, msg) }

func (k feedKey) signAs(number [8]byte, msg []byte) []byte {
	blob := append(append([]byte("Ed"), number[:]...), ed25519.Sign(k.priv, msg)...)
	return []byte("untrusted comment: signed by key " + k.id() + "\n" + base64.StdEncoding.EncodeToString(blob) + "\n")
}

const (
	feedArch    = "aarch64_cortex-a53"
	feedVersion = "0.6.0-r37"
	feedPath    = "/artifacts/openwrt/pro-canary/" + feedArch
	artifact    = "/artifacts/vectra-controller-pro_" + feedVersion + "_" + feedArch + ".ipk"
)

// feedStanza is a package of the index as feedtool index writes it.
func feedStanza(pkg, version, arch, sha string) string {
	return "Package: " + pkg + "\nVersion: " + version + "\n" +
		"Depends: ca-bundle, xray-core (>= 26.3.27), vectra-geodata, vectra-reporter\n" +
		"Architecture: " + arch + "\nInstalled-Size: 31457280\n" +
		"Filename: " + pkg + "_" + version + "_" + arch + ".ipk\nSize: 10485760\nSHA256sum: " + sha + "\n" +
		"Description: stands in for " + pkg + "\n in update_controller's tests.\n\n"
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func writeFeedFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// signedFeedStand is the Vectra feed as scripts/build-pro-feed.sh and
// sign-pro-feed.sh publish it, served over https next to the panel's artifact
// store, and the router as install/install.sh leaves it: the feed's line in
// customfeeds.conf, the feed's key in /etc/opkg/keys, the router's
// architecture in /etc/openwrt_release. opkg and the restart are stood in.
type signedFeedStand struct {
	srv *httptest.Server
	key feedKey
	ipk []byte // the package the panel's job names
	sha string

	mu    sync.Mutex
	index []byte // Packages as served; Packages.gz is it, packed
	sig   []byte // Packages.sig as served
	hits  map[string]int

	conf, keys, release string

	installed []string // the sha256 of each package opkg was given
	restarts  int
}

func newSignedFeedStand(t *testing.T) *signedFeedStand {
	t.Helper()
	oldCommand := maintenanceCommand
	t.Cleanup(func() { maintenanceCommand = oldCommand })
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "opkg" || len(args) != 2 || args[0] != "status" || args[1] != proPackageName {
			return nil, errors.New("unexpected fake opkg command")
		}
		return []byte("Version: " + feedVersion + "\n"), nil
	}
	s := &signedFeedStand{key: newFeedKey(7), hits: map[string]int{}}
	s.ipk = []byte("stands in for vectra-controller-pro " + feedVersion)
	s.sha = sha256Hex(s.ipk)
	s.signIndex(feedStanza("vectra-controller-pro", feedVersion, feedArch, s.sha) +
		feedStanza("vectra-geodata", "2026.9.28-r2", "all", strings.Repeat("2", 64)))
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)

	dir := t.TempDir()
	s.conf = filepath.Join(dir, "etc", "opkg", "customfeeds.conf")
	s.keys = filepath.Join(dir, "etc", "opkg", "keys")
	s.release = filepath.Join(dir, "etc", "openwrt_release")
	writeFeedFile(t, s.conf, "# add your custom package feeds here\nsrc/gz vectra_pro "+s.srv.URL+feedPath+"\n")
	writeFeedFile(t, filepath.Join(s.keys, s.key.id()), s.key.pubFile())
	writeFeedFile(t, s.release, "DISTRIB_ID='OpenWrt'\nDISTRIB_RELEASE='24.10.6'\nDISTRIB_ARCH='"+feedArch+"'\n")
	// The download lands in os.TempDir(): this test's.
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)

	prevConf, prevKeys, prevRelease := vectraFeedConf, opkgKeysDir, openwrtReleasePath
	prevClient, prevInstall, prevRestart := updateHTTPClient, runControllerInstall, scheduleControllerRestart
	vectraFeedConf, opkgKeysDir, openwrtReleasePath = s.conf, s.keys, s.release
	updateHTTPClient = func() *http.Client { return s.srv.Client() }
	runControllerInstall = func(_ context.Context, dest string) ([]byte, error) {
		b, err := os.ReadFile(dest)
		if err != nil {
			return nil, err
		}
		s.installed = append(s.installed, sha256Hex(b))
		return []byte("Installing vectra-controller-pro (stand-in)\n"), nil
	}
	scheduleControllerRestart = func() { s.restarts++ }
	t.Cleanup(func() {
		vectraFeedConf, opkgKeysDir, openwrtReleasePath = prevConf, prevKeys, prevRelease
		updateHTTPClient, runControllerInstall, scheduleControllerRestart = prevClient, prevInstall, prevRestart
	})
	return s
}

// signIndex serves index, signed with the feed's key.
func (s *signedFeedStand) signIndex(index string) {
	s.serveIndex([]byte(index), s.key.sign([]byte(index)))
}

// serveIndex serves index and sig as they are.
func (s *signedFeedStand) serveIndex(index, sig []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index, s.sig = index, sig
}

func (s *signedFeedStand) served() (index []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.index
}

func (s *signedFeedStand) hit(p string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[p]
}

func (s *signedFeedStand) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.hits[r.URL.Path]++
	index, sig := s.index, s.sig
	s.mu.Unlock()
	switch r.URL.Path {
	case feedPath + "/Packages":
		_, _ = w.Write(index)
	case feedPath + "/Packages.gz":
		var b bytes.Buffer
		zw := gzip.NewWriter(&b)
		_, _ = zw.Write(index)
		_ = zw.Close()
		_, _ = w.Write(b.Bytes())
	case feedPath + "/Packages.sig":
		_, _ = w.Write(sig)
	case artifact:
		_, _ = w.Write(s.ipk)
	default:
		http.NotFound(w, r)
	}
}

// job is the panel's update_controller for the stand's package, with extra
// payload fields over the stand's own.
func (s *signedFeedStand) job(id string, extra map[string]any) controlplane.Job {
	p := map[string]any{"artifactUrl": s.srv.URL + artifact, "sha256": s.sha, "name": "vectra-controller-pro"}
	for k, v := range extra {
		p[k] = v
	}
	return controlplane.Job{ID: id, Type: "update_controller", Payload: p}
}

// A package the signed Vectra feed publishes — its sha256 in the index the
// feed's key signed, built for this router, the version the job names — is
// installed, and vctl restarts into it. The index is read as opkg reads it:
// Packages.gz and its signature, from the feed in customfeeds.conf.
func TestUpdateControllerInstallsWhatTheSignedFeedPublishes(t *testing.T) {
	for name, extra := range map[string]map[string]any{
		"the job names no version":          nil,
		"the job names the feed's version":  {"version": feedVersion},
		"the panel's artifactVersion":       {"artifactVersion": feedVersion},
		"the panel's sha256 in upper case":  {"sha256": strings.ToUpper(sha256Hex([]byte("stands in for vectra-controller-pro " + feedVersion)))},
		"the legacy checksumSha256 key too": {"sha256": "", "checksumSha256": sha256Hex([]byte("stands in for vectra-controller-pro " + feedVersion))},
	} {
		t.Run(name, func(t *testing.T) {
			results := map[string][]controlplane.JobResultRequest{}
			mu := &sync.Mutex{}
			d := buildGuardTestDaemon(t, results, mu)
			s := newSignedFeedStand(t)

			err := d.executeJob(context.Background(), s.job("u1", extra), controlplane.CheckInResponse{})
			if !errors.Is(err, errControllerRestartRequested) {
				fail, _ := lastResult(results, mu, "u1")
				t.Fatalf("not installed: %v %v", err, fail.Result)
			}
			if len(s.installed) != 1 || s.installed[0] != s.sha || s.restarts != 1 {
				t.Fatalf("opkg was given %v (want the published %s), restarts %d", s.installed, s.sha, s.restarts)
			}
			if p := d.st.PendingJobResult; p == nil || p.Status != "success" {
				t.Fatalf("no success journaled for after the restart: %+v", p)
			}
			if s.hit(feedPath+"/Packages.gz") != 1 || s.hit(feedPath+"/Packages.sig") != 1 || s.hit(feedPath+"/Packages") != 0 {
				t.Fatalf("not read as opkg reads a src/gz feed: %v", s.hits)
			}
		})
	}
}

// The refusal the panel shows its operator starts so; nothing was installed.
const wantNotInFeed = "update_controller: not in the signed Vectra feed (nothing installed): "

// Anything the signed Vectra feed does not vouch for is refused before it is
// even downloaded, and nothing is installed. The panel can name any URL and
// any sha256; only the feed's key, which never leaves the build host, can put
// a package in the feed.
func TestUpdateControllerRefusesWhatTheSignedFeedDoesNotPublish(t *testing.T) {
	other := newFeedKey(9)
	evil := []byte("an ipk the panel's attacker built")
	for _, c := range []struct {
		name  string
		extra map[string]any
		setup func(t *testing.T, s *signedFeedStand)
		say   func(s *signedFeedStand) string
	}{
		{name: "no feed line", setup: func(t *testing.T, s *signedFeedStand) {
			writeFeedFile(t, s.conf, "# add your custom package feeds here\nsrc/gz vectra_pro_old https://elsewhere.invalid/x\n")
		}, say: func(s *signedFeedStand) string { return "no vectra_pro feed in " + s.conf }},
		{name: "no customfeeds.conf", setup: func(t *testing.T, s *signedFeedStand) {
			if err := os.Remove(s.conf); err != nil {
				t.Fatal(err)
			}
		}, say: func(s *signedFeedStand) string { return "no vectra_pro feed in " + s.conf }},
		{name: "no key", setup: func(t *testing.T, s *signedFeedStand) {
			if err := os.RemoveAll(s.keys); err != nil {
				t.Fatal(err)
			}
		}, say: func(s *signedFeedStand) string { return "unknown key: signed by key " + s.key.id() }},
		{name: "a bad signature", setup: func(t *testing.T, s *signedFeedStand) {
			s.serveIndex(s.served(), s.key.sign([]byte("another index")))
		}, say: func(*signedFeedStand) string { return "bad signature" }},
		{name: "a tampered index", extra: map[string]any{"sha256": sha256Hex(evil)}, setup: func(t *testing.T, s *signedFeedStand) {
			// The attacker's package in place of ours; the signature is ours.
			s.serveIndex([]byte(strings.Replace(string(s.served()), s.sha, sha256Hex(evil), 1)), s.key.sign(s.served()))
		}, say: func(*signedFeedStand) string { return "bad signature" }},
		{name: "a signature by another key", extra: map[string]any{"sha256": sha256Hex(evil)}, setup: func(t *testing.T, s *signedFeedStand) {
			idx := []byte(feedStanza("vectra-controller-pro", feedVersion, feedArch, sha256Hex(evil)))
			s.serveIndex(idx, other.sign(idx))
		}, say: func(*signedFeedStand) string { return "unknown key: signed by key " + other.id() }},
		{name: "a key number mismatch", extra: map[string]any{"sha256": sha256Hex(evil)}, setup: func(t *testing.T, s *signedFeedStand) {
			// Another key filed under our key's number, and its signature
			// naming our number.
			writeFeedFile(t, filepath.Join(s.keys, s.key.id()), other.pubFile())
			idx := []byte(feedStanza("vectra-controller-pro", feedVersion, feedArch, sha256Hex(evil)))
			s.serveIndex(idx, other.signAs(s.key.number, idx))
		}, say: func(*signedFeedStand) string { return "key number mismatch" }},
		{name: "an index entry with a different sha", setup: func(t *testing.T, s *signedFeedStand) {
			s.signIndex(feedStanza("vectra-controller-pro", feedVersion, feedArch, strings.Repeat("3", 64)))
		}, say: func(s *signedFeedStand) string { return "the feed lists no vectra-controller-pro with sha256 " + s.sha }},
		{name: "the sha under another package's name", setup: func(t *testing.T, s *signedFeedStand) {
			s.signIndex(feedStanza("xray-core", "26.3.27-r1", feedArch, s.sha))
		}, say: func(s *signedFeedStand) string { return "the feed lists no vectra-controller-pro with sha256 " + s.sha }},
		{name: "another architecture", setup: func(t *testing.T, s *signedFeedStand) {
			s.signIndex(feedStanza("vectra-controller-pro", feedVersion, "mipsel_24kc", s.sha))
		}, say: func(*signedFeedStand) string { return "built for mipsel_24kc, this router is " + feedArch }},
		{name: "another version than the job names", extra: map[string]any{"version": "0.6.0-r38"},
			say: func(*signedFeedStand) string { return "is " + feedVersion + ", the job names 0.6.0-r38" }},
		{name: "another version than the panel's artifactVersion", extra: map[string]any{"artifactVersion": "0.6.0-r38"},
			say: func(*signedFeedStand) string { return "is " + feedVersion + ", the job names 0.6.0-r38" }},
		{name: "a feed over plain http", setup: func(t *testing.T, s *signedFeedStand) {
			writeFeedFile(t, s.conf, "src/gz vectra_pro "+strings.Replace(s.srv.URL, "https://", "http://", 1)+feedPath+"\n")
		}, say: func(*signedFeedStand) string { return "refusing non-https url" }},
		{name: "no architecture", setup: func(t *testing.T, s *signedFeedStand) {
			if err := os.Remove(s.release); err != nil {
				t.Fatal(err)
			}
		}, say: func(*signedFeedStand) string { return "cannot tell this router's architecture" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			results := map[string][]controlplane.JobResultRequest{}
			mu := &sync.Mutex{}
			d := buildGuardTestDaemon(t, results, mu)
			s := newSignedFeedStand(t)
			if c.setup != nil {
				c.setup(t, s)
			}

			err := d.executeJob(context.Background(), s.job("r1", c.extra), controlplane.CheckInResponse{})
			fail, ok := lastResult(results, mu, "r1")
			msg, _ := fail.Result["error"].(string)
			if !ok || !strings.HasPrefix(msg, wantNotInFeed) || !strings.Contains(msg, c.say(s)) {
				t.Errorf("refusal %q (failure reported: %v), want %q ... %q", msg, ok, wantNotInFeed, c.say(s))
			}
			if errors.Is(err, errControllerRestartRequested) || len(s.installed) != 0 || s.restarts != 0 {
				t.Fatalf("installed anyway: opkg given %v, restarts %d", s.installed, s.restarts)
			}
			if n := s.hit(artifact); n != 0 {
				t.Fatalf("the package was downloaded %d time(s) before the feed vouched for it", n)
			}
		})
	}
}

func TestRawUpdateControllerRefusesSignedDowngrade(t *testing.T) {
	results := map[string][]controlplane.JobResultRequest{}
	mu := &sync.Mutex{}
	d := buildGuardTestDaemon(t, results, mu)
	s := newSignedFeedStand(t)
	old := maintenanceCommand
	t.Cleanup(func() { maintenanceCommand = old })
	maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "opkg" {
			t.Fatal(name)
		}
		if len(args) == 2 && args[0] == "status" && args[1] == proPackageName {
			return []byte("Version: 0.6.0-r38\n"), nil
		}
		if len(args) != 4 || args[0] != "compare-versions" || args[1] != "0.6.0-r38" || args[2] != ">" || args[3] != feedVersion {
			t.Fatal(args)
		}
		return nil, nil
	}
	err := d.executeJob(context.Background(), s.job("raw-downgrade", nil), controlplane.CheckInResponse{})
	if errors.Is(err, errControllerRestartRequested) {
		t.Fatal("signed older package installed")
	}
	r, ok := lastResult(results, mu, "raw-downgrade")
	if !ok || r.Status != "failure" {
		t.Fatal("downgrade not refused", r, ok)
	}
	if len(s.installed) != 0 || s.restarts != 0 || s.hit(artifact) != 0 {
		t.Fatal("downgrade downloaded/installed/restarted", s.installed, s.restarts, s.hits)
	}
}

func TestRawUpdateControllerVersionFloorFailureAndUpgrade(t *testing.T) {
	for _, tc := range []struct {
		name, installed string
		comparisonErr   bool
		wantFailure     bool
	}{{"unknown installed version", "", false, true}, {"comparison unavailable", "0.6.0-r36", true, true}, {"signed upgrade", "0.6.0-r36", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			results := map[string][]controlplane.JobResultRequest{}
			mu := &sync.Mutex{}
			d := buildGuardTestDaemon(t, results, mu)
			s := newSignedFeedStand(t)
			oldCompare := signedFloorNewer
			t.Cleanup(func() { signedFloorNewer = oldCompare })
			maintenanceCommand = func(_ context.Context, name string, args ...string) ([]byte, error) {
				if name != "opkg" || len(args) != 2 || args[0] != "status" || args[1] != proPackageName {
					t.Fatal(name, args)
				}
				return []byte("Version: " + tc.installed + "\n"), nil
			}
			signedFloorNewer = func(_ context.Context, installed, candidate string) (bool, error) {
				if installed != tc.installed || candidate != feedVersion {
					t.Fatal(installed, candidate)
				}
				if tc.comparisonErr {
					return false, errors.New("fake comparison unavailable")
				}
				return false, nil
			}
			e := d.executeJob(context.Background(), s.job("floor-test", nil), controlplane.CheckInResponse{})
			if tc.wantFailure {
				if len(s.installed) != 0 || s.restarts != 0 || s.hit(artifact) != 0 {
					t.Fatal("refused floor mutated router")
				}
				if _, ok := lastResult(results, mu, "floor-test"); !ok {
					t.Fatal("missing failure")
				}
			} else if !errors.Is(e, errControllerRestartRequested) || len(s.installed) != 1 || s.restarts != 1 {
				t.Fatal("upgrade refused", e)
			}
		})
	}
}
