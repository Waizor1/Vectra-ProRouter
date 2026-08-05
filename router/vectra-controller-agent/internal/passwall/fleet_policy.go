package passwall

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

const FleetRoutePolicyVersion = "2026-08-03-v4"

type FleetRoutePolicyIdentity struct {
	Name             string
	DisplayName      string
	Hostname         string
	DeviceIdentifier string
}

type fleetRoutePolicySlot struct {
	ID       string
	Expected string
	// TargetNodeID, when non-empty, pins this slot to an exact node and bypasses
	// the semantic scorer. Only panel-authored directives set it.
	TargetNodeID       string
	RequiredRuleExtras map[string]string
	RequiredNodeExtras map[string]string
}

// Routers excluded from fleet package normalization. Keep aligned with
// exceptionIdentityValues in apps/web/src/server/vectra/fleet-route-policy.ts —
// the panel and the on-router self-heal must agree, or one will keep undoing the
// other every check-in.
//
//   - hh: operator-designated no-touch router.
//   - vagrandrouter: its ISP filters the non-standard high ports (50051-50061)
//     that every RU-entry gRPC node uses, so the canonical targets are
//     unreachable from that line while port 443 works fine (verified
//     2026-07-29: ru12.nfnpx.online:50053 connect fails, fin2/pl1:443 connect in
//     0.61s). Reachability is not part of the scorer, so normalization kept
//     rebinding the slots to a dead RU-entry node once a minute. This router
//     runs on the vless/raw :443 node family instead.
var fleetRoutePolicyExceptionValues = map[string]struct{}{
	"hh":            {},
	"vagrandrouter": {},
}

