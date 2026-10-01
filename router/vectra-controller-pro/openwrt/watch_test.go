package openwrt

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// ---- the hand-back, once vctl is gone (rc.common's stop) -------------------

// rc.common's stop is stop_service, procd_kill — which does not wait for the
// process — then service_stopped. The hand-back waits there for vctl and its
// xray to be gone: before that it put two proxy stacks on the router at once.
func TestTheHandBackWaitsForVctlToBeGone(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	if _, out := s.run("1", "start_service; echo \"rc=$?\"\n"); !strings.Contains(out, "rc=0") {
		t.Fatal(out)
	}
	s.write("vctl.linger", "3") // vctl and its xray still there for three more looks
	events, out := s.run("1", "stop\n")
	got := withoutLogs(events)
	look := "pgrep -f run -c " + s.path("run") + "/|(^|/)(vctl-xray-wrapper|vctl-xray-private) run -c stdin:($| )"
	kill, enable := indexOf(got, "procd_kill"), indexOf(got, "passwall enable")
	if kill < 0 || enable < 0 || kill > enable || countLine(got, look) != 4 || indexOf(got, "teardown") > kill {
		t.Fatalf("want the teardown, procd_kill, four looks at vctl (three alive), then the hand-back:\n  %s\n%s", strings.Join(got, "\n  "), out)
	}
	for i, e := range got {
		if e == look && i > enable {
			t.Fatalf("looked at vctl after the hand-back began:\n  %s", strings.Join(got, "\n  "))
		}
	}
	if !s.exists("pw.running") || !s.exists("agent.running") {
		t.Fatal("not handed back")
	}
}

// Bounded: an xray a killed vctl left behind (its own process group) holds
// the hand-back up for 15 s, not for ever.
func TestTheHandBackWaitsNoLongerThanFifteenSeconds(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.run("1", "start_service\n")
	s.write("vctl.linger", "1000")
	events, _ := s.run("1", "stop\n")
	if n := countLine(events, "pgrep -f run -c "+s.path("run")+"/|(^|/)(vctl-xray-wrapper|vctl-xray-private) run -c stdin:($| )"); n != 16 || !containsLine(events, "passwall enable") ||
		!strings.Contains(strings.Join(events, "\n"), "still runs 15 s after the stop") {
		t.Fatalf("%d looks:\n%s", n, strings.Join(events, "\n"))
	}
}

// A restart (the self-update's, the postinst's) and a reload stop and start
// vctl without a hand-back: nothing to wait for, nothing restored.
func TestARestartIsNotAHandBackInServiceStoppedEither(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.run("1", "start_service\n")
	for _, verb := range []string{"restart", "reload_service"} {
		events, _ := s.run("1", verb+"\n")
		if containsLine(events, "passwall enable") || containsLine(events, "agent enable") || containsLine(events, "pgrep -f "+s.path("run")+"/") {
			t.Fatalf("%s handed back:\n%s", verb, strings.Join(events, "\n"))
		}
		if !containsLine(events, "procd_kill") || !containsLine(events, "instance") {
			t.Fatalf("%s did not stop and start vctl:\n%s", verb, strings.Join(events, "\n"))
		}
	}
}

// A system shutdown (rc.common's `shutdown`, the K link of every reboot) is
// not a hand-back either: vctl's data plane goes and vctl stops, and the
// agent, PassWall, their breadcrumbs and vctl's kernel reserve stay as vctl
// holds them — the boot starts vctl again.
func TestAShutdownIsNotAHandBack(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.write("meminfo", "MemTotal:         239720 kB\n")
	s.write("sysctl.d/99-vectra-lowmem.conf", "vm.min_free_kbytes = 24576\n")
	on(t, s)
	events, _ := s.run("1", "shutdown\n")
	for _, handBack := range []string{"passwall enable", "passwall start", "agent enable", "agent start"} {
		if containsLine(events, handBack) {
			t.Fatalf("the shutdown handed back (%s):\n%s", handBack, strings.Join(events, "\n"))
		}
	}
	if !containsLine(events, "procd_kill") || s.exists("nft.vctl") {
		t.Fatalf("the shutdown did not stop vctl and its data plane:\n%s", strings.Join(events, "\n"))
	}
	if !s.exists(".legacy-agent-disabled-by-vctl") || !s.exists(".passwall-disabled-by-vctl") {
		t.Fatal("the shutdown took the hand-back's breadcrumbs")
	}
	if !s.exists("sysctl.d/99-vectra-pro-memory.conf") {
		t.Fatal("the shutdown gave the agent's kernel reserve back")
	}
}

