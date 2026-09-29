package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/inventory"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/recovery"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

// andrey-avito, 2026-09-29: /usr/bin/xray gone, xray_file still the wrapper.
func missingXrayBinaryEvent() controlplane.RouterSafetyEvent {
	return controlplane.RouterSafetyEvent{
		Type:      inventory.ProxyRuntimeUnusableEventType,
		Severity:  "critical",
		Component: "xray",
		Source:    "filesystem",
		Message:   "xray binary /usr/bin/xray missing",
		Evidence:  "/usr/bin/xray is missing or not executable; passwall2.@global_app[0].xray_file=/usr/sbin/vectra-xray-wrapper is the Vectra wrapper, which execs it",
	}
}

// andrey-avito, 2026-09-28: upstream xray 26.9.9 refusing PassWall 26.4.10's
// config, first seen a minute ago.
func failedXrayStartEvent() controlplane.RouterSafetyEvent {
	return failedXrayStartEventFirstSeen(time.Now().UTC().Add(-time.Minute))
}

func failedXrayStartEventFirstSeen(firstSeen time.Time) controlplane.RouterSafetyEvent {
	return controlplane.RouterSafetyEvent{
		Type:       inventory.ProxyRuntimeUnusableEventType,
		Severity:   "critical",
		Component:  "xray",
		Source:     inventory.ProxyRuntimeStartFailureSource,
		Message:    "last PassWall proxy start failed in xray",
		ObservedAt: firstSeen.UTC().Format(time.RFC3339),
		Evidence:   `Failed to start: main: failed to load config files: [/tmp/etc/passwall2/acl/default/global.json] > infra/conf: failed to build outbound config with tag dns-out`,
	}
}

func proxyRuntimeMissingEvent() controlplane.RouterSafetyEvent {
	return controlplane.RouterSafetyEvent{
		Type:      "proxy_runtime_missing",
		Severity:  "critical",
		Component: "xray",
		Source:    "process",
		Message:   "PassWall2 is running but expected xray process is missing",
	}
}

func TestMaybeRestartPasswallWatchdogSkipsUnusableRuntime(t *testing.T) {
	cases := []struct {
		name        string
		reason      string
		events      []controlplane.RouterSafetyEvent
		wantRestart bool
	}{
		{
			name:   "runtime missing over a missing binary",
			reason: passwallWatchdogRuntimeReason,
			events: []controlplane.RouterSafetyEvent{proxyRuntimeMissingEvent(), missingXrayBinaryEvent()},
		},
		{
			name:   "connectivity over a failed xray start",
			reason: passwallWatchdogConnectivityReason,
			events: []controlplane.RouterSafetyEvent{failedXrayStartEvent()},
		},
		{
			name:   "service stopped over a missing binary",
			reason: passwallWatchdogServiceReason,
			events: []controlplane.RouterSafetyEvent{missingXrayBinaryEvent()},
		},
		{
			// Unchanged behaviour: a merely missing process is still restarted.
			name:        "runtime missing with a usable runtime",
			reason:      passwallWatchdogRuntimeReason,
			events:      []controlplane.RouterSafetyEvent{proxyRuntimeMissingEvent()},
			wantRestart: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeRescueBackend{}
			persisted := state.PersistedState{}
			inventory := controlplane.RouterInventory{
				PasswallEnabled: true,
				SelectedNodeID:  "myshunt",
				ServiceHealth:   controlplane.RouterServiceHealth{Passwall: "running"},
				SafetyEvents:    tc.events,
			}
			runtimeStatus := state.RuntimeStatus{}

			restarted, err := maybeRestartPasswallWatchdog(
				context.Background(),
				baseRescueConfig("http://panel.invalid"),
				backend,
				&persisted,
				&inventory,
				&runtimeStatus,
				time.Now().UTC(),
				tc.reason,
			)
			if err != nil {
				t.Fatalf("maybeRestartPasswallWatchdog returned error: %v", err)
			}
			if restarted != tc.wantRestart {
				t.Fatalf("restarted = %v, want %v (commands %#v)", restarted, tc.wantRestart, backend.runCommands)
			}
			if got, want := countCommand(backend.runCommands, "/etc/init.d/passwall2 restart"), map[bool]int{true: 1, false: 0}[tc.wantRestart]; got != want {
				t.Fatalf("PassWall restarts = %d, want %d", got, want)
			}
			if !tc.wantRestart && persisted.ControlPlaneRecovery.PasswallWatchdogRestartCount != 0 {
				t.Fatalf("watchdog restart count = %d, want 0", persisted.ControlPlaneRecovery.PasswallWatchdogRestartCount)
			}
		})
	}
}

