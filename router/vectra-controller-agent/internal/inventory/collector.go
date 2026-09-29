package inventory

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/rescue"
)

type Collector struct {
	// ProxyRuntimeFailure, when set, is the persisted memory behind the
	// proxy_runtime_unusable verdict (see ProxyRuntimeStartFailure); Collect
	// reads and updates it in place. Nil judges the PassWall log alone, which is
	// fine for one-off snapshots but not for the run loop that acts on the
	// verdict: without the memory a repaired router could stay condemned by a log
	// line written about the binary it no longer has.
	ProxyRuntimeFailure *ProxyRuntimeStartFailure
}

var semverLikePattern = regexp.MustCompile(`\b[vV]?\d+\.\d+(?:\.\d+)?(?:[-+._0-9A-Za-z]*)?\b`)
var oomSafetyPattern = regexp.MustCompile(`(?i)(out of memory|oom-killer|invoked oom-killer|killed process|oom_reaper)`)
var crashSafetyPattern = regexp.MustCompile(`(?i)(crash loop|segfault|fatal error|panic:)`)
var killedProcessComponentPattern = regexp.MustCompile(`\(([^)]+)\)`)
var opkgInfoDir = "/usr/lib/opkg/info"
var opkgStatusFile = "/usr/lib/opkg/status"

const telegramProbeURL = "https://telegram.org/"
const telegramProbeTimeout = 3 * time.Second
const telegramProbeCacheTTL = 30 * time.Minute
const youtubeProbeURL = "https://www.youtube.com/generate_204"

// youtubeVideoProbeURL reaches the video CDN rather than the page. Host choice
// matters: redirector.googlevideo.com answered 204 only intermittently when
// measured from a known-good line (1 of 3 attempts), which would have painted the
// whole fleet red, while www.googlevideo.com answered 204 on every attempt.
// googlevideo.com is inside geosite:YOUTUBE, so this probe rides the same slot
// the player does.
const youtubeVideoProbeURL = "https://www.googlevideo.com/generate_204"
const youtubeProbeTimeout = 3 * time.Second
const youtubeProbeCacheTTL = 30 * time.Minute
const instagramProbeTimeout = 3 * time.Second
const instagramProbeCacheTTL = 30 * time.Minute
const lowMemoryExpensiveProbeFloorMB = 64
const serviceReachabilityProbeFloorMB = 128

// serviceReachabilityLeanFloorMB is the floor for the reduced probe profile.
// The AX3000T fleet baseline is 234 MB total with 37-67 MB available, so it can
// never clear serviceReachabilityProbeFloorMB — before the lean profile existed
// those routers reported telegram/youtube/instagram reachability as null and the
// panel's telegram_blocked auto-rescue trigger could never fire on them. Lean
// mode probes a single endpoint per service, which is strictly cheaper per cycle
// than the full profile it replaces, so it stays affordable that far down.
const serviceReachabilityLeanFloorMB = 24

// serviceReachabilityBlockedRetryTTL replaces the long cache as soon as a
// service stops being reachable. The panel needs blockedSnapshotWindow (3)
// consecutive snapshots carrying DISTINCT checkedAt values before it opens a
// rescue case; at the 30-minute steady-state TTL that takes ~90 minutes of
// continuous outage, so real outages lasting minutes were never detected. This
// must stay comfortably below the 60s controller poll interval, otherwise
// consecutive check-ins replay one cached checkedAt and the window collapses.
const serviceReachabilityBlockedRetryTTL = 30 * time.Second

// serviceReachabilityBlockedRetryBurst bounds the fast cadence. A few probes is
// all the panel needs to arm the trigger; a permanently blocked service then
// falls back to the steady-state TTL instead of probing a struggling box
// forever.
const serviceReachabilityBlockedRetryBurst = 5
const safetyDiagnosticsCacheTTL = 10 * time.Minute
const safetyDiagnosticsTimeout = 2 * time.Second
const proxyRuntimeProbeTimeout = time.Second

// passwallGlobalRuntimeConfigPaths are the places PassWall writes the global
// instance's runtime config: acl/default/global.json up to 26.8, and
// acl/acl_default.json from 26.9 on. Knowing only the first made a live xray
// look missing on andrey-avito (26.9.16, 2026-09-29), and the watchdog then
// restarted PassWall every five minutes to "revive" it.
var passwallGlobalRuntimeConfigPaths = []string{
	"/tmp/etc/passwall2/acl/default/global.json",
	"/tmp/etc/passwall2/acl/acl_default.json",
}

const safetyDiagnosticsMemoryFloorMB = 64
const safetyLogLines = 160
const maxSafetyEvents = 12
const routerMemoryCriticalFloorMB = 48
const routerMemoryWarningFloorMB = 64
const routerMemoryCriticalPercent = 20
const routerMemoryWarningPercent = 28
const routerOverlayCriticalFloorMB = 8
const routerOverlayWarningFloorMB = 16
const routerTMPCriticalFloorMB = 16
const routerTMPWarningFloorMB = 32

type telegramProbeTarget struct {
	ID    string
	Label string
	URL   string
}

var telegramProbeTargets = []telegramProbeTarget{
	{ID: "telegram-org", Label: "telegram.org", URL: telegramProbeURL},
	{ID: "web", Label: "web.telegram.org", URL: "https://web.telegram.org/"},
	{ID: "share", Label: "t.me", URL: "https://t.me/"},
	{ID: "bot-api", Label: "api.telegram.org", URL: "https://api.telegram.org/"},
}

type youtubeProbeTarget struct {
	ID    string
	Label string
	URL   string
}

var youtubeProbeTargets = []youtubeProbeTarget{
	// googlevideo carries the actual video bytes, and it is the limb users mean
	// when they report "YouTube does not work": the page loads, thumbnails load,
	// playback stalls. Probing only youtube.com reported that router green, so
	// the complaint was invisible to the panel. It leads the list because nearly
	// the whole AX3000T fleet runs the lean profile (see
	// serviceReachabilityLeanFloorMB) and lean takes targets from the front.
	{ID: "youtube-video", Label: "googlevideo.com", URL: youtubeVideoProbeURL},
	{ID: "youtube-main", Label: "youtube.com", URL: youtubeProbeURL},
	{ID: "youtube-img", Label: "i.ytimg.com", URL: "https://i.ytimg.com/generate_204"},
	{ID: "youtube-api", Label: "youtubei.googleapis.com", URL: "https://youtubei.googleapis.com/generate_204"},
}

type instagramProbeTarget struct {
	ID    string
	Label string
	URL   string
}

var instagramProbeTargets = []instagramProbeTarget{
	{ID: "instagram-main", Label: "instagram.com", URL: "https://www.instagram.com/"},
	{ID: "instagram-cdn", Label: "cdninstagram.com", URL: "https://www.cdninstagram.com/"},
}

var telegramProbeCache = struct {
	mu            sync.Mutex
	result        *controlplane.RouterReachabilityProbe
	expiresAt     time.Time
	blockedStreak int
}{}

var youtubeProbeCache = struct {
	mu            sync.Mutex
	result        *controlplane.RouterReachabilityProbe
	expiresAt     time.Time
	blockedStreak int
}{}

var instagramProbeCache = struct {
	mu            sync.Mutex
	result        *controlplane.RouterReachabilityProbe
	expiresAt     time.Time
	blockedStreak int
}{}

var safetyDiagnosticsCache = struct {
	mu        sync.Mutex
	events    []controlplane.RouterSafetyEvent
	expiresAt time.Time
}{}

var passwallInventoryPackages = []string{
	"luci-app-vectra-controller",
	"luci-app-passwall2",
	"xray-core",
	"sing-box",
	"hysteria",
	"geoview",
	"tcping",
	"v2ray-geoip",
	"v2ray-geosite",
	"dnsmasq",
	"dnsmasq-full",
	"chinadns-ng",
	"kmod-nft-socket",
	"kmod-nft-tproxy",
	"kmod-nft-nat",
}

type systemBoardInfo struct {
	Hostname  string `json:"hostname"`
	Model     string `json:"model"`
	BoardName string `json:"board_name"`
	Release   struct {
		Target      string `json:"target"`
		Version     string `json:"version"`
		Description string `json:"description"`
	} `json:"release"`
}

func NewCollector() Collector {
	return Collector{}
}

