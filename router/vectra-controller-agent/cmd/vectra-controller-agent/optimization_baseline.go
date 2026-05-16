package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
)

const optimizationBaselineVersion = "2026-05-15-v1"

const (
	maxOptimizationBaselineStdoutChars = 14000
	maxOptimizationBaselineStderrChars = 4000
)

type optimizationBaselineRequest struct {
	LogSource     string
	LogLines      int
	IncludeLogs   bool
	IncludeRoutes bool
}

func parseCollectOptimizationBaselineJob(payload map[string]interface{}) optimizationBaselineRequest {
	return optimizationBaselineRequest{
		LogSource:     normalizeRouterLogSource(payloadString(payload, "logSource")),
		LogLines:      payloadInt(payload, "logLines", 160),
		IncludeLogs:   payloadBoolDefault(payload, "includeLogs", true),
		IncludeRoutes: payloadBoolDefault(payload, "includeRoutes", true),
	}
}

func runOptimizationBaselineJob(
	ctx context.Context,
	backend passwall.UCIBackend,
	request optimizationBaselineRequest,
	baseInventory controlplane.RouterInventory,
) (map[string]interface{}, controlplane.RouterInventory, string, string) {
	now := time.Now().UTC()
	freshInventory := collectInventoryWithRuntimeVersion(baseInventory)
	warnings := make([]string, 0)
	stdoutBlocks := make([]string, 0)
	stderrBlocks := make([]string, 0)

	processes, processStdout, processStderr, err := collectOptimizationProcesses(ctx, backend)
	if processStdout != "" {
		stdoutBlocks = append(stdoutBlocks, "[processes]\n"+processStdout)
	}
	if processStderr != "" {
		stderrBlocks = append(stderrBlocks, "[processes]\n"+processStderr)
	}
	if err != nil {
		warnings = append(warnings, "process scan: "+err.Error())
	}

	conntrack, conntrackStdout, conntrackStderr, err := collectOptimizationConntrack(ctx, backend)
	if conntrackStdout != "" {
		stdoutBlocks = append(stdoutBlocks, "[conntrack]\n"+conntrackStdout)
	}
	if conntrackStderr != "" {
		stderrBlocks = append(stderrBlocks, "[conntrack]\n"+conntrackStderr)
	}
	if err != nil {
		warnings = append(warnings, "conntrack scan: "+err.Error())
	}

	configSurface, configStdout, configStderr, err := collectOptimizationPasswallConfigSurface(ctx, backend)
	if configStdout != "" {
		stdoutBlocks = append(stdoutBlocks, "[passwall-config]\n"+configStdout)
	}
	if configStderr != "" {
		stderrBlocks = append(stderrBlocks, "[passwall-config]\n"+configStderr)
	}
	if err != nil {
		warnings = append(warnings, "passwall config scan: "+err.Error())
	}

	payload := map[string]interface{}{
		"baselineVersion":     optimizationBaselineVersion,
		"collectedAt":         now.Format(time.RFC3339),
		"ok":                  len(warnings) == 0,
		"hostname":            freshInventory.Hostname,
		"model":               freshInventory.Model,
		"boardName":           freshInventory.BoardName,
		"target":              freshInventory.Target,
		"architecture":        freshInventory.Architecture,
		"openwrtRelease":      freshInventory.OpenWrtRelease,
		"passwallEnabled":     freshInventory.PasswallEnabled,
		"selectedNodeId":      freshInventory.SelectedNodeID,
		"selectedNodeLabel":   freshInventory.SelectedNodeLabel,
		"nodeCount":           freshInventory.NodeCount,
		"subscriptionCount":   freshInventory.SubscriptionCount,
		"resources":           freshInventory.Resources,
		"serviceHealth":       freshInventory.ServiceHealth,
		"safetyEvents":        freshInventory.SafetyEvents,
		"packageVersions":     freshInventory.PackageVersions,
		"binaryVersions":      freshInventory.BinaryVersions,
		"rulesAssets":         freshInventory.RulesAssets,
		"processes":           processes,
		"conntrack":           conntrack,
		"passwallConfig":      configSurface,
		"warnings":            warnings,
		"errors":              []string{},
		"diagnosticReadOnly":  true,
		"diagnosticWorkload":  "optimization-baseline",
		"diagnosticScope":     "resources,proxy-runtime,conntrack,dns,logs,routes",
		"diagnosticLogSource": normalizeRouterLogSource(request.LogSource),
		"diagnosticLogLines":  clampRouterLogLines(request.LogLines),
	}

	if request.IncludeLogs {
		logRequest := routerLogCollectionRequest{
			Source: normalizeRouterLogSource(request.LogSource),
			Lines:  clampRouterLogLines(request.LogLines),
		}
		snapshots, logsStdout, logsStderr, logsErr := collectRouterLogs(ctx, backend, logRequest)
		logPayload := buildRouterLogResultPayload(logRequest, snapshots, logsStdout, logsStderr)
		if logsErr != nil {
			logPayload["error"] = logsErr.Error()
			warnings = append(warnings, "log snapshot: "+logsErr.Error())
		}
		payload["logs"] = logPayload
		if logsStdout != "" {
			stdoutBlocks = append(stdoutBlocks, "[logs]\n"+logsStdout)
		}
		if logsStderr != "" {
			stderrBlocks = append(stderrBlocks, "[logs]\n"+logsStderr)
		}
	}

	if request.IncludeRoutes {
		verification, routeErr := passwall.VerifyFleetRoutes(
			ctx,
			backend,
			fleetRoutePolicyIdentity(freshInventory),
		)
		routePayload, marshalErr := resultToMap(verification)
		if marshalErr != nil {
			warnings = append(warnings, "route verification marshal: "+marshalErr.Error())
		} else {
			routePayload["services"] = freshInventory.ServiceHealth
			routePayload["resources"] = freshInventory.Resources
			routePayload["passwallEnabled"] = freshInventory.PasswallEnabled
			routePayload["selectedNodeId"] = freshInventory.SelectedNodeID
			routePayload["selectedNodeLabel"] = freshInventory.SelectedNodeLabel
			routePayload["packageVersions"] = freshInventory.PackageVersions
			routePayload["binaryVersions"] = freshInventory.BinaryVersions
			if routeErr != nil {
				routePayload["error"] = routeErr.Error()
				warnings = append(warnings, "route verification: "+routeErr.Error())
			}
			warnings = appendOptimizationRouteVerificationWarnings(warnings, verification)
			payload["routeVerification"] = routePayload
		}
	}

	payload["warnings"] = warnings
	payload["ok"] = len(warnings) == 0
	stdout, _ := truncateRouterLogText(
		strings.Join(stdoutBlocks, "\n\n"),
		maxOptimizationBaselineStdoutChars,
	)
	stderr, _ := truncateRouterLogText(
		strings.Join(stderrBlocks, "\n\n"),
		maxOptimizationBaselineStderrChars,
	)
	return payload, freshInventory, stdout, stderr
}