func TestEvaluateLocalRescueFallsBackToDirectWhenRuntimeUnusable(t *testing.T) {
	cases := []struct {
		name       string
		event      controlplane.RouterSafetyEvent
		wantReason string
	}{
		{
			name:       "missing xray binary",
			event:      missingXrayBinaryEvent(),
			wantReason: "Proxy runtime cannot start (xray binary /usr/bin/xray missing); router switched to direct so LAN traffic is not black-holed.",
		},
		{
			name:       "failed xray start",
			event:      failedXrayStartEvent(),
			wantReason: "Proxy runtime cannot start (last PassWall proxy start failed in xray: Failed to start: main: failed to load config files:",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverProbe := newHTTPTestServer(http.StatusNoContent)
			defer serverProbe.Close()
			publicProbe := newHTTPTestServer(http.StatusServiceUnavailable)
			defer publicProbe.Close()

			backend := &fakeRescueBackend{
				runResults: map[string]passwall.CommandResult{
					"/usr/share/passwall2/test.sh url_test_node node-1": {Stdout: "204:0.1"},
				},
				protocols: map[string]string{
					"passwall2.myshunt.protocol":     "_shunt",
					"passwall2.myshunt.default_node": "node-1",
					"passwall2.node-1.protocol":      "vless",
				},
			}
			rescueState := rescue.State{Mode: rescue.ModeProxy}
			persisted := state.PersistedState{}
			inventory := controlplane.RouterInventory{
				PasswallEnabled: true,
				SelectedNodeID:  "myshunt",
				ServiceHealth:   controlplane.RouterServiceHealth{Passwall: "running"},
				SafetyEvents:    []controlplane.RouterSafetyEvent{proxyRuntimeMissingEvent(), tc.event},
			}
			runtimeStatus := state.RuntimeStatus{}

			transitioned, health, err := evaluateLocalRescue(
				context.Background(),
				baseRescueConfig(serverProbe.URL, publicProbe.URL),
				backend,
				&rescueState,
				&persisted,
				&inventory,
				&runtimeStatus,
			)
			if err != nil {
				t.Fatalf("evaluateLocalRescue returned error: %v", err)
			}
			if !transitioned || health.CurrentMode != "direct" || rescueState.Mode != rescue.ModeDirect {
				t.Fatalf("expected a transition to direct, got transitioned=%v health=%+v state=%+v", transitioned, health, rescueState)
			}
			if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='0'") {
				t.Fatalf("expected PassWall to be switched off, got %#v", backend.batchCommands)
			}
			if containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
				t.Fatalf("PassWall must not be switched back on, got %#v", backend.batchCommands)
			}
			if !strings.HasPrefix(persisted.Rescue.LastReason, tc.wantReason) {
				t.Fatalf("rescue reason = %q, want prefix %q", persisted.Rescue.LastReason, tc.wantReason)
			}
			if !containsBatchLine(backend.batchCommands, "set vectra-controller.main.last_rescue_reason='"+persisted.Rescue.LastReason+"'") {
				t.Fatalf("expected the reason to reach LuCI, got %#v", backend.batchCommands)
			}
			if persisted.Rescue.LastMode != string(rescue.ModeDirect) || persisted.Rescue.HappenedAt == "" {
				t.Fatalf("rescue metadata not recorded: %+v", persisted.Rescue)
			}
			if inventory.LastRescue == nil || inventory.LastRescue.Reason != persisted.Rescue.LastReason {
				t.Fatalf("expected the rescue to be reported to the panel, got %+v", inventory.LastRescue)
			}
			// The only restart is the one that applies enabled=0; no watchdog
			// restart over the broken runtime, no probing it.
			if got := countCommand(backend.runCommands, "/etc/init.d/passwall2 restart"); got != 1 {
				t.Fatalf("PassWall restarts = %d, want exactly the one applying direct mode (%#v)", got, backend.runCommands)
			}
			if persisted.ControlPlaneRecovery.PasswallWatchdogRestartCount != 0 {
				t.Fatalf("watchdog restarted PassWall %d time(s)", persisted.ControlPlaneRecovery.PasswallWatchdogRestartCount)
			}
			if containsCommand(backend.runCommands, "/usr/share/passwall2/test.sh") {
				t.Fatalf("did not expect proxy probes over a broken runtime, got %#v", backend.runCommands)
			}
		})
	}
}

