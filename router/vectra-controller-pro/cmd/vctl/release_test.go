package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/supervisor"
	"vectra-controller-pro/internal/uiapi"
)

// releasePanel is a panel that can configure the router (a desired revision
// and an apply job), name its owner, and release it — whatever a test sets
// for the next answer. It keeps every check-in body and job result.
type releasePanel struct {
	*httptest.Server
	mu      sync.Mutex
	desired []byte // with an apply job on every check-in; nil: neither
	revID   string
	info    controlplane.ClaimInfo
	regInfo controlplane.ClaimInfo
	bodies  []json.RawMessage
	results map[string][]controlplane.JobResultRequest
}

func newReleasePanel(t *testing.T) *releasePanel {
	t.Helper()
	p := &releasePanel{results: map[string][]controlplane.JobResultRequest{}}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		switch r.URL.Path {
		case "/api/router/register":
			_ = json.NewEncoder(w).Encode(controlplane.RegisterResponse{RouterID: "r-rel", IssuedToken: "tok-rel",
				Status: "approved", ClaimInfo: p.regInfo})
		case "/api/router/check-in":
			var body json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.bodies = append(p.bodies, body)
			resp := controlplane.CheckInResponse{Status: "ok", ClaimInfo: p.info}
			if p.desired != nil {
				rev, _ := json.Marshal(controlplane.DesiredRevisionSummary{ID: p.revID, RevisionNumber: 1, Status: "approved",
					EngineMode: controlplane.EngineModeXrayDirect, Config: json.RawMessage(p.desired)})
				resp.DesiredRevision = rev
				resp.Jobs = []controlplane.Job{{ID: "apply-" + p.revID, Type: "apply_xray_config", State: "queued"}}
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/router/job-result":
			var req controlplane.JobResultRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			p.results[req.JobID] = append(p.results[req.JobID], req)
			_ = json.NewEncoder(w).Encode(controlplane.JobResultResponse{Acknowledged: true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.Close)
	return p
}

// next sets what the following answers say.
func (p *releasePanel) next(fn func(p *releasePanel)) {
	p.mu.Lock()
	fn(p)
	p.mu.Unlock()
}

func (p *releasePanel) resultsFor(id string) []controlplane.JobResultRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]controlplane.JobResultRequest(nil), p.results[id]...)
}

func (p *releasePanel) lastClaim(t *testing.T) *controlplane.ClaimAnnouncement {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bodies) == 0 {
		t.Fatal("no check-in reached the panel")
	}
	var req struct {
		Claim *controlplane.ClaimAnnouncement `json:"claim"`
	}
	if err := json.Unmarshal(p.bodies[len(p.bodies)-1], &req); err != nil {
		t.Fatal(err)
	}
	return req.Claim
}

// logLines captures what the controller logs, goroutine-safe.
type logLines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logLines) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(b)
}

func (l *logLines) matching(s string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, line := range strings.Split(l.buf.String(), "\n") {
		if strings.Contains(line, s) {
			out = append(out, line)
		}
	}
	return out
}

func captureLog(t *testing.T) *logLines {
	t.Helper()
	l := &logLines{}
	prev := logging.L()
	logging.SetDefault(logging.New("info", l, "text"))
	t.Cleanup(func() { logging.SetDefault(prev) })
	return l
}

// releaseRouter is a router in service: registered, configured by the panel
// (operator config, provider document, locations cache, render, xray
// running) with choices made on it. owner: whether someone claimed it.
type releaseRouter struct {
	d        *daemon
	dir      string
	panel    *releasePanel
	provider *providerStub
	fw       *recordedCmds
	spec     firewall.Spec
	codeSeen string // the claim code shown before the router was linked
}

