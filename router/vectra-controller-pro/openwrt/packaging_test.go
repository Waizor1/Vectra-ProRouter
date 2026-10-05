// Package openwrt holds tests for the shipped OpenWrt packaging: the init
// script and the libexec helpers. They are plain shell, they run on the stop /
// removal path where the Go binary may be gone, and they are therefore exactly
// the code that is easiest to break unnoticed. These tests execute them for
// real against stub nft/ip/uci/jsonfilter binaries.
package openwrt

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/uci"
)

const (
	initScript      = "files/etc/init.d/vectra-controller-pro"
	deadmanScript   = "files/usr/libexec/vectra-controller-pro/deadman.sh"
	ntpHotplug      = "files/etc/hotplug.d/ntp/60-vectra-controller-pro"
	teardownScript  = "files/usr/libexec/vectra-controller-pro/dataplane-teardown.sh"
	renderScript    = "files/usr/libexec/vectra-controller-pro/render-xray-config.sh"
	defaultTeardown = "/usr/libexec/vectra-controller-pro/dataplane-teardown.sh"
)

func TestShippedShellParses(t *testing.T) {
	for _, s := range []string{initScript, teardownScript, renderScript, deadmanScript, ntpHotplug, defaultsScript} {
		out, err := exec.Command("sh", "-n", s).CombinedOutput()
		if err != nil {
			t.Errorf("sh -n %s: %v\n%s", s, err, out)
		}
	}
}

// ---- teardown harness ------------------------------------------------------

type teardownRun struct {
	t       *testing.T
	dir     string
	binDir  string
	logPath string
}

func writeExec(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// newTeardownRun builds a PATH of stubs that record their argv. ipExit picks
// whether `ip` reports success (so the delete loop keeps going) or failure (so
// it stops at the first miss).
func newTeardownRun(t *testing.T, ipExit int) *teardownRun {
	t.Helper()
	r := &teardownRun{t: t, dir: t.TempDir()}
	r.binDir = filepath.Join(r.dir, "bin")
	r.logPath = filepath.Join(r.dir, "cmds.log")
	if err := os.MkdirAll(r.binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	recorder := func(name string, exitCode string) string {
		return "#!/bin/sh\necho \"" + name + " $*\" >>\"$STUB_LOG\"\nexit " + exitCode + "\n"
	}
	writeExec(t, filepath.Join(r.binDir, "nft"), recorder("nft", "0"))
	writeExec(t, filepath.Join(r.binDir, "logger"), recorder("logger", "0"))
	writeExec(t, filepath.Join(r.binDir, "ip"), recorder("ip", itoa(ipExit)))

	// `uci -q get <key>` -> the value from STUB_UCI_<flattened key>, else fail
	// like a missing option does.
	writeExec(t, filepath.Join(r.binDir, "uci"), "#!/bin/sh\n"+
		"echo \"uci $*\" >>\"$STUB_LOG\"\n"+
		"[ -n \"${STUB_UCI_XRAY_CONFIG_PATH:-}\" ] || exit 1\n"+
		"printf '%s\\n' \"$STUB_UCI_XRAY_CONFIG_PATH\"\n")

	// OpenWrt's jsonfilter, for -i FILE -e '@.a.b.c' (jsonfilterStandIn): it
	// really evaluates the expression, so a wrong path in the script fails the
	// test rather than being papered over by a canned answer.
	if err := os.MkdirAll(filepath.Join(r.dir, "standin"), 0o755); err != nil {
		t.Fatal(err)
	}
	standIn(t, filepath.Join(r.dir, "standin"), "jsonfilter")
	writeExec(t, filepath.Join(r.binDir, "jsonfilter"), "#!/bin/sh\n"+
		"echo \"jsonfilter $*\" >>\"$STUB_LOG\"\n"+
		"exec '"+filepath.Join(r.dir, "standin", "jsonfilter")+"' \"$@\"\n")

	return r
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return "1"
}

// writeDesiredConfig writes an operator config with the given tproxy fwmark and
// points the uci stub at it.
func (r *teardownRun) writeDesiredConfig(body string) string {
	r.t.Helper()
	p := filepath.Join(r.dir, "xray-desired.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		r.t.Fatal(err)
	}
	return p
}

func (r *teardownRun) run(extraEnv ...string) []string {
	r.t.Helper()
	abs, err := filepath.Abs(teardownScript)
	if err != nil {
		r.t.Fatal(err)
	}
	cmd := exec.Command("sh", abs)
	cmd.Env = append(os.Environ(),
		"PATH="+r.binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_LOG="+r.logPath,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("teardown script failed: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(r.logPath)
	if err != nil {
		r.t.Fatalf("no commands recorded: %v", err)
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func countLine(lines []string, want string) int {
	n := 0
	for _, l := range lines {
		if l == want {
			n++
		}
	}
	return n
}

// ---- teardown behaviour ----------------------------------------------------

// The whole point: the nft table AND both families' policy route/rule pairs go.
// Leaving any of them loaded is what kills the router's own egress.
func TestTeardownRemovesTableAndPolicyRoute(t *testing.T) {
	r := newTeardownRun(t, 1) // `ip` fails: nothing left to delete
	cfg := r.writeDesiredConfig(`{"schema":1,"inbounds":{"tproxy":{"listenIP":"0.0.0.0","port":12345,"fwmark":1}}}`)

	lines := r.run("STUB_UCI_XRAY_CONFIG_PATH=" + cfg)

	for _, want := range []string{
		"nft flush table inet vctl",
		"nft delete table inet vctl",
		"ip rule del fwmark 0x1 lookup 100",
		"ip route del local 0.0.0.0/0 dev lo table 100",
		"ip -6 rule del fwmark 0x1 lookup 100",
		"ip -6 route del local ::/0 dev lo table 100",
	} {
		if !containsLine(lines, want) {
			t.Errorf("teardown did not run %q\ngot:\n  %s", want, strings.Join(lines, "\n  "))
		}
	}
}

// The fwmark is operator-settable (inbounds.tproxy.fwmark of the desired
// config). A hardcoded 0x1 would leave a non-default router's policy route in
// place — the exact "loaded data plane, no xray" state this exists to prevent.
func TestTeardownReadsTheOperatorFwmark(t *testing.T) {
	r := newTeardownRun(t, 1)
	cfg := r.writeDesiredConfig(`{"schema":1,"inbounds":{"tproxy":{"port":12345,"fwmark":99}}}`)

	lines := r.run("STUB_UCI_XRAY_CONFIG_PATH=" + cfg)

	if !containsLine(lines, "ip rule del fwmark 0x63 lookup 100") {
		t.Errorf("fwmark 99 must be torn down as 0x63\ngot:\n  %s", strings.Join(lines, "\n  "))
	}
	if containsLine(lines, "ip rule del fwmark 0x1 lookup 100") {
		t.Errorf("teardown used the default mark instead of the configured one\ngot:\n  %s", strings.Join(lines, "\n  "))
	}
	// And it must actually consult the config the uci option points at.
	if !containsLine(lines, "jsonfilter -i "+cfg+" -e @.inbounds.tproxy.fwmark") {
		t.Errorf("teardown did not read the fwmark from %s\ngot:\n  %s", cfg, strings.Join(lines, "\n  "))
	}
}

// No config, no uci, garbage fwmark: fall back to 1, the same default
// config.ApplyDefaults and firewallSpecFromConfig use. Tearing down SOMETHING
// beats tearing down nothing.
func TestTeardownFallsBackToTheDefaultFwmark(t *testing.T) {
	cases := map[string]func(*teardownRun) []string{
		"no uci value": func(r *teardownRun) []string { return r.run() },
		"config missing": func(r *teardownRun) []string {
			return r.run("STUB_UCI_XRAY_CONFIG_PATH=" + filepath.Join(r.dir, "absent.json"))
		},
		"fwmark absent": func(r *teardownRun) []string {
			return r.run("STUB_UCI_XRAY_CONFIG_PATH=" + r.writeDesiredConfig(`{"schema":1,"inbounds":{}}`))
		},
		"fwmark zero": func(r *teardownRun) []string {
			return r.run("STUB_UCI_XRAY_CONFIG_PATH=" + r.writeDesiredConfig(`{"inbounds":{"tproxy":{"fwmark":0}}}`))
		},
		"fwmark not a number": func(r *teardownRun) []string {
			return r.run("STUB_UCI_XRAY_CONFIG_PATH=" + r.writeDesiredConfig(`{"inbounds":{"tproxy":{"fwmark":"nope"}}}`))
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			lines := run(newTeardownRun(t, 1))
			if !containsLine(lines, "ip rule del fwmark 0x1 lookup 100") {
				t.Errorf("expected the default fwmark 0x1\ngot:\n  %s", strings.Join(lines, "\n  "))
			}
			if !containsLine(lines, "nft delete table inet vctl") {
				t.Error("the nft table must be removed regardless of the fwmark")
			}
		})
	}
}

// `vctl firewall routing` re-runs on every apply and its ip rule adds are
// best-effort, so duplicates accumulate. Delete until the kernel says no more —
// but bounded, so a misbehaving `ip` cannot hang the stop path forever.
func TestTeardownDrainsDuplicateRulesButIsBounded(t *testing.T) {
	r := newTeardownRun(t, 0) // `ip` always succeeds: an endless supply of rules
	cfg := r.writeDesiredConfig(`{"inbounds":{"tproxy":{"fwmark":1}}}`)

	lines := r.run("STUB_UCI_XRAY_CONFIG_PATH=" + cfg)

	for _, want := range []string{
		"ip rule del fwmark 0x1 lookup 100",
		"ip -6 rule del fwmark 0x1 lookup 100",
	} {
		if n := countLine(lines, want); n != 8 {
			t.Errorf("%q ran %d times, want the 8-iteration bound", want, n)
		}
	}
}

// ---- init script behaviour -------------------------------------------------

// sourceInit runs body with the init script's functions in scope. The script
// only assigns variables and defines functions at top level, so sourcing it is
// safe outside rc.common; the functions rc.common would call are invoked (and
// where needed redefined) by body.
func sourceInit(t *testing.T, body string, env ...string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "order.log")

	stub := filepath.Join(dir, "teardown-stub")
	writeExec(t, stub, "#!/bin/sh\necho teardown >>\""+log+"\"\n")

	// start_service validates its preconditions BEFORE it takes anything away
	// from the router, so without a renderer and a binary it returns early and
	// never reaches the hand-over. Both are stubbed here so the enabled=1 path
	// can be driven at all — and that ordering is itself the point: a packaging
	// accident must not leave a router with no controller and no proxy.
	renderer := filepath.Join(dir, "render-stub")
	writeExec(t, renderer, "#!/bin/sh\nexit 0\n")
	vctlBin := filepath.Join(dir, "vctl-stub")
	writeExec(t, vctlBin, "#!/bin/sh\nexit 0\n")

	abs, err := filepath.Abs(initScript)
	if err != nil {
		t.Fatal(err)
	}
	// `logger` is stubbed into the same ordered log, so a diagnostic the script
	// emits is a recorded EVENT rather than a line that vanishes into the host's
	// syslog. Shell functions win over PATH, so this catches every call site.
	script := ". " + abs + "\n" +
		"DATAPLANE_TEARDOWN='" + stub + "'\n" +
		"RENDERER='" + renderer + "'\n" +
		"VCTL_BIN='" + vctlBin + "'\n" +
		"AGENT_JSON='" + filepath.Join(dir, "agent.json") + "'\n" +
		"procd_open_instance() { :; }\nprocd_set_param() { :; }\nprocd_close_instance() { :; }\n" +
		"restore_legacy_controller() { echo restore >>'" + log + "'; }\n" +
		"stop_passwall_stack() { echo passwall-stop >>'" + log + "'; }\n" +
		"restore_passwall_stack() { echo passwall-restore >>'" + log + "'; }\n" +
		"vctl_alive() { return 1; }\n" +
		"logger() { shift 2; echo \"log:$*\" >>'" + log + "'; }\n" +
		// rc.common's stop, without procd: stop_service, then service_stopped.
		"stop() { stop_service; service_stopped; }\n" +
		body

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), env...)
	out, _ := cmd.CombinedOutput()

	raw, _ := os.ReadFile(log)
	return strings.TrimSpace(string(raw)), string(out)
}

// The teardown must run on every stop, and BEFORE the hand-back: the restored
// legacy agent sets no SO_MARK, so a still-loaded vctl output chain would mark
// its packets and the fwmark policy route would resolve them to `local ... dev
// lo` — the controller we just handed the router back to could not reach the
// panel. stop_service unloads; the hand-back is service_stopped's, which
// rc.common runs after procd_kill.
func TestStopServiceUnloadsTheDataPlaneBeforeHandingBack(t *testing.T) {
	if got, out := sourceInit(t, "stop_service\n"); got != "teardown" {
		t.Fatalf("stop_service = %q, want the teardown alone: the hand-back waits for service_stopped\n%s", got, out)
	}
	got, out := sourceInit(t, "stop\n")
	// PassWall before the agent: the agent's watchdog should find a stack that
	// is already up rather than race its own start against ours.
	if got != "teardown\npasswall-restore\nrestore" {
		t.Fatalf("stop order = %q, want teardown, then the PassWall stack, then the agent\n%s", got, out)
	}
}

// A reload deliberately skips the hand-back (two daemons must never check in
// with the same identity), but a data plane left loaded while vctl is stopped
// is never right, reload or not.
func TestStopServiceUnloadsTheDataPlaneOnReloadToo(t *testing.T) {
	got, out := sourceInit(t, "stop\n", "VCTL_RELOADING=1")
	if got != "teardown" {
		t.Fatalf("reload stop = %q, want the teardown alone (no hand-back)\n%s", got, out)
	}
}

// ---- H2: reload with enabled=0 --------------------------------------------

// sourceInitWithUCI is sourceInit plus stubs for the UCI helpers rc.common
// would normally provide, so start_service can be driven with a chosen
// `enabled` value.
func sourceInitWithUCI(t *testing.T, enabled string, body string, env ...string) (string, string) {
	t.Helper()
	return sourceInit(t,
		"config_load() { :; }\n"+
			"config_get_bool() { eval \"$1="+enabled+"\"; }\n"+
			body, env...)
}

// A disabled vctl must hand the router back rather than return early. Otherwise
// the reload path (stop skips the hand-back by design, start bails before
// re-taking ownership) leaves vctl stopped, the legacy agent disabled and the
// marker in place: no controller at all.
//
// It must also SAY it declined. `enable` and `uci ... enabled` are two separate
// switches and only the second one gates the daemon, so a `start` that quietly
// exits 0 having started nothing is the most expensive kind of silence here.
func TestStartServiceHandsBackWhenDisabled(t *testing.T) {
	got, out := sourceInitWithUCI(t, "0", "start_service\n")
	lines := strings.Split(got, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "log:") ||
		lines[1] != "passwall-restore" || lines[2] != "restore" {
		t.Fatalf("start with enabled=0 logged %q, want a diagnostic then BOTH stacks handed back\n%s", got, out)
	}
	// Name the option an operator has to change, not just "disabled".
	for _, want := range []string{"enabled=0", "init.d enable"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the declined-start message does not mention %q: %s", want, lines[0])
		}
	}
}

