package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
	"unicode"
	"unicode/utf8"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/setup"
)

var connectWifiEnv = setup.RouterEnv
var connectWifiApply = setup.ApplyWifi

// connectApplyWifi returns only a bounded outcome, never UCI output, keys,
// SSIDs, or the helper's detail. The Wi-Fi lock spans the final verification.
func connectApplyWifi(ctx context.Context, _ agentcfg.Config, params json.RawMessage) (string, bool) {
	var p struct {
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if dec.Decode(&p) != nil || dec.Decode(new(any)) != io.EOF || !connectWifiParamsValid(p.SSID, p.Password) {
		return "invalid_params", false
	}
	if ctx.Err() != nil {
		return "interrupted", false
	}
	env := connectWifiEnv()
	lock, err := setup.LockWifi(env)
	if errors.Is(err, setup.ErrBusy) {
		return "busy", false
	}
	if err != nil {
		return "apply_failed", false
	}
	defer lock.Close()
	// Do not overwrite a stopped helper's credential-bearing rollback job.
	if _, err := os.Lstat(env.WifiJob); !errors.Is(err, os.ErrNotExist) {
		return "interrupted", false
	}
	w := setup.ReadWifi(ctx, env)
	req := setup.WifiRequest{Radios: map[string]setup.WifiChange{}}
	for _, r := range w.Radios {
		if r.AP {
			req.Radios[r.Device] = setup.WifiChange{SSID: &p.SSID, Key: &p.Password}
		}
	}
	if len(req.Radios) == 0 {
		return "unsupported", false
	}
	plan, err := setup.PlanWifi(ctx, env, req)
	if errors.Is(err, setup.ErrPending) {
		return "busy", false
	}
	if setup.IsUnsupported(err) || setup.IsInvalid(err) {
		return "unsupported", false
	}
	if err != nil {
		return "apply_failed", false
	}
	if ctx.Err() != nil {
		return "interrupted", false
	}
	staged, err := setup.Stage(ctx, env, plan)
	if err != nil {
		return "apply_failed", false
	}
	if ctx.Err() != nil {
		staged.Abort()
		return "interrupted", false
	}
	if err := staged.Commit(ctx); err != nil {
		if errors.Is(err, setup.ErrPending) {
			return "busy", false
		}
		if ctx.Err() != nil {
			return "interrupted", false
		}
		return "apply_failed", false
	}
	// Once committed, finish verification/rollback even if the requesting
	// transport disconnects. This remains synchronous, with a bounded deadline.
	verifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 4*time.Minute)
	defer cancel()
	a := connectWifiApply(verifyCtx, env)
	if ctx.Err() != nil || verifyCtx.Err() != nil {
		return "interrupted", false
	}
	switch a.State {
	case setup.ApplyOK:
		if len(a.Radios) == 0 {
			return "unsupported", false
		}
		for _, up := range a.Radios {
			if !up {
				return "unverified", false
			}
		}
		return "applied", true
	case setup.ApplyRolledBack:
		return "rolled_back", false
	case setup.ApplyPartial, setup.ApplyUnverified:
		return "unverified", false
	default:
		return "apply_failed", false
	}
}

func connectWifiParamsValid(ssid, password string) bool {
	if len(ssid) < 1 || len(ssid) > 32 || !utf8.ValidString(ssid) || len(password) < 8 || len(password) > 63 {
		return false
	}
	for _, r := range ssid {
		if unicode.IsControl(r) {
			return false
		}
	}
	for i := range password {
		if password[i] < 0x20 || password[i] > 0x7e {
			return false
		}
	}
	return true
}
