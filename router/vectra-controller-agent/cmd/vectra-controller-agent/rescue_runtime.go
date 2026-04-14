package main

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

func reconcileRescueStateWithInventory(
	rescueState *rescue.State,
	inventory *controlplane.RouterInventory,
) {
	expectedMode := rescue.ModeProxy
	if !inventory.PasswallEnabled {
		expectedMode = rescue.ModeDirect
	}

	if rescueState.Mode == "" {
		rescueState.Mode = expectedMode
		return
	}

	if rescueState.Mode != expectedMode {
		rescueState.Mode = expectedMode
		rescueState.ProxyFailureCount = 0
		rescueState.DirectSuccessCount = 0
		rescueState.ProxySuccessCount = 0
	}
}

func applyRescueMetadata(
	persisted *state.PersistedState,
	rescueState *rescue.State,
	inventory *controlplane.RouterInventory,
	runtimeStatus *state.RuntimeStatus,
) {
	if rescueState.Mode != rescue.ModeDirect {
		clearPersistedRescueMetadata(persisted, runtimeStatus)
	}

	persisted.Rescue.State = *rescueState
	runtimeStatus.RescueMode = string(rescueState.Mode)
	runtimeStatus.LastRescueReason = persisted.Rescue.LastReason
	runtimeStatus.LastRescueAt = persisted.Rescue.HappenedAt
	runtimeStatus.PasswallEnabled = inventory.PasswallEnabled
	runtimeStatus.SelectedNodeID = inventory.SelectedNodeID
	runtimeStatus.SelectedNodeLabel = inventory.SelectedNodeLabel
	runtimeStatus.ServiceState = inventory.ServiceHealth.Controller
	runtimeStatus.ProxyFailureCount = rescueState.ProxyFailureCount
	runtimeStatus.ProxySuccessCount = rescueState.ProxySuccessCount
	runtimeStatus.DirectSuccessCount = rescueState.DirectSuccessCount

	if rescueState.Mode == rescue.ModeDirect &&
		persisted.Rescue.LastReason != "" &&
		persisted.Rescue.HappenedAt != "" {
		mode := persisted.Rescue.LastMode
		if mode == "" {
			mode = string(rescue.ModeDirect)
		}
		inventory.LastRescue = &controlplane.LastRescue{
			Mode:       mode,
			Reason:     persisted.Rescue.LastReason,
			HappenedAt: persisted.Rescue.HappenedAt,
		}
	} else {
		inventory.LastRescue = nil
	}
}

func clearPersistedRescueMetadata(
	persisted *state.PersistedState,
	runtimeStatus *state.RuntimeStatus,
) {
	if persisted != nil {
		persisted.Rescue.LastMode = ""
		persisted.Rescue.LastReason = ""
		persisted.Rescue.HappenedAt = ""
	}

	if runtimeStatus != nil {
		runtimeStatus.LastRescueReason = ""
		runtimeStatus.LastRescueAt = ""
	}
}

func evaluateLocalRescue(
	ctx context.Context,
	cfg *config.Config,
	backend passwall.UCIBackend,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	inventory *controlplane.RouterInventory,
	runtimeStatus *state.RuntimeStatus,
) (bool, controlplane.RouterHealth, error) {
	now := time.Now().UTC()
	reconcileRescueStateWithInventory(rescueState, inventory)

	prober := rescue.NewHTTPProber(probeTimeout(cfg.RequestTimeout))
	serverProbe := rescue.ProbeAny(ctx, prober, serverHealthURLs(cfg.ControlURL))
	publicProbe := rescue.ProbeAny(ctx, prober, cfg.Rescue.HealthURLs)
	serverReachable := serverProbe.Reachable

	runtimeStatus.ServerReachable = serverProbe.Reachable
	runtimeStatus.PublicReachable = publicProbe.Reachable
	runtimeStatus.LastServerError = serverProbe.Error
	runtimeStatus.LastPublicError = publicProbe.Error

	input := rescue.EvaluationInput{
		Now:    now,
		Policy: cfg.Rescue,
		State:  *rescueState,
	}

	switch rescueState.Mode {
	case rescue.ModeDirect:
		if publicProbe.Reachable {
			input.DirectSuccessIncrement = 1
		}
		if publicProbe.Reachable {
			if proxyReachable, _, _ := probeProxyPath(ctx, backend, inventory.SelectedNodeID); proxyReachable {
				input.ProxySuccessIncrement = 1
			}
		}
	default:
		if publicProbe.Reachable {
			input.ProxySuccessIncrement = 1
		} else {
			proxyReachable, proxyConclusive, _ := probeProxyPath(ctx, backend, inventory.SelectedNodeID)
			if proxyReachable {
				input.ProxySuccessIncrement = 1
			} else if proxyConclusive {
				input.ProxyFailureIncrement = 1
				if cfg.Rescue.RequireDirectPathSuccess &&
					rescue.ShouldAttemptDirectFallback(now, cfg.Rescue, *rescueState) {
					directReachable, err := validateDirectFallback(ctx, cfg, backend)
					if err != nil {
						applyRescueMetadata(persisted, rescueState, inventory, runtimeStatus)
						return false, buildRouterHealth(*rescueState, serverReachable), err
					}
					if directReachable {
						input.DirectSuccessIncrement = 1
					}
				}
			}
		}
	}

	decision := rescue.Evaluate(input)
	*rescueState = decision.NextState

	transitioned := false
	if decision.ShouldTransition {
		switch decision.NextMode {
		case rescue.ModeDirect:
			if err := setPasswallMainSwitch(ctx, backend, false, mainSwitchOptions{
				Reason: decision.Reason,
			}); err != nil {
				applyRescueMetadata(persisted, rescueState, inventory, runtimeStatus)
				return false, buildRouterHealth(*rescueState, serverReachable), err
			}
			persisted.Rescue.LastMode = string(rescue.ModeDirect)
			persisted.Rescue.LastReason = decision.Reason
			persisted.Rescue.HappenedAt = now.Format(time.RFC3339)
			transitioned = true
		case rescue.ModeProxy:
			if err := setPasswallMainSwitch(ctx, backend, true, mainSwitchOptions{
				ClearRescueReason: true,
			}); err != nil {
				applyRescueMetadata(persisted, rescueState, inventory, runtimeStatus)
				return false, buildRouterHealth(*rescueState, serverReachable), err
			}
			clearPersistedRescueMetadata(persisted, runtimeStatus)
			transitioned = true
		}
	}

	applyRescueMetadata(persisted, rescueState, inventory, runtimeStatus)
	return transitioned, buildRouterHealth(*rescueState, serverReachable), nil
}

