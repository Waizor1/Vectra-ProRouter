package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/logging"
)

// killOrphanXray ends an xray that a vctl killed outright (kill -9, the OOM
// killer) left behind: its parent is init and its argv is vctl's own. procd
// respawns vctl without the init script's start_service, which until r15 was
// the only place that did this (kill_orphan_xray). The next xray could not
// bind the tproxy port and crash-looped beside an orphan nobody supervised;
// once the orphan went, the LAN had no xray for the supervisor's backoff
// (the vctl-kill drill on 1111, 2026-10-04: about a minute). Returns how many
// it ended.
func (d *daemon) killOrphanXray() int {
	kill := d.signalProcess
	if kill == nil {
		kill = func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }
	}
	pids := orphanXrayPIDs(d.procRoot(), filepath.Dir(d.cfg.XrayRenderPath))
	for _, pid := range pids {
		logging.L().Warn("an xray of a vctl that is gone still runs; ending it", "pid", pid)
		_ = kill(pid, syscall.SIGTERM)
	}
	if len(pids) == 0 {
		return 0
	}
	// Its sockets must be gone before the supervisor's xray binds them.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		alive := 0
		for _, pid := range pids {
			if kill(pid, 0) == nil {
				alive++
			}
		}
		if alive == 0 {
			return len(pids)
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, pid := range pids {
		_ = kill(pid, syscall.SIGKILL)
	}
	return len(pids)
}

// orphanXrayPIDs are the processes whose parent is init and whose command
// line is an xray vctl starts: the private config on stdin under vctl's own
// argv[0] (vctl-xray-private, vctl-xray-wrapper), or a config in renderDir —
// never a generic xray, which may belong to another service. The init
// script's pattern (vctl_xray_pattern), in Go.
func orphanXrayPIDs(procRoot, renderDir string) []int {
	pattern := regexp.MustCompile(`(^|/)(vctl-xray-wrapper|vctl-xray-private) run -c stdin:($| )|run -c ` + regexp.QuoteMeta(renderDir+"/"))
	dirs, err := os.ReadDir(procRoot)
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range dirs {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		if parentPID(filepath.Join(procRoot, e.Name(), "stat")) != 1 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		cmdline := strings.TrimRight(strings.ReplaceAll(string(raw), "\x00", " "), " ")
		if pattern.MatchString(cmdline) {
			out = append(out, pid)
		}
	}
	return out
}

// parentPID reads the parent pid from /proc/<pid>/stat: the field after the
// state, past the command in parentheses (which may itself hold spaces).
func parentPID(statPath string) int {
	raw, err := os.ReadFile(statPath)
	if err != nil {
		return 0
	}
	s := string(raw)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(fields[1])
	return ppid
}
