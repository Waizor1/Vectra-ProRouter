package main

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"syscall"
	"time"

	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/portfwd"
	"vectra-controller-pro/internal/rescue"
)

// The port forwards' «past the VPN» devices (firewall.SetPortForwardDirect4).
//
// The rules live in fw4's config, which `vctl rpcd` and Vectra Connect
// change; the set lives in the data plane this daemon loads. A load replaces
// the whole table, and the set with it — empty — so the daemon writes the set
// after every load (programFirewallWithin), and again whenever the firewall
// config changed since (the loop compares its stamp: size and mtime). A
// change made on the router asks for it at once (localctl.OpSyncPortForwards)
// instead of waiting for the next loop. Writing it is one small nft
// transaction (flush + add) that touches nothing else of the table.
//
// It is the daemon's only while the daemon has loaded the data plane in proxy
// mode: in rescue's direct mode everything goes out by the kernel anyway, and
// without the direct conntrack bit the rule is not in the table at all.

// portForwardsWanted: the set is in a table this process loaded, and means
// something there.
func (d *daemon) portForwardsWanted() (firewall.Spec, bool) {
	if d.fwProgrammed == nil || d.desired == nil || d.rescueState().Mode == rescue.ModeDirect {
		return firewall.Spec{}, false
	}
	spec, ok := firewallSpecFromConfig(d.desired)
	if !ok || spec.DirectCtMark == 0 {
		return firewall.Spec{}, false
	}
	return spec, true
}

// maybeSyncPortForwards writes the set from the firewall config when it is
// not already written from the config as it is now; force writes it anyway
// (a load just replaced the table, or a change on the router asks). A failed
// write is tried again by the loop after directRetry, or at once when forced.
//
// Every outcome is recorded for `vctl rpcd` and the telemetry
// (portfwd.DirectStatus): a rule's direct flag that nothing carries out must
// read as such, not as true.
func (d *daemon) maybeSyncPortForwards(ctx context.Context, force bool) error {
	env := portfwdEnv()
	path := env.FirewallConfig
	stamp := fileStamp(path)
	spec, ok := d.portForwardsWanted()
	if !ok {
		// No set to write. In rescue's direct mode every device goes out by
		// the kernel — the ones asked for included; otherwise none does.
		if d.fwProgrammed == nil && d.desired != nil && d.rescueState().Mode == rescue.ModeDirect {
			d.notePortForwardsStatus(env, "rescue|"+stamp, true, func() []netip.Addr {
				rules, _ := portfwd.LoadOwn(path)
				return portfwd.DirectAddrs(rules)
			})
		} else {
			d.notePortForwardsStatus(env, "off", false, nil)
		}
		return nil
	}
	if !force && (stamp == d.pfLoaded || (!d.pfFailAt.IsZero() && time.Since(d.pfFailAt) < directRetry)) {
		return nil
	}
	rules, err := portfwd.LoadOwn(path)
	if err != nil {
		// No config, or one uci cannot read: no device is «past the VPN» —
		// xray carries them all, as without the feature.
		rules = nil
	}
	addrs := portfwd.DirectAddrs(rules)
	load := d.pfLoad
	if load == nil {
		load = loadNFTScript
	}
	if err := load(ctx, firewall.PortForwardDirectScript(spec.TableName, addrs)); err != nil {
		d.pfFailAt = time.Now()
		d.notePortForwardsStatus(env, "failed", false, nil)
		logging.L().Warn("could not write the port forwards' «past the VPN» devices into the data plane; xray carries their traffic", "err", err.Error())
		return err
	}
	d.pfLoaded, d.pfFailAt = stamp, time.Time{}
	d.notePortForwardsStatus(env, "on|"+stamp, true, func() []netip.Addr { return addrs })
	if len(addrs) > 0 {
		logging.L().Info("port forwards: devices past the VPN", "devices", len(addrs))
	}
	return nil
}

// notePortForwardsStatus writes the status file when what it says changed
// (key): every loop asks, the file is written once per change.
func (d *daemon) notePortForwardsStatus(env portfwd.Env, key string, active bool, addrs func() []netip.Addr) {
	if key == d.pfStatusKey || env.DirectStatus == "" {
		return
	}
	st := portfwd.DirectStatus{PID: os.Getpid(), Active: active, Addrs: []string{}}
	if addrs != nil {
		for _, a := range addrs() {
			st.Addrs = append(st.Addrs, a.String())
		}
	}
	if err := portfwd.WriteDirectStatus(env.DirectStatus, st); err != nil {
		logging.L().Warn("could not record the «past the VPN» status; the router UI reads it as not in effect", "err", err.Error())
		return
	}
	d.pfStatusKey = key
}

// portfwdAlive: the process pid runs (a status file's daemon).
var portfwdAlive = func(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// portForwardsDirectActive is the answers' directActive for rules.
func portForwardsDirectActive(rules []portfwd.Rule) *bool {
	return portfwd.DirectActive(rules, portfwdEnv().DirectStatus, portfwdAlive)
}

// syncPortForwardsNow answers localctl.OpSyncPortForwards.
func (d *daemon) syncPortForwardsNow(ctx context.Context) localctl.SocketResponse {
	if err := d.maybeSyncPortForwards(ctx, true); err != nil {
		return localctl.SocketResponse{Code: "apply_failed", Detail: err.Error()}
	}
	return localctl.SocketResponse{OK: true}
}
