package bugreport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const panicStack = `panic: runtime error: index out of range [3] with length 3

goroutine 42 [running]:
vectra-controller-pro/internal/routepolicy.pickNode(0x4000123456, 0x3)
	/build/internal/routepolicy/refresh.go:211 +0x1c8
vectra-controller-pro/internal/routepolicy.Refresh({0x40001, 0x2}, 0x1)
	/build/internal/routepolicy/refresh.go:120 +0x3f0
main.(*daemon).refreshNative(0x4000010000)
	/build/cmd/vctl/native_source.go:480 +0x120
main.(*daemon).loop(0x4000010000)
	/build/cmd/vctl/cmd_agent.go:600 +0x88
`

func TestPanicKeyIgnoresNumbersAndAddresses(t *testing.T) {
	title, key := PanicKey(panicStack)
	if title != "panic: runtime error: index out of range [3] with length 3" {
		t.Fatalf("title %q", title)
	}
	_, key2 := PanicKey(strings.ReplaceAll(strings.ReplaceAll(panicStack, "[3] with length 3", "[7] with length 7"), "0x4000123456", "0x4000999999"))
	if key != key2 {
		t.Fatalf("keys differ:\n%s\n---\n%s", key, key2)
	}
	if !strings.Contains(key, "routepolicy.pickNode") || strings.Contains(key, "cmd_agent.go") {
		t.Fatalf("key %q", key)
	}
}

func TestCrashesOfOneKindMergeIntoOneReport(t *testing.T) {
	dir := t.TempDir()
	for _, pid := range []string{"101", "102", "103"} {
		if err := os.WriteFile(filepath.Join(dir, "vctl."+pid), []byte(panicStack), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "vctl.104"), nil, 0o644); err != nil { // stopped cleanly
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vctl.200"), []byte(panicStack), 0o644); err != nil { // still runs
		t.Fatal(err)
	}
	found := Crashes(dir, func(pid int) bool { return pid == 200 })
	codes := map[string]int{}
	for _, in := range found {
		codes[in.Code]++
	}
	if codes["VCTL_PANIC"] != 3 || codes["VCTL_CRASH_LOOP"] != 1 || found[0].Stack == "" {
		t.Fatalf("%+v", found)
	}
	s := Spool{Dir: t.TempDir(), MaxFiles: 32, MaxBytes: 512 << 10}
	now := time.Unix(1790000000, 0).UTC()
	for _, in := range found {
		s.Add(FromIncident(in, Router{DeviceID: "d", Vctl: "v"}, now), now, time.Time{})
	}
	counts := map[string]int{}
	for _, e := range s.Entries() {
		counts[e.Report.Code] = e.Report.Count
	}
	if len(counts) != 2 || counts["VCTL_PANIC"] != 3 || counts["VCTL_CRASH_LOOP"] != 1 {
		t.Fatalf("%+v", counts)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "vctl.*"))
	if len(left) != 1 || filepath.Base(left[0]) != "vctl.200" {
		t.Fatalf("left %v", left)
	}
}

const dmesgText = `[    5.100000] random: crng init done
[38201.412345] xray invoked oom-killer: gfp_mask=0x140cca(GFP_HIGHUSER_MOVABLE|__GFP_COMP), order=0
[38201.500000] Out of memory: Killed process 2211 (xray) total-vm:1263452kB, anon-rss:98304kB, file-rss:0kB
[38300.000000] kworker/0:1: page allocation failure: order:2, mode:0x40cc0(GFP_KERNEL|__GFP_COMP)
[38300.000001] CPU: 0 PID: 12 Comm: kworker/0:1
[38300.000002] Call trace:
`

func TestDmesgFindsOOMKillsAndKernelErrorsAfterTheCursor(t *testing.T) {
	found, last := Dmesg(dmesgText, 10)
	if last != 38300.000002 || len(found) != 2 {
		t.Fatalf("last %v, %+v", last, found)
	}
	if found[0].Code != "OOM_KILL" || found[0].Key != "oom xray" || found[0].Details["anonRssKB"] != 98304 {
		t.Fatalf("%+v", found[0])
	}
	if found[1].Code != "KERNEL_ERROR" || found[1].Key != "page allocation failure order:2" || len(found[1].Kernel) != 3 {
		t.Fatalf("%+v", found[1])
	}
	if again, _ := Dmesg(dmesgText, last); len(again) != 0 {
		t.Fatalf("read twice: %+v", again)
	}
}

func TestDataplaneWithoutXrayIsSaidOnceAtTheSecondMinute(t *testing.T) {
	var st State
	if DataplaneWithoutXray(true, false, &st) != nil {
		t.Fatal("said at the first minute")
	}
	if in := DataplaneWithoutXray(true, false, &st); in == nil || in.Code != "DATAPLANE_WITHOUT_XRAY" {
		t.Fatal("not said at the second")
	}
	if DataplaneWithoutXray(true, false, &st) != nil {
		t.Fatal("said again")
	}
	DataplaneWithoutXray(true, true, &st)
	if st.XrayMissing != 0 {
		t.Fatal("xray back, the count stayed")
	}
}