func (c Collector) Collect(base controlplane.RouterInventory) controlplane.RouterInventory {
	inventory := base
	inventory.PackageVersions = cloneMap(base.PackageVersions)
	inventory.BinaryVersions = cloneMap(base.BinaryVersions)

	board := readSystemBoard()
	if board.Hostname != "" {
		inventory.Hostname = board.Hostname
	}
	if board.Model != "" {
		inventory.Model = board.Model
	}
	if board.BoardName != "" {
		inventory.BoardName = board.BoardName
	}
	if board.Release.Target != "" {
		inventory.Target = board.Release.Target
	}
	if board.Release.Version != "" {
		inventory.OpenWrtRelease = board.Release.Version
	}
	if board.Release.Description != "" {
		inventory.OpenWrtDescription = board.Release.Description
	}
	if architecture := readKeyValueFile("/etc/openwrt_release", "DISTRIB_ARCH"); architecture != "" {
		inventory.Architecture = architecture
	}

	passwallEnabled := readUCI("passwall2.@global[0].enabled")
	inventory.PasswallEnabled = passwallEnabled == "1"
	inventory.SelectedNodeID = readUCI("passwall2.@global[0].node")
	inventory.SelectedNodeLabel = resolveSelectedNodeLabel(inventory.SelectedNodeID)
	inventory.NodeCount = countPasswallSections("nodes")
	inventory.SubscriptionCount = countPasswallSections("subscribe_list")
	inventory.SubscriptionHealth = collectSubscriptionHealth()

	if inventory.Hostname == "" {
		inventory.Hostname = firstLine("hostname")
	}

	if inventory.LayoutFamily == "" {
		inventory.LayoutFamily = detectLayoutFamily(inventory.BoardName)
	}

	if inventory.OpenWrtDescription == "" {
		inventory.OpenWrtDescription = openWrtDescription()
	}

	controllerVersion := packageVersion("vectra-controller-agent")
	if controllerVersion != "" {
		inventory.ControllerVersion = controllerVersion
		inventory.PackageVersions["vectra-controller-agent"] = controllerVersion
	}

	for _, pkg := range passwallInventoryPackages {
		if version := packageVersion(pkg); version != "" {
			inventory.PackageVersions[pkg] = version
		}
	}

	inventory.Resources = collectResources()
	deferExpensiveProbes := shouldDeferExpensiveInventoryProbes(inventory.Resources)
	if !deferExpensiveProbes {
		setBinaryVersion(&inventory, "xray", commandVersion("/usr/bin/xray", "-version"))
		setBinaryVersion(&inventory, "sing-box", commandVersion("/usr/bin/sing-box", "version"))
		setBinaryVersion(&inventory, "hysteria", commandVersion("/usr/bin/hysteria", "version"))
		setBinaryVersion(&inventory, "geoview", commandVersion("/usr/bin/geoview", "-version"))
		setBinaryVersion(&inventory, "dnsmasq", firstLine("dnsmasq", "-v"))
	}

	inventory.RulesAssets = collectRulesAssets()
	inventory.ServiceHealth = controlplane.RouterServiceHealth{
		Controller:     serviceState("/etc/init.d/vectra-controller"),
		Passwall:       serviceState("/etc/init.d/passwall2"),
		PasswallServer: serviceState("/etc/init.d/passwall2_server"),
		DNSMasq:        serviceState("/etc/init.d/dnsmasq"),
	}
	inventory.SafetyEvents = collectSafetyEvents(inventory, c.ProxyRuntimeFailure)
	if mode := serviceReachabilityModeFor(inventory); mode != serviceReachabilityOff {
		inventory.TelegramReachability = collectTelegramReachability(mode)
		inventory.YouTubeReachability = collectYouTubeReachability(mode)
		inventory.InstagramReachability = collectInstagramReachability(mode)
	}

	return inventory
}

func cloneMap(input map[string]string) map[string]string {
	if input == nil {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func readSystemBoard() systemBoardInfo {
	output, err := exec.Command("ubus", "call", "system", "board").Output()
	if err != nil {
		return systemBoardInfo{}
	}

	return parseSystemBoardOutput(output)
}

func parseSystemBoardOutput(output []byte) systemBoardInfo {
	if len(output) == 0 {
		return systemBoardInfo{}
	}

	var board systemBoardInfo
	if err := json.Unmarshal(output, &board); err != nil {
		return systemBoardInfo{}
	}

	return board
}

func readUCI(key string) string {
	output, err := exec.Command("uci", "-q", "get", key).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

// subscriptionPlaceholderAddress is the address the provider hands back when it
// refuses the request — currently because PassWall asked without a hardware id.
// A node carrying it is not a node, it is the stub that replaced one.
const subscriptionPlaceholderAddress = "0.0.0.0"

func collectSubscriptionHealth() controlplane.RouterSubscriptionHealth {
	output, err := exec.Command("uci", "-q", "show", "passwall2").Output()
	if err != nil {
		return controlplane.RouterSubscriptionHealth{}
	}
	return parseSubscriptionHealth(string(output))
}

// parseSubscriptionHealth reads the gate, the schedule and the placeholder count
// out of a `uci show passwall2` dump.
//
// The subscribe_list section name differs across the fleet
// (vectra_sub_subscribe_list_0, vectra_sub_sub_puoAF, ...), so options are
// matched by suffix against whichever section declared itself a subscribe_list
// rather than by a hardcoded path.
func parseSubscriptionHealth(output string) controlplane.RouterSubscriptionHealth {
	health := controlplane.RouterSubscriptionHealth{}

	subscriptionSections := map[string]bool{}
	nodeSections := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		name, _, isOption := strings.Cut(strings.TrimPrefix(key, "passwall2."), ".")
		if isOption {
			continue
		}
		switch strings.Trim(value, `"'`) {
		case "subscribe_list":
			subscriptionSections[name] = true
		case "nodes":
			nodeSections[name] = true
		}
	}

	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		section, option, isOption := strings.Cut(strings.TrimPrefix(key, "passwall2."), ".")
		if !isOption {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch {
		case subscriptionSections[section] && option == "hwid":
			if value == "1" {
				health.HwidEnabled = true
			}
		case subscriptionSections[section] && option == "update_week_mode":
			if strings.TrimSpace(value) != "" {
				health.ScheduleEnabled = true
			}
		case nodeSections[section] && option == "address":
			if value == subscriptionPlaceholderAddress {
				health.PlaceholderNodes++
			}
		}
	}

	return health
}

func countPasswallSections(sectionType string) int {
	output, err := exec.Command("uci", "-q", "show", "passwall2").Output()
	if err != nil {
		return 0
	}

	return countUCISections(string(output), sectionType)
}

func countUCISections(output string, sectionType string) int {
	count := 0
	for _, line := range strings.Split(output, "\n") {
		_, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if strings.Trim(value, `"'`) == sectionType {
			count++
		}
	}
	return count
}

func packageVersion(name string) string {
	// Self-update can leave opkg in a half-installed state where the new package
	// control metadata is already present, but `opkg status <name>` returns
	// nothing. In that case we still want inventory to surface the unpacked
	// version instead of falling back to "unknown". Prefer direct file reads for
	// the steady-state inventory path: forking `opkg` on low-memory routers can
	// add enough pressure for the kernel to kill the already-running Xray.
	if version := packageVersionFromControlFile(
		filepath.Join(opkgInfoDir, name+".control"),
	); version != "" {
		return version
	}

	return packageVersionFromStatusFile(opkgStatusFile, name)
}

func packageVersionFromControlFile(controlPath string) string {
	content, err := os.ReadFile(controlPath)
	if err != nil {
		return ""
	}

	return parseControlVersion(string(content))
}

func parseControlVersion(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if version, ok := strings.CutPrefix(line, "Version: "); ok {
			return strings.TrimSpace(version)
		}
	}

	return ""
}

func packageVersionFromStatusFile(statusPath string, packageName string) string {
	content, err := os.ReadFile(statusPath)
	if err != nil {
		return ""
	}

	return parseStatusPackageVersion(string(content), packageName)
}

func parseStatusPackageVersion(content string, packageName string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	currentPackage := ""
	currentVersion := ""

	flush := func() string {
		if currentPackage == packageName {
			return currentVersion
		}
		return ""
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if version := flush(); version != "" {
				return version
			}
			currentPackage = ""
			currentVersion = ""
			continue
		}
		if value, ok := strings.CutPrefix(line, "Package: "); ok {
			currentPackage = strings.TrimSpace(value)
			continue
		}
		if value, ok := strings.CutPrefix(line, "Version: "); ok {
			currentVersion = strings.TrimSpace(value)
		}
	}

	return flush()
}

func firstLine(binary string, args ...string) string {
	output := commandOutput(binary, args...)
	if output == "" {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func commandOutput(binary string, args ...string) string {
	output, err := exec.Command(binary, args...).CombinedOutput()
	if err != nil && len(output) == 0 {
		return ""
	}

	return string(output)
}

func commandVersion(binary string, args ...string) string {
	output := commandOutput(binary, args...)
	if output == "" {
		return ""
	}

	return extractVersionLine(output)
}

func extractVersionLine(output string) string {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if semverLikePattern.MatchString(trimmed) {
			return trimmed
		}
	}

	return ""
}

func setBinaryVersion(inventory *controlplane.RouterInventory, key string, value string) {
	if value == "" {
		return
	}

	inventory.BinaryVersions[key] = value
}

func resolveSelectedNodeLabel(nodeID string) string {
	if nodeID == "" {
		return ""
	}

	remark := readUCI("passwall2." + nodeID + ".remarks")
	if remark != "" {
		return remark
	}

	address := readUCI("passwall2." + nodeID + ".address")
	port := readUCI("passwall2." + nodeID + ".port")
	switch {
	case address != "" && port != "":
		return fmt.Sprintf("%s:%s", address, port)
	case address != "":
		return address
	}

	protocol := readUCI("passwall2." + nodeID + ".protocol")
	if protocol != "" {
		return protocol
	}

	return nodeID
}

func openWrtDescription() string {
	if text := readKeyValueFile("/usr/lib/os-release", "PRETTY_NAME"); text != "" {
		return text
	}
	if text := readKeyValueFile("/etc/openwrt_release", "DISTRIB_DESCRIPTION"); text != "" {
		return text
	}
	return ""
}

func readKeyValueFile(path string, key string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	prefix := key + "="
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		return strings.Trim(strings.TrimPrefix(line, prefix), `"'`)
	}

	return ""
}

func detectLayoutFamily(boardName string) string {
	normalized := strings.ToLower(strings.TrimSpace(boardName))
	if strings.Contains(normalized, "ubootmod") {
		return "ubootmod"
	}

	cmdline, err := os.ReadFile("/proc/cmdline")
	if err == nil && strings.Contains(string(cmdline), "firmware=") {
		return "stock-layout"
	}

	if normalized == "xiaomi,mi-router-ax3000t" {
		return "stock-layout"
	}

	return ""
}

func collectResources() controlplane.RouterResources {
	mem := parseMemInfoMB(readTextFile("/proc/meminfo"))

	return controlplane.RouterResources{
		MemoryTotalMB:     mem["MemTotal"],
		MemoryAvailableMB: mem["MemAvailable"],
		SwapTotalMB:       mem["SwapTotal"],
		SwapFreeMB:        mem["SwapFree"],
		OverlayFreeMB:     diskFreeMB("/overlay"),
		TMPFreeMB:         diskFreeMB("/tmp"),
	}
}

