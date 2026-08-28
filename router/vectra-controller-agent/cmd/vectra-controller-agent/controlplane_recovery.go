package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/recovery"
	"vectra-controller-agent/internal/rescue"
	"vectra-controller-agent/internal/state"
)

type controlPlaneRecoveryOutcome struct {
	SkipControlPlane bool
	InventoryChanged bool
}

type probeTarget struct {
	ID    string
	Label string
	URL   string
}

type reachabilityGroups struct {
	Panel   *controlplane.RouterReachabilityProbe
	RU      *controlplane.RouterReachabilityProbe
	Foreign *controlplane.RouterReachabilityProbe
}

type cachedReachabilityGroups struct {
	Groups    reachabilityGroups
	ExpiresAt time.Time
}

var controlPlaneReachabilityCache = struct {
	mu      sync.Mutex
	entries map[string]cachedReachabilityGroups
}{
	entries: map[string]cachedReachabilityGroups{},
}

// recoveryProcessStartedAt anchors the no-contact-from-boot outage window for
// routers that strand before they ever record a successful control-plane
// contact (e.g. right after onboarding commits enabled=1 + a proxy
// default_node). It is captured once at process start and only overridden by
// tests.
var recoveryProcessStartedAt = time.Now().UTC()

var ruProbeTargets = []probeTarget{
	{ID: "ya", Label: "ya.ru", URL: "https://ya.ru/"},
	{ID: "vk", Label: "vk.com", URL: "https://vk.com/"},
}

var foreignProbeTargets = []probeTarget{
	{ID: "youtube", Label: "youtube.com", URL: "https://www.youtube.com/"},
	{ID: "instagram", Label: "instagram.com", URL: "https://www.instagram.com/"},
	{ID: "telegram", Label: "telegram.org", URL: "https://telegram.org/"},
}

const (
	controlPlaneRestartReason    = "Control plane unreachable for over one hour; scheduled local vectra-controller restart."
	controlPlaneDirectReason     = "Control plane unreachable and proxy-dependent internet checks failed; router switched to direct mode."
	controlPlaneRebootReason     = "Control plane still unreachable after direct fallback; scheduled one router reboot within recovery budget."
	controlPlaneRetryReason      = "RU connectivity restored after reboot; retrying PassWall proxy path."
	operatorAttentionReason      = "After auto-reboot and PassWall retry, foreign resources are still unavailable; router left in direct mode."
	panelRecoveredDirectReason   = "Control plane recovered only in direct mode; router is waiting for operator review."
	autoProxyRetryReason         = "Control plane recovered after direct fallback; retrying PassWall proxy path before operator attention."
	proxyNodeRecoveredReason     = "PassWall proxy node responded after retry; keeping proxy mode while service probes refresh."
	wanRecoveredReason           = "Control plane and foreign connectivity recovered."
	operatorAttentionRetryReason = "Still awaiting operator; auto-retrying PassWall proxy path before falling back to direct again."
)

