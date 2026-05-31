package inventory

import (
	"context"
	"testing"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/supervisor"
)

func TestParseMeminfo(t *testing.T) {
	data := []byte("MemTotal:      262144 kB\nMemAvailable:  131072 kB\nSwapTotal:     0 kB\nSwapFree:      0 kB\n")
	res := parseMeminfo(data)
	if res.MemoryTotalMB != 256 || res.MemoryAvailableMB != 128 {
		t.Errorf("meminfo parse: %+v", res)
	}
}

func TestCollectAssemblesXrayNativeInventory(t *testing.T) {
	c := NewCollector(Options{
		ControllerVersion: "0.2.0-r1",
		PanelDomain:       "router.example.net",
		XrayBinary:        "/usr/bin/xray",
	})
	// Inject a fake OS.
	c.run = func(_ context.Context, name string, args ...string) (string, error) {
		switch {
		case name == "ubus":
			return `{"model":"Xiaomi AX3000T","board_name":"xiaomi,ax3000t","release":{"version":"24.10.6","target":"mediatek/filogic","description":"OpenWrt 24.10.6"}}`, nil
		case name == "/usr/bin/xray":
			return "Xray 1.8.24 (Xray, Penetrates everything.)", nil
		case name == "pgrep":
			return "1234", nil
		}
		return "", nil
	}
	c.readFile = func(path string) ([]byte, error) {
		if path == "/proc/meminfo" {
			return []byte("MemTotal: 262144 kB\nMemAvailable: 200000 kB\n"), nil
		}
		return nil, errNotExist{}
	}
	c.statfsMB = func(string) int { return 42 }
	c.hostname = func() (string, error) { return "ax-test", nil }

	inv := c.Collect(context.Background(), supervisor.Status{State: supervisor.StateRunning}, 27, 1)

	if inv.EngineMode != controlplane.EngineModeXrayDirect {
		t.Errorf("engineMode = %q", inv.EngineMode)
	}
	if inv.ProtocolVersion != controlplane.ProtocolVersion {
		t.Errorf("protocol = %q", inv.ProtocolVersion)
	}
	if inv.Model != "Xiaomi AX3000T" || inv.BoardName != "xiaomi,ax3000t" || inv.Target != "mediatek/filogic" {
		t.Errorf("board fields: %+v", inv)
	}
	if inv.XrayVersion != "1.8.24" || !inv.XrayEnabled {
		t.Errorf("xray version/enabled: %q %v", inv.XrayVersion, inv.XrayEnabled)
	}
	if inv.ServiceHealth.Xray != "running" || inv.ServiceHealth.Passwall != "stopped" {
		t.Errorf("service health: %+v", inv.ServiceHealth)
	}
	if inv.PasswallEnabled {
		t.Errorf("xray-direct inventory must report passwallEnabled=false")
	}
	if inv.Resources.MemoryAvailableMB != 195 { // 200000kB/1024
		t.Errorf("resources mem: %+v", inv.Resources)
	}
	if inv.Resources.OverlayFreeMB != 42 || inv.Resources.TMPFreeMB != 42 {
		t.Errorf("statfs not applied: %+v", inv.Resources)
	}
	if inv.NodeCount != 27 || inv.SubscriptionCount != 1 {
		t.Errorf("counts: %d %d", inv.NodeCount, inv.SubscriptionCount)
	}
	if inv.Hostname != "ax-test" {
		t.Errorf("hostname: %q", inv.Hostname)
	}
}

type errNotExist struct{}

func (errNotExist) Error() string { return "not exist" }