// The full reload sequence with enabled=0: stop unloads the data plane and
// skips the hand-back, then start performs it. The router must not come out of
// this with nothing managing it.
func TestReloadWithDisabledConfigStillLeavesAController(t *testing.T) {
	got, out := sourceInitWithUCI(t, "0",
		// rc.common's start, reduced to what reload_service uses (its stop is
		// sourceInit's).
		"start() { start_service; }\n"+
			"reload_service\n")
	events := []string{}
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "log:") {
			events = append(events, line)
		}
	}
	// The stop half skips the hand-back (VCTL_RELOADING), the start half performs
	// it because the config says vctl does not own this router.
	if strings.Join(events, "\n") != "teardown\npasswall-restore\nrestore" {
		t.Fatalf("reload with enabled=0 logged %q, want the data plane unloaded and BOTH stacks handed back\n%s", got, out)
	}
}

// And the enabled path must NOT hand back — that would start the legacy agent
// alongside vctl, two daemons checking in with the same identity. What it MUST
// do is take the other proxy stack down.
//
// Disabling the legacy agent was never enough for that. PassWall2 is an
// independent service with its own rc.d symlink: it starts at boot without the
// agent, runs its own xray and installs its own nftables rules and fwmark
// policy route. Stopping the agent stops what RECONFIGURES PassWall, not
// PassWall — so the supported hand-over left two proxy stacks on the router,
// which is the state the first canary was rolled back from.
func TestStartServiceTakesTheOtherProxyStackDown(t *testing.T) {
	got, out := sourceInitWithUCI(t, "1", "start_service\n")
	if got != "passwall-stop" {
		t.Fatalf("start with enabled=1 logged %q, want the PassWall stack stopped and no hand-back\n%s", got, out)
	}
}

// The init script must invoke the path the Makefile actually installs.
func TestInitScriptPointsAtTheInstalledTeardownPath(t *testing.T) {
	raw, err := os.ReadFile(initScript)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), defaultTeardown) {
		t.Errorf("init script does not reference %s", defaultTeardown)
	}
	mk, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mk), "dataplane-teardown.sh $(1)"+defaultTeardown) {
		t.Errorf("Makefile does not install the teardown helper to %s", defaultTeardown)
	}
}

// ---- the OTHER proxy stack -------------------------------------------------

// sourcePasswall runs body with the init script sourced, PASSWALL_MARKER
// pointed at a temp path and passwall_init pointed at a recording stand-in for
// /etc/init.d/passwall2. The real stop_passwall_stack / restore_passwall_stack
// bodies run, so the marker discipline and the enable/start/disable/stop calls
// are exercised rather than stubbed.
//
// pwEnabledExit picks what `<init> enabled` reports: 0 = the service was
// enabled (so WE are the ones disabling it and a restore is owed), 1 = the
// operator had already disabled it.
func sourcePasswall(t *testing.T, body string, pwEnabledExit int, markerPresent bool) (argv []string, marker bool, out string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "pw.log")
	marker0 := filepath.Join(dir, ".passwall-disabled-by-vctl")
	pw := filepath.Join(dir, "passwall2")

	writeExec(t, pw, "#!/bin/sh\n"+
		"echo \"$1\" >>\""+log+"\"\n"+
		"[ \"$1\" = enabled ] && exit "+itoa(pwEnabledExit)+"\n"+
		"exit 0\n")
	if markerPresent {
		writeExec(t, marker0, "")
	}

	abs, err := filepath.Abs(initScript)
	if err != nil {
		t.Fatal(err)
	}
	// PassWall's own switch is not this harness's subject (see stack): no
	// config for it here, and nothing of the host's.
	script := ". " + abs + "\n" +
		"LEGACY_MARKER_DIR='" + dir + "'\n" +
		"PASSWALL_MARKER='" + marker0 + "'\n" +
		"PASSWALL_SWITCH_MARKER='" + filepath.Join(dir, ".passwall-switch-off-by-vctl") + "'\n" +
		"PASSWALL_SWITCH_SNIPPET='" + filepath.Join(dir, "uci-defaults", "99-vectra-trial-passwall-switch") + "'\n" +
		"TRIAL_FILE='" + filepath.Join(dir, "vectra-trial.json") + "'\n" +
		"TRIAL_MARKER_DIR='" + filepath.Join(dir, "trial.d") + "'\n" +
		"passwall_init() { echo '" + pw + "'; }\n" +
		"uci() { return 1; }\n" +
		"logger() { :; }\n" +
		body

	cmd := exec.Command("sh", "-c", script)
	cmd.Env = os.Environ()
	raw, _ := cmd.CombinedOutput()

	recorded, _ := os.ReadFile(log)
	for _, l := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
		if l != "" {
			argv = append(argv, l)
		}
	}
	_, err = os.Stat(marker0)
	return argv, err == nil, string(raw)
}

// Taking the stack down: disable AND stop, and leave a breadcrumb so the
// hand-back knows to bring it back.
func TestStopPassWallStackDisablesStopsAndMarks(t *testing.T) {
	argv, marker, out := sourcePasswall(t, "stop_passwall_stack\n", 0, false)

	for _, want := range []string{"disable", "stop"} {
		if !containsLine(argv, want) {
			t.Errorf("stop_passwall_stack did not run %q: %v\n%s", want, argv, out)
		}
	}
	if !marker {
		t.Error("no marker written, so the hand-back would leave PassWall down forever")
	}
}

// A stack the OPERATOR had already disabled must still be stopped — vctl is
// taking the router either way — but must NOT be marked, or a later stop would
// switch back on something a human deliberately switched off. Same discipline
// as the legacy agent.
func TestStopPassWallStackDoesNotClaimAnOperatorDisabledStack(t *testing.T) {
	argv, marker, out := sourcePasswall(t, "stop_passwall_stack\n", 1, false)

	if !containsLine(argv, "stop") {
		t.Errorf("an operator-disabled stack must still be stopped: %v\n%s", argv, out)
	}
	if marker {
		t.Error("marked a stack the operator had already disabled; a later hand-back would resurrect it")
	}
}

// The hand-back, both directions.
func TestRestorePassWallStackIsMarkerGuarded(t *testing.T) {
	argv, marker, out := sourcePasswall(t, "restore_passwall_stack\n", 0, true)
	for _, want := range []string{"enable", "start"} {
		if !containsLine(argv, want) {
			t.Errorf("restore_passwall_stack did not run %q: %v\n%s", want, argv, out)
		}
	}
	if marker {
		t.Error("the marker survived the restore, so a second stop would restore again")
	}

	argv, _, out = sourcePasswall(t, "restore_passwall_stack\n", 0, false)
	if len(argv) != 0 {
		t.Errorf("restore_passwall_stack touched a stack it never disabled: %v\n%s", argv, out)
	}
}

// The package name has not been stable — luci-app-passwall2 installs
// /etc/init.d/passwall2, older builds shipped /etc/init.d/passwall. Guessing one
// spelling and silently doing nothing on the other is how a mutual exclusion
// becomes a no-op on half a fleet.
//
// BEHAVIOURAL, because the obvious version was worthless. Asserting that the
// source text contains both spellings passes with the second candidate deleted:
// "/etc/init.d/passwall" is a PREFIX of "/etc/init.d/passwall2", so the second
// substring check is satisfied by the first path. Verified by mutation — with
// the list reduced to passwall2 alone, that test still reported PASS.
//
// So this drives the REAL passwall_init against a directory holding exactly one
// spelling at a time, and requires it found.
func TestPassWallInitFindsBothSpellings(t *testing.T) {
	abs, err := filepath.Abs(initScript)
	if err != nil {
		t.Fatal(err)
	}

	for _, spelling := range []string{"passwall2", "passwall"} {
		t.Run(spelling, func(t *testing.T) {
			dir := t.TempDir()
			// ONLY this spelling exists, and it is executable.
			only := filepath.Join(dir, spelling)
			writeExec(t, only, "#!/bin/sh\nexit 0\n")

			script := ". " + abs + "\n" +
				"PASSWALL_INIT_CANDIDATES='" + filepath.Join(dir, "passwall2") +
				" " + filepath.Join(dir, "passwall") + "'\n" +
				"passwall_init\n"
			out, err := exec.Command("sh", "-c", script).CombinedOutput()
			if err != nil {
				t.Fatalf("passwall_init did not find %s: %v\n%s", spelling, err, out)
			}
			if got := strings.TrimSpace(string(out)); got != only {
				t.Errorf("passwall_init = %q, want %q", got, only)
			}
		})
	}

	// AND the SHIPPED default list really carries both. The subtests above
	// override PASSWALL_INIT_CANDIDATES, so on their own they prove the loop
	// works over whatever list it is handed and say nothing about the list the
	// router gets — the same vacuity, one level up. Verified by mutation: with
	// the default reduced to passwall2 alone, they still passed.
	//
	// Compared as whole words, because "/etc/init.d/passwall" is a prefix of
	// "/etc/init.d/passwall2" and a substring check cannot tell them apart.
	t.Run("shipped default", func(t *testing.T) {
		out, err := exec.Command("sh", "-c",
			". "+abs+"\nprintf '%s\\n' $PASSWALL_INIT_CANDIDATES\n").CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		got := map[string]bool{}
		for _, l := range strings.Fields(string(out)) {
			got[l] = true
		}
		for _, want := range []string{"/etc/init.d/passwall2", "/etc/init.d/passwall"} {
			if !got[want] {
				t.Errorf("the shipped candidate list does not include %q: %v", want, got)
			}
		}
	})

	// A non-executable file is not an init script — the same `[ -x ]` rule the
	// legacy-agent block uses, so the two definitions of "installed" match.
	t.Run("not executable", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "passwall2"), []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		script := ". " + abs + "\n" +
			"PASSWALL_INIT_CANDIDATES='" + filepath.Join(dir, "passwall2") + "'\n" +
			"passwall_init && echo FOUND\n"
		out, _ := exec.Command("sh", "-c", script).CombinedOutput()
		if strings.Contains(string(out), "FOUND") {
			t.Errorf("a non-executable file was treated as an init script:\n%s", out)
		}
	})

	// And with neither present it must be a clean no-op, not an error that
	// aborts start_service on a router that never had PassWall.
	t.Run("no passwall at all", func(t *testing.T) {
		dir := t.TempDir()
		script := ". " + abs + "\nlogger() { :; }\n" +
			"PASSWALL_MARKER='" + filepath.Join(dir, "absent") + "'\n" +
			"PASSWALL_INIT_CANDIDATES='" + filepath.Join(dir, "nothing-here") + "'\n" +
			"stop_passwall_stack && echo STOP_OK\n" +
			"restore_passwall_stack && echo RESTORE_OK\n"
		out, err := exec.Command("sh", "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("no-PassWall router: %v\n%s", err, out)
		}
		for _, want := range []string{"STOP_OK", "RESTORE_OK"} {
			if !strings.Contains(string(out), want) {
				t.Errorf("a router without PassWall must be a clean no-op, got:\n%s", out)
			}
		}
	})
}

// Preconditions before destruction. The hand-over is destructive — it stops the
// legacy agent AND the PassWall stack — so a `start` that is going to fail
// anyway must fail BEFORE it takes either away.
//
// With the checks in the other order, a packaging accident (renderer missing
// from the payload, binary not installed, render erroring on bad UCI) left the
// router with no controller and no proxy, from a command that printed nothing.
// Ordered this way the worst case is a failed start on a router that still
// works.
func TestStartServiceValidatesBeforeItTakesAnythingAway(t *testing.T) {
	got, out := sourceInitWithUCI(t, "1",
		// The renderer the payload should have installed is missing.
		"RENDERER=/nonexistent/render-xray-config.sh\n"+
			"start_service; echo \"rc=$?\"\n")

	if strings.Contains(got, "passwall-stop") {
		t.Errorf("the PassWall stack was stopped by a start that could not succeed: %q\n%s", got, out)
	}
	if !strings.Contains(out, "rc=1") {
		t.Errorf("start_service must fail when the renderer is missing, got:\n%s", out)
	}
}

// The other two preconditions the comment names, and the direction that matters
// most: a start that cannot succeed must HAND THE ROUTER BACK, not just decline.
//
// The disabled state is persistent — after one successful hand-over neither
// service has an rc.d symlink and both markers live in /etc (kept across
// sysupgrade). From the second boot onward a bare `return 1` would leave a
// router with no vctl, no PassWall and no legacy agent, and stop_service never
// runs at boot, so nothing would undo it.
func TestStartServiceHandsBackWhenItCannotStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"renderer missing", "RENDERER=/nonexistent/render.sh\n"},
		{"controller binary missing", "VCTL_BIN=/nonexistent/vctl\n"},
		{"render fails", "RENDERER=$(mktemp)\nprintf '#!/bin/sh\\nexit 3\\n' >\"$RENDERER\"\nchmod +x \"$RENDERER\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, out := sourceInitWithUCI(t, "1", tc.body+"start_service; echo \"rc=$?\"\n")

			if strings.Contains(got, "passwall-stop") {
				t.Errorf("the PassWall stack was stopped by a start that could not succeed: %q\n%s", got, out)
			}
			if !strings.Contains(out, "rc=1") {
				t.Errorf("start_service must fail, got:\n%s", out)
			}
			// Both restores are marker-guarded, so calling them is the correct
			// behaviour whether or not this particular router is owed one.
			for _, want := range []string{"passwall-restore", "restore"} {
				if !strings.Contains(got, want) {
					t.Errorf("a failed start did not hand the router back (%q missing): %q\n%s", want, got, out)
				}
			}
		})
	}
}

