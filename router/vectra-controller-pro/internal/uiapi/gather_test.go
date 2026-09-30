package uiapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vectra-controller-pro/internal/power"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestParseXrayLine(t *testing.T) {
	msk := time.FixedZone("+0300", 3*3600)
	l := ParseXrayLine("2026/09/27 16:33:18.749926 [Warning] app/observatory/burst: error ping x with bridge-fr5", msk)
	if l.Level != "warn" || *l.Source != "xray" || *l.Time != "2026-09-27T13:33:18Z" || l.Message != "app/observatory/burst: error ping x with bridge-fr5" {
		t.Fatalf("%+v %s", l, *l.Time)
	}
	raw := ParseXrayLine("A unified platform for anti-censorship.", msk)
	if raw.Level != "unknown" || raw.Time != nil || raw.Message == "" {
		t.Fatalf("unparsed line = %+v", raw)
	}
}

func TestParseLogreadLinePrefersSlogTimeAndLevel(t *testing.T) {
	l, ok := ParseLogreadLine(`Sat Sep 27 16:33:18 2026 daemon.err vctl[4127]: time=2026-09-27T13:33:18.5Z level=INFO msg="check-in ok"`, time.UTC)
	if !ok || l.Level != "info" || *l.Time != "2026-09-27T13:33:18Z" || *l.Source != "vctl" {
		t.Fatalf("%+v", l)
	}
	l, ok = ParseLogreadLine(`Sat Sep  7 06:03:08 2026 daemon.warn vectra-controller-pro: plain line`, time.UTC)
	if !ok || l.Level != "warn" || l.Time == nil || *l.Time != "2026-09-07T06:03:08Z" {
		t.Fatalf("%+v", l)
	}
}

// Anyone who can write one syslog line — a failed LuCI login's username is
// logged — must not be able to forge the controller's lines.
func TestOnlyTheControllersOwnTagReachesTheJournal(t *testing.T) {
	for _, forged := range []string{
		`Sat Sep 27 16:33:18 2026 authpriv.notice luci: failed login on / for vctl time=2026-09-27T23:59:00Z level=ERROR msg="call support"`,
		`Sat Sep 27 16:33:18 2026 daemon.info dnsmasq-dhcp[1]: DHCPACK(br-lan) 10.0.0.9 vctl-laptop`,
		`Sat Sep 27 16:33:18 2026 daemon.err vctlx[1]: level=ERROR msg=x`,
	} {
		if _, ok := ParseLogreadLine(forged, time.UTC); ok {
			t.Errorf("a line from another tag reached the journal: %s", forged)
		}
	}
	if _, ok := ParseLogreadLine(`Sat Sep 27 16:33:18 2026 daemon.info vectra-controller-pro.instance1[9]: ok`, time.UTC); !ok {
		t.Error("the procd instance tag was refused")
	}
}

func TestUntimedLinesStayWhereTheyWerePrinted(t *testing.T) {
	x, v := "xray", "vctl"
	ts := func(s string) *string { return &s }
	lines := []LogLine{
		{Time: ts("2026-09-27T09:00:00Z"), Source: &x, Message: "x1"},
		{Time: nil, Source: &x, Message: "x-banner"},
		{Time: ts("2026-09-27T08:00:01Z"), Source: &v, Message: "v1"},
	}
	sortLogLines(lines)
	got := lines[0].Message + "," + lines[1].Message + "," + lines[2].Message
	if got != "v1,x1,x-banner" {
		t.Fatalf("order = %s, want the 08:00 line first and the banner after its own stream's line", got)
	}
}

func TestProcessMemoryReadsRSSAndTheWrapperLimit(t *testing.T) {
	proc := t.TempDir()
	write(t, filepath.Join(proc, "42", "status"), "Name:\txray\nVmRSS:\t   62874 kB\n")
	write(t, filepath.Join(proc, "42", "environ"), "PATH=/usr/bin\x00GOMEMLIMIT=80MiB\x00GOGC=30\x00")
	rss, limit := processMemory(proc, 42)
	if rss == nil || *rss != 61.4 || limit == nil || *limit != 80 {
		t.Fatalf("rss=%v limit=%v", rss, limit)
	}
	for in, want := range map[string]int{"80MiB": 80, "1GiB": 1024, "83886080": 80, "81920KiB": 80} {
		if got := parseMemLimit(in); got == nil || *got != want {
			t.Errorf("parseMemLimit(%q) = %v, want %d", in, got, want)
		}
	}
	if parseMemLimit("lots") != nil {
		t.Error("parsed garbage")
	}
}

