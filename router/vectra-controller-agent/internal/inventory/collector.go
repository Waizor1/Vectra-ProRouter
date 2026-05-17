package inventory

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/rescue"
)

type Collector struct{}

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
const youtubeProbeTimeout = 3 * time.Second
const youtubeProbeCacheTTL = 30 * time.Minute
const lowMemoryExpensiveProbeFloorMB = 64
const serviceReachabilityProbeFloorMB = 128
const safetyDiagnosticsCacheTTL = 10 * time.Minute
const safetyDiagnosticsTimeout = 2 * time.Second
const proxyRuntimeProbeTimeout = time.Second
const passwallGlobalRuntimeConfigPath = "/tmp/etc/passwall2/acl/default/global.json"
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
	{ID: "youtube-main", Label: "youtube.com", URL: youtubeProbeURL},
	{ID: "youtube-img", Label: "i.ytimg.com", URL: "https://i.ytimg.com/generate_204"},
	{ID: "youtube-api", Label: "youtubei.googleapis.com", URL: "https://youtubei.googleapis.com/generate_204"},
}

var telegramProbeCache = struct {
	mu        sync.Mutex
	result    *controlplane.RouterReachabilityProbe
	expiresAt time.Time
}{}

var youtubeProbeCache = struct {
	mu        sync.Mutex
	result    *controlplane.RouterReachabilityProbe
	expiresAt time.Time
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

func (Collector) Collect(base controlplane.RouterInventory) controlplane.RouterInventory {
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
	inventory.SafetyEvents = collectSafetyEvents(inventory)
	if shouldCollectServiceReachability(inventory) {
		inventory.TelegramReachability = collectTelegramReachability()
		inventory.YouTubeReachability = collectYouTubeReachability()
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

func shouldCollectServiceReachability(inventory controlplane.RouterInventory) bool {
	if !inventory.PasswallEnabled {
		return false
	}
	if inventory.ServiceHealth.Passwall != "running" {
		return false
	}
	return inventory.Resources.MemoryAvailableMB >= serviceReachabilityProbeFloorMB
}

func collectSafetyEvents(inventory controlplane.RouterInventory) []controlplane.RouterSafetyEvent {
	events := make([]controlplane.RouterSafetyEvent, 0, maxSafetyEvents)
	now := time.Now().UTC()

	events = append(events, resourceSafetyEvents(inventory.Resources, now)...)
	events = append(events, serviceSafetyEvents(inventory, now)...)
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
		return processTableHasRuntimeConfig(component, passwallGlobalRuntimeConfigPath)
	}
	return strings.TrimSpace(boundedCommandOutput(proxyRuntimeProbeTimeout, "pidof", component)) != ""
}

func proxyRuntimeMissingEvidence(runtime string, nodeID string, rawType string) string {
	if runtime == "xray" || runtime == "sing-box" {
		return fmt.Sprintf(
			"process table has no %s using %s; selected node %s type=%s",
			runtime,
			passwallGlobalRuntimeConfigPath,
			nodeID,
			strings.TrimSpace(rawType),
		)
	}
	return fmt.Sprintf("pidof %s returned no pid; selected node %s type=%s", runtime, nodeID, strings.TrimSpace(rawType))
}

func processTableHasRuntimeConfig(component string, configPath string) bool {
	component = strings.TrimSpace(component)
	configPath = strings.TrimSpace(configPath)
	if component == "" || configPath == "" {
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
		if processCommandMatchesRuntimeConfig(cmdline, component, configPath) {
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

func collectTelegramReachability() *controlplane.RouterReachabilityProbe {
	now := time.Now().UTC()

	telegramProbeCache.mu.Lock()
	if telegramProbeCache.result != nil && now.Before(telegramProbeCache.expiresAt) {
		cached := cloneTelegramReachability(telegramProbeCache.result)
		telegramProbeCache.mu.Unlock()
		return cached
	}
	telegramProbeCache.mu.Unlock()

	prober := rescue.NewHTTPProber(telegramProbeTimeout)
	checks := make([]controlplane.RouterReachabilityProbe, 0, len(telegramProbeTargets))
	for _, target := range telegramProbeTargets {
		ctx, cancel := context.WithTimeout(context.Background(), telegramProbeTimeout)
		result := prober.Probe(ctx, target.URL)
		cancel()
		checks = append(checks, buildTelegramReachabilityCheck(target, result))
	}
	probe := buildTelegramReachabilitySummary(checks)
	if probe == nil {
		return nil
	}

	telegramProbeCache.mu.Lock()
	telegramProbeCache.result = cloneTelegramReachability(probe)
	telegramProbeCache.expiresAt = now.Add(telegramProbeCacheTTL)
	telegramProbeCache.mu.Unlock()

	return cloneTelegramReachability(probe)
}

func collectYouTubeReachability() *controlplane.RouterReachabilityProbe {
	now := time.Now().UTC()

	youtubeProbeCache.mu.Lock()
	if youtubeProbeCache.result != nil && now.Before(youtubeProbeCache.expiresAt) {
		cached := cloneYouTubeReachability(youtubeProbeCache.result)
		youtubeProbeCache.mu.Unlock()
		return cached
	}
	youtubeProbeCache.mu.Unlock()

	prober := rescue.NewHTTPProber(youtubeProbeTimeout)
	checks := make([]controlplane.RouterReachabilityProbe, 0, len(youtubeProbeTargets))
	for _, target := range youtubeProbeTargets {
		ctx, cancel := context.WithTimeout(context.Background(), youtubeProbeTimeout)
		result := prober.Probe(ctx, target.URL)
		cancel()
		checks = append(checks, buildYouTubeReachabilityCheck(target, result))
	}
	probe := buildYouTubeReachabilitySummary(checks)
	if probe == nil {
		return nil
	}

	youtubeProbeCache.mu.Lock()
	youtubeProbeCache.result = cloneYouTubeReachability(probe)
	youtubeProbeCache.expiresAt = now.Add(youtubeProbeCacheTTL)
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

func normalizeProbeError(value string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(normalized) > 160 {
		return normalized[:157] + "..."
	}
	return normalized
}