// ---- the whole hand-over, against stand-ins -------------------------------

// stack runs the init script's REAL functions — start_service, stop_service,
// service_stopped and the hand-over — against recording stand-ins for the
// legacy agent, PassWall, uci, ubus and pgrep, in the shell a router has
// (testShell). Only rc.common's own pieces (procd_*, config_*, and its stop:
// stop_service, procd_kill, service_stopped) and the time (sleep) are
// functions here.
//
// The router is as the fleet has it: PassWall and the legacy agent enabled
// and running, PassWall's own switch on in a real /etc/config/passwall2
// (config/passwall2 here), vctl's UCI switch on (config/vectra-controller-pro)
// — files the uci stand-in reads and commits. The PassWall stand-in runs its
// stack only while its switch is on, the way app.sh's start runs no proxy
// with it off. State lives in files: pw.running, pw.enabled, agent.running,
// agent.enabled, vctl.running (procd runs vctl), vctl.enabled (its boot
// links), vctl.linger (how many more looks find vctl's process after procd
// let go of it).
type stack struct {
	t   *testing.T
	dir string
	env []string
	// procd: the PassWall stand-in is a procd service, which answers
	// `running` itself; otherwise it is shaped like luci-app-passwall2's —
	// rc.common answers `running` with its usage and exit status 0.
	procd bool
	// pgrep: whether the router has pgrep at all.
	pgrep bool
}

func newStack(t *testing.T) *stack {
	s := &stack{t: t, dir: t.TempDir(), pgrep: true}
	s.setSwitch("1")
	s.write("config/vectra-controller-pro", "\nconfig controller 'main'\n\toption enabled '1'\n\n")
	for _, f := range []string{"pw.running", "pw.enabled", "agent.running", "agent.enabled"} {
		s.write(f, "")
	}
	return s
}

func (s *stack) path(name string) string { return filepath.Join(s.dir, name) }

func (s *stack) write(name, body string) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.path(name)), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(s.path(name), []byte(body), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// setSwitch writes PassWall's config with its own switch as given.
func (s *stack) setSwitch(v string) {
	s.write("config/passwall2", "\nconfig global\n\toption enabled '"+v+"'\n\toption node 'myshunt'\n\toption localhost_proxy '1'\n"+
		"\nconfig global_delay\n\toption start_daemon '1'\n\n")
}

// pwSwitch is PassWall's own switch as /etc/config/passwall2 has it: what a
// commit wrote, not what uci merely holds staged.
func (s *stack) pwSwitch() string { return s.option("passwall2", "global", "enabled") }

// option is an option of the first section of a type, as its file has it.
func (s *stack) option(config, typ, name string) string {
	s.t.Helper()
	f, err := uci.Load(s.path("config/" + config))
	if err != nil {
		s.t.Fatal(err)
	}
	secs := f.OfType(typ)
	if len(secs) == 0 {
		s.t.Fatalf("no %s section in %s", typ, config)
	}
	return secs[0].Get(name)
}

// testShell is the shell the init script runs under here: dash where there
// is one — the router's busybox ash keeps its rules, a failed redirection on
// the special builtin `:` ending the whole script among them — else /bin/sh,
// which in an OpenWrt rootfs is that very busybox ash.
func testShell() string {
	if p, err := exec.LookPath("dash"); err == nil {
		return p
	}
	return "/bin/sh"
}

// prologue writes the stand-ins and returns the init script, sourced, with
// its paths pointed at them. enabled is vctl's UCI switch as config_get_bool
// reads it; "" reads it from config/vectra-controller-pro, as a router does.
func (s *stack) prologue(enabled string) string {
	s.t.Helper()
	bin := s.path("bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.MkdirAll(s.path("lock"), 0o755); err != nil {
		s.t.Fatal(err)
	}
	log := s.path("events.log")
	rec := func(who string) string { return "echo \"" + who + " $*\" >>\"" + log + "\"\n" }
	q := func(name string) string { return "'" + s.path(name) + "'" }

	pw := "#!/bin/sh\n"
	if s.procd {
		pw += "USE_PROCD=1\n"
	}
	stack := "rm -f " + q("pw.running") + "; [ \"$(uci -q get passwall2.@global[0].enabled)\" = 1 ] && true >" + q("pw.running") + "\n"
	pw += rec("passwall") +
		"case \"$1\" in\n" +
		"  enabled) [ -n \"${PW_ENABLED:-}\" ] && exit \"$PW_ENABLED\"; [ -f " + q("pw.enabled") + " ];;\n" +
		"  enable) true >" + q("pw.enabled") + ";;\n" +
		"  disable) rm -f " + q("pw.enabled") + ";;\n"
	if s.procd {
		pw += "  running) exit ${PW_RUNNING:-1};;\n"
	} else {
		pw += "  running) echo 'Syntax: /etc/init.d/passwall2 [command]'; exit 0;;\n"
	}
	// PW_SLOW: a restart in flight, as subscribe.lua's at midnight — under its
	// init's lock, its stop first, its config read, and the stack back two
	// seconds later, as that config said.
	pw += "  restart) [ -n \"${PW_SLOW:-}\" ] && exec flock " + q("lock/passwall2.lock") + " sh -c '" +
		"rm -f " + s.path("pw.running") + "; on=$(uci -q get passwall2.@global[0].enabled); true >" + s.path("restart.began") +
		"; sleep 2; [ \"$on\" = 1 ] && true >" + s.path("pw.running") + "; rm -f " + s.path("restart.began") + "'\n" +
		"    " + stack + "    ;;\n" +
		"  start) " + stack + "    ;;\n" +
		"  stop) [ -f " + q("lock/passwall2.lock") + " ] && ! flock -n " + q("lock/passwall2.lock") + " true && " + rec("passwall-stop-found-its-lock-held") +
		"    rm -f " + q("pw.running") + ";;\n" +
		"esac\nexit 0\n"
	writeExec(s.t, s.path("passwall2"), pw)
	writeExec(s.t, s.path("vectra-controller"), "#!/bin/sh\n"+rec("agent")+
		"case \"$1\" in\n"+
		"  enabled) [ -n \"${AGENT_ENABLED:-}\" ] && exit \"$AGENT_ENABLED\"; [ -f "+q("agent.enabled")+" ];;\n"+
		"  enable) true >"+q("agent.enabled")+";;\n"+
		"  disable) rm -f "+q("agent.enabled")+";;\n"+
		"  start) true >"+q("agent.running")+";;\n"+
		"  stop) rm -f "+q("agent.running")+";;\n"+
		"esac\nexit 0\n")
	writeExec(s.t, s.path("teardown"), "#!/bin/sh\n"+rec("teardown")+"rm -f "+q("nft.vctl")+"\n")
	writeExec(s.t, s.path("render"), "#!/bin/sh\nexit 0\n")
	writeExec(s.t, s.path("vctl"), "#!/bin/sh\nexit 0\n")
	standIn(s.t, bin, "uci")
	linkTool(s.t, bin, "flock")
	writeExec(s.t, filepath.Join(bin, "ubus"), "#!/bin/sh\n[ -z \"${UBUS_DOWN:-}\" ]\n")
	// vctl's table, loaded while nft.vctl is there. Asked tersely (-t): the
	// table's direct set is not to be printed every minute.
	writeExec(s.t, filepath.Join(bin, "nft"), "#!/bin/sh\n[ \"$*\" = '-t list table inet vctl' ] && [ -f "+q("nft.vctl")+" ]\n")
	writeExec(s.t, filepath.Join(bin, "sysctl"), "#!/bin/sh\n"+rec("sysctl"))
	_ = os.Remove(filepath.Join(bin, "pgrep"))
	if s.pgrep {
		// PassWall's processes by what the stand-in runs (PGREP overrides);
		// vctl's by vctl.linger: alive for that many more looks.
		writeExec(s.t, filepath.Join(bin, "pgrep"), "#!/bin/sh\n"+rec("pgrep")+
			"case \"$*\" in\n"+
			// pw.looks: what the next looks say, one answer each (0 runs).
			"*/tmp/etc/*) if [ -s "+q("pw.looks")+" ]; then set -- $(cat "+q("pw.looks")+"); a=$1; shift; echo \"$*\" >"+q("pw.looks")+"; exit \"$a\"; fi\n"+
			"  [ -n \"${PGREP:-}\" ] && exit \"$PGREP\"; [ -f "+q("pw.running")+" ];;\n"+
			// vctl's daemon: there while procd runs it, unless it vanished.
			"*'/vctl agent'*) [ -f "+q("vctl.running")+" ] && [ ! -f "+q("vctl.vanished")+" ];;\n"+
			// An xray a killed vctl left behind (its parent init): xray.orphan.
			"'-P 1 '*) [ -f "+q("xray.orphan")+" ] && echo 4242;;\n"+
			"*) n=$(cat "+q("vctl.linger")+" 2>/dev/null); [ -n \"$n\" ] && [ \"$n\" -gt 0 ] || exit 1; echo $((n - 1)) >"+q("vctl.linger")+";;\n"+
			"esac\n")
	}
	// The tools the hand-over takes from PATH, and nothing else: whether
	// pgrep is there is the stand-in's to decide. (flock runs its program
	// from PATH: `true`, the stand-in's `sh`.)
	for _, tool := range []string{"grep", "mkdir", "rm", "mv", "dirname", "cat", "date", "sleep", "true", "sh", "awk", "ls", "wc"} {
		p, err := exec.LookPath(tool)
		if err != nil {
			s.t.Fatal(err)
		}
		_ = os.Remove(filepath.Join(bin, tool))
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			s.t.Fatal(err)
		}
	}

	abs, err := filepath.Abs(initScript)
	if err != nil {
		s.t.Fatal(err)
	}
	getBool := "config_get_bool() { eval \"$1=" + enabled + "\"; }\n"
	if enabled == "" {
		getBool = "config_get_bool() { case \"$(uci -q get vectra-controller-pro.main.enabled)\" in 0|off|false|no|disabled) eval \"$1=0\";; *) eval \"$1=1\";; esac; }\n"
	}
	// rc.common's own shutdown — its `stop` — comes before the init script,
	// which may define its own.
	return "shutdown() { stop \"$@\"; }\n" +
		". " + abs + "\n" +
		"LEGACY_INIT=" + q("vectra-controller") + "\n" +
		"LEGACY_MARKER_DIR='" + s.dir + "'\n" +
		"LEGACY_MARKER=" + q(".legacy-agent-disabled-by-vctl") + "\n" +
		"PASSWALL_MARKER=" + q(".passwall-disabled-by-vctl") + "\n" +
		"PASSWALL_SWITCH_MARKER=" + q(".passwall-switch-off-by-vctl") + "\n" +
		"PASSWALL_SWITCH_SNIPPET=" + q("uci-defaults/99-vectra-trial-passwall-switch") + "\n" +
		"PASSWALL_RETIRED=" + q(".passwall-retired-by-vctl") + "\n" +
		"PASSWALL_RETIRE_CLOCK=" + q(".passwall-retire-clock") + "\n" +
		"PASSWALL_INIT_CANDIDATES=" + q("passwall2") + "\n" +
		"PASSWALL_LOCK_DIR=" + q("lock") + "\n" +
		"TRIAL_FILE=" + q("vectra-trial.json") + "\n" +
		"TRIAL_MARKER_DIR=" + q("trial.d") + "\n" +
		"AGENT_WATCHDOG_STAMP=" + q("watchdog.last-restart") + "\n" +
		"DATAPLANE_TEARDOWN=" + q("teardown") + "\n" +
		"RENDERER=" + q("render") + "\n" +
		"VCTL_BIN=" + q("vctl") + "\n" +
		"AGENT_JSON=" + q("run/agent.json") + "\n" +
		"MEMCONF=" + q("sysctl.d/99-vectra-pro-memory.conf") + "\n" +
		"LOWMEMCONF=" + q("sysctl.d/99-vectra-lowmem.conf") + "\n" +
		"MEMINFO=" + q("meminfo") + "\n" +
		"config_load() { :; }\n" +
		getBool +
		// procd runs vctl, which loads its data plane — unless VCTL_CRASHES: it
		// dies at once, every time.
		"procd_open_instance() { echo instance >>'" + log + "'; [ -n \"${VCTL_CRASHES:-}\" ] || { true >" + q("vctl.running") + "; true >" + q("nft.vctl") + "; }; }\n" +
		"procd_set_param() { :; }\nprocd_close_instance() { :; }\n" +
		// rc.common's stop: procd_kill lets go of vctl before its process is
		// gone (vctl.linger), and service_stopped comes last.
		"stop() { stop_service \"$@\"; rm -f " + q("vctl.running") + "; echo procd_kill >>'" + log + "'; " +
		"if type service_stopped >/dev/null 2>&1; then service_stopped; fi; }\n" +
		"start() { start_service \"$@\"; }\n" +
		"logger() { while [ $# -gt 1 ]; do case \"$1\" in -t|-p) shift 2;; *) break;; esac; done; echo \"log:$*\" >>'" + log + "'; }\n" +
		"sleep() { :; }\n"
}

// run runs body with the init script sourced; enabled is the UCI switch.
// PW_ENABLED / AGENT_ENABLED (0 = enabled) override what the stand-ins
// answer to `enabled`, PW_RUNNING (procd: 0 = running) and PGREP (0 =
// something runs, 1 = nothing) what they say about PassWall running.
func (s *stack) run(enabled, body string, env ...string) (events []string, out string) {
	s.t.Helper()
	return s.exec([]string{"-c", s.prologue(enabled) + body}, env...)
}

