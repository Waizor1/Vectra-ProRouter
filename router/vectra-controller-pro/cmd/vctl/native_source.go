package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"vectra-controller-pro/internal/brand"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/routepolicy"
	"vectra-controller-pro/internal/subscription"
)

// Native routing (UCI route_source 'native'): the operator's route policy in
// vctl's own store, made into xray's configuration by vctl itself
// (internal/routepolicy) — the configuration PassWall2's generator made from
// the same policy, proven against it — and its subscription refreshed by vctl
// on the policy's own schedule. Nothing of PassWall2 is needed: not its
// generator, its Lua, its geo packages or its cron.
//
// The store is PassWall2's configuration, imported once, as it was: the panel's
// slots, rules and nodes carry over to the byte, and a policy written for
// PassWall reads the same here. Everything downstream — the adaptation, the
// splice, DNS through the tunnel, FakeDNS carried into xray, the kernel's
// direct routes — is PassWall-compatible routing's (passwallMode is true for
// both).

const routeSourceNative = "native"

var (
	// nativeStorePath is the route policy: a UCI file, so `uci` reads and
	// changes it like any other (uci show vectra_route).
	nativeStorePath = "/etc/config/vectra_route"
	// nativeGeoDir holds the geo files the policy's rules name: imported
	// with it from PassWall's directory, then kept by vctl. Not PassWall's
	// v2ray-geoip/geosite packages' files, which leave with them.
	nativeGeoDir = "/usr/share/vectra-controller-pro/route-geo"
	// nativeRefreshPath remembers when each subscription was last asked, so
	// a restart neither asks again at once nor skips a day.
	nativeRefreshPath = "/etc/vectra-controller-pro/route-refresh.json"
)

func (d *daemon) nativeMode() bool { return d.cfg.RouteSource == routeSourceNative }

// routeAssetDir is where the route policy's geo files are: in native routing
// vctl's own once both are in place — until the import has put them there,
// PassWall's, which the last render was made with.
func (d *daemon) routeAssetDir() string {
	if d.nativeMode() {
		return nativeAssetDir()
	}
	return passwallAssetDir()
}

func nativeAssetDir() string {
	if fileExists(filepath.Join(nativeGeoDir, "geoip.dat")) && fileExists(filepath.Join(nativeGeoDir, "geosite.dat")) {
		return nativeGeoDir
	}
	return passwallAssetDir()
}

// loadNativePolicy reads the store — importing PassWall2's configuration into
// it the first time.
func loadNativePolicy() ([]routepolicy.Section, error) {
	if _, err := os.Stat(nativeStorePath); errors.Is(err, os.ErrNotExist) {
		if err := importPassWallPolicy(); err != nil {
			return nil, err
		}
	}
	b, err := os.ReadFile(nativeStorePath)
	if err != nil {
		return nil, err
	}
	return routepolicy.ParseUCI(string(b))
}

// importPassWallPolicy copies PassWall2's configuration into the store, and
// its geo files into nativeGeoDir, as they are now. The store is written last:
// it existing means the import is complete.
func importPassWallPolicy() error {
	b, err := os.ReadFile(passwallUCIFile)
	if err != nil {
		return fmt.Errorf("no route policy: %s is missing and there is no PassWall2 configuration to import (%w)", nativeStorePath, err)
	}
	secs, err := routepolicy.ParseUCI(string(b))
	if err != nil {
		return fmt.Errorf("PassWall2's configuration does not parse: %w", err)
	}
	src := passwallAssetDir()
	var need int64
	var files []string
	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		if fileExists(filepath.Join(nativeGeoDir, f)) {
			continue
		}
		st, err := os.Stat(filepath.Join(src, f))
		if err != nil {
			return fmt.Errorf("importing PassWall2's %s: %w", f, err)
		}
		need += st.Size()
		files = append(files, f)
	}
	// The files go to flash. PassWall's may be the full lists (28 MB of
	// Loyalsoldier's where the fleet's are 0.5 MB): not copied into the last
	// megabytes of the overlay.
	if len(files) > 0 {
		if err := os.MkdirAll(nativeGeoDir, 0o755); err != nil {
			return err
		}
		if free, ok := freeBytes(nativeGeoDir); ok && free < need+nativeImportMargin {
			return fmt.Errorf("PassWall2's geo files are %d KB and %s has %d KB free: not enough room to import them", need>>10, nativeGeoDir, free>>10)
		}
	}
	for _, f := range files {
		if err := copyFileAtomic(filepath.Join(src, f), filepath.Join(nativeGeoDir, f), 0o644); err != nil {
			return fmt.Errorf("importing PassWall2's %s: %w", f, err)
		}
	}
	if err := writeFileAtomic(nativeStorePath, b, 0o600); err != nil {
		return err
	}
	logging.L().Info("imported PassWall2's route policy into vctl's own", "store", nativeStorePath, "sections", len(secs), "geo", nativeGeoDir)
	return nil
}

