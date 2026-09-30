package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"vectra-controller-pro/internal/localctl"
)

// A Wi-Fi change (set_wifi, optimize_wifi) runs under one exclusive lock from
// reading the router to the end of the restart that proves it:
//
//  1. Plan: read, validate (a refusal changes nothing), scan (optimize_wifi,
//     radios that are up), decide the channels, build the uci steps.
//  2. Stage: the steps go into a private uci save directory — dropping it
//     takes them all back — and /etc/config/wireless is kept aside for a
//     rollback. `uci -t` adds a save directory; it does not leave out uci's
//     default one (/tmp/.uci), which every commit reads: a change someone left
//     uncommitted there would go live with this one, so it refuses the change
//     (ErrPending) — before the plan, and again just before the commit. The
//     changes LuCI stages are rpcd's, per session, and never read here.
//  3. Commit, with the restart's state set to "applying" first; then the
//     detached helper (wifiapply.go), which holds the lock, restarts the
//     Wi-Fi, checks it came up, and rolls back if a radio that was up is not.

// ErrBusy is a Wi-Fi change still being applied (code busy).
var ErrBusy = errors.New("another Wi-Fi change is still being applied")

// ErrPending is a Wi-Fi change someone left uncommitted in uci's default save
// directory, which a commit would take along (code busy).
var ErrPending = errors.New("uncommitted Wi-Fi changes are waiting in uci (uci changes wireless): " +
	"commit or revert them first — they would go live with this change")

// pendingUCI: uci's default save directory holds changes to the wireless
// config.
func pendingUCI(env Env) bool {
	if env.UCISaveDir == "" {
		return false
	}
	fi, err := os.Stat(filepath.Join(env.UCISaveDir, "wireless"))
	return err == nil && fi.Size() > 0
}

// Invalid is a request the router refuses as asked (invalid_params). Its
// message never quotes a key.
type Invalid struct{ msg string }

func (e Invalid) Error() string { return e.msg }

func invalidf(format string, a ...interface{}) error { return Invalid{fmt.Sprintf(format, a...)} }

// IsInvalid reports whether err is the router refusing the request as asked.
func IsInvalid(err error) bool {
	var e Invalid
	return errors.As(err, &e)
}

// Unsupported is a router the tuning cannot cover (code unsupported).
type Unsupported struct{ msg string }

func (e Unsupported) Error() string { return e.msg }

// IsUnsupported reports whether err is a router the tuning cannot cover.
func IsUnsupported(err error) bool {
	var e Unsupported
	return errors.As(err, &e)
}

// LockWifi takes the Wi-Fi lock without waiting: ErrBusy when a change holds
// it. It lives as long as the returned file stays open — here, or in the
// helper it is handed to.
func LockWifi(env Env) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(env.WifiLock), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(env.WifiLock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, err
	}
	return f, nil
}

