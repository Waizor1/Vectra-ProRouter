package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vectra-controller-agent/internal/controlplane"
)

// The fixture lays out a router filesystem under a temp dir -- /usr/bin/xray,
// the Vectra wrapper, sing-box, /etc/config/passwall2, the geodata, the opkg
// control file and PassWall's log -- and answers UCI reads from a map, so the
// detection runs against real stat() semantics (missing file, exec bit, mtime)
// without touching the host.
//
// Every file is written with the same "installed" mtime, well before the
// logged attempts, unless a test moves it. The router's clock runs in a zone
// the controller does not know (MSK unless a test says otherwise): log stamps
// are wall-clock, and the log file's mtime is the UTC instant of its last line,
// exactly as on a router.
type proxyRuntimeFixture struct {
	root     string
	uci      map[string]string
	uciReads int
	zone     time.Duration
	config   string
}

var fixtureInstalledAt = time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

const fixtureConfig = `
config global
	option enabled '1'
	option node 'myshunt'

config global_app
	option xray_file '/usr/bin/xray'

config global_rules
	option v2ray_location_asset 'ASSET_DIR'

config nodes 'myshunt'
	option type 'Xray'
	option protocol '_shunt'

config nodes 'pl2'
	option type 'Xray'
	option address 'pl2.example.net'
	option port '443'
`

func newProxyRuntimeFixture(t *testing.T) *proxyRuntimeFixture {
	t.Helper()

	previousLogPath := passwallLogPath
	previousConfigPath := passwallConfigPath
	previousBinary := xrayPackageBinaryPath
	previousWrapper := vectraXrayWrapperPath
	previousFallbacks := xrayFallbackBinaryPaths
	previousSingBox := singBoxFallbackBinaryPaths
	previousUCI := readProxyRuntimeUCI
	previousInfoDir := opkgInfoDir
	previousStatusFile := opkgStatusFile
	t.Cleanup(func() {
		passwallLogPath = previousLogPath
		passwallConfigPath = previousConfigPath
		xrayPackageBinaryPath = previousBinary
		vectraXrayWrapperPath = previousWrapper
		xrayFallbackBinaryPaths = previousFallbacks
		singBoxFallbackBinaryPaths = previousSingBox
		readProxyRuntimeUCI = previousUCI
		opkgInfoDir = previousInfoDir
		opkgStatusFile = previousStatusFile
	})

	root := t.TempDir()
	fixture := &proxyRuntimeFixture{
		root: root,
		uci:  map[string]string{"passwall2.myshunt.type": "Xray"},
		zone: 3 * time.Hour,
	}
	passwallLogPath = filepath.Join(root, "tmp/log/passwall2.log")
	passwallConfigPath = filepath.Join(root, "etc/config/passwall2")
	xrayPackageBinaryPath = filepath.Join(root, "usr/bin/xray")
	vectraXrayWrapperPath = filepath.Join(root, "usr/sbin/vectra-xray-wrapper")
	xrayFallbackBinaryPaths = []string{filepath.Join(root, "bin/xray"), xrayPackageBinaryPath}
	singBoxFallbackBinaryPaths = []string{filepath.Join(root, "usr/bin/sing-box")}
	opkgInfoDir = filepath.Join(root, "usr/lib/opkg/info")
	opkgStatusFile = filepath.Join(root, "usr/lib/opkg/status")
	readProxyRuntimeUCI = func(key string) string {
		fixture.uciReads++
		return fixture.uci[key]
	}

	assetDir := filepath.Join(root, "usr/share/v2ray")
	fixture.config = strings.ReplaceAll(fixtureConfig, "ASSET_DIR", assetDir)
	fixture.writeFile(t, passwallConfigPath, fixture.config, 0o644)
	fixture.writeFile(t, filepath.Join(assetDir, "geosite.dat"), "geosite 202609200000", 0o644)
	fixture.writeFile(t, filepath.Join(assetDir, "geoip.dat"), "geoip 202609200000", 0o644)
	fixture.setPasswallVersion(t, "26.4.10-r1")
	return fixture
}

