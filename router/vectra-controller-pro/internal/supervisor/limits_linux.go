//go:build linux

package supervisor

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// applyOOMScoreAdj writes oom_score_adj for the given pid (Linux only).
func applyOOMScoreAdj(pid int, score int) error {
	path := fmt.Sprintf("/proc/%d/oom_score_adj", pid)
	return os.WriteFile(path, []byte(strconv.Itoa(score)), 0o644)
}

// applyMemoryHardLimit sets RLIMIT_AS (virtual memory) for the calling process.
// We do NOT use RLIMIT_RSS because Linux ignores it; RLIMIT_AS at least caps
// virtual address space and gives a predictable malloc failure mode rather
// than an OOM-kill.
func applyMemoryHardLimit(memHardMiB int) error {
	if memHardMiB <= 0 {
		return nil
	}
	limit := uint64(memHardMiB) * 1024 * 1024
	return syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: limit, Max: limit})
}

// applyNiceLevel sets the nice level for the calling process.
func applyNiceLevel(level int) error {
	if level == 0 {
		return nil
	}
	// PRIO_PROCESS = 0
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, level)
}
