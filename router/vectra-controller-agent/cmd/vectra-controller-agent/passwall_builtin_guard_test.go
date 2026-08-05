package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-agent/internal/controlplane"
)

func TestBuiltInUpdaterTargetsManagedBinaryDetectsWrapper(t *testing.T) {
	backend := &fakeCommandRunner{stdout: "unmanaged:/usr/sbin/vectra-xray-wrapper\n"}

	managed, _, err := builtInUpdaterTargetsManagedBinary(context.Background(), backend, "xray")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if managed {
		t.Fatal("expected a wrapper-backed xray_file to be reported as unmanaged")
	}
}

func TestBuiltInUpdaterTargetsManagedBinaryAllowsRealBinary(t *testing.T) {
	backend := &fakeCommandRunner{stdout: "managed\n"}

	managed, _, err := builtInUpdaterTargetsManagedBinary(context.Background(), backend, "xray")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !managed {
		t.Fatal("expected a package-owned xray_file to be reported as managed")
	}
}

// The probe verdict is an allowlist: only the exact "managed" marker unlocks the
// built-in updater. Unexpected output means the probe did not run as written --
// a busybox without `head -c`, a truncated pipe, a shell that failed with exit 0
// -- and must not be read as permission to overwrite the component path.
func TestBuiltInUpdaterTargetsManagedBinaryFailsClosedOnUnexpectedOutput(t *testing.T) {
	for _, stdout := range []string{"", "ok", "managed: /usr/bin/xray", "head: unrecognized option"} {
		backend := &fakeCommandRunner{stdout: stdout}

		managed, _, err := builtInUpdaterTargetsManagedBinary(context.Background(), backend, "xray")
		if err != nil {
			t.Fatalf("stdout %q: unexpected error: %v", stdout, err)
		}
		if managed {
			t.Fatalf("stdout %q: expected an unrecognised probe verdict to block the built-in updater", stdout)
		}
	}
}

// The probe must reject any component path outside the package-owned
// /usr/bin/<component>, including one that does not exist yet: api.to_move()
// creates the file, so a wrapper path that is still missing is exactly as
// dangerous as one that is already in place.
//
// This runs the real probe text through /bin/sh against a fake uci, so the shell
// itself is under test -- including the '-' -> '_' key derivation, which has to
// match get_app_path()'s app_name:gsub("%-","_").
func TestBuiltInUpdaterProbeAllowlistsOnlyThePackageOwnedPath(t *testing.T) {
	const managedPathToken = "<managed>"

	probeCases := []struct {
		name        string
		component   string
		configured  string
		managedFile string
		wantManaged bool
	}{
		{name: "unset uci falls back to default_path", component: "xray", configured: "", managedFile: "binary", wantManaged: true},
		{name: "explicit package path", component: "xray", configured: managedPathToken, managedFile: "binary", wantManaged: true},
		{name: "package path not populated yet", component: "xray", configured: managedPathToken, managedFile: "", wantManaged: true},
		{name: "wrapper in place", component: "xray", configured: "/usr/sbin/vectra-xray-wrapper", managedFile: "binary"},
		{name: "wrapper not created yet", component: "xray", configured: "/usr/sbin/vectra-xray-wrapper", managedFile: ""},
		{name: "wrapper parked elsewhere", component: "xray", configured: "/root/xray-shim", managedFile: "binary"},
		{name: "script installed at the package path", component: "xray", configured: managedPathToken, managedFile: "script"},
		{name: "dashed component keeps its default_path", component: "sing-box", configured: "", managedFile: "binary", wantManaged: true},
		{name: "dashed component pointed at a wrapper", component: "sing-box", configured: "/usr/sbin/sing-box-shim", managedFile: "binary"},
	}

	for _, probeCase := range probeCases {
		t.Run(probeCase.name, func(t *testing.T) {
			stdout := runPasswallGuardProbe(t, probeCase.component, probeCase.configured, managedPathToken, probeCase.managedFile)
			managed := stdout == passwallGuardManagedMarker
			if managed != probeCase.wantManaged {
				t.Fatalf("probe verdict %q: managed = %v, want %v", stdout, managed, probeCase.wantManaged)
			}
		})
	}
}