func buildRouterHealth(state rescue.State, serverReachable bool) controlplane.RouterHealth {
	return controlplane.RouterHealth{
		CurrentMode:                 string(state.Mode),
		PublicConnectivityFailures:  state.ProxyFailureCount,
		DirectConnectivitySuccesses: state.DirectSuccessCount,
		ProxyConnectivitySuccesses:  state.ProxySuccessCount,
		ServerReachable:             serverReachable,
	}
}

func setPasswallMainSwitch(
	ctx context.Context,
	backend passwall.UCIBackend,
	enabled bool,
	options mainSwitchOptions,
) error {
	value := "0"
	if enabled {
		value = "1"
	}

	commands := []string{
		fmt.Sprintf("set passwall2.@global[0].enabled='%s'", value),
		"commit passwall2",
	}
	if strings.TrimSpace(options.Reason) != "" {
		commands = append(commands,
			fmt.Sprintf("set vectra-controller.main.last_rescue_reason='%s'", escapeBatchValue(options.Reason)),
			"commit vectra-controller",
		)
	} else if options.ClearRescueReason {
		commands = append(commands,
			"set vectra-controller.main.last_rescue_reason=''",
			"commit vectra-controller",
		)
	}

	if err := backend.Batch(ctx, commands); err != nil {
		return err
	}

	_, err := backend.Run(ctx, "/etc/init.d/passwall2", "restart")
	return err
}

func validateDirectFallback(
	ctx context.Context,
	cfg *config.Config,
	backend passwall.UCIBackend,
) (bool, error) {
	if err := setPasswallMainSwitch(ctx, backend, false, mainSwitchOptions{}); err != nil {
		return false, fmt.Errorf("disable passwall for direct fallback probe: %w", err)
	}

	prober := rescue.NewHTTPProber(probeTimeout(cfg.RequestTimeout))
	result := rescue.ProbeAny(ctx, prober, cfg.Rescue.HealthURLs)

	if err := setPasswallMainSwitch(ctx, backend, true, mainSwitchOptions{}); err != nil {
		return false, fmt.Errorf("restore passwall after direct fallback probe: %w", err)
	}

	return result.Reachable, nil
}

type mainSwitchOptions struct {
	Reason            string
	ClearRescueReason bool
}

func probeProxyPath(
	ctx context.Context,
	backend passwall.UCIBackend,
	selectedNodeID string,
) (bool, bool, passwall.CommandResult) {
	if strings.TrimSpace(selectedNodeID) == "" {
		result := passwall.CommandResult{
			Command: "/usr/share/passwall2/test.sh url_test_node <selected-node>",
			Stderr:  "selected node is not configured",
		}
		return false, false, result
	}

	probeNodeID, conclusive, result := resolveProxyProbeNode(
		ctx,
		backend,
		selectedNodeID,
	)
	if !conclusive {
		return false, false, result
	}

	result, err := backend.Run(ctx, "/usr/share/passwall2/test.sh", "url_test_node", probeNodeID)
	if err != nil {
		return false, true, result
	}

	output := strings.TrimSpace(result.Stdout)
	return strings.HasPrefix(output, "200:") ||
		strings.HasPrefix(output, "204:") ||
		output == "200" ||
		output == "204", true, result
}

