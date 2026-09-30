package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/uiapi"
)

// The operator's lock (UCI ui_lock) is enforced by the router, not by the UI
// hiding a tab: under it `vctl rpcd` refuses the Pro methods before it reads
// anything or asks the daemon anything.

// fakeUILock stands in for `uci get vectra-controller-pro.main.ui_lock`, and
// points the file uci would read at one only this test controls (absent until
// the test writes it).
func fakeUILock(t *testing.T, value string, err error) {
	t.Helper()
	prev, prevFile := rpcdReadUILock, rpcdConfigFile
	rpcdReadUILock = func(context.Context) (string, error) { return value, err }
	rpcdConfigFile = filepath.Join(t.TempDir(), "vectra-controller-pro")
	t.Cleanup(func() { rpcdReadUILock, rpcdConfigFile = prev, prevFile })
}

// fakeGather records what the answers would have read; nothing reads the
// router — Vectra's switch is on, as on every router these tests stand for.
func fakeGather(t *testing.T) *[]string {
	t.Helper()
	var calls []string
	prevG, prevL, prevP := rpcdGather, rpcdGatherLogs, rpcdPower
	rpcdGather = func(_ context.Context, _ uiapi.Env, need uiapi.Need) uiapi.Inputs {
		calls = append(calls, fmt.Sprintf("gather %+v", need))
		return uiapi.Inputs{Now: time.Now()}
	}
	rpcdGatherLogs = func(context.Context, string, int) uiapi.Logs {
		calls = append(calls, "logs")
		return uiapi.Logs{Lines: []uiapi.LogLine{}}
	}
	rpcdPower = func(context.Context, bool, bool) power.Facts {
		return power.Facts{UCI: true, Boot: true, Running: true, Carrying: true}
	}
	t.Cleanup(func() { rpcdGather, rpcdGatherLogs, rpcdPower = prevG, prevL, prevP })
	return &calls
}

// lockStand is a router for rpcd: a daemon answering the UI socket, and an
// "xray API" port, both counting what reached them.
type lockStand struct {
	cfg     agentcfg.Config
	mu      sync.Mutex
	daemon  []localctl.SocketRequest
	apiHits atomic.Int32
}

func (s *lockStand) daemonRequests() []localctl.SocketRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]localctl.SocketRequest(nil), s.daemon...)
}

