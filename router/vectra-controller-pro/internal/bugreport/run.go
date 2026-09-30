package bugreport

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"time"

	"vectra-controller-pro/internal/incident"
)

// Spool caps and the time a report may wait at most.
const (
	SpoolFiles = 32
	SpoolBytes = 512 << 10
	MaxAge     = 7 * 24 * time.Hour
	Window     = 6 * time.Hour
)

// Env is one run's world; on a router cmd/vectra-reporter fills it.
type Env struct {
	Root, Reporter                          string
	SpoolDir, StatePath, InboxDir, CrashDir string
	Now                                     func() time.Time
	Alive                                   func(pid int) bool
	Dmesg                                   func() string
	BootID                                  func() string
	TableLoaded, XrayRunning                func() bool
	Facts                                   func() (Router, []string)
	Sender                                  Sender
	Budget                                  time.Duration
}

func (e Env) spool() Spool { return Spool{Dir: e.SpoolDir, MaxFiles: SpoolFiles, MaxBytes: SpoolBytes} }

// Run is one minute's work: what went wrong since the last run into the
// spool, and what is due out of it. A failure to keep a report is counted in
// the state, never the run's end.
func Run(ctx context.Context, e Env) (queued, sent int, err error) {
	ctx, cancel := context.WithTimeout(ctx, e.Budget)
	defer cancel()
	st := LoadState(e.StatePath)
	// Flash is written when the state changed, and at the first run — not
	// every minute.
	before, _ := json.Marshal(st)
	_, noState := os.Stat(e.StatePath)
	now := e.Now()
	// Before NTP the clock can be years behind: nothing is sent, and nothing
	// is judged old by it.
	clockOK := !now.Before(clockSet)

	var found []incident.Incident
	found = append(found, Crashes(e.CrashDir, e.Alive)...)
	pending := incident.Read(e.InboxDir)
	for _, p := range pending {
		if p.Code != "" {
			found = append(found, p.Incident)
		}
	}
	if id := e.BootID(); id != st.BootID {
		st.BootID, st.DmesgAt = id, 0
	}
	var kernel []incident.Incident
	kernel, st.DmesgAt = Dmesg(e.Dmesg(), st.DmesgAt)
	found = append(found, kernel...)
	if in := DataplaneWithoutXray(e.TableLoaded(), e.XrayRunning(), &st); in != nil {
		found = append(found, *in)
	}
	if len(found) > 0 {
		r, log := e.Facts()
		for _, in := range found {
			if Queue(e, &st, in, r, log) == nil {
				queued++
			}
		}
	}
	for _, p := range pending {
		_ = os.Remove(p.Path)
	}

	for _, en := range e.spool().Entries() {
		if ctx.Err() != nil {
			break
		}
		if !clockOK {
			continue
		}
		if en.Meta.QueuedAt.Before(clockSet) {
			// Queued before the clock was set (a boot before NTP): its times
			// start now, and its week with them.
			en.Meta.QueuedAt, en.Meta.NextAt = now, time.Time{}
			if en.Report.FirstAt.Before(clockSet) {
				en.Report.FirstAt = now
			}
			if en.Report.LastAt.Before(clockSet) {
				en.Report.LastAt = now
			}
			_ = e.spool().Save(en)
		}
		if now.Sub(en.Meta.QueuedAt) > MaxAge {
			_ = e.spool().Remove(en.Report.ReportID)
			st.Dropped++
			continue
		}
		if now.Before(en.Meta.NextAt) || !st.Allow(now) {
			continue
		}
		o := e.Sender.Send(ctx, en)
		if o.Done {
			_ = e.spool().Remove(en.Report.ReportID)
			if o.Err == nil {
				sent++
				if st.Sent == nil {
					st.Sent = map[string]time.Time{}
				}
				st.Sent[en.Report.Fingerprint] = now
			} else {
				st.Dropped++
				st.LastError = o.Err.Error()
			}
			continue
		}
		en.Meta.Attempts++
		en.Meta.NextAt = now.Add(o.Retry)
		if o.Err != nil {
			en.Meta.LastStatus = o.Err.Error()
			st.LastError = o.Err.Error()
		}
		_ = e.spool().Save(en)
	}
	for f, at := range st.Sent {
		if now.Sub(at) > Window {
			delete(st.Sent, f)
		}
	}
	if after, _ := json.Marshal(st); noState == nil && bytes.Equal(before, after) {
		return queued, sent, nil
	}
	return queued, sent, st.Save(e.StatePath)
}

// Queue makes a report of in and spools it: held to the end of the window
// when one of its fingerprint went out within it.
func Queue(e Env, st *State, in incident.Incident, r Router, log []string) error {
	now := e.Now()
	rep := FromIncident(in, r, now)
	if len(rep.Evidence.Log) == 0 {
		rep.Evidence.Log = log
	}
	var notBefore time.Time
	if at, ok := st.Sent[rep.Fingerprint]; ok && now.Sub(at) < Window {
		notBefore = at.Add(Window)
	}
	_, evicted, err := e.spool().Add(rep, now, notBefore)
	st.Dropped += evicted
	if err != nil {
		st.Dropped++
		st.LastError = err.Error()
	}
	return err
}
