package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/retire"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uiapi"
)

// retireRouter is a router for the daemon's side of PassWall2's retirement:
// PassWall2 installed as a fleet router has it (its package, its init
// script, its configuration, the takeover's breadcrumbs), Vectra switched on
// for good and carrying the traffic, an opkg that removes what it is asked
// to. What the daemon asks of the kernel is answered by its seams.
type retireRouter struct {
	t    *testing.T
	d    *daemon
	env  retire.Env
	dir  string
	opkg [][]string
	// refuse: packages the fake opkg will not remove.
	refuse map[string]bool
	logs   bytes.Buffer
	// what the kernel says: vctl's table, its policy rule and route.
	table, rule, route bool
	running            bool
}

const retireStatus = `Package: luci-app-passwall2
Depends: libc, tcping, geoview, v2ray-geoip, v2ray-geosite
Status: install user installed

Package: luci-i18n-passwall2-ru
Depends: luci-app-passwall2
Status: install user installed

Package: geoview
Status: install ok installed

Package: tcping
Status: install ok installed

Package: v2ray-geoip
Status: install ok installed

Package: v2ray-geosite
Status: install ok installed

Package: xray-core
Status: install user installed

Package: dnsmasq-full
Provides: dnsmasq
Status: install user installed
`

func newRetireRouter(t *testing.T) *retireRouter {
	t.Helper()
	dir := t.TempDir()
	r := &retireRouter{t: t, dir: dir, refuse: map[string]bool{}, table: true, rule: true, route: true, running: true}
	r.env = retire.Env{
		StatusFile:   filepath.Join(dir, "opkg", "status"),
		InfoDir:      filepath.Join(dir, "opkg", "info"),
		PassWall:     []string{filepath.Join(dir, "init.d", "passwall2"), filepath.Join(dir, "init.d", "passwall")},
		MarkerDir:    filepath.Join(dir, "etc"),
		TrialMarkers: filepath.Join(dir, "vectra-trial.d"),
		Snippet:      filepath.Join(dir, "uci-defaults", "99-vectra-trial-passwall-switch"),
		BackupDir:    filepath.Join(dir, "etc", "backup"),
		Configs:      []string{filepath.Join(dir, "config", "passwall2")},
	}
	r.write(r.env.StatusFile, retireStatus, 0o644)
	for _, p := range []string{"luci-app-passwall2", "luci-i18n-passwall2-ru", "geoview", "tcping", "v2ray-geoip", "v2ray-geosite"} {
		r.write(filepath.Join(r.env.InfoDir, p+".control"), "Package: "+p+"\n", 0o644)
	}
	r.write(r.env.PassWall[0], "#!/bin/sh /etc/rc.common\n", 0o755)
	r.write(r.env.Configs[0], "config global\n\toption enabled '0'\n", 0o600)
	r.write(filepath.Join(r.env.MarkerDir, ".passwall-disabled-by-vctl"), "", 0o644)
	r.write(filepath.Join(r.env.MarkerDir, ".passwall-switch-off-by-vctl"), "", 0o644)

	// Vectra on for good: its UCI switch and its boot link.
	penv := power.Env{
		Config:       filepath.Join(dir, "config", "vectra-controller-pro"),
		RCDir:        filepath.Join(dir, "rc.d"),
		Init:         filepath.Join(dir, "init.d", "vectra-controller-pro"),
		MarkerDir:    r.env.MarkerDir,
		PassWall:     r.env.PassWall,
		Agent:        filepath.Join(dir, "init.d", "vectra-controller"),
		ProcDir:      filepath.Join(dir, "proc"),
		Lock:         filepath.Join(dir, "vectra-power.lock"),
		Trial:        filepath.Join(dir, "vectra-trial.json"),
		TrialMarkers: r.env.TrialMarkers,
		Snippet:      r.env.Snippet,
	}
	r.write(penv.Config, "config controller 'main'\n\toption enabled '1'\n", 0o600)
	r.write(filepath.Join(penv.RCDir, "S95vectra-controller-pro"), "", 0o755)
	if err := os.MkdirAll(penv.ProcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg, _ := agentcfg.Parse([]byte(`{"controlUrl":"unused"}`))
	cfg.ProviderConfigPath = filepath.Join(dir, "etc", "provider-config.json")
	cfg.XrayRenderPath = filepath.Join(dir, "run", "xray.json")
	r.write(passwallDocumentPath(cfg.ProviderConfigPath), `{"outbounds":[]}`, 0o600)
	r.d = &daemon{
		cfg:     cfg,
		desired: &config.Config{Inbounds: config.Inbounds{Tproxy: &config.TproxyInbound{Port: 12345, FwMark: 1}}},
		tableLoaded: func(context.Context, string) bool {
			return r.table
		},
		ipOutput: func(_ context.Context, args ...string) ([]byte, error) {
			switch strings.Join(args, " ") {
			case "rule show":
				if r.rule {
					return []byte("0:\tfrom all lookup local\n100:\tfrom all fwmark 0x1 lookup 100\n32766:\tfrom all lookup main\n"), nil
				}
				return []byte("0:\tfrom all lookup local\n32766:\tfrom all lookup main\n"), nil
			case "route show table 100":
				if r.route {
					return []byte("local default dev lo scope host \n"), nil
				}
				return nil, nil
			}
			return nil, errors.New("unexpected ip " + strings.Join(args, " "))
		},
		xrayRunning: func() bool { return r.running },
	}

	prevEnv, prevOpkg, prevPower, prevLog := retireEnv, retireOpkg, powerEnv, logging.L()
	retireEnv = func() retire.Env { return r.env }
	retireOpkg = r.fakeOpkg
	powerEnv = func() power.Env { return penv }
	logging.SetDefault(logging.New("info", &r.logs, "text"))
	t.Cleanup(func() { retireEnv, retireOpkg, powerEnv = prevEnv, prevOpkg, prevPower; logging.SetDefault(prevLog) })
	return r
}

func (r *retireRouter) write(path, body string, mode os.FileMode) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		r.t.Fatal(err)
	}
}

