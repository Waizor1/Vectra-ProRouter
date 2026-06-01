package config

import (
	"fmt"
	"net"
	"strings"
)

// Validate performs schema-level checks. Returns nil if config is well-formed.
// On invalid input it returns a multierror-style message joined with newlines.
func Validate(c *Config) error {
	if c == nil {
		return fmt.Errorf("%w: nil config", ErrInvalid)
	}
	var errs []string
	add := func(format string, a ...any) {
		errs = append(errs, fmt.Sprintf(format, a...))
	}

	if c.Schema != SchemaVersion {
		add("schema: expected %d, got %d", SchemaVersion, c.Schema)
	}

	// Process
	if c.Process.XrayBinary == "" {
		add("process.xrayBinary: required")
	}
	if c.Process.WorkDir == "" {
		add("process.workDir: required")
	}
	if c.Process.OOMScoreAdj < -1000 || c.Process.OOMScoreAdj > 1000 {
		add("process.oomScoreAdj: out of range [-1000,1000]: %d", c.Process.OOMScoreAdj)
	}
	if c.Process.RestartBackoff.InitialMs <= 0 {
		add("process.restartBackoff.initialMs: must be > 0")
	}
	if c.Process.RestartBackoff.Factor < 1 {
		add("process.restartBackoff.factor: must be >= 1")
	}
	if c.Process.RestartBackoff.MaxMs < c.Process.RestartBackoff.InitialMs {
		add("process.restartBackoff.maxMs: must be >= initialMs")
	}

	// Inbounds — at least one must exist
	if c.Inbounds.Tproxy == nil && c.Inbounds.Socks == nil &&
		c.Inbounds.HTTP == nil && c.Inbounds.DNS == nil &&
		c.Inbounds.Dokodemo == nil && c.Inbounds.Shadowsocks == nil &&
		c.Inbounds.Reality == nil {
		add("inbounds: at least one inbound is required")
	}
	if t := c.Inbounds.Tproxy; t != nil {
		if t.Port <= 0 || t.Port > 65535 {
			add("inbounds.tproxy.port: invalid %d", t.Port)
		}
		if ip := net.ParseIP(t.ListenIP); ip == nil && t.ListenIP != "" {
			add("inbounds.tproxy.listenIP: not an IP: %q", t.ListenIP)
		}
	}
	if s := c.Inbounds.Socks; s != nil {
		if s.Port <= 0 || s.Port > 65535 {
			add("inbounds.socks.port: invalid %d", s.Port)
		}
	}
	if h := c.Inbounds.HTTP; h != nil {
		if h.Port <= 0 || h.Port > 65535 {
			add("inbounds.http.port: invalid %d", h.Port)
		}
	}
	if d := c.Inbounds.DNS; d != nil {
		if d.Port <= 0 || d.Port > 65535 {
			add("inbounds.dns.port: invalid %d", d.Port)
		}
		if d.Address == "" {
			// We refuse to invent an upstream — operator must set it explicitly.
			add("inbounds.dns.address: required (set the upstream DNS server explicitly; controller will not silently default)")
		}
	}
	if r := c.Inbounds.Reality; r != nil {
		if r.Port <= 0 || r.Port > 65535 {
			add("inbounds.realityInbound.port: invalid %d", r.Port)
		}
		if len(r.Settings) == 0 {
			// Server-side REALITY needs operator-supplied protocol settings
			// (e.g. clients[]/decryption); the controller will not invent them.
			add("inbounds.realityInbound.settings: required (operator must supply server-side protocol settings; controller will not silently default)")
		}
	}

	// DNS: at least one server required if DNS block is used at all.
	// Empty DNS is OK; Xray will use its defaults.

	// Nodes
	tags := map[string]string{} // tag -> nodeID for dup detection
	ids := map[string]bool{}
	for i, n := range c.Nodes {
		ctx := fmt.Sprintf("nodes[%d]", i)
		if n.ID == "" {
			add("%s.id: required", ctx)
		} else if ids[n.ID] {
			add("%s.id: duplicate id %q", ctx, n.ID)
		} else {
			ids[n.ID] = true
		}
		if n.Tag != "" {
			if other, dup := tags[n.Tag]; dup {
				add("%s.tag: duplicate tag %q (also used by node %s)", ctx, n.Tag, other)
			} else {
				tags[n.Tag] = n.ID
			}
		}
		validateOutbound(&n.Outbound, ctx, add)
	}

	// Routing rules must reference existing tags
	for i, r := range c.Routing.Rules {
		ctx := fmt.Sprintf("routing.rules[%d]", i)
		if r.OutboundTag == "" && r.BalancerTag == "" {
			add("%s: must set outboundTag or balancerTag", ctx)
		}
		if r.OutboundTag != "" && r.BalancerTag != "" {
			add("%s: cannot set both outboundTag and balancerTag", ctx)
		}
		if r.OutboundTag != "" && !isWellKnownOutbound(r.OutboundTag) {
			if _, ok := tags[r.OutboundTag]; !ok {
				add("%s.outboundTag: %q does not match any node tag (or well-known)", ctx, r.OutboundTag)
			}
		}
	}

	// Subscriptions
	for i, s := range c.Subscriptions {
		ctx := fmt.Sprintf("subscriptions[%d]", i)
		if s.URL == "" {
			add("%s.url: required", ctx)
		} else if !strings.HasPrefix(s.URL, "http://") && !strings.HasPrefix(s.URL, "https://") {
			add("%s.url: must be http(s)://", ctx)
		}
		if s.ID == "" {
			add("%s.id: required", ctx)
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w:\n  - %s", ErrInvalid, strings.Join(errs, "\n  - "))
}

func validateOutbound(o *Outbound, ctx string, add func(string, ...any)) {
	switch o.Protocol {
	case "":
		add("%s.protocol: required", ctx)
		return
	case "vless":
		if o.Settings.VLESS == nil {
			add("%s.settings.vless: required for protocol=vless", ctx)
		} else if o.Settings.VLESS.UUID == "" {
			add("%s.settings.vless.uuid: required", ctx)
		}
	case "vmess":
		if o.Settings.VMess == nil {
			add("%s.settings.vmess: required for protocol=vmess", ctx)
		} else if o.Settings.VMess.UUID == "" {
			add("%s.settings.vmess.uuid: required", ctx)
		}
	case "trojan":
		if o.Settings.Trojan == nil {
			add("%s.settings.trojan: required for protocol=trojan", ctx)
		} else if o.Settings.Trojan.Password == "" {
			add("%s.settings.trojan.password: required", ctx)
		}
	case "shadowsocks":
		if o.Settings.Shadowsocks == nil {
			add("%s.settings.shadowsocks: required for protocol=shadowsocks", ctx)
		} else if o.Settings.Shadowsocks.Method == "" {
			add("%s.settings.shadowsocks.method: required", ctx)
		}
	case "socks":
		// no-required-fields beyond server/port
	case "http":
		// likewise
	case "hysteria2":
		if o.Settings.Hysteria2 == nil {
			add("%s.settings.hysteria2: required for protocol=hysteria2", ctx)
		}
	case "wireguard":
		if o.Settings.Wireguard == nil {
			add("%s.settings.wireguard: required for protocol=wireguard", ctx)
		} else {
			w := o.Settings.Wireguard
			if w.SecretKey == "" {
				add("%s.settings.wireguard.secretKey: required", ctx)
			}
			if len(w.Address) == 0 {
				add("%s.settings.wireguard.address: required", ctx)
			}
			if len(w.Peers) == 0 {
				add("%s.settings.wireguard.peers: at least one peer required", ctx)
			}
		}
	case "freedom", "blackhole", "dns", "loopback":
		// no server required
		return
	default:
		add("%s.protocol: unknown %q", ctx, o.Protocol)
	}

	// Server/Port required for real protocols (everything except freedom/blackhole/dns/loopback handled above).
	switch o.Protocol {
	case "vless", "vmess", "trojan", "shadowsocks", "socks", "http", "hysteria2":
		if o.Server == "" {
			add("%s.server: required for protocol=%s", ctx, o.Protocol)
		}
		if o.Port <= 0 || o.Port > 65535 {
			add("%s.port: invalid %d", ctx, o.Port)
		}
	}

	if o.Stream != nil {
		validateStream(o.Stream, ctx+".stream", add)
	}
}

func validateStream(s *StreamSettings, ctx string, add func(string, ...any)) {
	switch s.Transport {
	case "":
		add("%s.transport: required", ctx)
		return
	case "tcp", "ws", "grpc", "kcp", "quic", "http", "xhttp", "httpupgrade", "domainsocket":
		// known
	default:
		add("%s.transport: unknown %q", ctx, s.Transport)
	}
	if s.Security != "" && s.Security != "none" && s.Security != "tls" && s.Security != "reality" {
		add("%s.security: unknown %q", ctx, s.Security)
	}
	if s.Security == "reality" && s.REALITY == nil {
		add("%s.reality: required when security=reality", ctx)
	}
	if s.Security == "reality" && s.REALITY != nil {
		if s.REALITY.PublicKey == "" {
			add("%s.reality.publicKey: required", ctx)
		}
		if s.REALITY.ServerName == "" {
			add("%s.reality.serverName: required", ctx)
		}
	}
}

// isWellKnownOutbound returns true for Xray's built-in synthetic tags
// (freedom and blackhole are commonly used as fallback outbounds; we let
// the operator reference them without declaring nodes).
func isWellKnownOutbound(tag string) bool {
	switch tag {
	case "direct", "block", "dns-out", "freedom", "blackhole":
		return true
	}
	return false
}
