package retire

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A fleet router's /usr/lib/opkg/status, as opkg writes it: PassWall2
// 26.8.10 with its Chinese translation and every helper, xray-core, and the
// packages PassWall2 pulled in that other software needs too.
const fleetStatus = `Package: libc
Version: 1.2.5-r4
Depends: libgcc1
Status: install hold installed
Essential: yes
Architecture: aarch64_cortex-a53
Installed-Time: 1733000000

Package: luci-app-passwall2
Version: 26.8.10-r1
Depends: libc, coreutils, coreutils-base64, coreutils-nohup, coreutils-timeout, curl, ip-full, libuci-lua, lua, luci-compat, luci-lib-jsonc, lyaml, resolveip, tcping, geoview, v2ray-geoip, v2ray-geosite, unzip, luci-lua-runtime
Status: install user installed
Architecture: all
Conffiles:
 /etc/config/passwall2 0c5c5b0f1b1a8b6d2d0f3d4b5a6c7d8e
 /etc/config/passwall2_server 1c5c5b0f1b1a8b6d2d0f3d4b5a6c7d8e
Installed-Time: 1790000000

Package: luci-i18n-passwall2-zh-cn
Version: 26.8.10-r1
Depends: libc, luci-app-passwall2
Status: install user installed
Architecture: all
Installed-Time: 1790000000

Package: chinadns-ng
Version: 2025.08.09-r1
Depends: libc
Status: install ok installed
Architecture: aarch64_cortex-a53
Installed-Time: 1790000000

Package: geoview
Version: 0.2.6-r1
Depends: libc
Status: install ok installed
Architecture: aarch64_cortex-a53
Installed-Time: 1790000000

Package: tcping
Version: 0.3-r1
Depends: libc
Status: install ok installed
Architecture: aarch64_cortex-a53
Installed-Time: 1790000000

Package: v2ray-geoip
Version: 202509050054.1-r1
Status: install ok installed
Architecture: all
Installed-Time: 1790000000

Package: v2ray-geosite
Version: 20250905011304-r1
Status: install ok installed
Architecture: all
Installed-Time: 1790000000

Package: xray-core
Version: 26.3.27-r1
Depends: libc, ca-bundle
Status: install user installed
Architecture: aarch64_cortex-a53
Installed-Time: 1790000000

Package: dnsmasq-full
Version: 2.90-r4
Depends: libc, libubus20250102, libnettle8, kmod-ipt-ipset, libnetfilter-conntrack3, jsonfilter
Provides: dnsmasq
Status: install user installed
Architecture: aarch64_cortex-a53
Installed-Time: 1790000000

Package: vectra-controller-pro
Version: 0.6.0-r37
Depends: libc, ca-bundle, jsonfilter, jshn, procd, procd-ujail, uci, xray-core (>= 26.3.27), kmod-nft-tproxy, kmod-nft-socket, kmod-nft-nat, dnsmasq-full, rpcd-mod-iwinfo, vectra-geodata, vectra-reporter
Status: install user installed
Architecture: aarch64_cortex-a53
Installed-Time: 1790000000

Package: luci-app-podkop-old
Version: 0.1-r1
Status: deinstall ok not-installed
Depends: tcping
Architecture: all
`

func parseFleet(t *testing.T, extra string) map[string]Package {
	t.Helper()
	return ParseStatus([]byte(fleetStatus + "\n" + extra))
}

func TestTheStatusFileNamesWhatIsInstalledAndWhatItNeeds(t *testing.T) {
	got := parseFleet(t, "")
	if _, ok := got["luci-app-podkop-old"]; ok {
		t.Fatal("a package opkg lists as not-installed was taken for installed")
	}
	app, ok := got[App]
	if !ok {
		t.Fatalf("%s missing from %v", App, keys(got))
	}
	for _, want := range []string{"tcping", "geoview", "v2ray-geoip", "v2ray-geosite", "ip-full"} {
		if !contains(app.Depends, want) {
			t.Errorf("%s's Depends lost %q: %v", App, want, app.Depends)
		}
	}
	if vctl := got["vectra-controller-pro"]; !contains(vctl.Depends, "xray-core") {
		t.Errorf("a versioned dependency kept its version: %v", vctl.Depends)
	}
	if full := got["dnsmasq-full"]; !reflect.DeepEqual(full.Provides, []string{"dnsmasq"}) {
		t.Errorf("dnsmasq-full provides %v, want [dnsmasq]", full.Provides)
	}
}

