package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultTerminalCommandTimeoutSeconds = 30
	minTerminalCommandTimeoutSeconds     = 5
	maxTerminalCommandTimeoutSeconds     = 120
	maxTerminalStdoutChars               = 12000
	maxTerminalStderrChars               = 8000
	terminalTruncationMessage            = "\n\n[output truncated]"
)

type terminalCommandRequest struct {
	Command        string
	TimeoutSeconds int
}

type terminalCommandResult struct {
	Command          string
	TimeoutSeconds   int
	StartedAt        time.Time
	CompletedAt      time.Time
	DurationMs       int
	ExitCode         *int
	TimedOut         bool
	Stdout           string
	Stderr           string
	StdoutTruncated  bool
	StderrTruncated  bool
	ExecutionFailure error
}

func parseRunTerminalCommandJob(
	payload map[string]interface{},
) terminalCommandRequest {
	return terminalCommandRequest{
		Command:        strings.TrimSpace(payloadString(payload, "command")),
		TimeoutSeconds: clampTerminalCommandTimeout(payloadInt(payload, "timeoutSeconds", defaultTerminalCommandTimeoutSeconds)),
	}
}

func clampTerminalCommandTimeout(value int) int {
	if value <= 0 {
		return defaultTerminalCommandTimeoutSeconds
	}
	if value < minTerminalCommandTimeoutSeconds {
		return minTerminalCommandTimeoutSeconds
	}
	if value > maxTerminalCommandTimeoutSeconds {
		return maxTerminalCommandTimeoutSeconds
	}
	return value
}

func executeTerminalCommand(
	ctx context.Context,
	request terminalCommandRequest,
) terminalCommandResult {
	startedAt := time.Now().UTC()
	timeoutSeconds := clampTerminalCommandTimeout(request.TimeoutSeconds)
	commandText := strings.TrimSpace(request.Command)
	if commandText == "" {
		completedAt := time.Now().UTC()
		return terminalCommandResult{
			Command:          "",
			TimeoutSeconds:   timeoutSeconds,
			StartedAt:        startedAt,
			CompletedAt:      completedAt,
			DurationMs:       int(completedAt.Sub(startedAt).Milliseconds()),
			ExecutionFailure: fmt.Errorf("terminal command payload missing command"),
		}
	}

	commandCtx, cancel := context.WithTimeout(
		ctx,
		time.Duration(timeoutSeconds)*time.Second,
	)
	defer cancel()

	cmd := exec.CommandContext(commandCtx, "sh", "-c", commandText)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	completedAt := time.Now().UTC()

	stdoutText, stdoutTruncated := truncateTerminalText(
		stdout.String(),
		maxTerminalStdoutChars,
	)
	stderrText, stderrTruncated := truncateTerminalText(
		stderr.String(),
		maxTerminalStderrChars,
	)

	result := terminalCommandResult{
		Command:         commandText,
		TimeoutSeconds:  timeoutSeconds,
		StartedAt:       startedAt,
		CompletedAt:     completedAt,
		DurationMs:      int(completedAt.Sub(startedAt).Milliseconds()),
		TimedOut:        errors.Is(commandCtx.Err(), context.DeadlineExceeded),
		Stdout:          stdoutText,
		Stderr:          stderrText,
		StdoutTruncated: stdoutTruncated,
		StderrTruncated: stderrTruncated,
	}

	if runErr == nil {
		exitCode := 0
		result.ExitCode = &exitCode
		return result
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		exitCode := exitErr.ExitCode()
		result.ExitCode = &exitCode
	}

	if result.TimedOut {
		result.ExecutionFailure = fmt.Errorf(
			"terminal command timed out after %ds",
			timeoutSeconds,
		)
		return result
	}

	if result.ExitCode != nil {
		result.ExecutionFailure = fmt.Errorf(
			"terminal command failed with exit code %d",
			*result.ExitCode,
		)
		return result
	}

	result.ExecutionFailure = fmt.Errorf("terminal command failed: %w", runErr)
	return result
}

func truncateTerminalText(input string, maxChars int) (string, bool) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(input, "\r\n", "\n"))
	if trimmed == "" || maxChars <= 0 {
		return trimmed, false
	}

	if len(trimmed) <= maxChars {
		return trimmed, false
	}

	limit := maxChars - len(terminalTruncationMessage)
	if limit <= 0 {
		return terminalTruncationMessage, true
	}

	return strings.TrimSpace(trimmed[:limit]) + terminalTruncationMessage, true
}

func buildTerminalCommandResultPayload(
	result terminalCommandResult,
) map[string]interface{} {
	payload := map[string]interface{}{
		"command":         result.Command,
		"timeoutSeconds":  result.TimeoutSeconds,
		"startedAt":       result.StartedAt.Format(time.RFC3339),
		"completedAt":     result.CompletedAt.Format(time.RFC3339),
		"durationMs":      result.DurationMs,
		"timedOut":        result.TimedOut,
		"stdoutTruncated": result.StdoutTruncated,
		"stderrTruncated": result.StderrTruncated,
	}

	if result.ExitCode != nil {
		payload["exitCode"] = *result.ExitCode
	}
	if result.Stdout != "" {
		payload["stdout"] = result.Stdout
	}
	if result.Stderr != "" {
		payload["stderr"] = result.Stderr
	}

	return payload
}
