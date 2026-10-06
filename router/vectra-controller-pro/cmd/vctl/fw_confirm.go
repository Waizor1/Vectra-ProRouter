package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vectra-controller-pro/internal/firewall"

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
//
// Those proofs go out on the control plane's marked sockets, which the output
// chain returns on at its first rule: they never cross the LAN clients' path
// (prerouting TPROXY, the fwmark policy route, the kill switch's forward
// guard). So a proof confirms only with that path whole as well
// (clientChainFault) — structure and counters, not the nodes' liveness: a
// dead tunnel is the rescue's business, and making it the deadman's would
// bring the flapping back.

// fwConfirmEvery is how often a pending confirmation is proven again.
// fwConfirmMargin is how long before the deadman wakes the sentinel must be
// written at the latest; fwConfirmMaxTry bounds one try (the probes'
// budgets and the chain's counter window), so the last try starts that long
// earlier still and a "confirmed" never lands after the revert.
const (
	fwConfirmEvery  = 10 * time.Second
	fwConfirmMargin = 5 * time.Second
	fwConfirmMaxTry = 22 * time.Second
)

// now is time.Now, or the test's clock.
func (d *daemon) now() time.Time {
	if d.clock != nil {
		return d.clock()
	}
	return time.Now()
}

// confirmFirewall is the confirmation right after a ruleset went in, its
// deadman armed at armedAt (taken before the apply): it arms the retries
// (retryFirewallConfirm) and makes the first try now.
func (d *daemon) confirmFirewall(ctx context.Context, armedAt time.Time) bool {
	timeout := 90 * time.Second
	if d.confirmer != nil && d.confirmer.Timeout > 0 {
		timeout = d.confirmer.Timeout
	}
	d.fwConfirmDeadline = armedAt.Add(timeout - fwConfirmMargin)
	d.fwConfirmBy = d.fwConfirmDeadline.Add(-fwConfirmMaxTry)
	d.fwConfirmNext, d.fwConfirmFault = d.now(), ""
	return d.retryFirewallConfirm(ctx, d.fwConfirmNext)
}

// retryFirewallConfirm proves a pending confirmation again when its turn has
// come; true when it confirmed. Run from the loop and its DNS watch.
func (d *daemon) retryFirewallConfirm(ctx context.Context, now time.Time) bool {
	if d.fwConfirmBy.IsZero() || now.Before(d.fwConfirmNext) {
		return false
	}
	if now.After(d.fwConfirmBy) {
		d.fwConfirmBy = time.Time{}
		logging.L().Warn("firewall commit-confirm: no proof within the deadman's time that the router still reaches the internet and the LAN's path is whole; the deadman reverts the ruleset",
			"lastFault", d.fwConfirmFault)
		return false
	}
	d.fwConfirmNext = now.Add(fwConfirmEvery)
	how := d.firewallHealthProof(ctx)
	if how == "" {
		d.fwConfirmFault = "no answer: not around the tunnel, not through it, not from the panel"
		return false
	}
	if fault := d.clientChainFault(ctx); fault != "" {
		if fault == d.fwConfirmFault {
			return false // said already
		}
		d.fwConfirmFault = fault
		logging.L().Warn("firewall commit-confirm: the router reaches the internet but the LAN's path is not whole; not confirmed", "fault", fault)
		return false
	}
	if t := d.now(); !t.Before(d.fwConfirmDeadline) {
		d.fwConfirmBy = time.Time{}
		logging.L().Warn("firewall commit-confirm: proven too late; the deadman reverts the ruleset", "by", how)
		return false
	}
	if err := d.confirmer.Confirm(); err != nil {
		logging.L().Warn("firewall commit-confirm sentinel write failed", "err", err.Error())
		return false
	}
	d.fwConfirmBy, d.fwConfirmFault = time.Time{}, ""
	logging.L().Info("firewall change confirmed", "by", how)
	return true
}

// firewallConfirmed: something else confirmed (a check-in) or nothing is
// loaded any more (a teardown); no retries are owed.
func (d *daemon) firewallConfirmed() { d.fwConfirmBy = time.Time{} }

