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
