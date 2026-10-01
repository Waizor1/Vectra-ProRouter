package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"vectra-controller-pro/internal/vault"

	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/xrayview"
)

// twoLocations is the real provider entry plus a second, smaller location
// with a different remark, as the provider's array would carry them.
func twoLocations(t *testing.T) (entries [][]byte, remarks []string) {
	t.Helper()
	first := providerEntry(t)
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(first, &doc); err != nil {
		t.Fatal(err)
	}
	var r0 string
	_ = json.Unmarshal(doc["remarks"], &r0)
	second := bytes.Replace(first, []byte(`"`+r0+`"`), []byte(`"🇩🇪 Германия"`), 1)
	if bytes.Equal(first, second) {
		t.Fatal("could not derive a second location from the fixture")
	}
	return [][]byte{first, second}, []string{r0, "🇩🇪 Германия"}
}

func newLocalUIDaemon(t *testing.T) (*daemon, *providerStub, [][]byte, []string) {
	t.Helper()
	dir := t.TempDir()
	entries, remarks := twoLocations(t)
	provider := newProviderStub(t, bytes.Join(entries, []byte(",")))
	panel := newPanelStub(t, operatorConfigPointingAt(t, provider.URL))
	d := newTestDaemon(t, dir, panel, provider)
	cfg, err := config.Unmarshal(operatorConfigPointingAt(t, provider.URL))
	if err != nil {
		t.Fatal(err)
	}
	config.ApplyDefaults(cfg)
	d.desired = cfg
	d.rebuildApplier()
	return d, provider, entries, remarks
}

func TestFetchCachesEveryLocationAndHonoursTheRouterChoice(t *testing.T) {
	d, _, entries, remarks := newLocalUIDaemon(t)
	ctx := context.Background()

	// No local choice: the panel's (index 0).
	raw, meta, err := d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, entries[0]) || meta["entrySource"] != "panel" {
		t.Fatalf("got entry %v source %v", meta["entryIndex"], meta["entrySource"])
	}
	idx, err := localctl.LoadEntriesIndex(d.cfg.EntriesIndexPath)
	if err != nil {
		t.Fatalf("the fetch cached nothing: %v", err)
	}
	if len(idx.Entries) != 2 || idx.Entries[1].Digest != apply.Digest(entries[1]) {
		t.Fatalf("index = %+v", idx.Entries)
	}

	// A location chosen on the router wins on the next fetch — a nightly
	// refresh must not undo it.
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.EntryRemark = remarks[1]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, meta, err = d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, entries[1]) || meta["entrySource"] != "local" || meta["entryIndex"] != 1 {
		t.Fatalf("router choice ignored: index %v source %v", meta["entryIndex"], meta["entrySource"])
	}

	// A choice the provider no longer offers falls back to the panel's, and
	// says so.
	_, _ = localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.EntryRemark = "🇫🇷 Франция"
		return nil
	})
	raw, meta, err = d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, entries[0]) || meta["localEntryStale"] != true {
		t.Fatalf("stale choice: index %v stale %v", meta["entryIndex"], meta["localEntryStale"])
	}
}

// Switching location on the router is a local re-render from the cache: the
// provider is not asked again.
func TestLocalReapplySwitchesLocationWithoutFetching(t *testing.T) {
	d, provider, entries, remarks := newLocalUIDaemon(t)
	ctx := context.Background()
	raw, _, err := d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.applyProvider(ctx, raw, false); err != nil {
		t.Fatal(err)
	}
	fetches := len(provider.headers())

	resp := d.localReapply(ctx, &localctl.Change{SetEntry: &localctl.EntryChoice{Remark: remarks[1], Index: 1}})
	if !resp.OK {
		t.Fatalf("localReapply = %+v", resp)
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); ov.EntryRemark != remarks[1] {
		t.Fatalf("the applied choice was not persisted: %+v", ov)
	}
	if n := len(provider.headers()); n != fetches {
		t.Fatalf("the provider was asked %d more time(s); a location switch must use the cache", n-fetches)
	}
	if d.st.ConfigDigest != apply.Digest(entries[1]) {
		t.Fatal("the installed document is not the chosen location")
	}
	onDisk, _ := vault.ReadFile(d.cfg.ProviderConfigPath)
	if !bytes.Equal(onDisk, entries[1]) {
		t.Fatal("the persisted provider document is not the chosen location, byte for byte")
	}
	e := d.runningEntry()
	if e == nil || e.Index != 1 || !e.Local || e.Remark != remarks[1] || e.Count != 2 {
		t.Fatalf("runningEntry = %+v", e)
	}

	// Back to the panel's choice.
	if resp := d.localReapply(ctx, &localctl.Change{ResetEntry: true}); !resp.OK {
		t.Fatalf("reset = %+v", resp)
	}
	if e := d.runningEntry(); e == nil || e.Index != 0 || e.Local {
		t.Fatalf("after reset runningEntry = %+v", e)
	}
}

