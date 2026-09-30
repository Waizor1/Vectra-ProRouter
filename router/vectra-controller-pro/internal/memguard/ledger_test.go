package memguard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeProc(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTakeSnapshot(t *testing.T) {
	root := t.TempDir()
	writeProc(t, root, map[string]string{
		"meminfo":     "MemTotal: 239720 kB\nMemAvailable: 98696 kB\nShmem: 520 kB\nSUnreclaim: 25588 kB\nSwapTotal: 119804 kB\nSwapFree: 118780 kB\n",
		"100/status":  "Name:\txray\nRssAnon:\t   12948 kB\nRssFile:\t 26212 kB\n",
		"101/status":  "Name:\tvctl\nRssAnon:\t    6332 kB\n",
		"102/status":  "Name:\tdnsmasq\nRssAnon:\t     408 kB\n",
		"103/status":  "Name:\tdnsmasq\nRssAnon:\t     300 kB\n",
		"104/status":  "Name:\tsleep\nRssAnon:\t      80 kB\n",
		"105/status":  "Name:\tkthreadd\n",
		"self/status": "Name:\tvctl\nRssAnon:\t 1 kB\n",
		"100/fd/0":    "", "100/fd/1": "", "100/fd/7": "",
		"102/fd/0": "", "103/fd/0": "", "103/fd/4": "",
		"sys/net/netfilter/nf_conntrack_count": "412\n",
	})
	s, err := TakeSnapshot(root, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if s.AvailableKB != 98696 || s.SUnreclaimKB != 25588 || s.ShmemKB != 520 || s.SwapUsedKB != 1024 {
		t.Fatalf("system figures: %+v", s)
	}
	want := map[string]uint64{"xray": 12948, "vctl": 6332, "dnsmasq": 708}
	if len(s.Procs) != len(want) {
		t.Fatalf("procs = %v, want %v (a small sleep and a kernel thread are no series)", s.Procs, want)
	}
	for n, kb := range want {
		if s.Procs[n] != kb {
			t.Fatalf("%s = %d, want %d", n, s.Procs[n], kb)
		}
	}
	// The load: open descriptors by name (two dnsmasq, 3 between them), the
	// tracked connections for the router's own figures; none for tmpfs, none
	// where a process's descriptors cannot be read.
	for n, want := range map[string]uint64{"xray": 3, "dnsmasq": 3, SeriesAvailable: 412, SeriesSlab: 412, SeriesSwap: 412} {
		if got, ok := s.Load[n]; !ok || got != want {
			t.Errorf("load of %s = %d (%v), want %d", n, got, ok, want)
		}
	}
	for _, n := range []string{"vctl", SeriesTmpfs} {
		if _, ok := s.Load[n]; ok {
			t.Errorf("a load for %s", n)
		}
	}
	if _, err := TakeSnapshot(filepath.Join(root, "none"), time.Now()); err == nil {
		t.Fatal("no meminfo, and no error")
	}
}

// A floor that rose with the load is the load: xray's connections of the
// evening against the quiet hour after the 04:30 reboot. At the load it
// started at, the same rise is said — a leak, or a cache that fills.
func TestLedgerJudgesAtTheSameLoad(t *testing.T) {
	start := time.Date(2026, 9, 29, 1, 30, 0, 0, time.UTC)
	day := func(lastHourBusy bool) []Growth {
		l := NewLedger()
		feed(l, start, 8*time.Hour, func(m int) Snapshot {
			xray, fds, slab, ct := uint64(9000), uint64(40), uint64(20000), uint64(150)
			if m >= 60 {
				xray, fds, slab, ct = 21000, 500, 30000, 2400 // the day's traffic
			}
			if m >= 7*60 && !lastHourBusy {
				fds, ct = 42, 160 // quiet again; what was held stays
			}
			return Snapshot{
				AvailableKB: 90000, SUnreclaimKB: slab,
				Procs: map[string]uint64{"xray": xray},
				Load:  map[string]uint64{"xray": fds, SeriesSlab: ct, SeriesAvailable: ct},
			}
		})
		return l.Growing(8*1024, 0.25, 6*time.Hour)
	}
	if g := day(true); len(g) != 0 {
		t.Fatalf("growth with the load read as a leak: %+v", g)
	}
	g := day(false)
	got := map[string]Growth{}
	for _, x := range g {
		got[x.Series] = x
	}
	x, ok := got["xray"]
	if !ok || !x.LoadKnown || x.LoadFrom != 40 || x.LoadTo != 42 {
		t.Fatalf("xray holding 12 MB more at the same load was not said: %+v", g)
	}
	if _, ok := got[SeriesSlab]; !ok {
		t.Fatalf("the kernel's slab 10 MB up at the same load was not said: %+v", g)
	}
}

// feed adds a sample every minute for d, from start, with values from f.
func feed(l *Ledger, start time.Time, d time.Duration, f func(min int) Snapshot) time.Time {
	t := start
	for i := 0; t.Before(start.Add(d)); i++ {
		s := f(i)
		s.At = t
		l.Add(s)
		t = t.Add(time.Minute)
	}
	return t
}

// A floor that rises is a leak; spikes on a flat floor are load, not a
// leak; MemAvailable's ceiling falling is memory lost.
func TestLedgerFindsRisingFloorsNotSpikes(t *testing.T) {
	l := NewLedger()
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	feed(l, start, 6*time.Hour, func(m int) Snapshot {
		spike := uint64(0)
		if m%30 < 5 {
			spike = 40000 // xray under load for 5 minutes in every 30
		}
		return Snapshot{
			AvailableKB:  100000 - uint64(m)*20, // loses 7.2 MB over 6 h
			SUnreclaimKB: 25000,
			Procs: map[string]uint64{
				"xray":    13000 + spike,        // flat floor, big spikes
				"vctl":    6000 + uint64(m)*10,  // +3.6 MB over 6 h: a leak
				"dnsmasq": 700 + uint64(m%7)*10, // noise
			},
		}
	})
	g := l.Growing(1024, 0.10, 5*time.Hour)
	got := map[string]Growth{}
	for _, x := range g {
		got[x.Series] = x
	}
	if _, ok := got["xray"]; ok {
		t.Fatalf("xray's spikes read as a leak: %+v", g)
	}
	if _, ok := got["dnsmasq"]; ok {
		t.Fatalf("dnsmasq's noise read as a leak: %+v", g)
	}
	v, ok := got["vctl"]
	if !ok || v.ToKB <= v.FromKB || v.ToKB-v.FromKB < 3000 {
		t.Fatalf("vctl's rising floor not found: %+v", g)
	}
	a, ok := got[SeriesAvailable]
	if !ok || a.FromKB <= a.ToKB {
		t.Fatalf("MemAvailable's falling ceiling not found: %+v", g)
	}
	// Less than the span of samples: nothing is judged yet.
	l2 := NewLedger()
	feed(l2, start, 3*time.Hour, func(m int) Snapshot {
		return Snapshot{Procs: map[string]uint64{"vctl": 6000 + uint64(m)*100}}
	})
	if g := l2.Growing(1024, 0.1, 5*time.Hour); len(g) != 0 {
		t.Fatalf("judged on 3 hours: %+v", g)
	}
}

// The window is 24 hours of 10-minute buckets; a process gone for two hours
// is dropped.
func TestLedgerKeepsADay(t *testing.T) {
	l := NewLedger()
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	end := feed(l, start, 30*time.Hour, func(m int) Snapshot {
		p := map[string]uint64{"xray": 13000}
		if m < 60 {
			p["opkg"] = 9000
		}
		return Snapshot{AvailableKB: 90000, Procs: p}
	})
	l.mu.Lock()
	n := len(l.series["xray"])
	_, opkg := l.series["opkg"]
	l.mu.Unlock()
	if n != LedgerKeep {
		t.Fatalf("%d buckets kept, want %d", n, LedgerKeep)
	}
	if opkg {
		t.Fatal("a process gone for a day is still a series")
	}
	top, last := l.Top(5)
	if len(top) != 1 || top[0].Name != "xray" || !last.At.Equal(end.Add(-time.Minute)) {
		t.Fatalf("top %+v at %v", top, last.At)
	}
}