var fleetRoutePolicySlots = []fleetRoutePolicySlot{
	{ID: "WorldProxy", Expected: "Poland direct :443 (RU-entry Poland fallback)"},
	{ID: "YouTube", Expected: "RU Russia"},
	{ID: "Special", Expected: "Netherlands"},
	{ID: "Tiktok", Expected: "Belarus"},
	{
		ID:       "DiscordVoiceUdp",
		Expected: "same node as WorldProxy + UDP/mux/xudp tuning",
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

// FleetRoutePolicyDirective is the panel-authored route policy delivered in the
// check-in response. It exists so route-policy changes (a new canonical exit, a
// per-router exemption) ship as a panel deploy instead of a controller rebuild
// plus a fleet-wide controller rollout.
//
// Precedence inside NormalizeFleetRoutePolicyConfig:
//
//  1. directive.Exempt        -> normalization is skipped entirely.
//  2. directive.Slots present -> bind exactly what the panel asked for. The
//     built-in scorer is NOT consulted; the panel is authoritative.
//  3. directive nil/empty     -> fall back to the built-in scorer below.
//
// Case 3 is deliberate and must stay: a router that cannot reach the panel, or
// one talking to a panel older than this field, still self-heals its bindings
// rather than drifting. The built-in scorer is the offline safety net, not the
// source of truth.
type FleetRoutePolicyDirective struct {
	Version string                          `json:"version"`
	Exempt  bool                            `json:"exempt"`
	Reason  string                          `json:"reason,omitempty"`
	Slots   []FleetRoutePolicyDirectiveSlot `json:"slots"`
}

type FleetRoutePolicyDirectiveSlot struct {
	ID string `json:"id"`
	// NodeID is the concrete node the panel wants bound to this slot. Node IDs
	// are per-router (the subscription mints them), so the panel computes this
	// per router from the same live config the router last imported.
	NodeID string `json:"nodeId"`
	// Fingerprint is advisory ("label | host:port | transport | protocol") and is
	// only used for operator-facing logging.
	Fingerprint string            `json:"fingerprint,omitempty"`
	RuleExtras  map[string]string `json:"ruleExtras,omitempty"`
	NodeExtras  map[string]string `json:"nodeExtras,omitempty"`
}

// HasBindings reports whether the directive carries at least one usable slot.
// An empty slot list means the panel had nothing to say (e.g. it could not
// resolve targets for this router yet) and must not blank existing bindings.
func (d *FleetRoutePolicyDirective) HasBindings() bool {
	if d == nil {
		return false
	}
	for _, slot := range d.Slots {
		if strings.TrimSpace(slot.ID) != "" && strings.TrimSpace(slot.NodeID) != "" {
			return true
		}
	}
	return false
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
	return NormalizeFleetRoutePolicyConfigWithDirective(current, identity, nil)
}

// NormalizeFleetRoutePolicyConfigWithDirective applies the panel-authored
// directive when one is present and falls back to the built-in scorer when it
// is not. See FleetRoutePolicyDirective for the precedence rules.
func NormalizeFleetRoutePolicyConfigWithDirective(current DesiredConfig, identity FleetRoutePolicyIdentity, directive *FleetRoutePolicyDirective) (DesiredConfig, bool) {
	// A panel directive overrides the local exemption list in BOTH directions:
	// the panel can exempt a router the local list does not know about, and it
	// can un-exempt one the local list still carries. That is the whole point —
	// changing an exemption must not require shipping a new controller.
	if directive != nil {
		if directive.Exempt {
			return current, false
		}
	} else if IsFleetRoutePolicyExempt(identity) {
		return current, false
	}

	slots := fleetRoutePolicySlots
	if directive.HasBindings() {
		slots = directiveSlots(directive)
	}

	desired := cloneDesiredConfig(current)
	changed := false
	for _, slot := range slots {
		target := resolveFleetRoutePolicyTarget(desired.Nodes, slot)
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
	return ReconcileFleetRoutePolicyWithDirective(ctx, backend, identity, nil)
}

// ReconcileFleetRoutePolicyWithDirective reconciles live UCI against the
// panel-authored directive, falling back to the built-in scorer when the panel
// sent nothing. Callers on the check-in path should pass the directive from the
// latest CheckInResponse.
func ReconcileFleetRoutePolicyWithDirective(ctx context.Context, backend UCIBackend, identity FleetRoutePolicyIdentity, directive *FleetRoutePolicyDirective) (ShuntReconcileResult, error) {
	if backend == nil {
		backend = ExecBackend{}
	}
	if directive != nil {
		if directive.Exempt {
			return ShuntReconcileResult{}, nil
		}
	} else if IsFleetRoutePolicyExempt(identity) {
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
	desired, changed := NormalizeFleetRoutePolicyConfigWithDirective(currentConfig, identity, directive)
	if !changed {
		return ShuntReconcileResult{}, nil
	}
	return reconcileShuntBindingsFromCurrent(ctx, backend, currentConfig, desired)
}

// directiveSlots converts the panel directive into the internal slot shape.
// Slots with a blank ID or NodeID are dropped: a half-specified slot must leave
// the existing binding alone rather than clear it.
func directiveSlots(directive *FleetRoutePolicyDirective) []fleetRoutePolicySlot {
	slots := make([]fleetRoutePolicySlot, 0, len(directive.Slots))
	for _, slot := range directive.Slots {
		id := strings.TrimSpace(slot.ID)
		nodeID := strings.TrimSpace(slot.NodeID)
		if id == "" || nodeID == "" {
			continue
		}
		slots = append(slots, fleetRoutePolicySlot{
			ID:                 id,
			Expected:           slot.Fingerprint,
			TargetNodeID:       nodeID,
			RequiredRuleExtras: slot.RuleExtras,
			RequiredNodeExtras: slot.NodeExtras,
		})
	}
	return slots
}

// resolveFleetRoutePolicyTarget picks the node a slot should bind to: the exact
// node the panel named, or the scorer's pick when no directive is in play.
//
// A pinned node that is not in the live config yields nil, which skips the slot.
// That case is real — the subscription re-mints node IDs on refresh, so a
// directive computed one check-in earlier can name an ID that no longer exists.
// Skipping preserves the current binding; blanking it would strand the slot.
func resolveFleetRoutePolicyTarget(nodes []NodeConfig, slot fleetRoutePolicySlot) *NodeConfig {
	if slot.TargetNodeID == "" {
		return findFleetRoutePolicyTarget(nodes, slot.ID)
	}
	for i := range nodes {
		if nodes[i].ID != slot.TargetNodeID {
			continue
		}
		if nodes[i].Protocol == "shunt" || !nodes[i].Enabled {
			return nil
		}
		return &nodes[i]
	}
	return nil
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
		// History: RU-entry Germany (ru*:50052) -> RU-entry Poland (ru*:50053)
		// on 2026-07-02 because the shared German exit was overloaded.
		//
		// Moved again on 2026-08-02 to the DIRECT Poland exit (pl*:443). The
		// provider blackholed a subset of prefixes — Telegram DCs and the
		// Netflix OCA CDN — on part of its RU-entry fleet (ru3/ru4/ru5:50053
		// dead, ru7-ru12 fine), which killed WorldProxy for 12 of 25 routers.
		// Measured on one router at one moment: pl2:443 reached
		// web.telegram.org 200 and pulled 200 KB of OCA video at ~583 KB/s
		// while ru3:50053 returned 000 for both, with the SAME egress IP. The
		// RU-entry hop, not the exit, was the fault domain.
		//
		// RU-entry Poland is kept as a scored fallback (120 < 140) so a router
		// whose subscription carries no direct pl*:443 node is not stranded
		// with an unbound slot.
		//
		// DiscordVoiceUdp deliberately resolves to this same node — see that
		// slot's case below for why splitting them broke Discord voice. Keep
		// this aligned with the panel-side scorer in
		// apps/web/src/server/vectra/fleet-route-policy.ts.
		if !containsAny(label, "польш", "poland", "🇵🇱") {
			return 0
		}
		score := 60
		if !ruEntry && node.Port == 443 {
			// Canonical shape: direct foreign Poland exit on :443.
			score += 80
			if !containsAny(label, "extreme") {
				// Deterministic tie-break. Subscriptions carry two direct
				// Poland :443 nodes ("🇵🇱 ⚡️Польша YouTube 🚫Ad🚫" and
				// "⚡Extreme Польша 🇵🇱"); without this they score equal and the
				// winner depends on node order, which the subscription
				// re-mints on every refresh.
				score += 5
			}
			return score
		}
		if ruEntry {
			score += 40
			if node.Port == 50053 {
				score += 15
			}
			if isGRPC {
				score += 5
			}
			return score
		}
	case "YouTube":
		// Subscription labels are an entry/exit flag pair: "🇷🇺🇩🇪 Германия
		// YouTube" is RU-entry / DE-exit, "🇷🇺🇦🇪 ОАЭ" is RU-entry / UAE-exit.
		// The leading 🇷🇺 is therefore the ENTRY marker and must NOT, on its own,
		// qualify a node for this slot — that is precisely how the dead generic
		// "🇷🇺🇦🇪 ОАЭ" node (port 50061, passes a trivial google-204 healthcheck
		// but fails real youtube.com) used to win the YouTube slot while a working
		// "...Германия YouTube" node sat unused. Require an explicit YouTube
		// purpose or a genuine Russia destination (the fleet provides no pure
		// Russia-exit node, so YouTube-purposed RU-entry nodes are the real
		// targets). Keep this aligned with the panel-side scorer in
		// apps/web/src/server/vectra/fleet-route-policy.ts.
		youTubePurposed := containsAny(label, "youtube")
		ruRussiaPort := hostLooksLikeRuEntry(address) && node.Port == 50051
		russiaExit := containsAny(label, "росси", "russia") || ruRussiaPort
		if !youTubePurposed && !russiaExit {
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
		if youTubePurposed {
			score += 30
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
		// This slot MUST land on the same node as WorldProxy, so it scores
		// with the WorldProxy scorer verbatim.
		//
		// Why: the generated Xray routing chain puts the WorldProxy rule ABOVE
		// this one, and that rule already carries the Discord prefixes
		// (66.22.192.0/18, 66.22.176.0/24, 66.22.188.0/22) with
		// network=tcp,udp. Xray takes the first matching rule, so every
		// Discord voice packet leaves through the WorldProxy node — this rule
		// (network=udp, port=19294-19344,50000-50100, no domain/ip of its own)
		// never sees them. The slot's only real effect is the mux/xudp tuning
		// it stamps onto whichever node it resolves to.
		//
		// That held silently until 2026-08-02: WorldProxy and this slot both
		// pointed at ru*:50053, so the tuning landed where the traffic
		// actually went. Moving WorldProxy to the direct pl*:443 exit left
		// this slot pinned to RU-entry, so the mux/xudp settings decorated a
		// node no Discord packet reached, and the traffic ran over a node with
		// no XUDP. Voice died fleet-wide the next day (operator report
		// 2026-08-03).
		//
		// Delegating keeps the two in lockstep through any future canon move,
		// including onto the RU-entry fallback. mux_concurrency=-1 disables
		// TCP mux, so this costs the WorldProxy TCP path nothing; XTLS Vision
		// rejects UDP/443 on its own, so QUIC behaviour is unchanged too.
		return fleetRoutePolicyScore("WorldProxy", node)
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
