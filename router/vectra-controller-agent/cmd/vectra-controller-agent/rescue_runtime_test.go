package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

type fakeRescueBackend struct {
	batchCommands [][]string
	runCommands   []string
	runResults    map[string]passwall.CommandResult
	runErrors     map[string]error
	protocols     map[string]string
}

func (f *fakeRescueBackend) Show(context.Context, string) ([]string, error) {
	return nil, nil
}

func (f *fakeRescueBackend) Batch(_ context.Context, commands []string) error {
	copied := append([]string(nil), commands...)
	f.batchCommands = append(f.batchCommands, copied)
	return nil
}

func (f *fakeRescueBackend) Run(_ context.Context, name string, args ...string) (passwall.CommandResult, error) {
	command := name
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}
	f.runCommands = append(f.runCommands, command)

	if name == "uci" && len(args) == 3 && args[0] == "-q" && args[1] == "get" {
		key := args[2]
		if protocol, ok := f.protocols[key]; ok {
			return passwall.CommandResult{
				Command: command,
				Stdout:  protocol,
			}, nil
		}
		return passwall.CommandResult{
			Command: command,
			Stderr:  "not found",
		}, fmt.Errorf("%s: not found", command)
	}

	result := passwall.CommandResult{Command: command}
	if configured, ok := f.runResults[command]; ok {
		result = configured
		if result.Command == "" {
			result.Command = command
		}
	}
	if err, ok := f.runErrors[command]; ok {
		if result.Stderr == "" {
			result.Stderr = err.Error()
		}
		return result, err
	}

	if result.Command == "" {
		result.Command = command
	}
	return result, nil
}

func newHTTPTestServer(statusCode int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(statusCode)
	}))
}

func baseRescueConfig(controlURL string, healthURLs ...string) *config.Config {
	return &config.Config{
		ControlURL:     controlURL,
		RequestTimeout: time.Second,
		Rescue: rescue.Policy{
			HealthURLs:               healthURLs,
			TriggerFailureCount:      1,
			RecoverySuccessCount:     1,
			Cooldown:                 0,
			RequireDirectPathSuccess: true,
			DirectModeReason:         "Subscription expired or upstream proxy unavailable",
		},
	}
}

func TestEvaluateLocalRescueIgnoresPublicProbeFailureWhenProxyProbeSucceeds(t *testing.T) {
	t.Parallel()

	serverProbe := newHTTPTestServer(http.StatusNoContent)
	defer serverProbe.Close()
	publicProbe := newHTTPTestServer(http.StatusServiceUnavailable)
	defer publicProbe.Close()

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/usr/share/passwall2/test.sh url_test_node node-1": {
				Stdout: "204:0.12",
			},
		},
		protocols: map[string]string{
			"passwall2.node-1.protocol": "vmess",
		},
	}

	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: true,
		SelectedNodeID:  "node-1",
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
	if transitioned {
		t.Fatalf("expected rescue mode to remain in proxy")
	}
	if health.CurrentMode != "proxy" {
		t.Fatalf("expected current mode proxy, got %s", health.CurrentMode)
	}
	if rescueState.ProxyFailureCount != 0 {
		t.Fatalf("expected proxy failure count to stay 0, got %d", rescueState.ProxyFailureCount)
	}
	if rescueState.ProxySuccessCount != 1 {
		t.Fatalf("expected proxy success count 1, got %d", rescueState.ProxySuccessCount)
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no UCI writes, got %d batch call(s)", len(backend.batchCommands))
	}
}

func TestEvaluateLocalRescueKeepsProxyModeWhenShuntDefaultNodeIsMissing(t *testing.T) {
	t.Parallel()

	serverProbe := newHTTPTestServer(http.StatusNoContent)
	defer serverProbe.Close()
	publicProbe := newHTTPTestServer(http.StatusServiceUnavailable)
	defer publicProbe.Close()

	backend := &fakeRescueBackend{
		protocols: map[string]string{
			"passwall2.myshunt.protocol": "_shunt",
		},
	}

	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: true,
		SelectedNodeID:  "myshunt",
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
	if transitioned {
		t.Fatalf("expected virtual selected node to keep proxy mode")
	}
	if health.CurrentMode != "proxy" {
		t.Fatalf("expected current mode proxy, got %s", health.CurrentMode)
	}
	if rescueState.ProxyFailureCount != 0 {
		t.Fatalf("expected proxy failure count to stay 0, got %d", rescueState.ProxyFailureCount)
	}
	for _, command := range backend.runCommands {
		if strings.Contains(command, "/usr/share/passwall2/test.sh") {
			t.Fatalf("did not expect url_test_node probe for unresolved shunt node, got %s", command)
		}
	}
}

