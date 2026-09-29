package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/inventory"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/recovery"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

// The proxy-runtime guard.
//
// andrey-avito (Cudy WR3000H, PassWall2 26.4.10, controller 0.1.13-r40,
// 2026-09-28/29): the xray behind PassWall first refused PassWall's config and
// then vanished from disk while xray_file still named the Vectra wrapper.
// PassWall kept arming its nft/fakedns interception with no xray behind it, so
// every proxied domain was black-holed for the LAN -- the router's own curl to
// github failed "after 3 ms". The controller made it worse: the watchdog kept
// restarting PassWall on proxy_runtime_missing, and every restart re-armed the
// black hole; local rescue and control-plane recovery would switch the proxy
// back on by themselves as soon as their probes allowed.
//
// The collector now reports proxy_runtime_unusable when the runtime provably
// cannot run (see inventory.proxyRuntimeUnusableSafetyEvent). While it stands:
//   - an enabled PassWall is switched off, once -- by evaluateLocalRescue, or by
//     runOnce itself while control-plane recovery owns PassWall -- and the
//     router stays in direct;
//   - the watchdog restarts nothing;
//   - no automatic path re-enables PassWall: local rescue earns no proxy credit,
//     and every control-plane recovery retry is skipped. For a missing binary
//     that holds until the file is back. For a failed start it holds for one
//     RebootCooldown from when the failure was first seen -- the cadence the
//     operator-attention retry already uses -- because a bad node, geosite code
//     or applied config can be fixed in ways no fingerprint sees; a retry that
//     fails again is simply recorded again and falls back to direct again;
//   - operator jobs (reconnect, run_rescue_repair) still resume -- that is the
//     operator's call -- but say loudly what they are resuming over.
//
// The verdict ends by itself when the runtime changes: the missing binary
// comes back, or what a start failed on is replaced
// (inventory.ProxyRuntimeStartFailure).

// proxyRuntimeUnusable returns the collector's proxy_runtime_unusable event,
// if the proxy runtime provably cannot run.
func proxyRuntimeUnusable(collected *controlplane.RouterInventory) (controlplane.RouterSafetyEvent, bool) {
	if collected == nil {
		return controlplane.RouterSafetyEvent{}, false
	}
	for _, event := range collected.SafetyEvents {
		if strings.TrimSpace(event.Type) == inventory.ProxyRuntimeUnusableEventType {
			return event, true
		}
	}
	return controlplane.RouterSafetyEvent{}, false
}

// proxyRuntimeBlocksAutoResume decides whether the automatic paths may switch
// PassWall back on. A missing binary always blocks. A failed start blocks for
// one RebootCooldown from when it was first seen, then lets an automatic retry
// through; if that retry fails, the collector records the new attempt with a
// fresh first-seen time and the block starts over.
func proxyRuntimeBlocksAutoResume(
	collected *controlplane.RouterInventory,
	policy rescue.Policy,
	now time.Time,
) (controlplane.RouterSafetyEvent, bool) {
	event, unusable := proxyRuntimeUnusable(collected)
	if !unusable {
		return event, false
	}
	if event.Source != inventory.ProxyRuntimeStartFailureSource {
		return event, true
	}
	firstSeen := recovery.ParseTime(event.ObservedAt)
	if firstSeen.IsZero() {
		// Without a first-seen time the block could never lapse; never block
		// forever on a failed start.
		return event, false
	}
	policy.Normalize()
	return event, now.Sub(firstSeen) < policy.RebootCooldown
}

// proxyRuntimeUnusableDetail names the evidence in one line: the missing path,
// or xray's own start failure.
func proxyRuntimeUnusableDetail(event controlplane.RouterSafetyEvent) string {
	detail := strings.TrimSpace(event.Message)
	if evidence := strings.TrimSpace(event.Evidence); event.Source == inventory.ProxyRuntimeStartFailureSource && evidence != "" {
		detail += ": " + evidence
	}
	return detail
}

func proxyRuntimeUnusableDirectReason(event controlplane.RouterSafetyEvent) string {
	return fmt.Sprintf(
		"Proxy runtime cannot start (%s); router switched to direct so LAN traffic is not black-holed.",
		proxyRuntimeUnusableDetail(event),
	)
}

func proxyRuntimeRetryBlockedReason(event controlplane.RouterSafetyEvent) string {
	until := "until the runtime changes"
	if event.Source == inventory.ProxyRuntimeStartFailureSource {
		until = "until the runtime changes or the periodic retry is due"
	}
	return fmt.Sprintf(
		"Proxy runtime cannot start (%s); automatic PassWall retry skipped %s.",
		proxyRuntimeUnusableDetail(event),
		until,
	)
}

