package setup

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"vectra-controller-pro/internal/uci"
)

// The Wi-Fi the wizard shows: every radio (wifi-device) with its interfaces,
// the first access point being the network of that band. The changes are in
// wifichange.go, the scan and the channel rules in scan.go, and the restart
// that proves them in wifiapply.go.

// Panama is the regulatory domain the tuning sets on every radio: among the
// highest power limits in both bands, 5 GHz UNII-3 (149-165) in particular.
// The owner chose it; it is above what some countries allow.
const Panama = "PA"

// Radio is one wifi-device and its interfaces. Never a key.
type Radio struct {
	Device   string // the wifi-device's UCI name: radio0
	Band     string // 2g | 5g | 6g | 60g; "" when the router cannot tell
	Channel  int    // 0: auto — the driver picks
	HTMode   string // "" when unset
	Width    int    // MHz, from htmode; 0 when unknown
	Country  string // "" when unset
	TxPower  *int   // dBm; nil when unset: the driver's maximum
	MaxPower bool   // at full power: no txpower (or the driver's most), and no vif_txpower on its access points
	PowerCut *int   // dB below the driver's most (its txpower, or an access point's lower vif_txpower); nil: cannot tell
	SSID     string // the first access point's; "" with none
	Secured  bool   // the first access point has encryption (not none/open)
	Enabled  bool   // the radio and its first access point are both on
	AP       bool   // it carries an access point
	Mesh     bool   // it carries a mesh or ad-hoc interface: its channel is its peers'
	Up       *bool  // live, from netifd; nil when netifd cannot say

	radioOn bool
	ifaces  []ifaceConf // every interface on the radio, in file order
}

// ifaceConf is one wifi-iface as UCI has it.
type ifaceConf struct {
	ref      string // its UCI ref
	mode     string // ap | mesh | adhoc | sta | …
	ssid     string
	enc      string // its encryption option, as set
	secure   bool
	on       bool // its own disabled option is not set
	vifPower bool // it sets vif_txpower
	vifDBm   *int // that vif_txpower, when it is a number
}

// firstAP is the radio's first access point: the network the wizard shows,
// names and secures for that band; nil with none.
func (r Radio) firstAP() *ifaceConf {
	for i := range r.ifaces {
		if r.ifaces[i].mode == "ap" {
			return &r.ifaces[i]
		}
	}
	return nil
}

// Wifi is every radio, the router's own network name, and the last change's
// restart as the helper judged it.
type Wifi struct {
	Radios    []Radio
	Suggested string      // SuggestedSSID; "" when unknown
	Apply     *ApplyState // nil: no change since boot
}

// Tunable: every radio is 2.4 or 5 GHz, the bands the tuning has rules for.
// The country is router-wide: Panama's would switch a 6 GHz radio off.
func (w Wifi) Tunable() bool {
	for _, r := range w.Radios {
		if r.Band != "2g" && r.Band != "5g" {
			return false
		}
	}
	return true
}

// Tuned: every enabled radio runs under Panama's rules, at full power, on a
// fixed channel; nothing enabled — or no radio at all — is tuned. nil when
// the router cannot be tuned (Tunable).
func (w Wifi) Tuned() *bool {
	if !w.Tunable() {
		return nil
	}
	tuned := true
	for _, r := range w.Radios {
		if r.Enabled && (r.Country != Panama || !r.MaxPower || r.Channel == 0) {
			tuned = false
		}
	}
	return &tuned
}

// The wizard's verdict on the Wi-Fi (setup.wifi.verdict).
const (
	// VerdictFine: it works as it is — the tuning's own, or a setup of the
	// owner's that does as well.
	VerdictFine = "fine"
	// VerdictBoost: there is more to get, and the boost gets it.
	VerdictBoost = "boost"
	// VerdictManual: the owner's own choice the tuning would undo (a radar
	// channel, the power cut hard): left alone, not suggested.
	VerdictManual = "manual"
)

// MaxPowerCutDB is how far below the driver's most a radio may run and still
// count as fine: 6 dB is a quarter of the power, and some reach.
const MaxPowerCutDB = 6

