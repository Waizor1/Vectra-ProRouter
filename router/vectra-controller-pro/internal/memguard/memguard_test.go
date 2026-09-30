package memguard

import (
	"os"
	"path/filepath"
	"testing"
)

const meminfo1111 = `MemTotal:         239720 kB
MemFree:           51408 kB
MemAvailable:      45948 kB
Buffers:               0 kB
Cached:            62808 kB
SwapCached:          120 kB
SwapTotal:        119804 kB
SwapFree:         115452 kB
`

func TestParseMeminfo(t *testing.T) {
	in, err := Parse([]byte(meminfo1111))
	if err != nil {
		t.Fatal(err)
	}
	if in.TotalKB != 239720 || in.AvailableKB != 45948 || in.FreeKB != 51408 || in.SwapTotalKB != 119804 || in.SwapFreeKB != 115452 {
		t.Fatalf("parsed %+v", in)
	}
	if _, err := Parse([]byte("MemTotal: 100 kB\nMemFree: 50 kB\n")); err == nil {
		t.Fatal("meminfo without MemAvailable must be refused")
	}
}

func TestThresholdsFor234MB(t *testing.T) {
	const total = 239720
	if got := XrayGoMemLimitMiB(total); got != 51 {
		t.Fatalf("xray GOMEMLIMIT on 234 MB = %d MiB, want 51", got)
	}
	if got := XrayGoMemLimitMiB(128 * 1024); got != 28 {
		t.Fatalf("xray GOMEMLIMIT on 128 MB = %d, want 28", got)
	}
	if got := XrayGoMemLimitMiB(64 * 1024); got != 24 {
		t.Fatalf("floor: %d, want 24", got)
	}
	if got := XrayGoMemLimitMiB(1024 * 1024); got != 80 {
		t.Fatalf("ceiling: %d, want 80", got)
	}
	if got := MiB(CriticalKB(total)); got != 12 {
		t.Fatalf("critical on 234 MB = %d MiB, want 12", got)
	}
	if got := MiB(HeavyFloorKB(total)); got != 24 {
		t.Fatalf("heavy floor on 234 MB = %d MiB, want 24", got)
	}
	if got := MiB(XrayReliefKB(total)); got != 21 {
		t.Fatalf("relief threshold on 234 MB = %d MiB, want 21", got)
	}
}

func TestElementBudget(t *testing.T) {
	// 60 MB available on 1111: the whole DIRECT list (14.8k ranges) fits.
	if n := ElementBudget(Info{TotalKB: 239720, AvailableKB: 60 * 1024}); n < 15000 {
		t.Fatalf("60 MB available: budget %d, want the whole list (>=15000)", n)
	}
	// 36 MB: a part of it.
	if n := ElementBudget(Info{TotalKB: 239720, AvailableKB: 36 * 1024}); n < 2000 || n > 9000 {
		t.Fatalf("36 MB available: budget %d, want a part (2000..9000)", n)
	}
	// At the floor: nothing.
	if n := ElementBudget(Info{TotalKB: 239720, AvailableKB: 26 * 1024}); n != 0 {
		t.Fatalf("26 MB available: budget %d, want 0", n)
	}
}

func TestOOMKills(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vmstat")
	if err := os.WriteFile(p, []byte("pgfault 12\noom_kill 3\npswpin 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, ok := oomKillsFrom(p); !ok || n != 3 {
		t.Fatalf("oom_kill = %d %v, want 3", n, ok)
	}
	if err := os.WriteFile(p, []byte("pgfault 12\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := oomKillsFrom(p); ok {
		t.Fatal("no oom_kill line must be ok=false")
	}
}

// With swap (zram on the fleet) MemAvailable stays up while the kernel
// compresses programs' pages into it: the router is out of memory once the
// swap is all but full, before MemAvailable crosses the line (1111 under load,
// 2026-09-30: SwapFree down to 2 MB with MemAvailable still at 12 MB).
func TestCriticalCountsAFullSwap(t *testing.T) {
	const total, swap = 239720, 119804
	for _, c := range []struct {
		name string
		in   Info
		want bool
	}{
		{"at rest", Info{TotalKB: total, AvailableKB: 45 * 1024, SwapTotalKB: swap, SwapFreeKB: 110 * 1024}, false},
		{"below the line, swap free", Info{TotalKB: total, AvailableKB: 11 * 1024, SwapTotalKB: swap, SwapFreeKB: 110 * 1024}, true},
		{"swap all but full, memory low", Info{TotalKB: total, AvailableKB: 20 * 1024, SwapTotalKB: swap, SwapFreeKB: 10 * 1024}, true},
		{"swap all but full, memory fine", Info{TotalKB: total, AvailableKB: 40 * 1024, SwapTotalKB: swap, SwapFreeKB: 10 * 1024}, false},
		{"swap half used, memory low", Info{TotalKB: total, AvailableKB: 20 * 1024, SwapTotalKB: swap, SwapFreeKB: 60 * 1024}, false},
		{"no swap, memory low", Info{TotalKB: total, AvailableKB: 20 * 1024}, false},
		{"a token swap, full", Info{TotalKB: total, AvailableKB: 20 * 1024, SwapTotalKB: 8 * 1024}, false},
	} {
		if got := Critical(c.in); got != c.want {
			t.Errorf("%s: Critical = %v, want %v", c.name, got, c.want)
		}
	}
	if got := MiB(SwapLowKB(swap)); got != 17 {
		t.Fatalf("the full-swap line on 1111's zram = %d MiB, want 17", got)
	}
}

// A small swap is "all but full" in proportion: its floor never passes a
// quarter of it (review of r35: 16 MiB of a 32 MiB swap was half of it).
func TestSwapLowScalesForSmallSwaps(t *testing.T) {
	for _, c := range []struct{ swapMiB, want uint64 }{{32, 8}, {64, 16}, {117, 17}, {512, 76}} {
		if got := MiB(SwapLowKB(c.swapMiB * 1024)); got != c.want {
			t.Errorf("swap %d MiB: all but full below %d MiB, want %d", c.swapMiB, got, c.want)
		}
	}
}

// A process's own memory counts what it has in swap too: with zram all but
// full, xray's pages sit there, compressed but still the router's RAM, and a
// resident-only count would call a fat xray thin (review of r35).
func TestReadRSSCountsSwappedPages(t *testing.T) {
	p := filepath.Join(t.TempDir(), "status")
	if err := os.WriteFile(p, []byte("Name:\txray\nVmRSS:\t  30000 kB\nRssAnon:\t  22000 kB\nRssFile:\t   8000 kB\nVmSwap:\t  25000 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	anon, file, err := readRSSFrom(p)
	if err != nil || anon != 47000 || file != 8000 {
		t.Fatalf("anon %d file %d err %v, want 47000 (22000 resident + 25000 swapped) and 8000", anon, file, err)
	}
}
