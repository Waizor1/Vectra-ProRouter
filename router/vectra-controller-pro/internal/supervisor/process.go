package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/logging"
)

// Process supervises a single Xray instance: start, stop, reload, crash-restart.
type Process struct {
	cfg           config.Process
	binary        string
	configFile    string
	logDir        string
	memorySoftMiB int
	memoryHardMiB int
	oomScoreAdj   int
	niceLevel     int

	mu           sync.Mutex
	cmd          *exec.Cmd
	cancel       context.CancelFunc
	done         chan struct{} // closed exactly once after the active cmd's Wait returns
	status       atomic.Pointer[Status]
	backoff      *BackoffState
	startedAt    time.Time

	stopping     atomic.Bool // true after Stop has been called
	expectedExit atomic.Bool // true when a controlled restart (Reload/Stop) signalled
}

// NewProcess builds a Process from the operator's Process config block.
func NewProcess(c config.Process) *Process {
	backoff := NewBackoff(
		c.RestartBackoff.InitialMs,
		c.RestartBackoff.MaxMs,
		c.RestartBackoff.Factor,
		parseDuration(c.RestartBackoff.Reset, 60*time.Second),
	)
	p := &Process{
		cfg:           c,
		binary:        c.XrayBinary,
		configFile:    c.ConfigFile,
		logDir:        c.LogDir,
		memorySoftMiB: c.MemorySoftMiB,
		memoryHardMiB: c.MemoryHardMiB,
		oomScoreAdj:   c.OOMScoreAdj,
		niceLevel:     c.NiceLevel,
		backoff:       backoff,
	}
	p.status.Store(&Status{State: StateIdle})
	return p
}

// Status returns the latest published status snapshot.
func (p *Process) Status() Status {
	if s := p.status.Load(); s != nil {
		return *s
	}
	return Status{State: StateIdle}
}

// WriteXrayConfig atomically writes the given config bytes to ConfigFile,
// fsyncing the file and parent dir so a power-cut cannot truncate it.
func (p *Process) WriteXrayConfig(data []byte) error {
	return atomicWriteFile(p.configFile, data, 0o600)
}

// Run starts the supervised process loop. Returns when ctx is cancelled
// (orderly shutdown) or a non-restartable failure occurs.
func (p *Process) Run(ctx context.Context) error {
	log := logging.L()
	if err := p.applySelfLimits(); err != nil {
		log.Warn("apply self limits", "err", err.Error())
	}
	for {
		if ctx.Err() != nil {
			p.updateStatus(StateStopped, 0, nil)
			return nil
		}
		// Each iteration is a fresh start attempt. expectedExit is reset
		// at start time so a Reload during this run is correctly captured.
		p.expectedExit.Store(false)
		if err := p.startOnce(ctx); err != nil {
			log.Error("xray start failed", "err", err.Error(), "attempt", p.backoff.Attempt())
			p.updateStatus(StateBackoff, 0, err)
		} else {
			exitErr, runDuration := p.waitOnce()
			if p.stopping.Load() {
				p.updateStatus(StateStopped, exitCodeOf(exitErr), exitErr)
				return nil
			}
			// Intentional restart (Reload, soft-cap reload, etc.): don't
			// charge it as a crash. Reset backoff iff the previous run was
			// long enough to count as stable.
			if p.expectedExit.Load() {
				p.backoff.MaybeResetAfter(runDuration)
				log.Info("xray exited (intentional restart)", "runDuration", runDuration.String())
				continue
			}
			p.backoff.MaybeResetAfter(runDuration)
			log.Warn("xray exited; will restart",
				"exitErr", exitErr,
				"runDuration", runDuration.String(),
				"attempt", p.backoff.Attempt(),
			)
			p.updateStatus(StateBackoff, exitCodeOf(exitErr), exitErr)
		}
		next := p.backoff.Next()
		select {
		case <-ctx.Done():
			p.updateStatus(StateStopped, 0, nil)
			return nil
		case <-time.After(next):
		}
	}
}