func TestBootSaysWhatItDidNotSeeComing(t *testing.T) {
	dir := t.TempDir()
	marker, pstore := filepath.Join(dir, "clean-shutdown"), filepath.Join(dir, "pstore")
	now := time.Unix(1790000000, 0).UTC()
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if Boot(marker, pstore, now) != nil {
		t.Fatal("a clean shutdown reported")
	}
	if in := Boot(marker, pstore, now); in == nil || in.Code != "UNEXPECTED_REBOOT" {
		t.Fatal("no marker, and nothing said")
	}
	if err := os.MkdirAll(pstore, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pstore, "dmesg-ramoops-0"), []byte("Kernel panic - not syncing: Oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(marker, nil, 0o644)
	in := Boot(marker, pstore, now)
	if in == nil || len(in.Kernel) != 1 || !strings.Contains(in.Title, "kernel") {
		t.Fatalf("%+v", in)
	}
	if left, _ := filepath.Glob(filepath.Join(pstore, "*")); len(left) != 0 {
		t.Fatal("pstore kept what was read")
	}
}

// A kernel that keeps its console in pstore (ramoops' console-size) leaves
// console-ramoops-0 after EVERY boot, and pmsg-ramoops-0 is userspace's: only
// a dmesg record is the kernel's end. (The AX3000T keeps none of the two; other
// routers do.)
func TestBootTakesOnlyTheKernelsDumpsForAFailure(t *testing.T) {
	dir := t.TempDir()
	marker, pstore := filepath.Join(dir, "clean-shutdown"), filepath.Join(dir, "pstore")
	now := time.Unix(1790000000, 0).UTC()
	if err := os.MkdirAll(pstore, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"console-ramoops-0": "[    0.000000] Booting Linux on physical CPU 0x0\nreboot: Restarting system\n",
		"pmsg-ramoops-0":    "procd: - shutdown -\n",
	} {
		if err := os.WriteFile(filepath.Join(pstore, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if in := Boot(marker, pstore, now); in != nil {
		t.Fatalf("a clean reboot reported for the console the kernel keeps: %+v", in)
	}
	if err := os.WriteFile(filepath.Join(pstore, "dmesg-ramoops-0"), []byte("Kernel panic - not syncing: Oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Boot(marker, pstore, now)
	if in == nil || len(in.Kernel) != 1 || in.Kernel[0] != "Kernel panic - not syncing: Oops" {
		t.Fatalf("%+v", in)
	}
	if left, _ := filepath.Glob(filepath.Join(pstore, "*")); len(left) != 2 {
		t.Fatalf("pstore after the dump was read: %v (the console and pmsg records are not the reporter's)", left)
	}
}

// A kernel told to dump at every shutdown too (ramoops.max_reason, or
// printk.always_kmsg_dump) leaves a dmesg record after a clean reboot as
// well; the record's header says why it was written — "Panic#", "Oops#",
// "Emergency#" are a failure, "Shutdown#", "Restart#", "Halt#", "Poweroff#"
// are not.
func TestBootReadsWhyTheKernelDumped(t *testing.T) {
	dir := t.TempDir()
	marker, pstore := filepath.Join(dir, "clean-shutdown"), filepath.Join(dir, "pstore")
	now := time.Unix(1790000000, 0).UTC()
	if err := os.MkdirAll(pstore, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, clean := range []string{"Shutdown", "Restart", "Halt", "Poweroff"} {
		if err := os.WriteFile(filepath.Join(pstore, "dmesg-ramoops-0"), []byte(clean+"#1 Part1\n<6>[  812.1] reboot: Restarting system\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if in := Boot(marker, pstore, now); in != nil {
			t.Fatalf("a %s dump reported: %+v", clean, in)
		}
		if left, _ := filepath.Glob(filepath.Join(pstore, "*")); len(left) != 0 {
			t.Fatalf("the %s dump kept: %v", clean, left)
		}
	}
	for _, fail := range []string{"Panic", "Oops", "Emergency"} {
		if err := os.WriteFile(filepath.Join(pstore, "dmesg-ramoops-0"), []byte(fail+"#1 Part1\n<0>[  812.1] Kernel panic - not syncing\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if in := Boot(marker, pstore, now); in == nil || len(in.Kernel) != 2 || !strings.Contains(in.Title, "kernel failure") {
			t.Fatalf("a %s dump: %+v", fail, in)
		}
	}
}

func TestStateAllowsTwelveAnHour(t *testing.T) {
	var st State
	now := time.Unix(1790000000, 0).UTC()
	n := 0
	for i := 0; i < 20; i++ {
		if st.Allow(now) {
			n++
		}
	}
	if n != 12 || !st.Allow(now.Add(61*time.Minute)) {
		t.Fatalf("%d allowed in an hour", n)
	}
}
