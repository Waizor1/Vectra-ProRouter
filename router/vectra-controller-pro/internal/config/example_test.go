package config

import (
	"path/filepath"
	"testing"
)

// TestExamplesValid ensures every example config in examples/ loads + validates.
// This catches drift between docs and code at PR time.
func TestExamplesValid(t *testing.T) {
	matches, err := filepath.Glob("../../examples/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no examples found")
	}
	for _, p := range matches {
		p := p
		t.Run(filepath.Base(p), func(t *testing.T) {
			c, err := Load(p)
			if err != nil {
				t.Fatalf("load %s: %v", p, err)
			}
			if c.Schema != SchemaVersion {
				t.Errorf("schema = %d, want %d", c.Schema, SchemaVersion)
			}
		})
	}
}
