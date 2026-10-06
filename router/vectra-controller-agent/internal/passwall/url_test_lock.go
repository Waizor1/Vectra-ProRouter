package passwall

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// test.sh url_test_node starts `app.sh run_socks flag=url_test_<node>` and,
// when it finishes, kills every process whose command line names
// url_test_<node>. Two probes of the same node at once therefore kill each
// other's temporary xray and both report the node dead. The agent and the
// cron watchdog (vectra-controller-watchdog) share this mkdir lock so only one
// url_test_node runs at a time. The owner file holds "<unix epoch> <pid>"; a
// lock older than URLTestLockStale is abandoned and may be broken.
var URLTestLockDir = "/var/run/vectra-url-test.lock"

const (
	URLTestLockStale = 2 * time.Minute
	urlTestLockPoll  = 500 * time.Millisecond
)

// URLTestLockWait is longer than one watchdog probe (30 s timeout) so the
// agent waits out a watchdog probe instead of giving up on it. A variable
// only so tests need not wait 45 s.
var URLTestLockWait = 45 * time.Second

// ErrURLTestBusy: another url_test_node held the lock for the whole wait.
// Callers must treat the node as unjudged, never as dead.
var ErrURLTestBusy = errors.New("another url_test_node probe is running")

const urlTestScript = "/usr/share/passwall2/test.sh"

// URLTestTimeout bounds one url_test_node, the same budget the watchdog
// gives it. test.sh's curl has no --max-time, so without it a hung probe would
// hold the shared lock until it is broken as stale. A variable for tests.
var URLTestTimeout = 30 * time.Second

// RunURLTestNode runs `test.sh url_test_node <nodeID>` under the shared lock,
// for at most URLTestTimeout.
func RunURLTestNode(ctx context.Context, backend UCIBackend, nodeID string) (CommandResult, error) {
	release, err := acquireURLTestLock(ctx, time.Now)
	if err != nil {
		return CommandResult{
			Command: urlTestScript + " url_test_node " + nodeID,
			Stderr:  err.Error(),
		}, err
	}
	defer release()

	probeCtx, cancel := context.WithTimeout(ctx, URLTestTimeout)
	defer cancel()
	finished := make(chan struct{})
	reaped := make(chan struct{})
	go func() {
		defer close(reaped)
		select {
		case <-probeCtx.Done():
			// The deadline kills test.sh only. Its run_socks/xray and curl keep
			// the output pipe open (so the Run below would not return) and the
			// xray keeps ~30 MB; reap them by name, as test.sh would have.
			if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
				reapURLTestLeftovers(nodeID)
			}
		case <-finished:
		}
	}()
	result, runErr := backend.Run(probeCtx, urlTestScript, "url_test_node", nodeID)
	close(finished)
	<-reaped
	return result, runErr
}

// Where reapURLTestLeftovers looks; variables for tests.
var (
	urlTestProcRoot     = "/proc"
	urlTestTmpDir       = "/tmp/etc/passwall2"
	killURLTestLeftover = func(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
)

// reapURLTestLeftovers kills processes whose command line names
// url_test_<nodeID> exactly (not url_test_<nodeID>x, and never test.sh
// itself) and removes their temporary config: what test.sh's own cleanup
// does when it is allowed to finish. Only called while holding the shared
// lock, so no other probe of the node is running.
func reapURLTestLeftovers(nodeID string) {
	pattern, err := regexp.Compile(`url_test_` + regexp.QuoteMeta(nodeID) + `([^A-Za-z0-9_]|$)`)
	if err != nil {
		return
	}
	entries, _ := os.ReadDir(urlTestProcRoot)
	self := os.Getpid()
	for _, entry := range entries {
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid == self {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(urlTestProcRoot, entry.Name(), "cmdline"))
		if readErr != nil {
			continue
		}
		cmdline := strings.TrimSpace(strings.ReplaceAll(string(raw), "\x00", " "))
		if strings.Contains(cmdline, "test.sh") || !pattern.MatchString(cmdline) {
			continue
		}
		_ = killURLTestLeftover(pid)
	}
	leftovers, _ := filepath.Glob(filepath.Join(urlTestTmpDir, "*url_test_"+nodeID+"*.json"))
	for _, path := range leftovers {
		if pattern.MatchString(filepath.Base(path)) {
			_ = os.Remove(path)
		}
	}
}

func acquireURLTestLock(ctx context.Context, now func() time.Time) (func(), error) {
	deadline := now().Add(URLTestLockWait)
	ownerPath := filepath.Join(URLTestLockDir, "owner")
	for {
		token := fmt.Sprintf("%d %d", now().Unix(), os.Getpid())
		err := os.Mkdir(URLTestLockDir, 0o700)
		if err == nil {
			_ = os.WriteFile(ownerPath, []byte(token), 0o600)
			return func() {
				if held, readErr := os.ReadFile(ownerPath); readErr == nil && string(held) == token {
					_ = os.RemoveAll(URLTestLockDir)
				}
			}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			// No writable run dir (tests, odd builds): a probe without the
			// lock is what every release before r46 did.
			return func() {}, nil
		}
		if urlTestLockAbandoned(ownerPath, now()) {
			_ = os.RemoveAll(URLTestLockDir)
			continue
		}
		if !now().Before(deadline) {
			return nil, ErrURLTestBusy
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", ErrURLTestBusy, ctx.Err())
		case <-time.After(urlTestLockPoll):
		}
	}
}

func urlTestLockAbandoned(ownerPath string, now time.Time) bool {
	held, err := os.ReadFile(ownerPath)
	if err != nil {
		// Owner not written yet: the holder is between mkdir and write. Give it
		// the benefit of the doubt unless the directory itself is old.
		info, statErr := os.Stat(filepath.Dir(ownerPath))
		return statErr == nil && now.Sub(info.ModTime()) >= URLTestLockStale
	}
	fields := strings.Fields(string(held))
	if len(fields) == 0 {
		return true
	}
	epoch, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return true
	}
	age := now.Sub(time.Unix(epoch, 0))
	return age >= URLTestLockStale || age < 0
}
