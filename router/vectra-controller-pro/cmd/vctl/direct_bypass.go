package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/geodat"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/netrange"
	"vectra-controller-pro/internal/rescue"
)

// Direct destinations routed by the kernel.
//
// What the routing sends straight out by address anyway (xray.DirectBypass:
// on the fleet, the panel's geoip:DIRECT — Russian networks) is loaded into
// the data plane's direct sets, and a LAN connection to it never enters xray
// (firewall.Spec.DirectCtMark). PassWall2 did the same; vctl carried all of
// it through xray, at ~38 KB of xray's heap per connection. It also means
// Russian sites keep working through an xray restart.
//
// The sets cost the kernel ~210 bytes a range and the nft process that loads
// them ~1.5 KB a range at its peak (15k ranges: 3 MB and 24 MB, measured on
// the data-plane stand). So the list is merged into the fewest ranges first,
// and loaded only as far as the memory free right now allows: the largest
// ranges first (the top 4000 of the fleet's 14.8k cover 92% of its
// addresses), the rest left to xray, which sends them straight out as before.
// A partial or failed load costs efficiency, never correctness.

// directRetry is how long a failed or deferred load waits before the next
// try; directUpgrade how long a partial load (memory was short) stands
// before a fuller one is tried.
const (
	directRetry   = 5 * time.Minute
	directUpgrade = 30 * time.Minute
)

// directUpgradeDue: the sets hold part of the list, long enough ago to try
// for more.
func (d *daemon) directUpgradeDue() bool {
	return d.directPartial && time.Since(d.directLoadedAt) >= directUpgrade
}

// fileStamp is a file's size and modification time ("-" when absent).
func fileStamp(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("%d/%d", fi.Size(), fi.ModTime().UnixNano())
}

// directBypassWanted: the data plane is loaded by this process, in proxy mode,
// and the owner has not turned the kernel's direct routing off.
func (d *daemon) directBypassWanted() bool {
	return !d.cfg.NoDirectBypass && d.fwProgrammed != nil && d.desired != nil &&
		d.rescueState().Mode != rescue.ModeDirect
}