// fakeOpkg removes what it is asked to, as opkg does, but for r.refuse.
func (r *retireRouter) fakeOpkg(_ context.Context, args ...string) ([]byte, error) {
	r.opkg = append(r.opkg, args)
	raw, _ := os.ReadFile(r.env.StatusFile)
	var kept []string
	failed := false
	for _, stanza := range strings.Split(string(raw), "\n\n") {
		name := strings.TrimPrefix(strings.SplitN(strings.TrimSpace(stanza), "\n", 2)[0], "Package: ")
		gone := false
		for _, a := range args[1:] {
			if a == name && !r.refuse[name] {
				gone = true
			}
			if a == name && r.refuse[name] {
				failed = true
			}
		}
		if gone {
			_ = os.Remove(filepath.Join(r.env.InfoDir, name+".control"))
			if name == retire.App {
				_ = os.Remove(r.env.PassWall[0])
			}
			continue
		}
		kept = append(kept, strings.TrimSpace(stanza))
	}
	r.write(r.env.StatusFile, strings.Join(kept, "\n\n")+"\n", 0o644)
	if failed {
		return []byte("Package luci-app-passwall2 is depended upon by packages: luci-theme-x"), errors.New("exit status 255")
	}
	return []byte("Removing package ..."), nil
}

var r0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

// The loop: the first look that finds Vectra carrying the traffic starts the
// window, and says when PassWall2 goes; looks inside it do nothing; the
// first look past it removes PassWall2 — its document of the passwall route
// source (credentials, a cache now) with it.
func TestTheLoopRetiresPassWallAfterTheWindow(t *testing.T) {
	r := newRetireRouter(t)
	ctx := context.Background()
	r.d.maybeRetirePassWall(ctx, r0)
	if since, ok := r.env.Since(); !ok || !since.Equal(r0) {
		t.Fatalf("the window did not start: %v %v", since, ok)
	}
	if !strings.Contains(r.logs.String(), "PassWall2 goes from this router") {
		t.Fatalf("the start of the window is not said:\n%s", r.logs.String())
	}
	for _, at := range []time.Duration{time.Minute, 6 * time.Minute, 12 * time.Hour, 23*time.Hour + 59*time.Minute} {
		r.d.maybeRetirePassWall(ctx, r0.Add(at))
	}
	if len(r.opkg) != 0 {
		t.Fatalf("opkg ran inside the window: %v", r.opkg)
	}
	r.d.maybeRetirePassWall(ctx, r0.Add(24*time.Hour+6*time.Minute))
	if len(r.opkg) != 2 {
		t.Fatalf("opkg %v, want PassWall2 and then its helpers", r.opkg)
	}
	if s, _ := r.env.State(); s != "retired" {
		t.Fatalf("state %s", s)
	}
	if fileExists(passwallDocumentPath(r.d.cfg.ProviderConfigPath)) {
		t.Fatal("passwall-config.json left: the provider route source does not read it")
	}
	if !strings.Contains(r.logs.String(), "PassWall2 retired") {
		t.Fatalf("no line says it:\n%s", r.logs.String())
	}
	// Nothing left to do: later looks run nothing.
	r.d.maybeRetirePassWall(ctx, r0.Add(48*time.Hour))
	if len(r.opkg) != 2 {
		t.Fatalf("opkg ran again: %v", r.opkg)
	}
}

