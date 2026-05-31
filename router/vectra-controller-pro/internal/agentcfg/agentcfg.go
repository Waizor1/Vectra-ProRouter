// Package agentcfg is the DAEMON's own configuration (control-plane endpoint,
// credentials, paths, poll cadence, job-safety floors) — distinct from
// internal/config, which is the operator's desired XRAY config pushed by the
// panel. On a router this file is rendered from UCI by render-xray-config.sh
// to /etc/vectra-controller-pro/agent.json.
package agentcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"vectra-controller-pro/internal/jobsafety"
)

// Config is the daemon configuration.
type Config struct {
	// Control plane.
	ControlURL string `json:"controlUrl"`
	PanelURL   string `json:"panelUrl,omitempty"`
	RouterID   string `json:"routerId,omitempty"`
	AgentToken string `json:"agentToken,omitempty"`

	// Filesystem paths.
	StatePath      string `json:"statePath"`           // persisted identity + journal
	StatusPath     string `json:"statusPath"`          // runtime status snapshot
	XrayConfigPath string `json:"xrayConfigPath"`      // operator desired config (config.Config JSON)
	XrayRenderPath string `json:"xrayRenderPath"`      // rendered xray.json the supervisor runs
	XrayBinary     string `json:"xrayBinary,omitempty"`
	LegacyStatePath string `json:"legacyStatePath,omitempty"` // old agent state for canary identity reuse

	// Timing (seconds on disk, exposed as Duration).
	PollIntervalSeconds   int `json:"pollIntervalSeconds"`
	RequestTimeoutSeconds int `json:"requestTimeoutSeconds"`

	// Job safety floors.
	JobSafety jobsafety.Config `json:"jobSafety"`
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
	if c.XrayRenderPath == "" {
		c.XrayRenderPath = "/var/run/vectra-controller-pro/xray.json"
	}
	if c.XrayBinary == "" {
		c.XrayBinary = "/usr/bin/xray"
	}
	if c.LegacyStatePath == "" {
		c.LegacyStatePath = "/etc/vectra-controller/state.json"
	}
	c.JobSafety = c.JobSafety.WithDefaults()
}

// Validate checks the minimum viable configuration.
func (c Config) Validate() error {
	if c.ControlURL == "" {
		return fmt.Errorf("agentcfg: controlUrl is required")
	}
	return nil
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
