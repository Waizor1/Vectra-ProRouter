// Package install holds the universal installer, install.sh. Its whole runs
// are proven on real OpenWrt userland in containers (test/install); these
// tests prove its decisions on their own, without Docker: the script is
// sourced (VECTRA_INSTALL_LIB) and its functions are run against a fake
// opkg, xray, wget and df, and feed lists written by the test.
package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// router is the fake router a sourced install.sh runs against.
type router struct {
	t   *testing.T
	dir string
	env []string
}

// The fakes read their answers from files in the router's directory:
// installed and available ("<pkg> - <version>" lines), xray-version, df-free
// (KB), download (what wget fetches); opkg and wget note what they were
// asked in log.
const fakeOpkg = `#!/bin/sh
d="$FAKE"
case "$1" in
list-installed) grep "^$2 - " "$d/installed" 2> /dev/null ;;
list) grep "^$2 - " "$d/available" 2> /dev/null ;;
print-architecture) echo "arch all 1"; echo "arch aarch64_cortex-a53 10" ;;
info) ;;
*) echo "opkg $*" >> "$d/log" ;;
esac
exit 0
`

const fakeXray = `#!/bin/sh
case "$1" in
version) [ -s "$FAKE/xray-version" ] || exit 1; echo "Xray $(cat "$FAKE/xray-version") (Xray, Penetrates Everything.)" ;;
run) exit 0 ;;
esac
`

const fakeWget = `#!/bin/sh
out="" url=""
while [ $# -gt 0 ]; do
	case "$1" in
	-O) out="$2"; shift ;;
	-q) ;;
	-T) shift ;;
	*) url="$1" ;;
	esac
	shift
done
echo "wget $url" >> "$FAKE/log"
[ -f "$FAKE/download" ] || exit 1
cp "$FAKE/download" "$out"
`

const fakeDf = `#!/bin/sh
echo "Filesystem 1K-blocks Used Available Use% Mounted"
echo "overlay 100000 0 $(cat "$FAKE/df-free") 0% /overlay"
`

func newRouter(t *testing.T) *router {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{bin, filepath.Join(dir, "lists"), filepath.Join(dir, "work"), filepath.Join(dir, "opkg")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakes := map[string]string{
		"opkg": fakeOpkg, "xray": fakeXray, "wget": fakeWget, "df": fakeDf,
		// The lists are written plain here: opkg's gzipped ones are read by
		// zcat, which the router has.
		"zcat": "#!/bin/sh\nexit 1\n",
		"ubus": "#!/bin/sh\nexit 0\n",
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		fakes["sha256sum"] = "#!/bin/sh\nexec shasum -a 256 \"$@\"\n"
	}
	for name, body := range fakes {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := &router{t: t, dir: dir}
	r.env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"FAKE="+dir,
		"VECTRA_INSTALL_LIB=1",
		"VECTRA_OPKG_LISTS="+filepath.Join(dir, "lists"),
		"VECTRA_OPKG_CONFS="+filepath.Join(dir, "opkg", "*.conf"),
		"VECTRA_LEGACY_AGENT_MARKER="+filepath.Join(dir, "vault-read-v1"),
		"VECTRA_XRAY_MIN=26.3.27",
	)
	r.write("df-free", "60000")
	return r
}

func (r *router) write(name, body string) {
	r.t.Helper()
	p := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *router) read(name string) string {
	b, _ := os.ReadFile(filepath.Join(r.dir, name))
	return string(b)
}

// feed writes a feed's list, as opkg update leaves it, and its src line.
func (r *router) feed(name, url string, pkgs ...string) {
	r.write(filepath.Join("lists", name), strings.Join(pkgs, "\n\n")+"\n")
	r.write(filepath.Join("opkg", name+".conf"), "src/gz "+name+" "+url+"\n")
}

func pkg(name, version, filename, sum string) string {
	return "Package: " + name + "\nVersion: " + version + "\nDepends: libc\nFilename: " + filename + "\nSHA256sum: " + sum
}

// run sources install.sh and runs script after it, in the router's shell:
// busybox ash on a router, dash here where there is one. It gives what the
// script said and how it exited.
func (r *router) run(script string) (string, int) {
	r.t.Helper()
	shell := "sh"
	if p, err := exec.LookPath("dash"); err == nil {
		shell = p
	}
	src, err := filepath.Abs("install.sh")
	if err != nil {
		r.t.Fatal(err)
	}
	prelude := ". '" + src + "'\nLOG='" + filepath.Join(r.dir, "install.log") + "'\nWORK='" + filepath.Join(r.dir, "work") +
		"'\nARCH=aarch64_cortex-a53\nSTORE=/overlay\n"
	cmd := exec.Command(shell, "-c", prelude+script)
	cmd.Env = r.env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		r.t.Fatal(err)
	}
	return out.String(), code
}

