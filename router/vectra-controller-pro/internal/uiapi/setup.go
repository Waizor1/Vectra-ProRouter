package uiapi

import (
	"time"

	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/setup"
)

// Setup answers `setup`: what the setup wizard shows (ui/contract/setup.json).
type Setup struct {
	Done bool `json:"done"`
	// PasswordSet: root has a password, so LuCI's login asks for one; nil =
	// the router cannot tell. Never the password or its hash.
	PasswordSet *bool       `json:"passwordSet"`
	Wan         SetupWan    `json:"wan"`
	Lan         SetupLan    `json:"lan"`
	Wifi        SetupWifi   `json:"wifi"`
	Vectra      SetupVectra `json:"vectra"`
}

// SetupLan is how the home network reaches the router: IPv4, where this page
// always opens (nil: netifd cannot say).
type SetupLan struct {
	IPv4 *string `json:"ipv4"`
}

// SetupWan is the internet connection as the router has it: read-only (the
// router sets it up itself).
type SetupWan struct {
	Proto   string   `json:"proto"`
	Link    *bool    `json:"link"`
	IPv4    *string  `json:"ipv4"`
	Gateway *string  `json:"gateway"`
	DNS     []string `json:"dns"`
}

// SetupWifi is every radio, whether the tuning can apply and is in place,
// the router's own network name, and the last change's restart.
type SetupWifi struct {
	Radios    []SetupRadio `json:"radios"`
	Tuned     *bool        `json:"tuned"`
	Tunable   bool         `json:"tunable"`
	Suggested *string      `json:"suggested"`
	Apply     *SetupApply  `json:"apply"`
}

// SetupRadio is one radio and its first access point — never a key.
type SetupRadio struct {
	Device   string  `json:"device"`
	Band     *string `json:"band"`
	Channel  *int    `json:"channel"`
	Auto     bool    `json:"auto"`
	HTMode   *string `json:"htmode"`
	Width    *int    `json:"width"`
	Country  *string `json:"country"`
	TxPower  *int    `json:"txpower"`
	MaxPower bool    `json:"maxPower"`
	SSID     *string `json:"ssid"`
	Secured  bool    `json:"secured"`
	Enabled  bool    `json:"enabled"`
	AP       bool    `json:"ap"`
	Mesh     bool    `json:"mesh"`
	Up       *bool   `json:"up"`
}

// SetupApply is the last Wi-Fi change's restart, as the helper judged it.
type SetupApply struct {
	State  string          `json:"state"`
	At     string          `json:"at"`
	Detail *string         `json:"detail"`
	Radios map[string]bool `json:"radios"`
}

// WifiScan answers `wifi_scan` (ui/contract/wifi_scan.json): the air as the
// last optimize_wifi heard it.
type WifiScan struct {
	Radios    []ScanRadio `json:"radios"`
	ScannedAt *string     `json:"scannedAt"`
}

type ScanRadio struct {
	Device      string        `json:"device"`
	Band        *string       `json:"band"`
	Current     *int          `json:"current"`
	Recommended *int          `json:"recommended"`
	Networks    int           `json:"networks"`
	Channels    []ScanChannel `json:"channels"`
	Error       *string       `json:"error"`
}

type ScanChannel struct {
	Channel   int `json:"channel"`
	Networks  int `json:"networks"`
	Strongest int `json:"strongest"`
}

// BuildWifiScan answers `wifi_scan` from the last scan (nil: none yet).
func BuildWifiScan(scan *setup.ScanResult) WifiScan {
	out := WifiScan{Radios: []ScanRadio{}}
	if scan == nil {
		return out
	}
	out.ScannedAt = strPtr(scan.ScannedAt.UTC().Format(time.RFC3339))
	for _, s := range scan.Radios {
		r := ScanRadio{Device: s.Device, Band: strPtr(s.Band), Current: intPtr(s.Current), Recommended: intPtr(s.Recommended),
			Networks: s.Networks, Channels: make([]ScanChannel, 0, len(s.Channels)), Error: strPtr(s.Err)}
		for _, c := range s.Channels {
			r.Channels = append(r.Channels, ScanChannel{Channel: c.Channel, Networks: c.Networks, Strongest: c.Strongest})
		}
		out.Radios = append(out.Radios, r)
	}
	return out
}

