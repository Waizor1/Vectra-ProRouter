package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-agent/internal/config"
	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/passwall"
	"vectra-controller-agent/internal/state"
)

type fakeCommandRunner struct {
	calls   []string
	runHook func(name string, args ...string)
}

func (f *fakeCommandRunner) Run(_ context.Context, name string, args ...string) (passwall.CommandResult, error) {
	command := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.calls = append(f.calls, command)
	if f.runHook != nil {
		f.runHook(name, args...)
	}
	return passwall.CommandResult{
		Command: command,
		Stdout:  "ok",
	}, nil
}

func TestRunStagedPackageInstallJobInstallsPinnedArtifactsWithoutOpkgUpdate(t *testing.T) {
	payloads := map[string][]byte{
		"/agent.ipk": []byte("agent-package"),
		"/luci.ipk":  []byte("luci-package"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		payload, ok := payloads[request.URL.Path]
		if !ok {
			http.NotFound(response, request)
			return
		}
		_, _ = response.Write(payload)
	}))
	t.Cleanup(server.Close)

	sha := func(payload []byte) string {
		sum := sha256.Sum256(payload)
		return hex.EncodeToString(sum[:])
	}

	cfg := config.Config{
		StatePath:      filepath.Join(t.TempDir(), "state.json"),
		RequestTimeout: 0,
		RouterID:       "router-123",
	}
	persisted := state.PersistedState{}
	backend := &fakeCommandRunner{}

	err := runStagedPackageInstallJob(
		context.Background(),
		nil,
		&cfg,
		&persisted,
		"job-123",
		backend,
		artifactJob{
			ArtifactVersion: "0.1.8-r1",
			PackageList: []string{
				"vectra-controller-agent",
				"luci-app-vectra-controller",
			},
			PackageArtifacts: []packageArtifact{
				{
					Name:            "vectra-controller-agent",
					ArtifactURL:     server.URL + "/agent.ipk",
					SHA256:          sha(payloads["/agent.ipk"]),
					ArtifactVersion: "0.1.8-r1",
				},
				{
					Name:            "luci-app-vectra-controller",
					ArtifactURL:     server.URL + "/luci.ipk",
					SHA256:          sha(payloads["/luci.ipk"]),
					ArtifactVersion: "0.1.8-r1",
				},
			},
		},
		true,
		true,
		false,
	)
	if !errors.Is(err, errControllerRestartRequested) {
		t.Fatalf("expected controller restart request, got %v", err)
	}

	if len(backend.calls) != 2 {
		t.Fatalf("expected wrapped opkg install plus delayed restart scheduling, got %v", backend.calls)
	}
	if got := backend.calls[0]; !strings.HasPrefix(
		got,
		"sh -c VECTRA_SKIP_POSTINST_RESTART='1' 'opkg' 'install' '--force-reinstall' ",
	) {
		t.Fatalf(
			"package command = %q, want forced opkg install wrapped with %s",
			got,
			skipControllerPostinstRestartEnv,
		)
	}
	if got := backend.calls[1]; !strings.HasPrefix(
		got,
		"sh -c (sleep 2; /etc/init.d/vectra-controller restart >/tmp/vectra-controller-self-update.log 2>&1) &",
	) {
		t.Fatalf("restart scheduling command = %q, want delayed controller restart", got)
	}
	if persisted.PendingJobResult == nil || persisted.PendingJobResult.Status != "success" {
		t.Fatalf("expected pending success result to survive controller restart, got %#v", persisted.PendingJobResult)
	}
}

func TestDefaultPasswallPackageListIncludesRecoveryDependencies(t *testing.T) {
	want := []string{
		"tcping",
		"xray-core",
		"v2ray-geoip",
		"v2ray-geosite",
		"geoview",
		"chinadns-ng",
		"dnsmasq-full",
		"kmod-nft-socket",
		"kmod-nft-tproxy",
		"kmod-nft-nat",
		"luci-app-passwall2",
	}

	if !reflect.DeepEqual(defaultPasswallPackageList, want) {
		t.Fatalf("defaultPasswallPackageList = %#v, want %#v", defaultPasswallPackageList, want)
	}
}