func TestRenderCarriesTheRouterRuntimeAndTheProbeInterval(t *testing.T) {
	d, _, entries, _ := newLocalUIDaemon(t)
	ctx := context.Background()
	if _, err := d.applyProvider(ctx, entries[0], false); err != nil {
		t.Fatal(err)
	}
	render, _ := vault.ReadFile(d.cfg.XrayRenderPath)
	for _, want := range []string{`"listen":"127.0.0.1:10085"`, `"listen":"127.0.0.1:10086"`, `"interval":"600s"`} {
		if !strings.Contains(string(render), want) {
			t.Errorf("render lacks %s", want)
		}
	}
	if d.probe == nil || d.probe.Source != "default" || d.probe.IntervalSec != 600 || d.probe.ProviderIntervalSec != 43200 {
		t.Fatalf("probe = %+v", d.probe)
	}
	key := d.st.SpliceKey

	// Same provider bytes, new interval chosen on the router: re-rendered.
	_, _ = localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.ProbeIntervalSec = 120
		return nil
	})
	res, err := d.applyProvider(ctx, entries[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || d.st.SpliceKey == key {
		t.Fatal("a new probe interval did not re-render an unchanged provider document")
	}
	render, _ = vault.ReadFile(d.cfg.XrayRenderPath)
	if !strings.Contains(string(render), `"interval":"120s"`) || d.probe.Source != "local" {
		t.Fatalf("probe = %+v", d.probe)
	}
	// And unchanged again: a no-op.
	if res, _ := d.applyProvider(ctx, entries[0], false); !res.Noop {
		t.Fatal("an unchanged document under unchanged options was re-rendered")
	}
}

// An upgrade that adds the API must not wait for the next panel job: the
// daemon re-renders the installed document at start, before xray is up.
func TestReconcileRenderAddsTheAPIToAnOldRender(t *testing.T) {
	d, _, entries, _ := newLocalUIDaemon(t)
	ctx := context.Background()
	// An install made by a controller without the router runtime.
	old := d.applier
	old.Splice = xrayOptionsZero()
	res, err := old.Apply(ctx, entries[0], "", false)
	if err != nil {
		t.Fatal(err)
	}
	d.st.ConfigDigest, d.st.SpliceKey = res.AppliedDigest, ""
	if err := vault.WriteFile(d.cfg.ProviderConfigPath, entries[0]); err != nil {
		t.Fatal(err)
	}
	if b, _ := vault.ReadFile(d.cfg.XrayRenderPath); strings.Contains(string(b), "10085") {
		t.Fatal("precondition: the old render already has the API")
	}
	d.reconcileRender(ctx)
	if b, _ := vault.ReadFile(d.cfg.XrayRenderPath); !strings.Contains(string(b), `"listen":"127.0.0.1:10085"`) {
		t.Fatal("reconcileRender left the old render in place")
	}
	if d.st.SpliceKey == "" {
		t.Fatal("SpliceKey not recorded")
	}
}

func TestUISocketServesRuntimeAndQueuesChangesToTheLoop(t *testing.T) {
	d, _, entries, _ := newLocalUIDaemon(t)
	dir, err := os.MkdirTemp("/tmp", "vui")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d.cfg.UISocketPath = filepath.Join(dir, "ui.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := d.applyProvider(ctx, entries[0], false); err != nil {
		t.Fatal(err)
	}
	d.publishRuntime()
	go d.serveUI(ctx)
	// The loop: serve UI requests until the test ends.
	go d.waitForTick(ctx, make(chan time.Time))

	var resp localctl.SocketResponse
	for i := 0; i < 100; i++ {
		cctx, ccancel := context.WithTimeout(ctx, time.Second)
		resp, err = localctl.Call(cctx, d.cfg.UISocketPath, localctl.SocketRequest{Op: localctl.OpRuntime})
		ccancel()
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || !resp.OK || resp.Runtime == nil {
		t.Fatalf("runtime: %+v %v", resp, err)
	}
	if resp.Runtime.ControllerPID != os.Getpid() || resp.Runtime.Probe == nil || resp.Runtime.Engine.State == "" {
		t.Fatalf("runtime = %+v", resp.Runtime)
	}

	cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
	defer ccancel()
	resp, err = localctl.Call(cctx, d.cfg.UISocketPath, localctl.SocketRequest{Op: localctl.OpReapply})
	if err != nil || !resp.OK {
		t.Fatalf("reapply through the socket: %+v %v", resp, err)
	}
}

// A pin whose balancer or node the running config no longer has is dropped,
// not applied to whatever now carries that tag.
func TestReapplyPinsDropsPinsTheRunningConfigCannotHonour(t *testing.T) {
	d, _, entries, _ := newLocalUIDaemon(t)
	if _, err := d.applyProvider(context.Background(), entries[0], false); err != nil {
		t.Fatal(err)
	}
	_, _ = localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.Pins = map[string]string{"BL-GONE": "bridge-de5", "BL-MAIN": "bridge-ru-tcp"}
		return nil
	})
	d.pinBudget = 200 * time.Millisecond // nothing listens on the API here
	d.reapplyPins(1)
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	if len(ov.Pins) != 0 {
		t.Fatalf("pins left: %v — both were impossible (no such balancer; a node of another balancer)", ov.Pins)
	}
}

