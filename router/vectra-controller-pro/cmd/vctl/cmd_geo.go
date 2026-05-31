package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/geo"
)

func cmdGeo(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("geo: subcommand required: update | verify")
	}
	switch args[0] {
	case "update":
		return geoUpdate(args[1:])
	case "verify":
		return geoVerify(args[1:])
	default:
		return fmt.Errorf("geo: unknown subcommand %q", args[0])
	}
}

func geoUpdate(args []string) error {
	fs := newFlagSet("geo update")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
	timeout := fs.Int("timeout", 120, "per-asset timeout (seconds)")
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
	assets := assetsFromConfig(c)
	if len(assets) == 0 {
		return fmt.Errorf("config has no geo URLs to update")
	}
	hc := &http.Client{Timeout: time.Duration(*timeout) * time.Second}
	ctx := context.Background()
	for _, a := range assets {
		r := geo.UpdateOne(ctx, c.Geo.AssetDir, a, hc)
		if r.Error != nil {
			fmt.Printf("fail %s: %v\n", a.Filename, r.Error)
			continue
		}
		state := "skip(hash-match)"
		if r.Updated {
			state = fmt.Sprintf("updated %d bytes", r.Bytes)
		}
		fmt.Printf("%-15s %s sha256=%s (%s)\n", a.Filename, state, r.SHA256, r.Took)
	}
	return nil
}

func geoVerify(args []string) error {
	fs := newFlagSet("geo verify")
	cfgPath := fs.String("config", "", "operator config JSON (required)")
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
	for _, a := range assetsFromConfig(c) {
		fmt.Printf("%-15s url=%s sha256=%s\n", a.Filename, a.URL, defaultStr(a.ExpectedSHA256, "(none)"))
	}
	return nil
}

func defaultStr(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func assetsFromConfig(c *config.Config) []geo.Asset {
	var out []geo.Asset
	if c.Geo.GeoIPURL != "" {
		out = append(out, geo.Asset{Filename: "geoip.dat", URL: c.Geo.GeoIPURL})
	}
	if c.Geo.GeoSiteURL != "" {
		out = append(out, geo.Asset{Filename: "geosite.dat", URL: c.Geo.GeoSiteURL})
	}
	for _, e := range c.Geo.ExtraAssets {
		out = append(out, geo.Asset{Filename: e.Filename, URL: e.URL, ExpectedSHA256: e.SHA256})
	}
	return out
}