func CollectResources() controlplane.RouterResources {
	return collectResources()
}

func readTextFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return string(content)
}

func parseMemInfoMB(content string) map[string]int {
	mem := map[string]int{}
	if strings.TrimSpace(content) == "" {
		return mem
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		valueKB, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		mem[strings.TrimSuffix(fields[0], ":")] = valueKB / 1024
	}

	return mem
}

func shouldDeferExpensiveInventoryProbes(resources controlplane.RouterResources) bool {
	return resources.MemoryAvailableMB > 0 && resources.MemoryAvailableMB < lowMemoryExpensiveProbeFloorMB
}

// serviceReachabilityMode describes how much service probing a router can
// afford right now.
type serviceReachabilityMode int

const (
	// serviceReachabilityOff runs no service probes at all.
	serviceReachabilityOff serviceReachabilityMode = iota
	// serviceReachabilityLean probes one representative endpoint per service.
	serviceReachabilityLean
	// serviceReachabilityFull probes every endpoint of every service.
	serviceReachabilityFull
)

func serviceReachabilityModeFor(
	inventory controlplane.RouterInventory,
) serviceReachabilityMode {
	if !inventory.PasswallEnabled {
		return serviceReachabilityOff
	}
	if inventory.ServiceHealth.Passwall != "running" {
		return serviceReachabilityOff
	}

	switch available := inventory.Resources.MemoryAvailableMB; {
	case available >= serviceReachabilityProbeFloorMB:
		return serviceReachabilityFull
	case available >= serviceReachabilityLeanFloorMB:
		return serviceReachabilityLean
	default:
		// Includes an unknown (zero) reading: without a trusted memory figure we
		// keep the old conservative behaviour and probe nothing.
		return serviceReachabilityOff
	}
}

// nextServiceReachabilityCache decides how long a probe result stays cached and
// how many consecutive blocked results we have seen. While a service is
// reachable the long steady-state TTL applies and the streak resets; once it is
// not reachable we re-probe on the short retry cadence for a bounded burst so
// the panel can accumulate its distinct-checkedAt evidence window.
func nextServiceReachabilityCache(
	base time.Duration,
	probe *controlplane.RouterReachabilityProbe,
	blockedStreak int,
) (time.Duration, int) {
	if probe == nil || probe.Reachable {
		return base, 0
	}

	streak := blockedStreak + 1
	if streak > serviceReachabilityBlockedRetryBurst {
		return base, streak
	}
	return serviceReachabilityBlockedRetryTTL, streak
}

func telegramTargetsFor(mode serviceReachabilityMode) []telegramProbeTarget {
	if mode == serviceReachabilityLean && len(telegramProbeTargets) > 0 {
		return telegramProbeTargets[:1]
	}
	return telegramProbeTargets
}

// youtubeLeanTargetCount keeps both limbs of the YouTube path in the lean
// profile — the video CDN and the page. One limb alone cannot separate "playback
// is broken" from "YouTube is down", and that is the distinction every fleet
// complaint turns on. The extra cost is one 3-second-timeout request per 30-minute
// cache cycle, which the lean floor can afford; the richer i.ytimg/youtubei
// targets stay behind the full profile.
const youtubeLeanTargetCount = 2

func youtubeTargetsFor(mode serviceReachabilityMode) []youtubeProbeTarget {
	if mode == serviceReachabilityLean && len(youtubeProbeTargets) > youtubeLeanTargetCount {
		return youtubeProbeTargets[:youtubeLeanTargetCount]
	}
	return youtubeProbeTargets
}

func instagramTargetsFor(mode serviceReachabilityMode) []instagramProbeTarget {
	if mode == serviceReachabilityLean && len(instagramProbeTargets) > 0 {
		return instagramProbeTargets[:1]
	}
	return instagramProbeTargets
}

func collectSafetyEvents(
	inventory controlplane.RouterInventory,
	proxyRuntimeFailure *ProxyRuntimeStartFailure,
) []controlplane.RouterSafetyEvent {
	events := make([]controlplane.RouterSafetyEvent, 0, maxSafetyEvents)
	now := time.Now().UTC()

	events = append(events, resourceSafetyEvents(inventory.Resources, now)...)
	events = append(events, serviceSafetyEvents(inventory, now, proxyRuntimeFailure)...)
	if shouldCollectSafetyDiagnostics(inventory.Resources) {
		events = append(events, collectCachedSafetyDiagnostics(now)...)
	}

	return limitSafetyEvents(dedupeSafetyEvents(events), maxSafetyEvents)
}

func resourceSafetyEvents(
	resources controlplane.RouterResources,
	observedAt time.Time,
) []controlplane.RouterSafetyEvent {
	events := make([]controlplane.RouterSafetyEvent, 0, 3)

	if resources.MemoryAvailableMB > 0 {
		severity := ""
		percent := 0
		if resources.MemoryTotalMB > 0 {
			percent = resources.MemoryAvailableMB * 100 / resources.MemoryTotalMB
		}
		switch {
		case resources.MemoryAvailableMB < routerMemoryCriticalFloorMB ||
			(percent > 0 && percent < routerMemoryCriticalPercent):
			severity = "critical"
		case resources.MemoryAvailableMB < routerMemoryWarningFloorMB ||
			(percent > 0 && percent < routerMemoryWarningPercent):
			severity = "warning"
		}
		if severity != "" {
			message := fmt.Sprintf(
				"available RAM is low: %d MB available",
				resources.MemoryAvailableMB,
			)
			if percent > 0 {
				message = fmt.Sprintf("%s (%d%% of %d MB)", message, percent, resources.MemoryTotalMB)
			}
			events = append(events, buildSafetyEvent(
				"low_memory",
				severity,
				"memory",
				"resources",
				message,
				observedAt,
				"",
			))
		}
	}

	if resources.OverlayFreeMB > 0 && resources.OverlayFreeMB < routerOverlayWarningFloorMB {
		severity := "warning"
		if resources.OverlayFreeMB < routerOverlayCriticalFloorMB {
			severity = "critical"
		}
		events = append(events, buildSafetyEvent(
			"low_overlay",
			severity,
			"overlay",
			"resources",
			fmt.Sprintf("/overlay free space is low: %d MB available", resources.OverlayFreeMB),
			observedAt,
			"",
		))
	}

	if resources.TMPFreeMB > 0 && resources.TMPFreeMB < routerTMPWarningFloorMB {
		severity := "warning"
		if resources.TMPFreeMB < routerTMPCriticalFloorMB {
			severity = "critical"
		}
		events = append(events, buildSafetyEvent(
			"low_tmp",
			severity,
			"tmp",
			"resources",
			fmt.Sprintf("/tmp free space is low: %d MB available", resources.TMPFreeMB),
			observedAt,
			"",
		))
	}

	return events
}

func serviceSafetyEvents(
	inventory controlplane.RouterInventory,
	observedAt time.Time,
	proxyRuntimeFailure *ProxyRuntimeStartFailure,
) []controlplane.RouterSafetyEvent {
	events := make([]controlplane.RouterSafetyEvent, 0, 4)

	if inventory.PasswallEnabled && inventory.ServiceHealth.Passwall != "" &&
		inventory.ServiceHealth.Passwall != "running" &&
		inventory.ServiceHealth.Passwall != "unknown" {
		events = append(events, buildSafetyEvent(
			"service_degraded",
			"critical",
			"passwall2",
			"service",
			fmt.Sprintf("PassWall2 is enabled but service state is %s", inventory.ServiceHealth.Passwall),
			observedAt,
			"",
		))
	}

	if inventory.ServiceHealth.DNSMasq != "" &&
		inventory.ServiceHealth.DNSMasq != "running" &&
		inventory.ServiceHealth.DNSMasq != "unknown" {
		events = append(events, buildSafetyEvent(
			"service_degraded",
			"warning",
			"dnsmasq",
			"service",
			fmt.Sprintf("dnsmasq service state is %s", inventory.ServiceHealth.DNSMasq),
			observedAt,
			"",
		))
	}

	if inventory.PasswallEnabled &&
		inventory.ServiceHealth.PasswallServer != "" &&
		inventory.ServiceHealth.PasswallServer != "running" &&
		inventory.ServiceHealth.PasswallServer != "unknown" {
		events = append(events, buildSafetyEvent(
			"service_degraded",
			"warning",
			"passwall2_server",
			"service",
			fmt.Sprintf("PassWall server service state is %s", inventory.ServiceHealth.PasswallServer),
			observedAt,
			"",
		))
	}

	if event, ok := proxyRuntimeSafetyEvent(inventory, observedAt, proxyRuntimeRunning); ok {
		events = append(events, event)
	}

	if event, ok := proxyRuntimeUnusableSafetyEvent(inventory, observedAt, proxyRuntimeFailure); ok {
		events = append(events, event)
	}

	return events
}

func proxyRuntimeSafetyEvent(
	inventory controlplane.RouterInventory,
	observedAt time.Time,
	runtimeRunning func(string) bool,
) (controlplane.RouterSafetyEvent, bool) {
	if !inventory.PasswallEnabled || inventory.ServiceHealth.Passwall != "running" {
		return controlplane.RouterSafetyEvent{}, false
	}

	nodeID := strings.TrimSpace(inventory.SelectedNodeID)
	if nodeID == "" {
		return controlplane.RouterSafetyEvent{}, false
	}

	rawType := readUCI("passwall2." + nodeID + ".type")
	return proxyRuntimeSafetyEventForNodeType(inventory, observedAt, nodeID, rawType, runtimeRunning)
}

