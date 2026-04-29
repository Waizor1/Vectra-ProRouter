package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

const (
	rescueRepairActionRestartController    = "restart_controller"
	rescueRepairActionRestartPasswall      = "restart_passwall"
	rescueRepairActionRestartDNSMasq       = "restart_dnsmasq"
	rescueRepairActionRefreshRules         = "refresh_rules"
	rescueRepairActionRefreshSubscriptions = "refresh_subscriptions"
	rescueRepairActionReconnectProxy       = "reconnect_proxy"

	defaultRescueRepairTimeoutSeconds = 90
	minRescueRepairTimeoutSeconds     = 10
	maxRescueRepairTimeoutSeconds     = 180
	maxRescueRepairOutputChars        = 4000
)

var allowedRescueRepairActions = map[string]struct{}{
	rescueRepairActionRestartController:    {},
	rescueRepairActionRestartPasswall:      {},
	rescueRepairActionRestartDNSMasq:       {},
	rescueRepairActionRefreshRules:         {},
	rescueRepairActionRefreshSubscriptions: {},
	rescueRepairActionReconnectProxy:       {},
}

type rescueRepairJobRequest struct {
	Actions        []string
	TimeoutSeconds int
	CaseID         string
	Reason         string
	RequestedBy    string
}

type rescueRepairHealthCollector func() controlplane.RouterInventory

func parseRunRescueRepairJob(payload map[string]interface{}) (rescueRepairJobRequest, error) {
	if strings.TrimSpace(payloadString(payload, "command")) != "" {
		return rescueRepairJobRequest{}, fmt.Errorf("run_rescue_repair does not accept raw command payloads")
	}

	actions := payloadStringSlice(payload, "actions")
	if len(actions) == 0 {
		return rescueRepairJobRequest{}, fmt.Errorf("run_rescue_repair requires at least one action")
	}

	if len(actions) > 8 {
		return rescueRepairJobRequest{}, fmt.Errorf("run_rescue_repair accepts at most 8 actions")
	}

	normalized := make([]string, 0, len(actions))
	seen := map[string]struct{}{}
	for _, action := range actions {
		action = strings.TrimSpace(action)
		if _, ok := allowedRescueRepairActions[action]; !ok {
			return rescueRepairJobRequest{}, fmt.Errorf("unsupported rescue repair action %q", action)
		}
		if _, ok := seen[action]; ok {
			continue
		}
		seen[action] = struct{}{}
		normalized = append(normalized, action)
	}

	requestedBy := strings.TrimSpace(payloadString(payload, "requestedBy"))
	switch requestedBy {
	case "", "auto_rescue":
		requestedBy = "auto_rescue"
	case "operator", "telegram":
	default:
		return rescueRepairJobRequest{}, fmt.Errorf("unsupported rescue repair requester %q", requestedBy)
	}

	return rescueRepairJobRequest{
		Actions:        orderRescueRepairActions(normalized),
		TimeoutSeconds: clampRescueRepairTimeout(payloadIntRaw(payload, "timeoutSeconds", defaultRescueRepairTimeoutSeconds)),
		CaseID:         strings.TrimSpace(payloadString(payload, "caseId")),
		Reason:         strings.TrimSpace(payloadString(payload, "reason")),
		RequestedBy:    requestedBy,
	}, nil
}

func orderRescueRepairActions(actions []string) []string {
	ordered := make([]string, 0, len(actions))
	reconnect := make([]string, 0, 1)
	controller := make([]string, 0, 1)
	for _, action := range actions {
		switch action {
		case rescueRepairActionReconnectProxy:
			reconnect = append(reconnect, action)
		case rescueRepairActionRestartController:
			controller = append(controller, action)
		default:
			ordered = append(ordered, action)
		}
	}
	ordered = append(ordered, reconnect...)
	ordered = append(ordered, controller...)
	return ordered
}

func payloadIntRaw(payload map[string]interface{}, key string, fallback int) int {
	if payload == nil {
		return fallback
	}
	switch value := payload[key].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	default:
		return fallback
	}
}

func clampRescueRepairTimeout(value int) int {
	if value <= 0 {
		return defaultRescueRepairTimeoutSeconds
	}
	if value < minRescueRepairTimeoutSeconds {
		return minRescueRepairTimeoutSeconds
	}
	if value > maxRescueRepairTimeoutSeconds {
		return maxRescueRepairTimeoutSeconds
	}
	return value
}