// confirmPending: a ruleset waits for its confirmation.
func (d *daemon) confirmPending() bool { return !d.fwConfirmBy.IsZero() }

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

// chainWindow is how long the kill switch's and the escape counters are
// watched for a climb.
var chainWindow = 2 * time.Second

// clientChainFault says what of the LAN clients' path the loaded ruleset
// lacks, "" when it is whole — or when there is nothing to check: no ruleset
// programmed, or xray not running (the kernel then lets the LAN past it by
// design; the DNS watch and the rescue own that case).
//
//   - prerouting: the TPROXY rule to xray's port;
//   - the policy route: `ip rule fwmark <mark> lookup <table>` and its local
//     default route;
//   - xray listening on the TPROXY port;
//   - over chainWindow, packets that TPROXY captured escaping to the WAN
//     (vctl_tproxy_escaped) and, with the kill switch on, its drops
//     (vctl_killswitch_drops) not climbing.
func (d *daemon) clientChainFault(ctx context.Context) string {
	if d.fwSpec == nil || d.xrayStatus().State != supervisor.StateRunning {
		return ""
	}
	spec := *d.fwSpec
	table := spec.TableName
	if table == "" {
		table = "vctl"
	}
	out, err := d.nftShow(ctx, "-t", "list", "chain", "inet", table, "prerouting")
	if err != nil {
		return "its prerouting chain cannot be read"
	}
	if !strings.Contains(string(out), "tproxy") || !strings.Contains(string(out), fmt.Sprintf(":%d", spec.TproxyPort)) {
		return fmt.Sprintf("its prerouting chain has no TPROXY rule to :%d", spec.TproxyPort)
	}
	rules, err := d.ipShow(ctx, "rule", "show")
	if err != nil || !hasFwmarkRule(string(rules), spec.FwMark, spec.RtTable) {
		return fmt.Sprintf("its policy rule (fwmark 0x%x lookup %d) is not in the kernel", spec.FwMark, spec.RtTable)
	}
	routes, err := d.ipShow(ctx, "route", "show", "table", fmt.Sprint(spec.RtTable))
	if err != nil || !hasLocalDefault(string(routes)) {
		return fmt.Sprintf("its local route in table %d is not in the kernel", spec.RtTable)
	}
	if !tcpListening(d.procRoot(), spec.TproxyPort) {
		return fmt.Sprintf("nothing listens on the TPROXY port %d", spec.TproxyPort)
	}
	before, ok := d.leakCounters(ctx)
	if !ok {
		return "its counters cannot be read"
	}
	select {
	case <-ctx.Done():
		return "cancelled"
	case <-time.After(chainWindow):
	}
	after, ok := d.leakCounters(ctx)
	if !ok {
		return "its counters cannot be read"
	}
	if after[firewall.CounterTproxyEscaped] > before[firewall.CounterTproxyEscaped] {
		return "packets TPROXY captured leave by the WAN (the policy route does not take them)"
	}
	if spec.KillSwitch && after[firewall.CounterKillSwitchDrops] > before[firewall.CounterKillSwitchDrops] {
		return "the kill switch is dropping the LAN's traffic"
	}
	return ""
}

// nftShow runs nft for its output (d.nftOutput in tests).
func (d *daemon) nftShow(ctx context.Context, args ...string) ([]byte, error) {
	if d.nftOutput != nil {
		return d.nftOutput(ctx, args...)
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(c, "nft", args...).Output()
}

// tcpListening: a socket listens on port, IPv4 or IPv6 (/proc/net/tcp*).
func tcpListening(proc string, port int) bool {
	want := fmt.Sprintf(":%04X", port)
	for _, f := range []string{"tcp", "tcp6"} {
		raw, err := os.ReadFile(filepath.Join(proc, "net", f))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(raw), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 3 && strings.HasSuffix(fields[1], want) && fields[3] == "0A" {
				return true
			}
		}
	}
	return false
}
