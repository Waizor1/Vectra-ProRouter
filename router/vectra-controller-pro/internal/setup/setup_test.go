package setup

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// router is a fake router: config files in a temp dir, and every command it
// is asked to run recorded (with its stdin).
type router struct {
	env    Env
	mu     sync.Mutex
	cmds   [][]string
	stdins []string
	fail   func(args []string) bool // a command that fails
	ubus   string                   // what `ubus call network.interface.wan status` prints; "" = ubus fails
	// answers are what other commands print (ubus without "-t N", iw…), by
	// their arguments (`call iwinfo scan {"device":"phy0-ap0"}`); absent =
	// the command fails. dynamic ones win, for what changes over time.
	answers map[string]string
	dynamic map[string]func() (string, error)
	asked   []string
	now     time.Time // the fake clock: Sleep moves it
	onRun   func(args []string)
}

func newRouter(t *testing.T) *router {
	t.Helper()
	dir := t.TempDir()
	r := &router{}
	r.env = Env{
		NetworkConfig:  filepath.Join(dir, "network"),
		WirelessConfig: filepath.Join(dir, "wireless"),
		VectraConfig:   filepath.Join(dir, "vectra-controller-pro"),
		Shadow:         filepath.Join(dir, "shadow"),
		SysClassNet:    filepath.Join(dir, "sys"),
		WifiLock:       filepath.Join(dir, "lock", "vectra-wifi.lock"),
		WifiApply:      filepath.Join(dir, "run", "wifi-apply.json"),
		WifiJob:        filepath.Join(dir, "run", "wifi-job.json"),
		WifiScan:       filepath.Join(dir, "run", "wifi-scan.json"),
		RunDir:         filepath.Join(dir, "run"),
		UCISaveDir:     filepath.Join(dir, "uci"),
		Run: func(_ context.Context, stdin io.Reader, name string, args ...string) error {
			in := ""
			if stdin != nil {
				b, _ := io.ReadAll(stdin)
				in = string(b)
			}
			r.mu.Lock()
			r.cmds = append(r.cmds, append([]string{name}, args...))
			r.stdins = append(r.stdins, in)
			fail, hook := r.fail, r.onRun
			r.mu.Unlock()
			if hook != nil {
				hook(append([]string{name}, args...))
			}
			if fail != nil && fail(append([]string{name}, args...)) {
				return errors.New("exit status 1")
			}
			return nil
		},
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "ubus" && len(args) > 2 && args[0] == "-t" {
				args = args[2:]
			}
			key := strings.Join(args, " ")
			if name != "ubus" {
				key = name + " " + key
			}
			r.mu.Lock()
			r.asked = append(r.asked, key)
			fn, dyn := r.dynamic[key]
			out, ok := r.answers[key]
			wan := r.ubus
			r.mu.Unlock()
			switch {
			case key == "call network.interface.wan status" && wan != "":
				return []byte(wan), nil
			case dyn:
				o, err := fn()
				return []byte(o), err
			case ok:
				return []byte(out), nil
			}
			return nil, errors.New("not found")
		},
	}
	r.now = time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC)
	r.env.Now = func() time.Time {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.now
	}
	r.env.Sleep = func(d time.Duration) {
		r.mu.Lock()
		r.now = r.now.Add(d)
		r.mu.Unlock()
	}
	return r
}

func (r *router) write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *router) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.cmds))
	for i, c := range r.cmds {
		out[i] = strings.Join(c, " ")
	}
	return out
}

const network = `
config interface 'loopback'
	option device 'lo'
	option proto 'static'

config interface 'wan'
	option device 'wan'
	option proto 'pppoe'
	option username 'ivanov@isp'
	option password 's3cret-isp'
	list dns '8.8.8.8'
`

const wirelessCfg = `
config wifi-device 'radio0'
	option band '2g'
	option disabled '1'

config wifi-device 'radio1'
	option hwmode '11a'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option mode 'ap'
	option ssid 'OpenWrt'
	option encryption 'none'

config wifi-iface
	option device 'radio1'
	option mode 'ap'
	option ssid 'OpenWrt-5'
	option encryption 'none'

config wifi-iface
	option device 'radio1'
	option mode 'sta'
`