// writeFile writes a file with the fixture's "installed" mtime.
func (f *proxyRuntimeFixture) writeFile(t *testing.T, path string, content string, mode os.FileMode) {
	t.Helper()
	f.writeFileAt(t, path, content, mode, fixtureInstalledAt)
}

func (f *proxyRuntimeFixture) writeFileAt(t *testing.T, path string, content string, mode os.FileMode, modTime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func (f *proxyRuntimeFixture) touch(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func (f *proxyRuntimeFixture) installXray(t *testing.T, content string) {
	f.writeFile(t, xrayPackageBinaryPath, content, 0o755)
}

func (f *proxyRuntimeFixture) installWrapperScript(t *testing.T) {
	f.writeFile(t, vectraXrayWrapperPath, "#!/bin/sh\nexport GOMEMLIMIT=40MiB\nexec /usr/bin/xray \"$@\"\n", 0o755)
	f.uci["passwall2.@global_app[0].xray_file"] = vectraXrayWrapperPath
}

// clobberWrapper is PassWall's LuCI updater writing a raw xray binary over
// whatever xray_file names -- here, the wrapper.
func (f *proxyRuntimeFixture) clobberWrapper(t *testing.T, content string) {
	f.writeFile(t, vectraXrayWrapperPath, "\x7fELF"+content, 0o755)
	f.uci["passwall2.@global_app[0].xray_file"] = vectraXrayWrapperPath
}

// writeLog writes PassWall's log; its mtime is the UTC instant of the last
// stamped line, stamped in the fixture's zone.
func (f *proxyRuntimeFixture) writeLog(t *testing.T, parts ...string) {
	t.Helper()
	content := strings.Join(parts, "")
	modTime := fixtureInstalledAt
	for _, line := range strings.Split(content, "\n") {
		if stamp, ok := passwallLogStamp(strings.TrimSpace(line)); ok {
			modTime = stamp.Add(-f.zone)
		}
	}
	f.writeFileAt(t, passwallLogPath, content, 0o644, modTime)
}

func (f *proxyRuntimeFixture) setPasswallVersion(t *testing.T, version string) {
	f.writeFile(t, filepath.Join(opkgInfoDir, "luci-app-passwall2.control"), "Package: luci-app-passwall2\nVersion: "+version+"\n", 0o644)
}

func (f *proxyRuntimeFixture) rewriteConfig(t *testing.T, content string, modTime time.Time) {
	f.writeFileAt(t, passwallConfigPath, content, 0o644, modTime)
}

func (f *proxyRuntimeFixture) evaluate(memory *ProxyRuntimeStartFailure) (controlplane.RouterSafetyEvent, bool) {
	return f.evaluateAt(memory, time.Date(2026, 9, 29, 1, 0, 0, 0, time.UTC))
}

func (f *proxyRuntimeFixture) evaluateAt(memory *ProxyRuntimeStartFailure, observedAt time.Time) (controlplane.RouterSafetyEvent, bool) {
	return proxyRuntimeUnusableSafetyEvent(
		controlplane.RouterInventory{SelectedNodeID: "myshunt"},
		observedAt,
		memory,
	)
}

// The start/stop shapes below are the ones app.sh writes, and the ones
// andrey-avito's /tmp/log/passwall2.log showed on 2026-09-28/29.

const incidentStartFailure = `Failed to start: main: failed to load config files: [/tmp/etc/passwall2/acl/default/global.json] > infra/conf: failed to build outbound config with tag dns-out > common/errors: The feature outbound "proxySettings" has been removed and migrated to "streamSettings.sockopt.dialerProxy". Please update your config(s) according to release note and documentation.`

func failedPasswallStart(at string) string {
	return at + ": Clearing and closing related programs and cache complete.\n" +
		at + ": [Global] process /tmp/etc/passwall2/acl/default/global.json error, skip this transparent proxy!\n" +
		"Xray 26.9.9 (Xray, Penetrates Everything.) Custom (go1.25.1 linux/arm64)\n" +
		incidentStartFailure + "\n" +
		at + ": Running in no proxy mode, it only allows scheduled tasks for starting and stopping services.\n" +
		at + ": Running complete!\n\n"
}

func successfulPasswallStart(at string) string {
	return at + ": Clearing and closing related programs and cache complete.\n" +
		at + ": DNS: 127.0.0.1#15353\n" +
		at + ": Starting to load nftables firewall rules...\n" +
		at + ": nftables firewall rules load complete!\n" +
		at + ": Running complete!\n\n"
}

// directPasswallStart is PassWall starting with enabled=0 -- what our own
// fallback to direct produces.
func directPasswallStart(at string) string {
	return at + ": Clearing and closing related programs and cache complete.\n" +
		at + ": Running in no proxy mode, it only allows scheduled tasks for starting and stopping services.\n" +
		at + ": Running complete!\n\n"
}

// aclFailedPasswallStart is an ACL instance failing its config test while the
// global proxy starts fine; app.sh appends the ACL's xray output to the same
// log.
func aclFailedPasswallStart(at string) string {
	return at + ": Clearing and closing related programs and cache complete.\n" +
		at + ": DNS: 127.0.0.1#15353\n" +
		at + ":     - [kids-tv] process /tmp/etc/passwall2/acl/pl2_TCP_UDP_DNS_1041.json error, skip this transparent proxy!\n" +
		"Failed to start: main: failed to load config files: [/tmp/etc/passwall2/acl/pl2_TCP_UDP_DNS_1041.json] > infra/conf: invalid domain rule: geosite:no-such-code\n" +
		at + ": nftables firewall rules load complete!\n" +
		at + ": Running complete!\n\n"
}

// The translated lines below are the po/ru and po/fa msgstr, byte for byte.
func russianFailedPasswallStart(at string) string {
	return at + ": 【Глобальный режим】 ошибка процесса /tmp/etc/passwall2/acl/default/global.json, этот прозрачный прокси пропускается!\n" +
		incidentStartFailure + "\n" +
		at + ": Работа в режиме без прокси: разрешены только задачи автозапуска и автоотключения службы.\n" +
		at + ": Выполнение завершено!\n"
}

func farsiFailedPasswallStart(at string) string {
	return at + ": خطای فرآیند [سراسری] /tmp/etc/passwall2/acl/default/global.json، از این پروکسی شفاف صرف‌نظر کنید!\n" +
		incidentStartFailure + "\n" +
		at + ": در حالت بدون پروکسی اجرا می‌شود، فقط اجازه وظایف زمان‌بندی شده برای شروع و توقف سرویس‌ها را می‌دهد.\n" +
		at + ": اجرا کامل شد!\n"
}

func TestProxyRuntimeUnusableSafetyEvent(t *testing.T) {
	cases := []struct {
		name         string
		setup        func(t *testing.T, f *proxyRuntimeFixture)
		wantEvent    bool
		wantSource   string
		wantMessage  string
		wantEvidence string
	}{
		{
			// andrey-avito, 2026-09-29: xray_file still named the wrapper script
			// after /usr/bin/xray vanished, so PassWall armed its interception
			// over nothing.
			name: "wrapper script configured but the binary it execs is missing",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installWrapperScript(t)
			},
			wantEvent:    true,
			wantSource:   "filesystem",
			wantMessage:  "usr/bin/xray missing",
			wantEvidence: "is missing or not executable",
		},
		{
			name:        "default xray_file and /usr/bin/xray missing",
			setup:       func(t *testing.T, f *proxyRuntimeFixture) {},
			wantEvent:   true,
			wantSource:  "filesystem",
			wantMessage: "usr/bin/xray missing",
		},
		{
			name: "explicit /usr/bin/xray xray_file that is not executable",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.writeFile(t, xrayPackageBinaryPath, "half-written", 0o644)
				f.uci["passwall2.@global_app[0].xray_file"] = xrayPackageBinaryPath
			},
			wantEvent:   true,
			wantSource:  "filesystem",
			wantMessage: "usr/bin/xray missing",
		},
		{
			// first_type() falls back to /usr/bin/xray, so a stale xray_file breaks nothing.
			name: "stale xray_file with /usr/bin/xray present",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.7.28")
				f.uci["passwall2.@global_app[0].xray_file"] = filepath.Join(f.root, "opt/xray-gone")
			},
		},
		{
			name: "wrapper script with the binary it execs present",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installWrapperScript(t)
				f.installXray(t, "xray 26.7.28")
			},
		},
		{
			// PassWall's LuCI updater wrote a working xray over the wrapper path:
			// that file is the runtime now, /usr/bin/xray is irrelevant.
			name: "wrapper path holds an xray binary and /usr/bin/xray is gone",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.clobberWrapper(t, "xray 26.7.28")
			},
		},
		{
			// app.sh runs an xray-typed node on sing-box when no xray resolves.
			name: "no xray anywhere but sing-box runs the node",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.writeFile(t, singBoxFallbackBinaryPaths[0], "sing-box 1.12", 0o755)
			},
		},
		{
			// XRAY_BIN resolves to the wrapper script, so app.sh runs xray, not sing-box.
			name: "sing-box present does not rescue a wrapper script with no xray behind it",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installWrapperScript(t)
				f.writeFile(t, singBoxFallbackBinaryPaths[0], "sing-box 1.12", 0o755)
			},
			wantEvent:   true,
			wantSource:  "filesystem",
			wantMessage: "usr/bin/xray missing",
		},
		{
			// andrey-avito, 2026-09-28: upstream xray 26.9.9 refusing PassWall 26.4.10's config.
			name: "last start attempt failed in the global instance",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installWrapperScript(t)
				f.installXray(t, "xray 26.9.9")
				f.writeLog(t, successfulPasswallStart("2026-09-28 20:00:00"), failedPasswallStart("2026-09-28 23:34:23"))
			},
			wantEvent:    true,
			wantSource:   ProxyRuntimeStartFailureSource,
			wantMessage:  "last PassWall proxy start failed",
			wantEvidence: "Failed to start: main: failed to load config files",
		},
		{
			name: "last start attempt succeeded after an earlier failure",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.7.28")
				f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"), successfulPasswallStart("2026-09-29 01:31:32"))
			},
		},
		{
			// Our own fallback restarts PassWall with enabled=0. That start says
			// nothing about the runtime and must not erase the failure behind it.
			name: "direct-mode restart after a failure keeps the failure",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.9.9")
				f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"), directPasswallStart("2026-09-28 23:35:40"))
			},
			wantEvent:    true,
			wantSource:   ProxyRuntimeStartFailureSource,
			wantEvidence: "Failed to start:",
		},
		{
			name: "a start still in progress is not judged",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.9.9")
				f.writeLog(t,
					successfulPasswallStart("2026-09-29 01:31:32"),
					"2026-09-29 01:40:00: [Global] process /tmp/etc/passwall2/acl/default/global.json error, skip this transparent proxy!\n",
					incidentStartFailure+"\n",
				)
			},
		},
		{
			// A failed ACL instance does not stop the global proxy.
			name: "an ACL instance failing does not condemn the router",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.7.28")
				f.writeLog(t, aclFailedPasswallStart("2026-09-28 23:34:23"))
			},
		},
		{
			name: "a failure line not tied to the global instance is ignored",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.7.28")
				f.writeLog(t,
					"2026-09-28 23:34:20: Clearing and closing related programs and cache complete.\n",
					incidentStartFailure+"\n",
					"2026-09-28 23:34:23: Running complete!\n",
				)
			},
		},
		{
			name: "Russian LuCI log",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.9.9")
				f.writeLog(t, russianFailedPasswallStart("2026-09-28 23:34:23"))
			},
			wantEvent:    true,
			wantSource:   ProxyRuntimeStartFailureSource,
			wantEvidence: "Failed to start:",
		},
		{
			name: "Farsi LuCI log",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.9.9")
				f.writeLog(t, farsiFailedPasswallStart("2026-09-28 23:34:23"))
			},
			wantEvent:    true,
			wantSource:   ProxyRuntimeStartFailureSource,
			wantEvidence: "Failed to start:",
		},
		{
			name: "failure far behind a long log is still found in the tail",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.9.9")
				filler := strings.Repeat(successfulPasswallStart("2026-09-28 12:00:00"), 1+passwallLogTailBytes/150)
				f.writeLog(t, filler, failedPasswallStart("2026-09-28 23:34:23"))
			},
			wantEvent:    true,
			wantSource:   ProxyRuntimeStartFailureSource,
			wantEvidence: "Failed to start:",
		},
		{
			name: "non-xray runtime",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.uci["passwall2.myshunt.type"] = "sing-box"
				f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newProxyRuntimeFixture(t)
			tc.setup(t, f)

			event, ok := f.evaluate(&ProxyRuntimeStartFailure{})
			if ok != tc.wantEvent {
				t.Fatalf("event reported = %v, want %v (event %+v)", ok, tc.wantEvent, event)
			}
			if !ok {
				return
			}
			if event.Type != ProxyRuntimeUnusableEventType || event.Severity != "critical" || event.Component != "xray" {
				t.Fatalf("event = %+v, want critical %s for xray", event, ProxyRuntimeUnusableEventType)
			}
			if event.Source != tc.wantSource {
				t.Fatalf("event source = %q, want %q", event.Source, tc.wantSource)
			}
			if !strings.Contains(event.Message, tc.wantMessage) {
				t.Fatalf("event message = %q, want it to contain %q", event.Message, tc.wantMessage)
			}
			if !strings.Contains(event.Evidence, tc.wantEvidence) {
				t.Fatalf("event evidence = %q, want it to contain %q", event.Evidence, tc.wantEvidence)
			}
			if tc.wantSource == "filesystem" && !strings.HasPrefix(event.Evidence, xrayPackageBinaryPath) {
				t.Fatalf("event evidence = %q, want it to lead with the missing path %q", event.Evidence, xrayPackageBinaryPath)
			}
		})
	}
}

