package main

import (
	"context"
	"net/http"
	"time"

	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/rescue"
	"vectra-controller-pro/internal/supervisor"
)

// The commit-confirm deadman (internal/firewall) reverts a ruleset nothing
// confirmed within its timeout. Until r21 only the panel confirmed: a probe
// of its /healthz right after the apply, or the next successful check-in. A
// router that could not reach the panel — the VPS down, its address blocked,
// its certificate expired — had every ruleset it loaded reverted 90 s later
// and loaded again by ensureDataPlane: after the nightly reboot, a DNS watch
// correction, a rescue's way back, the VPN flapped every minute and a half
// for as long as the panel stayed away.
//
// The deadman guards one thing: a ruleset that cut the router off. Whether
// the router still reaches the internet is something it can prove itself,
// with the same marked sockets the panel's probe went out on — so every
// programming, wherever it came from (a start, the rescue, the DNS watch, a
// panel job), is confirmed by local proof first (firewallHealthProof): the
// router's own way out past the new ruleset, or the LAN's way through the
// tunnel. The panel still confirms too — its probe while it is not known to
// be down, and every successful check-in — but nothing waits for it. A
// ruleset with neither proof for the whole timeout is reverted as before.

// fwConfirmEvery is how often a pending confirmation is proven again;
// fwConfirmMargin stops the tries that long before the deadman wakes.
const (
	fwConfirmEvery  = 10 * time.Second
	fwConfirmMargin = 5 * time.Second
)

// now is time.Now, or the test's clock.
func (d *daemon) now() time.Time {
	if d.clock != nil {
		return d.clock()
	}
	return time.Now()
}

// confirmFirewall is the confirmation right after a ruleset went in: it
// arms the retries (retryFirewallConfirm) and makes the first try now.
func (d *daemon) confirmFirewall(ctx context.Context) bool {
	now := d.now()
	timeout := 90 * time.Second
	if d.confirmer != nil && d.confirmer.Timeout > 0 {
		timeout = d.confirmer.Timeout
	}
	d.fwConfirmBy, d.fwConfirmNext = now.Add(timeout-fwConfirmMargin), now
	return d.retryFirewallConfirm(ctx, now)
}

// retryFirewallConfirm proves a pending confirmation again when its turn has
// come; true when it confirmed. Run from the loop and its DNS watch.
func (d *daemon) retryFirewallConfirm(ctx context.Context, now time.Time) bool {
	if d.fwConfirmBy.IsZero() || now.Before(d.fwConfirmNext) {
		return false
	}
	if now.After(d.fwConfirmBy) {
		d.fwConfirmBy = time.Time{}
		logging.L().Warn("firewall commit-confirm: no proof the router still reaches the internet within the deadman's time; it reverts the ruleset")
		return false
	}
	d.fwConfirmNext = now.Add(fwConfirmEvery)
	how := d.firewallHealthProof(ctx)
	if how == "" {
		return false
	}
	if err := d.confirmer.Confirm(); err != nil {
		logging.L().Warn("firewall commit-confirm sentinel write failed", "err", err.Error())
		return false
	}
	d.fwConfirmBy = time.Time{}
	logging.L().Info("firewall change confirmed", "by", how)
	return true
}

// firewallConfirmed: something else confirmed (a check-in) or nothing is
// loaded any more (a teardown); no retries are owed.
func (d *daemon) firewallConfirmed() { d.fwConfirmBy = time.Time{} }

// firewallHealthProof says how the router proved, after a ruleset went in,
// that the ruleset did not cut it off; "" when it could not.
//
//   - Around the tunnel: the rescue's public health URLs on the control
//     plane's own client — the marked sockets the panel's probe used, which
//     the output chain returns on before any TPROXY rule — so this is the
//     panel's proof without the panel.
//   - Through it: the LAN's way out answers (probeThroughTunnel), for a WAN
//     that lets only the tunnel's nodes through. Only with xray running.
//   - The panel, as before, while it is not known to be down: a dead panel
//     would cost its timeout every try.
func (d *daemon) firewallHealthProof(ctx context.Context) string {
	if d.client == nil {
		return ""
	}
	if rescue.ProbeAnyWithin(ctx, d.client.HTTPClient(), d.rescuePolicy.HealthURLs, directProbeBudget) {
		return "local: the router reaches the internet"
	}
	if d.desired != nil && d.xrayStatus().State == supervisor.StateRunning {
		hc := &http.Client{Timeout: 8 * time.Second, Transport: tunnelProbeTransport}
		if d.probeThroughTunnel(ctx, hc) {
			return "local: the tunnel carries"
		}
	}
	if d.lastControlPlaneOK == nil || *d.lastControlPlaneOK {
		if rescue.ProbeAnyWithin(ctx, d.client.HTTPClient(), serverHealthURLs(d.cfg.ControlURL), directProbeBudget) {
			return "the panel is reachable"
		}
	}
	return ""
}
