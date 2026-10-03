// Package logging is a thin slog-based wrapper used across the controller.
// It centralizes level/format choice so the rest of the code stays plain slog.
package logging

import (
	"context"
	"fmt"
	"io"
	"regexp"

	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"vectra-controller-pro/internal/redact"
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
	opts := &slog.HandlerOptions{Level: lvl, ReplaceAttr: sanitizeAttr}
	var h slog.Handler
	if strings.ToLower(format) == "json" {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	return slog.New(safeHandler{h})
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

// Redact before persistence, including records that never reach a UI/export.
var secretField = regexp.MustCompile(`(?i)(password|passwd|token|secret|private.?key|authorization|subscription.?url|uuid|short.?id|^id$|^pbk$|^psk$)`)

func sanitizeAttr(_ []string, a slog.Attr) slog.Attr {
	if secretField.MatchString(a.Key) {
		a.Value = slog.StringValue("<redacted>")
		return a
	}
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(safeText(a.Value.String()))
	case slog.KindAny:
		a.Value = slog.StringValue(safeText(fmt.Sprint(a.Value.Any())))
	}
	return a
}

type safeHandler struct{ slog.Handler }

func (h safeHandler) Handle(ctx context.Context, r slog.Record) error {
	r.Message = safeText(r.Message)
	return h.Handler.Handle(ctx, r)
}
func (h safeHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return safeHandler{h.Handler.WithAttrs(a)}
}
func (h safeHandler) WithGroup(n string) slog.Handler { return safeHandler{h.Handler.WithGroup(n)} }

func safeText(s string) string {
	if strings.Contains(s, "-----BEGIN ") && strings.Contains(s, "PRIVATE KEY-----") {
		return "<private key material omitted>"
	}
	return redact.Text(s)
}
