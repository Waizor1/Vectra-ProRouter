package tune

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// crond logs every job it starts into logread's ring: the tune quiets it to
// its warnings — only where nobody chose a level — reloads cron for it, and
// undo takes it back.
func TestCronLogLevelIsQuietedAndPutBack(t *testing.T) {
	r := newRouter(t)
	res := r.apply()
	if got := states(res.Plan)[ItemCronLogLevel]; got != Applied {
		t.Fatalf("cron_loglevel = %s", got)
	}
	if v, _ := r.option("system", "system", "cronloglevel"); v != "9" {
		t.Fatalf("cronloglevel = %q", v)
	}
	if !containsCall(r.calls, "/etc/init.d/cron reload") {
		t.Fatalf("cron not reloaded: %v", r.calls)
	}
	if v, _ := r.option("system", "system", "hostname"); v != "OpenWrt" {
		t.Fatal("the rest of the system section was lost")
	}
	r.calls = nil
	r.undo()
	if _, set := r.option("system", "system", "cronloglevel"); set {
		t.Fatal("undo left cronloglevel")
	}
	if !containsCall(r.calls, "/etc/init.d/cron reload") {
		t.Fatalf("cron not reloaded on undo: %v", r.calls)
	}
}

// A level the owner chose — before the tune or since — is theirs: never set,
// never put back.
func TestCronLogLevelTheOwnerChoseStays(t *testing.T) {
	r := newRouter(t)
	r.write("etc/config/system", "config system\n\toption cronloglevel '5'\n")
	r.apply()
	if v, _ := r.option("system", "system", "cronloglevel"); v != "5" || containsCall(r.calls, "uci -q commit system") {
		t.Fatalf("the owner's level %q, calls %v", v, r.calls)
	}

	r = newRouter(t)
	r.apply()
	r.write("etc/config/system", "config system\n\toption cronloglevel '8'\n")
	res := r.apply()
	if got := states(res.Plan)[ItemCronLogLevel]; got != UserSet {
		t.Fatalf("set since by the owner: %s", got)
	}
	if _, noted := r.backup().Items[ItemCronLogLevel]; noted {
		t.Fatal("the tune still claims the owner's level")
	}
	r.calls = nil
	r.undo()
	if v, _ := r.option("system", "system", "cronloglevel"); v != "8" || containsCall(r.calls, "uci -q delete system.@system[0].cronloglevel") {
		t.Fatalf("undo touched the owner's level: %q, %v", v, r.calls)
	}
}

// No cron, no system section, uncommitted changes to system, the switch off:
// nothing is set.
func TestCronLogLevelWaitsOrSkips(t *testing.T) {
	r := newRouter(t)
	if err := os.Remove(r.path("etc/init.d/cron")); err != nil {
		t.Fatal(err)
	}
	if got := states(Inspect(r.env))[ItemCronLogLevel]; got != Skipped+"/"+ReasonNoCron {
		t.Fatalf("no cron: %s", got)
	}

	r = newRouter(t)
	r.write("etc/config/system", "config timeserver 'ntp'\n\toption enabled '1'\n")
	if got := states(Inspect(r.env))[ItemCronLogLevel]; got != Skipped+"/"+ReasonNoSystem {
		t.Fatalf("no system section: %s", got)
	}

	r = newRouter(t)
	r.write("tmp/.uci/system", "system.@system[0].hostname='x'\n")
	r.apply()
	if got := states(Inspect(r.env))[ItemCronLogLevel]; got != Skipped+"/"+ReasonUCIPending || containsCall(r.calls, "uci -q commit system") {
		t.Fatalf("uncommitted system changes: %s, %v", got, r.calls)
	}

	r = newRouter(t)
	r.write("etc/config/vectra-controller-pro", "config controller 'main'\n\toption tune '0'\n")
	r.apply()
	if _, set := r.option("system", "system", "cronloglevel"); set || len(r.calls) != 0 {
		t.Fatalf("the switch off: calls %v", r.calls)
	}
}

// leftover writes a file aged by age under the router.
func (r *router) leftover(rel string, size int, age time.Duration) string {
	r.t.Helper()
	r.write(rel, strings.Repeat("x", size))
	at := time.Now().Add(-age)
	if err := os.Chtimes(r.path(rel), at, at); err != nil {
		r.t.Fatal(err)
	}
	return r.path(rel)
}

