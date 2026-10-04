package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"vectra-controller-pro/internal/agentcfg"
)

// orphanProc writes /proc/<pid>/{stat,cmdline} for each process: ppid and argv.
func orphanProc(t *testing.T, procs map[string]struct {
	ppid int
	argv []string
}) string {
	t.Helper()
	root := t.TempDir()
	for pid, p := range procs {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		stat := pid + " (xray worker) S " + strconv.Itoa(p.ppid) + " 1 1 0 -1"
		_ = os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.Join(p.argv, "\x00")+"\x00"), 0o644)
	}
	return root
}

// Only vctl's own xray whose vctl is gone (parent init) is ended: not one a
// live vctl supervises, not a generic xray of another service.
func TestOrphanXrayIsEndedAtStart(t *testing.T) {
	type proc = struct {
		ppid int
		argv []string
	}
	root := orphanProc(t, map[string]proc{
		"101": {1, []string{"vctl-xray-private", "run", "-c", "stdin:"}},
		"102": {5, []string{"vctl-xray-private", "run", "-c", "stdin:"}},
		"103": {1, []string{"/usr/bin/xray", "run", "-c", "/etc/other/config.json"}},
		"104": {1, []string{"/usr/bin/xray", "run", "-c", "/var/run/vectra-controller-pro/xray.json"}},
	})
	d := &daemon{procDir: root, cfg: agentcfg.Config{XrayRenderPath: "/var/run/vectra-controller-pro/xray.json"}}
	var termed []int
	d.signalProcess = func(pid int, sig syscall.Signal) error {
		if sig == syscall.SIGTERM {
			termed = append(termed, pid)
		}
		if sig == 0 {
			return syscall.ESRCH // gone at once
		}
		return nil
	}
	if n := d.killOrphanXray(); n != 2 {
		t.Fatalf("ended %d, want 2", n)
	}
	got := map[int]bool{}
	for _, p := range termed {
		got[p] = true
	}
	if !reflect.DeepEqual(got, map[int]bool{101: true, 104: true}) {
		t.Fatalf("signalled %v, want the two orphans 101 and 104", termed)
	}
}