// The loop looks every few minutes, not every poll.
func TestTheLoopLooksEveryFewMinutes(t *testing.T) {
	r := newRetireRouter(t)
	looks := 0
	r.d.tableLoaded = func(context.Context, string) bool { looks++; return true }
	for i := 0; i < 10; i++ {
		r.d.maybeRetirePassWall(context.Background(), r0.Add(time.Duration(i)*45*time.Second))
	}
	if looks != 1 {
		t.Fatalf("vctl's table asked %d times in 7.5 minutes of 45 s polls", looks)
	}
}

// `vctl retire-passwall --now` skips the window, and only the window.
func TestNowSkipsTheWindowOnly(t *testing.T) {
	r := newRetireRouter(t)
	resp := r.d.retirePassWall(context.Background(), r.env, r0, true)
	if !resp.OK || resp.Code != "passwall_retired" || len(r.opkg) != 2 {
		t.Fatalf("--now: %+v, opkg %v", resp, r.opkg)
	}
	if !strings.Contains(resp.Detail, "vectra off") {
		t.Fatalf("the answer does not say what `vectra off` does now: %s", resp.Detail)
	}
}

// Without --now, before the window: it says when.
func TestWithoutNowItSaysWhen(t *testing.T) {
	r := newRetireRouter(t)
	resp := r.d.retirePassWall(context.Background(), r.env, r0, false)
	if resp.OK || resp.Code != "waiting" || !strings.Contains(resp.Detail, "2026-10-01") {
		t.Fatalf("%+v", resp)
	}
	if len(r.opkg) != 0 {
		t.Fatalf("opkg ran: %v", r.opkg)
	}
}

// Every other condition refuses, --now or not; nothing changes.
func TestTheConditionsRefuse(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(r *retireRouter)
		want string
	}{
		{"disabled", func(r *retireRouter) { r.d.cfg.NoRetirePassWall = true }, "retire_passwall"},
		{"trial", func(r *retireRouter) { r.write(powerEnv().Trial, `{"id":"x"}`, 0o644) }, "trial"},
		{"off", func(r *retireRouter) { _ = os.Remove(filepath.Join(powerEnv().RCDir, "S95vectra-controller-pro")) }, "vectra on"},
		{"route source passwall", func(r *retireRouter) { r.d.cfg.RouteSource = routeSourcePassWall }, "route_source 'passwall'"},
		{"no data plane", func(r *retireRouter) { r.table = false }, "data plane"},
		{"no policy rule", func(r *retireRouter) { r.rule = false }, "fwmark 0x1"},
		{"xray down", func(r *retireRouter) { r.running = false }, "xray"},
		{"direct", func(r *retireRouter) { r.d.st.Rescue.Mode = string(rescue.ModeDirect) }, "direct"},
		{"no operator config", func(r *retireRouter) { r.d.desired = nil }, "operator config"},
		{"low memory", func(r *retireRouter) {
			r.d.retireResources = func() controlplane.RouterResources {
				return controlplane.RouterResources{MemoryAvailableMB: 20, OverlayFreeMB: 30, TMPFreeMB: 60}
			}
		}, "memory 20MB"},
		{"PassWall runs", func(r *retireRouter) {
			r.write(filepath.Join(powerEnv().ProcDir, "4100", "cmdline"), "/tmp/etc/passwall2/bin/xray\x00run\x00", 0o644)
		}, "PassWall2 runs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRetireRouter(t)
			tc.set(r)
			resp := r.d.retirePassWall(context.Background(), r.env, r0, true)
			if resp.OK || resp.Code != "refused" || !strings.Contains(resp.Detail, tc.want) {
				t.Fatalf("%+v, want a refusal naming %q", resp, tc.want)
			}
			if len(r.opkg) != 0 || !r.env.Present() {
				t.Fatalf("something changed: opkg %v", r.opkg)
			}
		})
	}
}

