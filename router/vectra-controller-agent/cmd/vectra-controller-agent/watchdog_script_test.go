package main

import (
	"os/exec"
	"testing"
)

// The cron watchdog is a shell script with its own off-router suite (stubbed
// uci/jsonfilter/test.sh, sandboxed markers). CI only runs `go test`, so run
// the suite from here: a regression in the dead-man's switch — the piece
// that can switch the whole fleet's VPN off — must fail the build.
func TestWatchdogDeadmanShellSuite(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no POSIX sh on this host: %v", err)
	}
	output, err := exec.Command(shell, "../../openwrt/tests/watchdog_deadman_test.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("watchdog_deadman_test.sh failed: %v\n%s", err, output)
	}
}