func advanceControlPlaneRecovery(
	ctx context.Context,
	cfg *config.Config,
	backend passwall.UCIBackend,
	recoveryState *recovery.State,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	inventory *controlplane.RouterInventory,
	runtimeStatus *state.RuntimeStatus,
) (controlPlaneRecoveryOutcome, error) {
	outcome := controlPlaneRecoveryOutcome{}
	if recoveryState == nil || inventory == nil {
		return outcome, nil
	}

	recoveryState.Normalize()
	now := time.Now().UTC()
	panelProbe, err := collectPanelReachability(ctx, cfg)
	if err != nil {
		return outcome, err
	}

	inventory.PanelReachability = panelProbe
	recoveryState.LastPanelStatus = probeStatus(panelProbe)
	runtimeStatus.LastPanelStatus = recoveryState.LastPanelStatus
	runtimeStatus.RecoveryPhase = string(recoveryState.Phase)
	runtimeStatus.LastRecoveryAction = recoveryState.LastActionReason
	runtimeStatus.AwaitingOperator = recoveryState.AwaitingOperator
	runtimeStatus.ServerReachable = panelProbe != nil && panelProbe.Reachable

	hasSuccessfulContact := !recovery.ParseTime(recoveryState.LastSuccessfulControlPlaneAt).IsZero()

	if !panelProbe.Reachable && recovery.ParseTime(recoveryState.OutageStartedAt).IsZero() {
		// A router that has never recorded a successful contact still has to fail
		// safe to direct eventually. Anchor the outage to process/boot start so the
		// existing bounded direct-fallback path arms once the no-contact-from-boot
		// window exceeds the panel-outage threshold. The first few minutes after
		// boot stay a no-op because controlPlaneOutageReady measures against this
		// anchor.
		outageStart := now
		if !hasSuccessfulContact {
			if bootStart := recoveryProcessStartedAt.UTC(); !bootStart.IsZero() && bootStart.Before(now) {
				outageStart = bootStart
			}
		}
		recoveryState.OutageStartedAt = recovery.FormatTime(outageStart)
		if recoveryState.Phase == recovery.PhaseIdle {
			recoveryState.Phase = recovery.PhaseMonitoring
		}
	}

	needsGroupedProbes := recoveryState.Phase != recovery.PhaseIdle ||
		recoveryState.AwaitingOperator ||
		controlPlaneOutageReady(now, cfg.Rescue, recoveryState)
	if needsGroupedProbes {
		groups, err := collectRecoveryReachabilityGroups(ctx, cfg)
		if err != nil {
			return outcome, err
		}
		if groups.Panel != nil {
			inventory.PanelReachability = groups.Panel
			recoveryState.LastPanelStatus = probeStatus(groups.Panel)
			runtimeStatus.LastPanelStatus = recoveryState.LastPanelStatus
			runtimeStatus.ServerReachable = groups.Panel.Reachable
		}
		inventory.RUReachability = groups.RU
		inventory.ForeignReachability = groups.Foreign
		recoveryState.LastRUStatus = probeStatus(groups.RU)
		recoveryState.LastForeignStatus = probeStatus(groups.Foreign)
		runtimeStatus.LastRUStatus = recoveryState.LastRUStatus
		runtimeStatus.LastForeignStatus = recoveryState.LastForeignStatus
	}

	switch recoveryState.Phase {
	case recovery.PhaseIdle:
		if !panelProbe.Reachable {
			outcome.SkipControlPlane = true
			if controlPlaneOutageReady(now, cfg.Rescue, recoveryState) {
				return startControlPlaneRecovery(
					ctx,
					cfg,
					backend,
					recoveryState,
					rescueState,
					persisted,
					inventory,
					runtimeStatus,
					now,
				)
			}
			recoveryState.Phase = recovery.PhaseMonitoring
			runtimeStatus.RecoveryPhase = string(recoveryState.Phase)
		} else if shouldResumeProxyAfterExternalDirect(now, cfg.Rescue, recoveryState, inventory, panelProbe) {
			return resumeProxyAfterExternalDirect(ctx, backend, recoveryState, rescueState, persisted, runtimeStatus, now)
		}
	case recovery.PhaseMonitoring:
		if !panelProbe.Reachable {
			outcome.SkipControlPlane = true
			if controlPlaneOutageReady(now, cfg.Rescue, recoveryState) {
				return startControlPlaneRecovery(
					ctx,
					cfg,
					backend,
					recoveryState,
					rescueState,
					persisted,
					inventory,
					runtimeStatus,
					now,
				)
			}
		} else if shouldResumeProxyAfterExternalDirect(now, cfg.Rescue, recoveryState, inventory, panelProbe) {
			return resumeProxyAfterExternalDirect(ctx, backend, recoveryState, rescueState, persisted, runtimeStatus, now)
		}
	case recovery.PhaseControllerRestartWait:
		if !panelProbe.Reachable {
			outcome.SkipControlPlane = true
			lastRestartAt := recovery.ParseTime(recoveryState.LastControllerRestartAt)
			if !lastRestartAt.IsZero() &&
				now.Sub(lastRestartAt) >= effectiveControllerRestartSettle(cfg) {
				recoveryState.Phase = recovery.PhaseMonitoring
				runtimeStatus.RecoveryPhase = string(recoveryState.Phase)
			}
		}
	case recovery.PhaseDirectSettle:
		outcome.SkipControlPlane = true
		if now.Sub(rescueState.LastTransitionAt) < cfg.Rescue.DirectSettle {
			break
		}
		if panelProbe.Reachable {
			if shouldAutoRetryPasswallAfterDirectSettle(inventory, recoveryState) {
				if err := resumeProxyMode(ctx, backend, rescueState, persisted, runtimeStatus, now); err != nil {
					return outcome, err
				}
				recoveryState.LastPasswallRetryAt = recovery.FormatTime(now)
				setControlPlaneRecoveryPhase(
					recoveryState,
					runtimeStatus,
					recovery.PhasePasswallRetryWait,
					autoProxyRetryReason,
					false,
				)
				outcome.InventoryChanged = true
				break
			}
			setControlPlaneRecoveryPhase(
				recoveryState,
				runtimeStatus,
				recovery.PhaseOperatorAttention,
				panelRecoveredDirectReason,
				true,
			)
			outcome.SkipControlPlane = false
			break
		}
		if shouldTriggerReboot(inventory, recoveryState) &&
			canScheduleAutoReboot(now, cfg.Rescue, recoveryState) {
			recoveryState.LastAutoRebootAt = recovery.FormatTime(now)
			setControlPlaneRecoveryPhase(
				recoveryState,
				runtimeStatus,
				recovery.PhaseRebootWait,
				controlPlaneRebootReason,
				false,
			)
			if err := scheduleRouterReboot(ctx, backend); err != nil {
				return outcome, err
			}
			break
		}
	case recovery.PhaseRebootWait:
		outcome.SkipControlPlane = true
		setControlPlaneRecoveryPhase(
			recoveryState,
			runtimeStatus,
			recovery.PhasePostRebootCheck,
			recoveryState.LastActionReason,
			false,
		)
	case recovery.PhasePostRebootCheck:
		outcome.SkipControlPlane = true
		if now.Sub(recovery.ParseTime(recoveryState.LastAutoRebootAt)) < cfg.Rescue.PostRebootSettle {
			break
		}
		if inventory.RUReachability != nil && inventory.RUReachability.Status == recovery.StatusReachable {
			if !inventory.PasswallEnabled {
				if err := resumeProxyMode(ctx, backend, rescueState, persisted, runtimeStatus, now); err != nil {
					return outcome, err
				}
				recoveryState.LastPasswallRetryAt = recovery.FormatTime(now)
				setControlPlaneRecoveryPhase(
					recoveryState,
					runtimeStatus,
					recovery.PhasePasswallRetryWait,
					controlPlaneRetryReason,
					false,
				)
				outcome.InventoryChanged = true
				break
			}
			if inventory.ForeignReachability != nil &&
				inventory.ForeignReachability.Status == recovery.StatusHealthy {
				setControlPlaneRecoveryPhase(
					recoveryState,
					runtimeStatus,
					recovery.PhaseIdle,
					wanRecoveredReason,
					false,
				)
				outcome.SkipControlPlane = false
				break
			}
			if proxyNodeReachableForRecovery(ctx, backend, inventory) {
				setControlPlaneRecoveryPhase(
					recoveryState,
					runtimeStatus,
					recovery.PhaseIdle,
					proxyNodeRecoveredReason,
					false,
				)
				outcome.SkipControlPlane = false
				break
			}
		}
		if inventory.PasswallEnabled {
			if err := enterDirectModeForRecovery(
				ctx,
				backend,
				rescueState,
				persisted,
				runtimeStatus,
				operatorAttentionReason,
				now,
			); err != nil {
				return outcome, err
			}
			outcome.InventoryChanged = true
		}
		setControlPlaneRecoveryPhase(
			recoveryState,
			runtimeStatus,
			recovery.PhaseOperatorAttention,
			operatorAttentionReason,
			true,
		)
		outcome.SkipControlPlane = panelProbe == nil || !panelProbe.Reachable
	case recovery.PhasePasswallRetryWait:
		outcome.SkipControlPlane = true
		if now.Sub(recovery.ParseTime(recoveryState.LastPasswallRetryAt)) < cfg.Rescue.PasswallWarmup {
			break
		}
		if inventory.ForeignReachability != nil &&
			inventory.ForeignReachability.Status == recovery.StatusHealthy {
			setControlPlaneRecoveryPhase(
				recoveryState,
				runtimeStatus,
				recovery.PhaseIdle,
				wanRecoveredReason,
				false,
			)
			outcome.SkipControlPlane = false
			break
		}
		if proxyNodeReachableForRecovery(ctx, backend, inventory) {
			setControlPlaneRecoveryPhase(
				recoveryState,
				runtimeStatus,
				recovery.PhaseIdle,
				proxyNodeRecoveredReason,
				false,
			)
			outcome.SkipControlPlane = false
			break
		}
		if inventory.PasswallEnabled {
			if err := enterDirectModeForRecovery(
				ctx,
				backend,
				rescueState,
				persisted,
				runtimeStatus,
				operatorAttentionReason,
				now,
			); err != nil {
				return outcome, err
			}
			outcome.InventoryChanged = true
		}
		setControlPlaneRecoveryPhase(
			recoveryState,
			runtimeStatus,
			recovery.PhaseOperatorAttention,
			operatorAttentionReason,
			true,
		)
		outcome.SkipControlPlane = panelProbe == nil || !panelProbe.Reachable
	case recovery.PhaseOperatorAttention:
		outcome.SkipControlPlane = panelProbe == nil || !panelProbe.Reachable
		// GAP-5: operator attention must never be a terminal park. While the panel
		// is still unreachable and the router is parked in direct, re-attempt the
		// proxy path once per RebootCooldown. The AwaitingOperator flag stays set
		// for panel visibility, but auto-recovery keeps looping forever: the
		// re-armed PhasePasswallRetryWait path re-disables PassWall on failure, so
		// the router stays reachable in direct and never requires a human.
		// The panel-reachable case used to be excluded outright, on the theory
		// that a live panel would decide. It did not: the panel only ever
		// resolves a park on a transition the ROUTER sends, so both sides waited
		// for each other and routers sat in direct for days (DmitryGubenko,
		// 2026-08-26, 33 hours with every bound node answering 204).
		//
		// With a live panel this now retries only on positive proof that the
		// node answers — measurable with the proxy stopped, see
		// proxyNodeReachableForRecovery. With a dead panel the old blind retry
		// stands: nobody else can help, so trying is strictly better than
		// parking. Both stay behind the RebootCooldown.
		if !inventory.PasswallEnabled &&
			operatorAttentionRetryReady(now, cfg.Rescue, recoveryState) &&
			(panelProbe == nil || !panelProbe.Reachable ||
				proxyNodeReachableForRecovery(ctx, backend, inventory)) {
			if err := resumeProxyMode(ctx, backend, rescueState, persisted, runtimeStatus, now); err != nil {
				return outcome, err
			}
			recoveryState.LastPasswallRetryAt = recovery.FormatTime(now)
			setControlPlaneRecoveryPhase(
				recoveryState,
				runtimeStatus,
				recovery.PhasePasswallRetryWait,
				operatorAttentionRetryReason,
				true,
			)
			outcome.InventoryChanged = true
			outcome.SkipControlPlane = true
		}
	}

	runtimeStatus.RecoveryPhase = string(recoveryState.Phase)
	runtimeStatus.LastRecoveryAction = recoveryState.LastActionReason
	runtimeStatus.AwaitingOperator = recoveryState.AwaitingOperator
	return outcome, nil
}

