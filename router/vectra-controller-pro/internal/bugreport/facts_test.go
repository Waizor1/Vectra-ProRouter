package bugreport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"vectra-controller-pro/internal/vault"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFactsReadTheRouter(t *testing.T) {
	root := tree(t, map[string]string{
		"etc/vectra-controller-pro/state.json":            `{"device_identifier":"vectra-0123456789ab"}`,
		"usr/lib/opkg/info/vectra-controller-pro.control": "Package: vectra-controller-pro\nVersion: 0.6.0-r19\n",
		"etc/openwrt_release":                             "DISTRIB_RELEASE='24.10.6'\nDISTRIB_ARCH='aarch64_cortex-a53'\n",
		"tmp/sysinfo/model":                               "Xiaomi Mi Router AX3000T\n",
		"proc/uptime":                                     "38207.12 70000.00\n",
		"proc/meminfo":                                    "MemTotal: 239720 kB\nMemAvailable: 98696 kB\n",
		"etc/config/vectra-controller-pro":                "config controller 'main'\n\toption route_source 'native'\n",
		"etc/vectra-controller-pro/xray-desired.json":     `{"schema":1,"inbounds":{"tproxy":{"listenIP":"0.0.0.0","port":12345,"killSwitch":true}}}`,
	})
	for _, name := range []string{"etc/vectra-controller-pro/state.json", "etc/vectra-controller-pro/xray-desired.json"} {
		path := filepath.Join(root, name)
		if err := vault.MigrateFile(path, func([]byte) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	r, log := Facts(FactsEnv{Root: root, Reporter: "1.0.0-r1",
		XrayVersion: func() string { return "26.7.28" },
		Logread: func() []string {
			return []string{"daemon.info dnsmasq[1]: ok", "daemon.warn vctl[12]: fetch https://sub.example.com/x failed"}
		}})
	if r.DeviceID != "vectra-0123456789ab" || r.Vctl != "0.6.0-r19" || r.OpenWrt != "24.10.6" || r.Arch != "aarch64_cortex-a53" ||
		r.Model != "Xiaomi Mi Router AX3000T" || r.UptimeSec != 38207 || r.MemTotalMiB != 234 || r.MemAvailableMiB != 96 ||
		r.RouteSource != "native" || r.Xray != "26.7.28" || r.Reporter != "1.0.0-r1" || r.KillSwitch == nil || !*r.KillSwitch {
		t.Fatalf("%+v", r)
	}
	if len(log) != 1 || log[0] != "daemon.warn vctl[12]: fetch <url> failed" {
		t.Fatalf("log %q", log)
	}
}

// The journal is taken by the syslog tag of what logs it — vctl, its init
// script, the dead-man, the reporter — not by a mention: cron logs every run
// of the dead-man with its path, "/usr/libexec/vectra-controller-pro/...",
// once a minute, and on 1111 those took 69 of a report's 80 lines. And what
// others say of vctl: procd of its instance (a crash loop, a kill), the
// kernel of an OOM kill of vctl or xray.
func TestFactsTakeTheJournalByTag(t *testing.T) {
	_, log := Facts(FactsEnv{Root: t.TempDir(), Logread: func() []string {
		return []string{
			"Tue Sep 29 20:06:00 2026 cron.err crond[2058]: USER root pid 10010 cmd /usr/libexec/vectra-controller-pro/deadman.sh >/dev/null 2>&1",
			"Tue Sep 29 20:07:54 2026 user.notice vectra-controller-pro: data plane unloaded (table inet vctl, fwmark 0x1, table 100)",
			"Tue Sep 29 20:07:57 2026 daemon.err vectra-controller-agent[10533]: run once failed",
			"Tue Sep 29 20:08:00 2026 daemon.err vctl[11171]: time=2026-09-29T17:08:00Z level=ERROR msg=\"xray exited\"",
			"Tue Sep 29 20:08:01 2026 cron.err crond[2058]: USER root pid 10011 cmd /usr/sbin/vectra-reporter run >/dev/null 2>&1",
			"Tue Sep 29 20:09:00 2026 daemon.warn vectra-controller-pro-deadman: vctl should run and does not (down 1 minutes)",
			"Tue Sep 29 20:09:05 2026 user.notice root: looked at vectra-controller-pro by hand",
			"Tue Sep 29 20:09:06 2026 daemon.err vectra-reporter: the spool is full",
			"Tue Sep 29 20:10:00 2026 daemon.info procd: Instance vectra-controller-pro::instance1 s in a crash loop 6 crashes, 0 seconds since last crash",
			"Tue Sep 29 20:10:01 2026 daemon.info procd: Instance dnsmasq::cfg01411c s in a crash loop 6 crashes, 0 seconds since last crash",
			"Tue Sep 29 20:10:02 2026 daemon.info procd: Instance vectra-controller-pro::instance1 pid 11171 not stopped on SIGTERM, sending SIGKILL instead",
			"Tue Sep 29 20:10:03 2026 kern.err kernel: [ 5012.3] Out of memory: Killed process 11252 (xray) total-vm:1262000kB, anon-rss:52000kB",
			"Tue Sep 29 20:10:04 2026 kern.err kernel: [ 5013.3] Out of memory: Killed process 1502 (hostapd) total-vm:9000kB, anon-rss:3000kB",
		}
	}})
	want := []string{
		"Tue Sep 29 20:07:54 2026 user.notice vectra-controller-pro: data plane unloaded (table inet vctl, fwmark 0x1, table 100)",
		"Tue Sep 29 20:08:00 2026 daemon.err vctl[11171]: time=2026-09-29T17:08:00Z level=ERROR msg=\"xray exited\"",
		"Tue Sep 29 20:09:00 2026 daemon.warn vectra-controller-pro-deadman: vctl should run and does not (down 1 minutes)",
		"Tue Sep 29 20:09:06 2026 daemon.err vectra-reporter: the spool is full",
		"Tue Sep 29 20:10:00 2026 daemon.info procd: Instance vectra-controller-pro::instance1 s in a crash loop 6 crashes, 0 seconds since last crash",
		"Tue Sep 29 20:10:02 2026 daemon.info procd: Instance vectra-controller-pro::instance1 pid 11171 not stopped on SIGTERM, sending SIGKILL instead",
		"Tue Sep 29 20:10:03 2026 kern.err kernel: [ 5012.3] Out of memory: Killed process 11252 (xray) total-vm:1262000kB, anon-rss:52000kB",
	}
	if strings.Join(log, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s", strings.Join(log, "\n"))
	}
}
