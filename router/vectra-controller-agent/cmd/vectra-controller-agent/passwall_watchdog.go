package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/recovery"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

const (
	passwallWatchdogServiceReason      = "PassWall watchdog restarted local proxy because the PassWall service is not running."
	passwallWatchdogRuntimeReason      = "PassWall watchdog restarted local proxy because the expected proxy runtime process is missing."
	passwallWatchdogConnectivityReason = "PassWall watchdog restarted local proxy after proxy connectivity failed."
)

func passwallWatchdogRestartReason(inventory *controlplane.RouterInventory) (string, bool) {
	if reason, ok := passwallWatchdogServiceRestartReason(inventory); ok {
		return reason, true
	}
	return passwallWatchdogRuntimeRestartReason(inventory)
}

func passwallWatchdogServiceRestartReason(inventory *controlplane.RouterInventory) (string, bool) {
	if inventory == nil || !inventory.PasswallEnabled {
		return "", false
	}

	switch strings.ToLower(strings.TrimSpace(inventory.ServiceHealth.Passwall)) {
	case "stopped", "degraded":
		return passwallWatchdogServiceReason, true
	default:
		return "", false
	}
}

func passwallWatchdogRuntimeRestartReason(inventory *controlplane.RouterInventory) (string, bool) {
	if inventory == nil || !inventory.PasswallEnabled {
		return "", false
	}

	if strings.ToLower(strings.TrimSpace(inventory.ServiceHealth.Passwall)) != "running" {
		return "", false
	}

	for _, event := range inventory.SafetyEvents {
		if strings.TrimSpace(event.Type) != "proxy_runtime_missing" {
			continue
		}
		if strings.TrimSpace(event.Severity) != "critical" {
			continue
		}
		return passwallWatchdogRuntimeReason, true
	}

	return "", false
}

func maybeRestartPasswallWatchdog(
	ctx context.Context,
	cfg *config.Config,
	backend passwall.UCIBackend,
	persisted *state.PersistedState,
	inventory *controlplane.RouterInventory,
	runtimeStatus *state.RuntimeStatus,
	now time.Time,
	reason string,
) (bool, error) {
	if cfg == nil || persisted == nil || inventory == nil || !inventory.PasswallEnabled {
		return false, nil
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		return false, nil
	}

	persisted.ControlPlaneRecovery.Normalize()
	if recovery.PasswallOwnedByRecovery(persisted.ControlPlaneRecovery.Phase) {
		return false, nil
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	lastRestartAt := recovery.ParseTime(persisted.ControlPlaneRecovery.LastPasswallWatchdogRestartAt)
	if !lastRestartAt.IsZero() &&
		now.Sub(lastRestartAt) < passwallWatchdogCooldown(cfg.Rescue) {
		return false, nil
	}

	if _, err := backend.Run(ctx, "/etc/init.d/passwall2", "restart"); err != nil {
		return false, fmt.Errorf("passwall watchdog restart failed: %w", err)
	}

	persisted.ControlPlaneRecovery.LastPasswallWatchdogRestartAt = recovery.FormatTime(now)
	persisted.ControlPlaneRecovery.PasswallWatchdogRestartCount++
	persisted.ControlPlaneRecovery.LastPasswallWatchdogReason = reason
	persisted.ControlPlaneRecovery.LastActionReason = reason

	if runtimeStatus != nil {
		runtimeStatus.LastPasswallWatchdogAt = persisted.ControlPlaneRecovery.LastPasswallWatchdogRestartAt
		runtimeStatus.LastPasswallWatchdogReason = reason
		runtimeStatus.PasswallWatchdogRestartCount = persisted.ControlPlaneRecovery.PasswallWatchdogRestartCount
		runtimeStatus.LastRecoveryAction = reason
	}

	clearControlPlaneReachabilityCache()
	return true, nil
}

func passwallWatchdogCooldown(policy rescue.Policy) time.Duration {
	policy.Normalize()
	cooldown := policy.Cooldown
	if policy.PasswallWarmup > cooldown {
		cooldown = policy.PasswallWarmup
	}
	return cooldown
}
