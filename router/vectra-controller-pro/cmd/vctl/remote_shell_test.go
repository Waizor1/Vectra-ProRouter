package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"vectra-controller-pro/internal/controlplane"
)

// fakeRemoteShell stands in for UCI vectra-controller-pro.main.remote_shell.
func fakeRemoteShell(t *testing.T, on bool) {
	t.Helper()
	prev := remoteShellAllowed
	remoteShellAllowed = func() bool { return on }
	t.Cleanup(func() { remoteShellAllowed = prev })
}

// The panel's support shell — any command, as root — runs only where the
// router's owner allows it. Refused, nothing runs, and the panel is told why
// in words it can show.
func TestTheSupportShellRunsOnlyWhereTheOwnerAllowsIt(t *testing.T) {
	results := map[string][]controlplane.JobResultRequest{}
	mu := &sync.Mutex{}
	d := buildGuardTestDaemon(t, results, mu)
	marker := filepath.Join(t.TempDir(), "ran")
	job := func(id string) controlplane.Job {
		return controlplane.Job{ID: id, Type: "run_terminal_command", Payload: map[string]any{"command": "touch " + marker}}
	}

	fakeRemoteShell(t, false)
	_ = d.executeJob(context.Background(), job("t1"), controlplane.CheckInResponse{})
	fail, ok := lastResult(results, mu, "t1")
	if !ok {
		t.Fatal("the shell ran with the owner's switch off")
	}
	if msg, _ := fail.Result["error"].(string); !strings.Contains(msg, "support shell access is off on this router") {
		t.Errorf("the refusal does not say why: %v", fail.Result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the command ran anyway: %v", err)
	}

	// Anti-vacuity: allowed, the same job runs.
	fakeRemoteShell(t, true)
	_ = d.executeJob(context.Background(), job("t2"), controlplane.CheckInResponse{})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("allowed, the command did not run: %v (results %+v)", err, results["t2"])
	}
}

// Operators see the switch: every check-in says whether the shell is on.
func TestTheCheckInSaysWhetherTheSupportShellIsOn(t *testing.T) {
	for _, on := range []bool{true, false} {
		dir := t.TempDir()
		legacy := writeLegacyState(t, dir)
		panel := newCapturingPanel(t)
		d := newIdentityDaemon(t, dir, panel.URL, legacy)
		fakeRemoteShell(t, on)
		if err := d.runOnce(context.Background()); err != nil {
			t.Fatalf("runOnce: %v", err)
		}
		var body struct {
			Inventory map[string]any `json:"inventory"`
		}
		if err := json.Unmarshal(panel.body("/api/router/check-in"), &body); err != nil {
			t.Fatal(err)
		}
		if got, ok := body.Inventory["remoteShell"].(bool); !ok || got != on {
			t.Errorf("remote_shell %v: the check-in says %v", on, body.Inventory["remoteShell"])
		}
	}
}
