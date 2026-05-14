package passwall

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

const FleetRoutePolicyVersion = "2026-05-12-v1"

type FleetRoutePolicyIdentity struct {
	Name             string
	DisplayName      string
	Hostname         string
	DeviceIdentifier string
}

type fleetRoutePolicySlot struct {
	ID                 string
	Expected           string
	RequiredRuleExtras map[string]string
	RequiredNodeExtras map[string]string
}

var fleetRoutePolicyExceptionValues = map[string]struct{}{
	"hh": {},
}

var fleetRoutePolicySlots = []fleetRoutePolicySlot{
	{ID: "WorldProxy", Expected: "RU-entry Germany"},
	{ID: "YouTube", Expected: "RU Russia"},
	{ID: "Special", Expected: "Netherlands"},
	{ID: "Tiktok", Expected: "Belarus"},
	{
		ID:       "DiscordVoiceUdp",
		Expected: "RU-entry Poland + UDP/mux/xudp tuning",
		RequiredRuleExtras: map[string]string{
			"network": "udp",
			"port":    "19294-19344,50000-50100",
		},
		RequiredNodeExtras: map[string]string{
			"mux":              "1",
			"mux_concurrency":  "-1",
			"xudp_concurrency": "16",
		},
	},
}

var nonIdentityChars = regexp.MustCompile(`[^a-zа-я0-9-]+`)
var textSeparators = regexp.MustCompile(`[_|()\[\]{}:;,.]+`)

func IsFleetRoutePolicyExempt(identity FleetRoutePolicyIdentity) bool {
	values := []string{identity.Name, identity.DisplayName, identity.Hostname, identity.DeviceIdentifier}
	for _, value := range values {
		if _, ok := fleetRoutePolicyExceptionValues[normalizePolicyIdentity(value)]; ok {
			return true
		}
	}
	return false
}

func NormalizeFleetRoutePolicyConfig(current DesiredConfig, identity FleetRoutePolicyIdentity) (DesiredConfig, bool) {
	if IsFleetRoutePolicyExempt(identity) {
		return current, false
	}

	desired := cloneDesiredConfig(current)
	changed := false
	for _, slot := range fleetRoutePolicySlots {
		target := findFleetRoutePolicyTarget(desired.Nodes, slot.ID)
		if target == nil || target.ID == "" {
			continue
		}

		for i := range desired.BasicSettings.ShuntRules {
			rule := &desired.BasicSettings.ShuntRules[i]
			if !samePolicySlot(rule, slot.ID) {
				continue
			}
			if rule.OutboundNodeID != target.ID {
				rule.OutboundNodeID = target.ID
				changed = true
			}
			if rule.Extras == nil {
				rule.Extras = map[string]any{}
			}
			for key, value := range slot.RequiredRuleExtras {
				if stringify(rule.Extras[key]) != value {
					rule.Extras[key] = value
					changed = true
				}
			}
		}

		if target.Extras == nil {
			target.Extras = map[string]any{}
		}
		for key, value := range slot.RequiredNodeExtras {
			if stringify(target.Extras[key]) != value {
				target.Extras[key] = value
				changed = true
			}
		}
		for i := range desired.Nodes {
			node := &desired.Nodes[i]
			if node.Protocol != "shunt" {
				continue
			}
			if node.Extras == nil {
				node.Extras = map[string]any{}
			}
			if stringify(node.Extras[slot.ID]) != target.ID {
				node.Extras[slot.ID] = target.ID
				changed = true
			}
		}
	}

	if changed {
		desired.RuleManage.ShuntRules = cloneShuntRules(desired.BasicSettings.ShuntRules)
	}
	return desired, changed
}

func ReconcileFleetRoutePolicy(ctx context.Context, backend UCIBackend, identity FleetRoutePolicyIdentity) (ShuntReconcileResult, error) {
	if backend == nil {
		backend = ExecBackend{}
	}
	if IsFleetRoutePolicyExempt(identity) {
		return ShuntReconcileResult{}, nil
	}

	currentLines, err := backend.Show(ctx, "passwall2")
	if err != nil {
		return ShuntReconcileResult{}, err
	}
	currentSections, err := ParseUCILines(currentLines)
	if err != nil {
		return ShuntReconcileResult{}, err
	}
	currentConfig := importDesiredConfig(currentSections)
	desired, changed := NormalizeFleetRoutePolicyConfig(currentConfig, identity)
	if !changed {
		return ShuntReconcileResult{}, nil
	}
	return reconcileShuntBindingsFromCurrent(ctx, backend, currentConfig, desired)
}

func findFleetRoutePolicyTarget(nodes []NodeConfig, slotID string) *NodeConfig {
	bestIndex := -1
	bestScore := 0
	for i := range nodes {
		score := fleetRoutePolicyScore(slotID, nodes[i])
		if score > bestScore {
			bestScore = score
			bestIndex = i
		}
	}
	if bestIndex < 0 || bestScore < 100 {
		return nil
	}
	return &nodes[bestIndex]
}