func proxyRuntimeSafetyEventForNodeType(
	inventory controlplane.RouterInventory,
	observedAt time.Time,
	nodeID string,
	rawType string,
	runtimeRunning func(string) bool,
) (controlplane.RouterSafetyEvent, bool) {
	if !inventory.PasswallEnabled || inventory.ServiceHealth.Passwall != "running" {
		return controlplane.RouterSafetyEvent{}, false
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return controlplane.RouterSafetyEvent{}, false
	}

	runtime := normalizeProxyRuntimeType(rawType)
	if runtime == "" {
		return controlplane.RouterSafetyEvent{}, false
	}
	if runtimeRunning == nil || runtimeRunning(runtime) {
		return controlplane.RouterSafetyEvent{}, false
	}

	return buildSafetyEvent(
		"proxy_runtime_missing",
		"critical",
		runtime,
		"process",
		fmt.Sprintf("PassWall2 is running but expected %s process is missing", runtime),
		observedAt,
		proxyRuntimeMissingEvidence(runtime, nodeID, rawType),
	), true
}

func normalizeProxyRuntimeType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch normalized {
	case "xray", "xray-core", "v2ray":
		return "xray"
	case "sing-box", "singbox":
		return "sing-box"
	case "hysteria", "hysteria2", "hysteria-2", "hy2":
		return "hysteria"
	default:
		return ""
	}
}

func proxyRuntimeRunning(component string) bool {
	component = strings.TrimSpace(component)
	if component == "" {
		return false
	}
	switch component {
	case "xray", "sing-box":
		return processTableHasRuntimeConfig(component, passwallGlobalRuntimeConfigPaths)
	}
	return strings.TrimSpace(boundedCommandOutput(proxyRuntimeProbeTimeout, "pidof", component)) != ""
}

func proxyRuntimeMissingEvidence(runtime string, nodeID string, rawType string) string {
	if runtime == "xray" || runtime == "sing-box" {
		return fmt.Sprintf(
			"process table has no %s using %s; selected node %s type=%s",
			runtime,
			strings.Join(passwallGlobalRuntimeConfigPaths, " or "),
			nodeID,
			strings.TrimSpace(rawType),
		)
	}
	return fmt.Sprintf("pidof %s returned no pid; selected node %s type=%s", runtime, nodeID, strings.TrimSpace(rawType))
}

func processTableHasRuntimeConfig(component string, configPaths []string) bool {
	component = strings.TrimSpace(component)
	if component == "" || len(configPaths) == 0 {
		return false
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return strings.TrimSpace(boundedCommandOutput(proxyRuntimeProbeTimeout, "pidof", component)) != ""
	}

	for _, entry := range entries {
		if !entry.IsDir() || !isProcessDirectory(entry.Name()) {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue
		}
		if processCommandMatchesAnyRuntimeConfig(cmdline, component, configPaths) {
			return true
		}
	}

	return false
}