// maybeLoadDirect brings the direct sets in line with the running render and
// its geo files, when they are not already.
func (d *daemon) maybeLoadDirect(ctx context.Context) {
	if !d.directBypassWanted() {
		return
	}
	spec, ok := firewallSpecFromConfig(d.desired)
	if !ok || spec.DirectCtMark == 0 {
		return
	}
	assetDir := d.runningAssetDir()
	// Unchanged since the last load: nothing to read (the render is 75 KB of
	// JSON, and this runs every loop).
	stamp := d.directStampNow(assetDir)
	if d.directLoaded != "" && stamp == d.directStamp && !d.directUpgradeDue() {
		return
	}
	render, err := os.ReadFile(d.cfg.XrayRenderPath)
	if err != nil {
		return
	}
	tag := "tproxy-in"
	if d.desired.Inbounds.Tproxy != nil && d.desired.Inbounds.Tproxy.Tag != "" {
		tag = d.desired.Inbounds.Tproxy.Tag
	}
	src, err := xray.DirectBypass(render, tag)
	if err != nil {
		return
	}
	fake := fakeDNSPrefixes(render)
	key := directKey(src, assetDir, fake)
	if key == d.directLoaded && !d.directUpgradeDue() {
		d.directStamp = stamp
		return
	}
	if key == d.directFailKey && time.Since(d.directFailAt) < directRetry {
		return
	}
	load := d.directLoad
	if load == nil {
		load = loadNFTScript
	}
	fail := func(msg string, args ...any) {
		d.directFailKey, d.directFailAt = key, time.Now()
		logging.L().Warn(msg, args...)
		// The sets hold an older list, and out of date is not "straight out
		// as before": what left the list must go back to xray. Emptying them
		// is cheap — nft reads nothing back for a set it flushes.
		if d.directLoaded != "" && d.directLoaded != key {
			if err := load(ctx, directScript(spec, nil, nil)); err == nil {
				d.directLoaded, d.directCount, d.directPartial = "", 0, false
				logging.L().Warn("the direct sets were emptied: the list they held is no longer the routing's; xray carries those addresses until the new list is in")
			}
		}
	}

	// Memory first: reading the list is itself a few MB of vctl's heap.
	budget := -1 // not known: all of it
	readMem := d.readMem
	if readMem == nil {
		readMem = memguard.Read
	}
	if mi, err := readMem(); err == nil && !src.Empty() {
		budget = memguard.ElementBudget(mi)
		if budget == 0 {
			fail("too little free memory to route direct addresses in the kernel now; xray carries them, it is tried again later",
				"available_mib", memguard.MiB(mi.AvailableKB))
			return
		}
		if key == d.directLoaded && budget <= d.directCount {
			// An upgrade that would load no more than the sets hold: not now.
			d.directLoadedAt = time.Now()
			return
		}
	}

	v4, v6, err := directPrefixes(src, assetDir)
	if err != nil {
		fail("could not read the addresses the routing sends straight out; xray carries them", "err", err.Error())
		return
	}
	// What earlier rules send elsewhere by address is never direct.
	x4, x6, err := directPrefixes(xray.DirectSource{Prefixes: src.ExcludePrefixes, GeoIP: src.ExcludeGeoIP}, assetDir)
	if err != nil {
		fail("could not read the addresses earlier rules proxy; xray carries the direct ones", "err", err.Error())
		return
	}
	r4 := netrange.Subtract(netrange.Merge(v4), append(append([]netip.Prefix{}, fake...), x4...))
	var r6 []netrange.Range
	// While IPv6 is refused (UCI ipv6 unset) the refusal comes before the v6
	// set in both chains: it would buy nothing, and the budget goes to IPv4.
	if spec.IPv6Enabled && d.cfg.IPv6 {
		r6 = netrange.Subtract(netrange.Merge(v6), append(append([]netip.Prefix{}, fake...), x6...))
	}
	total := len(r4) + len(r6)
	if budget < 0 {
		budget = total
	}
	k6, share6 := netrange.Largest(r6, min(len(r6), budget/8))
	k4, share4 := netrange.Largest(r4, budget-len(k6))

	script := directScript(spec, k4, k6)
	err = load(ctx, script)
	loaded := len(k4) + len(k6)
	// The list, its ranges and the script were a few MB of vctl's heap for
	// a moment: give them back now, not whenever Go gets round to it.
	script, v4, v6, r4, r6, k4, k6 = "", nil, nil, nil, nil, nil, nil
	debug.FreeOSMemory()
	if err != nil {
		fail("could not load the direct addresses into the data plane; xray carries them", "err", err.Error())
		return
	}
	d.directLoaded, d.directFailKey = key, ""
	d.directCount, d.directPartial, d.directLoadedAt = loaded, loaded < total, time.Now()
	d.directGeoFiles = geoFiles(src, assetDir)
	d.directStamp = d.directStampNow(assetDir)
	if total == 0 {
		logging.L().Info("the routing sends nothing straight out by address; the direct sets are empty", "stop", src.StopReason)
		return
	}
	logging.L().Info("direct destinations routed by the kernel, not xray",
		"ranges", loaded, "of", total,
		"covered_v4", fmt.Sprintf("%.1f%%", share4*100), "covered_v6", fmt.Sprintf("%.1f%%", share6*100),
		"rules", src.Rules, "geoip", geoNames(src.GeoIP))
}

// directStampNow stamps what a load is made from: the render, and the geo
// files the last load read (geoip.dat before the first).
func (d *daemon) directStampNow(assetDir string) string {
	files := d.directGeoFiles
	if len(files) == 0 {
		files = []string{filepath.Join(assetDir, "geoip.dat")}
	}
	var b strings.Builder
	b.WriteString(fileStamp(d.cfg.XrayRenderPath))
	for _, f := range files {
		b.WriteString("|" + f + ":" + fileStamp(f))
	}
	return b.String()
}

