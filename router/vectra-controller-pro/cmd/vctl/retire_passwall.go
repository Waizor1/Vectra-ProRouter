package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/jobsafety"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/retire"
	"vectra-controller-pro/internal/supervisor"
)

// PassWall2's retirement, the daemon's side: when to look, what vctl's own
// state says about the conditions, and the hooks the removal needs. What
// goes, the backup and the order are internal/retire's.

// Seams: tests point them at a temp router and a fake opkg.
var (
	retireEnv  = retire.RouterEnv
	retireOpkg = retire.RunOpkg
)

var (
	// retireCheckEvery is how often the loop looks while PassWall2 is on
	// the router: its first look after the window, at most this late.
	retireCheckEvery = 5 * time.Minute
	// retireFailWait is how long a removal that failed waits before the
	// next: opkg refusing once refuses again.
	retireFailWait = 6 * time.Hour
)

// opRetirePassWallNow is `vctl retire-passwall --now` on the loop's queue.
const opRetirePassWallNow = "retire_passwall_now"

// retireReplyWait is how long the socket waits for a retirement asked for
// at the console: two opkg runs at most retire.OpkgTime each, and the data
// plane's return. Past it the answer is "pending" and the loop goes on.
const retireReplyWait = 8 * time.Minute

// maybeRetirePassWall is the loop's look. PassWall2 gone, it is a few file
// stats — and the end of a retirement cut short — unless a person removed it
// and it left something behind (retire.Env.Left): that is finished as a
// retirement too, after the same window. Otherwise, every retireCheckEvery:
// the conditions that cost nothing, the window, and only then what asks the
// kernel.
func (d *daemon) maybeRetirePassWall(ctx context.Context, now time.Time) {
	env := retireEnv()
	present := env.Present()
	if !present {
		// PassWall routing still needs its configuration even after its
		// package disappeared; do not finish cleanup under this source.
		if d.cfg.RouteSource == "passwall" {
			return
		}
		done, err := env.Finish()
		// The cached document can be the only unfinished cleanup left.
		// Retry it independently of breadcrumbs and the retirement clock.
		if rec, ok := env.ReadRecord(); ok && d.cfg.RouteSource == "" && rec.Backup != "" {
			path := passwallDocumentPath(d.cfg.ProviderConfigPath)
			if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
				done = true
				err = errors.Join(err, d.dropRetiredPassWallDocument(rec.Backup))
			}
		}
		if done {
			if err != nil {
				logging.L().Warn("finishing PassWall2's retirement: tidying after it failed", "err", err.Error())
			} else {
				logging.L().Info("finished PassWall2's retirement: nothing on the router says it is owed back any more")
			}
			return
		}
		if !env.Left() {
			// A window measured while it was here does not wait for its
			// return.
			_, _ = env.DropStaleClock()
			return
		}
	}
	if now.Before(d.retireNextAt) {
		return
	}
	d.retireNextAt = now.Add(retireCheckEvery)
	if present {
		if dropped, _ := env.DropStaleRecord(); dropped {
			logging.L().Info("PassWall2 is on the router again: the record of its retirement goes")
		}
	}
	resp := d.retirePassWall(ctx, env, now, false)
	switch resp.Code {
	case "passwall_retired", "apply_failed":
		// retirePassWall said it.
	default:
		// A refusal, or the wait: said when it changes, not every look.
		if why := resp.Code + resp.Detail; why != d.retireSaid && resp.Code != "waiting" {
			d.retireSaid = why
			logging.L().Info("PassWall2 stays for now", "why", resp.Detail)
		}
	}
}

