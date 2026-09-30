package config

import (
	"fmt"
	"net"
	"strings"

	"vectra-controller-pro/internal/uaguard"
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

	// Inbounds — the tproxy inbound is the controller's only contribution to
	// the provider document, so it is required.
	if t := c.Inbounds.Tproxy; t == nil {
		add("inbounds.tproxy: required (it is the only inbound the controller splices into the provider config)")
	} else {
		if t.Port <= 0 || t.Port > 65535 {
			add("inbounds.tproxy.port: invalid %d", t.Port)
		}
		if ip := net.ParseIP(t.ListenIP); ip == nil && t.ListenIP != "" {
			add("inbounds.tproxy.listenIP: not an IP: %q", t.ListenIP)
		}
		for _, d := range t.Sniffing.DestOverride {
			switch d {
			case "http", "tls", "quic":
			case "fakedns":
				// The provider document carries no fakedns block; asking Xray to
				// sniff into a pool that does not exist is a hard start failure.
				add("inbounds.tproxy.sniffing.destOverride: %q is not usable (provider config has no fakedns block)", d)
			default:
				add("inbounds.tproxy.sniffing.destOverride: unknown %q", d)
			}
		}
	}

	// Subscriptions
	for i, s := range c.Subscriptions {
		ctx := fmt.Sprintf("subscriptions[%d]", i)
		if s.URL == "" {
			add("%s.url: required", ctx)
		} else if !strings.HasPrefix(s.URL, "https://") {
			// Cleartext lets an on-path attacker reshape the whole proxy config.
			add("%s.url: must be https://", ctx)
		}
		if s.ID == "" {
			add("%s.id: required", ctx)
		}
		switch s.Mode {
		case "", SubscriptionModeJSON, SubscriptionModeLinkList:
		default:
			add("%s.mode: unknown %q (want %q or %q)", ctx, s.Mode, SubscriptionModeJSON, SubscriptionModeLinkList)
		}
		if s.EntryIndex < 0 {
			add("%s.entryIndex: must be >= 0", ctx)
		}

		if s.MaxBytes < 0 {
			add("%s.maxBytes: must be >= 0", ctx)
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w:\n  - %s", ErrInvalid, strings.Join(errs, "\n  - "))
}

// ValidateSubscriptionAgents refuses a config whose subscription User-Agent
// the provider's anti-fraud sweep would punish, or that copies the router's
// own (see internal/uaguard).
//
// It is deliberately NOT part of Validate. Validate also runs when the daemon
// loads the config it already has on disk, and routers configured before the
// guard existed carry "Happ/1.0" there: failing that load left the daemon with
// no desired config, so after a restart it started xray and never programmed
// the firewall. The stored config stays loadable — the fetcher refuses to send
// the agent — and a NEW config is refused here, when it is adopted.
func ValidateSubscriptionAgents(c *Config) error {
	var errs []string
	for i, s := range c.Subscriptions {
		if err := uaguard.CheckConfigured(s.UserAgent); err != nil {
			errs = append(errs, fmt.Sprintf("subscriptions[%d].userAgent: %v", i, err))
		}
		// headers["User-Agent"] overrides userAgent on the wire.
		if ua, ok := uaguard.FromHeaders(s.Headers); ok {
			if err := uaguard.CheckConfigured(ua); err != nil {
				errs = append(errs, fmt.Sprintf("subscriptions[%d].headers.User-Agent: %v", i, err))
			}
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w:\n  - %s", ErrInvalid, strings.Join(errs, "\n  - "))
}
