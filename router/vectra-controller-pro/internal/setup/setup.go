// Package setup is the router side of the setup wizard a customer runs after
// unboxing: the Wi-Fi — tuned, and each band's network optionally renamed —
// then done. The internet connection is the router's own business: the wizard
// only shows it and checks it. The router's password is LuCI's: the wizard
// sets it through LuCI's own `luci.setPassword`, and this package only says
// whether root has one. The package reads what the router has, validates what
// the wizard asks for, and changes the router only through the uci command
// (one argv per value — nothing the browser sends can become a statement) and
// the router's own tools.
package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vectra-controller-pro/internal/brand"
	"vectra-controller-pro/internal/uci"
)

// Env is where the router keeps what the wizard reads and changes; tests
// point it at a temp dir and at fakes.
type Env struct {
	NetworkConfig  string // /etc/config/network
	WirelessConfig string // /etc/config/wireless
	VectraConfig   string // /etc/config/vectra-controller-pro
	Shadow         string // /etc/shadow
	SysClassNet    string // /sys/class/net
	NamePrefix     string // the suggested network name's first part ("" = "Router")

	// The Wi-Fi changes' own files.
	WifiLock  string // held from reading the router to the end of the restart's check
	WifiApply string // the last change's restart (setup.wifi.apply)
	WifiJob   string // what the restart needs to undo the change (0600: it holds the keys)
	WifiScan  string // the last scan (wifi_scan)
	RunDir    string // private uci save directories are made here
	// uci's default save directory (/tmp/.uci): every uci commit reads it,
	// whatever -t says.
	UCISaveDir string

	// Run runs a command (uci, wifi); stdin may be nil.
	Run func(ctx context.Context, stdin io.Reader, name string, args ...string) error
	// Output runs a command and returns its stdout (ubus, iw).
	Output func(ctx context.Context, name string, args ...string) ([]byte, error)
	Now    func() time.Time
	Sleep  func(time.Duration)
}

// RouterEnv is the production Env.
func RouterEnv() Env {
	return Env{
		NetworkConfig:  "/etc/config/network",
		WirelessConfig: "/etc/config/wireless",
		VectraConfig:   "/etc/config/vectra-controller-pro",
		Shadow:         "/etc/shadow",
		SysClassNet:    "/sys/class/net",
		WifiLock:       "/var/lock/vectra-wifi.lock",
		WifiApply:      "/var/run/vectra-controller-pro/wifi-apply.json",
		WifiJob:        "/var/run/vectra-controller-pro/wifi-job.json",
		WifiScan:       "/var/run/vectra-controller-pro/wifi-scan.json",
		RunDir:         "/var/run/vectra-controller-pro",
		UCISaveDir:     "/tmp/.uci",
		Run:            runCommand,
		Output:         outputCommand,
		Now:            time.Now,
		Sleep:          time.Sleep,
	}
}

func runCommand(ctx context.Context, stdin io.Reader, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.WaitDelay = time.Second
	return cmd.Run()
}

func outputCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	return cmd.Output()
}

// Facts is what the wizard shows about the router.
type Facts struct {
	Done bool
	// Password: root has a password (Password); nil = cannot tell.
	Password *bool
	Wan      Wan
	Lan      Lan
	Wifi     Wifi
	// SupportBot is the support bot the box was prepared with (SupportBot).
	SupportBot string
}

// Lan is how the home network reaches the router.
type Lan struct {
	IPv4 string // the LAN's address, where this page always opens; "" = unknown
}

// Wan is the router's internet connection, as it is: the wizard never
// changes it. No credential of it is ever read out.
type Wan struct {
	Proto   string // dhcp | pppoe | static | other
	Link    *bool  // cable in the WAN port; nil = cannot tell
	IPv4    string // "" = none
	Gateway string
	DNS     []string
}