// retirePassWall is the one path to a retirement — the loop's, and `vctl
// retire-passwall`'s (force: --now, which skips the window and nothing else).
// PassWall2 gone, removed by a person, with something of it left
// (retire.Env.Left): the same conditions and the same window — measured
// while it is gone — and then that is finished instead (tidyPassWall).
func (d *daemon) retirePassWall(ctx context.Context, env retire.Env, now time.Time, force bool) localctl.SocketResponse {
	present := env.Present()
	if !present && !env.Left() {
		return localctl.SocketResponse{OK: true, Code: "nothing", Detail: retire.ErrNothing.Error()}
	}
	refused := retireRefused
	if !present {
		refused = tidyRefused
	}
	// A window measured while PassWall2 was in the other state counts for
	// nothing (retire.Env.Since): its clock goes, before anything refuses.
	_, _ = env.DropStaleClock()
	// What costs nothing: UCI, the trial's file, the boot links, the route
	// source's files.
	c := d.retireConditions(ctx)
	if b := c.Blockers(); len(b) > 0 {
		return refused(b)
	}
	// The window. Its clock starts at the first look that finds vctl
	// carrying the traffic, and runs across restarts and reboots; a
	// hand-back (the init script) takes it away.
	window := d.cfg.PassWallRetireAfter()
	since, started := env.Since()
	if !started {
		if err := d.retireCarrying(ctx); err != nil {
			return refused([]string{"vctl does not carry the router's traffic: " + err.Error()})
		}
		var err error
		if since, err = env.StartClock(now); err != nil {
			return refused([]string{"the window's clock could not be written: " + err.Error()})
		}
		if present {
			logging.L().Info(fmt.Sprintf("PassWall2 goes from this router once Vectra has carried its traffic for %s: at %s. `vctl retire-passwall --now` does it at once; UCI retire_passwall '0' keeps it",
				window, since.Add(window).UTC().Format(time.RFC3339)))
		} else {
			logging.L().Info(fmt.Sprintf("PassWall2 was removed by hand from this router and left its configuration, or what the takeover owes it, behind: they go once Vectra has carried the traffic without it for %s: at %s. `vctl retire-passwall --now` does it at once; UCI retire_passwall '0' keeps them",
				window, since.Add(window).UTC().Format(time.RFC3339)))
		}
	}
	due := since.Add(window)
	if !force && now.Before(due) {
		if !present {
			return localctl.SocketResponse{Code: "waiting", Detail: fmt.Sprintf("what PassWall2, removed by hand, left goes at %s: Vectra has carried the router's traffic without it since %s, and waits out %s (`vctl retire-passwall --now` does not wait)",
				due.UTC().Format(time.RFC3339), since.UTC().Format(time.RFC3339), window)}
		}
		return localctl.SocketResponse{Code: "waiting", Detail: fmt.Sprintf("PassWall2 goes at %s: Vectra has carried the router's traffic since %s, and waits out %s (`vctl retire-passwall --now` does not wait)",
			due.UTC().Format(time.RFC3339), since.UTC().Format(time.RFC3339), window)}
	}
	if present && !force && now.Before(d.retireFailedUntil) {
		return localctl.SocketResponse{Code: "waiting", Detail: "the last removal of PassWall2 failed; the next try is at " + d.retireFailedUntil.UTC().Format(time.RFC3339)}
	}
	// What asks the kernel and the rest of the router; room for opkg only
	// where opkg runs.
	c.Carrying = d.retireCarrying(ctx)
	c.PassWallRuns = power.PassWallRunning(powerEnv().ProcDir)
	if present {
		c.Resources = jobsafety.Evaluate("retire_passwall", d.retireRouterResources(), d.cfg.JobSafety).Reasons
	}
	if b := c.Blockers(); len(b) > 0 {
		return refused(b)
	}
	if !present {
		return d.tidyPassWall(env, now)
	}

	res, err := env.Retire(ctx, now, env.GeoOwners(d.retireGeoDirs()), retire.Hooks{
		Opkg:     retireOpkg,
		Carrying: d.retireCarrying,
		Restore:  d.retireRestore,
	})
	switch {
	case errors.Is(err, retire.ErrNothing):
		return localctl.SocketResponse{OK: true, Code: "nothing", Detail: err.Error()}
	case errors.Is(err, retire.ErrRefused):
		return retireRefused([]string{strings.TrimPrefix(err.Error(), retire.ErrRefused.Error()+": ")})
	case err != nil:
		d.retireFailedUntil = now.Add(retireFailWait)
		logging.L().Warn(err.Error(), "retryAt", d.retireFailedUntil.UTC().Format(time.RFC3339))
		return localctl.SocketResponse{Code: "apply_failed", Detail: err.Error()}
	}
	d.retireFailedUntil, d.retireSaid = time.Time{}, ""
	// The passwall route source's document is a cache of PassWall2's
	// configuration, credentials included; native routing renders from it
	// too, so it stays there.
	res.Cleanup = errors.Join(res.Cleanup, d.dropRetiredPassWallDocument(res.Backup))
	detail := fmt.Sprintf("PassWall2 retired: removed %s; its configuration is kept in %s. `vectra off` now leaves the router on plain internet, without a VPN",
		strings.Join(res.Removed, " "), orNone(res.Backup))
	logging.L().Info("PassWall2 retired: its packages are removed and its configuration is backed up; `vectra off` now leaves the router on plain internet",
		"removed", strings.Join(res.Removed, " "), "kept", strings.Join(res.Kept, " "), "backup", orNone(res.Backup))
	if res.Restored {
		if res.Carrying != nil {
			logging.L().Error("PassWall2's removal took a piece of vctl's data plane with it, and it did not come back", "err", res.Carrying.Error())
			detail += "; vctl's data plane did not come back after it: " + res.Carrying.Error()
		} else {
			logging.L().Warn("PassWall2's removal took a piece of vctl's data plane with it; it was put back")
		}
	}
	if res.Cleanup != nil {
		logging.L().Warn("tidying up after PassWall2's retirement; tried again at the next look", "err", res.Cleanup.Error())
	}
	return localctl.SocketResponse{OK: true, Code: "passwall_retired", Detail: detail}
}