func startControlPlaneRecovery(
	ctx context.Context,
	cfg *config.Config,
	backend passwall.UCIBackend,
	recoveryState *recovery.State,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	inventory *controlplane.RouterInventory,
	runtimeStatus *state.RuntimeStatus,
	now time.Time,
) (controlPlaneRecoveryOutcome, error) {
	outcome := controlPlaneRecoveryOutcome{SkipControlPlane: true}

	switch {
	case inventory.RUReachability != nil &&
		inventory.RUReachability.Status == recovery.StatusReachable &&
		(inventory.ForeignReachability == nil ||
			inventory.ForeignReachability.Status == recovery.StatusHealthy):
		restartedThisOutage := restartedDuringCurrentOutage(recoveryState)
		setControlPlaneRecoveryPhase(
			recoveryState,
			runtimeStatus,
			recovery.PhaseControllerRestartWait,
			controlPlaneRestartReason,
			false,
		)
		if restartedThisOutage {
			return outcome, nil
		}
		recoveryState.LastControllerRestartAt = recovery.FormatTime(now)
		if err := scheduleControllerServiceRestart(ctx, backend); err != nil {
			return outcome, err
		}
		return outcome, nil
	case shouldTriggerDirectFallback(inventory):
		if !inventory.PasswallEnabled {
			setControlPlaneRecoveryPhase(
				recoveryState,
				runtimeStatus,
				recovery.PhaseMonitoring,
				"",
				false,
			)
			return outcome, nil
		}
		noteDirectRecoveryWindow(
			rescueState,
			persisted,
			inventory,
			runtimeStatus,
			now,
		)
		if inventory.PasswallEnabled {
			if err := enterDirectModeForRecovery(
				ctx,
				backend,
				rescueState,
				persisted,
				runtimeStatus,
				controlPlaneDirectReason,
				now,
			); err != nil {
				return outcome, err
			}
			outcome.InventoryChanged = true
		}
		setControlPlaneRecoveryPhase(
			recoveryState,
			runtimeStatus,
			recovery.PhaseDirectSettle,
			controlPlaneDirectReason,
			false,
		)
		return outcome, nil
	default:
		recoveryState.Phase = recovery.PhaseMonitoring
		runtimeStatus.RecoveryPhase = string(recoveryState.Phase)
		return outcome, nil
	}
}

