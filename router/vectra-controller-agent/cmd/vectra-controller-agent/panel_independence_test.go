package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/recovery"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

// The VPN must not depend on the panel. These tests pin the three ways a
// panel outage used to switch it off or keep it off: the outage-driven
// direct fallback, a DirectSettle park with no reboot budget, and the 12-hour
// operator-attention wait.

const worldNodeProbe = "/usr/share/passwall2/test.sh url_test_node world-node"

func shuntBackend(worldNodeAnswer string) *fakeRescueBackend {
	return &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			worldNodeProbe:                  {Stdout: worldNodeAnswer},
			"/etc/init.d/passwall2 restart": {Stdout: "restarted"},
		},
		protocols: map[string]string{
			"passwall2.myshunt.protocol":     "_shunt",
			"passwall2.myshunt.default_node": "_direct",
			"passwall2.myshunt.WorldProxy":   "world-node",
			"passwall2.world-node.protocol":  "vless",
			"passwall2.myshunt.YouTube":      "_default",
			"passwall2.myshunt.GooglePlay":   "_default",
			"passwall2.myshunt.Proxy":        "_default",
			"passwall2.myshunt.ProxyGame":    "_default",
			"passwall2.myshunt.Tiktok":       "_default",
			"passwall2.myshunt.Special":      "_default",
		},
	}
}

// deadPanelForeignBlocked starts a dead panel, live RU targets and blocked
// foreign targets, and returns the panel URL.
func deadPanelForeignBlocked(t *testing.T) string {
	t.Helper()
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	t.Cleanup(panel.Close)
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	t.Cleanup(ru.Close)
	blocked := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	t.Cleanup(blocked.Close)
	setRecoveryProbeTargets(t,
		[]probeTarget{
			{ID: "ya", Label: "ya.ru", URL: ru.URL},
			{ID: "vk", Label: "vk.com", URL: ru.URL},
		},
		[]probeTarget{
			{ID: "youtube", Label: "youtube", URL: blocked.URL},
			{ID: "instagram", Label: "instagram", URL: blocked.URL},
			{ID: "telegram", Label: "telegram", URL: blocked.URL},
		},
	)
	return panel.URL
}

func advanceOnce(
	t *testing.T,
	cfg *config.Config,
	backend passwall.UCIBackend,
	persisted *state.PersistedState,
	rescueState *rescue.State,
	inventory *controlplane.RouterInventory,
) controlPlaneRecoveryOutcome {
	t.Helper()
	runtimeStatus := state.RuntimeStatus{}
	outcome, err := advanceControlPlaneRecovery(
		context.Background(),
		cfg,
		backend,
		&persisted.ControlPlaneRecovery,
		rescueState,
		persisted,
		inventory,
		&runtimeStatus,
	)
	if err != nil {
		t.Fatalf("advanceControlPlaneRecovery returned error: %v", err)
	}
	return outcome
}

func TestPanelOutageWithAnsweringNodeKeepsProxyMode(t *testing.T) {
	panelURL := deadPanelForeignBlocked(t)
	backend := shuntBackend("204:0.31")
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseMonitoring,
	}}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	inventory := controlplane.RouterInventory{PasswallEnabled: true, SelectedNodeID: "myshunt"}

	outcome := advanceOnce(t, baseControlPlaneRecoveryConfig(panelURL), backend, &persisted, &rescueState, &inventory)

	if containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='0'") {
		t.Fatalf("a panel outage must not switch off a proxy whose node answers, got %#v", backend.batchCommands)
	}
	if outcome.InventoryChanged {
		t.Fatal("did not expect a PassWall toggle")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseMonitoring; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if got, want := rescueState.Mode, rescue.ModeProxy; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
	}
	if !containsCommand(backend.runCommands, worldNodeProbe) {
		t.Fatalf("expected the node verdict to come from url_test_node, got %#v", backend.runCommands)
	}
}

// The protection for a genuinely dead proxy stays: same outage, node silent.
func TestPanelOutageWithDeadNodeStillFallsBackToDirect(t *testing.T) {
	panelURL := deadPanelForeignBlocked(t)
	backend := shuntBackend("000:0.00")
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseMonitoring,
	}}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	inventory := controlplane.RouterInventory{PasswallEnabled: true, SelectedNodeID: "myshunt"}

	advanceOnce(t, baseControlPlaneRecoveryConfig(panelURL), backend, &persisted, &rescueState, &inventory)

	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseDirectSettle; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='0'") {
		t.Fatalf("expected the dead proxy to fail safe to direct, got %#v", backend.batchCommands)
	}
}

// DirectSettle with a dead panel and no reboot budget used to be a dead end:
// only a reachable panel or a reboot moved it on.
func TestDirectSettleWithDeadPanelReturnsToProxyWhenNodeAnswers(t *testing.T) {
	panelURL := deadPanelForeignBlocked(t)
	backend := shuntBackend("204:0.28")
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-3 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-2 * time.Hour)),
		Phase:                        recovery.PhaseDirectSettle,
		LastActionReason:             controlPlaneDirectReason,
		// Reboot budget spent an hour ago.
		LastAutoRebootAt: recovery.FormatTime(now.Add(-time.Hour)),
	}}
	rescueState := rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: now.Add(-30 * time.Minute)}
	inventory := controlplane.RouterInventory{PasswallEnabled: false, SelectedNodeID: "myshunt"}

	outcome := advanceOnce(t, baseControlPlaneRecoveryConfig(panelURL), backend, &persisted, &rescueState, &inventory)

	if !outcome.InventoryChanged {
		t.Fatal("expected PassWall to be re-enabled")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhasePasswallRetryWait; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if got, want := persisted.ControlPlaneRecovery.LastActionReason, parkedProxyProofRetryReason; got != want {
		t.Fatalf("reason = %q, want %q", got, want)
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected passwall enable batch, got %#v", backend.batchCommands)
	}
	if containsCommand(backend.runCommands, "sh -c") {
		t.Fatalf("did not expect a reboot or restart, got %#v", backend.runCommands)
	}
}

