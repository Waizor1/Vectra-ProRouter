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

var routeRuleExtras = []string{
	"network",
	"port",
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
	return reconcileShuntBindingsFromCurrent(ctx, backend, currentConfig, desired, nil)
}

// ReconcileShuntBindingsYieldingTo is ReconcileShuntBindings for a router that
// also receives the panel's route-policy directive. A slot the directive pins
// belongs to the directive: the last desired revision only remembers where the
// slot pointed when an operator last applied, and after the subscription
// re-mints node IDs that memory can name a different exit than the panel now
// wants. Letting both self-heals own the slot made them undo each other on
// every check-in, each time with a PassWall restart (andrey-avito, 2026-09-28).
//
// A pin only counts while it names a node that is actually on the router and
// usable. A directive computed before the subscription re-minted the IDs points
// at nodes that no longer exist; the directive self-heal skips such a slot, so
// yielding it as well would leave it with no owner and its traffic on the
// shunt's default route. Slots the directive is silent about, or pins to a
// node that is gone, stay with the desired revision.
func ReconcileShuntBindingsYieldingTo(ctx context.Context, backend UCIBackend, desired DesiredConfig, directive *FleetRoutePolicyDirective) (ShuntReconcileResult, error) {
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
	return reconcileShuntBindingsFromCurrent(ctx, backend, currentConfig, withoutDirectivePinnedSlots(desired, directive, currentConfig.Nodes), nil)
}

func withoutDirectivePinnedSlots(desired DesiredConfig, directive *FleetRoutePolicyDirective, currentNodes []NodeConfig) DesiredConfig {
	if directive == nil || directive.Exempt || !directive.HasBindings() {
		return desired
	}
	pinned := make([]fleetRoutePolicySlot, 0, len(directive.Slots))
	for _, slot := range directiveSlots(directive) {
		if resolveFleetRoutePolicyTarget(currentNodes, slot) != nil {
			pinned = append(pinned, slot)
		}
	}
	if len(pinned) == 0 {
		return desired
	}
	kept := make([]ShuntRule, 0, len(desired.BasicSettings.ShuntRules))
	for i := range desired.BasicSettings.ShuntRules {
		rule := desired.BasicSettings.ShuntRules[i]
		owned := false
		for _, slot := range pinned {
			if samePolicySlot(&rule, slot.ID) {
				owned = true
				break
			}
		}
		if !owned {
			kept = append(kept, rule)
		}
	}
	trimmed := desired
	trimmed.BasicSettings.ShuntRules = kept
	return trimmed
}

// exactSlots lists the slots whose exact desired node wins over a node that
// merely matches its route intent (see pickShuntBindingTarget). Only the panel
// directive's caller passes any, and only the slots the directive pins: the
// panel picks that node from the router's own live config and its node-health
// picture, and the desired-revision self-heal yields every such slot. Slots
// the directive is silent about go to the built-in scorer, whose pick among
// equally named hosts is just the first in UCI order, and the desired-revision
// paths keep matching by intent first as well. Giving those exact-wins too made
// the two self-heals trade a slot between equally named hosts on every
// check-in whenever the directive did not own it, each trade a PassWall restart.
func reconcileShuntBindingsFromCurrent(ctx context.Context, backend UCIBackend, currentConfig DesiredConfig, desired DesiredConfig, exactSlots []string) (ShuntReconcileResult, error) {
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
			candidate, rebind := pickShuntBindingTarget(currentConfig.Nodes, currentNodes, currentTargetID, desiredRule.OutboundNodeID, *desiredTarget, ruleInSlots(&desiredRule, exactSlots))
			if candidate == nil {
				continue
			}

			ruleCommands := []string{}
			if rebind {
				ruleCommands = append(ruleCommands, setValue("passwall2."+safeID(currentShunt.ID)+"."+safeID(desiredRule.ID), candidate.ID))
			}
			if currentRule != nil {
				ruleCommands = append(ruleCommands, renderRouteRuleExtraCommands("passwall2."+safeID(currentRule.ID), *currentRule, desiredRule)...)
			}
			nodeCommands := renderRouteTuningCommands("passwall2."+safeID(candidate.ID), *candidate, *desiredTarget)
			if len(ruleCommands) == 0 && len(nodeCommands) == 0 {
				continue
			}
			commands = append(commands, ruleCommands...)
			commands = append(commands, nodeCommands...)
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

// pickShuntBindingTarget decides which node a slot should end up on and whether
// that is a move away from the node it points at now.
//
// With preferExactNode the exact node the desired rule names wins whenever the
// router has it and it is enabled (the caller has already ruled out shunts). Without it, or when that node is gone, matching
// by route intent decides (label first, then transport, port and address): the
// subscription re-mints every node ID on refresh, so a desired revision
// recorded before a refresh names IDs that are gone and the label is all that
// is left to go by.
//
// Asking the label first let any equally named node stand in for the one the
// panel pinned. The provider gives several hosts the same name, e.g.
// "🇷🇺⚡Россия YouTube 🚫Ad🚫" on ru4, ru13 and ru18 :50051. When ru4 left the
// subscription, AndreyVK_Sochi kept YouTube on it for three days while the
// panel's directive named the live ru13 node on every check-in, and nothing was
// logged because the writer saw nothing to do (2026-10-19..22).
func pickShuntBindingTarget(nodes []NodeConfig, nodesByID map[string]*NodeConfig, currentTargetID string, desiredTargetID string, desiredTarget NodeConfig, preferExactNode bool) (*NodeConfig, bool) {
	if preferExactNode {
		if exact := nodesByID[desiredTargetID]; exact != nil && exact.Enabled {
			return exact, exact.ID != currentTargetID
		}
	}
	if current := nodesByID[currentTargetID]; current != nil && nodesMatchRouteIntent(*current, desiredTarget) {
		return current, false
	}
	candidate := findNodeByRouteIntent(nodes, desiredTarget)
	if candidate == nil || candidate.ID == "" {
		return nil, false
	}
	return candidate, candidate.ID != currentTargetID
}

func ruleInSlots(rule *ShuntRule, slotIDs []string) bool {
	for _, id := range slotIDs {
		if samePolicySlot(rule, id) {
			return true
		}
	}
	return false
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

func renderRouteRuleExtraCommands(ref string, current ShuntRule, desired ShuntRule) []string {
	commands := []string{}
	for _, key := range routeRuleExtras {
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