// Verdict judges the Wi-Fi a person has, not the tuning's recipe (Tuned is
// that). Of the radios that are on: one not up, or 2.4 GHz on a channel
// other than 1, 6 or 11 (2-5 and 7-10 overlap two of those, 12 and 13 some
// clients do not see) is VerdictBoost. Else 5 GHz on a channel that needs
// radar detection or that its width cannot carry, or the power more than
// MaxPowerCutDB below the driver's most, is VerdictManual. Else — any
// country, a channel on auto, the power a little down, what the router
// cannot tell — VerdictFine. A mesh radio's channel is its peers' and never
// judged. "" when the router cannot be tuned (Tunable).
func (w Wifi) Verdict() string {
	if !w.Tunable() {
		return ""
	}
	verdict := VerdictFine
	for _, r := range w.Radios {
		if !r.Enabled {
			continue
		}
		if r.Up != nil && !*r.Up {
			return VerdictBoost
		}
		if !r.Mesh && r.Channel != 0 {
			switch {
			case r.Band == "2g" && r.Channel != 1 && r.Channel != 6 && r.Channel != 11:
				return VerdictBoost
			case r.Band == "5g" && ValidChannel("5g", r.HTMode, r.Channel) != nil:
				verdict = VerdictManual
			}
		}
		if r.PowerCut != nil && *r.PowerCut > MaxPowerCutDB {
			verdict = VerdictManual
		}
	}
	return verdict
}

// secured: some access point is on and secured, and none is on and open.
func (w Wifi) secured() bool {
	on := false
	for _, r := range w.Radios {
		if !r.Enabled {
			continue
		}
		if !r.Secured {
			return false
		}
		on = true
	}
	return on
}

func (w Wifi) radio(device string) (Radio, bool) {
	for _, r := range w.Radios {
		if r.Device == device {
			return r, true
		}
	}
	return Radio{}, false
}

// ReadWifi reads every radio from UCI, whether each is up from netifd, and
// the last change's restart. A radio whose txpower is set is at full power
// when that reaches the most the driver offers (iwinfo txpowerlist, less its
// txpower offset): asked only then.
func ReadWifi(ctx context.Context, env Env) Wifi {
	w := readRadios(env)
	status, ok := wirelessStatus(ctx, env)
	for i := range w.Radios {
		r := &w.Radios[i]
		if s, listed := status[r.Device]; ok && listed {
			up := s.Up
			r.Up = &up
		}
		set, ok := r.setPower()
		if !ok {
			continue
		}
		max, known := maxTxPower(ctx, env, status[r.Device].APIfname)
		if !known {
			continue
		}
		if r.TxPower != nil && !r.vifPower() {
			r.MaxPower = *r.TxPower >= max
		}
		cut := max - set
		if cut < 0 {
			cut = 0
		}
		r.PowerCut = &cut
	}
	w.Apply = LoadApply(env)
	return w
}

func (r Radio) vifPower() bool {
	for _, i := range r.ifaces {
		if i.mode == "ap" && i.vifPower {
			return true
		}
	}
	return false
}

// setPower is the lowest power set on the radio (txpower) or its access
// points (vif_txpower), in dBm; false when nothing is set there.
func (r Radio) setPower() (int, bool) {
	v, ok := 0, false
	if r.TxPower != nil {
		v, ok = *r.TxPower, true
	}
	for _, i := range r.ifaces {
		if i.mode == "ap" && i.vifDBm != nil && (!ok || *i.vifDBm < v) {
			v, ok = *i.vifDBm, true
		}
	}
	return v, ok
}

// readRadios reads the radios from UCI alone.
func readRadios(env Env) Wifi {
	var w Wifi
	f, err := uci.Load(env.WirelessConfig)
	if err != nil {
		return w
	}
	for _, d := range f.OfType("wifi-device") {
		r := Radio{Device: d.Ref(), Band: RadioBand(d), Channel: fixedChannel(d.Get("channel")), HTMode: d.Get("htmode"),
			Width: widthOf(d.Get("htmode")), Country: d.Get("country"), radioOn: !disabledOpt(d.Get("disabled"))}
		if p, err := strconv.Atoi(strings.TrimSpace(d.Get("txpower"))); err == nil {
			r.TxPower = &p
		}
		for _, i := range interfaces(f, d) {
			c := ifaceConf{ref: i.Ref(), mode: i.Get("mode"), ssid: i.Get("ssid"), enc: i.Get("encryption"),
				secure: encrypted(i.Get("encryption")), on: !disabledOpt(i.Get("disabled")),
				vifPower: strings.TrimSpace(i.Get("vif_txpower")) != ""}
			if p, err := strconv.Atoi(strings.TrimSpace(i.Get("vif_txpower"))); err == nil {
				c.vifDBm = &p
			}
			switch c.mode {
			case "ap":
				r.AP = true
			case "mesh", "adhoc":
				r.Mesh = true
			}
			r.ifaces = append(r.ifaces, c)
		}
		if ap := r.firstAP(); ap != nil {
			r.SSID, r.Secured, r.Enabled = ap.ssid, ap.secure, r.radioOn && ap.on
		}
		r.MaxPower = r.TxPower == nil && !r.vifPower()
		if r.MaxPower {
			zero := 0
			r.PowerCut = &zero
		}
		w.Radios = append(w.Radios, r)
	}
	return w
}

