package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	configSource  func() ([]byte, error)
	logDir        string
	assetDir      string
	memorySoftMiB int
	memoryHardMiB int
	oomScoreAdj   int
	niceLevel     int

	// adjPinned: SetOOMScoreAdj set selfAdj/childAdj, and both are written
	// explicitly — 0 included, since a child otherwise inherits the
	// controller's adjustment.
	adjPinned bool
	selfAdj   int
	childAdj  int
	// childEnv is added to every xray's environment (SetChildEnv).
	childEnv []string

	mu          sync.Mutex
	cmd         *exec.Cmd
	configWrite *os.File
	configDone  chan struct{}
	cancel      context.CancelFunc
	done        chan struct{} // closed exactly once after the active cmd's Wait returns
	status      atomic.Pointer[Status]
	backoff     *BackoffState
	startedAt   time.Time

	stopping     atomic.Bool // true after Stop has been called
	expectedExit atomic.Bool // true when a controlled restart (Reload/Stop) signalled

	// onStart runs (in its own goroutine) after every successful start.
	onStart atomic.Pointer[func(pid int)]
	// onExit runs (in its own goroutine) after xray exits unasked.
	onExit atomic.Pointer[func(code int, err error, ran time.Duration)]
	// xrayLog is the size-capped sink for xray's stdout/stderr, shared across
	// restarts; nil until the first start with a log dir.
	xrayLog *cappedLog
	// hooks counts the onStart/onExit goroutines still running (WaitHooks).
	hooks sync.WaitGroup
}

// WaitHooks waits for the start and exit hooks still running. Call it after
// Run has returned: Run is what starts them, so none can begin after that.
func (p *Process) WaitHooks() { p.hooks.Wait() }

// SetOnStart registers fn to run after every successful xray start — the
// first one and every restart, intentional or not. It runs in its own
// goroutine, so it may wait for xray to come up.
// SetOnExit runs fn (in its own goroutine) every time xray exits without
// having been asked to — not while the supervisor itself stops (vctl going
// down takes xray with it): its exit code, the error, how long it ran.
func (p *Process) SetOnExit(fn func(code int, err error, ran time.Duration)) {
	if fn == nil {
		p.onExit.Store(nil)
		return
	}
	p.onExit.Store(&fn)
}

func (p *Process) SetOnStart(fn func(pid int)) {
	if fn == nil {
		p.onStart.Store(nil)
		return
	}
	p.onStart.Store(&fn)
}

// SetConfigSource supplies a fresh configuration for each start, including crash
// restarts. The source must return an independent snapshot. Config bytes travel
// only through the child stdin pipe; child output is discarded in this mode.
// This reduces disk artifacts, not root access to process memory.
func (p *Process) SetConfigSource(source func() ([]byte, error)) {
	p.mu.Lock()
	p.configSource = source
	p.mu.Unlock()
}

// SetOOMScoreAdj pins the OOM score adjustment of the controller itself
// (applied when Run starts) and of every xray it starts, both written even
// when 0. Call before Run.
func (p *Process) SetOOMScoreAdj(self, child int) {
	p.adjPinned, p.selfAdj, p.childAdj = true, self, child
}

// SetAssetDir changes where the next xray start reads its geo files
// (XRAY_LOCATION_ASSET): a route policy's own files, once they are in place.
func (p *Process) SetAssetDir(dir string) {
	if dir == "" {
		return
	}
	p.mu.Lock()
	p.assetDir = dir
	p.mu.Unlock()
}

// AssetDir is the directory xray's next start reads its geo files from.
func (p *Process) AssetDir() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.assetDir
}

// SetChildEnv adds variables to every xray's environment (after the
// controller's own Go tuning is dropped, see childEnv). Call before Run.
func (p *Process) SetChildEnv(env []string) {
	p.childEnv = append([]string(nil), env...)
}

// NewProcess builds a Process from the operator's Process config block.
// The geo asset dir defaults to config.DefaultGeoAssetDir; use
// NewProcessWithAssetDir to pin the operator's value.
func NewProcess(c config.Process) *Process {
	return NewProcessWithAssetDir(c, config.DefaultGeoAssetDir)
}

// NewProcessWithAssetDir builds a Process that exports XRAY_LOCATION_ASSET=assetDir
// to the supervised child. Xray otherwise looks for geoip.dat/geosite.dat next
// to its own binary and fails to start on any config that references geo data.
func NewProcessWithAssetDir(c config.Process, assetDir string) *Process {
	if assetDir == "" {
		assetDir = config.DefaultGeoAssetDir
	}
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
		assetDir:      assetDir,
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
	p.mu.Lock()
	hardened := p.configSource != nil
	p.mu.Unlock()
	if hardened {
		return errors.New("supervisor: file config disabled with private config source")
	}
	return atomicWriteFile(p.configFile, data, 0o600)
}