func TestEvaluateLocalRescueUsesShuntDefaultNodeAsConclusiveProxyProbe(t *testing.T) {
	t.Parallel()

	serverProbe := newHTTPTestServer(http.StatusNoContent)
	defer serverProbe.Close()
	publicProbe := newHTTPTestServer(http.StatusServiceUnavailable)
	defer publicProbe.Close()

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/usr/share/passwall2/test.sh url_test_node node-1": {
				Stdout: "204:0.09",
			},
		},
		protocols: map[string]string{
			"passwall2.myshunt.protocol":     "_shunt",
			"passwall2.myshunt.default_node": "node-1",
			"passwall2.node-1.protocol":      "vmess",
		},
	}

	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: true,
		SelectedNodeID:  "myshunt",
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
	if transitioned {
		t.Fatalf("expected shunt default node success to keep proxy mode")
	}
	if health.CurrentMode != "proxy" {
		t.Fatalf("expected current mode proxy, got %s", health.CurrentMode)
	}
	if rescueState.ProxySuccessCount != 1 {
		t.Fatalf("expected proxy success count 1, got %d", rescueState.ProxySuccessCount)
	}
	if !containsCommand(backend.runCommands, "/usr/share/passwall2/test.sh url_test_node node-1") {
		t.Fatalf("expected url_test_node to target shunt default node, got %#v", backend.runCommands)
	}
}

func TestEvaluateLocalRescueRestartsPasswallWhenServiceStopped(t *testing.T) {
	t.Parallel()

	serverProbe := newHTTPTestServer(http.StatusNoContent)
	defer serverProbe.Close()
	publicProbe := newHTTPTestServer(http.StatusNoContent)
	defer publicProbe.Close()

	backend := &fakeRescueBackend{}

	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: true,
		SelectedNodeID:  "node-1",
		ServiceHealth: controlplane.RouterServiceHealth{
			Passwall: "stopped",
		},
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
	if transitioned {
		t.Fatalf("expected watchdog restart to keep current mode while PassWall warms up")
	}
	if health.CurrentMode != "proxy" {
		t.Fatalf("expected current mode proxy, got %s", health.CurrentMode)
	}
	if !containsCommand(backend.runCommands, "/etc/init.d/passwall2 restart") {
		t.Fatalf("expected PassWall restart, got %#v", backend.runCommands)
	}
	if persisted.ControlPlaneRecovery.LastPasswallWatchdogRestartAt == "" {
		t.Fatalf("expected watchdog restart timestamp to be persisted")
	}
	if persisted.ControlPlaneRecovery.PasswallWatchdogRestartCount != 1 {
		t.Fatalf("expected watchdog restart count 1, got %d", persisted.ControlPlaneRecovery.PasswallWatchdogRestartCount)
	}
	if persisted.ControlPlaneRecovery.LastPasswallWatchdogReason != passwallWatchdogServiceReason {
		t.Fatalf("unexpected watchdog reason %q", persisted.ControlPlaneRecovery.LastPasswallWatchdogReason)
	}
	if runtimeStatus.LastPasswallWatchdogAt == "" ||
		runtimeStatus.LastPasswallWatchdogReason != passwallWatchdogServiceReason ||
		runtimeStatus.PasswallWatchdogRestartCount != 1 {
		t.Fatalf("runtime watchdog status was not populated: %+v", runtimeStatus)
	}
}