// Alternatives count as needs, as opkg counts them when it refuses a removal.
func TestAnAlternativeIsANeed(t *testing.T) {
	got := ParseStatus([]byte("Package: a\nDepends: b | c (>= 1), d:any\nStatus: install ok installed\n"))
	if want := []string{"b", "c", "d"}; !reflect.DeepEqual(got["a"].Depends, want) {
		t.Fatalf("Depends %v, want %v", got["a"].Depends, want)
	}
}

// The fleet router: everything on the list goes, the translation before
// PassWall2 and PassWall2 before what it depends on — opkg refuses to take a
// package another installed one still needs.
func TestAFleetRoutersPassWallGoesWhole(t *testing.T) {
	p := MakePlan(parseFleet(t, ""), nil)
	want := []string{"luci-i18n-passwall2-zh-cn", App, "chinadns-ng", "geoview", "tcping", "v2ray-geoip", "v2ray-geosite"}
	if !reflect.DeepEqual(p.Remove, want) {
		t.Fatalf("remove %v, want %v", p.Remove, want)
	}
	if len(p.Keep) != 0 {
		t.Fatalf("kept %v", p.Keep)
	}
}

// Only what is installed: a router without chinadns-ng and v2ray-geosite.
func TestOnlyWhatIsInstalledGoes(t *testing.T) {
	st := parseFleet(t, "")
	delete(st, "chinadns-ng")
	delete(st, "v2ray-geosite")
	p := MakePlan(st, nil)
	want := []string{"luci-i18n-passwall2-zh-cn", App, "geoview", "tcping", "v2ray-geoip"}
	if !reflect.DeepEqual(p.Remove, want) {
		t.Fatalf("remove %v, want %v", p.Remove, want)
	}
}

// Every translation of PassWall2 goes with it, whatever its language.
func TestEveryTranslationGoes(t *testing.T) {
	p := MakePlan(parseFleet(t, "Package: luci-i18n-passwall2-ru\nDepends: libc, luci-app-passwall2\nStatus: install user installed\n"), nil)
	if want := []string{"luci-i18n-passwall2-ru", "luci-i18n-passwall2-zh-cn", App}; len(p.Remove) < 3 || !reflect.DeepEqual(p.Remove[:3], want) {
		t.Fatalf("remove %v, want it to start with %v", p.Remove, want)
	}
}

// A helper another installed package needs stays, and says why.
func TestAHelperSomethingElseNeedsStays(t *testing.T) {
	p := MakePlan(parseFleet(t, "Package: my-monitor\nDepends: libc, tcping\nStatus: install user installed\n"), nil)
	if contains(p.Remove, "tcping") {
		t.Fatalf("tcping removed though my-monitor needs it: %v", p.Remove)
	}
	if !strings.Contains(p.Keep["tcping"], "my-monitor") {
		t.Fatalf("keep %v, want tcping kept for my-monitor", p.Keep)
	}
	if !contains(p.Remove, App) || !contains(p.Remove, "geoview") {
		t.Fatalf("the rest should still go: %v", p.Remove)
	}
}

// A need by a name a helper provides is a need of the helper.
func TestANeedByAProvidedNameCounts(t *testing.T) {
	extra := "Package: my-monitor\nDepends: libc, ping-tool\nStatus: install user installed\n"
	st := parseFleet(t, extra)
	tc := st["tcping"]
	tc.Provides = []string{"ping-tool"}
	st["tcping"] = tc
	if p := MakePlan(st, nil); contains(p.Remove, "tcping") {
		t.Fatalf("tcping provides what my-monitor needs, yet goes: %v", p.Remove)
	}
}