func TestEvaluateLocalRescueStaysDirectWhileRuntimeUnusable(t *testing.T) {
	cases := []struct {
		name          string
		events        []controlplane.RouterSafetyEvent
		wantBackProxy bool
	}{
		{
			name:   "runtime unusable",
			events: []controlplane.RouterSafetyEvent{failedXrayStartEvent()},
		},
		{
			// Control: the same router with a usable runtime goes back to proxy.
			name:          "runtime usable",
			wantBackProxy: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverProbe := newHTTPTestServer(http.StatusNoContent)
			defer serverProbe.Close()
			publicProbe := newHTTPTestServer(http.StatusNoContent)
			defer publicProbe.Close()

			backend := &fakeRescueBackend{
				runResults: map[string]passwall.CommandResult{
					"/usr/share/passwall2/test.sh url_test_node node-1": {Stdout: "204:0.1"},
				},
				protocols: map[string]string{
					"passwall2.node-1.protocol": "vless",
				},
			}
			// Proxy credit earned before the runtime broke must not carry it back.
			rescueState := rescue.State{
				Mode:              rescue.ModeDirect,
				ProxySuccessCount: 5,
				LastTransitionAt:  time.Now().Add(-time.Hour),
			}
			persisted := state.PersistedState{}
			inventory := controlplane.RouterInventory{
				PasswallEnabled: false,
				SelectedNodeID:  "node-1",
				SafetyEvents:    tc.events,
			}
			runtimeStatus := state.RuntimeStatus{}

			transitioned, _, err := evaluateLocalRescue(
				context.Background(),
				baseRescueConfig(serverProbe.URL, publicProbe.URL),
				backend,
				&rescueState,
				&persisted,
				&inventory,
				&runtimeStatus,
			)
			if err != nil {
				t.Fatalf("evaluateLocalRescue returned error: %v", err)
			}
			enabled := containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'")
			if transitioned != tc.wantBackProxy || enabled != tc.wantBackProxy {
				t.Fatalf("back to proxy: transitioned=%v enabled=%v, want %v (batches %#v)", transitioned, enabled, tc.wantBackProxy, backend.batchCommands)
			}
			if tc.wantBackProxy {
				return
			}
			if rescueState.Mode != rescue.ModeDirect || rescueState.ProxySuccessCount != 0 {
				t.Fatalf("expected to stay direct with no proxy credit, got %+v", rescueState)
			}
			if len(backend.batchCommands) != 0 {
				t.Fatalf("expected no UCI writes, got %#v", backend.batchCommands)
			}
			if containsCommand(backend.runCommands, "/usr/share/passwall2/test.sh") {
				t.Fatalf("did not expect proxy probes over a broken runtime, got %#v", backend.runCommands)
			}
		})
	}
}

func TestValidateDirectFallbackLeavesPassWallOffOverUnusableRuntime(t *testing.T) {
	cases := []struct {
		name        string
		events      []controlplane.RouterSafetyEvent
		wantRestore bool
	}{
		{name: "runtime unusable", events: []controlplane.RouterSafetyEvent{missingXrayBinaryEvent()}},
		{name: "runtime usable", wantRestore: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			publicProbe := newHTTPTestServer(http.StatusNoContent)
			defer publicProbe.Close()

			backend := &fakeRescueBackend{}
			inventory := controlplane.RouterInventory{PasswallEnabled: true, SafetyEvents: tc.events}

			reachable, err := validateDirectFallback(
				context.Background(),
				baseRescueConfig("http://panel.invalid", publicProbe.URL),
				backend,
				&inventory,
			)
			if err != nil {
				t.Fatalf("validateDirectFallback returned error: %v", err)
			}
			if !reachable {
				t.Fatal("expected the direct probe to succeed")
			}
			if got := containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'"); got != tc.wantRestore {
				t.Fatalf("PassWall restored = %v, want %v (batches %#v)", got, tc.wantRestore, backend.batchCommands)
			}
		})
	}
}

