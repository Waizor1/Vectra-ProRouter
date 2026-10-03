package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/retire"
	"vectra-controller-pro/internal/uiapi"
)

// A PassWall2 a person removed — `opkg remove luci-app-passwall2` and its
// helpers, as docs/CANARY.md once said to — from a router Vectra holds: the
// test router as it is. No package and no init script of PassWall2's, no
// record of a retirement; the takeover's breadcrumbs, PassWall2's
// configuration and vctl's cache of it (passwall-config.json) left behind.
func newHandRemovedRouter(t *testing.T) *retireRouter {
	t.Helper()
	r := newRetireRouter(t)
	r.write(r.env.StatusFile, "Package: xray-core\nStatus: install user installed\n\nPackage: dnsmasq-full\nProvides: dnsmasq\nStatus: install user installed\n", 0o644)
	for _, p := range []string{"luci-app-passwall2", "luci-i18n-passwall2-ru", "geoview", "tcping", "v2ray-geoip", "v2ray-geosite"} {
		_ = os.Remove(filepath.Join(r.env.InfoDir, p+".control"))
	}
	_ = os.Remove(r.env.PassWall[0])
	if r.env.Present() || !r.env.Left() {
		t.Fatal("not a PassWall2 removed by hand")
	}
	return r
}

// putBack is PassWall2 installed again (an opkg upgrade of it done, or a
// person's install), takeAway is it gone again.
func (r *retireRouter) putBack() {
	r.write(filepath.Join(r.env.InfoDir, retire.App+".control"), "Package: "+retire.App+"\n", 0o644)
	r.write(r.env.PassWall[0], "#!/bin/sh /etc/rc.common\n", 0o755)
}

func (r *retireRouter) takeAway() {
	_ = os.Remove(filepath.Join(r.env.InfoDir, retire.App+".control"))
	_ = os.Remove(r.env.PassWall[0])
}

// left says whether what PassWall2 left is all still there — the breadcrumbs,
// its configuration, vctl's cache — with no record.
func (r *retireRouter) left() bool {
	_, rec := r.env.ReadRecord()
	return !rec && fileExists(filepath.Join(r.env.MarkerDir, ".passwall-disabled-by-vctl")) &&
		fileExists(filepath.Join(r.env.MarkerDir, ".passwall-switch-off-by-vctl")) &&
		fileExists(r.env.Configs[0]) && fileExists(passwallDocumentPath(r.d.cfg.ProviderConfigPath))
}

// tidied says whether it was finished as a retirement by hand.
func (r *retireRouter) tidied() bool {
	rec, ok := r.env.ReadRecord()
	s, _ := r.env.State()
	return ok && rec.ByHand && s == "retired" && rec.Backup != "" && fileExists(rec.Backup) &&
		!fileExists(filepath.Join(r.env.MarkerDir, ".passwall-disabled-by-vctl")) &&
		!fileExists(filepath.Join(r.env.MarkerDir, ".passwall-switch-off-by-vctl")) &&
		!fileExists(r.env.Configs[0]) && !fileExists(passwallDocumentPath(r.d.cfg.ProviderConfigPath)) &&
		!fileExists(filepath.Join(r.env.MarkerDir, retire.ClockName))
}

// The loop finishes it as a retirement by hand — after the same window, its
// clock started at the first look that finds PassWall2 gone and vctl
// carrying the traffic; never with opkg.
func TestTheLoopTidiesAPassWallRemovedByHandAfterTheWindow(t *testing.T) {
	r := newHandRemovedRouter(t)
	ctx := context.Background()
	r.d.maybeRetirePassWall(ctx, r0)
	if since, ok := r.env.Since(); !ok || !since.Equal(r0) {
		t.Fatalf("the window did not start: %v %v", since, ok)
	}
	if !strings.Contains(r.logs.String(), "removed by hand") {
		t.Fatalf("the start of the window is not said:\n%s", r.logs.String())
	}
	for _, at := range []time.Duration{time.Minute, 6 * time.Minute, 12 * time.Hour, 23*time.Hour + 59*time.Minute} {
		r.d.maybeRetirePassWall(ctx, r0.Add(at))
	}
	if !r.left() {
		t.Fatal("tidied inside the window")
	}
	r.d.maybeRetirePassWall(ctx, r0.Add(24*time.Hour+6*time.Minute))
	if !r.tidied() {
		t.Fatalf("not tidied after the window:\n%s", r.logs.String())
	}
	if len(r.opkg) != 0 {
		t.Fatalf("opkg ran for a PassWall2 a person removed: %v", r.opkg)
	}
	if !strings.Contains(r.logs.String(), "by hand") || !strings.Contains(r.logs.String(), "`vectra off` now leaves the router on plain internet") {
		t.Fatalf("no line says it:\n%s", r.logs.String())
	}
	// Done: later looks are file stats.
	looks := 0
	r.d.tableLoaded = func(context.Context, string) bool { looks++; return true }
	before, _ := r.env.ReadRecord()
	r.d.maybeRetirePassWall(ctx, r0.Add(48*time.Hour))
	if after, _ := r.env.ReadRecord(); looks != 0 || !after.At.Equal(before.At) {
		t.Fatalf("looked again after the tidy: kernel asked %d times, record %+v", looks, after)
	}
}

