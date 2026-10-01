package agentcfg

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/jobsafety"
	"vectra-controller-pro/internal/localctl"
)

func TestParseAppliesDefaults(t *testing.T) {
	c, err := Parse([]byte(`{"controlUrl":"https://api.example.net"}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if c.PollInterval() != 60*time.Second {
		t.Errorf("poll default = %v", c.PollInterval())
	}
	if c.RequestTimeout() != 10*time.Second {
		t.Errorf("timeout default = %v", c.RequestTimeout())
	}
	if c.StatePath == "" || c.XrayConfigPath == "" || c.XrayRenderPath == "" || c.XrayBinary == "" {
		t.Errorf("path defaults missing: %+v", c)
	}
	if c.LegacyStatePath != "/etc/vectra-controller/state.json" {
		t.Errorf("legacy state default = %q", c.LegacyStatePath)
	}
	if c.JobSafety.HeavyMemoryFloorMB != 40 {
		t.Errorf("jobsafety defaults not applied: %+v", c.JobSafety)
	}
}

func TestParseRejectsMissingControlURL(t *testing.T) {
	if _, err := Parse([]byte(`{}`)); err == nil {
		t.Fatal("expected error for missing controlUrl")
	}
}

func TestParseHonoursExplicitValues(t *testing.T) {
	c, err := Parse([]byte(`{"controlUrl":"https://x","pollIntervalSeconds":15,"requestTimeoutSeconds":5}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.PollInterval() != 15*time.Second || c.RequestTimeout() != 5*time.Second {
		t.Errorf("explicit timing not honoured: poll=%v timeout=%v", c.PollInterval(), c.RequestTimeout())
	}
}

// On a router the UI's files land exactly on localctl's defaults; anywhere
// else they follow the state and status paths, so a test never writes /etc.
func TestUIPathsFollowTheStateAndStatusDirs(t *testing.T) {
	c, err := Parse([]byte(`{"controlUrl":"https://x.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.OverridesPath != localctl.DefaultOverridesPath || c.EntriesPath != localctl.DefaultEntriesPath ||
		c.EntriesIndexPath != localctl.DefaultEntriesIndexPath || c.UISocketPath != localctl.DefaultSocketPath {
		t.Fatalf("router defaults = %+v", c)
	}
	c, err = Parse([]byte(`{"controlUrl":"https://x.test","statePath":"/tmp/t/state.json","statusPath":"/tmp/r/status.json"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.OverridesPath != "/tmp/t/local-overrides.json" || c.EntriesPath != "/tmp/t/provider-entries.json.gz" || c.UISocketPath != "/tmp/r/ui.sock" {
		t.Fatalf("derived = %s %s %s", c.OverridesPath, c.EntriesPath, c.UISocketPath)
	}
}

// The panel is reached over https: the router's token travels in every call.
// Plain http is refused — anywhere but the router itself, where nothing
// leaves the box (a panel stand-in on loopback).
func TestParseRejectsAControlURLThatIsNotHTTPS(t *testing.T) {
	for _, u := range []string{"http://api.vectra-pro.net", "http://10.0.0.5:3000", "ftp://api.vectra-pro.net", "api.vectra-pro.net",
		"unused", "https://", "https:///api", "http://127.0.0.1.evil.test"} {
		if _, err := Parse([]byte(`{"controlUrl":"` + u + `"}`)); err == nil {
			t.Errorf("controlUrl %q was accepted", u)
		}
	}
	for _, u := range []string{"https://api.vectra-pro.net", "HTTPS://api.vectra-pro.net/", "http://127.0.0.1:18080", "http://[::1]:1", "http://localhost:3000"} {
		if _, err := Parse([]byte(`{"controlUrl":"` + u + `"}`)); err != nil {
			t.Errorf("controlUrl %q: %v", u, err)
		}
	}
}

// PassWall2's retirement (internal/retire): on unless UCI retire_passwall is
// '0' (render-xray-config.sh: noRetirePassWall), after a day unless
// passwall_retire_after says otherwise (passWallRetireAfterSec).
func TestPassWallsRetirementFollowsUCI(t *testing.T) {
	c, err := Parse([]byte(`{"controlUrl":"https://x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.NoRetirePassWall || c.PassWallRetireAfter() != 24*time.Hour {
		t.Fatalf("defaults: off %v, after %v", c.NoRetirePassWall, c.PassWallRetireAfter())
	}
	c, err = Parse([]byte(`{"controlUrl":"https://x","noRetirePassWall":true,"passWallRetireAfterSec":600}`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.NoRetirePassWall || c.PassWallRetireAfter() != 10*time.Minute {
		t.Fatalf("set: off %v, after %v", c.NoRetirePassWall, c.PassWallRetireAfter())
	}
}

// Every key render-xray-config.sh writes is one this package reads: a key
// spelled otherwise is an option the router silently ignores.
func TestTheRenderScriptWritesOnlyKnownKeys(t *testing.T) {
	raw, err := os.ReadFile("../../openwrt/files/usr/libexec/vectra-controller-pro/render-xray-config.sh")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, typ := range []reflect.Type{reflect.TypeOf(Config{}), reflect.TypeOf(jobsafety.Config{})} {
		for i := 0; i < typ.NumField(); i++ {
			if tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); tag != "" {
				known[tag] = true
			}
		}
	}
	keys := regexp.MustCompile(`json_add_(?:string|int|boolean|array|object) ([A-Za-z0-9]+)`).FindAllStringSubmatch(string(raw), -1)
	if len(keys) < 20 {
		t.Fatalf("found only %d keys: the pattern no longer reads the script", len(keys))
	}
	for _, k := range keys {
		if !known[k[1]] {
			t.Errorf("render-xray-config.sh writes %q, which agentcfg does not read", k[1])
		}
	}
	for _, want := range []string{"noRetirePassWall", "passWallRetireAfterSec"} {
		if !strings.Contains(string(raw), " "+want+" ") {
			t.Errorf("render-xray-config.sh does not write %s", want)
		}
	}
}