func resolveProxyProbeNode(
	ctx context.Context,
	backend passwall.UCIBackend,
	selectedNodeID string,
) (string, bool, passwall.CommandResult) {
	const maxVirtualResolutionDepth = 4

	currentNodeID := strings.TrimSpace(selectedNodeID)
	for depth := 0; depth < maxVirtualResolutionDepth; depth++ {
		protocol, ok := selectedNodeProtocol(ctx, backend, currentNodeID)
		if !ok {
			return currentNodeID, true, passwall.CommandResult{}
		}

		if !isVirtualSelectedNodeProtocol(protocol) {
			return currentNodeID, true, passwall.CommandResult{}
		}

		if protocol != "_shunt" {
			return "", false, passwall.CommandResult{
				Command: "uci -q get passwall2.<selected-node>.protocol",
				Stdout:  protocol,
				Stderr:  "selected node uses a virtual protocol and cannot prove proxy outage directly",
			}
		}

		defaultNodeID, ok := getUCIValue(
			ctx,
			backend,
			fmt.Sprintf("passwall2.%s.default_node", currentNodeID),
		)
		if !ok {
			return "", false, passwall.CommandResult{
				Command: fmt.Sprintf("uci -q get passwall2.%s.default_node", currentNodeID),
				Stdout:  protocol,
				Stderr:  "selected shunt node does not define a concrete default outbound node",
			}
		}

		defaultNodeID = strings.TrimSpace(defaultNodeID)
		switch strings.ToLower(defaultNodeID) {
		case "", "_default", "_direct", "_blackhole":
			return "", false, passwall.CommandResult{
				Command: fmt.Sprintf("uci -q get passwall2.%s.default_node", currentNodeID),
				Stdout:  defaultNodeID,
				Stderr:  "selected shunt node points to a non-proxy default path and cannot prove proxy outage directly",
			}
		}

		if _, ok := selectedNodeProtocol(ctx, backend, defaultNodeID); !ok {
			return "", false, passwall.CommandResult{
				Command: fmt.Sprintf("uci -q get passwall2.%s.protocol", defaultNodeID),
				Stdout:  defaultNodeID,
				Stderr:  "selected shunt node points to a missing default outbound node",
			}
		}

		currentNodeID = defaultNodeID
	}

	return "", false, passwall.CommandResult{
		Command: fmt.Sprintf("uci -q get passwall2.%s.default_node", selectedNodeID),
		Stderr:  "selected node virtual chain is too deep to resolve safely",
	}
}

func selectedNodeProtocol(
	ctx context.Context,
	backend passwall.UCIBackend,
	selectedNodeID string,
) (string, bool) {
	result, ok := getUCIValue(
		ctx,
		backend,
		fmt.Sprintf("passwall2.%s.protocol", selectedNodeID),
	)
	if !ok {
		return "", false
	}

	return strings.ToLower(strings.TrimSpace(result)), true
}

func getUCIValue(
	ctx context.Context,
	backend passwall.UCIBackend,
	key string,
) (string, bool) {
	result, err := backend.Run(
		ctx,
		"uci",
		"-q",
		"get",
		key,
	)
	if err != nil {
		return "", false
	}

	value := strings.TrimSpace(result.Stdout)
	if value == "" {
		return "", false
	}

	return value, true
}

func isVirtualSelectedNodeProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "_shunt", "_balancing", "_urltest":
		return true
	default:
		return false
	}
}

func resumeProxyMode(
	ctx context.Context,
	backend passwall.UCIBackend,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	runtimeStatus *state.RuntimeStatus,
	now time.Time,
) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if err := setPasswallMainSwitch(ctx, backend, true, mainSwitchOptions{
		ClearRescueReason: true,
	}); err != nil {
		return err
	}

	if rescueState != nil {
		rescueState.Mode = rescue.ModeProxy
		rescueState.ProxyFailureCount = 0
		rescueState.DirectSuccessCount = 0
		rescueState.ProxySuccessCount = 0
		rescueState.LastTransitionAt = now
	}

	if persisted != nil && rescueState != nil {
		persisted.Rescue.State = *rescueState
	}

	if runtimeStatus != nil {
		runtimeStatus.RescueMode = string(rescue.ModeProxy)
		runtimeStatus.LastRescueReason = ""
		runtimeStatus.LastRescueAt = ""
		runtimeStatus.PasswallEnabled = true
		runtimeStatus.ProxyFailureCount = 0
		runtimeStatus.DirectSuccessCount = 0
		runtimeStatus.ProxySuccessCount = 0
	}

	clearPersistedRescueMetadata(persisted, runtimeStatus)

	return nil
}

func serverHealthURLs(controlURL string) []string {
	if strings.TrimSpace(controlURL) == "" {
		return nil
	}

	parsed, err := url.Parse(controlURL)
	if err != nil {
		return []string{strings.TrimRight(controlURL, "/") + "/api/health"}
	}
	parsed.Path = path.Join(parsed.Path, "/api/health")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return []string{parsed.String()}
}

func probeTimeout(requestTimeout time.Duration) time.Duration {
	if requestTimeout > 0 && requestTimeout < 5*time.Second {
		return requestTimeout
	}
	return 5 * time.Second
}

func escapeBatchValue(value string) string {
	return strings.ReplaceAll(value, "'", "'\\''")
}