// operator_attention's periodic retry (GAP-5) must not re-arm the black hole,
// and must fire as soon as the runtime changes.
func TestAdvanceControlPlaneRecoveryOperatorAttentionDoesNotRetryOverUnusableRuntime(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	blocked := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer blocked.Close()
	setRecoveryProbeTargets(t,
		[]probeTarget{{ID: "ya", Label: "ya.ru", URL: ru.URL}},
		[]probeTarget{{ID: "youtube", Label: "youtube", URL: blocked.URL}},
	)

	backend := &fakeRescueBackend{}
	now := time.Now()
	staleRetryAt := recovery.FormatTime(now.Add(-13 * time.Hour))
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-6 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-5 * time.Hour)),
		Phase:                        recovery.PhaseOperatorAttention,
		AwaitingOperator:             true,
		LastActionReason:             operatorAttentionReason,
		LastPasswallRetryAt:          staleRetryAt,
	}}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: false,
		SafetyEvents:    []controlplane.RouterSafetyEvent{missingXrayBinaryEvent()},
	}
	runtimeStatus := state.RuntimeStatus{}

	outcome, err := advanceControlPlaneRecovery(
		context.Background(),
		baseControlPlaneRecoveryConfig(panel.URL),
		backend,
		&persisted.ControlPlaneRecovery,
		&rescueState,
		&persisted,
		&inventory,
		&runtimeStatus,
	)
	if err != nil {
		t.Fatalf("advanceControlPlaneRecovery returned error: %v", err)
	}
	if outcome.InventoryChanged || len(backend.batchCommands) != 0 {
		t.Fatalf("expected no PassWall retry over a broken runtime, got batches %#v", backend.batchCommands)
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseOperatorAttention; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if !persisted.ControlPlaneRecovery.AwaitingOperator || rescueState.Mode != rescue.ModeDirect {
		t.Fatalf("expected to stay parked in direct awaiting the operator, got %+v / %+v", persisted.ControlPlaneRecovery, rescueState)
	}
	if persisted.ControlPlaneRecovery.LastPasswallRetryAt != staleRetryAt {
		t.Fatal("a skipped retry must not consume the retry budget")
	}
	if reason := persisted.ControlPlaneRecovery.LastActionReason; !strings.Contains(reason, "xray binary /usr/bin/xray missing") ||
		!strings.Contains(reason, "automatic PassWall retry skipped") {
		t.Fatalf("expected the recovery reason to name the runtime, got %q", reason)
	}

	// The binary is back: the retry that was due fires on the next tick.
	inventory.SafetyEvents = nil
	outcome, err = advanceControlPlaneRecovery(
		context.Background(),
		baseControlPlaneRecoveryConfig(panel.URL),
		backend,
		&persisted.ControlPlaneRecovery,
		&rescueState,
		&persisted,
		&inventory,
		&runtimeStatus,
	)
	if err != nil {
		t.Fatalf("second advanceControlPlaneRecovery returned error: %v", err)
	}
	if !outcome.InventoryChanged || !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected the retry once the runtime changed, got batches %#v", backend.batchCommands)
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhasePasswallRetryWait; got != want {
		t.Fatalf("recovery phase after the runtime changed = %q, want %q", got, want)
	}
}

func TestAdvanceControlPlaneRecoveryDirectSettleDoesNotAutoRetryOverUnusableRuntime(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusNoContent})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	blocked := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer blocked.Close()
	setRecoveryProbeTargets(t,
		[]probeTarget{{ID: "ya", Label: "ya.ru", URL: ru.URL}},
		[]probeTarget{{ID: "youtube", Label: "youtube", URL: blocked.URL}},
	)

	backend := &fakeRescueBackend{}
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseDirectSettle,
		LastActionReason:             controlPlaneDirectReason,
	}}
	rescueState := rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: now.Add(-2 * time.Minute)}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: false,
		SafetyEvents:    []controlplane.RouterSafetyEvent{failedXrayStartEvent()},
	}
	runtimeStatus := state.RuntimeStatus{}

	outcome, err := advanceControlPlaneRecovery(
		context.Background(),
		baseControlPlaneRecoveryConfig(panel.URL),
		backend,
		&persisted.ControlPlaneRecovery,
		&rescueState,
		&persisted,
		&inventory,
		&runtimeStatus,
	)
	if err != nil {
		t.Fatalf("advanceControlPlaneRecovery returned error: %v", err)
	}
	if outcome.InventoryChanged || len(backend.batchCommands) != 0 {
		t.Fatalf("expected no PassWall retry over a broken runtime, got batches %#v", backend.batchCommands)
	}
	if outcome.SkipControlPlane {
		t.Fatal("the panel is reachable: check-in must resume so the operator sees the router")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseOperatorAttention; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if !strings.Contains(persisted.ControlPlaneRecovery.LastActionReason, "automatic PassWall retry skipped") {
		t.Fatalf("expected the recovery reason to name the runtime, got %q", persisted.ControlPlaneRecovery.LastActionReason)
	}
}

