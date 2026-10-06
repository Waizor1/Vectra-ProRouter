package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
)

func useWatchdogDirectMarker(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watchdog-direct")
	original := watchdogDirectMarkerPath
	watchdogDirectMarkerPath = path
	t.Cleanup(func() { watchdogDirectMarkerPath = original })
	return path
}

func writeMarker(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("1000000000"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

// Any deliberate write of the main switch by the agent (rescue, recovery,
// resume) takes ownership away from the watchdog.
func TestAgentMainSwitchWriteReleasesWatchdogOwnership(t *testing.T) {
	marker := useWatchdogDirectMarker(t)
	writeMarker(t, marker, time.Now().Add(-time.Hour))
	backend := &fakeRescueBackend{runResults: map[string]passwall.CommandResult{
		"/etc/init.d/passwall2 restart": {Stdout: "restarted"},
	}}

	if err := setPasswallMainSwitch(context.Background(), backend, true, mainSwitchOptions{ClearRescueReason: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("expected the watchdog marker to be released, stat err = %v", err)
	}
}

// validateDirectFallback's off/on toggle is a measurement. Right after the
// watchdog switched to direct it must not run at all: it would end with
// PassWall on again and the watchdog would cut it on its next tick.
func TestValidateDirectFallbackLeavesAFreshWatchdogDirectAlone(t *testing.T) {
	marker := useWatchdogDirectMarker(t)
	writeMarker(t, marker, time.Now().Add(-10*time.Second))
	backend := &fakeRescueBackend{}

	reachable, err := validateDirectFallback(context.Background(), baseRescueConfig("http://127.0.0.1:1"), backend, &controlplane.RouterInventory{PasswallEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if reachable {
		t.Fatal("did not expect a direct-path verdict without a probe")
	}
	if len(backend.batchCommands) != 0 || len(backend.runCommands) != 0 {
		t.Fatalf("expected no PassWall toggle, got batch %#v run %#v", backend.batchCommands, backend.runCommands)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the watchdog's marker must stay, stat err = %v", err)
	}
}

// An old marker (PassWall on again, cron cleanup pending) must not block the
// agent's own dead-proxy protection, and the measurement toggle must not
// claim ownership.
func TestValidateDirectFallbackIgnoresAStaleMarkerAndKeepsIt(t *testing.T) {
	marker := useWatchdogDirectMarker(t)
	writeMarker(t, marker, time.Now().Add(-time.Hour))
	backend := &fakeRescueBackend{runResults: map[string]passwall.CommandResult{
		"/etc/init.d/passwall2 restart": {Stdout: "restarted"},
	}}

	if _, err := validateDirectFallback(context.Background(), baseRescueConfig("http://127.0.0.1:1", "http://127.0.0.1:1/"), backend, &controlplane.RouterInventory{PasswallEnabled: true}); err != nil {
		t.Fatal(err)
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='0'") ||
		!containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected the usual off/on probe toggle, got %#v", backend.batchCommands)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a measurement toggle must not release ownership, stat err = %v", err)
	}
}