func TestEvaluateLocalRescueRestartsPasswallWhenProxyRuntimeMissing(t *testing.T) {
	t.Parallel()

	serverProbe := newHTTPTestServer(http.StatusNoContent)
	defer serverProbe.Close()
	publicProbe := newHTTPTestServer(http.StatusNoContent)
	defer publicProbe.Close()

	backend := &fakeRescueBackend{}

	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: true,
		SelectedNodeID:  "myshunt",
		ServiceHealth: controlplane.RouterServiceHealth{
			Passwall: "running",
		},
		SafetyEvents: []controlplane.RouterSafetyEvent{
			{
				Type:      "proxy_runtime_missing",
				Severity:  "critical",
				Component: "xray",
				Source:    "process",
				Message:   "PassWall2 is running but expected xray process is missing",
			},
		},
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
	if transitioned {
		t.Fatalf("expected watchdog restart to keep current mode while PassWall warms up")
	}
	if health.CurrentMode != "proxy" {
		t.Fatalf("expected current mode proxy, got %s", health.CurrentMode)
	}
	if !containsCommand(backend.runCommands, "/etc/init.d/passwall2 restart") {
		t.Fatalf("expected PassWall restart, got %#v", backend.runCommands)
	}
	if containsCommand(backend.runCommands, "/usr/share/passwall2/test.sh") {
		t.Fatalf("expected cheap runtime guard to restart before node probes, got %#v", backend.runCommands)
	}
	if persisted.ControlPlaneRecovery.LastPasswallWatchdogReason != passwallWatchdogRuntimeReason {
		t.Fatalf("unexpected watchdog reason %q", persisted.ControlPlaneRecovery.LastPasswallWatchdogReason)
	}
	if runtimeStatus.LastPasswallWatchdogReason != passwallWatchdogRuntimeReason ||
		runtimeStatus.PasswallWatchdogRestartCount != 1 {
		t.Fatalf("runtime watchdog status was not populated: %+v", runtimeStatus)
	}
}

func TestEvaluateLocalRescueRestartsPasswallBeforeDirectModeOnConclusiveProxyFailure(t *testing.T) {
	t.Parallel()

	serverProbe := newHTTPTestServer(http.StatusNoContent)
	defer serverProbe.Close()
	publicProbe := newHTTPTestServer(http.StatusServiceUnavailable)
	defer publicProbe.Close()

	backend := &fakeRescueBackend{
		runErrors: map[string]error{
			"/usr/share/passwall2/test.sh url_test_node node-1": fmt.Errorf("node-1 unreachable"),
		},
		protocols: map[string]string{
			"passwall2.myshunt.protocol":     "_shunt",
			"passwall2.myshunt.default_node": "node-1",
			"passwall2.node-1.protocol":      "vmess",
		},
	}

	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: true,
		SelectedNodeID:  "myshunt",
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
	if transitioned {
		t.Fatalf("expected watchdog restart to happen before direct-mode transition")
	}
	if health.CurrentMode != "proxy" {
		t.Fatalf("expected current mode proxy, got %s", health.CurrentMode)
	}
	if rescueState.ProxyFailureCount != 0 {
		t.Fatalf("expected watchdog restart to defer proxy failure count, got %d", rescueState.ProxyFailureCount)
	}
	if !containsCommand(backend.runCommands, "/etc/init.d/passwall2 restart") {
		t.Fatalf("expected PassWall restart, got %#v", backend.runCommands)
	}
	if len(backend.batchCommands) != 0 {
		t.Fatalf("expected no direct-mode UCI writes before warmup, got %d batch call(s)", len(backend.batchCommands))
	}
}

