package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/recovery"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

func newStatusServer(statusByPath map[string]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, ok := statusByPath[r.URL.Path]
		if !ok {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
	}))
}

func baseControlPlaneRecoveryConfig(controlURL string) *config.Config {
	cfg := &config.Config{
		ControlURL:     controlURL,
		RequestTimeout: time.Second,
		Rescue: rescue.Policy{
			HealthURLs:               []string{"https://www.gstatic.com/generate_204"},
			TriggerFailureCount:      3,
			RecoverySuccessCount:     2,
			Cooldown:                 5 * time.Minute,
			RequireDirectPathSuccess: true,
			DirectModeReason:         "Subscription expired or upstream proxy unavailable",
			PanelOutageThreshold:     time.Hour,
			ProbeCacheTTL:            time.Minute,
			ControllerRestartSettle:  90 * time.Second,
			DirectSettle:             45 * time.Second,
			PostRebootSettle:         4 * time.Minute,
			PasswallWarmup:           75 * time.Second,
			RebootCooldown:           12 * time.Hour,
		},
	}
	cfg.Rescue.Normalize()
	return cfg
}

func setRecoveryProbeTargets(
	t *testing.T,
	ru []probeTarget,
	foreign []probeTarget,
) {
	t.Helper()

	originalRU := ruProbeTargets
	originalForeign := foreignProbeTargets
	ruProbeTargets = ru
	foreignProbeTargets = foreign
	resetControlPlaneProbeCache()

	t.Cleanup(func() {
		ruProbeTargets = originalRU
		foreignProbeTargets = originalForeign
		resetControlPlaneProbeCache()
	})
}

func resetControlPlaneProbeCache() {
	clearControlPlaneReachabilityCache()
}

func TestAdvanceControlPlaneRecoveryControllerRestartOnly(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	foreignA := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer foreignA.Close()
	foreignB := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer foreignB.Close()
	foreignC := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer foreignC.Close()

	setRecoveryProbeTargets(t,
		[]probeTarget{
			{ID: "ya", Label: "ya.ru", URL: ru.URL},
			{ID: "vk", Label: "vk.com", URL: ru.URL},
		},
		[]probeTarget{
			{ID: "youtube", Label: "youtube", URL: foreignA.URL},
			{ID: "instagram", Label: "instagram", URL: foreignB.URL},
			{ID: "telegram", Label: "telegram", URL: foreignC.URL},
		},
	)

	backend := &fakeRescueBackend{}
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseMonitoring,
	}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{PasswallEnabled: true}
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

	if !outcome.SkipControlPlane {
		t.Fatal("expected control plane work to be skipped during controller restart wait")
	}
	if outcome.InventoryChanged {
		t.Fatal("did not expect inventory-changing direct-mode transition")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseControllerRestartWait; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no UCI writes, got %#v", backend.batchCommands)
	}
	if !containsCommand(backend.runCommands, "sh -c (sleep 2; /etc/init.d/vectra-controller restart >/tmp/vectra-controller-recovery.log 2>&1) &") {
		t.Fatalf("expected controller restart command, got %#v", backend.runCommands)
	}
}

