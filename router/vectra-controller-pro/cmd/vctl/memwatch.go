package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/supervisor"
)

// The memory watchdog: vctl's answer to the router running out of memory,
// before the kernel's.
//
// When MemAvailable stays below memguard.CriticalKB the kernel is already
// reclaiming the pages of running programs — the router slows to a crawl for
// minutes before the OOM killer acts, and then it takes whatever scores
// highest. If xray is what holds the memory (every connection it carries is
// ~38 KB, and Go keeps what it freed), vctl restarts it: a few seconds without
// the proxy, every byte it held given back, Wi-Fi, DNS and the direct routes
// untouched. At most once per memReliefGap — and a restart that did not give
// the memory back (a minute later the router is still short: something else
// holds it) doubles the wait before the next one, up to memReliefMaxGap, so a
// router short of memory for another reason is not left restarting xray. It
// also reports the kernel's own OOM kills, where the kernel counts them.

const (
	memWatchEvery   = 5 * time.Second
	memLowSamples   = 3 // 15 s below critical
	memReliefGap    = 10 * time.Minute
	memReliefMaxGap = 4 * time.Hour
	memReliefJudge  = time.Minute
)

// xraySup is what the watchdog needs of the supervisor.
type xraySup interface {
	Status() supervisor.Status
	Reload(ctx context.Context) error
}

type memWatch struct {
	// onGrowth hears of a series the review says keeps growing.
	onGrowth func(series string, fromMiB, toMiB uint64)

	read  func() (memguard.Info, error)
	rss   func(pid int) (anonKB, fileKB uint64, err error)
	kills func() (uint64, bool)
	sup   xraySup

	// The ledger (memguard/ledger.go): a snapshot once a minute, reviewed
	// once an hour for floors that rise — leaks — which are logged and
	// written to reportPath for a person or a terminal job to read.
	ledger     *memguard.Ledger
	snapshot   func(now time.Time) (memguard.Snapshot, error)
	reportPath string
	lastSample time.Time
	lastReview time.Time
	reported   map[string]uint64

	low        int
	lastRelief time.Time
	killsSeen  uint64
	killsKnown bool
	// gap is the wait between restarts now (memReliefGap, doubled by each
	// that did not help); judging: the last one is still to be judged.
	gap     time.Duration
	judging bool
}

// The ledger's pace, and what counts as a leak: a floor up by 8 MiB and by a
// quarter over at least 6 hours of samples (MemAvailable: its ceiling down by
// 8 MiB). Reported again only when it has grown 4 MiB more.
const (
	ledgerEvery    = time.Minute
	ledgerReview   = time.Hour
	leakMinKB      = 8 * 1024
	leakMinFrac    = 0.25
	leakSpan       = 6 * time.Hour
	leakReportStep = 4 * 1024
)

// memoryReportPath is the review's report: tmpfs, a few KB.
var memoryReportPath = "/var/run/vectra-controller-pro/memory.json"

// watchMemory runs the watchdog until ctx ends. Its own goroutine: it reads
// only what is safe to share (the supervisor, the config).
func (d *daemon) watchMemory(ctx context.Context) {
	w := &memWatch{read: memguard.Read, rss: memguard.ReadRSS, kills: memguard.OOMKills, sup: d.sup,
		ledger:     memguard.NewLedger(),
		snapshot:   func(now time.Time) (memguard.Snapshot, error) { return memguard.TakeSnapshot("/proc", now) },
		reportPath: memoryReportPath,
		onGrowth: func(s string, from, to uint64) {
			d.incident("MEMORY_GROWTH", s, fmt.Sprintf("memory keeps growing: %s %d → %d MiB", s, from, to), nil)
		},
	}
	if _, err := w.read(); err != nil {
		return // no /proc/meminfo: nothing to watch
	}
	t := time.NewTicker(memWatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			w.step(ctx, now)
		}
	}
}

// step is one look at the router's memory; true when it restarted xray.
func (w *memWatch) step(ctx context.Context, now time.Time) bool {
	in, err := w.read()
	if err != nil {
		return false
	}
	w.account(now)
	if n, ok := w.kills(); ok {
		if w.killsKnown && n > w.killsSeen {
			logging.L().Warn("the kernel ran out of memory and killed a process",
				"kills", n-w.killsSeen, "available_mib", memguard.MiB(in.AvailableKB))
		}
		w.killsSeen, w.killsKnown = n, true
	}
	critical := in.AvailableKB < memguard.CriticalKB(in.TotalKB)
	if critical {
		w.low++
	} else {
		w.low = 0
	}
	if w.gap == 0 {
		w.gap = memReliefGap
	}
	if w.judging && now.Sub(w.lastRelief) >= memReliefJudge {
		w.judging = false
		if in.AvailableKB >= 2*memguard.CriticalKB(in.TotalKB) {
			w.gap = memReliefGap
		} else {
			w.gap *= 2
			if w.gap > memReliefMaxGap {
				w.gap = memReliefMaxGap
			}
			logging.L().Warn("restarting xray did not give the router its memory back: something else holds it; the next restart for memory waits longer",
				"available_mib", memguard.MiB(in.AvailableKB), "wait", w.gap.String())
		}
	}
	if !critical || w.low < memLowSamples || (!w.lastRelief.IsZero() && now.Sub(w.lastRelief) < w.gap) {
		return false
	}
	st := w.sup.Status()
	if st.State != supervisor.StateRunning || st.PID <= 0 {
		return false
	}
	anon, _, err := w.rss(st.PID)
	if err != nil || anon < memguard.XrayReliefKB(in.TotalKB) {
		return false
	}
	logging.L().Warn("the router is running out of memory: restarting xray to give back what it holds",
		"available_mib", memguard.MiB(in.AvailableKB), "xray_mib", memguard.MiB(anon))
	if err := w.sup.Reload(ctx); err != nil {
		logging.L().Warn("could not restart xray for memory", "err", err.Error())
		return false
	}
	w.lastRelief, w.low, w.judging = now, 0, true
	return true
}