// holdRecoveryDirectForUnusableRuntime is consulted by control-plane recovery
// right before it would re-enable PassWall on its own. While the runtime cannot
// run, that retry could only re-arm the black hole, so recovery parks in
// operator attention instead -- with PassWall still off, and with a reason that
// says why -- and the retry becomes due again the moment the block lapses.
func holdRecoveryDirectForUnusableRuntime(
	recoveryState *recovery.State,
	runtimeStatus *state.RuntimeStatus,
	collected *controlplane.RouterInventory,
	policy rescue.Policy,
	now time.Time,
) bool {
	event, blocked := proxyRuntimeBlocksAutoResume(collected, policy, now)
	if !blocked {
		return false
	}
	setControlPlaneRecoveryPhase(
		recoveryState,
		runtimeStatus,
		recovery.PhaseOperatorAttention,
		proxyRuntimeRetryBlockedReason(event),
		true,
	)
	return true
}

// failSafeUnusableRuntimeUnderRecovery is the fail-safe for the phases in
// which control-plane recovery owns PassWall and evaluateLocalRescue does not
// run. Recovery only ever acts on a disabled PassWall there, so PassWall
// switched on over a broken runtime in those phases -- an apply with the main
// switch on, the owner toggling LuCI, a periodic retry whose start failed --
// would otherwise stay on until the phase ends. It switches PassWall off
// through recovery's own direct transition and leaves the phase alone.
//
// It acts only on what this cycle's collection saw, before any job runs, so it
// never undoes an operator reconnect from the same cycle; if that reconnect's
// fresh start fails too, the next cycle sees the new attempt and acts then.
func failSafeUnusableRuntimeUnderRecovery(
	ctx context.Context,
	backend passwall.UCIBackend,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	collected *controlplane.RouterInventory,
	runtimeStatus *state.RuntimeStatus,
	now time.Time,
) (bool, error) {
	if collected == nil || !collected.PasswallEnabled {
		return false, nil
	}
	event, unusable := proxyRuntimeUnusable(collected)
	if !unusable {
		return false, nil
	}
	reason := proxyRuntimeUnusableDirectReason(event)
	if err := enterDirectModeForRecovery(ctx, backend, rescueState, persisted, runtimeStatus, reason, now); err != nil {
		return false, err
	}
	log.Printf("%s", reason)
	return true, nil
}

// operatorProxyResumeWarning is logged by operator jobs that re-enable
// PassWall. They keep that authority -- the operator may know the runtime was
// just fixed, and a fresh start is the proof either way -- but the warning makes
// clear what they resume over. If the runtime still cannot start, the next
// cycle's fail-safe puts the router back in direct.
func operatorProxyResumeWarning(collected *controlplane.RouterInventory, job string) string {
	event, unusable := proxyRuntimeUnusable(collected)
	if !unusable {
		return ""
	}
	warning := fmt.Sprintf(
		"%s re-enables PassWall on operator authority although the proxy runtime cannot start (%s); if it still cannot, the controller falls back to direct on its next cycle",
		job,
		proxyRuntimeUnusableDetail(event),
	)
	log.Printf("warning: %s", warning)
	return warning
}

// collectWithCollector runs a collector; tests swap it to drive runOnce
// without touching the host.
var collectWithCollector = func(collector inventory.Collector, base controlplane.RouterInventory) controlplane.RouterInventory {
	return collector.Collect(base)
}

// collectCycleInventory is collectInventoryWithRuntimeVersion for the run
// loop: the collector keeps its proxy start-failure memory in the persisted
// state, which is what lets proxy_runtime_unusable end by itself when the
// runtime is replaced instead of condemning a repaired router forever.
func collectCycleInventory(
	base controlplane.RouterInventory,
	persisted *state.PersistedState,
) controlplane.RouterInventory {
	if persisted == nil {
		return collectInventoryWithRuntimeVersion(base)
	}
	collected := collectWithCollector(inventory.Collector{ProxyRuntimeFailure: &persisted.ProxyRuntimeFailure}, base)
	applyControllerRuntimeVersion(&collected)
	return collected
}

// rescueRepairActionsForRuntime drops reconnect_proxy from a rescue repair when
// the xray binary is missing. The panel's unattended auto-rescue sends that
// action for every direct-mode case, three times in half an hour; over a
// missing binary each one only re-arms PassWall's interception for a cycle, and
// no operator is behind it to accept that. Resuming over a missing binary can
// never work, so it is refused outright; the other repairs still run. A failed
// start (source passwall_log) is not refused — it may have been fixed by the
// repair itself, and the operator-authority warning covers it.
func rescueRepairActionsForRuntime(actions []string, collected *controlplane.RouterInventory) ([]string, string) {
	event, unusable := proxyRuntimeUnusable(collected)
	if !unusable || event.Source == inventory.ProxyRuntimeStartFailureSource {
		return actions, ""
	}
	kept := make([]string, 0, len(actions))
	refused := false
	for _, action := range actions {
		if action == rescueRepairActionReconnectProxy {
			refused = true
			continue
		}
		kept = append(kept, action)
	}
	if !refused {
		return actions, ""
	}
	return kept, fmt.Sprintf(
		"reconnect_proxy refused: proxy runtime cannot start (%s); reinstall xray (update xray-runtime) before resuming the proxy.",
		proxyRuntimeUnusableDetail(event),
	)
}
