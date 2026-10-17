package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/vault"
)

// The daemon's own subscription refresh restarted xray every time the
// provider's document changed (four times a day), and a restart cuts every
// open connection — a video, a call, a download (1111, 2026-10-17: «ютуб и
// инста иногда тупят»). A document that changes no node — the same outbounds,
// only routing, balancers, the observatory — is held until a quiet moment:
// the router's night window, or quietHoldMax at the latest. Nodes and
// credentials changed (Connect rotates a router's account daily, the old one
// stops after minutes), a router that left the tunnel, or a refresh someone
// asked for (a job, the router UI, Connect) are applied at once, as before.
var (
	// quietFrom and quietTo bound the night window, the router's local hours.
	quietFrom, quietTo = 3, 6
	quietHoldMax       = 12 * time.Hour
)

// inQuietWindow: now is in the router's night window.
func inQuietWindow(now time.Time) bool {
	h := now.In(routerLocation()).Hour()
	return h >= quietFrom && h < quietTo
}

// sameNodes: two provider documents dial the same nodes the same way — their
// outbounds are equal as JSON (key order and spacing aside).
func sameNodes(a, b []byte) bool {
	oa, okA := canonicalOutbounds(a)
	ob, okB := canonicalOutbounds(b)
	return okA && okB && bytes.Equal(oa, ob)
}

func canonicalOutbounds(doc []byte) ([]byte, bool) {
	var top struct {
		Outbounds []any `json:"outbounds"`
	}
	if json.Unmarshal(doc, &top) != nil || len(top.Outbounds) == 0 {
		return nil, false
	}
	b, err := json.Marshal(top.Outbounds) // maps marshal with sorted keys
	return b, err == nil
}

// holdForQuiet: the daemon's own refresh brought doc; it is held (true) when
// it changes no node, the router carries the tunnel, and now is not quiet.
func (d *daemon) holdForQuiet(doc []byte, now time.Time) bool {
	if inQuietWindow(now) || d.rescueState().Mode == rescue.ModeDirect {
		return false
	}
	running, err := vault.ReadFile(d.documentPath())
	if err != nil || bytes.Equal(running, doc) || !sameNodes(running, doc) {
		return false
	}
	if d.pendingDoc == nil {
		d.pendingSince = now
	}
	d.pendingDoc = doc
	d.pendingBase = sha256.Sum256(running)
	return true
}

// maybeApplyHeld applies a held document once the moment is quiet, the hold
// has lasted quietHoldMax, or the router left the tunnel.
func (d *daemon) maybeApplyHeld(ctx context.Context, now time.Time) {
	if d.pendingDoc == nil {
		return
	}
	if !inQuietWindow(now) && now.Sub(d.pendingSince) < quietHoldMax && d.rescueState().Mode != rescue.ModeDirect {
		return
	}
	doc, base := d.pendingDoc, d.pendingBase
	d.pendingDoc, d.pendingSince = nil, time.Time{}
	// Held over the document that ran then: if another runs now — the owner
	// chose another location, a refresh someone asked for applied a newer
	// one — the held one is stale and would undo it.
	if running, err := vault.ReadFile(d.documentPath()); err != nil || sha256.Sum256(running) != base {
		logging.L().Info("dropped the held subscription document: the running one changed since")
		return
	}
	applyHeldDoc(d, ctx, doc)
}

// applyHeldDoc applies a held document as the refresh would have; tests
// stand it in.
var applyHeldDoc = func(d *daemon, ctx context.Context, doc []byte) {
	res, err := d.applyProvider(ctx, doc, false)
	if err != nil {
		logging.L().Warn("the held subscription document was refused; what runs keeps running", "err", err.Error())
		return
	}
	if res.Changed {
		d.nodeCount = countProviderOutbounds(doc)
		if d.supStarted {
			_ = d.sup.Reload(d.supCtx)
		}
	}
	logging.L().Info("applied the subscription document held for a quiet moment", "changed", res.Changed, "unchanged", res.RenderUnchanged)
	if err := d.persist(); err != nil {
		logging.L().Warn("persist state after the held document", "err", err.Error())
	}
}