// exec runs the test shell with args in the stack's world, and returns what
// the stand-ins recorded — uci only for what it changes.
func (s *stack) exec(args []string, env ...string) (events []string, out string) {
	s.t.Helper()
	log := s.path("events.log")
	cmd := exec.Command(testShell(), args...)
	cmd.Env = append([]string{"PATH=" + s.path("bin"), "STUB_LOG=" + log,
		"UCI_CONFIG_DIR=" + s.path("config"), "UCI_SAVE_DIR=" + s.path("uci-save")}, append(s.env, env...)...)
	raw, _ := cmd.CombinedOutput()
	recorded, _ := os.ReadFile(log)
	_ = os.Remove(log)
	for _, l := range strings.Split(strings.TrimSpace(string(recorded)), "\n") {
		if l != "" {
			events = append(events, l)
		}
	}
	return events, string(raw)
}

// initScriptAt writes /etc/init.d/vectra-controller-pro as rc.common runs
// it, for what calls it by path — the prerm, the dead-man: this init
// script's functions behind rc.common's verbs, its UCI switch read from
// config/vectra-controller-pro.
func (s *stack) initScriptAt() string {
	s.t.Helper()
	init := s.path("init")
	log := s.path("events.log")
	writeExec(s.t, init, "#!"+testShell()+"\n"+s.prologue("")+
		"case \"${1:-}\" in\n"+
		"start) start ;;\n"+
		"stop) stop ;;\n"+
		"running) [ -f "+"'"+s.path("vctl.running")+"'"+" ] ;;\n"+
		"enable) echo 'vctl enable' >>'"+log+"'; true >'"+s.path("vctl.enabled")+"' ;;\n"+
		"disable) echo 'vctl disable' >>'"+log+"'; rm -f '"+s.path("vctl.enabled")+"' ;;\n"+
		"enabled) [ -f '"+s.path("vctl.enabled")+"' ] ;;\n"+
		"esac\n")
	return init
}

// prerm runs the package's prerm (openwrt/Makefile) with its
// /etc/init.d/vectra-controller-pro standing for this init script, with the
// arguments and environment opkg gives it.
func (s *stack) prerm(args []string, env ...string) (events []string, out string) {
	s.t.Helper()
	init := s.initScriptAt()
	raw, err := os.ReadFile(makefileScript(s.t, "prerm"))
	if err != nil {
		s.t.Fatal(err)
	}
	prerm := s.path("prerm")
	writeExec(s.t, prerm, strings.ReplaceAll(string(raw), "/etc/init.d/vectra-controller-pro", init))
	return s.exec(append([]string{prerm}, args...), env...)
}

// deadman runs the dead-man (deadman.sh) as cron does, once, against this
// stack.
func (s *stack) deadman(env ...string) (events []string, out string) {
	s.t.Helper()
	return s.exec([]string{mustAbs(s.t, deadmanScript)}, append(s.deadmanEnv(), env...)...)
}

// deadmanEnv points the dead-man at this stack: its init script (as rc.common
// runs it), the power lock, procd's lock for the service, its state, vctl's
// render and operator config.
func (s *stack) deadmanEnv() []string {
	return []string{"VCTL_INIT=" + s.initScriptAt(), "POWER_LOCK=" + s.path("lock/vectra-power.lock"),
		"PROCD_LOCK=" + s.path("lock/procd_vectra-controller-pro.lock"), "STATE=" + s.path("deadman.state"),
		"XRAY_RENDER=" + s.path("run/xray.json"), "OPERATOR_CONFIG=" + s.path("etc/xray-desired.json")}
}

// reboot is a power cycle up to the services: what ran and what tmpfs held
// are gone, and the boot's uci_apply_defaults (/etc/init.d/boot, S10) sources
// each file in /etc/uci-defaults in a subshell, deletes those that exit 0,
// then commits. What starts after it (vctl S95, PassWall S99) is the test's.
func (s *stack) reboot() (events []string, out string) {
	s.t.Helper()
	for _, p := range []string{"vectra-trial.json", "trial.d", "watchdog.last-restart", "uci-save", "pw.running",
		"agent.running", "vctl.running", "vctl.linger", "vctl.vanished", "nft.vctl", "deadman.state"} {
		if err := os.RemoveAll(s.path(p)); err != nil {
			s.t.Fatal(err)
		}
	}
	return s.exec([]string{"-c", "if cd '" + s.path("uci-defaults") + "' 2>/dev/null; then\n" +
		"  for file in ./*; do [ -f \"$file\" ] || continue; ( . \"$file\" ) && rm -f \"$file\"; done\n" +
		"  uci commit\n" +
		"fi\n"})
}

func (s *stack) exists(name string) bool {
	_, err := os.Stat(s.path(name))
	return err == nil
}

func (s *stack) isFile(name string) bool {
	st, err := os.Stat(s.path(name))
	return err == nil && st.Mode().IsRegular()
}

func withoutLogs(events []string) []string {
	var out []string
	for _, e := range events {
		if !strings.HasPrefix(e, "log:") {
			out = append(out, e)
		}
	}
	return out
}

// THE regression: luci-app-passwall2's init script is not a procd one, and
// rc.common answers `running` for it with its usage and exit status 0. Asked
// that, the takeover saw a stack that never stopped and handed every such
// router back after ten seconds. It is judged the way its own app.sh judges
// itself: by what runs from /tmp/etc/passwall2/bin.
func TestTheTakeoverTakesARouterWithLuciAppPasswall2(t *testing.T) {
	s := newStack(t)
	events, out := s.run("1", "start_service; echo \"rc=$?\"\n")
	if !strings.Contains(out, "rc=0") {
		t.Fatalf("start_service did not take the router:\n%s\n%s", strings.Join(events, "\n"), out)
	}
	// Down three times in a row before vctl runs.
	want := []string{
		"pgrep -P 1 -f run -c " + s.path("run") + "/|(^|/)(vctl-xray-wrapper|vctl-xray-private) run -c stdin:($| )",
		"agent enabled", "agent disable", "agent stop",
		"uci -q set passwall2.@global[0].enabled=0", "uci -q commit passwall2",
		"passwall enabled", "passwall disable", "passwall stop",
		"pgrep -f /tmp/etc/passwall2/bin", "pgrep -f /tmp/etc/passwall2/bin", "pgrep -f /tmp/etc/passwall2/bin",
		"instance",
	}
	if got := withoutLogs(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if !s.exists(".passwall-disabled-by-vctl") || !s.exists(".legacy-agent-disabled-by-vctl") {
		t.Fatal("the takeover left no breadcrumbs")
	}
	// The agent package's cron watchdog would have the agent — and with it
	// PassWall — back within 5 minutes: held off while vctl holds the router,
	// ten minutes at a time (deadman.sh holds it off again).
	s.heldFor(600)
}

// heldFor: the takeover's hold on the legacy agent's watchdog, that many
// seconds ahead of now (give or take the seconds the test took).
func (s *stack) heldFor(secs int64) {
	s.t.Helper()
	b, err := os.ReadFile(s.path("watchdog.last-restart"))
	if err != nil {
		s.t.Fatalf("the agent's watchdog is not held off: %v", err)
	}
	stamp, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if ahead := stamp - time.Now().Unix(); err != nil || ahead < secs-30 || ahead > secs {
		s.t.Fatalf("the hold is %q, want %d s ahead", b, secs)
	}
}

// Still running after its stop: the router is handed back, not shared.
func TestAPassWallThatKeepsRunningIsNotTakenOver(t *testing.T) {
	for _, tc := range []struct {
		name  string
		procd bool
		env   string
	}{
		{"luci-app-passwall2, something runs from its bin", false, "PGREP=0"},
		{"a procd build answering running", true, "PW_RUNNING=0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t)
			s.procd = tc.procd
			events, out := s.run("1", "start_service; echo \"rc=$?\"\n", tc.env)
			all := strings.Join(events, "\n")
			if !strings.Contains(out, "rc=1") || strings.Contains(all, "instance") {
				t.Fatalf("a stack that kept running was taken over:\n%s\n%s", all, out)
			}
			if !strings.Contains(all, "STILL running") || !strings.Contains(all, "passwall enable") {
				t.Fatalf("the router was not handed back:\n%s", all)
			}
		})
	}
}

// A router that cannot tell (no pgrep) is not taken: two stacks are the one
// state this must never create.
func TestAPassWallNobodyCanAskAboutCountsAsRunning(t *testing.T) {
	s := newStack(t)
	s.pgrep = false
	events, out := s.run("1", "start_service; echo \"rc=$?\"\n")
	if !strings.Contains(out, "rc=1") || strings.Contains(strings.Join(events, "\n"), "instance") {
		t.Fatalf("taken without knowing whether PassWall stopped:\n%s\n%s", strings.Join(events, "\n"), out)
	}
}

// The procd build is asked `running`, as before.
func TestAProcdPassWallIsAskedRunning(t *testing.T) {
	s := newStack(t)
	s.procd = true
	events, out := s.run("1", "start_service; echo \"rc=$?\"\n", "PW_RUNNING=1")
	all := strings.Join(events, "\n")
	if !strings.Contains(out, "rc=0") || !strings.Contains(all, "passwall running") || strings.Contains(all, "pgrep -f /tmp/etc") {
		t.Fatalf("events:\n%s\n%s", all, out)
	}
}

// A trial: taken as always, but nothing a reboot would keep — no disable,
// with the switch off too — and the agent's cron watchdog held off.
func TestATrialTakesTheRouterWithoutDisablingAnything(t *testing.T) {
	s := newStack(t)
	writeExec(t, s.path("vectra-trial.json"), "{}")
	events, out := s.run("0", "start_service; echo \"rc=$?\"\n")
	if !strings.Contains(out, "rc=0") {
		t.Fatalf("a trial with the switch off did not start:\n%s\n%s", strings.Join(events, "\n"), out)
	}
	want := []string{
		"pgrep -P 1 -f run -c " + s.path("run") + "/|(^|/)(vctl-xray-wrapper|vctl-xray-private) run -c stdin:($| )",
		"agent enabled", "agent stop",
		"uci -q set passwall2.@global[0].enabled=0", "uci -q commit passwall2",
		"passwall enabled", "passwall stop",
		"pgrep -f /tmp/etc/passwall2/bin", "pgrep -f /tmp/etc/passwall2/bin", "pgrep -f /tmp/etc/passwall2/bin",
		"instance",
	}
	if got := withoutLogs(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	// What it owes is noted on tmpfs, next to the trial file: a reboot, which
	// ends the trial, leaves nothing on /etc that says a hand-back is owed.
	if !s.exists("trial.d/.passwall-disabled-by-vctl") || !s.exists("trial.d/.legacy-agent-disabled-by-vctl") {
		t.Fatal("a trial left no breadcrumbs: `vectra off` could not give anything back")
	}
	if s.exists(".passwall-disabled-by-vctl") || s.exists(".legacy-agent-disabled-by-vctl") {
		t.Fatal("a trial wrote its breadcrumbs where a reboot keeps them")
	}
	s.heldFor(600)

	// Its stop gives everything back, and lets the watchdog go.
	events, _ = s.run("0", "stop\n")
	all := strings.Join(events, "\n")
	for _, want := range []string{"teardown", "passwall enable", "passwall start", "agent enable", "agent start"} {
		if !strings.Contains(all, want) {
			t.Errorf("the trial's stop did not run %q:\n%s", want, all)
		}
	}
	if s.exists("watchdog.last-restart") {
		t.Error("the hand-back left the agent's watchdog held off")
	}
	if s.exists("trial.d/.passwall-disabled-by-vctl") || s.exists("trial.d/.legacy-agent-disabled-by-vctl") {
		t.Error("the hand-back left the trial's breadcrumbs")
	}
}

// Without the trial file the switch still decides: off hands back.
func TestWithoutATrialTheSwitchStillDecides(t *testing.T) {
	s := newStack(t)
	events, out := s.run("0", "start_service; echo \"rc=$?\"\n")
	if !strings.Contains(out, "rc=0") || strings.Contains(strings.Join(events, "\n"), "instance") {
		t.Fatalf("enabled=0 started vctl:\n%s", strings.Join(events, "\n"))
	}
}

// A breadcrumb can outlive the takeover and find PassWall running — brought
// back while vctl held the router. Started again, it would only be stopped by
// an older luci-app-passwall2 (its start runs its stop, which ends in `exit
// 0`), and restarted all over by 26.8.10 (which runs that stop in a
// subshell). The hand-back leaves a running stack running: its switch, here,
// was never off.
func TestTheHandBackDoesNotStartARunningPassWall(t *testing.T) {
	s := newStack(t)
	writeExec(t, s.path(".passwall-disabled-by-vctl"), "")
	events, _ := s.run("1", "restore_passwall_stack\n", "PGREP=0")
	all := strings.Join(events, "\n")
	if !containsLine(events, "passwall enable") || containsLine(events, "passwall start") || containsLine(events, "passwall restart") {
		t.Fatalf("events:\n%s", all)
	}
	if s.exists(".passwall-disabled-by-vctl") {
		t.Fatal("the breadcrumb of a stack that is back stayed")
	}
	// Down: started, as always.
	writeExec(t, s.path(".passwall-disabled-by-vctl"), "")
	events, _ = s.run("1", "restore_passwall_stack\n", "PGREP=1")
	if !strings.Contains(strings.Join(events, "\n"), "passwall start") {
		t.Fatalf("a stopped stack was not started:\n%s", strings.Join(events, "\n"))
	}
}

// The hold against the REAL watchdog of the legacy agent's package
// (router/vectra-controller-agent): with the takeover's stamp, 10 minutes
// ahead, it neither enables nor starts the agent it finds missing; with the
// stamp lapsed, or none, it does both.
// Two copies: this tree's, and main's — the one the test router runs, md5
// pinned (testdata/vectra-controller-watchdog.main is `git cat-file -p
// main:router/vectra-controller-agent/openwrt/files/usr/sbin/vectra-controller-watchdog`).
// Its dead-man branch runs only while the agent does: with the agent
// stopped it takes its restart branch, whose throttle the stamp is.
func TestTheHoldKeepsTheAgentsCronWatchdogOff(t *testing.T) {
	for _, c := range []struct{ name, path, md5 string }{
		{"this tree's", "../../vectra-controller-agent/openwrt/files/usr/sbin/vectra-controller-watchdog", ""},
		{"main's, as the test router runs it", "testdata/vectra-controller-watchdog.main", "0d6db9074d5a6136601f027610f246fa"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src, err := os.ReadFile(c.path)
			if err != nil {
				t.Fatal(err)
			}
			if sum := md5.Sum(src); c.md5 != "" && hex.EncodeToString(sum[:]) != c.md5 {
				t.Fatalf("%s is not main's watchdog: md5 %x, want %s", c.path, sum, c.md5)
			}
			testWatchdogHold(t, string(src))
		})
	}
}