// geoFiles are the geo files a source reads.
func geoFiles(src xray.DirectSource, assetDir string) []string {
	seen := map[string]bool{}
	var out []string
	for _, g := range append(append([]xray.GeoRef{}, src.GeoIP...), src.ExcludeGeoIP...) {
		p := filepath.Join(assetDir, g.File)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// directKey identifies what the sets are to hold: the render's direct
// source, the FakeDNS ranges taken out of it, and the geo files it names as
// they are on disk now (a nightly geo update is a new list).
func directKey(src xray.DirectSource, assetDir string, fake []netip.Prefix) string {
	h := sha256.New()
	h.Write([]byte(src.Key()))
	for _, p := range fake {
		fmt.Fprintf(h, "|-%s", p)
	}
	for _, p := range geoFiles(src, assetDir) {
		fmt.Fprintf(h, "|%s:%s", p, fileStamp(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// directPrefixes reads the source's literal prefixes and geoip categories.
func directPrefixes(src xray.DirectSource, assetDir string) (v4, v6 []netip.Prefix, err error) {
	for _, p := range src.Prefixes {
		if p.Addr().Is4() {
			v4 = append(v4, p)
		} else {
			v6 = append(v6, p)
		}
	}
	byFile := map[string][]string{}
	for _, g := range src.GeoIP {
		byFile[g.File] = append(byFile[g.File], g.Code)
	}
	for file, codes := range byFile {
		cats, err := geodat.Load(filepath.Join(assetDir, file), codes...)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", file, err)
		}
		for _, c := range codes {
			cat, ok := cats[strings.ToUpper(c)]
			if !ok {
				return nil, nil, fmt.Errorf("%s has no category %q", file, c)
			}
			v4 = append(v4, cat.V4...)
			v6 = append(v6, cat.V6...)
		}
	}
	return v4, v6, nil
}

// fakeDNSPrefixes are the render's FakeDNS pools and the reserved range they
// come from: addresses that stand for domains only xray knows. Never direct.
func fakeDNSPrefixes(render []byte) []netip.Prefix {
	out := []netip.Prefix{netip.MustParsePrefix("198.18.0.0/15")}
	for _, s := range xray.RenderFakeDNSPools(render) {
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// directScript is one nft transaction replacing the direct sets' contents:
// the flush and the load land together, so no connection finds them half
// filled.
func directScript(spec firewall.Spec, v4, v6 []netrange.Range) string {
	var b strings.Builder
	set := func(name string, rs []netrange.Range, have bool) {
		if !have {
			return
		}
		fmt.Fprintf(&b, "flush set inet %s %s\n", spec.TableName, name)
		if len(rs) == 0 {
			return
		}
		fmt.Fprintf(&b, "add element inet %s %s {\n", spec.TableName, name)
		for i, r := range rs {
			b.WriteString(r.String())
			if i < len(rs)-1 {
				b.WriteString(",\n")
			}
		}
		b.WriteString("\n}\n")
	}
	set(spec.DirectSetV4, v4, spec.DirectSetV4 != "")
	set(spec.DirectSetV6, v6, spec.IPv6Enabled && spec.DirectSetV6 != "")
	return b.String()
}

// loadNFTScript runs an nft script. Its nft is the process to lose if the
// load tips the router over (the set simply stays as it was).
func loadNFTScript(ctx context.Context, script string) error {
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(c, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = memguard.SetOOMScoreAdj(cmd.Process.Pid, memguard.TransientAdj)
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%w: %s", err, tail(strings.TrimSpace(out.String()), 300))
	}
	return nil
}

func geoNames(gs []xray.GeoRef) string {
	names := make([]string, 0, len(gs))
	for _, g := range gs {
		if g.File == "geoip.dat" {
			names = append(names, "geoip:"+g.Code)
		} else {
			names = append(names, "ext:"+g.File+":"+g.Code)
		}
	}
	return strings.Join(names, ",")
}