func isProcessDirectory(name string) bool {
	if name == "" {
		return false
	}
	for _, ch := range name {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func processCommandMatchesAnyRuntimeConfig(cmdline []byte, component string, configPaths []string) bool {
	for _, configPath := range configPaths {
		if configPath = strings.TrimSpace(configPath); configPath != "" &&
			processCommandMatchesRuntimeConfig(cmdline, component, configPath) {
			return true
		}
	}
	return false
}

func processCommandMatchesRuntimeConfig(cmdline []byte, component string, configPath string) bool {
	args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	if len(args) == 0 {
		return false
	}
	if filepath.Base(args[0]) != component {
		return false
	}
	for _, arg := range args[1:] {
		if arg == configPath {
			return true
		}
	}
	return false
}

// ProxyRuntimeUnusableEventType marks a proxy runtime that provably cannot run.
// It is deliberately distinct from proxy_runtime_missing, which only says the
// runtime process is not there right now and that a restart may bring it back.
const ProxyRuntimeUnusableEventType = "proxy_runtime_unusable"

// ProxyRuntimeStartFailureSource is the Source of a proxy_runtime_unusable
// event that stands on a failed PassWall start, as opposed to "filesystem" (the
// xray binary is missing). Consumers treat them differently: a missing binary
// blocks until the file is back, while a failed start may still be retried now
// and then, because a bad node, geosite code or applied config can be fixed in
// ways no fingerprint sees.
const ProxyRuntimeStartFailureSource = "passwall_log"

// passwallLogTailBytes bounds how much of PassWall's own log is read per
// collection. clean_log() empties the file once it passes 1000 lines, so the
// whole log rarely exceeds this, and a few dozen lines already cover several
// start attempts.
const passwallLogTailBytes = 64 * 1024

const xrayStartFailurePrefix = "Failed to start:"

// passwallGlobalInstanceMarkers identify PassWall's global proxy instance in
// its log. When the global xray fails its config test, app.sh logs "[Global]
// process /tmp/etc/passwall2/acl/default/global.json error, skip this
// transparent proxy!" (acl/acl_default.json from 26.9 on) and then appends
// xray's own test output. ACL instances log the same message about their own
// config -- $TMP_ACL_PATH/<node>_TCP_UDP_DNS_<port>.json, or acl_<rule>.json
// from 26.9 -- and append their output to the same file; a failed ACL does not
// stop the global proxy and must not condemn the router. The message is
// translated, the path is a format argument, so the path identifies the global
// instance in every language.
var passwallGlobalInstanceMarkers = []string{"/acl/default/global.json", "/acl/acl_default.json"}

// passwallLogStampLayout is echolog_date's `date "+%Y-%m-%d %H:%M:%S"` prefix.
// PassWall's own lines carry it; the runtime output it appends does not (xray
// stamps its own lines with slashes).
const passwallLogStampLayout = "2006-01-02 15:04:05"

const defaultV2rayAssetDirectory = "/usr/share/v2ray/"

// passwallLogPath is PassWall2's own log (LOG_FILE in utils.sh). It lives in
// /tmp, so the nightly reboot wipes it.
var passwallLogPath = "/tmp/log/passwall2.log"

// passwallConfigPath is the UCI file PassWall generates its xray config from.
var passwallConfigPath = "/etc/config/passwall2"

// xrayPackageBinaryPath is the binary the xray-core package owns, and the one
// the Vectra wrapper execs.
var xrayPackageBinaryPath = "/usr/bin/xray"

// vectraXrayWrapperPath is where low-RAM boards point
// passwall2.@global_app[0].xray_file: a shell script that sets GOMEMLIMIT/GOGC
// and execs xrayPackageBinaryPath. Only while it IS that script, though:
// PassWall's LuCI component updater overwrites whatever xray_file names, so
// this path can just as well hold a raw xray binary.
var vectraXrayWrapperPath = "/usr/sbin/vectra-xray-wrapper"

// xrayFallbackBinaryPaths and singBoxFallbackBinaryPaths are where PassWall2's
// first_type() looks when the configured *_file is not an executable absolute
// path: /bin and /usr/bin explicitly, then `command -v` over the default PATH.
var xrayFallbackBinaryPaths = []string{"/bin/xray", "/usr/bin/xray", "/usr/sbin/xray", "/sbin/xray"}
var singBoxFallbackBinaryPaths = []string{"/bin/sing-box", "/usr/bin/sing-box", "/usr/sbin/sing-box", "/sbin/sing-box"}

// readProxyRuntimeUCI is readUCI, swappable in tests.
var readProxyRuntimeUCI = readUCI

// PassWall2 writes its start/stop lines through log_i18n, so they follow the
// LuCI language. These are the msgstr of every translation upstream ships
// (po/ru, po/zh-cn, po/zh-tw, po/fa; zh_Hans/zh_Hant are symlinks). An
// unrecognised language only means no start attempt is found, which fails
// open (no event).
var passwallStartCompleteMarkers = []string{
	"Running complete!",
	"Выполнение завершено!",
	"运行完成！",
	"運行完成！",
	"اجرا کامل شد!",
}

var passwallNoProxyModeMarkers = []string{
	"Running in no proxy mode",
	"Работа в режиме без прокси",
	"运行于非代理模式",
	"運行於非代理模式",
	"در حالت بدون پروکسی",
}

// ProxyRuntimeStartFailure is the controller's durable memory of the last
// failed PassWall start, persisted in the agent state.
//
// The PassWall log alone is not enough to judge by. A failed start stays the
// last proxy start in /tmp/log/passwall2.log until PassWall starts a proxy
// again, and once the router has fallen back to direct nothing starts one:
// judged by the log alone, a router whose xray was since replaced by an
// operator or a repair job would stay condemned forever by a line written
// about the old binary. So the first time a failed attempt is seen, the
// runtime it failed on is fingerprinted (see snapshotProxyRuntime) and the
// verdict only stands while that runtime is still the one installed. The
// converse holds too: the nightly reboot wipes /tmp and clean_log() empties the
// file, and neither repairs anything, so a remembered failure keeps standing
// while the log holds no proxy start at all. A successful proxy start ends it;
// a changed runtime retires it.
type ProxyRuntimeStartFailure struct {
	// Attempt is the "Running complete!" line that closed the failed start; its
	// timestamp tells one attempt from the next.
	Attempt string `json:"attempt,omitempty"`
	// Evidence is the "Failed to start:" line xray printed for the global
	// instance.
	Evidence string `json:"evidence,omitempty"`
	// Runtime fingerprints what the attempt failed on.
	Runtime string `json:"runtime,omitempty"`
	// ObservedAt is when the controller first saw the failure. Consumers time
	// their periodic retry of a failed start from it.
	ObservedAt string `json:"observed_at,omitempty"`
	// Retired means the attempt says nothing about the installed runtime any
	// more: the runtime changed after it (or before it was first seen). A
	// retired memory is kept only so the same attempt is not judged again; it
	// costs nothing per collection, where an active one costs two UCI reads
	// and a fingerprint.
	Retired bool `json:"retired,omitempty"`
}

// proxyRuntimeUnusableSafetyEvent reports a proxy runtime that provably cannot
// run.
//
// andrey-avito (Cudy WR3000H, PassWall2 26.4.10, controller 0.1.13-r40,
// 2026-09-28/29) is why. First /usr/bin/xray was an upstream 26.9.9 that
// refuses the config PassWall 26.4.10 generates ("The feature outbound
// proxySettings has been removed"): every start logged xray's "Failed to
// start:" and fell back to no proxy mode. Later the binary vanished outright
// while xray_file still pointed at the Vectra wrapper, so PassWall found an
// executable, armed its nft/fakedns interception and then had no xray to hand
// the traffic to: every proxied domain was black-holed for the LAN. The
// watchdog saw proxy_runtime_missing and kept restarting PassWall, and every
// restart re-armed the black hole. A restart cannot fix either condition; this
// event says so, so that the controller stops trying and fails safe to direct.
//
// Two conditions qualify, and only while the selected node runs on xray:
//   - the executable is missing: xray_file resolves (as PassWall's own
//     first_type() resolves it) to nothing executable while no sing-box is
//     there to run the node instead, or to the Vectra wrapper script while the
//     /usr/bin/xray it execs is missing. This clears itself as soon as the file
//     is back.
//   - the last PassWall start that actually tried to run a proxy failed in the
//     global xray instance, and the runtime it failed on is still the
//     installed one (see ProxyRuntimeStartFailure).
//
// It is reported whether or not PassWall is enabled: once the router is back
// in direct this event is what keeps the automatic paths from switching it on
// again over the same broken runtime.
//
// Cost matters, this runs on every collection of a 234 MB router. The healthy
// steady state -- /usr/bin/xray present, no unseen failed start in the log,
// nothing active in memory -- is decided from one stat and one bounded read of
// a /tmp file, without spawning anything. Only a suspect router pays for the
// UCI reads and the fingerprint.
func proxyRuntimeUnusableSafetyEvent(
	inventory controlplane.RouterInventory,
	observedAt time.Time,
	memory *ProxyRuntimeStartFailure,
) (controlplane.RouterSafetyEvent, bool) {
	attempt, attempted := lastPasswallProxyStartAttempt(readLogTail(passwallLogPath, passwallLogTailBytes))
	if attempted && attempt.Failure == "" && memory != nil {
		// A proxy start has succeeded since: whatever failed before is fixed.
		*memory = ProxyRuntimeStartFailure{}
	}
	unseenFailure := attempted && attempt.Failure != "" &&
		(memory == nil || memory.Attempt != attempt.Closing)
	activeMemory := memory != nil && memory.Attempt != "" && !memory.Retired
	if !unseenFailure && !activeMemory && isExecutableFile(xrayPackageBinaryPath) {
		return controlplane.RouterSafetyEvent{}, false
	}

	nodeID := strings.TrimSpace(inventory.SelectedNodeID)
	if nodeID == "" {
		return controlplane.RouterSafetyEvent{}, false
	}
	if normalizeProxyRuntimeType(readProxyRuntimeUCI("passwall2."+nodeID+".type")) != "xray" {
		return controlplane.RouterSafetyEvent{}, false
	}

	executable := resolveXrayExecutable(readProxyRuntimeUCI("passwall2.@global_app[0].xray_file"))
	if executable.Missing != "" && executable.Effective == "" &&
		firstType(readProxyRuntimeUCI("passwall2.@global_app[0].sing_box_file"), singBoxFallbackBinaryPaths) != "" {
		// app.sh prefers run_singbox when XRAY_BIN resolves to nothing, even for
		// an xray-typed node: the node still runs, so nothing is missing.
		executable.Missing = ""
	}

	// The start-failure memory is brought up to date even when the binary is
	// missing: a failure first seen now is recorded against the runtime as it is
	// now ("missing"), so the binary coming back counts as a change of runtime.
	failure, startFailed := ProxyRuntimeStartFailure{}, false
	if unseenFailure || activeMemory {
		failure, startFailed = judgeProxyRuntimeStartFailure(
			attempt,
			attempted,
			memory,
			snapshotProxyRuntime(executable),
			observedAt,
		)
	}

	if executable.Missing != "" {
		configured := executable.Configured
		if configured == "" {
			configured = "(unset)"
		}
		// The missing path leads, so evidence truncation can never drop it.
		evidence := fmt.Sprintf(
			"%s is missing or not executable; passwall2.@global_app[0].xray_file=%s and no xray at %s",
			executable.Missing,
			configured,
			strings.Join(xrayFallbackBinaryPaths, ", "),
		)
		if executable.Effective == vectraXrayWrapperPath {
			evidence = fmt.Sprintf(
				"%s is missing or not executable; passwall2.@global_app[0].xray_file=%s is the Vectra wrapper, which execs it",
				executable.Missing,
				configured,
			)
		}
		return buildSafetyEvent(
			ProxyRuntimeUnusableEventType,
			"critical",
			"xray",
			"filesystem",
			fmt.Sprintf("xray binary %s missing", executable.Missing),
			observedAt,
			evidence,
		), true
	}

	if !startFailed {
		return controlplane.RouterSafetyEvent{}, false
	}
	message := "last PassWall proxy start failed in xray"
	if !attempted || attempt.Failure == "" {
		message = "last recorded PassWall proxy start failed in xray; the log no longer holds it"
	}
	event := buildSafetyEvent(
		ProxyRuntimeUnusableEventType,
		"critical",
		"xray",
		ProxyRuntimeStartFailureSource,
		message,
		observedAt,
		failure.Evidence,
	)
	if failure.ObservedAt != "" {
		// When the failure was first seen, not when it was last collected:
		// consumers time their periodic retry from it.
		event.ObservedAt = failure.ObservedAt
	}
	return event, true
}

// judgeProxyRuntimeStartFailure applies the ProxyRuntimeStartFailure memory to
// what the log shows.
//
// A failed attempt the memory has not seen yet is recorded against the
// current runtime and stands -- unless the runtime changed after the attempt
// ran. That happens at rollout: the last attempt in a log can predate a manual
// fix, and pinning it to the fixed runtime would strand a working router in
// direct. Such an attempt is recorded as already retired.
//
// The attempt the memory already holds -- or, when the log holds no proxy
// start at all, the remembered one -- stands only while the runtime is
// unchanged; once it changes, the memory is retired. A nil memory trusts the
// log as it is. Successful attempts are the caller's business: they never
// stand.
func judgeProxyRuntimeStartFailure(
	attempt passwallStartAttempt,
	attempted bool,
	memory *ProxyRuntimeStartFailure,
	runtime proxyRuntimeSnapshot,
	observedAt time.Time,
) (ProxyRuntimeStartFailure, bool) {
	if attempted && attempt.Failure != "" && (memory == nil || memory.Attempt != attempt.Closing) {
		observed := ProxyRuntimeStartFailure{
			Attempt:    attempt.Closing,
			Evidence:   attempt.Failure,
			Runtime:    runtime.Fingerprint,
			ObservedAt: observedAt.UTC().Format(time.RFC3339),
		}
		// The stamp has one-second resolution; anything changed within the
		// attempt's own second is taken to predate it.
		observed.Retired = !attempt.At.IsZero() && runtime.ChangedAt.After(attempt.At.Add(time.Second))
		if memory != nil {
			*memory = observed
		}
		if observed.Retired {
			return ProxyRuntimeStartFailure{}, false
		}
		return observed, true
	}
	if memory == nil || memory.Attempt == "" || memory.Retired {
		return ProxyRuntimeStartFailure{}, false
	}
	if memory.Runtime != runtime.Fingerprint {
		memory.Retired = true
		return ProxyRuntimeStartFailure{}, false
	}
	return *memory, true
}

type passwallStartAttempt struct {
	// Closing is the "Running complete!" line that ended the attempt.
	Closing string
	// Failure is the "Failed to start:" line the global xray instance printed,
	// empty when the attempt succeeded.
	Failure string
	// At is when the attempt ended, in UTC; zero when its stamp is unreadable.
	At      time.Time
	noProxy bool
}

// lastPasswallProxyStartAttempt finds the last PassWall start that actually
// tried to run a proxy.
//
// app.sh start always ends with "Running complete!", so the log splits into
// attempts at those lines. An attempt failed when the global xray instance
// failed: PassWall's stamped "process .../acl/default/global.json error" line,
// followed by the xray output it appends, which holds "Failed to start:". An
// attempt that only says "Running in no proxy mode" is PassWall starting with
// enabled=0 -- exactly what our own fallback to direct does -- so it says
// nothing about the runtime and is skipped; judging by it would let the
// fallback erase the evidence that justified it, and the router would flap
// between direct and a black hole. A failed start also says "no proxy mode"
// (PassWall drops the global ACL once xray refuses its config), so the failure
// is what decides. Output after the last "Running complete!" belongs to a
// start still in progress, or a stop, and is ignored.
func lastPasswallProxyStartAttempt(logTail string, logModTime time.Time) (passwallStartAttempt, bool) {
	last := passwallStartAttempt{}
	found := false
	current := passwallStartAttempt{}
	inGlobalFailure := false
	var lastStamp time.Time
	for _, line := range strings.Split(strings.ReplaceAll(logTail, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		stamp, stamped := passwallLogStamp(trimmed)
		if !stamped {
			// Runtime output PassWall appended; it belongs to the stamped line
			// before it.
			if inGlobalFailure && current.Failure == "" && strings.HasPrefix(trimmed, xrayStartFailurePrefix) {
				current.Failure = trimmed
			}
			continue
		}
		lastStamp = stamp
		inGlobalFailure = slices.ContainsFunc(passwallGlobalInstanceMarkers, func(marker string) bool {
			return strings.Contains(trimmed, marker)
		})
		switch {
		case slices.ContainsFunc(passwallStartCompleteMarkers, func(marker string) bool {
			return strings.HasSuffix(trimmed, marker)
		}):
			current.Closing = trimmed
			current.At = stamp
			if current.Failure != "" || !current.noProxy {
				last = current
				found = true
			}
			current = passwallStartAttempt{}
		case slices.ContainsFunc(passwallNoProxyModeMarkers, func(marker string) bool {
			return strings.Contains(trimmed, marker)
		}):
			current.noProxy = true
		}
	}
	if found && !last.At.IsZero() {
		last.At = last.At.Add(-passwallLogZoneOffset(lastStamp, logModTime))
	}
	return last, found
}

// passwallLogStamp parses echolog_date's prefix, taking the wall clock as if it
// were UTC; passwallLogZoneOffset corrects it.
func passwallLogStamp(line string) (time.Time, bool) {
	if len(line) < len(passwallLogStampLayout)+2 || line[len(passwallLogStampLayout):len(passwallLogStampLayout)+2] != ": " {
		return time.Time{}, false
	}
	stamp, err := time.Parse(passwallLogStampLayout, line[:len(passwallLogStampLayout)])
	if err != nil {
		return time.Time{}, false
	}
	return stamp, true
}

// passwallLogZoneOffset recovers the zone PassWall stamped its log in. busybox
// `date` honours OpenWrt's /etc/TZ, which Go does not read, so the stamps are
// wall-clock time in a zone the controller may not know (MSK on most of the
// fleet). The last stamped line was written when the file was last modified,
// so the difference between the two, rounded to the 15 minutes every zone
// offset is a multiple of, is the zone offset.
func passwallLogZoneOffset(lastStamp time.Time, logModTime time.Time) time.Duration {
	if lastStamp.IsZero() || logModTime.IsZero() {
		return 0
	}
	return lastStamp.Sub(logModTime.UTC()).Round(15 * time.Minute)
}

// readLogTail returns at most the last limit bytes of path, starting on a line
// boundary, and the file's modification time. A missing or unreadable file
// reads as empty.
func readLogTail(path string, limit int64) (string, time.Time) {
	file, err := os.Open(path)
	if err != nil {
		return "", time.Time{}
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", time.Time{}
	}
	offset := info.Size() - limit
	if offset < 0 {
		offset = 0
	}
	content, err := io.ReadAll(io.NewSectionReader(file, offset, info.Size()-offset))
	if err != nil {
		return "", time.Time{}
	}
	text := string(content)
	if offset > 0 {
		// The window starts mid-line; drop the fragment.
		_, text, _ = strings.Cut(text, "\n")
	}
	return text, info.ModTime()
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// firstType mirrors PassWall2's first_type(): the configured path wins when it
// is absolute and executable, otherwise the first executable fallback does.
func firstType(configured string, fallbacks []string) string {
	configured = strings.TrimSpace(configured)
	if strings.HasPrefix(configured, "/") && isExecutableFile(configured) {
		return configured
	}
	for _, candidate := range fallbacks {
		if isExecutableFile(candidate) {
			return candidate
		}
	}
	return ""
}

// isShellScript reports whether path starts with "#!".
func isShellScript(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	head := make([]byte, 2)
	n, _ := io.ReadFull(file, head)
	return n == 2 && string(head) == "#!"
}

type xrayExecutable struct {
	// Configured is passwall2.@global_app[0].xray_file as set, possibly empty.
	Configured string
	// Effective is what PassWall will exec, empty when nothing is runnable.
	Effective string
	// Binary is the real xray behind Effective: the wrapper script execs
	// xrayPackageBinaryPath.
	Binary string
	// Missing names the path whose absence makes xray unrunnable.
	Missing string
}

// resolveXrayExecutable mirrors PassWall2's `first_type $(xray_file) xray`: a
// stale xray_file breaks nothing while /usr/bin/xray exists and must not be
// reported as if it did. The one indirection PassWall cannot see through is
// the Vectra wrapper script: it is perfectly executable, so PassWall picks it,
// and it then execs /usr/bin/xray -- which is how andrey-avito armed its
// interception with no xray behind it. The redirect applies only while the
// wrapper path really holds that script: once PassWall's LuCI updater has
// written a raw xray binary over it, the file at that path IS the runtime, and
// a missing /usr/bin/xray is irrelevant.
func resolveXrayExecutable(configured string) xrayExecutable {
	resolved := xrayExecutable{Configured: strings.TrimSpace(configured)}
	resolved.Effective = firstType(resolved.Configured, xrayFallbackBinaryPaths)

	switch {
	case resolved.Effective == "":
		resolved.Binary = xrayPackageBinaryPath
		if strings.HasPrefix(resolved.Configured, "/") && resolved.Configured != vectraXrayWrapperPath {
			resolved.Binary = resolved.Configured
		}
		resolved.Missing = resolved.Binary
	case resolved.Effective == vectraXrayWrapperPath && isShellScript(resolved.Effective):
		resolved.Binary = xrayPackageBinaryPath
		if !isExecutableFile(resolved.Binary) {
			resolved.Missing = resolved.Binary
		}
	default:
		resolved.Binary = resolved.Effective
	}
	return resolved
}

// proxyRuntimeSnapshot is what a PassWall proxy start depends on.
type proxyRuntimeSnapshot struct {
	// Fingerprint changes whenever something the start depends on does.
	Fingerprint string
	// ChangedAt is the latest modification among those inputs.
	ChangedAt time.Time
}

// snapshotProxyRuntime fingerprints everything a PassWall proxy start runs on,
// so that any repair -- not only a new binary -- retires a remembered failure:
//   - the file PassWall execs and the binary behind it. When the wrapper path
//     is in use it is always one of the two -- the script in front of
//     /usr/bin/xray, or the raw binary the LuCI updater wrote over it -- so
//     restoring the wrapper script over a clobbered binary is a change even
//     when /usr/bin/xray is untouched;
//   - luci-app-passwall2, which generates the config;
//   - /etc/config/passwall2, which the config is generated from: a subscription
//     refresh, a rules or route-policy apply can fix a node or shunt xray
//     refused;
//   - geosite.dat/geoip.dat in v2ray_location_asset: a missing geosite code is
//     a "Failed to start:" that a rules refresh or compact_geodata fixes.
//
// The UCI file is fingerprinted by content, not mtime, with the switches that
// flip on their own masked: our own fallback to direct commits enabled=0, and
// PassWall's stop() rewrites dnsmasq_dns_redirect. Fingerprinting their mtime
// would retire the verdict one cycle after we acted on it. ChangedAt does use
// the raw mtime: it only matters for an attempt not recorded yet.
//
// Only stats, and reads of the UCI file and an opkg control file; no process
// is spawned.
func snapshotProxyRuntime(executable xrayExecutable) proxyRuntimeSnapshot {
	snapshot := proxyRuntimeSnapshot{}
	parts := make([]string, 0, 8)
	noteFile := func(label string, path string) {
		info, err := os.Stat(path)
		if err != nil {
			parts = append(parts, fmt.Sprintf("%s %s missing", label, path))
			return
		}
		parts = append(parts, fmt.Sprintf(
			"%s %s size=%d mtime=%s",
			label,
			path,
			info.Size(),
			info.ModTime().UTC().Format(time.RFC3339Nano),
		))
		if info.ModTime().After(snapshot.ChangedAt) {
			snapshot.ChangedAt = info.ModTime()
		}
	}

	if executable.Effective != "" && executable.Effective != executable.Binary {
		noteFile("xray_file", executable.Effective)
	}
	noteFile("xray", executable.Binary)
	noteFile("luci-app-passwall2 "+packageVersion("luci-app-passwall2"), filepath.Join(opkgInfoDir, "luci-app-passwall2.control"))

	config := ""
	if info, err := os.Stat(passwallConfigPath); err == nil {
		if content, err := os.ReadFile(passwallConfigPath); err == nil {
			config = string(content)
		}
		if info.ModTime().After(snapshot.ChangedAt) {
			snapshot.ChangedAt = info.ModTime()
		}
	}
	parts = append(parts, "passwall2 config "+passwallConfigDigest(config))

	assetDirectory := uciFileOption(config, "global_rules", "v2ray_location_asset")
	if assetDirectory == "" {
		assetDirectory = defaultV2rayAssetDirectory
	}
	noteFile("geosite", filepath.Join(assetDirectory, "geosite.dat"))
	noteFile("geoip", filepath.Join(assetDirectory, "geoip.dat"))

	snapshot.Fingerprint = strings.Join(parts, "; ")
	return snapshot
}

// passwallConfigDigest hashes a UCI file by its tokens, so libuci re-quoting or
// re-indenting a file on commit does not count as a change, and leaves out the
// options that flip without changing what xray is given (see
// snapshotProxyRuntime).
func passwallConfigDigest(content string) string {
	hash := sha256.New()
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields) >= 2 && fields[0] == "option" &&
			(fields[1] == "enabled" || fields[1] == "dnsmasq_dns_redirect") {
			continue
		}
		for index, field := range fields {
			fields[index] = strings.Trim(field, `'"`)
		}
		hash.Write([]byte(strings.Join(fields, " ")))
		hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// uciFileOption returns an option of the first section of sectionType in a UCI
// file, the way `uci get <config>.@<sectionType>[0].<option>` would; read from
// the text already in hand rather than by forking uci.
func uciFileOption(content string, sectionType string, option string) string {
	inSection := false
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "config":
			if inSection {
				return ""
			}
			inSection = len(fields) >= 2 && strings.Trim(fields[1], `'"`) == sectionType
		case "option":
			if inSection && len(fields) >= 3 && fields[1] == option {
				return strings.Trim(strings.Join(fields[2:], " "), `'"`)
			}
		}
	}
	return ""
}

func shouldCollectSafetyDiagnostics(resources controlplane.RouterResources) bool {
	return resources.MemoryAvailableMB >= safetyDiagnosticsMemoryFloorMB
}

func collectCachedSafetyDiagnostics(now time.Time) []controlplane.RouterSafetyEvent {
	safetyDiagnosticsCache.mu.Lock()
	if safetyDiagnosticsCache.events != nil && now.Before(safetyDiagnosticsCache.expiresAt) {
		cached := cloneSafetyEvents(safetyDiagnosticsCache.events)
		safetyDiagnosticsCache.mu.Unlock()
		return cached
	}
	safetyDiagnosticsCache.mu.Unlock()

	events := collectSafetyDiagnostics(now)

	safetyDiagnosticsCache.mu.Lock()
	safetyDiagnosticsCache.events = cloneSafetyEvents(events)
	safetyDiagnosticsCache.expiresAt = now.Add(safetyDiagnosticsCacheTTL)
	safetyDiagnosticsCache.mu.Unlock()

	return cloneSafetyEvents(events)
}

func collectSafetyDiagnostics(observedAt time.Time) []controlplane.RouterSafetyEvent {
	events := make([]controlplane.RouterSafetyEvent, 0)
	for _, source := range []struct {
		name    string
		command []string
	}{
		{
			name:    "logread",
			command: []string{"logread", "-l", strconv.Itoa(safetyLogLines)},
		},
		{
			name:    "dmesg",
			command: []string{"sh", "-c", fmt.Sprintf("dmesg | tail -n %d", safetyLogLines)},
		},
	} {
		output := boundedCommandOutput(safetyDiagnosticsTimeout, source.command[0], source.command[1:]...)
		events = append(events, parseSafetyDiagnostics(source.name, output, observedAt)...)
	}
	return events
}

func boundedCommandOutput(timeout time.Duration, binary string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil && len(output) == 0 {
		return ""
	}
	return string(output)
}

func parseSafetyDiagnostics(
	source string,
	output string,
	observedAt time.Time,
) []controlplane.RouterSafetyEvent {
	events := make([]controlplane.RouterSafetyEvent, 0)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		normalized := strings.ToLower(line)
		component := detectSafetyComponent(normalized, line)
		switch {
		case oomSafetyPattern.MatchString(line):
			if component == "" {
				component = "kernel"
			}
			severity := "warning"
			if isProxyRuntimeComponent(component) || strings.Contains(normalized, "killed process") {
				severity = "critical"
			}
			events = append(events, buildSafetyEvent(
				"oom_kill",
				severity,
				component,
				source,
				fmt.Sprintf("OOM pressure mentioned %s", component),
				observedAt,
				line,
			))
		case component != "" && crashSafetyPattern.MatchString(line):
			events = append(events, buildSafetyEvent(
				"runtime_crash",
				"warning",
				component,
				source,
				fmt.Sprintf("runtime log mentioned %s instability", component),
				observedAt,
				line,
			))
		}
	}

	return events
}

func detectSafetyComponent(normalizedLine string, originalLine string) string {
	if matches := killedProcessComponentPattern.FindStringSubmatch(originalLine); len(matches) == 2 {
		candidate := normalizeSafetyComponent(matches[1])
		if candidate != "" {
			return candidate
		}
	}

	for _, component := range []string{
		"xray",
		"sing-box",
		"hysteria",
		"geoview",
		"dnsmasq",
		"chinadns",
		"passwall2_server",
		"passwall2",
		"passwall",
		"vectra-controller",
		"vectra-controller-agent",
	} {
		if strings.Contains(normalizedLine, component) {
			return normalizeSafetyComponent(component)
		}
	}

	return ""
}

func normalizeSafetyComponent(component string) string {
	normalized := strings.ToLower(strings.TrimSpace(component))
	normalized = strings.Trim(normalized, `"'`)
	switch normalized {
	case "xray", "sing-box", "hysteria", "geoview", "dnsmasq", "chinadns":
		return normalized
	case "passwall", "passwall2", "passwall2_server":
		return normalized
	case "vectra-controller", "vectra-controller-agent":
		return "vectra-controller"
	default:
		return ""
	}
}

func isProxyRuntimeComponent(component string) bool {
	switch component {
	case "xray", "sing-box", "hysteria", "geoview", "passwall", "passwall2", "passwall2_server":
		return true
	default:
		return false
	}
}

func buildSafetyEvent(
	eventType string,
	severity string,
	component string,
	source string,
	message string,
	observedAt time.Time,
	evidence string,
) controlplane.RouterSafetyEvent {
	return controlplane.RouterSafetyEvent{
		Type:       eventType,
		Severity:   severity,
		Component:  component,
		Source:     source,
		Message:    message,
		ObservedAt: observedAt.UTC().Format(time.RFC3339),
		Evidence:   truncateSafetyEvidence(evidence),
	}
}

func truncateSafetyEvidence(evidence string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(evidence, "\r\n", "\n"))
	if len(trimmed) <= 240 {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:237]) + "..."
}