func testWatchdogHold(t *testing.T, src string) {
	init, err := os.ReadFile(initScript)
	if err != nil {
		t.Fatal(err)
	}
	stamp := regexp.MustCompile(`(?m)^AGENT_WATCHDOG_STAMP="([^"]+)"$`).FindSubmatch(init)
	if stamp == nil || !strings.Contains(src, "\nLAST_RESTART_FILE=\""+string(stamp[1])+"\"\n") {
		t.Fatalf("the watchdog's stamp is not the init script's AGENT_WATCHDOG_STAMP (%q)", stamp)
	}

	run := func(hold string) string {
		dir := t.TempDir()
		log := filepath.Join(dir, "events.log")
		agentInit := filepath.Join(dir, "vectra-controller")
		writeExec(t, agentInit, "#!/bin/sh\necho \"init $*\" >>\""+log+"\"\n")
		agentBin := filepath.Join(dir, "vectra-controller-agent")
		writeExec(t, agentBin, "#!/bin/sh\n")
		bin := filepath.Join(dir, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		writeExec(t, filepath.Join(bin, "pgrep"), "#!/bin/sh\nexit 1\n") // no agent process
		writeExec(t, filepath.Join(bin, "logger"), "#!/bin/sh\n")
		writeExec(t, filepath.Join(bin, "sleep"), "#!/bin/sh\n")
		stampFile := filepath.Join(dir, "last-restart")
		if hold != "" {
			writeExec(t, stampFile, hold+"\n")
		}
		script := src
		for name, path := range map[string]string{
			"INIT_SCRIPT": agentInit, "AGENT_BIN": agentBin,
			"STATE_FILE": filepath.Join(dir, "state"), "LAST_RESTART_FILE": stampFile,
		} {
			re := regexp.MustCompile(`(?m)^` + name + `="[^"]*"$`)
			if !re.MatchString(script) {
				t.Fatalf("the watchdog no longer sets %s", name)
			}
			script = re.ReplaceAllString(script, name+"=\""+path+"\"")
		}
		// main's also marks the boot for its dead-man's grace: never on this
		// machine's /var/run.
		script = regexp.MustCompile(`(?m)^DEADMAN_BOOT_MARKER="[^"]*"$`).
			ReplaceAllString(script, "DEADMAN_BOOT_MARKER=\""+filepath.Join(dir, "boot-epoch")+"\"")
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + "/usr/bin:/bin"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the watchdog failed: %v\n%s", err, out)
		}
		b, _ := os.ReadFile(log)
		return string(b)
	}
	now := time.Now().Unix()
	if got := run(strconv.FormatInt(now+600, 10)); got != "" {
		t.Fatalf("held off, the watchdog still ran: %q", got)
	}
	// The hold lapses by itself: 10 minutes after it was last set, and the
	// watchdog's own 5 minutes after that.
	if got := run(strconv.FormatInt(now-310, 10)); !strings.Contains(got, "init enable") || !strings.Contains(got, "init start") {
		t.Fatalf("a hold that lapsed still held the watchdog off: %q", got)
	}
	if got := run(""); !strings.Contains(got, "init enable") || !strings.Contains(got, "init start") {
		t.Fatalf("the control: without the hold the watchdog should restart the agent, ran %q", got)
	}
}

// ---- PassWall's own switch -------------------------------------------------

var (
	switchOff = []string{"uci -q set passwall2.@global[0].enabled=0", "uci -q commit passwall2"}
	switchOn  = []string{"uci -q set passwall2.@global[0].enabled=1", "uci -q commit passwall2"}
)

const (
	switchCrumb = ".passwall-switch-off-by-vctl"
	snippet     = "uci-defaults/99-vectra-trial-passwall-switch"
)

func indexOf(events []string, want string) int {
	for i, e := range events {
		if e == want {
			return i
		}
	}
	return -1
}

// changes is what the events changed in a config through uci.
func changes(events []string) []string {
	var out []string
	for _, e := range events {
		if strings.HasPrefix(e, "uci ") {
			out = append(out, e)
		}
	}
	return out
}

// The takeover turns PassWall's own switch off, noted on /etc, and before
// PassWall is stopped: no restart in between finds it on. PassWall's own
// restart — how its nightly subscription run ends, and a LuCI save — then
// brings up nothing next to vctl. The hand-back turns it on again before
// PassWall is enabled and started.
func TestTheTakeoverTurnsPassWallsOwnSwitchOff(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	events, out := s.run("1", "start_service; echo \"rc=$?\"\n")
	if !strings.Contains(out, "rc=0") {
		t.Fatalf("not taken:\n%s\n%s", strings.Join(events, "\n"), out)
	}
	got := withoutLogs(events)
	if i, stop := indexOf(got, switchOff[0]), indexOf(got, "passwall stop"); i < 0 || stop < 0 || indexOf(got, switchOff[1]) != i+1 || i > stop {
		t.Fatalf("the switch did not go off, committed, before PassWall's stop:\n  %s", strings.Join(got, "\n  "))
	}
	if sw := s.pwSwitch(); sw != "0" || !s.exists(switchCrumb) || s.exists("trial.d/"+switchCrumb) || s.exists(snippet) {
		t.Fatalf("switch %q, noted on /etc %v, on tmpfs %v, snippet %v", sw, s.exists(switchCrumb), s.exists("trial.d/"+switchCrumb), s.exists(snippet))
	}
	// Midnight: subscribe.lua's `/etc/init.d/passwall2 restart &`.
	s.run("1", "'"+s.path("passwall2")+"' restart\n")
	if s.exists("pw.running") {
		t.Fatal("PassWall's own restart brought its stack up next to vctl's")
	}

	events, out = s.run("1", "stop\n")
	got = withoutLogs(events)
	if i, enable, start := indexOf(got, switchOn[0]), indexOf(got, "passwall enable"), indexOf(got, "passwall start"); i < 0 || enable < 0 || start < 0 ||
		indexOf(got, switchOn[1]) != i+1 || i > enable {
		t.Fatalf("the hand-back did not turn the switch on, committed, before PassWall's enable and start:\n  %s\n%s", strings.Join(got, "\n  "), out)
	}
	if sw := s.pwSwitch(); sw != "1" || s.exists(switchCrumb) || !s.exists("pw.running") {
		t.Fatalf("after the hand-back: switch %q, breadcrumb %v, running %v", sw, s.exists(switchCrumb), s.exists("pw.running"))
	}
	// The stand-in's control: with its switch on, its own restart does run
	// its stack — the midnight check above is not vacuous.
	s.run("1", "'"+s.path("passwall2")+"' stop; '"+s.path("passwall2")+"' restart\n")
	if !s.exists("pw.running") {
		t.Fatal("the stand-in ran nothing with its switch on")
	}
}

// Every hand-back turns it on again: the stop, the package's prerm, a start
// with vctl switched off, and a start that cannot start.
func TestEveryHandBackTurnsPassWallsSwitchOnAgain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		back func(s *stack) ([]string, string)
	}{
		{"stop", func(s *stack) ([]string, string) { return s.run("1", "stop\n") }},
		{"prerm", func(s *stack) ([]string, string) { return s.prerm([]string{"remove"}) }},
		{"a start with vctl switched off", func(s *stack) ([]string, string) { return s.run("0", "start_service\n") }},
		{"a start that cannot start", func(s *stack) ([]string, string) {
			return s.run("1", "RENDERER=/nonexistent/render.sh\nstart_service\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			if _, out := s.run("1", "start_service; echo \"rc=$?\"\n"); !strings.Contains(out, "rc=0") || s.pwSwitch() != "0" {
				t.Fatalf("not taken:\n%s", out)
			}
			events, out := tc.back(s)
			if sw := s.pwSwitch(); sw != "1" || s.exists(switchCrumb) || !s.exists("pw.running") || !containsLine(events, "passwall start") {
				t.Fatalf("switch %q, breadcrumb %v, running %v:\n%s\n%s", sw, s.exists(switchCrumb), s.exists("pw.running"), strings.Join(events, "\n"), out)
			}
		})
	}
}

// AN UPGRADE IS NOT A HAND-BACK EITHER. opkg runs the OLD package's prerm on
// every upgrade — `prerm upgrade <new version>`, with PKG_UPGRADE=1
// (libopkg/opkg_install.c) — and that prerm stopped vctl: PassWall and the
// legacy agent back for the seconds of the file swap, next to vctl's outgoing
// xray, until the new postinst took them away again. And a vctl that updates
// itself (update_controller) runs that opkg as its own child: the stop killed
// the opkg in the middle of its work. The new postinst restarts the running
// vctl instead, and a restart is no hand-back.
func TestAnUpgradeIsNotAHandBack(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
		env  []string
	}{
		{"as opkg runs it", []string{"upgrade", "0.6.0-r20"}, []string{"PKG_UPGRADE=1"}},
		{"PKG_UPGRADE alone", nil, []string{"PKG_UPGRADE=1"}},
		{"its argument alone", []string{"upgrade", "0.6.0-r20"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			s.write("vctl.enabled", "")
			if _, out := s.run("1", "start_service; echo \"rc=$?\"\n"); !strings.Contains(out, "rc=0") || s.pwSwitch() != "0" {
				t.Fatalf("not taken:\n%s", out)
			}
			events, out := s.prerm(tc.args, tc.env...)
			if len(events) != 0 || s.pwSwitch() != "0" || s.exists("pw.running") || s.exists("agent.running") ||
				!s.exists("vctl.running") || !s.exists("nft.vctl") || !s.exists("vctl.enabled") {
				t.Fatalf("switch %q, PassWall running %v, agent running %v, vctl running %v, data plane %v, boot links %v:\n%s\n%s",
					s.pwSwitch(), s.exists("pw.running"), s.exists("agent.running"), s.exists("vctl.running"),
					s.exists("nft.vctl"), s.exists("vctl.enabled"), strings.Join(events, "\n"), out)
			}
		})
	}
}

// A trial turns it off as well, noted on tmpfs — and, since /etc/config
// outlives the trial, writes the snippet that turns it on at the next boot:
// after a power cycle PassWall's own boot start runs its stack again, and
// nothing on /etc says a hand-back is owed.
func TestATrialTurnsPassWallsSwitchOffUntilTheNextBoot(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	writeExec(t, s.path("vectra-trial.json"), "{}")
	events, out := s.run("0", "start_service; echo \"rc=$?\"\n")
	if !strings.Contains(out, "rc=0") || s.pwSwitch() != "0" {
		t.Fatalf("the trial did not take the switch:\n%s\n%s", strings.Join(events, "\n"), out)
	}
	if !s.exists("trial.d/"+switchCrumb) || s.exists(switchCrumb) || !s.exists(snippet) {
		t.Fatalf("noted on tmpfs %v, on /etc %v, snippet %v", s.exists("trial.d/"+switchCrumb), s.exists(switchCrumb), s.exists(snippet))
	}
	written, _ := os.ReadFile(s.path(snippet))

	events, out = s.reboot()
	// PassWall's own boot start (S99): the trial left its link.
	s.run("0", "'"+s.path("passwall2")+"' start\n")
	if sw := s.pwSwitch(); sw != "1" || s.exists(snippet) || !s.exists("pw.running") {
		t.Fatalf("after the reboot: switch %q, snippet left %v, PassWall runs %v\nthe snippet:\n%s\n%s\n%s",
			sw, s.exists(snippet), s.exists("pw.running"), written, strings.Join(events, "\n"), out)
	}
	for _, crumb := range []string{switchCrumb, ".passwall-disabled-by-vctl", ".legacy-agent-disabled-by-vctl"} {
		if s.exists(crumb) {
			t.Errorf("after the reboot /etc still has %s", crumb)
		}
	}
}

// `vectra off` during the trial turns it on at once, and takes the snippet
// away: the trial's file goes first, then the stop.
func TestATrialsEndTurnsPassWallsSwitchOnAtOnce(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	writeExec(t, s.path("vectra-trial.json"), "{}")
	if _, out := s.run("0", "start_service; echo \"rc=$?\"\n"); !strings.Contains(out, "rc=0") {
		t.Fatal(out)
	}
	if err := os.Remove(s.path("vectra-trial.json")); err != nil {
		t.Fatal(err)
	}
	events, out := s.run("0", "stop\n")
	if s.pwSwitch() != "1" || s.exists(snippet) || s.exists("trial.d/"+switchCrumb) || !s.exists("pw.running") {
		t.Fatalf("switch %q, snippet %v, breadcrumb %v, running %v:\n%s\n%s", s.pwSwitch(), s.exists(snippet),
			s.exists("trial.d/"+switchCrumb), s.exists("pw.running"), strings.Join(events, "\n"), out)
	}
}

