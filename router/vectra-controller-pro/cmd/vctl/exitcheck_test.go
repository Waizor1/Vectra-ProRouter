package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"vectra-controller-pro/internal/vault"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/exitcheck"
	"vectra-controller-pro/internal/localctl"
)

const exitFixture = "../../internal/coreengine/xray/testdata/provider/entry-00.json"

var exitT0 = time.Date(2026, 9, 30, 6, 0, 0, 0, time.UTC)

func exitDoc(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(exitFixture)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Every render asks for the exit probe and leaves out the exits found unfit
// that the entry has — and none of it where the check is off or the router
// routes by PassWall's configuration.
func TestTheRenderCarriesTheExitProbeAndLeavesTheUnfitOut(t *testing.T) {
	d := &daemon{}
	d.exits.Restore(map[string]time.Time{"bridge-us5": exitT0, "bridge-gone9": exitT0})
	o := d.withExits(xray.SpliceOptions{}, exitDoc(t))
	if o.ExitProbeListen != xray.DefaultExitProbeListen || !reflect.DeepEqual(o.LeaveOut, []string{"bridge-us5"}) {
		t.Fatalf("options %+v", o)
	}
	for _, cfg := range []agentcfg.Config{{NoExitCheck: true}, {RouteSource: routeSourcePassWall}, {RouteSource: routeSourceNative}} {
		d.cfg = cfg
		if o := d.withExits(xray.SpliceOptions{}, exitDoc(t)); o.ExitProbeListen != "" || o.LeaveOut != nil {
			t.Fatalf("%+v: options %+v", cfg, o)
		}
	}
}

func exitRender(t *testing.T, leaveOut ...string) string {
	t.Helper()
	tp := &config.TproxyInbound{ListenIP: "0.0.0.0", Port: 12345, FwMark: 1, Tag: "tproxy-in"}
	out, _, err := xray.Splice(exitDoc(t), tp, xray.SpliceOptions{ExitProbeListen: xray.DefaultExitProbeListen, LeaveOut: leaveOut})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "xray.json")
	if err := vault.WriteFile(p, out); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeLoop answers the render requests the check sends, and counts them:
// the given answers first, then OK.
func fakeLoop(t *testing.T, d *daemon, answers ...localctl.SocketResponse) *int {
	t.Helper()
	d.uiReqs = make(chan uiRequest, 1)
	n := new(int)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case r := <-d.uiReqs:
				if r.op == opRerender && r.change == nil {
					*n++
				}
				if len(answers) > 0 {
					r.reply <- answers[0]
					answers = answers[1:]
					continue
				}
				r.reply <- localctl.SocketResponse{OK: true}
			case <-ctx.Done():
				return
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-done })
	return n
}

func fakeExitRound(t *testing.T, answer func(tag string) exitcheck.Result) *[]string {
	t.Helper()
	old := exitProbeRound
	t.Cleanup(func() { exitProbeRound = old })
	asked := new([]string)
	exitProbeRound = func(_ context.Context, p exitcheck.Prober, exits []string) []exitcheck.Result {
		*asked = exits
		var out []exitcheck.Result
		for _, e := range exits {
			out = append(out, answer(e))
		}
		return out
	}
	return asked
}

func filteredUS(tag string) exitcheck.Result {
	if tag == "bridge-us5" {
		return exitcheck.Result{Tag: tag, Control: true, Blocked: []bool{false, false, false}}
	}
	return exitcheck.Result{Tag: tag, Control: true, Blocked: []bool{true}}
}

// 1111, 2026-09-30: the round finds bridge-us5 filtered and has the loop
// render again; once the running render leaves it out, the next round asks
// for nothing.
func TestAnExitRoundLeavesAFilteredExitOut(t *testing.T) {
	asked := fakeExitRound(t, filteredUS)
	d := &daemon{cfg: agentcfg.Config{XrayRenderPath: exitRender(t)}}
	renders := fakeLoop(t, d)
	w := &exitWatch{}
	d.exitRound(context.Background(), w, exitT0)
	if !reflect.DeepEqual(*asked, xray.ExitsToCheck(exitDoc(t))) {
		t.Fatalf("asked %v", *asked)
	}
	if !reflect.DeepEqual(d.exits.Unfit(), []string{"bridge-us5"}) || *renders != 1 {
		t.Fatalf("unfit %v, renders %d", d.exits.Unfit(), *renders)
	}
	// The loop rendered: the running render leaves bridge-us5 out.
	d.cfg.XrayRenderPath = exitRender(t, "bridge-us5")
	d.exitRound(context.Background(), w, exitT0.Add(10*time.Minute))
	if *renders != 1 {
		t.Fatalf("asked for %d renders with the render already right", *renders)
	}
	// Still probed, so it can come back.
	if !contains(*asked, "bridge-us5") {
		t.Fatalf("the left-out exit is no longer asked: %v", *asked)
	}
	// A render that brings it back in (whatever made it) is corrected at
	// the next round, not an hour later.
	d.cfg.XrayRenderPath = exitRender(t)
	d.exitRound(context.Background(), w, exitT0.Add(20*time.Minute))
	if *renders != 2 {
		t.Fatalf("%d renders: a render without the leave-out was not corrected", *renders)
	}
}

