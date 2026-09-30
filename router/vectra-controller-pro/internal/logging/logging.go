// Package logging is a thin slog-based wrapper used across the controller.
// It centralizes level/format choice so the rest of the code stays plain slog.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

var current atomic.Pointer[slog.Logger]

func init() {
	current.Store(New("info", os.Stderr, "text"))
}

// New builds a slog.Logger with the requested level and format.
// format: "text" (human-friendly) or "json" (machine-friendly).
func New(level string, w io.Writer, format string) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warning", "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if strings.ToLower(format) == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(h)
}

// SetDefault replaces the package-level logger.
func SetDefault(l *slog.Logger) {
	if l == nil {
		return
	}
	current.Store(l)
}

// L returns the active logger.
func L() *slog.Logger {
	return current.Load()
}
