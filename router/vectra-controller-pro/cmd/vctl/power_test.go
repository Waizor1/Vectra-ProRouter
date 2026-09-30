package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/uiapi"
)

// powerRouter is a router for `vctl power` and set_power: its files in a temp
// dir, every command recorded, procd answering from two flags. The switch's
// sequences themselves are proven in internal/power; this is the wiring.
type powerRouter struct {
	env      power.Env
	cmds     []string
	vctl     bool // procd runs vctl
	idle     bool // ...without its data plane
	passwall bool
	spawned  []*exec.Cmd
	spawnErr error
	out      bytes.Buffer
}

func newPowerRouter(t *testing.T) *powerRouter {
	t.Helper()
	dir := t.TempDir()
	r := &powerRouter{}
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	r.env = power.Env{
		Config:       filepath.Join(dir, "vectra-controller-pro"),
		RCDir:        filepath.Join(dir, "rc.d"),
		Init:         filepath.Join(dir, "init.d", "vectra-controller-pro"),
		MarkerDir:    filepath.Join(dir, "etc"),
		PassWall:     []string{filepath.Join(dir, "passwall2")},
		Agent:        filepath.Join(dir, "vectra-controller"),
		ProcDir:      filepath.Join(dir, "proc"),
		Lock:         filepath.Join(dir, "vectra-power.lock"),
		Log:          filepath.Join(dir, "vectra-power.log"),
		Trial:        filepath.Join(dir, "vectra-trial.json"),
		TrialMarkers: filepath.Join(dir, "vectra-trial.d"),
		Snippet:      filepath.Join(dir, "uci-defaults", "99-vectra-trial-passwall-switch"),
		Run: func(_ context.Context, name string, args ...string) error {
			r.cmds = append(r.cmds, filepath.Base(name)+" "+strings.Join(args, " "))
			switch {
			case name == r.env.Init && args[0] == "start":
				r.vctl, r.passwall = true, false
			case name == r.env.Init && args[0] == "stop":
				r.vctl, r.passwall = false, true
			}
			r.sync(t)
			return nil
		},
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if !strings.Contains(args[3], `"vectra-controller-pro"`) || !r.vctl {
				return []byte("{}"), nil
			}
			return []byte(`{"vectra-controller-pro":{"instances":{"instance1":{"running":true}}}}`), nil
		},
		// vctl carries the traffic while it runs, unless idle.
		Loaded: func(context.Context) bool { return r.vctl && !r.idle },
		Now:    func() time.Time { return now },
		Sleep:  func(d time.Duration) { now = now.Add(d) },
	}
	for _, d := range []string{r.env.RCDir, r.env.MarkerDir, r.env.ProcDir, filepath.Dir(r.env.Init)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []string{r.env.Init, r.env.PassWall[0]} {
		if err := os.WriteFile(s, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.passwall = true
	r.sync(t)

	prevEnv, prevStart, prevOut, prevHanded := powerEnv, powerStart, powerOut, powerHanded
	powerEnv = func() power.Env { return r.env }
	// The test binary's own fd 3 is not a lock, and is not this test's to close.
	powerHanded = func() *os.File { return nil }
	powerStart = func(c *exec.Cmd) (int, error) {
		r.spawned = append(r.spawned, c)
		if r.spawnErr != nil {
			return 0, r.spawnErr
		}
		return 4242, nil
	}
	powerOut = &r.out
	t.Cleanup(func() { powerEnv, powerStart, powerOut, powerHanded = prevEnv, prevStart, prevOut, prevHanded })
	return r
}

func (r *powerRouter) sync(t *testing.T) {
	p := filepath.Join(r.env.ProcDir, "4100")
	_ = os.RemoveAll(p)
	if r.passwall {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "cmdline"), []byte("/tmp/etc/passwall2/bin/xray\x00run\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *powerRouter) uci(t *testing.T, enabled string) {
	t.Helper()
	if err := os.WriteFile(r.env.Config, []byte("config controller 'main'\n\toption enabled '"+enabled+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *powerRouter) boot(t *testing.T) {
	t.Helper()
	if err := os.Symlink("../init.d/vectra-controller-pro", filepath.Join(r.env.RCDir, "S95vectra-controller-pro")); err != nil {
		t.Fatal(err)
	}
}

// The change outlives whoever asked: a session of its own, stdin on
// /dev/null, output to the power log, the power lock as its fd 3 — and the
// command that asked returns at once, saying where to look.
func TestPowerOnDetachesWithTheLockUnlessForeground(t *testing.T) {
	r := newPowerRouter(t)
	if err := cmdPower([]string{"on"}); err != nil {
		t.Fatal(err)
	}
	if len(r.cmds) != 0 {
		t.Fatalf("the detaching command changed the router itself: %v", r.cmds)
	}
	if len(r.spawned) != 1 {
		t.Fatalf("spawned %d processes", len(r.spawned))
	}
	c := r.spawned[0]
	if c.SysProcAttr == nil || !c.SysProcAttr.Setsid {
		t.Fatal("the change is not in a session of its own: it dies with the rpcd call or the agent's job shell")
	}
	if got := c.Args[1:]; !reflect.DeepEqual(got, []string{"power", "on", "--foreground"}) {
		t.Fatalf("args = %v", got)
	}
	if f, ok := c.Stdin.(*os.File); !ok || f.Name() != os.DevNull {
		t.Fatalf("stdin = %v", c.Stdin)
	}
	if f, ok := c.Stdout.(*os.File); !ok || f.Name() != r.env.Log || c.Stderr != c.Stdout {
		t.Fatalf("output goes to %v / %v, not the power log", c.Stdout, c.Stderr)
	}
	if len(c.ExtraFiles) != 1 || c.ExtraFiles[0].Name() != r.env.Lock {
		t.Fatalf("fd 3 = %v, want the power lock", c.ExtraFiles)
	}
	if !strings.Contains(r.out.String(), "tail -f "+r.env.Log) {
		t.Fatalf("said %q", r.out.String())
	}
	// The trial goes the same way, with its minutes.
	if err := cmdPower([]string{"on", "--trial", "--minutes", "5"}); err != nil {
		t.Fatal(err)
	}
	if got := r.spawned[1].Args[1:]; !reflect.DeepEqual(got, []string{"power", "on", "--foreground", "--trial", "--minutes=5"}) {
		t.Fatalf("trial args = %v", got)
	}
	// --foreground makes the change here: no process of its own.
	r.uci(t, "0")
	if err := cmdPower([]string{"on", "--foreground"}); err != nil {
		t.Fatal(err)
	}
	if len(r.spawned) != 2 {
		t.Fatalf("--foreground spawned %d more", len(r.spawned)-2)
	}
	want := []string{"uci set vectra-controller-pro.main=controller", "uci set vectra-controller-pro.main.enabled=1",
		"uci commit vectra-controller-pro", "vectra-controller-pro enable", "vectra-controller-pro start"}
	if !reflect.DeepEqual(r.cmds, want) {
		t.Fatalf("commands:\n  %s\nwant:\n  %s", strings.Join(r.cmds, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestPowerRefusesWhileAChangeIsInFlight(t *testing.T) {
	r := newPowerRouter(t)
	held, err := power.Lock(r.env)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for _, args := range [][]string{{"off"}, {"off", "--foreground"}, {"keep"}} {
		err := cmdPower(args)
		if !errors.Is(err, power.ErrBusy) || !strings.Contains(err.Error(), r.env.Log) {
			t.Errorf("%v: %v", args, err)
		}
	}
	if len(r.spawned) != 0 || len(r.cmds) != 0 {
		t.Fatalf("a refused change did something: %v %v", r.spawned, r.cmds)
	}
}

func TestPowerFlags(t *testing.T) {
	newPowerRouter(t)
	for _, args := range [][]string{
		{"off", "--trial"}, {"on", "--minutes", "5"}, {"on", "--trial", "--minutes", "0"},
		{"on", "--trial", "--minutes", "1441"}, {"status", "--foreground"}, {"reboot"}, {"on", "now"},
	} {
		if err := cmdPower(args); err == nil {
			t.Errorf("%v was taken", args)
		}
	}
	if err := cmdPower(nil); err == nil {
		t.Error("no verb was taken")
	}
}

// The trial's deadman is started detached too, with no lock of its own.
func TestATrialStartsItsDeadmanDetached(t *testing.T) {
	r := newPowerRouter(t)
	r.uci(t, "0")
	if err := cmdPower([]string{"on", "--trial", "--minutes", "5", "--foreground"}); err != nil {
		t.Fatal(err)
	}
	if len(r.spawned) != 1 {
		t.Fatalf("spawned %d", len(r.spawned))
	}
	d := r.spawned[0]
	trial := power.LoadTrial(r.env)
	if trial == nil || trial.Deadman != 4242 {
		t.Fatalf("trial = %+v", trial)
	}
	if got := d.Args[1:]; !reflect.DeepEqual(got, []string{"power", "deadman", "--id", trial.ID}) {
		t.Fatalf("deadman args = %v", got)
	}
	if !d.SysProcAttr.Setsid || len(d.ExtraFiles) != 0 {
		t.Fatal("the deadman is not detached, or holds the lock while it sleeps")
	}
	if !reflect.DeepEqual(r.cmds, []string{"vectra-controller-pro start"}) {
		t.Fatalf("a trial ran %v: no switch, no boot links", r.cmds)
	}
}

// What a change runs must not inherit the lock: the handed descriptor is
// marked close-on-exec.
func TestTheHandedLockIsNotInherited(t *testing.T) {
	r := newPowerRouter(t)
	lock, err := power.Lock(r.env)
	if err != nil {
		t.Fatal(err)
	}
	got, err := powerLock(r.env, lock)
	if err != nil || got != lock {
		t.Fatalf("the handed lock was not used: %v", err)
	}
	defer got.Close()
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, got.Fd(), syscall.F_GETFD, 0)
	if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
		t.Fatalf("the lock would be inherited by the init scripts (flags %#x, %v)", flags, errno)
	}
	// A descriptor that is not the lock is let go, and the lock taken.
	stray, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	got.Close()
	own, err := powerLock(r.env, stray)
	if err != nil || !power.Holds(r.env, own) {
		t.Fatalf("with a stray fd 3: %v", err)
	}
	own.Close()
}

func TestPowerStatus(t *testing.T) {
	r := newPowerRouter(t)
	r.uci(t, "0")
	if err := cmdPower([]string{"status", "--json"}); err != nil {
		t.Fatal(err)
	}
	var st map[string]interface{}
	if err := json.Unmarshal(r.out.Bytes(), &st); err != nil {
		t.Fatal(err, r.out.String())
	}
	want := map[string]interface{}{"enabled": false, "running": false, "carrying": false, "holder": "passwall2", "handBack": nil,
		"uci": false, "boot": false, "trial": nil, "busy": false}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("status --json = %v\nwant %v", st, want)
	}
	r.out.Reset()
	if err := cmdPower([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(r.out.String(), "Vectra is off: the traffic goes through PassWall2.") {
		t.Fatalf("status = %q", r.out.String())
	}

	// On, with PassWall owed back.
	r.uci(t, "1")
	r.boot(t)
	r.vctl, r.passwall = true, false
	r.sync(t)
	if err := os.WriteFile(filepath.Join(r.env.MarkerDir, ".passwall-disabled-by-vctl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	r.out.Reset()
	if err := cmdPower([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Vectra is on: the traffic goes through Vectra.", "it will go through PassWall2.", "switch (uci enabled): on · starts at boot: yes · running: yes"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, r.out.String())
		}
	}

	// On and running, without its data plane: it carries nothing, and says so.
	r.idle = true
	r.out.Reset()
	if err := cmdPower([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Vectra is on, and runs, but carries no traffic yet: the traffic goes directly, without a VPN.", "Its data plane is not loaded"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, r.out.String())
		}
	}
	r.idle = false

	// A trial: minutes left, and a warning when its deadman is gone.
	if err := os.Remove(filepath.Join(r.env.RCDir, "S95vectra-controller-pro")); err != nil {
		t.Fatal(err)
	}
	now := r.env.Now()
	raw, _ := json.Marshal(power.Trial{ID: "t1", Started: now.Add(-3 * time.Minute), Until: now.Add(7 * time.Minute), Minutes: 10, Deadman: 999999})
	if err := os.WriteFile(r.env.Trial, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	r.out.Reset()
	if err := cmdPower([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Vectra is on for a trial (7 min left)", "`vectra keep` keeps it on", "deadman does not run"} {
		if !strings.Contains(r.out.String(), want) {
			t.Errorf("status lacks %q:\n%s", want, r.out.String())
		}
	}
}

// ---- set_power ----------------------------------------------------------

// powerCfg is a daemon config whose files are all this test's: no operator
// config, so no panel lock.
func powerCfg(t *testing.T) agentcfg.Config {
	t.Helper()
	cfg, err := agentcfg.Parse([]byte(`{"controlUrl":"unused"}`))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg.XrayConfigPath = filepath.Join(dir, "xray-desired.json")
	cfg.UISocketPath = filepath.Join(dir, "ui.sock")
	cfg.StatePath = filepath.Join(dir, "state.json")
	return cfg
}

func setPower(t *testing.T, params string) uiapi.Action {
	t.Helper()
	a, ok := rpcdCall(context.Background(), powerCfg(t), "set_power", []byte(params)).(uiapi.Action)
	if !ok {
		t.Fatalf("set_power %s did not answer an action", params)
	}
	return a
}

func fakePowerEnv(t *testing.T, r *powerRouter) {
	t.Helper()
	prev := rpcdPowerEnv
	rpcdPowerEnv = func() power.Env { return r.env }
	t.Cleanup(func() { rpcdPowerEnv = prev })
	fakeUILock(t, "1", nil) // the operator's lock does not refuse it
}

func TestSetPowerHandsTheChangeToADetachedProcess(t *testing.T) {
	r := newPowerRouter(t)
	fakePowerEnv(t, r)
	r.uci(t, "0")
	a := setPower(t, `{"on":true}`)
	if !a.OK || a.Code != "pending" {
		t.Fatalf("set_power on = %+v", a)
	}
	if len(r.spawned) != 1 || len(r.cmds) != 0 {
		t.Fatalf("spawned %d, ran %v in the rpcd call itself", len(r.spawned), r.cmds)
	}
	c := r.spawned[0]
	if !reflect.DeepEqual(c.Args[1:], []string{"power", "on", "--foreground"}) || !c.SysProcAttr.Setsid || len(c.ExtraFiles) != 1 {
		t.Fatalf("spawned %v setsid=%v files=%d", c.Args, c.SysProcAttr.Setsid, len(c.ExtraFiles))
	}
	if f, ok := c.Stdout.(*os.File); !ok || f.Name() != r.env.Log {
		t.Fatal("the change writes into the rpcd pipe: rpcd would wait for it")
	}
	if a := setPower(t, `{"on":false}`); !a.OK || a.Code != "power_off" {
		t.Fatalf("off while off = %+v", a)
	}
	r.uci(t, "1")
	r.boot(t)
	r.vctl = true
	if a := setPower(t, `{"on":true}`); !a.OK || a.Code != "power_on" {
		t.Fatalf("on while on = %+v", a)
	}
	if a := setPower(t, `{"on":false}`); !a.OK || a.Code != "pending" || !reflect.DeepEqual(r.spawned[1].Args[1:], []string{"power", "off", "--foreground"}) {
		t.Fatalf("off = %+v", a)
	}
	if len(r.spawned) != 2 {
		t.Fatalf("spawned %d, want 2", len(r.spawned))
	}
}

// L7: off while a hand-back is still owed — Vectra switched off, its boot
// links kept with the breadcrumb — is the off change again, not power_off: it
// is how the hand-back is tried again.
func TestSetPowerOffTriesAHandBackStillOwed(t *testing.T) {
	r := newPowerRouter(t)
	fakePowerEnv(t, r)
	r.uci(t, "0")
	r.boot(t)
	r.passwall = false
	r.sync(t)
	if err := os.WriteFile(filepath.Join(r.env.MarkerDir, ".passwall-disabled-by-vctl"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if a := setPower(t, `{"on":false}`); !a.OK || a.Code != "pending" || len(r.spawned) != 1 ||
		!reflect.DeepEqual(r.spawned[0].Args[1:], []string{"power", "off", "--foreground"}) {
		t.Fatalf("off with a hand-back owed = %+v, spawned %d", a, len(r.spawned))
	}
}

func TestSetPowerRefusals(t *testing.T) {
	r := newPowerRouter(t)
	fakePowerEnv(t, r)
	for _, p := range []string{``, `{}`, `{"on":1}`, `{"on":"yes"}`, `{"on":true,"trial":true}`, `[true]`} {
		if a := setPower(t, p); a.OK || a.Code != "invalid_params" {
			t.Errorf("%q: %+v", p, a)
		}
	}
	held, err := power.Lock(r.env)
	if err != nil {
		t.Fatal(err)
	}
	if a := setPower(t, `{"on":true}`); a.OK || a.Code != "busy" {
		t.Errorf("while a change is in flight: %+v", a)
	}
	held.Close()
	r.spawnErr = errors.New("fork/exec: out of memory")
	if a := setPower(t, `{"on":true}`); a.OK || a.Code != "apply_failed" || a.Detail == nil || !strings.Contains(*a.Detail, "nothing changed") {
		t.Errorf("spawn failed: %+v", a)
	}
	if len(r.cmds) != 0 {
		t.Fatalf("a refusal changed the router: %v", r.cmds)
	}
	// The failed spawn let the lock go.
	if power.Busy(r.env) {
		t.Fatal("the lock is still held after the spawn failed")
	}
}

// status answers with vctl stopped: no daemon on the socket, Vectra off,
// PassWall carrying the traffic.
func TestStatusAnswersWithTheDaemonDown(t *testing.T) {
	r := newPowerRouter(t)
	fakeGather(t)
	fakeUILock(t, "0", nil)
	r.uci(t, "0")
	prev := rpcdPower
	var up []bool
	rpcdPower = func(ctx context.Context, daemon, loaded bool) power.Facts {
		up = append(up, daemon)
		env := r.env
		env.Loaded = func(context.Context) bool { return loaded }
		return power.Read(ctx, env, daemon)
	}
	t.Cleanup(func() { rpcdPower = prev })
	st, ok := rpcdCall(context.Background(), powerCfg(t), "status", nil).(uiapi.Status)
	if !ok {
		t.Fatal("status did not answer")
	}
	raw, _ := json.Marshal(st.Power)
	if string(raw) != `{"enabled":false,"running":false,"holder":"passwall2","handBack":null}` || st.Controller.Running {
		t.Fatalf("power = %s, controller = %+v", raw, st.Controller)
	}
	if !reflect.DeepEqual(up, []bool{false}) {
		t.Fatalf("rpcdPower asked with daemon=%v", up)
	}
}

// The init script's legacy agent is the one vctl's own guard looks at, and
// the one internal/power asks procd about.
func TestOneLegacyAgentPath(t *testing.T) {
	if legacyInitScript != power.RouterEnv().Agent {
		t.Fatalf("legacyInitScript %s, internal/power %s", legacyInitScript, power.RouterEnv().Agent)
	}
}