// HoldsWifiLock reports whether f is the Wi-Fi lock file and the lock is held
// on it: taking it again on the same descriptor is a no-op, on another
// holder's it fails.
func HoldsWifiLock(env Env, f *os.File) bool {
	if f == nil {
		return false
	}
	have, err := f.Stat()
	if err != nil {
		return false
	}
	want, err := os.Stat(env.WifiLock)
	if err != nil || !os.SameFile(have, want) {
		return false
	}
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

func wifiLockHeld(env Env) bool {
	f, err := LockWifi(env)
	if err != nil {
		return errors.Is(err, ErrBusy)
	}
	f.Close()
	return false
}

// WifiChange is a new name for one radio's network and, optionally, a key.
type WifiChange struct {
	SSID *string `json:"ssid"`
	Key  *string `json:"key"`
}

// WifiRequest is set_wifi (Radios) or optimize_wifi (Tune, Channels, Radios).
type WifiRequest struct {
	Tune     bool
	Channels map[string]int
	Radios   map[string]WifiChange
}

// Plan is a validated change: nothing on the router has changed yet.
type Plan struct {
	Notes  []string        // for the answer's detail
	before map[string]bool // radios up before the change (netifd)
	steps  []step
}

// validSSID: 1-32 bytes of text.
func validSSID(s string) bool {
	return len(s) >= 1 && len(s) <= 32 && utf8.ValidString(s) && !hasControl(s)
}

// validKey: a WPA passphrase, 8-63 printable ASCII characters.
func validKey(k string) bool {
	if len(k) < 8 || len(k) > 63 {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x20 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

// passphraseMode reports whether enc takes a passphrase: psk, psk2,
// psk-mixed, sae or sae-mixed, with or without a +cipher suffix.
func passphraseMode(enc string) bool {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(enc)), "+")
	switch base {
	case "psk", "psk2", "psk-mixed", "sae", "sae-mixed":
		return true
	}
	return false
}

func openNeedsKey(dev string) error {
	return invalidf("radios.%s.key: the network is open — give it a key", dev)
}

// PlanWifi reads the router and checks req against it. A refusal leaves
// everything as it was. optimize_wifi scans the radios that are up — the
// person asking has agreed to a Wi-Fi restart — and keeps what it heard for
// wifi_scan.
func PlanWifi(ctx context.Context, env Env, req WifiRequest) (*Plan, error) {
	if pendingUCI(env) {
		return nil, ErrPending
	}
	w := readRadios(env)
	status, statusOK := wirelessStatus(ctx, env)
	p := &Plan{before: map[string]bool{}}
	if statusOK {
		for dev, s := range status {
			p.before[dev] = s.Up
		}
	}
	if !req.Tune && len(req.Radios) == 0 {
		return nil, invalidf("no radio to set")
	}
	if req.Tune {
		for _, r := range w.Radios {
			if r.Band != "2g" && r.Band != "5g" {
				return nil, Unsupported{unsupportedWhy(r)}
			}
		}
		for _, dev := range sortedKeys(req.Channels) {
			r, ok := w.radio(dev)
			switch {
			case !ok:
				return nil, invalidf("channels.%s: there is no such radio", dev)
			case r.Mesh:
				return nil, invalidf("channels.%s: it carries a mesh or ad-hoc interface, whose channel is its peers'", dev)
			}
			if err := ValidChannel(r.Band, r.HTMode, req.Channels[dev]); err != nil {
				return nil, invalidf("channels.%s: %v", dev, err)
			}
		}
	}
	pre, enable, notes, err := changeSteps(w, req.Radios)
	if err != nil {
		return nil, err
	}
	p.Notes = notes
	if !req.Tune {
		p.steps = append(pre, enable...)
		return p, nil
	}
	for _, r := range w.Radios {
		if _, named := req.Radios[r.Device]; !named && r.Enabled && !r.Secured {
			return nil, openNeedsKey(r.Device)
		}
	}

	scan := scanRadios(ctx, env, w, status)
	_ = saveScan(env, scan)
	chosen := map[string]int{}
	for _, s := range scan.Radios {
		chosen[s.Device] = s.Recommended
	}
	var tune []step
	for _, r := range w.Radios {
		dev := "wireless." + r.Device
		tune = append(tune, set(dev+".country", Panama), del(dev+".txpower"))
		ch, asked := req.Channels[r.Device]
		if !asked {
			ch = chosen[r.Device]
		}
		if ch > 0 && !r.Mesh {
			tune = append(tune, set(dev+".channel", strconv.Itoa(ch)))
		}
		for _, i := range r.ifaces {
			if i.mode == "ap" {
				tune = append(tune, del("wireless."+i.ref+".vif_txpower"))
			}
		}
		if _, named := req.Radios[r.Device]; !named && r.firstAP() != nil && !r.Secured {
			p.Notes = append(p.Notes, r.Device+": open network left disabled; give it a key") // off: refused above if on
		}
	}
	p.steps = append(append(tune, pre...), enable...)
	return p, nil
}

func unsupportedWhy(r Radio) string {
	switch r.Band {
	case "6g":
		return r.Device + ": a 6 GHz radio — Panama's rules would switch it off"
	case "60g":
		return r.Device + ": a 60 GHz radio — the tuning has no rules for it"
	}
	return r.Device + ": a radio of unknown band — the tuning cannot tell what Panama's rules would do to it"
}

// changeSteps checks changes against the radios and returns the steps that
// name each radio's first access point, and — apart, to be staged last — the
// ones that switch it on:
//
//   - A secured network keeps its key when none is given, and a new key keeps
//     its WPA mode (psk, psk2, psk-mixed, sae, sae-mixed); ieee80211w is left
//     alone. A network in a mode without a passphrase is refused a key.
//   - An open network needs a key; it gets psk2 with it, and it and its radio
//     are switched on — that is the wizard's purpose on a fresh box. Naming a
//     secured network never changes whether it is on.
//   - A radio this call switches on (it was off) brings no open interface up
//     with it: every other open one on it — access point, mesh, ad-hoc or
//     client — is kept off, and noted.
func changeSteps(w Wifi, changes map[string]WifiChange) (pre, enable []step, notes []string, err error) {
	for _, dev := range sortedKeys(changes) {
		c := changes[dev]
		r, ok := w.radio(dev)
		if !ok {
			return nil, nil, nil, invalidf("radios.%s: there is no such radio", dev)
		}
		ap := r.firstAP()
		switch {
		case ap == nil:
			return nil, nil, nil, invalidf("radios.%s: it carries no access point to name", dev)
		case c.SSID == nil || !validSSID(*c.SSID):
			return nil, nil, nil, invalidf("radios.%s.ssid: the name must be 1-32 bytes of text", dev)
		case c.Key != nil && !validKey(*c.Key):
			return nil, nil, nil, invalidf("radios.%s.key: the key must be 8-63 printable ASCII characters", dev)
		case c.Key == nil && !ap.secure:
			return nil, nil, nil, openNeedsKey(dev)
		case c.Key != nil && ap.secure && !passphraseMode(ap.enc):
			return nil, nil, nil, invalidf("radios.%s.key: its network uses %q, which takes no passphrase", dev, ap.enc)
		}
		ref := "wireless." + ap.ref
		if !ap.secure && !r.radioOn {
			for _, other := range r.ifaces {
				if other.ref == ap.ref || other.secure || !other.on {
					continue
				}
				pre = append(pre, set("wireless."+other.ref+".disabled", "1"))
				notes = append(notes, dev+": "+keptOff(other.mode)+" on this radio kept off; give it a key")
			}
		}
		pre = append(pre, set(ref+".ssid", *c.SSID))
		if c.Key != nil {
			if !ap.secure {
				pre = append(pre, set(ref+".encryption", "psk2"))
			}
			pre = append(pre, set(ref+".key", *c.Key))
		}
		if !ap.secure {
			enable = append(enable, del(ref+".disabled"), set("wireless."+r.Device+".disabled", "0"))
		}
	}
	return pre, enable, notes, nil
}

func keptOff(mode string) string {
	switch mode {
	case "ap":
		return "another open network"
	case "mesh":
		return "an open mesh interface"
	case "adhoc":
		return "an open ad-hoc interface"
	case "sta":
		return "an open client interface"
	}
	return "an open interface"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Staged is a plan's steps in a private uci save directory, with
// /etc/config/wireless kept aside: nothing is committed yet.
type Staged struct {
	env  Env
	dir  string
	prev []byte // the apply file before this change; nil when there was none
}

// wifiJob is what the helper needs to prove the change and undo it: which
// radios were up before, and the wireless file as it was. 0600: it holds the
// keys.
type wifiJob struct {
	Before   map[string]bool `json:"before"`
	Wireless []byte          `json:"wireless"`
	Mode     uint32          `json:"mode"`
	Had      bool            `json:"had"`
}

// Stage writes p's steps into a private save directory and keeps the
// wireless file aside. On an error nothing is left behind.
func Stage(ctx context.Context, env Env, p *Plan) (*Staged, error) {
	job := wifiJob{Before: p.before}
	if raw, err := os.ReadFile(env.WirelessConfig); err == nil {
		job.Wireless, job.Had = raw, true
		if st, err := os.Stat(env.WirelessConfig); err == nil {
			job.Mode = uint32(st.Mode().Perm())
		}
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return nil, err
	}
	if err := localctl.WriteFileAtomic(env.WifiJob, raw, 0o600); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(env.RunDir, 0o755); err != nil {
		_ = os.Remove(env.WifiJob)
		return nil, err
	}
	dir, err := os.MkdirTemp(env.RunDir, "uci-wifi-")
	if err != nil {
		_ = os.Remove(env.WifiJob)
		return nil, err
	}
	s := &Staged{env: env, dir: dir}
	if err := env.applyIn(ctx, dir, p.steps); err != nil {
		s.Abort()
		return nil, err
	}
	return s, nil
}

// Abort drops what was staged: nothing of this change reaches the router.
func (s *Staged) Abort() {
	_ = os.RemoveAll(s.dir)
	_ = os.Remove(s.env.WifiJob)
}

// Commit sets the restart's state to "applying" — so a `setup` read right
// after the answer never shows the last change's result for this one — and
// commits. A failed commit puts the state back and drops the staged steps;
// so does a change left uncommitted in uci's default save directory since the
// plan (ErrPending), before anything is written.
func (s *Staged) Commit(ctx context.Context) error {
	if pendingUCI(s.env) {
		s.Abort()
		return ErrPending
	}
	s.prev, _ = os.ReadFile(s.env.WifiApply)
	if err := writeApply(s.env, ApplyState{State: ApplyApplying, At: s.env.Now().UTC(), Radios: map[string]bool{}}); err != nil {
		s.Abort()
		return err
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.env.Run(c, nil, "uci", "-t", s.dir, "commit", "wireless"); err != nil {
		s.Unapply()
		s.Abort()
		return fmt.Errorf("uci commit wireless: %w", err)
	}
	_ = os.RemoveAll(s.dir)
	return nil
}

// Undo takes back a committed change whose restart never started: the
// wireless file as it was, the restart's state as it was.
func (s *Staged) Undo() {
	var job wifiJob
	if raw, err := os.ReadFile(s.env.WifiJob); err == nil && json.Unmarshal(raw, &job) == nil && job.Had {
		mode := os.FileMode(job.Mode)
		if mode == 0 {
			mode = 0o600
		}
		_ = localctl.WriteFileAtomic(s.env.WirelessConfig, job.Wireless, mode)
	}
	s.Unapply()
	s.Abort()
}

// Unapply puts the restart's state back as it was before Commit: for a change
// whose restart never happens.
func (s *Staged) Unapply() {
	if s.prev == nil {
		_ = os.Remove(s.env.WifiApply)
		return
	}
	_ = localctl.WriteFileAtomic(s.env.WifiApply, s.prev, 0o644)
}

// applyIn runs steps against the save directory dir (uci -t).
func (env Env) applyIn(ctx context.Context, dir string, steps []step) error {
	withDir := make([]step, len(steps))
	for i, s := range steps {
		withDir[i] = step{args: append([]string{"-t", dir}, s.args...), ignoreErr: s.ignoreErr}
	}
	return env.apply(ctx, withDir)
}