func newReleaseRouter(t *testing.T, owner bool) *releaseRouter {
	t.Helper()
	r := &releaseRouter{dir: t.TempDir(), panel: newReleasePanel(t), provider: newProviderStub(t, providerEntry(t))}
	r.d = newTestDaemon(t, r.dir, &panelStub{Server: r.panel.Server}, r.provider)
	r.d.pinBudget = 200 * time.Millisecond // nothing answers on xray's API here
	r.fw = &recordedCmds{}
	r.d.runFirewallCmd = r.fw.run
	ctx := context.Background()
	t.Cleanup(func() { r.d.stopXray(context.Background()) })

	if err := r.d.runOnce(ctx); err != nil { // register
		t.Fatal(err)
	}
	r.codeSeen = r.d.claim.view(time.Now()).Code

	desired := operatorConfigPointingAt(t, r.provider.URL)
	r.panel.next(func(p *releasePanel) {
		p.desired, p.revID = desired, "rev-1"
		p.info = controlplane.ClaimInfo{ClaimKey: &controlplane.ClaimKey{Kid: 7, PublicKey: testClaimKeyB64(t)}, BotUsername: "VectraBot"}
		if owner {
			p.info.Owner = json.RawMessage(`{"label":"Иван П."}`)
		}
	})
	if err := r.d.runOnce(ctx); err != nil { // check-in: configured
		t.Fatal(err)
	}
	r.panel.next(func(p *releasePanel) { p.desired, p.info = nil, controlplane.ClaimInfo{} })
	waitUntil(t, "xray running", func() bool { return r.d.sup.Status().State == supervisor.StateRunning })

	idx := 1
	if _, err := localctl.UpdateOverrides(r.d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.EntryRemark, o.EntryIndex = "🇩🇪 Германия", &idx
		o.Pins = map[string]string{"BL-MAIN": "node-b"}
		o.ProbeIntervalSec = 300
		o.Direct, o.Proxy = []string{"sberbank.ru"}, []string{"rutracker.org"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, f := range r.ownerFiles() {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("the configured router has no %s: %v", f, err)
		}
	}
	if r.d.desired == nil || r.d.st.AppliedRevisionID != "rev-1" || r.d.st.ConfigDigest == "" || r.d.st.SpliceKey == "" ||
		r.d.st.LastDesiredRevision == nil || (r.d.st.ClaimOwner != nil) != owner {
		t.Fatalf("not configured as the test needs: %+v", r.d.st)
	}
	cfg, err := config.LoadSecret(r.d.cfg.XrayConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	r.spec, _ = firewallSpecFromConfig(cfg)
	return r
}

// ownerFiles are the files a release removes.
func (r *releaseRouter) ownerFiles() []string {
	c := r.d.cfg
	return []string{c.XrayConfigPath, c.ProviderConfigPath, c.XrayRenderPath, c.EntriesPath, c.EntriesIndexPath, c.OverridesPath}
}

func testClaimKeyB64(t *testing.T) string {
	t.Helper()
	_, key := testVectraKey(t)
	return key.PublicKey
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func alive(pid int) bool { return pid > 0 && syscall.Kill(pid, 0) == nil }

// The owner unbinds the router in the Vectra app: the router stops carrying
// traffic for them, forgets everything of theirs, keeps what is its own, and
// offers a new code — once.
func TestAReleasedRouterIsAsItCameOutOfTheBox(t *testing.T) {
	r := newReleaseRouter(t, true)
	logs := captureLog(t)
	st0 := r.d.st
	xrayPID := r.d.sup.Status().PID

	// The answer that releases also carries a revision and a job: meant for
	// the owner who just left, they are neither kept nor run.
	r.panel.next(func(p *releasePanel) {
		p.info = controlplane.ClaimInfo{Released: true, Owner: json.RawMessage(`null`)}
		p.desired, p.revID = operatorConfigPointingAt(t, r.provider.URL), "rev-stale"
	})
	if err := r.d.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Traffic: the data plane unloaded, xray gone.
	got := r.fw.snapshot()
	for _, want := range firewall.RevertCommands(r.spec) {
		if !containsCmd(got, want) {
			t.Errorf("the release did not run %q; ran:\n  %s", want, strings.Join(got, "\n  "))
		}
	}
	if r.d.supStarted || r.d.sup.Status().State != supervisor.StateStopped {
		t.Errorf("xray not stopped: started=%v state=%s", r.d.supStarted, r.d.sup.Status().State)
	}
	waitUntil(t, "the old xray to exit", func() bool { return !alive(xrayPID) })

	// The owner's things: gone, on disk and in memory.
	for _, f := range r.ownerFiles() {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("%s survived the release: %v", filepath.Base(f), err)
		}
	}
	onDisk, err := state.Load(r.d.cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]state.PersistedState{"memory": r.d.st, "state.json": onDisk} {
		if s.AppliedRevisionID != "" || s.ConfigDigest != "" || s.SpliceKey != "" || s.LastDesiredRevision != nil ||
			s.ClaimOwner != nil || s.Rescue != (state.RescueSnapshot{}) {
			t.Errorf("%s still has the owner's state: %+v", name, s)
		}
		// ...and the router's own things stay.
		if s.RouterID != st0.RouterID || s.AgentToken != st0.AgentToken || s.DeviceIdentifier != st0.DeviceIdentifier ||
			s.DevicePrivateKey != st0.DevicePrivateKey || s.ClaimKey == nil || s.BotUsername != "VectraBot" {
			t.Errorf("%s lost the router's own state: %+v", name, s)
		}
	}
	if r.d.desired != nil || r.d.applier.Tproxy != nil || r.d.nodeCount != 0 || r.d.probe != nil {
		t.Error("the daemon still holds the owner's config in memory")
	}
	if len(r.panel.resultsFor("apply-rev-stale")) != 0 {
		t.Error("a job in the releasing answer was run")
	}

	// A new claim: unlinked, nobody's, a code the previous owner never saw.
	view := r.d.liveRuntime().Claim
	if view == nil || view.State != "unclaimed" || view.Owner != nil || view.Code == r.codeSeen || view.QR == "" {
		t.Fatalf("claim after the release = %+v (the code before linking was %s)", view, r.codeSeen)
	}

	// One line, naming what went and never the subscription.
	lines := logs.matching("released this router")
	if len(lines) != 1 {
		t.Fatalf("%d release lines:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	for _, want := range []string{"the data plane", "xray (stopped)", "the operator config", "the provider config",
		"the rendered xray config", "the locations cache", "the choices made on the router", "the applied revision", "the owner"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the release line does not say %q was removed: %s", want, lines[0])
		}
	}
	if strings.Contains(lines[0], r.provider.URL) || strings.Contains(lines[0], "127.0.0.1") {
		t.Errorf("the release line names the subscription: %s", lines[0])
	}

	// The panel keeps saying released until the router is claimed again: once
	// wiped there is no owner, and the router does nothing more — a choice
	// made on it since survives, the code stays, nothing is torn down again.
	r.panel.next(func(p *releasePanel) { p.desired = nil })
	if _, err := localctl.UpdateOverrides(r.d.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.ProbeIntervalSec = 120
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ran := len(r.fw.snapshot())
	for i := 0; i < 2; i++ {
		if err := r.d.runOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(r.d.cfg.OverridesPath); err != nil || len(r.fw.snapshot()) != ran ||
		len(logs.matching("released this router")) != 1 || r.d.liveRuntime().Claim.Code != view.Code {
		t.Fatalf("a second released answer acted: overrides %v, %d more firewall commands, code %s -> %s",
			err, len(r.fw.snapshot())-ran, view.Code, r.d.liveRuntime().Claim.Code)
	}
	// ...and the check-ins announce the new code.
	if a := r.panel.lastClaim(t); a == nil || a.CodeHash != claim.CodeHash(view.Code) {
		t.Fatalf("check-in claim after the release = %+v", a)
	}
}

// A router nobody ever claimed — the fleet — never wipes itself, whatever the
// panel sends: it runs on, and the rest of the answer is acted on as always.
func TestARouterThatNeverHadAnOwnerIgnoresReleased(t *testing.T) {
	r := newReleaseRouter(t, false)
	logs := captureLog(t)
	r.panel.next(func(p *releasePanel) {
		p.info = controlplane.ClaimInfo{Released: true, Owner: json.RawMessage(`null`)}
		p.desired, p.revID = operatorConfigPointingAt(t, r.provider.URL), "rev-1"
	})
	for i := 0; i < 2; i++ {
		if err := r.d.runOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range r.ownerFiles() {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("a fleet router lost %s: %v", filepath.Base(f), err)
		}
	}
	if got := r.fw.snapshot(); len(got) != 0 {
		t.Errorf("a fleet router unloaded its data plane:\n  %s", strings.Join(got, "\n  "))
	}
	// (The apply job re-renders under the choices made on the router, so xray
	// may have been restarted onto it; it must be running.)
	waitUntil(t, "xray running", func() bool { return r.d.sup.Status().State == supervisor.StateRunning })
	if !r.d.supStarted || r.d.desired == nil || r.d.st.AppliedRevisionID != "rev-1" || r.d.st.ConfigDigest == "" {
		t.Errorf("a fleet router changed: started=%v desired=%v applied %q", r.d.supStarted, r.d.desired != nil, r.d.st.AppliedRevisionID)
	}
	if len(r.panel.resultsFor("apply-rev-1")) == 0 {
		t.Error("the jobs of a released answer were not run on a fleet router")
	}
	if n := len(logs.matching("released this router")); n != 0 {
		t.Errorf("%d release lines on a fleet router", n)
	}
}

// Released, the router takes the next owner's config from scratch: the
// revision applies, xray runs again under a supervisor that restarts it.
func TestAReleasedRouterTakesTheNextOwnersConfig(t *testing.T) {
	r := newReleaseRouter(t, true)
	r.panel.next(func(p *releasePanel) { p.info = controlplane.ClaimInfo{Released: true, Owner: json.RawMessage(`null`)} })
	if err := r.d.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.panel.next(func(p *releasePanel) {
		p.info = controlplane.ClaimInfo{Owner: json.RawMessage(`{"label":"Пётр"}`)}
		p.desired, p.revID = operatorConfigPointingAt(t, r.provider.URL), "rev-2"
	})
	if err := r.d.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	res := r.panel.resultsFor("apply-rev-2")
	if len(res) == 0 || res[len(res)-1].Status != "success" || r.d.st.AppliedRevisionID != "rev-2" || r.d.st.ClaimOwner == nil {
		t.Fatalf("the next owner's config did not apply: %+v, state %+v", res, r.d.st)
	}
	for _, f := range []string{r.d.cfg.XrayConfigPath, r.d.cfg.ProviderConfigPath, r.d.cfg.XrayRenderPath} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("no %s after the next owner's apply: %v", filepath.Base(f), err)
		}
	}
	waitUntil(t, "xray running again", func() bool { return r.d.sup.Status().State == supervisor.StateRunning })
	first := r.d.sup.Status().PID
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "xray restarted after a crash", func() bool {
		s := r.d.sup.Status()
		return s.State == supervisor.StateRunning && s.PID != first
	})
}

// A register answer can release too (a router that lost its token).
func TestARegisterAnswerCanRelease(t *testing.T) {
	dir := t.TempDir()
	panel := newReleasePanel(t)
	d := newClaimDaemon(t, dir, panel.URL)
	d.st.ClaimOwner = &controlplane.ClaimOwner{Label: "Иван П."}
	d.claim.setOwner(d.st.ClaimOwner)
	panel.next(func(p *releasePanel) {
		p.regInfo = controlplane.ClaimInfo{Released: true, Owner: json.RawMessage(`null`)}
	})
	if err := d.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.st.RouterID != "r-rel" || d.st.ClaimOwner != nil {
		t.Fatalf("after a releasing register: %+v", d.st)
	}
	if v := d.liveRuntime().Claim; v == nil || v.State != "unclaimed" {
		t.Fatalf("claim = %+v", v)
	}
}

// Traffic first: the data plane is unloaded while xray still runs — with the
// kill switch armed, xray gone under a loaded ruleset is a LAN with no
// internet at all — and only then is xray stopped.
func TestAReleaseUnloadsTheDataPlaneBeforeStoppingXray(t *testing.T) {
	r := newReleaseRouter(t, true)
	xrayPID := r.d.sup.Status().PID
	var mu sync.Mutex
	var seen, aliveAtFirst bool
	var stateAtFirst supervisor.State
	inner := r.fw.run
	r.d.runFirewallCmd = func(ctx context.Context, name string, args ...string) error {
		mu.Lock()
		if !seen {
			seen, aliveAtFirst, stateAtFirst = true, alive(xrayPID), r.d.sup.Status().State
		}
		mu.Unlock()
		return inner(ctx, name, args...)
	}
	r.panel.next(func(p *releasePanel) {
		p.info = controlplane.ClaimInfo{Released: true, Owner: json.RawMessage(`null`)}
	})
	if err := r.d.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("no firewall command ran: the data plane was not unloaded")
	}
	if !aliveAtFirst || stateAtFirst != supervisor.StateRunning {
		t.Fatalf("xray was already down (alive=%v, %s) when the data plane was unloaded", aliveAtFirst, stateAtFirst)
	}
	if r.d.sup.Status().State != supervisor.StateStopped {
		t.Fatal("xray was not stopped after the release")
	}
}

