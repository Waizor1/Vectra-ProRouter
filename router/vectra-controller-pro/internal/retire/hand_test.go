package retire

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A PassWall2 a person removed — `opkg remove luci-app-passwall2` and its
// helpers, as docs/CANARY.md once said to — on a router Vectra had taken:
// no package and no init script of PassWall2's, no record of a retirement,
// and what it left behind — the takeover's breadcrumbs (a trial's too), its
// configuration with the nodes' credentials. The test router is exactly so.

func handRemoved(t *testing.T) Env {
	t.Helper()
	e := testEnv(t)
	write(t, e.StatusFile, "Package: xray-core\nStatus: install user installed\n\nPackage: dnsmasq-full\nStatus: install user installed\n", 0o644)
	write(t, e.Configs[0], "config global\n\toption enabled '0'\n\toption node 'myshunt'\n", 0o600)
	write(t, e.Configs[1], "config global\n\toption enable '0'\n", 0o644)
	for _, n := range owedNames {
		write(t, filepath.Join(e.MarkerDir, n), "", 0o644)
		write(t, filepath.Join(e.TrialMarkers, n), "", 0o644)
	}
	write(t, e.Snippet, "uci -q set 'passwall2.@global[0].enabled=1'\n", 0o644)
	return e
}

// What it left: the takeover's breadcrumbs or its configuration — only while
// PassWall2 is gone and no retirement is on record.
func TestLeftIsWhatAPassWallRemovedByHandLeftBehind(t *testing.T) {
	e := handRemoved(t)
	if !e.Left() {
		t.Fatal("breadcrumbs and configuration there, yet nothing left")
	}
	_ = e.forget()
	if !e.Left() {
		t.Fatal("its configuration alone is left too: it holds the nodes' credentials")
	}
	for _, c := range e.Configs {
		_ = os.Remove(c)
	}
	if e.Left() {
		t.Fatal("nothing of it on the router, yet left")
	}
	write(t, filepath.Join(e.MarkerDir, owedNames[0]), "", 0o644)
	if !e.Left() {
		t.Fatal("a breadcrumb alone is left")
	}
	write(t, e.PassWall[0], "#!/bin/sh /etc/rc.common\n", 0o755)
	if e.Left() {
		t.Fatal("PassWall2's init script is there: it is not gone")
	}
	_ = os.Remove(e.PassWall[0])
	if err := e.writeRecord(Record{At: t0, Removed: []string{App}}); err != nil {
		t.Fatal(err)
	}
	if e.Left() {
		t.Fatal("retired already: a retirement cut short is Finish's")
	}
}

