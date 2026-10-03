package main

import (
	"os"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/power"
)

// No VPN gap when vctl takes a router over from PassWall2 before the panel
// has sent it an operator config. Until 0.7.0-r14 such a vctl ran without a
// data plane, and the LAN went out directly until the claim's apply came: 17
// minutes on the router migrated on 2026-10-03, most of them a claim code
// passed on by people.
//
// Now it routes by PassWall2's own configuration — route_source 'passwall',
// chosen by vctl, not by the owner: AUTOMATIC — on the operator config the
// panel gives every router, without its subscription (config.Base). The
// moment the panel's own arrives (the claim, apply_xray_config), vctl runs
// that and routes by the provider again: the route source it chose goes with
// the reason for it. The owner's route source is never touched: one set in
// UCI (passwall, native) keeps vctl from choosing at all.
//
// Nothing of it is written: no UCI, no operator config on /etc. The choice is
// made again at every start, from what is there (power.AutoPassWall), and is
// the same until the operator config is — so nothing has to be undone after
// it, and the operator config's absence keeps saying the router is not
// linked. A router without PassWall2 — a fresh install — has nothing to route
// by, and waits for its setup as before.

// autoRouteSource: a vctl started with cfg routes by PassWall2's
// configuration of itself.
func autoRouteSource(cfg agentcfg.Config) bool {
	return power.AutoPassWall(cfg.RouteSource, cfg.XrayConfigPath, passwallUCIFile, passwallGenerator)
}

// enterAutoRoute makes d route by PassWall2's configuration on the base
// operator config. Before the applier is built: its document is PassWall's.
func (d *daemon) enterAutoRoute() {
	name, _ := os.Hostname()
	d.cfg.RouteSource = routeSourcePassWall
	d.desired = config.Base(name)
	d.autoRoute = true
	logging.L().Info("no operator config yet: routing by PassWall2's configuration until the panel's arrives (route_source 'passwall', automatic)")
}

// leaveAutoRoute is the panel's operator config arriving on a router that
// routes by PassWall2's configuration of itself: the provider's route source
// again. What PassWall's renders were made from goes with it; the render
// that runs keeps running until the provider's replaces it — no gap.
func (d *daemon) leaveAutoRoute() bool {
	if !d.autoRoute {
		return false
	}
	d.autoRoute = false
	d.cfg.RouteSource = ""
	d.passwallSniffing = nil
	d.passwallCfgStamp, d.passwallGeoStamp = "", ""
	logging.L().Info("the panel's operator config arrived: routing by the provider again, not by PassWall2's configuration")
	return true
}

// linked: the router has the panel's operator config — not the base one it
// routes by PassWall2 on. Until then it shows a claim code.
func (d *daemon) linked() bool {
	return d.desired != nil && !d.autoRoute
}