func TestExecutePackageInstallSequenceRepairsPasswallAfterInstall(t *testing.T) {
	backend := &fakeCommandRunner{}

	results, err := executePackageInstallSequence(
		context.Background(),
		backend,
		[]string{"install", "luci-app-passwall2", "dnsmasq-full"},
		false,
		true,
		true,
	)
	if err != nil {
		t.Fatalf("executePackageInstallSequence returned error: %v", err)
	}

	wantCalls := []string{
		"opkg update",
		"opkg install luci-app-passwall2 dnsmasq-full",
		"lua /usr/share/passwall2/rule_update.lua log geoip,geosite",
		"sh -c " + passwallPostInstallRecoveryCommand,
	}
	if !reflect.DeepEqual(backend.calls, wantCalls) {
		t.Fatalf("backend calls = %#v, want %#v", backend.calls, wantCalls)
	}

	if got := collectPostInstallCommands(results); !reflect.DeepEqual(got, wantCalls[2:]) {
		t.Fatalf("collectPostInstallCommands(results) = %#v, want %#v", got, wantCalls[2:])
	}
}

func TestRunOpkgInstallCreatesAndClearsSelfUpdateSentinel(t *testing.T) {
	originalSentinelPath := skipControllerPostinstRestartSentinelPath
	sentinelPath := filepath.Join(t.TempDir(), "controller-self-update.guard")
	skipControllerPostinstRestartSentinelPath = sentinelPath
	t.Cleanup(func() {
		skipControllerPostinstRestartSentinelPath = originalSentinelPath
	})

	backend := &fakeCommandRunner{
		runHook: func(name string, args ...string) {
			if name != "sh" {
				t.Fatalf("expected wrapped shell install, got %q", name)
			}
			if _, err := os.Stat(sentinelPath); err != nil {
				t.Fatalf("expected sentinel %s to exist during install: %v", sentinelPath, err)
			}
		},
	}

	result, err := runOpkgInstall(
		context.Background(),
		backend,
		[]string{"install", "--force-reinstall", "/tmp/controller.ipk"},
		true,
	)
	if err != nil {
		t.Fatalf("runOpkgInstall returned error: %v", err)
	}
	if !strings.Contains(result.Command, skipControllerPostinstRestartEnv+"='1'") {
		t.Fatalf("expected wrapped command to export %s, got %q", skipControllerPostinstRestartEnv, result.Command)
	}
	if _, err := os.Stat(sentinelPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected sentinel cleanup after install, stat err = %v", err)
	}
}

func TestSortPasswallPackagesKeepsManagedStackDeterministic(t *testing.T) {
	got := sortPasswallPackages([]string{
		"luci-app-passwall2",
		"kmod-nft-tproxy",
		"xray-core",
		"tcping",
		"dnsmasq-full",
	})

	want := []string{
		"xray-core",
		"tcping",
		"dnsmasq-full",
		"kmod-nft-tproxy",
		"luci-app-passwall2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortPasswallPackages() = %#v, want %#v", got, want)
	}
}

func TestVersionAtLeastSupportsRuntimeDriftComparison(t *testing.T) {
	if !versionAtLeast("Xray 26.4.15", "26.3.27-r1") {
		t.Fatalf("expected runtime version to satisfy target package version")
	}
	if versionAtLeast("sing-box 1.12.0", "1.13.6-r1") {
		t.Fatalf("expected older runtime version to fail target comparison")
	}
}

func TestAssessPasswallPackageStatusRequiresRuntimeConvergenceForRuntimePackages(t *testing.T) {
	status, drift := assessPasswallPackageStatus(
		"xray-core",
		"26.3.27-r1",
		"26.3.27-r1",
		"Xray 26.3.26",
	)
	if status != "" || drift {
		t.Fatalf("expected package version alone to be insufficient, got status=%q drift=%v", status, drift)
	}

	status, drift = assessPasswallPackageStatus(
		"xray-core",
		"26.3.27-r1",
		"26.3.20-r1",
		"Xray 26.3.27",
	)
	if status != "runtime-only-converged" || !drift {
		t.Fatalf("expected runtime-only convergence drift, got status=%q drift=%v", status, drift)
	}
}

