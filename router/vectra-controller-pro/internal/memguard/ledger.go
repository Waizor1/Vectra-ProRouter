package memguard

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The ledger: what the router's memory did over the last day.
//
// A leak on a router is a floor that rises: what a process holds at its
// quietest keeps going up, day and night, where load only adds spikes on top
// (xray's ~38 KB a connection comes and, mostly, goes). So the ledger keeps,
// for every series, the lowest value of each 10-minute bucket — a floor curve
// that load does not move — for the last 24 hours, and says which floors rose.
// MemAvailable is the other way round: its ceiling, the highest of each
// bucket, falling is the router losing memory to something.
//
// Samples come once a minute (TakeSnapshot); a series is a process name's
// anonymous memory (summed over its processes: two dnsmasq instances are one
// dnsmasq), the kernel's unreclaimable slab, tmpfs (Shmem) and swap in use.
// All of it is a few tens of KB.
//
// Load moves a floor too: xray's at its quietest in the evening is the
// evening's connections, and the router reboots at 04:30, into its quietest
// hours. So each series keeps the load it goes with beside it — a process's
// open file descriptors (xray's connections are two each), the kernel's
// tracked connections for the router's own figures — and a floor counts as
// risen only where the load has not risen with it. What rises at the load it
// started at is a leak, or a cache that fills: worth saying either way.

// Snapshot is the router's memory at one moment, in kB.
type Snapshot struct {
	At           time.Time
	AvailableKB  uint64
	SUnreclaimKB uint64
	ShmemKB      uint64
	SwapUsedKB   uint64
	// Procs is each process name's anonymous memory (RssAnon, summed).
	Procs map[string]uint64
	// Load is what each series goes with: a process name's open file
	// descriptors (summed), the tracked connections for MemAvailable, the
	// kernel slab and swap. Not tmpfs: files fill it, whatever the load.
	Load map[string]uint64
}

// Series names outside the processes.
const (
	SeriesAvailable = "MemAvailable"
	SeriesSlab      = "kernel slab (unreclaimable)"
	SeriesTmpfs     = "tmpfs"
	SeriesSwap      = "swap in use"
)

// minProcKB: a process holding less than this is not a series of its own
// (a shell, a sleep): only what could matter to a 234 MB router is kept.
const minProcKB = 256

// TakeSnapshot reads meminfo and every process's status under procDir
// ("/proc" on a router).
func TakeSnapshot(procDir string, now time.Time) (Snapshot, error) {
	s := Snapshot{At: now, Procs: map[string]uint64{}, Load: map[string]uint64{}}
	b, err := os.ReadFile(filepath.Join(procDir, "meminfo"))
	if err != nil {
		return s, err
	}
	var swapTotal, swapFree uint64
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
		case "MemAvailable:":
			s.AvailableKB = v
		case "SUnreclaim:":
			s.SUnreclaimKB = v
		case "Shmem:":
			s.ShmemKB = v
		case "SwapTotal:":
			swapTotal = v
		case "SwapFree:":
			swapFree = v
		}
	}
	if swapTotal > swapFree {
		s.SwapUsedKB = swapTotal - swapFree
	}
	if b, err := os.ReadFile(filepath.Join(procDir, "sys/net/netfilter/nf_conntrack_count")); err == nil {
		if n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil {
			for _, k := range []string{SeriesAvailable, SeriesSlab, SeriesSwap} {
				s.Load[k] = n
			}
		}
	}
	dirs, _ := filepath.Glob(filepath.Join(procDir, "[0-9]*"))
	of := map[string][]string{}
	for _, d := range dirs {
		name, anon, ok := procAnon(filepath.Join(d, "status"))
		if ok && anon > 0 {
			s.Procs[name] += anon
			of[name] = append(of[name], d)
		}
	}
	for n, kb := range s.Procs {
		if kb < minProcKB {
			delete(s.Procs, n)
			continue
		}
		var fds uint64
		seen := false
		for _, d := range of[n] {
			if c, ok := countFDs(filepath.Join(d, "fd")); ok {
				fds += c
				seen = true
			}
		}
		if seen {
			s.Load[n] = fds
		}
	}
	return s, nil
}

// countFDs is how many descriptors a process has open.
func countFDs(dir string) (uint64, bool) {
	f, err := os.Open(dir)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	var n uint64
	for {
		names, err := f.Readdirnames(256)
		n += uint64(len(names))
		if err == io.EOF {
			return n, true
		}
		if err != nil {
			return 0, false // gone mid-listing: no count, not a low one
		}
	}
}

// procAnon is a process's name and RssAnon (kB) from its status file.
func procAnon(path string) (string, uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", 0, false
	}
	var name string
	var anon uint64
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "Name:"); ok {
			name = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "RssAnon:"); ok {
			f := strings.Fields(v)
			if len(f) > 0 {
				anon, _ = strconv.ParseUint(f[0], 10, 64)
			}
		}
	}
	return name, anon, name != ""
}

// LedgerBucket and LedgerKeep: 10-minute buckets, 24 hours of them.
const (
	LedgerBucket = 10 * time.Minute
	LedgerKeep   = 144
)

type bucket struct {
	start time.Time
	v     uint64
}

// Ledger keeps the floors (and MemAvailable's ceiling). Safe for concurrent
// use: the daemon samples in one goroutine and reports from another.
type Ledger struct {
	mu     sync.Mutex
	series map[string][]bucket
	loads  map[string][]bucket // each series' load: its floor, by bucket
	last   Snapshot
}