const wanUp = `{"up":true,"device":"wan","l3_device":"pppoe-wan","ipv4-address":[{"address":"100.64.12.7","mask":32}],
"route":[{"target":"10.0.0.0","mask":8,"nexthop":"100.64.12.9"},{"target":"0.0.0.0","mask":0,"nexthop":"100.64.12.1"}],
"dns-server":["100.64.12.1","100.64.12.2"]}`

func TestReadGathersWhatTheWizardShows(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.NetworkConfig, network)
	r.write(t, r.env.WirelessConfig, wirelessCfg)
	r.write(t, r.env.VectraConfig, "config controller 'main'\n\toption setup_done '0'\n")
	r.write(t, r.env.Shadow, "root::0:0:99999:7:::\ndaemon:*:0:0:99999:7:::\n")
	r.write(t, filepath.Join(r.env.SysClassNet, "wan", "carrier"), "1\n")
	r.write(t, filepath.Join(r.env.SysClassNet, "wan", "address"), "a4:39:b3:12:ab:cd\n")
	r.write(t, filepath.Join(r.env.SysClassNet, "br-lan", "address"), "a4:39:b3:12:ab:cc\n")
	r.ubus = wanUp

	f := Read(context.Background(), r.env)
	if f.Done {
		t.Errorf("done=%v", f.Done)
	}
	w := f.Wan
	if w.Proto != "pppoe" || w.IPv4 != "100.64.12.7" || w.Gateway != "100.64.12.1" ||
		!reflect.DeepEqual(w.DNS, []string{"100.64.12.1", "100.64.12.2"}) || w.Link == nil || !*w.Link {
		t.Errorf("wan = %+v", w)
	}
	wi := f.Wifi
	if len(wi.Radios) != 2 || wi.Suggested != "Vectra-ABCD" || wi.Radios[0].SSID != "OpenWrt" || wi.Radios[0].Band != "2g" ||
		wi.Radios[0].Secured || wi.Radios[0].Enabled || wi.Radios[1].Band != "5g" || !wi.Radios[1].Enabled {
		t.Errorf("wifi = %+v", wi)
	}

	// The password is set, the setup done, the AP secured and on; ubus down.
	r.write(t, r.env.Shadow, "root:$1$abc$def:0:0:99999:7:::\n")
	r.write(t, r.env.VectraConfig, "config controller 'main'\n\toption setup_done '1'\n")
	r.write(t, r.env.WirelessConfig, strings.Replace(strings.Replace(wirelessCfg, "'none'", "'psk2'", 1), "option disabled '1'", "option disabled '0'", 1))
	r.write(t, r.env.NetworkConfig, "config interface 'wan'\n\toption device 'eth1'\n\toption proto 'dhcp'\n")
	r.write(t, filepath.Join(r.env.SysClassNet, "eth1", "carrier"), "0\n")
	r.ubus = ""
	f = Read(context.Background(), r.env)
	if !f.Done || !f.Wifi.Radios[0].Secured || !f.Wifi.Radios[0].Enabled {
		t.Errorf("facts = %+v", f)
	}
	if w := f.Wan; w.Proto != "dhcp" || w.IPv4 != "" || w.DNS != nil || w.Link == nil || *w.Link {
		t.Errorf("wan without ubus = %+v", w)
	}
	// eth1 has no address file here: the name comes from br-lan.
	if f.Wifi.Suggested != "Vectra-ABCC" {
		t.Errorf("suggested = %q, want br-lan's", f.Wifi.Suggested)
	}
}

