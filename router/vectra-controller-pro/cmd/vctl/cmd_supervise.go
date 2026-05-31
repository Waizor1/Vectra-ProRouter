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
	dryRun := fs.Bool("dry-run", false, "render config and exit (do not exec xray)")
	statusOut := fs.String("status", "", "write status JSON to this path periodically")
	tickEvery := fs.Int("tick", 5, "monitor tick interval (seconds)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		fs.Usage()
		return fmt.Errorf("-config is required")
	}
	c, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	setupLogging(c.Instance.LogLevel)
	log := logging.L()

	eng := xray.New()
	if err := eng.Validate(context.Background(), c); err != nil {
		return fmt.Errorf("engine validate: %w", err)
	}
	data, err := eng.Render(context.Background(), c)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	if c.Process.ConfigFile == "" {
		c.Process.ConfigFile = filepath.Join(c.Process.WorkDir, "xray.json")
	}
	if c.Process.WorkDir == "" {
		c.Process.WorkDir = filepath.Dir(c.Process.ConfigFile)
	}
	proc := supervisor.NewProcess(c.Process)
	if err := proc.WriteXrayConfig(data); err != nil {
		return fmt.Errorf("write xray config: %w", err)
	}
	log.Info("rendered xray config", "path", c.Process.ConfigFile, "bytes", len(data))

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
