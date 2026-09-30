package bugreport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Meta is what the spool knows of a report's way out.
type Meta struct {
	QueuedAt   time.Time `json:"queuedAt"`
	NextAt     time.Time `json:"nextAt,omitempty"`
	Attempts   int       `json:"attempts,omitempty"`
	LastStatus string    `json:"lastStatus,omitempty"`
}

// Entry is a report waiting to go.
type Entry struct {
	Report Report `json:"report"`
	Meta   Meta   `json:"meta"`
}

// Spool keeps reports on flash until they are sent: one file each, at most
// MaxFiles and MaxBytes; when over, the least severe go first, oldest first.
type Spool struct {
	Dir      string
	MaxFiles int
	MaxBytes int64
}

func (s Spool) path(id string) string { return filepath.Join(s.Dir, id+".json") }

// Entries lists what waits, oldest queued first. A file that is no entry is
// taken away.
func (s Spool) Entries() []Entry {
	names, _ := filepath.Glob(filepath.Join(s.Dir, "*.json"))
	out := make([]Entry, 0, len(names))
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			continue
		}
		var e Entry
		if json.Unmarshal(b, &e) != nil || e.Report.ReportID == "" || filepath.Base(n) != e.Report.ReportID+".json" {
			_ = os.Remove(n)
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Meta.QueuedAt.Before(out[j].Meta.QueuedAt) })
	return out
}

// Add queues r — or, when an unsent report of its fingerprint waits already,
// counts it there. notBefore holds a new one back: the window after one of
// the same fingerprint was sent.
func (s Spool) Add(r Report, now, notBefore time.Time) (merged bool, evicted int, err error) {
	for _, e := range s.Entries() {
		if e.Report.Fingerprint != r.Fingerprint {
			continue
		}
		e.Report.Count += r.Count
		if r.LastAt.After(e.Report.LastAt) {
			e.Report.LastAt = r.LastAt
		}
		return true, 0, s.Save(e)
	}
	if err := s.Save(Entry{Report: r, Meta: Meta{QueuedAt: now, NextAt: notBefore}}); err != nil {
		return false, 0, err
	}
	return false, s.evict(), nil
}

// Save writes e whole or not at all.
func (s Spool) Save(e Entry) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	p := s.path(e.Report.ReportID)
	tmp := filepath.Join(s.Dir, "."+e.Report.ReportID+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Remove takes a report out of the spool.
func (s Spool) Remove(id string) error {
	if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s Spool) evict() int {
	es := s.Entries()
	size := func() (n int64) {
		for _, e := range es {
			if st, err := os.Stat(s.path(e.Report.ReportID)); err == nil {
				n += st.Size()
			}
		}
		return n
	}
	gone := 0
	for len(es) > 0 && (len(es) > s.MaxFiles || size() > s.MaxBytes) {
		victim := 0
		for i, e := range es {
			if SeverityRank(e.Report.Severity) < SeverityRank(es[victim].Report.Severity) {
				victim = i
			}
		}
		_ = s.Remove(es[victim].Report.ReportID)
		es = append(es[:victim], es[victim+1:]...)
		gone++
	}
	return gone
}
