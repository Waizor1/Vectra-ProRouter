// Package coreengine defines the abstraction the controller speaks to its
// proxy core (Xray today, possibly sing-box later). Only the Xray
// implementation lives in this repo today; the interface exists so that
// every other subsystem (supervisor, subscription, firewall, CLI) talks
// to a stable surface, not directly to Xray's JSON.
package coreengine

import (
	"context"

	"vectra-controller-pro/internal/config"
)

// Engine is the contract a proxy core implementation provides.
type Engine interface {
	// Name returns the engine identifier, e.g. "xray-core".
	Name() string

	// Render translates an operator Config into the engine's native config
	// (JSON bytes). The returned bytes are deterministic for a given input
	// so they can be diffed in golden-file tests.
	Render(ctx context.Context, c *config.Config) ([]byte, error)

	// Validate is an offline sanity check on c — independent of Render —
	// that uses engine-specific knowledge (e.g. "VLESS uuid must be lowercase
	// hex with dashes"). Returns a multierror.
	Validate(ctx context.Context, c *config.Config) error

	// Capabilities advertises what features the engine supports.
	// Useful for the CLI to warn early if an operator references a capability
	// the chosen core can't deliver.
	Capabilities() Capabilities
}

// Capabilities is a static description of what a core can do.
type Capabilities struct {
	Name              string   `json:"name"`
	Version           string   `json:"version,omitempty"`
	Protocols         []string `json:"protocols"`
	Transports        []string `json:"transports"`
	Securities        []string `json:"securities"`
	HasObservatory    bool     `json:"hasObservatory"`
	HasHandlerService bool     `json:"hasHandlerService"`
	HasStatsService   bool     `json:"hasStatsService"`
	HasFakeDNS        bool     `json:"hasFakeDns"`
}
