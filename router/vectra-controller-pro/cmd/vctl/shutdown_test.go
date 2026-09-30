package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/firewall"
)

// recordedCmds captures the firewall commands the daemon would run, plus
// whether the context it was handed was still alive.
type recordedCmds struct {
	mu         sync.Mutex
	lines      []string
	sawDeadCtx bool
}

func (r *recordedCmds) run(ctx context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		r.sawDeadCtx = true
	}
	r.lines = append(r.lines, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return nil
}

func (r *recordedCmds) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

// shutdownDaemon returns a daemon with a real operator config on disk (the
// teardown reads the tproxy block from it) and a recording command runner.
func shutdownDaemon(t *testing.T) (*daemon, *recordedCmds, firewall.Spec) {
	t.Helper()
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	panel := newPanelStub(t, nil)
	d := newTestDaemon(t, dir, panel, provider)

	cfg, err := config.Unmarshal(operatorConfigPointingAt(t, provider.URL))
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyDefaults(cfg)
	if err := config.Save(filepath.Join(dir, "operator.json"), cfg); err != nil {
		t.Fatal(err)
	}
	d.desired = cfg

	rec := &recordedCmds{}
	d.runFirewallCmd = rec.run

	spec, ok := firewallSpecFromConfig(cfg)
	if !ok {
		t.Fatal("test operator config has no tproxy inbound")
	}
	return d, rec, spec
}

// A SIGTERM outside procd must unload the data plane. Without this the nft
// table and the fwmark policy route survive the daemon: the output chain still
// marks local egress, the policy route resolves it to `local ... dev lo`, and
// the router's own traffic dies.
func TestRunAgentUnloadsTheDataPlaneAfterASignal(t *testing.T) {
	d, rec, spec := shutdownDaemon(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stand in for SIGTERM having already fired

	_ = runAgent(ctx, d, true)

	got := rec.snapshot()
	if len(got) == 0 {
		t.Fatal("a signal shutdown ran no teardown commands at all")
	}
	for _, want := range firewall.RevertCommands(spec) {
		if !containsCmd(got, want) {
			t.Errorf("shutdown did not run %q\ngot:\n  %s", want, strings.Join(got, "\n  "))
		}
	}
	// The confirm sentinel disarms any pending commit-confirm deadman: we are
	// intentionally down, not recovering from a bad ruleset.
	if _, err := os.Stat(d.confirmer.ConfirmPath); err != nil {
		t.Errorf("shutdown must disarm the commit-confirm deadman: %v", err)
	}
	// The teardown must NOT inherit the cancelled context. exec.CommandContext
	// on a dead context runs nothing, so passing it straight through would make
	// the whole shutdown a silent no-op on a real router.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.sawDeadCtx {
		t.Error("teardown ran with an already-cancelled context; exec would have skipped every command")
	}
}

// A normal `-once` run is not a shutdown: the controller is handing off to the
// next invocation (or to procd), and tearing the data plane down would drop
// traffic for no reason.
func TestRunAgentLeavesTheDataPlaneAloneWithoutASignal(t *testing.T) {
	d, rec, _ := shutdownDaemon(t)

	_ = runAgent(context.Background(), d, true)

	if got := rec.snapshot(); len(got) != 0 {
		t.Errorf("no signal, but the data plane was torn down:\n  %s", strings.Join(got, "\n  "))
	}
}

func containsCmd(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}
