package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"vectra-controller-pro/internal/geodat"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/routepolicy"
)

// The route policy's geo files, kept current by vctl in native routing:
// fetched from the policy's own sources (global_rules geoip_url, geosite_url
// — what PassWall2's rule_update.lua reads) on the policy's own schedule
// (update_week_mode/update_time_mode, or the older auto_update, week_update,
// time_update; the fleet: every day at 06:00), in the router's local time.
//
// A file is taken only when it names every category the policy routes by —
// one missing, and xray refuses the whole configuration — and when it matches
// the sha256 published next to it, if one is. It is downloaded to flash, not
// held in memory, and replaces the old one in a single rename. xray reads its
// geo files at its start: the file changing is what makes the next sync
// restart it (passwallStamp), and what reloads the kernel's direct routes
// (direct_bypass.go).
//
// The update runs beside the daemon's loop, never in it: a slow mirror must
// not hold up check-ins, jobs or the rescue.

// nativeGeoMaxBytes caps a download: the fleet's files are 95 KB and 430 KB;
// Loyalsoldier's full lists, 17 MB and 10 MB.
const nativeGeoMaxBytes = 32 << 20

// nativeGeoTimeout bounds one request.
var nativeGeoTimeout = 2 * time.Minute

// nativeGeoRetry is how soon a failed update is tried again.
var nativeGeoRetry = 3 * time.Hour

// nativeGeoClient fetches the files; tests replace it.
var nativeGeoClient = func() *http.Client { return &http.Client{Timeout: nativeGeoTimeout} }

// geoRef finds the policy's categories: geosite:NAME and geoip:NAME, an
// attribute after @ not part of the name.
var geoRef = regexp.MustCompile(`\b(geosite|geoip):([A-Za-z0-9_.!-]+)`)

// policyGeoCodes are the categories the policy's shunt rules name, by file.
func policyGeoCodes(secs []routepolicy.Section) (geoip, geosite []string) {
	seen := map[string]bool{}
	for _, s := range secs {
		if s.Type != "shunt_rules" {
			continue
		}
		var texts []string
		for _, k := range []string{"domain_list", "ip_list"} {
			texts = append(texts, s.Get(k))
			texts = append(texts, s.Lists[k]...)
		}
		for _, t := range texts {
			for _, m := range geoRef.FindAllStringSubmatch(t, -1) {
				code := strings.ToUpper(strings.TrimPrefix(m[2], "!"))
				key := m[1] + ":" + code
				if seen[key] {
					continue
				}
				seen[key] = true
				if m[1] == "geoip" {
					geoip = append(geoip, code)
				} else {
					geosite = append(geosite, code)
				}
			}
		}
	}
	sort.Strings(geoip)
	sort.Strings(geosite)
	return geoip, geosite
}

// maybeUpdateNativeGeo starts the geo update, in its own goroutine, when the
// policy's schedule says so.
func (d *daemon) maybeUpdateNativeGeo(ctx context.Context, now time.Time) {
	if !d.nativeMode() || d.desired == nil || d.nativeGeoBusy.Load() {
		return
	}
	now = now.In(routerLocation())
	if now.UnixNano() < d.nativeGeoRetryAt.Load() {
		return
	}
	secs, err := loadNativePolicy()
	if err != nil {
		return
	}
	rules, ok := firstOfType(secs, "global_rules")
	if !ok {
		return
	}
	last := seedRefreshTimes([]string{"geo"}, now)
	if !scheduleOf(rules).due(last["geo"], now) {
		return
	}
	d.startNativeGeo(ctx, secs, now, false)
}

