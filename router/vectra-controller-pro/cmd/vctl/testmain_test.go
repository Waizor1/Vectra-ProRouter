package main

import (
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/incident"
)

// No test writes into the router's own inbox: the reporter's directories are
// a temporary one's for the whole package.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "vctl-incidents-")
	if err != nil {
		panic(err)
	}
	incident.Dir, incident.CrashDir = filepath.Join(dir, "inbox"), filepath.Join(dir, "crash")
	// No test asks the host's netifd: the WAN is "wan" wherever a test
	// writes no firewall zones of its own (wanInterfaces).
	defaultRouteIfaces = func() []string { return []string{"wan"} }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
