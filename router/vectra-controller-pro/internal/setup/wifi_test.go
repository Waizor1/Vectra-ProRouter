package setup

import (
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
)

func str(s string) *string { return &s }

// Two radios as a router might have them before the wizard: 2.4 GHz off,
// open, on auto at 17 dBm under the US's rules; 5 GHz on, secured (a
// psk-mixed network with a vif_txpower), on 36 at 80 MHz with no country —
// and a guest network on it, open, off.
const wirelessTwo = `
config wifi-device 'radio0'
	option type 'mac80211'
	option band '2g'
	option channel 'auto'
	option htmode 'HE20'
	option txpower '17'
	option country 'US'
	option disabled '1'

config wifi-device 'radio1'
	option type 'mac80211'
	option band '5g'
	option channel '36'
	option htmode 'HE80'

config wifi-iface 'default_radio0'
	option device 'radio0'
	option network 'lan'
	option mode 'ap'
	option ssid 'OpenWrt'
	option encryption 'none'

config wifi-iface 'default_radio1'
	option device 'radio1'
	option network 'lan'
	option mode 'ap'
	option ssid 'Home-5G'
	option encryption 'psk-mixed'
	option key 'old-5g-key'
	option ieee80211w '1'

config wifi-iface 'guest_radio1'
	option device 'radio1'
	option mode 'ap'
	option ssid 'Guest'
	option encryption 'none'
	option disabled '1'
`

// statusUp is netifd with the radios given up, each with its access point's
// interface.
func statusUp(radios ...string) string {
	parts := make([]string, 0, len(radios))
	for i, r := range radios {
		parts = append(parts, fmt.Sprintf(`%q:{"up":true,"retry_setup_failed":false,"interfaces":[{"section":"default_%s","ifname":"phy%d-ap0","config":{"mode":"ap"}}]}`, r, r, i))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func uciSteps(r *router) string {
	var out []string
	for _, c := range r.commands() {
		if strings.HasPrefix(c, "uci -t ") {
			f := strings.SplitN(c, " ", 4) // uci -t <dir> <rest>
			c = "uci " + f[3]
		}
		out = append(out, c)
	}
	return strings.Join(out, "\n")
}

// ---- reading ---------------------------------------------------------------

func TestReadWifiShowsEveryRadioAndNeverAKey(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, wirelessTwo)
	r.answers = map[string]string{"call network.wireless status": `{"radio1":{"up":true,"interfaces":[{"ifname":"phy1-ap0","config":{"mode":"ap"}}]},"radio0":{"up":false,"interfaces":[]}}`}
	w := ReadWifi(context.Background(), r.env)
	if len(w.Radios) != 2 {
		t.Fatalf("radios = %+v", w.Radios)
	}
	r0, r1 := w.Radios[0], w.Radios[1]
	if r0.Device != "radio0" || r0.Band != "2g" || r0.Channel != 0 || r0.Width != 20 || r0.Country != "US" || r0.TxPower == nil ||
		*r0.TxPower != 17 || r0.MaxPower || r0.SSID != "OpenWrt" || r0.Secured || r0.Enabled || !r0.AP || r0.Mesh || r0.Up == nil || *r0.Up {
		t.Errorf("radio0 = %+v", r0)
	}
	// radio1: the driver's power, but an access point with a vif_txpower.
	if r1.Device != "radio1" || r1.Band != "5g" || r1.Channel != 36 || r1.Width != 80 || r1.TxPower != nil || !r1.MaxPower ||
		r1.SSID != "Home-5G" || !r1.Secured || !r1.Enabled || !r1.AP || r1.Up == nil || !*r1.Up {
		t.Errorf("radio1 = %+v", r1)
	}
	if strings.Contains(fmt.Sprintf("%+v", w), "old-5g-key") {
		t.Fatal("a key was read out")
	}
	if tuned := w.Tuned(); !w.Tunable() || tuned == nil || *tuned {
		t.Errorf("tunable=%v tuned=%v, want tunable and not tuned (radio1 is on with no country)", w.Tunable(), tuned)
	}

	// vif_txpower on an access point: not at full power.
	r.write(t, r.env.WirelessConfig, strings.Replace(wirelessTwo, "\toption ieee80211w '1'\n", "\toption ieee80211w '1'\n\toption vif_txpower '10'\n", 1))
	if ReadWifi(context.Background(), r.env).Radios[1].MaxPower {
		t.Error("a vif_txpower counted as full power")
	}

	// txpower set: full power when it reaches the driver's most, less the
	// txpower offset. Asked only then.
	r2 := newRouter(t)
	r2.write(t, r2.env.WirelessConfig, strings.Replace(wirelessTwo, "\toption disabled '1'\n", "", 1))
	r2.answers = map[string]string{
		"call network.wireless status":                  statusUp("radio0", "radio1"),
		`call iwinfo txpowerlist {"device":"phy0-ap0"}`: `{"results":[{"dbm":0,"mw":1},{"dbm":20,"mw":100}]}`,
		`call iwinfo info {"device":"phy0-ap0"}`:        `{"txpower_offset":3}`,
	}
	if !ReadWifi(context.Background(), r2.env).Radios[0].MaxPower {
		t.Error("17 dBm against 20 - 3 not counted as full power")
	}
	r2.answers[`call iwinfo info {"device":"phy0-ap0"}`] = `{"txpower_offset":0}`
	if ReadWifi(context.Background(), r2.env).Radios[0].MaxPower {
		t.Error("17 dBm of 20 counted as full power")
	}

	// No netifd: up is unknown, and nothing else asked without a txpower.
	r3 := newRouter(t)
	r3.write(t, r3.env.WirelessConfig, strings.Replace(wirelessTwo, "\toption txpower '17'\n", "", 1))
	for _, radio := range ReadWifi(context.Background(), r3.env).Radios {
		if radio.Up != nil {
			t.Errorf("%s: up without netifd", radio.Device)
		}
	}
	for _, a := range r3.asked {
		if strings.Contains(a, "iwinfo") {
			t.Errorf("asked %q without a txpower", a)
		}
	}
}

// Only "1" and "true" switch something off, as netifd reads `disabled`.
func TestDisabledIsWhatNetifdReads(t *testing.T) {
	for v, off := range map[string]bool{"1": true, "true": true, " 1 ": true, "0": false, "": false, "yes": false, "on": false, "enabled": false} {
		if disabledOpt(v) != off {
			t.Errorf("disabled %q = %v", v, !off)
		}
	}
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, strings.Replace(wirelessTwo, "option disabled '1'", "option disabled 'yes'", 1))
	if !readRadios(r.env).Radios[0].radioOn {
		t.Error("disabled 'yes' switched radio0 off")
	}
}

func TestWidthComesFromHtmode(t *testing.T) {
	for htmode, width := range map[string]int{"NOHT": 20, "HT20": 20, "VHT20": 20, "HE20": 20, "EHT20": 20,
		"HT40": 40, "HT40+": 40, "HT40-": 40, "VHT40": 40, "HE40": 40, "EHT40": 40,
		"VHT80": 80, "HE80": 80, "EHT80": 80, "VHT160": 160, "HE160": 160, "EHT160": 160, "EHT320": 320, "": 0, "HE9000": 0} {
		if got := widthOf(htmode); got != width {
			t.Errorf("%q: %d, want %d", htmode, got, width)
		}
	}
}