// startNativeGeo runs the geo update in its own goroutine, unless one runs
// already (false). asked: an update on request, which fetches both files
// whatever the schedule's switches say.
func (d *daemon) startNativeGeo(ctx context.Context, secs []routepolicy.Section, now time.Time, asked bool) bool {
	if !d.nativeGeoBusy.CompareAndSwap(false, true) {
		return false
	}
	// What a download cut short by a power cut left on the flash.
	for _, pat := range []string{".*.new*", ".*.tmp*"} {
		left, _ := filepath.Glob(filepath.Join(nativeGeoDir, pat))
		for _, f := range left {
			_ = os.Remove(f)
		}
	}
	// A failure is tried again after nativeGeoRetry; a success clears it.
	d.nativeGeoRetryAt.Store(now.Add(nativeGeoRetry).UnixNano())
	go func() {
		defer d.nativeGeoBusy.Store(false)
		changed, err := d.updateNativeGeo(ctx, secs, asked)
		if err != nil {
			if strings.Contains(err.Error(), "lacks") || strings.Contains(err.Error(), "sha256") {
				d.incident("GEO_REFUSED", reKeyNumber.ReplaceAllString(err.Error(), "N"), "a geo update was refused: "+err.Error(), nil)
			}
			logging.L().Warn("the route policy's geo files did not update; the ones in place stay", "err", err.Error(), "retryIn", nativeGeoRetry.String())
			return
		}
		d.nativeGeoRetryAt.Store(0)
		markRefreshed("geo", time.Now())
		if len(changed) > 0 {
			logging.L().Info("updated the route policy's geo files", "files", strings.Join(changed, ","))
		} else {
			logging.L().Info("the route policy's geo files are current")
		}
	}()
	return true
}

