package setup

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	"vectra-controller-pro/internal/localctl"
)

// The restart that proves a Wi-Fi change, run by a detached helper holding
// the Wi-Fi lock: `wifi reload`, then netifd is asked every 2 s, for up to
// 45 s, whether every radio that is on in the new configuration — with an
// access point on — is up with that access point's interface. A radio that
// was up before the change and is not now brings the old configuration back
// (the wireless file as it was, another reload, another wait). A radio that
// was not up before either proves nothing: a fresh box, or no radio at all.

// The restart's states (setup.wifi.apply.state).
const (
	ApplyApplying   = "applying"    // the change is committed; the restart and the check are running
	ApplyOK         = "ok"          // every radio that should be up is
	ApplyPartial    = "partial"     // some did not come up — none of them was up before
	ApplyRolledBack = "rolled_back" // a radio that was up before did not come up: the old settings are back
	ApplyUnverified = "unverified"  // nothing could be judged (netifd did not answer, or no radio came up and none was up before)
	ApplyFailed     = "failed"      // a radio that was up before did not come up, and the old settings could not be put back
)

// Timings of the check; tests shorten them.
var (
	ApplyDelay       = 3 * time.Second  // after the answer, before the reload
	ApplyWait        = 45 * time.Second // for the radios to come up
	ApplyPoll        = 2 * time.Second
	ApplyNoAnswerFor = 6 * time.Second // netifd never answering: nothing to judge
)

// ApplyState is the last change's restart (setup.wifi.apply).
type ApplyState struct {
	State  string          `json:"state"`
	At     time.Time       `json:"at"`
	Detail string          `json:"detail,omitempty"`
	Radios map[string]bool `json:"radios"`
}

// LoadApply is the last change's restart; nil when there has been none since
// boot. A state still "applying" with nobody holding the lock is a check that
// died before it finished.
func LoadApply(env Env) *ApplyState {
	raw, err := os.ReadFile(env.WifiApply)
	if err != nil {
		return nil
	}
	var a ApplyState
	if json.Unmarshal(raw, &a) != nil || a.State == "" {
		return nil
	}
	if a.Radios == nil {
		a.Radios = map[string]bool{}
	}
	if a.State == ApplyApplying && !wifiLockHeld(env) {
		a.State, a.Detail = ApplyUnverified, "the check stopped before it finished"
	}
	return &a
}

func writeApply(env Env, a ApplyState) error {
	raw, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return localctl.WriteFileAtomic(env.WifiApply, raw, 0o644)
}

// ApplyWifi is the helper's work, under the lock it was handed: restart the
// Wi-Fi, check it, roll back when a radio that was up is not. It writes and
// returns the final state, and removes the job (it holds the keys).
func ApplyWifi(ctx context.Context, env Env) ApplyState {
	var job wifiJob
	raw, jerr := os.ReadFile(env.WifiJob)
	if jerr == nil {
		jerr = json.Unmarshal(raw, &job)
	}
	defer os.Remove(env.WifiJob)

	env.Sleep(ApplyDelay)
	env.reload(ctx)
	expected := expectedUp(readRadios(env))
	up, answered := env.waitUp(ctx, expected)
	a := ApplyState{At: env.Now().UTC(), Radios: up}
	down := notUp(expected, up)
	var wasUp []string
	for _, dev := range down {
		if job.Before[dev] {
			wasUp = append(wasUp, dev)
		}
	}
	switch {
	case !answered:
		a.State, a.Detail = ApplyUnverified, "netifd did not answer network.wireless status: the restart could not be checked"
	case len(down) == 0:
		a.State = ApplyOK
		if len(expected) == 0 {
			a.Detail = "no radio is on"
		}
	case len(wasUp) > 0:
		a = env.rollBack(ctx, job, jerr, wasUp)
	case len(down) < len(expected):
		a.State, a.Detail = ApplyPartial, strings.Join(down, ", ")+" did not come up (not up before the change either)"
	default:
		a.State, a.Detail = ApplyUnverified, "no radio came up, and none was up before: a fault cannot be told from a radio the router cannot drive"
	}
	if a.Radios == nil {
		a.Radios = map[string]bool{}
	}
	_ = writeApply(env, a)
	return a
}

// rollBack puts the wireless file back as it was and restarts the Wi-Fi
// again.
func (env Env) rollBack(ctx context.Context, job wifiJob, jerr error, wasUp []string) ApplyState {
	a := ApplyState{At: env.Now().UTC(), Radios: map[string]bool{}}
	what := strings.Join(wasUp, ", ") + " did not come up with the new settings"
	if jerr != nil || !job.Had {
		a.State, a.Detail = ApplyFailed, what+", and the previous settings were not kept to put back"
		return a
	}
	mode := os.FileMode(job.Mode)
	if mode == 0 {
		mode = 0o600
	}
	if err := localctl.WriteFileAtomic(env.WirelessConfig, job.Wireless, mode); err != nil {
		a.State, a.Detail = ApplyFailed, what+", and the previous settings could not be put back: "+err.Error()
		return a
	}
	env.reload(ctx)
	expected := expectedUp(readRadios(env))
	up, answered := env.waitUp(ctx, expected)
	a.At, a.Radios, a.State = env.Now().UTC(), up, ApplyRolledBack
	a.Detail = what + "; the previous settings are back"
	if still := notUp(expected, up); answered && len(still) > 0 {
		a.Detail += ", but " + strings.Join(still, ", ") + " is still down"
	}
	return a
}

func (env Env) reload(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	_ = env.Run(c, nil, "wifi", "reload") // the check that follows is the verdict
}

// expectedUp are the radios the new configuration has on with an access
// point on.
func expectedUp(w Wifi) []string {
	var out []string
	for _, r := range w.Radios {
		if !r.radioOn {
			continue
		}
		for _, i := range r.ifaces {
			if i.mode == "ap" && i.on {
				out = append(out, r.Device)
				break
			}
		}
	}
	return out
}

func notUp(expected []string, up map[string]bool) []string {
	var out []string
	for _, dev := range expected {
		if !up[dev] {
			out = append(out, dev)
		}
	}
	sort.Strings(out)
	return out
}

// waitUp asks netifd until every expected radio is up with an access point
// interface, netifd gave up on the rest, or ApplyWait runs out. answered is
// false when netifd never answered — for ApplyNoAnswerFor, nothing waits
// longer on a router without network.wireless.
func (env Env) waitUp(ctx context.Context, expected []string) (up map[string]bool, answered bool) {
	start := env.Now()
	up = map[string]bool{}
	for {
		if st, ok := wirelessStatus(ctx, env); ok {
			answered = true
			all, gaveUp := true, true
			for _, dev := range expected {
				s := st[dev]
				up[dev] = s.Up && s.APIfname != ""
				if !up[dev] {
					all = false
					if !s.RetryFailed {
						gaveUp = false
					}
				}
			}
			if all || gaveUp {
				return up, true
			}
		} else if !answered && env.Now().Sub(start) >= ApplyNoAnswerFor {
			return map[string]bool{}, false
		}
		if env.Now().Sub(start) >= ApplyWait || ctx.Err() != nil {
			if !answered {
				return map[string]bool{}, false
			}
			return up, true
		}
		env.Sleep(ApplyPoll)
	}
}
