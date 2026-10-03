package retire

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeRouter is testEnv with PassWall2 installed as a fleet router has it,
// and an opkg that removes what it is asked to as the real one does: each
// named package out of the status file, its files gone — its modified
// conffile, /etc/config/passwall2, left behind ("Not deleting modified
// conffile") — unless another installed package still needs it.
type fakeRouter struct {
	t    *testing.T
	env  Env
	opkg [][]string
	// refuse: packages opkg will not remove (it names the reason).
	refuse map[string]bool
	// carrying: what vctl's check says, look by look (the last one repeats).
	carrying []error
	looks    int
	restored int
}

func newFakeRouter(t *testing.T) *fakeRouter {
	t.Helper()
	r := &fakeRouter{t: t, env: testEnv(t), refuse: map[string]bool{}}
	write(t, r.env.StatusFile, fleetStatus, 0o644)
	for _, p := range []string{App, "luci-i18n-passwall2-zh-cn", "chinadns-ng", "geoview", "tcping", "v2ray-geoip", "v2ray-geosite"} {
		write(t, filepath.Join(r.env.InfoDir, p+".control"), "Package: "+p+"\n", 0o644)
	}
	write(t, filepath.Join(r.env.InfoDir, App+".list"), r.env.PassWall[0]+"\n", 0o644)
	write(t, r.env.PassWall[0], "#!/bin/sh /etc/rc.common\n", 0o755)
	write(t, r.env.Configs[0], "config global\n\toption enabled '0'\n\toption node 'myshunt'\n", 0o600)
	// The takeover's breadcrumbs: PassWall2's link and switch are owed back.
	for _, n := range owedNames {
		write(t, filepath.Join(r.env.MarkerDir, n), "", 0o644)
	}
	if _, err := r.env.StartClock(t0.Add(-25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *fakeRouter) hooks() Hooks {
	return Hooks{
		Opkg: func(_ context.Context, args ...string) ([]byte, error) {
			r.opkg = append(r.opkg, args)
			if len(args) == 0 || args[0] != "remove" {
				return nil, errors.New("unexpected opkg " + strings.Join(args, " "))
			}
			st := ParseStatus(mustRead(r.t, r.env.StatusFile))
			var out []string
			failed := false
			for _, p := range args[1:] {
				if r.refuse[p] {
					out = append(out, "Package "+p+" is depended upon by packages: my-monitor")
					failed = true
					continue
				}
				delete(st, p)
				_ = os.Remove(filepath.Join(r.env.InfoDir, p+".control"))
				if p == App {
					_ = os.Remove(r.env.PassWall[0])
					out = append(out, "Not deleting modified conffile "+r.env.Configs[0]+".")
				}
				out = append(out, "Removing package "+p+" from root...")
			}
			r.writeStatus(st)
			if failed {
				return []byte(strings.Join(out, "\n")), errors.New("exit status 255")
			}
			return []byte(strings.Join(out, "\n")), nil
		},
		Carrying: func(context.Context) error {
			i := r.looks
			r.looks++
			if len(r.carrying) == 0 {
				return nil
			}
			if i >= len(r.carrying) {
				i = len(r.carrying) - 1
			}
			return r.carrying[i]
		},
		Restore: func(context.Context) error { r.restored++; return nil },
		Sleep:   func(time.Duration) {},
	}
}

// writeStatus writes back what is left, as a status file.
func (r *fakeRouter) writeStatus(st map[string]Package) {
	var b strings.Builder
	for _, p := range st {
		b.WriteString("Package: " + p.Name + "\n")
		if len(p.Depends) > 0 {
			b.WriteString("Depends: " + strings.Join(p.Depends, ", ") + "\n")
		}
		if len(p.Provides) > 0 {
			b.WriteString("Provides: " + strings.Join(p.Provides, ", ") + "\n")
		}
		b.WriteString("Status: install user installed\n\n")
	}
	write(r.t, r.env.StatusFile, b.String(), 0o644)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The whole retirement on a fleet router: the backup first, the record
// before anything goes, PassWall2 and its translations in one opkg run and
// its helpers in a second once it is gone — each in the order opkg accepts —
// PassWall2's leftover configuration taken (it is in the backup), nothing
// owed it any more, the clock stopped.
func TestRetireTakesPassWallOff(t *testing.T) {
	r := newFakeRouter(t)
	conf := string(mustRead(t, r.env.Configs[0]))
	res, err := r.env.Retire(context.Background(), t0, nil, r.hooks())
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"remove", "luci-i18n-passwall2-zh-cn", App},
		{"remove", "chinadns-ng", "geoview", "tcping", "v2ray-geoip", "v2ray-geosite"},
	}
	if !reflect.DeepEqual(r.opkg, want) {
		t.Fatalf("opkg %v, want %v", r.opkg, want)
	}
	for _, run := range r.opkg {
		for _, a := range run {
			if strings.HasPrefix(a, "-") {
				t.Fatalf("opkg run with a flag: %v", run)
			}
		}
	}
	removed := append(append([]string{}, want[0][1:]...), want[1][1:]...)
	if !reflect.DeepEqual(res.Removed, removed) || res.Backup == "" || res.Restored || res.Carrying != nil {
		t.Fatalf("result %+v", res)
	}
	if got := untar(t, res.Backup)[strings.TrimPrefix(r.env.Configs[0], "/")]; got != conf {
		t.Fatalf("the backup holds %q, want the configuration as it was", got)
	}
	if exists(r.env.Configs[0]) {
		t.Fatal("PassWall2's configuration left on the router (opkg keeps a modified conffile); it is in the backup")
	}
	if r.env.owes() {
		t.Fatal("a breadcrumb still says PassWall2 is owed back")
	}
	if _, ok := r.env.Since(); ok {
		t.Fatal("the clock still runs")
	}
	rec, ok := r.env.ReadRecord()
	if !ok || !rec.At.Equal(t0) || !reflect.DeepEqual(rec.Removed, removed) || rec.Backup != res.Backup {
		t.Fatalf("record %+v (%v)", rec, ok)
	}
	if s, at := r.env.State(); s != "retired" || !at.Equal(t0) {
		t.Fatalf("state %s %v", s, at)
	}
}

// The record is there before opkg runs: whatever happens meanwhile, the
// hand-back knows PassWall2 is going, not merely absent for an upgrade.
func TestTheRecordIsWrittenBeforeOpkgRuns(t *testing.T) {
	r := newFakeRouter(t)
	h := r.hooks()
	remove := h.Opkg
	h.Opkg = func(ctx context.Context, args ...string) ([]byte, error) {
		if _, ok := r.env.ReadRecord(); !ok {
			t.Error("opkg runs before the record is written")
		}
		if rec, ok := r.env.ReadRecord(); !ok || !exists(rec.Backup) {
			t.Error("opkg runs before the backup is made")
		}
		return remove(ctx, args...)
	}
	if _, err := r.env.Retire(context.Background(), t0, nil, h); err != nil {
		t.Fatal(err)
	}
}

// PassWall2's own stop, inside its prerm, took a piece of vctl's data plane
// (an older PassWall's rule is vctl's fwmark 1 / table 100): it is put back,
// and PassWall2 is not.
func TestADataPlanePassWallsStopBrokeIsPutBack(t *testing.T) {
	r := newFakeRouter(t)
	r.carrying = []error{errors.New("its policy route is missing"), errors.New("its policy route is missing"), nil}
	res, err := r.env.Retire(context.Background(), t0, nil, r.hooks())
	if err != nil {
		t.Fatal(err)
	}
	if r.restored != 1 || !res.Restored || res.Carrying != nil {
		t.Fatalf("restored %d, result %+v", r.restored, res)
	}
	for _, run := range r.opkg {
		if run[0] != "remove" {
			t.Fatalf("opkg %v: nothing may put PassWall2 back", run)
		}
	}
}

// Put back and still not carrying: said, and PassWall2 stays gone.
func TestADataPlaneThatDoesNotComeBackIsSaid(t *testing.T) {
	r := newFakeRouter(t)
	r.carrying = []error{errors.New("xray does not run")}
	res, err := r.env.Retire(context.Background(), t0, nil, r.hooks())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Restored || res.Carrying == nil || !strings.Contains(res.Carrying.Error(), "xray does not run") {
		t.Fatalf("result %+v", res)
	}
	if r.env.Present() {
		t.Fatal("PassWall2 back")
	}
}

// PassWall2 needed by another package: nothing is touched.
func TestAPassWallSomethingNeedsIsNotTouched(t *testing.T) {
	r := newFakeRouter(t)
	write(t, r.env.StatusFile, fleetStatus+"\nPackage: luci-theme-passwall\nDepends: luci-app-passwall2\nStatus: install user installed\n", 0o644)
	_, err := r.env.Retire(context.Background(), t0, nil, r.hooks())
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "luci-theme-passwall") {
		t.Fatalf("err %v, want a refusal naming luci-theme-passwall", err)
	}
	r.untouched()
}