// A render made for this set of unfit exits that still does not leave one
// out (it would empty a balancer) is not asked for again: the render was
// made with it, and asking again changes nothing.
func TestARenderThatCannotLeaveTheExitOutIsNotAskedForAgain(t *testing.T) {
	fakeExitRound(t, filteredUS)
	d := &daemon{cfg: agentcfg.Config{XrayRenderPath: exitRender(t)}}
	renders := fakeLoop(t, d)
	w := &exitWatch{}
	for i := 0; i < 8; i++ {
		d.exitRound(context.Background(), w, exitT0.Add(time.Duration(i)*10*time.Minute))
	}
	if *renders != 1 {
		t.Fatalf("%d renders asked for", *renders)
	}
}

// Review of r26: the ask was remembered before it was answered — a render
// the loop refused, or a loop busy with a job, waited an hour instead of the
// next round.
func TestARefusedRenderIsAskedForAgainAtTheNextRound(t *testing.T) {
	fakeExitRound(t, filteredUS)
	d := &daemon{cfg: agentcfg.Config{XrayRenderPath: exitRender(t)}}
	renders := fakeLoop(t, d, localctl.SocketResponse{Code: "apply_failed", Detail: "the gate refused it"})
	w := &exitWatch{}
	d.exitRound(context.Background(), w, exitT0)
	d.exitRound(context.Background(), w, exitT0.Add(10*time.Minute))
	if *renders != 2 {
		t.Fatalf("%d renders asked for; a refused one must be asked for again at the next round", *renders)
	}
}

func TestABusyLoopIsAskedAgainAtTheNextRound(t *testing.T) {
	fakeExitRound(t, filteredUS)
	old := exitAskWait
	exitAskWait = 50 * time.Millisecond
	t.Cleanup(func() { exitAskWait = old })
	d := &daemon{cfg: agentcfg.Config{XrayRenderPath: exitRender(t)}}
	d.uiReqs = make(chan uiRequest) // nobody reads: the loop is in a job
	w := &exitWatch{}
	d.exitRound(context.Background(), w, exitT0)
	renders := fakeLoop(t, d)
	d.exitRound(context.Background(), w, exitT0.Add(10*time.Minute))
	if *renders != 1 {
		t.Fatalf("%d renders asked for once the loop was free", *renders)
	}
}

// A render made before the probe existed has nothing to ask through.
func TestNoRoundWithoutTheProbeInTheRunningRender(t *testing.T) {
	asked := fakeExitRound(t, filteredUS)
	p := filepath.Join(t.TempDir(), "xray.json")
	if err := vault.WriteFile(p, exitDoc(t)); err != nil {
		t.Fatal(err)
	}
	d := &daemon{cfg: agentcfg.Config{XrayRenderPath: p}}
	renders := fakeLoop(t, d)
	d.exitRound(context.Background(), &exitWatch{}, exitT0)
	if *asked != nil || *renders != 0 || d.exits.Unfit() != nil {
		t.Fatalf("asked %v renders %d unfit %v", *asked, *renders, d.exits.Unfit())
	}
}

// The unfit exits go through state.json and come back as they were.
func TestTheUnfitExitsGoThroughState(t *testing.T) {
	m := unfitSince(unfitStamps(map[string]time.Time{"bridge-us5": exitT0}))
	if !m["bridge-us5"].Equal(exitT0) || len(m) != 1 {
		t.Fatalf("round trip %v", m)
	}
	if unfitStamps(nil) != nil {
		t.Fatal("no unfit exits wrote something")
	}
}

// Review of r26: unfit exits no balancer can lose (all three of the backup's)
// still moved the splice key — a re-render to the same bytes and an xray
// restart for nothing. They stay out of the options, and the key with them.
func TestExitsNoBalancerCanLoseDoNotMoveTheKey(t *testing.T) {
	d := &daemon{}
	base := d.withExits(xray.SpliceOptions{}, exitDoc(t)).Key()
	d.exits.Restore(map[string]time.Time{"hy2-de5": exitT0, "hy2-fin5": exitT0, "hy2-nl5": exitT0})
	o := d.withExits(xray.SpliceOptions{}, exitDoc(t))
	if len(o.LeaveOut) != 0 || o.Key() != base {
		t.Fatalf("leave-out %v; key moved: %v", o.LeaveOut, o.Key() != base)
	}
	o = d.withExits(xray.SpliceOptions{Services: map[string]string{"tiktok": "DE"}}, exitDoc(t))
	if !reflect.DeepEqual(o.LeaveOut, []string{"hy2-de5"}) {
		t.Fatalf("with Germany chosen for TikTok hy2-de5 leaves its overlay: %v", o.LeaveOut)
	}
}

