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

func TestAdvanceControlPlaneRecoveryAutoRetriesPasswallWhenPanelRecoversInDirectMode(t *testing.T) {
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
	now := time.Now()
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseDirectSettle,
		LastPanelStatus:              recovery.StatusBlocked,
		LastRUStatus:                 recovery.StatusReachable,
		LastForeignStatus:            recovery.StatusBlocked,
		LastActionReason:             controlPlaneDirectReason,
	}
	rescueState := rescue.State{
		Mode:             rescue.ModeDirect,
		LastTransitionAt: now.Add(-2 * time.Minute),
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
		t.Fatal("expected control-plane work to stay paused during PassWall retry warmup")
	}
	if !outcome.InventoryChanged {
		t.Fatal("expected passwall retry to require inventory recollect")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhasePasswallRetryWait; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if persisted.ControlPlaneRecovery.LastPasswallRetryAt == "" {
		t.Fatal("expected last passwall retry timestamp to be set")
	}
	if persisted.ControlPlaneRecovery.AwaitingOperator {
		t.Fatal("did not expect operator attention during first auto retry")
	}
	if got, want := persisted.ControlPlaneRecovery.LastActionReason, autoProxyRetryReason; got != want {
		t.Fatalf("last action reason = %q, want %q", got, want)
	}
	if got, want := rescueState.Mode, rescue.ModeProxy; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
	}
	if !runtimeStatus.PasswallEnabled {
		t.Fatal("expected runtime status to reflect PassWall re-enabled")
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected passwall enable batch, got %#v", backend.batchCommands)
	}
	if !containsCommand(backend.runCommands, "/etc/init.d/passwall2 restart") {
		t.Fatalf("expected passwall restart command, got %#v", backend.runCommands)
	}
}

