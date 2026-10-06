package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/conntrack"
	"vectra-controller-pro/internal/incident"
	"vectra-controller-pro/internal/portfwd"
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
	// Nor the host's connection table.
	dnsFlowsRead = func() ([]conntrack.Entry, error) { return nil, nil }
	// Nor the host's firewall, uci or ubus: a router without fw4's config,
	// whose commands all fail. Port forward tests point it at their own.
	portfwdEnv = func() portfwd.Env {
		fail := errors.New("no router in tests")
		return portfwd.Env{FirewallConfig: filepath.Join(dir, "absent-firewall"), DHCPConfig: filepath.Join(dir, "absent-dhcp"),
			Leases: filepath.Join(dir, "absent-leases"), RunDir: filepath.Join(dir, "pf-run"), Lock: filepath.Join(dir, "pf.lock"),
			DirectStatus: filepath.Join(dir, "pf-run", "portfwd-direct.json"),
			Run:          func(context.Context, io.Reader, string, ...string) error { return fail },
			Output:       func(context.Context, string, ...string) ([]byte, error) { return nil, fail }}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
