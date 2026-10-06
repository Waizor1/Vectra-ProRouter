package passwall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type recordingBackend struct{ runs []string }

func (r *recordingBackend) Show(context.Context, string) ([]string, error) { return nil, nil }
func (r *recordingBackend) Batch(context.Context, []string) error          { return nil }
func (r *recordingBackend) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	r.runs = append(r.runs, name+" "+strings.Join(args, " "))
	return CommandResult{Stdout: "204:0.2"}, nil
}

func useURLTestLockDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "vectra-url-test.lock")
	original := URLTestLockDir
	URLTestLockDir = dir
	t.Cleanup(func() { URLTestLockDir = original })
	return dir
}

func TestRunURLTestNodeTakesAndReleasesTheSharedLock(t *testing.T) {
	dir := useURLTestLockDir(t)
	backend := &recordingBackend{}

	result, err := RunURLTestNode(context.Background(), backend, "node1")
	if err != nil || result.Stdout != "204:0.2" {
		t.Fatalf("RunURLTestNode = %#v, %v", result, err)
	}
	if len(backend.runs) != 1 || backend.runs[0] != "/usr/share/passwall2/test.sh url_test_node node1" {
		t.Fatalf("runs = %#v", backend.runs)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("lock left behind: %v", statErr)
	}
}

// The watchdog holds the lock (owner "<epoch> <pid>", the format both sides
// write): the agent must not run test.sh, which would kill the watchdog's
// xray, and must report the node busy rather than dead.
func TestRunURLTestNodeIsBusyWhileTheWatchdogProbes(t *testing.T) {
	dir := useURLTestLockDir(t)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	owner := fmt.Sprintf("%d 4242", time.Now().Unix())
	if err := os.WriteFile(filepath.Join(dir, "owner"), []byte(owner), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &recordingBackend{}
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()

	_, err := RunURLTestNode(ctx, backend, "node1")
	if !errors.Is(err, ErrURLTestBusy) {
		t.Fatalf("err = %v, want ErrURLTestBusy", err)
	}
	if len(backend.runs) != 0 {
		t.Fatalf("test.sh ran under someone else's lock: %#v", backend.runs)
	}
	if held, _ := os.ReadFile(filepath.Join(dir, "owner")); string(held) != owner {
		t.Fatalf("the watchdog's lock was disturbed: %q", held)
	}
}

func TestRunURLTestNodeBreaksAnAbandonedLock(t *testing.T) {
	dir := useURLTestLockDir(t)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := fmt.Sprintf("%d 4242", time.Now().Add(-URLTestLockStale-time.Second).Unix())
	if err := os.WriteFile(filepath.Join(dir, "owner"), []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &recordingBackend{}

	if _, err := RunURLTestNode(context.Background(), backend, "node1"); err != nil {
		t.Fatalf("RunURLTestNode: %v", err)
	}
	if len(backend.runs) != 1 {
		t.Fatalf("expected the probe to run after breaking the stale lock, got %#v", backend.runs)
	}
}

// Release removes the lock only while it is still ours.
func TestURLTestLockReleaseLeavesATakenOverLock(t *testing.T) {
	dir := useURLTestLockDir(t)
	release, err := acquireURLTestLock(context.Background(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "owner"), []byte("1 999"), 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("release removed a lock it no longer owned: %v", statErr)
	}
}

type hangingBackend struct{ started chan struct{} }

func (h *hangingBackend) Show(context.Context, string) ([]string, error) { return nil, nil }
func (h *hangingBackend) Batch(context.Context, []string) error          { return nil }
func (h *hangingBackend) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	close(h.started)
	<-ctx.Done()
	return CommandResult{Command: name + " " + strings.Join(args, " ")}, ctx.Err()
}

func fakeProc(t *testing.T, root string, pid int, args ...string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func useFakeProc(t *testing.T) (procRoot, tmpDir string, killed *[]int) {
	t.Helper()
	procRoot, tmpDir = t.TempDir(), t.TempDir()
	var list []int
	originalProc, originalTmp, originalKill := urlTestProcRoot, urlTestTmpDir, killURLTestLeftover
	urlTestProcRoot, urlTestTmpDir = procRoot, tmpDir
	killURLTestLeftover = func(pid int) error { list = append(list, pid); return nil }
	t.Cleanup(func() {
		urlTestProcRoot, urlTestTmpDir, killURLTestLeftover = originalProc, originalTmp, originalKill
	})
	return procRoot, tmpDir, &list
}

// A hung test.sh (its curl has no --max-time) must not hold the shared lock:
// the probe ends at URLTestTimeout, its leftovers are reaped and the lock is
// free again.
func TestRunURLTestNodeTimesOutReapsAndReleases(t *testing.T) {
	dir := useURLTestLockDir(t)
	procRoot, tmpDir, killed := useFakeProc(t)
	originalTimeout := URLTestTimeout
	URLTestTimeout = 150 * time.Millisecond
	t.Cleanup(func() { URLTestTimeout = originalTimeout })

	fakeProc(t, procRoot, 101, "xray", "run", "-c", "/tmp/etc/passwall2/url_test_n1.json")
	fakeProc(t, procRoot, 102, "/bin/sh", "/usr/share/passwall2/app.sh", "run_socks", "flag=url_test_n1", "node=n1")
	fakeProc(t, procRoot, 103, "/bin/sh", "/usr/share/passwall2/test.sh", "url_test_node", "n1")
	fakeProc(t, procRoot, 104, "xray", "run", "-c", "/tmp/etc/passwall2/url_test_n10.json")
	for _, name := range []string{"url_test_n1.json", "url_test_n10.json"} {
		if err := os.WriteFile(filepath.Join(tmpDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	backend := &hangingBackend{started: make(chan struct{})}
	start := time.Now()
	_, err := RunURLTestNode(context.Background(), backend, "n1")
	if err == nil {
		t.Fatal("expected the hung probe to fail")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("probe held for %s", elapsed)
	}
	if got := fmt.Sprint(*killed); got != "[101 102]" {
		t.Fatalf("killed %s, want exactly the node's xray and run_socks [101 102]", got)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "url_test_n1.json")); !os.IsNotExist(statErr) {
		t.Fatalf("leftover config not removed: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "url_test_n10.json")); statErr != nil {
		t.Fatalf("another node's config was removed: %v", statErr)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("lock still held after the timeout: %v", statErr)
	}
}

// A probe that finishes in time reaps nothing: test.sh cleaned up itself.
func TestRunURLTestNodeReapsNothingOnANormalFinish(t *testing.T) {
	useURLTestLockDir(t)
	procRoot, _, killed := useFakeProc(t)
	fakeProc(t, procRoot, 101, "xray", "run", "-c", "/tmp/etc/passwall2/url_test_node1.json")

	if _, err := RunURLTestNode(context.Background(), &recordingBackend{}, "node1"); err != nil {
		t.Fatal(err)
	}
	if len(*killed) != 0 {
		t.Fatalf("killed %v after a normal finish", *killed)
	}
}