// NewLedger is an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{series: map[string][]bucket{}, loads: map[string][]bucket{}}
}

// Add records a snapshot.
func (l *Ledger) Add(s Snapshot) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.last = s
	start := s.At.Truncate(LedgerBucket)
	put(l.series, SeriesAvailable, start, s.AvailableKB, true)
	put(l.series, SeriesSlab, start, s.SUnreclaimKB, false)
	put(l.series, SeriesTmpfs, start, s.ShmemKB, false)
	put(l.series, SeriesSwap, start, s.SwapUsedKB, false)
	for n, kb := range s.Procs {
		put(l.series, n, start, kb, false)
	}
	for n, v := range s.Load {
		put(l.loads, n, start, v, false)
	}
	// A process gone for two hours is no longer a series.
	for _, m := range []map[string][]bucket{l.series, l.loads} {
		for n, bs := range m {
			if len(bs) > 0 && start.Sub(bs[len(bs)-1].start) > 2*time.Hour {
				delete(m, n)
			}
		}
	}
}

// put folds v into name's bucket in m: the lowest value, or the highest for
// a ceiling (MemAvailable).
func put(m map[string][]bucket, name string, start time.Time, v uint64, ceiling bool) {
	bs := m[name]
	if n := len(bs); n > 0 && bs[n-1].start.Equal(start) {
		if (ceiling && v > bs[n-1].v) || (!ceiling && v < bs[n-1].v) {
			bs[n-1].v = v
		}
		m[name] = bs
		return
	}
	bs = append(bs, bucket{start, v})
	if len(bs) > LedgerKeep {
		bs = append(bs[:0:0], bs[len(bs)-LedgerKeep:]...)
	}
	m[name] = bs
}

// loadSlack: a load counts as risen above a quarter more and this much.
const loadSlack = 16

// loadFloors is the lowest load of name in [a0, a1] and in [b0, b1]; ok when
// both hold a bucket.
func (l *Ledger) loadFloors(name string, a0, a1, b0, b1 time.Time) (from, to uint64, ok bool) {
	var okA, okB bool
	for _, b := range l.loads[name] {
		if !b.start.Before(a0) && !b.start.After(a1) && (!okA || b.v < from) {
			from, okA = b.v, true
		}
		if !b.start.Before(b0) && !b.start.After(b1) && (!okB || b.v < to) {
			to, okB = b.v, true
		}
	}
	return from, to, okA && okB
}

// Growth is a series whose floor rose (or, for MemAvailable, whose ceiling
// fell) over the ledger's window.
type Growth struct {
	Series string
	FromKB uint64 // the lowest (MemAvailable: highest) of the first hour
	ToKB   uint64 // ... of the last hour
	Over   time.Duration
	// The load's floor in the first and the last hour, when it is known.
	LoadFrom, LoadTo uint64
	LoadKnown        bool
}

// Growing lists the series whose floor rose by at least minKB and by minFrac
// of where it started, between the first and the last hour of at least span
// of samples, at a load that did not rise with it — worst first.
// MemAvailable counts when its ceiling fell by minKB.
func (l *Ledger) Growing(minKB uint64, minFrac float64, span time.Duration) []Growth {
	l.mu.Lock()
	defer l.mu.Unlock()
	const hour = int(time.Hour / LedgerBucket)
	var out []Growth
	for name, bs := range l.series {
		if len(bs) < 2*hour {
			continue
		}
		over := bs[len(bs)-1].start.Sub(bs[0].start)
		if over < span {
			continue
		}
		ceiling := name == SeriesAvailable
		pick := func(part []bucket) uint64 {
			v := part[0].v
			for _, b := range part[1:] {
				if (ceiling && b.v > v) || (!ceiling && b.v < v) {
					v = b.v
				}
			}
			return v
		}
		from, to := pick(bs[:hour]), pick(bs[len(bs)-hour:])
		var delta uint64
		switch {
		case ceiling && from > to:
			delta = from - to
		case !ceiling && to > from:
			delta = to - from
		default:
			continue
		}
		// The fraction is of what a process started from: a floor drifting
		// by a MB on a large process is not a leak. What the router has left
		// is judged by the amount alone.
		if delta < minKB || (!ceiling && float64(delta) < minFrac*float64(from)) {
			continue
		}
		g := Growth{Series: name, FromKB: from, ToKB: to, Over: over}
		if lf, lt, ok := l.loadFloors(name, bs[0].start, bs[hour-1].start, bs[len(bs)-hour].start, bs[len(bs)-1].start); ok {
			if lt > lf+lf/4+loadSlack {
				continue // it grew with the load
			}
			g.LoadFrom, g.LoadTo, g.LoadKnown = lf, lt, true
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		di := int64(out[i].ToKB) - int64(out[i].FromKB)
		dj := int64(out[j].ToKB) - int64(out[j].FromKB)
		if di < 0 {
			di = -di
		}
		if dj < 0 {
			dj = -dj
		}
		return di > dj
	})
	return out
}

// Consumer is one process name and what it holds now.
type Consumer struct {
	Name   string
	AnonKB uint64
}

// Top is the n process names holding the most anonymous memory in the last
// snapshot, and that snapshot's time.
func (l *Ledger) Top(n int) ([]Consumer, Snapshot) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Consumer, 0, len(l.last.Procs))
	for name, kb := range l.last.Procs {
		out = append(out, Consumer{name, kb})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AnonKB != out[j].AnonKB {
			return out[i].AnonKB > out[j].AnonKB
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out, l.last
}
