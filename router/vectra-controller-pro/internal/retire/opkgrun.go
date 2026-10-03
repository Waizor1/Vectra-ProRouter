package retire

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"vectra-controller-pro/internal/memguard"
)

// OpkgTime bounds one opkg run. PassWall2's own stop runs inside its
// removal (its prerm) and restarts dnsmasq; seconds on a router, a minute on
// a slow one.
var OpkgTime = 3 * time.Minute

// opkgOutputMax bounds what is kept of opkg's output: its end is what says
// why.
const opkgOutputMax = 64 << 10

// RunOpkg runs opkg with args as given. In a session of its own and not on
// the caller's context — a vctl stopped meanwhile (procd, a signal) does not
// end it half way through the package database — but never past OpkgTime.
// Neutral to the OOM killer (memguard.JobAdj): it is not the controller.
func RunOpkg(ctx context.Context, args ...string) ([]byte, error) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), OpkgTime)
	defer cancel()
	cmd := exec.CommandContext(c, "sh", append([]string{"-c",
		fmt.Sprintf(`{ echo %d > /proc/self/oom_score_adj; } 2>/dev/null; exec opkg "$@"`, memguard.JobAdj), "opkg"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// Past its time the whole session goes — what its prerm started too,
	// which would otherwise hold the output open.
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var out capped
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.bytes(), err
}

// capped keeps the last opkgOutputMax bytes written to it.
type capped struct {
	mu  sync.Mutex
	buf []byte
}

func (c *capped) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf = append(c.buf, p...)
	if over := len(c.buf) - opkgOutputMax; over > 0 {
		c.buf = append(c.buf[:0], c.buf[over:]...)
	}
	return len(p), nil
}

func (c *capped) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf...)
}
