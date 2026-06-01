//go:build !linux

package supervisor

// Non-Linux stubs so the package builds on macOS for local development.
// All operations are no-ops; a warning is logged at startup via Process.

func applyOOMScoreAdj(pid int, score int) error { return nil }
func applyMemoryHardLimit(memHardMiB int) error { return nil }
func applyNiceLevel(level int) error            { return nil }
