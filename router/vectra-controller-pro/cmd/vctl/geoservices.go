package main

import (
 "vectra-controller-pro/internal/vault"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/geodat"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/sites"
)

// «+ сервис» (spec decision 4): the owner adds a service — a category of the
// router's geo file — to «Мои сайты», through the VPN or around it. The
// router offers the categories of the file xray runs with and keeps a service
// only when that file has it: xray refuses a render naming a category it
// lacks.

// hiddenServices are categories never offered: "private" is the LAN —
// through the VPN it would cut the router off its own network.
var hiddenServices = map[string]bool{"private": true}

// geoServices are the services a geo directory's geosite.dat offers,
// lowercased and sorted. ok is false when the file cannot be read now (none,
// not a geo file, or too little memory to read it): then nothing is offered,
// and nothing is judged by it.
func geoServices(dir string) (out []string, ok bool) {
	path := filepath.Join(dir, "geosite.dat")
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return nil, false
	}
	if in, err := memguard.Read(); err == nil && in.AvailableKB < memguard.HeavyFloorKB(in.TotalKB)+uint64(st.Size()>>10) {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	codes, err := geodat.Codes(b)
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	for _, c := range codes {
		c = strings.ToLower(c)
		if c == "" || seen[c] || hiddenServices[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	sort.Strings(out)
	return out, true
}

// servicesOf are the services named in the lists, without their prefix.
func servicesOf(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, e := range l {
			if strings.HasPrefix(e, sites.ServicePrefix) {
				out = append(out, strings.TrimPrefix(e, sites.ServicePrefix))
			}
		}
	}
	return out
}

// rpcdGeoDir is the geo directory xray runs with: the installed render's, as
// state.json keeps it — read, never written: the daemon owns that file —
// else the configured one.
func rpcdGeoDir(cfg agentcfg.Config) string {
	if raw, err := vault.ReadFile(cfg.StatePath); err == nil {
		var st struct {
			RenderAssetDir string `json:"render_asset_dir"`
		}
		if json.Unmarshal(raw, &st) == nil && st.RenderAssetDir != "" {
			return st.RenderAssetDir
		}
	}
	return config.ResolveGeoAssetDir(cfg.GeoAssetDir)
}

// withRuntime is everything the router adds to a render of providerRaw
// beyond the owner's overrides: DNS through the tunnel, the exit probe and
// the exits left out, the services the geo file has.
func (d *daemon) withRuntime(opts xray.SpliceOptions, providerRaw []byte) xray.SpliceOptions {
	opts = d.withDNS(opts)
	opts = d.withExits(opts, providerRaw)
	return d.withKnownServices(opts)
}

// withKnownServices drops from the owner's lists a service the geo file no
// longer has (an update removed it) — the render goes on without it instead
// of failing and keeping the router on its old one. A file that cannot be
// read now judges nothing: the gate (`xray -test`) still does.
func (d *daemon) withKnownServices(opts xray.SpliceOptions) xray.SpliceOptions {
	if len(servicesOf(opts.Rules.Direct, opts.Rules.Proxy)) == 0 {
		return opts
	}
	known, ok := geoServices(d.geoAssetDir())
	if !ok {
		return opts
	}
	have := map[string]bool{}
	for _, c := range known {
		have[c] = true
	}
	keep := func(list []string) []string {
		var out []string
		for _, e := range list {
			if name, isSvc := strings.CutPrefix(e, sites.ServicePrefix); isSvc && !have[name] {
				logging.L().Warn("a service in «Мои сайты» is not in the geo file; rendering without it", "service", name)
				continue
			}
			out = append(out, e)
		}
		return out
	}
	opts.Rules = xray.UserRules{Direct: keep(opts.Rules.Direct), Proxy: keep(opts.Rules.Proxy)}
	return opts
}
