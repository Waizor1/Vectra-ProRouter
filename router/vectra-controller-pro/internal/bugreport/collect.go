package bugreport

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vectra-controller-pro/internal/incident"
)

var reNumber = regexp.MustCompile(`0x[0-9a-fA-F]+|\d+`)

// PanicKey is a crash's title — its "panic:" or "fatal error:" line — and
// its key: that line with numbers and addresses as N, and the first three
// frames of vctl's own code, so the same crash on another router, or with
// another index, is the same problem.
func PanicKey(stack string) (title, key string) {
	lines := strings.Split(stack, "\n")
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "panic:") || strings.HasPrefix(t, "fatal error:") || strings.HasPrefix(t, "SIG") {
			title = t
			break
		}
	}
	if title == "" {
		for _, l := range lines {
			if t := strings.TrimSpace(l); t != "" {
				title = t
				break
			}
		}
	}
	var frames []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "vectra-controller-pro/") && !strings.HasPrefix(t, "main.") {
			continue
		}
		if i := strings.LastIndexByte(t, '('); i > 0 {
			t = t[:i]
		}
		frames = append(frames, t)
		if len(frames) == 3 {
			break
		}
	}
	return capRunes(title, 200), reNumber.ReplaceAllString(title, "N") + "\n" + strings.Join(frames, "\n")
}

// Crashes turns vctl's crash output (CrashDir/vctl.<pid>) into incidents once
// its process is gone: a file with a stack is a VCTL_PANIC, an empty one — a
// vctl that stopped as it should — is taken away. A process that still runs
// keeps its file.
func Crashes(dir string, alive func(pid int) bool) []incident.Incident {
	names, _ := filepath.Glob(filepath.Join(dir, "vctl.*"))
	var out []incident.Incident
	for _, n := range names {
		pid, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(n), "vctl."))
		if err != nil {
			_ = os.Remove(n)
			continue
		}
		if alive(pid) {
			continue
		}
		at := time.Now()
		if st, err := os.Stat(n); err == nil {
			at = st.ModTime()
		}
		b, _ := os.ReadFile(n)
		_ = os.Remove(n)
		if len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		title, key := PanicKey(string(b))
		out = append(out, incident.Incident{Code: "VCTL_PANIC", Key: key, Title: "vctl crashed: " + title,
			At: at.UTC(), Source: "reporter", Stack: string(b)})
	}
	// procd starts a vctl that crashes again within seconds: three at one
	// look (a minute) are a loop, whatever each one says.
	if len(out) >= 3 {
		out = append(out, incident.Incident{Code: "VCTL_CRASH_LOOP", Key: "vctl crash loop",
			Title: fmt.Sprintf("vctl crashed %d times in a minute", len(out)), Source: "reporter"})
	}
	return out
}

var (
	reDmesgLine  = regexp.MustCompile(`^\[\s*(\d+\.\d+)\]\s?(.*)$`)
	reOOMKill    = regexp.MustCompile(`Out of memory: Killed process (\d+) \(([^)]+)\)`)
	reAnonRSS    = regexp.MustCompile(`anon-rss:(\d+)kB`)
	reOrder      = regexp.MustCompile(`order:(\d+)`)
	kernelFaults = []struct {
		re  *regexp.Regexp
		key string
	}{
		{regexp.MustCompile(`page allocation failure`), "page allocation failure"},
		{regexp.MustCompile(`Internal error: Oops|\bOops\b`), "oops"},
		{regexp.MustCompile(`\bBUG: `), "BUG"},
		{regexp.MustCompile(`soft lockup`), "soft lockup"},
		{regexp.MustCompile(`blocked for more than \d+ seconds`), "hung task"},
		{regexp.MustCompile(`Kernel panic`), "kernel panic"},
	}
)