func TestAdvanceControlPlaneRecoveryDoesNotResumeOwnFailSafeDirect(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusNoContent})
	defer panel.Close()

	backend := &fakeRescueBackend{}
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-1 * time.Minute)),
		Phase:                        recovery.PhaseIdle,
	}}
	rescueState := rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: now.Add(-20 * time.Minute)}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: false,
		SafetyEvents:    []controlplane.RouterSafetyEvent{missingXrayBinaryEvent()},
	}
	runtimeStatus := state.RuntimeStatus{}

	outcome, err := advanceControlPlaneRecovery(
		context.Background(),
		baseControlPlaneRecoveryConfig(panel.URL),
		backend,
		&persisted.ControlPlaneRecovery,
		&rescueState,
		&persisted,
		&inventory,
		&runtimeStatus,
	)
	if err != nil {
		t.Fatalf("advanceControlPlaneRecovery returned error: %v", err)
	}
	if outcome.InventoryChanged || len(backend.batchCommands) != 0 {
		t.Fatalf("expected no proxy resume over a broken runtime, got batches %#v", backend.batchCommands)
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseIdle; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
}

// Operator authority: the reconnect job resumes proxy even over a broken
// runtime, it only warns. This is the sequence the reconnect case runs.
func TestReconnectResumeOverUnusableRuntimeWarnsButStillResumes(t *testing.T) {
	backend := &fakeRescueBackend{}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	persisted := state.PersistedState{}
	runtimeStatus := state.RuntimeStatus{}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: false,
		SafetyEvents:    []controlplane.RouterSafetyEvent{missingXrayBinaryEvent()},
	}

	warning := operatorProxyResumeWarning(&inventory, "reconnect job")
	if !strings.Contains(warning, "reconnect job re-enables PassWall on operator authority") ||
		!strings.Contains(warning, "xray binary /usr/bin/xray missing") {
		t.Fatalf("unexpected warning %q", warning)
	}
	if err := resumeProxyMode(context.Background(), backend, &rescueState, &persisted, &runtimeStatus, time.Now().UTC()); err != nil {
		t.Fatalf("resumeProxyMode returned error: %v", err)
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") || rescueState.Mode != rescue.ModeProxy {
		t.Fatalf("expected the operator resume to go through, got batches %#v state %+v", backend.batchCommands, rescueState)
	}

	inventory.SafetyEvents = nil
	if warning := operatorProxyResumeWarning(&inventory, "reconnect job"); warning != "" {
		t.Fatalf("did not expect a warning over a usable runtime, got %q", warning)
	}
}

// A missing binary blocks every automatic re-enable until it is back. A failed
// start blocks for one RebootCooldown from when it was first seen: config and
// geodata can be fixed in ways no fingerprint sees, so it must never block
// forever.
func TestProxyRuntimeBlocksAutoResume(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	policy := rescue.Policy{RebootCooldown: 12 * time.Hour}
	noFirstSeen := failedXrayStartEventFirstSeen(now)
	noFirstSeen.ObservedAt = ""
	oldMissing := missingXrayBinaryEvent()
	oldMissing.ObservedAt = now.Add(-72 * time.Hour).Format(time.RFC3339)

	cases := []struct {
		name        string
		events      []controlplane.RouterSafetyEvent
		wantBlocked bool
	}{
		{name: "usable runtime"},
		{name: "missing binary", events: []controlplane.RouterSafetyEvent{missingXrayBinaryEvent()}, wantBlocked: true},
		{name: "missing binary for days", events: []controlplane.RouterSafetyEvent{oldMissing}, wantBlocked: true},
		{name: "failed start first seen an hour ago", events: []controlplane.RouterSafetyEvent{failedXrayStartEventFirstSeen(now.Add(-time.Hour))}, wantBlocked: true},
		{name: "failed start first seen a RebootCooldown ago", events: []controlplane.RouterSafetyEvent{failedXrayStartEventFirstSeen(now.Add(-12 * time.Hour))}},
		{name: "failed start without a first-seen time", events: []controlplane.RouterSafetyEvent{noFirstSeen}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inventory := controlplane.RouterInventory{SafetyEvents: tc.events}
			if _, blocked := proxyRuntimeBlocksAutoResume(&inventory, policy, now); blocked != tc.wantBlocked {
				t.Fatalf("blocked = %v, want %v", blocked, tc.wantBlocked)
			}
		})
	}
}

