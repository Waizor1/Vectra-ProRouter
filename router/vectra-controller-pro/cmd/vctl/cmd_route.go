package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vectra-controller-pro/internal/agentcfg"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/geodat"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/routepolicy"
	"vectra-controller-pro/internal/sites"
)

func init() {
	register(command{name: "route", summary: "route provider: move a native router to the subscription's engine, carrying the sites it would otherwise send direct — a dry run unless -apply", run: cmdRoute})
}

// defaultRouteEntry is the subscription's entry a router runs by default
// (docs/superpowers/specs/2026-09-30-vctl-service-routing-design.md,
// decision 1).
const defaultRouteEntry = "🇷🇺🇪🇺 Авто Самый стабильный"

// routeCarrySlots are the native slots the owner sent through the VPN — the
// main VPN of the new model (decision 7).
var routeCarrySlots = []string{"WorldProxy", "Special"}

// routeRussianTLDs are what the subscription's Russian rules name, and
// RussiaDirect sends direct (decision 2): a site of these the owner sent
// through the VPN would change its way unless carried. рф is xn--p1ai.
var routeRussianTLDs = []string{"ru", "su", "xn--p1ai", "by"}

const routeSourceKey = "vectra-controller-pro.main.route_source"

var (
	routeUCIGet = func(key string) string {
		out, _ := exec.Command("uci", "-q", "get", key).Output()
		return strings.TrimSpace(string(out))
	}
	routeUCISet = func(key, value string) error {
		if out, err := exec.Command("uci", "set", key+"="+value).CombinedOutput(); err != nil {
			return fmt.Errorf("uci set %s: %v: %s", key, err, strings.TrimSpace(string(out)))
		}
		pkg, _, _ := strings.Cut(key, ".")
		if out, err := exec.Command("uci", "commit", pkg).CombinedOutput(); err != nil {
			return fmt.Errorf("uci commit %s: %v: %s", pkg, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
)

func cmdRoute(args []string) error {
	if len(args) == 0 || args[0] != "provider" {
		return fmt.Errorf("route: subcommand required: provider")
	}
	fs := newFlagSet("route provider")
	agentPath := fs.String("config", "/var/run/vectra-controller-pro/agent.json", "the daemon's agent config")
	entry := fs.String("entry", defaultRouteEntry, "the subscription's entry to run, by its remark")
	apply := fs.Bool("apply", false, "make the change (without it: say what it would do)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := agentcfg.Load(*agentPath)
	if err != nil {
		return err
	}
	return routeProvider(os.Stdout, cfg, *entry, *apply)
}

// routeCarry is what the move takes from the native slots: sites in the
// canonical form of internal/sites, domains first.
type routeCarry struct {
	domains, addrs         []string
	refs, foreign, invalid int
	slots                  []string
}

// routeProvider moves the router to the subscription's engine: the entry
// chosen by remark, the owner's Russian sites of the main-VPN slots carried
// into My sites → through the VPN, UCI route_source 'provider'. Without apply
// it prints what it would do, by counts — never a site.
func routeProvider(w io.Writer, cfg agentcfg.Config, remark string, apply bool) error {
	idx, err := localctl.LoadEntriesIndex(cfg.EntriesIndexPath)
	if err != nil {
		return fmt.Errorf("route provider: the subscription's entries: %w", err)
	}
	entryIndex := -1
	for _, e := range idx.Entries {
		if e.Remark == remark {
			entryIndex = e.Index
			break
		}
	}
	if entryIndex < 0 {
		return fmt.Errorf("route provider: the subscription has no entry %q (%d entries, fetched %s)", remark, len(idx.Entries), idx.FetchedAt.Format("2006-01-02 15:04"))
	}
	carry, err := routeCollect(routeGeoIP(cfg))
	if err != nil {
		return err
	}
	ov, err := localctl.LoadOverrides(cfg.OverridesPath)
	if err != nil {
		return err
	}
	proxy, added, direct := routeMerge(ov, carry)
	if _, _, err := sites.NormalizeLists(ov.Direct, proxy); err != nil {
		return fmt.Errorf("route provider: My sites would not hold it: %w", err)
	}
	after := ov
	after.Proxy = proxy
	// Native carry may include IP/CIDR sites: preserve the legacy parser.
	after.ConnectRules = false
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	render, checkErr := routeCheck(ctx, cfg, entryIndex, after)

	mode := "dry run"
	if apply {
		mode = "apply"
	}
	fmt.Fprintf(w, "route provider (%s)\n", mode)
	fmt.Fprintf(w, "  entry: %s (#%d of %d)\n", remark, entryIndex, len(idx.Entries))
	if len(carry.slots) == 0 {
		fmt.Fprintf(w, "  native slots: none of %s in %s — nothing to carry\n", strings.Join(routeCarrySlots, ", "), nativeStorePath)
	} else {
		fmt.Fprintf(w, "  native slots read: %s\n", strings.Join(carry.slots, ", "))
	}
	addrs := "addresses"
	if len(carry.addrs) == 1 {
		addrs = "address"
	}
	fmt.Fprintf(w, "  carry %d (%d domains, %d %s) into My sites → through the VPN: %d there now, %d after (at most %d)\n",
		len(carry.domains)+len(carry.addrs), len(carry.domains), len(carry.addrs), addrs,
		len(ov.Proxy), len(proxy), sites.Max)
	if added < len(carry.domains)+len(carry.addrs) || direct > 0 {
		fmt.Fprintf(w, "  of them %d already there, %d in My sites → without the VPN (the owner's choice, left there)\n",
			len(carry.domains)+len(carry.addrs)-added-direct, direct)
	}
	fmt.Fprintf(w, "  not carried: %d references (geosite:, geoip:, regexp:, keyword:), %d not Russian (the subscription sends them through the VPN), %d unreadable\n",
		carry.refs, carry.foreign, carry.invalid)
	from := routeUCIGet(routeSourceKey)
	if from == "" {
		from = "provider (unset)"
	}
	fmt.Fprintf(w, "  UCI %s: %s → provider\n", routeSourceKey, from)
	if checkErr != nil {
		fmt.Fprintf(w, "  render: REFUSED — %v\n", checkErr)
		return fmt.Errorf("route provider: the entry's render would not start, nothing changed: %w", checkErr)
	}
	fmt.Fprintf(w, "  render: %s\n", render)
	if !apply {
		fmt.Fprintln(w, "dry run: nothing changed; -apply makes it")
		return nil
	}

	if _, err := localctl.UpdateOverrides(cfg.OverridesPath, func(o *localctl.Overrides) error {
		p, _, _ := routeMerge(*o, carry)
		if _, _, err := sites.NormalizeLists(o.Direct, p); err != nil {
			return err
		}
		o.Proxy = p
		o.ConnectRules = false
		o.EntryDigest = ""
		o.EntryRemark = remark
		i := entryIndex
		o.EntryIndex = &i
		return nil
	}); err != nil {
		return fmt.Errorf("route provider: %w", err)
	}
	if err := routeUCISet(routeSourceKey, "provider"); err != nil {
		return fmt.Errorf("route provider: My sites and the entry are kept, the switch is not: %w", err)
	}
	fmt.Fprintln(w, "done: the entry and My sites are kept, route_source is provider — restart the service to run it (/etc/init.d/vectra-controller-pro restart)")
	return nil
}

// routeCheck renders the entry the way the daemon will run it — its splice
// options, the owner's sites after the carry, DNS through the tunnel, the
// provider's geo files — and asks xray whether it would start. Nothing is
// written: not the render, not the daemon's state.
var routeCheck = func(ctx context.Context, cfg agentcfg.Config, entryIndex int, ov localctl.Overrides) (string, error) {
	cfg.RouteSource = "" // the engine it moves to
	d := &daemon{cfg: cfg}
	desired, err := d.loadDesiredConfig()
	if err != nil {
		return "", err
	}
	if desired == nil || desired.Inbounds.Tproxy == nil {
		return "", fmt.Errorf("no operator config with a tproxy inbound at %s", cfg.XrayConfigPath)
	}
	d.desired = desired
	cache, err := localctl.LoadEntries(cfg.EntriesPath)
	if err != nil {
		return "", fmt.Errorf("the cached entries: %w", err)
	}
	if entryIndex < 0 || entryIndex >= len(cache.Entries) {
		return "", fmt.Errorf("entry #%d is not among the %d cached", entryIndex, len(cache.Entries))
	}
	raw := cache.Entries[entryIndex]
	opts := routePreviewOptions(d, raw, ov)
	spliced, res, err := xray.Splice(raw, desired.Inbounds.Tproxy, opts)
	if err != nil {
		return "", err
	}
	var floor uint64
	if mi, err := memguard.Read(); err == nil {
		floor = memguard.HeavyFloorKB(mi.TotalKB)
	}
	dir := d.geoAssetDir()
	v := xray.Validator{Binary: cfg.XrayBinary, AssetDir: dir, OOMScoreAdj: memguard.TransientAdj, MemFloorKB: floor}
	if err := v.Test(ctx, spliced); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d bytes, %d Russian rule(s) direct, %d rule(s) of My sites, geo files in %s — xray -test OK",
		len(spliced), res.RussiaDirectRules, res.UserRules.Rules, dir), nil
}

// routeMerge appends the carried sites to the owner's proxy list: a site
// already there is not doubled, one in the direct list is left there. It
// returns the list, how many it added and how many it left direct.
func routeMerge(ov localctl.Overrides, c routeCarry) (proxy []string, added, direct int) {
	have := map[string]bool{}
	for _, s := range ov.Proxy {
		have[s] = true
	}
	isDirect := map[string]bool{}
	for _, s := range ov.Direct {
		isDirect[s] = true
	}
	proxy = append([]string(nil), ov.Proxy...)
	for _, s := range append(append([]string(nil), c.domains...), c.addrs...) {
		switch {
		case isDirect[s]:
			direct++
		case have[s]:
		default:
			have[s] = true
			proxy = append(proxy, s)
			added++
		}
	}
	return proxy, added, direct
}

// routeCollect reads the main-VPN slots of the native store and keeps what
// the subscription would send direct: Russian domains, and addresses inside
// geoip:ru of the router's geo file.
func routeCollect(geoipPath string) (routeCarry, error) {
	var c routeCarry
	raw, err := os.ReadFile(nativeStorePath)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	secs, err := routepolicy.ParseUCI(string(raw))
	if err != nil {
		return c, fmt.Errorf("route provider: %s: %w", nativeStorePath, err)
	}
	var ru []netip.Prefix
	cats, err := geodat.Load(geoipPath, "RU")
	if err != nil {
		return c, fmt.Errorf("route provider: geoip:ru from %s: %w", geoipPath, err)
	}
	ru = append(ru, cats["RU"].V4...)
	ru = append(ru, cats["RU"].V6...)
	seen := map[string]bool{}
	keep := func(s sites.Site) {
		if seen[s.Display] {
			return
		}
		seen[s.Display] = true
		if s.Domain {
			c.domains = append(c.domains, s.Display)
		} else {
			c.addrs = append(c.addrs, s.Display)
		}
	}
	for _, slot := range routeCarrySlots {
		sec, ok := routeSlot(secs, slot)
		if !ok {
			continue
		}
		c.slots = append(c.slots, slot)
		for _, line := range routeLines(sec, "domain_list") {
			v, ok := routeDomainValue(line)
			if !ok {
				c.refs++
				continue
			}
			s, err := sites.Parse(v)
			if err != nil || !s.Domain {
				c.invalid++
				continue
			}
			if !routeRussianDomain(s.ASCII) {
				c.foreign++
				continue
			}
			keep(s)
		}
		for _, line := range routeLines(sec, "ip_list") {
			if routeIPReference(line) {
				c.refs++
				continue
			}
			s, err := sites.Parse(line)
			if err != nil || s.Domain {
				c.invalid++
				continue
			}
			if !routeInside(ru, s.Prefix) {
				c.foreign++
				continue
			}
			keep(s)
		}
	}
	return c, nil
}

// routeGeoIP is the geoip.dat to read geoip:ru from: the provider's geo
// files, or — on a router that has run only the route policy — its own, or
// PassWall2's.
func routeGeoIP(cfg agentcfg.Config) string {
	dirs := []string{config.ResolveGeoAssetDir(cfg.GeoAssetDir), nativeGeoDir, passwallAssetDir()}
	for _, d := range dirs {
		if p := filepath.Join(d, "geoip.dat"); fileExists(p) {
			return p
		}
	}
	return filepath.Join(dirs[0], "geoip.dat")
}

// routeSlot finds a shunt rule by its name or its remarks.
func routeSlot(secs []routepolicy.Section, name string) (routepolicy.Section, bool) {
	for _, s := range secs {
		if s.Type == "shunt_rules" && (strings.EqualFold(s.Name, name) || strings.EqualFold(s.Get("remarks"), name)) {
			return s, true
		}
	}
	return routepolicy.Section{}, false
}

// routeLines is a slot's list, one entry a line, as PassWall2 keeps it — an
// option over several lines, or a UCI list.
func routeLines(s routepolicy.Section, key string) []string {
	var out []string
	for _, v := range append([]string{s.Get(key)}, s.Lists[key]...) {
		for _, l := range strings.Split(v, "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				out = append(out, l)
			}
		}
	}
	return out
}

// routeDomainValue is the site a domain_list line names: "domain:x",
// "full:x" or a bare name; anything else (geosite:, regexp:, keyword:, ext:)
// is a reference, not a site.
func routeDomainValue(line string) (string, bool) {
	for _, p := range []string{"domain:", "full:"} {
		if v, ok := strings.CutPrefix(line, p); ok {
			return v, true
		}
	}
	if strings.Contains(line, ":") {
		return "", false
	}
	return line, true
}

// routeIPReference: an ip_list line naming a list (geoip:, ext:), not an
// address.
func routeIPReference(line string) bool {
	for _, p := range []string{"geoip:", "ext:", "geosite:"} {
		if strings.HasPrefix(strings.ToLower(line), p) {
			return true
		}
	}
	return false
}

func routeRussianDomain(ascii string) bool {
	ascii = strings.TrimSuffix(ascii, ".")
	for _, tld := range routeRussianTLDs {
		if ascii == tld || strings.HasSuffix(ascii, "."+tld) {
			return true
		}
	}
	return false
}

// routeInside: the whole of p lies in one of the prefixes.
func routeInside(in []netip.Prefix, p netip.Prefix) bool {
	for _, r := range in {
		if r.Bits() <= p.Bits() && r.Contains(p.Addr()) {
			return true
		}
	}
	return false
}

// routePreviewOptions preserves the running owner's exact service overlays;
// a choice that does not run is skipped, as the render skips it.
func routePreviewOptions(d *daemon, raw []byte, ov localctl.Overrides) xray.SpliceOptions {
	opts, _ := spliceOptionsFor(raw, ov, !d.cfg.NoRussiaDirect)
	opts.ServiceEntries, _ = d.connectServiceOptionsFor(ov, raw)
	return d.withRuntime(opts, raw)
}