func cloneSafetyEvents(events []controlplane.RouterSafetyEvent) []controlplane.RouterSafetyEvent {
	if events == nil {
		return nil
	}
	cloned := make([]controlplane.RouterSafetyEvent, len(events))
	copy(cloned, events)
	return cloned
}

func dedupeSafetyEvents(events []controlplane.RouterSafetyEvent) []controlplane.RouterSafetyEvent {
	seen := make(map[string]struct{}, len(events))
	deduped := make([]controlplane.RouterSafetyEvent, 0, len(events))
	for _, event := range events {
		key := strings.Join([]string{
			event.Type,
			event.Severity,
			event.Component,
			event.Source,
			event.Evidence,
		}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, event)
	}
	return deduped
}

func limitSafetyEvents(
	events []controlplane.RouterSafetyEvent,
	limit int,
) []controlplane.RouterSafetyEvent {
	if limit <= 0 || len(events) <= limit {
		return events
	}
	return events[:limit]
}

func diskFreeMB(path string) int {
	for _, args := range [][]string{
		{"-kP", path},
		{"-k", path},
	} {
		output, err := exec.Command("df", args...).Output()
		if err != nil {
			continue
		}
		if value := parseDFAvailableMB(string(output)); value >= 0 {
			return value
		}
	}

	return 0
}