// storeGetter reads the store as PassWall's UCI keys name it
// ("passwall2.@global[0].node", "passwall2.<section>.<option>"), for
// passwallArgs.
func storeGetter(secs []routepolicy.Section) func(string) string {
	return func(key string) string {
		key = strings.TrimPrefix(key, "passwall2.")
		i := strings.LastIndex(key, ".")
		if i < 0 {
			return ""
		}
		name, opt := key[:i], key[i+1:]
		if strings.HasPrefix(name, "@") && strings.HasSuffix(name, "]") {
			j := strings.Index(name, "[")
			typ := name[1:j]
			n, err := strconv.Atoi(name[j+1 : len(name)-1])
			if err != nil {
				return ""
			}
			for _, s := range secs {
				if s.Type == typ {
					if n == 0 {
						return s.Get(opt)
					}
					n--
				}
			}
			return ""
		}
		for _, s := range secs {
			if s.Name == name {
				return s.Get(opt)
			}
		}
		return ""
	}
}

// generateNative is generatePassWall made by vctl: the same arguments from
// the same policy, for the xray this router runs.
func (d *daemon) generateNative(ctx context.Context) ([]byte, error) {
	if d.desired == nil || d.desired.Inbounds.Tproxy == nil {
		return nil, errors.New("no operator config with a tproxy inbound")
	}
	secs, err := loadNativePolicy()
	if err != nil {
		return nil, err
	}
	return d.generateFrom(ctx, secs)
}

// generateFrom makes the xray configuration of a route policy.
func (d *daemon) generateFrom(ctx context.Context, secs []routepolicy.Section) ([]byte, error) {
	if d.desired == nil || d.desired.Inbounds.Tproxy == nil {
		return nil, errors.New("no operator config with a tproxy inbound")
	}
	_, dnsPort, _ := net.SplitHostPort(xray.DefaultDNSListen)
	dp, _ := strconv.Atoi(dnsPort)
	resolver := wanResolver(d.etcRoot())
	if resolver == "" {
		resolver = xray.DefaultDirectResolvers[0]
	}
	args, err := passwallArgs(storeGetter(secs), d.desired.Inbounds.Tproxy.Port, dp, resolver)
	if err != nil {
		return nil, err
	}
	ver, err := d.xrayVersionNow(ctx)
	if err != nil {
		return nil, err
	}
	return routepolicy.Generate(secs, args, ver)
}

// xrayVersionNow is the running xray's version: the inventory's, or asked of
// the binary. The configuration's schema follows it, so it is never guessed.
func (d *daemon) xrayVersionNow(ctx context.Context) (string, error) {
	if d.collector != nil {
		if v := d.collector.XrayVersion(); v != "" {
			return v, nil
		}
	}
	if d.xrayVersion != "" {
		return d.xrayVersion, nil
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, d.cfg.XrayBinary, "version").Output()
	if err != nil {
		return "", fmt.Errorf("the xray version, which the configuration's schema follows: %w", err)
	}
	f := strings.Fields(string(out))
	if len(f) < 2 || f[0] != "Xray" {
		return "", fmt.Errorf("the xray version: unexpected %q", strings.TrimSpace(tail(string(out), 80)))
	}
	d.xrayVersion = f[1]
	return f[1], nil
}

// ---------------------------------------------------------- subscriptions --