// PassWall2 gone for the seconds of an opkg upgrade, just as the loop looks,
// long after the retirement's window: never taken for a removal by hand —
// its absence starts a window of its own, and the retirement's starts over
// when it is back.
func TestAnUpgradeOfPassWallIsNeverTakenForARemovalByHand(t *testing.T) {
	r := newRetireRouter(t)
	ctx := context.Background()
	if _, err := r.env.StartClock(r0.Add(-25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	r.takeAway()
	r.d.maybeRetirePassWall(ctx, r0)
	if !r.left() {
		t.Fatal("a PassWall2 absent for an upgrade was tidied")
	}
	if since, ok := r.env.Since(); !ok || !since.Equal(r0) {
		t.Fatalf("its absence does not start a window of its own: %v %v", since, ok)
	}
	r.putBack()
	r.d.maybeRetirePassWall(ctx, r0.Add(6*time.Minute))
	if len(r.opkg) != 0 {
		t.Fatalf("PassWall2 back from its upgrade was removed at once: %v", r.opkg)
	}
	if since, ok := r.env.Since(); !ok || !since.Equal(r0.Add(6*time.Minute)) {
		t.Fatalf("the retirement's window did not start over: %v %v", since, ok)
	}
}

// A window of absence does not survive PassWall2's return, even one the loop
// only refused (UCI retire_passwall '0'): removed again, it waits out a
// whole window of absence once more.
func TestAWindowOfAbsenceEndsWhenPassWallIsBack(t *testing.T) {
	r := newHandRemovedRouter(t)
	ctx := context.Background()
	r.d.maybeRetirePassWall(ctx, r0)
	r.putBack()
	r.d.cfg.NoRetirePassWall = true
	r.d.maybeRetirePassWall(ctx, r0.Add(time.Hour))
	r.takeAway()
	r.d.cfg.NoRetirePassWall = false
	r.d.maybeRetirePassWall(ctx, r0.Add(2*time.Hour))
	if since, ok := r.env.Since(); !ok || !since.Equal(r0.Add(2*time.Hour)) {
		t.Fatalf("the window of absence did not start over: %v %v", since, ok)
	}
	r.d.maybeRetirePassWall(ctx, r0.Add(24*time.Hour+6*time.Minute))
	if !r.left() {
		t.Fatal("tidied after 22 hours of absence")
	}
	r.d.maybeRetirePassWall(ctx, r0.Add(26*time.Hour+6*time.Minute))
	if !r.tidied() {
		t.Fatal("not tidied after a whole window of absence")
	}
}

// `vctl retire-passwall --now` tidies it at once — the window, and nothing
// else, skipped — and says so.
func TestNowTidiesAPassWallRemovedByHand(t *testing.T) {
	r := newHandRemovedRouter(t)
	resp := r.d.retirePassWall(context.Background(), r.env, r0, true)
	if !resp.OK || resp.Code != "passwall_retired" || !r.tidied() || len(r.opkg) != 0 {
		t.Fatalf("--now: %+v, opkg %v", resp, r.opkg)
	}
	if !strings.Contains(resp.Detail, "by hand") || !strings.Contains(resp.Detail, "vectra off") {
		t.Fatalf("the answer does not say what was done and what `vectra off` does now: %s", resp.Detail)
	}
}

// The retirement's conditions hold for the tidy too, --now or not; nothing
// changes while one does not.
func TestTheConditionsRefuseTheTidy(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(r *retireRouter)
		want string
	}{
		{"disabled", func(r *retireRouter) { r.d.cfg.NoRetirePassWall = true }, "retire_passwall"},
		{"trial", func(r *retireRouter) { r.write(powerEnv().Trial, `{"id":"x"}`, 0o644) }, "trial"},
		{"off", func(r *retireRouter) { _ = os.Remove(filepath.Join(powerEnv().RCDir, "S95vectra-controller-pro")) }, "vectra on"},
		{"route source passwall", func(r *retireRouter) { r.d.cfg.RouteSource = routeSourcePassWall }, "route_source 'passwall'"},
		{"no data plane", func(r *retireRouter) { r.table = false }, "data plane"},
		{"xray down", func(r *retireRouter) { r.running = false }, "xray"},
		{"direct", func(r *retireRouter) { r.d.st.Rescue.Mode = string(rescue.ModeDirect) }, "direct"},
		{"no operator config", func(r *retireRouter) { r.d.desired = nil }, "operator config"},
		{"PassWall runs", func(r *retireRouter) {
			r.write(filepath.Join(powerEnv().ProcDir, "4100", "cmdline"), "/tmp/etc/passwall2/bin/xray\x00run\x00", 0o644)
		}, "PassWall2 runs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newHandRemovedRouter(t)
			tc.set(r)
			resp := r.d.retirePassWall(context.Background(), r.env, r0, true)
			if resp.OK || resp.Code != "refused" || !strings.Contains(resp.Detail, tc.want) {
				t.Fatalf("%+v, want a refusal naming %q", resp, tc.want)
			}
			if !r.left() {
				t.Fatal("something changed")
			}
		})
	}
}

