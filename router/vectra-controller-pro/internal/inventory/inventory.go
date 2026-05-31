// Package inventory collects the device facts the panel needs to manage an
// xray-direct router: identity, hardware/OS, resources, xray runtime health,
// and geo asset versions. It is OS-portable and best-effort — on a dev macOS
// host (no /proc, no ubus) it degrades to zero/empty rather than failing, so
// the daemon and tests can exercise it anywhere.
package inventory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/supervisor"
)

// Options seed the collector with values known at startup.
type Options struct {
	EngineMode               string
	ControllerVersion        string
	ControllerRuntimeVersion string
	PanelDomain              string
	XrayBinary               string
	AssetDir                 string
}

// Collector gathers inventory. Command execution and file reads are injectable
// so the assembly logic is unit-testable without a router.
type Collector struct {
	opts     Options
	run      func(ctx context.Context, name string, args ...string) (string, error)
	readFile func(path string) ([]byte, error)
	statfsMB func(path string) int
	hostname func() (string, error)
}

// NewCollector returns a Collector wired to the real OS.
func NewCollector(opts Options) *Collector {
	if opts.EngineMode == "" {
		opts.EngineMode = controlplane.EngineModeXrayDirect
	}
	if opts.XrayBinary == "" {
		opts.XrayBinary = "/usr/bin/xray"
	}
	return &Collector{
		opts:     opts,
		run:      runCmd,
		readFile: os.ReadFile,
		statfsMB: statfsFreeMB,
		hostname: os.Hostname,
	}
}

// Collect assembles a RouterInventory. xrayStatus comes from the supervisor so
// service health reflects the process this controller actually owns.
func (c *Collector) Collect(ctx context.Context, xrayStatus supervisor.Status, nodeCount, subCount int) controlplane.RouterInventory {
	// Bound all subprocess probes so a hung helper (e.g. a wedged `xray
	// version`) can never stall the control loop.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	inv := controlplane.RouterInventory{
		ProtocolVersion:          controlplane.ProtocolVersion,
		EngineMode:               c.opts.EngineMode,
		ControllerVersion:        c.opts.ControllerVersion,
		ControllerRuntimeVersion: c.opts.ControllerRuntimeVersion,
		PanelDomain:              c.opts.PanelDomain,
		NodeCount:                nodeCount,
		SubscriptionCount:        subCount,
		Resources:                c.resources(),
		PackageVersions:          map[string]string{},
		BinaryVersions:           map[string]string{},
	}

	if hn, err := c.hostname(); err == nil {
		inv.Hostname = hn
	}
	c.fillBoard(ctx, &inv)
	c.fillRelease(&inv)

	xrayRunning := xrayStatus.State == supervisor.StateRunning
	inv.XrayEnabled = xrayRunning
	if v := c.xrayVersion(ctx); v != "" {
		inv.XrayVersion = v
		inv.BinaryVersions["xray"] = v
	}
	inv.ServiceHealth = controlplane.RouterServiceHealth{
		Controller:     "running",
		Xray:           serviceState(xrayRunning),
		DNSMasq:        serviceState(c.processAlive(ctx, "dnsmasq")),
		Passwall:       "disabled",
		PasswallServer: "disabled",
	}
	inv.RulesAssets = c.geoAssets()
	return inv
}

func serviceState(up bool) string {
	if up {
		return "running"
	}
	return "stopped"
}

// Resources returns a fresh resource reading (used by the job-safety gate).
func (c *Collector) Resources() controlplane.RouterResources {
	return c.resources()
}

func (c *Collector) resources() controlplane.RouterResources {
	res := controlplane.RouterResources{}
	if data, err := c.readFile("/proc/meminfo"); err == nil {
		res = parseMeminfo(data)
	}
	res.OverlayFreeMB = c.statfsMB("/overlay")
	res.TMPFreeMB = c.statfsMB("/tmp")
	return res
}

// parseMeminfo extracts memory figures (kB in /proc/meminfo) as MB.
func parseMeminfo(data []byte) controlplane.RouterResources {
	res := controlplane.RouterResources{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		mb := kb / 1024
		switch strings.TrimSuffix(fields[0], ":") {
		case "MemTotal":
			res.MemoryTotalMB = mb
		case "MemAvailable":
			res.MemoryAvailableMB = mb
		case "SwapTotal":
			res.SwapTotalMB = mb
		case "SwapFree":
			res.SwapFreeMB = mb
		}
	}
	return res
}

func (c *Collector) fillBoard(ctx context.Context, inv *controlplane.RouterInventory) {
	out, err := c.run(ctx, "ubus", "call", "system", "board")
	if err != nil || strings.TrimSpace(out) == "" {
		return
	}
	// Avoid a JSON dependency cycle on the panel's exact shape; pull the few
	// string fields we need with a tolerant decoder.
	var board struct {
		Model     string `json:"model"`
		BoardName string `json:"board_name"`
		Release   struct {
			Distribution string `json:"distribution"`
			Version      string `json:"version"`
			Target       string `json:"target"`
			Description  string `json:"description"`
		} `json:"release"`
	}
	if json.Unmarshal([]byte(out), &board) == nil {
		inv.Model = board.Model
		inv.BoardName = board.BoardName
		inv.Target = board.Release.Target
		inv.OpenWrtRelease = board.Release.Version
		inv.OpenWrtDescription = board.Release.Description
	}
}

func (c *Collector) fillRelease(inv *controlplane.RouterInventory) {
	data, err := c.readFile("/etc/openwrt_release")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch strings.TrimSpace(key) {
		case "DISTRIB_ARCH":
			inv.Architecture = val
		case "DISTRIB_RELEASE":
			if inv.OpenWrtRelease == "" {
				inv.OpenWrtRelease = val
			}
		case "DISTRIB_TARGET":
			if inv.Target == "" {
				inv.Target = val
			}
		}
	}
}

func (c *Collector) xrayVersion(ctx context.Context) string {
	out, err := c.run(ctx, c.opts.XrayBinary, "version")
	if err != nil {
		return ""
	}
	// "Xray 1.8.4 (Xray, Penetrates ...)" -> "1.8.4"
	fields := strings.Fields(out)
	if len(fields) >= 2 {
		return fields[1]
	}
	return strings.TrimSpace(out)
}

func (c *Collector) processAlive(ctx context.Context, name string) bool {
	if _, err := c.run(ctx, "pgrep", "-x", name); err == nil {
		return true
	}
	return false
}

func (c *Collector) geoAssets() controlplane.RouterRulesAssets {
	assets := controlplane.RouterRulesAssets{}
	dir := c.opts.AssetDir
	if dir == "" {
		dir = "/usr/share/xray"
	}
	assets.AssetDirectory = dir
	if fi, err := os.Stat(dir + "/geoip.dat"); err == nil {
		assets.GeoIPVersion = strconv.FormatInt(fi.Size(), 10)
		assets.GeoIPUpdatedAt = fi.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	if fi, err := os.Stat(dir + "/geosite.dat"); err == nil {
		assets.GeoSiteVersion = strconv.FormatInt(fi.Size(), 10)
		assets.GeoSiteUpdatedAt = fi.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	}
	return assets
}

// runCmd executes a command and returns trimmed combined output.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// statfsFreeMB returns free MB on the filesystem holding path (0 if unknown).
func statfsFreeMB(path string) int {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	free := uint64(st.Bavail) * uint64(st.Bsize)
	return int(free / (1024 * 1024))
}