// The router's own network name: the WAN's MAC first, br-lan's when the WAN
// has none, nothing from an address that is not a MAC.
func TestTheSuggestedNetworkNameComesFromTheMAC(t *testing.T) {
	r := newRouter(t)
	mac := func(dev, v string) { r.write(t, filepath.Join(r.env.SysClassNet, dev, "address"), v+"\n") }
	if got := SuggestedSSID(r.env, "wan"); got != "" {
		t.Fatalf("no MAC anywhere: %q", got)
	}
	mac("pppoe-wan", "")             // a PPPoE session has no MAC
	mac("eth1", "00:00:00:00:00:00") // nor has a device without one
	mac("wan", "A4:39:B3:12:0f:0e")
	mac("br-lan", "a4:39:b3:12:ab:cc")
	for dev, want := range map[string]string{
		"wan": "Vectra-0F0E", "pppoe-wan": "Vectra-ABCC", "eth1": "Vectra-ABCC", "": "Vectra-ABCC",
		"../../etc": "Vectra-ABCC", "missing": "Vectra-ABCC",
	} {
		if got := SuggestedSSID(r.env, dev); got != want {
			t.Errorf("WAN device %q: %q, want %q", dev, got, want)
		}
	}
	mac("br-lan", "not-a-mac")
	if got := SuggestedSSID(r.env, "eth1"); got != "" {
		t.Errorf("no usable MAC: %q", got)
	}
}

// The support bot a box is prepared with: a Telegram username, or nothing.
func TestTheSupportBotIsATelegramUsernameOrNothing(t *testing.T) {
	r := newRouter(t)
	if got := SupportBot(r.env); got != "" {
		t.Fatalf("no config: %q", got)
	}
	for value, want := range map[string]string{
		"VectraHelpBot":         "VectraHelpBot",
		"vectra_help_2":         "vectra_help_2",
		strings.Repeat("b", 5):  strings.Repeat("b", 5),
		strings.Repeat("b", 32): strings.Repeat("b", 32),
		"":                      "",
		"help":                  "",
		strings.Repeat("b", 33): "",
		"@VectraHelpBot":        "",
		"vectra-help":           "",
		"t.me/VectraHelpBot":    "",
		"Вектра_бот":            "",
	} {
		r.write(t, r.env.VectraConfig, "config controller 'main'\n\toption support_bot '"+value+"'\n")
		if got := SupportBot(r.env); got != want {
			t.Errorf("support_bot %q = %q, want %q", value, got, want)
		}
	}
	r.write(t, r.env.VectraConfig, "config controller 'main'\n\toption support_bot 'VectraHelpBot'\n")
	if f := Read(context.Background(), r.env); f.SupportBot != "VectraHelpBot" {
		t.Fatalf("Read: support bot %q", f.SupportBot)
	}
}

func TestReadSaysLittleAboutARouterItCannotRead(t *testing.T) {
	r := newRouter(t)
	f := Read(context.Background(), r.env)
	if f.Done || f.Wan.Proto != "other" || f.Wan.Link != nil || f.Wifi.Suggested != "" || len(f.Wifi.Radios) != 0 || !f.Wifi.Tunable() || f.Wifi.Tuned() == nil || !*f.Wifi.Tuned() || f.Wifi.Apply != nil {
		t.Fatalf("facts = %+v", f)
	}
	r.write(t, r.env.NetworkConfig, "config interface 'wan'\n\toption proto 'dhcpv6'\n\toption device '../../etc'\n")
	if w := ReadWan(context.Background(), r.env); w.Proto != "other" || w.Link != nil {
		t.Fatalf("wan = %+v", w)
	}
}

// A value goes to uci as one argv element, staged in a private save
// directory; an error names the step, never the value.
func TestTheUCIStepsAreArgvWithoutAShell(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, "config wifi-device 'radio0'\n\toption band '2g'\nconfig wifi-iface 'ap0'\n\toption device 'radio0'\n\toption mode 'ap'\n")
	stageNames := func(ssid, key string) error {
		p, err := PlanWifi(context.Background(), r.env, WifiRequest{Radios: map[string]WifiChange{"radio0": {SSID: str(ssid), Key: str(key)}}})
		if err != nil {
			return err
		}
		s, err := Stage(context.Background(), r.env, p)
		if err == nil {
			s.Abort()
		}
		return err
	}
	if err := stageNames("a b;$(reboot)", "k w;$(reboot)"); err != nil {
		t.Fatal(err)
	}
	got := r.cmds[0]
	if len(got) != 5 || got[0] != "uci" || got[1] != "-t" || !strings.HasPrefix(got[2], r.env.RunDir) ||
		got[3] != "set" || got[4] != "wireless.ap0.ssid=a b;$(reboot)" {
		t.Errorf("argv = %q", got)
	}
	if d := set("wireless.ap0.key", "hunter22").describe(); strings.Contains(d, "hunter22") || d != "set wireless.ap0.key" {
		t.Errorf("describe = %q", d)
	}
	r.fail = func(args []string) bool { return len(args) > 4 && strings.HasPrefix(args[4], "wireless.ap0.key=") }
	if err := stageNames("home", "hunter22"); err == nil || strings.Contains(err.Error(), "hunter22") {
		t.Errorf("a failed step: %v", err)
	}
}