func TestProxyRuntimeUnusableSafetyEventReportsTheFailedStartLine(t *testing.T) {
	f := newProxyRuntimeFixture(t)
	f.installXray(t, "xray 26.9.9")
	f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"))

	event, ok := f.evaluate(nil)
	if !ok {
		t.Fatal("expected the failed start to be reported")
	}
	if want := truncateSafetyEvidence(incidentStartFailure); event.Evidence != want {
		t.Fatalf("evidence = %q, want the failed start line %q", event.Evidence, want)
	}
}

// Consumers time their periodic retry of a failed start from when it was
// first seen, so the event must keep that time across collections.
func TestProxyRuntimeUnusableSafetyEventKeepsTheFirstSeenTime(t *testing.T) {
	f := newProxyRuntimeFixture(t)
	f.installXray(t, "xray 26.9.9")
	f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"))

	memory := ProxyRuntimeStartFailure{}
	firstSeen := time.Date(2026, 9, 28, 20, 35, 0, 0, time.UTC)
	if _, ok := f.evaluateAt(&memory, firstSeen); !ok {
		t.Fatal("expected the failed start to be reported")
	}
	event, ok := f.evaluateAt(&memory, firstSeen.Add(6*time.Hour))
	if !ok {
		t.Fatal("expected the failed start to still be reported")
	}
	if want := firstSeen.Format(time.RFC3339); event.ObservedAt != want {
		t.Fatalf("event observedAt = %q, want the first-seen time %q", event.ObservedAt, want)
	}
}

