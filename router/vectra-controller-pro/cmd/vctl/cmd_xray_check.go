package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/vault"
)

func init() {
	register(command{name: "xray-check", summary: "Check that an xray binary loads the config this router runs", run: cmdXrayCheck})
}

// routeAssetDir is the geo directory xray starts with for cfg's route
// source, as the daemon picks it at start (cfg.RouteSource already settled).
func routeAssetDir(cfg agentcfg.Config) string {
	switch cfg.RouteSource {
	case routeSourcePassWall:
		return passwallAssetDir()
	case routeSourceNative:
		return nativeAssetDir()
	}
	return config.ResolveGeoAssetDir(cfg.GeoAssetDir)
}

// cmdXrayCheck runs the write gate's `xray run -test` on the render xray runs
// now, with another binary: does a new xray take THIS router's config, before
// it replaces the one carrying the LAN (xray 26.9.8 refuses configs 26.7.28
// ran). Nothing is changed. xray's own output is not shown: a parse error can
// quote the config's credentials.
func cmdXrayCheck(args []string) error {
	fs := newFlagSet("xray-check")
	cfgPath := fs.String("config", "/var/run/vectra-controller-pro/agent.json", "daemon path configuration")
	binary := fs.String("binary", "", "the xray (or a wrapper that execs it) to check with")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *binary == "" || fs.NArg() != 0 {
		fs.Usage()
		return errors.New("usage: vctl xray-check -binary <path> [-config <agent.json>]")
	}
	cfg, err := agentcfg.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("load agent config: %w", err)
	}
	render, err := vault.ReadFile(cfg.XrayRenderPath)
	if err != nil {
		return fmt.Errorf("no running config to check: %w", err)
	}
	defer clear(render)
	if autoRouteSource(cfg) {
		cfg.RouteSource = routeSourcePassWall
	}
	assetDir := routeAssetDir(cfg)
	if st, err := state.LoadReadOnly(cfg.StatePath); err == nil && st.RenderAssetDir != "" {
		assetDir = st.RenderAssetDir // the directory the running render passed the gate with
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, *binary, "version").Output()
	if err != nil {
		return fmt.Errorf("%s does not run: %w", *binary, err)
	}
	version, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")

	v := xray.Validator{Binary: *binary, AssetDir: assetDir, Timeout: 60 * time.Second, OOMScoreAdj: memguard.TransientAdj}
	if mi, err := memguard.Read(); err == nil {
		v.MemFloorKB = memguard.HeavyFloorKB(mi.TotalKB)
	}
	if err := v.Test(ctx, render); err != nil {
		return fmt.Errorf("%s: %w", version, err)
	}
	fmt.Fprintf(os.Stdout, "xray-check: OK: %s loads the running config (%d bytes, route source %s, geo %s)\n",
		version, len(render), orDash(cfg.RouteSource), assetDir)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