func TestAdvanceControlPlaneRecoveryLeavesRestartWaitAfterSettleWindow(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	foreignA := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer foreignA.Close()
	foreignB := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer foreignB.Close()
	foreignC := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer foreignC.Close()

	setRecoveryProbeTargets(t,
		[]probeTarget{
			{ID: "ya", Label: "ya.ru", URL: ru.URL},
			{ID: "vk", Label: "vk.com", URL: ru.URL},
		},
		[]probeTarget{
			{ID: "youtube", Label: "youtube", URL: foreignA.URL},
			{ID: "instagram", Label: "instagram", URL: foreignB.URL},
			{ID: "telegram", Label: "telegram", URL: foreignC.URL},
		},
	)

	cfg := baseControlPlaneRecoveryConfig(panel.URL)
	cfg.PollInterval = 30 * time.Second
	backend := &fakeRescueBackend{}
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseControllerRestartWait,
		LastControllerRestartAt:      recovery.FormatTime(time.Now().Add(-3 * time.Minute)),
	}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{PasswallEnabled: true}
	runtimeStatus := state.RuntimeStatus{}

	outcome, err := advanceControlPlaneRecovery(
		context.Background(),
		cfg,
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

	if !outcome.SkipControlPlane {
		t.Fatal("expected control-plane work to remain skipped while panel is still down")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseMonitoring; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if len(backend.runCommands) != 0 {
		t.Fatalf("expected no second controller restart, got %#v", backend.runCommands)
	}
}

func TestAdvanceControlPlaneRecoverySwitchesDirectWhenForeignBlocked(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	blocked := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer blocked.Close()

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

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/etc/init.d/passwall2 restart": {Stdout: "restarted"},
		},
	}
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseMonitoring,
	}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{PasswallEnabled: true}
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

	if !outcome.InventoryChanged {
		t.Fatal("expected passwall toggle to require inventory recollect")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseDirectSettle; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if got, want := rescueState.Mode, rescue.ModeDirect; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='0'") {
		t.Fatalf("expected passwall disable batch, got %#v", backend.batchCommands)
	}
}

func TestAdvanceControlPlaneRecoveryDoesNotEscalateWhenOnlyRUIsBlocked(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer ru.Close()
	foreign := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer foreign.Close()

	setRecoveryProbeTargets(t,
		[]probeTarget{
			{ID: "ya", Label: "ya.ru", URL: ru.URL},
			{ID: "vk", Label: "vk.com", URL: ru.URL},
		},
		[]probeTarget{
			{ID: "youtube", Label: "youtube", URL: foreign.URL},
			{ID: "instagram", Label: "instagram", URL: foreign.URL},
			{ID: "telegram", Label: "telegram", URL: foreign.URL},
		},
	)

	backend := &fakeRescueBackend{}
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseMonitoring,
	}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{PasswallEnabled: true}
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

	if !outcome.SkipControlPlane {
		t.Fatal("expected control-plane work to remain skipped while panel is still down")
	}
	if outcome.InventoryChanged {
		t.Fatal("did not expect direct-mode transition when only RU probes are blocked")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseMonitoring; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if got, want := rescueState.Mode, rescue.ModeProxy; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no UCI writes, got %#v", backend.batchCommands)
	}
	if len(backend.runCommands) != 0 {
		t.Fatalf("expected no shell commands, got %#v", backend.runCommands)
	}
}

func TestAdvanceControlPlaneRecoveryInvalidatesReachabilityCacheAfterEnteringDirectMode(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	foreignBlocked := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer foreignBlocked.Close()
	foreignHealthy := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer foreignHealthy.Close()

	setRecoveryProbeTargets(t,
		[]probeTarget{
			{ID: "ya", Label: "ya.ru", URL: ru.URL},
			{ID: "vk", Label: "vk.com", URL: ru.URL},
		},
		[]probeTarget{
			{ID: "youtube", Label: "youtube", URL: foreignBlocked.URL},
			{ID: "instagram", Label: "instagram", URL: foreignBlocked.URL},
			{ID: "telegram", Label: "telegram", URL: foreignBlocked.URL},
		},
	)

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/etc/init.d/passwall2 restart": {Stdout: "restarted"},
		},
	}
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseMonitoring,
	}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{PasswallEnabled: true}
	runtimeStatus := state.RuntimeStatus{}

	firstOutcome, err := advanceControlPlaneRecovery(
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
		t.Fatalf("first advanceControlPlaneRecovery returned error: %v", err)
	}
	if !firstOutcome.InventoryChanged {
		t.Fatal("expected first pass to disable PassWall")
	}

	foreignProbeTargets = []probeTarget{
		{ID: "youtube", Label: "youtube", URL: foreignHealthy.URL},
		{ID: "instagram", Label: "instagram", URL: foreignHealthy.URL},
		{ID: "telegram", Label: "telegram", URL: foreignHealthy.URL},
	}
	backend.runCommands = nil
	inventory.PasswallEnabled = false
	rescueState.LastTransitionAt = time.Now().Add(-2 * time.Minute)

	secondOutcome, err := advanceControlPlaneRecovery(
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

	if !secondOutcome.SkipControlPlane {
		t.Fatal("expected control-plane work to stay paused while panel is still down")
	}
	if got, want := persisted.ControlPlaneRecovery.LastForeignStatus, recovery.StatusHealthy; got != want {
		t.Fatalf("foreign status = %q, want %q", got, want)
	}
	if containsCommand(backend.runCommands, "/sbin/reboot") {
		t.Fatalf("expected no reboot scheduling after foreign probes recovered, got %#v", backend.runCommands)
	}
}

