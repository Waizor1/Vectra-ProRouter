package power

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// router is a fake OpenWrt router: the files the switch reads in a temp dir,
// every command it runs recorded, and the init script's start and stop
// played the way openwrt/files/etc/init.d/vectra-controller-pro plays them
// (the script itself is tested in openwrt/packaging_test.go).
type router struct {
	t   *testing.T
	env Env
	dir string
	now time.Time

	cmds []string

	vctl, vctlInstance bool // procd runs vctl / has an instance of it
	agent, passwall    bool // running
	agentInstalled     bool
	passwallInstalled  bool
	// pwSwitch is PassWall's own switch, passwall2.@global[0].enabled, in
	// /etc/config: a PassWall started with it off runs nothing.
	pwSwitch         string
	passwallStarts   int  // how often PassWall was started
	passwallRestarts int  // ...and restarted
	declines         bool // the takeover hands the router back
	ubusDown         bool
	restoreFails     bool // PassWall does not come back enabled
	switchFails      bool // its own switch cannot be turned on again
	// keepForgets is what the takeover of a kept trial leaves undone:
	// "link" (PassWall's), "breadcrumb" (the switch's, left on tmpfs) or
	// "snippet".
	keepForgets string
	// dataplane: vctl's `inet vctl` table is loaded. idle: vctl comes up
	// without it. socketDown: the daemon does not answer on its socket.
	// crashLoop: procd shows vctl running on every other look only.
	dataplane, idle, socketDown, crashLoop bool
	flaps                                  int
	uciFails                               bool // every uci command fails
}

func newRouter(t *testing.T) *router {
	t.Helper()
	dir := t.TempDir()
	r := &router{t: t, dir: dir, now: time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)}
	r.env = Env{
		Config:         filepath.Join(dir, "config", "vectra-controller-pro"),
		RCDir:          filepath.Join(dir, "rc.d"),
		Init:           filepath.Join(dir, "init.d", "vectra-controller-pro"),
		MarkerDir:      filepath.Join(dir, "etc-vectra"),
		PassWall:       []string{filepath.Join(dir, "init.d", "passwall2"), filepath.Join(dir, "init.d", "passwall")},
		Agent:          filepath.Join(dir, "init.d", "vectra-controller"),
		ProcDir:        filepath.Join(dir, "proc"),
		Lock:           filepath.Join(dir, "lock", "vectra-power.lock"),
		Log:            filepath.Join(dir, "vectra-power.log"),
		Trial:          filepath.Join(dir, "tmp", "vectra-trial.json"), // tmp/: what a reboot clears
		TrialMarkers:   filepath.Join(dir, "tmp", "vectra-trial.d"),
		Snippet:        filepath.Join(dir, "uci-defaults", "99-vectra-trial-passwall-switch"),
		OperatorConfig: filepath.Join(dir, "etc-vectra", "xray-desired.json"),
		ProviderDoc:    filepath.Join(dir, "etc-vectra", "provider-config.json"),
		Run:            r.run,
		Output:         r.output,
		Daemon:         func(context.Context) bool { return r.vctl && !r.socketDown && !r.crashLoop },
		Now:            func() time.Time { return r.now },
		Sleep:          func(d time.Duration) { r.now = r.now.Add(d) },
	}
	for _, d := range []string{"config", "rc.d", "init.d", "etc-vectra", "proc", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.script(r.env.Init)
	return r
}

// fleet is a router as the fleet has it: PassWall and the legacy agent
// enabled and running, vctl installed with its switch on but neither enabled
// nor started (VECTRA_SKIP_POSTINST_RESTART=1 opkg install).
func fleet(t *testing.T) *router {
	r := newRouter(t)
	r.passwallInstalled, r.agentInstalled = true, true
	r.script(r.env.PassWall[0])
	r.script(r.env.Agent)
	r.link("passwall2", true)
	r.link("vectra-controller", true)
	r.passwall, r.agent = true, true
	r.pwSwitch = "1"
	r.uci("1")
	r.configure()
	r.sync()
	return r
}

// configure gives vctl what it renders from: an operator config and a
// provider document on /etc. Configured, it loads its data plane when it
// starts, unless idle.
func (r *router) configure() {
	r.write(r.env.OperatorConfig)
	r.write(r.env.ProviderDoc)
}

func (r *router) script(path string) {
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		r.t.Fatal(err)
	}
}