// The collector runs every cycle on 234 MB routers; a healthy router must not
// pay a single UCI subprocess for this check.
func TestProxyRuntimeUnusableSafetyEventSpawnsNothingOnAHealthyRouter(t *testing.T) {
	f := newProxyRuntimeFixture(t)
	f.installWrapperScript(t)
	f.installXray(t, "xray 26.7.28")
	f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"), successfulPasswallStart("2026-09-29 01:31:32"))

	if event, ok := f.evaluate(&ProxyRuntimeStartFailure{}); ok {
		t.Fatalf("did not expect an event on a healthy router, got %+v", event)
	}
	if f.uciReads != 0 {
		t.Fatalf("healthy router spent %d UCI read(s), want none", f.uciReads)
	}
}

func TestProxyRuntimeStartFailureMemoryFollowsTheRuntime(t *testing.T) {
	repairedAt := time.Date(2026, 9, 29, 9, 15, 0, 0, time.UTC)
	cases := []struct {
		name string
		// setup installs the runtime the failure happens on; the default is the
		// wrapper script in front of /usr/bin/xray.
		setup        func(t *testing.T, f *proxyRuntimeFixture)
		change       func(t *testing.T, f *proxyRuntimeFixture)
		wantUnusable bool
	}{
		{
			name:         "runtime unchanged",
			change:       func(t *testing.T, f *proxyRuntimeFixture) {},
			wantUnusable: true,
		},
		{
			// The nightly reboot wipes /tmp; it does not repair xray.
			name: "log wiped by a reboot, runtime unchanged",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				if err := os.Remove(passwallLogPath); err != nil {
					t.Fatalf("remove log: %v", err)
				}
			},
			wantUnusable: true,
		},
		{
			name: "only direct-mode starts since, after a reboot",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.writeLog(t, directPasswallStart("2026-09-29 04:31:10"))
			},
			wantUnusable: true,
		},
		{
			// Our own fallback commits enabled=0, and libuci re-serialises the
			// file on commit; neither changes what xray is given.
			name: "our own fallback to direct committed enabled=0",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				recommitted := strings.ReplaceAll(f.config, "option enabled '1'", "option enabled '0'")
				recommitted = strings.ReplaceAll(recommitted, "\t", "    ")
				recommitted = strings.ReplaceAll(recommitted, "'", "\"")
				f.rewriteConfig(t, recommitted, repairedAt)
			},
			wantUnusable: true,
		},
		{
			// PassWall's stop() deletes dnsmasq_dns_redirect, start() sets it.
			name: "PassWall toggled dnsmasq_dns_redirect",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				toggled := strings.Replace(f.config, "option node 'myshunt'", "option node 'myshunt'\n\toption dnsmasq_dns_redirect '1'", 1)
				f.rewriteConfig(t, toggled, repairedAt)
			},
			wantUnusable: true,
		},
		{
			name: "binary replaced with a different size",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installXray(t, "xray 26.7.28 official XTLS build")
			},
		},
		{
			name: "binary replaced with the same size but a new mtime",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.touch(t, xrayPackageBinaryPath, repairedAt)
			},
		},
		{
			name: "PassWall upgraded",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.setPasswallVersion(t, "26.8.10-r1")
			},
		},
		{
			name: "binary replaced while the log was wiped",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				if err := os.Remove(passwallLogPath); err != nil {
					t.Fatalf("remove log: %v", err)
				}
				f.installXray(t, "xray 26.7.28 official XTLS build")
			},
		},
		{
			name: "a later proxy start succeeded",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"), successfulPasswallStart("2026-09-29 01:31:32"))
			},
		},
		{
			// The failure happened on a raw xray the LuCI updater wrote over the
			// wrapper path; the repair restores the wrapper script and leaves
			// /usr/bin/xray untouched.
			name: "wrapper script restored over a clobbered binary, /usr/bin/xray untouched",
			setup: func(t *testing.T, f *proxyRuntimeFixture) {
				f.clobberWrapper(t, "xray 26.9.9")
				f.installXray(t, "xray 26.7.28")
			},
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.installWrapperScript(t)
			},
		},
		{
			// A subscription refresh, a route-policy or config apply rewrote a node
			// xray refused; no binary changed.
			name: "a node in the config was rewritten",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.rewriteConfig(t, strings.ReplaceAll(f.config, "pl2.example.net", "pl2-new.example.net"), repairedAt)
			},
		},
		{
			// A missing geosite code is fixed by a rules refresh or compact_geodata.
			name: "geosite.dat refreshed",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.writeFileAt(t, filepath.Join(f.root, "usr/share/v2ray/geosite.dat"), "geosite 202609290500", 0o644, repairedAt)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newProxyRuntimeFixture(t)
			if tc.setup != nil {
				tc.setup(t, f)
			} else {
				f.installWrapperScript(t)
				f.installXray(t, "xray 26.9.9")
			}
			f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"))

			memory := ProxyRuntimeStartFailure{}
			if _, ok := f.evaluate(&memory); !ok {
				t.Fatalf("expected the failed start to be reported when first seen (memory %+v)", memory)
			}
			if !strings.HasSuffix(memory.Attempt, "Running complete!") ||
				!strings.HasPrefix(memory.Evidence, "Failed to start:") ||
				!strings.Contains(memory.Runtime, "size=") ||
				!strings.Contains(memory.Runtime, "luci-app-passwall2 26.4.10-r1") ||
				!strings.Contains(memory.Runtime, "passwall2 config ") ||
				!strings.Contains(memory.Runtime, "geosite ") ||
				memory.Retired {
				t.Fatalf("memory did not record the failed attempt and its runtime: %+v", memory)
			}

			tc.change(t, f)

			event, ok := f.evaluate(&memory)
			if ok != tc.wantUnusable {
				t.Fatalf("unusable = %v, want %v (event %+v, memory %+v)", ok, tc.wantUnusable, event, memory)
			}
			if ok && !strings.HasPrefix(event.Evidence, "Failed to start:") {
				t.Fatalf("remembered verdict lost its evidence: %+v", event)
			}
		})
	}
}