// "released" in the same answer that first names an owner: the router had no
// owner before this answer, so it must not wipe itself.
func TestReleasedWithTheFirstOwnerInTheSameAnswerDoesNotWipe(t *testing.T) {
	r := newReleaseRouter(t, false)
	r.panel.next(func(p *releasePanel) {
		p.info = controlplane.ClaimInfo{Released: true, Owner: json.RawMessage(`{"label":"X"}`)}
	})
	if err := r.d.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.d.desired == nil || r.d.st.AppliedRevisionID == "" || len(r.fw.snapshot()) != 0 {
		t.Fatal("wiped a router that had no owner before this answer")
	}
}

// The tunnel's word was the previous owner's: a release forgets the verdict
// held for Connect, so a claim right after it starts from unknown.
func TestAReleaseForgetsTheHeldConnectVerdict(t *testing.T) {
	r := newReleaseRouter(t, true)
	owner := ""
	if r.d.st.ClaimOwner != nil {
		owner = r.d.st.ClaimOwner.OwnerRef
	}
	old := connectGather
	t.Cleanup(func() { connectGather = old })
	// The check-in before the release judges and holds a verdict.
	connectGather = func(context.Context, *daemon) uiapi.Inputs {
		return uiapi.Inputs{Now: time.Now(), Runtime: &localctl.Runtime{}}
	}
	r.d.connectHeld = connectJudged{verdict: "ok", at: time.Now(), owner: owner}
	r.panel.next(func(p *releasePanel) {
		p.info = controlplane.ClaimInfo{Released: true, Owner: json.RawMessage(`null`)}
	})
	if err := r.d.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.d.connectHeld != (connectJudged{}) {
		t.Fatalf("the release kept the previous owner's verdict: %+v", r.d.connectHeld)
	}
}