func noteSuccessfulControlPlaneContact(
	persisted *state.PersistedState,
	runtimeStatus *state.RuntimeStatus,
	now time.Time,
) {
	if persisted == nil {
		return
	}

	recoveryState := &persisted.ControlPlaneRecovery
	recoveryState.Normalize()
	recoveryState.LastSuccessfulControlPlaneAt = recovery.FormatTime(now)
	recoveryState.OutageStartedAt = ""
	recoveryState.LastPanelStatus = recovery.StatusReachable
	clearControlPlaneReachabilityCache()

	switch recoveryState.Phase {
	case recovery.PhaseIdle,
		recovery.PhaseMonitoring,
		recovery.PhaseControllerRestartWait:
		recoveryState.Phase = recovery.PhaseIdle
		recoveryState.AwaitingOperator = false
		recoveryState.LastActionReason = ""
	}

	if runtimeStatus != nil {
		runtimeStatus.LastPanelStatus = recoveryState.LastPanelStatus
		runtimeStatus.RecoveryPhase = string(recoveryState.Phase)
		runtimeStatus.LastRecoveryAction = recoveryState.LastActionReason
		runtimeStatus.AwaitingOperator = recoveryState.AwaitingOperator
		runtimeStatus.ServerReachable = true
	}
}

func collectPanelReachability(
	ctx context.Context,
	cfg *config.Config,
) (*controlplane.RouterReachabilityProbe, error) {
	urls := serverHealthURLs(cfg.ControlURL)
	if len(urls) == 0 {
		return nil, nil
	}

	// The panel probe must measure the SAME direct path the check-in uses: its
	// sockets carry the control-plane fwmark so they bypass the PassWall2 tproxy
	// via the nftables carve-out. Without this a dead-proxy router would probe
	// the panel THROUGH the dead proxy, report it blocked, and needlessly enter
	// recovery even though the (marked) check-in would have reached the panel.
	// Only the panel probe is marked; the RU/foreign probes stay unmarked so they
	// keep testing the real proxied client path.
	prober := rescue.NewHTTPProberWithFwmark(probeTimeout(cfg.RequestTimeout), cfg.ControlPlaneFwmark)
	results := make([]rescue.HTTPProbeResult, 0, len(urls))
	for _, url := range urls {
		if strings.TrimSpace(url) == "" {
			continue
		}
		results = append(results, prober.Probe(ctx, url))
	}

	return summarizeReachabilityProbe(
		"panel",
		[]probeTarget{{ID: "panel-api", Label: "control plane", URL: urls[0]}},
		results,
		recovery.StatusReachable,
		"",
		recovery.StatusBlocked,
		1,
		1,
	), nil
}

