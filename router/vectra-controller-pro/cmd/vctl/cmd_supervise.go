package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/supervisor"
)

func cmdSupervise(args []string) error {
	fs := newFlagSet("supervise")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	providerPath := fs.String("provider", "", "provider Xray document, one entry (required); '-' for stdin")
	dryRun := fs.Bool("dry-run", false, "write the spliced config and exit (do not exec xray)")
	statusOut := fs.String("status", "", "write status JSON to this path periodically")
	tickEvery := fs.Int("tick", 5, "monitor tick interval (seconds)")
	ignoreLegacy := fs.Bool("ignore-legacy-agent", false,
		"start even though the legacy vectra-controller agent still owns this router (two proxy stacks)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *providerPath == "" {
		fs.Usage()
		return fmt.Errorf("-config and -provider are required")
	}

	// Mutual exclusion. This command is a human typing on a live router, and it
	// bypasses the ONLY place the hand-over is implemented (start_service in the
	// packaged init script). Refuse rather than warn: a warning scrolls past,
	// and the failure it precedes is the legacy watchdog bringing PassWall back
	// underneath a running xray. -dry-run is exempt because it starts nothing.
	if !*dryRun && !*ignoreLegacy {
		if l := detectLegacyAgent(legacyInitScript, runInitVerb); l.owns() {
			return legacyConflictError(l, "-ignore-legacy-agent")
		}
	}

	c, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	setupLogging(c.Instance.LogLevel)
	log := logging.L()

	providerRaw, err := readFileOrStdin(*providerPath)
	if err != nil {
		return err
	}
	if err := xray.ScanAllowInsecure(providerRaw); err != nil {
		return err
	}
	data, spliceRes, err := xray.SpliceInbounds(providerRaw, c.Inbounds.Tproxy)
	if err != nil {
		return fmt.Errorf("splice: %w", err)
	}

	if c.Process.ConfigFile == "" {
		c.Process.ConfigFile = filepath.Join(c.Process.WorkDir, "xray.json")
	}
	if c.Process.WorkDir == "" {
		c.Process.WorkDir = filepath.Dir(c.Process.ConfigFile)
	}
	// Gate the write on xray's own parser, exactly as the daemon does, with
	// the geo files the process will read.
	assetDir := config.ResolveGeoAssetDir(c.Geo.AssetDir)
	validator := xray.Validator{Binary: c.Process.XrayBinary, AssetDir: assetDir}
	if err := validator.Test(context.Background(), data); err != nil {
		return err
	}
	proc := supervisor.NewProcessWithAssetDir(c.Process, assetDir)
	proc.SetConfigSource(func() ([]byte, error) { return append([]byte(nil), data...), nil })
	log.Info("spliced provider config",
		"path", c.Process.ConfigFile, "bytes", len(data),
		"keptKeys", len(spliceRes.TopLevelKeys)-1, "droppedInbounds", spliceRes.DroppedInbounds)

	if *dryRun {
		log.Info("dry-run: config written, exiting without exec")
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling — SIGTERM/SIGINT cleanly stops the supervisor.
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigs
		log.Info("signal received; shutting down", "sig", s.String())
		_ = proc.Stop(ctx)
		cancel()
	}()

	mon := &supervisor.Monitor{
		Process:    proc,
		StatusPath: *statusOut,
		Interval:   time.Duration(*tickEvery) * time.Second,
		MemSoftMiB: c.Process.MemorySoftMiB,
	}
	go mon.Run(ctx)

	log.Info("supervisor starting", "binary", c.Process.XrayBinary, "config", c.Process.ConfigFile)
	if err := proc.Run(ctx); err != nil {
		return err
	}
	return nil
}
