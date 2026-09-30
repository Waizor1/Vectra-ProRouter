//go:build go1.23

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"vectra-controller-pro/internal/incident"
)

// captureCrashes points Go's crash output — an unhandled panic, a fatal
// error, SIGQUIT — at a file of this process's own, which the reporter turns
// into a report once the process is gone. stderr gets it as before.
func captureCrashes() {
	if err := os.MkdirAll(incident.CrashDir, 0o755); err != nil {
		return
	}
	f, err := os.Create(filepath.Join(incident.CrashDir, fmt.Sprintf("vctl.%d", os.Getpid())))
	if err != nil {
		return
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	_ = f.Close() // SetCrashOutput keeps its own duplicate
}
