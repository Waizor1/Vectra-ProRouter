package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	defaultRouterLogLines      = 200
	minRouterLogLines          = 50
	maxRouterLogLines          = 400
	maxRouterLogSnapshotChars  = 5000
	maxRouterLogStdoutChars    = 14000
	maxRouterLogStderrChars    = 4000
	routerLogTruncationMessage = "\n\n[output truncated]"
)

type routerLogCollectionRequest struct {
	Source string
	Lines  int
}

type routerLogCommand struct {
	ID      string
	Label   string
	Command string
}

func parseCollectRouterLogsJob(payload map[string]interface{}) routerLogCollectionRequest {
	return routerLogCollectionRequest{
		Source: normalizeRouterLogSource(payloadString(payload, "source")),
		Lines:  payloadInt(payload, "lines", defaultRouterLogLines),
	}
}

func normalizeRouterLogSource(value string) string {
	switch strings.TrimSpace(value) {
	case "controller", "passwall", "dnsmasq", "system":
		return value
	default:
		return "all"
	}
}

func payloadInt(payload map[string]interface{}, key string, fallback int) int {
	if payload == nil {
		return clampRouterLogLines(fallback)
	}

	switch value := payload[key].(type) {
	case int:
		return clampRouterLogLines(value)
	case int32:
		return clampRouterLogLines(int(value))
	case int64:
		return clampRouterLogLines(int(value))
	case float64:
		return clampRouterLogLines(int(value))
	case float32:
		return clampRouterLogLines(int(value))
	default:
		return clampRouterLogLines(fallback)
	}
}

func clampRouterLogLines(value int) int {
	if value <= 0 {
		return defaultRouterLogLines
	}
	if value < minRouterLogLines {
		return minRouterLogLines
	}
	if value > maxRouterLogLines {
		return maxRouterLogLines
	}
	return value
}

func buildRouterLogCommands(request routerLogCollectionRequest) []routerLogCommand {
	lines := clampRouterLogLines(request.Lines)

	allSources := []routerLogCommand{
		{
			ID:      "controller",
			Label:   "Vectra Controller",
			Command: fmt.Sprintf("logread | grep -E 'vectra-controller|vectra' | tail -n %d", lines),
		},
		{
			ID:      "passwall",
			Label:   "PassWall / Proxy",
			Command: fmt.Sprintf("logread -e 'passwall|xray|sing-box|hysteria|geoview|chinadns' | tail -n %d", lines),
		},
		{
			ID:      "dnsmasq",
			Label:   "dnsmasq",
			Command: fmt.Sprintf("logread -e 'dnsmasq' | tail -n %d", lines),
		},
		{
			ID:      "system",
			Label:   "System Log",
			Command: fmt.Sprintf("logread | tail -n %d", lines),
		},
	}

	if request.Source == "all" {
		return allSources
	}

	for _, command := range allSources {
		if command.ID == request.Source {
			return []routerLogCommand{command}
		}
	}

	return allSources
}

func collectRouterLogs(
	ctx context.Context,
	backend commandRunner,
	request routerLogCollectionRequest,
) ([]map[string]interface{}, string, string, error) {
	commands := buildRouterLogCommands(request)
	snapshots := make([]map[string]interface{}, 0, len(commands))
	stdoutBlocks := make([]string, 0, len(commands))
	stderrBlocks := make([]string, 0, len(commands))
	failures := make([]string, 0)

	for _, command := range commands {
		result, err := backend.Run(ctx, "sh", "-c", command.Command)
		content, truncated := truncateRouterLogText(result.Stdout, maxRouterLogSnapshotChars)
		if content != "" {
			stdoutBlocks = append(stdoutBlocks, fmt.Sprintf("[%s]\n%s", command.Label, content))
		}
		if trimmed := strings.TrimSpace(result.Stderr); trimmed != "" {
			stderrBlocks = append(stderrBlocks, fmt.Sprintf("[%s] %s", command.Label, trimmed))
		}

		snapshots = append(snapshots, map[string]interface{}{
			"id":        command.ID,
			"label":     command.Label,
			"command":   command.Command,
			"content":   content,
			"truncated": truncated,
		})

		if err != nil {
			failures = append(failures, err.Error())
		}
	}

	stdout, _ := truncateRouterLogText(strings.Join(stdoutBlocks, "\n\n"), maxRouterLogStdoutChars)
	stderr, _ := truncateRouterLogText(strings.Join(stderrBlocks, "\n"), maxRouterLogStderrChars)

	if len(failures) > 0 {
		return snapshots, stdout, stderr, fmt.Errorf(strings.Join(failures, "; "))
	}

	return snapshots, stdout, stderr, nil
}

func truncateRouterLogText(input string, maxChars int) (string, bool) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(input, "\r\n", "\n"))
	if trimmed == "" || maxChars <= 0 {
		return trimmed, false
	}

	if len(trimmed) <= maxChars {
		return trimmed, false
	}

	limit := maxChars - len(routerLogTruncationMessage)
	if limit <= 0 {
		return routerLogTruncationMessage, true
	}

	return strings.TrimSpace(trimmed[:limit]) + routerLogTruncationMessage, true
}

func buildRouterLogResultPayload(
	request routerLogCollectionRequest,
	snapshots []map[string]interface{},
	stdout string,
	stderr string,
) map[string]interface{} {
	payload := map[string]interface{}{
		"source":         normalizeRouterLogSource(request.Source),
		"requestedLines": clampRouterLogLines(request.Lines),
		"collectedAt":    time.Now().UTC().Format(time.RFC3339),
		"snapshots":      snapshots,
	}

	if stdout != "" {
		payload["stdout"] = stdout
	}
	if stderr != "" {
		payload["stderr"] = stderr
	}

	return payload
}