// vctl's own leftovers — by their exact names, older than ten minutes, open
// in no process — are removed, with the MiB freed said once; nothing else in
// /tmp ever is.
func TestTmpLeftoversRemovesOnlyVctlsOwnStaleFiles(t *testing.T) {
	r := newRouter(t)
	old := 20 * time.Minute
	update := r.leftover("tmp/vectra-controller-pro-update.ipk", 3<<20, old)
	auto := r.leftover("tmp/vectra-controller-pro-auto-123456.ipk", 1<<20, old)
	vaultTmp := r.leftover("var/run/vectra-controller-pro/.vault-tmp-42", 1024, old)
	young := r.leftover("tmp/vectra-controller-pro-auto-999.ipk", 10, time.Minute)
	open := r.leftover("var/run/vectra-controller-pro/.vault-tmp-7", 10, old)
	others := []string{
		r.leftover("tmp/vectra-controller-agent-update.ipk", 10, old),
		r.leftover("tmp/luci-app-passwall2.ipk", 10, old),
		r.leftover("tmp/vectra-controller-pro-update.ipk.bak", 10, old),
		r.leftover("var/run/vectra-controller-pro/xray.json", 10, old),
	}
	// A process holds one open; a symlink by a leftover's name is no leftover.
	r.link("proc/321/fd/4", open)
	if err := os.Symlink(r.path("root"), r.path("tmp/vectra-controller-pro-auto-1.ipk")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(r.path("tmp/vectra-controller-pro-auto-1.ipk"), time.Now().Add(-old), time.Now().Add(-old)); err != nil {
		t.Fatal(err)
	}

	p := Inspect(r.env)
	if got := states(p)[ItemTmpLeftovers]; got != Pending || value(p, ItemTmpLeftovers) != "4.0" {
		t.Fatalf("tmp_leftovers = %s, %s MiB", got, value(p, ItemTmpLeftovers))
	}
	res := r.apply()
	var lines []string
	for _, c := range res.Changes {
		if c.ID == ItemTmpLeftovers {
			lines = append(lines, c.String())
		}
	}
	if !reflect.DeepEqual(lines, []string{"vctl's leftovers in RAM: 3 file(s), 4.0 MiB -> removed"}) || len(res.Failed) != 0 {
		t.Fatalf("changes %v, failed %v", lines, res.Failed)
	}
	for _, gone := range []string{update, auto, vaultTmp} {
		if _, err := os.Lstat(gone); err == nil {
			t.Fatalf("%s stayed", gone)
		}
	}
	for _, kept := range append(others, young, open, r.path("tmp/vectra-controller-pro-auto-1.ipk"), r.path("root")) {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("%s was removed", kept)
		}
	}
	if got := states(res.Plan)[ItemTmpLeftovers]; got != Already {
		t.Fatalf("after: %s", got)
	}
	// Nothing backed up: there is nothing to put back, and undo does nothing
	// for it.
	if _, err := os.Stat(r.env.Backup); err == nil {
		if _, noted := r.backup().Items[ItemTmpLeftovers]; noted {
			t.Fatal("leftovers backed up")
		}
	}
	r.calls = nil
	if res := r.undo(); len(res.Failed) != 0 {
		t.Fatalf("undo: %v", res.Failed)
	}
}

// The switch off removes nothing; never the owner's either — a leftover is
// vctl's, so the item is never user_set.
func TestTmpLeftoversWithTheSwitchOff(t *testing.T) {
	r := newRouter(t)
	r.write("etc/config/vectra-controller-pro", "config controller 'main'\n\toption tune '0'\n")
	update := r.leftover("tmp/vectra-controller-pro-update.ipk", 10, time.Hour)
	res := r.apply()
	if got := states(res.Plan)[ItemTmpLeftovers]; got != Skipped+"/"+ReasonOff {
		t.Fatalf("tmp_leftovers = %s", got)
	}
	if _, err := os.Stat(update); err != nil {
		t.Fatal("removed with the switch off")
	}
	// The daemon's own start cleanup does not ask the switch.
	removed, err := RemoveLeftovers(r.env, time.Now())
	if err != nil || len(removed) != 1 || removed[0].Path != update {
		t.Fatalf("removed %v, %v", removed, err)
	}
	// An Env that names nowhere to look reads nothing.
	if got := states(plan(Facts{On: true}))[ItemTmpLeftovers]; got != Skipped+"/"+ReasonUnreadable {
		t.Fatalf("no dirs: %s", got)
	}
}

