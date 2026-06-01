package xray

import (
	"context"
	"encoding/json"
	"fmt"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine"
	"vectra-controller-pro/internal/logging"
)

// Engine implements coreengine.Engine for Xray-core.
type Engine struct {
	// Version is reported in Capabilities. May be empty if unknown.
	Version string
}

// New returns a default-configured Engine.
func New() *Engine { return &Engine{Version: "alpha-internal"} }

func (e *Engine) Name() string { return "xray-core" }

func (e *Engine) Capabilities() coreengine.Capabilities {
	return coreengine.Capabilities{
		Name:              "xray-core",
		Version:           e.Version,
		Protocols:         []string{"vless", "vmess", "trojan", "shadowsocks", "socks", "http", "hysteria2", "wireguard", "freedom", "blackhole", "dns", "loopback"},
		Transports:        []string{"tcp", "ws", "grpc", "kcp", "quic", "xhttp", "httpupgrade", "domainsocket"},
		Securities:        []string{"none", "tls", "reality"},
		HasObservatory:    true,
		HasHandlerService: true,
		HasStatsService:   true,
		HasFakeDNS:        true,
	}
}

// Validate runs engine-specific checks beyond the schema's validation.
func (e *Engine) Validate(_ context.Context, c *config.Config) error {
	// All structural checks live in config.Validate; here we'd add
	// xray-specific format checks (e.g. uuid lowercase). For v0.1 alpha
	// we trust config.Validate and only do a few last-mile sanity items.
	for i, n := range c.Nodes {
		if n.Outbound.Protocol == "vless" && n.Outbound.Settings.VLESS != nil {
			if len(n.Outbound.Settings.VLESS.UUID) < 8 {
				return fmt.Errorf("nodes[%d] (%s): vless.uuid looks too short", i, n.ID)
			}
		}
	}
	return nil
}

// Render translates a config.Config into Xray's JSON document.
func (e *Engine) Render(_ context.Context, c *config.Config) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("xray.Render: nil config")
	}
	// Pre-pass: apply operator-explicit normalization toggles (all default OFF).
	// We never mutate the caller's config silently — when a toggle IS on we work
	// on a clone and log every change, so the transformation is auditable.
	if c.Normalization.ForceFingerprint {
		dup, err := config.Clone(c)
		if err != nil {
			return nil, fmt.Errorf("xray.Render: clone for normalization: %w", err)
		}
		for _, ch := range config.ApplyNormalization(dup) {
			logging.L().Info("normalization applied", "change", ch)
		}
		c = dup
	}
	doc := xConfig{
		Log:      buildLog(c),
		API:      buildAPI(c),
		DNS:      buildDNS(c),
		FakeDNS:  buildFakeDNS(c),
		Policy:   buildPolicy(c),
		Stats:    buildStats(c),
		Inbounds: buildInbounds(c),
		Outbounds: buildOutbounds(c),
		Routing:  buildRouting(c),
		Reverse:  buildReverse(c),
		Metrics:  buildMetrics(c),
		Observatory:      buildObservatory(c),
		BurstObservatory: buildBurstObservatory(c),
	}
	return json.MarshalIndent(&doc, "", "  ")
}

func buildLog(c *config.Config) *xLog {
	lvl := c.Instance.LogLevel
	if lvl == "" {
		return nil
	}
	return &xLog{Loglevel: lvl}
}

func buildStats(c *config.Config) *struct{} {
	if c.Stats != nil && c.Stats.Enabled {
		return &struct{}{}
	}
	return nil
}

func buildPolicy(c *config.Config) *xPolicy {
	if c.Policy == nil {
		return nil
	}
	out := &xPolicy{}
	if len(c.Policy.Levels) > 0 {
		out.Levels = map[string]xPolicyLevel{}
		for k, v := range c.Policy.Levels {
			out.Levels[k] = xPolicyLevel(v)
		}
	}
	if c.Policy.System != nil {
		s := xSystemPolicy(*c.Policy.System)
		out.System = &s
	}
	return out
}

func buildAPI(c *config.Config) *xAPI {
	if c.API == nil {
		return nil
	}
	return &xAPI{
		Tag:      c.API.Tag,
		Services: c.API.Services,
		Listen:   c.API.Listen,
	}
}

func buildFakeDNS(c *config.Config) []xFakeDNSPool {
	if c.FakeDNS == nil {
		return nil
	}
	return []xFakeDNSPool{{IPPool: c.FakeDNS.IPPool, PoolSize: c.FakeDNS.PoolSize}}
}

func buildReverse(c *config.Config) *xReverse {
	if len(c.Reverse) == 0 {
		return nil
	}
	// Merge any number of operator entries into one Xray reverse block.
	r := &xReverse{}
	for _, e := range c.Reverse {
		for _, b := range e.Bridges {
			r.Bridges = append(r.Bridges, xReverseEndpoint{Tag: b.Tag, Domain: b.Domain})
		}
		for _, p := range e.Portals {
			r.Portals = append(r.Portals, xReverseEndpoint{Tag: p.Tag, Domain: p.Domain})
		}
	}
	return r
}

func buildMetrics(c *config.Config) *xMetrics {
	if c.Metrics == nil {
		return nil
	}
	return &xMetrics{Tag: c.Metrics.Tag}
}

func buildObservatory(c *config.Config) *xObservatory {
	if c.Observatory == nil {
		return nil
	}
	return &xObservatory{
		SubjectSelector:   c.Observatory.SubjectSelector,
		ProbeURL:          c.Observatory.ProbeURL,
		ProbeInterval:     c.Observatory.ProbeInterval,
		EnableConcurrency: c.Observatory.EnableConcurrency,
	}
}

func buildBurstObservatory(c *config.Config) *xBurstObservatory {
	if c.BurstObservatory == nil {
		return nil
	}
	out := &xBurstObservatory{
		SubjectSelector: c.BurstObservatory.SubjectSelector,
	}
	if p := c.BurstObservatory.PingConfig; p != nil {
		out.PingConfig = &xPingConfig{
			Destination:   p.Destination,
			Connectivity:  p.Connectivity,
			Interval:      p.Interval,
			SamplingCount: p.SamplingCount,
			Timeout:       p.Timeout,
		}
	}
	return out
}

// Compile-time assertion that *Engine satisfies coreengine.Engine.
var _ coreengine.Engine = (*Engine)(nil)
