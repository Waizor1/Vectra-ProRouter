// Package supervisor owns the Xray process lifecycle: start, stop, reload,
// crash-restart with exponential backoff (and stable-uptime reset), plus
// per-process resource readings (RSS, CPU, FD count) and self-protection
// (oom_score_adj, rlimits, soft memory cap).
//
// The package is OS-portable: Linux-specific syscalls (oom_score_adj,
// rlimit) are no-ops on non-Linux so the package can be exercised locally
// on macOS during development.
package supervisor

import "time"

// State describes the supervised process's current phase.
type State string

const (
	StateIdle       State = "idle"
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateExited     State = "exited"
	StateBackoff    State = "backoff"
	StateStopped    State = "stopped"
	StateFailed     State = "failed"
	StateReloading  State = "reloading"
)

// Status is the snapshot a Supervisor publishes (atomic, copyable).
type Status struct {
	State           State     `json:"state"`
	PID             int       `json:"pid,omitempty"`
	StartedAt       time.Time `json:"startedAt,omitempty"`
	LastExitAt      time.Time `json:"lastExitAt,omitempty"`
	LastExitCode    int       `json:"lastExitCode,omitempty"`
	LastExitErr     string    `json:"lastExitErr,omitempty"`
	RestartCount    int       `json:"restartCount"`
	BackoffNextMs   int64     `json:"backoffNextMs,omitempty"`
	StableUptimeMS  int64     `json:"stableUptimeMs"`
	LastReloadAt    time.Time `json:"lastReloadAt,omitempty"`
	ResourceSnapshot ResourceSnapshot `json:"resources"`
}

// ResourceSnapshot is the latest /proc reading for the supervised process.
type ResourceSnapshot struct {
	SampledAt time.Time `json:"sampledAt"`
	RSSBytes  uint64    `json:"rssBytes"`
	VmSizeBytes uint64  `json:"vmSizeBytes"`
	UserJiffies uint64  `json:"userJiffies"`
	SystemJiffies uint64 `json:"systemJiffies"`
	ThreadCount int     `json:"threadCount"`
	FDCount     int     `json:"fdCount"`
}