// opkg refusing PassWall2 itself: PassWall2 stays whole, and stays the way
// back — its helpers untouched, its record gone, its breadcrumbs kept.
func TestAFailedRemovalLeavesPassWallTheWayBack(t *testing.T) {
	r := newFakeRouter(t)
	r.refuse[App] = true
	_, err := r.env.Retire(context.Background(), t0, nil, r.hooks())
	if !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "depended upon") {
		t.Fatalf("err %v, want a failure with opkg's words", err)
	}
	if len(r.opkg) != 1 {
		t.Fatalf("opkg %v: the helpers must not go while PassWall2 stays", r.opkg)
	}
	if _, ok := r.env.ReadRecord(); ok {
		t.Fatal("a record says PassWall2 was retired, and it is still here")
	}
	if !r.env.owes() || !exists(r.env.Configs[0]) {
		t.Fatal("the way back went: breadcrumbs or configuration removed")
	}
	if _, ok := r.env.Since(); !ok {
		t.Fatal("the clock was stopped by a retirement that did not happen")
	}
}

// A helper opkg refused while PassWall2 went: the retirement stands, and
// the record says what stayed.
func TestAHelperOpkgRefusedStays(t *testing.T) {
	r := newFakeRouter(t)
	r.refuse["tcping"] = true
	res, err := r.env.Retire(context.Background(), t0, nil, r.hooks())
	if err != nil {
		t.Fatal(err)
	}
	if contains(res.Removed, "tcping") || !contains(res.Kept, "tcping") {
		t.Fatalf("result %+v", res)
	}
	if rec, _ := r.env.ReadRecord(); !contains(rec.Kept, "tcping") || contains(rec.Removed, "tcping") {
		t.Fatalf("record %+v", rec)
	}
}

