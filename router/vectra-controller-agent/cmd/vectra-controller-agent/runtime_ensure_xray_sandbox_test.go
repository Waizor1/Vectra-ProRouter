package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These run the real installer script against a sandbox: router paths point
// into a temp dir and curl/unzip/df/uci/uname/sha256sum are stubs on PATH. The
// point is the failure exits — which of them put PassWall back, which leave the
// current binary alone — not the happy path alone.

const (
	sandboxOldXray    = "#!/bin/sh\necho 'Xray 26.9.9 (Xray, Penetrates Everything.) 52a412d'\n"
	sandboxBrokenXray = "#!/bin/sh\nexit 1\n"
	sandboxNewXray    = "#!/bin/sh\necho 'Xray 26.7.28 (Xray, Penetrates Everything.) 5ca6f4b'\n"
	sandboxClobbered  = "\x7fELF-not-a-script"
	sandboxSHA        = "f5698bb218ada3b4022db26fafc39601c5f53b46b19eb76c9616325985807501"
)

type xraySandbox struct {
	t       *testing.T
	root    string
	paths   xrayRepairPaths
	env     map[string]string
	pwLog   string
	stubDir string
}

func newXraySandbox(t *testing.T) *xraySandbox {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell sandbox")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this host")
	}
	root := t.TempDir()
	sb := &xraySandbox{
		t:       t,
		root:    root,
		stubDir: filepath.Join(root, "stubs"),
		pwLog:   filepath.Join(root, "passwall.log"),
		paths: xrayRepairPaths{
			Bin:          filepath.Join(root, "usr/bin/xray"),
			Wrapper:      filepath.Join(root, "usr/sbin/vectra-xray-wrapper"),
			PasswallInit: filepath.Join(root, "etc/init.d/passwall2"),
			Meminfo:      filepath.Join(root, "meminfo"),
		},
		env: map[string]string{
			"STUB_ARCH":       "aarch64",
			"STUB_XRAY_FILE":  "",
			"STUB_CURL_FAIL":  "0",
			"STUB_SHA":        sandboxSHA,
			"STUB_SIZE":       "35389566",
			"STUB_FREE_KB":    "200000",
			"STUB_PW_RUNNING": "1",
			"STUB_UNZIP_MODE": "ok",
		},
	}
	for _, dir := range []string{sb.stubDir, filepath.Dir(sb.paths.Bin), filepath.Dir(sb.paths.Wrapper), filepath.Dir(sb.paths.PasswallInit), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sb.env["STUB_NEW_BIN"] = sb.write("new-xray", sandboxNewXray, 0o755)
	sb.env["STUB_ZIP"] = sb.write("release.zip", "zip-bytes", 0o644)
	sb.env["STUB_PW_LOG"] = sb.pwLog
	sb.env["STUB_BIN"] = sb.paths.Bin
	sb.write(sb.paths.Meminfo, "MemTotal: 240264 kB\nMemAvailable: 150000 kB\n", 0o644)

	stubs := map[string]string{
		"uname":     `echo "$STUB_ARCH"`,
		"uci":       `printf '%s' "$STUB_XRAY_FILE"`,
		"sha256sum": `echo "$STUB_SHA  $1"`,
		"curl": `[ "$STUB_CURL_FAIL" = 1 ] && exit 22
out=""; while [ $# -gt 0 ]; do [ "$1" = -o ] && { out="$2"; shift; }; shift; done
cp "$STUB_ZIP" "$out"`,
		"unzip": `if [ "$1" = -l ]; then
  [ -n "$STUB_SIZE" ] && printf '  Length      Date    Time    Name\n  %s  07-28-2026 10:00   xray\n' "$STUB_SIZE"
  exit 0
fi
case "$STUB_UNZIP_MODE" in
  fail) exit 1 ;;
  nospace_while_old) [ -e "$STUB_BIN" ] && exit 1 ;;
esac
cat "$STUB_NEW_BIN"`,
		"pgrep": `exit "$STUB_PW_RUNNING"`,
		"df":    `if [ "$STUB_FREE_KB" = garbage ]; then echo 'df: weird'; else printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/ubi0_2 45760 30000 %s 68%% /overlay\n' "$STUB_FREE_KB"; fi`,
	}
	for name, body := range stubs {
		sb.write(filepath.Join("stubs", name), "#!/bin/sh\n"+body+"\n", 0o755)
	}
	if err := os.WriteFile(sb.paths.PasswallInit, []byte(`#!/bin/sh
echo "$1" >> "$STUB_PW_LOG"
exit 0
`), 0o755); err != nil {
		t.Fatal(err)
	}
	return sb
}