func collectRecoveryReachabilityGroups(
	ctx context.Context,
	cfg *config.Config,
) (reachabilityGroups, error) {
	panelURL := ""
	urls := serverHealthURLs(cfg.ControlURL)
	if len(urls) > 0 {
		panelURL = urls[0]
	}
	cacheKey := fmt.Sprintf("%s|%s", cfg.ControlURL, panelURL)
	now := time.Now().UTC()

	controlPlaneReachabilityCache.mu.Lock()
	if cached, ok := controlPlaneReachabilityCache.entries[cacheKey]; ok &&
		now.Before(cached.ExpiresAt) {
		controlPlaneReachabilityCache.mu.Unlock()
		panelProbe, err := collectPanelReachability(ctx, cfg)
		if err != nil {
			return reachabilityGroups{}, err
		}
		cached.Groups.Panel = panelProbe
		return cached.Groups, nil
	}
	controlPlaneReachabilityCache.mu.Unlock()

	prober := rescue.NewHTTPProber(probeTimeout(cfg.RequestTimeout))
	panelProbe, err := collectPanelReachability(ctx, cfg)
	if err != nil {
		return reachabilityGroups{}, err
	}
	ruProbe := probeTargetGroup(ctx, prober, ruProbeTargets, recovery.StatusReachable, "", recovery.StatusBlocked, 1, 1)
	foreignProbe := probeTargetGroup(ctx, prober, foreignProbeTargets, recovery.StatusHealthy, recovery.StatusPartial, recovery.StatusBlocked, 2, 1)

	groups := reachabilityGroups{
		Panel:   panelProbe,
		RU:      ruProbe,
		Foreign: foreignProbe,
	}

	controlPlaneReachabilityCache.mu.Lock()
	controlPlaneReachabilityCache.entries[cacheKey] = cachedReachabilityGroups{
		Groups:    groups,
		ExpiresAt: now.Add(cfg.Rescue.ProbeCacheTTL),
	}
	controlPlaneReachabilityCache.mu.Unlock()

	return groups, nil
}

func clearControlPlaneReachabilityCache() {
	controlPlaneReachabilityCache.mu.Lock()
	defer controlPlaneReachabilityCache.mu.Unlock()
	controlPlaneReachabilityCache.entries = map[string]cachedReachabilityGroups{}
}

func probeTargetGroup(
	ctx context.Context,
	prober rescue.HTTPProber,
	targets []probeTarget,
	fullStatus string,
	partialStatus string,
	blockedStatus string,
	fullThreshold int,
	partialThreshold int,
) *controlplane.RouterReachabilityProbe {
	results := make([]rescue.HTTPProbeResult, 0, len(targets))
	for _, target := range targets {
		ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
		result := prober.Probe(ctxWithTimeout, target.URL)
		cancel()
		results = append(results, result)
	}
	return summarizeReachabilityProbe(
		"",
		targets,
		results,
		fullStatus,
		partialStatus,
		blockedStatus,
		fullThreshold,
		partialThreshold,
	)
}