// A backup that cannot be made: nothing goes.
func TestNoBackupNothingGoes(t *testing.T) {
	r := newFakeRouter(t)
	r.env.BackupDir = filepath.Join(r.env.Configs[0], "backup") // under a file: cannot be made
	_, err := r.env.Retire(context.Background(), t0, nil, r.hooks())
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err %v, want a refusal", err)
	}
	r.untouched()
}

// An init script of PassWall2's no package installed (copied by hand): opkg
// cannot take it, so nothing is.
func TestAnInitScriptOfNoPackageIsLeftToAPerson(t *testing.T) {
	r := newFakeRouter(t)
	st := ParseStatus([]byte(fleetStatus))
	delete(st, App)
	delete(st, "luci-i18n-passwall2-zh-cn")
	r.writeStatus(st)
	_, err := r.env.Retire(context.Background(), t0, nil, r.hooks())
	if !errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), r.env.PassWall[0]) {
		t.Fatalf("err %v, want a refusal naming the init script", err)
	}
	if len(r.opkg) != 0 {
		t.Fatalf("opkg ran: %v", r.opkg)
	}
}

// Not on the router at all: nothing to do.
func TestNothingToRetire(t *testing.T) {
	e := testEnv(t)
	write(t, e.StatusFile, "Package: xray-core\nStatus: install user installed\n", 0o644)
	if _, err := e.Retire(context.Background(), t0, nil, Hooks{}); !errors.Is(err, ErrNothing) {
		t.Fatalf("err %v, want ErrNothing", err)
	}
}

