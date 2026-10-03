package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"vectra-controller-pro/internal/tune"
)

// tuneStand is a small router's files in a temp dir for `vctl tune` and the
// daemon's tune at start: 234 MiB, two cores, swappiness at the kernel's 60
// and nothing else to do. Its runner notes each call and makes a `sysctl -p`
// of the tune's own file take.
type tuneStand struct {
	env   tune.Env
	mu    sync.Mutex
	calls []string
}

func newTuneStand(t *testing.T) *tuneStand {
	t.Helper()
	dir := t.TempDir()
	s := &tuneStand{}
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/meminfo", "MemTotal: 239792 kB\nMemAvailable: 90000 kB\n")
	write("proc/cpuinfo", "processor\t: 0\nprocessor\t: 1\n")
	write("proc/swaps", "Filename\tType\tSize\tUsed\tPriority\n")
	write("proc/sys/vm/swappiness", "60\n")
	write("proc/sys/vm/vfs_cache_pressure", "200\n")
	write("etc/config/network", "config globals 'globals'\n\toption packet_steering '1'\n")
	write("etc/config/firewall", "config defaults\n\toption flow_offloading '1'\n")
	s.env = tune.Env{
		ProcDir: filepath.Join(dir, "proc"), SysctlDir: filepath.Join(dir, "etc/sysctl.d"), SysctlConf: filepath.Join(dir, "etc/sysctl.conf"),
		OwnSysctl: filepath.Join(dir, "etc/sysctl.d/95-vectra-tune.conf"), Backup: filepath.Join(dir, "etc/vectra-controller-pro/tune-backup.json"),
		InitDir: filepath.Join(dir, "etc/init.d"), RCDir: filepath.Join(dir, "etc/rc.d"), ConfigDir: filepath.Join(dir, "etc/config"),
		UCISaveDir: filepath.Join(dir, "tmp/.uci"), Fw4: filepath.Join(dir, "sbin/fw4"), SysModule: filepath.Join(dir, "sys/module"),
		ModulesDir: filepath.Join(dir, "lib/modules"), Lock: filepath.Join(dir, "var/lock/vectra-tune.lock"),
	}
	s.env.Run = func(_ context.Context, name string, args ...string) error {
		s.mu.Lock()
		s.calls = append(s.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		s.mu.Unlock()
		if name == "sysctl" && len(args) == 3 && args[1] == "-p" {
			write("proc/sys/vm/swappiness", "80\n")
		}
		return nil
	}
	return s
}

func (s *tuneStand) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// withTune points `vctl tune` and the daemon's tune at the stand; trial says
// whether a trial runs.
func withTune(t *testing.T, s *tuneStand, trial func() bool) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	env, tr, o, delay, poll := tuneEnv, tuneTrial, tuneOut, tuneStartDelay, tuneTrialPoll
	tuneEnv = func() tune.Env { return s.env }
	tuneTrial = trial
	tuneOut = &out
	tuneStartDelay, tuneTrialPoll = 0, 10*time.Millisecond
	t.Cleanup(func() { tuneEnv, tuneTrial, tuneOut, tuneStartDelay, tuneTrialPoll = env, tr, o, delay, poll })
	return &out
}

func noTrial() bool { return false }

// `vctl tune` (plan) is a dry run: every item, its state, nothing run.
func TestTuneThePlanIsADryRun(t *testing.T) {
	s := newTuneStand(t)
	out := withTune(t, s, noTrial)
	if err := cmdTune(nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"profile lowmem: 234 MiB of RAM, 2 cores; the tune is on",
		"swappiness", "pending", "60 -> 80",
		"vfs_cache_pressure", "already",
		"zram", "skipped", "not_installed",
		"flow_offloading", "no_fw4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the plan lacks %q:\n%s", want, got)
		}
	}
	if calls := s.called(); len(calls) != 0 {
		t.Fatalf("a dry run ran %v", calls)
	}
}

