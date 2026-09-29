package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"vectra-controller-agent/internal/controlplane"
)

func TestEnsureRuntimeXrayBinaryInstallsPinnedFleetBuild(t *testing.T) {
	backend := &fakeCommandRunner{}

	payload, _, err := runEnsurePasswallRuntimeJob(
		context.Background(),
		backend,
		map[string]interface{}{"actions": []interface{}{ensureRuntimeActionXrayBinary}},
		controlplane.RouterInventory{},
	)
	if err != nil {
		t.Fatalf("runEnsurePasswallRuntimeJob returned error: %v", err)
	}
	if got, want := payload["ok"], true; got != want {
		t.Fatalf("payload ok = %v, want %v", got, want)
	}
	if len(backend.calls) != 2 {
		t.Fatalf("expected the installer and the post-install restart, got %#v", backend.calls)
	}

	script := backend.calls[0]
	for _, needle := range []string{
		"'" + defaultFleetXrayVersion + "'",
		"'" + defaultFleetXraySHA256 + "'",
		"https://github.com/XTLS/Xray-core/releases/download/v" + defaultFleetXrayVersion + "/Xray-linux-arm64-v8a.zip",
		"sha256sum",
		`unzip -p "$zip" xray > "$bin.new"`,
		`mv "$bin.new" "$bin"`,
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("installer script is missing %q:\n%s", needle, script)
		}
	}

	// PassWall must be stopped BEFORE the download: with the runtime broken its
	// interception black-holes the router's own fetch of the fix (andrey-avito,
	// 2026-09-29: "Failed to connect ... after 3 ms").
	if !strings.Contains(script, "passwall='/etc/init.d/passwall2'") {
		t.Fatalf("installer must drive the real PassWall init script:\n%s", script)
	}
	stop := strings.Index(script, `"$passwall" stop`)
	fetch := strings.Index(script, "curl ")
	if stop < 0 || fetch < 0 || stop > fetch {
		t.Fatalf("installer must stop PassWall before downloading, got:\n%s", script)
	}
	// The broken binary is only removed after the new one has been downloaded
	// and its checksum verified.
	if remove := strings.Index(script, `rm -f "$bin"`); remove >= 0 && remove < strings.Index(script, "sha256sum") {
		t.Fatalf("installer must not remove the current binary before verifying the download:\n%s", script)
	}
	if got, want := backend.calls[1], "sh -c "+passwallPostInstallRecoveryCommand; got != want {
		t.Fatalf("restart command = %q, want %q", got, want)
	}
}

// The installer runs under BusyBox ash on the router; at least make sure it
// parses as POSIX shell before it ever gets that far.
func TestXrayBinaryRepairScriptParses(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on this host")
	}
	cmd := exec.Command(shell, "-n")
	cmd.Stdin = strings.NewReader(xrayBinaryRepairScript(defaultFleetXrayVersion, defaultFleetXraySHA256, false))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installer script does not parse: %v\n%s", err, out)
	}
}

func TestEnsureRuntimeXrayBinaryHonoursValidPin(t *testing.T) {
	backend := &fakeCommandRunner{}
	sha := strings.Repeat("ab", 32)

	_, _, err := runEnsurePasswallRuntimeJob(
		context.Background(),
		backend,
		map[string]interface{}{
			"actions":     []interface{}{ensureRuntimeActionXrayBinary},
			"xrayVersion": "26.8.1",
			"xraySha256":  sha,
		},
		controlplane.RouterInventory{},
	)
	if err != nil {
		t.Fatalf("runEnsurePasswallRuntimeJob returned error: %v", err)
	}
	script := backend.calls[0]
	if !strings.Contains(script, "'26.8.1'") || !strings.Contains(script, "'"+sha+"'") ||
		!strings.Contains(script, "/download/v26.8.1/Xray-linux-arm64-v8a.zip") {
		t.Fatalf("installer did not use the requested pin:\n%s", script)
	}
}

func TestEnsureRuntimeXrayBinaryRejectsUnsafePins(t *testing.T) {
	for name, payload := range map[string]map[string]interface{}{
		"version with shell":   {"xrayVersion": "26.7.28'; reboot; '", "xraySha256": strings.Repeat("ab", 32)},
		"version without hash": {"xrayVersion": "26.8.1"},
		"short hash":           {"xraySha256": "abc"},
		"non-hex hash":         {"xraySha256": strings.Repeat("zz", 32)},
	} {
		t.Run(name, func(t *testing.T) {
			backend := &fakeCommandRunner{}
			payload["actions"] = []interface{}{ensureRuntimeActionXrayBinary}

			result, _, err := runEnsurePasswallRuntimeJob(context.Background(), backend, payload, controlplane.RouterInventory{})
			if err == nil {
				t.Fatalf("expected the unsafe pin to be rejected")
			}
			if got, want := result["ok"], false; got != want {
				t.Fatalf("payload ok = %v, want %v", got, want)
			}
			if len(backend.calls) != 0 {
				t.Fatalf("a rejected pin must not run anything, got %#v", backend.calls)
			}
		})
	}
}

func TestVectraXrayWrapperScriptMatchesPackagedFile(t *testing.T) {
	packaged, err := os.ReadFile("../../openwrt/files/usr/sbin/vectra-xray-wrapper")
	if err != nil {
		t.Fatalf("read packaged wrapper: %v", err)
	}
	if string(packaged) != vectraXrayWrapperScript {
		t.Fatalf("embedded wrapper drifted from openwrt/files/usr/sbin/vectra-xray-wrapper; copy the file into vectraXrayWrapperScript")
	}
}

func TestXrayBinaryRepairRestoresClobberedWrapperFirst(t *testing.T) {
	script := xrayBinaryRepairScript(defaultFleetXrayVersion, defaultFleetXraySHA256, false)
	restore := strings.Index(script, `mv "$wrapper.tmp" "$wrapper"`)
	shortcut := strings.Index(script, "already installed")
	if restore < 0 || shortcut < 0 || restore > shortcut {
		t.Fatalf("the wrapper must be restored before the already-current shortcut:\n%s", script)
	}
	if !strings.Contains(script, `exec -a "$0" /usr/bin/xray "$@"`) {
		t.Fatalf("restored wrapper must exec /usr/bin/xray with argv intact:\n%s", script)
	}
}
