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
var opkgInfoDir = "/usr/lib/opkg/info"

const telegramProbeURL = "https://telegram.org/"
const telegramProbeTimeout = 3 * time.Second
const telegramProbeCacheTTL = 5 * time.Minute

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

var telegramProbeCache = struct {
	mu        sync.Mutex
	result    *controlplane.RouterReachabilityProbe
	expiresAt time.Time
}{}

var passwallInventoryPackages = []string{
	"luci-app-vectra-controller",
	"luci-app-passwall2",
	"xray-core",
	"sing-box",
	"hysteria",
	"geoview",
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

	setBinaryVersion(&inventory, "xray", commandVersion("/usr/bin/xray", "-version"))
	setBinaryVersion(&inventory, "sing-box", commandVersion("/usr/bin/sing-box", "version"))
	setBinaryVersion(&inventory, "hysteria", commandVersion("/usr/bin/hysteria", "version"))
	setBinaryVersion(&inventory, "geoview", commandVersion("/usr/bin/geoview", "-version"))
	setBinaryVersion(&inventory, "dnsmasq", firstLine("dnsmasq", "-v"))

	inventory.Resources = collectResources()
	inventory.RulesAssets = collectRulesAssets()
	inventory.ServiceHealth = controlplane.RouterServiceHealth{
		Controller:     serviceState("/etc/init.d/vectra-controller"),
		Passwall:       serviceState("/etc/init.d/passwall2"),
		PasswallServer: serviceState("/etc/init.d/passwall2_server"),
		DNSMasq:        serviceState("/etc/init.d/dnsmasq"),
	}
	inventory.TelegramReachability = collectTelegramReachability()

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
	command := "uci -q show passwall2 2>/dev/null | grep '=" + sectionType + "' | wc -l"
	output, err := exec.Command("sh", "-c", command).Output()
	if err != nil {
		return 0
	}

	count, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0
	}

	return count
}

func packageVersion(name string) string {
	if version := packageVersionFromOpkgStatus(name); version != "" {
		return version
	}

	// Self-update can leave opkg in a half-installed state where the new package
	// control metadata is already present, but `opkg status <name>` returns
	// nothing. In that case we still want inventory to surface the unpacked
	// version instead of falling back to "unknown".
	return packageVersionFromControlFile(
		filepath.Join(opkgInfoDir, name+".control"),
	)
}

func packageVersionFromOpkgStatus(name string) string {
	output, err := exec.Command(
		"sh",
		"-c",
		"opkg status '"+name+"' 2>/dev/null | awk -F': ' '/^Version: / { print $2; exit }'",
	).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
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
	switch {
	case strings.Contains(normalized, "ubootmod"):
		return "ubootmod"
	case normalized == "xiaomi,mi-router-ax3000t":
		cmdline, err := os.ReadFile("/proc/cmdline")
		if err == nil && strings.Contains(string(cmdline), "firmware=") {
			return "stock-layout"
		}
		return "stock-layout"
	default:
		return ""
	}
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

func normalizeProbeError(value string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(normalized) > 160 {
		return normalized[:157] + "..."
	}
	return normalized
}
