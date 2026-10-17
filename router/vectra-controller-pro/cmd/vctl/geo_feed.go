package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/feedverify"
	"vectra-controller-pro/internal/geodat"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/vault"
)

// The geo feed: the router's geo files kept current from the Vectra feed's
// channel, where a builder (tools/geobuild, run on the build host) publishes
// merged geosite.dat and geoip.dat — itdog's lists with the categories the
// provider's documents route by — and geo.json, signed with the feed's key,
// naming each file by path, sha256, size and categories.
//
// Without it a router in provider mode ran the geo files its package shipped
// for as long as it lived (on 2026-10-17 1111's were at least 73 days old):
// only native routing refreshed its own (native_geo.go).
//
// A new set is taken only when the manifest is the feed key's and newer than
// the one in use, each file is the one it names (sha256, size), each holds
// the categories it names and every category the files in use hold (a
// category gone would make xray refuse the configuration), and the running
// render passes `xray -test` against the new set in a staging directory —
// only then are the files renamed into place, the manifest last. The loop
// then renders again under the new set (its serial is in the render key):
// xray -test once more, and xray restarts reading it.
//
// The download runs beside the loop, never in it; the loop takes a finished
// set at its next poll, so no render ever races the swap.

var (
	// geoFeedDir is where the feed's files live (vctl's own: a package
	// upgrade never rolls them back). Tests replace it.
	geoFeedDir = "/etc/vectra-controller-pro/geo"
	// geoFeedFirstAfter: the first ask after a start waits this long, so the
	// router's own start (xray, DNS, the check-in) comes first.
	geoFeedFirstAfter = 2 * time.Minute
	// geoFeedEvery is how often the manifest is asked (±10 %); it is a few
	// hundred bytes, the files are fetched only when they change.
	geoFeedEvery = 6 * time.Hour
	// geoFeedRetry is how soon a failed ask is tried again.
	geoFeedRetry = 30 * time.Minute
)

const (
	geoManifestName     = "geo.json"
	geoManifestMaxBytes = 64 << 10
	// geoFeedMaxFile caps a file: the merged files are ~0.1 and ~0.8 MB; a
	// file is checked in memory before it touches the flash.
	geoFeedMaxFile = 8 << 20
)

// geoManifest is geo.json.
type geoManifest struct {
	Schema  int                        `json:"schema"`
	Serial  int64                      `json:"serial"`
	BuiltAt string                     `json:"builtAt"`
	Files   map[string]geoManifestFile `json:"files"`
}

type geoManifestFile struct {
	Path   string   `json:"path"`
	SHA256 string   `json:"sha256"`
	Size   int64    `json:"size"`
	Codes  []string `json:"codes"`
}

var (
	geoFilePath = regexp.MustCompile(`^geo/[a-z0-9][a-z0-9._-]{0,80}\.dat$`)
	geoSHA      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	geoCode     = regexp.MustCompile(`^[A-Z0-9][A-Z0-9!@._-]{0,63}$`)
)

// geoFeedFiles are the files a set holds, in the order they are put in place.
var geoFeedFiles = []string{"geoip.dat", "geosite.dat"}

func parseGeoManifest(raw []byte) (*geoManifest, error) {
	if len(raw) > geoManifestMaxBytes {
		return nil, errors.New("geo.json: too large")
	}
	var m geoManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("geo.json: %w", err)
	}
	if m.Schema != 1 {
		return nil, fmt.Errorf("geo.json: schema %d (this vctl reads 1)", m.Schema)
	}
	if m.Serial <= 0 {
		return nil, errors.New("geo.json: serial must be positive")
	}
	if len(m.Files) != len(geoFeedFiles) {
		return nil, fmt.Errorf("geo.json: %d files, want %v", len(m.Files), geoFeedFiles)
	}
	for _, name := range geoFeedFiles {
		f, ok := m.Files[name]
		switch {
		case !ok:
			return nil, fmt.Errorf("geo.json: no %s", name)
		case !geoFilePath.MatchString(f.Path):
			return nil, fmt.Errorf("geo.json: %s: path %q", name, f.Path)
		case !geoSHA.MatchString(f.SHA256):
			return nil, fmt.Errorf("geo.json: %s: sha256 %q", name, f.SHA256)
		case f.Size <= 0 || f.Size > geoFeedMaxFile:
			return nil, fmt.Errorf("geo.json: %s: size %d", name, f.Size)
		case len(f.Codes) == 0:
			return nil, fmt.Errorf("geo.json: %s names no category", name)
		}
		for _, c := range f.Codes {
			if !geoCode.MatchString(c) {
				return nil, fmt.Errorf("geo.json: %s: category %q", name, c)
			}
		}
	}
	return &m, nil
}

