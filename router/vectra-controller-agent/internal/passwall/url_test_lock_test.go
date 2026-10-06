package passwall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
