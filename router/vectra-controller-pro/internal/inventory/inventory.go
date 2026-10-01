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
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/connecttelemetry"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/supervisor"
)

// Options seed the collector with values known at startup.
type Options struct {
	EngineMode string
	// DeviceIdentifier and DevicePublicKey come from the persisted state
	// (state.EnsureIdentity / state.ImportLegacyIdentity), not from the OS.
	// The panel declares both as z.string().min(1) and NOT optional, so an
	// empty value here makes every register/check-in fail with HTTP 400.
	DeviceIdentifier         string
	DevicePublicKey          string
	ControllerVersion        string
	ControllerRuntimeVersion string
	PanelDomain              string
	XrayBinary               string
	AssetDir                 string
}

// Collector gathers inventory. Command execution and file reads are injectable
// so the assembly logic is unit-testable without a router.
type Collector struct {
	connectMu       sync.Mutex
	connectSnapshot connecttelemetry.Snapshot
	opts            Options
	run             func(ctx context.Context, name string, args ...string) (string, error)
	readFile        func(path string) ([]byte, error)
	statfsMB        func(path string) int
	hostname        func() (string, error)

	verMu    sync.Mutex
	verKey   string // size/mtime of the xray binary the cached version came from
	verValue string

	assetMu  sync.Mutex
	assetDir string // set by SetAssetDir; opts.AssetDir until then
}

// SetAssetDir is where xray reads its geo files now: what is reported from
// the next inventory on.
func (c *Collector) SetAssetDir(dir string) {
	c.assetMu.Lock()
	c.assetDir = dir
	c.assetMu.Unlock()
}

// AssetDir is the geo directory the inventory reports.
func (c *Collector) AssetDir() string {
	c.assetMu.Lock()
	dir := c.assetDir
	c.assetMu.Unlock()
	if dir == "" {
		dir = c.opts.AssetDir
	}
	if dir == "" {
		dir = config.DefaultGeoAssetDir
	}
	return dir
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
		DeviceIdentifier:         c.opts.DeviceIdentifier,
		DevicePublicKey:          c.opts.DevicePublicKey,
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
	inv.PasswallEnabled = false // xray-direct: PassWall2 is never the data plane
	if v := c.xrayVersion(ctx); v != "" {
		inv.XrayVersion = v
		inv.BinaryVersions["xray"] = v
	}
	// Service states must be valid panel enum values (running|stopped|
	// degraded|unknown). PassWall2 is intentionally not running here.
	inv.ServiceHealth = controlplane.RouterServiceHealth{
		Controller:     "running",
		Xray:           serviceState(xrayRunning),
		DNSMasq:        serviceState(c.processAlive(ctx, "dnsmasq")),
		Passwall:       "stopped",
		PasswallServer: "stopped",
	}
	inv.RulesAssets = c.geoAssets()
	c.connectMu.Lock()
	inv.Connect = connecttelemetry.Build(c.readFile, time.Now(), c.connectSnapshot)
	c.connectMu.Unlock()
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

// xrayVersion runs `xray version` only when the binary changed since the last
// answer. It used to run on every check-in — a 32 MB binary exec'd every 45 s
// on a 234 MB router, to learn the same string. The key is what an upgrade
// changes: the file's size and mtime (through the wrapper, the wrapper's —
// replacing the real binary under it is caught by the next controller start).
func (c *Collector) xrayVersion(ctx context.Context) string {
	key := ""
	if st, err := os.Stat(c.opts.XrayBinary); err == nil {
		key = fmt.Sprintf("%d/%d", st.Size(), st.ModTime().UnixNano())
	}
	c.verMu.Lock()
	if key != "" && key == c.verKey && c.verValue != "" {
		v := c.verValue
		c.verMu.Unlock()
		return v
	}
	c.verMu.Unlock()

	out, err := c.run(ctx, c.opts.XrayBinary, "version")
	if err != nil {
		return ""
	}
	// "Xray 1.8.4 (Xray, Penetrates ...)" -> "1.8.4"
	v := strings.TrimSpace(out)
	if fields := strings.Fields(out); len(fields) >= 2 {
		v = fields[1]
	}
	c.verMu.Lock()
	c.verKey, c.verValue = key, v
	c.verMu.Unlock()
	return v
}

// XrayVersion is the last version `xray version` reported, "" before the
// first check-in.
func (c *Collector) XrayVersion() string {
	c.verMu.Lock()
	defer c.verMu.Unlock()
	return c.verValue
}

func (c *Collector) processAlive(ctx context.Context, name string) bool {
	if _, err := c.run(ctx, "pgrep", "-x", name); err == nil {
		return true
	}
	return false
}

func (c *Collector) geoAssets() controlplane.RouterRulesAssets {
	assets := controlplane.RouterRulesAssets{}
	dir := c.AssetDir()
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
	// No child inherits the controller's own GC tuning (see
	// config.XrayAssetEnv); `xray version` is one such child.
	cmd.Env = config.XrayAssetEnv(os.Environ(), "")
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

// SetConnect supplies fresh measured daemon state and applied owner settings.
// Call before Collect; unavailable fields must remain nil/empty.
func (c *Collector) SetConnect(snapshot connecttelemetry.Snapshot) {
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	// Build clones mutable slices/maps and clears stale observations.
	telemetry := connecttelemetry.Build(func(string) ([]byte, error) { return nil, os.ErrNotExist }, time.Now(), snapshot)
	c.connectSnapshot = connecttelemetry.Snapshot{Telemetry: *telemetry, ObservedAt: snapshot.ObservedAt}
}
