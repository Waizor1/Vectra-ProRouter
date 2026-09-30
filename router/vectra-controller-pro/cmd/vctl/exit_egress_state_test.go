package main

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/state"
)

// Where each exit leaves goes through the state file: saved with the rest,
// taken back at a start (the nightly reboot keeps the card's «(выход: …)»
// and asks nothing again before its day).
func TestTheStateFileKeepsWhereEachExitLeaves(t *testing.T) {
	d := &daemon{cfg: agentcfg.Config{StatePath: filepath.Join(t.TempDir(), "state.json")}}
	at := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	d.exits.SetEgress([]string{"bridge-tr5", "bridge-de5"}, map[string]string{"bridge-tr5": "PL"}, at)
	if err := d.persist(); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(d.cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var r exitcheck.State
	r.RestoreEgress(egressFrom(st.ExitEgress))
	if !reflect.DeepEqual(r.Egress(), map[string]string{"bridge-tr5": "PL"}) {
		t.Fatalf("after a restart: %v (state %v)", r.Egress(), st.ExitEgress)
	}
	if due := r.EgressDue([]string{"bridge-tr5"}, at.Add(3*time.Hour), exitWhereEvery); len(due) != 0 {
		t.Fatalf("asked again after a restart: %v", due)
	}
}
