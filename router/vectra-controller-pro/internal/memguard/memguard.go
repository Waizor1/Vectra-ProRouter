// Package memguard is how vctl lives within a small router's memory.
//
// The fleet is 234 MB routers with 40-65 MB available at rest. What the
// data-plane stand measured on the same arm64 build of xray (PassWall-shaped
// config, 2026-09-29):
//
//   - every connection xray carries costs it ~38 KB: 1000 held open took its
//     heap from 8 to 47 MB, 2000 to 82 MB — past its GOMEMLIMIT, which is a
//     GC target and caps nothing — and Go kept 55 MB after they closed;
//   - `xray run -test` peaks at ~40 MB RSS (~15 MB of it its own, the rest the
//     binary's pages it shares with the running xray);
//   - `nft` holding a 15k-range set peaks at ~24 MB — to load it, and to LIST
//     it (`nft -t` does not list set contents: 2.5 MB).
//
// And the kernel's choice when memory runs out was the wrong one: vctl ran
// xray (and itself) at oom_score_adj -100, which on this hardware put xray
// BELOW hostapd, dnsmasq, netifd and rpcd — the OOM killer would take the
// Wi-Fi, DNS and the LAN first and keep going while xray, the one process
// that had grown, survived. That is "it works for five minutes and hangs".
//
// So: the kernel kills what grew (xray at 0, the transient helpers first),
// never the controller that restarts it; xray's GC target follows the RAM
// the router has; vctl gives xray's memory back itself (a restart) before
// the kernel has to; and nothing heavy starts when memory is short.
package memguard

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Info is /proc/meminfo's view of the router, in kB.
type Info struct {
	TotalKB     uint64
	AvailableKB uint64
	FreeKB      uint64
	SwapTotalKB uint64
	SwapFreeKB  uint64
}

// MiB converts kB to whole MiB, for logs.
func MiB(kb uint64) uint64 { return kb / 1024 }

// Read reads /proc/meminfo.
func Read() (Info, error) { return ReadFrom("/proc/meminfo") }

// ReadFrom reads a meminfo-format file.
func ReadFrom(path string) (Info, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Info{}, err
	}
	return Parse(b)
}

// Parse reads meminfo text. MemAvailable is required: without it (kernels
// older than 3.14) there is no honest "how much can still be had".
func Parse(b []byte) (Info, error) {
	var in Info
	var haveAvail bool
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			in.TotalKB = v
		case "MemAvailable:":
			in.AvailableKB, haveAvail = v, true
		case "MemFree:":
			in.FreeKB = v
		case "SwapTotal:":
			in.SwapTotalKB = v
		case "SwapFree:":
			in.SwapFreeKB = v
		}
	}
	if in.TotalKB == 0 || !haveAvail {
		return Info{}, fmt.Errorf("meminfo: no MemTotal/MemAvailable")
	}
	return in, nil
}

// OOM score adjustments (oom_score_adj, -1000..1000). The kernel's pick is
// the process with the highest (RSS + swap + page tables) / (RAM + swap) *
// 1000 + adj, so on a 234 MB router with 117 MB of zram every 3.5 MB of RSS
// is one point. An adj is a handicap in those points.
const (
	// ControllerAdj keeps vctl out of the kernel's reach: it is what brings
	// xray back after a kill and holds the data plane's safety nets. It is
	// small (6 MB of heap) and does not grow with traffic.
	ControllerAdj = -800
	// XrayAdj is neutral on purpose. xray is the one process whose memory
	// grows with traffic, so when memory runs out it is — and should be — the
	// kernel's pick; vctl restarts it within seconds. Any protection puts
	// hostapd, dnsmasq and netifd (1-4 MB, a handful of points) in front of it.
	XrayAdj = 0
	// TransientAdj is for what vctl runs for a moment beside xray — the
	// `xray -test` of a new configuration, PassWall's generator: when one of
	// them tips the router over, it is the one to go, and the apply is
	// retried later; the running data plane is untouched.
	TransientAdj = 500
	// JobAdj is for commands the panel runs (terminal jobs, installs): they
	// would otherwise inherit the controller's protection.
	JobAdj = 0
)

// XrayGoMemLimitMiB is xray's GOMEMLIMIT for a router with totalKB of RAM:
// 22% of it, 24..80 MiB. At 80 MiB (the old fixed value) a 234 MB router is
// out of memory before Go even starts to collect harder; at 22% (51 MiB)
// there is room left for the kernel's socket buffers, the page cache xray's
// own binary lives in, and a configuration check.
func XrayGoMemLimitMiB(totalKB uint64) int {
	v := int(totalKB * 22 / 100 / 1024)
	if v < 24 {
		v = 24
	}
	if v > 80 {
		v = 80
	}
	return v
}