func appendOptimizationRouteVerificationWarnings(
	warnings []string,
	verification passwall.RouteVerificationResult,
) []string {
	if verification.OK {
		return warnings
	}
	for _, routeError := range verification.Errors {
		trimmed := strings.TrimSpace(routeError)
		if trimmed != "" {
			warnings = append(warnings, "route verification: "+trimmed)
		}
	}
	return warnings
}

func collectOptimizationProcesses(
	ctx context.Context,
	backend commandRunner,
) ([]map[string]interface{}, string, string, error) {
	result, err := backend.Run(ctx, "sh", "-c", optimizationProcessScanCommand)
	processes := parseOptimizationProcessLines(result.Stdout)
	return processes, result.Stdout, result.Stderr, err
}

func collectOptimizationConntrack(
	ctx context.Context,
	backend commandRunner,
) (map[string]interface{}, string, string, error) {
	result, err := backend.Run(ctx, "sh", "-c", optimizationConntrackCommand)
	conntrack := map[string]interface{}{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(value)); parseErr == nil {
			conntrack[key] = parsed
		}
	}
	return conntrack, result.Stdout, result.Stderr, err
}

func collectOptimizationPasswallConfigSurface(
	ctx context.Context,
	backend commandRunner,
) (map[string]interface{}, string, string, error) {
	result, err := backend.Run(ctx, "sh", "-c", optimizationPasswallConfigCommand)
	return map[string]interface{}{
		"command": result.Command,
		"stdout":  result.Stdout,
	}, result.Stdout, result.Stderr, err
}

