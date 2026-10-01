package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/uiapi"
)

// The setup wizard's half of the contract: fixtures, codes, and each method
// against a fake router — rpcd never runs the real uci, ubus or wifi here,
// and never spawns anything: the Wi-Fi helper is faked.

func TestSetupFixturesRoundTripThroughTheGoTypes(t *testing.T) {
	for name, target := range map[string]interface{}{"setup.json": &uiapi.Setup{}, "wan_check.json": &uiapi.WanCheck{},
		"wifi_scan.json": &uiapi.WifiScan{}} {
		raw := readContract(t, name)
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(target); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		back, _ := json.Marshal(target)
		if !reflect.DeepEqual(generic(t, raw), generic(t, back)) {
			t.Fatalf("%s re-encoded differently:\nfixture: %s\ngo:      %s", name, compactJSON(t, raw), back)
		}
	}
}

// Every code the wizard's methods answer with is in the contract.
func TestEveryCodeTheSetupWizardEmitsIsInTheContract(t *testing.T) {
	readme := string(readContract(t, "README.md"))
	src, err := os.ReadFile("rpcd_setup.go")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	// action(ok, "code", …), and rpcdWifiChange(ctx, setup.WifiRequest{…}, "code").
	for _, m := range regexp.MustCompile(`(?:action\((?:true|false), |WifiRequest\{[^}]*\}, )"([a-z_]+)"`).FindAllStringSubmatch(string(src), -1) {
		codes[m[1]] = true
	}
	for _, want := range []string{"invalid_params", "internal", "busy", "unsupported", "apply_failed", "wifi_set", "wifi_optimized", "setup_finished"} {
		if !codes[want] {
			t.Errorf("the scan did not find %q: it is not looking where the codes are", want)
		}
	}
	for c := range codes {
		if !strings.Contains(readme, "`"+c+"`") {
			t.Errorf("code %q is not in the contract", c)
		}
	}
}

// wizardRouter is the fake router behind rpcd: config files in a temp dir,
// every command recorded with its stdin, the Wi-Fi helper faked.
type wizardRouter struct {
	cfg    agentcfg.Config
	env    setup.Env
	mu     sync.Mutex
	cmds   []string        // uci's private save directory cut out: "uci set …"
	dirs   map[string]bool // the save directories uci was given (uci -t)
	stdins []string
	fail   func(cmd string) bool
	onRun  func(cmd string)
	// ubus answers other than the WAN's status, by their arguments without
	// "-t N"; absent: ubus fails, as it does with no netifd or no radio.
	ubus  map[string]string
	asked []string

	// The helper: each spawn's arguments, whether it was handed the Wi-Fi
	// lock, and the word it got (true: go). spawnErr fails the spawn,
	// startErr the word. holdLock keeps the helper's copy of the lock open, as
	// the real one does while it restarts and checks the Wi-Fi, until
	// releaseLock.
	spawns   [][]string
	handed   []bool
	words    []bool
	spawnErr error
	startErr error
	holdLock bool
	held     []*os.File
}