// At rollout the last attempt in a log can predate a manual fix. Pinning it to
// the fixed runtime would strand a working router in direct.
func TestProxyRuntimeStartFailureIgnoresAnAttemptThatPredatesTheRuntime(t *testing.T) {
	cases := []struct {
		name string
		// zone is the router's clock; the attempt below is stamped in it.
		zone         time.Duration
		stamp        string
		change       func(t *testing.T, f *proxyRuntimeFixture)
		wantUnusable bool
	}{
		{
			name:         "runtime installed before the attempt",
			zone:         3 * time.Hour,
			stamp:        "2026-09-28 23:34:23",
			change:       func(t *testing.T, f *proxyRuntimeFixture) {},
			wantUnusable: true,
		},
		{
			// 21:00 UTC is 00:00 MSK, 26 minutes after the attempt; read as UTC the
			// stamp would look 2.5 hours later than the fix.
			name:  "binary replaced after the attempt on an MSK router",
			zone:  3 * time.Hour,
			stamp: "2026-09-28 23:34:23",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.touch(t, xrayPackageBinaryPath, time.Date(2026, 9, 28, 21, 0, 0, 0, time.UTC))
			},
		},
		{
			name:  "config rewritten after the attempt",
			zone:  3 * time.Hour,
			stamp: "2026-09-28 23:34:23",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.rewriteConfig(t, f.config, time.Date(2026, 9, 29, 6, 0, 0, 0, time.UTC))
			},
		},
		{
			// 15:34 at UTC-5 is 20:34 UTC, after the 18:00 UTC install; read as UTC
			// the stamp would look older than the binary and hide a real failure.
			name:  "binary installed before the attempt on a UTC-5 router",
			zone:  -5 * time.Hour,
			stamp: "2026-09-28 15:34:23",
			change: func(t *testing.T, f *proxyRuntimeFixture) {
				f.touch(t, xrayPackageBinaryPath, time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC))
			},
			wantUnusable: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newProxyRuntimeFixture(t)
			f.zone = tc.zone
			f.installXray(t, "xray 26.9.9")
			tc.change(t, f)
			f.writeLog(t, failedPasswallStart(tc.stamp))

			memory := ProxyRuntimeStartFailure{}
			event, ok := f.evaluate(&memory)
			if ok != tc.wantUnusable {
				t.Fatalf("unusable = %v, want %v (event %+v, memory %+v)", ok, tc.wantUnusable, event, memory)
			}
			if tc.wantUnusable {
				return
			}
			if memory.Attempt == "" || !memory.Retired {
				t.Fatalf("expected the stale attempt to be remembered as retired, got %+v", memory)
			}
			f.uciReads = 0
			if _, ok := f.evaluate(&memory); ok {
				t.Fatal("a retired attempt must not come back")
			}
			if f.uciReads != 0 {
				t.Fatalf("a retired attempt still cost %d UCI read(s) per collection", f.uciReads)
			}
		})
	}
}

