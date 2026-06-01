package agentcfg

import (
	"testing"
	"time"
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