func TestDirectSettleWithDeadPanelStaysDirectWhenNodeIsDead(t *testing.T) {
	panelURL := deadPanelForeignBlocked(t)
	backend := shuntBackend("000:0.00")
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-3 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-2 * time.Hour)),
		Phase:                        recovery.PhaseDirectSettle,
		LastAutoRebootAt:             recovery.FormatTime(now.Add(-time.Hour)),
	}}
	rescueState := rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: now.Add(-30 * time.Minute)}
	inventory := controlplane.RouterInventory{PasswallEnabled: false, SelectedNodeID: "myshunt"}

	advanceOnce(t, baseControlPlaneRecoveryConfig(panelURL), backend, &persisted, &rescueState, &inventory)

	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseDirectSettle; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no PassWall writes, got %#v", backend.batchCommands)
	}
}

// Operator attention with a dead panel no longer waits the 12-hour
// RebootCooldown once the node answers again.
func TestOperatorAttentionWithDeadPanelRetriesOnProofWithinRebootCooldown(t *testing.T) {
	panelURL := deadPanelForeignBlocked(t)
	backend := shuntBackend("204:0.30")
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-6 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-5 * time.Hour)),
		Phase:                        recovery.PhaseOperatorAttention,
		AwaitingOperator:             true,
		LastActionReason:             operatorAttentionReason,
		LastPasswallRetryAt:          recovery.FormatTime(now.Add(-20 * time.Minute)),
	}}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	inventory := controlplane.RouterInventory{PasswallEnabled: false, SelectedNodeID: "myshunt"}

	outcome := advanceOnce(t, baseControlPlaneRecoveryConfig(panelURL), backend, &persisted, &rescueState, &inventory)

	if !outcome.InventoryChanged {
		t.Fatal("expected a probe-proven retry before RebootCooldown")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhasePasswallRetryWait; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected passwall enable batch, got %#v", backend.batchCommands)
	}
}

func TestOperatorAttentionProofRetryRespectsItsInterval(t *testing.T) {
	panelURL := deadPanelForeignBlocked(t)
	backend := shuntBackend("204:0.30")
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-6 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-5 * time.Hour)),
		Phase:                        recovery.PhaseOperatorAttention,
		AwaitingOperator:             true,
		LastActionReason:             operatorAttentionReason,
		LastPasswallRetryAt:          recovery.FormatTime(now.Add(-5 * time.Minute)),
	}}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	inventory := controlplane.RouterInventory{PasswallEnabled: false, SelectedNodeID: "myshunt"}

	outcome := advanceOnce(t, baseControlPlaneRecoveryConfig(panelURL), backend, &persisted, &rescueState, &inventory)

	if outcome.InventoryChanged || len(backend.batchCommands) != 0 {
		t.Fatalf("expected no retry inside the proof interval, got %#v", backend.batchCommands)
	}
	if containsCommand(backend.runCommands, worldNodeProbe) {
		t.Fatalf("did not expect a node probe inside the proof interval, got %#v", backend.runCommands)
	}
}

func TestCachedProxyNodeVerdictProbesOncePerCooldown(t *testing.T) {
	resetProxyNodeVerdictCache()
	t.Cleanup(resetProxyNodeVerdictCache)
	backend := shuntBackend("204:0.30")
	policy := rescue.Policy{Cooldown: 5 * time.Minute}
	inventory := controlplane.RouterInventory{SelectedNodeID: "myshunt"}
	now := time.Now()

	if !cachedProxyNodeReachable(context.Background(), backend, policy, &inventory, now) {
		t.Fatal("expected the node to answer")
	}
	if !cachedProxyNodeReachable(context.Background(), backend, policy, &inventory, now.Add(time.Minute)) {
		t.Fatal("expected the cached verdict")
	}
	if got := countCommand(backend.runCommands, worldNodeProbe); got != 1 {
		t.Fatalf("url_test_node ran %d times inside one cooldown, want 1", got)
	}
	cachedProxyNodeReachable(context.Background(), backend, policy, &inventory, now.Add(6*time.Minute))
	if got := countCommand(backend.runCommands, worldNodeProbe); got != 2 {
		t.Fatalf("url_test_node ran %d times after the cooldown, want 2", got)
	}
	rebound := controlplane.RouterInventory{SelectedNodeID: "other"}
	cachedProxyNodeReachable(context.Background(), backend, policy, &rebound, now.Add(7*time.Minute))
	if cached := proxyNodeVerdictCache.nodeID; cached != "other" {
		t.Fatalf("a rebind must be measured afresh, cache still keyed by %q", cached)
	}
}