func TestAdvanceControlPlaneRecoverySchedulesSingleRebootWithinBudget(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	blocked := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer blocked.Close()

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

	backend := &fakeRescueBackend{}
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseDirectSettle,
		LastRUStatus:                 recovery.StatusReachable,
		LastForeignStatus:            recovery.StatusBlocked,
		LastPanelStatus:              recovery.StatusBlocked,
	}
	rescueState := rescue.State{
		Mode:             rescue.ModeDirect,
		LastTransitionAt: time.Now().Add(-2 * time.Minute),
	}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{PasswallEnabled: false}
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

	if !outcome.SkipControlPlane {
		t.Fatal("expected reboot scheduling to short-circuit control-plane work")
	}
	if persisted.ControlPlaneRecovery.LastAutoRebootAt == "" {
		t.Fatal("expected auto reboot timestamp to be recorded")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseRebootWait; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if !containsCommand(backend.runCommands, "sh -c set -eu\nlog_path=\"/tmp/vectra-router-reboot.log\"\n(sleep 5; /sbin/reboot) >\"$log_path\" 2>&1 &\nprintf 'router reboot scheduled\\n'") {
		t.Fatalf("expected reboot command, got %#v", backend.runCommands)
	}

	backend.runCommands = nil
	persisted.ControlPlaneRecovery.Phase = recovery.PhaseDirectSettle
	persisted.ControlPlaneRecovery.LastAutoRebootAt = recovery.FormatTime(time.Now().Add(-6 * time.Hour))
	rescueState.LastTransitionAt = time.Now().Add(-2 * time.Minute)

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
	if len(backend.runCommands) != 0 {
		t.Fatalf("expected reboot cooldown to block second reboot, got %#v", backend.runCommands)
	}
	if !outcome.SkipControlPlane {
		t.Fatal("expected control plane work to remain skipped while outage persists")
	}
}

func TestAdvanceControlPlaneRecoveryStartsDirectSettleWindowWhenAlreadyDirect(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	blocked := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer blocked.Close()

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

	backend := &fakeRescueBackend{}
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseMonitoring,
	}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	persisted := state.PersistedState{}
	inventory := controlplane.RouterInventory{PasswallEnabled: false}
	runtimeStatus := state.RuntimeStatus{
		RescueMode:      string(rescue.ModeDirect),
		PasswallEnabled: false,
	}

	outcome, err := advanceControlPlaneRecovery(
		context.Background(),
		baseControlPlaneRecoveryConfig(panel.URL),
		backend,
		&recoveryState,
		&rescueState,
		&persisted,
		&inventory,
		&runtimeStatus,
	)
	if err != nil {
		t.Fatalf("advanceControlPlaneRecovery returned error: %v", err)
	}

	if !outcome.SkipControlPlane {
		t.Fatal("expected control-plane work to stay paused in direct settle")
	}
	if outcome.InventoryChanged {
		t.Fatal("did not expect an extra passwall toggle when it is already disabled")
	}
	if got, want := recoveryState.Phase, recovery.PhaseMonitoring; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if !rescueState.LastTransitionAt.IsZero() {
		t.Fatalf("expected already-direct routers to keep existing rescue ownership, got %s", rescueState.LastTransitionAt)
	}
	if !persisted.Rescue.State.LastTransitionAt.IsZero() {
		t.Fatalf("expected no persisted rescue transition update, got %s", persisted.Rescue.State.LastTransitionAt)
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no UCI writes, got %#v", backend.batchCommands)
	}
	if len(backend.runCommands) != 0 {
		t.Fatalf("expected no shell commands, got %#v", backend.runCommands)
	}
}