func (sb *xraySandbox) write(rel string, content string, mode os.FileMode) string {
	sb.t.Helper()
	path := rel
	if !filepath.IsAbs(rel) {
		path = filepath.Join(sb.root, rel)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		sb.t.Fatal(err)
	}
	return path
}

func (sb *xraySandbox) run(replaceInPlace bool) (int, string) {
	sb.t.Helper()
	script := renderXrayBinaryRepairScript(defaultFleetXrayVersion, sandboxSHA, replaceInPlace, sb.paths)
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+sb.stubDir+":"+os.Getenv("PATH"), "TMPDIR="+filepath.Join(sb.root, "tmp"))
	for key, value := range sb.env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out)
	}
	sb.t.Fatalf("run installer: %v\n%s", err, out)
	return -1, ""
}

func (sb *xraySandbox) read(path string) string {
	sb.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func (sb *xraySandbox) passwallCalls() string {
	return strings.Join(strings.Fields(sb.read(sb.pwLog)), " ")
}

func TestXrayRepairSandboxInstallsAndRestoresClobberedWrapper(t *testing.T) {
	sb := newXraySandbox(t)
	sb.write(sb.paths.Bin, sandboxOldXray, 0o755)
	sb.write(sb.paths.Wrapper, sandboxClobbered, 0o755)
	sb.env["STUB_PW_RUNNING"] = "0"

	code, out := sb.run(false)
	if code != 0 {
		t.Fatalf("exit %d, want 0:\n%s", code, out)
	}
	if sb.read(sb.paths.Bin) != sandboxNewXray {
		t.Fatalf("binary was not replaced:\n%s", out)
	}
	if got := sb.read(sb.paths.Wrapper); got != vectraXrayWrapperScript {
		t.Fatalf("clobbered wrapper was not restored, got %q", got)
	}
	if _, err := os.Stat(sb.paths.Bin + ".new"); !os.IsNotExist(err) {
		t.Fatalf("staging file left behind")
	}
	if strings.Contains(out, "in place") {
		t.Fatalf("with room to spare the install must go side by side:\n%s", out)
	}
	// The job's post-install step starts PassWall; the script only stops it.
	if got := sb.passwallCalls(); got != "stop" {
		t.Fatalf("passwall calls = %q", got)
	}
}

func TestXrayRepairSandboxFailuresThatKeepTheBinaryRestorePassWall(t *testing.T) {
	cases := map[string]struct {
		mutate   func(*xraySandbox)
		wantCode int
	}{
		"download fails":                          {func(sb *xraySandbox) { sb.env["STUB_CURL_FAIL"] = "1" }, 4},
		"checksum differs":                        {func(sb *xraySandbox) { sb.env["STUB_SHA"] = strings.Repeat("0", 64) }, 5},
		"no xray in zip":                          {func(sb *xraySandbox) { sb.env["STUB_SIZE"] = "" }, 6},
		"no room and the current xray still runs": {func(sb *xraySandbox) { sb.env["STUB_UNZIP_MODE"] = "nospace_while_old" }, 7},
		"new build fails its self-check": {func(sb *xraySandbox) {
			sb.env["STUB_NEW_BIN"] = sb.write("bad-new-xray", "#!/bin/sh\necho 'Xray 1.0.0'\n", 0o755)
		}, 9},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sb := newXraySandbox(t)
			sb.write(sb.paths.Bin, sandboxOldXray, 0o755)
			sb.env["STUB_PW_RUNNING"] = "0" // pgrep finds PassWall
			tc.mutate(sb)

			code, out := sb.run(false)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d:\n%s", code, tc.wantCode, out)
			}
			if sb.read(sb.paths.Bin) != sandboxOldXray {
				t.Fatalf("the current binary must be untouched:\n%s", out)
			}
			if got := sb.passwallCalls(); got != "stop start" {
				t.Fatalf("PassWall was running and must be put back, calls = %q\n%s", got, out)
			}
		})
	}
}

func TestXrayRepairSandboxDoesNotStartAPassWallThatWasStopped(t *testing.T) {
	sb := newXraySandbox(t)
	sb.write(sb.paths.Bin, sandboxOldXray, 0o755)
	sb.env["STUB_PW_RUNNING"] = "1" // pgrep finds nothing
	sb.env["STUB_CURL_FAIL"] = "1"

	if code, out := sb.run(false); code != 4 {
		t.Fatalf("exit %d, want 4:\n%s", code, out)
	}
	if got := sb.passwallCalls(); got != "stop" {
		t.Fatalf("a PassWall that was not running must stay stopped, calls = %q", got)
	}
}

