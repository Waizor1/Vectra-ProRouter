package apply_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/coreengine/xray"
)

func loadExample(t *testing.T) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "p0-vless-reality.json"))
	if err != nil {
		t.Fatalf("read example config: %v", err)
	}
	return raw
}

func TestApplyRendersWritesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "desired.json")

	var written []byte
	writeXray := func(data []byte) error { written = append([]byte(nil), data...); return nil }

	a := apply.New(xray.New(), configPath, writeXray)
	raw := loadExample(t)

	res, err := a.Apply(context.Background(), raw, "", false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Changed || res.Noop {
		t.Fatalf("expected change on first apply: %+v", res)
	}
	if res.DesiredDigest == "" || res.AppliedDigest != res.DesiredDigest {
		t.Errorf("digests: %+v", res)
	}
	if len(written) == 0 || !json.Valid(written) {
		t.Errorf("rendered xray.json not written/invalid (%d bytes)", len(written))
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("desired config not persisted: %v", err)
	}

	// Second apply with the same digest + render present -> noop.
	res2, err := a.Apply(context.Background(), raw, res.DesiredDigest, true)
	if err != nil {
		t.Fatalf("Apply (noop): %v", err)
	}
	if !res2.Noop || res2.Changed {
		t.Errorf("expected noop on identical apply: %+v", res2)
	}
}

func TestApplyRejectsInvalidConfig(t *testing.T) {
	a := apply.New(xray.New(), "", func([]byte) error { return nil })
	_, err := a.Apply(context.Background(), json.RawMessage(`{"schema":999}`), "", false)
	if err == nil {
		t.Fatal("expected error on unsupported schema")
	}
}