// Dmesg reads the kernel's lines after the cursor (seconds since boot):
// OOM kills and kernel faults, each fault with up to 14 lines after it (its
// call trace). last is the newest line's time, the next cursor.
func Dmesg(text string, after float64) (found []incident.Incident, last float64) {
	last = after
	type line struct {
		at   float64
		text string
	}
	var lines []line
	for _, l := range strings.Split(text, "\n") {
		m := reDmesgLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		at, err := strconv.ParseFloat(m[1], 64)
		if err != nil || at <= after {
			continue
		}
		lines = append(lines, line{at, m[2]})
		if at > last {
			last = at
		}
	}
	for i, l := range lines {
		if m := reOOMKill.FindStringSubmatch(l.text); m != nil {
			details := map[string]any{"victim": m[2]}
			if r := reAnonRSS.FindStringSubmatch(l.text); r != nil {
				kb, _ := strconv.Atoi(r[1])
				details["anonRssKB"] = kb
			}
			found = append(found, incident.Incident{Code: "OOM_KILL", Key: "oom " + m[2],
				Title: "the kernel killed " + m[2] + " for want of memory", Source: "reporter",
				Details: details, Kernel: []string{l.text}})
			continue
		}
		for _, f := range kernelFaults {
			if !f.re.MatchString(l.text) {
				continue
			}
			key := f.key
			if o := reOrder.FindStringSubmatch(l.text); o != nil && f.key == "page allocation failure" {
				key += " order:" + o[1]
			}
			var kernel []string
			for j := i; j < len(lines) && j < i+15; j++ {
				kernel = append(kernel, lines[j].text)
			}
			found = append(found, incident.Incident{Code: "KERNEL_ERROR", Key: key,
				Title: "kernel: " + capRunes(l.text, 200), Source: "reporter", Kernel: kernel})
			break
		}
	}
	return found, last
}

// DataplaneWithoutXray says once, at the second minute in a row, that vctl's
// data plane is loaded while no xray runs: the LAN has no way out.
func DataplaneWithoutXray(tableLoaded, xrayRunning bool, st *State) *incident.Incident {
	if !tableLoaded || xrayRunning {
		st.XrayMissing = 0
		return nil
	}
	st.XrayMissing++
	if st.XrayMissing != 2 {
		return nil
	}
	return &incident.Incident{Code: "DATAPLANE_WITHOUT_XRAY", Key: "dataplane without xray",
		Title: "vctl's data plane is loaded and xray is not running: the LAN has no way out", Source: "reporter"}
}

// A pstore dmesg record's header says why the kernel wrote it ("Panic#1
// Part1"); a kernel told to dump at every shutdown too (ramoops.max_reason,
// printk.always_kmsg_dump) writes one on a clean reboot as well.
var reCleanDump = regexp.MustCompile(`^(Shutdown|Restart|Halt|Poweroff)#`)

// Boot is the reporter's look at a boot: the router went down cleanly (the
// shutdown marker, which it takes away) or not — then an UNEXPECTED_REBOOT,
// with whatever the kernel kept of its end (pstore's dmesg records of a
// panic, an oops or an emergency, taken away as they are read). The console
// and pmsg records are not a failure: a kernel that keeps its console there
// leaves one after every boot.
func Boot(marker, pstoreDir string, now time.Time) *incident.Incident {
	_, err := os.Stat(marker)
	clean := err == nil
	_ = os.Remove(marker)
	var kept []string
	files, _ := filepath.Glob(filepath.Join(pstoreDir, "dmesg-*"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		_ = os.Remove(f)
		if err != nil || reCleanDump.Match(b) {
			continue
		}
		kept = append(kept, tail(strings.Split(strings.TrimRight(string(b), "\n"), "\n"), MaxKernel)...)
	}
	kept = tail(kept, MaxKernel)
	if clean && len(kept) == 0 {
		return nil
	}
	in := &incident.Incident{Code: "UNEXPECTED_REBOOT", Key: "unexpected reboot",
		Title: "the router rebooted without going down cleanly", At: now.UTC(), Source: "reporter"}
	if len(kept) > 0 {
		in.Key, in.Title, in.Kernel = "reboot after a kernel failure", "the router rebooted after a kernel failure", kept
	}
	return in
}
