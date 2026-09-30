// vectra-reporter sends Vectra's bug reports (ADR-0007): cron runs `run`
// every minute; the init script runs `boot` at boot and `shutdown` on the
// way down; `test` sends a TEST report now; `status` shows the spool.
// Nothing here needs vctl running, or vctl's binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/bugreport"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/incident"
	"vectra-controller-pro/internal/routepolicy"
	"vectra-controller-pro/internal/routersign"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/subscription"
)

// Version is stamped at build time.
var Version = "dev"

const (
	defaultURL = "https://api-app.vectra-pro.net/errors/router"
	configPath = "/etc/config/vectra-reporter"
	baseDir    = "/etc/vectra-reporter"
	lockPath   = "/var/lock/vectra-reporter.lock"
)

type cfg struct {
	enabled bool
	url     string
}

func readConfig(path string) cfg {
	c := cfg{enabled: true, url: defaultURL}
	b, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	secs, err := routepolicy.ParseUCI(string(b))
	if err != nil {
		return c
	}
	for _, s := range secs {
		if s.Name != "main" {
			continue
		}
		switch s.Get("enabled") {
		case "0", "off", "false", "no":
			c.enabled = false
		}
		if u := s.Get("url"); u != "" {
			c.url = u
		}
	}
	return c
}

// xrayRunning: a process runs vctl's render (`… run -c /var/run/vectra-controller-pro/…`).
func xrayRunning(procDir string) bool {
	dirs, _ := filepath.Glob(filepath.Join(procDir, "[0-9]*"))
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "cmdline"))
		if err != nil {
			continue
		}
		if strings.Contains(strings.ReplaceAll(string(b), "\x00", " "), "run -c /var/run/vectra-controller-pro/") {
			return true
		}
	}
	return false
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// commandOK runs a command and says whether it succeeded — within timeout:
// the run holds the reporter's lock, and a command that hangs (nft stuck on
// netlink) must not keep it for good.
func commandOK(timeout time.Duration, name string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Run() == nil
}

func out(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, _ := exec.CommandContext(ctx, name, args...).Output()
	return string(b)
}

func env(c cfg) bugreport.Env {
	st, _ := state.Load("/etc/vectra-controller-pro/state.json")
	dev := subscription.ReadDeviceFacts()
	vctl := strings.TrimSpace(strings.TrimPrefix(firstLine("/usr/lib/opkg/info/vectra-controller-pro.control", "Version:"), "Version:"))
	return bugreport.Env{
		Root: "/", Reporter: Version,
		SpoolDir: filepath.Join(baseDir, "spool"), StatePath: filepath.Join(baseDir, "state.json"),
		InboxDir: incident.Dir, CrashDir: incident.CrashDir,
		Now: time.Now, Alive: alive,
		Dmesg: func() string { return out("dmesg") },
		BootID: func() string {
			b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
			return strings.TrimSpace(string(b))
		},
		TableLoaded: func() bool { return commandOK(10*time.Second, "nft", "-t", "list", "table", "inet", "vctl") },
		XrayRunning: func() bool { return xrayRunning("/proc") },
		Facts: func() (bugreport.Router, []string) {
			return bugreport.Facts(bugreport.FactsEnv{Root: "/", Reporter: Version,
				XrayVersion: func() string {
					f := strings.Fields(out("xray", "version"))
					if len(f) > 1 {
						return f[1]
					}
					return ""
				},
				Logread: func() []string { return strings.Split(out("logread", "-l", "300"), "\n") }})
		},
		Sender: bugreport.Sender{URL: c.url, Client: controlplane.MarkedHTTPClient(firewall.DefaultControlMark, 20*time.Second),
			Sign: routersign.Signer(st, vctl, time.Now), HWID: dev.HWID, Model: dev.Model, OSRelease: dev.OSRelease},
		Budget: 50 * time.Second,
	}
}

func firstLine(path, prefix string) string {
	b, _ := os.ReadFile(path)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// lock: one reporter at a time; a second run just goes.
func lock() (*os.File, bool) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, false
	}
	return f, true
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: vectra-reporter run|boot|shutdown|test|status")
		os.Exit(2)
	}
	// When memory runs out, the kernel takes this before anything that
	// carries the router.
	_ = os.WriteFile("/proc/self/oom_score_adj", []byte("500"), 0o644)
	c := readConfig(configPath)
	var err error
	switch os.Args[1] {
	case "shutdown":
		err = os.MkdirAll(baseDir, 0o700)
		if err == nil {
			err = os.WriteFile(filepath.Join(baseDir, "clean-shutdown"), nil, 0o600)
		}
	case "boot":
		for _, d := range []string{incident.Dir, incident.CrashDir} {
			_ = os.MkdirAll(d, 0o755)
		}
		marker := filepath.Join(baseDir, "clean-shutdown")
		if _, err := os.Stat(filepath.Join(baseDir, "state.json")); err != nil {
			// Its first boot: nothing it could have seen go down. (A mark
			// left at the install would hide an unclean first reboot.)
			_ = os.Remove(marker)
			break
		}
		if in := bugreport.Boot(marker, "/sys/fs/pstore", time.Now()); in != nil && c.enabled {
			// Into the inbox: the next run spools and sends it, with the
			// facts of a router that is up.
			incident.NewRecorder(incident.Dir, time.Minute).Record(*in)
		}
	case "run":
		if !c.enabled {
			return
		}
		f, ok := lock()
		if !ok {
			return
		}
		defer f.Close()
		_, _, err = bugreport.Run(context.Background(), env(c))
	case "test":
		if !c.enabled {
			err = errors.New("switched off (uci vectra-reporter.main.enabled)")
			break
		}
		e := env(c)
		st := bugreport.LoadState(e.StatePath)
		r, log := e.Facts()
		if err = bugreport.Queue(e, &st, incident.Incident{Code: "TEST", Key: "test " + strconv.FormatInt(time.Now().Unix(), 10),
			Title: "a test report from vectra-reporter", Source: "reporter"}, r, log); err == nil {
			_ = st.Save(e.StatePath)
			var sent int
			_, sent, err = bugreport.Run(context.Background(), e)
			if err == nil && sent == 0 {
				err = errors.New("queued; not sent yet: see `vectra-reporter status`")
			}
		}
	case "status":
		e := env(c)
		for _, en := range (bugreport.Spool{Dir: e.SpoolDir, MaxFiles: bugreport.SpoolFiles, MaxBytes: bugreport.SpoolBytes}).Entries() {
			fmt.Printf("%s %-22s x%-3d attempts %d next %s %s\n", en.Report.Severity, en.Report.Code, en.Report.Count,
				en.Meta.Attempts, en.Meta.NextAt.Format(time.RFC3339), en.Meta.LastStatus)
		}
		st := bugreport.LoadState(e.StatePath)
		fmt.Printf("enabled %v, url %s, sent this hour %d, today %d, dropped %d, last error %q\n",
			c.enabled, c.url, st.HourCount, st.DayCount, st.Dropped, st.LastError)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vectra-reporter:", err)
		os.Exit(1)
	}
}