func TestLegacyAndPasswallDetection(t *testing.T) {
	rc := t.TempDir()
	if legacyAgentEnabled(rc) {
		t.Fatal("empty rc.d reported the agent enabled")
	}
	write(t, filepath.Join(rc, "S99vectra-controller-pro"), "")
	if legacyAgentEnabled(rc) {
		t.Fatal("vctl's own link was taken for the legacy agent")
	}
	write(t, filepath.Join(rc, "S99vectra-controller"), "")
	if !legacyAgentEnabled(rc) {
		t.Fatal("the legacy agent's link was missed")
	}

	proc := t.TempDir()
	write(t, filepath.Join(proc, "100", "cmdline"), "/usr/bin/xray\x00run\x00-c\x00/var/run/vectra-controller-pro/xray.json\x00")
	if power.PassWallRunning(proc) {
		t.Fatal("vctl's xray taken for PassWall")
	}
	// Its subscription run is not its stack (internal/power's test has the rest).
	write(t, filepath.Join(proc, "150", "cmdline"), "lua\x00/usr/share/passwall2/subscribe.lua\x00start\x00")
	if power.PassWallRunning(proc) {
		t.Fatal("subscribe.lua taken for PassWall's stack")
	}
	// PassWall runs its binaries from its temp bin directory.
	write(t, filepath.Join(proc, "200", "cmdline"), "/tmp/etc/passwall2/bin/xray\x00run\x00-c\x00/tmp/etc/passwall2/acl/default/global.json\x00")
	if !power.PassWallRunning(proc) {
		t.Fatal("PassWall's xray missed")
	}
}

func TestRouterFactsFromProc(t *testing.T) {
	dir := t.TempDir()
	env := Env{ProcDir: filepath.Join(dir, "proc"), ModelF: filepath.Join(dir, "model"), Release: filepath.Join(dir, "rel"), Overlay: dir, Tmp: dir}
	write(t, filepath.Join(env.ProcDir, "meminfo"), "MemTotal:         239616 kB\nMemFree: 1 kB\nMemAvailable:      90112 kB\n")
	write(t, filepath.Join(env.ProcDir, "loadavg"), "0.31 0.24 0.18 1/97 4242\n")
	write(t, filepath.Join(env.ProcDir, "uptime"), "93211.42 180000.00\n")
	write(t, env.ModelF, "Xiaomi Mi Router AX3000T\n")
	write(t, env.Release, "DISTRIB_ID='OpenWrt'\nDISTRIB_DESCRIPTION='OpenWrt 24.10.6 r28427-6df0e3d02a'\n")
	f := routerFacts(env)
	if *f.MemTotalMiB != 234 || *f.MemAvailableMiB != 88 || *f.UptimeSec != 93211 || len(f.Load) != 3 || f.Load[0] != 0.31 {
		t.Fatalf("%+v", f)
	}
	if f.Model != "Xiaomi Mi Router AX3000T" || f.Release != "OpenWrt 24.10.6 r28427-6df0e3d02a" || f.OverlayFreeMiB == nil {
		t.Fatalf("%+v", f)
	}
}

func TestNftCountersAndAMissingTable(t *testing.T) {
	dir := t.TempDir()
	nft := filepath.Join(dir, "nft")
	write(t, nft, `#!/bin/sh
cat <<'J'
{"nftables":[{"metainfo":{"version":"1.1.1"}},{"counter":{"family":"inet","name":"vctl_tproxy_hits","table":"vctl","handle":3,"packets":184213,"bytes":9}},{"counter":{"family":"inet","name":"vctl_would_leak","table":"vctl","handle":4,"packets":0,"bytes":0}}]}
J
`)
	c, loaded := NftCounters(context.Background(), Env{Nft: nft, CallTime: 2 * time.Second})
	if !loaded || c["vctl_tproxy_hits"] != 184213 || len(c) != 2 {
		t.Fatalf("counters=%v loaded=%v", c, loaded)
	}
	write(t, nft, "#!/bin/sh\necho 'Error: No such file or directory' >&2\nexit 1\n")
	if c, loaded := NftCounters(context.Background(), Env{Nft: nft, CallTime: 2 * time.Second}); loaded || c != nil {
		t.Fatalf("a missing table read as loaded: %v", c)
	}
}

func TestXrayBannerIsNotAJournalLine(t *testing.T) {
	for _, s := range []string{"Xray 26.3.27 (Xray, Penetrates Everything.) d2758a0 (go1.26.1 linux/arm64)", "A unified platform for anti-censorship."} {
		if !isXrayBanner(s) {
			t.Errorf("banner not recognised: %q", s)
		}
	}
	if isXrayBanner("2026/09/27 17:52:39.155186 [Warning] core: Xray 26.3.27 started") {
		t.Error("the 'started' log line was taken for the banner")
	}
}