// Keep is the takeover once more, without the trial's file: the switch stays
// off, for good — its breadcrumb moved to /etc, the snippet gone — and is not
// turned off twice. A reboot keeps it off; the hand-back still gives it back.
func TestKeepKeepsPassWallsSwitchOffForGood(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	writeExec(t, s.path("vectra-trial.json"), "{}")
	if _, out := s.run("0", "start_service; echo \"rc=$?\"\n"); !strings.Contains(out, "rc=0") {
		t.Fatal(out)
	}
	if err := os.Remove(s.path("vectra-trial.json")); err != nil {
		t.Fatal(err)
	}
	events, out := s.run("1", "start_service; echo \"rc=$?\"\n")
	if !strings.Contains(out, "rc=0") || len(changes(events)) != 0 {
		t.Fatalf("keep: changed %v\n%s\n%s", changes(events), strings.Join(events, "\n"), out)
	}
	if s.pwSwitch() != "0" || !s.exists(switchCrumb) || s.exists("trial.d/"+switchCrumb) || s.exists(snippet) {
		t.Fatalf("after keep: switch %q, noted on /etc %v, on tmpfs %v, snippet %v",
			s.pwSwitch(), s.exists(switchCrumb), s.exists("trial.d/"+switchCrumb), s.exists(snippet))
	}
	s.reboot()
	if s.pwSwitch() != "0" || !s.exists(switchCrumb) {
		t.Fatalf("after keep and a reboot: switch %q, noted %v", s.pwSwitch(), s.exists(switchCrumb))
	}
	s.run("1", "stop\n")
	if s.pwSwitch() != "1" || s.exists(switchCrumb) || !s.exists("pw.running") {
		t.Fatalf("the hand-back after keep: switch %q, noted %v, running %v", s.pwSwitch(), s.exists(switchCrumb), s.exists("pw.running"))
	}
}

// An operator's switch that is off is theirs: never noted, never turned on —
// by a takeover, a trial or a hand-back.
func TestAnOperatorsPassWallSwitchOffIsNeverTouched(t *testing.T) {
	t.Parallel()
	for _, trial := range []bool{false, true} {
		t.Run(map[bool]string{false: "for good", true: "trial"}[trial], func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			s.setSwitch("0")
			if trial {
				writeExec(t, s.path("vectra-trial.json"), "{}")
			}
			events, out := s.run("1", "start_service; echo \"rc=$?\"\n")
			if !strings.Contains(out, "rc=0") {
				t.Fatalf("not taken:\n%s", out)
			}
			more, _ := s.run("1", "stop\n")
			if c := changes(append(events, more...)); len(c) != 0 {
				t.Fatalf("an operator's switch was changed: %v", c)
			}
			if s.pwSwitch() != "0" || s.exists(switchCrumb) || s.exists("trial.d/"+switchCrumb) || s.exists(snippet) {
				t.Fatalf("switch %q, noted %v/%v, snippet %v", s.pwSwitch(), s.exists(switchCrumb), s.exists("trial.d/"+switchCrumb), s.exists(snippet))
			}
		})
	}
}

// A PassWall whose rc.d link an operator had switched off still restarts
// through its own switch, so the switch is taken all the same — and given
// back, though the link is not: nothing is started the operator had not.
func TestPassWallsSwitchIsTakenWhereItsLinkWasNotOurs(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	events, out := s.run("1", "start_service; echo \"rc=$?\"\n", "PW_ENABLED=1")
	if !strings.Contains(out, "rc=0") || s.pwSwitch() != "0" || !s.exists(switchCrumb) || s.exists(".passwall-disabled-by-vctl") {
		t.Fatalf("switch %q, noted %v:\n%s\n%s", s.pwSwitch(), s.exists(switchCrumb), strings.Join(events, "\n"), out)
	}
	events, _ = s.run("1", "stop\n", "PW_ENABLED=1")
	if s.pwSwitch() != "1" || s.exists(switchCrumb) {
		t.Fatalf("not given back: switch %q, noted %v", s.pwSwitch(), s.exists(switchCrumb))
	}
	for _, not := range []string{"passwall enable", "passwall start", "passwall restart"} {
		if containsLine(events, not) {
			t.Errorf("ran %q for a PassWall whose link was not ours:\n%s", not, strings.Join(events, "\n"))
		}
	}
}

// A PassWall brought back while vctl held the router came up with its switch
// off: turned on again, it is restarted — not left so, and not started, which
// on an older luci-app-passwall2 only stops a running stack. The hand-back
// does not ask `uci get` whether the switch was off (a staged value answers
// that), so one an operator turned on meanwhile is restarted too, for
// nothing, briefly — and the breadcrumb goes only with a commit made.
func TestAPassWallThatRanWithItsSwitchOffIsRestarted(t *testing.T) {
	t.Parallel()
	for _, env := range []string{"PW_ENABLED=0", "PW_ENABLED=1"} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			s.run("1", "start_service\n", env)
			events, _ := s.run("1", "stop\n", env, "PGREP=0")
			if !containsLine(events, "passwall restart") || containsLine(events, "passwall start") || s.pwSwitch() != "1" {
				t.Fatalf("switch %q:\n%s", s.pwSwitch(), strings.Join(events, "\n"))
			}
		})
	}
	s := newStack(t)
	s.run("1", "start_service\n")
	s.setSwitch("1")
	events, _ := s.run("1", "stop\n", "PGREP=0")
	if !containsLine(events, "passwall restart") || !containsLine(events, switchOn[1]) || s.exists(switchCrumb) {
		t.Fatalf("a PassWall whose switch an operator turned on meanwhile:\n%s", strings.Join(events, "\n"))
	}
}

// An unwritable breadcrumb, or a trial's unwritable snippet, takes nothing:
// the takeover declines, and the router is handed back with PassWall's
// switch on and PassWall running — the hand-back saying it turns the switch
// on only where it was off. Under dash where there is one (testShell): `:`
// rather than `true` for these writes ends the whole script there, as in
// busybox ash, and nothing is handed back.
func TestAnUnwritableBreadcrumbTakesNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file, say string
		trial, kept     bool
		wasOff          bool // the switch was off when the takeover declined
	}{
		{"the switch's breadcrumb", switchCrumb, "refusing to turn PassWall's switch off", false, false, false},
		{"a trial's snippet", snippet, "no trial", true, false, false},
		{"the link's breadcrumb", ".passwall-disabled-by-vctl", "refusing to disable PassWall", false, false, true},
		{"a kept trial's switch breadcrumb", switchCrumb, "refusing to keep PassWall's switch off", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStack(t)
			if tc.trial || tc.kept {
				writeExec(t, s.path("vectra-trial.json"), "{}")
			}
			if tc.kept {
				if _, out := s.run("0", "start_service; echo \"rc=$?\"\n"); !strings.Contains(out, "rc=0") {
					t.Fatal(out)
				}
				if err := os.Remove(s.path("vectra-trial.json")); err != nil {
					t.Fatal(err)
				}
			}
			// A directory where the file should go: every shell fails that
			// redirection.
			if err := os.MkdirAll(s.path(tc.file), 0o755); err != nil {
				t.Fatal(err)
			}
			events, out := s.run("1", "start_service; echo \"rc=$?\"\n")
			all := strings.Join(events, "\n")
			if !strings.Contains(out, "rc=1") || containsLine(events, "instance") || !strings.Contains(all, tc.say) {
				t.Fatalf("under %s:\n%s\n%s", testShell(), all, out)
			}
			if strings.Contains(all, "turning PassWall's switch back on") != tc.wasOff {
				t.Fatalf("the switch was off: %v, yet the hand-back said:\n%s", tc.wasOff, all)
			}
			// (The directory put in the snippet's place stays: not a snippet.)
			if !containsLine(events, "agent start") || s.pwSwitch() != "1" || !s.exists("pw.running") ||
				s.exists("trial.d/"+switchCrumb) || s.isFile(snippet) {
				t.Fatalf("not handed back: switch %q, running %v, tmpfs breadcrumb %v, snippet %v:\n%s",
					s.pwSwitch(), s.exists("pw.running"), s.exists("trial.d/"+switchCrumb), s.isFile(snippet), all)
			}
		})
	}
}

// ---- the vectra command and vectra.lan -------------------------------------

func TestTheVectraCommandIsVctlPower(t *testing.T) {
	raw, err := os.ReadFile("files/usr/sbin/vectra")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), "#!/bin/sh") || !strings.Contains(string(raw), "\nexec /usr/sbin/vctl power \"$@\"\n") {
		t.Errorf("/usr/sbin/vectra does not exec vctl power:\n%s", raw)
	}
	if st, _ := os.Stat("files/usr/sbin/vectra"); st == nil || st.Mode().Perm()&0o111 == 0 {
		t.Error("/usr/sbin/vectra is not executable")
	}
	if out, err := exec.Command("sh", "-n", "files/usr/sbin/vectra").CombinedOutput(); err != nil {
		t.Errorf("sh -n: %v\n%s", err, out)
	}
}

// lanRouter holds a router's UCI (the dhcp config as given, in the uci
// stand-in's JSON store) for the uci-defaults script and the postrm, with
// dnsmasq, cron, killall and rm stood in and root's crontab in crontab.
type lanRouter struct {
	t     *testing.T
	dir   string
	state string
	log   string
	env   []string
}

type uciSection struct {
	Name  string              `json:"name"`
	Type  string              `json:"type"`
	Opts  map[string]string   `json:"opts"`
	Lists map[string][]string `json:"lists"`
}

func newLanRouter(t *testing.T, dhcp []uciSection, dnsmasqUp bool) *lanRouter {
	t.Helper()
	r := &lanRouter{t: t, dir: t.TempDir()}
	r.state = filepath.Join(r.dir, "uci.json")
	r.log = filepath.Join(r.dir, "calls.log")
	raw, err := json.Marshal(map[string][]uciSection{"dhcp": dhcp})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.state, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(r.dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	standIn(t, bin, "uci")
	rec := func(who string) string { return "#!/bin/sh\necho \"" + who + " $*\" >>\"$STUB_LOG\"\n" }
	writeExec(t, filepath.Join(bin, "killall"), rec("killall"))
	// The postrm clears LuCI's caches under /tmp: not this machine's.
	writeExec(t, filepath.Join(bin, "rm"), rec("rm"))
	up := "1"
	if dnsmasqUp {
		up = "0"
	}
	dnsmasq := filepath.Join(r.dir, "dnsmasq")
	writeExec(t, dnsmasq, rec("dnsmasq")+"[ \"$1\" = running ] && exit "+up+"\nexit 0\n")
	cron := filepath.Join(r.dir, "cron")
	writeExec(t, cron, rec("cron")+"[ \"$1\" = running ] && exit ${CRON_RUNNING:-0}\nexit 0\n")
	r.env = []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"STUB_LOG=" + r.log, "UCI_STATE=" + r.state, "DNSMASQ_INIT=" + dnsmasq,
		"CRONTAB=" + filepath.Join(r.dir, "crontabs", "root"), "CRON_INIT=" + cron,
	}
	return r
}