// Read gathers the facts. It never fails: what cannot be read stays empty.
func Read(ctx context.Context, env Env) Facts {
	wan, device := readWan(ctx, env)
	wifi := ReadWifi(ctx, env)
	wifi.Suggested = SuggestedSSID(env, device, env.NamePrefix)
	return Facts{
		Done:       Done(env),
		Password:   Password(env),
		Wan:        wan,
		Lan:        ReadLan(ctx, env),
		Wifi:       wifi,
		SupportBot: SupportBot(env),
	}
}

// Done reports vectra-controller-pro.main.setup_done.
func Done(env Env) bool {
	f, err := uci.Load(env.VectraConfig)
	if err != nil {
		return false
	}
	main := f.Named("main")
	return main != nil && uciTrue(main.Get("setup_done"))
}

// RemoteShell reports vectra-controller-pro.main.remote_shell: the router's
// owner lets the panel's support shell run here (run_terminal_command — any
// command, as root). Only a yes allows it: the option absent, or a file uci
// could not read, is no. It is read at every use, so the owner's switch
// applies to the next job, and nothing restarts.
func RemoteShell(env Env) bool {
	f, err := uci.Load(env.VectraConfig)
	if err != nil {
		return false
	}
	main := f.Named("main")
	return main != nil && uciTrue(main.Get("remote_shell"))
}

// SupportBot is vectra-controller-pro.main.support_bot: the Telegram bot (no
// "@") a box is prepared with, so that one that has never been online — the
// panel names its bot only at the first register or check-in — can still
// point to support. "" when unset or not a Telegram username.
func SupportBot(env Env) string {
	f, err := uci.Load(env.VectraConfig)
	if err != nil {
		return ""
	}
	main := f.Named("main")
	if main == nil {
		return ""
	}
	if b := main.Get("support_bot"); TelegramUsername(b) {
		return b
	}
	return ""
}

// InstallBrand is vectra-controller-pro.main.brand: the installer's label for
// the router's brand (install.sh --brand), shown until the router's
// subscription names one. "" when unset or not a brand vctl knows.
func InstallBrand(env Env) brand.ID {
	f, err := uci.Load(env.VectraConfig)
	if err != nil {
		return ""
	}
	main := f.Named("main")
	if main == nil {
		return ""
	}
	id, _ := brand.Parse(main.Get("brand"))
	return id
}

// TelegramUsername: 5-32 characters of A-Z, a-z, 0-9 and _.
func TelegramUsername(s string) bool {
	if len(s) < 5 || len(s) > 32 {
		return false
	}
	for _, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func uciTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "enabled":
		return true
	}
	return false
}

// ifaceStatus is what netifd says about an interface
// (`ubus call network.interface.<name> status`).
type ifaceStatus struct {
	Up       bool   `json:"up"`
	Device   string `json:"device"`
	L3Device string `json:"l3_device"`
	IPv4     []struct {
		Address string `json:"address"`
	} `json:"ipv4-address"`
	Route []struct {
		Target  string `json:"target"`
		Mask    int    `json:"mask"`
		Nexthop string `json:"nexthop"`
	} `json:"route"`
	DNS []string `json:"dns-server"`
}

func readIfaceStatus(ctx context.Context, env Env, name string) *ifaceStatus {
	c, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := env.Output(c, "ubus", "call", "network.interface."+name, "status")
	if err != nil {
		return nil
	}
	var st ifaceStatus
	if json.Unmarshal(out, &st) != nil {
		return nil
	}
	return &st
}

// ReadLan reads the LAN's IPv4 address as netifd has it up: the first one,
// and only a real IPv4 address — the UI puts it in a link.
func ReadLan(ctx context.Context, env Env) Lan {
	st := readIfaceStatus(ctx, env, "lan")
	if st == nil || len(st.IPv4) == 0 {
		return Lan{}
	}
	if a, err := netip.ParseAddr(st.IPv4[0].Address); err == nil && a.Is4() {
		return Lan{IPv4: a.String()}
	}
	return Lan{}
}