// Review of r26: the exit check leaving a pinned exit out made the next xray
// start drop the owner's pin for good — a temporary, automatic condition
// deleting an explicit choice. The pin is parked instead: kept, not applied
// while the exit is out (it would steer everything back onto it), and held
// again at the start that brings the exit back.
func TestReapplyPinsParksAPinToAnExitTheCheckLeftOut(t *testing.T) {
	d, _, entries, _ := newLocalUIDaemon(t)
	d.exits.Restore(map[string]time.Time{"bridge-us5": time.Now()})
	if _, err := d.applyProvider(context.Background(), entries[0], false); err != nil {
		t.Fatal(err)
	}
	raw, _ := vault.ReadFile(d.cfg.XrayRenderPath)
	if v, err := xrayview.Parse(raw); err != nil || v.CanPin("BL-MAIN", "bridge-us5") == nil {
		t.Fatalf("precondition: the render still lets BL-MAIN pin bridge-us5 (%v)", err)
	}
	_, _ = localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.Pins = map[string]string{"BL-MAIN": "bridge-us5", "BL-GONE": "bridge-de5"}
		return nil
	})
	d.pinBudget = 200 * time.Millisecond // nothing listens on the API here
	d.reapplyPins(1)
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	if !reflect.DeepEqual(ov.Pins, map[string]string{"BL-MAIN": "bridge-us5"}) {
		t.Fatalf("pins %v: the parked pin must stay, the impossible one go", ov.Pins)
	}
}

func xrayOptionsZero() xray.SpliceOptions { return xray.SpliceOptions{} }