// storedGeoManifest is the manifest of the set in geoFeedDir, when the set
// is complete: the manifest is written last, after both files.
func storedGeoManifest() (*geoManifest, bool) {
	raw, err := os.ReadFile(filepath.Join(geoFeedDir, geoManifestName))
	if err != nil {
		return nil, false
	}
	m, err := parseGeoManifest(raw)
	if err != nil || !config.GeoFilesAt(geoFeedDir) {
		return nil, false
	}
	return m, true
}

// fetchGeoManifest asks the Vectra feed's channel for geo.json, signed by the
// key that signs the feed (an OpenWrt key in /etc/opkg/keys is no authority
// for routing data). It returns the manifest, its bytes and signature, and
// the channel's root, where the files' paths start.
func fetchGeoManifest(ctx context.Context, fetch func(context.Context, string, int64) ([]byte, error)) (*geoManifest, []byte, []byte, string, error) {
	_, feed, _, key, err := trustedFeedIndexWith(ctx, fetch)
	if err != nil {
		return nil, nil, nil, "", err
	}
	u, err := url.Parse(strings.TrimRight(feed.URL, "/"))
	if err != nil {
		return nil, nil, nil, "", err
	}
	u.Path = path.Dir(u.Path)
	root := u.String()
	raw, err := fetch(ctx, root+"/"+geoManifestName, geoManifestMaxBytes)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("geo.json: %w", err)
	}
	sig, err := fetch(ctx, root+"/"+geoManifestName+".sig", maxFeedSignatureBytes)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("geo.json.sig: %w", err)
	}
	k, err := feedverify.VerifyWithKeys(opkgKeysDir, raw, sig)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("geo.json.sig: %w", err)
	}
	if k != key {
		return nil, nil, nil, "", fmt.Errorf("geo.json: signed by %s, the feed by %s", k, key)
	}
	m, err := parseGeoManifest(raw)
	if err != nil {
		return nil, nil, nil, "", err
	}
	return m, raw, sig, root, nil
}

// geoFeedTester runs the running render through `xray -test` against a
// directory of geo files; nil when there is nothing to test with.
type geoFeedTester func(ctx context.Context, assetDir string) error

// geoRefusal is a set that failed a check of its own (a file not the
// manifest's, a category gone, xray refusing it): the same serial is not
// fetched again; a newer one is.
type geoRefusal struct {
	serial int64
	err    error
}

func (r *geoRefusal) Error() string { return fmt.Sprintf("geo set %d refused: %v", r.serial, r.err) }
func (r *geoRefusal) Unwrap() error { return r.err }