// jsonLines are the --json output: each line a JSON object, ASCII only, each
// short enough for the panel's output filter.
func jsonLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if len(l) > 300 {
			t.Errorf("a line of %d bytes: %s", len(l), l)
		}
		for _, c := range l {
			if c > 0x7e || c < 0x20 {
				t.Errorf("not ASCII: %q", l)
				break
			}
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not a JSON line: %q (%v)", l, err)
		}
		lines = append(lines, m)
	}
	return lines
}

// On the router of 2026-10-03 xray 26.7.28 was on the router without its
// package (PassWall2's, swapped in by hand), and Vectra's feed offered
// xray-core 26.3.27: opkg would have put the older one over it. The
// installer installs the xray-core of the binary's own version first, from
// whichever feed has it, checked against that feed's list.
func TestAnXrayWithoutItsPackageBecomesThePackageOfItsVersion(t *testing.T) {
	r := newRouter(t)
	r.write("xray-version", "26.7.28")
	ipk := "an xray-core package"
	sum := sha256.Sum256([]byte(ipk))
	r.write("download", ipk)
	r.feed("vectra_pro", "https://router.vectra-pro.net/pro/aarch64_cortex-a53",
		pkg("xray-core", "26.3.27-r1", "xray-core_26.3.27-r1_aarch64_cortex-a53.ipk", "00"))
	r.feed("passwall_packages", "https://example.invalid/passwall",
		pkg("chinadns-ng", "2025.08.09-r1", "chinadns-ng.ipk", "11"),
		pkg("xray-core", "26.7.28-r1", "xray-core_26.7.28-r1_aarch64_cortex-a53.ipk", hex.EncodeToString(sum[:])))
	out, code := r.run("plan_xray; echo \"PIN=$XRAY_PIN\"; install_xray_pin")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "PIN=passwall_packages 26.7.28-r1 xray-core_26.7.28-r1_aarch64_cortex-a53.ipk") {
		t.Fatalf("not the package of the binary's version:\n%s", out)
	}
	log := r.read("log")
	if !strings.Contains(log, "wget https://example.invalid/passwall/xray-core_26.7.28-r1_aarch64_cortex-a53.ipk") ||
		!strings.Contains(log, "opkg install "+filepath.Join(r.dir, "work", "pkgs", "xray-core_26.7.28-r1_aarch64_cortex-a53.ipk")) {
		t.Fatalf("what was fetched and installed:\n%s", log)
	}

	// The download is not what the feed's list says: nothing is installed.
	r.write("log", "")
	r.write("download", "something else")
	out, code = r.run("plan_xray; install_xray_pin")
	if code != 1 || !strings.Contains(out, "sha256") || strings.Contains(r.read("log"), "opkg install") {
		t.Fatalf("exit %d, log %q:\n%s", code, r.read("log"), out)
	}
}