// Once the RebootCooldown has passed, local rescue gets its say again over a
// failed start -- but never over a missing binary.
func TestEvaluateLocalRescueRetriesAFailedStartAfterRebootCooldown(t *testing.T) {
	cases := []struct {
		name      string
		event     controlplane.RouterSafetyEvent
		wantRetry bool
	}{
		{name: "failed start first seen 13 hours ago", event: failedXrayStartEventFirstSeen(time.Now().Add(-13 * time.Hour)), wantRetry: true},
		{name: "failed start first seen an hour ago", event: failedXrayStartEventFirstSeen(time.Now().Add(-time.Hour))},
		{name: "binary missing", event: missingXrayBinaryEvent()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			serverProbe := newHTTPTestServer(http.StatusNoContent)
			defer serverProbe.Close()
			publicProbe := newHTTPTestServer(http.StatusNoContent)
			defer publicProbe.Close()

			backend := &fakeRescueBackend{
				runResults: map[string]passwall.CommandResult{
					"/usr/share/passwall2/test.sh url_test_node node-1": {Stdout: "204:0.1"},
				},
				protocols: map[string]string{"passwall2.node-1.protocol": "vless"},
			}
			cfg := baseRescueConfig(serverProbe.URL, publicProbe.URL)
			cfg.Rescue.RebootCooldown = 12 * time.Hour
			rescueState := rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: time.Now().Add(-time.Hour)}
			persisted := state.PersistedState{}
			inventory := controlplane.RouterInventory{
				PasswallEnabled: false,
				SelectedNodeID:  "node-1",
				SafetyEvents:    []controlplane.RouterSafetyEvent{tc.event},
			}
			runtimeStatus := state.RuntimeStatus{}

			transitioned, _, err := evaluateLocalRescue(context.Background(), cfg, backend, &rescueState, &persisted, &inventory, &runtimeStatus)
			if err != nil {
				t.Fatalf("evaluateLocalRescue returned error: %v", err)
			}
			retried := containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'")
			if transitioned != tc.wantRetry || retried != tc.wantRetry {
				t.Fatalf("retry: transitioned=%v enabled=%v, want %v (batches %#v)", transitioned, retried, tc.wantRetry, backend.batchCommands)
			}
		})
	}
}

// L1: the operator-attention retry with a live panel probes the node through
// test.sh and a temporary xray; with a runtime that cannot run it must not
// spawn them at all, every poll.
func TestAdvanceControlPlaneRecoveryOperatorAttentionChecksTheRuntimeBeforeProbingTheNode(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusNoContent})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	setRecoveryProbeTargets(t,
		[]probeTarget{{ID: "ya", Label: "ya.ru", URL: ru.URL}},
		[]probeTarget{{ID: "youtube", Label: "youtube", URL: ru.URL}},
	)

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/usr/share/passwall2/test.sh url_test_node node-1": {Stdout: "204:0.1"},
		},
		protocols: map[string]string{"passwall2.node-1.protocol": "vless"},
	}
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-time.Minute)),
		Phase:                        recovery.PhaseOperatorAttention,
		AwaitingOperator:             true,
		LastActionReason:             operatorAttentionReason,
		LastPasswallRetryAt:          recovery.FormatTime(now.Add(-13 * time.Hour)),
	}}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: false,
		SelectedNodeID:  "node-1",
		SafetyEvents:    []controlplane.RouterSafetyEvent{missingXrayBinaryEvent()},
	}
	runtimeStatus := state.RuntimeStatus{}

	if _, err := advanceControlPlaneRecovery(
		context.Background(),
		baseControlPlaneRecoveryConfig(panel.URL),
		backend,
		&persisted.ControlPlaneRecovery,
		&rescueState,
		&persisted,
		&inventory,
		&runtimeStatus,
	); err != nil {
		t.Fatalf("advanceControlPlaneRecovery returned error: %v", err)
	}
	if containsCommand(backend.runCommands, "/usr/share/passwall2/test.sh") {
		t.Fatalf("did not expect a node probe over a runtime that cannot run, got %#v", backend.runCommands)
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no PassWall retry, got %#v", backend.batchCommands)
	}
	if !strings.Contains(persisted.ControlPlaneRecovery.LastActionReason, "automatic PassWall retry skipped") {
		t.Fatalf("expected the recovery reason to name the runtime, got %q", persisted.ControlPlaneRecovery.LastActionReason)
	}
}

