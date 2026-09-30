package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
	"unicode/utf8"

	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/state"
)

// claimer is the router's side of being claimed by a Vectra account
// (ADR-0006) while it is not linked: the code that rotates, the QR sealed to
// Vectra's key, and who claimed it. The loop goroutine tells it what the
// panel said; the UI socket's goroutine reads it for rpcd.
type claimer struct {
	nonces   *claim.Nonces
	deviceID string
	model    string
	device   ed25519.PrivateKey // nil: no QR (the code still works)

	mu     sync.Mutex
	linked bool
	vectra *claim.Key
	owner  *controlplane.ClaimOwner
	qrKey  string // what the cached QR was sealed for
	qr     string
	shown  string // the code the UI was last given

	// rotated wakes the loop when the UI starts showing a new code, so the
	// panel hears of it at once and not at the next check-in: until then a
	// person who types the code on screen would be told it is unknown.
	rotated chan struct{}
}

func newClaimer(st state.PersistedState, model string, period time.Duration) *claimer {
	c := &claimer{nonces: claim.NewNonces(period, rand.Reader), deviceID: st.DeviceIdentifier, model: model,
		owner: st.ClaimOwner, rotated: make(chan struct{}, 1)}
	if k, err := claim.DeviceKey(st.DevicePrivateKey); err == nil {
		c.device = k
	}
	if st.ClaimKey != nil {
		if k, err := claim.ParseKey(st.ClaimKey.Kid, st.ClaimKey.PublicKey); err == nil {
			c.vectra = &k
		}
	}
	return c
}

// setLinked: a linked router (it has an operator config) has no claim.
func (c *claimer) setLinked(linked bool) {
	c.mu.Lock()
	c.linked = linked
	c.mu.Unlock()
}

func (c *claimer) setKey(k claim.Key) {
	c.mu.Lock()
	c.vectra = &k
	c.mu.Unlock()
}

func (c *claimer) setOwner(o *controlplane.ClaimOwner) {
	c.mu.Lock()
	c.owner = o
	c.mu.Unlock()
}

// release starts the claim over, as on a router out of the box: no owner,
// not linked, and a code nobody has seen yet.
func (c *claimer) release() {
	c.mu.Lock()
	c.linked, c.owner, c.qrKey, c.qr = false, nil, "", ""
	c.mu.Unlock()
	c.nonces.Reset()
}

// view is the claim for the UI; nil once linked.
func (c *claimer) view(now time.Time) *localctl.Claim {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.linked {
		return nil
	}
	n, exp, err := c.nonces.Current(now)
	if err != nil {
		return nil
	}
	v := &localctl.Claim{State: "unclaimed", Code: claim.Code(n), ExpiresAt: exp.UTC()}
	if c.shown != "" && c.shown != v.Code {
		select {
		case c.rotated <- struct{}{}:
		default: // a wake-up is already pending
		}
	}
	c.shown = v.Code
	if c.owner != nil {
		v.State, v.Owner = "claimed", &localctl.ClaimOwner{Label: c.owner.Label}
	}
	if c.vectra != nil && c.device != nil {
		key := hex.EncodeToString(n) + "/" + hex.EncodeToString(append([]byte{c.vectra.Kid}, c.vectra.PublicKey...))
		if key != c.qrKey {
			qr, err := claim.SealStable(claim.Plain{Exp: exp, Nonce: n, DeviceID: c.deviceID, Model: c.model}, c.device, *c.vectra)
			if err != nil {
				logging.L().Warn("could not seal the claim QR; the code still works", "err", err.Error())
				qr = ""
			}
			c.qrKey, c.qr = key, qr
		}
		v.QR = c.qr
	}
	return v
}

// announcement is the check-in's claim: the code's hash, never the code;
// nil once linked.
func (c *claimer) announcement(now time.Time) *controlplane.ClaimAnnouncement {
	c.mu.Lock()
	linked := c.linked
	c.mu.Unlock()
	if linked {
		return nil
	}
	n, exp, err := c.nonces.Current(now)
	if err != nil {
		return nil
	}
	return &controlplane.ClaimAnnouncement{CodeHash: claim.CodeHash(claim.Code(n)), ExpiresAt: exp.UTC().Format(time.RFC3339)}
}

// adoptClaimInfo keeps what a register or check-in answer said about claiming
// the router: in state, so it survives a restart, and in the claimer. A value
// the router cannot use is dropped with a warning; an absent one changes
// nothing, and "owner": null means nobody owns the router any more.
//
// "released": true is acted on only by a router that HAD an owner before this
// answer (see release): it reports whether the router released itself, and
// then nothing else in the answer concerns it. A router that never had an
// owner — the fleet — ignores the flag, whatever else the answer says.
func (d *daemon) adoptClaimInfo(ctx context.Context, info controlplane.ClaimInfo) (released bool) {
	hadOwner := d.st.ClaimOwner != nil
	if k := info.ClaimKey; k != nil {
		if key, err := claim.ParseKey(k.Kid, k.PublicKey); err != nil {
			logging.L().Warn("the panel sent a claim key the router cannot use", "err", err.Error())
		} else {
			d.st.ClaimKey = &controlplane.ClaimKey{Kid: k.Kid, PublicKey: k.PublicKey}
			d.claim.setKey(key)
		}
	}
	if b := info.BotUsername; b != "" {
		if botUsername(b) {
			d.st.BotUsername = b
		} else {
			logging.L().Warn("the panel sent a bot username the router cannot put in a link", "botUsername", b)
		}
	}
	if info.Released && hadOwner {
		d.release(ctx)
		return true
	}
	if len(info.Owner) > 0 {
		var owner *controlplane.ClaimOwner
		if err := json.Unmarshal(info.Owner, &owner); err != nil {
			logging.L().Warn("the panel sent an owner the router cannot read", "err", err.Error())
			return false
		}
		if owner != nil && utf8.RuneCountInString(owner.Label) > 64 {
			owner.Label = string([]rune(owner.Label)[:64])
		}
		d.st.ClaimOwner = owner
		d.claim.setOwner(owner)
	}
	return false
}

// botUsername: what Telegram allows in a username, so it can go into a link
// as it is.
func botUsername(s string) bool {
	if len(s) < 3 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