// CriticalKB is the MemAvailable below which the router is about to start
// killing: 5% of RAM, at least 12 MiB. The kernel keeps min_free_kbytes
// (24 MB on the fleet) out of MemAvailable already; below this it is
// reclaiming the pages of running programs and thrashing.
func CriticalKB(totalKB uint64) uint64 {
	v := totalKB * 5 / 100
	if v < 12*1024 {
		v = 12 * 1024
	}
	return v
}

// Critical is whether the router is about to start killing: MemAvailable
// below CriticalKB — or, with swap, the swap all but full (below SwapLowKB)
// and MemAvailable below twice CriticalKB. MemAvailable counts no swap: while
// zram (the fleet's) has room, the kernel keeps it up by compressing programs'
// pages into it, and once zram is full it falls at once (1111 under load,
// 2026-09-30: SwapFree down to 2 MB with MemAvailable still at 12 MB). A swap
// under swapMinKB is too small to matter.
func Critical(in Info) bool {
	c := CriticalKB(in.TotalKB)
	if in.AvailableKB < c {
		return true
	}
	return in.SwapTotalKB >= swapMinKB && in.SwapFreeKB < SwapLowKB(in.SwapTotalKB) && in.AvailableKB < 2*c
}

// swapMinKB is the smallest swap Critical counts.
const swapMinKB = 32 * 1024

// SwapLowKB is the SwapFree below which a swap is all but full: 15% of it, at
// least 16 MiB — but never more than a quarter of it.
func SwapLowKB(swapTotalKB uint64) uint64 {
	floor := uint64(16 * 1024)
	if q := swapTotalKB / 4; q < floor {
		floor = q
	}
	v := swapTotalKB * 15 / 100
	if v < floor {
		v = floor
	}
	return v
}

// HeavyFloorKB is the MemAvailable a heavy transient (a configuration check,
// a large nft load) needs to be started at all: 24 MiB, or 10% of RAM.
func HeavyFloorKB(totalKB uint64) uint64 {
	v := totalKB * 10 / 100
	if v < 24*1024 {
		v = 24 * 1024
	}
	return v
}

// XrayReliefKB is the heap xray must hold before vctl restarts it to give
// memory back (see the daemon's watchdog): 9% of RAM, at least 20 MiB. Below
// that xray is not what ran the router out of memory.
func XrayReliefKB(totalKB uint64) uint64 {
	v := totalKB * 9 / 100
	if v < 20*1024 {
		v = 20 * 1024
	}
	return v
}

// What an nft process costs at its peak: 2.5 MB to run at all, and ~1.5 KB
// for every range of an interval set it loads — adding to a set that already
// holds ranges costs as much as loading them all, because nft reads the set
// back to check the new ones against it (measured on the stand: 15k ranges
// 24 MB; in chunks of 2000, the last chunk alone 23 MB).
const (
	nftBaseKB      = 2560
	nftPerElementB = 1536
)

// ElementBudget is how many ranges an nft set may be loaded with now: what
// the nft process may take at its peak without MemAvailable falling below
// the heavy floor. 0 when not even a useful handful fits.
func ElementBudget(in Info) int {
	floor := HeavyFloorKB(in.TotalKB)
	if in.AvailableKB <= floor+nftBaseKB {
		return 0
	}
	n := int((in.AvailableKB - floor - nftBaseKB) * 1024 / nftPerElementB)
	if n < 256 {
		return 0
	}
	return n
}

// ReadRSS reads a process's memory split, in kB: its own (anon) pages,
// resident or in swap — zram's are still the router's RAM — and the file pages
// it maps (the binary, shared and reclaimable).
func ReadRSS(pid int) (anonKB, fileKB uint64, err error) {
	return readRSSFrom(fmt.Sprintf("/proc/%d/status", pid))
}

func readRSSFrom(path string) (anonKB, fileKB uint64, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "RssAnon:", "VmSwap:":
			anonKB += v
		case "RssFile:":
			fileKB = v
		}
	}
	return anonKB, fileKB, nil
}

// OOMKills is the kernel's count of OOM kills since boot (/proc/vmstat
// oom_kill); ok false when the kernel does not report it.
func OOMKills() (uint64, bool) { return oomKillsFrom("/proc/vmstat") }

func oomKillsFrom(path string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "oom_kill "); ok {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

// SetOOMScoreAdj writes pid's oom_score_adj (Linux; an error elsewhere).
// Raising it needs no privilege; lowering it needs CAP_SYS_RESOURCE, which
// vctl (root) has.
func SetOOMScoreAdj(pid, adj int) error {
	return os.WriteFile(fmt.Sprintf("/proc/%d/oom_score_adj", pid), []byte(strconv.Itoa(adj)), 0o644)
}