func newLockStand(t *testing.T) *lockStand {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "vlk") // a unix socket path must stay short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cfg, err := agentcfg.Parse([]byte(`{"controlUrl":"https://api.vectra-pro.net"}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg.UISocketPath = filepath.Join(dir, "ui.sock")
	cfg.XrayRenderPath = filepath.Join(dir, "xray.json")
	cfg.OverridesPath = filepath.Join(dir, "local-overrides.json")
	cfg.EntriesPath = filepath.Join(dir, "provider-entries.json.gz")
	cfg.EntriesIndexPath = filepath.Join(dir, "provider-entries-index.json")
	cfg.XrayConfigPath = filepath.Join(dir, "xray-desired.json") // no operator config: no panel lock
	cfg.StatePath = filepath.Join(dir, "state.json")
	s := &lockStand{cfg: cfg}

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
			s.apiHits.Add(1)
			c.Close()
		}
	}()
	render := fmt.Sprintf(`{"api":{"listen":%q},"outbounds":[`+
		`{"tag":"node-a","protocol":"vless","settings":{"vnext":[{"address":"10.44.0.2","port":2001}]}},`+
		`{"tag":"node-b","protocol":"vless","settings":{"vnext":[{"address":"10.44.0.2","port":2002}]}}],`+
		`"routing":{"rules":[{"balancerTag":"BL-MAIN","network":"tcp,udp"}],"balancers":[{"tag":"BL-MAIN","selector":["node-"]}]}}`,
		ln.Addr().String())
	if err := os.WriteFile(cfg.XrayRenderPath, []byte(render), 0o600); err != nil {
		t.Fatal(err)
	}
	idx, _ := json.Marshal(localctl.EntriesIndex{FetchedAt: time.Now().UTC(), Entries: []localctl.EntrySummary{
		{Index: 0, Remark: "🇷🇺🇪🇺 Авто"}, {Index: 1, Remark: "🇩🇪 Германия"},
	}})
	if err := os.WriteFile(cfg.EntriesIndexPath, idx, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = localctl.Serve(ctx, cfg.UISocketPath, func(_ context.Context, req localctl.SocketRequest) localctl.SocketResponse {
			s.mu.Lock()
			s.daemon = append(s.daemon, req)
			s.mu.Unlock()
			return localctl.SocketResponse{OK: true}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	for i := 0; ; i++ {
		if fi, err := os.Stat(cfg.UISocketPath); err == nil && fi.Mode()&os.ModeSocket != 0 {
			break
		}
		if i > 200 {
			t.Fatal("the fake daemon never listened")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return s
}

var lockedAnswer = `{"ok":false,"code":"locked","detail":null}`

// proCalls are the Pro-only methods with the params the UI sends them.
var proCalls = []struct{ method, params string }{
	{"balancers", ""},
	{"nodes", ""},
	{"logs", `{"lines":10}`},
	{"pin_balancer", `{"balancer":"BL-MAIN","node":"node-b"}`},
	{"set_probe_interval", `{"seconds":600}`},
}

func TestTheLockRefusesProMethodsBeforeAnyWork(t *testing.T) {
	s := newLockStand(t)
	calls := fakeGather(t)
	fakeUILock(t, "1\n", nil)
	for _, c := range proCalls {
		out, _ := json.Marshal(rpcdCall(context.Background(), s.cfg, c.method, []byte(c.params)))
		if string(out) != lockedAnswer {
			t.Errorf("locked %s = %s, want %s", c.method, out, lockedAnswer)
		}
	}
	if len(*calls) > 0 {
		t.Errorf("gathered under the lock: %v", *calls)
	}
	if n := len(s.daemonRequests()); n > 0 {
		t.Errorf("the daemon was asked %d time(s) under the lock", n)
	}
	if n := s.apiHits.Load(); n > 0 {
		t.Errorf("xray's API was dialled %d time(s) under the lock", n)
	}
	if _, err := os.Stat(s.cfg.OverridesPath); !os.IsNotExist(err) {
		t.Errorf("a pin reached the overrides under the lock (%v)", err)
	}

	// Anti-vacuity: unlocked, the same calls do reach everything counted above.
	fakeUILock(t, "0\n", nil)
	for _, c := range proCalls {
		out, _ := json.Marshal(rpcdCall(context.Background(), s.cfg, c.method, []byte(c.params)))
		if strings.Contains(string(out), `"locked"`) {
			t.Errorf("unlocked %s = %s", c.method, out)
		}
	}
	if len(*calls) != 3 || len(s.daemonRequests()) != 1 || s.apiHits.Load() == 0 {
		t.Fatalf("unlocked: gathered %v, daemon asked %d time(s), API dialled %d time(s) — the fakes are not where the work is",
			*calls, len(s.daemonRequests()), s.apiHits.Load())
	}
}

func TestTheLockKeepsTheSimpleView(t *testing.T) {
	s := newLockStand(t)
	calls := fakeGather(t)
	fakeUILock(t, "on", nil)
	ctx := context.Background()

	st, ok := rpcdCall(ctx, s.cfg, "status", nil).(uiapi.Status)
	if !ok || !st.UI.Locked {
		t.Fatalf("status under the lock = %+v", st)
	}
	if _, ok := rpcdCall(ctx, s.cfg, "entries", nil).(uiapi.Entries); !ok {
		t.Error("entries is not served under the lock")
	}
	if _, ok := rpcdCall(ctx, s.cfg, "diagnostics", nil).(uiapi.Diagnostics); !ok {
		t.Error("diagnostics is not served under the lock")
	}
	if len(*calls) != 3 {
		t.Errorf("gathered %v", *calls)
	}

	for _, c := range []struct{ method, params, code string }{
		{"select_entry", `{"index":1}`, "entry_selected"},
		{"reset_entry", "", "entry_reset"},
		{"restart_xray", "", "xray_restarted"},
		// No xray behind the API port here: released on disk, and said so.
		{"unpin_balancer", `{"balancer":"BL-MAIN"}`, "balancer_unpinned"},
	} {
		a, ok := rpcdCall(ctx, s.cfg, c.method, []byte(c.params)).(uiapi.Action)
		if !ok || !a.OK || a.Code != c.code {
			t.Errorf("%s under the lock = %+v, want ok %s", c.method, a, c.code)
		}
	}
	reqs := s.daemonRequests()
	if len(reqs) != 3 || reqs[0].Change == nil || reqs[0].Change.SetEntry == nil || reqs[0].Change.SetEntry.Remark != "🇩🇪 Германия" ||
		reqs[2].Op != localctl.OpRestartXray {
		t.Errorf("the daemon was asked %+v", reqs)
	}
	if s.apiHits.Load() == 0 {
		t.Error("unpin_balancer did not ask xray to release the balancer")
	}

	// My sites is the simple view's own: read and changed under the lock.
	if r, ok := rpcdCall(ctx, s.cfg, "rules", nil).(uiapi.Rules); !ok || r.Max != 100 || r.Direct == nil {
		t.Errorf("rules under the lock = %+v", r)
	}
	a, ok := rpcdCall(ctx, s.cfg, "set_rules", []byte(`{"direct":["https://www.Sberbank.ru/x"],"proxy":[]}`)).(uiapi.Action)
	if !ok || !a.OK || a.Code != "rules_set" {
		t.Errorf("set_rules under the lock = %+v", a)
	}
	if reqs = s.daemonRequests(); len(reqs) != 4 || reqs[3].Op != localctl.OpReapply || reqs[3].Change == nil ||
		reqs[3].Change.SetRules == nil || strings.Join(reqs[3].Change.SetRules.Direct, ",") != "sberbank.ru" {
		t.Errorf("set_rules asked the daemon %+v", reqs)
	}
}

// The lock is read at every call: an operator's commit applies to the next
// one. Only 1/true/yes/on lock; uci failing — no option, no uci — does not.
func TestTheLockIsWhatUCISaysAtThisCall(t *testing.T) {
	s := newLockStand(t)
	fakeGather(t)
	for _, tc := range []struct {
		value  string
		err    error
		locked bool
	}{
		{"1\n", nil, true}, {"true", nil, true}, {"TRUE\n", nil, true}, {"yes", nil, true}, {"Yes", nil, true},
		{"on", nil, true}, {" ON \n", nil, true},
		{"0\n", nil, false}, {"", nil, false}, {"false", nil, false}, {"no", nil, false}, {"off", nil, false},
		{"2", nil, false}, {"locked", nil, false}, {"yes please", nil, false},
		{"", errUCIAbsent, false}, // uci: no such option
	} {
		fakeUILock(t, tc.value, tc.err)
		// uci answered: the file is not what decides (it would say locked).
		if err := os.WriteFile(rpcdConfigFile, []byte("config controller 'main'\n\toption ui_lock '1'\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		st := rpcdCall(context.Background(), s.cfg, "status", nil).(uiapi.Status)
		out, _ := json.Marshal(rpcdCall(context.Background(), s.cfg, "nodes", nil))
		if st.UI.Locked != tc.locked || (string(out) == lockedAnswer) != tc.locked {
			t.Errorf("uci %q (err %v): status.ui.locked=%v, nodes=%s; want locked=%v", tc.value, tc.err, st.UI.Locked, out, tc.locked)
		}
	}
}

// uci did not answer — it timed out, was killed, is missing, or failed on the
// file: the file uci reads decides, by uci's own quoting rules. When the file
// cannot say either, the router stays LOCKED.
func TestWhenUCIDoesNotAnswerTheFileDecidesOrTheRouterStaysLocked(t *testing.T) {
	const top = "# Operator lock, for customer routers.\nconfig controller 'main'\n\toption enabled '1'\n"
	for _, tc := range []struct {
		name   string
		file   *string
		locked bool
	}{
		{"the file says 1", ptrTo(top + "\toption ui_lock '1'\n"), true},
		{"the file says 0", ptrTo(top + "\toption ui_lock '0'\n"), false},
		{"double quotes", ptrTo(top + "\toption ui_lock \"on\"\n"), true},
		{"bare word and a comment", ptrTo(top + "\toption ui_lock yes # locked for a customer\n"), true},
		{"quoted name, parts joined", ptrTo(top + "\toption 'ui_lock' 'o'\"n\"\n"), true},
		{"the last one counts", ptrTo(top + "\toption ui_lock '1'\n\toption ui_lock '0'\n"), false},
		{"no such option", ptrTo(top), false},
		{"only another section has it", ptrTo(top + "config controller 'other'\n\toption ui_lock '1'\n"), false},
		{"a value over two lines", ptrTo(top + "\toption ui_lock \"o\\\nn\"\n"), true},
		{"a comment that looks like the option", ptrTo(top + "#\toption ui_lock '1'\n"), false},
		{"no file", nil, true},
		{"an unterminated quote", ptrTo(top + "\toption ui_lock '1\n"), true},
		{"not uci", ptrTo(top + "\tui_lock = 1\n"), true},
	} {
		for _, failure := range []error{errors.New("signal: killed"), errors.New(`exec: "uci": executable file not found in $PATH`),
			errors.New("uci get: exit status 1: uci: Parse error (invalid command) at line 4, byte 1")} {
			fakeUILock(t, "", failure)
			if tc.file != nil {
				if err := os.WriteFile(rpcdConfigFile, []byte(*tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := uiLocked(context.Background()); got != tc.locked {
				t.Errorf("%s, uci %v: locked=%v, want %v", tc.name, failure, got, tc.locked)
			}
		}
	}
}

func ptrTo(s string) *string { return &s }

// What uci's answer is, read from what it prints and how it exits — with a
// stand-in `uci` on PATH, never the real one.
func TestUCIsAnswerIsReadForWhatItIs(t *testing.T) {
	bin := t.TempDir()
	fake := func(script string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, "uci"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+":/bin:/usr/bin")
	prev := rpcdUCITimeout
	t.Cleanup(func() { rpcdUCITimeout = prev })
	ctx := context.Background()

	// A freshly written executable can take a while to start the first time
	// (macOS scans it): the answers get all the time they need.
	rpcdUCITimeout = 10 * time.Second
	fake(`[ "$*" = "get vectra-controller-pro.main.ui_lock" ] || exit 9; echo 1`)
	if v, err := readUCIUILock(ctx); err != nil || v != "1\n" {
		t.Errorf("an answer: %q %v", v, err)
	}
	fake(`echo "uci: Entry not found" >&2; exit 1`)
	if _, err := readUCIUILock(ctx); !errors.Is(err, errUCIAbsent) {
		t.Errorf("no such option: %v, want errUCIAbsent", err)
	}
	rpcdUCITimeout = time.Second
	for name, script := range map[string]string{
		"a parse error":         `echo "uci: Parse error (invalid command) at line 3, byte 1" >&2; exit 1`,
		"exit 1, no message":    `exit 1`,
		"another exit code":     `echo "uci: I/O error" >&2; exit 5`,
		"a uci that hangs":      `exec sleep 5`,
		"output with an exit 1": `echo 1; echo "uci: Entry not found" >&2; exit 1`,
	} {
		fake(script)
		start := time.Now()
		if _, err := readUCIUILock(ctx); err == nil || errors.Is(err, errUCIAbsent) {
			t.Errorf("%s: %v, want a failure (not an answer, not absent)", name, err)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s: took %s; the timeout is %s", name, d, rpcdUCITimeout)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := readUCIUILock(ctx); err == nil || errors.Is(err, errUCIAbsent) {
		t.Errorf("no uci at all: %v, want a failure", err)
	}
}

// Every method is either Pro-only or served under the lock, on purpose — a new
// method must be placed — and the contract says which.
func TestEveryMethodIsPlacedUnderTheLock(t *testing.T) {
	simple := map[string]bool{"status": true, "entries": true, "diagnostics": true, "select_entry": true,
		"reset_entry": true, "restart_xray": true, "unpin_balancer": true, "rules": true, "set_rules": true,
		"set_power": true, "services": true, "set_service": true}
	for m := range rpcdSetupSignatures { // the setup wizard is the simple view
		simple[m] = true
	}
	for m := range rpcdSignatures {
		if rpcdProOnly[m] == simple[m] {
			t.Errorf("method %s: Pro-only=%v, served under the lock=%v; it must be exactly one", m, rpcdProOnly[m], simple[m])
		}
	}
	for m := range rpcdProOnly {
		if _, ok := rpcdSignatures[m]; !ok {
			t.Errorf("Pro-only method %s does not exist", m)
		}
	}
	readme := string(readContract(t, "README.md"))
	section := strings.SplitN(strings.SplitN(readme, "## Operator lock", 2)[1], "\n## ", 2)[0]
	for m := range rpcdSignatures {
		if !strings.Contains(section, "`"+m+"`") {
			t.Errorf("the contract's Operator lock section does not place %s", m)
		}
	}
}

// Before the daemon ever rendered its config, rpcd reads the router's
// defaults — its files, and the panel's address wan_check asks — never an
// empty config.
func TestRPCDConfigBeforeTheDaemonStarted(t *testing.T) {
	if _, err := os.Stat(rpcdAgentConfig); err == nil {
		t.Skip("this machine has a daemon config")
	}
	c := rpcdConfig()
	if c.ControlURL != "https://api.vectra-pro.net" || c.UISocketPath != localctl.DefaultSocketPath || c.StatePath == "" {
		t.Fatalf("rpcd config = %+v", c)
	}
}
