package supervisor

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ReadProcSnapshot returns a ResourceSnapshot for the given pid.
// On non-Linux it returns a stub with SampledAt only — useful so the
// supervisor logic can be exercised on macOS during dev.
func ReadProcSnapshot(pid int) (ResourceSnapshot, error) {
	snap := ResourceSnapshot{SampledAt: time.Now()}
	if runtime.GOOS != "linux" || pid <= 0 {
		return snap, nil
	}
	if err := readProcStatus(pid, &snap); err != nil {
		return snap, err
	}
	if err := readProcStat(pid, &snap); err != nil {
		// Stat is optional; not fatal.
		_ = err
	}
	snap.FDCount = readFDCount(pid)
	return snap, nil
}

func readProcStatus(pid int, snap *ResourceSnapshot) error {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return err
	}
	defer f.Close()
	scn := bufio.NewScanner(f)
	for scn.Scan() {
		line := scn.Text()
		switch {
		case strings.HasPrefix(line, "VmRSS:"):
			snap.RSSBytes = parseKB(line)
		case strings.HasPrefix(line, "VmSize:"):
			snap.VmSizeBytes = parseKB(line)
		case strings.HasPrefix(line, "Threads:"):
			snap.ThreadCount = parseInt(line)
		}
	}
	return scn.Err()
}

func parseKB(line string) uint64 {
	// "VmRSS:\t   12345 kB"
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(parts[1], 10, 64)
	return v * 1024
}

func parseInt(line string) int {
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return 0
	}
	v, _ := strconv.Atoi(parts[1])
	return v
}

func readProcStat(pid int, snap *ResourceSnapshot) error {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return err
	}
	// Format: pid (comm) state ppid pgrp session tty_nr tpgid flags ...
	// We need utime (field 14) and stime (field 15). comm may contain spaces,
	// so split on the last ')'.
	s := string(data)
	rp := strings.LastIndex(s, ")")
	if rp < 0 {
		return fmt.Errorf("bad /proc/%d/stat", pid)
	}
	tail := strings.Fields(s[rp+1:])
	if len(tail) < 14 {
		return fmt.Errorf("too few fields in /proc/%d/stat", pid)
	}
	// tail[0] is state, tail[1] is ppid, ... utime = field 14 of full stat = tail[11]
	utime, _ := strconv.ParseUint(tail[11], 10, 64)
	stime, _ := strconv.ParseUint(tail[12], 10, 64)
	snap.UserJiffies = utime
	snap.SystemJiffies = stime
	return nil
}

func readFDCount(pid int) int {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	return len(entries)
}

// SelfSnapshot reads /proc/self for our own controller process.
// On macOS it returns Go-runtime memory stats so we at least have a number.
func SelfSnapshot() ResourceSnapshot {
	if runtime.GOOS == "linux" {
		s, _ := ReadProcSnapshot(os.Getpid())
		return s
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return ResourceSnapshot{
		SampledAt: time.Now(),
		RSSBytes:  m.Sys,
		VmSizeBytes: m.HeapSys + m.StackSys,
		ThreadCount: runtime.NumGoroutine(),
	}
}

// WriteStatus writes a JSON status file atomically (fsync + dir-fsync).
func WriteStatus(path string, s Status) error {
	return atomicWriteFile(path, mustJSON(s), 0o600)
}
