package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestCredentialsRedactedBeforePersistence(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		var out bytes.Buffer
		l := New("debug", &out, format).With("token", "short-token").WithGroup("request")
		l.Error("failed https://example.test/sub/opaque-token", "error", errors.New(`bad id 11111111-2222-3333-4444-555555555555`), "privateKey", "very-short", "nested", slog.GroupValue(slog.String("password", "short-pass")), "phase", "validate")
		for _, secret := range []string{"opaque-token", "short-token", "very-short", "short-pass", "11111111-2222-3333-4444-555555555555"} {
			if strings.Contains(out.String(), secret) {
				t.Fatalf("%s leaked %s: %s", format, secret, out.String())
			}
		}
		if !strings.Contains(out.String(), "validate") {
			t.Fatal("lost actionable phase")
		}
	}
}