func TestEvaluateLocalRescueEntersDirectModeAfterConclusiveShuntProxyFailure(t *testing.T) {
	t.Parallel()

	serverProbe := newHTTPTestServer(http.StatusNoContent)
	defer serverProbe.Close()

	var publicCalls atomic.Int32
	publicProbe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if publicCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer publicProbe.Close()

	backend := &fakeRescueBackend{
		runErrors: map[string]error{
			"/usr/share/passwall2/test.sh url_test_node node-1": fmt.Errorf("node-1 unreachable"),
		},
		protocols: map[string]string{
			"passwall2.myshunt.protocol":     "_shunt",
			"passwall2.myshunt.default_node": "node-1",
			"passwall2.node-1.protocol":      "vmess",
		},
	}

	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{}
	persisted.ControlPlaneRecovery.LastPasswallWatchdogRestartAt = time.Now().UTC().Format(time.RFC3339)
	inventory := controlplane.RouterInventory{
		PasswallEnabled: true,
		SelectedNodeID:  "myshunt",
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
	if !transitioned {
		t.Fatalf("expected rescue to transition into direct mode after conclusive proxy failure")
	}
	if health.CurrentMode != "direct" {
		t.Fatalf("expected current mode direct, got %s", health.CurrentMode)
	}
	if rescueState.Mode != rescue.ModeDirect {
		t.Fatalf("expected rescue state direct, got %s", rescueState.Mode)
	}
	if rescueState.DirectSuccessCount != 1 {
		t.Fatalf("expected direct success count 1, got %d", rescueState.DirectSuccessCount)
	}
	if !containsCommand(backend.runCommands, "/usr/share/passwall2/test.sh url_test_node node-1") {
		t.Fatalf("expected url_test_node to target shunt default node, got %#v", backend.runCommands)
	}
	if len(backend.batchCommands) == 0 {
		t.Fatalf("expected passwall main switch writes for direct-mode transition")
	}
	if countCommand(backend.runCommands, "/etc/init.d/passwall2 restart") > 3 {
		t.Fatalf("expected no extra watchdog restart during cooldown, got %#v", backend.runCommands)
	}
}

func TestEvaluateLocalRescueKeepsVirtualOnlyShuntChainInconclusive(t *testing.T) {
	t.Parallel()

	serverProbe := newHTTPTestServer(http.StatusNoContent)
	defer serverProbe.Close()
	publicProbe := newHTTPTestServer(http.StatusServiceUnavailable)
	defer publicProbe.Close()

	backend := &fakeRescueBackend{
		protocols: map[string]string{
			"passwall2.myshunt.protocol":     "_shunt",
			"passwall2.myshunt.default_node": "group-1",
			"passwall2.group-1.protocol":     "_urltest",
		},
	}

	rescueState := rescue.State{Mode: rescue.ModeProxy}
	persisted := state.PersistedState{}
	inventory := controlplane.RouterInventory{
		PasswallEnabled: true,
		SelectedNodeID:  "myshunt",
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
	if transitioned {
		t.Fatalf("expected virtual-only shunt chain to remain inconclusive")
	}
	if rescueState.ProxyFailureCount != 0 {
		t.Fatalf("expected proxy failure count to stay 0, got %d", rescueState.ProxyFailureCount)
	}
	if containsCommand(backend.runCommands, "/usr/share/passwall2/test.sh") {
		t.Fatalf("did not expect url_test_node probe for virtual-only shunt chain, got %#v", backend.runCommands)
	}
}

func TestResumeProxyModeEnablesPasswallAndClearsLocalRescueFlag(t *testing.T) {
	t.Parallel()

	backend := &fakeRescueBackend{
		runResults: map[string]passwall.CommandResult{
			"/etc/init.d/passwall2 restart": {
				Stdout: "restarted",
			},
		},
	}

	now := time.Date(2026, 4, 6, 12, 0, 0, 0, time.UTC)
	rescueState := rescue.State{
		Mode:               rescue.ModeDirect,
		ProxyFailureCount:  3,
		DirectSuccessCount: 1,
		ProxySuccessCount:  2,
	}
	persisted := state.PersistedState{
		Rescue: state.RescueSnapshot{
			State:      rescueState,
			LastMode:   "direct",
			LastReason: "Subscription expired or upstream proxy unavailable",
			HappenedAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
		},
	}
	runtimeStatus := state.RuntimeStatus{
		RescueMode:        "direct",
		LastRescueReason:  persisted.Rescue.LastReason,
		PasswallEnabled:   false,
		ProxyFailureCount: 3,
	}

	if err := resumeProxyMode(
		context.Background(),
		backend,
		&rescueState,
		&persisted,
		&runtimeStatus,
		now,
	); err != nil {
		t.Fatalf("resumeProxyMode returned error: %v", err)
	}

	if rescueState.Mode != rescue.ModeProxy {
		t.Fatalf("expected proxy mode, got %s", rescueState.Mode)
	}
	if rescueState.ProxyFailureCount != 0 || rescueState.DirectSuccessCount != 0 || rescueState.ProxySuccessCount != 0 {
		t.Fatalf("expected rescue counters to be reset, got %+v", rescueState)
	}
	if !runtimeStatus.PasswallEnabled {
		t.Fatalf("expected runtime status to mark passwall enabled")
	}
	if runtimeStatus.RescueMode != "proxy" {
		t.Fatalf("expected runtime rescue mode proxy, got %s", runtimeStatus.RescueMode)
	}
	if runtimeStatus.LastRescueReason != "" {
		t.Fatalf("expected runtime rescue reason to be cleared, got %q", runtimeStatus.LastRescueReason)
	}
	if runtimeStatus.LastRescueAt != "" {
		t.Fatalf("expected runtime rescue timestamp to be cleared, got %q", runtimeStatus.LastRescueAt)
	}
	if persisted.Rescue.LastReason != "" {
		t.Fatalf("expected persisted rescue reason to be cleared, got %q", persisted.Rescue.LastReason)
	}
	if persisted.Rescue.HappenedAt != "" {
		t.Fatalf("expected persisted rescue timestamp to be cleared, got %q", persisted.Rescue.HappenedAt)
	}
	if persisted.Rescue.LastMode != "" {
		t.Fatalf("expected persisted rescue mode marker to be cleared, got %q", persisted.Rescue.LastMode)
	}
	if len(backend.batchCommands) != 1 {
		t.Fatalf("expected one UCI batch call, got %d", len(backend.batchCommands))
	}
	batch := strings.Join(backend.batchCommands[0], "\n")
	if !strings.Contains(batch, "set passwall2.@global[0].enabled='1'") {
		t.Fatalf("expected passwall enable command, got %s", batch)
	}
	if !strings.Contains(batch, "set vectra-controller.main.last_rescue_reason=''") {
		t.Fatalf("expected local rescue reason to be cleared, got %s", batch)
	}
}

func containsCommand(commands []string, needle string) bool {
	for _, command := range commands {
		if command == needle || strings.Contains(command, needle) {
			return true
		}
	}
	return false
}

func countCommand(commands []string, needle string) int {
	count := 0
	for _, command := range commands {
		if command == needle || strings.Contains(command, needle) {
			count++
		}
	}
	return count
}

func TestApplyRescueMetadataClearsStaleReasonWhenRouterIsBackInProxyMode(t *testing.T) {
	t.Parallel()

	rescueState := rescue.State{
		Mode:               rescue.ModeProxy,
		ProxyFailureCount:  0,
		DirectSuccessCount: 0,
		ProxySuccessCount:  2,
	}
	persisted := state.PersistedState{
		Rescue: state.RescueSnapshot{
			State:      rescueState,
			LastMode:   "direct",
			LastReason: "Subscription expired or upstream proxy unavailable",
			HappenedAt: "2026-04-06T07:32:23Z",
		},
	}
	inventory := controlplane.RouterInventory{
		PasswallEnabled:   true,
		SelectedNodeID:    "node-1",
		SelectedNodeLabel: "Node 1",
		ServiceHealth: controlplane.RouterServiceHealth{
			Controller: "running",
		},
	}
	runtimeStatus := state.RuntimeStatus{}

	applyRescueMetadata(&persisted, &rescueState, &inventory, &runtimeStatus)

	if persisted.Rescue.LastReason != "" {
		t.Fatalf("expected persisted rescue reason to be cleared, got %q", persisted.Rescue.LastReason)
	}
	if persisted.Rescue.HappenedAt != "" {
		t.Fatalf("expected persisted rescue timestamp to be cleared, got %q", persisted.Rescue.HappenedAt)
	}
	if persisted.Rescue.LastMode != "" {
		t.Fatalf("expected persisted rescue mode marker to be cleared, got %q", persisted.Rescue.LastMode)
	}
	if runtimeStatus.LastRescueReason != "" {
		t.Fatalf("expected runtime rescue reason to be cleared, got %q", runtimeStatus.LastRescueReason)
	}
	if runtimeStatus.LastRescueAt != "" {
		t.Fatalf("expected runtime rescue timestamp to be cleared, got %q", runtimeStatus.LastRescueAt)
	}
	if inventory.LastRescue != nil {
		t.Fatalf("expected no active last rescue payload while router is in proxy mode, got %+v", inventory.LastRescue)
	}
}
