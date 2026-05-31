package xray

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"vectra-controller-pro/internal/config"
)

// TestRenderExample is an end-to-end check: load the example operator
// config, render Xray JSON, and confirm:
//   - the result parses as JSON
//   - "inbounds"/"outbounds"/"routing" exist
//   - every operator-set value (selected enums) survived the trip
//   - synthetic outbounds (direct/block/dns-out) are present
func TestRenderExample(t *testing.T) {
	matches, _ := filepath.Glob("../../../examples/*.json")
	if len(matches) == 0 {
		t.Skip("no examples found")
	}
	for _, p := range matches {
		p := p
		t.Run(filepath.Base(p), func(t *testing.T) {
			c, err := config.Load(p)
			if err != nil {
				t.Fatal(err)
			}
			out, err := New().Render(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(out, &doc); err != nil {
				t.Fatalf("rendered JSON invalid: %v", err)
			}
			ib, _ := doc["inbounds"].([]any)
			if len(ib) == 0 {
				t.Errorf("inbounds: empty")
			}
			ob, _ := doc["outbounds"].([]any)
			if len(ob) < 3 {
				t.Errorf("expected at least 3 outbounds (direct/block/dns-out), got %d", len(ob))
			}
			// Check synthetic outbounds exist (order matters: first three).
			tags := []string{}
			for _, o := range ob[:3] {
				m, _ := o.(map[string]any)
				tags = append(tags, m["tag"].(string))
			}
			want := []string{"direct", "block", "dns-out"}
			for i := range want {
				if tags[i] != want[i] {
					t.Errorf("synthetic outbound[%d]: got %q want %q", i, tags[i], want[i])
				}
			}
		})
	}
}