// `vctl tune apply`: one line per change; a second run has nothing to do.
func TestTuneApplySaysWhatItChanged(t *testing.T) {
	s := newTuneStand(t)
	out := withTune(t, s, noTrial)
	if err := cmdTune([]string{"apply"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "changed: vm.swappiness: 60 -> 80") {
		t.Fatalf("output:\n%s", got)
	}
	out.Reset()
	if err := cmdTune([]string{"apply"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "nothing to change") {
		t.Fatalf("a second run:\n%s", got)
	}
}

// A trial changes nothing a reboot would keep: the tune waits for `vectra keep`.
func TestTuneApplyWaitsOutATrial(t *testing.T) {
	s := newTuneStand(t)
	out := withTune(t, s, func() bool { return true })
	if err := cmdTune([]string{"apply"}); err != nil {
		t.Fatal(err)
	}
	if calls := s.called(); len(calls) != 0 {
		t.Fatalf("a trial was tuned: %v", calls)
	}
	if !strings.Contains(out.String(), "trial") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestTuneRefusesAnUnknownVerb(t *testing.T) {
	s := newTuneStand(t)
	withTune(t, s, noTrial)
	if err := cmdTune([]string{"everything"}); err == nil {
		t.Fatal("an unknown verb was taken")
	}
}

// The daemon tunes at its start, in the background — and during a trial not
// before it is kept.
func TestTheDaemonTunesAtStartOnceATrialIsKept(t *testing.T) {
	s := newTuneStand(t)
	var mu sync.Mutex
	trial := true
	withTune(t, s, func() bool { mu.Lock(); defer mu.Unlock(); return trial })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&daemon{}).tuneAtStart(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	if calls := s.called(); len(calls) != 0 {
		t.Fatalf("tuned during a trial: %v", calls)
	}
	mu.Lock()
	trial = false
	mu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the tune did not run once the trial was kept")
	}
	if calls := s.called(); len(calls) != 1 || !strings.HasPrefix(calls[0], "sysctl -q -p ") {
		t.Fatalf("calls: %v", calls)
	}
}

// A daemon stopped during a trial leaves without tuning.
func TestTheDaemonsTuneEndsWithIt(t *testing.T) {
	s := newTuneStand(t)
	withTune(t, s, func() bool { return true })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&daemon{}).tuneAtStart(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the tune outlived the daemon")
	}
	if calls := s.called(); len(calls) != 0 {
		t.Fatalf("calls: %v", calls)
	}
}

// The wiring, on the daemon's own run: its start tunes the router, in the
// background, and its loop goes on meanwhile.
func TestTheDaemonsRunTunesTheRouter(t *testing.T) {
	s := newTuneStand(t)
	withTune(t, s, noTrial)
	dir := t.TempDir()
	d := newTestDaemon(t, dir, newPanelStub(t, nil), newProviderStub(t, providerEntry(t)))
	d.cfg.UISocketPath = filepath.Join(dir, "ui.sock")
	d.runFirewallCmd = (&recordedCmds{}).run
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = d.run(ctx, false)
	}()
	for deadline := time.Now().Add(5 * time.Second); len(s.called()) == 0 && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the daemon did not stop")
	}
	if calls := s.called(); len(calls) == 0 {
		t.Fatal("the daemon's start did not tune the router")
	}
}

// The daemon's start removes an update's package an earlier vctl left in
// RAM — whatever the tune's switch says — and nothing that is not vctl's.
func TestTheDaemonsStartRemovesAnUpdatesLeftoverPackage(t *testing.T) {
	s := newTuneStand(t)
	withTune(t, s, noTrial)
	dir := t.TempDir()
	s.env.TmpDir = dir
	old := time.Now().Add(-time.Hour)
	for _, name := range []string{controllerUpdateFile, "someone-elses.ipk"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("pkg"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	removeLeftoversAtStart()
	if _, err := os.Stat(filepath.Join(dir, controllerUpdateFile)); !os.IsNotExist(err) {
		t.Fatalf("the update's package stayed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "someone-elses.ipk")); err != nil {
		t.Fatal("removed a file that is not vctl's")
	}
}

// `vctl tune plan` prints where the router's memory and flash go after the
// items, and carries it in its JSON.
func TestTunePlanCarriesTheAnalysis(t *testing.T) {
	s := newTuneStand(t)
	out := withTune(t, s, noTrial)
	if err := cmdTune([]string{"plan"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "memory: 87 MiB available") || !strings.Contains(out.String(), "cron_loglevel") {
		t.Fatalf("plan:\n%s", out.String())
	}
	out.Reset()
	if err := cmdTune([]string{"plan", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"analysis": {`) || !strings.Contains(out.String(), `"memAvailableMiB": 87`) {
		t.Fatalf("plan --json:\n%s", out.String())
	}
	if calls := s.called(); len(calls) != 0 {
		t.Fatalf("the plan ran %v", calls)
	}
}
