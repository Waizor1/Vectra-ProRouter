package passwall

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const RouteVerificationVersion = "2026-05-14-v1"

var urlTestStatusPattern = regexp.MustCompile(`\b([0-9]{3})\b`)

type RouteVerificationResult struct {
	VerifierVersion   string                        `json:"verifierVersion"`
	VerifiedAt        string                        `json:"verifiedAt"`
	OK                bool                          `json:"ok"`
	Exempt            bool                          `json:"exempt"`
	SelectedNodeID    string                        `json:"selectedNodeId,omitempty"`
	SelectedNodeLabel string                        `json:"selectedNodeLabel,omitempty"`
	Slots             []RouteSlotVerificationResult `json:"slots"`
	CommandResults    []CommandResult               `json:"commandResults,omitempty"`
	Errors            []string                      `json:"errors,omitempty"`
}

type RouteSlotVerificationResult struct {
	SlotID             string            `json:"slotId"`
	Expected           string            `json:"expected,omitempty"`
	RuleID             string            `json:"ruleId,omitempty"`
	RuleLabel          string            `json:"ruleLabel,omitempty"`
	BoundNodeID        string            `json:"boundNodeId,omitempty"`
	BoundNodeLabel     string            `json:"boundNodeLabel,omitempty"`
	BindingOK          bool              `json:"bindingOk"`
	RuleExtrasOK       bool              `json:"ruleExtrasOk"`
	NodeExtrasOK       bool              `json:"nodeExtrasOk"`
	SmokeOK            bool              `json:"smokeOk"`
	StatusCode         int               `json:"statusCode"`
	Command            string            `json:"command,omitempty"`
	RequiredRuleExtras map[string]string `json:"requiredRuleExtras,omitempty"`
	ActualRuleExtras   map[string]string `json:"actualRuleExtras,omitempty"`
	RequiredNodeExtras map[string]string `json:"requiredNodeExtras,omitempty"`
	ActualNodeExtras   map[string]string `json:"actualNodeExtras,omitempty"`
	Error              string            `json:"error,omitempty"`
}

func VerifyFleetRoutes(ctx context.Context, backend UCIBackend, identity FleetRoutePolicyIdentity) (RouteVerificationResult, error) {
	if backend == nil {
		backend = ExecBackend{}
	}

	result := RouteVerificationResult{
		VerifierVersion: RouteVerificationVersion,
		VerifiedAt:      time.Now().UTC().Format(time.RFC3339),
		Slots:           []RouteSlotVerificationResult{},
		CommandResults:  []CommandResult{},
	}

	if IsFleetRoutePolicyExempt(identity) {
		result.OK = true
		result.Exempt = true
		return result, nil
	}

	lines, err := backend.Show(ctx, "passwall2")
	if err != nil {
		return result, err
	}
	sections, err := ParseUCILines(lines)
	if err != nil {
		return result, err
	}
	config := importDesiredConfig(sections)
	result.SelectedNodeID = config.BasicSettings.Main.SelectedNodeID

	nodesByID := nodesByID(config.Nodes)
	selectedShunt := nodesByID[config.BasicSettings.Main.SelectedNodeID]
	if selectedShunt != nil {
		result.SelectedNodeLabel = selectedShunt.Label
	}

	if !config.BasicSettings.Main.MainSwitch {
		result.Errors = append(result.Errors, "PassWall main switch is disabled")
	}
	if selectedShunt == nil || selectedShunt.Protocol != "shunt" {
		result.Errors = append(result.Errors, "selected PassWall node is not a shunt node")
	}

	for _, slot := range fleetRoutePolicySlots {
		slotResult := verifyFleetRouteSlot(ctx, backend, config, nodesByID, selectedShunt, slot)
		result.Slots = append(result.Slots, slotResult)
		if slotResult.Command != "" {
			result.CommandResults = append(result.CommandResults, CommandResult{
				Command: slotResult.Command,
			})
		}
		if slotResult.Error != "" {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", slot.ID, slotResult.Error))
		}
	}

	result.OK = len(result.Errors) == 0
	return result, nil
}