// Native routing keeps PassWall2's document: it is its own render's.
func TestNativeRoutingKeepsItsDocument(t *testing.T) {
	r := newRetireRouter(t)
	r.d.cfg.RouteSource = routeSourceNative
	prevStore, prevGeo := nativeStorePath, nativeGeoDir
	nativeStorePath, nativeGeoDir = filepath.Join(r.dir, "config", "vectra_route"), filepath.Join(r.dir, "route-geo")
	t.Cleanup(func() { nativeStorePath, nativeGeoDir = prevStore, prevGeo })
	if resp := r.d.retirePassWall(context.Background(), r.env, r0, true); resp.OK || !strings.Contains(resp.Detail, "vectra_route") {
		t.Fatalf("native without its store: %+v", resp)
	}
	r.write(nativeStorePath, "config global\n", 0o600)
	if resp := r.d.retirePassWall(context.Background(), r.env, r0, true); resp.OK || !strings.Contains(resp.Detail, "geo files") {
		t.Fatalf("native without its own geo files: %+v", resp)
	}
	r.write(filepath.Join(nativeGeoDir, "geoip.dat"), "x", 0o644)
	r.write(filepath.Join(nativeGeoDir, "geosite.dat"), "x", 0o644)
	if resp := r.d.retirePassWall(context.Background(), r.env, r0, true); !resp.OK {
		t.Fatalf("native with its store and geo files: %+v", resp)
	}
	if !fileExists(passwallDocumentPath(r.d.cfg.ProviderConfigPath)) {
		t.Fatal("native routing's own document went")
	}
}

