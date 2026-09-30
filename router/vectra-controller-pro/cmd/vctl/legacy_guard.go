package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Exactly one controller owns a router. vctl drives xray directly; the legacy
// vectra-controller agent drives the PassWall2 stack. Both program the same
// nftables rules, the same routing table and the same LAN, and the legacy agent
// runs a watchdog that restarts PassWall whenever it finds it stopped.
//
// The mutual exclusion is implemented in exactly ONE place: start_service in
// openwrt/files/etc/init.d/vectra-controller-pro, which stops the legacy agent,
// disables it and writes a hand-back marker. Every path that starts xray
// WITHOUT going through that init script bypasses it — and `vctl supervise`
// typed by hand is such a path. That is not hypothetical: it is how the canary
// ended up with two proxy stacks fighting, PassWall resurrected under vctl by
// the legacy agent's own watchdog.
//
// legacyInitScript is the path start_service probes, kept in lockstep with it.
// A var, not a const, only so the tests can drive the real command against a
// real script instead of re-implementing the gate.
var legacyInitScript = "/etc/init.d/vectra-controller"

// legacyAgent is what the legacy init script says about itself.
type legacyAgent struct {
	Installed bool
	Enabled   bool
	Running   bool
}

// owns reports whether the legacy agent would still act on this router. Enabled
// is the load-bearing signal — it is what survives a reboot and what the legacy
// watchdog is started from. Running catches the narrower case of an agent
// started by hand while disabled.
func (l legacyAgent) owns() bool { return l.Installed && (l.Enabled || l.Running) }

func (l legacyAgent) describe() string {
	var s []string
	if l.Enabled {
		s = append(s, "enabled")
	}
	if l.Running {
		s = append(s, "running")
	}
	if len(s) == 0 {
		s = append(s, "installed")
	}
	return strings.Join(s, " and ")
}

// runInitVerb runs `<script> <verb>` and reports whether it exited 0. rc.common
// answers `enabled` from the /etc/rc.d symlink, which is the same source of
// truth start_service uses, and the same probe it already runs. A script with
// no `running` verb makes rc.common print usage and exit non-zero, which is
// read here as "not running" — deliberately, since Enabled is what matters.
func runInitVerb(script, verb string) error {
	cmd := exec.Command(script, verb)
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Run()
}

// detectLegacyAgent probes the legacy init script. run is injected so the
// probe is testable without an OpenWrt box.
func detectLegacyAgent(script string, run func(script, verb string) error) legacyAgent {
	fi, err := os.Stat(script)
	if err != nil || fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		// Not installed, or not executable — start_service's own `[ -x ]` test
		// would skip it too, so there is nothing here that could take the
		// router back.
		return legacyAgent{}
	}
	return legacyAgent{
		Installed: true,
		Enabled:   run(script, "enabled") == nil,
		Running:   run(script, "running") == nil,
	}
}

// legacyConflictError is the refusal `vctl supervise` returns. It names the
// supported way to take the router, because "refused" without "do this instead"
// is how an operator ends up reaching for the override.
func legacyConflictError(l legacyAgent, overrideFlag string) error {
	return fmt.Errorf(
		"the legacy vectra-controller agent still owns this router (%s at %s).\n"+
			"Starting xray here does NOT stop it: its watchdog restarts the PassWall stack, and the two\n"+
			"proxy stacks then fight over the same nft rules, the same routing table and the same LAN.\n"+
			"\n"+
			"Hand the router over the supported way — it stops the legacy agent, disables it, and writes\n"+
			"the marker that lets a later stop or opkg remove hand the router BACK:\n"+
			"\n"+
			"    /etc/init.d/vectra-controller-pro start\n"+
			"\n"+
			"To start anyway, accepting two live proxy stacks on this router: %s",
		l.describe(), legacyInitScript, overrideFlag)
}
