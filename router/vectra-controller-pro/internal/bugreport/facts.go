package bugreport

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/routepolicy"
	"vectra-controller-pro/internal/state"
)

// FactsEnv is where the facts are read: Root is "/" on a router.
type FactsEnv struct {
	Root        string
	Reporter    string
	XrayVersion func() string
	Logread     func() []string
}

// Facts is what every report says about the router, and vctl's last lines
// in the system log (its own and its init script's), redacted.
func Facts(e FactsEnv) (Router, []string) {
	p := func(rel string) string { return filepath.Join(e.Root, rel) }
	var r Router
	if st, err := state.Load(p("etc/vectra-controller-pro/state.json")); err == nil {
		r.DeviceID = st.DeviceIdentifier
	}
	r.Vctl = field(p("usr/lib/opkg/info/vectra-controller-pro.control"), "Version:")
	r.OpenWrt = strings.Trim(field(p("etc/openwrt_release"), "DISTRIB_RELEASE="), `'"`)
	r.Arch = strings.Trim(field(p("etc/openwrt_release"), "DISTRIB_ARCH="), `'"`)
	if b, err := os.ReadFile(p("tmp/sysinfo/model")); err == nil {
		r.Model = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(p("proc/uptime")); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				r.UptimeSec = int64(v)
			}
		}
	}
	if mi, err := memguard.ReadFrom(p("proc/meminfo")); err == nil {
		r.MemTotalMiB, r.MemAvailableMiB = memguard.MiB(mi.TotalKB), memguard.MiB(mi.AvailableKB)
	}
	if b, err := os.ReadFile(p("etc/config/vectra-controller-pro")); err == nil {
		if secs, err := routepolicy.ParseUCI(string(b)); err == nil {
			for _, s := range secs {
				if s.Name == "main" {
					r.RouteSource = s.Get("route_source")
				}
			}
		}
	}
	if c, err := config.Load(p("etc/vectra-controller-pro/xray-desired.json")); err == nil && c.Inbounds.Tproxy != nil {
		ks := c.Inbounds.Tproxy.KillSwitch
		r.KillSwitch = &ks
	}
	r.Reporter = e.Reporter
	if e.XrayVersion != nil {
		r.Xray = e.XrayVersion()
	}
	var log []string
	if e.Logread != nil {
		for _, l := range e.Logread() {
			if reOwnTag.MatchString(l) || reOfVctl.MatchString(l) {
				log = append(log, l)
			}
		}
	}
	return r, tail(RedactLines(log), MaxLog)
}

// reOwnTag is the syslog tag of what logs for Vectra — vctl, its init
// script, the dead-man, the reporter — in a logread line ("... daemon.err
// vctl[123]: ..."). A mention is not enough: cron logs every run of the
// dead-man with its path, once a minute.
var reOwnTag = regexp.MustCompile(`(?:^|\s)(?:vctl|vectra-controller-pro|vectra-controller-pro-deadman|vectra-reporter)(?:\[\d+\])?:\s`)

// reOfVctl is what others log of vctl: procd of its instance (a crash loop,
// a kill after SIGTERM), the kernel of an OOM kill of vctl or its xray.
var reOfVctl = regexp.MustCompile(`\sprocd: (?:Instance|Service) vectra-controller-pro\b|\skernel: .*Killed process \d+ \((?:vctl|xray)\)`)

// field is the rest of the first line of path that starts with prefix.
func field(path, prefix string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(l, prefix))
		}
	}
	return ""
}