func fleetRoutePolicyScore(slotID string, node NodeConfig) int {
	if !node.Enabled || node.Protocol == "shunt" {
		return 0
	}
	label := normalizePolicyText(node.Label)
	address := normalizePolicyHost(node.Address)
	transport := normalizePolicyText(node.Transport)
	ruEntry := hostLooksLikeRuEntry(address) || strings.Contains(label, "🇷🇺")
	isGRPC := transport == "grpc"

	switch slotID {
	case "WorldProxy":
		if !containsAny(label, "германи", "germany", "deutsch", "🇩🇪") {
			return 0
		}
		score := 60
		if ruEntry {
			score += 40
		}
		if node.Port == 50052 {
			score += 30
		}
		if isGRPC {
			score += 20
		}
		if ruEntry {
			return score
		}
	case "YouTube":
		ruRussiaPort := hostLooksLikeRuEntry(address) && node.Port == 50051
		if !containsAny(label, "росси", "russia", "🇷🇺") && !ruRussiaPort {
			return 0
		}
		score := 60
		if ruEntry {
			score += 25
		}
		if node.Port == 50051 {
			score += 35
		}
		if isGRPC {
			score += 20
		}
		return score
	case "Special":
		nlHost := strings.HasPrefix(address, "nl") && strings.Contains(address, ".")
		ruNLPort := hostLooksLikeRuEntry(address) && node.Port == 50055
		if !containsAny(label, "нидерланд", "netherlands", "holland", "🇳🇱") && !nlHost && !ruNLPort {
			return 0
		}
		score := 60
		// Keep this aligned with the panel-side policy scorer. Several live
		// routers have a plain NL node that matches the country label but returns
		// 000, while the RU-entry Netherlands subscription slot on port 50055 is
		// the proven safe fallback. Treat that shape as a first-class Special
		// target instead of rejecting it below the semantic threshold.
		if ruEntry {
			score += 20
		}
		if isGRPC {
			score += 15
		}
		if ruNLPort {
			score += 65
		}
		if nlHost {
			score += 25
		}
		if node.Port == 443 {
			score += 15
		}
		return score
	case "Tiktok":
		byHost := strings.HasPrefix(address, "by") && strings.Contains(address, ".")
		if !containsAny(label, "беларус", "belarus", "🇧🇾") && !byHost {
			return 0
		}
		score := 70
		if byHost {
			score += 25
		}
		if node.Port == 443 {
			score += 10
		}
		return score
	case "DiscordVoiceUdp":
		if !containsAny(label, "польш", "poland", "🇵🇱") {
			return 0
		}
		score := 60
		if ruEntry {
			score += 35
		}
		if node.Port == 50053 {
			score += 35
		}
		if isGRPC {
			score += 20
		}
		if ruEntry {
			return score
		}
	}
	return 0
}

func samePolicySlot(rule *ShuntRule, slotID string) bool {
	if rule == nil {
		return false
	}
	slot := normalizePolicyText(slotID)
	return normalizePolicyText(rule.ID) == slot || normalizePolicyText(rule.Label) == slot
}

func cloneDesiredConfig(config DesiredConfig) DesiredConfig {
	bytes, err := json.Marshal(config)
	if err != nil {
		return config
	}
	var cloned DesiredConfig
	if err := json.Unmarshal(bytes, &cloned); err != nil {
		return config
	}
	return cloned
}

func cloneShuntRules(rules []ShuntRule) []ShuntRule {
	bytes, err := json.Marshal(rules)
	if err != nil {
		return rules
	}
	var cloned []ShuntRule
	if err := json.Unmarshal(bytes, &cloned); err != nil {
		return rules
	}
	return cloned
}

func normalizePolicyIdentity(value string) string {
	return nonIdentityChars.ReplaceAllString(normalizePolicyText(value), "")
}

func normalizePolicyText(value string) string {
	value = strings.ToLower(strings.ReplaceAll(value, "ё", "е"))
	value = textSeparators.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func normalizePolicyHost(value string) string {
	return strings.TrimSpace(strings.ToLower(strings.ReplaceAll(value, "ё", "е")))
}

func hostLooksLikeRuEntry(host string) bool {
	return (strings.HasPrefix(host, "ru") && strings.Contains(host, ".")) || strings.Contains(host, "ru-entry") || strings.Contains(host, "ru entry")
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func stringify(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(strings.Trim(strings.TrimSpace(toString(value)), "'"), "\n", " "))
}

func toString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return strings.TrimSpace(jsonNumberString(v))
	}
}

func jsonNumberString(value any) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(bytes)
}