// interfaces are radio d's interfaces, in file order.
func interfaces(f *uci.File, d uci.Section) []uci.Section {
	if d.Name == "" {
		return nil // an interface cannot name an anonymous radio
	}
	var out []uci.Section
	for _, i := range f.OfType("wifi-iface") {
		if i.Get("device") == d.Name {
			out = append(out, i)
		}
	}
	return out
}

// disabledOpt is a `disabled` option as netifd reads it: only "1" and
// "true" switch a radio or an interface off.
func disabledOpt(v string) bool {
	v = strings.TrimSpace(v)
	return v == "1" || v == "true"
}

func encrypted(enc string) bool {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "", "none", "open":
		return false
	}
	return true
}

// RadioBand is a radio's band (2g/5g/6g/60g): its `band` option when that is
// one of those, otherwise the one its hwmode implies; "" when unknown. Every
// reader of a radio's band (the wizard, Connect's set_wifi and its readback
// marker) resolves it here so they cannot disagree.
func RadioBand(r uci.Section) string {
	switch b := r.Get("band"); b {
	case "2g", "5g", "6g", "60g":
		return b
	}
	switch r.Get("hwmode") {
	case "11b", "11g":
		return "2g"
	case "11a":
		return "5g"
	}
	return ""
}

// widthOf is the channel width an htmode runs, in MHz; 0 when unknown.
func widthOf(htmode string) int {
	switch strings.ToUpper(strings.TrimSpace(htmode)) {
	case "NOHT", "HT20", "VHT20", "HE20", "EHT20":
		return 20
	case "HT40", "HT40+", "HT40-", "VHT40", "HE40", "EHT40":
		return 40
	case "VHT80", "HE80", "EHT80":
		return 80
	case "VHT160", "HE160", "EHT160":
		return 160
	case "EHT320":
		return 320
	}
	return 0
}

// fixedChannel is a radio's channel option as a number; 0 for "auto", for
// none, and for anything else the driver would choose itself.
func fixedChannel(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// radioStatus is netifd's view of one radio.
type radioStatus struct {
	Up          bool
	RetryFailed bool   // netifd gave up setting it up
	APIfname    string // the interface its first access point runs on; "" with none
}

// wirelessStatus is netifd's view of every radio (`ubus call network.wireless
// status`); ok is false when netifd cannot be asked.
func wirelessStatus(ctx context.Context, env Env) (map[string]radioStatus, bool) {
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := env.Output(c, "ubus", "call", "network.wireless", "status")
	if err != nil {
		return nil, false
	}
	var st map[string]struct {
		Up          bool `json:"up"`
		RetryFailed bool `json:"retry_setup_failed"`
		Interfaces  []struct {
			Ifname string `json:"ifname"`
			Config struct {
				Mode string `json:"mode"`
			} `json:"config"`
		} `json:"interfaces"`
	}
	if json.Unmarshal(raw, &st) != nil {
		return nil, false
	}
	out := make(map[string]radioStatus, len(st))
	for radio, s := range st {
		rs := radioStatus{Up: s.Up, RetryFailed: s.RetryFailed}
		for _, i := range s.Interfaces {
			if i.Config.Mode == "ap" && ifname(i.Ifname) {
				rs.APIfname = i.Ifname
				break
			}
		}
		out[radio] = rs
	}
	return out, true
}

// ifname is what the kernel accepts as an interface name.
func ifname(s string) bool {
	if s == "" || len(s) > 15 {
		return false
	}
	for _, c := range s {
		if !(c == '-' || c == '_' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// iwinfo calls rpcd's iwinfo object about one interface, as LuCI does.
func iwinfo(ctx context.Context, env Env, method, ifn string, timeout time.Duration, out interface{}) error {
	arg, _ := json.Marshal(map[string]string{"device": ifn})
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	secs := int(timeout / time.Second)
	if secs < 1 {
		secs = 1
	}
	raw, err := env.Output(c, "ubus", "-t", strconv.Itoa(secs), "call", "iwinfo", method, string(arg))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// maxTxPower is the most the driver offers on ifn, in dBm as txpower is set:
// the top of iwinfo's txpowerlist less the interface's txpower offset.
func maxTxPower(ctx context.Context, env Env, ifn string) (int, bool) {
	if ifn == "" {
		return 0, false
	}
	var list struct {
		Results []struct {
			Dbm int `json:"dbm"`
		} `json:"results"`
	}
	if iwinfo(ctx, env, "txpowerlist", ifn, 3*time.Second, &list) != nil || len(list.Results) == 0 {
		return 0, false
	}
	max := list.Results[0].Dbm
	for _, r := range list.Results[1:] {
		if r.Dbm > max {
			max = r.Dbm
		}
	}
	var info struct {
		Offset int `json:"txpower_offset"`
	}
	_ = iwinfo(ctx, env, "info", ifn, 3*time.Second, &info)
	return max - info.Offset, true
}