func TestAdvanceControlPlaneRecoveryRetriesPasswallAfterReboot(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusNoContent})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	foreign := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer foreign.Close()

	setRecoveryProbeTargets(t,
		[]probeTarget{
			{ID: "ya", Label: "ya.ru", URL: ru.URL},
			{ID: "vk", Label: "vk.com", URL: ru.URL},
		},
		[]probeTarget{
			{ID: "youtube", Label: "youtube", URL: foreign.URL},
			{ID: "instagram", Label: "instagram", URL: foreign.URL},
			{ID: "telegram", Label: "telegram", URL: foreign.URL},
		},
	)

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/etc/init.d/passwall2 restart": {Stdout: "restarted"},
		},
	}
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhasePostRebootCheck,
		LastAutoRebootAt:             recovery.FormatTime(time.Now().Add(-10 * time.Minute)),
	}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{PasswallEnabled: false}
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

	if !outcome.InventoryChanged {
		t.Fatal("expected passwall retry to change inventory")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhasePasswallRetryWait; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if persisted.ControlPlaneRecovery.LastPasswallRetryAt == "" {
		t.Fatal("expected last passwall retry timestamp to be set")
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected passwall enable batch, got %#v", backend.batchCommands)
	}
}

func TestAdvanceControlPlaneRecoveryEscalatesToOperatorAttentionAfterFailedRetry(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusNoContent})
	defer panel.Close()
	ru := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ru.Close()
	blocked := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer blocked.Close()

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

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/etc/init.d/passwall2 restart": {Stdout: "restarted"},
		},
	}
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhasePasswallRetryWait,
		LastPasswallRetryAt:          recovery.FormatTime(time.Now().Add(-2 * time.Minute)),
	}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{PasswallEnabled: true}
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

	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseOperatorAttention; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if !persisted.ControlPlaneRecovery.AwaitingOperator {
		t.Fatal("expected operator attention flag to be set")
	}
	if got, want := rescueState.Mode, rescue.ModeDirect; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='0'") {
		t.Fatalf("expected passwall disable batch, got %#v", backend.batchCommands)
	}
	if outcome.SkipControlPlane {
		t.Fatal("expected panel reachability to allow check-in for operator attention reporting")
	}
}

func TestSummarizeReachabilityProbeMarksForeignPartialAsNonHealthy(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	probe := summarizeReachabilityProbe(
		"foreign",
		[]probeTarget{
			{ID: "youtube", Label: "youtube", URL: "https://youtube.test"},
			{ID: "instagram", Label: "instagram", URL: "https://instagram.test"},
			{ID: "telegram", Label: "telegram", URL: "https://telegram.test"},
		},
		[]rescue.HTTPProbeResult{
			{URL: "https://youtube.test", Reachable: true, CheckedAt: now},
			{URL: "https://instagram.test", Reachable: false, CheckedAt: now},
			{URL: "https://telegram.test", Reachable: false, CheckedAt: now},
		},
		recovery.StatusHealthy,
		recovery.StatusPartial,
		recovery.StatusBlocked,
		2,
		1,
	)

	if got, want := probe.Status, recovery.StatusPartial; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if probe.Reachable {
		t.Fatal("expected partial foreign probe to remain non-healthy")
	}
}

