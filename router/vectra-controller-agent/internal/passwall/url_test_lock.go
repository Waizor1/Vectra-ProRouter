package passwall

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// RunURLTestNode runs `test.sh url_test_node <nodeID>` under the shared lock.
func RunURLTestNode(ctx context.Context, backend UCIBackend, nodeID string) (CommandResult, error) {
	release, err := acquireURLTestLock(ctx, time.Now)
	if err != nil {
		return CommandResult{
			Command: urlTestScript + " url_test_node " + nodeID,
			Stderr:  err.Error(),
		}, err
	}
	defer release()
	return backend.Run(ctx, urlTestScript, "url_test_node", nodeID)
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
