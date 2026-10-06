package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/portfwd"
	"vectra-controller-pro/internal/setup"
	"vectra-controller-pro/internal/uiapi"
)

// Port forwards (ui/contract/README.md, "Port forwards"): the owner's rules
// as fw4 redirects, read with `port_forwards` and replaced whole with
// `set_port_forwards`. Both are the Pro view's: under the operator's lock the
// router refuses them (rpcdProOnly) — an owner whose router is locked to the
// simple view manages port forwards from Vectra Connect instead.
func init() {
	rpcdSignatures["port_forwards"] = map[string]interface{}{}
	rpcdSignatures["set_port_forwards"] = map[string]interface{}{"rules": []interface{}{}}
	rpcdProOnly["port_forwards"] = true
	rpcdProOnly["set_port_forwards"] = true
}

// portfwdEnv is where port forwards live; tests point it at a temp dir.
var portfwdEnv = portfwd.RouterEnv

// rpcdPortForwardsCall answers the port forwards' methods; ok is false for
// any other.
func rpcdPortForwardsCall(ctx context.Context, cfg agentcfg.Config, method string, params []byte) (interface{}, bool) {
	switch method {
	case "port_forwards":
		wan := setup.ReadWan(ctx, rpcdSetupEnv())
		st := portfwd.Read(ctx, portfwdEnv())
		return uiapi.BuildPortForwards(st, portfwd.CGNAT(wan.IPv4), portForwardsDirectActive(st.Rules)), true
	case "set_port_forwards":
		return rpcdSetPortForwards(ctx, cfg, params), true
	}
	return nil, false
}

// rpcdSetPortForwards replaces the owner's port forwards. The change is the
// router's configuration, made here — fw4 carries a forward whether the
// daemon runs or not. Only the «past the VPN» set is the daemon's (it lives
// in the data plane it owns), so the daemon is asked to write it now; when it
// is down or slow the forwards are in all the same, and the set follows when
// it runs (it is written after every load of the data plane); the answer's
// directActive says whether it is in effect.
//
// Bounded so LuCI's ~20 s call budget is never what stops it: the apply has
// rpcdPortForwardsApplyWait (portfwd.Apply keeps a failed reload's restore
// within it, give or take its floor), the daemon rpcdPortForwardsSyncWait —
// an rpcd child killed between the commit and the reload would leave
// redirects committed but not running.
func rpcdSetPortForwards(ctx context.Context, cfg agentcfg.Config, params []byte) uiapi.Action {
	rules, code, detail := decodePortForwards(params)
	if code != "" {
		return action(false, code, detail)
	}
	actx, cancel := context.WithTimeout(ctx, rpcdPortForwardsApplyWait)
	_, err := portfwd.Apply(actx, portfwdEnv(), rules)
	cancel()
	if err != nil {
		return action(false, err.Code, err.Detail)
	}
	sctx, cancel := context.WithTimeout(ctx, rpcdPortForwardsSyncWait)
	defer cancel()
	resp, serr := localctl.Call(sctx, cfg.UISocketPath, localctl.SocketRequest{Op: localctl.OpSyncPortForwards})
	switch {
	case errors.Is(serr, localctl.ErrDaemonDown):
		return action(true, "port_forwards_set", "the controller is not running: «past the VPN» applies once it is")
	case serr != nil:
		// Out of time, or the socket failed: the daemon writes the set at its
		// next loop from the config committed here.
		return action(true, "port_forwards_set", "")
	case resp.OK:
		return action(true, "port_forwards_set", "")
	}
	return action(true, "port_forwards_set", "«past the VPN» could not be applied yet; the controller tries again: "+resp.Detail)
}

// The set_port_forwards budget within LuCI's ~20 s: the apply, then the
// daemon's write of the set.
const (
	rpcdPortForwardsApplyWait = 12 * time.Second
	rpcdPortForwardsSyncWait  = 3 * time.Second
)

// decodePortForwards reads {"rules": [...]}: the whole list, always — a list
// left out is refused rather than read as «remove every forward», as for
// set_rules. Unknown fields are refused too: a misspelt «direct» must not
// silently become false.
func decodePortForwards(params []byte) ([]portfwd.Rule, string, string) {
	var p struct {
		Rules *[]portfwd.Rule `json:"rules"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if len(params) == 0 || dec.Decode(&p) != nil || !errors.Is(dec.Decode(new(json.RawMessage)), io.EOF) {
		return nil, "invalid_params", `params must be {"rules": [...]}`
	}
	if p.Rules == nil {
		return nil, "invalid_params", "rules is required; [] removes every forward"
	}
	return *p.Rules, "", ""
}
