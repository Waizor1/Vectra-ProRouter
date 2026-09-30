package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/routepolicy"
)

func init() {
	register(command{name: "passwall-render", summary: "render the operator's route policy as route_source 'passwall' (or, with -native, 'native') would, and test it with xray — installs nothing; -compare proves vctl's generator against PassWall2's on this router", run: cmdPassWallRender})
}

// cmdPassWallRender is the PassWall-compatible render without the daemon:
// the generator, the adaptation, the splice with vctl's DNS options, and
// `xray run -test` with the policy's geo files. It writes the result to -out
// and changes nothing that runs — the check before route_source 'passwall'
// or 'native' is set on a live router.
//
// -native makes the render with vctl's own generator, from vctl's store
// (/etc/config/vectra_route) or, before the import, from PassWall2's
// configuration as it is — never writing either.
//
// -compare makes it with both generators from PassWall2's configuration and
// says where they differ, by JSON path only: the documents carry the
// subscription's keys, and no value of theirs is printed.
func cmdPassWallRender(args []string) error {
	fs := newFlagSet("passwall-render")
	agentPath := fs.String("config", "/var/run/vectra-controller-pro/agent.json", "the daemon's agent config")
	out := fs.String("out", "/tmp/vctl-passwall-render.json", "where to write the render (0600)")
	native := fs.Bool("native", false, "vctl's own generator (route_source 'native')")
	compare := fs.Bool("compare", false, "both generators on PassWall2's configuration: where do they differ?")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := agentcfg.Load(*agentPath)
	if err != nil {
		return err
	}
	cfg.RouteSource = routeSourcePassWall
	if *native {
		cfg.RouteSource = routeSourceNative
	}
	d := &daemon{cfg: cfg}
	desired, err := d.loadDesiredConfig()
	if err != nil {
		return err
	}
	d.desired = desired
	if desired.Inbounds.Tproxy == nil {
		return fmt.Errorf("the operator config has no tproxy inbound")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if *compare {
		return comparePassWallRender(ctx, d)
	}
	var raw []byte
	assetDir := passwallAssetDir()
	if *native {
		secs, from, err := nativePolicyReadOnly()
		if err != nil {
			return err
		}
		fmt.Printf("route policy: %s\n", from)
		if raw, err = d.generateFrom(ctx, secs); err != nil {
			return err
		}
		if fileExists(filepath.Join(nativeGeoDir, "geoip.dat")) && fileExists(filepath.Join(nativeGeoDir, "geosite.dat")) {
			assetDir = nativeGeoDir
		}
	} else if raw, err = d.generatePassWall(ctx); err != nil {
		return err
	}
	tag := desired.Inbounds.Tproxy.Tag
	if tag == "" {
		tag = "tproxy-in"
	}
	doc, pres, err := xray.AdaptPassWall(raw, tag)
	if err != nil {
		return err
	}
	t := *desired.Inbounds.Tproxy
	t.Sniffing = pres.Sniffing
	opts, _ := spliceOptionsFor(doc, localctl.Overrides{}, false)
	opts = d.withRuntime(opts, doc)
	spliced, res, err := xray.Splice(doc, &t, opts)
	if err != nil {
		return err
	}
	v := xray.Validator{Binary: cfg.XrayBinary, AssetDir: assetDir}
	if err := v.Test(ctx, spliced); err != nil {
		return fmt.Errorf("xray -test refused the render (geo files: %s): %w", assetDir, err)
	}
	if err := os.WriteFile(*out, spliced, 0o600); err != nil {
		return err
	}
	who := "PassWall2's generator"
	if *native {
		who = "vctl's generator"
	}
	fmt.Printf("the route policy renders (%s) and passes xray -test with the geo files in %s: %d bytes -> %s\n", who, assetDir, len(spliced), *out)
	fmt.Printf("  rules %d, FakeDNS %s, sniffing %s routeOnly=%t\n", pres.Rules, strings.Join(pres.FakeDNSPools, ","), strings.Join(pres.Sniffing.DestOverride, ","), pres.Sniffing.RouteOnly)
	fmt.Printf("  DNS through the tunnel: %s (node names answered directly: %d)%s\n", orNone(res.DNS.Listen), res.DNS.NodeHosts, skipped(res.DNS.Skipped))
	fmt.Printf("  outbounds marked: %d; owner's sites: %s\n", res.OutboundsMarked, res.UserRules.Describe())
	return nil
}

// nativePolicyReadOnly is the store, or PassWall2's configuration where
// there is no store yet — read, never imported.
func nativePolicyReadOnly() ([]routepolicy.Section, string, error) {
	path := nativeStorePath
	if !fileExists(path) {
		path = passwallUCIFile
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	secs, err := routepolicy.ParseUCI(string(b))
	return secs, path, err
}

// comparePassWallRender runs both generators on PassWall2's configuration.
func comparePassWallRender(ctx context.Context, d *daemon) error {
	theirs, err := d.generatePassWall(ctx)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(passwallUCIFile)
	if err != nil {
		return err
	}
	secs, err := routepolicy.ParseUCI(string(b))
	if err != nil {
		return err
	}
	ours, err := d.generateFrom(ctx, secs)
	if err != nil {
		return fmt.Errorf("vctl's generator: %w", err)
	}
	var a, t any
	if err := json.Unmarshal(ours, &a); err != nil {
		return err
	}
	if err := json.Unmarshal(theirs, &t); err != nil {
		return err
	}
	var paths []string
	jsonDiffPaths("$", a, t, &paths)
	ver, _ := d.xrayVersionNow(ctx)
	if len(paths) == 0 {
		fmt.Printf("identical: vctl's generator and PassWall2's make the same configuration from this router's route policy (xray %s, %d bytes)\n", ver, len(theirs))
		return nil
	}
	fmt.Printf("DIFFERENT in %d place(s) (xray %s; paths only, no values):\n", len(paths), ver)
	for _, p := range paths {
		fmt.Println("  " + p)
	}
	return fmt.Errorf("the generators differ")
}

// jsonDiffPaths lists where two JSON values differ, by path.
func jsonDiffPaths(path string, a, b any, out *[]string) {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, path+": an object vs something else")
			return
		}
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			av, aok := x[k]
			bv, bok := y[k]
			switch {
			case !aok:
				*out = append(*out, path+"."+k+": only PassWall2's")
			case !bok:
				*out = append(*out, path+"."+k+": only vctl's")
			default:
				jsonDiffPaths(path+"."+k, av, bv, out)
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			*out = append(*out, path+": an array vs something else")
			return
		}
		if len(x) != len(y) {
			*out = append(*out, fmt.Sprintf("%s: %d items vs PassWall2's %d", path, len(x), len(y)))
		}
		for i := 0; i < len(x) && i < len(y); i++ {
			jsonDiffPaths(fmt.Sprintf("%s[%d]", path, i), x[i], y[i], out)
		}
	default:
		if !reflect.DeepEqual(a, b) {
			*out = append(*out, path+": the value differs")
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func skipped(s string) string {
	if s == "" {
		return ""
	}
	return " — skipped: " + s
}