func (r *lanRouter) run(script string, args ...string) []string {
	r.t.Helper()
	_ = os.Remove(r.log)
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), r.env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("%s %v: %v\n%s", script, args, err, out)
	}
	raw, _ := os.ReadFile(r.log)
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// seed puts a section of config pkg in the uci stand-in's store.
func (r *lanRouter) seed(pkg string, sec uciSection) {
	r.t.Helper()
	st := r.store()
	st[pkg] = append(st[pkg], sec)
	raw, err := json.Marshal(st)
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(r.state, raw, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// option is an option's value in the uci stand-in's store ("" when unset).
func (r *lanRouter) option(pkg, section, opt string) string {
	r.t.Helper()
	for _, s := range r.store()[pkg] {
		if s.Name == section {
			return s.Opts[opt]
		}
	}
	return ""
}

func (r *lanRouter) store() map[string][]uciSection {
	r.t.Helper()
	raw, err := os.ReadFile(r.state)
	if err != nil {
		r.t.Fatal(err)
	}
	var st map[string][]uciSection
	if err := json.Unmarshal(raw, &st); err != nil {
		r.t.Fatal(err)
	}
	return st
}

func (r *lanRouter) names(section string) []string {
	r.t.Helper()
	raw, err := os.ReadFile(r.state)
	if err != nil {
		r.t.Fatal(err)
	}
	var st map[string][]uciSection
	if err := json.Unmarshal(raw, &st); err != nil {
		r.t.Fatal(err)
	}
	for _, s := range st["dhcp"] {
		if s.Name == section {
			return s.Lists["interface_name"]
		}
	}
	r.t.Fatalf("no section %s", section)
	return nil
}

func stockDHCP() []uciSection {
	return []uciSection{
		{Name: "cfg01411c", Type: "dnsmasq", Opts: map[string]string{"domain": "lan"}},
		// The LAN's section is found by its interface, not its name.
		{Name: "home", Type: "dhcp", Opts: map[string]string{"interface": "lan", "start": "100"}},
		{Name: "wan", Type: "dhcp", Opts: map[string]string{"interface": "wan", "ignore": "1"}},
	}
}

const defaultsScript = "files/etc/uci-defaults/90_vectra_controller_pro_defaults"

// The router's own names on the LAN, as the uci-defaults script adds them.
var routerNames = []string{"vectra.lan", "my.vectra-pro.net"}

// The uci-defaults script names the router vectra.lan and my.vectra-pro.net
// on the LAN's dhcp section — once, however often it runs — and has dnsmasq
// take them up with one commit and one reload.
func TestUCIDefaultsNameTheRouter(t *testing.T) {
	t.Parallel()
	r := newLanRouter(t, stockDHCP(), true)
	calls := strings.Join(r.run(defaultsScript), "\n")
	if got := r.names("home"); !reflect.DeepEqual(got, routerNames) {
		t.Fatalf("home.interface_name = %v\n%s", got, calls)
	}
	for _, want := range []string{"uci -q add_list dhcp.home.interface_name=vectra.lan", "uci -q add_list dhcp.home.interface_name=my.vectra-pro.net",
		"uci -q commit dhcp", "dnsmasq reload"} {
		if !strings.Contains(calls, want) {
			t.Errorf("did not run %q:\n%s", want, calls)
		}
	}
	if n := strings.Count(calls, "commit dhcp"); n != 1 || strings.Count(calls, "dnsmasq reload") != 1 {
		t.Errorf("%d commits for the two names, and reloads:\n%s", n, calls)
	}
	if strings.Contains(calls, "dhcp.wan.interface_name") {
		t.Errorf("named the WAN:\n%s", calls)
	}
	// Again (every upgrade's postinst, the first boot): nothing more.
	calls = strings.Join(r.run(defaultsScript), "\n")
	if got := r.names("home"); !reflect.DeepEqual(got, routerNames) || strings.Contains(calls, "add_list") ||
		strings.Contains(calls, "commit dhcp") || strings.Contains(calls, "dnsmasq") {
		t.Fatalf("a second run added or reloaded: %v\n%s", got, calls)
	}
}

// A router upgraded from a version that named it vectra.lan only gets
// my.vectra-pro.net beside it — and vectra.lan is not added twice.
func TestUCIDefaultsAddTheNewNameOnAnUpgrade(t *testing.T) {
	t.Parallel()
	dhcp := stockDHCP()
	dhcp[1].Lists = map[string][]string{"interface_name": {"vectra.lan"}}
	r := newLanRouter(t, dhcp, true)
	calls := strings.Join(r.run(defaultsScript), "\n")
	if got := r.names("home"); !reflect.DeepEqual(got, routerNames) {
		t.Fatalf("home.interface_name = %v\n%s", got, calls)
	}
	if strings.Contains(calls, "interface_name=vectra.lan") || !strings.Contains(calls, "dnsmasq reload") {
		t.Fatalf("calls:\n%s", calls)
	}
}

// With dnsmasq not running yet (the first boot runs uci-defaults before it
// starts) nothing is reloaded; dnsmasq reads the names when it starts.
func TestUCIDefaultsDoNotStartDnsmasq(t *testing.T) {
	t.Parallel()
	r := newLanRouter(t, stockDHCP(), false)
	calls := strings.Join(r.run(defaultsScript), "\n")
	if !reflect.DeepEqual(r.names("home"), routerNames) || strings.Contains(calls, "dnsmasq reload") {
		t.Fatalf("calls:\n%s", calls)
	}
}

// A name of the owner's own stays, and so does a router without a LAN dhcp
// section.
func TestUCIDefaultsKeepWhatIsThere(t *testing.T) {
	t.Parallel()
	dhcp := stockDHCP()
	dhcp[1].Lists = map[string][]string{"interface_name": {"nas.lan"}}
	r := newLanRouter(t, dhcp, true)
	r.run(defaultsScript)
	if got := r.names("home"); !reflect.DeepEqual(got, append([]string{"nas.lan"}, routerNames...)) {
		t.Fatalf("interface_name = %v", got)
	}
	none := newLanRouter(t, []uciSection{{Name: "wan", Type: "dhcp", Opts: map[string]string{"interface": "wan"}}}, true)
	if calls := strings.Join(none.run(defaultsScript), "\n"); strings.Contains(calls, "add_list") || strings.Contains(calls, "dnsmasq") {
		t.Fatalf("no LAN, yet:\n%s", calls)
	}
}

// makefileScript is a maintainer script as openwrt/Makefile defines it, as
// the builds extract it (scripts/lib/pkg.sh): `$$` is `$`.
func makefileScript(t *testing.T, name string) string {
	t.Helper()
	mk, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	start := "define Package/$(PKG_NAME)/" + name + "\n"
	i := strings.Index(string(mk), start)
	if i < 0 {
		t.Fatalf("no %s in the Makefile", name)
	}
	body := string(mk)[i+len(start):]
	body = body[:strings.Index(body, "\nendef\n")+1]
	p := filepath.Join(t.TempDir(), name)
	writeExec(t, p, strings.ReplaceAll(body, "$$", "$"))
	if out, err := exec.Command("sh", "-n", p).CombinedOutput(); err != nil {
		t.Fatalf("%s does not parse: %v\n%s", name, err, out)
	}
	return p
}

// Removing the package takes vectra.lan and my.vectra-pro.net away; an
// upgrade — whose old postrm opkg runs as `postrm upgrade <version>` with
// PKG_UPGRADE=1 — keeps them.
func TestPostrmTakesTheNamesAwayOnRemovalOnly(t *testing.T) {
	t.Parallel()
	postrm := makefileScript(t, "postrm")
	named := stockDHCP()
	named[1].Lists = map[string][]string{"interface_name": {"nas.lan", "vectra.lan", "my.vectra-pro.net"}}

	up := newLanRouter(t, named, true)
	up.env = append(up.env, "PKG_UPGRADE=1")
	calls := strings.Join(up.run(postrm, "upgrade", "0.6.0-r1"), "\n")
	if got := up.names("home"); !reflect.DeepEqual(got, []string{"nas.lan", "vectra.lan", "my.vectra-pro.net"}) || strings.Contains(calls, "del_list") {
		t.Fatalf("an upgrade took a name away: %v\n%s", got, calls)
	}
	if !strings.Contains(calls, "killall -HUP rpcd") {
		t.Errorf("the upgrade's postrm no longer tells rpcd:\n%s", calls)
	}

	gone := newLanRouter(t, named, true)
	gone.env = append(gone.env, "PKG_UPGRADE=0")
	calls = strings.Join(gone.run(postrm, "remove"), "\n")
	if got := gone.names("home"); !reflect.DeepEqual(got, []string{"nas.lan"}) {
		t.Fatalf("removal left %v\n%s", got, calls)
	}
	for _, want := range []string{"uci -q commit dhcp", "dnsmasq reload", "killall -HUP rpcd"} {
		if !strings.Contains(calls, want) {
			t.Errorf("removal did not run %q:\n%s", want, calls)
		}
	}
	if strings.Count(calls, "commit dhcp") != 1 || strings.Count(calls, "dnsmasq reload") != 1 {
		t.Errorf("one commit and one reload for both names:\n%s", calls)
	}
	// Nothing named: nothing committed, nothing reloaded.
	calls = strings.Join(gone.run(postrm, "remove"), "\n")
	if strings.Contains(calls, "commit") || strings.Contains(calls, "dnsmasq") {
		t.Fatalf("a removal with no name to take away:\n%s", calls)
	}
}

// An upgrade during a trial must not give vctl the boot links the trial left
// off: the postinst's `enable` waits for no trial file — the init script's.
// (The postinst runs absolute paths; the procd stand proves it on a router.)
func TestThePostinstGivesATrialNoBootLinks(t *testing.T) {
	postinst, err := os.ReadFile(makefileScript(t, "postinst"))
	if err != nil {
		t.Fatal(err)
	}
	init, err := os.ReadFile(initScript)
	if err != nil {
		t.Fatal(err)
	}
	trial := regexp.MustCompile(`(?m)^TRIAL_FILE="([^"]+)"$`).FindSubmatch(init)
	if trial == nil {
		t.Fatal("the init script names no TRIAL_FILE")
	}
	var enables []string
	for _, l := range strings.Split(string(postinst), "\n") {
		if strings.Contains(l, "/etc/init.d/vectra-controller-pro enable") {
			enables = append(enables, strings.TrimSpace(l))
		}
	}
	want := "[ -f " + string(trial[1]) + " ] || /etc/init.d/vectra-controller-pro enable >/dev/null 2>&1 || true; \\"
	if len(enables) != 1 || enables[0] != want {
		t.Fatalf("the postinst enables vctl as %q, want only %q", enables, want)
	}
}

// vctl's install takes away the links vectra-geodata before 2026.9.28-r2 left
// in /usr/share/v2ray — its own links only — and the directory when nothing
// else is in it.
func TestTheOldGeoLinksGoWithTheVctlThatNeededThem(t *testing.T) {
	t.Parallel()
	r := newLanRouter(t, stockDHCP(), true)
	legacy, own := filepath.Join(r.dir, "v2ray"), filepath.Join(r.dir, "geo")
	r.env = append(r.env, "GEOLEGACY="+legacy, "GEOOWN="+own)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(own, "geosite.dat"), filepath.Join(legacy, "geosite.dat")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "geoip.dat"), []byte("another package's"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := r.run(defaultsScript)
	if !containsLine(calls, "rm -f "+filepath.Join(legacy, "geosite.dat")) {
		t.Fatalf("vctl's own link stayed:\n%s", strings.Join(calls, "\n"))
	}
	if containsLine(calls, "rm -f "+filepath.Join(legacy, "geoip.dat")) {
		t.Fatal("another package's file was taken")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("the directory went with another package's file in it")
	}

	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		_ = os.Remove(filepath.Join(legacy, f))
	}
	r.run(defaultsScript)
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("an empty /usr/share/v2ray stayed")
	}

	// Where PassWall2 is installed the links stay: its own xray may read
	// them, and a hand-back gives the router to it.
	pw := filepath.Join(r.dir, "passwall2")
	writeExec(t, pw, "#!/bin/sh\n")
	r.env = append(r.env, "PASSWALL_INIT="+pw)
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(own, "geoip.dat"), filepath.Join(legacy, "geoip.dat")); err != nil {
		t.Fatal(err)
	}
	if calls := r.run(defaultsScript); containsLine(calls, "rm -f "+filepath.Join(legacy, "geoip.dat")) {
		t.Fatal("took a link PassWall2 may read")
	}
}

// The kernel's reserve follows the router. Where the previous agent reserved
// 24 MB (99-vectra-lowmem.conf), vctl's takeover puts OpenWrt's own 16 MB
// back in a file read after it — on a router of more than 64 MB, never over a
// file already there; a trial only in the running kernel — and every
// hand-back, the package's removal too, gives the agent's back. A restart is
// no hand-back; a standby install changes nothing.
func TestTheMemoryReserveFollowsTheRouter(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	memconf, lowmem := s.path("sysctl.d/99-vectra-pro-memory.conf"), s.path("sysctl.d/99-vectra-lowmem.conf")
	s.write("meminfo", "MemTotal:         239720 kB\n")
	sysctls := func(events []string) (out []string) {
		for _, e := range events {
			if strings.HasPrefix(e, "sysctl ") {
				out = append(out, e)
			}
		}
		return out
	}
	mine := func() string { b, _ := os.ReadFile(memconf); return string(b) }

	// A router the agent reserved nothing on keeps what it has.
	if events, _ := s.run("1", "start; stop"); len(sysctls(events)) != 0 || s.exists("sysctl.d/99-vectra-pro-memory.conf") {
		t.Fatalf("changed a reserve the agent never set: %v", sysctls(events))
	}

	s.write("sysctl.d/99-vectra-lowmem.conf", "vm.min_free_kbytes = 24576\n")
	events, _ := s.run("1", "start")
	if !strings.Contains(mine(), "vm.min_free_kbytes = 16384") || !containsLine(events, "sysctl -q -p "+memconf) {
		t.Fatalf("the takeover did not put OpenWrt's reserve back: %q %v", mine(), sysctls(events))
	}
	if events, _ := s.run("1", "VCTL_RELOADING=1 stop; start"); containsLine(events, "sysctl -q -p "+lowmem) || mine() == "" {
		t.Fatalf("a restart gave the agent's reserve back: %v", sysctls(events))
	}
	events, _ = s.run("1", "stop")
	if s.exists("sysctl.d/99-vectra-pro-memory.conf") || !containsLine(events, "sysctl -q -p "+lowmem) {
		t.Fatalf("the hand-back kept vctl's reserve: %v", sysctls(events))
	}

	// A trial: the running value, nothing a reboot keeps.
	s.write("vectra-trial.json", "{}")
	events, _ = s.run("1", "start")
	if s.exists("sysctl.d/99-vectra-pro-memory.conf") || !containsLine(events, "sysctl -q -w vm.min_free_kbytes=16384") {
		t.Fatalf("a trial: %v, file there %v", sysctls(events), s.exists("sysctl.d/99-vectra-pro-memory.conf"))
	}
	if events, _ = s.run("1", "stop"); !containsLine(events, "sysctl -q -p "+lowmem) {
		t.Fatalf("the trial's end kept OpenWrt's reserve: %v", sysctls(events))
	}
	_ = os.Remove(s.path("vectra-trial.json"))

	// Someone else's file there is neither overwritten nor taken away.
	s.write("sysctl.d/99-vectra-pro-memory.conf", "# mine\nvm.min_free_kbytes = 12000\n")
	s.run("1", "start; stop")
	if !strings.Contains(mine(), "12000") {
		t.Fatalf("touched a file not vctl's: %q", mine())
	}
	_ = os.Remove(memconf)

	// A router of 64 MB or less keeps OpenWrt's smaller reserve.
	s.write("meminfo", "MemTotal:          61440 kB\n")
	if s.run("1", "start"); s.exists("sysctl.d/99-vectra-pro-memory.conf") {
		t.Fatal("a 60 MB router got the 16 MB reserve")
	}
	s.run("1", "stop")
	s.write("meminfo", "MemTotal:         239720 kB\n")

	// The package's removal takes vctl's file away; an upgrade leaves it.
	s.run("1", "start")
	r := newLanRouter(t, stockDHCP(), true)
	writeExec(t, filepath.Join(r.dir, "bin", "sysctl"), "#!/bin/sh\necho \"sysctl $*\" >>\"$STUB_LOG\"\n")
	r.env = append(r.env, "MEMCONF="+memconf, "LOWMEMCONF="+lowmem)
	postrm := makefileScript(t, "postrm")
	r.run(postrm, "upgrade", "0.6.0-r17")
	if mine() == "" {
		t.Fatal("an upgrade's postrm took the reserve away")
	}
	calls := strings.Join(r.run(postrm, "remove"), "\n")
	if !strings.Contains(calls, "rm -f "+memconf) || !strings.Contains(calls, "sysctl -q -p "+lowmem) {
		t.Fatalf("the removal kept vctl's reserve or did not apply the agent's:\n%s", calls)
	}

	// A standby install (the package's defaults, nothing started) changes
	// nothing.
	_ = os.Remove(memconf)
	r.env = append(r.env, "MEMINFO="+s.path("meminfo"))
	if calls := strings.Join(r.run(defaultsScript), "\n"); strings.Contains(calls, "sysctl") || mine() != "" {
		t.Fatalf("the package's defaults set the reserve:\n%s", calls)
	}
}

