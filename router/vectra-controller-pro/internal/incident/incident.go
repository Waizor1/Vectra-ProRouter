// Package incident is how vctl and its shell — the dead-man, the init
// script — tell the reporter (vectra-reporter, ADR-0007) that something went
// other than meant: one small JSON file per incident in the inbox, a tmpfs
// directory the reporter empties every minute. Nothing here sends anything:
// without the reporter the files stay, never more than MaxQueued, until a
// reboot clears tmpfs.
package incident

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Incident is one thing that went other than meant. Key is what makes two
// alike — the reporter's fingerprint is made of Code and Key — so it holds
// no times, counts or addresses.
type Incident struct {
	Code     string         `json:"code"`
	Severity string         `json:"severity,omitempty"`
	Key      string         `json:"key"`
	Title    string         `json:"title"`
	At       time.Time      `json:"at"`
	Source   string         `json:"source"`
	Details  map[string]any `json:"details,omitempty"`
	Log      []string       `json:"log,omitempty"`
	Stack    string         `json:"stack,omitempty"`
	Kernel   []string       `json:"kernel,omitempty"`
}

// Dir is the inbox; CrashDir holds vctl's crash output (vctl.<pid>).
var (
	Dir      = "/var/run/vectra-reporter/inbox"
	CrashDir = "/var/run/vectra-reporter/crash"
)

// MaxQueued caps the inbox: a producer never writes past it.
const MaxQueued = 32

// Recorder writes incidents into its directory, one per code in every
// interval at most.
type Recorder struct {
	dir   string
	every time.Duration
	now   func() time.Time
	mu    sync.Mutex
	last  map[string]time.Time
}

// NewRecorder writes into dir, at most one incident of a code per every.
func NewRecorder(dir string, every time.Duration) *Recorder {
	return &Recorder{dir: dir, every: every, now: time.Now, last: map[string]time.Time{}}
}

// Record writes in, and says whether it did: not within the interval of the
// same code, not into a full inbox, not when the write fails.
func (r *Recorder) Record(in Incident) bool {
	if r == nil || in.Code == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if last, ok := r.last[in.Code]; ok && now.Sub(last) < r.every {
		return false
	}
	if in.At.IsZero() {
		in.At = now.UTC()
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return false
	}
	if queued, _ := filepath.Glob(filepath.Join(r.dir, "*.json")); len(queued) >= MaxQueued {
		return false
	}
	b, err := json.Marshal(in)
	if err != nil {
		return false
	}
	name := filepath.Join(r.dir, fmt.Sprintf("%d-%s-%d.json", now.UnixNano(), in.Code, os.Getpid()))
	tmp := filepath.Join(r.dir, "."+filepath.Base(name)+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		_ = os.Remove(tmp)
		return false
	}
	if err := os.Rename(tmp, name); err != nil {
		_ = os.Remove(tmp)
		return false
	}
	r.last[in.Code] = now
	return true
}

// Pending is an incident in the inbox and its file.
type Pending struct {
	Path string
	Incident
}

// Read lists the inbox, oldest first. A file that is not an incident comes
// back with no Code: the reporter takes it away.
func Read(dir string) []Pending {
	names, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	out := make([]Pending, 0, len(names))
	for _, n := range names {
		p := Pending{Path: n}
		if b, err := os.ReadFile(n); err == nil {
			if json.Unmarshal(b, &p.Incident) != nil {
				p.Incident = Incident{}
			}
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
