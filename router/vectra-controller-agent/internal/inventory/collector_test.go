package inventory

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/rescue"
)

func TestParseSystemBoardOutput(t *testing.T) {
	board := parseSystemBoardOutput([]byte(`{
		"hostname": "router-test",
		"model": "Xiaomi Mi Router AX3000T",
		"board_name": "xiaomi,mi-router-ax3000t",
		"release": {
			"target": "mediatek/filogic",
			"version": "24.10.6",
			"description": "OpenWrt 24.10.6 r29141-81be8a8869"
		}
	}`))

	if got, want := board.Hostname, "router-test"; got != want {
		t.Fatalf("hostname = %q, want %q", got, want)
	}
	if got, want := board.BoardName, "xiaomi,mi-router-ax3000t"; got != want {
		t.Fatalf("board_name = %q, want %q", got, want)
	}
	if got, want := board.Release.Version, "24.10.6"; got != want {
		t.Fatalf("release.version = %q, want %q", got, want)
	}
	if got, want := board.Release.Target, "mediatek/filogic"; got != want {
		t.Fatalf("release.target = %q, want %q", got, want)
	}
}

func TestParseControlVersion(t *testing.T) {
	content := `Package: vectra-controller-agent
Version: 0.1.12-r5
Depends: ca-bundle, jsonfilter`

	if got, want := parseControlVersion(content), "0.1.12-r5"; got != want {
		t.Fatalf("parseControlVersion() = %q, want %q", got, want)
	}
}

func TestPackageVersionFromControlFile(t *testing.T) {
	controlPath := filepath.Join(t.TempDir(), "vectra-controller-agent.control")
	if err := os.WriteFile(controlPath, []byte(`Package: vectra-controller-agent
Version: 0.1.12-r5
`), 0o644); err != nil {
		t.Fatalf("write control file: %v", err)
	}

	if got, want := packageVersionFromControlFile(controlPath), "0.1.12-r5"; got != want {
		t.Fatalf("packageVersionFromControlFile() = %q, want %q", got, want)
	}
}

func TestBuildTelegramReachabilityCheckReachable(t *testing.T) {
	checkedAt := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)

	probe := buildTelegramReachabilityCheck(telegramProbeTarget{
		ID:    "telegram-org",
		Label: "telegram.org",
		URL:   telegramProbeURL,
	}, rescue.HTTPProbeResult{
		URL:        telegramProbeURL,
		Reachable:  true,
		StatusCode: 200,
		CheckedAt:  checkedAt,
	})

	if !probe.Reachable {
		t.Fatalf("Reachable = false, want true")
	}
	if got, want := probe.TargetURL, telegramProbeURL; got != want {
		t.Fatalf("TargetURL = %q, want %q", got, want)
	}
	if got, want := probe.Label, "telegram.org"; got != want {
		t.Fatalf("Label = %q, want %q", got, want)
	}
	if got, want := probe.StatusCode, 200; got != want {
		t.Fatalf("StatusCode = %d, want %d", got, want)
	}
	if got, want := probe.CheckedAt, checkedAt.Format(time.RFC3339); got != want {
		t.Fatalf("CheckedAt = %q, want %q", got, want)
	}
	if probe.Error != "" {
		t.Fatalf("Error = %q, want empty", probe.Error)
	}
}

func TestBuildTelegramReachabilityCheckUnreachable(t *testing.T) {
	probe := buildTelegramReachabilityCheck(telegramProbeTarget{
		ID:    "telegram-org",
		Label: "telegram.org",
		URL:   telegramProbeURL,
	}, rescue.HTTPProbeResult{
		URL:       telegramProbeURL,
		Reachable: false,
		Error:     "Get https://telegram.org/:   context deadline exceeded   ",
	})

	if probe.Reachable {
		t.Fatalf("Reachable = true, want false")
	}
	if got, want := probe.Error, "Get https://telegram.org/: context deadline exceeded"; got != want {
		t.Fatalf("Error = %q, want %q", got, want)
	}
}

func TestBuildTelegramReachabilitySummaryPartial(t *testing.T) {
	summary := buildTelegramReachabilitySummary([]controlplane.RouterReachabilityProbe{
		{
			Label:     "telegram.org",
			Reachable: true,
			CheckedAt: "2026-04-14T12:00:00Z",
			TargetURL: telegramProbeURL,
		},
		{
			Label:     "web.telegram.org",
			Reachable: false,
			CheckedAt: "2026-04-14T12:00:01Z",
			TargetURL: "https://web.telegram.org/",
			Error:     "unexpected status 403",
		},
	})

	if summary == nil {
		t.Fatal("expected summary")
	}
	if summary.Reachable {
		t.Fatalf("Reachable = true, want false")
	}
	if got, want := summary.Status, "partial"; got != want {
		t.Fatalf("Status = %q, want %q", got, want)
	}
	if got, want := summary.ReachableCount, 1; got != want {
		t.Fatalf("ReachableCount = %d, want %d", got, want)
	}
	if got, want := summary.TotalCount, 2; got != want {
		t.Fatalf("TotalCount = %d, want %d", got, want)
	}
	if got := len(summary.Checks); got != 2 {
		t.Fatalf("len(Checks) = %d, want 2", got)
	}
}

func TestBuildTelegramReachabilitySummaryBlocked(t *testing.T) {
	summary := buildTelegramReachabilitySummary([]controlplane.RouterReachabilityProbe{
		{
			Label:     "telegram.org",
			Reachable: false,
			CheckedAt: "2026-04-14T12:00:00Z",
			TargetURL: telegramProbeURL,
			Error:     "timeout",
		},
	})

	if summary == nil {
		t.Fatal("expected summary")
	}
	if summary.Reachable {
		t.Fatalf("Reachable = true, want false")
	}
	if got, want := summary.Status, "blocked"; got != want {
		t.Fatalf("Status = %q, want %q", got, want)
	}
	if got, want := summary.ReachableCount, 0; got != want {
		t.Fatalf("ReachableCount = %d, want %d", got, want)
	}
	if got, want := summary.TotalCount, 1; got != want {
		t.Fatalf("TotalCount = %d, want %d", got, want)
	}
}

func TestPasswallInventoryPackagesIncludesTcping(t *testing.T) {
	if !slices.Contains(passwallInventoryPackages, "tcping") {
		t.Fatalf("passwallInventoryPackages = %#v, want tcping to be tracked", passwallInventoryPackages)
	}
}