func (r *router) uci(enabled string) {
	body := "config controller 'main'\n"
	if enabled != "" {
		body += "\toption enabled '" + enabled + "'\n"
	}
	if err := os.WriteFile(r.env.Config, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// link makes (or removes) a service's rc.d start and stop links.
func (r *router) link(name string, on bool) {
	for _, l := range []string{"S95" + name, "K20" + name} {
		p := filepath.Join(r.env.RCDir, l)
		_ = os.Remove(p)
		if on {
			if err := os.Symlink("../init.d/"+name, p); err != nil {
				r.t.Fatal(err)
			}
		}
	}
}

func (r *router) linked(name string) bool { return startsAtBoot(r.env.RCDir, name) }

func (r *router) marker(name string) string { return filepath.Join(r.env.MarkerDir, name) }

func (r *router) trialMarker(name string) string { return filepath.Join(r.env.TrialMarkers, name) }

// sync writes PassWall's processes into the fake /proc.
func (r *router) sync() {
	p := filepath.Join(r.env.ProcDir, "4100")
	_ = os.RemoveAll(p)
	if r.passwall {
		if err := os.MkdirAll(p, 0o755); err != nil {
			r.t.Fatal(err)
		}
		cmd := "/tmp/etc/passwall2/bin/xray\x00run\x00-c\x00/tmp/etc/passwall2/global.json\x00"
		if err := os.WriteFile(filepath.Join(p, "cmdline"), []byte(cmd), 0o644); err != nil {
			r.t.Fatal(err)
		}
	}
}

func (r *router) run(_ context.Context, name string, args ...string) error {
	line := strings.Join(append([]string{filepath.Base(name)}, args...), " ")
	if name == "nft" { // a question, not a change: not recorded
		if strings.Join(args, " ") != "-t list table inet vctl" {
			r.t.Fatalf("unexpected command %q", line)
		}
		if !r.vctl || !r.dataplane {
			return errors.New("nft: No such file or directory")
		}
		return nil
	}
	r.cmds = append(r.cmds, line)
	defer r.sync()
	switch {
	case name == "uci" && r.uciFails:
		return errors.New("uci: I/O error")
	case name == "uci" && len(args) == 2 && strings.HasPrefix(args[1], "vectra-controller-pro.main.enabled="):
		r.uci(strings.TrimPrefix(args[1], "vectra-controller-pro.main.enabled="))
	case name == "uci":
	case name == r.env.Init:
		switch args[0] {
		case "enable":
			r.link("vectra-controller-pro", true)
		case "disable":
			r.link("vectra-controller-pro", false)
		case "start":
			r.start()
		case "stop":
			r.stop()
		}
	default:
		r.t.Fatalf("unexpected command %q", line)
	}
	return nil
}

// start plays start_service: the switch (a trial runs with it off), then the
// takeover — the legacy agent, then PassWall, each marked when it was
// enabled (on tmpfs for a trial), stopped, and disabled unless this is a
// trial; PassWall's own switch turned off when it was on, marked the same
// way, with a snippet that turns it on at the next boot for a trial. A kept
// trial's takeover moves that breadcrumb to /etc and deletes the snippet.
func (r *router) start() {
	trial := exists(r.env.Trial)
	if (!uciOn(r.env.Config) && !trial) || r.declines {
		// rc_procd sets the service with no instance: procd stops a vctl
		// that ran.
		r.vctl, r.vctlInstance, r.dataplane = false, false, false
		r.handBack()
		return
	}
	if r.agentInstalled {
		if r.linked("vectra-controller") {
			r.touch(agentMarker)
		}
		if !trial {
			r.link("vectra-controller", false)
		}
		r.agent = false
	}
	if r.passwallInstalled {
		if !trial && exists(r.trialMarker(switchMarker)) {
			if r.keepForgets != "breadcrumb" {
				r.touch(switchMarker)
				_ = os.Remove(r.trialMarker(switchMarker))
			}
			if r.keepForgets != "snippet" {
				_ = os.Remove(r.env.Snippet)
			}
		}
		if r.pwSwitch == "1" {
			r.touch(switchMarker)
			if trial {
				r.write(r.env.Snippet)
			}
			r.pwSwitch = "0"
		}
		if r.linked("passwall2") {
			r.touch(passwallMarker)
		}
		if !trial && r.keepForgets != "link" {
			r.link("passwall2", false)
		}
		r.passwall = false
	}
	r.vctl, r.vctlInstance = true, true
	r.dataplane = !r.idle
}

// stop plays rc.common's stop: the data plane goes (stop_service), procd
// lets go of vctl, then the hand-back (service_stopped).
func (r *router) stop() {
	r.dataplane = false
	r.vctl, r.vctlInstance = false, false
	r.handBack()
}

// handBack plays restore_passwall_stack and restore_legacy_controller: only
// what a breadcrumb owes, PassWall first — its own switch before its link —
// and a stack that runs already is not started again, only restarted when
// its switch was off until now.
func (r *router) handBack() {
	on := false
	if r.owes(switchMarker) && r.passwallInstalled && !r.switchFails {
		if r.pwSwitch != "1" {
			r.pwSwitch, on = "1", true
		}
		r.consume(switchMarker)
		_ = os.Remove(r.env.Snippet)
	}
	owed := r.owes(passwallMarker) && r.passwallInstalled
	if owed && !r.restoreFails {
		r.link("passwall2", true)
	}
	switch {
	case r.passwall && on:
		r.passwallRestarts++
	case !r.passwall && owed:
		r.passwall = r.pwSwitch == "1"
		r.passwallStarts++
	}
	if owed && r.linked("passwall2") {
		r.consume(passwallMarker)
	}
	if r.owes(agentMarker) {
		r.consume(agentMarker)
		if r.agentInstalled {
			r.link("vectra-controller", true)
			r.agent = true
		}
	}
}

func (r *router) touch(name string) {
	p := r.marker(name)
	if exists(r.env.Trial) {
		p = r.trialMarker(name)
	}
	r.write(p)
}

func (r *router) write(p string) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// owes: a breadcrumb for name, on /etc or on tmpfs.
func (r *router) owes(name string) bool { return exists(r.marker(name)) || exists(r.trialMarker(name)) }

func (r *router) consume(name string) {
	_ = os.Remove(r.marker(name))
	_ = os.Remove(r.trialMarker(name))
}

// reboot: each running process is gone, tmpfs too, the uci-defaults run (a
// trial's snippet turns PassWall's own switch on again), and every service
// with a start link starts, in their order: vctl (S95) before PassWall (S99),
// which runs only with its switch on — and only from a link the boot found.
func (r *router) reboot() {
	_ = os.Remove(r.env.Trial)
	_ = os.RemoveAll(r.env.TrialMarkers)
	if exists(r.env.Snippet) {
		r.pwSwitch = "1"
		_ = os.Remove(r.env.Snippet)
	}
	r.vctl, r.vctlInstance, r.passwall, r.dataplane = false, false, false, false
	found := r.linked("passwall2")
	r.agent = r.agentInstalled && r.linked("vectra-controller")
	if r.linked("vectra-controller-pro") {
		r.start()
	}
	if r.passwallInstalled && found && r.linked("passwall2") && r.pwSwitch == "1" {
		r.passwall = true
	}
	r.sync()
}

func (r *router) output(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "ubus" || len(args) != 4 || args[0] != "call" || args[1] != "service" || args[2] != "list" {
		r.t.Fatalf("unexpected question %s %v", name, args)
	}
	if r.ubusDown {
		return nil, errors.New("ubus: not found")
	}
	var q struct{ Name string }
	_ = json.Unmarshal([]byte(args[3]), &q)
	up, present := false, false
	switch q.Name {
	case "vectra-controller-pro":
		up, present = r.vctl, r.vctlInstance
		if r.crashLoop {
			r.flaps++
			up = up && r.flaps%2 == 1
		}
	case "vectra-controller":
		up, present = r.agent, r.agent
	default:
		r.t.Fatalf("procd asked about %q", q.Name)
	}
	if !present {
		return []byte("{ }\n"), nil
	}
	return []byte(fmt.Sprintf(`{"%s":{"instances":{"instance1":{"running":%t,"pid":3012}}}}`, q.Name, up)), nil
}

func (r *router) facts() Facts { return Read(context.Background(), r.env, false) }

func (r *router) said() (Say, *[]string) {
	var lines []string
	return func(format string, a ...interface{}) { lines = append(lines, fmt.Sprintf(format, a...)) }, &lines
}

func (r *router) take() []string {
	c := r.cmds
	r.cmds = nil
	return c
}

var (
	switchOn  = []string{"uci set vectra-controller-pro.main=controller", "uci set vectra-controller-pro.main.enabled=1", "uci commit vectra-controller-pro"}
	switchOff = []string{"uci set vectra-controller-pro.main=controller", "uci set vectra-controller-pro.main.enabled=0", "uci commit vectra-controller-pro"}
)

func seq(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestOnTakesTheRouterAndOnTwiceTakesNothingTwice(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	say, _ := r.said()
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if got, want := r.take(), seq(switchOn, []string{"vectra-controller-pro enable", "vectra-controller-pro start"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	f := r.facts()
	if !f.On() || !f.Running || f.Holder() != Vectra || f.HandBack() != PassWall {
		t.Fatalf("after on: %+v holder=%s handBack=%s", f, f.Holder(), f.HandBack())
	}
	if r.linked("passwall2") || r.linked("vectra-controller") {
		t.Fatal("the persistent takeover left a stack starting at boot")
	}
	if r.pwSwitch != "0" || !exists(r.marker(switchMarker)) {
		t.Fatalf("PassWall's own switch is %q after on (noted: %v)", r.pwSwitch, exists(r.marker(switchMarker)))
	}
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if got := r.take(); len(got) != 0 {
		t.Fatalf("on twice ran %v", got)
	}
}

// Off gives back exactly what was taken: the switch first, then the stop,
// then the boot links; and off twice gives nothing back twice.
func TestOffGivesTheRouterBackInThatOrderAndOffTwiceFlapsNothing(t *testing.T) {
	r := fleet(t)
	say, _ := r.said()
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	r.take()
	if err := Off(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if got, want := r.take(), seq(switchOff, []string{"vectra-controller-pro stop", "vectra-controller-pro disable"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	f := r.facts()
	if f.On() || f.Running || f.Holder() != PassWall || f.HandBack() != "" {
		t.Fatalf("after off: %+v holder=%s", f, f.Holder())
	}
	if !r.linked("passwall2") || !r.linked("vectra-controller") || !r.agent || r.pwSwitch != "1" || !r.passwall {
		t.Fatal("off did not give back what on took")
	}
	starts := r.passwallStarts
	if err := Off(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	// The stop runs again — it is also how a data plane left behind goes —
	// but with nothing owed it starts nothing, and the switch is not written.
	if got := r.take(); !reflect.DeepEqual(got, []string{"vectra-controller-pro stop"}) {
		t.Fatalf("off twice ran %v", got)
	}
	if r.passwallStarts != starts {
		t.Fatal("off twice started PassWall again")
	}
}

func TestOnSaysSoWhenTheTakeoverHandsTheRouterBack(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	r.declines = true
	say, _ := r.said()
	err := On(context.Background(), r.env, say)
	if err == nil || !strings.Contains(err.Error(), "declined") || !strings.Contains(err.Error(), "off again, the traffic goes through PassWall2") {
		t.Fatalf("err = %v", err)
	}
	// And turned off again: nothing of the switch-on left behind.
	if f := r.facts(); f.UCI || f.Boot || f.Running || f.Holder() != PassWall {
		t.Fatalf("after a switch-on that declined: %+v", f)
	}
	// It did not wait out Settle for an instance procd never had.
	if r.now.Sub(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)) > time.Second {
		t.Errorf("waited %s for a start that had declined", r.now.Sub(time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)))
	}
}

// A breadcrumb that is still there after the stop means a hand-back owed:
// the boot links stay, so every boot tries it again.
func TestOffKeepsTheBootLinksWhileAHandBackIsOwed(t *testing.T) {
	r := fleet(t)
	say, lines := r.said()
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	r.take()
	r.restoreFails = true
	if err := Off(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if got := r.take(); !reflect.DeepEqual(got, seq(switchOff, []string{"vectra-controller-pro stop"})) {
		t.Fatalf("commands = %v", got)
	}
	if !r.linked("vectra-controller-pro") {
		t.Fatal("the boot links went while PassWall was still owed")
	}
	if !strings.Contains(strings.Join(*lines, "\n"), "PassWall2 did not come back") {
		t.Fatalf("not said: %q", *lines)
	}
}

// PassWall's own switch that could not be turned on again is a hand-back
// owed as well: the boot links stay, so every boot tries it again.
func TestOffKeepsTheBootLinksWhilePassWallsSwitchIsOwed(t *testing.T) {
	r := fleet(t)
	say, lines := r.said()
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	r.take()
	r.switchFails = true
	if err := Off(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if !r.linked("vectra-controller-pro") || r.pwSwitch != "0" || !exists(r.marker(switchMarker)) {
		t.Fatalf("switch %q, noted %v, boot links %v", r.pwSwitch, exists(r.marker(switchMarker)), r.linked("vectra-controller-pro"))
	}
	if f := r.facts(); f.Owed != PassWall {
		t.Fatalf("owed %q with PassWall's switch still to turn on", f.Owed)
	}
	if !strings.Contains(strings.Join(*lines, "\n"), "PassWall2 did not come back") {
		t.Fatalf("not said: %q", *lines)
	}
	// The next boot's hand-back turns it on, before PassWall's own start.
	r.switchFails = false
	r.reboot()
	if r.pwSwitch != "1" || exists(r.marker(switchMarker)) || !r.passwall || r.passwallRestarts != 0 {
		t.Fatalf("after the reboot: switch %q, noted %v, running %v, restarts %d", r.pwSwitch, exists(r.marker(switchMarker)), r.passwall, r.passwallRestarts)
	}
}

func deadmanAt(r *router, ids *[]string) func(string) (int, error) {
	return func(id string) (int, error) {
		*ids = append(*ids, id)
		r.cmds = append(r.cmds, "deadman")
		return 4242, nil
	}
}

// A trial from the installer's standby state (switch off, no boot links):
// the way back runs first, nothing persistent changes, and a reboot brings
// back exactly what ran before.
func TestATrialChangesNothingARebootWouldKeep(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	var ids []string
	say, _ := r.said()
	if err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	if got, want := r.take(), []string{"deadman", "vectra-controller-pro start"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %v, want %v (the deadman first, no switch, no boot links)", got, want)
	}
	f := r.facts()
	if !f.On() || !f.Running || f.Holder() != Vectra || f.HandBack() != PassWall || f.Trial == nil {
		t.Fatalf("during the trial: %+v", f)
	}
	if f.Trial.Minutes != 10 || !f.Trial.Until.Equal(r.now.Add(10*time.Minute)) || f.Trial.Deadman != 4242 || f.Trial.ID != ids[0] {
		t.Fatalf("trial = %+v", f.Trial)
	}
	if !r.linked("passwall2") || !r.linked("vectra-controller") || r.linked("vectra-controller-pro") {
		t.Fatal("a trial changed what starts at boot")
	}
	if r.passwall || r.agent {
		t.Fatal("a trial left the other stack running")
	}
	// What it owes is noted on tmpfs, not on /etc.
	if exists(r.marker(passwallMarker)) || exists(r.marker(agentMarker)) || !exists(r.trialMarker(passwallMarker)) || !exists(r.trialMarker(agentMarker)) {
		t.Fatal("a trial's breadcrumbs are not (only) on tmpfs")
	}
	// PassWall's own switch lives in /etc/config, which a reboot keeps: off,
	// noted on tmpfs, with the snippet that turns it on at the next boot.
	if r.pwSwitch != "0" || exists(r.marker(switchMarker)) || !exists(r.trialMarker(switchMarker)) || !exists(r.env.Snippet) {
		t.Fatalf("PassWall's switch during a trial: %q, noted on /etc %v, on tmpfs %v, snippet %v",
			r.pwSwitch, exists(r.marker(switchMarker)), exists(r.trialMarker(switchMarker)), exists(r.env.Snippet))
	}
	r.reboot()
	f = r.facts()
	if f.On() || f.Running || f.Holder() != PassWall || !r.agent || f.Trial != nil || f.Owed != "" {
		t.Fatalf("after a reboot during the trial: %+v, agent=%v", f, r.agent)
	}
	if r.pwSwitch != "1" || exists(r.env.Snippet) {
		t.Fatalf("after a reboot during the trial PassWall's switch is %q, the snippet left: %v", r.pwSwitch, exists(r.env.Snippet))
	}
	// Nothing is owed after it: off afterwards starts nothing again.
	starts := r.passwallStarts
	say2, _ := r.said()
	if err := Off(context.Background(), r.env, say2); err != nil {
		t.Fatal(err)
	}
	if r.passwallStarts != starts || !r.passwall {
		t.Fatalf("off after the reboot: starts %d -> %d, running=%v", starts, r.passwallStarts, r.passwall)
	}
}

func TestKeepMakesTheTrialVectraOnForGood(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	var ids []string
	say, _ := r.said()
	if err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	r.take()
	if err := Keep(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if got, want := r.take(), seq(switchOn, []string{"vectra-controller-pro enable", "vectra-controller-pro start"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	f := r.facts()
	if !f.UCI || !f.Boot || f.Trial != nil || !f.Running {
		t.Fatalf("after keep: %+v", f)
	}
	if r.linked("passwall2") || r.linked("vectra-controller") {
		t.Fatal("keep left a stack starting at boot")
	}
	// PassWall's own switch stays off, for good: noted on /etc, no snippet.
	if r.pwSwitch != "0" || !exists(r.marker(switchMarker)) || exists(r.trialMarker(switchMarker)) || exists(r.env.Snippet) {
		t.Fatalf("after keep PassWall's switch is %q, noted on /etc %v, on tmpfs %v, snippet %v",
			r.pwSwitch, exists(r.marker(switchMarker)), exists(r.trialMarker(switchMarker)), exists(r.env.Snippet))
	}
	r.reboot()
	if f := r.facts(); !f.Running || r.passwall || r.agent || r.pwSwitch != "0" {
		t.Fatalf("after keep and a reboot: vctl=%v passwall=%v agent=%v switch=%q", f.Running, r.passwall, r.agent, r.pwSwitch)
	}
	// Keep, and `on` during a trial, twice: nothing more.
	if err := Keep(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if got := r.take(); len(got) != 0 {
		t.Fatalf("keep twice ran %v", got)
	}
	// And off, even after that reboot, gives it all back.
	if err := Off(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if !r.passwall || !r.agent || r.pwSwitch != "1" || exists(r.marker(switchMarker)) {
		t.Fatalf("off after keep and a reboot: passwall=%v agent=%v switch=%q", r.passwall, r.agent, r.pwSwitch)
	}
}

// Keep proves what it claims: nothing it leaves undone would bring the old
// stack back after a reboot, or leave PassWall's switch off with nothing on
// /etc that says so.
func TestKeepProvesTheTrialIsKeptForGood(t *testing.T) {
	for forget, want := range map[string]string{
		"link":       "did not disable passwall2",
		"breadcrumb": "did not note on /etc that PassWall's own switch is off",
		"snippet":    "turns PassWall's own switch on again",
	} {
		t.Run(forget, func(t *testing.T) {
			r := fleet(t)
			r.uci("0")
			var ids []string
			say, _ := r.said()
			if err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say); err != nil {
				t.Fatal(err)
			}
			r.keepForgets = forget
			if err := Keep(context.Background(), r.env, say); err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "off again") {
				t.Fatalf("keep with the %s left: %v, want %q, and Vectra off again", forget, err, want)
			}
			// The trial's deadman is disarmed by then: the keep that failed
			// gave the router back itself.
			if f := r.facts(); f.Running || f.Trial != nil || f.UCI || f.Boot || f.Holder() != PassWall || !r.agent {
				t.Fatalf("after a keep that failed: %+v agent=%v", f, r.agent)
			}
		})
	}
}

// A keep whose start does not get there — the takeover declines — gives the
// router back too: the trial's file is gone, and nothing else would.
func TestAKeepThatDoesNotComeUpGivesTheRouterBack(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	var ids []string
	say, _ := r.said()
	if err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	r.declines = true
	err := Keep(context.Background(), r.env, say)
	if err == nil || !strings.Contains(err.Error(), "declined") || !strings.Contains(err.Error(), "off again") {
		t.Fatalf("err = %v", err)
	}
	if f := r.facts(); f.Running || f.Trial != nil || f.UCI || f.Boot || f.Holder() != PassWall {
		t.Fatalf("after: %+v", f)
	}
}

// M4: a switch-on that does not get there is turned off again — the whole
// hand-back — and says what happened: vctl crash-looping, vctl running
// without its data plane. One look at a running vctl is not "up": procd
// shows a crash-looping one running between two crashes.
func TestASwitchOnThatDoesNotGetThereIsTurnedOffAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(r *router)
		want string
		took time.Duration
	}{
		{"crash-looping", func(r *router) { r.crashLoop = true }, "did not stay up", Settle},
		{"no data plane", func(r *router) { r.idle = true }, "without its data plane", DataplaneWait},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := fleet(t)
			r.uci("0")
			tc.set(r)
			say, _ := r.said()
			start := r.now
			err := On(context.Background(), r.env, say)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "off again, the traffic goes through PassWall2") {
				t.Fatalf("err = %v", err)
			}
			if took := r.now.Sub(start); took < tc.took {
				t.Fatalf("gave up after %s", took)
			}
			if f := r.facts(); f.UCI || f.Boot || f.Running || f.Holder() != PassWall || !r.agent || !r.linked("passwall2") {
				t.Fatalf("not given back: %+v agent=%v", f, r.agent)
			}
		})
	}
}

// Up is the socket answering, or procd running vctl three looks in a row.
func TestUpIsThreeLooksWithoutTheSocket(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	r.socketDown = true
	say, _ := r.said()
	start := r.now
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if took := r.now.Sub(start); took != 2*Poll {
		t.Fatalf("up after %s, want three looks, %s apart", took, Poll)
	}
	if f := r.facts(); !f.Running || !f.Carrying || f.Holder() != Vectra {
		t.Fatalf("%+v", f)
	}
}

// An unconfigured vctl — before its first setup through the router UI's
// wizard — runs without a data plane by design: on is on, and the traffic
// goes out directly until it is set up. Said so, not called Vectra's. Only
// with no PassWall2 configuration to route by (Env.PassWallUCI unset here):
// with one, it carries (TestATakeoverFromPassWallCarriesBeforeAnyOperatorConfig);
// and `vectra on` asks for --force first (cmd/vctl, WouldIdle).
func TestAnUnconfiguredVctlIsOnWithoutADataPlane(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	r.idle = true
	for _, p := range []string{r.env.OperatorConfig, r.env.ProviderDoc} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	say, _ := r.said()
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if f := r.facts(); !f.On() || !f.Running || f.Carrying || f.Holder() != Direct {
		t.Fatalf("%+v holder %s", f, f.Holder())
	}
}

// unconfigure takes the panel's files away and, with passwall, gives the
// router PassWall2's configuration and generator: vctl then routes by it
// until the panel's operator config arrives (AutoPassWall).
func (r *router) unconfigure(passwall bool) {
	for _, p := range []string{r.env.OperatorConfig, r.env.ProviderDoc} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			r.t.Fatal(err)
		}
	}
	r.env.PassWallUCI = filepath.Join(r.dir, "config", "passwall2")
	r.env.PassWallGenerator = filepath.Join(r.dir, "util_xray.lua")
	if passwall {
		if err := os.WriteFile(r.env.PassWallUCI, []byte("config global\n\toption enabled '1'\n\toption node 'myshunt'\n"), 0o644); err != nil {
			r.t.Fatal(err)
		}
		r.write(r.env.PassWallGenerator)
	}
}

// No VPN gap when vctl takes a router from PassWall2 before it is linked: it
// routes by PassWall2's configuration, so `on` waits for its data plane like
// a configured vctl's — and the traffic goes through Vectra, not directly.
func TestATakeoverFromPassWallCarriesBeforeAnyOperatorConfig(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	r.unconfigure(true)
	say, _ := r.said()
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if f := r.facts(); !f.On() || !f.Carrying || f.Holder() != Vectra {
		t.Fatalf("%+v holder %s", f, f.Holder())
	}
}

// ...and one that does not load it is no takeover: like a configured vctl's,
// the router goes back to PassWall2 rather than out directly.
func TestATakeoverFromPassWallThatCarriesNothingGivesTheRouterBack(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	r.unconfigure(true)
	r.idle = true
	say, _ := r.said()
	err := On(context.Background(), r.env, say)
	if !errors.Is(err, errIdle) {
		t.Fatalf("err = %v, want the data plane's absence", err)
	}
	if f := r.facts(); f.On() || f.Running || f.Holder() != PassWall {
		t.Fatalf("%+v holder %s", f, f.Holder())
	}
}

// WouldIdle is what `vectra on` warns of: no operator config and nothing to
// route by. PassWall2's configuration is something; the owner's own route
// source, a configuration without a node, a missing generator are not.
func TestWouldIdle(t *testing.T) {
	r := fleet(t)
	if r.env.WouldIdle() {
		t.Fatal("configured: idle")
	}
	r.unconfigure(true)
	if r.env.WouldIdle() {
		t.Fatal("PassWall2 to route by: idle")
	}
	if !AutoPassWall("", r.env.OperatorConfig, r.env.PassWallUCI, r.env.PassWallGenerator) {
		t.Fatal("not routing by PassWall2")
	}
	// The owner's route source is theirs: vctl does not choose another.
	if err := os.WriteFile(r.env.Config, []byte("config controller 'main'\n\toption route_source 'native'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !r.env.WouldIdle() {
		t.Fatal("route_source 'native' without an operator config: not idle")
	}
	// 'provider' is the default, spelled out.
	if err := os.WriteFile(r.env.Config, []byte("config controller 'main'\n\toption route_source 'provider'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r.env.WouldIdle() {
		t.Fatal("route_source 'provider': idle")
	}
	if err := os.WriteFile(r.env.PassWallUCI, []byte("config global\n\toption enabled '0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !r.env.WouldIdle() {
		t.Fatal("PassWall2 with no node: not idle")
	}
	r.unconfigure(false)
	_ = os.Remove(r.env.PassWallUCI)
	if !r.env.WouldIdle() {
		t.Fatal("no PassWall2 at all: not idle")
	}
	r.write(r.env.OperatorConfig)
	if r.env.WouldIdle() {
		t.Fatal("an operator config: idle")
	}
	if (Env{}).WouldIdle() {
		t.Fatal("an Env that names no operator config cannot tell, and says nothing")
	}
}

// On, with Vectra on already but carrying nothing (its render reverted, say):
// said, and nothing changed — this `on` took nothing, so gives nothing back.
func TestOnWithVectraOnButIdleSaysSo(t *testing.T) {
	r := fleet(t)
	say, _ := r.said()
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	r.take()
	r.dataplane = false
	err := On(context.Background(), r.env, say)
	if err == nil || !strings.Contains(err.Error(), "Vectra is on, but vctl runs without its data plane") {
		t.Fatalf("err = %v", err)
	}
	if got := r.take(); len(got) != 0 {
		t.Fatalf("ran %v", got)
	}
	if f := r.facts(); !f.On() || !f.Running || f.Holder() != Direct {
		t.Fatalf("%+v", f)
	}
}

// L1: a trial ends even when the UCI switch cannot be written — its deadman's
// off must still give the router back. Vectra on for good, the same failure
// stops there: the switch is what keeps a reboot from taking the router again.
func TestOffEndsATrialEvenWhenItsSwitchCannotBeWritten(t *testing.T) {
	r := fleet(t)
	var ids []string
	say, lines := r.said()
	if err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	r.take()
	r.uciFails = true
	if err := Off(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if f := r.facts(); f.Trial != nil || f.Running || f.Holder() != PassWall || !r.agent {
		t.Fatalf("after: %+v", f)
	}
	if !strings.Contains(strings.Join(*lines, "\n"), "the trial ends all the same") {
		t.Fatalf("said %q", *lines)
	}

	r2 := fleet(t)
	if err := On(context.Background(), r2.env, say); err != nil {
		t.Fatal(err)
	}
	r2.take()
	r2.uciFails = true
	if err := Off(context.Background(), r2.env, say); err == nil {
		t.Fatal("off for good without its switch reported success")
	}
	if got := r2.take(); len(got) != 1 || !strings.HasPrefix(got[0], "uci set") || !r2.facts().Running {
		t.Fatalf("ran %v", got)
	}
}

func TestOnDuringATrialKeepsIt(t *testing.T) {
	r := fleet(t)
	var ids []string
	say, _ := r.said()
	if err := StartTrial(context.Background(), r.env, 5, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	r.take()
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if f := r.facts(); f.Trial != nil || !f.Boot || r.linked("passwall2") {
		t.Fatalf("on during a trial did not keep it: %+v", f)
	}
}

func TestKeepWithoutATrial(t *testing.T) {
	r := fleet(t)
	say, _ := r.said()
	if err := Keep(context.Background(), r.env, say); err == nil || !strings.Contains(err.Error(), "no trial runs") {
		t.Fatalf("keep on a router Vectra does not hold: %v", err)
	}
	if got := r.take(); len(got) != 0 {
		t.Fatalf("a refused keep ran %v", got)
	}
}

func TestATrialIsRefusedWhereItCouldNotGiveTheRouterBack(t *testing.T) {
	r := fleet(t)
	say, lines := r.said()
	var ids []string
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	r.take()
	// On for good: it starts at boot, there is no old state to return to.
	if err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	if got := r.take(); len(got) != 0 || !strings.Contains(strings.Join(*lines, "\n"), "nothing to try") {
		t.Fatalf("a trial over Vectra on for good ran %v, said %q", got, *lines)
	}
	// Running without a trial (started by hand, boot links off): its takeover
	// disabled what it stopped.
	r.link("vectra-controller-pro", false)
	if err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say); err == nil || !strings.Contains(err.Error(), "`vectra off` first") {
		t.Fatalf("err = %v", err)
	}
	if got := r.take(); len(got) != 0 || exists(r.env.Trial) {
		t.Fatalf("a refused trial ran %v", got)
	}
}

func TestATrialWhoseDeadmanDoesNotStartTakesNothing(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	say, _ := r.said()
	err := StartTrial(context.Background(), r.env, 10, func(string) (int, error) { return 0, errors.New("fork: out of memory") }, say)
	if err == nil || !strings.Contains(err.Error(), "nothing was taken") {
		t.Fatalf("err = %v", err)
	}
	if got := r.take(); len(got) != 0 || exists(r.env.Trial) || !r.passwall {
		t.Fatalf("ran %v, trial file left: %v", got, exists(r.env.Trial))
	}
}

// Starting a trial over, the old deadman may already have seen the new trial
// and left: without a new one the trial ends now, not never.
func TestATrialStartedOverWithoutADeadmanEndsNow(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	var ids []string
	say, _ := r.said()
	if err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	r.take()
	r.now = r.now.Add(time.Minute)
	err := StartTrial(context.Background(), r.env, 10, func(string) (int, error) { return 0, errors.New("fork: out of memory") }, say)
	if err == nil {
		t.Fatal("a trial started over without a deadman reported success")
	}
	if got := r.take(); !reflect.DeepEqual(got, []string{"vectra-controller-pro stop"}) {
		t.Fatalf("commands = %v", got)
	}
	if f := r.facts(); f.Trial != nil || f.Running || f.Holder() != PassWall {
		t.Fatalf("after: %+v", f)
	}
}

func TestATrialThatDoesNotComeUpEndsAtOnce(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	r.declines = true
	var ids []string
	say, _ := r.said()
	if err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say); err == nil {
		t.Fatal("a trial that never came up reported success")
	}
	if got, want := r.take(), []string{"deadman", "vectra-controller-pro start", "vectra-controller-pro stop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	if exists(r.env.Trial) || !r.passwall || !r.linked("passwall2") {
		t.Fatal("the failed trial did not give the router back")
	}
}

// A trial whose vctl comes up — configured — without its data plane ends at
// once: it would hold the router and carry nothing.
func TestATrialThatCarriesNothingEndsAtOnce(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	r.idle = true
	var ids []string
	say, _ := r.said()
	err := StartTrial(context.Background(), r.env, 10, deadmanAt(r, &ids), say)
	if err == nil || !strings.Contains(err.Error(), "without its data plane") {
		t.Fatalf("err = %v", err)
	}
	if f := r.facts(); f.Trial != nil || f.Running || f.Holder() != PassWall || !r.agent {
		t.Fatalf("the trial did not end: %+v", f)
	}
}

// The deadman: nothing before the minutes are up, off once they are — and
// nothing at all after keep, off or another trial.
func TestTheDeadmanEndsItsTrialOnly(t *testing.T) {
	DeadmanPoll = 30 * time.Second
	defer func() { DeadmanPoll = 5 * time.Second }()

	r := fleet(t)
	r.uci("0")
	var ids []string
	say, lines := r.said()
	if err := StartTrial(context.Background(), r.env, 2, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	r.take()
	start := r.now
	if err := Deadman(context.Background(), r.env, ids[0], say); err != nil {
		t.Fatal(err)
	}
	if r.now.Sub(start) < 2*time.Minute || r.now.Sub(start) > 2*time.Minute+DeadmanPoll {
		t.Fatalf("the deadman acted after %s, the trial was 2 min", r.now.Sub(start))
	}
	if got, want := r.take(), []string{"vectra-controller-pro stop"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	if f := r.facts(); f.On() || f.Running || f.Holder() != PassWall || !r.agent {
		t.Fatalf("after the deadman: %+v", f)
	}
	if !strings.Contains(strings.Join(*lines, "\n"), "the trial's 2 min are up") {
		t.Fatalf("said %q", *lines)
	}

	// Kept meanwhile: the deadman leaves without touching anything.
	if err := StartTrial(context.Background(), r.env, 2, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	if err := Keep(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	r.take()
	if err := Deadman(context.Background(), r.env, ids[1], say); err != nil {
		t.Fatal(err)
	}
	if got := r.take(); len(got) != 0 {
		t.Fatalf("a kept trial's deadman ran %v", got)
	}

	// Replaced by another trial: the old deadman leaves, the new one ends it.
	if err := Off(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if err := StartTrial(context.Background(), r.env, 2, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	r.now = r.now.Add(time.Second)
	if err := StartTrial(context.Background(), r.env, 3, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	r.take()
	if err := Deadman(context.Background(), r.env, ids[2], say); err != nil {
		t.Fatal(err)
	}
	if got := r.take(); len(got) != 0 {
		t.Fatalf("a replaced trial's deadman ran %v", got)
	}
	if f := r.facts(); f.Trial == nil || f.Trial.ID != ids[3] || f.Trial.Minutes != 3 {
		t.Fatalf("the trial started over is %+v", f.Trial)
	}
}

// The deadman waits for a change in flight, then looks again: the change may
// have been `vectra keep`.
func TestTheDeadmanWaitsForAChangeInFlight(t *testing.T) {
	r := fleet(t)
	r.uci("0")
	var ids []string
	say, _ := r.said()
	if err := StartTrial(context.Background(), r.env, 1, deadmanAt(r, &ids), say); err != nil {
		t.Fatal(err)
	}
	r.take()
	held, err := Lock(r.env)
	if err != nil {
		t.Fatal(err)
	}
	polls := 0
	r.env.Sleep = func(d time.Duration) {
		r.now = r.now.Add(d)
		if d == Poll {
			polls++
			if polls == 3 {
				// The change in flight was a keep.
				_ = os.Remove(r.env.Trial)
				held.Close()
			}
		}
	}
	if err := Deadman(context.Background(), r.env, ids[0], say); err != nil {
		t.Fatal(err)
	}
	if polls < 3 {
		t.Fatalf("the deadman did not wait for the lock (%d polls)", polls)
	}
	if got := r.take(); len(got) != 0 {
		t.Fatalf("the deadman ran %v after the change in flight had kept the trial", got)
	}
}

func TestReadIsWhatTheInitScriptReads(t *testing.T) {
	r := newRouter(t)
	for v, want := range map[string]bool{"1": true, "on": true, "true": true, "yes": true, "enabled": true,
		"0": false, "off": false, "false": false, "no": false, "disabled": false, "NO": true, "": true, "2": true} {
		r.uci(v)
		if got := r.facts().UCI; got != want {
			t.Errorf("enabled=%q: on=%v, want %v", v, got, want)
		}
	}
	if err := os.Remove(r.env.Config); err != nil {
		t.Fatal(err)
	}
	if !r.facts().UCI {
		t.Error("no config at all reads as off; the init script's default is on")
	}
	// Exactly its own name: the legacy agent's link is not vctl's.
	r.link("vectra-controller", true)
	if r.facts().Boot {
		t.Error("S95vectra-controller read as vctl starting at boot")
	}
}

func TestHolderAndHandBack(t *testing.T) {
	for _, c := range []struct {
		f        Facts
		holder   string
		handBack string
	}{
		{Facts{UCI: true, Boot: true, Running: true, Carrying: true, Owed: PassWall}, Vectra, PassWall},
		{Facts{UCI: true, Boot: true, Running: true, Carrying: true, Owed: Agent}, Vectra, Agent},
		{Facts{UCI: true, Boot: true, Running: true, Carrying: true}, Vectra, Direct},
		// vctl runs without its data plane: nothing takes the traffic to it.
		{Facts{UCI: true, Boot: true, Running: true, Owed: PassWall}, Direct, PassWall},
		{Facts{UCI: true, Boot: true, Running: true, PassWall: true}, PassWall, Direct},
		{Facts{PassWall: true, Agent: true}, PassWall, ""},
		{Facts{Agent: true}, Agent, ""},
		{Facts{}, Direct, ""},
		// Being turned off: the switch is off, vctl still runs.
		{Facts{Boot: true, Running: true, Carrying: true, Owed: PassWall}, Vectra, PassWall},
		// A trial is on, whatever the switches say.
		{Facts{Running: true, Carrying: true, Trial: &Trial{}}, Vectra, Direct},
	} {
		if got := c.f.Holder(); got != c.holder {
			t.Errorf("%+v: holder %s, want %s", c.f, got, c.holder)
		}
		if got := c.f.HandBack(); got != c.handBack {
			t.Errorf("%+v: handBack %q, want %q", c.f, got, c.handBack)
		}
	}
}

// procd is asked only as far as the answer needs it: not at all when the
// daemon answered on its socket, and not about the agent while PassWall runs.
func TestReadAsksProcdOnlyWhatTheHolderNeeds(t *testing.T) {
	r := fleet(t)
	asked := []string{}
	r.env.Output = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		asked = append(asked, args[3])
		return r.output(ctx, name, args...)
	}
	if f := Read(context.Background(), r.env, true); !f.Running || len(asked) != 0 {
		t.Fatalf("daemon up: %+v, asked %v", f, asked)
	}
	if f := r.facts(); f.Holder() != PassWall || len(asked) != 1 {
		t.Fatalf("PassWall running: %+v, asked %v", f, asked)
	}
	r.passwall = false
	r.sync()
	asked = asked[:0]
	if f := r.facts(); f.Holder() != Agent || len(asked) != 2 {
		t.Fatalf("the agent alone: %+v, asked %v", f, asked)
	}
	// procd not answering: nothing runs, as far as it can tell — and status
	// still answers.
	r.ubusDown = true
	if f := r.facts(); f.Running || f.Holder() != Direct {
		t.Fatalf("ubus down: %+v", f)
	}
}

// H2: vctl running is not Vectra carrying the traffic. Without its data plane
// the holder is whoever else runs — PassWall, the agent — or nobody: direct.
func TestVectraIsTheHolderOnlyWithItsDataPlane(t *testing.T) {
	r := fleet(t)
	say, _ := r.said()
	if err := On(context.Background(), r.env, say); err != nil {
		t.Fatal(err)
	}
	if f := r.facts(); !f.Running || !f.Carrying || f.Holder() != Vectra {
		t.Fatalf("on: %+v", f)
	}
	r.dataplane = false
	if f := r.facts(); !f.Running || f.Carrying || f.Holder() != Direct {
		t.Fatalf("without the data plane: %+v holder %s", f, f.Holder())
	}
	r.passwall = true
	r.sync()
	if f := r.facts(); f.Holder() != PassWall {
		t.Fatalf("PassWall back next to an idle vctl: holder %s", f.Holder())
	}
	// And asked of nft itself when nothing else knows it.
	r.dataplane = true
	env := r.env
	env.Loaded = nil
	if f := Read(context.Background(), env, true); !f.Carrying {
		t.Fatal("the table nft lists was not seen")
	}
}

// L2: PassWall runs when something runs from its temp bin directory, the way
// its own app.sh and the init script judge it — not whenever a command line
// names it.
func TestPassWallRunningIsItsStackNotItsName(t *testing.T) {
	for _, c := range []struct {
		cmdline string
		want    bool
	}{
		{"lua\x00/usr/share/passwall2/subscribe.lua\x00start\x00vectra_sub\x00cron\x00", false},
		{"tail\x00-f\x00/tmp/log/passwall2.log\x00", false},
		{"/bin/sh\x00/usr/share/passwall2/lease2hosts.sh\x00", false},
		{"/tmp/etc/passwall2/bin/xray\x00run\x00-c\x00/tmp/etc/passwall2/global.json\x00", true},
		{"/tmp/etc/passwall/bin/sing-box\x00run\x00-c\x00/tmp/etc/passwall/acl.json\x00", true},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "4100"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "4100", "cmdline"), []byte(c.cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := PassWallRunning(dir); got != c.want {
			t.Errorf("%q: running %v, want %v", c.cmdline, got, c.want)
		}
	}
}

// L8: Busy looks at the kernel's list of locks and takes nothing, so a status
// poll never refuses a change that comes at that moment. Only where that
// list cannot be read does it take the lock for a moment.
func TestBusyLooksWithoutTakingTheLock(t *testing.T) {
	r := newRouter(t)
	if Busy(r.env) || exists(r.env.Lock) {
		t.Fatal("no lock file: busy, or one was made")
	}
	f, err := Lock(r.env)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	st, err := os.Stat(r.env.Lock)
	if err != nil {
		t.Fatal(err)
	}
	sys := st.Sys().(*syscall.Stat_t)
	major, minor := devNumbers(uint64(sys.Dev))
	locks := filepath.Join(r.env.ProcDir, "locks")
	write := func(ino uint64) {
		line := fmt.Sprintf("1: FLOCK  ADVISORY  WRITE 4242 %02x:%02x:%d 0 EOF\n2: POSIX  ADVISORY  READ 17 00:1a:99 0 EOF\n", major, minor, ino)
		if err := os.WriteFile(locks, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The kernel lists a lock on it — nobody here holds it, so taking it
	// would have succeeded: busy all the same, because it was only looked at.
	write(uint64(sys.Ino))
	if !Busy(r.env) {
		t.Fatal("a lock the kernel lists was not seen")
	}
	write(uint64(sys.Ino) + 1)
	if Busy(r.env) {
		t.Fatal("another file's lock counted")
	}
	// No list to read: the lock itself, for a moment.
	if err := os.Remove(locks); err != nil {
		t.Fatal(err)
	}
	if Busy(r.env) {
		t.Fatal("busy with nothing held")
	}
	held, err := Lock(r.env)
	if err != nil {
		t.Fatal(err)
	}
	if !Busy(r.env) {
		t.Fatal("a held lock was not seen")
	}
	// On Linux — the router's kernel — the real /proc/locks shows that lock,
	// held right here; elsewhere there is none to read.
	env := r.env
	env.ProcDir = "/proc"
	h, ok := lockHeld(env)
	if runtime.GOOS == "linux" && (!ok || !h) {
		t.Fatalf("the kernel's /proc/locks: held %v, readable %v", h, ok)
	}
	if runtime.GOOS != "linux" && ok {
		t.Fatal("read a /proc/locks this machine does not have")
	}
	held.Close()
	if h, ok := lockHeld(env); runtime.GOOS == "linux" && (!ok || h) {
		t.Fatalf("after the change let go: held %v, readable %v", h, ok)
	}
}

func TestTheLockIsOneChangeAtATime(t *testing.T) {
	r := newRouter(t)
	a, err := Lock(r.env)
	if err != nil {
		t.Fatal(err)
	}
	if !Busy(r.env) {
		t.Fatal("not busy while a change holds the lock")
	}
	if _, err := Lock(r.env); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second change: %v", err)
	}
	if !Holds(r.env, a) {
		t.Fatal("the holder's own descriptor does not hold it")
	}
	stray, err := os.OpenFile(filepath.Join(r.dir, "stray"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer stray.Close()
	if Holds(r.env, stray) || Holds(r.env, nil) {
		t.Fatal("a file that is not the lock holds it")
	}
	a.Close()
	if Busy(r.env) {
		t.Fatal("busy after the change let go")
	}
}

func TestDeadmanAliveIsThatVeryDeadman(t *testing.T) {
	r := newRouter(t)
	proc := filepath.Join(r.env.ProcDir, "4242")
	if err := os.MkdirAll(proc, 0o755); err != nil {
		t.Fatal(err)
	}
	t1 := &Trial{ID: "abc", Deadman: 4242}
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(proc, "cmdline"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("/usr/sbin/vctl\x00power\x00deadman\x00--id\x00abc\x00")
	if !DeadmanAlive(r.env, t1) {
		t.Error("the trial's deadman is not seen")
	}
	write("/usr/sbin/vctl\x00power\x00deadman\x00--id\x00xyz\x00")
	if DeadmanAlive(r.env, t1) {
		t.Error("another trial's deadman counts")
	}
	write("/usr/sbin/dropbear\x00")
	if DeadmanAlive(r.env, t1) || DeadmanAlive(r.env, &Trial{ID: "abc"}) || DeadmanAlive(r.env, nil) {
		t.Error("a reused pid, or none, counts as the deadman")
	}
}

// A trial file that does not read as one is still a trial: the init script
// only asks whether it is there.
func TestAnUnreadableTrialIsStillATrial(t *testing.T) {
	r := newRouter(t)
	if err := os.WriteFile(r.env.Trial, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f := r.facts(); f.Trial == nil || !f.On() {
		t.Fatalf("%+v", f)
	}
}

func TestTheInitScriptIsRunWithoutTheVariablesThatMakeItDoNothing(t *testing.T) {
	got := cleanEnv([]string{"PATH=/usr/bin", "VECTRA_SKIP_POSTINST_RESTART=1", "VCTL_RELOADING=1", "HOME=/root"})
	if !reflect.DeepEqual(got, []string{"PATH=/usr/bin", "HOME=/root"}) {
		t.Fatalf("env = %v", got)
	}
}

// The init script and this package name the same files: the trial file one
// writes is the one the other asks about, the breadcrumbs one leaves are the
// ones the other reads.
func TestTheInitScriptNamesTheSameFiles(t *testing.T) {
	raw, err := os.ReadFile("../../openwrt/files/etc/init.d/vectra-controller-pro")
	if err != nil {
		t.Fatal(err)
	}
	env := RouterEnv()
	for _, want := range []string{
		fmt.Sprintf("TRIAL_FILE=%q", env.Trial),
		fmt.Sprintf("TRIAL_MARKER_DIR=%q", env.TrialMarkers),
		fmt.Sprintf("PASSWALL_SWITCH_SNIPPET=%q", env.Snippet),
		fmt.Sprintf("LEGACY_INIT=%q", env.Agent),
		fmt.Sprintf("LEGACY_MARKER_DIR=%q", env.MarkerDir),
		`LEGACY_MARKER="$LEGACY_MARKER_DIR/` + agentMarker + `"`,
		`PASSWALL_MARKER="$LEGACY_MARKER_DIR/` + passwallMarker + `"`,
		`PASSWALL_SWITCH_MARKER="$LEGACY_MARKER_DIR/` + switchMarker + `"`,
		`PASSWALL_INIT_CANDIDATES="${PASSWALL_INIT_CANDIDATES:-` + strings.Join(env.PassWall, " ") + `}"`,
	} {
		if !strings.Contains(string(raw), "\n"+want+"\n") {
			t.Errorf("the init script does not say %s", want)
		}
	}
	if filepath.Base(env.Init) != pkg || filepath.Base(env.Config) != pkg {
		t.Errorf("init %s, config %s: the service and its config are %s", env.Init, env.Config, pkg)
	}
}