// updateGeoFeed fetches the channel's set when it is newer than the one in
// use (and not the one refused last, skip) and puts it in place, all checks
// passed. It returns the serial put in place (0: nothing changed). Each file
// is checked in memory before it is written; nothing is written for a set
// that fails.
func updateGeoFeed(ctx context.Context, fetch func(context.Context, string, int64) ([]byte, error), current string, test geoFeedTester, skip int64) (int64, error) {
	m, raw, sig, root, err := fetchGeoManifest(ctx, fetch)
	if err != nil {
		return 0, err
	}
	if have, ok := storedGeoManifest(); ok && m.Serial <= have.Serial {
		return 0, nil // the set in use, or an older one: nothing to do
	}
	if m.Serial == skip {
		return 0, nil // refused already: wait for a newer one
	}
	refuse := func(err error) (int64, error) { return 0, &geoRefusal{serial: m.Serial, err: err} }
	type ready struct {
		name string
		b    []byte // fetched; nil: the set in use holds it already
	}
	var files []ready
	for _, name := range geoFeedFiles {
		f := m.Files[name]
		need := append([]string(nil), f.Codes...)
		if b, err := os.ReadFile(filepath.Join(current, name)); err == nil {
			if codes, err := geodat.Codes(b); err == nil {
				need = append(need, codes...)
			}
		}
		if sum, err := fileSHA256(filepath.Join(geoFeedDir, name)); err == nil && sum == f.SHA256 {
			if err := checkGeoCodes(filepath.Join(geoFeedDir, name), uniqueSorted(need)); err != nil {
				return refuse(fmt.Errorf("%s: %w", name, err))
			}
			files = append(files, ready{name: name})
			continue
		}
		b, err := fetch(ctx, root+"/"+f.Path, f.Size)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", name, err)
		}
		if err := checkGeoFeedBytes(b, f, uniqueSorted(need)); err != nil {
			return refuse(fmt.Errorf("%s: %w", name, err))
		}
		files = append(files, ready{name: name, b: b})
	}
	var need int64
	for _, r := range files {
		need += int64(len(r.b))
	}
	if err := os.MkdirAll(geoFeedDir, 0o755); err != nil {
		return 0, err
	}
	if free, ok := freeBytes(geoFeedDir); ok && free < need+nativeImportMargin {
		return 0, fmt.Errorf("%s has %d KB free, the set needs %d KB and a margin", geoFeedDir, free>>10, need>>10)
	}
	stage := filepath.Join(geoFeedDir, ".new")
	_ = os.RemoveAll(stage) // what a power cut left
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return 0, err
	}
	defer os.RemoveAll(stage)
	for _, r := range files {
		dest := filepath.Join(stage, r.name)
		if r.b == nil {
			// The file in use: linked, not copied — no flash written for it.
			if err := os.Link(filepath.Join(geoFeedDir, r.name), dest); err != nil {
				if err := copyFile(filepath.Join(geoFeedDir, r.name), dest); err != nil {
					return 0, err
				}
			}
			continue
		}
		if err := os.WriteFile(dest, r.b, 0o644); err != nil {
			return 0, err
		}
		if err := syncFile(dest); err != nil {
			return 0, err
		}
	}
	if test != nil {
		if err := test(ctx, stage); err != nil {
			if errors.Is(err, xray.ErrLowMemory) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return 0, fmt.Errorf("checking the new geo files with xray: %w", err) // not now: tried again
			}
			return refuse(fmt.Errorf("the running configuration with the new geo files: %w", err))
		}
	}
	// In place: the files, then the signature, the manifest last — the set
	// counts as complete only once its manifest is there.
	for _, r := range files {
		if r.b == nil {
			continue
		}
		if err := os.Rename(filepath.Join(stage, r.name), filepath.Join(geoFeedDir, r.name)); err != nil {
			return 0, err
		}
	}
	if err := writeFileAtomic(filepath.Join(geoFeedDir, geoManifestName+".sig"), sig, 0o644); err != nil {
		return 0, err
	}
	if err := writeFileAtomic(filepath.Join(geoFeedDir, geoManifestName), raw, 0o644); err != nil {
		return 0, err
	}
	return m.Serial, nil
}

// checkGeoFeedBytes: a fetched file is the one the manifest names (size,
// sha256) and holds every category in need — read from memory, when the
// router can spare it.
func checkGeoFeedBytes(b []byte, f geoManifestFile, need []string) error {
	if int64(len(b)) != f.Size {
		return fmt.Errorf("%d bytes, the manifest says %d", len(b), f.Size)
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != f.SHA256 {
		return errors.New("its sha256 is not the manifest's")
	}
	codes, err := geodat.Codes(b)
	if err != nil {
		return fmt.Errorf("not a geo file: %w", err)
	}
	have := map[string]bool{}
	for _, c := range codes {
		have[strings.ToUpper(c)] = true
	}
	var missing []string
	for _, c := range need {
		if !have[strings.ToUpper(c)] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the new file lacks %s, which the router routes by", strings.Join(missing, ", "))
	}
	return nil
}

func uniqueSorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return slicesCompactStrings(out)
}