// H2 at the recovery level: the operator-attention retry goes ahead over a
// failed start once the RebootCooldown has passed since it was first seen.
func TestAdvanceControlPlaneRecoveryOperatorAttentionRetriesAFailedStartAfterRebootCooldown(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	blocked := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer blocked.Close()
	setRecoveryProbeTargets(t,
		[]probeTarget{{ID: "ya", Label: "ya.ru", URL: blocked.URL}},
		[]probeTarget{{ID: "youtube", Label: "youtube", URL: blocked.URL}},
	)

	backend := &fakeRescueBackend{}
	now := time.Now()
	persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-20 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-19 * time.Hour)),
		Phase:                        recovery.PhaseOperatorAttention,
		AwaitingOperator:             true,
		LastActionReason:             operatorAttentionReason,
		LastPasswallRetryAt:          recovery.FormatTime(now.Add(-13 * time.Hour)),
	}}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: false,
		SafetyEvents:    []controlplane.RouterSafetyEvent{failedXrayStartEventFirstSeen(now.Add(-13 * time.Hour))},
	}
	runtimeStatus := state.RuntimeStatus{}

	outcome, err := advanceControlPlaneRecovery(
		context.Background(),
		baseControlPlaneRecoveryConfig(panel.URL),
		backend,
		&persisted.ControlPlaneRecovery,
		&rescueState,
		&persisted,
		&inventory,
		&runtimeStatus,
	)
	if err != nil {
		t.Fatalf("advanceControlPlaneRecovery returned error: %v", err)
	}
	if !outcome.InventoryChanged || !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected the periodic retry over a stale failed start, got batches %#v", backend.batchCommands)
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhasePasswallRetryWait; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
}

// M4: while control-plane recovery owns PassWall, evaluateLocalRescue does not
// run and recovery only acts on a disabled PassWall. PassWall switched on over
// a broken runtime in those phases has to be switched off by this fail-safe.
func TestFailSafeUnusableRuntimeUnderRecovery(t *testing.T) {
	cases := []struct {
		name         string
		enabled      bool
		events       []controlplane.RouterSafetyEvent
		wantSwitched bool
	}{
		{name: "enabled over a missing binary", enabled: true, events: []controlplane.RouterSafetyEvent{missingXrayBinaryEvent()}, wantSwitched: true},
		{
			// The periodic retry is about re-enabling; a failed start is never
			// left enabled.
			name:         "enabled over a failed start past the retry window",
			enabled:      true,
			events:       []controlplane.RouterSafetyEvent{failedXrayStartEventFirstSeen(time.Now().Add(-13 * time.Hour))},
			wantSwitched: true,
		},
		{name: "already disabled", events: []controlplane.RouterSafetyEvent{missingXrayBinaryEvent()}},
		{name: "enabled over a usable runtime", enabled: true, events: []controlplane.RouterSafetyEvent{proxyRuntimeMissingEvent()}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeRescueBackend{}
			rescueState := rescue.State{Mode: rescue.ModeProxy}
			persisted := state.PersistedState{ControlPlaneRecovery: recovery.State{
				Phase:            recovery.PhaseOperatorAttention,
				AwaitingOperator: true,
				LastActionReason: operatorAttentionReason,
			}}
			inventory := controlplane.RouterInventory{PasswallEnabled: tc.enabled, SafetyEvents: tc.events}
			runtimeStatus := state.RuntimeStatus{}

			switched, err := failSafeUnusableRuntimeUnderRecovery(
				context.Background(), backend, &rescueState, &persisted, &inventory, &runtimeStatus, time.Now().UTC(),
			)
			if err != nil {
				t.Fatalf("failSafeUnusableRuntimeUnderRecovery returned error: %v", err)
			}
			if switched != tc.wantSwitched {
				t.Fatalf("switched = %v, want %v (batches %#v)", switched, tc.wantSwitched, backend.batchCommands)
			}
			if !tc.wantSwitched {
				if len(backend.batchCommands) != 0 || len(backend.runCommands) != 0 {
					t.Fatalf("expected no action, got batches %#v commands %#v", backend.batchCommands, backend.runCommands)
				}
				return
			}
			if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='0'") {
				t.Fatalf("expected PassWall to be switched off, got %#v", backend.batchCommands)
			}
			if rescueState.Mode != rescue.ModeDirect || !strings.HasPrefix(persisted.Rescue.LastReason, "Proxy runtime cannot start (") {
				t.Fatalf("expected a recorded fallback to direct, got state %+v rescue %+v", rescueState, persisted.Rescue)
			}
			if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseOperatorAttention; got != want {
				t.Fatalf("recovery phase = %q, want it left at %q", got, want)
			}
		})
	}
}