// A band the rules do not cover makes the router untunable: tuned is null.
func TestTunableAndTuned(t *testing.T) {
	on := func(band, country string, maxPower bool, ch int) Radio {
		return Radio{Band: band, Enabled: true, Country: country, MaxPower: maxPower, Channel: ch}
	}
	for _, tc := range []struct {
		name    string
		radios  []Radio
		tunable bool
		tuned   *bool
	}{
		{"no radios", nil, true, boolp(true)},
		{"all tuned", []Radio{on("2g", "PA", true, 11), on("5g", "PA", true, 149)}, true, boolp(true)},
		{"one on auto", []Radio{on("2g", "PA", true, 11), on("5g", "PA", true, 0)}, true, boolp(false)},
		{"one below full power", []Radio{on("2g", "PA", false, 11)}, true, boolp(false)},
		{"one elsewhere", []Radio{on("2g", "US", true, 11)}, true, boolp(false)},
		{"an untuned radio that is off", []Radio{on("2g", "PA", true, 11), {Band: "5g", Country: "US"}}, true, boolp(true)},
		{"a 6 GHz radio", []Radio{on("2g", "PA", true, 11), on("6g", "PA", true, 37)}, false, nil},
		{"a radio of unknown band", []Radio{{Band: ""}}, false, nil},
	} {
		w := Wifi{Radios: tc.radios}
		if w.Tunable() != tc.tunable || !reflect.DeepEqual(w.Tuned(), tc.tuned) {
			t.Errorf("%s: tunable=%v tuned=%v", tc.name, w.Tunable(), w.Tuned())
		}
	}
}

func boolp(b bool) *bool { return &b }