// No feed has the binary's version: refused before anything changes, and
// said why — with --json, by code.
func TestAnXrayWithoutAPackageOfItsVersionIsLeftAlone(t *testing.T) {
	r := newRouter(t)
	r.write("xray-version", "26.7.28")
	r.feed("vectra_pro", "https://router.vectra-pro.net/pro/aarch64_cortex-a53",
		pkg("xray-core", "26.3.27-r1", "xray-core_26.3.27-r1_aarch64_cortex-a53.ipk", "00"))
	out, code := r.run("plan_xray")
	if code != 1 || !strings.Contains(out, "нет пакета xray-core 26.7.28") || !strings.Contains(out, "xray не тронут") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	out, code = r.run("JSON=1; plan_xray")
	lines := jsonLines(t, out)
	last := lines[len(lines)-1]
	if code != 1 || last["result"] != "refused" || last["code"] != "XRAY_NO_SAME_VERSION" || last["exit"] != float64(1) {
		t.Fatalf("exit %d: %v", code, lines)
	}
	if strings.Contains(r.read("log"), "install") {
		t.Fatalf("installed something: %s", r.read("log"))
	}

	// Older than Vectra needs: refused too, the binary left as it is.
	r.write("xray-version", "25.1.1")
	if out, code := r.run("plan_xray"); code != 1 || !strings.Contains(out, "26.3.27 или новее") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	// A package already owns xray: opkg's business, as before.
	r.write("installed", "xray-core - 26.7.28-r1\n")
	if out, code := r.run("plan_xray; echo \"PIN=[$XRAY_PIN]\""); code != 0 || !strings.Contains(out, "PIN=[]") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// A first install names vectra-geodata and vectra-reporter itself: without
// them xray refuses every route by country (the router of 2026-10-03, whose
// feed's package did not depend on them).
func TestAFirstInstallBringsTheGeoDataAndTheReporter(t *testing.T) {
	r := newRouter(t)
	r.write("available", "vectra-controller-pro - 0.7.0-r14\nvectra-geodata - 2026.9.28-r2\nvectra-reporter - 1.0.0-r4\n")
	out, code := r.run("install_packages")
	if code != 0 || !strings.Contains(r.read("log"), "opkg install vectra-controller-pro vectra-geodata vectra-reporter") {
		t.Fatalf("exit %d, log:\n%s\n%s", code, r.read("log"), out)
	}
	// A feed without the reporter: the rest all the same.
	r.write("log", "")
	r.write("available", "vectra-controller-pro - 0.7.0-r14\nvectra-geodata - 2026.9.28-r2\n")
	if _, code := r.run("install_packages"); code != 0 || !strings.Contains(r.read("log"), "opkg install vectra-controller-pro vectra-geodata\n") {
		t.Fatalf("exit %d, log:\n%s", code, r.read("log"))
	}
}

// The version check reads the feed's own list, which opkg installed from —
// not the version this file was signed with: «установлена 0.7.0-r13, в фиде
// 0.6.0-r36» was an installer older than its feed.
func TestTheVersionCheckReadsTheFeed(t *testing.T) {
	r := newRouter(t)
	r.write("installed", "vectra-controller-pro - 0.7.0-r13\n")
	r.write("xray-version", "26.7.28")
	r.feed("vectra_pro", "https://router.vectra-pro.net/pro/aarch64_cortex-a53",
		pkg("vectra-controller-pro", "0.7.0-r13", "vectra-controller-pro_0.7.0-r13_aarch64_cortex-a53.ipk", "00"))
	out, _ := r.run("VERSION=0.6.0-r36; STANDBY=1; verify; echo \"WARNINGS=[$WARNINGS]\"")
	if strings.Contains(out, "PKG_VERSION_MISMATCH") || strings.Contains(out, "в фиде 0.6.0-r36") ||
		!strings.Contains(out, "0.7.0-r13 — последняя в фиде") || !strings.Contains(out, "собран при выпуске 0.6.0-r36") {
		t.Fatalf("%s", out)
	}
	r.feed("vectra_pro", "https://router.vectra-pro.net/pro/aarch64_cortex-a53",
		pkg("vectra-controller-pro", "0.7.0-r14", "vectra-controller-pro_0.7.0-r14_aarch64_cortex-a53.ipk", "00"))
	if out, _ := r.run("STANDBY=1; verify; echo \"WARNINGS=[$WARNINGS]\""); !strings.Contains(out, "PKG_VERSION_MISMATCH") {
		t.Fatalf("an older copy than the feed's is not said:\n%s", out)
	}
}

// Free overlay against what Vectra's own update asks (16 MB): said before
// the install from the estimate, and after it from what is left.
func TestOverlayBelowTheUpdateFloorIsAWarning(t *testing.T) {
	r := newRouter(t)
	r.write("xray-version", "26.7.28")
	r.feed("vectra_pro", "https://router.vectra-pro.net/pro/aarch64_cortex-a53",
		pkg("vectra-controller-pro", "0.7.0-r14", "v.ipk", "00")+"\nInstalled-Size: 12000000")
	r.write("df-free", "25000")
	out, code := r.run("check_storage; echo \"WARNINGS=[$WARNINGS]\"")
	if code != 0 || !strings.Contains(out, "WARNINGS=[ OVERLAY_AFTER_BELOW_UPDATE_FLOOR]") || !strings.Contains(out, "«Обновить»") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	r.write("df-free", "80000")
	if out, _ := r.run("check_storage; echo \"WARNINGS=[$WARNINGS]\""); !strings.Contains(out, "WARNINGS=[]") {
		t.Fatalf("room enough, and warned:\n%s", out)
	}
	r.write("df-free", "9000")
	if out, _ := r.run("STANDBY=1; verify; echo \"WARNINGS=[$WARNINGS]\""); !strings.Contains(out, "OVERLAY_BELOW_UPDATE_FLOOR") {
		t.Fatalf("after the install: %s", out)
	}
}

// Exit codes: 0 done with nothing to warn of, 2 done with warnings — named
// in the summary — and 1 not done.
func TestTheExitCodeSaysWarningsFromErrors(t *testing.T) {
	r := newRouter(t)
	for _, tc := range []struct {
		script, says string
		code         int
	}{
		{`conclude "Vectra установлена"`, "Итог: Vectra установлена, без предупреждений (код 0).", 0},
		{`warn GEO_DATA_REJECTED "гео"; warn XRAY_TOO_OLD "xray"; conclude "Vectra установлена"`, "с предупреждениями: GEO_DATA_REJECTED XRAY_TOO_OLD (код 2)", 2},
		{`refuse FEED_UNREACHABLE "фид недоступен"`, "Итог: не установлено, FEED_UNREACHABLE (код 1).", 1},
		{`fail PKG_INSTALL "не прошёл"`, "Итог: не установлено, PKG_INSTALL (код 1).", 1},
	} {
		out, code := r.run(tc.script)
		if code != tc.code || !strings.Contains(out, tc.says) {
			t.Errorf("%s: exit %d, want %d saying %q:\n%s", tc.script, code, tc.code, tc.says, out)
		}
	}
	out, code := r.run(`JSON=1; warn GEO_DATA_REJECTED "гео-данные не те"; conclude "Vectra установлена"`)
	lines := jsonLines(t, out)
	if code != 2 || len(lines) != 2 || lines[0]["check"] != "GEO_DATA_REJECTED" || lines[0]["level"] != "warn" ||
		lines[1]["result"] != "warnings" || lines[1]["exit"] != float64(2) {
		t.Fatalf("exit %d: %v", code, lines)
	}
}

// The old agent: only next to one that knows Vectra (0.1.13-r45, its
// vault-read-v1 marker); and without --standby, --check says why it is
// needed, checks the rest as with it, and refuses at the end.
func TestTheLegacyAgent(t *testing.T) {
	r := newRouter(t)
	r.write("installed", "vectra-controller-agent - 0.1.13-r43\n")
	out, code := r.run("STANDBY=1; check_conflicts")
	if code != 1 || !strings.Contains(out, "0.1.13-r45 или новее") || !strings.Contains(out, "обновите агента") {
		t.Fatalf("an agent older than r45: exit %d:\n%s", code, out)
	}
	// The marker says it knows Vectra, whatever its version reads.
	r.write("vault-read-v1", "")
	if out, code := r.run("STANDBY=1; check_conflicts"); code != 0 {
		t.Fatalf("an agent with the marker: exit %d:\n%s", code, out)
	}
	_ = os.Remove(filepath.Join(r.dir, "vault-read-v1"))
	r.write("installed", "vectra-controller-agent - 0.1.13-r45\n")
	out, code = r.run("check_conflicts")
	if code != 1 || !strings.Contains(out, "нужен --standby") || !strings.Contains(out, "как один и тот же роутер") {
		t.Fatalf("install without --standby: exit %d:\n%s", code, out)
	}
	out, code = r.run("MODE=check; check_conflicts; echo \"BLOCKER=$BLOCKER STANDBY=$STANDBY\"")
	if code != 0 || !strings.Contains(out, "нужен --standby") || !strings.Contains(out, "BLOCKER=LEGACY_AGENT_NEEDS_STANDBY STANDBY=1") {
		t.Fatalf("--check without --standby: exit %d:\n%s", code, out)
	}
	out, _ = r.run("MODE=check; JSON=1; check_conflicts")
	if lines := jsonLines(t, out); len(lines) == 0 || lines[0]["check"] != "LEGACY_AGENT_NEEDS_STANDBY" || lines[0]["value"] != "0.1.13-r45" {
		t.Fatalf("%v", lines)
	}
}

// --json values are made ASCII and escaped, whatever they carry.
func TestJSONValuesAreASCII(t *testing.T) {
	r := newRouter(t)
	out, _ := r.run(`JSON=1; jcheck ok ROUTER 'OpenWrt 24.10.4 "netis" \ nx31 — роутер'`)
	lines := jsonLines(t, out)
	if len(lines) != 1 || lines[0]["value"] != `OpenWrt 24.10.4 "netis" \ nx31  ` {
		t.Fatalf("%q", out)
	}
}