// A router that stays direct for other reasons after its runtime was repaired
// must not keep paying for the old verdict.
func TestProxyRuntimeStartFailureRetiredMemoryCostsNothing(t *testing.T) {
	f := newProxyRuntimeFixture(t)
	f.installXray(t, "xray 26.9.9")
	f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"), directPasswallStart("2026-09-28 23:35:40"))

	memory := ProxyRuntimeStartFailure{}
	if _, ok := f.evaluate(&memory); !ok {
		t.Fatal("expected the failed start to be reported")
	}
	f.installXray(t, "xray 26.7.28 official XTLS build")
	if _, ok := f.evaluate(&memory); ok {
		t.Fatal("expected the verdict to be retired once the binary changed")
	}
	if !memory.Retired {
		t.Fatalf("expected the memory to be marked retired, got %+v", memory)
	}

	f.uciReads = 0
	for range 3 {
		if _, ok := f.evaluate(&memory); ok {
			t.Fatal("a retired verdict must not come back")
		}
	}
	if f.uciReads != 0 {
		t.Fatalf("retired memory cost %d UCI read(s), want none", f.uciReads)
	}
}

// After the binary is replaced the old failure is retired; if the new binary
// fails too, that is a new attempt and is recorded against the new runtime.
func TestProxyRuntimeStartFailureMemoryRecordsANewFailureOnTheNewRuntime(t *testing.T) {
	f := newProxyRuntimeFixture(t)
	f.installXray(t, "xray 26.9.9")
	f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"))

	memory := ProxyRuntimeStartFailure{}
	if _, ok := f.evaluate(&memory); !ok {
		t.Fatal("expected the first failure to be reported")
	}
	firstRuntime := memory.Runtime

	f.installXray(t, "xray 26.9.10 still incompatible")
	if _, ok := f.evaluate(&memory); ok {
		t.Fatal("expected the old failure to be retired once the binary changed")
	}

	f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"), failedPasswallStart("2026-09-29 12:20:05"))
	if _, ok := f.evaluate(&memory); !ok {
		t.Fatal("expected the new runtime's own failed start to be reported")
	}
	if memory.Runtime == firstRuntime || memory.Retired || !strings.Contains(memory.Attempt, "2026-09-29 12:20:05") {
		t.Fatalf("memory was not moved to the new attempt and runtime: %+v", memory)
	}
}