// nativeSchedule is when PassWall2 26.8.10 itself would refresh (app.sh's
// start_crontab): update_week_mode 0-6 a weekday (cron's: 0 is Sunday), 7
// every day, 8 every update_interval_mode hours; update_time_mode "H:MM"; the
// router's local time. That is all 26.8.10 reads — not auto_update, and not
// its own switch, which the takeover turned off. A section without
// update_week_mode is the older format — auto_update '1', week_update,
// time_update (the hour) — which 26.7.16 and later schedule not at all (the
// fleet's "nightly update was dead" incident): vctl schedules it, as meant.
type nativeSchedule struct {
	on       bool
	weekday  int // 0 Sunday ... 6; 7 every day; 8 every interval
	hour     int
	minute   int
	interval time.Duration
	atBoot   bool
}

func scheduleOf(s routepolicy.Section) nativeSchedule {
	sch := nativeSchedule{atBoot: s.Get("boot_update") == "1"}
	week, at := s.Get("update_week_mode"), s.Get("update_time_mode")
	if week == "" {
		if s.Get("auto_update") != "1" {
			return sch
		}
		week, at = s.Get("week_update"), s.Get("time_update")
	}
	w, err := strconv.Atoi(strings.TrimSpace(week))
	if err != nil || w < 0 || w > 8 {
		return sch
	}
	sch.weekday = w
	if w == 8 {
		hours, _ := strconv.Atoi(strings.TrimSpace(s.Get("update_interval_mode")))
		if hours <= 0 {
			return sch // PassWall's loop does nothing without it either
		}
		sch.interval, sch.on = time.Duration(hours)*time.Hour, true
		return sch
	}
	h, m := at, "0"
	if hh, mm, ok := strings.Cut(at, ":"); ok {
		h, m = hh, mm
	}
	hour, err1 := strconv.Atoi(strings.TrimSpace(h))
	minute, err2 := strconv.Atoi(strings.TrimSpace(m))
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return sch // no time cron could have run it at
	}
	sch.hour, sch.minute, sch.on = hour, minute, true
	return sch
}

// due: a scheduled time has passed since last. Without a record nothing is
// due: the caller starts the record (seedRefreshTimes), so a router that
// takes native routing at 14:00 asks at its next scheduled time, as
// PassWall's cron would have.
func (s nativeSchedule) due(last, now time.Time) bool {
	if !s.on || last.IsZero() {
		return false
	}
	if s.weekday == 8 {
		return !now.Before(last.Add(s.interval))
	}
	// The latest scheduled moment at or before now.
	t := time.Date(now.Year(), now.Month(), now.Day(), s.hour, s.minute, 0, 0, now.Location())
	for i := 0; i < 8; i++ {
		if !t.After(now) && (s.weekday == 7 || int(t.Weekday()) == s.weekday) {
			return last.Before(t)
		}
		t = t.AddDate(0, 0, -1)
	}
	return false
}

func loadRefreshTimes() map[string]time.Time {
	out := map[string]time.Time{}
	b, err := os.ReadFile(nativeRefreshPath)
	if err != nil {
		return out
	}
	var raw map[string]int64
	if json.Unmarshal(b, &raw) != nil {
		return out
	}
	for k, v := range raw {
		out[k] = time.Unix(v, 0)
	}
	return out
}

func saveRefreshTimes(m map[string]time.Time) error {
	raw := map[string]int64{}
	for k, v := range m {
		raw[k] = v.Unix()
	}
	b, _ := json.Marshal(raw)
	return writeFileAtomic(nativeRefreshPath, b, 0o600)
}

// refreshTimesMu guards route-refresh.json's load-modify-save: the loop and
// the geo update's goroutine both write it.
var refreshTimesMu sync.Mutex

// seedRefreshTimes starts the record of each key that has none, at now.
func seedRefreshTimes(keys []string, now time.Time) map[string]time.Time {
	refreshTimesMu.Lock()
	defer refreshTimesMu.Unlock()
	last := loadRefreshTimes()
	seeded := false
	for _, k := range keys {
		if _, ok := last[k]; !ok {
			last[k], seeded = now, true
		}
	}
	if seeded {
		if err := saveRefreshTimes(last); err != nil {
			logging.L().Warn("could not save when the route policy was last refreshed", "err", err.Error())
		}
	}
	return last
}