// Tidy finishes what the person started, as Retire finishes once opkg is
// done: the configuration backed up (root only), the record written — by
// hand, nothing removed by opkg — nothing owed any more, on /etc or tmpfs,
// the trial's snippet gone, the configuration taken, the clock stopped; the
// state then says retired, and when.
func TestTidyFinishesARemovalByHand(t *testing.T) {
	e := handRemoved(t)
	conf := string(mustRead(t, e.Configs[0]))
	if _, err := e.StartClock(t0.Add(-25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	res, err := e.Tidy(t0)
	if err != nil || res.Cleanup != nil {
		t.Fatalf("tidy: %+v, %v", res, err)
	}
	if res.Backup == "" || len(res.Removed) != 0 || !res.ByHand {
		t.Fatalf("result %+v", res)
	}
	if got := untar(t, res.Backup)[strings.TrimPrefix(e.Configs[0], "/")]; got != conf {
		t.Fatalf("the backup holds %q, want the configuration as it was", got)
	}
	if st, _ := os.Stat(res.Backup); st == nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v, want 0600", st)
	}
	for _, c := range e.Configs {
		if exists(c) {
			t.Fatalf("%s left on the router: it is in the backup", c)
		}
	}
	if e.owes() {
		t.Fatal("something still says PassWall2 is owed back")
	}
	if exists(filepath.Join(e.MarkerDir, ClockName)) {
		t.Fatal("the clock still runs")
	}
	rec, ok := e.ReadRecord()
	if !ok || !rec.ByHand || !rec.At.Equal(t0) || len(rec.Removed) != 0 || rec.Backup != res.Backup {
		t.Fatalf("record %+v (%v)", rec, ok)
	}
	if s, at := e.State(); s != "retired" || !at.Equal(t0) {
		t.Fatalf("state %s %v", s, at)
	}
	if e.Left() {
		t.Fatal("left after the tidy")
	}
}

// A person reads the record with cat: it says PassWall2 went by hand.
func TestTheRecordSaysARemovalByHand(t *testing.T) {
	e := testEnv(t)
	r := Record{At: t0, Backup: "/etc/vectra-controller-pro/backup/passwall2-1.tar.gz", ByHand: true}
	if err := e.writeRecord(r); err != nil {
		t.Fatal(err)
	}
	got, ok := e.ReadRecord()
	if !ok || !got.ByHand || !got.At.Equal(r.At) || got.Backup != r.Backup || len(got.Removed)+len(got.Kept) != 0 {
		t.Fatalf("record %+v (%v), want %+v", got, ok, r)
	}
	if raw := string(mustRead(t, filepath.Join(e.MarkerDir, RecordName))); !strings.Contains(raw, "\nby=hand\n") {
		t.Fatalf("the record does not say it went by hand:\n%s", raw)
	}
	r.ByHand = false
	if err := e.writeRecord(r); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.ReadRecord(); got.ByHand {
		t.Fatal("a retirement's record read as one by hand")
	}
}

// Nothing to finish, or not Tidy's to finish: nothing changes.
func TestTidyLeavesAloneWhatIsNotItsToFinish(t *testing.T) {
	t.Run("nothing left", func(t *testing.T) {
		e := testEnv(t)
		if _, err := e.Tidy(t0); !errors.Is(err, ErrNothing) {
			t.Fatalf("err %v, want ErrNothing", err)
		}
		if _, ok := e.ReadRecord(); ok {
			t.Fatal("a record of a PassWall2 that was never here")
		}
	})
	t.Run("retired already", func(t *testing.T) {
		e := handRemoved(t)
		if err := e.writeRecord(Record{At: t0.Add(-time.Hour), Removed: []string{App}}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Tidy(t0); !errors.Is(err, ErrNothing) {
			t.Fatalf("err %v, want ErrNothing", err)
		}
		if rec, _ := e.ReadRecord(); rec.ByHand || !rec.At.Equal(t0.Add(-time.Hour)) {
			t.Fatalf("the retirement's record was rewritten: %+v", rec)
		}
	})
	t.Run("an init script of no package", func(t *testing.T) {
		e := handRemoved(t)
		write(t, e.PassWall[0], "#!/bin/sh /etc/rc.common\n", 0o755)
		if _, err := e.Tidy(t0); !errors.Is(err, ErrRefused) {
			t.Fatalf("err %v, want a refusal: an init script on the router is a person's to take", err)
		}
		untouchedByHand(t, e)
	})
	t.Run("no backup", func(t *testing.T) {
		e := handRemoved(t)
		e.BackupDir = filepath.Join(e.Configs[0], "backup") // under a file: cannot be made
		if _, err := e.Tidy(t0); !errors.Is(err, ErrRefused) {
			t.Fatalf("err %v, want a refusal", err)
		}
		untouchedByHand(t, e)
	})
}

func untouchedByHand(t *testing.T, e Env) {
	t.Helper()
	if _, ok := e.ReadRecord(); ok {
		t.Fatal("a record written")
	}
	if !e.owes() || !exists(e.Configs[0]) || !exists(e.Snippet) {
		t.Fatal("something of what PassWall2 left went")
	}
}

// The clock measures the state it was started in: PassWall2 on the router,
// or gone with no record of it. In the other it counts for nothing, so a
// PassWall2 absent for the seconds of an opkg upgrade is never taken for one
// a person removed, and one put back never inherits the time it was away.
func TestTheClockCountsOnlyInTheStateItStartedIn(t *testing.T) {
	e := handRemoved(t)
	if at, err := e.StartClock(t0); err != nil || !at.Equal(t0) {
		t.Fatalf("start = %v, %v", at, err)
	}
	if raw := string(mustRead(t, filepath.Join(e.MarkerDir, ClockName))); raw != "2026-09-30T12:00:00Z\nabsent\n" {
		t.Fatalf("the clock of PassWall2's absence reads %q", raw)
	}
	if since, ok := e.Since(); !ok || !since.Equal(t0) {
		t.Fatalf("since = %v, %v", since, ok)
	}

	// PassWall2 back: its absence's window is not its.
	write(t, e.PassWall[0], "#!/bin/sh /etc/rc.common\n", 0o755)
	if _, ok := e.Since(); ok {
		t.Fatal("the window of PassWall2's absence counts while it is on the router")
	}
	if at, _ := e.StartClock(t0.Add(time.Hour)); !at.Equal(t0.Add(time.Hour)) {
		t.Fatalf("not started over for PassWall2 on the router: %v", at)
	}
	if raw := string(mustRead(t, filepath.Join(e.MarkerDir, ClockName))); raw != "2026-09-30T13:00:00Z\n" {
		t.Fatalf("the retirement's clock reads %q", raw)
	}

	// Gone again: the retirement's window is not the absence's.
	_ = os.Remove(e.PassWall[0])
	if _, ok := e.Since(); ok {
		t.Fatal("the retirement's window counts while PassWall2 is gone")
	}
	if at, _ := e.StartClock(t0.Add(2 * time.Hour)); !at.Equal(t0.Add(2 * time.Hour)) {
		t.Fatalf("not started over for PassWall2 gone: %v", at)
	}
}

// A clock of the other state goes at a look, so that it cannot come back to
// life when the state does: PassWall2 put back and taken away again within
// the window must wait out a whole window of absence.
func TestAStaleClockGoes(t *testing.T) {
	e := handRemoved(t)
	if _, err := e.StartClock(t0); err != nil {
		t.Fatal(err)
	}
	if dropped, err := e.DropStaleClock(); dropped || err != nil {
		t.Fatalf("the clock of the state the router is in went: %v %v", dropped, err)
	}
	write(t, e.PassWall[0], "#!/bin/sh /etc/rc.common\n", 0o755)
	if dropped, err := e.DropStaleClock(); !dropped || err != nil {
		t.Fatalf("a clock of PassWall2's absence kept while it is back: %v %v", dropped, err)
	}
	_ = os.Remove(e.PassWall[0])
	if _, ok := e.Since(); ok {
		t.Fatal("the old window of absence came back to life")
	}
	if dropped, _ := e.DropStaleClock(); dropped {
		t.Fatal("no clock, yet one went")
	}
}