// The verdict judges the Wi-Fi a person has, not the tuning's own recipe: a
// setup of the owner's that works is fine — any country, a channel of their
// choosing on either band, the power a little down — and the boost is
// suggested only where there is more to get: 2.4 GHz on a channel between the
// three that do not overlap, or a radio that is on and not up. A radar
// channel, or the power cut hard, is the owner's own choice: left alone,
// never nagged about.
func TestTheVerdictLeavesAWorkingSetupAlone(t *testing.T) {
	up, down := boolp(true), boolp(false)
	cut := func(db int) *int { return &db }
	on := func(band string, ch int, htmode string, powerCut *int, isUp *bool) Radio {
		return Radio{Band: band, Enabled: true, Channel: ch, HTMode: htmode, Width: widthOf(htmode), Country: "RU", PowerCut: powerCut, Up: isUp}
	}
	good5 := on("5g", 36, "HE80", cut(0), up)
	for _, tc := range []struct {
		name   string
		radios []Radio
		want   string
	}{
		{"the tuning's own", []Radio{{Band: "2g", Enabled: true, Channel: 11, HTMode: "HE20", Width: 20, Country: "PA", MaxPower: true, PowerCut: cut(0), Up: up},
			{Band: "5g", Enabled: true, Channel: 149, HTMode: "HE80", Width: 80, Country: "PA", MaxPower: true, PowerCut: cut(0), Up: up}}, VerdictFine},
		{"both on auto", []Radio{on("2g", 0, "HE20", cut(0), up), on("5g", 0, "HE80", cut(0), up)}, VerdictFine},
		{"2.4 GHz on 1, 6 and 11", []Radio{on("2g", 1, "HE20", cut(0), up), on("2g", 6, "HE40", cut(0), up), on("2g", 11, "HT20", cut(0), up), good5}, VerdictFine},
		{"5 GHz on the upper block", []Radio{on("5g", 161, "HE80", cut(0), up), on("5g", 165, "HE20", cut(0), up)}, VerdictFine},
		{"5 GHz at 160 MHz on 36", []Radio{on("5g", 36, "HE160", cut(0), up)}, VerdictFine},
		{"power 6 dB down", []Radio{on("2g", 6, "HE20", cut(6), up), good5}, VerdictFine},
		{"power the router cannot tell", []Radio{on("2g", 6, "HE20", nil, up)}, VerdictFine},
		{"up the router cannot tell", []Radio{on("2g", 6, "HE20", cut(0), nil)}, VerdictFine},
		{"a radio that is off", []Radio{on("2g", 11, "HE20", cut(0), up), {Band: "5g", Channel: 100, Up: down}}, VerdictFine},
		{"a mesh radio keeps its peers' channel", []Radio{{Band: "2g", Enabled: true, Mesh: true, Channel: 3, PowerCut: cut(0), Up: up}}, VerdictFine},
		{"no radios", nil, VerdictFine},

		{"2.4 GHz on 3", []Radio{on("2g", 3, "HE20", cut(0), up), good5}, VerdictBoost},
		{"2.4 GHz on 9", []Radio{on("2g", 9, "HE20", cut(0), up)}, VerdictBoost},
		{"2.4 GHz on 13", []Radio{on("2g", 13, "HE20", cut(0), up)}, VerdictBoost},
		{"a radio that does not come up", []Radio{on("2g", 11, "HE20", cut(0), up), on("5g", 36, "HE80", cut(0), down)}, VerdictBoost},
		{"a mesh radio down", []Radio{{Band: "2g", Enabled: true, Mesh: true, Channel: 3, PowerCut: cut(0), Up: down}}, VerdictBoost},
		{"a boost wins over a radar channel", []Radio{on("2g", 4, "HE20", cut(0), up), on("5g", 100, "HE80", cut(0), up)}, VerdictBoost},

		{"5 GHz on a radar channel", []Radio{on("2g", 11, "HE20", cut(0), up), on("5g", 100, "HE80", cut(0), up)}, VerdictManual},
		{"5 GHz on 149 at 160 MHz", []Radio{on("5g", 149, "HE160", cut(0), up)}, VerdictManual},
		{"the power cut by 7 dB", []Radio{on("2g", 11, "HE20", cut(7), up), good5}, VerdictManual},

		{"a 6 GHz radio", []Radio{on("2g", 3, "HE20", cut(0), up), {Band: "6g", Enabled: true, Channel: 37, Up: up}}, ""},
	} {
		if got := (Wifi{Radios: tc.radios}).Verdict(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// How far a radio's power is cut below the most its driver offers: its
// txpower, or an access point's lower vif_txpower, against iwinfo's list less
// the txpower offset. Asked only where something is set; unknown when iwinfo
// cannot say.
func TestReadWifiKnowsHowFarThePowerIsCut(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, strings.Replace(strings.Replace(wirelessTwo, "\toption disabled '1'\n", "", 1),
		"\toption ieee80211w '1'\n", "\toption ieee80211w '1'\n\toption vif_txpower '12'\n", 1))
	r.answers = map[string]string{
		"call network.wireless status":                  statusUp("radio0", "radio1"),
		`call iwinfo txpowerlist {"device":"phy0-ap0"}`: `{"results":[{"dbm":0,"mw":1},{"dbm":20,"mw":100}]}`,
		`call iwinfo info {"device":"phy0-ap0"}`:        `{"txpower_offset":0}`,
		`call iwinfo txpowerlist {"device":"phy1-ap0"}`: `{"results":[{"dbm":23,"mw":199}]}`,
		`call iwinfo info {"device":"phy1-ap0"}`:        `{"txpower_offset":0}`,
	}
	w := ReadWifi(context.Background(), r.env)
	if c := w.Radios[0].PowerCut; c == nil || *c != 3 {
		t.Errorf("radio0 (17 of 20 dBm): cut %v, want 3", c)
	}
	if c := w.Radios[1].PowerCut; c == nil || *c != 11 {
		t.Errorf("radio1 (an access point at 12 of 23 dBm): cut %v, want 11", c)
	}
	if w.Radios[1].MaxPower {
		t.Error("a vif_txpower counted as full power")
	}

	// Nothing set: at the driver's most, and iwinfo is not asked.
	r2 := newRouter(t)
	r2.write(t, r2.env.WirelessConfig, strings.Replace(wirelessTwo, "\toption txpower '17'\n", "", 1))
	r2.answers = map[string]string{"call network.wireless status": statusUp("radio0", "radio1")}
	for _, radio := range ReadWifi(context.Background(), r2.env).Radios {
		if radio.PowerCut == nil || *radio.PowerCut != 0 {
			t.Errorf("%s: cut %v, want 0", radio.Device, radio.PowerCut)
		}
	}
	for _, a := range r2.asked {
		if strings.Contains(a, "iwinfo") {
			t.Errorf("asked %q with nothing set", a)
		}
	}

	// iwinfo cannot say: unknown.
	r3 := newRouter(t)
	r3.write(t, r3.env.WirelessConfig, wirelessTwo)
	r3.answers = map[string]string{"call network.wireless status": statusUp("radio0", "radio1")}
	if c := ReadWifi(context.Background(), r3.env).Radios[0].PowerCut; c != nil {
		t.Errorf("no txpower list, cut %d", *c)
	}
}

// ---- channel rules ---------------------------------------------------------

func TestChannelRulesFollowTheWidth(t *testing.T) {
	for _, tc := range []struct {
		band, htmode string
		ch           int
		why          string // "" = allowed
	}{
		{"5g", "HE160", 36, ""}, {"5g", "HE160", 48, ""},
		{"5g", "HE160", 149, "cannot carry 160 MHz"}, {"5g", "HE160", 165, "cannot carry 160 MHz"},
		{"5g", "EHT320", 149, "cannot carry 320 MHz"},
		{"5g", "HE80", 149, ""}, {"5g", "HE80", 161, ""}, {"5g", "HE80", 165, "20 MHz only"},
		{"5g", "HE40", 157, ""}, {"5g", "HE40", 165, "20 MHz only"},
		{"5g", "HE20", 165, ""}, {"5g", "", 165, "20 MHz only"}, {"5g", "", 149, ""},
		{"5g", "HE80", 100, "radar detection"}, {"5g", "HE20", 52, "radar detection"}, {"5g", "HE20", 144, "radar detection"},
		{"5g", "HE80", 38, "36-48, 149-165"}, {"5g", "HE20", 169, "36-48, 149-165"},
		{"2g", "HE20", 1, ""}, {"2g", "HE20", 11, ""}, {"2g", "HE20", 12, "client compatibility"}, {"2g", "HT40+", 13, "client compatibility"},
		{"2g", "HT40+", 9, ""}, {"2g", "HT40+", 10, "1-9"}, {"2g", "HT40+", 11, "1-9"},
		{"2g", "HT40-", 5, ""}, {"2g", "HT40-", 4, "5-11"}, {"2g", "HT40-", 1, "5-11"},
		{"2g", "HE20", 0, "1-11"}, {"2g", "HE20", 36, "1-11"},
		{"6g", "HE80", 37, "no channel rules"},
	} {
		err := ValidChannel(tc.band, tc.htmode, tc.ch)
		if (err == nil) != (tc.why == "") || (err != nil && !strings.Contains(err.Error(), tc.why)) {
			t.Errorf("%s %s %d: %v, want %q", tc.band, tc.htmode, tc.ch, err, tc.why)
		}
	}
	// No Panama in the wording of refusals that are not Panama's.
	for _, c := range []struct {
		band string
		ch   int
	}{{"2g", 12}, {"5g", 100}} {
		if err := ValidChannel(c.band, "HE20", c.ch); err == nil || strings.Contains(err.Error(), "Panama") {
			t.Errorf("%s %d: %v", c.band, c.ch, err)
		}
	}
}

func heardOf(heard map[int][]int) []Neighbour {
	var out []Neighbour
	for ch, sigs := range heard {
		for _, s := range sigs {
			out = append(out, Neighbour{Channel: ch, Signal: s})
		}
	}
	return out
}

func TestTheChannelChoice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		band    string
		htmode  string
		current int
		heard   map[int][]int
		want    int
	}{
		{"2.4: crowded 1 and 6, 11 empty", "2g", "HE20", 1, map[int][]int{1: {-40, -45, -60}, 6: {-42, -50}}, 11},
		{"2.4: crowded 6 and 11, 1 empty", "2g", "HE20", 6, map[int][]int{6: {-40}, 11: {-45, -50}, 9: {-55}}, 1},
		{"2.4: silence keeps the channel it is on", "2g", "HE20", 6, nil, 6},
		{"2.4: silence on auto", "2g", "HE20", 0, nil, 1},
		{"2.4 HT40+ never gets 11: 11 empty, 1 and 6 crowded", "2g", "HT40+", 1, map[int][]int{1: {-40}, 6: {-50}}, 6},
		{"2.4 HT40+ never gets 11: silence", "2g", "HT40+", 11, nil, 1},
		{"2.4 HT40- never gets 1: 1 empty, 6 and 11 crowded", "2g", "HT40-", 11, map[int][]int{6: {-40}, 11: {-50}}, 11},
		{"2.4 HT40- never gets 1: silence", "2g", "HT40-", 1, nil, 6},
		{"2.4: networks under -85 dBm do not count", "2g", "HE20", 1, map[int][]int{1: {-86, -90}}, 1},
		{"2.4: a signal of -255 or below is no signal", "2g", "HE20", 1, map[int][]int{1: {-255, -300}}, 1},
		{"2.4: a signal of 0 is no signal", "2g", "HE20", 1, map[int][]int{1: {0}}, 1},
		{"5: networks under -85 dBm do not count", "5g", "HE80", 149, map[int][]int{149: {-86, -88, -90}}, 149},
		{"5 HE160: 36, even with 149 quieter", "5g", "HE160", 149, map[int][]int{36: {-40}}, 36},
		{"5 HE160: 36 on auto", "5g", "HE160", 0, nil, 36},
		{"5: auto, a tie goes to 36", "5g", "HE80", 0, nil, 36},
		{"5: auto, the less busy block", "5g", "HE80", 0, map[int][]int{36: {-50}}, 149},
		{"5: a near-tie keeps the block it is on", "5g", "HE80", 149, map[int][]int{149: {-50}, 36: {-51}}, 149},
		{"5: the other block below 2/3 wins", "5g", "HE80", 149, map[int][]int{149: {-45, -48}, 36: {-60}}, 36},
		{"5: a quiet block it is on stays", "5g", "HE80", 44, map[int][]int{149: {-80}}, 44},
		{"5 80 MHz: the primary it is on is kept in its block", "5g", "HE80", 157, map[int][]int{36: {-40}}, 157},
		{"5 80 MHz: moving block, the block's first", "5g", "HE80", 149, map[int][]int{149: {-40}, 157: {-45}}, 36},
		{"5 20 MHz: the least busy channel in the block", "5g", "HE20", 149, map[int][]int{149: {-50}, 153: {-60}, 157: {-55}, 161: {-58}, 165: {-70}, 36: {-45}, 40: {-45}, 44: {-45}, 48: {-45}}, 165},
		{"5: DFS neighbours count for neither block", "5g", "HE80", 0, map[int][]int{100: {-30}, 52: {-30}}, 36},
		{"6 GHz: no rules", "6g", "HE80", 37, nil, 0},
	} {
		if got := Recommend(tc.band, tc.htmode, tc.current, heardOf(tc.heard)); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A radio with no scan to go by keeps its fixed channel if its width allows
// it, else 6 / 36; a mesh radio keeps whatever it has.
func TestNoScanKeepsAValidChannel(t *testing.T) {
	for _, tc := range []struct {
		r    Radio
		want int
	}{
		{Radio{Band: "2g", HTMode: "HE20", Channel: 11}, 11},
		{Radio{Band: "2g", HTMode: "HT40+", Channel: 11}, 6},
		{Radio{Band: "2g", HTMode: "HE20", Channel: 0}, 6},
		{Radio{Band: "5g", HTMode: "HE80", Channel: 149}, 149},
		{Radio{Band: "5g", HTMode: "HE160", Channel: 149}, 36},
		{Radio{Band: "5g", HTMode: "HE80", Channel: 100}, 36},
		{Radio{Band: "5g", HTMode: "HE80", Channel: 0}, 36},
		{Radio{Band: "5g", HTMode: "HE80", Channel: 100, Mesh: true}, 100},
		{Radio{Band: "5g", HTMode: "HE80", Channel: 0, Mesh: true}, 0},
	} {
		if got := keepOrDefault(tc.r); got != tc.want {
			t.Errorf("%+v: %d, want %d", tc.r, got, tc.want)
		}
	}
}

const iwScanOut = `BSS 02:00:00:00:00:01(on phy1-ap0)
	last seen: 1234.567s [boottime]
	TSF: 12345 usec (0d, 00:00:00)
	freq: 5180.0
	beacon interval: 100 TUs
	signal: -48.00 dBm
	SSID: one
	BSS Load:
		 * station count: 3
BSS 02:00:00:00:00:02(on phy1-ap0)
	freq: 5745
	signal: -71.50 dBm
	SSID: two
BSS 02:00:00:00:00:03(on phy1-ap0) -- associated
	freq: 2437
	signal: -60.00 dBm
BSS 02:00:00:00:00:04(on phy1-ap0)
	freq: 6115
	signal: -60.00 dBm
`

func TestParsingIwScan(t *testing.T) {
	got := parseIwScan([]byte(iwScanOut))
	want := []Neighbour{{Channel: 36, Signal: -48}, {Channel: 149, Signal: -72}, {Channel: 6, Signal: -60}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsed %+v, want %+v", got, want)
	}
	for f, ch := range map[int]int{2412: 1, 2472: 13, 2484: 14, 5180: 36, 5825: 165, 5260: 52, 6115: 0, 2400: 0} {
		if got := channelOfFreq(f); got != ch {
			t.Errorf("%d MHz: %d, want %d", f, got, ch)
		}
	}
}

// ---- planning --------------------------------------------------------------

func plan(t *testing.T, r *router, req WifiRequest) (*Plan, error) {
	t.Helper()
	return PlanWifi(context.Background(), r.env, req)
}

// A router with a band the rules do not cover cannot be tuned — the refusal
// comes before anything is scanned or staged — but its networks can still be
// named.
func TestA6GHzRouterIsNotTunedButCanBeNamed(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, wirelessTwo+"\nconfig wifi-device 'radio2'\n\toption band '6g'\n\toption htmode 'HE80'\n")
	r.answers = map[string]string{"call network.wireless status": statusUp("radio0", "radio1", "radio2")}
	_, err := plan(t, r, WifiRequest{Tune: true})
	if !IsUnsupported(err) || err.Error() != "radio2: a 6 GHz radio — Panama's rules would switch it off" {
		t.Fatalf("tuning a 6 GHz router: %v", err)
	}
	for _, a := range r.asked {
		if strings.Contains(a, "scan") {
			t.Fatalf("scanned before refusing: %q", a)
		}
	}
	if p, err := plan(t, r, WifiRequest{Radios: map[string]WifiChange{"radio1": {SSID: str("Home")}}}); err != nil || len(p.steps) == 0 {
		t.Fatalf("naming on a 6 GHz router: %v", err)
	}
}

func TestPlanRefusesChannelsTheRadioCannotTake(t *testing.T) {
	cfg160 := strings.Replace(wirelessTwo, "option htmode 'HE80'", "option htmode 'HE160'", 1)
	cfgMesh := wirelessTwo + "\nconfig wifi-iface 'mesh1'\n\toption device 'radio1'\n\toption mode 'mesh'\n\toption encryption 'sae'\n\toption key 'mesh-key-1'\n"
	for _, tc := range []struct {
		name     string
		cfg      string
		channels map[string]int
		want     string
	}{
		{"HE160 on 149", cfg160, map[string]int{"radio1": 149}, "channels.radio1: channel 149 cannot carry 160 MHz: at 160 MHz only 36-48"},
		{"DFS", wirelessTwo, map[string]int{"radio1": 100}, "channels.radio1: channel 100 needs radar detection"},
		{"a mesh radio", cfgMesh, map[string]int{"radio1": 36}, "channels.radio1: it carries a mesh or ad-hoc interface"},
		{"an unknown radio", wirelessTwo, map[string]int{"radio9": 6}, "channels.radio9: there is no such radio"},
	} {
		r := newRouter(t)
		r.write(t, r.env.WirelessConfig, tc.cfg)
		if _, err := plan(t, r, WifiRequest{Tune: true, Channels: tc.channels}); !IsInvalid(err) || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

// Naming: a secured network keeps whether it is on, its WPA mode and its
// ieee80211w; an open one needs a key, gets psk2 and is switched on — with
// every other open interface on a radio switched on kept off.
func TestNamingFollowsTheNetworksMode(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, wirelessTwo)
	p, err := plan(t, r, WifiRequest{Radios: map[string]WifiChange{"radio1": {SSID: str("Дом-5"), Key: str("new-5g-key")}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "uci set wireless.default_radio1.ssid=Дом-5\nuci set wireless.default_radio1.key=new-5g-key"
	if got := stepsText(p.steps); got != want || len(p.Notes) != 0 {
		t.Fatalf("a secured network renamed and rekeyed:\n%s\nwant:\n%s", got, want)
	}

	// Open, off, with other open interfaces on its radio: each kept off,
	// before anything is switched on.
	cfg := strings.Replace(wirelessTwo, "\toption encryption 'none'\n\toption disabled '1'\n", "\toption encryption 'none'\n", 1) + // the guest on radio1 is on
		"\nconfig wifi-iface 'mesh0'\n\toption device 'radio0'\n\toption mode 'mesh'\n\toption encryption 'none'\n" +
		"\nconfig wifi-iface 'sta0'\n\toption device 'radio0'\n\toption mode 'sta'\n\toption encryption 'none'\n" +
		"\nconfig wifi-iface 'adhoc0'\n\toption device 'radio0'\n\toption mode 'adhoc'\n\toption encryption 'psk2'\n\toption key 'adhoc-key'\n"
	r.write(t, r.env.WirelessConfig, cfg)
	p, err = plan(t, r, WifiRequest{Radios: map[string]WifiChange{"radio0": {SSID: str("Home"), Key: str("k3y-k3y-k3y")}, "radio1": {SSID: str("Home-5G")}}})
	if err != nil {
		t.Fatal(err)
	}
	want = "uci set wireless.mesh0.disabled=1\nuci set wireless.sta0.disabled=1\n" +
		"uci set wireless.default_radio0.ssid=Home\nuci set wireless.default_radio0.encryption=psk2\nuci set wireless.default_radio0.key=k3y-k3y-k3y\n" +
		"uci set wireless.default_radio1.ssid=Home-5G\n" +
		"uci -q delete wireless.default_radio0.disabled\nuci set wireless.radio0.disabled=0"
	if got := stepsText(p.steps); got != want {
		t.Fatalf("an open radio switched on:\n%s\nwant:\n%s", got, want)
	}
	if strings.Join(p.Notes, "|") != "radio0: an open mesh interface on this radio kept off; give it a key|radio0: an open client interface on this radio kept off; give it a key" {
		t.Fatalf("notes = %q", p.Notes)
	}
	// radio1 was already on: its open guest network is left alone.
	if strings.Contains(stepsText(p.steps), "guest_radio1") {
		t.Fatal("the guest network on a radio already on was touched")
	}

	for _, tc := range []struct {
		name    string
		cfg     string
		changes map[string]WifiChange
		want    string
	}{
		{"an open network without a key", wirelessTwo, map[string]WifiChange{"radio0": {SSID: str("Home")}}, "radios.radio0.key: the network is open — give it a key"},
		{"a key where there is no passphrase", strings.Replace(wirelessTwo, "'psk-mixed'", "'owe'", 1),
			map[string]WifiChange{"radio1": {SSID: str("x"), Key: str("S3CRET!!-key")}}, "radios.radio1.key: its network uses \"owe\""},
		{"no access point", wirelessTwo + "\nconfig wifi-device 'radio2'\n\toption band '5g'\n", map[string]WifiChange{"radio2": {SSID: str("x"), Key: str("12345678")}}, "radios.radio2: it carries no access point"},
		{"an unknown radio", wirelessTwo, map[string]WifiChange{"radio7": {SSID: str("x"), Key: str("12345678")}}, "radios.radio7: there is no such radio"},
		{"no name", wirelessTwo, map[string]WifiChange{"radio1": {Key: str("12345678")}}, "radios.radio1.ssid"},
		{"a name too long", wirelessTwo, map[string]WifiChange{"radio1": {SSID: str(strings.Repeat("s", 33))}}, "radios.radio1.ssid"},
		{"a short key", wirelessTwo, map[string]WifiChange{"radio0": {SSID: str("x"), Key: str("S3CRET!")}}, "radios.radio0.key: the key must be 8-63"},
		{"a key that is not ASCII", wirelessTwo, map[string]WifiChange{"radio0": {SSID: str("x"), Key: str("ключ-ключ-ключ")}}, "radios.radio0.key: the key must be 8-63"},
		{"nothing to set", wirelessTwo, map[string]WifiChange{}, "no radio to set"},
	} {
		r := newRouter(t)
		r.write(t, r.env.WirelessConfig, tc.cfg)
		_, err := plan(t, r, WifiRequest{Radios: tc.changes})
		if !IsInvalid(err) || !strings.HasPrefix(err.Error(), tc.want) || len(r.commands()) != 0 {
			t.Errorf("%s: %v", tc.name, err)
		}
		if err != nil && (strings.Contains(err.Error(), "S3CRET") || strings.Contains(err.Error(), "ключ")) {
			t.Errorf("%s: the refusal quotes the key: %v", tc.name, err)
		}
	}
}

func stepsText(steps []step) string {
	var out []string
	for _, s := range steps {
		out = append(out, "uci "+strings.Join(s.args, " "))
	}
	return strings.Join(out, "\n")
}

// The tuning: Panama, the txpower and every access point's vif_txpower
// removed, a fixed channel; htmode and the rest untouched; the names in the
// same transaction, every switch-on last.
func TestTheTuningAndItsOrder(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, wirelessTwo)
	p, err := plan(t, r, WifiRequest{Tune: true, Channels: map[string]int{"radio0": 11, "radio1": 149},
		Radios: map[string]WifiChange{"radio0": {SSID: str("Vectra-6D39"), Key: str("k3y-k3y-k3y")}, "radio1": {SSID: str("Vectra-6D39")}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "uci set wireless.radio0.country=PA\nuci -q delete wireless.radio0.txpower\nuci set wireless.radio0.channel=11\n" +
		"uci -q delete wireless.default_radio0.vif_txpower\n" +
		"uci set wireless.radio1.country=PA\nuci -q delete wireless.radio1.txpower\nuci set wireless.radio1.channel=149\n" +
		"uci -q delete wireless.default_radio1.vif_txpower\nuci -q delete wireless.guest_radio1.vif_txpower\n" +
		"uci set wireless.default_radio0.ssid=Vectra-6D39\nuci set wireless.default_radio0.encryption=psk2\n" +
		"uci set wireless.default_radio0.key=k3y-k3y-k3y\nuci set wireless.default_radio1.ssid=Vectra-6D39\n" +
		"uci -q delete wireless.default_radio0.disabled\nuci set wireless.radio0.disabled=0"
	if got := stepsText(p.steps); got != want || len(p.Notes) != 0 {
		t.Fatalf("renamed and tuned: %q\n%s\nwant:\n%s", p.Notes, got, want)
	}
	if strings.Contains(stepsText(p.steps), "htmode") || strings.Contains(stepsText(p.steps), "ieee80211w") {
		t.Fatal("touched htmode or ieee80211w")
	}

	// radio0's network is open, off and not given a key: it stays off — its
	// radio is still tuned — and the answer says so.
	p, err = plan(t, r, WifiRequest{Tune: true, Channels: map[string]int{"radio0": 1, "radio1": 36}})
	if got := stepsText(p.steps); err != nil || len(p.Notes) != 1 || p.Notes[0] != "radio0: open network left disabled; give it a key" ||
		strings.Contains(got, "radio0.disabled") || strings.Contains(got, "default_radio0.ssid") || !strings.Contains(got, "wireless.radio0.channel=1") {
		t.Fatalf("an open network off: %v %q\n%s", err, p.Notes, got)
	}

	// On and open with no key in the request: refused, nothing scanned.
	r2 := newRouter(t)
	r2.write(t, r2.env.WirelessConfig, strings.Replace(wirelessTwo, "\toption country 'US'\n\toption disabled '1'\n", "\toption country 'US'\n", 1))
	for name, changes := range map[string]map[string]WifiChange{
		"no names":                   nil,
		"radio0 named without a key": {"radio0": {SSID: str("Home")}},
	} {
		if _, err := plan(t, r2, WifiRequest{Tune: true, Radios: changes}); !IsInvalid(err) ||
			err.Error() != "radios.radio0.key: the network is open — give it a key" {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, a := range r2.asked {
		if strings.Contains(a, "scan") {
			t.Fatalf("scanned before refusing: %q", a)
		}
	}

	// A mesh radio keeps its channel, whatever the scan says.
	r3 := newRouter(t)
	r3.write(t, r3.env.WirelessConfig, wirelessTwo+"\nconfig wifi-iface 'mesh1'\n\toption device 'radio1'\n\toption mode 'mesh'\n\toption encryption 'sae'\n\toption key 'mesh-key-1'\n")
	p, err = plan(t, r3, WifiRequest{Tune: true})
	if err != nil || strings.Contains(stepsText(p.steps), "radio1.channel") || !strings.Contains(stepsText(p.steps), "radio1.country=PA") {
		t.Fatalf("a mesh radio: %v\n%s", err, stepsText(p.steps))
	}
}

// optimize_wifi scans only what is up, one radio after the other; a radio
// that heard nothing is asked once more; the scan is kept for wifi_scan.
func TestTheScanOnlyTouchesWhatIsUp(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, strings.Replace(wirelessTwo, "\toption country 'US'\n\toption disabled '1'\n", "\toption country 'US'\n", 1))
	emptyScans := 0
	r.answers = map[string]string{
		"call network.wireless status":           statusUp("radio0", "radio1"),
		`call iwinfo info {"device":"phy0-ap0"}`: `{"channel":1}`,
		`call iwinfo info {"device":"phy1-ap0"}`: `{"channel":36}`,
		`call iwinfo scan {"device":"phy0-ap0"}`: `{"results":[{"channel":1,"signal":-40},{"channel":1,"signal":-50},{"channel":6,"signal":-45},{"channel":3,"signal":-300}]}`,
	}
	r.dynamic = map[string]func() (string, error){
		`call iwinfo scan {"device":"phy1-ap0"}`: func() (string, error) { emptyScans++; return `{"results":[]}`, nil },
	}
	_, err := plan(t, r, WifiRequest{Tune: true, Radios: map[string]WifiChange{"radio0": {SSID: str("Home"), Key: str("k3y-k3y-k3y")}}})
	if err != nil {
		t.Fatal(err)
	}
	scan := LoadScan(r.env)
	if scan == nil || len(scan.Radios) != 2 || !scan.ScannedAt.Equal(r.now) {
		t.Fatalf("no scan kept: %+v", scan)
	}
	s0, s1 := scan.Radios[0], scan.Radios[1]
	wantCh := []ChannelUse{{Channel: 1, Networks: 2, Strongest: -40}, {Channel: 3, Networks: 1, Strongest: -100}, {Channel: 6, Networks: 1, Strongest: -45}}
	if s0.Err != "" || s0.Current != 1 || s0.Networks != 4 || s0.Recommended != 11 || !reflect.DeepEqual(s0.Channels, wantCh) {
		t.Errorf("radio0 = %+v", s0)
	}
	// radio1 (80 MHz, fixed 36) heard nothing, twice: it keeps 36.
	if s1.Err != ScanEmpty || s1.Recommended != 36 || emptyScans != 2 {
		t.Errorf("radio1 = %+v after %d scans", s1, emptyScans)
	}
	// iw was tried first on 5 GHz, with the non-DFS frequencies only.
	var iw string
	for _, a := range r.asked {
		if strings.HasPrefix(a, "iw ") {
			iw = a
		}
	}
	if iw != "iw dev phy1-ap0 scan freq 5180 5200 5220 5240 5745 5765 5785 5805 5825 ap-force" {
		t.Errorf("iw = %q", iw)
	}

	// Off radios are not scanned; a failed scan says so; iw is used when it answers.
	r2 := newRouter(t)
	r2.write(t, r2.env.WirelessConfig, strings.Replace(wirelessTwo, "option channel '36'", "option channel '100'", 1))
	r2.answers = map[string]string{
		"call network.wireless status": `{"radio0":{"up":false,"interfaces":[]},"radio1":{"up":true,"interfaces":[{"ifname":"phy1-ap0","config":{"mode":"ap"}}]}}`,
		"iw dev phy1-ap0 scan freq 5180 5200 5220 5240 5745 5765 5785 5805 5825 ap-force": iwScanOut,
	}
	_, err = plan(t, r2, WifiRequest{Tune: true})
	if err != nil {
		t.Fatal(err)
	}
	scan = LoadScan(r2.env)
	if s0 := scan.Radios[0]; s0.Err != ScanOff || s0.Recommended != 6 {
		t.Errorf("an off radio: %+v", s0)
	}
	// radio1 is on 100 (radar detection), 80 MHz: the quieter block, 149.
	if s1 := scan.Radios[1]; s1.Err != "" || s1.Networks != 3 || s1.Current != 100 || s1.Recommended != 149 {
		t.Errorf("radio1 through iw: %+v", s1)
	}
	for _, a := range r2.asked {
		if strings.Contains(a, "phy0") || strings.Contains(a, `iwinfo scan {"device":"phy1-ap0"}`) {
			t.Errorf("asked %q", a)
		}
	}

	r3 := newRouter(t)
	r3.write(t, r3.env.WirelessConfig, wirelessTwo)
	r3.answers = map[string]string{"call network.wireless status": statusUp("radio0", "radio1")}
	r3.dynamic = map[string]func() (string, error){
		`call iwinfo scan {"device":"phy1-ap0"}`: func() (string, error) { return "", errors.New("Command failed: Timeout") },
	}
	if _, err := plan(t, r3, WifiRequest{Tune: true, Channels: map[string]int{"radio0": 6}}); err != nil {
		t.Fatal(err)
	}
	if s1 := LoadScan(r3.env).Radios[1]; s1.Err != ScanFailed || s1.Recommended != 36 {
		t.Errorf("a failed scan: %+v", s1)
	}

	// No netifd: a radio the configuration has on may well be up — failed,
	// not off.
	r4 := newRouter(t)
	r4.write(t, r4.env.WirelessConfig, wirelessTwo)
	if _, err := plan(t, r4, WifiRequest{Tune: true}); err != nil {
		t.Fatal(err)
	}
	if sc := LoadScan(r4.env); sc.Radios[0].Err != ScanOff || sc.Radios[1].Err != ScanFailed || sc.Radios[0].Recommended != 6 || sc.Radios[1].Recommended != 36 {
		t.Errorf("no netifd: %+v", sc.Radios)
	}
}

// ---- staging, the lock, the commit -----------------------------------------

func TestStagingIsPrivateAndCommitSaysApplyingFirst(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, wirelessTwo)
	p, err := plan(t, r, WifiRequest{Radios: map[string]WifiChange{"radio0": {SSID: str("Home"), Key: str("k3y-k3y-k3y")}}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := Stage(context.Background(), r.env, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.commands() {
		if !strings.HasPrefix(c, "uci -t "+st.dir+" ") {
			t.Fatalf("staged outside its own save directory: %q", c)
		}
	}
	if fi, err := os.Stat(r.env.WifiJob); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the job (it holds the keys) = %v %v", fi, err)
	}
	var job wifiJob
	raw, _ := os.ReadFile(r.env.WifiJob)
	if json.Unmarshal(raw, &job) != nil || !job.Had || string(job.Wireless) != wirelessTwo {
		t.Fatalf("the wireless file was not kept aside: %+v", job)
	}

	// The last change's result is there; Commit replaces it with "applying"
	// before it commits.
	_ = writeApply(r.env, ApplyState{State: ApplyOK, At: r.now.Add(-time.Hour), Radios: map[string]bool{"radio1": true}})
	r.onRun = func(args []string) {
		if len(args) > 3 && args[3] == "commit" {
			if a := LoadApply(r.env); a == nil || a.State != ApplyApplying && a.State != ApplyUnverified {
				t.Errorf("committing while the state says %+v", a)
			}
		}
	}
	if err := st.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(r.env.WifiApply)
	var a ApplyState
	if json.Unmarshal(raw, &a) != nil || a.State != ApplyApplying || !a.At.Equal(r.now) || len(a.Radios) != 0 {
		t.Fatalf("after Commit: %s", raw)
	}
	if last := r.commands()[len(r.commands())-1]; last != "uci -t "+st.dir+" commit wireless" {
		t.Fatalf("last = %q", last)
	}
	if _, err := os.Stat(st.dir); !os.IsNotExist(err) {
		t.Fatal("the save directory outlived the commit")
	}

	// A failed commit puts the last state back and leaves nothing staged.
	r2 := newRouter(t)
	r2.write(t, r2.env.WirelessConfig, wirelessTwo)
	prev := ApplyState{State: ApplyOK, At: r2.now.Add(-time.Hour), Radios: map[string]bool{"radio1": true}}
	_ = writeApply(r2.env, prev)
	p, _ = plan(t, r2, WifiRequest{Radios: map[string]WifiChange{"radio1": {SSID: str("Home")}}})
	st2, err := Stage(context.Background(), r2.env, p)
	if err != nil {
		t.Fatal(err)
	}
	r2.fail = func(args []string) bool { return len(args) > 3 && args[3] == "commit" }
	if err := st2.Commit(context.Background()); err == nil {
		t.Fatal("a failed commit went through")
	}
	if a := LoadApply(r2.env); a == nil || a.State != ApplyOK || !a.At.Equal(prev.At) {
		t.Fatalf("after a failed commit: %+v", a)
	}
	if _, err := os.Stat(st2.dir); !os.IsNotExist(err) {
		t.Fatal("a failed commit left its save directory")
	}
	if _, err := os.Stat(r2.env.WifiJob); !os.IsNotExist(err) {
		t.Fatal("a failed commit left the job (and its keys)")
	}
}

// uci reads its default save directory on every commit, whatever -t says: a
// Wi-Fi change someone left uncommitted there refuses this one — at the plan,
// and at the commit if it appeared meanwhile — before anything is committed.
func TestAChangeLeftUncommittedInUCIRefusesTheChange(t *testing.T) {
	r := newRouter(t)
	r.write(t, r.env.WirelessConfig, wirelessTwo)
	req := WifiRequest{Radios: map[string]WifiChange{"radio1": {SSID: str("Home")}}}
	pending := filepath.Join(r.env.UCISaveDir, "wireless")
	r.write(t, pending, "wireless.radio1.channel='1'\n")
	if _, err := plan(t, r, req); !errors.Is(err, ErrPending) || len(r.commands()) != 0 {
		t.Fatalf("with a change pending: %v, commands %q", err, r.commands())
	}
	r.write(t, pending, "") // committed or reverted: uci leaves the file empty
	p, err := plan(t, r, req)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Stage(context.Background(), r.env, p)
	if err != nil {
		t.Fatal(err)
	}
	prev := ApplyState{State: ApplyOK, At: r.now.Add(-time.Hour), Radios: map[string]bool{}}
	_ = writeApply(r.env, prev)
	r.write(t, pending, "wireless.radio1.channel='1'\n") // since the plan
	if err := st.Commit(context.Background()); !errors.Is(err, ErrPending) {
		t.Fatalf("a change pending at the commit: %v", err)
	}
	for _, c := range r.commands() {
		if strings.HasSuffix(c, "commit wireless") {
			t.Fatalf("committed: %q", c)
		}
	}
	if a := LoadApply(r.env); a == nil || a.State != ApplyOK || !a.At.Equal(prev.At) {
		t.Fatalf("the last restart's state: %+v", a)
	}
	if _, err := os.Stat(st.dir); !os.IsNotExist(err) {
		t.Fatal("the save directory was left behind")
	}
	if _, err := os.Stat(r.env.WifiJob); !os.IsNotExist(err) {
		t.Fatal("the job (and its keys) was left behind")
	}
}

func TestTheWifiLockIsExclusive(t *testing.T) {
	r := newRouter(t)
	lock, err := LockWifi(r.env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockWifi(r.env); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second lock: %v", err)
	}
	if !HoldsWifiLock(r.env, lock) {
		t.Fatal("the holder's own descriptor is not the lock")
	}
	stray, _ := os.CreateTemp(t.TempDir(), "stray")
	if HoldsWifiLock(r.env, stray) {
		t.Fatal("a stray file was taken for the lock")
	}
	lock.Close()
	if l, err := LockWifi(r.env); err != nil {
		t.Fatalf("after release: %v", err)
	} else {
		l.Close()
	}
}

// ---- the restart and its check ---------------------------------------------

// applyRouter is a router with a change committed: the job (radios up
// before, the old wireless file) and the new wireless file in place.
func applyRouter(t *testing.T, oldCfg, newCfg string, before map[string]bool) *router {
	t.Helper()
	r := newRouter(t)
	raw, _ := json.Marshal(wifiJob{Before: before, Wireless: []byte(oldCfg), Mode: 0o600, Had: true})
	r.write(t, r.env.WifiJob, string(raw))
	r.write(t, r.env.WirelessConfig, newCfg)
	return r
}

// bothOn is wirelessTwo with both radios and their networks on and secured.
var bothOn = strings.Replace(strings.Replace(wirelessTwo, "\toption country 'US'\n\toption disabled '1'\n", "\toption country 'US'\n", 1),
	"option ssid 'OpenWrt'\n\toption encryption 'none'", "option ssid 'OpenWrt'\n\toption encryption 'psk2'\n\toption key 'k3y-k3y-k3y'", 1)

func TestTheRestartIsCheckedAndRolledBack(t *testing.T) {
	ctx := context.Background()

	// ok: every radio on in the new configuration comes up.
	r := applyRouter(t, wirelessTwo, bothOn, map[string]bool{"radio1": true})
	r.answers = map[string]string{"call network.wireless status": statusUp("radio0", "radio1")}
	a := ApplyWifi(ctx, r.env)
	if a.State != ApplyOK || !reflect.DeepEqual(a.Radios, map[string]bool{"radio0": true, "radio1": true}) ||
		!reflect.DeepEqual(r.commands(), []string{"wifi reload"}) {
		t.Fatalf("ok: %+v, commands %q", a, r.commands())
	}
	if _, err := os.Stat(r.env.WifiJob); !os.IsNotExist(err) {
		t.Fatal("the job (and its keys) outlived the restart")
	}
	if got := LoadApply(r.env); got == nil || got.State != ApplyOK {
		t.Fatalf("written: %+v", got)
	}

	// rolled_back: radio1 was up before and is not now — the old file goes
	// back, a second reload, and radio1 is up again.
	r = applyRouter(t, wirelessTwo, bothOn, map[string]bool{"radio1": true})
	reloads := 0
	r.onRun = func(args []string) {
		if strings.Join(args, " ") == "wifi reload" {
			reloads++
		}
	}
	r.dynamic = map[string]func() (string, error){"call network.wireless status": func() (string, error) {
		if reloads < 2 {
			return statusUp("radio0"), nil // radio1 does not come up with the change
		}
		return statusUp("radio1"), nil // the old settings: radio0 is off in them
	}}
	a = ApplyWifi(ctx, r.env)
	back, _ := os.ReadFile(r.env.WirelessConfig)
	if a.State != ApplyRolledBack || string(back) != wirelessTwo || reloads != 2 || !reflect.DeepEqual(a.Radios, map[string]bool{"radio1": true}) ||
		!strings.Contains(a.Detail, "radio1 did not come up with the new settings; the previous settings are back") {
		t.Fatalf("rolled_back: %+v, %d reloads, file back=%v", a, reloads, string(back) == wirelessTwo)
	}
	if strings.Contains(a.Detail, "k3y") || strings.Contains(a.Detail, "old-5g-key") {
		t.Fatal("a key in the detail")
	}

	// partial: radio0 comes up, radio1 does not — and radio1 was not up
	// before either: no rollback.
	r = applyRouter(t, wirelessTwo, bothOn, map[string]bool{})
	r.answers = map[string]string{"call network.wireless status": statusUp("radio0")}
	a = ApplyWifi(ctx, r.env)
	if a.State != ApplyPartial || !reflect.DeepEqual(a.Radios, map[string]bool{"radio0": true, "radio1": false}) ||
		!reflect.DeepEqual(r.commands(), []string{"wifi reload"}) || !r.now.After(time.Date(2026, 9, 28, 7, 0, 44, 0, time.UTC)) {
		t.Fatalf("partial: %+v, commands %q, waited until %s", a, r.commands(), r.now)
	}

	// unverified: nothing came up, and nothing was up before (a fresh box, no
	// phy) — netifd gave up, so no need to wait out the 45 s.
	r = applyRouter(t, wirelessTwo, bothOn, map[string]bool{"radio0": false, "radio1": false})
	r.answers = map[string]string{"call network.wireless status": `{"radio0":{"up":false,"retry_setup_failed":true,"interfaces":[]},"radio1":{"up":false,"retry_setup_failed":true,"interfaces":[]}}`}
	a = ApplyWifi(ctx, r.env)
	if a.State != ApplyUnverified || len(r.commands()) != 1 || r.now.After(time.Date(2026, 9, 28, 7, 0, 10, 0, time.UTC)) {
		t.Fatalf("unverified (no phy): %+v, commands %q, at %s", a, r.commands(), r.now)
	}

	// unverified: no network.wireless at all (the stand) — given up after a
	// few seconds, never rolled back.
	r = applyRouter(t, wirelessTwo, bothOn, map[string]bool{"radio1": true})
	a = ApplyWifi(ctx, r.env)
	if a.State != ApplyUnverified || len(r.commands()) != 1 || r.now.After(time.Date(2026, 9, 28, 7, 0, 12, 0, time.UTC)) {
		t.Fatalf("unverified (no netifd): %+v, commands %q, at %s", a, r.commands(), r.now)
	}

	// failed: a rollback is due but the old file cannot be put back.
	r = applyRouter(t, wirelessTwo, bothOn, map[string]bool{"radio1": true})
	raw, _ := json.Marshal(wifiJob{Before: map[string]bool{"radio1": true}, Had: false})
	r.write(t, r.env.WifiJob, string(raw))
	r.answers = map[string]string{"call network.wireless status": statusUp("radio0")}
	a = ApplyWifi(ctx, r.env)
	if a.State != ApplyFailed || !strings.Contains(a.Detail, "radio1 did not come up") || len(r.commands()) != 1 {
		t.Fatalf("a failed restore: %+v, commands %q", a, r.commands())
	}
	// The wireless file's directory turns into a file once the new settings
	// are read: the old ones cannot be written back.
	r = applyRouter(t, wirelessTwo, bothOn, map[string]bool{"radio1": true})
	etc := filepath.Join(t.TempDir(), "etc")
	r.env.WirelessConfig = filepath.Join(etc, "wireless")
	r.write(t, r.env.WirelessConfig, bothOn)
	r.dynamic = map[string]func() (string, error){"call network.wireless status": func() (string, error) {
		if fi, err := os.Stat(etc); err == nil && fi.IsDir() {
			_ = os.RemoveAll(etc)
			_ = os.WriteFile(etc, []byte("not a directory"), 0o600)
		}
		return statusUp("radio0"), nil
	}}
	a = ApplyWifi(ctx, r.env)
	if a.State != ApplyFailed || !strings.Contains(a.Detail, "could not be put back") {
		t.Fatalf("an unwritable restore: %+v", a)
	}
}

// A state still "applying" that nobody is checking is a check that died.
func TestAnApplyingStateWithNobodyCheckingIsUnverified(t *testing.T) {
	r := newRouter(t)
	_ = writeApply(r.env, ApplyState{State: ApplyApplying, At: r.now})
	lock, err := LockWifi(r.env)
	if err != nil {
		t.Fatal(err)
	}
	if a := LoadApply(r.env); a == nil || a.State != ApplyApplying {
		t.Fatalf("while the lock is held: %+v", a)
	}
	lock.Close()
	if a := LoadApply(r.env); a == nil || a.State != ApplyUnverified || a.Detail != "the check stopped before it finished" {
		t.Fatalf("with nobody checking: %+v", a)
	}
	if LoadApply(newRouter(t).env) != nil {
		t.Fatal("a state before any change")
	}
}