func summarizeReachabilityProbe(
	groupID string,
	targets []probeTarget,
	results []rescue.HTTPProbeResult,
	fullStatus string,
	partialStatus string,
	blockedStatus string,
	fullThreshold int,
	partialThreshold int,
) *controlplane.RouterReachabilityProbe {
	if len(results) == 0 {
		return nil
	}

	checks := make([]controlplane.RouterReachabilityProbe, 0, len(results))
	reachableCount := 0
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	for index, result := range results {
		target := probeTarget{}
		if index < len(targets) {
			target = targets[index]
		}
		if result.Reachable {
			reachableCount++
		}
		if !result.CheckedAt.IsZero() {
			checkedAt = result.CheckedAt.UTC().Format(time.RFC3339)
		}

		check := controlplane.RouterReachabilityProbe{
			ID:        target.ID,
			Label:     target.Label,
			Reachable: result.Reachable,
			CheckedAt: checkedAt,
			TargetURL: target.URL,
		}
		if result.StatusCode > 0 {
			check.StatusCode = result.StatusCode
		}
		if strings.TrimSpace(result.Error) != "" {
			check.Error = strings.Join(strings.Fields(result.Error), " ")
		}
		checks = append(checks, check)
	}

	status := blockedStatus
	reachable := false
	switch {
	case reachableCount >= fullThreshold:
		status = fullStatus
		reachable = true
	case partialStatus != "" && reachableCount >= partialThreshold:
		status = partialStatus
	}

	return &controlplane.RouterReachabilityProbe{
		ID:             groupID,
		Reachable:      reachable,
		CheckedAt:      checkedAt,
		Status:         status,
		ReachableCount: reachableCount,
		TotalCount:     len(checks),
		Checks:         checks,
	}
}

func restartedDuringCurrentOutage(recoveryState *recovery.State) bool {
	if recoveryState == nil {
		return false
	}
	lastRestart := recovery.ParseTime(recoveryState.LastControllerRestartAt)
	outageStarted := recovery.ParseTime(recoveryState.OutageStartedAt)
	return !lastRestart.IsZero() &&
		!outageStarted.IsZero() &&
		(lastRestart.Equal(outageStarted) || lastRestart.After(outageStarted))
}

func passwallRetriedDuringCurrentOutage(recoveryState *recovery.State) bool {
	if recoveryState == nil {
		return false
	}
	lastRetry := recovery.ParseTime(recoveryState.LastPasswallRetryAt)
	outageStarted := recovery.ParseTime(recoveryState.OutageStartedAt)
	return !lastRetry.IsZero() &&
		!outageStarted.IsZero() &&
		(lastRetry.Equal(outageStarted) || lastRetry.After(outageStarted))
}

func controlPlaneOutageReady(now time.Time, policy rescue.Policy, recoveryState *recovery.State) bool {
	outageStarted := recovery.ParseTime(recoveryState.OutageStartedAt)
	return !outageStarted.IsZero() && now.Sub(outageStarted) >= policy.PanelOutageThreshold
}

func shouldAutoRetryPasswallAfterDirectSettle(inventory *controlplane.RouterInventory, recoveryState *recovery.State) bool {
	if inventory == nil || recoveryState == nil || inventory.PasswallEnabled {
		return false
	}

	if inventory.RUReachability == nil || inventory.RUReachability.Status != recovery.StatusReachable {
		return false
	}

	return !passwallRetriedDuringCurrentOutage(recoveryState)
}

func shouldTriggerDirectFallback(inventory *controlplane.RouterInventory) bool {
	if inventory == nil {
		return false
	}

	// GAP-4: a controller restart never fixes a routing strand. Once the panel
	// has been blocked past the outage threshold, any non-healthy foreign result
	// (fully blocked OR only partial) means the proxy path cannot reliably carry
	// panel/foreign traffic, so fail safe to direct rather than looping restarts.
	return inventory.ForeignReachability != nil &&
		(inventory.ForeignReachability.Status == recovery.StatusBlocked ||
			inventory.ForeignReachability.Status == recovery.StatusPartial)
}

func shouldTriggerReboot(inventory *controlplane.RouterInventory, recoveryState *recovery.State) bool {
	if inventory == nil || recoveryState == nil {
		return false
	}

	panelBlocked := recoveryState.LastPanelStatus == recovery.StatusBlocked
	foreignBlocked := recoveryState.LastForeignStatus == recovery.StatusBlocked
	ruBlocked := recoveryState.LastRUStatus == recovery.StatusBlocked
	return panelBlocked && (foreignBlocked || ruBlocked)
}

func canScheduleAutoReboot(now time.Time, policy rescue.Policy, recoveryState *recovery.State) bool {
	lastReboot := recovery.ParseTime(recoveryState.LastAutoRebootAt)
	return lastReboot.IsZero() || now.Sub(lastReboot) >= policy.RebootCooldown
}