func (r *fakeRouter) untouched() {
	r.t.Helper()
	if len(r.opkg) != 0 {
		r.t.Fatalf("opkg ran: %v", r.opkg)
	}
	if _, ok := r.env.ReadRecord(); ok {
		r.t.Fatal("a record written")
	}
	if !r.env.owes() || !exists(r.env.Configs[0]) || !r.env.Present() {
		r.t.Fatal("something of PassWall2 went")
	}
}

// vctl killed between opkg and its cleanup: the next look finishes it —
// nothing owed any more, the clock stopped. Not before PassWall2 is gone,
// and not without the record: an absent PassWall2 without it is an upgrade
// in flight, and its breadcrumbs are owed.
func TestFinishCompletesAnInterruptedRetirement(t *testing.T) {
	r := newFakeRouter(t)
	if done, err := r.env.Finish(); done || err != nil {
		t.Fatalf("finished with PassWall2 installed: %v %v", done, err)
	}
	_ = os.Remove(r.env.PassWall[0])
	_ = os.Remove(filepath.Join(r.env.InfoDir, App+".control"))
	if done, _ := r.env.Finish(); done || !r.env.owes() {
		t.Fatal("finished without a record: an upgrade in flight lost what is owed")
	}
	if err := r.env.writeRecord(Record{At: t0, Removed: []string{App}}); err != nil {
		t.Fatal(err)
	}
	done, err := r.env.Finish()
	if !done || err != nil || r.env.owes() {
		t.Fatalf("finish: %v %v, owes %v", done, err, r.env.owes())
	}
	if _, ok := r.env.Since(); ok {
		t.Fatal("the clock still runs")
	}
	if done, _ := r.env.Finish(); done {
		t.Fatal("finished twice")
	}
}