// Run starts the supervised process loop. Returns when ctx is cancelled
// (orderly shutdown) or a non-restartable failure occurs. A Run after Stop
// supervises afresh: the stop belonged to the run it ended.
func (p *Process) Run(ctx context.Context) error {
	p.stopping.Store(false)
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
			// The controller shutting down: its context ended xray (a KILL from
			// exec.CommandContext). Orderly, not a crash — no restart to promise.
			if ctx.Err() != nil {
				log.Info("xray stopped with the controller", "runDuration", runDuration.String())
				p.updateStatus(StateStopped, 0, nil)
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
			if hook := p.onExit.Load(); hook != nil && ctx.Err() == nil {
				p.hooks.Add(1)
				go func() { defer p.hooks.Done(); (*hook)(exitCodeOf(exitErr), exitErr, runDuration) }()
			}
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
	p.mu.Lock()
	source := p.configSource
	p.mu.Unlock()
	configArg := p.configFile
	var candidate []byte
	if source != nil {
		var err error
		candidate, err = source()
		if err != nil {
			cancel()
			return errors.New("supervisor: private configuration unavailable")
		}
		if len(candidate) == 0 {
			cancel()
			return errors.New("supervisor: private configuration empty")
		}
		configArg = "stdin:"
	}
	cmd := exec.CommandContext(subCtx, p.binary, "run", "-c", configArg)
	cmd.WaitDelay = 100 * time.Millisecond
	var configRead, configWrite *os.File
	if source != nil {
		var err error
		configRead, configWrite, err = os.Pipe()
		if err != nil {
			cancel()
			return errors.New("supervisor: private configuration pipe unavailable")
		}
		cmd.Stdin = configRead
		originalCancel := cmd.Cancel
		cmd.Cancel = func() error { _ = configWrite.Close(); return originalCancel() }
		// Public ownership marker for init orphan cleanup. The production shell
		// wrapper retains its own argv so its scoped cleanup pattern still works.
		if filepath.Base(p.binary) != "vctl-xray-wrapper" {
			cmd.Args[0] = "vctl-xray-private"
		}
	}
	// Pin the geo asset dir regardless of how xray was launched. The
	// vctl-xray-wrapper exports it too, but the supervisor may exec
	// /usr/bin/xray directly (dev, doctor, a config that bypasses the wrapper)
	// and Xray then resolves geoip.dat next to its own binary and dies.
	// XrayAssetEnv drops any GOGC/GOMEMLIMIT: the pinned ones go after it.
	p.mu.Lock()
	assetDir := p.assetDir
	p.mu.Unlock()
	cmd.Env = append(config.XrayAssetEnv(childEnv(os.Environ()), assetDir), p.childEnv...)
	cmd.SysProcAttr = newSysProcAttr()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if source == nil && p.logDir != "" {
		if err := os.MkdirAll(p.logDir, 0o755); err == nil {
			// Size-capped, NOT an O_APPEND file handed to xray. The log dir is
			// tmpfs — RAM — and xray logs a warning per failed probe and per
			// failed connection; an append-only file grows until the nightly
			// reboot on a 234 MB router. The cap keeps the newest lines.
			if p.xrayLog == nil {
				p.xrayLog = newCappedLog(filepath.Join(p.logDir, "xray.log"), XrayLogMaxBytes)
			}
			cmd.Stdout = p.xrayLog
			cmd.Stderr = p.xrayLog
		}
	}
	if err := cmd.Start(); err != nil {
		if configRead != nil {
			_ = configRead.Close()
			_ = configWrite.Close()
		}
		cancel()
		return err
	}
	var configDone chan struct{}
	if configRead != nil {
		configDone = make(chan struct{})
		_ = configRead.Close()
		// A real OS pipe avoids exec retaining a Reader and the plaintext snapshot
		// throughout the child's lifetime. EOF terminates Xray's config read.
		go func(data []byte) {
			defer close(configDone)
			defer clear(data)
			_, _ = configWrite.Write(data)
			_ = configWrite.Close()
		}(candidate)
	}
	doneCh := make(chan struct{})
	p.mu.Lock()
	p.cmd = cmd
	p.configWrite = configWrite
	p.configDone = configDone
	p.cancel = cancel
	p.done = doneCh
	p.startedAt = time.Now()
	p.mu.Unlock()
	p.updateStatus(StateRunning, 0, nil)
	if hook := p.onStart.Load(); hook != nil && cmd.Process != nil {
		pid := cmd.Process.Pid
		p.hooks.Add(1)
		go func() { defer p.hooks.Done(); (*hook)(pid) }()
	}
	if cmd.Process != nil && (p.adjPinned || p.oomScoreAdj != 0) {
		adj := p.oomScoreAdj
		if p.adjPinned {
			adj = p.childAdj
		}
		if err := applyOOMScoreAdj(cmd.Process.Pid, adj); err != nil {
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
	configWrite, configDone := p.configWrite, p.configDone
	p.mu.Unlock()
	if cmd == nil {
		return errors.New("supervisor: nil cmd in waitOnce"), 0
	}
	err := cmd.Wait()
	if configWrite != nil {
		_ = configWrite.Close()
		<-configDone
	}
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
	}
	p.cmd = nil
	p.configWrite, p.configDone = nil, nil
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
	self := p.oomScoreAdj
	if p.adjPinned {
		self = p.selfAdj
	}
	if err := applyOOMScoreAdj(os.Getpid(), self); err != nil && firstErr == nil {
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

// childEnv is the controller's environment minus its OWN Go runtime tuning.
// procd starts the controller with GOGC/GOMEMLIMIT sized for the controller
// (init script); xray inheriting them would run under the controller's 32 MiB
// soft limit, because vctl-xray-wrapper only sets its own defaults
// (GOMEMLIMIT=80MiB, GOGC=30) when the variables are unset.
func childEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOMEMLIMIT=") || strings.HasPrefix(kv, "GOGC=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