// ---- breadcrumbs --------------------------------------------------------------

// M1: the agent's breadcrumb is checked like PassWall's, and before anything
// is taken: an unwritable one declines the takeover with the agent and
// PassWall as they were. (A directory where the file goes fails the
// redirection in every shell; under busybox ash a `:` there ended the script.)
func TestAnUnwritableAgentBreadcrumbTakesNothing(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	if err := os.MkdirAll(s.path(".legacy-agent-disabled-by-vctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	events, out := s.run("1", "start_service; echo \"rc=$?\"\n")
	all := strings.Join(events, "\n")
	if !strings.Contains(out, "rc=1") || containsLine(events, "instance") || !strings.Contains(all, "refusing to disable the legacy agent") {
		t.Fatalf("under %s:\n%s\n%s", testShell(), all, out)
	}
	for _, taken := range []string{"agent disable", "agent stop", "passwall stop", "passwall disable", switchOff[0]} {
		if containsLine(events, taken) {
			t.Errorf("took %q before refusing:\n%s", taken, all)
		}
	}
	if !s.exists("agent.running") || !s.exists("pw.running") || s.exists("watchdog.last-restart") || s.pwSwitch() != "1" {
		t.Fatal("the router is not as it was")
	}
}

// L6: the agent's breadcrumb goes once the agent is back enabled, not
// before — an `enable` that could not write its link leaves the next stop,
// prerm or boot something to retry.
func TestTheAgentsBreadcrumbStaysUntilItIsEnabledAgain(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.run("1", "start_service\n")
	events, _ := s.run("1", "stop\n", "AGENT_ENABLED=1") // its enable does not take
	if !containsLine(events, "agent enable") || !s.exists(".legacy-agent-disabled-by-vctl") {
		t.Fatalf("the breadcrumb went with the agent not enabled:\n%s", strings.Join(events, "\n"))
	}
	events, _ = s.run("0", "start_service\n")
	if !containsLine(events, "agent enable") || s.exists(".legacy-agent-disabled-by-vctl") || !s.exists("agent.enabled") {
		t.Fatalf("the retry:\n%s", strings.Join(events, "\n"))
	}
}

// M2: a commit that failed leaves the switch staged on and off on flash. The
// retried hand-back sets and commits it again rather than believing `uci
// get`, and only then lets the breadcrumb go.
func TestAHandBackRetriesAFailedCommitOfPassWallsSwitch(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.run("1", "start_service\n")
	events, _ := s.run("1", "stop\n", "UCI_COMMIT_FAIL=1")
	if !containsLine(events, switchOn[0]) || s.pwSwitch() != "0" || !s.exists(switchCrumb) || !s.exists("uci-save/passwall2") {
		t.Fatalf("a failed commit: switch %q, breadcrumb %v, staged %v\n%s", s.pwSwitch(), s.exists(switchCrumb), s.exists("uci-save/passwall2"), strings.Join(events, "\n"))
	}
	events, _ = s.run("0", "start_service\n")
	if !containsLine(events, switchOn[0]) || !containsLine(events, switchOn[1]) || s.pwSwitch() != "1" || s.exists(switchCrumb) {
		t.Fatalf("the retry: switch %q on flash, breadcrumb %v\n%s", s.pwSwitch(), s.exists(switchCrumb), strings.Join(events, "\n"))
	}
}

// ---- a PassWall restart in flight -----------------------------------------

// subscribe.lua's `restart &` at midnight — or the hotplug's, or one the
// agent spawned — runs under PassWall's own init lock: its stop, its config
// read, and the stack back seconds later. The takeover waits for that lock
// (without holding it while PassWall's own stop runs), and wants PassWall
// down three looks in a row; stopped under the restart, the stack came back
// after the takeover. Real seconds here.
func TestTheTakeoverWaitsForAPassWallRestartInFlight(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	events, out := s.run("1", "PW_SLOW=1 '"+s.path("passwall2")+"' restart >/dev/null 2>&1 &\n"+
		"i=0; while [ ! -f '"+s.path("restart.began")+"' ] && [ \"$i\" -lt 200000 ]; do i=$((i + 1)); done\n"+
		"sleep() { command sleep 1; }\n"+
		"start_service; echo \"rc=$?\"\n")
	all := strings.Join(events, "\n")
	if !strings.Contains(out, "rc=0") || containsLine(events, "passwall-stop-found-its-lock-held stop") {
		t.Fatalf("under %s:\n%s\n%s", testShell(), all, out)
	}
	// Whatever the restart brought back, the stop came after it.
	time.Sleep(2500 * time.Millisecond)
	if s.exists("pw.running") {
		t.Fatalf("the restart in flight brought PassWall back after the takeover:\n%s", all)
	}
}

// Down three looks in a row, a second apart: a stack seen down once and then
// back — restarted behind the takeover's back — is not taken for stopped.
func TestPassWallMustStayDownThreeLooksInARow(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.write("pw.looks", "1 0 1 1 1") // down, back up, then down for good
	events, out := s.run("1", "start_service; echo \"rc=$?\"\n")
	if !strings.Contains(out, "rc=0") || countLine(events, "pgrep -f /tmp/etc/passwall2/bin") != 5 {
		t.Fatalf("want five looks — the stack back after the first — before vctl runs:\n%s\n%s", strings.Join(events, "\n"), out)
	}
	s2 := newStack(t)
	s2.write("pw.looks", "1 0 1 1 0 1 1 0 1 1 0 1 1 0 1") // never three in a row
	events, out = s2.run("1", "start_service; echo \"rc=$?\"\n")
	if !strings.Contains(out, "rc=1") || containsLine(events, "instance") {
		t.Fatalf("a stack never down three looks in a row was taken over:\n%s\n%s", strings.Join(events, "\n"), out)
	}
}

// ---- the watchdog's hold, and the dead-man ---------------------------------

// H1(a): the hold is a stamp 10 minutes ahead, which lapses by itself: the
// dead-man sets it again every minute while procd runs vctl — and only then,
// and only where the agent is installed — and the hand-back takes away a
// stamp still ahead, but not one the watchdog wrote.
func TestTheHoldLapsesOnceVctlIsGone(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.run("1", "start_service\n")
	s.heldFor(600)
	stale := strconv.FormatInt(time.Now().Unix()-400, 10)

	s.write("watchdog.last-restart", stale)
	s.deadman() // procd runs vctl: held off again
	s.heldFor(600)

	s.write("watchdog.last-restart", stale)
	_ = os.Remove(s.path("vctl.running")) // procd gave up on vctl
	s.deadman()
	if b, _ := os.ReadFile(s.path("watchdog.last-restart")); string(b) != stale {
		t.Fatalf("held off again with vctl gone: %q", b)
	}

	// The legacy agent not installed: no hold.
	s2 := newStack(t)
	s2.run("1", "start_service\n")
	if err := os.Remove(s2.path("watchdog.last-restart")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s2.path("vectra-controller"), 0o644); err != nil {
		t.Fatal(err)
	}
	s2.deadman()
	if s2.exists("watchdog.last-restart") {
		t.Fatal("a hold for an agent that is not there")
	}
}

// The hand-back takes away a hold still ahead — its own — and leaves the
// watchdog's own stamp, which is the time of a restart it made.
func TestTheHandBackTakesOnlyItsOwnHoldAway(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	own := strconv.FormatInt(time.Now().Unix()-10, 10)
	s.write("watchdog.last-restart", own)
	s.run("1", "restore_legacy_controller\n")
	if !s.exists("watchdog.last-restart") {
		t.Fatal("the watchdog's own stamp was removed")
	}
	for _, hold := range []string{strconv.FormatInt(time.Now().Unix()+300, 10), "4102444800"} { // and an older package's
		s.write("watchdog.last-restart", hold)
		s.run("1", "restore_legacy_controller\n")
		if s.exists("watchdog.last-restart") {
			t.Fatalf("the hold %s stayed", hold)
		}
	}
}

// A clock step (ntpd, after a long power cut) can put the hold in the past
// at once: the NTP hotplug holds the watchdog off again, while procd runs
// vctl; any other ntp event does nothing.
func TestAClockStepHoldsTheWatchdogOffAgain(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.run("1", "start_service\n")
	raw, err := os.ReadFile(ntpHotplug)
	if err != nil {
		t.Fatal(err)
	}
	dm, err := filepath.Abs(deadmanScript)
	if err != nil {
		t.Fatal(err)
	}
	hook := s.path("hotplug-ntp")
	s.write("hotplug-ntp", strings.ReplaceAll(string(raw), "/usr/libexec/vectra-controller-pro/deadman.sh", dm))
	stale := strconv.FormatInt(time.Now().Unix()-4000, 10)
	env := s.deadmanEnv()
	for _, action := range []string{"stratum", "periodic"} {
		s.write("watchdog.last-restart", stale)
		s.exec([]string{"-c", "( . '" + hook + "' )"}, append(env, "ACTION="+action)...)
		if b, _ := os.ReadFile(s.path("watchdog.last-restart")); string(b) != stale {
			t.Fatalf("ACTION=%s held it off: %q", action, b)
		}
	}
	s.exec([]string{"-c", "( . '" + hook + "' )"}, append(env, "ACTION=step")...)
	s.heldFor(600)
	if s.exists("deadman.state") {
		t.Fatal("a clock step counted as a minute of the dead-man's")
	}
}

// ---- the dead-man ---------------------------------------------------------

// on is a stack Vectra holds for good: taken over, its boot links on, vctl
// running with its data plane loaded.
func on(t *testing.T, s *stack) {
	t.Helper()
	if _, out := s.run("1", "start_service; echo \"rc=$?\"\n"); !strings.Contains(out, "rc=0") {
		t.Fatal(out)
	}
	s.write("vctl.enabled", "")
	if !s.exists("vctl.running") || !s.exists("nft.vctl") {
		t.Fatal("vctl is not up")
	}
}

// bare is a router of Vectra's own: no PassWall, no legacy agent — nothing
// to give it back to.
func bare(t *testing.T) *stack {
	t.Helper()
	s := newStack(t)
	s.prologue("1") // the stand-ins exist: now take PassWall and the agent away
	for _, p := range []string{"passwall2", "vectra-controller"} {
		if err := os.Chmod(s.path(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// minutes runs the dead-man once a minute, as cron does, and returns the
// minutes (1, 2, …) in which it started vctl and the events of each.
func minutes(t *testing.T, s *stack, n int, env ...string) (starts []int, all [][]string) {
	t.Helper()
	for m := 1; m <= n; m++ {
		events, _ := s.deadman(env...)
		all = append(all, events)
		if containsLine(events, "instance") {
			starts = append(starts, m)
		}
	}
	return starts, all
}

// A dead vctl with its data plane loaded black-holes the LAN: the table goes
// the first minute, before anything else.
func TestTheDeadManUnloadsADeadVctlsDataPlaneAtOnce(t *testing.T) {
	t.Parallel()
	s := bare(t)
	on(t, s)
	_ = os.Remove(s.path("vctl.running")) // crashed; its table stays behind
	events, out := s.deadman("VCTL_CRASHES=1")
	got := withoutLogs(events)
	if s.exists("nft.vctl") || indexOf(got, "teardown ") < 0 || indexOf(got, "teardown ") > indexOf(got, "instance") {
		t.Fatalf("the table was not unloaded first:\n  %s\n%s", strings.Join(events, "\n  "), out)
	}
	if !strings.Contains(strings.Join(events, "\n"), "unloading it") {
		t.Fatal("not said")
	}
}

// The first minutes after a boot are the boot's own: its start of vctl may
// be on the way, and the dead-man leaves it alone until they are over.
func TestTheDeadManLeavesTheBootItsFirstMinutes(t *testing.T) {
	t.Parallel()
	s := bare(t)
	on(t, s)
	_ = os.Remove(s.path("vctl.running"))
	up := filepath.Join(t.TempDir(), "uptime")
	if err := os.WriteFile(up, []byte("42.17 80.00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events, out := s.deadman("VCTL_CRASHES=1", "UPTIME_FILE="+up)
	if containsLine(events, "instance") || indexOf(withoutLogs(events), "teardown ") >= 0 {
		t.Fatalf("it acted 42 s after a boot:\n  %s\n%s", strings.Join(events, "\n  "), out)
	}
	if err := os.WriteFile(up, []byte("181.00 80.00\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	events, out = s.deadman("VCTL_CRASHES=1", "UPTIME_FILE="+up)
	if !containsLine(events, "instance") {
		t.Fatalf("it did not act once the boot had settled:\n  %s\n%s", strings.Join(events, "\n  "), out)
	}
}

// vctl is started again at once, then after 1, 2, 5, 10 and 15 minutes, then
// every 15 — for as long as it takes: on a router with nobody to give it
// back to, the dead-man never gives up and never hands back. Every start is
// said, loudly.
func TestTheDeadManStartsVctlAgainWithABackoffAndNeverGivesUpAlone(t *testing.T) {
	t.Parallel()
	s := bare(t)
	on(t, s)
	_ = os.Remove(s.path("vctl.running"))
	starts, all := minutes(t, s, 70, "VCTL_CRASHES=1")
	if want := []int{1, 2, 4, 9, 19, 34, 49, 64}; !reflectEqual(starts, want) {
		t.Fatalf("started in minutes %v, want %v", starts, want)
	}
	for m, events := range all {
		if containsLine(events, "vctl disable") || containsLine(events, "procd_kill") || len(changes(events)) != 0 {
			t.Fatalf("minute %d gave up:\n%s", m+1, strings.Join(events, "\n"))
		}
		if containsLine(events, "instance") && !strings.Contains(strings.Join(events, "\n"), "starting it, attempt") {
			t.Fatalf("minute %d started vctl without saying so", m+1)
		}
	}
	if s.option("vectra-controller-pro", "controller", "enabled") != "1" || !s.exists("vctl.enabled") {
		t.Fatal("Vectra was switched off on a router with nobody to hand it to")
	}
	// It comes back: the count starts again.
	s.write("vctl.running", "")
	s.deadman()
	_ = os.Remove(s.path("vctl.running"))
	if starts, _ := minutes(t, s, 2, "VCTL_CRASHES=1"); !reflectEqual(starts, []int{1, 2}) {
		t.Fatalf("after vctl ran again: starts in minutes %v", starts)
	}
}

// Where PassWall (and the agent) are owed back, and vctl has stayed down for
// 10 minutes and four starts: `vectra off`, in shell — the router back as it
// was, Vectra off. Not a minute sooner.
func TestTheDeadManHandsBackAfterTenMinutesWhenPassWallIsOwed(t *testing.T) {
	t.Parallel()
	for _, trial := range []bool{false, true} {
		t.Run(map[bool]string{false: "for good", true: "trial"}[trial], func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			if trial {
				s.write("vectra-trial.json", "{}")
			}
			on(t, s)
			if trial {
				_ = os.Remove(s.path("vctl.enabled")) // a trial has no boot links
			}
			_ = os.Remove(s.path("vctl.running"))
			starts, all := minutes(t, s, 9, "VCTL_CRASHES=1")
			if !reflectEqual(starts, []int{1, 2, 4, 9}) {
				t.Fatalf("starts in minutes %v", starts)
			}
			for m, events := range all {
				if containsLine(events, "passwall enable") || containsLine(events, "vctl disable") {
					t.Fatalf("handed back in minute %d", m+1)
				}
			}
			events, out := s.deadman("VCTL_CRASHES=1")
			all10 := strings.Join(events, "\n")
			if !strings.Contains(all10, "giving the router back") || containsLine(events, "instance") {
				t.Fatalf("minute 10:\n%s\n%s", all10, out)
			}
			if s.pwSwitch() != "1" || !s.exists("pw.running") || !s.exists("pw.enabled") || !s.exists("agent.running") || !s.exists("agent.enabled") {
				t.Fatalf("not given back: switch %q, PassWall running %v enabled %v, agent running %v enabled %v\n%s",
					s.pwSwitch(), s.exists("pw.running"), s.exists("pw.enabled"), s.exists("agent.running"), s.exists("agent.enabled"), all10)
			}
			if s.option("vectra-controller-pro", "controller", "enabled") != "0" || s.exists("vctl.enabled") ||
				s.exists("watchdog.last-restart") || s.exists("vectra-trial.json") || s.exists("deadman.state") {
				t.Fatalf("Vectra is not off: uci %q, boot links %v, hold %v, trial %v, state %v", s.option("vectra-controller-pro", "controller", "enabled"),
					s.exists("vctl.enabled"), s.exists("watchdog.last-restart"), s.exists("vectra-trial.json"), s.exists("deadman.state"))
			}
			// Off now: nothing more to do.
			if starts, _ := minutes(t, s, 3, "VCTL_CRASHES=1"); len(starts) != 0 {
				t.Fatalf("started vctl after Vectra was turned off: %v", starts)
			}
		})
	}
}

// A hand-back that stays owed keeps vctl's boot links — every boot tries it
// again — and says so.
func TestTheDeadManKeepsTheBootLinksWhileAHandBackIsOwed(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	on(t, s)
	_ = os.Remove(s.path("vctl.running"))
	_, all := minutes(t, s, 10, "VCTL_CRASHES=1", "PW_ENABLED=1") // PassWall's enable does not take
	last := all[len(all)-1]
	if containsLine(last, "vctl disable") || !s.exists("vctl.enabled") || !strings.Contains(strings.Join(last, "\n"), "still owed") {
		t.Fatalf("PassWall not back enabled, yet:\n%s", strings.Join(last, "\n"))
	}
}

// Nothing at all while vctl runs (its hold on the agent's watchdog set again,
// the count started again), while a power change holds its lock, while the
// init script's stop holds procd's, while procd does not answer, while
// Vectra is switched off — its UCI switch, or no boot links outside a trial.
func TestTheDeadManLeavesAloneWhatIsNotItsToFix(t *testing.T) {
	t.Parallel()
	hold := func(t *testing.T, path string) func() {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		return func() { f.Close() }
	}
	none := func(*testing.T, *stack) func() { return func() {} }
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, s *stack) func()
		env   []string
	}{
		{"a power change in flight", func(t *testing.T, s *stack) func() { return hold(t, s.path("lock/vectra-power.lock")) }, nil},
		{"the init script's stop", func(t *testing.T, s *stack) func() { return hold(t, s.path("lock/procd_vectra-controller-pro.lock")) }, nil},
		{"procd not answering", none, []string{"UBUS_DOWN=1"}},
		{"Vectra switched off", func(t *testing.T, s *stack) func() {
			s.write("config/vectra-controller-pro", "\nconfig controller 'main'\n\toption enabled '0'\n\n")
			return func() {}
		}, nil},
		{"no boot links", func(t *testing.T, s *stack) func() { _ = os.Remove(s.path("vctl.enabled")); return func() {} }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			on(t, s)
			_ = os.Remove(s.path("vctl.running")) // its table left loaded, too
			release := tc.setup(t, s)
			_, all := minutes(t, s, 12, append([]string{"VCTL_CRASHES=1"}, tc.env...)...)
			release()
			for m, events := range all {
				if len(actions(events)) != 0 {
					t.Fatalf("minute %d did something:\n%s", m+1, strings.Join(events, "\n"))
				}
			}
			if !s.exists("nft.vctl") || s.pwSwitch() != "0" {
				t.Fatal("touched the router")
			}
		})
	}

	s := newStack(t)
	on(t, s)
	stale := strconv.FormatInt(time.Now().Unix()-400, 10)
	s.write("watchdog.last-restart", stale)
	s.write("deadman.state", "3 2 4 0")
	events, _ := s.deadman()
	if len(actions(events)) != 0 || s.exists("vctl.vanished") {
		t.Fatalf("vctl runs, yet:\n%s", strings.Join(events, "\n"))
	}
	s.heldFor(600)
	if b, _ := os.ReadFile(s.path("deadman.state")); strings.TrimSpace(string(b)) != "0 0 0 0" {
		t.Fatalf("the count did not start again: %q", b)
	}
	// procd says it runs, but its process is gone: down.
	s.write("vctl.vanished", "")
	if events, _ := s.deadman("VCTL_CRASHES=1"); !containsLine(events, "instance") {
		t.Fatalf("a vanished vctl was not started again:\n%s", strings.Join(events, "\n"))
	}
}

// vctl running, configured — its render and an operator config there — but
// without its data plane for 5 minutes: said, and every 15 minutes after, and
// nothing done; the daemon's rescue owns that. Not configured: nothing said.
func TestTheDeadManSaysSoWhenVctlRunsWithoutItsDataPlane(t *testing.T) {
	t.Parallel()
	s := bare(t)
	on(t, s)
	_ = os.Remove(s.path("nft.vctl"))
	var said []int
	for m := 1; m <= 21; m++ {
		events, _ := s.deadman()
		if len(actions(events)) != 0 {
			t.Fatalf("minute %d did something:\n%s", m, strings.Join(events, "\n"))
		}
		if m == 1 && strings.Contains(strings.Join(events, "\n"), "has not been loaded") {
			t.Fatal("said without a render")
		}
		if m == 1 {
			// From here on it is configured.
			s.write("run/xray.json", "{}")
			s.write("etc/xray-desired.json", "{}")
		}
		if strings.Contains(strings.Join(events, "\n"), "has not been loaded") {
			said = append(said, m)
		}
	}
	// Configured from minute 2: its fifth minute idle is minute 6, then 21.
	if !reflectEqual(said, []int{6, 21}) {
		t.Fatalf("said in minutes %v", said)
	}
}

// actions is what the events did: neither said (log:) nor asked (pgrep).
func actions(events []string) []string {
	var out []string
	for _, e := range withoutLogs(events) {
		if !strings.HasPrefix(e, "pgrep ") {
			out = append(out, e)
		}
	}
	return out
}

func reflectEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// ---- the dead-man's cron block ------------------------------------------------

// Written by the uci-defaults script on every install and upgrade — whole,
// once, next to whatever else root's crontab holds, cron told only when it
// changed (and started where it was not running) — and taken away by the
// postrm on a removal only: an upgrade runs the old postrm too.
func TestTheDeadMansCronBlock(t *testing.T) {
	t.Parallel()
	r := newLanRouter(t, stockDHCP(), true)
	crontab := filepath.Join(r.dir, "crontabs", "root")
	others := "00 0 * * * lua /usr/share/passwall2/subscribe.lua start vectra_sub_subscribe_list1 cron\n" +
		"# >>> vectra-controller-watchdog (managed) >>>\n*/5 * * * * /usr/sbin/vectra-controller-watchdog >/dev/null 2>&1\n# <<< vectra-controller-watchdog (managed) <<<\n"
	block := "# >>> vectra-controller-pro (managed) >>>\n* * * * * /usr/libexec/vectra-controller-pro/deadman.sh >/dev/null 2>&1\n# <<< vectra-controller-pro (managed) <<<\n"
	write := func(body string) {
		if err := os.MkdirAll(filepath.Dir(crontab), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(crontab, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		b, err := os.ReadFile(crontab)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	write(others)
	calls := strings.Join(r.run(defaultsScript), "\n")
	if got := read(); got != others+block || !strings.Contains(calls, "cron restart") {
		t.Fatalf("crontab:\n%s\ncalls:\n%s", got, calls)
	}
	// Again: nothing rewritten, cron not told.
	if calls := strings.Join(r.run(defaultsScript), "\n"); read() != others+block || strings.Contains(calls, "cron ") {
		t.Fatalf("a second run:\n%s", calls)
	}
	// A block an older package wrote is brought up to date, in its place.
	write("# >>> vectra-controller-pro (managed) >>>\n*/5 * * * * /old/deadman\n# <<< vectra-controller-pro (managed) <<<\n" + others)
	r.run(defaultsScript)
	if got := read(); got != others+block {
		t.Fatalf("an older block:\n%s", got)
	}
	// No crontab at all, cron not running: written, and cron started.
	if err := os.Remove(crontab); err != nil {
		t.Fatal(err)
	}
	stopped := *r
	stopped.env = append(append([]string{}, r.env...), "CRON_RUNNING=1")
	calls = strings.Join(stopped.run(defaultsScript), "\n")
	if got := read(); got != block || !strings.Contains(calls, "cron enable") || !strings.Contains(calls, "cron start") {
		t.Fatalf("no crontab:\n%s\ncalls:\n%s", got, calls)
	}

	postrm := makefileScript(t, "postrm")
	write(others + block)
	up := *r
	up.env = append(append([]string{}, r.env...), "PKG_UPGRADE=1")
	if calls := strings.Join(up.run(postrm, "upgrade", "0.6.0-r1"), "\n"); read() != others+block || strings.Contains(calls, "cron ") {
		t.Fatalf("an upgrade touched the block:\n%s", calls)
	}
	gone := *r
	gone.env = append(append([]string{}, r.env...), "PKG_UPGRADE=0")
	if calls := strings.Join(gone.run(postrm, "remove"), "\n"); read() != others || !strings.Contains(calls, "cron restart") {
		t.Fatalf("a removal left:\n%s\ncalls:\n%s", read(), calls)
	}
	// Nothing of ours there: nothing done.
	if calls := strings.Join(gone.run(postrm, "remove"), "\n"); read() != others || strings.Contains(calls, "cron ") {
		t.Fatalf("a removal with no block:\n%s", calls)
	}
}

// A start that has to give the router back says so to the reporter.
func TestAFailedStartTellsTheReporter(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.run("1", "RENDERER=/nonexistent/render.sh\nstart_service\n", "INCIDENT_DIR="+s.path("inbox"))
	b, err := os.ReadFile(firstFile(t, s.path("inbox")))
	if err != nil || !strings.Contains(string(b), `"code":"HANDBACK"`) || !strings.Contains(string(b), `"source":"init"`) {
		t.Fatalf("%s %v", b, err)
	}
}

// The dead-man's start of a vctl that was not running is an incident.
func TestTheDeadManTellsTheReporterWhenItStartsVctl(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	on(t, s)
	_ = os.Remove(s.path("vctl.running"))
	s.deadman("VCTL_CRASHES=1", "INCIDENT_DIR="+s.path("inbox"))
	b, err := os.ReadFile(firstFile(t, s.path("inbox")))
	if err != nil || !strings.Contains(string(b), `"code":"VCTL_DOWN"`) || !strings.Contains(string(b), `"source":"deadman"`) {
		t.Fatalf("%s %v", b, err)
	}
}

func firstFile(t *testing.T, dir string) string {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(names) == 0 {
		t.Fatalf("nothing in %s", dir)
	}
	return names[0]
}