// account feeds the ledger and, once an hour, reviews it.
func (w *memWatch) account(now time.Time) {
	if w.ledger == nil || w.snapshot == nil {
		return
	}
	if now.Sub(w.lastSample) >= ledgerEvery {
		w.lastSample = now
		if snap, err := w.snapshot(now); err == nil {
			w.ledger.Add(snap)
		}
	}
	if w.lastReview.IsZero() {
		w.lastReview = now // the first review an hour in: nothing to judge before
		return
	}
	if now.Sub(w.lastReview) < ledgerReview {
		return
	}
	w.lastReview = now
	w.review(now)
}

// memoryReport is what the review writes: what holds the memory now and what
// keeps growing.
type memoryReport struct {
	At           time.Time         `json:"at"`
	AvailableMiB uint64            `json:"availableMiB"`
	SlabMiB      uint64            `json:"kernelSlabMiB"`
	TmpfsMiB     uint64            `json:"tmpfsMiB"`
	SwapUsedMiB  uint64            `json:"swapUsedMiB"`
	TopMiB       map[string]uint64 `json:"topAnonMiB"`
	Growing      []memoryGrowth    `json:"growing"`
}

type memoryGrowth struct {
	Series  string  `json:"series"`
	FromMiB float64 `json:"fromMiB"`
	ToMiB   float64 `json:"toMiB"`
	Hours   float64 `json:"hours"`
	// The load it was judged at: open descriptors for a process, tracked
	// connections for the router's own figures.
	LoadFrom *uint64 `json:"loadFrom,omitempty"`
	LoadTo   *uint64 `json:"loadTo,omitempty"`
}

// review logs the floors that rose since last said, and writes the report.
func (w *memWatch) review(now time.Time) {
	if w.reported == nil {
		w.reported = map[string]uint64{}
	}
	top, snap := w.ledger.Top(8)
	rep := memoryReport{At: now, AvailableMiB: memguard.MiB(snap.AvailableKB), SlabMiB: memguard.MiB(snap.SUnreclaimKB),
		TmpfsMiB: memguard.MiB(snap.ShmemKB), SwapUsedMiB: memguard.MiB(snap.SwapUsedKB), TopMiB: map[string]uint64{}}
	for _, c := range top {
		rep.TopMiB[c.Name] = memguard.MiB(c.AnonKB)
	}
	for _, g := range w.ledger.Growing(leakMinKB, leakMinFrac, leakSpan) {
		delta := g.ToKB - g.FromKB
		if g.Series == memguard.SeriesAvailable {
			delta = g.FromKB - g.ToKB
		}
		mg := memoryGrowth{Series: g.Series, FromMiB: float64(g.FromKB) / 1024, ToMiB: float64(g.ToKB) / 1024, Hours: g.Over.Hours()}
		load := []any{}
		if g.LoadKnown {
			lf, lt := g.LoadFrom, g.LoadTo
			mg.LoadFrom, mg.LoadTo = &lf, &lt
			load = []any{"load_from", lf, "load_to", lt}
		}
		rep.Growing = append(rep.Growing, mg)
		if last, ok := w.reported[g.Series]; ok && delta < last+leakReportStep {
			continue
		}
		w.reported[g.Series] = delta
		if w.onGrowth != nil {
			w.onGrowth(g.Series, memguard.MiB(g.FromKB), memguard.MiB(g.ToKB))
		}
		atLoad := ""
		if g.LoadKnown {
			atLoad = ", at a load no higher"
		}
		if g.Series == memguard.SeriesAvailable {
			logging.L().Warn("the router keeps losing memory: what is available at its best has fallen"+atLoad,
				append([]any{"from_mib", memguard.MiB(g.FromKB), "to_mib", memguard.MiB(g.ToKB), "hours", int(g.Over.Hours())}, load...)...)
			continue
		}
		logging.L().Warn("memory keeps growing: what this holds at its quietest has risen"+atLoad+" (a leak, or a cache that fills)",
			append([]any{"what", g.Series, "from_mib", memguard.MiB(g.FromKB), "to_mib", memguard.MiB(g.ToKB), "hours", int(g.Over.Hours())}, load...)...)
	}
	if w.reportPath != "" {
		if b, err := json.MarshalIndent(rep, "", "  "); err == nil {
			_ = localctl.WriteFileAtomic(w.reportPath, append(b, '\n'), 0o644)
		}
	}
}
