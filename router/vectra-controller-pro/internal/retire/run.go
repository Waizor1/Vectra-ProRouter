package retire

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Why PassWall2 did not go.
var (
	// ErrNothing: PassWall2 is not on this router.
	ErrNothing = errors.New("PassWall2 is not on this router: nothing to retire")
	// ErrRefused: something that must hold did not, and nothing changed.
	ErrRefused = errors.New("PassWall2 stays")
	// ErrFailed: opkg ran and PassWall2 is still here — it stays, as the
	// way back, with what is owed it.
	ErrFailed = errors.New("PassWall2 could not be removed; it stays as the way back")
)

// Conditions are what must hold for PassWall2 to go, as the daemon found
// them. The zero value holds; the daemon fills them in cheapest first.
type Conditions struct {
	// Disabled: UCI retire_passwall is '0'.
	Disabled bool
	// Trial: a trial runs — a reboot is to give the router back.
	Trial bool
	// Off: Vectra is not switched on for good (the UCI switch and the boot
	// links).
	Off bool
	// RouteSource is UCI route_source: "" (the provider's), "passwall" —
	// PassWall2's own configuration and generator — or "native", which needs
	// its own store and its own geo files (NativeStore, NativeGeo).
	RouteSource            string
	NativeStore, NativeGeo bool
	// Carrying is nil while vctl carries the router's traffic, else why not.
	Carrying error
	// PassWallRuns: PassWall2's stack runs.
	PassWallRuns bool
	// Resources are the resource guard's reasons to wait.
	Resources []string
}

// Blockers says why PassWall2 may not go now, cheapest first; nothing when
// it may.
func (c Conditions) Blockers() []string {
	var out []string
	if c.Disabled {
		out = append(out, "UCI retire_passwall is '0': PassWall2 is kept")
	}
	if c.Trial {
		out = append(out, "a trial runs (`vectra keep` keeps Vectra on for good)")
	}
	if c.Off {
		out = append(out, "Vectra is not switched on for good (`vectra on`)")
	}
	switch c.RouteSource {
	case "":
	case "native":
		if !c.NativeStore {
			out = append(out, "route_source 'native' has no route policy of its own yet (/etc/config/vectra_route)")
		}
		if !c.NativeGeo {
			out = append(out, "route_source 'native' still reads PassWall2's geo files: vctl's own are not in place yet")
		}
	case "passwall":
		out = append(out, "route_source 'passwall' routes by PassWall2's own configuration and generator")
	default:
		out = append(out, fmt.Sprintf("route_source '%s' is not one vctl knows", c.RouteSource))
	}
	if c.Carrying != nil {
		out = append(out, "vctl does not carry the router's traffic: "+c.Carrying.Error())
	}
	if c.PassWallRuns {
		out = append(out, "PassWall2 runs")
	}
	for _, r := range c.Resources {
		out = append(out, "too little room for opkg: "+r)
	}
	return out
}

// geoPackages are the list's packages that hold geo files.
var geoPackages = []string{"v2ray-geoip", "v2ray-geosite"}

// GeoOwners names those of geoPackages that own a geo file vctl reads — a
// geoip.dat or geosite.dat in one of dirs, or what one of them is a link to
// — and why. They stay: their files are what xray reads at its next start.
func (e Env) GeoOwners(dirs []string) map[string]string {
	out := map[string]string{}
	for _, pkg := range geoPackages {
		owned := e.owned(pkg)
		for _, dir := range dirs {
			if dir == "" {
				continue
			}
			for _, f := range []string{"geoip.dat", "geosite.dat"} {
				p := filepath.Join(dir, f)
				if owned[p] || owned[realPath(p)] {
					out[pkg] = "vctl reads " + p
				}
			}
		}
	}
	return out
}

// owned are the files a package installed (opkg's <package>.list: a path,
// then a mode and a link's target after tabs), and where each resolves.
func (e Env) owned(pkg string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(filepath.Join(e.InfoDir, pkg+".list"))
	if err != nil {
		return out
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		p, _, _ := strings.Cut(sc.Text(), "\t")
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		out[filepath.Clean(p)] = true
		if r := realPath(p); r != "" {
			out[r] = true
		}
	}
	return out
}

func realPath(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	return r
}