// markRefreshed records a successful refresh of key.
func markRefreshed(key string, now time.Time) {
	refreshTimesMu.Lock()
	defer refreshTimesMu.Unlock()
	last := loadRefreshTimes()
	last[key] = now
	if err := saveRefreshTimes(last); err != nil {
		logging.L().Warn("could not save when the route policy was last refreshed", "err", err.Error())
	}
}

// nativeRefreshRetry is how soon a failed refresh is tried again; an answer
// the refresh refuses (the provider's placeholder, what vctl cannot render)
// is asked for again after nativeRefusedRetry — not every half hour.
var (
	nativeRefreshRetry = 30 * time.Minute
	nativeRefusedRetry = 2 * time.Hour
)

func retryAfter(err error) time.Duration {
	if errors.Is(err, routepolicy.ErrEmptyFeed) || errors.Is(err, routepolicy.ErrUnsupported) {
		return nativeRefusedRetry
	}
	return nativeRefreshRetry
}

// maybeRefreshNative runs each subscription's refresh when its schedule says
// so, in the router's local time; one with boot_update '1' also at the
// daemon's first loop (PassWall: at its boot start). A failure changes
// nothing and is tried again after retryAfter.
func (d *daemon) maybeRefreshNative(ctx context.Context, now time.Time) {
	if !d.nativeMode() || d.desired == nil {
		return
	}
	now = now.In(routerLocation())
	boot := !d.nativeBooted
	d.nativeBooted = true
	if now.Before(d.nativeRetryAt) {
		return
	}
	secs, err := loadNativePolicy()
	if err != nil {
		return // maybeSyncPassWall says why
	}
	var subs []routepolicy.Section
	var keys []string
	for _, s := range secs {
		if s.Type == "subscribe_list" && s.Get("enabled") != "0" {
			subs = append(subs, s)
			keys = append(keys, s.Name)
		}
	}
	last := seedRefreshTimes(keys, now)
	for _, s := range subs {
		sch := scheduleOf(s)
		if !(boot && sch.atBoot) && !sch.due(last[s.Name], now) {
			continue
		}
		rep, err := d.refreshNative(ctx, s.Name, false)
		if err != nil {
			if errors.Is(err, routepolicy.ErrEmptyFeed) || errors.Is(err, routepolicy.ErrUnsupported) {
				d.incident("SUBSCRIPTION_REFUSED", reKeyNumber.ReplaceAllString(err.Error(), "N"),
					"the subscription answered what vctl refuses: "+err.Error(), map[string]any{"subscription": s.Name})
			}
			wait := retryAfter(err)
			d.nativeRetryAt = now.Add(wait)
			logging.L().Warn("the route policy's subscription did not refresh; the nodes stay as they are", "subscription", s.Name, "err", err.Error(), "retryIn", wait.String())
			return
		}
		logRefresh(rep)
	}
}

// refreshNativeAll refreshes every enabled subscription of the route policy
// now, whether or not its answer changed (the refresh job).
func (d *daemon) refreshNativeAll(ctx context.Context) ([]map[string]interface{}, error) {
	secs, err := loadNativePolicy()
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for _, s := range secs {
		if s.Type != "subscribe_list" || s.Get("enabled") == "0" {
			continue
		}
		rep, err := d.refreshNative(ctx, s.Name, true)
		if err != nil {
			return out, fmt.Errorf("subscription %s: %w", s.Name, err)
		}
		logRefresh(rep)
		followed, replaced := 0, 0
		for _, r := range rep.Rebound {
			switch r.How {
			case "name+address:port", "address:port", "name":
				followed++ // the same node, on a new host or by a new name
			default:
				replaced++ // its node gone: the owner's fallback chose another
			}
		}
		out = append(out, map[string]interface{}{"subscription": s.Name, "group": rep.Group, "nodes": rep.Nodes, "skipped": len(rep.Skipped), "slotsFollowed": followed, "slotsReplaced": replaced})
	}
	return out, nil
}