// No opkg runs: its room is not asked for.
func TestTheTidyNeedsNoRoomForOpkg(t *testing.T) {
	r := newHandRemovedRouter(t)
	r.d.retireResources = func() controlplane.RouterResources {
		return controlplane.RouterResources{MemoryAvailableMB: 20, OverlayFreeMB: 1, TMPFreeMB: 2}
	}
	if resp := r.d.retirePassWall(context.Background(), r.env, r0, true); !resp.OK || !r.tidied() {
		t.Fatalf("%+v", resp)
	}
}

// Native routing: not before its own store and geo files are in place
// (PassWall2's configuration is what it imports from), and its document,
// its own render's, stays.
func TestNativeRoutingTidiesOnlyWithItsOwnStore(t *testing.T) {
	r := newHandRemovedRouter(t)
	r.d.cfg.RouteSource = routeSourceNative
	prevStore, prevGeo := nativeStorePath, nativeGeoDir
	nativeStorePath, nativeGeoDir = filepath.Join(r.dir, "config", "vectra_route"), filepath.Join(r.dir, "route-geo")
	t.Cleanup(func() { nativeStorePath, nativeGeoDir = prevStore, prevGeo })
	if resp := r.d.retirePassWall(context.Background(), r.env, r0, true); resp.OK || !strings.Contains(resp.Detail, "vectra_route") || !r.left() {
		t.Fatalf("native without its store: %+v", resp)
	}
	r.write(nativeStorePath, "config global\n", 0o600)
	r.write(filepath.Join(nativeGeoDir, "geoip.dat"), "x", 0o644)
	r.write(filepath.Join(nativeGeoDir, "geosite.dat"), "x", 0o644)
	if resp := r.d.retirePassWall(context.Background(), r.env, r0, true); !resp.OK {
		t.Fatalf("native with its store and geo files: %+v", resp)
	}
	if fileExists(r.env.Configs[0]) || !fileExists(passwallDocumentPath(r.d.cfg.ProviderConfigPath)) {
		t.Fatal("PassWall2's configuration kept, or native routing's own document gone")
	}
}

// Nothing of PassWall2 left (a router that never had it, or one tidied):
// nothing to do, and nothing asked of the kernel.
func TestNothingLeftIsNothingToDo(t *testing.T) {
	r := newHandRemovedRouter(t)
	for _, p := range []string{".passwall-disabled-by-vctl", ".passwall-switch-off-by-vctl"} {
		_ = os.Remove(filepath.Join(r.env.MarkerDir, p))
	}
	_ = os.Remove(r.env.Configs[0])
	looks := 0
	r.d.tableLoaded = func(context.Context, string) bool { looks++; return true }
	for i := 0; i < 3; i++ {
		r.d.maybeRetirePassWall(context.Background(), r0.Add(time.Duration(i)*time.Hour))
	}
	if looks != 0 || fileExists(filepath.Join(r.env.MarkerDir, retire.ClockName)) {
		t.Fatalf("kernel asked %d times, or a clock started", looks)
	}
	if resp := r.d.retirePassWall(context.Background(), r.env, r0, true); !resp.OK || resp.Code != "nothing" {
		t.Fatalf("%+v", resp)
	}
	if _, ok := r.env.ReadRecord(); ok {
		t.Fatal("a record of nothing")
	}
}

// An init script of PassWall2's that no package installed stays a person's
// to take: refused, nothing tidied.
func TestAnInitScriptWithoutItsPackageIsStillRefused(t *testing.T) {
	r := newHandRemovedRouter(t)
	r.write(r.env.PassWall[0], "#!/bin/sh /etc/rc.common\n", 0o755)
	resp := r.d.retirePassWall(context.Background(), r.env, r0, true)
	if resp.OK || resp.Code != "refused" || !strings.Contains(resp.Detail, r.env.PassWall[0]) {
		t.Fatalf("%+v, want a refusal naming the init script", resp)
	}
	if !r.left() || len(r.opkg) != 0 {
		t.Fatalf("something changed: opkg %v", r.opkg)
	}
}

// `ubus call vectra status`: absent before, retired (with when) after.
func TestStatusSaysAPassWallRemovedByHandRetired(t *testing.T) {
	r := newHandRemovedRouter(t)
	fakeGather(t)
	fakeUILock(t, "0", nil)
	status := func() uiapi.Legacy {
		st, ok := rpcdCall(context.Background(), powerCfg(t), "status", nil).(uiapi.Status)
		if !ok {
			t.Fatal("status did not answer")
		}
		return st.Legacy
	}
	if l := status(); l.Passwall == nil || *l.Passwall != "absent" {
		t.Fatalf("before: %+v", l)
	}
	if resp := r.d.retirePassWall(context.Background(), r.env, r0, true); !resp.OK {
		t.Fatalf("%+v", resp)
	}
	if l := status(); l.Passwall == nil || *l.Passwall != "retired" || l.PasswallRetiredAt == nil || *l.PasswallRetiredAt != "2026-09-30T12:00:00Z" {
		t.Fatalf("after: %+v", l)
	}
}