func TestPackageVersionAtLeastMatchesOpkgSemanticsForMixedGeodataVersions(t *testing.T) {
	if !packageVersionAtLeast("20250916122507-r1", "202603292224.1") {
		t.Fatalf("expected opkg-style comparison to keep 14-digit geosite build ahead of 12-digit target")
	}
	if packageVersionAtLeast("202603292224.1", "20250916122507-r1") {
		t.Fatalf("expected 12-digit geosite target to stay behind 14-digit installed build")
	}
	if !packageVersionAtLeast("202604090028.1", "202603260032.1") {
		t.Fatalf("expected newer 12-digit geoip datestamp to satisfy older target")
	}
}

func TestAssessPasswallPackageStatusTreatsNewerGeodataPackageAsCurrent(t *testing.T) {
	status, drift := assessPasswallPackageStatus(
		"v2ray-geosite",
		"202603292224.1",
		"20250916122507-r1",
		"",
	)
	if status != "already-current" || drift {
		t.Fatalf("expected newer geodata package to count as current, got status=%q drift=%v", status, drift)
	}
}

func TestUpdateSinglePasswallPackageSkipsOlderGeodataTargetWhenRouterAlreadyAhead(t *testing.T) {
	backend := &fakeCommandRunner{}
	currentInventory := controlplane.RouterInventory{
		PackageVersions: map[string]string{
			"v2ray-geosite": "20250916122507-r1",
		},
		BinaryVersions: map[string]string{},
	}

	result, commandResults, nextInventory := updateSinglePasswallPackage(
		context.Background(),
		backend,
		currentInventory,
		"v2ray-geosite",
		artifactJob{
			PackageList: []string{"v2ray-geosite"},
			PackageArtifacts: []packageArtifact{
				{
					Name:            "v2ray-geosite",
					ArtifactVersion: "202603292224.1",
				},
			},
		},
	)

	if result.Status != "already-current" || result.PathUsed != "not-needed" {
		t.Fatalf("expected geosite to be treated as already current, got %#v", result)
	}
	if len(commandResults) != 0 || len(backend.calls) != 0 {
		t.Fatalf("expected no package-path commands for older geosite target, got results=%#v calls=%#v", commandResults, backend.calls)
	}
	if nextInventory.PackageVersions["v2ray-geosite"] != "20250916122507-r1" {
		t.Fatalf("expected inventory to remain on the newer geosite package, got %#v", nextInventory.PackageVersions)
	}
}

func TestReconcileRuleManagedPasswallPackageResultTreatsOpkgAheadPackageAsCurrent(t *testing.T) {
	result := reconcileRuleManagedPasswallPackageResult(passwallPackageUpdateResult{
		Package:              "v2ray-geosite",
		PackageTargetVersion: "202603292224.1",
		Status:               "failed",
		PathUsed:             "package",
		PackageVersionBefore: "20250916122507-r1",
		PackageVersionAfter:  "20250916122507-r1",
		Error:                "v2ray-geosite did not reach target through package path",
	})

	if result.Status != "already-current" {
		t.Fatalf("expected rule-managed geosite to reconcile as already-current, got %#v", result)
	}
	if result.PathUsed != "not-needed" {
		t.Fatalf("expected reconciled pathUsed to switch to not-needed, got %#v", result)
	}
	if result.Error != "" || result.DriftDetected {
		t.Fatalf("expected reconciled geosite result to clear error/drift, got %#v", result)
	}
}

func TestReconcileRuleManagedPasswallPackageResultTreatsRuleRefreshAsUpdated(t *testing.T) {
	result := reconcileRuleManagedPasswallPackageResult(passwallPackageUpdateResult{
		Package:              "v2ray-geoip",
		PackageTargetVersion: "202603260032.1",
		Status:               "storage-blocked",
		PathUsed:             "package",
		PackageVersionBefore: "202511270021.1",
		PackageVersionAfter:  "202511270021.1",
		Error:                "v2ray-geoip package path skipped: not enough overlay space",
	})

	if result.Status != "updated" {
		t.Fatalf("expected rule-managed geoip to reconcile as updated, got %#v", result)
	}
	if result.PathUsed != "not-needed" {
		t.Fatalf("expected reconciled pathUsed to switch to not-needed, got %#v", result)
	}
	if result.Error != "" {
		t.Fatalf("expected reconciled geoip result to clear error, got %#v", result)
	}
	if !result.DriftDetected {
		t.Fatalf("expected geoip rule-refresh reconciliation to preserve drift, got %#v", result)
	}
}

