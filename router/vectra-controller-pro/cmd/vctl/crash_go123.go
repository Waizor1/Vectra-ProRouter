//go:build go1.23

package main

import "runtime/debug"

// The production agent must not duplicate raw Go panic values or stack data
// into persistent diagnostics. Procd still reports exit/crash-loop events.
// CLI developer commands retain their normal stderr outside agent startup.
func captureCrashes() {
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	debug.SetTraceback("none")
}
