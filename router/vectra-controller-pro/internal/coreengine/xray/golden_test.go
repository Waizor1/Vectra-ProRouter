package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/config"
)

// TestGoldenCorpus renders every operator config in testdata/corpus and
// compares the rendered Xray JSON against a frozen golden file. This locks the
// renderer's P0 output so any unintended change to generated Xray config is
// caught in review. Regenerate goldens with:
//
//	UPDATE_GOLDEN=1 go test ./internal/coreengine/xray/ -run TestGoldenCorpus
//
// This is the deterministic-snapshot half of the parity strategy; the live
// parity oracle (vctl render vs PassWall2 gen_config) lives in parity_test.go
// and runs against a captured corpus.
func TestGoldenCorpus(t *testing.T) {
	corpus, err := filepath.Glob(filepath.Join("testdata", "corpus", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus) == 0 {
		t.Fatal("no corpus configs found under testdata/corpus")
	}
	eng := New()
	update := os.Getenv("UPDATE_GOLDEN") == "1"

	for _, src := range corpus {
		name := strings.TrimSuffix(filepath.Base(src), ".json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(src)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Read(bytes.NewReader(raw), src)
			if err != nil {
				t.Fatalf("read/validate corpus config: %v", err)
			}
			got, err := eng.Render(context.Background(), cfg)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !json.Valid(got) {
				t.Fatalf("rendered output is not valid JSON")
			}

			// Determinism: a second render must be byte-identical.
			got2, err := eng.Render(context.Background(), cfg)
			if err != nil {
				t.Fatalf("render(2): %v", err)
			}
			if !bytes.Equal(got, got2) {
				t.Fatal("renderer is non-deterministic for the same input")
			}

			goldenPath := filepath.Join("testdata", "golden", name+".xray.json")
			if update {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("updated golden %s (%d bytes)", goldenPath, len(got))
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("missing golden (run UPDATE_GOLDEN=1): %v", err)
			}
			if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
				t.Errorf("rendered Xray JSON drifted from golden %s.\n--- got ---\n%s\n--- want ---\n%s",
					goldenPath, firstLines(got, 40), firstLines(want, 40))
			}
		})
	}
}

func firstLines(b []byte, n int) string {
	lines := strings.SplitN(string(b), "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
		lines = append(lines, "...(truncated)")
	}
	return strings.Join(lines, "\n")
}
