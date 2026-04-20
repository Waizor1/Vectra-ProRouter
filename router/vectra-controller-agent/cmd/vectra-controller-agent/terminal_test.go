package main

import (
	"testing"

	"vectra-controller-agent/internal/rescue"
)

func TestClampTerminalCommandTimeout(t *testing.T) {
	if got := clampTerminalCommandTimeout(0); got != defaultTerminalCommandTimeoutSeconds {
		t.Fatalf("default timeout mismatch: got %d", got)
	}
	if got := clampTerminalCommandTimeout(2); got != minTerminalCommandTimeoutSeconds {
		t.Fatalf("min timeout mismatch: got %d", got)
	}
	if got := clampTerminalCommandTimeout(999); got != maxTerminalCommandTimeoutSeconds {
		t.Fatalf("max timeout mismatch: got %d", got)
	}
}

func TestTruncateTerminalText(t *testing.T) {
	text, truncated := truncateTerminalText("abcdefghij", 8)
	if !truncated {
		t.Fatalf("expected output to be truncated")
	}
	if text == "" {
		t.Fatalf("expected truncated text to stay non-empty")
	}
}

func TestShouldResumeProxyAfterTerminalSuccess(t *testing.T) {
	t.Run("returns true only for controller self-update in direct mode", func(t *testing.T) {
		rescueState := &rescue.State{Mode: rescue.ModeDirect}
		if !shouldResumeProxyAfterTerminalSuccess(map[string]interface{}{
			"purpose": controllerSelfUpdateTerminalPurpose,
		}, rescueState) {
			t.Fatalf("expected controller self-update in direct mode to resume proxy")
		}
	})

	t.Run("returns false when not in direct mode", func(t *testing.T) {
		rescueState := &rescue.State{Mode: rescue.ModeProxy}
		if shouldResumeProxyAfterTerminalSuccess(map[string]interface{}{
			"purpose": controllerSelfUpdateTerminalPurpose,
		}, rescueState) {
			t.Fatalf("did not expect proxy mode to auto-resume")
		}
	})

	t.Run("returns false for other terminal commands", func(t *testing.T) {
		rescueState := &rescue.State{Mode: rescue.ModeDirect}
		if shouldResumeProxyAfterTerminalSuccess(map[string]interface{}{
			"purpose": "collect-router-logs",
		}, rescueState) {
			t.Fatalf("did not expect unrelated terminal command to auto-resume")
		}
	})
}