// Hooks are what the retirement cannot see from files: the daemon's.
type Hooks struct {
	// Opkg runs opkg (RunOpkg): its output, combined.
	Opkg func(ctx context.Context, args ...string) ([]byte, error)
	// Carrying is nil while vctl carries the router's traffic, else why not.
	Carrying func(ctx context.Context) error
	// Restore puts vctl's data plane back.
	Restore func(ctx context.Context) error
	// Sleep is between two looks at Carrying (nil: time.Sleep).
	Sleep func(time.Duration)
}

// Result is what a retirement did.
type Result struct {
	Record
	// Restored: PassWall2's removal took a piece of vctl's data plane with it
	// — its own stop runs inside it — and it was put back.
	Restored bool
	// Carrying is nil when vctl carries the router's traffic after it all.
	Carrying error
	// Cleanup is what could not be tidied after PassWall2 went (Finish
	// tries again).
	Cleanup error
}

// SettleLooks is how many looks, a second apart, vctl's data plane gets to
// come back once it was put back.
var SettleLooks = 20

// Retire takes PassWall2 off the router; the caller has seen every condition
// hold (Conditions). In this order, each step only once the one before it
// held:
//
//  1. the plan: what of the list goes, from opkg's status — nothing when
//     PassWall2 itself is needed by another package;
//  2. its configuration backed up (Backup);
//  3. the record written: PassWall2 is going, not merely absent for an
//     upgrade — the hand-back reads it;
//  4. `opkg remove` of PassWall2 and its translations (its own prerm stops
//     and disables it): still here after, and it stays — the record goes,
//     what is owed it stays owed, its helpers are not touched;
//  5. `opkg remove` of its helpers: what opkg refuses stays;
//  6. vctl's data plane looked at, and put back when PassWall2's stop took a
//     piece of it (an older PassWall's rule is vctl's fwmark 1 / table 100);
//  7. what the takeover owed PassWall2 forgotten, its configuration taken
//     (it is in the backup: opkg keeps a conffile that was changed), the
//     clock stopped.
//
// No opkg flag is ever passed: no --autoremove, no --force-*.
func (e Env) Retire(ctx context.Context, now time.Time, protect map[string]string, h Hooks) (Result, error) {
	installed, err := e.Installed()
	if err != nil {
		return Result{}, fmt.Errorf("%w: opkg's status: %v", ErrRefused, err)
	}
	if _, ok := installed[App]; !ok {
		for _, p := range e.PassWall {
			if p != "" && exists(p) {
				return Result{}, fmt.Errorf("%w: %s is on the router, but no package installed it: opkg cannot take it away, a person can", ErrRefused, p)
			}
		}
		return Result{}, ErrNothing
	}
	plan := MakePlan(installed, protect)
	if why := plan.Keep[App]; why != "" {
		return Result{}, fmt.Errorf("%w: %s is %s", ErrRefused, App, why)
	}
	backup, err := e.Backup(now)
	if err != nil {
		return Result{}, fmt.Errorf("%w: its configuration could not be backed up: %v", ErrRefused, err)
	}
	rec := Record{At: now.UTC().Truncate(time.Second), Removed: plan.Remove, Kept: sortedNames(plan.Keep), Backup: backup}
	if err := e.writeRecord(rec); err != nil {
		return Result{}, fmt.Errorf("%w: its record could not be written: %v", ErrRefused, err)
	}

	first, rest := split(plan.Remove)
	out, oerr := h.Opkg(ctx, append([]string{"remove"}, first...)...)
	if e.Present() {
		_ = e.dropRecord()
		return Result{}, fmt.Errorf("%w: opkg remove %s: %v: %s", ErrFailed, strings.Join(first, " "), oerr, tail(out))
	}
	if len(rest) > 0 {
		_, _ = h.Opkg(ctx, append([]string{"remove"}, rest...)...)
	}
	res := Result{Record: rec}
	if after, err := e.Installed(); err == nil {
		res.Removed, res.Kept = nil, sortedNames(plan.Keep)
		for _, n := range plan.Remove {
			if _, still := after[n]; still {
				res.Kept = append(res.Kept, n)
			} else {
				res.Removed = append(res.Removed, n)
			}
		}
		sort.Strings(res.Kept)
	}
	res.Cleanup = e.writeRecord(res.Record)

	if err := h.Carrying(ctx); err != nil {
		res.Restored = true
		if rerr := h.Restore(ctx); rerr != nil {
			res.Carrying = fmt.Errorf("%v; putting it back failed: %w", err, rerr)
		} else {
			res.Carrying = settle(ctx, h)
		}
	}
	res.Cleanup = errors.Join(res.Cleanup, e.forget(), e.dropConfigs(backup), e.stopClock())
	return res, nil
}