// refreshNative asks one subscription and applies its answer to the store —
// or, when the answer is the one applied last and force is off, leaves it.
// The store changing is what re-renders (maybeSyncPassWall).
func (d *daemon) refreshNative(ctx context.Context, name string, force bool) (routepolicy.RefreshReport, error) {
	rep := routepolicy.RefreshReport{Subscription: name}
	secs, err := loadNativePolicy()
	if err != nil {
		return rep, err
	}
	var sub *routepolicy.Section
	for i := range secs {
		if secs[i].Type == "subscribe_list" && secs[i].Name == name {
			sub = &secs[i]
		}
	}
	if sub == nil {
		return rep, fmt.Errorf("no subscription %q in the route policy", name)
	}
	body, err := d.fetchNativeFeed(ctx, sub.Get("url"))
	if err != nil {
		return rep, err
	}
	if !force && routepolicy.FeedMD5(body) == sub.Get("md5") {
		rep.Group = sub.Get("remark")
		markRefreshed(name, time.Now())
		return rep, nil
	}
	out, rep, err := routepolicy.Refresh(secs, name, body, nil)
	if err != nil {
		return rep, err
	}
	if err := writeFileAtomic(nativeStorePath, []byte(routepolicy.Export(out)), 0o600); err != nil {
		return rep, err
	}
	markRefreshed(name, time.Now())
	return rep, nil
}

// fetchNativeFeed asks the subscription as the router itself: its own signed
// User-Agent and its device headers (the x-hwid PassWall2 sends, byte for
// byte). The URL is a secret: no error carries it.
func (d *daemon) fetchNativeFeed(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, errors.New("the subscription has no URL")
	}
	if err := d.device.Validate(); err != nil {
		return nil, fmt.Errorf("%w (the provider refuses a device without a valid x-hwid)", err)
	}
	opts := subscription.FetchOptions{
		URL:           url,
		HWID:          d.device.HWID,
		MAC:           d.device.MAC,
		Model:         d.device.Model,
		OSRelease:     d.device.OSRelease,
		HTTPClient:    d.subClient,
		SignUserAgent: uaSigner(d.st, controllerVersion(), time.Now),
	}
	fr, err := subscription.Fetch(ctx, opts)
	if err != nil {
		return nil, subscription.Sanitize(err, url)
	}
	if fr.StatusCode < 200 || fr.StatusCode > 299 {
		return nil, fmt.Errorf("the subscription answered http %d", fr.StatusCode)
	}
	// The same subscription fetchProviderDocument asks, so it names the
	// router's brand the same way.
	if id, support, ok := brandFromFetch(fr); ok {
		d.learnBrand(id, brand.SourceSubscription, support)
	}
	return fr.Body, nil
}

func logRefresh(rep routepolicy.RefreshReport) {
	l := logging.L()
	if rep.Nodes == 0 && len(rep.Rebound) == 0 {
		l.Info("the route policy's subscription is unchanged", "subscription", rep.Subscription, "group", rep.Group)
		return
	}
	l.Info("refreshed the route policy's nodes", "subscription", rep.Subscription, "group", rep.Group, "nodes", rep.Nodes, "skipped", len(rep.Skipped))
	for _, r := range rep.Rebound {
		if r.To == "" {
			l.Warn("a slot keeps its old node: the subscription has nothing like it", "ref", r.Ref, "node", r.From)
			continue
		}
		l.Info("a slot moved to the refreshed node", "ref", r.Ref, "from", r.From, "to", r.To, "matched", r.How)
	}
	for _, s := range rep.Skipped {
		l.Info("a subscription line was skipped", "why", s)
	}
}

// ---------------------------------------------------------------- files ----

// nativeImportMargin is what the import leaves free on the flash beyond the
// geo files themselves.
const nativeImportMargin = 2 << 20

// freeBytes is the space an unprivileged write may use where path is.
func freeBytes(path string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}

// writeFileAtomic writes to flash so that a power cut leaves the old file or
// the new one, never a torn one (the store is the only copy of the policy
// once PassWall is gone), and no temporary file behind on a failure.
func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	return localctl.WriteFileAtomic(path, b, mode)
}

// copyFileAtomic is writeFileAtomic for a file too big to hold in memory
// twice: streamed, synced, renamed.
func copyFileAtomic(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error { _ = tmp.Close(); _ = os.Remove(name); return err }
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if _, err := io.Copy(tmp, in); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, dst); err != nil {
		_ = os.Remove(name)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
