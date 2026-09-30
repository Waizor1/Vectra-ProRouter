package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigReadsEnabledAndURL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vectra-reporter")
	if c := readConfig(p); !c.enabled || c.url != defaultURL {
		t.Fatalf("no file: %+v", c)
	}
	if err := os.WriteFile(p, []byte("config reporter 'main'\n\toption enabled '0'\n\toption url 'https://example.test/errors/router'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := readConfig(p); c.enabled || c.url != "https://example.test/errors/router" {
		t.Fatalf("%+v", c)
	}
}

func TestXrayRunningFindsVctlsXray(t *testing.T) {
	root := t.TempDir()
	for pid, cmd := range map[string]string{"10": "/usr/sbin/vctl\x00agent\x00", "20": "/usr/bin/xray\x00run\x00-c\x00/var/run/vectra-controller-pro/xray.json\x00"} {
		if err := os.MkdirAll(filepath.Join(root, pid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, pid, "cmdline"), []byte(cmd), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !xrayRunning(root) {
		t.Fatal("xray not found")
	}
	os.Remove(filepath.Join(root, "20", "cmdline"))
	if xrayRunning(root) {
		t.Fatal("found without xray")
	}
}

// A command that hangs (nft stuck on netlink) is given up on: the run holds
// the lock, and a run that never ends would silence the reporter.
func TestCommandOKGivesUpOnAHang(t *testing.T) {
	start := time.Now()
	if commandOK(200*time.Millisecond, "sleep", "5") {
		t.Fatal("a hang counted as success")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("waited for the hang")
	}
	if !commandOK(time.Second, "true") {
		t.Fatal("true failed")
	}
}