// tidyPassWall finishes a removal of PassWall2 a person made, as a
// retirement (retire.Env.Tidy) — and, under the provider route source, the
// passwall route source's document with it, as after a retirement.
func (d *daemon) tidyPassWall(env retire.Env, now time.Time) localctl.SocketResponse {
	res, err := env.Tidy(now)
	switch {
	case errors.Is(err, retire.ErrNothing):
		return localctl.SocketResponse{OK: true, Code: "nothing", Detail: err.Error()}
	case err != nil:
		return tidyRefused([]string{strings.TrimPrefix(err.Error(), retire.ErrRefused.Error()+": ")})
	}
	d.retireSaid = ""
	res.Cleanup = errors.Join(res.Cleanup, d.dropRetiredPassWallDocument(res.Backup))
	logging.L().Info("PassWall2 was removed by hand from this router; what it left is tidied as a retirement: its configuration is backed up, nothing says it is owed back; `vectra off` now leaves the router on plain internet",
		"backup", orNone(res.Backup))
	if res.Cleanup != nil {
		logging.L().Warn("tidying up after PassWall2, removed by hand", "err", res.Cleanup.Error())
	}
	return localctl.SocketResponse{OK: true, Code: "passwall_retired", Detail: fmt.Sprintf("PassWall2 was removed by hand; what it left is tidied: its configuration is kept in %s, and nothing says it is owed back. `vectra off` now leaves the router on plain internet, without a VPN",
		orNone(res.Backup))}
}

