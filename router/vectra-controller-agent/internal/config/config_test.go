package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfigFixture(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}
	return path
}

func TestLoadUsesControlURLWhenProvided(t *testing.T) {
	path := writeConfigFixture(t, `{
  "control_url": "https://api.vectra-pro.net",
  "panel_url": "https://router.vectra-pro.net"
}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.ControlURL, "https://api.vectra-pro.net"; got != want {
		t.Fatalf("control url = %q, want %q", got, want)
	}
	if got, want := cfg.PanelURL, "https://router.vectra-pro.net"; got != want {
		t.Fatalf("panel url = %q, want %q", got, want)
	}
}

func TestLoadFallsBackToPanelURLForLegacyConfigs(t *testing.T) {
	path := writeConfigFixture(t, `{
  "panel_url": "https://legacy.example"
}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.ControlURL, "https://legacy.example"; got != want {
		t.Fatalf("control url = %q, want %q", got, want)
	}
	if got, want := cfg.PanelURL, "https://legacy.example"; got != want {
		t.Fatalf("panel url = %q, want %q", got, want)
	}
}

func TestLoadRequiresAtLeastOneURL(t *testing.T) {
	path := writeConfigFixture(t, `{}`)

	if _, err := Load(path); err == nil {
		t.Fatal("expected error for missing control_url/panel_url")
	}
}

func TestLoadParsesStringDurations(t *testing.T) {
	path := writeConfigFixture(t, `{
  "control_url": "https://api.vectra-pro.net",
  "panel_url": "https://router.vectra-pro.net",
  "poll_interval": "45s",
  "request_timeout": "10s"
}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.PollInterval, 45*time.Second; got != want {
		t.Fatalf("poll interval = %v, want %v", got, want)
	}
	if got, want := cfg.RequestTimeout, 10*time.Second; got != want {
		t.Fatalf("request timeout = %v, want %v", got, want)
	}
}

func TestLoadDefaultsRescueHealthURLsWhenMissing(t *testing.T) {
	path := writeConfigFixture(t, `{
  "control_url": "https://api.vectra-pro.net",
  "panel_url": "https://router.vectra-pro.net"
}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(cfg.Rescue.HealthURLs) == 0 {
		t.Fatal("expected default rescue health urls")
	}
	if got, want := cfg.Rescue.HealthURLs[0], "https://www.gstatic.com/generate_204"; got != want {
		t.Fatalf("first rescue health url = %q, want %q", got, want)
	}
}

func TestLoadKeepsDefaultRescuePolicyWhenConfigOmitsIt(t *testing.T) {
	path := writeConfigFixture(t, `{
  "control_url": "https://api.vectra-pro.net",
  "panel_url": "https://router.vectra-pro.net"
}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if len(cfg.Rescue.HealthURLs) < 2 {
		t.Fatalf("expected default rescue health urls, got %#v", cfg.Rescue.HealthURLs)
	}
	if cfg.Rescue.TriggerFailureCount != 3 {
		t.Fatalf("trigger failure count = %d, want 3", cfg.Rescue.TriggerFailureCount)
	}
	if cfg.Rescue.RecoverySuccessCount != 2 {
		t.Fatalf("recovery success count = %d, want 2", cfg.Rescue.RecoverySuccessCount)
	}
	if cfg.Rescue.Cooldown != 5*time.Minute {
		t.Fatalf("cooldown = %v, want %v", cfg.Rescue.Cooldown, 5*time.Minute)
	}
	if !cfg.Rescue.RequireDirectPathSuccess {
		t.Fatal("expected require direct path success to stay enabled by default")
	}
	if cfg.Rescue.PanelOutageThreshold != time.Hour {
		t.Fatalf("panel outage threshold = %v, want %v", cfg.Rescue.PanelOutageThreshold, time.Hour)
	}
	if cfg.Rescue.ProbeCacheTTL != 5*time.Minute {
		t.Fatalf("probe cache ttl = %v, want %v", cfg.Rescue.ProbeCacheTTL, 5*time.Minute)
	}
	if cfg.Rescue.ControllerRestartSettle != 90*time.Second {
		t.Fatalf("controller restart settle = %v, want %v", cfg.Rescue.ControllerRestartSettle, 90*time.Second)
	}
	if cfg.Rescue.DirectSettle != 45*time.Second {
		t.Fatalf("direct settle = %v, want %v", cfg.Rescue.DirectSettle, 45*time.Second)
	}
	if cfg.Rescue.PostRebootSettle != 4*time.Minute {
		t.Fatalf("post reboot settle = %v, want %v", cfg.Rescue.PostRebootSettle, 4*time.Minute)
	}
	if cfg.Rescue.PasswallWarmup != 75*time.Second {
		t.Fatalf("passwall warmup = %v, want %v", cfg.Rescue.PasswallWarmup, 75*time.Second)
	}
	if cfg.Rescue.RebootCooldown != 12*time.Hour {
		t.Fatalf("reboot cooldown = %v, want %v", cfg.Rescue.RebootCooldown, 12*time.Hour)
	}
}

func TestLoadMergesPartialRescuePolicyWithoutDroppingDefaultURLs(t *testing.T) {
	path := writeConfigFixture(t, `{
  "control_url": "https://api.vectra-pro.net",
  "rescue_policy": {
    "trigger_failure_count": 5,
    "require_direct_path_success": false
  }
}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Rescue.TriggerFailureCount != 5 {
		t.Fatalf("trigger failure count = %d, want 5", cfg.Rescue.TriggerFailureCount)
	}
	if cfg.Rescue.RequireDirectPathSuccess {
		t.Fatal("expected require direct path success override to be applied")
	}
	if len(cfg.Rescue.HealthURLs) < 2 {
		t.Fatalf("expected default rescue health urls to survive partial override, got %#v", cfg.Rescue.HealthURLs)
	}
}

func TestLoadParsesExtendedRecoveryDurations(t *testing.T) {
	path := writeConfigFixture(t, `{
  "control_url": "https://api.vectra-pro.net",
  "rescue_policy": {
    "panel_outage_threshold": "90m",
    "controller_restart_settle": "2m",
    "direct_settle": "30s",
    "post_reboot_settle": "5m",
    "passwall_warmup": "80s",
    "reboot_cooldown": "6h"
  }
}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if got, want := cfg.Rescue.PanelOutageThreshold, 90*time.Minute; got != want {
		t.Fatalf("panel outage threshold = %v, want %v", got, want)
	}
	if got, want := cfg.Rescue.ControllerRestartSettle, 2*time.Minute; got != want {
		t.Fatalf("controller restart settle = %v, want %v", got, want)
	}
	if got, want := cfg.Rescue.DirectSettle, 30*time.Second; got != want {
		t.Fatalf("direct settle = %v, want %v", got, want)
	}
	if got, want := cfg.Rescue.PostRebootSettle, 5*time.Minute; got != want {
		t.Fatalf("post reboot settle = %v, want %v", got, want)
	}
	if got, want := cfg.Rescue.PasswallWarmup, 80*time.Second; got != want {
		t.Fatalf("passwall warmup = %v, want %v", got, want)
	}
	if got, want := cfg.Rescue.RebootCooldown, 6*time.Hour; got != want {
		t.Fatalf("reboot cooldown = %v, want %v", got, want)
	}
}