// Review of r26: the check's render went the router UI's way — the location
// resolved from the entries cache under the owner's choice. A choice gone
// stale refused it (the unfit exit never left), and a newer cached entry
// would have been installed in the running one's place. The check renders
// the running document again, as it is, under the current options.
func TestTheExitCheckRendersTheRunningDocumentNotTheCache(t *testing.T) {
	d, _, entries, _ := newLocalUIDaemon(t)
	ctx := context.Background()
	if _, err := d.applyProvider(ctx, entries[0], false); err != nil {
		t.Fatal(err)
	}
	_, _ = localctl.UpdateOverrides(d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.EntryRemark = "a location the provider dropped"
		return nil
	})
	if resp := d.localReapply(ctx, nil); resp.OK {
		t.Fatalf("precondition: the UI's way refuses here, got %+v", resp)
	}
	d.exits.Restore(map[string]time.Time{"bridge-us5": time.Now()})
	if resp := d.handleUIRequest(ctx, opRerender, nil); !resp.OK {
		t.Fatalf("the check's render: %+v", resp)
	}
	raw, _ := vault.ReadFile(d.cfg.XrayRenderPath)
	probed, left, _ := renderExits(raw)
	if !contains(probed, "bridge-us5") || !reflect.DeepEqual(left, []string{"bridge-us5"}) {
		t.Fatalf("the running document was not rendered with the leave-out: probed %v left %v", probed, left)
	}
}

// 1111, 2026-09-30: «Турция» and «ОАЭ» left in Poland. The check learns where
// each exit really leaves, once a day, and the runtime carries it to the UI.
func TestTheExitCheckLearnsWhereExitsLeaveOnceADay(t *testing.T) {
	fakeExitRound(t, filteredUS)
	old := exitLocate
	t.Cleanup(func() { exitLocate = old })
	var asked [][]string
	exitLocate = func(_ context.Context, p exitcheck.Prober, exits []string) map[string]string {
		asked = append(asked, exits)
		if p.Where == "" {
			t.Fatal("no trace to locate by")
		}
		return map[string]string{"bridge-tr5": "PL", "bridge-de5": "DE"}
	}
	d := &daemon{cfg: agentcfg.Config{XrayRenderPath: exitRender(t, "bridge-us5")}}
	fakeLoop(t, d)
	w := &exitWatch{}
	d.exitRound(context.Background(), w, exitT0)
	d.exitRound(context.Background(), w, exitT0.Add(10*time.Minute))
	if len(asked) != 1 || len(asked[0]) != len(xray.ExitsToCheck(exitDoc(t))) {
		t.Fatalf("located %d times: %v", len(asked), asked)
	}
	if got := d.exits.Egress(); got["bridge-tr5"] != "PL" || got["bridge-de5"] != "DE" {
		t.Fatalf("egress %v", got)
	}
	d.exitRound(context.Background(), w, exitT0.Add(25*time.Hour))
	if len(asked) != 2 {
		t.Fatalf("not located again after a day: %d", len(asked))
	}
}

// The review of r29–r31: the location look asked exits the same round had
// found dead — an 8-s timeout each, for nothing. Only exits that answered the
// round's neutral URL are asked; a dead one waits for a round it answers.
func TestTheLocationLookSkipsExitsTheRoundFoundDead(t *testing.T) {
	fakeExitRound(t, func(tag string) exitcheck.Result {
		if tag == "bridge-tr5" {
			return exitcheck.Result{Tag: tag} // dead: the neutral URL did not answer
		}
		return filteredUS(tag)
	})
	old := exitLocate
	t.Cleanup(func() { exitLocate = old })
	var asked []string
	exitLocate = func(_ context.Context, _ exitcheck.Prober, exits []string) map[string]string {
		asked = append(asked, exits...)
		return nil
	}
	d := &daemon{cfg: agentcfg.Config{XrayRenderPath: exitRender(t, "bridge-us5")}}
	fakeLoop(t, d)
	d.exitRound(context.Background(), &exitWatch{}, exitT0)
	if len(asked) == 0 || contains(asked, "bridge-tr5") {
		t.Fatalf("located %v — the dead bridge-tr5 asked, or nothing asked", asked)
	}
	if due := d.exits.EgressDue([]string{"bridge-tr5"}, exitT0.Add(10*time.Minute), exitWhereEvery); len(due) != 1 {
		t.Fatalf("the dead exit is not due at the next round: %v", due)
	}
}