// operatorAttentionRetryReady gates how often the operator-attention loop
// re-attempts the proxy path. It reuses the RebootCooldown budget so the retry
// is slow and bounded per attempt (no PassWall thrashing) while remaining
// unbounded overall, guaranteeing recovery never permanently stalls on a human.
func operatorAttentionRetryReady(now time.Time, policy rescue.Policy, recoveryState *recovery.State) bool {
	if recoveryState == nil {
		return false
	}
	lastRetry := recovery.ParseTime(recoveryState.LastPasswallRetryAt)
	return lastRetry.IsZero() || now.Sub(lastRetry) >= policy.RebootCooldown
}

// shouldResumeProxyAfterExternalDirect detects a PassWall-disabled state that
// recovery did NOT cause — i.e. the cron watchdog's dead-man switch (or another
// external actor) flipped the router to direct so the controller could reach the
// panel. In that case check-in works again and recovery sits Idle/Monitoring,
// which means the recovery state-machine's own resume-to-proxy phases
// (PhaseDirectSettle etc.) are never entered and proxy would otherwise stay off
// until an operator intervened — contradicting the watchdog's promise that the
// agent's auto-resume re-enables proxy once contact is restored.
//
// It returns true only when the panel is healthy (this really is an
// external/watchdog-induced direct, not a panel outage), PassWall is currently
// disabled, and the bounded retry cooldown has elapsed. The actual re-enable is
// routed through the EXISTING probe-gated PhasePasswallRetryWait path, so it is
// flap-safe: a still-dead proxy is re-disabled after the warmup probe and the
// cooldown prevents thrashing the enable/disable with the watchdog.
func shouldResumeProxyAfterExternalDirect(
	now time.Time,
	policy rescue.Policy,
	recoveryState *recovery.State,
	inventory *controlplane.RouterInventory,
	panelProbe *controlplane.RouterReachabilityProbe,
) bool {
	if recoveryState == nil || inventory == nil {
		return false
	}
	// Only Idle/Monitoring reach here, but guard explicitly: never act while
	// recovery owns PassWall (a recovery-driven direct runs its own machinery).
	if recovery.PasswallOwnedByRecovery(recoveryState.Phase) {
		return false
	}
	if inventory.PasswallEnabled {
		return false
	}
	if panelProbe == nil || !panelProbe.Reachable {
		return false
	}
	return operatorAttentionRetryReady(now, policy, recoveryState)
}

func resumeProxyAfterExternalDirect(
	ctx context.Context,
	backend passwall.UCIBackend,
	recoveryState *recovery.State,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	runtimeStatus *state.RuntimeStatus,
	now time.Time,
) (controlPlaneRecoveryOutcome, error) {
	outcome := controlPlaneRecoveryOutcome{}
	if err := resumeProxyMode(ctx, backend, rescueState, persisted, runtimeStatus, now); err != nil {
		return outcome, err
	}
	recoveryState.LastPasswallRetryAt = recovery.FormatTime(now)
	setControlPlaneRecoveryPhase(
		recoveryState,
		runtimeStatus,
		recovery.PhasePasswallRetryWait,
		autoProxyRetryReason,
		false,
	)
	outcome.InventoryChanged = true
	// Pause control-plane work for this tick: PassWall just changed and the
	// inventory must be recollected before the next check-in, exactly like the
	// PhaseDirectSettle auto-retry.
	outcome.SkipControlPlane = true
	return outcome, nil
}

func effectiveControllerRestartSettle(cfg *config.Config) time.Duration {
	if cfg == nil {
		return 90 * time.Second
	}

	settle := cfg.Rescue.ControllerRestartSettle
	if settle <= 0 {
		settle = 90 * time.Second
	}

	if cfg.PollInterval > 0 {
		minimum := cfg.PollInterval * 2
		if minimum > settle {
			settle = minimum
		}
	}

	return settle
}

func setControlPlaneRecoveryPhase(
	recoveryState *recovery.State,
	runtimeStatus *state.RuntimeStatus,
	phase recovery.Phase,
	reason string,
	awaitingOperator bool,
) {
	if recoveryState == nil {
		return
	}

	recoveryState.Phase = phase
	recoveryState.AwaitingOperator = awaitingOperator
	recoveryState.LastActionReason = reason
	if runtimeStatus != nil {
		runtimeStatus.RecoveryPhase = string(phase)
		runtimeStatus.LastRecoveryAction = reason
		runtimeStatus.AwaitingOperator = awaitingOperator
	}
}

