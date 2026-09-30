package agentcfg

import (
	"testing"
	"time"

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