func executeRescueRepairJob(
	ctx context.Context,
	backend passwall.UCIBackend,
	request rescueRepairJobRequest,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	runtimeStatus *state.RuntimeStatus,
	collectHealth rescueRepairHealthCollector,
) (map[string]interface{}, string, string, error) {
	startedAt := time.Now().UTC()
	repairCtx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutSeconds)*time.Second)
	defer cancel()

	payload := map[string]interface{}{
		"caseId":         nullableString(request.CaseID),
		"requestedBy":    request.RequestedBy,
		"reason":         nullableString(request.Reason),
		"actions":        request.Actions,
		"timeoutSeconds": request.TimeoutSeconds,
		"startedAt":      startedAt.Format(time.RFC3339),
		"before":         captureRescueRepairHealth(rescueState, runtimeStatus, collectHealth),
	}

	results := make([]map[string]interface{}, 0, len(request.Actions))
	stdoutBlocks := make([]string, 0, len(request.Actions))
	stderrBlocks := make([]string, 0, len(request.Actions))
	failures := make([]string, 0)
	recoveredProxy := false

	for _, action := range request.Actions {
		result := runRescueRepairAction(
			repairCtx,
			backend,
			action,
			rescueState,
			persisted,
			runtimeStatus,
		)
		results = append(results, result)
		if stdout := strings.TrimSpace(stringFromMap(result, "stdout")); stdout != "" {
			stdoutBlocks = append(stdoutBlocks, fmt.Sprintf("[%s]\n%s", action, stdout))
		}
		if stderr := strings.TrimSpace(stringFromMap(result, "stderr")); stderr != "" {
			stderrBlocks = append(stderrBlocks, fmt.Sprintf("[%s]\n%s", action, stderr))
		}
		if stringFromMap(result, "status") == "failure" || stringFromMap(result, "status") == "unsupported" {
			failure := stringFromMap(result, "error")
			if failure == "" {
				failure = fmt.Sprintf("%s failed", action)
			}
			failures = append(failures, failure)
		}
		if action == rescueRepairActionReconnectProxy && stringFromMap(result, "status") == "success" {
			recoveredProxy = true
		}
		if repairCtx.Err() != nil {
			failures = append(failures, repairCtx.Err().Error())
			break
		}
	}

	completedAt := time.Now().UTC()
	payload["completedAt"] = completedAt.Format(time.RFC3339)
	payload["after"] = captureRescueRepairHealth(rescueState, runtimeStatus, collectHealth)
	payload["results"] = results
	payload["recoveredProxy"] = recoveredProxy

	stdout := truncateRescueRepairText(strings.Join(stdoutBlocks, "\n\n"))
	stderr := truncateRescueRepairText(strings.Join(stderrBlocks, "\n"))
	if len(failures) > 0 {
		errText := strings.Join(failures, "; ")
		payload["error"] = errText
		return payload, stdout, stderr, fmt.Errorf(errText)
	}

	return payload, stdout, stderr, nil
}

func runRescueRepairAction(
	ctx context.Context,
	backend passwall.UCIBackend,
	action string,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	runtimeStatus *state.RuntimeStatus,
) map[string]interface{} {
	startedAt := time.Now().UTC()
	status := "success"
	command := ""
	stdout := ""
	stderr := ""
	errorText := ""

	runCommand := func(name string, args ...string) {
		result, err := backend.Run(ctx, name, args...)
		result = passwall.NormalizeCommandResult(result)
		command = result.Command
		stdout = result.Stdout
		stderr = result.Stderr
		if err != nil {
			status = "failure"
			errorText = err.Error()
		}
	}

	switch action {
	case rescueRepairActionRestartController:
		command = "(sleep 2; /etc/init.d/vectra-controller restart >/tmp/vectra-controller-recovery.log 2>&1) &"
		if err := scheduleControllerServiceRestart(ctx, backend); err != nil {
			status = "failure"
			errorText = err.Error()
		} else {
			status = "scheduled"
		}
	case rescueRepairActionRestartPasswall:
		runCommand("sh", "-c", passwallPostInstallRecoveryCommand)
	case rescueRepairActionRestartDNSMasq:
		runCommand("/etc/init.d/dnsmasq", "restart")
	case rescueRepairActionRefreshRules:
		runCommand("lua", "/usr/share/passwall2/rule_update.lua", "log", "geoip,geosite")
	case rescueRepairActionRefreshSubscriptions:
		runCommand("lua", "/usr/share/passwall2/subscribe.lua", "start", "all")
	case rescueRepairActionReconnectProxy:
		command = "uci batch passwall2 enabled=1 + /etc/init.d/passwall2 restart"
		if err := resumeProxyMode(ctx, backend, rescueState, persisted, runtimeStatus, time.Now().UTC()); err != nil {
			status = "failure"
			errorText = err.Error()
		}
	default:
		status = "unsupported"
		errorText = fmt.Sprintf("unsupported rescue repair action %q", action)
	}

	completedAt := time.Now().UTC()
	result := map[string]interface{}{
		"action":      action,
		"status":      status,
		"startedAt":   startedAt.Format(time.RFC3339),
		"completedAt": completedAt.Format(time.RFC3339),
		"durationMs":  int(completedAt.Sub(startedAt).Milliseconds()),
	}
	if command != "" {
		result["command"] = command
	}
	if stdout != "" {
		result["stdout"] = truncateRescueRepairText(stdout)
	}
	if stderr != "" {
		result["stderr"] = truncateRescueRepairText(stderr)
	}
	if errorText != "" {
		result["error"] = errorText
	}
	return result
}

