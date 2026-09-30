package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/supervisor"
)

// waitBaseline waits until the runtime serves a leak baseline for a running
// xray other than notPID.
func waitBaseline(t *testing.T, d *daemon, notPID int) *localctl.LeakBaseline {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		rt := d.liveRuntime()
		b := rt.LeakBaseline
		if b == nil {
			continue
		}
		if b.XrayPID != rt.Engine.PID || rt.Engine.State != "running" {
			t.Fatalf("served the baseline of xray %d with xray %d %s", b.XrayPID, rt.Engine.PID, rt.Engine.State)
		}
		if b.XrayPID != notPID {
			return b
		}
	}
	t.Fatal("no leak baseline was taken")
	return nil
}

// After every xray start the daemon reads the leak counter once that xray
// answers — the restart window's fall-through stays below the line — and
// serves it, for that xray only, in the runtime snapshot rpcd reads.
func TestLeakBaselineIsTakenAfterEachXrayStartAndServed(t *testing.T) {
	d, _, _, _ := newLocalUIDaemon(t)
	dir, err := os.MkdirTemp("/tmp", "vlb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d.cfg.UISocketPath = filepath.Join(dir, "ui.sock")

	// The running xray's API: it accepts once xray has started.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(`{"api":{"listen":"`+ln.Addr().String()+`"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The table is still loading for the first two reads.
	var reads, leak atomic.Int64
	leak.Store(42)
	d.readCounters = func(context.Context) (map[string]int64, bool) {
		if reads.Add(1) <= 2 {
			return nil, false
		}
		return map[string]int64{"vctl_would_leak": leak.Load(), "vctl_tproxy_hits": 7, "vctl_tproxy_escaped": 2,
			"vctl_killswitch_drops": 3, "vctl_unproxied_other": 9}, true
	}
	d.pinBudget = 10 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		for i := 0; i < 500 && d.sup.Status().State != supervisor.StateStopped; i++ {
			time.Sleep(10 * time.Millisecond)
		}
	})
	d.publishRuntime()
	go d.serveUI(ctx)
	if b := d.liveRuntime().LeakBaseline; b != nil {
		t.Fatalf("a baseline before any xray start: %+v", b)
	}

	d.ensureSupervisor(ctx) // the fake xray starts; the start hook takes the baseline
	first := waitBaseline(t, d, 0)
	if first.Packets != 42 || first.Escaped != 2 || first.Drops != 3 || first.TproxyHits != 7 || first.At.IsZero() || reads.Load() != 3 {
		t.Fatalf("baseline = %+v after %d read(s); want 42/2/3/7, taken once the table answered", first, reads.Load())
	}
	// A second reading for the same xray does not replace its baseline, and one
	// for an xray that is not the running one is never stored.
	if d.storeLeakBaseline(&localctl.LeakBaseline{Packets: 999, XrayPID: first.XrayPID}) || d.leakBaseline.Load().Packets != 42 {
		t.Fatalf("the same xray's baseline was replaced: %+v", d.leakBaseline.Load())
	}
	if d.storeLeakBaseline(&localctl.LeakBaseline{Packets: 1, XrayPID: first.XrayPID + 100000}) {
		t.Fatal("stored a baseline for an xray that is not running")
	}

	// rpcd reads it through the UI socket.
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
	if err != nil || resp.Runtime == nil || resp.Runtime.LeakBaseline == nil ||
		resp.Runtime.LeakBaseline.Packets != 42 || resp.Runtime.LeakBaseline.XrayPID != first.XrayPID {
		t.Fatalf("runtime over the socket: %+v %v", resp.Runtime, err)
	}

	// A baseline taken for another xray is not this one's.
	d.leakBaseline.Store(&localctl.LeakBaseline{Packets: 1, XrayPID: first.XrayPID + 100000})
	if b := d.liveRuntime().LeakBaseline; b != nil {
		t.Fatalf("served another xray's baseline: %+v", b)
	}

	// A restart falls through again, by design: the new xray gets its own
	// baseline, and the old one is never served for it.
	leak.Store(50)
	if err := d.sup.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	second := waitBaseline(t, d, first.XrayPID)
	if second.Packets != 50 {
		t.Fatalf("baseline after the restart = %+v, want 50", second)
	}
	// A reading that was still under way for the replaced xray lands late: it
	// must not overwrite the running xray's baseline.
	if d.storeLeakBaseline(&localctl.LeakBaseline{Packets: 1, XrayPID: first.XrayPID}) || d.leakBaseline.Load().XrayPID != second.XrayPID {
		t.Fatalf("a late reading for the replaced xray won: %+v", d.leakBaseline.Load())
	}
}

// No baseline is taken without an API to tell when xray has started, nor for
// an xray that is no longer the running one — and none is served for one.
func TestNoLeakBaselineWithoutAnAPIOrARunningXray(t *testing.T) {
	d, _, _, _ := newLocalUIDaemon(t)
	d.readCounters = func(context.Context) (map[string]int64, bool) {
		return map[string]int64{"vctl_would_leak": 1}, true
	}
	d.pinBudget = 5 * time.Second
	start := time.Now()
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(`{"outbounds":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d.takeLeakBaseline(4242)
	if err := os.WriteFile(d.cfg.XrayRenderPath, []byte(`{"api":{"listen":"127.0.0.1:10085"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	d.takeLeakBaseline(4242) // xray is not running at all here
	if b := d.leakBaseline.Load(); b != nil || time.Since(start) > 2*time.Second {
		t.Fatalf("baseline %+v after %s", b, time.Since(start))
	}
	d.leakBaseline.Store(&localctl.LeakBaseline{Packets: 1, XrayPID: 4242})
	if b := d.liveRuntime().LeakBaseline; b != nil {
		t.Fatalf("served a baseline with no xray running: %+v", b)
	}
}
