package uiapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vectra-controller-pro/internal/tune"
)

func sp(s string) *string { return &s }

// lowmemPlan is a 234 MB router the tune has been through: its zram up, the
// memory settings its own, packet steering there before it.
func lowmemPlan() *tune.Plan {
	return &tune.Plan{On: true, Profile: tune.Lowmem, MemTotalMiB: 234, Cores: 2, Items: []tune.Item{
		{ID: tune.ItemZram, State: tune.Applied, Value: sp("117"), Target: "on"},
		{ID: tune.ItemSwappiness, State: tune.Applied, Value: sp("80"), Target: "80"},
		{ID: tune.ItemVFSCachePressure, State: tune.Applied, Value: sp("200"), Target: "200"},
		{ID: tune.ItemPacketSteering, State: tune.Already, Value: sp("1"), Target: "1"},
		{ID: tune.ItemFlowOffloading, State: tune.Skipped, Target: "1", Reason: tune.ReasonNoKernelSupport},
	}}
}

// status.tune is the tune's plan, as codes and short values: null where the
// router could not read it, and a reason only where there is one.
func TestStatusCarriesTheTune(t *testing.T) {
	st := BuildStatus(Inputs{Now: time.Now(), Tune: lowmemPlan()})
	if st.Tune == nil || !st.Tune.Enabled || st.Tune.Profile != "lowmem" || len(st.Tune.Items) != 5 {
		t.Fatalf("tune = %+v", st.Tune)
	}
	raw, err := json.Marshal(st.Tune.Items[4])
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"id":"flow_offloading","state":"skipped","value":null,"target":"1","reason":"no_kernel_support"}`; string(raw) != want {
		t.Fatalf("item = %s\nwant   %s", raw, want)
	}
	raw, _ = json.Marshal(st.Tune.Items[0])
	if want := `{"id":"zram","state":"applied","value":"117","target":"on","reason":null}`; string(raw) != want {
		t.Fatalf("item = %s\nwant   %s", raw, want)
	}
	if none := BuildStatus(Inputs{Now: time.Now()}); none.Tune != nil {
		t.Fatalf("no plan read, tune = %+v", none.Tune)
	}
}

// The diagnostics' line: what is in place, in plain codes; a small router
// without its compressed swap is a warning — the memory guard and every
// measurement of it assume one.
func TestTheTuneCheck(t *testing.T) {
	c := check(t, BuildDiagnostics(Inputs{Now: time.Now(), Tune: lowmemPlan()}), "tune")
	if c == nil || c.Status != "ok" {
		t.Fatalf("tune = %+v", c)
	}
	if got, want := paramsJSON(t, c), `{"enabled":true,"items":["zram","swappiness","vfs_cache_pressure","packet_steering"],"profile":"lowmem","zramMiB":117}`; got != want {
		t.Fatalf("params = %s\nwant     %s", got, want)
	}

	noSwap := lowmemPlan()
	noSwap.Items[0] = tune.Item{ID: tune.ItemZram, State: tune.Skipped, Target: "on", Reason: tune.ReasonNotInstalled}
	c = check(t, BuildDiagnostics(Inputs{Now: time.Now(), Tune: noSwap}), "tune")
	if c == nil || c.Status != "warn" || c.Params["zramMiB"] != nil {
		t.Fatalf("a small router without its swap: %+v", c)
	}

	big := &tune.Plan{On: false, Profile: tune.Standard, MemTotalMiB: 1024, Cores: 4, Items: []tune.Item{
		{ID: tune.ItemZram, State: tune.Skipped, Target: "on", Reason: tune.ReasonEnoughRAM},
		{ID: tune.ItemPacketSteering, State: tune.Skipped, Target: "1", Reason: tune.ReasonOff},
	}}
	c = check(t, BuildDiagnostics(Inputs{Now: time.Now(), Tune: big}), "tune")
	if c == nil || c.Status != "ok" || paramsJSON(t, c) != `{"enabled":false,"items":[],"profile":"standard","zramMiB":null}` {
		t.Fatalf("a large router, the tune off: %+v", c)
	}

	if check(t, BuildDiagnostics(Inputs{Now: time.Now()}), "tune") != nil {
		t.Fatal("a tune check without a plan")
	}
}

// Gather reads the tune's plan only when the answer needs it, and never
// runs anything for it (the tune's Env has no runner here).
func TestGatherReadsTheTuneWhenAsked(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "proc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proc/meminfo"), []byte("MemTotal: 239792 kB\nMemAvailable: 90000 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := Env{Tune: tune.Env{ProcDir: filepath.Join(dir, "proc"), ConfigDir: filepath.Join(dir, "config"),
		RCDir: filepath.Join(dir, "rc.d"), InitDir: filepath.Join(dir, "init.d"), Backup: filepath.Join(dir, "tune-backup.json")}}
	in := Gather(context.Background(), env, Need{Tune: true})
	if in.Tune == nil || in.Tune.Profile != tune.Lowmem || in.Tune.MemTotalMiB != 234 {
		t.Fatalf("tune = %+v", in.Tune)
	}
	if in := Gather(context.Background(), env, Need{}); in.Tune != nil {
		t.Fatal("read the tune for an answer that does not carry it")
	}
	if !NeedStatus.Tune || !NeedDiagnostics.Tune {
		t.Fatal("status and diagnostics do not read the tune")
	}
}