// PassWall2 itself needed by another package: it stays, and with it
// everything it needs.
func TestPassWallSomethingElseNeedsStaysWithItsNeeds(t *testing.T) {
	p := MakePlan(parseFleet(t, "Package: luci-theme-passwall\nDepends: luci-app-passwall2\nStatus: install user installed\n"), nil)
	if !strings.Contains(p.Keep[App], "luci-theme-passwall") {
		t.Fatalf("keep %v, want %s kept for luci-theme-passwall", p.Keep, App)
	}
	for _, dep := range []string{"geoview", "tcping", "v2ray-geoip", "v2ray-geosite"} {
		if contains(p.Remove, dep) {
			t.Errorf("%s goes though the PassWall2 that stays needs it: %v", dep, p.Remove)
		}
	}
}

// What the caller protects stays (vctl reads v2ray-geoip's file).
func TestAProtectedPackageStays(t *testing.T) {
	p := MakePlan(parseFleet(t, ""), map[string]string{"v2ray-geoip": "vctl reads /usr/share/v2ray/geoip.dat"})
	if contains(p.Remove, "v2ray-geoip") || p.Keep["v2ray-geoip"] != "vctl reads /usr/share/v2ray/geoip.dat" {
		t.Fatalf("remove %v keep %v", p.Remove, p.Keep)
	}
	if !contains(p.Remove, "v2ray-geosite") {
		t.Fatalf("v2ray-geosite should still go: %v", p.Remove)
	}
}

// Dependents first even when the list's own order would be wrong.
func TestTheOrderFollowsTheNeeds(t *testing.T) {
	st := parseFleet(t, "")
	tc := st["tcping"]
	tc.Depends = append(tc.Depends, "v2ray-geosite")
	st["tcping"] = tc
	g := st["v2ray-geoip"]
	g.Depends = append(g.Depends, "chinadns-ng")
	st["v2ray-geoip"] = g
	p := MakePlan(st, nil)
	pos := map[string]int{}
	for i, n := range p.Remove {
		pos[n] = i
	}
	if !(pos["v2ray-geoip"] < pos["chinadns-ng"] && pos["tcping"] < pos["v2ray-geosite"]) {
		t.Fatalf("a package removed before one that needs it: %v", p.Remove)
	}
	if len(p.Remove) != 7 {
		t.Fatalf("remove %v", p.Remove)
	}
}

// The list never names what the router needs whatever PassWall2 pulled in:
// xray-core, dnsmasq, curl, ip-full, coreutils, kernel modules, Lua, the
// rest of LuCI, CA certificates.
func TestTheListNeverNamesWhatTheRouterNeeds(t *testing.T) {
	for _, name := range []string{"xray-core", "dnsmasq", "dnsmasq-full", "curl", "ip-full", "coreutils", "coreutils-nohup",
		"kmod-nft-tproxy", "lua", "luci-compat", "luci-base", "luci-lib-jsonc", "ca-bundle", "ca-certificates", "libuci-lua", "unzip"} {
		if !neverRemove(name) {
			t.Errorf("%s is not guarded", name)
		}
		if listed(name) {
			t.Errorf("%s is on the removal list", name)
		}
	}
	for _, name := range []string{App, "luci-i18n-passwall2-ru", "chinadns-ng", "geoview", "tcping", "v2ray-geoip", "v2ray-geosite"} {
		if neverRemove(name) || !listed(name) {
			t.Errorf("%s: guarded %v, listed %v", name, neverRemove(name), listed(name))
		}
	}
	for _, n := range MakePlan(parseFleet(t, ""), nil).Remove {
		if neverRemove(n) {
			t.Fatalf("%s on the plan", n)
		}
	}
}

func TestInstalledReadsTheStatusFile(t *testing.T) {
	dir := t.TempDir()
	env := Env{StatusFile: filepath.Join(dir, "status")}
	if _, err := env.Installed(); err == nil {
		t.Fatal("a missing status file read as a router with nothing installed")
	}
	if err := os.WriteFile(env.StatusFile, []byte(fleetStatus), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := env.Installed()
	if err != nil || len(got) != 11 {
		t.Fatalf("installed %d (%v), err %v", len(got), keys(got), err)
	}
}

func keys(m map[string]Package) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
