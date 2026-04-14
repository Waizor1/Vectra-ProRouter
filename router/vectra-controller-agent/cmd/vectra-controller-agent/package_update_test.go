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

func TestRunStagedPackageInstallJobUpdatesOpkgIndexBeforeInstall(t *testing.T) {
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

	if len(backend.calls) < 2 {
		t.Fatalf("expected at least opkg update and opkg install, got %v", backend.calls)
	}
	if got, want := backend.calls[0], "opkg update"; got != want {
		t.Fatalf("first package command = %q, want %q", got, want)
	}
	if got := backend.calls[1]; !strings.HasPrefix(
		got,
		"sh -c VECTRA_SKIP_POSTINST_RESTART='1' 'opkg' 'install' '--force-reinstall' ",
	) {
		t.Fatalf(
			"second package command = %q, want forced opkg install wrapped with %s",
			got,
			skipControllerPostinstRestartEnv,
		)
	}
	if persisted.PendingJobResult == nil || persisted.PendingJobResult.Status != "success" {
		t.Fatalf("expected pending success result to survive controller restart, got %#v", persisted.PendingJobResult)
	}
}

func TestDefaultPasswallPackageListIncludesRecoveryDependencies(t *testing.T) {
	want := []string{
		"luci-app-passwall2",
		"xray-core",
		"sing-box",
		"hysteria",
		"geoview",
		"v2ray-geoip",
		"v2ray-geosite",
		"dnsmasq-full",
		"chinadns-ng",
		"kmod-nft-socket",
		"kmod-nft-tproxy",
		"kmod-nft-nat",
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
