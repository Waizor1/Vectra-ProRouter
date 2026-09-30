package sites

import (
	"encoding/json"
	"os"
	"testing"
)

// TestSharedVector holds the router to ui/contract/sites-vector.json, the
// cases the router UI's own normalizer is held to as well: what a person sees
// in the list is what the router keeps.
func TestSharedVector(t *testing.T) {
	raw, err := os.ReadFile("../../ui/contract/sites-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		OK          [][2]string `json:"ok"`
		SingleLabel [][2]string `json:"singleLabel"`
		Refused     []string    `json:"refused"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.OK) < 40 || len(v.Refused) < 40 || len(v.SingleLabel) == 0 {
		t.Fatalf("the vector looks truncated: %d ok, %d refused, %d single-label", len(v.OK), len(v.Refused), len(v.SingleLabel))
	}
	for _, c := range append(v.OK, v.SingleLabel...) {
		s, err := Parse(c[0])
		if err != nil {
			t.Errorf("Parse(%q): %v; want %q", c[0], err, c[1])
		} else if s.Display != c[1] {
			t.Errorf("Parse(%q) = %q; want %q", c[0], s.Display, c[1])
		}
	}
	for _, in := range v.Refused {
		if s, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) accepted %q; the vector says refused", in, s.Display)
		}
	}
}
