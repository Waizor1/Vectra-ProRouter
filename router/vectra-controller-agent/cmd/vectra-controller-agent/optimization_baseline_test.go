package main

import (
	"strings"
	"testing"

	"vectra-controller-agent/internal/passwall"
)

func TestParseCollectOptimizationBaselineJobDefaultsToReadOnlyScope(t *testing.T) {
	request := parseCollectOptimizationBaselineJob(map[string]interface{}{})

	if request.LogSource != "all" {
		t.Fatalf("LogSource = %q, want all", request.LogSource)
	}
	if request.LogLines != 160 {
		t.Fatalf("LogLines = %d, want 160", request.LogLines)
	}
	if !request.IncludeLogs {
		t.Fatalf("IncludeLogs = false, want true")
	}
	if !request.IncludeRoutes {
		t.Fatalf("IncludeRoutes = false, want true")
	}
}

func TestParseOptimizationProcessLinesClassifiesProxyProcesses(t *testing.T) {
	processes := parseOptimizationProcessLines(
		"123\t49680\t1361952\t10\t/tmp/etc/passwall2/bin/xray run -c /tmp/etc/passwall2/acl/default/global.json\n" +
			"456\t1024\t2048\t2\t/usr/sbin/dnsmasq -C /tmp/etc/dnsmasq_default.conf\n" +
			"bad\t1\t2\t3\tignored\n",
	)

	if len(processes) != 2 {
		t.Fatalf("len(processes) = %d, want 2: %#v", len(processes), processes)
	}
	if got := processes[0]["role"]; got != "xray" {
		t.Fatalf("first process role = %v, want xray", got)
	}
	if got := processes[0]["vmRssKb"]; got != 49680 {
		t.Fatalf("first process rss = %v, want 49680", got)
	}
	if got := processes[1]["role"]; got != "passwall-dnsmasq" {
		t.Fatalf("second process role = %v, want passwall-dnsmasq", got)
	}
}

func TestOptimizationProcessScanSkipsDiagnosticShell(t *testing.T) {
	if !strings.Contains(optimizationProcessScanCommand, `[ "$pid" = "$$" ] && continue`) {
		t.Fatalf("optimization process scan must skip its own shell command so the embedded xray match pattern is not reported as an xray process")
	}
}

func TestAppendOptimizationRouteVerificationWarningsMarksFailedRouteSmoke(t *testing.T) {
	warnings := appendOptimizationRouteVerificationWarnings(nil, passwall.RouteVerificationResult{
		OK:     false,
		Errors: []string{"Special: url_test_node returned 000", " "},
	})

	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want one route verification warning", warnings)
	}
	if got := warnings[0]; got != "route verification: Special: url_test_node returned 000" {
		t.Fatalf("warning = %q", got)
	}
}