// Only remove the credentials cache once the configuration backup is still
// persisted. Native and PassWall routing continue to read this document.
func (d *daemon) dropRetiredPassWallDocument(backup string) error {
	if d.cfg.RouteSource != "" {
		return nil
	}
	path := passwallDocumentPath(d.cfg.ProviderConfigPath)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	st, err := os.Lstat(backup)
	if err != nil {
		return fmt.Errorf("confirming configuration backup: %w", err)
	}
	if !st.Mode().IsRegular() {
		return errors.New("configuration backup is not a regular file")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func retireRefused(why []string) localctl.SocketResponse {
	return localctl.SocketResponse{Code: "refused", Detail: "PassWall2 stays: " + strings.Join(why, "; ")}
}

func tidyRefused(why []string) localctl.SocketResponse {
	return localctl.SocketResponse{Code: "refused", Detail: "what PassWall2, removed by hand, left stays: " + strings.Join(why, "; ")}
}

// retireConditions are the conditions that cost nothing to read.
func (d *daemon) retireConditions(ctx context.Context) retire.Conditions {
	penv := powerEnv()
	// Loaded answers "yes" so that Read asks nft nothing: whether vctl
	// carries the traffic is retireCarrying's.
	penv.Loaded = func(context.Context) bool { return true }
	f := power.Read(ctx, penv, true)
	c := retire.Conditions{
		Disabled:    d.cfg.NoRetirePassWall,
		Trial:       f.Trial != nil,
		Off:         !(f.UCI && f.Boot),
		RouteSource: d.cfg.RouteSource,
	}
	if c.RouteSource == routeSourceNative {
		c.NativeStore = fileExists(nativeStorePath)
		c.NativeGeo = fileExists(filepath.Join(nativeGeoDir, "geoip.dat")) && fileExists(filepath.Join(nativeGeoDir, "geosite.dat"))
	}
	return c
}

// retireCarrying is nil when vctl carries the router's traffic — an operator
// config, not the rescue's direct mode, xray running, its table loaded and
// its policy rule and route in the kernel — else why not. `ip` that cannot
// answer does not count against it.
func (d *daemon) retireCarrying(ctx context.Context) error {
	if d.desired == nil {
		return errors.New("it has no operator config yet")
	}
	spec, ok := firewallSpecFromConfig(d.desired)
	if !ok {
		return errors.New("its operator config has no tproxy inbound")
	}
	spec = withLoadGuards(spec, d.cfg)
	if d.rescueState().Mode == rescue.ModeDirect {
		return errors.New("the rescue holds the router direct")
	}
	if !d.retireXrayRunning() {
		return errors.New("xray does not run")
	}
	if !d.tableLoaded(ctx, spec.TableName) {
		return errors.New("its data plane is not loaded")
	}
	if miss := d.policyRouteMissing(ctx, spec); miss != "" {
		return errors.New(miss)
	}
	return nil
}

func (d *daemon) retireXrayRunning() bool {
	if d.xrayRunning != nil {
		return d.xrayRunning()
	}
	return d.supStarted && d.sup != nil && d.sup.Status().State == supervisor.StateRunning
}

func (d *daemon) retireRouterResources() controlplane.RouterResources {
	if d.retireResources != nil {
		return d.retireResources()
	}
	if d.collector != nil {
		return d.collector.Resources()
	}
	return controlplane.RouterResources{}
}

// retireRestore puts vctl's data plane back after PassWall2's removal took
// a piece of it — its own stop runs inside it, and an older PassWall's
// policy rule is vctl's. PassWall2 is never put back.
func (d *daemon) retireRestore(ctx context.Context) error {
	if d.desired == nil {
		return errors.New("no operator config to load it from")
	}
	if !d.supStarted {
		if !fileExists(d.cfg.XrayRenderPath) {
			return errors.New("there is no render to start xray from")
		}
		d.ensureSupervisor(ctx)
	}
	logging.L().Warn("PassWall2's removal took a piece of vctl's data plane with it; loading it again")
	d.programFirewall(ctx, d.desired)
	return nil
}

// retireGeoDirs are where vctl's xray reads its geo files: the running
// render's directory, the next render's, the operator's resolved — and one a
// render names itself (PassWall's generator writes its own; AdaptPassWall
// takes it out since 0.6.0-r18).
func (d *daemon) retireGeoDirs() []string {
	dirs := []string{d.runningAssetDir(), d.geoAssetDir(), config.ResolveGeoAssetDir(d.cfg.GeoAssetDir)}
	if raw, err := os.ReadFile(d.cfg.XrayRenderPath); err == nil {
		var doc struct {
			Env map[string]string `json:"env"`
		}
		if json.Unmarshal(raw, &doc) == nil && doc.Env[config.XrayAssetEnvKey] != "" {
			dirs = append(dirs, doc.Env[config.XrayAssetEnvKey])
		}
	}
	return dirs
}

// policyRouteMissing says what of the data plane's policy route the kernel
// does not have: "" when it has all of it, or when `ip` cannot say.
func (d *daemon) policyRouteMissing(ctx context.Context, spec firewall.Spec) string {
	rules, err := d.ipShow(ctx, "rule", "show")
	if err != nil {
		return ""
	}
	if !hasFwmarkRule(string(rules), spec.FwMark, spec.RtTable) {
		return fmt.Sprintf("its policy rule (fwmark 0x%x lookup %d) is gone", spec.FwMark, spec.RtTable)
	}
	routes, err := d.ipShow(ctx, "route", "show", "table", strconv.Itoa(spec.RtTable))
	if err != nil {
		return ""
	}
	if !hasLocalDefault(string(routes)) {
		return fmt.Sprintf("its local route in table %d is gone", spec.RtTable)
	}
	return ""
}

func (d *daemon) ipShow(ctx context.Context, args ...string) ([]byte, error) {
	if d.ipOutput != nil {
		return d.ipOutput(ctx, args...)
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(c, "ip", args...).Output()
}

// A rule as ip-full and busybox print it: "100:	from all fwmark 0x1 lookup
// 100", a mask after the mark, "table" for "lookup".
var reFwmarkRule = regexp.MustCompile(`\bfwmark 0x([0-9a-fA-F]+)(?:/0x[0-9a-fA-F]+)?\s+(?:lookup|table)\s+(\d+)\b`)

func hasFwmarkRule(rules string, mark, table int) bool {
	for _, m := range reFwmarkRule.FindAllStringSubmatch(rules, -1) {
		v, err1 := strconv.ParseUint(m[1], 16, 64)
		t, err2 := strconv.Atoi(m[2])
		if err1 == nil && err2 == nil && v == uint64(mark) && t == table {
			return true
		}
	}
	return false
}

func hasLocalDefault(routes string) bool {
	for _, line := range strings.Split(routes, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "local default ") || strings.HasPrefix(line, "local 0.0.0.0/0 ") || line == "local default" {
			return true
		}
	}
	return false
}