// updateNativeGeo fetches the policy's geo files and puts in place each one
// that is new and names every category the policy routes by — on the
// schedule, those its switches have on; asked for, both. It returns the
// files it replaced; an error leaves every file as it was.
func (d *daemon) updateNativeGeo(ctx context.Context, secs []routepolicy.Section, asked bool) ([]string, error) {
	rules, ok := firstOfType(secs, "global_rules")
	if !ok {
		return nil, errors.New("the route policy has no global_rules")
	}
	needIP, needSite := policyGeoCodes(secs)
	type job struct {
		file, url string
		need      []string
	}
	var jobs []job
	for _, j := range []job{
		{"geoip.dat", rules.Get("geoip_url"), needIP},
		{"geosite.dat", rules.Get("geosite_url"), needSite},
	} {
		if j.url != "" && (asked || geoUpdateOn(rules, strings.TrimSuffix(j.file, ".dat")+"_update")) {
			jobs = append(jobs, j)
		}
	}
	// Everything is fetched and checked first: a new geoip.dat is not put in
	// place beside a geosite.dat that failed.
	type ready struct{ file, tmp string }
	var todo []ready
	defer func() {
		for _, r := range todo {
			_ = os.Remove(r.tmp) // what was not renamed into place
		}
	}()
	for _, j := range jobs {
		tmp, err := fetchGeo(ctx, j.url, nativeGeoDir, j.file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", j.file, err)
		}
		if same, _ := sameFile(tmp, filepath.Join(nativeGeoDir, j.file)); same {
			_ = os.Remove(tmp)
			continue
		}
		todo = append(todo, ready{j.file, tmp})
		if err := checkGeoCodes(tmp, j.need); err != nil {
			return nil, fmt.Errorf("%s: %w", j.file, err)
		}
	}
	var changed []string
	for i, r := range todo {
		if err := os.Rename(r.tmp, filepath.Join(nativeGeoDir, r.file)); err != nil {
			return changed, err
		}
		todo[i].tmp = ""
		changed = append(changed, r.file)
	}
	if d, err := os.Open(nativeGeoDir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return changed, nil
}

// geoUpdateOn: the policy's schedule updates this file. PassWall2's
// rule_update.lua, run by its cron, fetches a file only while its switch
// (geoip_update, geosite_update) reads "1", and takes a missing one as "1".
func geoUpdateOn(rules routepolicy.Section, key string) bool {
	v, ok := rules.Options[key]
	return !ok || v == "1"
}

// checkGeoCodes: the file names every category the policy routes by. It is
// read into memory for that — only when the router can spare it.
func checkGeoCodes(path string, need []string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if in, err := memguard.Read(); err == nil {
		if in.AvailableKB < memguard.HeavyFloorKB(in.TotalKB)+uint64(st.Size()>>10) {
			return fmt.Errorf("not enough memory to check a %d KB file now", st.Size()>>10)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	codes, err := geodat.Codes(b)
	if err != nil {
		return fmt.Errorf("not a geo file: %w", err)
	}
	have := map[string]bool{}
	for _, c := range codes {
		have[c] = true
	}
	var missing []string
	for _, c := range need {
		if !have[c] {
			missing = append(missing, c)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the new file lacks %s, which the route policy routes by", strings.Join(missing, ", "))
	}
	return nil
}

// fetchGeo downloads one file into a temporary file in dir — never more than
// nativeGeoMaxBytes, never into the flash's last megabytes — checked against
// the sha256 published beside it (<url>.sha256sum) when there is one. It
// returns the temporary file's path; on an error nothing is left behind.
func fetchGeo(ctx context.Context, url, dir, name string) (string, error) {
	c, cancel := context.WithTimeout(ctx, nativeGeoTimeout)
	defer cancel()
	resp, err := geoGet(c, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.ContentLength > nativeGeoMaxBytes {
		return "", fmt.Errorf("%d bytes: larger than %d", resp.ContentLength, nativeGeoMaxBytes)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if free, ok := freeBytes(dir); ok {
		want := int64(nativeImportMargin)
		if resp.ContentLength > 0 {
			want += resp.ContentLength
		}
		if free < want {
			return "", fmt.Errorf("%s has %d KB free: not enough room", dir, free>>10)
		}
	}
	tmp, err := os.CreateTemp(dir, "."+name+".new*")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	fail := func(err error) (string, error) { _ = tmp.Close(); _ = os.Remove(path); return "", err }
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, nativeGeoMaxBytes+1))
	if err != nil {
		return fail(err)
	}
	if n == 0 {
		return fail(errors.New("an empty file"))
	}
	if n > nativeGeoMaxBytes {
		return fail(fmt.Errorf("larger than %d bytes", nativeGeoMaxBytes))
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	// The published sha256, asked with a time of its own: a download that
	// took most of the minute must not leave the check none.
	sc, scancel := context.WithTimeout(ctx, nativeGeoTimeout)
	defer scancel()
	want, ok, err := publishedSHA256(sc, url)
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("its published sha256: %w", err)
	}
	if ok && !strings.EqualFold(want, hex.EncodeToString(h.Sum(nil))) {
		_ = os.Remove(path)
		return "", errors.New("the download does not match its published sha256")
	}
	return path, nil
}

// publishedSHA256 is the sha256 in <url>.sha256sum: ok false when the source
// publishes none (an answer without one), an error when it could not be
// asked — then nothing is taken unchecked.
func publishedSHA256(ctx context.Context, url string) (string, bool, error) {
	resp, err := geoGet(ctx, url+".sha256sum")
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", false, err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 || len(f[0]) != 64 {
		return "", false, nil
	}
	return f[0], true, nil
}

func geoGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// What PassWall2's rule_update sends (a browser's), so a mirror that
	// refuses curl answers the same.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	return nativeGeoClient().Do(req)
}

// sameFile: a and b have the same bytes (by sha256, streamed).
func sameFile(a, b string) (bool, error) {
	ha, err := fileSHA256(a)
	if err != nil {
		return false, err
	}
	hb, err := fileSHA256(b)
	if err != nil {
		return false, err
	}
	return ha == hb, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func firstOfType(secs []routepolicy.Section, typ string) (routepolicy.Section, bool) {
	for _, s := range secs {
		if s.Type == typ {
			return s, true
		}
	}
	return routepolicy.Section{}, false
}
