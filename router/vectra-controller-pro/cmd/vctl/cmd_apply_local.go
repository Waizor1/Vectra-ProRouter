package main

import (
	"context"
	"fmt"
	"os"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/state"
)

func init() {
	register(command{
		name:    "apply-local",
		summary: "Install the data plane from local documents when the panel cannot deliver one",
		run:     cmdApplyLocal,
	})
}

// cmdApplyLocal is the degradation path for a router whose panel cannot
// configure it yet.
//
// The deployed panel predates the xray-direct contract: it has no
// apply_xray_config job type, so it can never hand this controller its operator
// config, and the revision it does hand over is a PassWall one this controller
// refuses (see prod_panel_compat_test.go for the measured behaviour). The
// router still enrolls, checks in and takes terminal/log/update jobs — it just
// has no data plane.
//
// Everything needed to build one is already on the router:
//
//   - the OPERATOR config at agentcfg.XrayConfigPath — the tproxy inbound, geo
//     assets and subscription list. Written by the last successful apply, or
//     seeded by hand for a first canary.
//   - the PROVIDER document at agentcfg.ProviderConfigPath — the last-good
//     document, stored VERBATIM. When it is absent the subscription named in
//     the operator config is fetched, which is the same path a job takes.
//
// What was missing is anything that CONSUMES them unprompted. The daemon only
// brings xray up when a rendered config already exists (cmd_agent.go: run), and
// the render is written by applier.Apply, which until now ran solely inside a
// panel-queued job. So a router could hold every ingredient and sit idle — the
// ingredients present, nothing acting on them, and nothing saying so.
//
// This command is that consumer, and deliberately an OPERATOR command rather
// than an automatic fallback: a router that quietly re-applies a stale local
// config after a panel outage is a worse failure than one that waits. It runs
// the SAME applier the job path runs — splice, `xray -test` gate, atomic write,
// verbatim provider persistence — so nothing here is a parallel implementation.
//
// It stops at the config. The firewall, the xray process and state.json belong
// to the daemon; see the notes at the digest write and at the closing message
// for what goes wrong when a one-shot command takes any of them.
func cmdApplyLocal(args []string) error {
	fs := newFlagSet("apply-local")
	configPath := fs.String("config", "/etc/vectra-controller-pro/agent.json", "daemon config (JSON)")
	providerPath := fs.String("provider", "",
		"provider document to install ('-' for stdin); default: the cached last-good document, then a subscription fetch")
	logLevel := fs.String("log", "info", "log level: debug|info|warning|error")
	dryRun := fs.Bool("dry-run", false, "resolve and validate through `xray -test`, but install nothing")
	ignoreLegacy := fs.Bool("ignore-legacy-agent", false,
		"proceed even though the legacy vectra-controller agent still owns this router (two proxy stacks)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	setupLogging(*logLevel)
	log := logging.L()

	// Same mutual exclusion `supervise` enforces: this command installs the
	// config the router's xray will run, so a live legacy agent means two proxy
	// stacks the moment the service comes up. -dry-run installs nothing and is
	// exempt.
	if !*dryRun && !*ignoreLegacy {
		if l := detectLegacyAgent(legacyInitScript, runInitVerb); l.owns() {
			return legacyConflictError(l, "-ignore-legacy-agent")
		}
	}

	cfg, err := agentcfg.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load agent config: %w", err)
	}
	d, err := newDaemon(cfg)
	if err != nil {
		return err
	}
	if d.desired == nil {
		return fmt.Errorf(
			"no operator config at %s.\n"+
				"That file is what defines the tproxy inbound, the geo assets and the subscription, and it is\n"+
				"normally written by an apply_xray_config job. A panel that cannot queue that job has never\n"+
				"written it, so seed it first — `vctl validate <file>` checks one before you install it:\n"+
				"\n"+
				"    cp my-operator-config.json %s",
			cfg.XrayConfigPath, cfg.XrayConfigPath)
	}

	ctx := context.Background()

	var providerRaw []byte
	source := ""
	if *providerPath != "" {
		providerRaw, err = readFileOrStdin(*providerPath)
		if err != nil {
			return err
		}
		source = *providerPath
	} else {
		providerRaw, source, err = d.providerDocument(ctx, d.desired)
		if err != nil {
			return fmt.Errorf("provider document: %w", err)
		}
	}
	log.Info("provider document resolved", "source", source, "bytes", len(providerRaw))

	if *dryRun {
		// Validate through the same gate an install would use, then stop. The
		// applier writes nothing when WriteXray is nil-safe, so run the
		// validator directly rather than pretending an apply happened.
		if err := d.applier.Validate.Test(ctx, providerRaw); err != nil {
			return fmt.Errorf("xray -test rejected the document as-is: %w", err)
		}
		fmt.Printf("dry-run: %d provider bytes from %s would be spliced and installed\n", len(providerRaw), source)
		return nil
	}

	res, err := d.applyProvider(ctx, providerRaw, false)
	if err != nil {
		return fmt.Errorf("apply: %w", err)
	}
	d.nodeCount = countProviderOutbounds(providerRaw)

	// Write ONLY the digest, and re-read state.json first.
	//
	// d.persist() saves the whole PersistedState — rescue counters, the current
	// job, a pending job result the daemon has not submitted yet, the last
	// desired revision. The daemon is normally running while an operator types
	// this, and saving the snapshot this process loaded seconds ago would throw
	// away whatever it wrote in between. A lost pending_job_result is a job the
	// panel never hears about again.
	//
	// The digest is the one field this command owns: it is what makes the
	// daemon's next apply a no-op instead of a redundant reinstall.
	if st, err := state.Load(cfg.StatePath); err == nil {
		st.ConfigDigest = res.AppliedDigest
		// ...and the options the render was made with, or the daemon's start
		// would consider it stale and render it again.
		st.SpliceKey = d.st.SpliceKey
		if err := state.Save(cfg.StatePath, st); err != nil {
			log.Warn("persist config digest failed", "err", err.Error())
		}
	} else {
		log.Warn("could not re-read state to record the digest", "err", err.Error())
	}

	fmt.Printf("installed: changed=%v noop=%v digest=%s xrayBytes=%d outbounds=%d\n",
		res.Changed, res.Noop, res.AppliedDigest, res.XrayBytes, d.nodeCount)
	if len(res.DroppedInbounds) > 0 {
		fmt.Printf("dropped provider inbounds: %v\n", res.DroppedInbounds)
	}
	// This command installs the CONFIG and stops there. It does not program the
	// firewall and it does not start xray, and that is the design, not a gap.
	//
	// The daemon is the single owner of the kernel state: on boot it programs
	// the ruleset whenever a rendered config exists, and its check-in is the
	// only thing that can confirm the commit-confirm deadman. A command that
	// armed that deadman and then exited would leave nothing to confirm it — the
	// data-plane stand demonstrated exactly that, carrying real traffic through
	// an apply-local ruleset and then losing the table and the policy route
	// ninety seconds later. An operator would see the proxy work, walk away, and
	// come back to a direct router.
	//
	// So the last step is the operator's, and it is printed rather than implied:
	// an installed config nothing is running is the same silence this command
	// exists to end.
	fmt.Fprint(os.Stderr,
		"\nThe config is installed. It is NOT live yet — the daemon owns the firewall and the\n"+
			"xray process, and programs both from this config on start. Bring the data plane up with:\n"+
			"\n    /etc/init.d/vectra-controller-pro restart\n")
	return nil
}
