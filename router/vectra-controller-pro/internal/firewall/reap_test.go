//go:build unix

package firewall

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// The deadman is vctl's child; once it is done it must be reaped, not left a
// zombie (the test router had one per firewall programming). A zombie still
// answers kill(pid, 0); a reaped process is gone.
func TestTheDeadmanIsReapedWhenItExits(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := startReaped(cmd); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still exists 10 s after it exited: a zombie nobody reaps", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
