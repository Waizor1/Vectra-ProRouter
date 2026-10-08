package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"vectra-controller-pro/internal/power"
	"vectra-controller-pro/internal/uiapi"
)

// set_power: Vectra on or off from the router UI (ui/contract/README.md,
// "Power"), the same change `vectra on` and `vectra off` make — never a
// trial, which is for a person at the console. The simple view's: the
// operator's lock never refuses it.
func init() {
	rpcdSignatures["set_power"] = map[string]interface{}{"on": true, "force": false}
}

// Seams: tests read a fake router and never spawn anything.
var (
	rpcdPowerEnv = power.RouterEnv
	// rpcdPower is Vectra's switch and who carries the traffic, for status;
	// up says the daemon answered, which spares asking procd, and loaded that
	// nft lists vctl's table — status has read it already.
	rpcdPower = func(ctx context.Context, up, loaded bool) power.Facts {
		env := rpcdPowerEnv()
		env.Loaded = func(context.Context) bool { return loaded }
		return power.Read(ctx, env, up)
	}
)

// rpcdSetPower answers set_power {"on": bool}. Already so: power_on or
// power_off at once — off only with nothing owed back: a hand-back still
// owed (its breadcrumbs kept, the boot links with them) is what `off` tries
// again. Otherwise the change is handed, with the power lock, to a detached
// `vctl power on|off` — it outlives this call and LuCI's timeout — and the
// answer is pending: the UI watches status.power until it shows.
func rpcdSetPower(ctx context.Context, params []byte) uiapi.Action {
	var p struct {
		On *bool `json:"on"`
		// Force: on also when Vectra would carry nothing (status.power
		// .wouldIdle) — the UI asks the person first.
		Force bool `json:"force"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if len(params) == 0 || dec.Decode(&p) != nil || p.On == nil {
		return action(false, "invalid_params", `params must be {"on": true} or {"on": false}, with "force": true to switch on a vctl that would carry nothing yet`)
	}
	env := rpcdPowerEnv()
	lock, err := power.Lock(env)
	switch {
	case errors.Is(err, power.ErrBusy):
		return action(false, "busy", err.Error())
	case err != nil:
		return action(false, "internal", err.Error())
	}
	defer lock.Close() // the change holds its own descriptor of the lock
	f := power.Read(ctx, env, false)
	switch {
	case *p.On && f.UCI && f.Boot && f.Running && f.Trial == nil:
		return action(true, "power_on", "")
	case !*p.On && !f.On() && !f.Running && f.Owed == "":
		return action(true, "power_off", "")
	}
	// What `vectra on` refuses without --force (errWouldIdle): the page says
	// so first (status.power.wouldIdle), and asks again with force.
	if *p.On && f.WouldIdle && !p.Force {
		// The Pro view shows this as it is, on every brand's router: it names
		// the program (vctl), not a service.
		return action(false, "would_idle", "vctl would carry no traffic yet: no operator config, and no PassWall2 to route by — the LAN would go out without a VPN until the router is linked")
	}
	args := []string{"power", "off", "--foreground"}
	if *p.On {
		args = []string{"power", "on", "--foreground"}
		if p.Force {
			args = append(args, "--force")
		}
	}
	if _, err := powerSpawn(env, lock, args...); err != nil {
		return action(false, "apply_failed", "the switch did not start, so nothing changed: "+err.Error())
	}
	return action(true, "pending", "")
}