func buildWifi(w setup.Wifi) SetupWifi {
	out := SetupWifi{Radios: make([]SetupRadio, 0, len(w.Radios)), Tuned: w.Tuned(), Tunable: w.Tunable(), Suggested: strPtr(w.Suggested)}
	for _, r := range w.Radios {
		out.Radios = append(out.Radios, SetupRadio{Device: r.Device, Band: strPtr(r.Band), Channel: intPtr(r.Channel),
			Auto: r.Channel == 0, HTMode: strPtr(r.HTMode), Width: intPtr(r.Width), Country: strPtr(r.Country), TxPower: r.TxPower,
			MaxPower: r.MaxPower, SSID: strPtr(r.SSID), Secured: r.Secured, Enabled: r.Enabled, AP: r.AP, Mesh: r.Mesh, Up: r.Up})
	}
	if a := w.Apply; a != nil {
		radios := a.Radios
		if radios == nil {
			radios = map[string]bool{}
		}
		out.Apply = &SetupApply{State: a.State, At: a.At.UTC().Format(time.RFC3339), Detail: strPtr(a.Detail), Radios: radios}
	}
	return out
}

// intPtr is n, or nil for 0.
func intPtr(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

type SetupVectra struct {
	Linked      bool        `json:"linked"`
	BotUsername *string     `json:"botUsername"`
	Owner       *ClaimOwner `json:"owner"`
	Claim       *SetupClaim `json:"claim"`
}

type SetupClaim struct {
	State     string      `json:"state"`
	Code      string      `json:"code"`
	QR        *string     `json:"qr"`
	ExpiresAt string      `json:"expiresAt"`
	BotURL    *string     `json:"botUrl"`
	Owner     *ClaimOwner `json:"owner"`
}

type ClaimOwner struct {
	Label string `json:"label"`
}

// WanCheck answers `wan_check` (ui/contract/wan_check.json).
type WanCheck struct {
	Link      *bool   `json:"link"`
	IPv4      *string `json:"ipv4"`
	DNS       bool    `json:"dns"`
	Internet  bool    `json:"internet"`
	Panel     bool    `json:"panel"`
	CheckedAt string  `json:"checkedAt"`
}

// BuildWanCheck answers `wan_check`.
func BuildWanCheck(w setup.Wan, r setup.Result, now time.Time) WanCheck {
	return WanCheck{Link: w.Link, IPv4: strPtr(w.IPv4), DNS: r.DNS, Internet: r.Internet, Panel: r.Panel,
		CheckedAt: now.UTC().Format(time.RFC3339)}
}

// BuildSetup answers `setup` from the router's facts, the daemon's claim (rt
// nil: the daemon is down), whether the router is linked, and what the daemon
// kept of the panel's word: the bot username and the owner (nil: none).
// Support goes to the panel's bot, else to the box's own (f.SupportBot); the
// claim's link only ever to the panel's — a code means something only there.
func BuildSetup(f setup.Facts, rt *localctl.Runtime, linked bool, bot string, owner *ClaimOwner) Setup {
	s := Setup{Done: f.Done, PasswordSet: f.Password, Lan: SetupLan{IPv4: strPtr(f.Lan.IPv4)}}
	w := f.Wan
	s.Wan = SetupWan{Proto: w.Proto, Link: w.Link, IPv4: strPtr(w.IPv4), Gateway: strPtr(w.Gateway), DNS: nonNil(w.DNS)}
	s.Wifi = buildWifi(f.Wifi)
	support := bot
	if support == "" {
		support = f.SupportBot
	}
	s.Vectra = SetupVectra{Linked: linked, BotUsername: strPtr(support), Owner: owner}
	if !linked && rt != nil && rt.Claim != nil {
		c := rt.Claim
		sc := &SetupClaim{State: c.State, Code: c.Code, QR: strPtr(c.QR), ExpiresAt: c.ExpiresAt.UTC().Format(time.RFC3339)}
		if bot != "" {
			sc.BotURL = strPtr("https://t.me/" + bot + "?start=rt_" + c.Code)
		}
		if c.Owner != nil {
			sc.Owner = &ClaimOwner{Label: c.Owner.Label}
		}
		s.Vectra.Claim = sc
	}
	return s
}
