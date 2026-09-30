package inventory

import (
	"context"
	"os"
	"path/filepath"
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
		DeviceIdentifier:  "vectra-07bf0887f662",
		DevicePublicKey:   "Zm9vYmFyLXB1YmxpYy1rZXk=",
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

	// REGRESSION: the collector never copied the persisted device identity into
	// the report, so every register/check-in shipped deviceIdentifier:"" and
	// devicePublicKey:"". The panel declares both z.string().min(1) and
	// answered HTTP 400 "Invalid request payload" on every loop iteration.
	if inv.DeviceIdentifier != "vectra-07bf0887f662" {
		t.Errorf("deviceIdentifier = %q, want vectra-07bf0887f662", inv.DeviceIdentifier)
	}
	if inv.DevicePublicKey != "Zm9vYmFyLXB1YmxpYy1rZXk=" {
		t.Errorf("devicePublicKey = %q, want the seeded key", inv.DevicePublicKey)
	}
}

// TestCollectReportsPanelRequiredFieldsOnFilogic pins the panel's min(1)
// contract to the exact OpenWrt 24.10.6 / Filogic facts the live AX3000T
// reports. Every field named here is `z.string().min(1)` and NOT optional in
// routerInventorySchema, so an empty one costs the router its whole check-in.
func TestCollectReportsPanelRequiredFieldsOnFilogic(t *testing.T) {
	c := NewCollector(Options{
		DeviceIdentifier:  "vectra-07bf0887f662",
		DevicePublicKey:   "Zm9vYmFyLXB1YmxpYy1rZXk=",
		ControllerVersion: "0.2.0-r1",
		XrayBinary:        "/usr/bin/xray",
	})
	// Verbatim `ubus call system board` from router 1111111111.
	c.run = func(_ context.Context, name string, _ ...string) (string, error) {
		if name == "ubus" {
			return `{"kernel":"6.6.127","hostname":"1111111111","system":"ARMv8 Processor rev 4",
			 "model":"Xiaomi Mi Router AX3000T","board_name":"xiaomi,mi-router-ax3000t","rootfs_type":"squashfs",
			 "release":{"distribution":"OpenWrt","version":"24.10.6","revision":"r29141-81be8a8869",
			            "target":"mediatek/filogic","description":"OpenWrt 24.10.6 r29141-81be8a8869","builddate":"1773709139"}}`, nil
		}
		return "", errNotExist{}
	}
	c.readFile = func(path string) ([]byte, error) {
		if path == "/etc/openwrt_release" {
			return []byte("DISTRIB_ARCH='aarch64_cortex-a53'\nDISTRIB_RELEASE='24.10.6'\nDISTRIB_TARGET='mediatek/filogic'\n"), nil
		}
		return nil, errNotExist{}
	}
	c.statfsMB = func(string) int { return 0 }
	c.hostname = func() (string, error) { return "1111111111", nil }

	inv := c.Collect(context.Background(), supervisor.Status{}, 0, 0)

	if missing := inv.MissingRequiredFields(); len(missing) > 0 {
		t.Fatalf("panel-required inventory fields are empty: %v", missing)
	}
	if inv.Architecture != "aarch64_cortex-a53" {
		t.Errorf("architecture = %q", inv.Architecture)
	}
	if inv.OpenWrtRelease != "24.10.6" || inv.Target != "mediatek/filogic" {
		t.Errorf("release/target = %q / %q", inv.OpenWrtRelease, inv.Target)
	}
}

type errNotExist struct{}

func (errNotExist) Error() string { return "not exist" }

// `xray version` execs the 32 MB binary; it must run once per binary, not once
// per check-in, and again when the binary is replaced.
func TestXrayVersionIsCachedUntilTheBinaryChanges(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "xray")
	if err := os.WriteFile(bin, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewCollector(Options{XrayBinary: bin})
	execs := 0
	version := "26.3.27"
	c.run = func(_ context.Context, name string, _ ...string) (string, error) {
		if name == bin {
			execs++
			return "Xray " + version + " (Xray, Penetrates Everything.)", nil
		}
		return "", nil
	}
	for i := 0; i < 5; i++ {
		if v := c.xrayVersion(context.Background()); v != "26.3.27" {
			t.Fatalf("version = %q", v)
		}
	}
	if execs != 1 {
		t.Fatalf("xray exec'd %d times for an unchanged binary", execs)
	}
	// An upgrade: new bytes, new mtime.
	version = "26.8.1"
	if err := os.WriteFile(bin, []byte("v2-longer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v := c.xrayVersion(context.Background()); v != "26.8.1" || execs != 2 {
		t.Fatalf("after replacing the binary: version %q, %d execs", v, execs)
	}
	if c.XrayVersion() != "26.8.1" {
		t.Fatalf("XrayVersion() = %q", c.XrayVersion())
	}
}
