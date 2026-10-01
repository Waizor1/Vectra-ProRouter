package openwrt

import (
	"os"
	"strings"
	"testing"
)

// PassWall2's retirement (internal/retire, the daemon): once vctl has carried
// the traffic for a day it removes PassWall2's packages, its record
// (.passwall-retired-by-vctl) written first. What the init script owes then.

const (
	retiredRecord = ".passwall-retired-by-vctl"
	retireClock   = ".passwall-retire-clock"
	pwCrumb       = ".passwall-disabled-by-vctl"
	switchCrumb2  = ".passwall-switch-off-by-vctl"
)

// retired is a stack Vectra took, whose PassWall2 vctl then removed: its
// init script gone (not executable, for the stand-in: the prologue rewrites
// its file), the record there. The takeover's breadcrumbs are left as they
// were, as a retirement cut short would leave them.
func retired(t *testing.T) *stack {
	t.Helper()
	s := newStack(t)
	on(t, s)
	if !s.exists(pwCrumb) || !s.exists(switchCrumb2) {
		t.Fatal("the takeover noted nothing owed to PassWall2")
	}
	if err := os.Chmod(s.path("passwall2"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.write(retiredRecord, "at=2026-09-30T12:00:00Z\nremoved=luci-app-passwall2 geoview\nkept=\nbackup=/etc/vectra-controller-pro/backup/passwall2-1790769600.tar.gz\n")
	return s
}

// After the retirement a hand-back owes PassWall2 nothing: it does not look
// for it, and what said it was owed goes — on /etc, a trial's on tmpfs, the
// trial's snippet. The old agent's is not PassWall2's: it comes back.
func TestTheHandBackOwesARetiredPassWallNothing(t *testing.T) {
	t.Parallel()
	s := retired(t)
	s.write("trial.d/"+pwCrumb, "")
	s.write("trial.d/"+switchCrumb2, "")
	s.write("uci-defaults/99-vectra-trial-passwall-switch", "uci -q set 'passwall2.@global[0].enabled=1'\n")
	events, out := s.run("1", "stop\n")
	for _, e := range events {
		if strings.HasPrefix(e, "passwall ") || strings.Contains(e, "passwall2.@global[0].enabled") {
			t.Fatalf("the hand-back went for a retired PassWall2: %q\n%s", e, out)
		}
	}
	for _, p := range []string{pwCrumb, switchCrumb2, "trial.d/" + pwCrumb, "trial.d/" + switchCrumb2, "uci-defaults/99-vectra-trial-passwall-switch"} {
		if s.exists(p) {
			t.Errorf("%s left: it says PassWall2 is owed back", p)
		}
	}
	if !s.exists(retiredRecord) {
		t.Fatal("the record went: it is what says why PassWall2 is gone")
	}
	if !s.exists("agent.running") || !s.exists("agent.enabled") {
		t.Fatal("the old agent was not given back")
	}
}

// A PassWall2 that is merely absent — an opkg upgrade of it in flight, a
// half-unpacked overlay — without the record stays owed: its breadcrumbs are
// the only record that a restore is owed.
func TestAnAbsentPassWallWithoutTheRecordIsStillOwed(t *testing.T) {
	t.Parallel()
	s := retired(t)
	_ = os.Remove(s.path(retiredRecord))
	s.run("1", "stop\n")
	if !s.exists(pwCrumb) || !s.exists(switchCrumb2) {
		t.Fatal("the breadcrumbs of a PassWall2 absent for an upgrade went")
	}
}

// Every hand-back starts the retirement's window over — the next takeover
// waits out a whole one; a restart, a reload, a reboot's shutdown keep it.
func TestAHandBackStartsTheRetirementWindowOver(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		kept       bool
	}{
		{"stop", "stop\n", false},
		{"switched off", "start_service\n", false},
		{"restart", "restart\n", true},
		{"reload", "VCTL_RELOADING=1 stop; start\n", true},
		{"shutdown", "shutdown\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			on(t, s)
			s.write(retireClock, "2026-09-30T12:00:00Z\n")
			enabled := "1"
			if tc.name == "switched off" {
				enabled = "0"
			}
			_, out := s.run(enabled, tc.body)
			if s.exists(retireClock) != tc.kept {
				t.Fatalf("clock there %v, want %v\n%s", s.exists(retireClock), tc.kept, out)
			}
		})
	}
}

// The dead-man never hands a router back to a retired PassWall2: with
// nobody left to give it to, it starts vctl again for as long as it takes,
// even with the takeover's breadcrumbs still there.
func TestTheDeadManNeverHandsBackARetiredPassWall(t *testing.T) {
	t.Parallel()
	s := retired(t)
	if err := os.Chmod(s.path("vectra-controller"), 0o644); err != nil { // no old agent either
		t.Fatal(err)
	}
	_ = os.Remove(s.path("vctl.running"))
	starts, all := minutes(t, s, 20, "VCTL_CRASHES=1")
	if want := []int{1, 2, 4, 9, 19}; !reflectEqual(starts, want) {
		t.Fatalf("started in minutes %v, want %v", starts, want)
	}
	for m, events := range all {
		if strings.Contains(strings.Join(events, "\n"), "giving the router back") || containsLine(events, "vctl disable") {
			t.Fatalf("minute %d handed back:\n%s", m+1, strings.Join(events, "\n"))
		}
	}
}
