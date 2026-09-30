//go:build !go1.23

package main

// captureCrashes needs Go 1.23 (debug.SetCrashOutput); a build with an older
// toolchain — the OpenWrt SDK's — leaves crashes to stderr and logd.
func captureCrashes() {}