func TestAdvanceControlPlaneRecoveryDoesNotAutoRetryPasswallTwiceInSameOutage(t *testing.T) {
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

	backend := &fakeRescueBackend{}
	now := time.Now()
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseDirectSettle,
		LastPasswallRetryAt:          recovery.FormatTime(now.Add(-5 * time.Minute)),
		LastPanelStatus:              recovery.StatusBlocked,
		LastRUStatus:                 recovery.StatusReachable,
		LastForeignStatus:            recovery.StatusBlocked,
		LastActionReason:             controlPlaneDirectReason,
	}
	rescueState := rescue.State{
		Mode:             rescue.ModeDirect,
		LastTransitionAt: now.Add(-2 * time.Minute),
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

	if outcome.SkipControlPlane {
		t.Fatal("expected panel reachability to allow operator-attention reporting")
	}
	if outcome.InventoryChanged {
		t.Fatal("did not expect another passwall toggle after retry budget is spent")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseOperatorAttention; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if !persisted.ControlPlaneRecovery.AwaitingOperator {
		t.Fatal("expected operator attention after retry budget is spent")
	}
	if got, want := persisted.ControlPlaneRecovery.LastActionReason, panelRecoveredDirectReason; got != want {
		t.Fatalf("last action reason = %q, want %q", got, want)
	}
	if containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("did not expect second passwall enable batch, got %#v", backend.batchCommands)
	}
	if containsCommand(backend.runCommands, "/etc/init.d/passwall2 restart") {
		t.Fatalf("did not expect second passwall restart, got %#v", backend.runCommands)
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

func TestAdvanceControlPlaneRecoveryKeepsProxyWhenShuntNodeRespondsAfterRetry(t *testing.T) {
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
			"/usr/share/passwall2/test.sh url_test_node world-node": {Stdout: "204:0.31"},
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
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(time.Now().Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(time.Now().Add(-70 * time.Minute)),
		Phase:                        recovery.PhasePasswallRetryWait,
		LastPasswallRetryAt:          recovery.FormatTime(time.Now().Add(-2 * time.Minute)),
	}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: true,
		SelectedNodeID:  "myshunt",
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

	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseIdle; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if persisted.ControlPlaneRecovery.AwaitingOperator {
		t.Fatal("did not expect operator attention while a shunt proxy node responds")
	}
	if got, want := persisted.ControlPlaneRecovery.LastActionReason, proxyNodeRecoveredReason; got != want {
		t.Fatalf("last action reason = %q, want %q", got, want)
	}
	if got, want := rescueState.Mode, rescue.ModeProxy; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
	}
	if containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='0'") {
		t.Fatalf("did not expect passwall disable batch, got %#v", backend.batchCommands)
	}
	if !containsCommand(backend.runCommands, "/usr/share/passwall2/test.sh url_test_node world-node") {
		t.Fatalf("expected recovery guard to test concrete shunt node, got %#v", backend.runCommands)
	}
	if outcome.SkipControlPlane {
		t.Fatal("expected panel reachability to allow normal check-in after proxy node recovery")
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

func setRecoveryProcessStart(t *testing.T, started time.Time) {
	t.Helper()

	original := recoveryProcessStartedAt
	recoveryProcessStartedAt = started
	t.Cleanup(func() {
		recoveryProcessStartedAt = original
	})
}

// GAP-1: a router that strands BEFORE it ever records a successful contact
// (e.g. right after onboarding commits enabled=1 + a proxy default_node) must
// still eventually fail safe to direct once the no-contact-from-boot outage
// exceeds the panel-outage threshold.
func TestAdvanceControlPlaneRecoveryFailsSafeToDirectWhenNeverContacted(t *testing.T) {
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
	// Boot happened long before the threshold, so the no-contact outage is ripe.
	setRecoveryProcessStart(t, time.Now().Add(-70*time.Minute))

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/etc/init.d/passwall2 restart": {Stdout: "restarted"},
		},
	}
	recoveryState := recovery.State{
		Phase: recovery.PhaseIdle,
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
		t.Fatal("expected fail-safe direct switch to require inventory recollect when never contacted")
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

// GAP-1 guard: during the first few minutes after boot (still no successful
// contact) the machine must not act, so transient boot-time panel blips do not
// flip a healthy router to direct.
func TestAdvanceControlPlaneRecoveryWaitsDuringFirstBootGraceWhenNeverContacted(t *testing.T) {
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
	// Boot only seconds ago — still inside the outage threshold window.
	setRecoveryProcessStart(t, time.Now().Add(-30*time.Second))

	backend := &fakeRescueBackend{}
	recoveryState := recovery.State{
		Phase: recovery.PhaseIdle,
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
		t.Fatal("expected control-plane work to be skipped while panel is unreachable")
	}
	if outcome.InventoryChanged {
		t.Fatal("did not expect any direct-mode switch during first-boot grace period")
	}
	if got, want := rescueState.Mode, rescue.ModeProxy; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no UCI writes during first-boot grace, got %#v", backend.batchCommands)
	}
	if len(backend.runCommands) != 0 {
		t.Fatalf("expected no shell commands during first-boot grace, got %#v", backend.runCommands)
	}
}

// GAP-4: when the panel is blocked for >= threshold but foreign reachability is
// only partial (not fully blocked), the machine must still enter direct as long
// as a working direct path exists — a controller restart never fixes a routing
// strand.
func TestAdvanceControlPlaneRecoveryEntersDirectWhenPanelBlockedAndForeignPartial(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusServiceUnavailable})
	defer panel.Close()
	// RU reachable = a working direct path exists; previously this drove a
	// controller-restart loop that never fixed the routing strand.
	ruUp := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer ruUp.Close()
	foreignUp := newStatusServer(map[string]int{"/": http.StatusNoContent})
	defer foreignUp.Close()
	foreignDown := newStatusServer(map[string]int{"/": http.StatusServiceUnavailable})
	defer foreignDown.Close()

	setRecoveryProbeTargets(t,
		[]probeTarget{
			{ID: "ya", Label: "ya.ru", URL: ruUp.URL},
			{ID: "vk", Label: "vk.com", URL: ruUp.URL},
		},
		// One of three foreign targets reachable -> StatusPartial, not StatusBlocked.
		[]probeTarget{
			{ID: "youtube", Label: "youtube", URL: foreignUp.URL},
			{ID: "instagram", Label: "instagram", URL: foreignDown.URL},
			{ID: "telegram", Label: "telegram", URL: foreignDown.URL},
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
		t.Fatal("expected direct switch to require inventory recollect when panel blocked + foreign partial")
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
	if containsCommand(backend.runCommands, "/etc/init.d/vectra-controller restart") {
		t.Fatalf("did not expect a controller restart for a routing strand, got %#v", backend.runCommands)
	}
}

// GAP-5: operator-attention must never be a terminal park. Each RebootCooldown
// it re-attempts proxy; on failure it re-disables PassWall (staying reachable in
// direct) and keeps looping, while still emitting AwaitingOperator for panel
// visibility.
func TestAdvanceControlPlaneRecoveryOperatorAttentionReattemptsProxy(t *testing.T) {
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
	now := time.Now()
	staleRetryAt := recovery.FormatTime(now.Add(-13 * time.Hour))
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-6 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-5 * time.Hour)),
		Phase:                        recovery.PhaseOperatorAttention,
		AwaitingOperator:             true,
		LastActionReason:             operatorAttentionReason,
		// Last retry well beyond RebootCooldown so a fresh attempt is due.
		LastPasswallRetryAt: staleRetryAt,
	}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	// Parked in direct after a prior failed retry; the re-attempt must re-enable
	// PassWall (proxy) and re-arm the bounded retry-wait machinery.
	inventory := controlplane.RouterInventory{PasswallEnabled: false}
	runtimeStatus := state.RuntimeStatus{}

	// Tick 1: cooldown elapsed -> re-attempt proxy and re-arm retry-wait, while
	// still surfacing AwaitingOperator for the panel.
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
		t.Fatal("expected operator-attention re-attempt to re-enable PassWall (inventory changed)")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhasePasswallRetryWait; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if got, want := rescueState.Mode, rescue.ModeProxy; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
	}
	if !persisted.ControlPlaneRecovery.AwaitingOperator {
		t.Fatal("expected AwaitingOperator to remain set for panel visibility")
	}
	if persisted.ControlPlaneRecovery.LastPasswallRetryAt == staleRetryAt {
		t.Fatal("expected last passwall retry timestamp to advance on re-attempt")
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected passwall enable batch on re-attempt, got %#v", backend.batchCommands)
	}

	// Tick 2 (after warmup): proxy still cannot reach foreign, so the existing
	// retry-wait path re-disables PassWall and loops back to operator attention --
	// the router stays reachable in direct and never requires a human.
	backend.batchCommands = nil
	backend.runCommands = nil
	inventory.PasswallEnabled = true
	persisted.ControlPlaneRecovery.LastPasswallRetryAt = recovery.FormatTime(now.Add(-2 * time.Minute))

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

	if !outcome.InventoryChanged {
		t.Fatal("expected failed retry to re-disable PassWall (inventory changed)")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseOperatorAttention; got != want {
		t.Fatalf("recovery phase after failed retry = %q, want %q", got, want)
	}
	if got, want := rescueState.Mode, rescue.ModeDirect; got != want {
		t.Fatalf("rescue mode after failed retry = %q, want %q", got, want)
	}
	if !persisted.ControlPlaneRecovery.AwaitingOperator {
		t.Fatal("expected AwaitingOperator to stay set after failed retry")
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='0'") {
		t.Fatalf("expected passwall to be re-disabled after failed retry, got %#v", backend.batchCommands)
	}
}

