package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/config"
)

// The supervised child MUST see XRAY_LOCATION_ASSET. Xray otherwise resolves
// geoip.dat/geosite.dat next to its own binary and refuses to start on any
// config that references geo data — which every provider config does.
func TestSupervisedChildGetsAssetEnv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")
	stub := filepath.Join(dir, "fake-xray")
	script := "#!/bin/sh\nprintf '%s' \"$XRAY_LOCATION_ASSET\" > " + out + "\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := NewProcessWithAssetDir(config.Process{
		XrayBinary: stub,
		ConfigFile: filepath.Join(dir, "xray.json"),
		WorkDir:    dir,
		RestartBackoff: config.Backoff{
			InitialMs: 1, Factor: 1, MaxMs: 1, Reset: "1s",
		},
		ReloadGrace: "1s",
	}, "/usr/share/v2ray")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.startOnce(ctx); err != nil {
		t.Fatalf("startOnce: %v", err)
	}
	_, _ = p.waitOnce()

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("child did not report its env: %v", err)
	}
	if string(got) != "/usr/share/v2ray" {
		t.Fatalf("XRAY_LOCATION_ASSET = %q, want /usr/share/v2ray", got)
	}
}

// A stale inherited value must be replaced, not appended to.
func TestSupervisedChildAssetEnvOverridesInherited(t *testing.T) {
	t.Setenv(config.XrayAssetEnvKey, "/usr/share/xray")

	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")
	stub := filepath.Join(dir, "fake-xray")
	script := "#!/bin/sh\nenv | grep '^XRAY_LOCATION_ASSET=' > " + out + "\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := NewProcessWithAssetDir(config.Process{
		XrayBinary:     stub,
		ConfigFile:     filepath.Join(dir, "xray.json"),
		WorkDir:        dir,
		RestartBackoff: config.Backoff{InitialMs: 1, Factor: 1, MaxMs: 1, Reset: "1s"},
		ReloadGrace:    "1s",
	}, "/usr/share/v2ray")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.startOnce(ctx); err != nil {
		t.Fatalf("startOnce: %v", err)
	}
	_, _ = p.waitOnce()

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("child did not report its env: %v", err)
	}
	lines := strings.Fields(strings.TrimSpace(string(got)))
	if len(lines) != 1 {
		t.Fatalf("expected exactly one XRAY_LOCATION_ASSET entry, got %q", got)
	}
	if lines[0] != config.XrayAssetEnvKey+"=/usr/share/v2ray" {
		t.Fatalf("child env = %q, want the operator value to win", lines[0])
	}
}

// NewProcess (no explicit dir) must still pin the fleet default.
func TestNewProcessDefaultsAssetDir(t *testing.T) {
	p := NewProcess(config.Process{XrayBinary: "/bin/true"})
	if p.assetDir != config.DefaultGeoAssetDir {
		t.Fatalf("assetDir = %q, want %q", p.assetDir, config.DefaultGeoAssetDir)
	}
}
