package main

import (
	"testing"
	"time"
)

// A name the render no longer has leaves the watchdog's memory of when it
// last resolved: it grew with every server the provider ever named.
func TestTheWatchdogForgetsNamesTheRenderNoLongerHas(t *testing.T) {
	var w failoverWatch
	t0 := time.Unix(1790000000, 0)
	w.namesResolved([]string{"pl5", "de5", "ru9"}, nil, t0, true)
	if len(w.seenResolved) != 3 {
		t.Fatalf("seen = %v", w.seenResolved)
	}
	// A partial look (a batch) forgets nothing.
	w.namesResolved([]string{"pl5"}, nil, t0.Add(time.Minute), false)
	if len(w.seenResolved) != 3 {
		t.Fatalf("a batch forgot names: %v", w.seenResolved)
	}
	w.namesResolved([]string{"pl5", "de5"}, nil, t0.Add(2*time.Minute), true)
	if _, kept := w.seenResolved["ru9"]; kept || len(w.seenResolved) != 2 {
		t.Fatalf("seen = %v", w.seenResolved)
	}
}

// «Нейросети» refused on one document is not remembered once another is
// applied: the refusal holds only on the document it joined.
func TestARefusedAIDefaultIsForgottenWithItsDocument(t *testing.T) {
	d := &daemon{aiRefused: map[string]bool{}}
	old, cur := []byte(`{"remarks":"old"}`), []byte(`{"remarks":"new"}`)
	d.aiRefused[aiRefusedKey([]byte(`{"a":1}`), old)] = true
	d.aiRefused[aiRefusedKey([]byte(`{"a":2}`), old)] = true
	keep := aiRefusedKey([]byte(`{"a":1}`), cur)
	d.aiRefused[keep] = true
	d.keepAIRefusedFor(cur)
	if len(d.aiRefused) != 1 || !d.aiRefused[keep] {
		t.Fatalf("aiRefused = %v", d.aiRefused)
	}
	(&daemon{}).keepAIRefusedFor(cur) // nil map: nothing to forget
}