// GAP-5 guard: operator-attention must not re-attempt more often than the
// RebootCooldown; a fresh-enough prior attempt keeps the router parked in direct
// without thrashing PassWall.
func TestAdvanceControlPlaneRecoveryOperatorAttentionRespectsRetryCooldown(t *testing.T) {
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
	now := time.Now()
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-6 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-5 * time.Hour)),
		Phase:                        recovery.PhaseOperatorAttention,
		AwaitingOperator:             true,
		LastActionReason:             operatorAttentionReason,
		// Last retry just an hour ago — RebootCooldown (12h) has not elapsed.
		LastPasswallRetryAt: recovery.FormatTime(now.Add(-1 * time.Hour)),
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

	if outcome.InventoryChanged {
		t.Fatal("did not expect a re-attempt before RebootCooldown elapsed")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseOperatorAttention; got != want {
		t.Fatalf("recovery phase = %q, want %q", got, want)
	}
	if !persisted.ControlPlaneRecovery.AwaitingOperator {
		t.Fatal("expected AwaitingOperator to remain set while parked in direct")
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no PassWall writes before retry cooldown elapses, got %#v", backend.batchCommands)
	}
	if !outcome.SkipControlPlane {
		t.Fatal("expected control-plane work to stay skipped while panel is unreachable")
	}
}

