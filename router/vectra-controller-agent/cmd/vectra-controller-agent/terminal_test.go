package main

import "testing"

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