func TestNoteSuccessfulControlPlaneContactClearsOutageWindow(t *testing.T) {
	t.Parallel()

	persisted := &state.PersistedState{
		ControlPlaneRecovery: recovery.State{
			LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
			OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
			Phase:                        recovery.PhaseControllerRestartWait,
			LastAutoRebootAt:             recovery.FormatTime(time.Now().Add(-3 * time.Hour)),
			LastActionReason:             "waiting",
		},
	}
	runtimeStatus := &state.RuntimeStatus{}

	noteSuccessfulControlPlaneContact(persisted, runtimeStatus, time.Now().UTC())

	if persisted.ControlPlaneRecovery.OutageStartedAt != "" {
		t.Fatal("expected outage window to be cleared")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseIdle; got != want {
		t.Fatalf("phase = %q, want %q", got, want)
	}
	if persisted.ControlPlaneRecovery.LastAutoRebootAt == "" {
		t.Fatal("expected reboot timestamp budget to be preserved")
	}
}

func TestClearControlPlaneRecoveryOwnershipClearsStickyOperatorAttention(t *testing.T) {
	t.Parallel()

	persisted := &state.PersistedState{
		ControlPlaneRecovery: recovery.State{
			LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
			OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
			Phase:                        recovery.PhaseOperatorAttention,
			AwaitingOperator:             true,
			LastActionReason:             operatorAttentionReason,
			LastPanelStatus:              recovery.StatusBlocked,
			LastRUStatus:                 recovery.StatusReachable,
			LastForeignStatus:            recovery.StatusBlocked,
			LastControllerRestartAt:      recovery.FormatTime(time.Now().Add(-30 * time.Minute)),
			LastPasswallRetryAt:          recovery.FormatTime(time.Now().Add(-10 * time.Minute)),
			LastAutoRebootAt:             recovery.FormatTime(time.Now().Add(-3 * time.Hour)),
		},
	}
	runtimeStatus := &state.RuntimeStatus{
		LastPanelStatus:    recovery.StatusBlocked,
		LastRUStatus:       recovery.StatusReachable,
		LastForeignStatus:  recovery.StatusBlocked,
		RecoveryPhase:      string(recovery.PhaseOperatorAttention),
		LastRecoveryAction: operatorAttentionReason,
		AwaitingOperator:   true,
	}

	clearControlPlaneRecoveryOwnership(persisted, runtimeStatus)

	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseIdle; got != want {
		t.Fatalf("phase = %q, want %q", got, want)
	}
	if persisted.ControlPlaneRecovery.AwaitingOperator {
		t.Fatal("expected awaitingOperator to be cleared")
	}
	if persisted.ControlPlaneRecovery.OutageStartedAt != "" {
		t.Fatal("expected outage window to be cleared")
	}
	if persisted.ControlPlaneRecovery.LastActionReason != "" {
		t.Fatalf("expected last action reason to be cleared, got %q", persisted.ControlPlaneRecovery.LastActionReason)
	}
	if persisted.ControlPlaneRecovery.LastPanelStatus != "" || persisted.ControlPlaneRecovery.LastRUStatus != "" || persisted.ControlPlaneRecovery.LastForeignStatus != "" {
		t.Fatalf("expected persisted probe summaries to be cleared, got panel=%q ru=%q foreign=%q",
			persisted.ControlPlaneRecovery.LastPanelStatus,
			persisted.ControlPlaneRecovery.LastRUStatus,
			persisted.ControlPlaneRecovery.LastForeignStatus,
		)
	}
	if persisted.ControlPlaneRecovery.LastAutoRebootAt == "" {
		t.Fatal("expected reboot cooldown timestamp to be preserved")
	}
	if runtimeStatus.RecoveryPhase != string(recovery.PhaseIdle) {
		t.Fatalf("runtime recovery phase = %q, want %q", runtimeStatus.RecoveryPhase, recovery.PhaseIdle)
	}
	if runtimeStatus.AwaitingOperator {
		t.Fatal("expected runtime awaitingOperator to be cleared")
	}
}

func containsBatchLine(commands [][]string, expected string) bool {
	for _, batch := range commands {
		for _, command := range batch {
			if command == expected {
				return true
			}
		}
	}
	return false
}