// BUG H1 regression: the panel-reachability probe must use the control-plane
// fwmark so it measures the SAME direct path the check-in uses. With a fwmark
// configured and the panel reachable on that direct path, the agent must NOT
// enter recovery (it must allow control-plane work to proceed) — exactly the
// divergence that previously made a dead-proxy router flip to direct because the
// UNMARKED probe failed while the marked check-in would have succeeded.
//
// (On the non-Linux test host SO_MARK is a no-op, so the marked prober reaches
// the same httptest server; the assertion here is that wiring the fwmark through
// the panel probe does not break the reachable-panel path and keeps recovery
// idle.)
func TestAdvanceControlPlaneRecoveryPanelProbeUsesFwmarkAndStaysIdleWhenPanelReachable(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusNoContent})
	defer panel.Close()

	backend := &fakeRescueBackend{}
	now := time.Now()
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-2 * time.Minute)),
		Phase:                        recovery.PhaseIdle,
	}
	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	inventory := controlplane.RouterInventory{PasswallEnabled: true}
	runtimeStatus := state.RuntimeStatus{}

	cfg := baseControlPlaneRecoveryConfig(panel.URL)
	cfg.ControlPlaneFwmark = 0x564354

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

	if outcome.SkipControlPlane {
		t.Fatal("expected control-plane work to proceed when the panel is reachable on the marked direct path")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseIdle; got != want {
		t.Fatalf("recovery phase = %q, want %q (must stay idle, not enter recovery)", got, want)
	}
	if got, want := persisted.ControlPlaneRecovery.LastPanelStatus, recovery.StatusReachable; got != want {
		t.Fatalf("last panel status = %q, want %q", got, want)
	}
	if !runtimeStatus.ServerReachable {
		t.Fatal("expected runtime status to report the panel reachable")
	}
}

// BUG M1 regression: after the cron watchdog's dead-man switch disables PassWall
// (router -> direct so the controller can reach the panel), the agent's check-in
// works again and recovery sits Idle/Monitoring — a watchdog-induced direct does
// NOT enter PhaseDirectSettle. The agent must therefore detect "PassWall disabled
// while recovery is Idle/Monitoring AND the panel is healthy" and route into the
// existing probe-gated, cooldown-bounded proxy re-attempt so the router
// self-heals back to proxy, satisfying the watchdog header's promise that the
// agent's own auto-resume re-enables proxy once contact is restored.
func TestAdvanceControlPlaneRecoveryResumesProxyAfterWatchdogInducedDirect(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusNoContent})
	defer panel.Close()

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/etc/init.d/passwall2 restart": {Stdout: "restarted"},
		},
	}
	now := time.Now()
	// Healthy control-plane contact (check-in works in direct), recovery idle:
	// this is a watchdog-induced direct, not a recovery-driven one.
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-1 * time.Minute)),
		Phase:                        recovery.PhaseIdle,
	}
	rescueState := rescue.State{
		Mode:             rescue.ModeDirect,
		LastTransitionAt: now.Add(-20 * time.Minute),
	}
	persisted := state.PersistedState{ControlPlaneRecovery: recoveryState}
	// PassWall was disabled by the watchdog dead-man switch.
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
		t.Fatal("expected the watchdog-induced direct to trigger a proxy re-attempt (inventory changed)")
	}
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhasePasswallRetryWait; got != want {
		t.Fatalf("recovery phase = %q, want %q (must arm the bounded retry-wait path)", got, want)
	}
	if persisted.ControlPlaneRecovery.LastPasswallRetryAt == "" {
		t.Fatal("expected last passwall retry timestamp to be set so the cooldown gate engages")
	}
	if got, want := rescueState.Mode, rescue.ModeProxy; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
	}
	if !runtimeStatus.PasswallEnabled {
		t.Fatal("expected runtime status to reflect PassWall re-enabled")
	}
	if !containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected passwall enable batch, got %#v", backend.batchCommands)
	}
	if !containsCommand(backend.runCommands, "/etc/init.d/passwall2 restart") {
		t.Fatalf("expected passwall restart command, got %#v", backend.runCommands)
	}
}

