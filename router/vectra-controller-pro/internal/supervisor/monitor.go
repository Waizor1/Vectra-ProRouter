package supervisor

import (
	"context"
	"sync"
	"time"

	"vectra-controller-pro/internal/logging"
)

// Monitor periodically reads /proc for the supervised process, updates the
// Status snapshot, optionally writes it to a JSON status file, and enforces
// soft memory cap by triggering a reload (NOT a kill) when RSS goes high.
type Monitor struct {
	Process     *Process
	StatusPath  string
	Interval    time.Duration
	// MemSoftMiB triggers a reload when child RSS exceeds this. 0 disables.
	MemSoftMiB  int
	// MemSoftGrace prevents reload thrashing — at most one reload per grace window.
	MemSoftGrace time.Duration

	mu         sync.Mutex
	lastReload time.Time
}

// Run blocks until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	if m.Interval == 0 {
		m.Interval = 5 * time.Second
	}
	if m.MemSoftGrace == 0 {
		m.MemSoftGrace = 60 * time.Second
	}
	t := time.NewTicker(m.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.tick(ctx)
		}
	}
}

func (m *Monitor) tick(ctx context.Context) {
	status := m.Process.Status()
	pid := status.PID
	snap, _ := ReadProcSnapshot(pid)
	status.ResourceSnapshot = snap
	if !status.StartedAt.IsZero() && status.State == StateRunning {
		status.StableUptimeMS = int64(time.Since(status.StartedAt) / time.Millisecond)
	}
	m.Process.status.Store(&status)

	if m.StatusPath != "" {
		if err := WriteStatus(m.StatusPath, status); err != nil {
			logging.L().Warn("status write failed", "err", err.Error())
		}
	}

	// Soft cap check.
	if m.MemSoftMiB > 0 && snap.RSSBytes > uint64(m.MemSoftMiB)*1024*1024 {
		m.mu.Lock()
		within := time.Since(m.lastReload) < m.MemSoftGrace
		if !within {
			m.lastReload = time.Now()
		}
		m.mu.Unlock()
		if within {
			return // within grace window — don't thrash
		}
		logging.L().Warn("xray RSS over soft cap; reloading",
			"rss_mib", snap.RSSBytes/1024/1024,
			"cap_mib", m.MemSoftMiB,
		)
		if err := m.Process.Reload(ctx); err != nil {
			logging.L().Warn("reload failed", "err", err.Error())
		}
	}
}