func parseDFAvailableMB(output string) int {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return -1
	}

	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return -1
	}

	valueKB, err := strconv.Atoi(fields[3])
	if err != nil {
		return -1
	}
	return valueKB / 1024
}

func collectRulesAssets() controlplane.RouterRulesAssets {
	assetDirectory := readUCI("passwall2.@global_rules[0].v2ray_location_asset")
	if assetDirectory == "" {
		assetDirectory = "/usr/share/v2ray/"
	}

	geoipPath := filepath.Join(assetDirectory, "geoip.dat")
	geositePath := filepath.Join(assetDirectory, "geosite.dat")

	return controlplane.RouterRulesAssets{
		AssetDirectory:   assetDirectory,
		GeoIPVersion:     ruleAssetVersion(assetDirectory, "geoip", geoipPath),
		GeoSiteVersion:   ruleAssetVersion(assetDirectory, "geosite", geositePath),
		GeoIPUpdatedAt:   fileUpdatedAt(geoipPath),
		GeoSiteUpdatedAt: fileUpdatedAt(geositePath),
	}
}

func ruleAssetVersion(assetDirectory string, stem string, artifactPath string) string {
	candidateFiles := []string{
		filepath.Join(assetDirectory, stem+".version"),
		filepath.Join(assetDirectory, stem+"_version"),
		filepath.Join(assetDirectory, stem+".dat.version"),
	}

	for _, candidate := range candidateFiles {
		if value, err := os.ReadFile(candidate); err == nil {
			text := strings.TrimSpace(string(value))
			if text != "" {
				return text
			}
		}
	}

	info, err := os.Stat(artifactPath)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("size:%d", info.Size())
}

func fileUpdatedAt(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339)
}

func serviceState(script string) string {
	if _, err := os.Stat(script); err != nil {
		return "unknown"
	}

	if err := exec.Command(script, "running").Run(); err == nil {
		return "running"
	}

	if err := exec.Command(script, "enabled").Run(); err == nil {
		return "stopped"
	}

	return "degraded"
}

func collectTelegramReachability(
	mode serviceReachabilityMode,
) *controlplane.RouterReachabilityProbe {
	now := time.Now().UTC()

	telegramProbeCache.mu.Lock()
	if telegramProbeCache.result != nil && now.Before(telegramProbeCache.expiresAt) {
		cached := cloneTelegramReachability(telegramProbeCache.result)
		telegramProbeCache.mu.Unlock()
		return cached
	}
	blockedStreak := telegramProbeCache.blockedStreak
	telegramProbeCache.mu.Unlock()

	targets := telegramTargetsFor(mode)
	prober := rescue.NewHTTPProber(telegramProbeTimeout)
	checks := make([]controlplane.RouterReachabilityProbe, 0, len(targets))
	for _, target := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), telegramProbeTimeout)
		result := prober.Probe(ctx, target.URL)
		cancel()
		checks = append(checks, buildTelegramReachabilityCheck(target, result))
	}
	probe := buildTelegramReachabilitySummary(checks)
	if probe == nil {
		return nil
	}

	ttl, streak := nextServiceReachabilityCache(
		telegramProbeCacheTTL,
		probe,
		blockedStreak,
	)

	telegramProbeCache.mu.Lock()
	telegramProbeCache.result = cloneTelegramReachability(probe)
	telegramProbeCache.expiresAt = now.Add(ttl)
	telegramProbeCache.blockedStreak = streak
	telegramProbeCache.mu.Unlock()

	return cloneTelegramReachability(probe)
}