// ReadWan reads the WAN: its protocol from UCI, its address, gateway and DNS
// from netifd, the cable from sysfs.
func ReadWan(ctx context.Context, env Env) Wan {
	w, _ := readWan(ctx, env)
	return w
}

// readWan also names the WAN's physical device ("" = unknown).
func readWan(ctx context.Context, env Env) (Wan, string) {
	w := Wan{Proto: "other"}
	device := ""
	if f, err := uci.Load(env.NetworkConfig); err == nil {
		if wan := f.Named("wan"); wan != nil {
			switch p := wan.Get("proto"); p {
			case "dhcp", "pppoe", "static":
				w.Proto = p
			}
			device = wan.Get("device")
			if device == "" {
				device = wan.Get("ifname")
			}
		}
	}
	if st := readIfaceStatus(ctx, env, "wan"); st != nil {
		if len(st.IPv4) > 0 {
			w.IPv4 = st.IPv4[0].Address
		}
		for _, r := range st.Route {
			if r.Target == "0.0.0.0" && r.Mask == 0 && r.Nexthop != "" {
				w.Gateway = r.Nexthop
				break
			}
		}
		w.DNS = st.DNS
		// The cable is the physical device; for PPPoE the L3 device only
		// exists while the session is up.
		switch {
		case st.Device != "":
			device = st.Device
		case st.L3Device != "":
			device = st.L3Device
		}
	}
	w.Link = carrier(env, device)
	return w, device
}

// sysNet reads /sys/class/net/<device>/<name>; device is a name, never a path.
func sysNet(env Env, device, name string) (string, bool) {
	if device == "" || strings.ContainsAny(device, "/\x00") || strings.HasPrefix(device, ".") {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(env.SysClassNet, device, name))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

func carrier(env Env, device string) *bool {
	v, ok := sysNet(env, device, "carrier")
	if !ok {
		return nil // an interface that is down has no carrier to read
	}
	up := v == "1"
	return &up
}

// SuggestedSSID is a network name of the router's own, "<prefix>-XXXX": the
// prefix is the brand's (Vectra, BloopCat) or the model's name (AX3000T),
// "Router" when none is given; XXXX are the last 4 hex digits of the WAN's
// MAC address, or of br-lan's when the WAN has none (a PPPoE session has no
// MAC; an unknown WAN device neither). "" when neither has one. The wizard
// offers it in place of OpenWrt's default.
func SuggestedSSID(env Env, wanDevice, prefix string) string {
	if prefix == "" {
		prefix = "Router"
	}
	for _, d := range []string{wanDevice, "br-lan"} {
		v, ok := sysNet(env, d, "address")
		if !ok {
			continue
		}
		mac, err := net.ParseMAC(v)
		if err != nil || len(mac) != 6 || (mac[0]|mac[1]|mac[2]|mac[3]|mac[4]|mac[5]) == 0 {
			continue
		}
		return fmt.Sprintf("%s-%02X%02X", prefix, mac[4], mac[5])
	}
	return ""
}

// Password reports whether root has a password — whether LuCI's login asks
// for one: the second field of root's /etc/shadow line is not empty. rpcd
// lets anyone in on an empty one (session.c, rpc_login_test_password), and
// nobody without the password otherwise, a locked "!" included. nil when it
// cannot tell: no shadow file to read, or no root in it.
func Password(env Env) *bool {
	b, err := os.ReadFile(env.Shadow)
	if err != nil {
		return nil
	}
	for _, line := range bytes.Split(b, []byte("\n")) {
		if f := bytes.SplitN(line, []byte(":"), 3); len(f) >= 2 && string(f[0]) == "root" {
			set := len(f[1]) > 0
			return &set
		}
	}
	return nil
}

// PasswordSet reports whether root has a password; one it cannot tell is
// none. Only the install-time guess (Working) asks.
func PasswordSet(env Env) bool {
	p := Password(env)
	return p != nil && *p
}