func verifyFleetRouteSlot(
	ctx context.Context,
	backend UCIBackend,
	config DesiredConfig,
	nodes map[string]*NodeConfig,
	selectedShunt *NodeConfig,
	slot fleetRoutePolicySlot,
) RouteSlotVerificationResult {
	out := RouteSlotVerificationResult{
		SlotID:             slot.ID,
		Expected:           slot.Expected,
		RequiredRuleExtras: cloneStringMap(slot.RequiredRuleExtras),
		RequiredNodeExtras: cloneStringMap(slot.RequiredNodeExtras),
	}

	rule := findRoutePolicyRule(config.BasicSettings.ShuntRules, slot.ID)
	if rule == nil {
		out.Error = "managed shunt rule is missing"
		return out
	}
	out.RuleID = rule.ID
	out.RuleLabel = rule.Label

	boundNodeID := strings.TrimSpace(rule.OutboundNodeID)
	if boundNodeID == "" && selectedShunt != nil {
		boundNodeID = currentShuntBindingID(selectedShunt, *rule)
	}
	out.BoundNodeID = boundNodeID
	if boundNodeID == "" {
		out.Error = "selected shunt binding is empty"
		return out
	}

	boundNode := nodes[boundNodeID]
	if boundNode == nil {
		out.Error = "selected shunt binding points to a missing node"
		return out
	}
	out.BoundNodeLabel = boundNode.Label
	out.BindingOK = boundNode.Enabled && boundNode.Protocol != "shunt" && fleetRoutePolicyScore(slot.ID, *boundNode) >= 100
	out.RuleExtrasOK = extrasMatch(rule.Extras, slot.RequiredRuleExtras)
	out.NodeExtrasOK = extrasMatch(boundNode.Extras, slot.RequiredNodeExtras)
	out.ActualRuleExtras = pickExtras(rule.Extras, slot.RequiredRuleExtras)
	out.ActualNodeExtras = pickExtras(boundNode.Extras, slot.RequiredNodeExtras)

	commandResult, err := backend.Run(ctx, "/usr/share/passwall2/test.sh", "url_test_node", boundNodeID)
	out.Command = commandResult.Command
	out.StatusCode = parseURLTestStatusCode(commandResult.Stdout, commandResult.Stderr)
	out.SmokeOK = err == nil && out.StatusCode == 204
	if err != nil {
		out.Error = strings.TrimSpace(err.Error())
	} else if !out.BindingOK {
		out.Error = "selected shunt binding does not match expected route intent"
	} else if !out.RuleExtrasOK {
		out.Error = "shunt rule extras do not match expected policy"
	} else if !out.NodeExtrasOK {
		out.Error = "route node extras do not match expected policy"
	} else if !out.SmokeOK {
		out.Error = fmt.Sprintf("url_test_node returned %03d", out.StatusCode)
	}

	return out
}

func findRoutePolicyRule(rules []ShuntRule, slotID string) *ShuntRule {
	for i := range rules {
		if samePolicySlot(&rules[i], slotID) {
			return &rules[i]
		}
	}
	return nil
}

func extrasMatch(actual map[string]any, required map[string]string) bool {
	if len(required) == 0 {
		return true
	}
	for key, expected := range required {
		if stringify(actual[key]) != expected {
			return false
		}
	}
	return true
}

func pickExtras(actual map[string]any, required map[string]string) map[string]string {
	if len(required) == 0 {
		return nil
	}
	out := make(map[string]string, len(required))
	for key := range required {
		out[key] = stringify(actual[key])
	}
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func parseURLTestStatusCode(values ...string) int {
	for _, value := range values {
		match := urlTestStatusPattern.FindStringSubmatch(value)
		if len(match) < 2 {
			continue
		}
		parsed, err := strconv.Atoi(match[1])
		if err == nil {
			return parsed
		}
	}
	return 0
}
