package config

import (
	"os"
	"path/filepath"
	"strings"
)

// XrayAssetEnvKey is the environment variable Xray reads the geo asset dir
// from. It is set NOWHERE by the OpenWrt packaging by default, and without it
// Xray resolves geoip.dat/geosite.dat next to its own binary and refuses to
// start on any config that references geo data.
const XrayAssetEnvKey = "XRAY_LOCATION_ASSET"

// XrayAssetEnv returns env with XRAY_LOCATION_ASSET forced to assetDir (or
// DefaultGeoAssetDir when empty). Any inherited value is REPLACED so the child
// can never pick up a stale path from the init system.
func XrayAssetEnv(env []string, assetDir string) []string {
	if assetDir == "" {
		assetDir = DefaultGeoAssetDir
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		// The controller runs with GOGC/GOMEMLIMIT sized for itself (init
		// script); an xray child inheriting them keeps them, because
		// vctl-xray-wrapper applies its own defaults only when they are unset.
		if strings.HasPrefix(kv, XrayAssetEnvKey+"=") || strings.HasPrefix(kv, "GOGC=") || strings.HasPrefix(kv, "GOMEMLIMIT=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, XrayAssetEnvKey+"="+assetDir)
}

// DefaultGeoAssetDir is where vctl's own geoip.dat/geosite.dat are: the
// vectra-geodata package's, which vctl's package depends on — every category
// the provider's config routes by, checked when the feed is built. It is
// what XRAY_LOCATION_ASSET is pinned to (supervisor, xray -test,
// vctl-xray-wrapper); without it Xray looks for the .dat files next to its
// own binary and fails.
const DefaultGeoAssetDir = "/usr/share/vectra-controller-pro/geo"

// LegacyGeoAssetDir is where the fleet kept them before: PassWall2's
// v2ray-geoip/geosite packages' directory, the panel's operator configs'
// geo.assetDir. It goes with those packages (the test router lost it with
// PassWall, 2026-09-29), so a config naming it gets vctl's own directory.
const LegacyGeoAssetDir = "/usr/share/v2ray"

// GeoAssetDir is the directory xray reads its geo files from for a
// configured one: vctl's own when none is named, or the legacy one is; any
// other directory as named.
func GeoAssetDir(configured string) string {
	d := strings.TrimRight(strings.TrimSpace(configured), "/")
	if d == "" || d == LegacyGeoAssetDir {
		return DefaultGeoAssetDir
	}
	return d
}

// ResolveGeoAssetDir is the directory xray is to read on this router for a
// configured one: GeoAssetDir's — unless that is vctl's own and its files are
// not there while the fleet's old directory has them. Only the pro feed's
// package depends on vectra-geodata; one built otherwise (the SDK's, a local
// build) and installed where PassWall's packages keep the geo data reads
// those, as vctl before 0.6.0-r18 did, rather than failing on files that are
// not there.
func ResolveGeoAssetDir(configured string) string {
	d := GeoAssetDir(configured)
	if d == DefaultGeoAssetDir && !GeoFilesAt(d) && GeoFilesAt(LegacyGeoAssetDir) {
		return LegacyGeoAssetDir
	}
	return d
}

// GeoFilesAt: dir holds a geoip.dat and a geosite.dat. Tests replace it.
var GeoFilesAt = func(dir string) bool {
	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		if st, err := os.Stat(filepath.Join(dir, f)); err != nil || st.IsDir() || st.Size() == 0 {
			return false
		}
	}
	return true
}

// DefaultXraySockMark is the SO_MARK stamped on Xray's OWN sockets — both the
// TPROXY inbound (so its replies to clients are not re-captured) and every
// dialling outbound (so its egress is not re-captured).
//
// It MUST NOT equal Inbounds.Tproxy.FwMark. That is not a style preference, it
// is a routing fact:
//
//	`vctl firewall routing` installs `ip rule add fwmark <FwMark> lookup 100`
//	plus `ip route add local 0.0.0.0/0 dev lo table 100`. That rule applies to
//	OUTPUT route lookups too, and a locally-generated packet whose socket
//	carries FwMark therefore resolves to a LOCAL route:
//
//	    # ip route get 44.44.44.44 mark 1
//	    local 44.44.44.44 dev lo table 100 src 44.44.44.44 mark 1
//
//	So Xray's own packets never leave the box. They go out lo, re-enter through
//	PREROUTING, match the TPROXY rule again and are handed straight back to
//	Xray — an unbounded loop. Measured on the stand with FwMark == sock mark:
//	85,150 packets through the TPROXY rule for a handful of client requests,
//	and Xray's RSS at 381 MiB (an OOM on a 234 MB router).
//
// The nft output chain returns on this mark before it can reach the marking
// rule (see firewall.Spec.SockMark), and no ip rule matches it, so Xray's own
// traffic is routed normally. This mirrors the upstream v2ray/Xray TPROXY
// recipe, which has always used two distinct marks (fwmark 1 + sockopt 255).
//
// 0x5644 is "VD" (Vectra Data plane) — adjacent to, and distinct from,
// firewall.DefaultControlMark 0x5643 ("VC", the controller's control plane), so
// the two are separable in `nft list counters`.
const DefaultXraySockMark = 0x5644

// ApplyDefaults fills in defaults for fields the operator left unset.
// It NEVER overwrites a non-zero operator value. Every default applied
// is the controller's "we have to put something here" choice, and is
// loggable via DefaultsDiff(c).
func ApplyDefaults(c *Config) {
	if c == nil {
		return
	}
	if c.Schema == 0 {
		c.Schema = SchemaVersion
	}
	if c.Instance.LogLevel == "" {
		c.Instance.LogLevel = "warning"
	}

	// Process defaults
	p := &c.Process
	if p.XrayBinary == "" {
		p.XrayBinary = "/usr/bin/xray"
	}
	if p.WorkDir == "" {
		p.WorkDir = "/var/run/vectra-controller-pro"
	}
	if p.ConfigFile == "" {
		p.ConfigFile = p.WorkDir + "/xray.json"
	}
	if p.LogDir == "" {
		p.LogDir = "/var/log/vectra-controller-pro"
	}
	if p.OOMScoreAdj == 0 {
		// Slightly less likely to be OOM-killed than default (0).
		// -1000 would make us unkillable which is anti-social; -100 is a sane
		// "important but not critical" hint.
		p.OOMScoreAdj = -100
	}
	if p.RestartBackoff.InitialMs == 0 {
		p.RestartBackoff.InitialMs = 500
	}
	if p.RestartBackoff.Factor == 0 {
		p.RestartBackoff.Factor = 2.0
	}
	if p.RestartBackoff.MaxMs == 0 {
		p.RestartBackoff.MaxMs = 60_000
	}
	if p.RestartBackoff.Reset == "" {
		p.RestartBackoff.Reset = "60s"
	}
	if p.ReloadGrace == "" {
		p.ReloadGrace = "5s"
	}
	if p.StartTimeout == "" {
		p.StartTimeout = "15s"
	}

	// Inbound tproxy defaults
	if t := c.Inbounds.Tproxy; t != nil {
		if t.ListenIP == "" {
			t.ListenIP = "0.0.0.0"
		}
		if t.Port == 0 {
			t.Port = 12345
		}
		if t.FwMark == 0 {
			t.FwMark = 0x1
		}
		if t.Tag == "" {
			t.Tag = "tproxy-in"
		}
	}

	// Geo defaults
	if c.Geo.AssetDir == "" {
		c.Geo.AssetDir = DefaultGeoAssetDir
	}

	// Subscription defaults
	for i := range c.Subscriptions {
		s := &c.Subscriptions[i]
		if s.Mode == "" {
			s.Mode = SubscriptionModeJSON
		}
	}
}

// DefaultsDiff returns a list of human-readable strings describing every
// field that ApplyDefaults would change on c. Useful for `vctl validate -v`.
func DefaultsDiff(c *Config) []string {
	if c == nil {
		return nil
	}
	dup, err := Clone(c)
	if err != nil {
		return []string{"clone failed: " + err.Error()}
	}
	ApplyDefaults(dup)
	var diffs []string
	if c.Process.OOMScoreAdj == 0 && dup.Process.OOMScoreAdj != 0 {
		diffs = append(diffs, "process.oomScoreAdj: 0 -> -100 (default)")
	}
	if c.Geo.AssetDir == "" && dup.Geo.AssetDir != "" {
		diffs = append(diffs, "geo.assetDir: \"\" -> \""+dup.Geo.AssetDir+"\" (default)")
	}
	if c.Process.XrayBinary == "" {
		diffs = append(diffs, "process.xrayBinary: \"\" -> \""+dup.Process.XrayBinary+"\" (default)")
	}
	if c.Process.WorkDir == "" {
		diffs = append(diffs, "process.workDir: \"\" -> \""+dup.Process.WorkDir+"\" (default)")
	}
	for i := range c.Subscriptions {
		if c.Subscriptions[i].Mode == "" {
			diffs = append(diffs, "subscriptions["+c.Subscriptions[i].ID+"].mode: \"\" -> \""+dup.Subscriptions[i].Mode+"\" (default)")
		}
	}
	return diffs
}