func parseOptimizationProcessLines(stdout string) []map[string]interface{} {
	processes := make([]map[string]interface{}, 0)
	for _, line := range strings.Split(stdout, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 5)
		if len(parts) < 5 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil || pid <= 0 {
			continue
		}
		command := strings.TrimSpace(parts[4])
		process := map[string]interface{}{
			"pid":     pid,
			"role":    classifyOptimizationProcess(command),
			"command": truncateOptimizationText(command, 512),
		}
		if value := parseOptionalNonNegativeInt(parts[1]); value != nil {
			process["vmRssKb"] = *value
		}
		if value := parseOptionalNonNegativeInt(parts[2]); value != nil {
			process["vmSizeKb"] = *value
		}
		if value := parseOptionalNonNegativeInt(parts[3]); value != nil {
			process["threads"] = *value
		}
		processes = append(processes, process)
	}
	return processes
}

func classifyOptimizationProcess(command string) string {
	normalized := strings.ToLower(command)
	switch {
	case strings.Contains(normalized, "xray"):
		return "xray"
	case strings.Contains(normalized, "sing-box"):
		return "sing-box"
	case strings.Contains(normalized, "dnsmasq_default"):
		return "passwall-dnsmasq"
	case strings.Contains(normalized, "dnsmasq"):
		return "dnsmasq"
	case strings.Contains(normalized, "chinadns"):
		return "chinadns-ng"
	case strings.Contains(normalized, "geoview"):
		return "geoview"
	default:
		return "other"
	}
}

func parseOptionalNonNegativeInt(value string) *int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 0 {
		return nil
	}
	return &parsed
}

func truncateOptimizationText(value string, maxChars int) string {
	trimmed := strings.TrimSpace(value)
	if maxChars <= 0 || len(trimmed) <= maxChars {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:maxChars]) + "…"
}

func payloadBoolDefault(payload map[string]interface{}, key string, fallback bool) bool {
	if payload == nil {
		return fallback
	}
	value, ok := payload[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case bool:
		return typed
	default:
		return fallback
	}
}

const optimizationProcessScanCommand = `
for p in /proc/[0-9]*; do
  pid="${p##*/}"
  [ "$pid" = "$$" ] && continue
  cmd="$(tr '\000' ' ' < "$p/cmdline" 2>/dev/null)"
  case "$cmd" in
    *xray*|*sing-box*|*dnsmasq*|*chinadns*|*geoview*)
      rss="$(awk '/^VmRSS:/ {print $2}' "$p/status" 2>/dev/null)"
      vmsize="$(awk '/^VmSize:/ {print $2}' "$p/status" 2>/dev/null)"
      threads="$(awk '/^Threads:/ {print $2}' "$p/status" 2>/dev/null)"
      printf '%s\t%s\t%s\t%s\t%s\n' "$pid" "${rss:-0}" "${vmsize:-0}" "${threads:-0}" "$cmd"
      ;;
  esac
done
exit 0
`

const optimizationConntrackCommand = `
printf 'count=%s\n' "$(cat /proc/sys/net/netfilter/nf_conntrack_count 2>/dev/null)"
printf 'max=%s\n' "$(cat /proc/sys/net/netfilter/nf_conntrack_max 2>/dev/null)"
exit 0
`

const optimizationPasswallConfigCommand = `
{
  uci -q show passwall2.@global[0]
  uci -q show passwall2.@global_forwarding[0]
  uci -q show passwall2.@global_xray[0]
  uci -q show passwall2.@global_rules[0]
  printf 'generated_global_json_bytes='
  wc -c < /tmp/etc/passwall2/acl/default/global.json 2>/dev/null || printf '0\n'
  printf 'dns_listeners=\n'
  netstat -lnup 2>/dev/null | grep -E '(:53|:15353)' || true
} 2>/dev/null || true
exit 0
`
