// Package agentcfg is the DAEMON's own configuration (control-plane endpoint,
// credentials, paths, poll cadence, job-safety floors) — distinct from
// internal/config, which is the operator's desired XRAY config pushed by the
// panel. On a router this file is rendered from UCI by render-xray-config.sh
// to /etc/vectra-controller-pro/agent.json.
package agentcfg

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/jobsafety"
	"vectra-controller-pro/internal/localctl"
)

// Config is the daemon configuration.
type Config struct {
	// Control plane.
	ControlURL string `json:"controlUrl"`
	PanelURL   string `json:"panelUrl,omitempty"`
	RouterID   string `json:"routerId,omitempty"`
	AgentToken string `json:"agentToken,omitempty"`

	// Filesystem paths.
	StatePath      string `json:"statePath"`      // persisted identity + journal
	StatusPath     string `json:"statusPath"`     // runtime status snapshot
	XrayConfigPath string `json:"xrayConfigPath"` // operator desired config (config.Config JSON, 4 blocks)
	// ProviderConfigPath is where the last-good PROVIDER document is stored
	// VERBATIM. It is deliberately a separate file from XrayConfigPath: the
	// provider document is not an operator config and must never be decoded by
	// config.Read (DisallowUnknownFields would reject it outright).
	ProviderConfigPath string `json:"providerConfigPath,omitempty"`
	XrayRenderPath     string `json:"xrayRenderPath"` // spliced xray.json the supervisor runs
	XrayBinary         string `json:"xrayBinary,omitempty"`
	// GeoAssetDir is exported to xray as XRAY_LOCATION_ASSET.
	GeoAssetDir     string `json:"geoAssetDir,omitempty"`
	LegacyStatePath string `json:"legacyStatePath,omitempty"` // old agent state for canary identity reuse

	// The router UI's files (internal/localctl). Unset, they live next to
	// statePath (persistent) and statusPath (tmpfs) — on a router exactly the
	// localctl defaults, and in a test wherever the test put its state.
	OverridesPath    string `json:"overridesPath,omitempty"`
	EntriesPath      string `json:"entriesPath,omitempty"`
	EntriesIndexPath string `json:"entriesIndexPath,omitempty"`
	UISocketPath     string `json:"uiSocketPath,omitempty"`

	// Timing (seconds on disk, exposed as Duration).
	PollIntervalSeconds   int `json:"pollIntervalSeconds"`
	RequestTimeoutSeconds int `json:"requestTimeoutSeconds"`

	// Job safety floors.
	JobSafety jobsafety.Config `json:"jobSafety"`

	// ClaimRotateSeconds is how often the claim code changes (ADR-0006);
	// 0 = claim.Period, every 20 minutes. Shorter only for tests.
	ClaimRotateSeconds int `json:"claimRotateSeconds,omitempty"`

	// NoDNSTunnel keeps the router's resolver on the open path (UCI
	// dns_tunnel '0'): no DNS inbound in the render, nothing redirected. The
	// default sends it through the tunnel (internal/coreengine/xray,
	// dns_steer.go).
	NoDNSTunnel bool `json:"noDnsTunnel,omitempty"`
	// NoDNSHijack leaves the LAN's queries to public resolvers alone (UCI
	// dns_hijack '0'). By default, while the router's resolver asks through
	// the tunnel, it answers them too (firewall.Spec.HijackDNS).
	NoDNSHijack bool `json:"noDnsHijack,omitempty"`
	// NoDirectBypass keeps every connection in xray (UCI direct_bypass '0'):
	// by default the addresses the routing sends straight out are routed by
	// the kernel and never enter it (cmd/vctl/direct_bypass.go).
	NoDirectBypass bool `json:"noDirectBypass,omitempty"`
	// NoP2PBypass keeps a P2P host's connections in xray (UCI p2p_bypass '0').
	NoP2PBypass bool `json:"noP2PBypass,omitempty"`
	// AdmitRate and PaceRate set the load guards' rates, new connections a
	// second (UCI admit_rate, pace_rate; '0' switches one off); nil: the
	// product's (firewall.DefaultSpec).
	AdmitRate *int `json:"admitRate,omitempty"`
	PaceRate  *int `json:"paceRate,omitempty"`
	// AdmitTotalRate opens the router's whole door, new connections a second
	// into xray from every device together (UCI admit_total_rate); nil or 0:
	// off.
	AdmitTotalRate *int `json:"admitTotalRate,omitempty"`
	// DNSRate is the DNS door's rate, a device's queries a second to the
	// router's resolver (UCI dns_rate; '0' switches it off); nil: the
	// product's.
	DNSRate *int `json:"dnsRate,omitempty"`
	// IPv6: the provider's nodes carry IPv6, so the LAN's IPv6 goes through
	// them (UCI ipv6 '1'). By default it is refused at once — they do not
	// (firewall.Spec.RefuseIPv6; the owner, 2026-09-30).
	IPv6 bool `json:"ipv6,omitempty"`
	// NoRussiaDirect keeps the provider's Russian bridge for Russian sites
	// (UCI russia_direct '0'); by default they go direct (xray.RussiaDirect).
	NoRussiaDirect bool `json:"noRussiaDirect,omitempty"`
	// NoFailoverWatchdog turns the failover watchdog off (UCI
	// failover_watchdog '0'; cmd/vctl/failover.go): a node that stops
	// answering is then left to the provider's observatory.
	NoFailoverWatchdog bool `json:"noFailoverWatchdog,omitempty"`
	// NoExitCheck turns the router's own check of its foreign exits off (UCI
	// exit_check '0'; cmd/vctl/exitcheck.go): an exit that answers no blocked
	// site then stays in the balancers.
	NoExitCheck bool `json:"noExitCheck,omitempty"`
	// ExitCheckSites are the blocked sites the check asks for (UCI list
	// exit_check_site) and ExitCheckControl its neutral URL (UCI
	// exit_check_control); empty: the product's.
	ExitCheckSites   []string `json:"exitCheckSites,omitempty"`
	ExitCheckControl string   `json:"exitCheckControl,omitempty"`
	// ExitCheckFirstSec and ExitCheckEverySec time its rounds (UCI
	// exit_check_first, exit_check_every); 0: the product's.
	ExitCheckFirstSec int `json:"exitCheckFirstSec,omitempty"`
	ExitCheckEverySec int `json:"exitCheckEverySec,omitempty"`

	// RouteSource is what the router routes by: "" (the provider's
	// xray-JSON, the default) or "passwall" — PassWall2's own configuration,
	// the operator's route policy on the fleet's routers, generated by
	// PassWall2's code (UCI route_source).
	RouteSource string `json:"routeSource,omitempty"`

	// NoRetirePassWall keeps PassWall2 on the router (UCI retire_passwall
	// '0'). By default the daemon removes it once vctl has carried the
	// traffic, switched on for good, for PassWallRetireAfter
	// (internal/retire).
	NoRetirePassWall bool `json:"noRetirePassWall,omitempty"`
	// PassWallRetireAfterSec is that window (UCI passwall_retire_after,
	// seconds); 0: a day.
	PassWallRetireAfterSec int `json:"passWallRetireAfterSec,omitempty"`
}