// runPasswallGuardProbe executes passwallBuiltInGuardProbe with a fake uci and a
// temp-dir stand-in for the package-owned path. The fake uci answers only the
// exact key the probe is expected to ask for, so a wrong key derivation shows up
// as an empty value instead of silently passing.
func runPasswallGuardProbe(t *testing.T, component, configured, managedPathToken, managedFile string) string {
	t.Helper()

	root := t.TempDir()
	managedPath := filepath.Join(root, "bin", component)
	if err := os.MkdirAll(filepath.Dir(managedPath), 0o755); err != nil {
		t.Fatalf("create managed dir: %v", err)
	}

	switch managedFile {
	case "":
	case "binary":
		writeProbeFile(t, managedPath, "\x7fELF not-a-script")
	case "script":
		writeProbeFile(t, managedPath, "#!/bin/sh\nexport GOMEMLIMIT=80MiB\nexec -a \"$0\" /usr/bin/"+component+" \"$@\"\n")
	default:
		t.Fatalf("unknown managedFile kind %q", managedFile)
	}

	uciValue := configured
	if uciValue == managedPathToken {
		uciValue = managedPath
	}

	wantKey := "passwall2.@global_app[0]." + strings.ReplaceAll(component, "-", "_") + "_file"
	fakeBin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatalf("create fake bin dir: %v", err)
	}
	writeProbeFile(t, filepath.Join(fakeBin, "uci"), fmt.Sprintf(
		"#!/bin/sh\n[ \"$3\" = %s ] || exit 1\n[ -n %s ] || exit 1\nprintf '%%s\\n' %s\n",
		shellSingleQuote(wantKey),
		shellSingleQuote(uciValue),
		shellSingleQuote(uciValue),
	))

	command := exec.Command("sh", "-c", passwallBuiltInGuardProbe, "passwall-builtin-guard", component, managedPath)
	command.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	stdout, err := command.Output()
	if err != nil {
		t.Fatalf("probe failed: %v (stdout %q)", err, string(stdout))
	}

	verdict := strings.TrimSpace(string(stdout))
	if managedPathToken != "" && verdict != passwallGuardManagedMarker && !strings.HasPrefix(verdict, "unmanaged:") {
		t.Fatalf("probe emitted an unrecognised verdict %q", verdict)
	}
	return verdict
}

func writeProbeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// A wrapper-backed xray_file must stop the built-in updater before it runs:
// api.to_move() would otherwise overwrite /usr/sbin/vectra-xray-wrapper with the
// raw xray binary, silently dropping the GOMEMLIMIT/GOGC caps that keep xray
// alive on 234 MB boards.
func TestTryBuiltInPasswallComponentFallbackSkipsWhenWrapperConfigured(t *testing.T) {
	backend := &fakeCommandRunner{stdout: "unmanaged:/usr/sbin/vectra-xray-wrapper\n"}
	inventory := controlplane.RouterInventory{
		PackageVersions: map[string]string{"xray-core": "26.4.25-r1"},
		BinaryVersions:  map[string]string{"xray": "Xray 26.4.25"},
	}

	results, ok, err := func() ([]string, bool, error) {
		commandResults, ok, err, _ := tryBuiltInPasswallComponentFallback(
			context.Background(),
			backend,
			inventory,
			"xray-core",
			"26.7.28",
		)
		commands := make([]string, 0, len(commandResults))
		for _, result := range commandResults {
			commands = append(commands, result.Command)
		}
		return commands, ok, err
	}()

	if ok {
		t.Fatal("expected the built-in updater to be skipped")
	}
	if err == nil || !strings.Contains(err.Error(), "/usr/bin/xray") {
		t.Fatalf("expected the skip error to name the package-owned path, got %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected only the guard probe to run, got %d commands: %v", len(results), results)
	}
	for _, call := range backend.calls {
		if strings.Contains(call, "api.to_move") || strings.Contains(call, "luci.passwall2.api") {
			t.Fatalf("built-in updater ran despite wrapper guard: %q", call)
		}
	}
}