func clearControlPlaneRecoveryOwnership(
	persisted *state.PersistedState,
	runtimeStatus *state.RuntimeStatus,
) {
	if persisted == nil {
		return
	}

	recoveryState := &persisted.ControlPlaneRecovery
	recoveryState.Normalize()
	recoveryState.OutageStartedAt = ""
	recoveryState.Phase = recovery.PhaseIdle
	recoveryState.AwaitingOperator = false
	recoveryState.LastActionReason = ""
	recoveryState.LastPanelStatus = ""
	recoveryState.LastRUStatus = ""
	recoveryState.LastForeignStatus = ""
	recoveryState.LastControllerRestartAt = ""
	recoveryState.LastPasswallRetryAt = ""
	clearControlPlaneReachabilityCache()

	if runtimeStatus != nil {
		runtimeStatus.LastPanelStatus = ""
		runtimeStatus.LastRUStatus = ""
		runtimeStatus.LastForeignStatus = ""
		runtimeStatus.RecoveryPhase = string(recovery.PhaseIdle)
		runtimeStatus.LastRecoveryAction = ""
		runtimeStatus.AwaitingOperator = false
	}
}

func probeStatus(probe *controlplane.RouterReachabilityProbe) string {
	if probe == nil {
		return ""
	}
	return strings.TrimSpace(probe.Status)
}

func scheduleControllerServiceRestart(
	ctx context.Context,
	backend commandRunner,
) error {
	restartCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := backend.Run(
		restartCtx,
		"sh",
		"-c",
		"(sleep 2; /etc/init.d/vectra-controller restart >/tmp/vectra-controller-recovery.log 2>&1) &",
	)
	if err == nil {
		clearControlPlaneReachabilityCache()
	}
	return err
}

func scheduleRouterReboot(
	ctx context.Context,
	backend commandRunner,
) error {
	rebootCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	command := strings.Join([]string{
		"set -eu",
		`log_path="/tmp/vectra-router-reboot.log"`,
		`(sleep 5; /sbin/reboot) >"$log_path" 2>&1 &`,
		`printf 'router reboot scheduled\n'`,
	}, "\n")
	_, err := backend.Run(rebootCtx, "sh", "-c", command)
	if err == nil {
		clearControlPlaneReachabilityCache()
	}
	return err
}

func enterDirectModeForRecovery(
	ctx context.Context,
	backend passwall.UCIBackend,
	rescueState *rescue.State,
	persisted *state.PersistedState,
	runtimeStatus *state.RuntimeStatus,
	reason string,
	now time.Time,
) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}

	if err := setPasswallMainSwitch(ctx, backend, false, mainSwitchOptions{
		Reason: reason,
	}); err != nil {
		return err
	}

	if rescueState != nil {
		rescueState.Mode = rescue.ModeDirect
		rescueState.ProxyFailureCount = 0
		rescueState.DirectSuccessCount = 0
		rescueState.ProxySuccessCount = 0
		rescueState.LastTransitionAt = now
	}

	if persisted != nil && rescueState != nil {
		persisted.Rescue.State = *rescueState
		persisted.Rescue.LastMode = string(rescue.ModeDirect)
		persisted.Rescue.LastReason = reason
		persisted.Rescue.HappenedAt = recovery.FormatTime(now)
	}

	if runtimeStatus != nil {
		runtimeStatus.RescueMode = string(rescue.ModeDirect)
		runtimeStatus.LastRescueReason = reason
		runtimeStatus.LastRescueAt = recovery.FormatTime(now)
		runtimeStatus.PasswallEnabled = false
		runtimeStatus.ProxyFailureCount = 0
		runtimeStatus.ProxySuccessCount = 0
		runtimeStatus.DirectSuccessCount = 0
	}

	clearControlPlaneReachabilityCache()

	return nil
}

func noteDirectRecoveryWindow(
	rescueState *rescue.State,
	persisted *state.PersistedState,
	inventory *controlplane.RouterInventory,
	runtimeStatus *state.RuntimeStatus,
	now time.Time,
) {
	if rescueState == nil {
		return
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	rescueState.Mode = rescue.ModeDirect
	rescueState.ProxyFailureCount = 0
	rescueState.DirectSuccessCount = 0
	rescueState.ProxySuccessCount = 0
	rescueState.LastTransitionAt = now

	if persisted != nil && inventory != nil && runtimeStatus != nil {
		applyRescueMetadata(persisted, rescueState, inventory, runtimeStatus)
		return
	}

	if persisted != nil {
		persisted.Rescue.State = *rescueState
	}
	if runtimeStatus != nil {
		runtimeStatus.RescueMode = string(rescue.ModeDirect)
		runtimeStatus.ProxyFailureCount = 0
		runtimeStatus.DirectSuccessCount = 0
		runtimeStatus.ProxySuccessCount = 0
		if inventory != nil {
			runtimeStatus.PasswallEnabled = inventory.PasswallEnabled
		}
	}
}