// The support shell (vectra-controller-pro.main.remote_shell) is decided once,
// by the uci-defaults script: on ('1') where nothing set it, a new router and
// an upgraded one alike (since r15). What is set — by this script or the
// owner — is never changed again.
func TestUCIDefaultsDecideTheSupportShellOnce(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		state bool
		set   string
		want  string
	}{
		{"a new router", false, "", "1"},
		{"a router upgraded from a vctl without the switch", true, "", "1"},
		{"the owner turned it off", true, "0", "0"},
		{"the owner turned it on", false, "1", "1"},
	} {
		r := newLanRouter(t, stockDHCP(), true)
		statePath := filepath.Join(r.dir, "etc", "state.json")
		if c.state {
			if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath, []byte(`{"router_id":"r-1","agent_token":"x"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// No old agent's state: this machine's own is none of the test's.
		opts := map[string]string{"state_path": statePath, "legacy_state_path": filepath.Join(r.dir, "no-legacy.json"), "enabled": "1"}
		if c.set != "" {
			opts["remote_shell"] = c.set
		}
		r.seed("vectra-controller-pro", uciSection{Name: "main", Type: "controller", Opts: opts})
		r.run(defaultsScript)
		if got := r.option("vectra-controller-pro", "main", "remote_shell"); got != c.want {
			t.Errorf("%s: remote_shell = %q, want %q", c.name, got, c.want)
		}
		// Again (every upgrade's postinst): nothing changes, whatever the
		// state says by then.
		if !c.state {
			if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath, []byte(`{"router_id":"r-1"}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		r.run(defaultsScript)
		if got := r.option("vectra-controller-pro", "main", "remote_shell"); got != c.want {
			t.Errorf("%s, run again: remote_shell = %q, want %q", c.name, got, c.want)
		}
	}
}

// Since r15 the support shell is on for every router the switch has not been
// set on — one the old Vectra agent ran, one vctl ran, and a new one alike
// (the owner's decision of 2026-10-04: support must reach a router whose VPN
// failed); whatever the state files say.
func TestUCIDefaultsKeepTheSupportShellOfARouterTheOldAgentRan(t *testing.T) {
	t.Parallel()
	const none = "-"
	for _, c := range []struct {
		name          string
		state, legacy string // the files' contents, or none
		want          string
	}{
		{"the old agent's state", none, `{"router_id":"r-1","agent_token":"x"}`, "1"},
		{"both states", `{"router_id":"r-1"}`, `{"router_id":"r-1","agent_token":"x"}`, "1"},
		{"an empty old agent's state", none, "", "1"},
		{"both states empty", "", "", "1"},
		{"neither", none, none, "1"},
	} {
		r := newLanRouter(t, stockDHCP(), true)
		statePath := filepath.Join(r.dir, "etc", "vectra-controller-pro", "state.json")
		legacyPath := filepath.Join(r.dir, "etc", "vectra-controller", "state.json")
		for p, body := range map[string]string{statePath: c.state, legacyPath: c.legacy} {
			if body == none {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		r.seed("vectra-controller-pro", uciSection{Name: "main", Type: "controller",
			Opts: map[string]string{"state_path": statePath, "legacy_state_path": legacyPath, "enabled": "1"}})
		r.run(defaultsScript)
		if got := r.option("vectra-controller-pro", "main", "remote_shell"); got != c.want {
			t.Errorf("%s: remote_shell = %q, want %q", c.name, got, c.want)
		}
	}
}

// The package's config file does not name remote_shell: a conffile opkg puts
// in place of an untouched one on an upgrade would decide for the router,
// before the uci-defaults script could tell a new router from an old one.
func TestTheConffileLeavesTheSupportShellToTheUCIDefaults(t *testing.T) {
	raw, err := os.ReadFile("files/etc/config/vectra-controller-pro")
	if err != nil {
		t.Fatal(err)
	}
	f, err := uci.Parse(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if main := f.Named("main"); main == nil {
		t.Fatal("no main section")
	} else if v, ok := main.Options["remote_shell"]; ok {
		t.Fatalf("the conffile sets remote_shell %q", v)
	}
}

// A small router's swap comes with the package: zram-swap (and with it
// kmod-zram) is a dependency, so a clean install has what the tune switches
// on (internal/tune) and the memory guard assumes.
func TestTheSwapComesWithThePackage(t *testing.T) {
	t.Parallel()
	mk, err := os.ReadFile("Makefile")
	if err != nil {
		t.Fatal(err)
	}
	deps := regexp.MustCompile(`(?m)^\s*DEPENDS:=(.*)$`).FindSubmatch(mk)
	if deps == nil {
		t.Fatal("no DEPENDS in the Makefile")
	}
	if !strings.Contains(" "+string(deps[1])+" ", " +zram-swap ") {
		t.Fatalf("DEPENDS lacks +zram-swap: %s", deps[1])
	}
}

// postinstStand runs the package's postinst with its init script, vctl and
// the tools it calls stood in: `running` answers runningAfter once the
// postinst has started or restarted vctl, and every call is noted.
func postinstStand(t *testing.T, runningAfter bool, env ...string) []string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls.log")
	rec := func(who string) string { return "#!/bin/sh\necho \"" + who + " $*\" >>\"$STUB_LOG\"\n" }
	for _, tool := range []string{"killall", "rm", "sysctl"} {
		writeExec(t, filepath.Join(bin, tool), rec(tool))
	}
	// logger takes its lines on stdin.
	writeExec(t, filepath.Join(bin, "logger"), "#!/bin/sh\nwhile IFS= read -r l; do echo \"logger $* $l\" >>\"$STUB_LOG\"; done\n")
	up := "1"
	if runningAfter {
		up = "0"
	}
	started := filepath.Join(dir, "started")
	init := filepath.Join(dir, "init")
	writeExec(t, init, rec("init")+"case \"$1\" in\n"+
		"  running) [ -f '"+started+"' ] && exit "+up+"; exit 1;;\n"+
		"  start|restart) true >'"+started+"';;\n"+
		"esac\nexit 0\n")
	vctl := filepath.Join(dir, "vctl")
	writeExec(t, vctl, rec("vctl")+"echo 'changed: vm.swappiness: 60 -> 80'\n")
	raw, err := os.ReadFile(makefileScript(t, "postinst"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "postinst")
	writeExec(t, script, strings.ReplaceAll(string(raw), "/etc/init.d/vectra-controller-pro", init))
	cmd := exec.Command("sh", script, "configure")
	cmd.Env = append(append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "STUB_LOG="+log, "VCTL="+vctl), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("postinst: %v\n%s", err, out)
	}
	calls, _ := os.ReadFile(log)
	return strings.Split(strings.TrimSpace(string(calls)), "\n")
}

// The install tunes the router once vctl is up — its changes go to the log —
// and a standby install, or a start that gave the router back, tunes nothing.
func TestThePostinstTunesTheRouterOnceVctlRuns(t *testing.T) {
	t.Parallel()
	calls := postinstStand(t, true)
	tuned := -1
	for i, c := range calls {
		if c == "vctl tune apply" {
			tuned = i
		}
	}
	if tuned < 0 {
		t.Fatalf("no tune:\n%s", strings.Join(calls, "\n"))
	}
	for _, c := range calls[:tuned] {
		if c == "init start" || c == "init restart" {
			goto started
		}
	}
	t.Fatalf("tuned before vctl was started:\n%s", strings.Join(calls, "\n"))
started:
	if !containsLine(calls, "logger -t vectra-controller-pro changed: vm.swappiness: 60 -> 80") {
		t.Fatalf("the tune's changes are not in the log:\n%s", strings.Join(calls, "\n"))
	}

	for name, calls := range map[string][]string{
		"a standby install":         postinstStand(t, true, "VECTRA_SKIP_POSTINST_RESTART=1"),
		"a start that gave it back": postinstStand(t, false),
	} {
		for _, c := range calls {
			if strings.HasPrefix(c, "vctl ") {
				t.Fatalf("%s tuned the router:\n%s", name, strings.Join(calls, "\n"))
			}
		}
	}
}

// A removal puts back what the tune changed — before the hand-back, while
// vctl's binary is still on disk — and an upgrade does not.
func TestThePrermUndoesTheTuneOnRemovalOnly(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	s.write("vctl.enabled", "")
	if _, out := s.run("1", "start_service; echo \"rc=$?\"\n"); !strings.Contains(out, "rc=0") {
		t.Fatalf("not taken:\n%s", out)
	}
	vctl := s.path("bin/vctl-tune")
	writeExec(t, vctl, "#!/bin/sh\necho \"vctl $*\" >>\"$STUB_LOG\"\n")
	writeExec(t, s.path("bin/logger"), "#!/bin/sh\n")
	events, out := s.prerm([]string{"upgrade", "0.6.0-r36"}, "PKG_UPGRADE=1", "VCTL="+vctl)
	if len(events) != 0 {
		t.Fatalf("an upgrade undid the tune:\n%s\n%s", strings.Join(events, "\n"), out)
	}
	events, out = s.prerm([]string{"remove"}, "VCTL="+vctl)
	events = withoutLogs(events)
	if len(events) == 0 || events[0] != "vctl tune undo" {
		t.Fatalf("the removal did not undo the tune first:\n%s\n%s", strings.Join(events, "\n"), out)
	}
	if countLine(events, "vctl tune undo") != 1 {
		t.Fatalf("undone more than once:\n%s", strings.Join(events, "\n"))
	}
}

// The postrm takes the tune's own files on a removal (whatever the prerm's
// undo could not), and an upgrade keeps them.
func TestThePostrmTakesTheTunesFilesOnRemovalOnly(t *testing.T) {
	t.Parallel()
	r := newLanRouter(t, stockDHCP(), true)
	conf, backup := filepath.Join(r.dir, "95-vectra-tune.conf"), filepath.Join(r.dir, "tune-backup.json")
	r.env = append(r.env, "TUNECONF="+conf, "TUNEBACKUP="+backup)
	postrm := makefileScript(t, "postrm")
	if calls := strings.Join(r.run(postrm, "upgrade", "0.6.0-r36"), "\n"); strings.Contains(calls, conf) || strings.Contains(calls, backup) {
		t.Fatalf("an upgrade took the tune's files:\n%s", calls)
	}
	if calls := r.run(postrm, "remove"); !containsLine(calls, "rm -f "+conf+" "+backup) {
		t.Fatalf("the removal kept the tune's files:\n%s", strings.Join(calls, "\n"))
	}
}

// tune '1' is seeded where it is not set; an operator's '0' stays.
func TestUCIDefaultsSeedTheTuneSwitch(t *testing.T) {
	t.Parallel()
	r := newLanRouter(t, stockDHCP(), true)
	if calls := r.run(defaultsScript); !containsLine(calls, "uci -q set vectra-controller-pro.main.tune=1") {
		t.Fatalf("tune not seeded:\n%s", strings.Join(calls, "\n"))
	}
	off := newLanRouter(t, stockDHCP(), true)
	raw, err := json.Marshal(map[string][]uciSection{"dhcp": stockDHCP(),
		"vectra-controller-pro": {{Name: "main", Type: "controller", Opts: map[string]string{"tune": "0"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(off.state, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range off.run(defaultsScript) {
		if strings.Contains(c, "main.tune") && strings.Contains(c, " set ") {
			t.Fatalf("an operator's tune '0' was overwritten: %s", c)
		}
	}
}

func TestPrivateStdinXrayOwnershipPatternIsScoped(t *testing.T) {
	s := newStack(t)
	_, out := s.run("1", "vctl_xray_pattern\n")
	pattern, err := regexp.Compile(strings.TrimSpace(out))
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"/usr/sbin/vctl-xray-wrapper run -c stdin:", "vctl-xray-private run -c stdin:", "/usr/bin/xray run -c " + s.path("run") + "/old.json"} {
		if !pattern.MatchString(cmd) {
			t.Errorf("owned child not recognized: %s", cmd)
		}
	}
	for _, cmd := range []string{"/usr/bin/xray run -c stdin:", "/tmp/fake-vctl-xray-wrapper run -c stdin:", "vctl-xray-private run -c stdin:other", "sleep 90; test -f " + s.path("run") + "/fw-confirm"} {
		if pattern.MatchString(cmd) {
			t.Errorf("unowned process recognized: %s", cmd)
		}
	}
	events, _ := s.run("1", "kill_orphan_xray\n")
	if len(events) != 1 || !strings.Contains(events[0], "pgrep -P 1 -f ") {
		t.Fatalf("orphan query lacks parent scope: %v", events)
	}
}

// After the table, the resolver's flows its DNS redirect took are forgotten
// (vctl forget-dns-flows): they keep the redirect's NAT to the dead inbound.
// A vctl that fails — one that predates the command — or none at all never
// fails the teardown.
func TestTeardownForgetsTheDNSFlowsAfterTheTable(t *testing.T) {
	for _, vctlExit := range []string{"0", "1"} {
		r := newTeardownRun(t, 1)
		writeExec(t, filepath.Join(r.binDir, "vctl"), "#!/bin/sh\necho \"vctl $*\" >>\"$STUB_LOG\"\n"+
			"[ \"$1\" = teardown-mark ] && exit 1\nexit "+vctlExit+"\n")
		cfg := r.writeDesiredConfig(`{"schema":1,"inbounds":{"tproxy":{"listenIP":"0.0.0.0","port":12345,"fwmark":1}}}`)
		lines := r.run("STUB_UCI_XRAY_CONFIG_PATH=" + cfg)
		del, forget := -1, -1
		for i, l := range lines {
			switch l {
			case "nft delete table inet vctl":
				del = i
			case "vctl forget-dns-flows":
				forget = i
			}
		}
		if del < 0 || forget < del {
			t.Errorf("vctl exiting %s: the flows are not forgotten after the table\ngot:\n  %s", vctlExit, strings.Join(lines, "\n  "))
		}
	}
	// Without our table nothing is ours to forget.
	r := newTeardownRun(t, 1)
	writeExec(t, filepath.Join(r.binDir, "nft"), "#!/bin/sh\necho \"nft $*\" >>\"$STUB_LOG\"\nexit 1\n")
	writeExec(t, filepath.Join(r.binDir, "vctl"), "#!/bin/sh\necho \"vctl $*\" >>\"$STUB_LOG\"\nexit 1\n")
	if lines := r.run(); containsLine(lines, "vctl forget-dns-flows") {
		t.Errorf("no table of ours, yet flows forgotten:\n  %s", strings.Join(lines, "\n  "))
	}
}
