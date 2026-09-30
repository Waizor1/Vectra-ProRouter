package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeInit writes a stand-in for /etc/init.d/vectra-controller with the given
// mode, and returns a run func that answers the verbs listed in ok with exit 0.
func fakeInit(t *testing.T, mode os.FileMode, ok ...string) (string, func(string, string) error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vectra-controller")
	if err := os.WriteFile(path, []byte("#!/bin/sh /etc/rc.common\n"), mode); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{}
	for _, v := range ok {
		allowed[v] = true
	}
	return path, func(_, verb string) error {
		if allowed[verb] {
			return nil
		}
		return errors.New("exit 1")
	}
}

func TestDetectLegacyAgent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     os.FileMode
		verbs    []string
		wantOwns bool
		wantDesc string
	}{
		// The canary situation this guard exists for: vctl was installed but the
		// hand-over never ran, so the legacy agent is still armed for the next
		// boot AND its watchdog is live right now.
		{"enabled and running", 0o755, []string{"enabled", "running"}, true, "enabled and running"},

		// Enabled alone is enough. It survives a reboot, and procd starts the
		// watchdog from it — "not running this second" is not ownership given up.
		{"enabled only", 0o755, []string{"enabled"}, true, "enabled"},

		// Started by hand while disabled. Narrower, but it is a live watchdog.
		{"running only", 0o755, []string{"running"}, true, "running"},

		// Installed, stopped and disabled: this is precisely the state
		// start_service leaves behind after a successful hand-over, so it must
		// NOT trip the guard or vctl could never start on a canary router.
		{"handed over", 0o755, nil, false, "installed"},

		// Never installed — the fleet's xray-direct-from-new state, and every
		// data-plane stand container.
		{"absent", 0, nil, false, "installed"},

		// start_service guards its own probe with `[ -x ]`, so a non-executable
		// file could not take the router back either. Matching that keeps the
		// two definitions of "installed" identical.
		{"not executable", 0o644, []string{"enabled"}, false, "installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var path string
			var run func(string, string) error
			if tc.mode == 0 {
				path, run = filepath.Join(t.TempDir(), "absent"), func(string, string) error { return nil }
			} else {
				path, run = fakeInit(t, tc.mode, tc.verbs...)
			}
			got := detectLegacyAgent(path, run)
			if got.owns() != tc.wantOwns {
				t.Errorf("owns() = %v, want %v (%+v)", got.owns(), tc.wantOwns, got)
			}
			if got.describe() != tc.wantDesc {
				t.Errorf("describe() = %q, want %q", got.describe(), tc.wantDesc)
			}
		})
	}
}

// A directory at the init path is not a controller. Guarding on os.Stat alone
// would call it installed and then hand `exec.Command` a directory.
func TestDetectLegacyAgentIgnoresADirectory(t *testing.T) {
	dir := t.TempDir()
	got := detectLegacyAgent(dir, func(string, string) error { return nil })
	if got.owns() || got.Installed {
		t.Errorf("a directory was reported as an installed legacy agent: %+v", got)
	}
}

// The refusal has to be actionable. An operator who reads "refused" and no more
// reaches for the override, which is the outcome the guard exists to prevent.
func TestLegacyConflictErrorNamesTheWayOut(t *testing.T) {
	msg := legacyConflictError(legacyAgent{Installed: true, Enabled: true}, "-ignore-legacy-agent").Error()
	for _, want := range []string{
		"/etc/init.d/vectra-controller-pro start", // the supported hand-over
		"-ignore-legacy-agent",                    // the override, named exactly
		"watchdog",                                // why starting anyway is not enough
		"enabled",                                 // which state was actually observed
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, msg)
		}
	}
}

// realFakeInit writes an EXECUTABLE rc.common-shaped script that answers
// `enabled` with exit 0, and points legacyInitScript at it for the test. The
// probe then runs for real, through runInitVerb and os/exec — so a broken
// argv, a swallowed exit code or a wrong verb fails here.
func realFakeInit(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vectra-controller")
	script := "#!/bin/sh\ncase \"$1\" in enabled) exit 0 ;; *) exit 1 ;; esac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := legacyInitScript
	legacyInitScript = path
	t.Cleanup(func() { legacyInitScript = old })
}

// THE POINT OF THE FILE: the real command must refuse. `vctl supervise` is the
// path that bypasses start_service — the only place the hand-over is done — so
// a guard that exists but is not wired into cmdSupervise closes nothing.
//
// The refusal must also come BEFORE any work: these runs pass a config path
// that does not exist, so anything other than the conflict error means the
// command got past the gate.
func TestSuperviseRefusesWhileTheLegacyAgentOwnsTheRouter(t *testing.T) {
	realFakeInit(t)
	missing := filepath.Join(t.TempDir(), "no-such-config.json")

	err := cmdSupervise([]string{"-config", missing, "-provider", missing})
	if err == nil || !strings.Contains(err.Error(), "still owns this router") {
		t.Fatalf("supervise must refuse with the legacy agent enabled, got: %v", err)
	}
	if !strings.Contains(err.Error(), "/etc/init.d/vectra-controller-pro start") {
		t.Errorf("the refusal must name the supported hand-over: %v", err)
	}
}

// -dry-run writes the spliced config and exits without starting xray, so it
// cannot create a second proxy stack and must not be blocked. It then fails on
// the missing config, which is how we know it got PAST the gate.
func TestSuperviseDryRunIsExemptFromTheLegacyGuard(t *testing.T) {
	realFakeInit(t)
	missing := filepath.Join(t.TempDir(), "no-such-config.json")

	err := cmdSupervise([]string{"-config", missing, "-provider", missing, "-dry-run"})
	if err == nil || strings.Contains(err.Error(), "still owns this router") {
		t.Fatalf("-dry-run must not be refused, got: %v", err)
	}
}

func TestSuperviseOverrideStartsAnyway(t *testing.T) {
	realFakeInit(t)
	missing := filepath.Join(t.TempDir(), "no-such-config.json")

	err := cmdSupervise([]string{"-config", missing, "-provider", missing, "-ignore-legacy-agent"})
	if err == nil || strings.Contains(err.Error(), "still owns this router") {
		t.Fatalf("-ignore-legacy-agent must override the refusal, got: %v", err)
	}
}

// The other direction, and the one that would strand a canary: after
// start_service has done the hand-over the legacy script is still INSTALLED,
// just stopped and disabled. vctl must start normally then.
func TestSuperviseRunsAfterTheHandOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectra-controller")
	// Disabled and stopped: every verb exits non-zero, as rc.common does.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := legacyInitScript
	legacyInitScript = path
	t.Cleanup(func() { legacyInitScript = old })

	missing := filepath.Join(t.TempDir(), "no-such-config.json")
	err := cmdSupervise([]string{"-config", missing, "-provider", missing})
	if err == nil || strings.Contains(err.Error(), "still owns this router") {
		t.Fatalf("a handed-over router must not be refused, got: %v", err)
	}
}