// A panel config carrying a User-Agent the provider punishes is refused when
// it is adopted — and nothing is fetched with it.
func TestApplyJobRefusesAConfigWithAMalformedHappAgent(t *testing.T) {
	dir := t.TempDir()
	provider := newProviderStub(t, providerEntry(t))
	desired := bytes.Replace(operatorConfigPointingAt(t, provider.URL),
		[]byte(`"v2rayNG/1.9.5"`), []byte(`"Happ/1.0"`), 1)
	if !bytes.Contains(desired, []byte("Happ/1.0")) {
		t.Fatal("precondition: could not plant the agent in the operator config")
	}
	panel := newPanelStub(t, desired)
	d := newTestDaemon(t, dir, panel, provider)
	ctx := context.Background()
	if err := d.runOnce(ctx); err != nil { // register
		t.Fatal(err)
	}
	_ = d.runOnce(ctx) // check-in + the apply job
	panel.mu.Lock()
	results := panel.results["j1"]
	panel.mu.Unlock()
	if len(results) == 0 {
		t.Fatal("the apply job reported nothing")
	}
	last := results[len(results)-1]
	if last.Status != "failure" || !strings.Contains(fmt.Sprint(last.Result), "anti-fraud") {
		t.Fatalf("apply job = %s %v; want a failure naming the anti-fraud rule", last.Status, last.Result)
	}
	if n := len(provider.headers()); n != 0 {
		t.Fatalf("the provider received %d request(s) with the refused agent", n)
	}
	if d.desired != nil && d.desired.Subscriptions[0].UserAgent == "Happ/1.0" {
		t.Fatal("the refused config became the desired state")
	}
}

type failingValidator struct{}

func (failingValidator) Test(context.Context, []byte) error {
	return errors.New("xray -test: rejected")
}

// A change is persisted only once it is running. One that cannot be applied —
// a location the cache does not have, a render xray refuses — leaves the
// overrides exactly as they were, so a later refresh does not keep choosing a
// location the router never ran.
func TestAFailedChangeIsNotRemembered(t *testing.T) {
	d, _, entries, remarks := newLocalUIDaemon(t)
	ctx := context.Background()
	raw, _, err := d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.applyProvider(ctx, raw, false); err != nil {
		t.Fatal(err)
	}
	digest := d.st.ConfigDigest

	// A location the cache does not have.
	resp := d.localReapply(ctx, &localctl.Change{SetEntry: &localctl.EntryChoice{Remark: "🇫🇷 Франция", Index: 5}})
	if resp.OK || resp.Code != "unknown_entry" {
		t.Fatalf("unknown location = %+v, want unknown_entry (not the panel's location reported as success)", resp)
	}
	// A render xray refuses.
	d.applier.Validate = failingValidator{}
	resp = d.localReapply(ctx, &localctl.Change{SetEntry: &localctl.EntryChoice{Remark: remarks[1], Index: 1}})
	if resp.OK || resp.Code != "apply_failed" {
		t.Fatalf("refused render = %+v, want apply_failed", resp)
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); ov.HasEntry() {
		t.Fatalf("a change that never ran was remembered: %+v", ov)
	}
	if d.st.ConfigDigest != digest || d.st.ConfigDigest != apply.Digest(entries[0]) {
		t.Fatal("the running location changed although the change failed")
	}
	if d.lastApplyErr == "" {
		t.Fatal("the failure left no trace for the status answer")
	}
}

// Two locations with one remark: the index the choice was made at decides.
func TestResolvePrefersTheChosenIndexAmongEqualRemarks(t *testing.T) {
	i := 2
	idx, local, stale, err := localctl.Resolve([]string{"de", "pl", "de"}, localctl.Overrides{EntryRemark: "de", EntryIndex: &i}, "", 0)
	if err != nil || idx != 2 || !local || stale {
		t.Fatalf("Resolve = %d %v %v %v; want 2 (the chosen twin)", idx, local, stale, err)
	}
}

// The rpcd side of the same rule: it writes no overrides itself; it hands the
// change to the daemon. With no daemon, nothing is written at all.
func TestRPCDWritesNoChoiceWhenTheDaemonIsDown(t *testing.T) {
	d, _, _, remarks := newLocalUIDaemon(t)
	ctx := context.Background()
	if _, _, err := d.fetchProviderDocument(ctx, d.desired); err != nil { // builds the index
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/tmp", "vrp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d.cfg.UISocketPath = filepath.Join(dir, "absent.sock")
	a := rpcdMutate(ctx, d.cfg, "select_entry", []byte(`{"index":1}`))
	if a.OK || a.Code != "controller_down" {
		t.Fatalf("select_entry with no daemon = %+v", a)
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); ov.HasEntry() {
		t.Fatalf("rpcd wrote a choice nobody will run: %+v (remarks %v)", ov, remarks)
	}
}