// Stop signals the supervised process to terminate and waits up to ReloadGrace.
func (p *Process) Stop(ctx context.Context) error {
	p.stopping.Store(true)
	p.expectedExit.Store(true)
	p.mu.Lock()
	cmd := p.cmd
	cancel := p.cancel
	done := p.done
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	grace := parseDuration(p.cfg.ReloadGrace, 5*time.Second)
	select {
	case <-time.After(grace):
		if cancel != nil {
			cancel()
		}
		// SIGKILL the whole process group to catch any orphans.
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return errors.New("supervisor: grace expired, killed")
	case <-done:
		return nil
	}
}

// Reload restarts the supervised process to pick up new config. Marks the
// exit as intentional so the run loop does NOT treat it as a crash.
func (p *Process) Reload(ctx context.Context) error {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return errors.New("supervisor: not running")
	}
	p.expectedExit.Store(true)
	return cmd.Process.Signal(syscall.SIGTERM)
}

func (p *Process) startOnce(ctx context.Context) error {
	subCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(subCtx, p.binary, "run", "-c", p.configFile)
	cmd.SysProcAttr = newSysProcAttr()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if p.logDir != "" {
		if err := os.MkdirAll(p.logDir, 0o755); err == nil {
			if f, err := os.OpenFile(filepath.Join(p.logDir, "xray.log"),
				os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644); err == nil {
				cmd.Stdout = f
				cmd.Stderr = f
			}
		}
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	doneCh := make(chan struct{})
	p.mu.Lock()
	p.cmd = cmd
	p.cancel = cancel
	p.done = doneCh
	p.startedAt = time.Now()
	p.mu.Unlock()
	p.updateStatus(StateRunning, 0, nil)
	if cmd.Process != nil && p.oomScoreAdj != 0 {
		if err := applyOOMScoreAdj(cmd.Process.Pid, p.oomScoreAdj); err != nil {
			logging.L().Debug("apply oom_score_adj to child", "err", err.Error())
		}
	}
	return nil
}

// waitOnce blocks on Wait() exactly once (per active cmd). On return it
// closes the done channel so Stop/external observers know the process exited.
func (p *Process) waitOnce() (error, time.Duration) {
	p.mu.Lock()
	cmd := p.cmd
	startedAt := p.startedAt
	doneCh := p.done
	p.mu.Unlock()
	if cmd == nil {
		return errors.New("supervisor: nil cmd in waitOnce"), 0
	}
	err := cmd.Wait()
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	p.cmd = nil
	p.done = nil
	p.mu.Unlock()
	close(doneCh)
	return err, time.Since(startedAt)
}

func (p *Process) updateStatus(state State, exitCode int, exitErr error) {
	cur := p.Status()
	s := Status{
		State:            state,
		StableUptimeMS:   cur.StableUptimeMS,
		StartedAt:        cur.StartedAt,
		RestartCount:     cur.RestartCount,
		LastReloadAt:     cur.LastReloadAt,
		ResourceSnapshot: cur.ResourceSnapshot,
	}
	switch state {
	case StateRunning:
		s.StartedAt = time.Now()
		s.PID = pidOf(p)
		s.BackoffNextMs = int64(p.backoff.CurrentMs())
	case StateBackoff:
		s.LastExitAt = time.Now()
		s.LastExitCode = exitCode
		if exitErr != nil {
			s.LastExitErr = exitErr.Error()
		}
		s.BackoffNextMs = int64(p.backoff.CurrentMs())
		s.RestartCount = cur.RestartCount + 1
	case StateStopped:
		s.LastExitAt = time.Now()
		if exitErr != nil {
			s.LastExitErr = exitErr.Error()
		}
	}
	p.status.Store(&s)
}

func (p *Process) applySelfLimits() error {
	var firstErr error
	if err := applyMemoryHardLimit(p.memoryHardMiB); err != nil {
		firstErr = err
	}
	if err := applyNiceLevel(p.niceLevel); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := applyOOMScoreAdj(os.Getpid(), p.oomScoreAdj); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func pidOf(p *Process) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func parseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

func newSysProcAttr() *syscall.SysProcAttr {
	// Isolate into a new process group so we can SIGKILL the whole tree.
	return &syscall.SysProcAttr{Setpgid: true}
}

// String is a small helper for logs/tests.
func (p *Process) String() string {
	return fmt.Sprintf("supervisor(%s pid=%d)", p.binary, pidOf(p))
}