// A removal that failed: PassWall2 stays, and the loop does not try again
// for hours.
func TestAFailedRemovalIsNotRetriedAtOnce(t *testing.T) {
	r := newRetireRouter(t)
	r.refuse[retire.App] = true
	if _, err := r.env.StartClock(r0.Add(-25 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	r.d.maybeRetirePassWall(context.Background(), r0)
	if len(r.opkg) != 1 || !r.env.Present() {
		t.Fatalf("opkg %v, present %v", r.opkg, r.env.Present())
	}
	if !strings.Contains(r.logs.String(), "stays as the way back") {
		t.Fatalf("the failure is not said:\n%s", r.logs.String())
	}
	for _, at := range []time.Duration{6 * time.Minute, time.Hour, 5 * time.Hour} {
		r.d.maybeRetirePassWall(context.Background(), r0.Add(at))
	}
	if len(r.opkg) != 1 {
		t.Fatalf("tried again within hours: %v", r.opkg)
	}
	r.d.maybeRetirePassWall(context.Background(), r0.Add(6*time.Hour+time.Minute))
	if len(r.opkg) != 2 {
		t.Fatalf("never tried again: %v", r.opkg)
	}
}

// PassWall2 gone: the loop's look is a few file stats; a retirement cut
// short is finished — nothing owed any more.
func TestTheLoopFinishesARetirementCutShort(t *testing.T) {
	r := newRetireRouter(t)
	_ = os.Remove(r.env.PassWall[0])
	_ = os.Remove(filepath.Join(r.env.InfoDir, retire.App+".control"))
	r.write(filepath.Join(r.env.MarkerDir, retire.RecordName), "at=2026-09-30T11:00:00Z\nremoved=luci-app-passwall2\n", 0o644)
	looks := 0
	r.d.tableLoaded = func(context.Context, string) bool { looks++; return true }
	r.d.maybeRetirePassWall(context.Background(), r0)
	if fileExists(filepath.Join(r.env.MarkerDir, ".passwall-disabled-by-vctl")) || fileExists(filepath.Join(r.env.MarkerDir, ".passwall-switch-off-by-vctl")) {
		t.Fatal("a breadcrumb says PassWall2 is owed back")
	}
	if looks != 0 || len(r.opkg) != 0 {
		t.Fatalf("PassWall2 gone, yet the kernel was asked %d times, opkg %v", looks, r.opkg)
	}
}

// What "carries the traffic" is: an operator config, not the rescue's
// direct mode, xray running, the table loaded, its policy rule and route in
// the kernel. ip that cannot answer does not stop it.
func TestRetireCarryingIsTheWholeDataPlane(t *testing.T) {
	r := newRetireRouter(t)
	if err := r.d.retireCarrying(context.Background()); err != nil {
		t.Fatalf("all there: %v", err)
	}
	r.route = false
	if err := r.d.retireCarrying(context.Background()); err == nil || !strings.Contains(err.Error(), "table 100") {
		t.Fatalf("no local route: %v", err)
	}
	r.d.ipOutput = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("no ip") }
	if err := r.d.retireCarrying(context.Background()); err != nil {
		t.Fatalf("ip cannot answer: %v", err)
	}
}

// busybox's ip and ip-full say the same rule a little differently.
func TestThePolicyRuleIsReadFromEitherIP(t *testing.T) {
	for _, out := range []string{
		"100:\tfrom all fwmark 0x1 lookup 100\n",
		"100: from all fwmark 0x1/0xff lookup 100 \n",
		"100:\tfrom all fwmark 0x00000001 table 100\n",
	} {
		if !hasFwmarkRule(out, 1, 100) {
			t.Errorf("not found in %q", out)
		}
	}
	for _, out := range []string{"100:\tfrom all fwmark 0x10 lookup 100\n", "100:\tfrom all fwmark 0x1 lookup 1000\n", ""} {
		if hasFwmarkRule(out, 1, 100) {
			t.Errorf("found in %q", out)
		}
	}
	if !hasLocalDefault("local default dev lo scope host\n") || !hasLocalDefault("local 0.0.0.0/0 dev lo\n") || hasLocalDefault("unreachable default\n") {
		t.Error("the local route is misread")
	}
}

// `vctl retire-passwall` reaches the loop through the UI socket, --now
// included, and waits for the answer.
func TestTheRetireRequestRunsOnTheLoop(t *testing.T) {
	r := newRetireRouter(t)
	dir, err := os.MkdirTemp("/tmp", "vrt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	r.d.cfg.UISocketPath = filepath.Join(dir, "ui.sock")
	r.d.uiReqs = make(chan uiRequest, 1)
	r.d.claim = newClaimer(state.PersistedState{}, "", time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.d.serveUI(ctx)
	go r.d.waitForTick(ctx, make(chan time.Time))
	var resp localctl.SocketResponse
	for i := 0; i < 200; i++ {
		cctx, ccancel := context.WithTimeout(ctx, 5*time.Second)
		resp, err = localctl.Call(cctx, r.d.cfg.UISocketPath, localctl.SocketRequest{Op: localctl.OpRetirePassWall, Now: true})
		ccancel()
		if !errors.Is(err, localctl.ErrDaemonDown) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || !resp.OK || resp.Code != "passwall_retired" {
		t.Fatalf("%+v %v", resp, err)
	}
}

// `ubus call vectra status` says what became of PassWall2, from the files
// the retirement keeps.
func TestStatusSaysPassWallRetired(t *testing.T) {
	r := newRetireRouter(t)
	fakeGather(t)
	fakeUILock(t, "0", nil)
	status := func() uiapi.Legacy {
		st, ok := rpcdCall(context.Background(), powerCfg(t), "status", nil).(uiapi.Status)
		if !ok {
			t.Fatal("status did not answer")
		}
		return st.Legacy
	}
	if l := status(); l.Passwall == nil || *l.Passwall != "installed" || l.PasswallRetiredAt != nil {
		t.Fatalf("before: %+v", l)
	}
	if resp := r.d.retirePassWall(context.Background(), r.env, r0, true); !resp.OK {
		t.Fatalf("%+v", resp)
	}
	if l := status(); l.Passwall == nil || *l.Passwall != "retired" || l.PasswallRetiredAt == nil || *l.PasswallRetiredAt != "2026-09-30T12:00:00Z" {
		t.Fatalf("after: %+v", l)
	}
}