func TestXrayRepairSandboxReplacesInPlaceOnlyWhenAllowed(t *testing.T) {
	cases := map[string]struct {
		current        string
		replaceInPlace bool
	}{
		"current xray no longer runs": {sandboxBrokenXray, false},
		"operator asked for it":       {sandboxOldXray, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sb := newXraySandbox(t)
			sb.write(sb.paths.Bin, tc.current, 0o755)
			sb.env["STUB_UNZIP_MODE"] = "nospace_while_old"

			code, out := sb.run(tc.replaceInPlace)
			if code != 0 {
				t.Fatalf("exit %d, want 0:\n%s", code, out)
			}
			if sb.read(sb.paths.Bin) != sandboxNewXray {
				t.Fatalf("binary was not replaced in place:\n%s", out)
			}
			if !strings.Contains(out, "in place") {
				t.Fatalf("expected the in-place path to be announced:\n%s", out)
			}
		})
	}
}

func TestXrayRepairSandboxInstallsWhereNoXrayIsLeft(t *testing.T) {
	sb := newXraySandbox(t)

	if code, out := sb.run(false); code != 0 || sb.read(sb.paths.Bin) != sandboxNewXray {
		t.Fatalf("exit %d, want the build installed:\n%s", code, out)
	}
}

func TestXrayRepairSandboxInPlaceFailureRestoresThePreviousXray(t *testing.T) {
	sb := newXraySandbox(t)
	sb.write(sb.paths.Bin, sandboxOldXray, 0o755)
	sb.env["STUB_UNZIP_MODE"] = "fail"
	sb.env["STUB_PW_RUNNING"] = "0"

	code, out := sb.run(true)
	if code != 8 {
		t.Fatalf("exit %d, want 8:\n%s", code, out)
	}
	if sb.read(sb.paths.Bin) != sandboxOldXray {
		t.Fatalf("the previous xray must be put back from the /tmp copy:\n%s", out)
	}
	if got := sb.passwallCalls(); got != "stop start" {
		t.Fatalf("with the previous xray back PassWall must be restored, calls = %q", got)
	}
}

func TestXrayRepairSandboxInPlaceWithoutRoomForABackupLeavesPassWallStopped(t *testing.T) {
	sb := newXraySandbox(t)
	sb.write(sb.paths.Bin, sandboxOldXray, 0o755)
	sb.write(sb.paths.Meminfo, "MemAvailable: 20000 kB\n", 0o644)
	sb.env["STUB_UNZIP_MODE"] = "fail"
	sb.env["STUB_PW_RUNNING"] = "0"

	code, out := sb.run(true)
	if code != 8 {
		t.Fatalf("exit %d, want 8:\n%s", code, out)
	}
	if _, err := os.Stat(sb.paths.Bin); !os.IsNotExist(err) {
		t.Fatalf("without a backup there is nothing to restore; expected no binary")
	}
	if got := sb.passwallCalls(); got != "stop" {
		t.Fatalf("with no xray left PassWall must stay stopped, calls = %q", got)
	}
}

func TestXrayRepairSandboxRefusesBeforeStoppingPassWall(t *testing.T) {
	cases := map[string]struct {
		mutate   func(*xraySandbox)
		wantCode int
	}{
		"foreign xray_file": {func(sb *xraySandbox) { sb.env["STUB_XRAY_FILE"] = "/opt/xray" }, 10},
		"not arm64":         {func(sb *xraySandbox) { sb.env["STUB_ARCH"] = "mips" }, 11},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sb := newXraySandbox(t)
			sb.write(sb.paths.Bin, sandboxOldXray, 0o755)
			tc.mutate(sb)

			code, out := sb.run(false)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d:\n%s", code, tc.wantCode, out)
			}
			if got := sb.passwallCalls(); got != "" {
				t.Fatalf("PassWall must not be touched on a refusal, calls = %q", got)
			}
		})
	}
}

func TestXrayRepairSandboxShortcutsWhenAlreadyCurrent(t *testing.T) {
	sb := newXraySandbox(t)
	sb.write(sb.paths.Bin, sandboxNewXray, 0o755)
	sb.env["STUB_CURL_FAIL"] = "1"

	code, out := sb.run(false)
	if code != 0 || !strings.Contains(out, "already installed") {
		t.Fatalf("exit %d, want the already-installed shortcut:\n%s", code, out)
	}
	if got := sb.passwallCalls(); got != "" {
		t.Fatalf("an already-current router must not have PassWall stopped, calls = %q", got)
	}
}
