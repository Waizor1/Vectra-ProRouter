package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/rescue"
)

type Config struct {
	ControlURL            string                       `json:"control_url,omitempty"`
	PanelURL              string                       `json:"panel_url"`
	StatePath             string                       `json:"state_path"`
	StatusPath            string                       `json:"status_path"`
	AgentToken            string                       `json:"agent_token"`
	RouterID              string                       `json:"router_id"`
	PollInterval          time.Duration                `json:"poll_interval"`
	RequestTimeout        time.Duration                `json:"request_timeout"`
	Rescue                rescue.Policy                `json:"rescue_policy"`
	Inventory             controlplane.RouterInventory `json:"inventory"`
	DryRunPasswallProfile *passwall.DesiredConfig      `json:"dry_run_passwall_profile,omitempty"`
}

type rawRescuePolicy struct {
	HealthURLs               []string         `json:"health_urls,omitempty"`
	TriggerFailureCount      int              `json:"trigger_failure_count"`
	RecoverySuccessCount     int              `json:"recovery_success_count"`
	Cooldown                 json.RawMessage  `json:"cooldown"`
	RequireDirectPathSuccess *bool            `json:"require_direct_path_success,omitempty"`
	DirectModeReason         string           `json:"direct_mode_reason"`
}

type rawConfig struct {
	ControlURL            string                       `json:"control_url,omitempty"`
	PanelURL              string                       `json:"panel_url"`
	StatePath             string                       `json:"state_path"`
	StatusPath            string                       `json:"status_path"`
	AgentToken            string                       `json:"agent_token"`
	RouterID              string                       `json:"router_id"`
	PollInterval          json.RawMessage              `json:"poll_interval"`
	RequestTimeout        json.RawMessage              `json:"request_timeout"`
	Rescue                rawRescuePolicy              `json:"rescue_policy"`
	Inventory             controlplane.RouterInventory `json:"inventory"`
	DryRunPasswallProfile *passwall.DesiredConfig      `json:"dry_run_passwall_profile,omitempty"`
}

func defaultRescuePolicy() rescue.Policy {
	return rescue.Policy{
		HealthURLs: []string{
			"https://www.gstatic.com/generate_204",
			"https://cp.cloudflare.com/",
		},
		TriggerFailureCount:      3,
		RecoverySuccessCount:     2,
		Cooldown:                 5 * time.Minute,
		RequireDirectPathSuccess: true,
		DirectModeReason:         "Subscription expired or upstream proxy unavailable",
	}
}

func mergeRescuePolicy(base rescue.Policy, raw rawRescuePolicy) (rescue.Policy, error) {
	policy := base

	if raw.HealthURLs != nil {
		policy.HealthURLs = make([]string, 0, len(raw.HealthURLs))
		for _, value := range raw.HealthURLs {
			if value != "" {
				policy.HealthURLs = append(policy.HealthURLs, value)
			}
		}
	}

	if raw.TriggerFailureCount != 0 {
		policy.TriggerFailureCount = raw.TriggerFailureCount
	}
	if raw.RecoverySuccessCount != 0 {
		policy.RecoverySuccessCount = raw.RecoverySuccessCount
	}
	if len(raw.Cooldown) > 0 && string(raw.Cooldown) != "null" {
		duration, err := parseJSONDuration(raw.Cooldown)
		if err != nil {
			return rescue.Policy{}, fmt.Errorf("decode rescue_policy.cooldown: %w", err)
		}
		policy.Cooldown = duration
	}
	if raw.RequireDirectPathSuccess != nil {
		policy.RequireDirectPathSuccess = *raw.RequireDirectPathSuccess
	}
	if raw.DirectModeReason != "" {
		policy.DirectModeReason = raw.DirectModeReason
	}

	return policy, nil
}

func parseJSONDuration(raw json.RawMessage) (time.Duration, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return 0, nil
		}

		duration, parseErr := time.ParseDuration(text)
		if parseErr != nil {
			return 0, fmt.Errorf("parse duration string %q: %w", text, parseErr)
		}
		return duration, nil
	}

	var numeric int64
	if err := json.Unmarshal(raw, &numeric); err == nil {
		return time.Duration(numeric), nil
	}

	return 0, fmt.Errorf("unsupported duration value %s", string(raw))
}

func Load(path string) (Config, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read file: %w", err)
	}

	cfg := Config{
		PollInterval:   60 * time.Second,
		RequestTimeout: 10 * time.Second,
		StatePath:      "/etc/vectra-controller/state.json",
		StatusPath:     "/var/run/vectra-controller/status.json",
		Rescue:         defaultRescuePolicy(),
	}

	var raw rawConfig
	if err := json.Unmarshal(bytes, &raw); err != nil {
		return Config{}, fmt.Errorf("decode json: %w", err)
	}

	cfg.ControlURL = raw.ControlURL
	cfg.PanelURL = raw.PanelURL
	cfg.StatePath = raw.StatePath
	cfg.StatusPath = raw.StatusPath
	cfg.AgentToken = raw.AgentToken
	cfg.RouterID = raw.RouterID
	cfg.Inventory = raw.Inventory
	cfg.DryRunPasswallProfile = raw.DryRunPasswallProfile

	cfg.Rescue, err = mergeRescuePolicy(cfg.Rescue, raw.Rescue)
	if err != nil {
		return Config{}, err
	}

	if len(raw.PollInterval) > 0 {
		cfg.PollInterval, err = parseJSONDuration(raw.PollInterval)
		if err != nil {
			return Config{}, fmt.Errorf("decode poll_interval: %w", err)
		}
	}

	if len(raw.RequestTimeout) > 0 {
		cfg.RequestTimeout, err = parseJSONDuration(raw.RequestTimeout)
		if err != nil {
			return Config{}, fmt.Errorf("decode request_timeout: %w", err)
		}
	}

	if cfg.ControlURL == "" {
		cfg.ControlURL = cfg.PanelURL
	}
	if cfg.PanelURL == "" {
		cfg.PanelURL = cfg.ControlURL
	}
	if cfg.ControlURL == "" {
		return Config{}, fmt.Errorf("control_url or panel_url is required")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 60 * time.Second
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 10 * time.Second
	}
	if cfg.StatePath == "" {
		cfg.StatePath = "/etc/vectra-controller/state.json"
	}
	if cfg.StatusPath == "" {
		cfg.StatusPath = "/var/run/vectra-controller/status.json"
	}
	cfg.Rescue.Normalize()
	return cfg, nil
}