// The start-failure memory lives in the persisted state so that a controller
// restart neither forgets a failure nor re-records it against whatever binary
// is installed by then. This runs two full cycles with a restart in between:
// the memory the collector recorded in the first reaches the collector again
// in the second, through state.json only.
func TestRunOnceKeepsTheProxyStartFailureMemoryAcrossRestarts(t *testing.T) {
	var checkIns atomic.Int32
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/health":
			w.WriteHeader(http.StatusNoContent)
		case "/api/router/check-in":
			checkIns.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"routerId":"router-1","status":"active","jobs":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer panel.Close()
	public := newHTTPTestServer(http.StatusNoContent)
	defer public.Close()

	dir := t.TempDir()
	cfg := &config.Config{
		ControlURL:     panel.URL,
		RouterID:       "router-1",
		AgentToken:     "token-1",
		StatePath:      filepath.Join(dir, "state.json"),
		StatusPath:     filepath.Join(dir, "status.json"),
		RequestTimeout: time.Second,
		// Keeps the per-cycle route-policy self-heal, which shells out to uci,
		// out of this test.
		ManualMode: true,
		Rescue:     rescue.Policy{HealthURLs: []string{public.URL}},
	}
	cfg.Rescue.Normalize()

	firstSeen := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	recorded := inventory.ProxyRuntimeStartFailure{
		Attempt:    "2026-09-28 23:34:23: Running complete!",
		Evidence:   failedXrayStartEvent().Evidence,
		Runtime:    "xray /usr/bin/xray size=31457280 mtime=2026-09-20T10:00:00Z; luci-app-passwall2 26.4.10-r1",
		ObservedAt: firstSeen.Format(time.RFC3339),
	}
	var handedIn []inventory.ProxyRuntimeStartFailure
	previousCollect := collectWithCollector
	t.Cleanup(func() { collectWithCollector = previousCollect })
	collectWithCollector = func(collector inventory.Collector, base controlplane.RouterInventory) controlplane.RouterInventory {
		// Stands in for the real collector (covered in internal/inventory):
		// records the failure the first time and reports it while remembered.
		handedIn = append(handedIn, *collector.ProxyRuntimeFailure)
		if collector.ProxyRuntimeFailure.Attempt == "" {
			*collector.ProxyRuntimeFailure = recorded
		}
		collected := base
		collected.PasswallEnabled = false
		collected.SelectedNodeID = "myshunt"
		collected.SafetyEvents = []controlplane.RouterSafetyEvent{failedXrayStartEventFirstSeen(firstSeen)}
		return collected
	}

	client := controlplane.NewClient(controlplane.Options{
		BaseURL:    panel.URL,
		RouterID:   "router-1",
		AgentToken: "token-1",
		Timeout:    time.Second,
	})
	persisted := state.PersistedState{RouterID: "router-1", AgentToken: "token-1"}
	rescueState := rescue.State{Mode: rescue.ModeDirect, LastTransitionAt: time.Now().Add(-time.Hour)}
	if err := runOnce(context.Background(), cfg, client, &rescueState, &persisted); err != nil {
		t.Fatalf("first runOnce returned error: %v", err)
	}
	if checkIns.Load() != 1 {
		t.Fatalf("expected the first cycle to check in once, got %d", checkIns.Load())
	}

	// Controller restart: nothing survives but the state file.
	reloaded, err := state.Load(cfg.StatePath)
	if err != nil {
		t.Fatalf("state.Load returned error: %v", err)
	}
	if reloaded.ProxyRuntimeFailure != recorded {
		t.Fatalf("persisted memory = %+v, want %+v", reloaded.ProxyRuntimeFailure, recorded)
	}
	reloadedRescue := reloaded.Rescue.State
	handedIn = nil
	if err := runOnce(context.Background(), cfg, client, &reloadedRescue, &reloaded); err != nil {
		t.Fatalf("second runOnce returned error: %v", err)
	}
	if len(handedIn) == 0 || handedIn[0] != recorded {
		t.Fatalf("the restarted controller did not hand the memory back to the collector: %+v", handedIn)
	}
	if reloadedRescue.Mode != rescue.ModeDirect {
		t.Fatalf("expected the router to stay direct over the remembered failure, got %q", reloadedRescue.Mode)
	}
}