func slicesCompactStrings(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func syncFile(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// geoFeedInUse is where the next render's geo files are when the feed's set
// is in use; "" otherwise. Only the default directory gives way to it: a
// directory an operator named stays theirs.
func (d *daemon) geoFeedInUse(configured string) string {
	if d.geoFeedSerial == 0 {
		return ""
	}
	if configured != config.DefaultGeoAssetDir && configured != config.LegacyGeoAssetDir {
		return ""
	}
	return geoFeedDir
}

// geoFeedKey is the part of the render key the feed's set adds: a new set is
// rendered again, and xray restarts reading it.
func (d *daemon) geoFeedKey() string {
	if d.geoFeedSerial == 0 || d.passwallMode() || d.geoAssetDir() != geoFeedDir {
		return "" // a set the renders do not use restarts nothing
	}
	return ";geofeed=" + fmt.Sprint(d.geoFeedSerial)
}

// maybeUpdateGeoFeed runs every poll: it takes a set the download finished,
// and starts the download when it is due. Provider routing only: native and
// PassWall routing read their own files.
func (d *daemon) maybeUpdateGeoFeed(ctx context.Context, now time.Time) {
	if d.passwallMode() {
		return
	}
	if s := d.geoFeedReady.Swap(0); s != 0 && s != d.geoFeedSerial {
		d.geoFeedSerial = s
		logging.L().Info("new geo files in place; rendering with them", "serial", s, "dir", geoFeedDir)
		if d.desired != nil {
			d.rebuildApplier()
		}
		if d.desired != nil && d.applier != nil && d.applier.Tproxy != nil && fileExists(d.cfg.XrayRenderPath) {
			if r := d.rerenderRunning(ctx); !r.OK {
				logging.L().Warn("the render with the new geo files was refused; the running one stays", "detail", r.Detail)
				d.rerenderRetryAt = time.Now().Add(rerenderRetryAfter)
			}
		}
	}
	if d.nextGeoFeed.IsZero() {
		d.nextGeoFeed = now.Add(geoFeedFirstAfter)
	}
	d.geoFeedDue()
	if now.Before(d.nextGeoFeed) || !d.geoFeedBusy.CompareAndSwap(false, true) {
		return
	}
	d.nextGeoFeed = now.Add(catalogRefreshPeriod(int(geoFeedEvery / time.Hour)))
	current := d.runningAssetDir()
	var test geoFeedTester
	if render, err := vault.ReadFile(d.cfg.XrayRenderPath); err == nil && len(render) > 0 {
		test = func(ctx context.Context, dir string) error {
			return xray.Validator{Binary: d.cfg.XrayBinary, AssetDir: dir,
				OOMScoreAdj: memguard.TransientAdj, MemFloorKB: d.memFloorKB}.Test(ctx, render)
		}
	}
	client := catalogHTTPClient()
	go func() {
		defer d.geoFeedBusy.Store(false)
		fetch := func(ctx context.Context, u string, max int64) ([]byte, error) {
			return fetchHTTPSWith(ctx, client, u, max)
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		serial, err := updateGeoFeed(c, fetch, current, test, d.geoFeedRefused.Load())
		var refused *geoRefusal
		switch {
		case errors.Is(err, errHTTPNotFound):
			logging.L().Debug("no geo set published on the channel")
			return
		case errors.As(err, &refused):
			d.geoFeedRefused.Store(refused.serial)
			d.incident("GEO_REFUSED", "the geo feed's set failed a check", refused.Error(), nil)
			logging.L().Warn("the geo feed's set was refused; the files in use stay until a newer set", "err", refused.Error())
			return
		case err != nil:
			d.nextGeoFeedRetry.Store(time.Now().Add(geoFeedRetry).UnixNano())
			logging.L().Warn("the geo files were not updated; the ones in use stay", "err", err.Error(), "retryIn", geoFeedRetry.String())
			return
		}
		if serial != 0 {
			d.geoFeedReady.Store(serial)
		}
	}()
}

// geoFeedDue folds a failed download's retry time into the schedule (the
// goroutine cannot write nextGeoFeed: the loop owns it).
func (d *daemon) geoFeedDue() {
	if at := d.nextGeoFeedRetry.Swap(0); at != 0 {
		if t := time.Unix(0, at); t.Before(d.nextGeoFeed) {
			d.nextGeoFeed = t
		}
	}
}

// startGeoFeedNow makes the next poll ask for the set (a job asked for it).
func (d *daemon) startGeoFeedNow() { d.nextGeoFeed = time.Now() }

// loadGeoFeed is the set in place at the daemon's start.
func loadGeoFeed() int64 {
	m, ok := storedGeoManifest()
	if !ok {
		return 0
	}
	// Checked once at the start: a file the flash spoilt, or one renamed into
	// place before a power cut kept the newer manifest from following it, is
	// no set — the package's files serve until the feed's next set (which the
	// stored serial does not hold back: it is not taken as in use).
	for _, name := range geoFeedFiles {
		if sum, err := fileSHA256(filepath.Join(geoFeedDir, name)); err != nil || sum != m.Files[name].SHA256 {
			logging.L().Warn("the geo feed's files do not match their manifest; using the package's until the next set", "file", name)
			_ = os.Remove(filepath.Join(geoFeedDir, geoManifestName))
			return 0
		}
	}
	return m.Serial
}
