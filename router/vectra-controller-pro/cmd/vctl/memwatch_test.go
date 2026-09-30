package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/supervisor"
)

type fakeSup struct {
	st      supervisor.Status
	reloads int
}

func (f *fakeSup) Status() supervisor.Status { return f.st }
func (f *fakeSup) Reload(context.Context) error {
	f.reloads++
	return nil
}

func TestMemWatchRestartsAFatXrayOnlyWhenMemoryStaysCritical(t *testing.T) {
	const total = 239720
	avail := uint64(40 * 1024)
	xrayAnon := uint64(48 * 1024)
	sup := &fakeSup{st: supervisor.Status{State: supervisor.StateRunning, PID: 4242}}
	w := &memWatch{
		read:  func() (memguard.Info, error) { return memguard.Info{TotalKB: total, AvailableKB: avail}, nil },
		rss:   func(int) (uint64, uint64, error) { return xrayAnon, 18 * 1024, nil },
		kills: func() (uint64, bool) { return 0, true },
		sup:   sup,
	}
	now := time.Unix(1_800_000_000, 0)
	step := func() bool { now = now.Add(memWatchEvery); return w.step(context.Background(), now) }

	for i := 0; i < 5; i++ {
		if step() {
			t.Fatal("restarted xray with 40 MiB available")
		}
	}
	avail = 9 * 1024 // below the 12 MiB critical line
	if step() || step() {
		t.Fatal("restarted before three samples in a row")
	}
	if !step() || sup.reloads != 1 {
		t.Fatalf("third critical sample with a 48 MiB xray: reloads %d, want 1", sup.reloads)
	}
	for i := 0; i < 10; i++ {
		if step() {
			t.Fatal("restarted again within the relief gap")
		}
	}
	// Still short a minute after it: the restart did not help, and the
	// next waits twice as long.
	now = now.Add(memReliefGap)
	if step() {
		t.Fatal("restarted again at the base gap after a restart that did not help")
	}
	now = now.Add(memReliefGap)
	if !step() || sup.reloads != 2 {
		t.Fatalf("after the doubled gap: reloads %d, want 2", sup.reloads)
	}
	// This one helps: memory is back a minute later, and the gap resets.
	avail = 60 * 1024
	now = now.Add(memReliefJudge)
	step()
	if w.gap != memReliefGap {
		t.Fatalf("gap %v after a restart that helped, want %v", w.gap, memReliefGap)
	}
	avail = 9 * 1024

	// Short of memory, but not because of xray: it is left alone.
	now = now.Add(memReliefGap)
	xrayAnon = 12 * 1024
	for i := 0; i < 5; i++ {
		if step() {
			t.Fatal("restarted a 12 MiB xray")
		}
	}
	// xray not running: nothing to restart.
	xrayAnon = 48 * 1024
	sup.st.State = supervisor.StateBackoff
	for i := 0; i < 5; i++ {
		if step() {
			t.Fatal("restarted an xray that is not running")
		}
	}
	// Memory back: the count starts over.
	sup.st.State = supervisor.StateRunning
	avail = 60 * 1024
	step()
	if w.low != 0 {
		t.Fatalf("low samples %d after memory came back, want 0", w.low)
	}
}

// The ledger in the watchdog: a snapshot a minute; an hourly review that,
// with six hours and more of samples, names a floor that rose and writes the
// report — and says so again only when it rose further.
func TestMemWatchReviewsTheLedger(t *testing.T) {
	report := filepath.Join(t.TempDir(), "memory.json")
	minute := 0
	w := &memWatch{
		read:   func() (memguard.Info, error) { return memguard.Info{TotalKB: 239720, AvailableKB: 90 * 1024}, nil },
		rss:    func(int) (uint64, uint64, error) { return 0, 0, nil },
		kills:  func() (uint64, bool) { return 0, true },
		sup:    &fakeSup{},
		ledger: memguard.NewLedger(),
		snapshot: func(now time.Time) (memguard.Snapshot, error) {
			minute++
			return memguard.Snapshot{At: now, AvailableKB: 90 * 1024, Procs: map[string]uint64{
				"xray": 13 * 1024,
				"vctl": 6*1024 + uint64(minute)*40, // +16 MiB over 7 h: a leak
			}}, nil
		},
		reportPath: report,
	}
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 7*60; i++ {
		now = now.Add(time.Minute)
		w.step(context.Background(), now)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("no report after 7 hours: %v", err)
	}
	var rep memoryReport
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Growing) != 1 || rep.Growing[0].Series != "vctl" || rep.Growing[0].ToMiB <= rep.Growing[0].FromMiB {
		t.Fatalf("growing: %+v", rep.Growing)
	}
	if rep.TopMiB["xray"] != 13 || rep.AvailableMiB != 90 {
		t.Fatalf("report: %+v", rep)
	}
	if _, ok := w.reported["vctl"]; !ok {
		t.Fatal("the leak was not logged")
	}
}