func collectYouTubeReachability(
	mode serviceReachabilityMode,
) *controlplane.RouterReachabilityProbe {
	now := time.Now().UTC()

	youtubeProbeCache.mu.Lock()
	if youtubeProbeCache.result != nil && now.Before(youtubeProbeCache.expiresAt) {
		cached := cloneYouTubeReachability(youtubeProbeCache.result)
		youtubeProbeCache.mu.Unlock()
		return cached
	}
	blockedStreak := youtubeProbeCache.blockedStreak
	youtubeProbeCache.mu.Unlock()

	targets := youtubeTargetsFor(mode)
	prober := rescue.NewHTTPProber(youtubeProbeTimeout)
	checks := make([]controlplane.RouterReachabilityProbe, 0, len(targets))
	for _, target := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), youtubeProbeTimeout)
		result := prober.Probe(ctx, target.URL)
		cancel()
		checks = append(checks, buildYouTubeReachabilityCheck(target, result))
	}
	probe := buildYouTubeReachabilitySummary(checks)
	if probe == nil {
		return nil
	}

	ttl, streak := nextServiceReachabilityCache(
		youtubeProbeCacheTTL,
		probe,
		blockedStreak,
	)

	youtubeProbeCache.mu.Lock()
	youtubeProbeCache.result = cloneYouTubeReachability(probe)
	youtubeProbeCache.expiresAt = now.Add(ttl)
	youtubeProbeCache.blockedStreak = streak
	youtubeProbeCache.mu.Unlock()

	return cloneYouTubeReachability(probe)
}

func buildTelegramReachabilityCheck(
	target telegramProbeTarget,
	result rescue.HTTPProbeResult,
) controlplane.RouterReachabilityProbe {
	targetURL := strings.TrimSpace(result.URL)
	if targetURL == "" {
		targetURL = target.URL
	}

	checkedAt := result.CheckedAt.UTC()
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}

	probe := controlplane.RouterReachabilityProbe{
		ID:        target.ID,
		Label:     target.Label,
		Reachable: result.Reachable,
		CheckedAt: checkedAt.Format(time.RFC3339),
		TargetURL: targetURL,
	}
	if result.StatusCode > 0 {
		probe.StatusCode = result.StatusCode
	}
	if result.Error != "" {
		probe.Error = normalizeProbeError(result.Error)
	}

	return probe
}

func buildYouTubeReachabilityCheck(
	target youtubeProbeTarget,
	result rescue.HTTPProbeResult,
) controlplane.RouterReachabilityProbe {
	targetURL := strings.TrimSpace(result.URL)
	if targetURL == "" {
		targetURL = target.URL
	}

	checkedAt := result.CheckedAt.UTC()
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}

	probe := controlplane.RouterReachabilityProbe{
		ID:        target.ID,
		Label:     target.Label,
		Reachable: result.Reachable,
		CheckedAt: checkedAt.Format(time.RFC3339),
		TargetURL: targetURL,
	}
	if result.StatusCode > 0 {
		probe.StatusCode = result.StatusCode
	}
	if result.Error != "" {
		probe.Error = normalizeProbeError(result.Error)
	}

	return probe
}

func buildTelegramReachabilitySummary(
	checks []controlplane.RouterReachabilityProbe,
) *controlplane.RouterReachabilityProbe {
	if len(checks) == 0 {
		return nil
	}

	reachableCount := 0
	checkedAt := checks[len(checks)-1].CheckedAt
	for _, check := range checks {
		if check.Reachable {
			reachableCount++
		}
		if strings.TrimSpace(check.CheckedAt) != "" {
			checkedAt = check.CheckedAt
		}
	}

	status := "blocked"
	reachable := false
	switch {
	case reachableCount == len(checks):
		status = "reachable"
		reachable = true
	case reachableCount > 0:
		status = "partial"
	}

	return &controlplane.RouterReachabilityProbe{
		Reachable:      reachable,
		CheckedAt:      checkedAt,
		Status:         status,
		ReachableCount: reachableCount,
		TotalCount:     len(checks),
		Checks:         append([]controlplane.RouterReachabilityProbe(nil), checks...),
	}
}

func buildYouTubeReachabilitySummary(
	checks []controlplane.RouterReachabilityProbe,
) *controlplane.RouterReachabilityProbe {
	if len(checks) == 0 {
		return nil
	}

	reachableCount := 0
	checkedAt := checks[len(checks)-1].CheckedAt
	for _, check := range checks {
		if check.Reachable {
			reachableCount++
		}
		if strings.TrimSpace(check.CheckedAt) != "" {
			checkedAt = check.CheckedAt
		}
	}

	status := "blocked"
	reachable := false
	switch {
	case reachableCount == len(checks):
		status = "reachable"
		reachable = true
	case reachableCount > 0:
		status = "partial"
	}

	return &controlplane.RouterReachabilityProbe{
		Reachable:      reachable,
		CheckedAt:      checkedAt,
		Status:         status,
		ReachableCount: reachableCount,
		TotalCount:     len(checks),
		Checks:         append([]controlplane.RouterReachabilityProbe(nil), checks...),
	}
}

func cloneTelegramReachability(
	probe *controlplane.RouterReachabilityProbe,
) *controlplane.RouterReachabilityProbe {
	if probe == nil {
		return nil
	}

	cloned := *probe
	if len(probe.Checks) > 0 {
		cloned.Checks = append([]controlplane.RouterReachabilityProbe(nil), probe.Checks...)
	}
	return &cloned
}

func cloneYouTubeReachability(
	probe *controlplane.RouterReachabilityProbe,
) *controlplane.RouterReachabilityProbe {
	if probe == nil {
		return nil
	}

	cloned := *probe
	if len(probe.Checks) > 0 {
		cloned.Checks = append([]controlplane.RouterReachabilityProbe(nil), probe.Checks...)
	}
	return &cloned
}

func collectInstagramReachability(
	mode serviceReachabilityMode,
) *controlplane.RouterReachabilityProbe {
	now := time.Now().UTC()

	instagramProbeCache.mu.Lock()
	if instagramProbeCache.result != nil && now.Before(instagramProbeCache.expiresAt) {
		cached := cloneInstagramReachability(instagramProbeCache.result)
		instagramProbeCache.mu.Unlock()
		return cached
	}
	blockedStreak := instagramProbeCache.blockedStreak
	instagramProbeCache.mu.Unlock()

	targets := instagramTargetsFor(mode)
	prober := rescue.NewHTTPProber(instagramProbeTimeout)
	checks := make([]controlplane.RouterReachabilityProbe, 0, len(targets))
	for _, target := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), instagramProbeTimeout)
		result := prober.Probe(ctx, target.URL)
		cancel()
		checks = append(checks, buildInstagramReachabilityCheck(target, result))
	}
	probe := buildInstagramReachabilitySummary(checks)
	if probe == nil {
		return nil
	}

	ttl, streak := nextServiceReachabilityCache(
		instagramProbeCacheTTL,
		probe,
		blockedStreak,
	)

	instagramProbeCache.mu.Lock()
	instagramProbeCache.result = cloneInstagramReachability(probe)
	instagramProbeCache.expiresAt = now.Add(ttl)
	instagramProbeCache.blockedStreak = streak
	instagramProbeCache.mu.Unlock()

	return cloneInstagramReachability(probe)
}

func buildInstagramReachabilityCheck(
	target instagramProbeTarget,
	result rescue.HTTPProbeResult,
) controlplane.RouterReachabilityProbe {
	targetURL := strings.TrimSpace(result.URL)
	if targetURL == "" {
		targetURL = target.URL
	}

	checkedAt := result.CheckedAt.UTC()
	if checkedAt.IsZero() {
		checkedAt = time.Now().UTC()
	}

	probe := controlplane.RouterReachabilityProbe{
		ID:        target.ID,
		Label:     target.Label,
		Reachable: result.Reachable,
		CheckedAt: checkedAt.Format(time.RFC3339),
		TargetURL: targetURL,
	}
	if result.StatusCode > 0 {
		probe.StatusCode = result.StatusCode
	}
	if result.Error != "" {
		probe.Error = normalizeProbeError(result.Error)
	}

	return probe
}

func buildInstagramReachabilitySummary(
	checks []controlplane.RouterReachabilityProbe,
) *controlplane.RouterReachabilityProbe {
	if len(checks) == 0 {
		return nil
	}

	reachableCount := 0
	checkedAt := checks[len(checks)-1].CheckedAt
	for _, check := range checks {
		if check.Reachable {
			reachableCount++
		}
		if strings.TrimSpace(check.CheckedAt) != "" {
			checkedAt = check.CheckedAt
		}
	}

	status := "blocked"
	reachable := false
	switch {
	case reachableCount == len(checks):
		status = "reachable"
		reachable = true
	case reachableCount > 0:
		status = "partial"
	}

	return &controlplane.RouterReachabilityProbe{
		Reachable:      reachable,
		CheckedAt:      checkedAt,
		Status:         status,
		ReachableCount: reachableCount,
		TotalCount:     len(checks),
		Checks:         append([]controlplane.RouterReachabilityProbe(nil), checks...),
	}
}

func cloneInstagramReachability(
	probe *controlplane.RouterReachabilityProbe,
) *controlplane.RouterReachabilityProbe {
	if probe == nil {
		return nil
	}

	cloned := *probe
	if len(probe.Checks) > 0 {
		cloned.Checks = append([]controlplane.RouterReachabilityProbe(nil), probe.Checks...)
	}
	return &cloned
}

func normalizeProbeError(value string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(normalized) > 160 {
		return normalized[:157] + "..."
	}
	return normalized
}