// PassWallRetireAfter is how long vctl carries the traffic before PassWall2
// goes.
func (c Config) PassWallRetireAfter() time.Duration {
	if c.PassWallRetireAfterSec <= 0 {
		return 24 * time.Hour
	}
	return time.Duration(c.PassWallRetireAfterSec) * time.Second
}

// ClaimRotate is the claim code's period.
func (c Config) ClaimRotate() time.Duration {
	if c.ClaimRotateSeconds <= 0 {
		return claim.Period
	}
	return time.Duration(c.ClaimRotateSeconds) * time.Second
}

// PollInterval is the loop cadence.
func (c Config) PollInterval() time.Duration {
	if c.PollIntervalSeconds <= 0 {
		return 60 * time.Second
	}
	return time.Duration(c.PollIntervalSeconds) * time.Second
}

// RequestTimeout is the per-HTTP-call timeout.
func (c Config) RequestTimeout() time.Duration {
	if c.RequestTimeoutSeconds <= 0 {
		return 10 * time.Second
	}
	return time.Duration(c.RequestTimeoutSeconds) * time.Second
}

// Defaults applies sane defaults to zero-valued required fields.
func (c *Config) Defaults() {
	if c.StatePath == "" {
		c.StatePath = "/etc/vectra-controller-pro/state.json"
	}
	if c.StatusPath == "" {
		c.StatusPath = "/var/run/vectra-controller-pro/status.json"
	}
	if c.XrayConfigPath == "" {
		c.XrayConfigPath = "/etc/vectra-controller-pro/xray-desired.json"
	}
	if c.ProviderConfigPath == "" {
		c.ProviderConfigPath = "/etc/vectra-controller-pro/provider-config.json"
	}
	if c.GeoAssetDir == "" {
		c.GeoAssetDir = config.DefaultGeoAssetDir
	}
	if c.XrayRenderPath == "" {
		c.XrayRenderPath = "/var/run/vectra-controller-pro/xray.json"
	}
	if c.XrayBinary == "" {
		c.XrayBinary = "/usr/bin/xray"
	}
	if c.LegacyStatePath == "" {
		c.LegacyStatePath = "/etc/vectra-controller/state.json"
	}
	persistent, runtime := filepath.Dir(c.StatePath), filepath.Dir(c.StatusPath)
	if c.OverridesPath == "" {
		c.OverridesPath = filepath.Join(persistent, filepath.Base(localctl.DefaultOverridesPath))
	}
	if c.EntriesPath == "" {
		c.EntriesPath = filepath.Join(persistent, filepath.Base(localctl.DefaultEntriesPath))
	}
	if c.EntriesIndexPath == "" {
		c.EntriesIndexPath = filepath.Join(persistent, filepath.Base(localctl.DefaultEntriesIndexPath))
	}
	if c.UISocketPath == "" {
		c.UISocketPath = filepath.Join(runtime, filepath.Base(localctl.DefaultSocketPath))
	}
	c.JobSafety = c.JobSafety.WithDefaults()
}

// Validate checks the minimum viable configuration.
func (c Config) Validate() error {
	if c.ControlURL == "" {
		return fmt.Errorf("agentcfg: controlUrl is required")
	}
	u, err := url.Parse(c.ControlURL)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("agentcfg: controlUrl %q is not a URL", c.ControlURL)
	}
	// The router's token travels in every call to the panel: https only.
	// Plain http only to the router itself — a panel stand-in on loopback,
	// where nothing leaves the box.
	switch {
	case strings.EqualFold(u.Scheme, "https"):
		return nil
	case strings.EqualFold(u.Scheme, "http") && loopback(u.Hostname()):
		return nil
	}
	return fmt.Errorf("agentcfg: controlUrl %q must be https: the router's token travels in every call", c.ControlURL)
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Load reads, defaults, and validates the daemon config from disk.
func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read agent config %s: %w", path, err)
	}
	return Parse(raw)
}

// Parse decodes daemon config bytes (defaults applied, then validated).
func Parse(raw []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("parse agent config: %w", err)
	}
	c.Defaults()
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}