// zram-swap with kmod-zram for another kernel (a firmware upgraded past it):
// said as no_kernel_module, never started at every run to fail.
func TestZramWithoutAModuleForTheRunningKernel(t *testing.T) {
	r := newRouter(t)
	r.write("proc/sys/kernel/osrelease", "6.6.93\n")
	r.write("lib/modules/6.6.86/zram.ko", "")
	if got := states(Inspect(r.env))[ItemZram]; got != Skipped+"/"+ReasonNoKernelModule {
		t.Fatalf("zram = %s", got)
	}
	r.apply()
	if containsCall(r.calls, "/etc/init.d/zram start") {
		t.Fatalf("started without a module: %v", r.calls)
	}
	// Its own release's module, built in, or loaded: pending as before.
	for _, fix := range []func(){
		func() { r.write("lib/modules/6.6.93/zram.ko", "") },
		func() { r.write("lib/modules/6.6.93/modules.builtin", "kernel/drivers/block/zram/zram.ko\n") },
		func() { r.write("sys/module/zram/refcnt", "0\n") },
	} {
		r = newRouter(t)
		r.write("proc/sys/kernel/osrelease", "6.6.93\n")
		fix()
		if got := states(Inspect(r.env))[ItemZram]; got != Pending {
			t.Fatalf("zram with a module = %s", got)
		}
	}
}

// The analysis only reads: memory headroom, the processes holding the most,
// the flash left and what the operator may free under /root.
func TestAnalyzeReadsWhereMemoryAndFlashGo(t *testing.T) {
	r := newRouter(t)
	r.write("proc/meminfo", "MemTotal: 239792 kB\nMemAvailable: 90112 kB\nSwapTotal: 119896 kB\nSwapFree: 99416 kB\n")
	for pid, st := range map[string]string{
		"100": "Name:\txray\nVmRSS:\t   62464 kB\n",
		"200": "Name:\tvctl\nVmRSS:\t   20480 kB\n",
		"300": "Name:\tdnsmasq\nVmRSS:\t    3072 kB\n",
		"2":   "Name:\tkthreadd\n",
	} {
		r.write("proc/"+pid+"/status", st)
	}
	r.write("root/vectra-controller-pro_0.7.0-r9_aarch64_cortex-a53.ipk", strings.Repeat("x", 2<<20))
	r.write("root/old/xray-core.ipk", strings.Repeat("x", 1<<20))
	r.write("root/r10-staging/vctl", strings.Repeat("x", 3<<20))
	r.write("root/notes.txt", "keep")
	a := Analyze(r.env)
	if a.MemAvailableMiB != 88 || a.SwapTotalMiB != 117 || a.SwapUsedMiB != 20 {
		t.Fatalf("memory: %+v", a)
	}
	if len(a.TopRSS) != 3 || a.TopRSS[0].Name != "xray" || a.TopRSS[0].RSSMiB != 61 || a.TopRSS[1].Name != "vctl" {
		t.Fatalf("top: %+v", a.TopRSS)
	}
	if a.OverlayFreeMiB == nil {
		t.Fatal("overlay unread")
	}
	want := []Reclaimable{
		{Path: r.path("root/old/xray-core.ipk"), MiB: 1, Kind: "package"},
		{Path: r.path("root/r10-staging"), MiB: 3, Kind: "staging"},
		{Path: r.path("root/vectra-controller-pro_0.7.0-r9_aarch64_cortex-a53.ipk"), MiB: 2, Kind: "package"},
	}
	if !reflect.DeepEqual(a.Reclaimable, want) {
		t.Fatalf("reclaimable: %+v", a.Reclaimable)
	}
	// Read only: everything is still there, and nothing ran.
	for _, p := range []string{"root/r10-staging/vctl", "root/old/xray-core.ipk", "root/notes.txt"} {
		if !r.exists(p) {
			t.Fatalf("%s went", p)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("ran %v", r.calls)
	}
	// An Env with nothing to read leaves it empty, never nil lists.
	if e := Analyze(Env{}); e.TopRSS == nil || e.Reclaimable == nil || e.OverlayFreeMiB != nil {
		t.Fatalf("empty: %+v", e)
	}
}
