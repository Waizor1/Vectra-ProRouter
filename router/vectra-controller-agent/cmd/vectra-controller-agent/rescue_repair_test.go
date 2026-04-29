package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

type fakeRescueRepairBackend struct {
	batchCommands [][]string
	runCommands   []string
	runErrors     map[string]error
}

func (f *fakeRescueRepairBackend) Show(context.Context, string) ([]string, error) {
	return nil, nil
}

func (f *fakeRescueRepairBackend) Batch(_ context.Context, commands []string) error {
	copied := append([]string(nil), commands...)
	f.batchCommands = append(f.batchCommands, copied)
	return nil
}

func (f *fakeRescueRepairBackend) Run(_ context.Context, name string, args ...string) (passwall.CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.runCommands = append(f.runCommands, command)
	result := passwall.CommandResult{Command: command, Stdout: "ok"}
	if err, ok := f.runErrors[command]; ok {
		result.Stderr = err.Error()
		return result, err
	}
	return result, nil
}

func TestParseRunRescueRepairJobRejectsRawShellAndUnsupportedActions(t *testing.T) {
	if _, err := parseRunRescueRepairJob(map[string]interface{}{
		"actions": []interface{}{"restart_passwall"},
		"command": "/bin/sh",
	}); err == nil {
		t.Fatalf("expected raw command payload to be rejected")
	}

	if _, err := parseRunRescueRepairJob(map[string]interface{}{
		"actions": []interface{}{"reboot"},
	}); err == nil {
		t.Fatalf("expected unsupported action to be rejected")
	}
}

func TestParseRunRescueRepairJobOrdersReconnectBeforeControllerRestart(t *testing.T) {
	request, err := parseRunRescueRepairJob(map[string]interface{}{
		"actions": []interface{}{
			"restart_controller",
			"restart_dnsmasq",
			"reconnect_proxy",
		},
	})
	if err != nil {
		t.Fatalf("parse repair job: %v", err)
	}

	want := []string{"restart_dnsmasq", "reconnect_proxy", "restart_controller"}
	if !reflect.DeepEqual(request.Actions, want) {
		t.Fatalf("actions = %#v, want %#v", request.Actions, want)
	}
}

func TestExecuteRescueRepairJobRunsWhitelistedActionsAndReportsHealth(t *testing.T) {
	backend := &fakeRescueRepairBackend{}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	persisted := state.PersistedState{}
	runtimeStatus := state.RuntimeStatus{PasswallEnabled: false, ServerReachable: true}
	request, err := parseRunRescueRepairJob(map[string]interface{}{
		"actions":        []interface{}{"restart_passwall", "restart_dnsmasq", "refresh_rules", "refresh_subscriptions", "reconnect_proxy"},
		"timeoutSeconds": float64(60),
		"requestedBy":    "auto_rescue",
	})
	if err != nil {
		t.Fatalf("parse repair job: %v", err)
	}

	payload, _, _, err := executeRescueRepairJob(
		context.Background(),
		backend,
		request,
		&rescueState,
		&persisted,
		&runtimeStatus,
		func() controlplane.RouterInventory {
			return controlplane.RouterInventory{
				PasswallEnabled: runtimeStatus.PasswallEnabled,
				ServiceHealth: controlplane.RouterServiceHealth{
					Controller: "running",
					Passwall:   "running",
					DNSMasq:    "running",
				},
			}
		},
	)
	if err != nil {
		t.Fatalf("execute repair job: %v", err)
	}

	wantCommands := []string{
		"sh -c " + passwallPostInstallRecoveryCommand,
		"/etc/init.d/dnsmasq restart",
		"lua /usr/share/passwall2/rule_update.lua log geoip,geosite",
		"lua /usr/share/passwall2/subscribe.lua start all",
		"/etc/init.d/passwall2 restart",
	}
	if !reflect.DeepEqual(backend.runCommands, wantCommands) {
		t.Fatalf("commands = %#v, want %#v", backend.runCommands, wantCommands)
	}
	if rescueState.Mode != rescue.ModeProxy || !runtimeStatus.PasswallEnabled {
		t.Fatalf("reconnect_proxy should restore proxy state, got mode=%s passwall=%v", rescueState.Mode, runtimeStatus.PasswallEnabled)
	}
	if recovered, _ := payload["recoveredProxy"].(bool); !recovered {
		t.Fatalf("expected recoveredProxy in payload")
	}
	results, ok := payload["results"].([]map[string]interface{})
	if !ok || len(results) != 5 {
		t.Fatalf("expected five action results, got %#v", payload["results"])
	}
}

func TestExecuteRescueRepairJobDoesNotClearRescueStateOnReconnectFailure(t *testing.T) {
	backend := &fakeRescueRepairBackend{
		runErrors: map[string]error{
			"/etc/init.d/passwall2 restart": fmt.Errorf("passwall restart failed"),
		},
	}
	rescueState := rescue.State{Mode: rescue.ModeDirect}
	runtimeStatus := state.RuntimeStatus{PasswallEnabled: false}
	request, err := parseRunRescueRepairJob(map[string]interface{}{
		"actions": []interface{}{"reconnect_proxy"},
	})
	if err != nil {
		t.Fatalf("parse repair job: %v", err)
	}

	_, _, _, err = executeRescueRepairJob(
		context.Background(),
		backend,
		request,
		&rescueState,
		&state.PersistedState{},
		&runtimeStatus,
		nil,
	)
	if err == nil {
		t.Fatalf("expected reconnect failure")
	}
	if rescueState.Mode != rescue.ModeDirect || runtimeStatus.PasswallEnabled {
		t.Fatalf("failed reconnect must not clear direct state, got mode=%s passwall=%v", rescueState.Mode, runtimeStatus.PasswallEnabled)
	}
}