func TestResolvePasswallRuntimeTargetVersionKeepsExplicitRuntimeTargetForBuiltInXray(t *testing.T) {
	got := resolvePasswallRuntimeTargetVersion(
		artifactJob{
			Strategy:             "xray-built-in-first",
			RuntimeTargetVersion: "26.4.17",
		},
		"xray-core",
		"26.3.27-r1",
	)

	if got != "26.4.17" {
		t.Fatalf("resolvePasswallRuntimeTargetVersion() = %q, want %q", got, "26.4.17")
	}
}

func TestResolvePasswallStatusTargetVersionPrefersRuntimeTargetForRuntimePackages(t *testing.T) {
	got := resolvePasswallStatusTargetVersion("xray-core", "26.3.27-r1", "26.4.17")
	if got != "26.4.17" {
		t.Fatalf("resolvePasswallStatusTargetVersion() = %q, want %q", got, "26.4.17")
	}

	got = resolvePasswallStatusTargetVersion("luci-app-passwall2", "26.4.10-r1", "26.4.17")
	if got != "26.4.10-r1" {
		t.Fatalf("resolvePasswallStatusTargetVersion() = %q, want %q", got, "26.4.10-r1")
	}
}

func TestClassifySuccessfulPasswallStatusDistinguishesRuntimeFallback(t *testing.T) {
	status, drift := classifySuccessfulPasswallStatus(
		"xray-core",
		"built-in-updater",
		"26.3.27-r1",
		"26.3.27",
		"25.10.15-r1",
		"25.10.15-r1",
		"Xray 26.2.6",
		"Xray 26.4.15",
	)
	if status != "runtime-updated" || !drift {
		t.Fatalf("expected runtime-updated drift, got status=%q drift=%v", status, drift)
	}
}

func TestClassifySuccessfulPasswallStatusKeepsRuntimeOnlyConvergenceWhenRuntimeAlreadyAhead(t *testing.T) {
	status, drift := classifySuccessfulPasswallStatus(
		"xray-core",
		"built-in-updater",
		"26.3.27-r1",
		"26.3.27",
		"25.10.15-r1",
		"25.10.15-r1",
		"Xray 26.4.15",
		"Xray 26.4.15",
	)
	if status != "runtime-only-converged" || !drift {
		t.Fatalf("expected runtime-only-converged drift, got status=%q drift=%v", status, drift)
	}
}

func TestFailedPasswallStatusesTreatStorageBlockAsTerminal(t *testing.T) {
	if !isFailedPasswallStatus("storage-blocked") {
		t.Fatalf("expected storage-blocked to stop the overall job")
	}
	if isFailedPasswallStatus("runtime-updated") {
		t.Fatalf("did not expect runtime-updated to stop the overall job")
	}
}

func TestPasswallPackageUpdateNeedsFeedRefresh(t *testing.T) {
	jobWithPinnedArtifacts := artifactJob{
		PackageList: []string{"xray-core", "luci-app-passwall2"},
		PackageArtifacts: []packageArtifact{
			{
				Name:        "xray-core",
				ArtifactURL: "https://example.com/xray-core.ipk",
			},
			{
				Name:        "luci-app-passwall2",
				ArtifactURL: "https://example.com/luci-app-passwall2.ipk",
			},
		},
	}
	if passwallPackageUpdateNeedsFeedRefresh(jobWithPinnedArtifacts) {
		t.Fatalf("expected pinned PassWall artifacts to skip opkg feed refresh")
	}

	jobWithFeedGap := artifactJob{
		PackageList: []string{"xray-core", "luci-app-passwall2"},
		PackageArtifacts: []packageArtifact{
			{
				Name:        "xray-core",
				ArtifactURL: "https://example.com/xray-core.ipk",
			},
		},
	}
	if !passwallPackageUpdateNeedsFeedRefresh(jobWithFeedGap) {
		t.Fatalf("expected missing package artifact to require opkg feed refresh")
	}
}