func TestFinishAndMarkDoneIfWorking(t *testing.T) {
	finish := "uci set vectra-controller-pro.main=controller\nuci set vectra-controller-pro.main.setup_done=1\nuci commit vectra-controller-pro"
	r := newRouter(t)
	if err := Finish(context.Background(), r.env); err != nil || strings.Join(r.commands(), "\n") != finish {
		t.Fatalf("finish: %v\n%s", err, strings.Join(r.commands(), "\n"))
	}

	working := func(r *router) {
		r.write(t, r.env.NetworkConfig, "config interface 'wan'\n\toption proto 'dhcp'\n")
		r.write(t, r.env.WirelessConfig, strings.ReplaceAll(wirelessCfg, "'none'", "'psk2'"))
		r.write(t, r.env.Shadow, "root:$5$x$y:0:0:::::\n")
		r.ubus = wanUp
	}
	for _, tc := range []struct {
		name   string
		setup  func(r *router)
		linked bool
		marked bool
	}{
		{"a working router", working, false, true},
		{"a linked router, whatever else", func(*router) {}, true, true},
		{"a fresh router", func(*router) {}, false, false},
		{"no password yet", func(r *router) { working(r); r.write(t, r.env.Shadow, "root::0:0:::::\n") }, false, false},
		{"open Wi-Fi", func(r *router) { working(r); r.write(t, r.env.WirelessConfig, wirelessCfg) }, false, false},
		{"no Wi-Fi on", func(r *router) {
			working(r)
			r.write(t, r.env.WirelessConfig, strings.ReplaceAll(strings.ReplaceAll(wirelessCfg, "'none'", "'psk2'"), "option hwmode '11a'", "option hwmode '11a'\n\toption disabled '1'"))
		}, false, false},
		{"no WAN address", func(r *router) { working(r); r.ubus = "" }, false, false},
		{"already done", func(r *router) {
			working(r)
			r.write(t, r.env.VectraConfig, "config controller 'main'\n\toption setup_done '1'\n")
		}, false, false},
	} {
		r := newRouter(t)
		tc.setup(r)
		marked, err := MarkDoneIfWorking(context.Background(), r.env, tc.linked)
		if err != nil || marked != tc.marked || (strings.Join(r.commands(), "\n") == finish) != tc.marked {
			t.Errorf("%s: marked=%v err=%v commands %q", tc.name, marked, err, r.commands())
		}
	}
}

func TestCheckerLooksWithinItsBudget(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/generate_204" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, "http://portal.example/login", http.StatusFound) // a captive portal
	}))
	defer probe.Close()
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer panel.Close()

	c := Checker{PanelURL: panel.URL, ProbeURL: probe.URL + "/generate_204", ProbeHost: "127.0.0.1", Budget: 3 * time.Second}
	if r := c.Run(context.Background(), nil); r != (Result{DNS: true, Internet: true, Panel: true}) {
		t.Fatalf("all up = %+v", r)
	}
	c.ProbeURL = probe.URL + "/captive"
	if r := c.Run(context.Background(), nil); r.Internet || !r.Panel {
		t.Fatalf("behind a captive portal = %+v", r)
	}
	// Nothing answers: TEST-NET-1 is unroutable. The budget still holds.
	c = Checker{PanelURL: "http://192.0.2.1/", ProbeURL: "http://192.0.2.1/generate_204", ProbeHost: "vectra.invalid", Budget: 500 * time.Millisecond}
	start := time.Now()
	if r := c.Run(context.Background(), []string{"192.0.2.1"}); r != (Result{}) || time.Since(start) > 2*time.Second {
		t.Fatalf("nothing up = %+v after %s", r, time.Since(start))
	}
}