func collectRescueRepairInventorySnapshot(
	ctx context.Context,
	backend passwall.UCIBackend,
	runtimeStatus *state.RuntimeStatus,
) controlplane.RouterInventory {
	serviceState := func(service string) string {
		result, err := backend.Run(ctx, "/etc/init.d/"+service, "running")
		if err == nil {
			return "running"
		}
		if strings.TrimSpace(result.Stderr) != "" {
			return "stopped"
		}
		return "stopped"
	}

	passwallEnabled := false
	if value, ok := getUCIValue(ctx, backend, "passwall2.@global[0].enabled"); ok {
		passwallEnabled = strings.TrimSpace(value) == "1"
	} else if runtimeStatus != nil {
		passwallEnabled = runtimeStatus.PasswallEnabled
	}

	selectedNodeID := ""
	if value, ok := getUCIValue(ctx, backend, "passwall2.@global[0].node"); ok {
		selectedNodeID = value
	} else if runtimeStatus != nil {
		selectedNodeID = runtimeStatus.SelectedNodeID
	}

	selectedNodeLabel := ""
	if runtimeStatus != nil {
		selectedNodeLabel = runtimeStatus.SelectedNodeLabel
	}

	return controlplane.RouterInventory{
		PasswallEnabled:   passwallEnabled,
		SelectedNodeID:    selectedNodeID,
		SelectedNodeLabel: selectedNodeLabel,
		ServiceHealth: controlplane.RouterServiceHealth{
			Controller:     serviceState("vectra-controller"),
			Passwall:       serviceState("passwall2"),
			PasswallServer: serviceState("passwall2_server"),
			DNSMasq:        serviceState("dnsmasq"),
		},
	}
}

func captureRescueRepairHealth(
	rescueState *rescue.State,
	runtimeStatus *state.RuntimeStatus,
	collectHealth rescueRepairHealthCollector,
) map[string]interface{} {
	capturedAt := time.Now().UTC()
	snapshot := map[string]interface{}{
		"capturedAt":    capturedAt.Format(time.RFC3339),
		"serviceHealth": map[string]interface{}{},
	}

	if collectHealth != nil {
		inventory := collectHealth()
		snapshot["passwallEnabled"] = inventory.PasswallEnabled
		snapshot["selectedNodeId"] = nullableString(inventory.SelectedNodeID)
		snapshot["selectedNodeLabel"] = nullableString(inventory.SelectedNodeLabel)
		snapshot["serviceHealth"] = map[string]interface{}{
			"controller":     nullableString(inventory.ServiceHealth.Controller),
			"passwall":       nullableString(inventory.ServiceHealth.Passwall),
			"passwallServer": nullableString(inventory.ServiceHealth.PasswallServer),
			"dnsmasq":        nullableString(inventory.ServiceHealth.DNSMasq),
		}
	}

	if rescueState != nil && rescueState.Mode != "" {
		snapshot["rescueMode"] = string(rescueState.Mode)
	} else if runtimeStatus != nil && runtimeStatus.RescueMode != "" {
		snapshot["rescueMode"] = runtimeStatus.RescueMode
	}

	if runtimeStatus != nil {
		if _, ok := snapshot["passwallEnabled"]; !ok {
			snapshot["passwallEnabled"] = runtimeStatus.PasswallEnabled
		}
		if _, ok := snapshot["selectedNodeId"]; !ok {
			snapshot["selectedNodeId"] = nullableString(runtimeStatus.SelectedNodeID)
		}
		if _, ok := snapshot["selectedNodeLabel"]; !ok {
			snapshot["selectedNodeLabel"] = nullableString(runtimeStatus.SelectedNodeLabel)
		}
		snapshot["serverReachable"] = runtimeStatus.ServerReachable
		snapshot["publicReachable"] = runtimeStatus.PublicReachable
		if serviceHealth, ok := snapshot["serviceHealth"].(map[string]interface{}); ok {
			if serviceHealth["controller"] == nil && runtimeStatus.ServiceState != "" {
				serviceHealth["controller"] = runtimeStatus.ServiceState
			}
		}
	}

	return snapshot
}

func nullableString(value string) interface{} {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func stringFromMap(payload map[string]interface{}, key string) string {
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return value
}

func truncateRescueRepairText(input string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(input, "\r\n", "\n"))
	if len(trimmed) <= maxRescueRepairOutputChars {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:maxRescueRepairOutputChars-len(terminalTruncationMessage)]) + terminalTruncationMessage
}
