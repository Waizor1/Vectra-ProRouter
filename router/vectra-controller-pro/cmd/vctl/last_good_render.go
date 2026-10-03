package main

import (
	"context"
	"crypto/sha256"
	"path/filepath"

	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/vault"
)

// The last render xray took, kept sealed on flash beside the provider's
// document.
//
// The render itself lives on tmpfs, and after a reboot resumeRender makes
// it again from the last-good provider document. A document the router
// refuses now (r12's guard, provider_guard.go) — or that cannot be made
// again for any other reason — would leave the router without a data plane
// after its nightly reboot: vctl up, holding the router, no xray. So every
// render written is kept here too, and resumeRender falls back to it.

// lastGoodRenderPath is where it is kept; "" when the router has no place
// for the provider's document either (CLI commands, tests without one).
func (d *daemon) lastGoodRenderPath() string {
	if d.cfg.ProviderConfigPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(d.cfg.ProviderConfigPath), "xray-last-good.json")
}

// keepLastGoodRender seals render on flash — only when it differs from the
// one kept: renders change with the provider's document and the router's
// options, not with every check-in.
func (d *daemon) keepLastGoodRender(render []byte) {
	path := d.lastGoodRenderPath()
	if path == "" {
		return
	}
	sum := sha256.Sum256(render)
	if d.lastGoodRenderSum == ([sha256.Size]byte{}) {
		// The first render after a start: what is on flash already, if any.
		if kept, err := vault.ReadFile(path); err == nil {
			d.lastGoodRenderSum = sha256.Sum256(kept)
		}
	}
	if sum == d.lastGoodRenderSum && fileExists(path) {
		return
	}
	if err := vault.WriteFile(path, render); err != nil {
		logging.L().Warn("could not keep the last good render on flash", "err", err.Error())
		return
	}
	d.lastGoodRenderSum = sum
}

// resumeLastGoodRender runs the last good render again when the render
// cannot be made from the provider's document (why), after `xray -test`
// takes it. It reports an incident either way and says whether it did.
func (d *daemon) resumeLastGoodRender(ctx context.Context, why string) bool {
	details := map[string]any{"reason": clipText(why, 300), "fallback": false}
	path := d.lastGoodRenderPath()
	raw, err := vault.ReadFile(path)
	if path == "" || err != nil || len(raw) == 0 {
		logging.L().Error("no last good render to fall back to; the data plane waits for an apply", "reason", why)
		d.incident("RENDER_RESUME_FAILED", "no last good render", "the render could not be made again after the restart, and no last good render was kept", details)
		return false
	}
	if d.applier.Validate != nil {
		if err := d.applier.Validate.Test(ctx, raw); err != nil {
			logging.L().Error("xray refuses the last good render; the data plane waits for an apply", "err", err.Error(), "reason", why)
			details["test"] = clipText(err.Error(), 300)
			d.incident("RENDER_RESUME_FAILED", "last good render refused", "the render could not be made again after the restart, and xray refuses the last good one", details)
			return false
		}
	}
	if err := d.applier.WriteXray(raw); err != nil {
		logging.L().Error("could not put the last good render back", "err", err.Error())
		details["write"] = clipText(err.Error(), 300)
		d.incident("RENDER_RESUME_FAILED", "last good render not written", "the render could not be made again after the restart, nor the last good one put back", details)
		return false
	}
	d.lastGoodRenderSum = sha256.Sum256(raw)
	d.nodeCount = countProviderOutbounds(raw)
	logging.L().Warn("the render could not be made again after the restart; running the last good render", "reason", why)
	details["fallback"] = true
	d.incident("RENDER_RESUME_FALLBACK", "last good render", "the render could not be made again after the restart; the last good render runs", details)
	return true
}

// clipText is s cut to n bytes for an incident.
func clipText(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