// v2ray-geoip and v2ray-geosite stay while vctl reads their files: in their
// own directory, or through a link to them.
func TestTheGeoPackagesVctlReadsStay(t *testing.T) {
	e := testEnv(t)
	dir := filepath.Dir(e.StatusFile)
	v2ray := filepath.Join(dir, "usr", "share", "v2ray")
	own := filepath.Join(dir, "usr", "share", "vectra-controller-pro", "geo")
	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		write(t, filepath.Join(v2ray, f), f, 0o644)
		write(t, filepath.Join(own, f), f, 0o644)
	}
	// opkg-lede's lists carry a mode (and a link target) after the path.
	write(t, filepath.Join(e.InfoDir, "v2ray-geoip.list"), filepath.Join(v2ray, "geoip.dat")+"\t100644\n", 0o644)
	write(t, filepath.Join(e.InfoDir, "v2ray-geosite.list"), filepath.Join(v2ray, "geosite.dat")+"\n", 0o644)

	if got := e.GeoOwners([]string{own}); len(got) != 0 {
		t.Fatalf("vctl reads its own files, yet %v", got)
	}
	got := e.GeoOwners([]string{own, v2ray + "/"})
	if len(got) != 2 || !strings.Contains(got["v2ray-geoip"], "geoip.dat") {
		t.Fatalf("vctl reads /usr/share/v2ray: %v", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	_ = os.Remove(filepath.Join(own, "geosite.dat"))
	if err := os.Symlink(filepath.Join(v2ray, "geosite.dat"), filepath.Join(own, "geosite.dat")); err != nil {
		t.Fatal(err)
	}
	if got := e.GeoOwners([]string{own}); len(got) != 1 || got["v2ray-geosite"] == "" {
		t.Fatalf("vctl's geosite.dat is a link to v2ray-geosite's: %v", got)
	}
}

// Why PassWall2 may not go, cheapest first; nothing when it may.
func TestTheConditions(t *testing.T) {
	if b := (Conditions{}).Blockers(); len(b) != 0 {
		t.Fatalf("all well, yet %v", b)
	}
	if b := (Conditions{RouteSource: "native", NativeStore: true, NativeGeo: true}).Blockers(); len(b) != 0 {
		t.Fatalf("native with its own store and geo, yet %v", b)
	}
	for _, tc := range []struct {
		c    Conditions
		want string
	}{
		{Conditions{Disabled: true}, "retire_passwall"},
		{Conditions{Trial: true}, "trial"},
		{Conditions{Off: true}, "vectra on"},
		{Conditions{RouteSource: "passwall"}, "route_source 'passwall'"},
		{Conditions{RouteSource: "native", NativeGeo: true}, "vectra_route"},
		{Conditions{RouteSource: "native", NativeStore: true}, "geo files"},
		{Conditions{RouteSource: "something"}, "something"},
		{Conditions{Carrying: errors.New("xray does not run")}, "xray does not run"},
		{Conditions{PassWallRuns: true}, "PassWall2 runs"},
		{Conditions{Resources: []string{"memory 30MB < floor 40MB"}}, "memory 30MB"},
	} {
		b := tc.c.Blockers()
		if len(b) != 1 || !strings.Contains(b[0], tc.want) {
			t.Errorf("%+v: %v, want one naming %q", tc.c, b, tc.want)
		}
	}
}

// A name that is not a plain package name never reaches opkg, which takes
// its arguments as patterns.
func TestOnlyPlainNamesReachOpkg(t *testing.T) {
	st := parseFleet(t, "Package: luci-i18n-passwall2-[a-z]*\nDepends: luci-app-passwall2\nStatus: install user installed\n")
	p := MakePlan(st, nil)
	for _, n := range p.Remove {
		if strings.ContainsAny(n, "*?[") {
			t.Fatalf("%q on the plan", n)
		}
	}
	if !strings.Contains(p.Keep[App], "luci-i18n-passwall2-[a-z]*") {
		t.Fatalf("PassWall2 goes though a package the plan will not name needs it: keep %v", p.Keep)
	}
}

func TestFinishRetriesConfigCleanupWithPersistedBackup(t *testing.T) {
	e := testEnv(t)
	write(t, e.Configs[0], "credentials", 0600)
	backup, err := e.Backup(t0)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.writeRecord(Record{At: t0, Backup: backup}); err != nil {
		t.Fatal(err)
	}
	done, err := e.Finish()
	if !done || err != nil || exists(e.Configs[0]) {
		t.Fatalf("finish %v %v, config remains %v", done, err, exists(e.Configs[0]))
	}
	if done, err := e.Finish(); done || err != nil {
		t.Fatalf("second finish %v %v", done, err)
	}
}
func TestFinishKeepsConfigsWithoutRegularBackup(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			e := testEnv(t)
			write(t, e.Configs[0], "credentials", 0600)
			backup := filepath.Join(t.TempDir(), "backup")
			switch kind {
			case "directory":
				if err := os.Mkdir(backup, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				write(t, backup+"-target", "backup", 0600)
				if err := os.Symlink(backup+"-target", backup); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.writeRecord(Record{At: t0, Backup: backup}); err != nil {
				t.Fatal(err)
			}
			_, err := e.Finish()
			if err == nil || !exists(e.Configs[0]) {
				t.Fatalf("finish error %v, config remains %v", err, exists(e.Configs[0]))
			}
		})
	}
}
