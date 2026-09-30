package supervisor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/logging"
)

// fakeXray stays up until it is signalled, like a supervised xray.
func fakeXray(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-xray")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A supervisor stopped once — a router its owner released — and run again
// for the next owner must restart a crashed xray, not take the old Stop for
// its own and give up.
func TestRunAfterStopSupervisesAfresh(t *testing.T) {
	p := NewProcess(config.Process{XrayBinary: fakeXray(t), ConfigFile: os.DevNull, ReloadGrace: "2s",
		RestartBackoff: config.Backoff{InitialMs: 50, Factor: 1, MaxMs: 50, Reset: "1s"}})
	run := func() (context.CancelFunc, chan struct{}) {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = p.Run(ctx)
		}()
		return cancel, done
	}
	ended := func(done chan struct{}) bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}

	cancel1, done1 := run()
	waitFor(t, "the first xray", func() bool { return p.Status().State == StateRunning })
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	cancel1()
	waitFor(t, "the first run to end", func() bool { return ended(done1) })

	cancel2, done2 := run()
	defer func() {
		cancel2()
		<-done2
	}()
	waitFor(t, "the second xray", func() bool { return p.Status().State == StateRunning })
	first := p.Status().PID
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil { // a crash
		t.Fatal(err)
	}
	waitFor(t, "the crashed xray to be restarted", func() bool {
		s := p.Status()
		return s.State == StateRunning && s.PID != 0 && s.PID != first
	})
	if ended(done2) {
		t.Fatal("the second run ended at the crash")
	}
}

// xray runs under the Go tuning vctl sizes for the router (SetChildEnv), not
// under the controller's own: the controller's GOMEMLIMIT/GOGC are dropped
// and the pinned ones added.
func TestChildEnvReplacesTheControllersGoTuning(t *testing.T) {
	dir := t.TempDir()
	envOut := filepath.Join(dir, "env")
	bin := filepath.Join(dir, "fake-xray")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nenv > "+envOut+".tmp && mv "+envOut+".tmp "+envOut+"\nexec sleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMEMLIMIT", "32MiB")
	t.Setenv("GOGC", "50")
	p := NewProcess(config.Process{XrayBinary: bin, ConfigFile: os.DevNull, ReloadGrace: "2s",
		RestartBackoff: config.Backoff{InitialMs: 50, Factor: 1, MaxMs: 50, Reset: "1s"}})
	p.SetChildEnv([]string{"GOMEMLIMIT=51MiB", "GOGC=30"})
	p.SetOOMScoreAdj(-800, 0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Run(ctx)
	}()
	defer func() {
		_ = p.Stop(context.Background())
		cancel()
		<-done
	}()
	var env string
	waitFor(t, "the child's environment", func() bool {
		b, err := os.ReadFile(envOut)
		env = string(b)
		return err == nil && len(b) > 0 && p.Status().State == StateRunning
	})
	has := func(kv string) bool {
		for _, line := range strings.Split(env, "\n") {
			if line == kv {
				return true
			}
		}
		return false
	}
	if !has("GOMEMLIMIT=51MiB") || !has("GOGC=30") || has("GOMEMLIMIT=32MiB") || has("GOGC=50") {
		t.Fatalf("child environment:\n%s", env)
	}
}

// syncBuffer is a log sink the supervisor's goroutine and the test share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// 1111, 30.09: every upgrade logged «xray exited; will restart» with
// "signal: killed" — the controller's own shutdown (its context cancelled)
// ending xray, read as a crash. An orderly shutdown is no crash: no warning,
// no restart promised, the state is stopped.
func TestShutdownIsNoCrash(t *testing.T) {
	var logs syncBuffer
	prev := logging.L()
	logging.SetDefault(logging.New("info", &logs, "text"))
	defer logging.SetDefault(prev)

	p := NewProcess(config.Process{XrayBinary: fakeXray(t), ConfigFile: os.DevNull, ReloadGrace: "2s",
		RestartBackoff: config.Backoff{InitialMs: 50, Factor: 1, MaxMs: 50, Reset: "1s"}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = p.Run(ctx)
	}()
	waitFor(t, "xray", func() bool { return p.Status().State == StateRunning })
	cancel() // the controller shutting down, as `service vectra-controller-pro stop` does
	<-done

	if strings.Contains(logs.String(), "will restart") || strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("an orderly shutdown was logged as a crash:\n%s", logs.String())
	}
	if s := p.Status().State; s != StateStopped {
		t.Fatalf("state after shutdown = %v, want %v", s, StateStopped)
	}
}