// BUG M1 flap-safety: a watchdog-induced direct must NOT thrash PassWall when the
// proxy is still dead. If a proxy re-attempt already happened within the cooldown
// (RebootCooldown), the agent must leave PassWall disabled and wait — so it never
// fights the watchdog by re-enabling a proxy that the watchdog will just disable
// again.
func TestAdvanceControlPlaneRecoveryDoesNotThrashWatchdogDirectWithinCooldown(t *testing.T) {
	panel := newStatusServer(map[string]int{"/api/health": http.StatusNoContent})
	defer panel.Close()

	backend := &fakeRescueBackend{}
	now := time.Now()
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-1 * time.Minute)),
		Phase:                        recovery.PhaseIdle,
		// A proxy re-attempt happened recently; RebootCooldown (12h) has not elapsed.
		LastPasswallRetryAt: recovery.FormatTime(now.Add(-1 * time.Hour)),
	}
	rescueState := rescue.State{
		Mode:             rescue.ModeDirect,
		LastTransitionAt: now.Add(-1 * time.Hour),
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

	if outcome.InventoryChanged {
		t.Fatal("did not expect a proxy re-attempt within the cooldown window")
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no PassWall writes within cooldown, got %#v", backend.batchCommands)
	}
	if got, want := rescueState.Mode, rescue.ModeDirect; got != want {
		t.Fatalf("rescue mode = %q, want %q (must stay direct within cooldown)", got, want)
	}
	if got := persisted.ControlPlaneRecovery.Phase; got != recovery.PhaseIdle && got != recovery.PhaseMonitoring {
		t.Fatalf("recovery phase = %q, want idle/monitoring (no re-attempt)", got)
	}
}

// BUG M1 isolation: the watchdog-direct self-heal must NOT interfere with a
// normal recovery-driven direct. When recovery owns PassWall (e.g. it just
// entered PhaseDirectSettle) the existing settle/retry machinery must run
// unchanged — the self-heal only applies to Idle/Monitoring (recovery does not
// own PassWall) phases.
func TestAdvanceControlPlaneRecoveryDoesNotPreemptRecoveryDrivenDirectSettle(t *testing.T) {
	// Panel still unreachable: a genuine recovery-driven direct that is mid-settle.
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
	now := time.Now()
	recoveryState := recovery.State{
		LastSuccessfulControlPlaneAt: recovery.FormatTime(now.Add(-2 * time.Hour)),
		OutageStartedAt:              recovery.FormatTime(now.Add(-70 * time.Minute)),
		Phase:                        recovery.PhaseDirectSettle,
		LastPanelStatus:              recovery.StatusBlocked,
		LastActionReason:             controlPlaneDirectReason,
	}
	// Inside the DirectSettle window so the existing path is a no-op this tick.
	rescueState := rescue.State{
		Mode:             rescue.ModeDirect,
		LastTransitionAt: now.Add(-2 * time.Second),
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

	// The recovery-driven DirectSettle must stay in DirectSettle (within window),
	// untouched by the watchdog self-heal, and must NOT re-enable PassWall.
	if got, want := persisted.ControlPlaneRecovery.Phase, recovery.PhaseDirectSettle; got != want {
		t.Fatalf("recovery phase = %q, want %q (self-heal must not pre-empt recovery-driven direct)", got, want)
	}
	if outcome.InventoryChanged {
		t.Fatal("did not expect any PassWall change while recovery-driven DirectSettle is mid-window")
	}
	if containsBatchLine(backend.batchCommands, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("self-heal must not re-enable PassWall during recovery-driven direct, got %#v", backend.batchCommands)
	}
	if got, want := rescueState.Mode, rescue.ModeDirect; got != want {
		t.Fatalf("rescue mode = %q, want %q", got, want)
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