func TestProxyRuntimeStartFailureMemoryIsForgottenAfterASuccessfulStart(t *testing.T) {
	f := newProxyRuntimeFixture(t)
	f.installXray(t, "xray 26.9.9")
	f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"))

	memory := ProxyRuntimeStartFailure{}
	if _, ok := f.evaluate(&memory); !ok {
		t.Fatal("expected the failed start to be reported")
	}

	f.writeLog(t, failedPasswallStart("2026-09-28 23:34:23"), successfulPasswallStart("2026-09-29 01:31:32"))
	if _, ok := f.evaluate(&memory); ok {
		t.Fatal("did not expect an event after a successful proxy start")
	}
	if memory != (ProxyRuntimeStartFailure{}) {
		t.Fatalf("expected the memory to be forgotten, got %+v", memory)
	}
}

func TestPasswallConfigDigestIgnoresFormattingAndSelfFlippingSwitches(t *testing.T) {
	base := "config global\n\toption enabled '1'\n\toption node 'myshunt'\n"
	cases := []struct {
		name      string
		content   string
		wantEqual bool
	}{
		{name: "re-quoted and re-indented", content: "config global\n    option enabled \"1\"\n    option node \"myshunt\"\n\n", wantEqual: true},
		{name: "enabled flipped", content: "config global\n\toption enabled '0'\n\toption node 'myshunt'\n", wantEqual: true},
		{name: "dnsmasq_dns_redirect added", content: base + "\toption dnsmasq_dns_redirect '1'\n", wantEqual: true},
		{name: "node changed", content: "config global\n\toption enabled '1'\n\toption node 'pl2'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := passwallConfigDigest(tc.content) == passwallConfigDigest(base); got != tc.wantEqual {
				t.Fatalf("digest equal = %v, want %v", got, tc.wantEqual)
			}
		})
	}
}