// split is the plan's first opkg run — everything up to PassWall2 itself,
// which needs it — and the rest.
func split(remove []string) (first, rest []string) {
	for i, n := range remove {
		if n == App {
			return remove[:i+1], remove[i+1:]
		}
	}
	return remove, nil
}

// settle waits for vctl to carry the traffic again.
func settle(ctx context.Context, h Hooks) error {
	sleep := h.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	var err error
	for i := 0; i < SettleLooks; i++ {
		if err = h.Carrying(ctx); err == nil || ctx.Err() != nil {
			return err
		}
		sleep(time.Second)
	}
	return err
}

// dropConfigs takes PassWall2's configuration off the router once it is in
// a backup — its nodes' credentials included; nothing without one.
func (e Env) dropConfigs(backup string) error {
	if backup == "" {
		return nil
	}
	st, err := os.Lstat(backup)
	if err != nil {
		return fmt.Errorf("confirming configuration backup: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("configuration backup is not a regular file")
	}
	var errs []error
	for _, c := range e.Configs {
		errs = append(errs, removeIfThere(c))
	}
	return errors.Join(errs...)
}

// Finish completes a retirement that was cut short — vctl stopped between
// opkg and what follows it, an /etc it could not write: PassWall2 gone, its
// record here, and still a breadcrumb of what the takeover owed it, or the
// clock. Only with the record: PassWall2 absent without one is an upgrade of
// it in flight, and what is owed it stays owed. It says whether it did
// anything.
func (e Env) Finish() (bool, error) {
	if e.Present() {
		return false, nil
	}
	rec, ok := e.ReadRecord()
	if !ok {
		return false, nil
	}
	configs := false
	if rec.Backup != "" {
		for _, c := range e.Configs {
			if _, err := os.Lstat(c); !errors.Is(err, os.ErrNotExist) {
				configs = true
			}
		}
	}
	if !e.owes() && !exists(filepath.Join(e.MarkerDir, ClockName)) && !configs {
		return false, nil
	}
	var cleanup error
	if configs {
		cleanup = e.dropConfigs(rec.Backup)
	}
	return true, errors.Join(e.forget(), cleanup, e.stopClock())
}

// Tidy finishes a removal of PassWall2 a person made: its packages gone by
// hand (as docs/CANARY.md once told operators to), no record of a
// retirement, and something of it left (Left). The caller has seen every
// condition hold and the window, measured while PassWall2 was absent, pass.
// What Retire does once opkg is done, and nothing else: its configuration
// backed up, the record written (by hand: nothing removed by opkg), what the
// takeover owed it forgotten, its configuration taken, the clock stopped. An
// init script of PassWall2's still on the router is a person's to take (see
// Retire): refused.
func (e Env) Tidy(now time.Time) (Result, error) {
	if e.Present() {
		for _, p := range e.PassWall {
			if p != "" && exists(p) {
				return Result{}, fmt.Errorf("%w: %s is on the router", ErrRefused, p)
			}
		}
		return Result{}, fmt.Errorf("%w: %s is installed", ErrRefused, App)
	}
	if !e.Left() {
		return Result{}, ErrNothing
	}
	backup, err := e.Backup(now)
	if err != nil {
		return Result{}, fmt.Errorf("%w: its configuration could not be backed up: %v", ErrRefused, err)
	}
	rec := Record{At: now.UTC().Truncate(time.Second), Backup: backup, ByHand: true}
	if err := e.writeRecord(rec); err != nil {
		return Result{}, fmt.Errorf("%w: its record could not be written: %v", ErrRefused, err)
	}
	return Result{Record: rec, Cleanup: errors.Join(e.forget(), e.dropConfigs(backup), e.stopClock())}, nil
}

// DropStaleRecord drops the record of a retirement PassWall2 is back from —
// reinstalled, or a removal cut short before it went: the record no longer
// says what the router is. It says whether there was one.
func (e Env) DropStaleRecord() (bool, error) {
	if !e.Present() {
		return false, nil
	}
	if _, ok := e.ReadRecord(); !ok {
		return false, nil
	}
	return true, e.dropRecord()
}

func sortedNames(m map[string]string) []string {
	var out []string
	for n := range m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// tail is the end of opkg's output, one line.
func tail(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 300 {
		s = "…" + s[len(s)-300:]
	}
	return s
}

// Describe is a plan's or a record's kept packages with why, for a log line.
func Describe(keep map[string]string) string { return describe(keep) }
