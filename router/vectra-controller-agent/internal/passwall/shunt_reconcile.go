package passwall

import (
	"context"
	"fmt"
	"strings"
)

var routeTuningExtras = []string{
	"mux",
	"mux_concurrency",
	"xudp_concurrency",
	"packet_encoding",
}

type ShuntReconcileChange struct {
	ShuntNodeID       string `json:"shuntNodeId"`
	RuleID            string `json:"ruleId"`
	RuleLabel         string `json:"ruleLabel"`
	PreviousNodeID    string `json:"previousNodeId,omitempty"`
	PreviousNodeLabel string `json:"previousNodeLabel,omitempty"`
	RestoredNodeID    string `json:"restoredNodeId"`
	RestoredNodeLabel string `json:"restoredNodeLabel"`
}

type ShuntReconcileResult struct {
	Changed     bool                   `json:"changed"`
	Changes     []ShuntReconcileChange `json:"changes,omitempty"`
	UCICommands []string               `json:"uciCommands,omitempty"`
	Restarted   bool                   `json:"restarted,omitempty"`
}

func ReconcileShuntBindings(ctx context.Context, backend UCIBackend, desired DesiredConfig) (ShuntReconcileResult, error) {
	if backend == nil {
		backend = ExecBackend{}
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

	desiredNodes := nodesByID(desired.Nodes)
	currentNodes := nodesByID(currentConfig.Nodes)
	currentRules := shuntRulesByIdentity(currentConfig.BasicSettings.ShuntRules)
	currentShunts := shuntNodesByIdentity(currentConfig.Nodes)

	commands := []string{}
	changes := []ShuntReconcileChange{}
	for _, desiredShunt := range desired.Nodes {
		if desiredShunt.Protocol != "shunt" {
			continue
		}
		currentShunt := findCurrentShunt(currentShunts, desiredShunt)
		if currentShunt == nil {
			continue
		}
		for _, desiredRule := range desired.BasicSettings.ShuntRules {
			if desiredRule.OutboundNodeID == "" {
				continue
			}
			desiredTarget := desiredNodes[desiredRule.OutboundNodeID]
			if desiredTarget == nil || desiredTarget.Protocol == "shunt" {
				continue
			}

			currentRule := findCurrentRule(currentRules, desiredRule)
			currentTargetID := ""
			if currentRule != nil {
				currentTargetID = currentRule.OutboundNodeID
			}
			if currentTargetID == "" {
				currentTargetID = currentShuntBindingID(currentShunt, desiredRule)
			}
			currentTarget := currentNodes[currentTargetID]
			if currentTarget != nil && nodesMatchRouteIntent(*currentTarget, *desiredTarget) {
				continue
			}

			candidate := findNodeByRouteIntent(currentConfig.Nodes, *desiredTarget)
			if candidate == nil || candidate.ID == "" || candidate.ID == currentTargetID {
				continue
			}

			commands = append(commands, setValue("passwall2."+safeID(currentShunt.ID)+"."+safeID(desiredRule.ID), candidate.ID))
			commands = append(commands, renderRouteTuningCommands("passwall2."+safeID(candidate.ID), *candidate, *desiredTarget)...)
			changes = append(changes, ShuntReconcileChange{
				ShuntNodeID:       currentShunt.ID,
				RuleID:            desiredRule.ID,
				RuleLabel:         desiredRule.Label,
				PreviousNodeID:    currentTargetID,
				PreviousNodeLabel: nodeLabel(currentTarget),
				RestoredNodeID:    candidate.ID,
				RestoredNodeLabel: candidate.Label,
			})
		}
	}

	result := ShuntReconcileResult{Changed: len(changes) > 0, Changes: changes}
	if len(commands) == 0 {
		return result, nil
	}
	commands = append(stripEmpty(commands), "commit passwall2")
	if err := backend.Batch(ctx, commands); err != nil {
		return ShuntReconcileResult{}, err
	}
	result.UCICommands = commands

	if _, err := backend.Run(ctx, "/etc/init.d/passwall2", "restart"); err != nil {
		return result, err
	}
	result.Restarted = true
	return result, nil
}

func nodesByID(nodes []NodeConfig) map[string]*NodeConfig {
	out := make(map[string]*NodeConfig, len(nodes))
	for i := range nodes {
		node := &nodes[i]
		if node.ID != "" {
			out[node.ID] = node
		}
	}
	return out
}

func shuntRulesByIdentity(rules []ShuntRule) map[string]*ShuntRule {
	out := make(map[string]*ShuntRule, len(rules)*2)
	for i := range rules {
		rule := &rules[i]
		if rule.ID != "" {
			out["id:"+rule.ID] = rule
		}
		if rule.Label != "" {
			out["label:"+normalizeIntentText(rule.Label)] = rule
		}
	}
	return out
}

func shuntNodesByIdentity(nodes []NodeConfig) map[string]*NodeConfig {
	out := make(map[string]*NodeConfig, len(nodes)*2)
	for i := range nodes {
		node := &nodes[i]
		if node.Protocol != "shunt" {
			continue
		}
		if node.ID != "" {
			out["id:"+node.ID] = node
		}
		if node.Label != "" {
			out["label:"+normalizeIntentText(node.Label)] = node
		}
	}
	return out
}

func findCurrentShunt(shunts map[string]*NodeConfig, desired NodeConfig) *NodeConfig {
	if node := shunts["id:"+desired.ID]; node != nil {
		return node
	}
	return shunts["label:"+normalizeIntentText(desired.Label)]
}

func findCurrentRule(rules map[string]*ShuntRule, desired ShuntRule) *ShuntRule {
	if rule := rules["id:"+desired.ID]; rule != nil {
		return rule
	}
	return rules["label:"+normalizeIntentText(desired.Label)]
}

func currentShuntBindingID(shunt *NodeConfig, desiredRule ShuntRule) string {
	if shunt == nil || len(shunt.Extras) == 0 {
		return ""
	}
	keys := []string{
		desiredRule.ID,
		safeID(desiredRule.ID),
		desiredRule.Label,
		safeID(desiredRule.Label),
	}
	for _, key := range keys {
		if key == "" {
			continue
		}
		value, ok := shunt.Extras[key]
		if !ok || value == nil {
			continue
		}
		if text := strings.TrimSpace(fmt.Sprint(value)); text != "" {
			return text
		}
	}
	return ""
}

func findNodeByRouteIntent(nodes []NodeConfig, desired NodeConfig) *NodeConfig {
	bestIndex := -1
	bestScore := 0
	for i := range nodes {
		node := nodes[i]
		if node.Protocol == "shunt" || !node.Enabled {
			continue
		}
		score := routeIntentScore(node, desired)
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

func nodesMatchRouteIntent(current NodeConfig, desired NodeConfig) bool {
	return routeIntentScore(current, desired) >= 100
}

func routeIntentScore(candidate NodeConfig, desired NodeConfig) int {
	score := 0
	candidateLabel := normalizeIntentText(candidate.Label)
	desiredLabel := normalizeIntentText(desired.Label)
	if desiredLabel != "" && candidateLabel == desiredLabel {
		score += 100
	}
	if desired.Transport != "" && normalizeIntentText(candidate.Transport) == normalizeIntentText(desired.Transport) {
		score += 10
	}
	if desired.Port != 0 && candidate.Port == desired.Port {
		score += 8
	}
	if desired.Address != "" && normalizeIntentText(candidate.Address) == normalizeIntentText(desired.Address) {
		score += 6
	}
	if score == 0 && desired.Address != "" && desired.Port != 0 &&
		normalizeIntentText(candidate.Address) == normalizeIntentText(desired.Address) && candidate.Port == desired.Port {
		score = 90
	}
	return score
}

func renderRouteTuningCommands(ref string, current NodeConfig, desired NodeConfig) []string {
	commands := []string{}
	for _, key := range routeTuningExtras {
		value, ok := desired.Extras[key]
		if !ok || value == nil {
			continue
		}
		if fmt.Sprint(current.Extras[key]) == fmt.Sprint(value) {
			continue
		}
		commands = append(commands, setValue(ref+"."+key, fmt.Sprint(value)))
	}
	return commands
}

func nodeLabel(node *NodeConfig) string {
	if node == nil {
		return ""
	}
	return node.Label
}

func normalizeIntentText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