func newWizardRouter(t *testing.T) *wizardRouter {
	t.Helper()
	dir := t.TempDir()
	run := filepath.Join(dir, "run")
	w := &wizardRouter{dirs: map[string]bool{}}
	w.env = setup.Env{
		NetworkConfig: filepath.Join(dir, "network"), WirelessConfig: filepath.Join(dir, "wireless"),
		VectraConfig: filepath.Join(dir, "vectra-controller-pro"), Shadow: filepath.Join(dir, "shadow"),
		SysClassNet: filepath.Join(dir, "sys"), Now: time.Now, Sleep: func(time.Duration) {},
		WifiLock: filepath.Join(dir, "lock", "vectra-wifi.lock"), RunDir: run, WifiApply: filepath.Join(run, "wifi-apply.json"),
		WifiJob: filepath.Join(run, "wifi-job.json"), WifiScan: filepath.Join(run, "wifi-scan.json"), UCISaveDir: filepath.Join(dir, "uci"),
		Run: func(_ context.Context, stdin io.Reader, name string, args ...string) error {
			in := ""
			if stdin != nil {
				b, _ := io.ReadAll(stdin)
				in = string(b)
			}
			w.mu.Lock()
			if name == "uci" && len(args) > 2 && args[0] == "-t" {
				w.dirs[args[1]] = true
				args = args[2:]
			}
			cmd := strings.Join(append([]string{name}, args...), " ")
			w.cmds = append(w.cmds, cmd)
			w.stdins = append(w.stdins, in)
			fail, hook := w.fail, w.onRun
			w.mu.Unlock()
			if hook != nil {
				hook(cmd)
			}
			if fail != nil && fail(cmd) {
				return errors.New("exit status 1")
			}
			return nil
		},
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if len(args) > 2 && args[0] == "-t" {
				args = args[2:]
			}
			key := strings.Join(args, " ")
			if key == "call network.interface.wan status" {
				return []byte(`{"device":"wan","ipv4-address":[{"address":"100.64.12.7"}],"route":[{"target":"0.0.0.0","mask":0,"nexthop":"100.64.12.1"}],"dns-server":["100.64.12.1"]}`), nil
			}
			w.mu.Lock()
			defer w.mu.Unlock()
			if name != "ubus" {
				key = name + " " + key
			}
			w.asked = append(w.asked, key)
			if out, ok := w.ubus[key]; ok && name == "ubus" {
				return []byte(out), nil
			}
			return nil, errors.New("Command failed: Not found")
		},
	}
	cfg, err := agentcfg.Parse([]byte(`{"controlUrl":"https://api.vectra-pro.net"}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg.StatePath, cfg.XrayConfigPath = filepath.Join(dir, "state.json"), filepath.Join(dir, "xray-desired.json")
	w.cfg = cfg

	prevEnv, prevSpawn := rpcdSetupEnv, rpcdSpawn
	rpcdSetupEnv = func() setup.Env { return w.env }
	rpcdSpawn = func(lock *os.File, args ...string) (func(bool) error, error) {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.spawns = append(w.spawns, args)
		w.handed = append(w.handed, setup.HoldsWifiLock(w.env, lock))
		if w.spawnErr != nil {
			return nil, w.spawnErr
		}
		if w.holdLock {
			fd, err := syscall.Dup(int(lock.Fd()))
			if err != nil {
				return nil, err
			}
			w.held = append(w.held, os.NewFile(uintptr(fd), "vectra-wifi.lock"))
		}
		return func(proceed bool) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.words = append(w.words, proceed)
			if proceed {
				return w.startErr
			}
			return nil
		}, nil
	}
	t.Cleanup(func() {
		rpcdSetupEnv, rpcdSpawn = prevEnv, prevSpawn
		w.releaseLock()
	})
	fakeUILock(t, "0", nil)
	return w
}

// releaseLock is the helper exiting: its copy of the Wi-Fi lock closes.
func (w *wizardRouter) releaseLock() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, f := range w.held {
		f.Close()
	}
	w.held = nil
}

func (w *wizardRouter) commands() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return strings.Join(w.cmds, "\n")
}

// forget clears what was recorded, for the next call.
func (w *wizardRouter) forget() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cmds, w.stdins, w.asked, w.spawns, w.handed, w.words = nil, nil, nil, nil, nil, nil
	w.dirs = map[string]bool{}
}

// noneStaged fails when a save directory outlived the call.
func (w *wizardRouter) noneStaged(t *testing.T) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	for d := range w.dirs {
		if filepath.Dir(d) != w.env.RunDir {
			t.Errorf("a save directory outside the run directory: %s", d)
		}
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("the save directory %s outlived the call", d)
		}
	}
}

func (w *wizardRouter) write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (w *wizardRouter) call(method, params string) interface{} {
	return rpcdCall(context.Background(), w.cfg, method, []byte(params))
}

// withRuntime makes the daemon answer the runtime rt (nil: the daemon is down).
func withRuntime(t *testing.T, rt *localctl.Runtime) {
	t.Helper()
	prev := rpcdGather
	rpcdGather = func(context.Context, uiapi.Env, uiapi.Need) uiapi.Inputs { return uiapi.Inputs{Runtime: rt} }
	t.Cleanup(func() { rpcdGather = prev })
}

func TestSetupAnswersWhatTheRouterHas(t *testing.T) {
	w := newWizardRouter(t)
	w.write(t, w.env.NetworkConfig, "config interface 'wan'\n\toption proto 'pppoe'\n\toption username 'ivanov'\n\toption password 'isp-secret'\n")
	w.write(t, w.env.WirelessConfig, "config wifi-device 'radio0'\n\toption band '2g'\n\toption disabled '1'\n"+
		"config wifi-iface\n\toption device 'radio0'\n\toption mode 'ap'\n\toption ssid 'OpenWrt'\n\toption encryption 'none'\n"+
		"\toption key 'wifi-secret-never-shown'\n")
	w.write(t, filepath.Join(w.env.SysClassNet, "wan", "carrier"), "1\n")
	w.write(t, filepath.Join(w.env.SysClassNet, "wan", "address"), "a4:39:b3:12:3f:2a\n")
	w.write(t, w.cfg.StatePath, `{"device_identifier":"vectra-1","bot_username":"VectraBot","agent_token":"tok-secret"}`)
	// Out of the box: no root password, the LAN at OpenWrt's address.
	w.write(t, w.env.Shadow, "root::0:0:99999:7:::\n")
	w.ubus = map[string]string{"call network.interface.lan status": `{"up":true,"device":"br-lan","ipv4-address":[{"address":"192.168.1.1","mask":24}]}`}
	exp := time.Date(2026, 9, 28, 7, 12, 0, 0, time.UTC)
	withRuntime(t, &localctl.Runtime{Claim: &localctl.Claim{State: "unclaimed", Code: "7ZKNPGS6", QR: "VECTRA:R1:AAAA", ExpiresAt: exp}})

	st, ok := w.call("setup", "").(uiapi.Setup)
	if !ok {
		t.Fatal("setup did not answer a Setup")
	}
	b, _ := json.Marshal(st)
	for _, secret := range []string{"ivanov", "isp-secret", "tok-secret", "wifi-secret-never-shown"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("%s reached the answer: %s", secret, b)
		}
	}
	if st.PasswordSet == nil || *st.PasswordSet || st.Lan.IPv4 == nil || *st.Lan.IPv4 != "192.168.1.1" {
		t.Fatalf("password and LAN = %s", b)
	}
	if st.Done || st.Wan.Proto != "pppoe" || *st.Wan.IPv4 != "100.64.12.7" || !*st.Wan.Link || len(st.Wifi.Radios) != 1 ||
		*st.Wifi.Radios[0].SSID != "OpenWrt" || st.Wifi.Radios[0].Secured || st.Wifi.Radios[0].Enabled || !st.Wifi.Radios[0].Auto ||
		st.Wifi.Radios[0].Channel != nil || !st.Wifi.Radios[0].MaxPower || st.Wifi.Radios[0].Width != nil || !st.Wifi.Radios[0].AP ||
		st.Wifi.Radios[0].Mesh || st.Wifi.Radios[0].Up != nil || !st.Wifi.Tunable || st.Wifi.Tuned == nil || !*st.Wifi.Tuned || st.Wifi.Apply != nil ||
		*st.Wifi.Suggested != "Vectra-3F2A" || st.Vectra.Linked || *st.Vectra.BotUsername != "VectraBot" || st.Vectra.Owner != nil ||
		st.Wifi.Verdict == nil || *st.Wifi.Verdict != "fine" {
		t.Fatalf("setup = %s", b)
	}
	c := st.Vectra.Claim
	if c == nil || c.State != "unclaimed" || c.Code != "7ZKNPGS6" || *c.QR != "VECTRA:R1:AAAA" || c.ExpiresAt != "2026-09-28T07:12:00Z" ||
		*c.BotURL != "https://t.me/VectraBot?start=rt_7ZKNPGS6" || c.Owner != nil {
		t.Fatalf("claim = %s", b)
	}
	var shape []string
	sameShape("", generic(t, readContract(t, "setup.json")), generic(t, b), &shape)
	if len(shape) > 0 {
		t.Fatalf("the answer differs from the fixture's shape:\n  %s", strings.Join(shape, "\n  "))
	}

	// Linked: no claim, the bot still named (support), and whose it is — the
	// owner the daemon kept. The password set: only that it is, never its hash.
	w.write(t, w.cfg.StatePath, `{"device_identifier":"vectra-1","bot_username":"VectraBot","agent_token":"tok-secret","claim_owner":{"label":"Иван П."}}`)
	w.write(t, w.cfg.XrayConfigPath, string(mustOperatorConfig(t, false)))
	w.write(t, w.env.Shadow, "root:$6$salt$shadow-hash-never-shown:0:0:99999:7:::\n")
	st = w.call("setup", "").(uiapi.Setup)
	if b, _ := json.Marshal(st.Vectra); !st.Vectra.Linked || st.Vectra.Claim != nil || *st.Vectra.BotUsername != "VectraBot" ||
		st.Vectra.Owner == nil || st.Vectra.Owner.Label != "Иван П." || strings.Contains(string(b), "tok-secret") {
		t.Fatalf("linked setup = %s", b)
	}
	if b, _ := json.Marshal(st); st.PasswordSet == nil || !*st.PasswordSet || strings.Contains(string(b), "shadow-hash") || strings.Contains(string(b), "$6$") {
		t.Fatalf("a password set = %s", b)
	}
	// Neither known: null, never a guess.
	os.Remove(w.env.Shadow)
	w.ubus = nil
	if st := w.call("setup", "").(uiapi.Setup); st.PasswordSet != nil || st.Lan.IPv4 != nil {
		t.Fatalf("unknown password and LAN = %+v, %+v", st.PasswordSet, st.Lan)
	}
	// The daemon down: no claim to show, and no guess; the owner is still known.
	os.Remove(w.cfg.XrayConfigPath)
	withRuntime(t, nil)
	if st := w.call("setup", "").(uiapi.Setup); st.Vectra.Linked || st.Vectra.Claim != nil || st.Vectra.Owner == nil {
		t.Fatalf("daemon down = %+v", st.Vectra)
	}
}

// Support: the bot the panel named, else the one the box was prepared with
// (UCI support_bot), else none. The claim's link goes only to the panel's
// bot: a code means something only there.
func TestSupportGoesToThePanelsBotElseTheBoxs(t *testing.T) {
	w := newWizardRouter(t)
	exp := time.Date(2026, 9, 28, 7, 12, 0, 0, time.UTC)
	withRuntime(t, &localctl.Runtime{Claim: &localctl.Claim{State: "unclaimed", Code: "7ZKNPGS6", ExpiresAt: exp}})
	answer := func() (bot, botURL *string) {
		st := w.call("setup", "").(uiapi.Setup)
		return st.Vectra.BotUsername, st.Vectra.Claim.BotURL
	}
	str := func(p *string) string {
		if p == nil {
			return "<null>"
		}
		return *p
	}
	for _, tc := range []struct {
		name, panel, uci, bot, url string
	}{
		{"a box never online, prepared with a bot", "", "VectraHelpBot", "VectraHelpBot", "<null>"},
		{"the panel's bot wins", "VectraBot", "VectraHelpBot", "VectraBot", "https://t.me/VectraBot?start=rt_7ZKNPGS6"},
		{"the panel's bot alone", "VectraBot", "", "VectraBot", "https://t.me/VectraBot?start=rt_7ZKNPGS6"},
		{"neither", "", "", "<null>", "<null>"},
		{"an invalid support bot is ignored", "", "@Vectra-Help", "<null>", "<null>"},
	} {
		state := `{"device_identifier":"vectra-1"}`
		if tc.panel != "" {
			state = `{"device_identifier":"vectra-1","bot_username":"` + tc.panel + `"}`
		}
		w.write(t, w.cfg.StatePath, state)
		w.write(t, w.env.VectraConfig, "config controller 'main'\n\toption support_bot '"+tc.uci+"'\n")
		if bot, url := answer(); str(bot) != tc.bot || str(url) != tc.url {
			t.Errorf("%s: botUsername %s, botUrl %s; want %s, %s", tc.name, str(bot), str(url), tc.bot, tc.url)
		}
	}
}

func mustOperatorConfig(t *testing.T, lock bool) []byte {
	t.Helper()
	raw := operatorExample(t)
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if lock {
		doc["ui"] = map[string]interface{}{"lock": true}
	}
	out, _ := json.Marshal(doc)
	return out
}

func TestWanCheckAnswersWithinItsBudget(t *testing.T) {
	w := newWizardRouter(t)
	probe := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) { rw.WriteHeader(http.StatusNoContent) }))
	defer probe.Close()
	prev := rpcdChecker
	rpcdChecker = func(agentcfg.Config) setup.Checker {
		return setup.Checker{PanelURL: probe.URL, ProbeURL: probe.URL + "/generate_204", ProbeHost: "127.0.0.1", Budget: 3 * time.Second}
	}
	t.Cleanup(func() { rpcdChecker = prev })
	start := time.Now()
	c, ok := w.call("wan_check", "").(uiapi.WanCheck)
	if !ok || !c.DNS || !c.Internet || !c.Panel || c.IPv4 == nil || *c.IPv4 != "100.64.12.7" || c.CheckedAt == "" ||
		time.Since(start) > setup.CheckBudget {
		t.Fatalf("wan_check = %+v after %s", c, time.Since(start))
	}
}

// A router's Wi-Fi for the wizard's methods: 2.4 GHz off and open, 5 GHz on
// and secured, both still on their own rules.
const wizardWireless = "config wifi-device 'radio0'\n\toption band '2g'\n\toption channel 'auto'\n\toption htmode 'HE20'\n" +
	"\toption txpower '17'\n\toption country 'US'\n\toption disabled '1'\n" +
	"config wifi-device 'radio1'\n\toption band '5g'\n\toption channel '36'\n\toption htmode 'HE80'\n" +
	"config wifi-iface 'ap0'\n\toption device 'radio0'\n\toption mode 'ap'\n\toption ssid 'OpenWrt'\n\toption encryption 'none'\n" +
	"config wifi-iface 'ap1'\n\toption device 'radio1'\n\toption mode 'ap'\n\toption ssid 'Home-5G'\n\toption encryption 'psk2'\n" +
	"\toption key 'old-5g-key'\n"

// The last change's restart, as a helper left it.
const lastApply = `{"state":"ok","at":"2026-09-28T06:00:00Z","radios":{"radio1":true}}`

// set_wifi, per radio: the radios left out are not touched; a secured
// network keeps its key and whether it is on; an open one given a key gets
// psk2 and is switched on, last. The steps go into a private save directory,
// the commit comes with the restart's state set to "applying", and the
// helper — handed the Wi-Fi lock — gets the word.
func TestSetWifiNamesEachRadioAndHandsTheRestartOver(t *testing.T) {
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, wizardWireless)
	for _, bad := range []string{
		`{"ssid":"home","key":"12345678"}`, // the old form
		`{"radios":{}}`,
		`{"radios":{"radio0":{"ssid":"home"}}}`, // open: needs a key
		`{"radios":{"radio0":{"ssid":"home","key":"short"}}}`,
		`{"radios":{"radio9":{"ssid":"home","key":"12345678"}}}`,
		`{"radios":{"radio1":{"ssid":"home","password":"12345678"}}}`,
		`{"channels":{"radio0":6}}`,
		`[]`,
	} {
		if a := w.call("set_wifi", bad).(uiapi.Action); a.OK || a.Code != "invalid_params" || (a.Detail != nil && strings.Contains(*a.Detail, "short")) {
			t.Errorf("set_wifi %s = %+v", bad, a)
		}
	}
	if w.commands() != "" || len(w.spawns) != 0 {
		t.Fatal("an invalid set_wifi changed something")
	}

	a := w.call("set_wifi", `{"radios":{"radio1":{"ssid":"Дом-5"}}}`).(uiapi.Action)
	if !a.OK || a.Code != "wifi_set" || a.Detail != nil {
		t.Fatalf("set_wifi = %+v", a)
	}
	if got := w.commands(); got != "uci set wireless.ap1.ssid=Дом-5\nuci commit wireless" {
		t.Fatalf("a secured network renamed:\n%s", got)
	}
	if len(w.dirs) != 1 || !reflect.DeepEqual(w.spawns, [][]string{{"setup", "wifi-apply"}}) ||
		!reflect.DeepEqual(w.handed, []bool{true}) || !reflect.DeepEqual(w.words, []bool{true}) {
		t.Fatalf("save directories %v, spawns %q, handed the lock %v, words %v", w.dirs, w.spawns, w.handed, w.words)
	}
	w.noneStaged(t)
	if a := setup.LoadApply(w.env); a == nil || a.State != setup.ApplyUnverified {
		// The fake helper has already gone: "applying" with nobody checking.
		t.Fatalf("apply = %+v", a)
	}

	w.forget()
	a = w.call("set_wifi", `{"radios":{"radio0":{"ssid":"Home","key":"k3y-k3y-k3y"}}}`).(uiapi.Action)
	want := "uci set wireless.ap0.ssid=Home\nuci set wireless.ap0.encryption=psk2\nuci set wireless.ap0.key=k3y-k3y-k3y\n" +
		"uci -q delete wireless.ap0.disabled\nuci set wireless.radio0.disabled=0\nuci commit wireless"
	if got := w.commands(); !a.OK || a.Code != "wifi_set" || got != want {
		t.Fatalf("an open network given a key = %+v:\n%s", a, got)
	}
}

// The answer comes once the change is committed and the restart's state says
// "applying" — a setup read right after never shows the last change's result
// — while the helper, holding the lock, restarts and checks the Wi-Fi: until
// it is done, another change is refused as busy.
func TestAWifiChangeAnswersWithTheRestartApplying(t *testing.T) {
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, wizardWireless)
	w.write(t, w.env.WifiApply, lastApply)
	w.holdLock = true
	since := time.Now().Add(-time.Second)
	if a := w.call("set_wifi", `{"radios":{"radio1":{"ssid":"Home"}}}`).(uiapi.Action); !a.OK {
		t.Fatalf("set_wifi = %+v", a)
	}
	raw, _ := os.ReadFile(w.env.WifiApply)
	var file map[string]interface{}
	if err := json.Unmarshal(raw, &file); err != nil || file["state"] != "applying" || !reflect.DeepEqual(file["radios"], map[string]interface{}{}) {
		t.Fatalf("the apply file after the answer: %s", raw)
	}
	if at, err := time.Parse(time.RFC3339, file["at"].(string)); err != nil || at.Before(since) {
		t.Fatalf("a stale at: %s", raw)
	}
	st := w.call("setup", "").(uiapi.Setup)
	if ap := st.Wifi.Apply; ap == nil || ap.State != "applying" || ap.Detail != nil || len(ap.Radios) != 0 {
		t.Fatalf("setup.wifi.apply = %+v", ap)
	}
	b, _ := json.Marshal(st)
	var shape []string
	sameShape("", generic(t, readContract(t, "setup.json")), generic(t, b), &shape)
	if len(shape) > 0 {
		t.Fatalf("the answer differs from the fixture's shape:\n  %s", strings.Join(shape, "\n  "))
	}

	n := len(w.cmds)
	for m, params := range map[string]string{"set_wifi": `{"radios":{"radio1":{"ssid":"Home2"}}}`, "optimize_wifi": `{}`} {
		if a := w.call(m, params).(uiapi.Action); a.OK || a.Code != "busy" || len(w.cmds) != n || len(w.spawns) != 1 {
			t.Errorf("%s while a change is applied = %+v", m, a)
		}
	}
	for _, q := range w.asked {
		if strings.Contains(q, "scan") {
			t.Fatalf("a busy optimize_wifi scanned: %q", q)
		}
	}

	// The helper gone without a verdict: the check stopped.
	w.releaseLock()
	if ap := w.call("setup", "").(uiapi.Setup).Wifi.Apply; ap == nil || ap.State != "unverified" || ap.Detail == nil ||
		*ap.Detail != "the check stopped before it finished" {
		t.Fatalf("setup.wifi.apply after the helper = %+v", ap)
	}
	if a := w.call("set_wifi", `{"radios":{"radio1":{"ssid":"Home2"}}}`).(uiapi.Action); !a.OK {
		t.Fatalf("set_wifi once the helper is done = %+v", a)
	}
}

// A restart that cannot be started is a failure, never a success with no
// restart: when the helper cannot be spawned, nothing is committed — the
// staged steps are dropped — and the last restart's state stays; when it
// cannot be given the word after the commit, the wireless file and the state
// are put back. A change pending in uci is refused, and a failed commit lets
// the helper go without a word.
func TestAWifiChangeWhoseRestartCannotStartIsTakenBack(t *testing.T) {
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, wizardWireless)
	w.write(t, w.env.WifiApply, lastApply)
	unchanged := func(what string) {
		t.Helper()
		if raw, _ := os.ReadFile(w.env.WifiApply); string(raw) != lastApply {
			t.Errorf("%s: the apply file became %s", what, raw)
		}
		if raw, _ := os.ReadFile(w.env.WirelessConfig); string(raw) != wizardWireless {
			t.Errorf("%s: the wireless file became:\n%s", what, raw)
		}
		if _, err := os.Stat(w.env.WifiJob); !os.IsNotExist(err) {
			t.Errorf("%s: the job (it holds the keys) was left behind", what)
		}
		w.noneStaged(t)
		if l, err := setup.LockWifi(w.env); err != nil {
			t.Errorf("%s: the lock is still held: %v", what, err)
		} else {
			l.Close()
		}
	}

	w.spawnErr = errors.New("fork/exec /usr/bin/vctl: cannot allocate memory")
	for m, params := range map[string]string{
		"set_wifi":      `{"radios":{"radio0":{"ssid":"Home","key":"k3y-k3y-k3y"}}}`,
		"optimize_wifi": `{"channels":{"radio0":6,"radio1":36},"radios":{"radio0":{"ssid":"Home","key":"k3y-k3y-k3y"}}}`,
	} {
		w.forget()
		a := w.call(m, params).(uiapi.Action)
		if a.OK || a.Code != "apply_failed" || a.Detail == nil || *a.Detail != "the Wi-Fi restart could not be started, so nothing was changed: "+
			"fork/exec /usr/bin/vctl: cannot allocate memory" || len(w.words) != 0 {
			t.Errorf("%s with no helper = %+v, words %v", m, a, w.words)
		}
		if got := w.commands(); !strings.Contains(got, "uci set wireless.ap0.key=k3y-k3y-k3y") || strings.Contains(got, "commit") {
			t.Errorf("%s with no helper: commands\n%s", m, got)
		}
		unchanged(m + " with no helper")
	}

	w.spawnErr, w.startErr = nil, errors.New("write |1: broken pipe")
	w.onRun = func(cmd string) {
		if cmd == "uci commit wireless" {
			_ = os.WriteFile(w.env.WirelessConfig, []byte(strings.Replace(wizardWireless, "'OpenWrt'", "'Home'", 1)), 0o600)
		}
	}
	w.forget()
	a := w.call("set_wifi", `{"radios":{"radio0":{"ssid":"Home","key":"k3y-k3y-k3y"}}}`).(uiapi.Action)
	if a.OK || a.Code != "apply_failed" || a.Detail == nil || *a.Detail != "the Wi-Fi restart did not start, so the change was taken back: write |1: broken pipe" ||
		!reflect.DeepEqual(w.words, []bool{true}) || !strings.HasSuffix(w.commands(), "uci commit wireless") {
		t.Errorf("a helper that never got the word = %+v, words %v", a, w.words)
	}
	unchanged("a helper that never got the word")

	// A change someone left uncommitted in uci's own save directory would be
	// committed with this one: busy, nothing staged.
	w.startErr, w.onRun = nil, nil
	w.write(t, filepath.Join(w.env.UCISaveDir, "wireless"), "wireless.ap1.ssid='Pending'\n")
	w.forget()
	a = w.call("set_wifi", `{"radios":{"radio1":{"ssid":"Home"}}}`).(uiapi.Action)
	if a.OK || a.Code != "busy" || a.Detail == nil || !strings.HasPrefix(*a.Detail, "uncommitted Wi-Fi changes are waiting in uci") ||
		w.commands() != "" || len(w.spawns) != 0 {
		t.Errorf("a change pending in uci = %+v, commands %q", a, w.cmds)
	}
	unchanged("a change pending in uci")
	os.Remove(filepath.Join(w.env.UCISaveDir, "wireless"))

	w.fail = func(cmd string) bool { return cmd == "uci commit wireless" }
	w.forget()
	a = w.call("set_wifi", `{"radios":{"radio1":{"ssid":"Home"}}}`).(uiapi.Action)
	if a.OK || a.Code != "internal" || !reflect.DeepEqual(w.words, []bool{false}) {
		t.Errorf("a failed commit = %+v, words %v", a, w.words)
	}
	unchanged("a failed commit")
}

// optimize_wifi: every radio tuned, the networks it is given named, in one
// transaction and one restart; an open network it is not given a key for is
// never switched on — the answer says so.
func TestOptimizeWifiTunesAndNamesInOneRestart(t *testing.T) {
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, wizardWireless)
	for _, bad := range []string{
		`{"channels":{"radio1":100}}`,
		`{"channels":{"radio1":165}}`,
		`{"channels":{"radio0":13}}`,
		`{"channels":{"radio0":"11"}}`,
		`{"channels":{"radio0":6.5}}`,
		`{"channels":{"radio0":6},"radios":{"radio0":{"ssid":"Home"}}}`,
		`{"tune":true}`,
	} {
		if a := w.call("optimize_wifi", bad).(uiapi.Action); a.OK || a.Code != "invalid_params" {
			t.Errorf("optimize_wifi %s = %+v", bad, a)
		}
	}
	if w.commands() != "" || len(w.spawns) != 0 {
		t.Fatal("an invalid optimize_wifi changed something")
	}

	a := w.call("optimize_wifi", `{"channels":{"radio0":11,"radio1":149},"radios":{"radio0":{"ssid":"Vectra-6D39","key":"k3y-k3y-k3y"},"radio1":{"ssid":"Vectra-6D39"}}}`).(uiapi.Action)
	want := "uci set wireless.radio0.country=PA\nuci -q delete wireless.radio0.txpower\nuci set wireless.radio0.channel=11\n" +
		"uci -q delete wireless.ap0.vif_txpower\n" +
		"uci set wireless.radio1.country=PA\nuci -q delete wireless.radio1.txpower\nuci set wireless.radio1.channel=149\n" +
		"uci -q delete wireless.ap1.vif_txpower\n" +
		"uci set wireless.ap0.ssid=Vectra-6D39\nuci set wireless.ap0.encryption=psk2\nuci set wireless.ap0.key=k3y-k3y-k3y\n" +
		"uci set wireless.ap1.ssid=Vectra-6D39\n" +
		"uci -q delete wireless.ap0.disabled\nuci set wireless.radio0.disabled=0\n" +
		"uci commit wireless"
	if got := w.commands(); !a.OK || a.Code != "wifi_optimized" || a.Detail != nil || got != want || len(w.dirs) != 1 ||
		!reflect.DeepEqual(w.words, []bool{true}) {
		t.Fatalf("optimize_wifi = %+v, %d spawns, commands:\n%s", a, len(w.spawns), got)
	}
	w.noneStaged(t)

	// No names, no channels, and netifd silent (nothing to scan): radio0 on
	// auto gets 6, radio1 keeps its 36 (valid at 80 MHz) — and the open 2.4
	// GHz network stays off.
	w.forget()
	a = w.call("optimize_wifi", `{}`).(uiapi.Action)
	got := w.commands()
	if !a.OK || a.Code != "wifi_optimized" || a.Detail == nil || *a.Detail != "radio0: open network left disabled; give it a key" ||
		!strings.Contains(got, "wireless.radio0.channel=6\n") || !strings.Contains(got, "wireless.radio1.channel=36\n") ||
		strings.Contains(got, "radio0.disabled") || strings.Contains(got, "ap0.ssid") {
		t.Fatalf("optimize_wifi {} = %+v, commands:\n%s", a, got)
	}
}

// A radio that is on with an open network and no key in the request: the
// whole call is refused before anything is scanned or staged, naming the
// radio — nothing switched off, nobody dropped. set_wifi refuses the same
// way.
func TestOptimizeWifiRefusesAnOpenNetworkThatIsOn(t *testing.T) {
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, strings.Replace(wizardWireless, "\toption disabled '1'\n", "", 1))
	w.ubus = map[string]string{"call network.wireless status": wizardStatus}
	for _, req := range []string{`{}`, `{"channels":{"radio0":6,"radio1":36}}`, `{"radios":{"radio0":{"ssid":"Home"}}}`} {
		a := w.call("optimize_wifi", req).(uiapi.Action)
		if a.OK || a.Code != "invalid_params" || a.Detail == nil || *a.Detail != "radios.radio0.key: the network is open — give it a key" ||
			w.commands() != "" || len(w.spawns) != 0 {
			t.Errorf("optimize_wifi %s on an open network that is on = %+v, commands %q", req, a, w.cmds)
		}
	}
	if a := w.call("set_wifi", `{"radios":{"radio0":{"ssid":"Home"}}}`).(uiapi.Action); a.OK || a.Code != "invalid_params" ||
		a.Detail == nil || *a.Detail != "radios.radio0.key: the network is open — give it a key" || w.commands() != "" {
		t.Errorf("set_wifi on an open network = %+v", a)
	}
	for _, q := range w.asked {
		if strings.Contains(q, "scan") {
			t.Fatalf("scanned before refusing: %q", q)
		}
	}
}

// A band the tuning has no rules for: tunable false, tuned null, and
// optimize_wifi refused as unsupported before anything is scanned or staged.
// Its networks can still be named.
func TestOptimizeWifiRefusesARouterItCannotTune(t *testing.T) {
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, wizardWireless+"config wifi-device 'radio2'\n\toption band '6g'\n\toption channel '37'\n\toption htmode 'HE160'\n"+
		"config wifi-iface 'ap2'\n\toption device 'radio2'\n\toption mode 'ap'\n\toption ssid 'Home-6G'\n\toption encryption 'sae'\n\toption key 'six-g-key'\n")
	w.ubus = map[string]string{"call network.wireless status": wizardStatus}
	a := w.call("optimize_wifi", `{}`).(uiapi.Action)
	if a.OK || a.Code != "unsupported" || a.Detail == nil || *a.Detail != "radio2: a 6 GHz radio — Panama's rules would switch it off" ||
		w.commands() != "" || len(w.spawns) != 0 {
		t.Fatalf("optimize_wifi on a 6 GHz router = %+v, commands %q", a, w.cmds)
	}
	for _, q := range w.asked {
		if strings.Contains(q, "scan") {
			t.Fatalf("scanned before refusing: %q", q)
		}
	}
	if sc := w.call("wifi_scan", "").(uiapi.WifiScan); len(sc.Radios) != 0 || sc.ScannedAt != nil {
		t.Fatalf("wifi_scan after a refusal = %+v", sc)
	}
	st := w.call("setup", "").(uiapi.Setup)
	if b, _ := json.Marshal(st.Wifi); st.Wifi.Tunable || st.Wifi.Tuned != nil || !strings.Contains(string(b), `"tuned":null,"tunable":false,"verdict":null`) {
		t.Fatalf("setup.wifi = %s", b)
	}
	if a := w.call("set_wifi", `{"radios":{"radio2":{"ssid":"Дом-6"}}}`).(uiapi.Action); !a.OK || a.Code != "wifi_set" ||
		!strings.HasPrefix(w.commands(), "uci set wireless.ap2.ssid=Дом-6\n") {
		t.Fatalf("set_wifi on a 6 GHz router = %+v, commands %q", a, w.cmds)
	}
}

// netifd with both radios up, each with its access point.
const wizardStatus = `{"radio0":{"up":true,"interfaces":[{"ifname":"phy0-ap0","config":{"mode":"ap"}}]},` +
	`"radio1":{"up":true,"interfaces":[{"ifname":"phy1-ap0","config":{"mode":"ap"}}]}}`

// wifi_scan never scans: it answers what the last optimize_wifi heard — or
// nothing, before the first.
func TestWifiScanAnswersTheLastOptimizesScan(t *testing.T) {
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, strings.Replace(strings.Replace(wizardWireless, "\toption disabled '1'\n", "", 1),
		"'OpenWrt'\n\toption encryption 'none'", "'OpenWrt'\n\toption encryption 'psk2'\n\toption key 'k3y-k3y-k3y'", 1))
	w.ubus = map[string]string{
		"call network.wireless status":           wizardStatus,
		`call iwinfo info {"device":"phy0-ap0"}`: `{"channel":6}`,
		`call iwinfo scan {"device":"phy0-ap0"}`: `{"results":[{"ssid":"a","channel":1,"signal":-48},{"ssid":"b","channel":1,"signal":-60},` +
			`{"ssid":"c","channel":6,"signal":-50},{"ssid":"d","channel":11,"signal":-86}]}`,
	}
	sc, ok := w.call("wifi_scan", "").(uiapi.WifiScan)
	if b, _ := json.Marshal(sc); !ok || string(b) != `{"radios":[],"scannedAt":null}` {
		t.Fatalf("wifi_scan before any optimize_wifi = %s", b)
	}
	if len(w.asked) != 0 {
		t.Fatalf("wifi_scan asked %q", w.asked)
	}

	if a := w.call("optimize_wifi", `{}`).(uiapi.Action); !a.OK {
		t.Fatalf("optimize_wifi = %+v", a)
	}
	w.forget()
	sc = w.call("wifi_scan", "").(uiapi.WifiScan)
	if len(w.asked) != 0 || len(sc.Radios) != 2 || sc.ScannedAt == nil {
		t.Fatalf("wifi_scan = %+v, asked %q", sc, w.asked)
	}
	r0, r1 := sc.Radios[0], sc.Radios[1]
	// radio0 (auto, now on 6) heard 1 and 6 busy; the network at -86 dBm does
	// not count, so 11 is the quiet one.
	if r0.Device != "radio0" || *r0.Band != "2g" || *r0.Current != 6 || *r0.Recommended != 11 || r0.Networks != 4 || r0.Error != nil ||
		len(r0.Channels) != 3 || r0.Channels[0] != (uiapi.ScanChannel{Channel: 1, Networks: 2, Strongest: -48}) {
		t.Errorf("radio0 = %+v", r0)
	}
	// radio1 could not be scanned (neither iw nor iwinfo answers): it keeps 36.
	if r1.Device != "radio1" || *r1.Band != "5g" || *r1.Current != 36 || *r1.Recommended != 36 || r1.Error == nil || *r1.Error != "failed" ||
		r1.Networks != 0 || r1.Channels == nil {
		t.Errorf("radio1 = %+v", r1)
	}
	b, _ := json.Marshal(sc)
	var shape []string
	sameShape("", generic(t, readContract(t, "wifi_scan.json")), generic(t, b), &shape)
	if len(shape) > 0 {
		t.Fatalf("the answer differs from the fixture's shape:\n  %s", strings.Join(shape, "\n  "))
	}
}

// The helper, spawned: handed the lock, it restarts the Wi-Fi only on the
// word; without it (rpcd did not commit) it does nothing. Run by hand, it
// takes the lock itself, and refuses while a change holds it.
func TestTheWifiHelperWaitsForTheWord(t *testing.T) {
	prev := setup.ApplyNoAnswerFor
	setup.ApplyNoAnswerFor = 0 // no netifd here: judged at once
	t.Cleanup(func() { setup.ApplyNoAnswerFor = prev })
	w := newWizardRouter(t)
	w.write(t, w.env.WirelessConfig, wizardWireless)
	word := func(s string) *os.File {
		r, wr, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = wr.WriteString(s)
		wr.Close()
		t.Cleanup(func() { r.Close() })
		return r
	}

	lock, err := setup.LockWifi(w.env)
	if err != nil {
		t.Fatal(err)
	}
	if err := setupWifiApply(w.env, lock, word("")); err != nil || w.commands() != "" {
		t.Fatalf("no word: %v, commands %q", err, w.cmds)
	}
	lock.Close()

	lock, _ = setup.LockWifi(w.env)
	if err := setupWifiApply(w.env, lock, word("go\n")); err != nil || w.commands() != "wifi reload" {
		t.Fatalf("the word: %v, commands %q", err, w.cmds)
	}
	if a := setup.LoadApply(w.env); a == nil || a.State != setup.ApplyUnverified {
		t.Fatalf("after the helper: %+v", a)
	}
	if l, err := setup.LockWifi(w.env); err != nil {
		t.Fatalf("the helper kept the lock: %v", err)
	} else {
		l.Close()
	}

	w.forget()
	if err := setupWifiApply(w.env, nil, word("")); err != nil || w.commands() != "wifi reload" {
		t.Fatalf("by hand: %v, commands %q", err, w.cmds)
	}
	// Run by hand with a descriptor 3 that is not the lock: it is let go, and
	// the lock taken.
	stray, err := os.CreateTemp(t.TempDir(), "stray")
	if err != nil {
		t.Fatal(err)
	}
	w.forget()
	if err := setupWifiApply(w.env, stray, word("")); err != nil || w.commands() != "wifi reload" {
		t.Fatalf("by hand with a stray descriptor: %v, commands %q", err, w.cmds)
	}
	if _, err := stray.Stat(); err == nil {
		t.Fatal("the stray descriptor was kept")
	}
	// No descriptor there at all: nothing is wrapped (fd 1000 is not open).
	if handedFile(1000, "vectra-wifi.lock") != nil {
		t.Fatal("a descriptor that is not open was wrapped")
	}
	other, _ := setup.LockWifi(w.env)
	defer other.Close()
	w.forget()
	if err := setupWifiApply(w.env, nil, word("")); !errors.Is(err, setup.ErrBusy) || w.commands() != "" {
		t.Fatalf("by hand while a change holds the lock: %v, commands %q", err, w.cmds)
	}
}

func TestFinishSetup(t *testing.T) {
	w := newWizardRouter(t)
	if a := w.call("finish_setup", "").(uiapi.Action); !a.OK || a.Code != "setup_finished" ||
		strings.Join(w.cmds, "\n") != "uci set vectra-controller-pro.main=controller\nuci set vectra-controller-pro.main.setup_done=1\nuci commit vectra-controller-pro" {
		t.Fatalf("finish_setup = %+v, commands %q", a, w.cmds)
	}
	for _, gone := range []string{"set_admin_password"} {
		if _, ok := rpcdSignatures[gone]; ok {
			t.Errorf("%s is still offered", gone)
		}
	}
}

// The wizard is the simple view: the operator's lock, from UCI or from the
// panel, never refuses it.
func TestTheLockNeverRefusesTheWizard(t *testing.T) {
	w := newWizardRouter(t)
	withRuntime(t, nil)
	w.write(t, w.env.WirelessConfig, wizardWireless)
	for _, lock := range []string{"uci", "panel"} {
		if lock == "uci" {
			fakeUILock(t, "1", nil)
		} else {
			fakeUILock(t, "0", nil)
			w.write(t, w.cfg.XrayConfigPath, string(mustOperatorConfig(t, true)))
		}
		for m, params := range map[string]string{"setup": "", "wan_check": "", "wifi_scan": "",
			"set_wifi": `{"radios":{"radio1":{"ssid":"s"}}}`, "optimize_wifi": `{"channels":{"radio0":6,"radio1":36}}`, "finish_setup": ""} {
			if m == "wan_check" {
				continue // no network needed to prove the point; its path is the same switch
			}
			out, _ := json.Marshal(w.call(m, params))
			if strings.Contains(string(out), `"code":"locked"`) {
				t.Errorf("%s lock refused %s", lock, m)
			}
		}
	}
}

// The panel can lock a router in the operator config it delivers; either lock
// locks, and a config the router cannot read locks too.
func TestThePanelCanLockTheRouterToo(t *testing.T) {
	s := newLockStand(t)
	fakeGather(t)
	fakeUILock(t, "0", nil)
	s.cfg.XrayConfigPath = filepath.Join(t.TempDir(), "xray-desired.json")
	for _, tc := range []struct {
		name   string
		config []byte
		locked bool
	}{
		{"no operator config", nil, false},
		{"the panel did not lock", mustOperatorConfig(t, false), false},
		{"the panel locked", mustOperatorConfig(t, true), true},
		{"a config the router cannot read", []byte(`{"schema":1,"surprise":true}`), true},
	} {
		os.Remove(s.cfg.XrayConfigPath)
		if tc.config != nil {
			if err := os.WriteFile(s.cfg.XrayConfigPath, tc.config, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		st := rpcdCall(context.Background(), s.cfg, "status", nil).(uiapi.Status)
		out, _ := json.Marshal(rpcdCall(context.Background(), s.cfg, "nodes", nil))
		if st.UI.Locked != tc.locked || (string(out) == lockedAnswer) != tc.locked {
			t.Errorf("%s: ui.locked=%v nodes=%s, want locked=%v", tc.name, st.UI.Locked, out, tc.locked)
		}
	}
}

// The owner's switch for the support shell (remote_shell.go): status says
// what the router does, set_remote_shell {"on": bool} changes it through uci
// — the simple view's, so the operator's lock never refuses it.
func TestTheOwnerSwitchesTheSupportShell(t *testing.T) {
	w := newWizardRouter(t)
	withRuntime(t, nil)
	prevPower := rpcdPower
	rpcdPower = func(context.Context, bool, bool) power.Facts { return power.Facts{UCI: true, Boot: true} }
	t.Cleanup(func() { rpcdPower = prevPower })
	shell := func() bool {
		t.Helper()
		st, ok := w.call("status", "").(uiapi.Status)
		if !ok {
			t.Fatal("status did not answer a status")
		}
		return st.RemoteShell
	}
	if shell() {
		t.Fatal("no remote_shell option, and status says the shell is on")
	}
	w.write(t, w.env.VectraConfig, "config controller 'main'\n\toption remote_shell '1'\n")
	if !shell() {
		t.Fatal("remote_shell '1', and status says the shell is off")
	}

	fakeUILock(t, "1", nil)
	for on, v := range map[bool]string{false: "0", true: "1"} {
		w.forget()
		a, ok := w.call("set_remote_shell", fmt.Sprintf(`{"on":%v}`, on)).(uiapi.Action)
		want := "uci set vectra-controller-pro.main=controller\nuci set vectra-controller-pro.main.remote_shell=" + v + "\nuci commit vectra-controller-pro"
		if !ok || !a.OK || a.Code != "remote_shell_set" || w.commands() != want {
			t.Errorf("set_remote_shell on=%v under the lock = %+v\n%s", on, a, w.commands())
		}
	}
	for _, p := range []string{``, `{}`, `{"on":1}`, `{"on":"yes"}`, `{"on":true,"for":"ever"}`, `[true]`} {
		w.forget()
		if a, _ := w.call("set_remote_shell", p).(uiapi.Action); a.OK || a.Code != "invalid_params" || w.commands() != "" {
			t.Errorf("%q = %+v, ran %q", p, a, w.commands())
		}
	}
	w.forget()
	w.fail = func(cmd string) bool { return strings.HasPrefix(cmd, "uci commit") }
	if a, _ := w.call("set_remote_shell", `{"on":true}`).(uiapi.Action); a.OK || a.Code != "internal" {
		t.Errorf("a commit that failed = %+v", a)
	}
}
