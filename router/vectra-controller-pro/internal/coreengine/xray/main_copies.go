package xray

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"vectra-controller-pro/internal/catalog"
)

// The games' own traffic on the stable exit (r28, docs/superpowers/specs/
// 2026-10-19-vctl-games-stable-ip.md). A game reaches its servers by
// address, with no name to sniff (Star Citizen: a hub on TCP 8000, servers
// on UDP 64xxx, 1111, 2026-10-19): it falls to the provider's main path,
// whose nodes change the client's address between connections, and the game
// drops. A class of traffic (catalog.Traffic: every UDP port but DNS, NTP and
// QUIC; every TCP port but the web's) rides the stable exit instead — for
// exactly what the main path carries: each rule of the main path gets, in
// front of it, a copy per class with the rule's own matchers. What the
// provider sends elsewhere (Russia, TikTok, a block) is not touched.

// insertMainCopies puts the classes' copies in front of every rule of the
// routing whose target is the main path (target) and that applies to the
// LAN's inbound. It returns how many rules were given copies.
func insertMainCopies(routing json.RawMessage, target string, isBalancer bool, inbound string, classes []catalog.Traffic) (json.RawMessage, int, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(routing, &obj); err != nil {
		return nil, 0, fmt.Errorf("xray splice: routing is not an object: %w", err)
	}
	field := "rules"
	for k := range obj {
		if foldKey(k) == foldKey("rules") {
			field = k
		}
	}
	var rules []json.RawMessage
	if raw, ok := obj[field]; ok && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return nil, 0, fmt.Errorf("xray splice: routing.rules is not an array: %w", err)
		}
	}
	out := make([]json.RawMessage, 0, len(rules)+8)
	copied := 0
	for _, raw := range rules {
		var r map[string]json.RawMessage
		if json.Unmarshal(raw, &r) == nil {
			if ruleTargets(r, target, isBalancer) && ruleAppliesTo(r, inbound) {
				if cs := mainCopies(r, inbound, classes, true); len(cs) > 0 {
					out = append(out, cs...)
					copied++
				}
			}
		}
		out = append(out, raw)
	}
	// A main path that is xray's default outbound (no catch-all rule) gets no
	// copies at the end: what reaches the default has passed every rule,
	// xray's second pass (IPIfNonMatch) too, and copies at the end would take
	// traffic first-pass IP rules leave for that second pass (a Russian
	// address restored from FakeDNS).
	if copied == 0 {
		return routing, 0, nil
	}
	rb, err := json.Marshal(out)
	if err != nil {
		return nil, 0, err
	}
	if _, ok := obj[field]; !ok {
		obj[field] = rb
		res, err := json.Marshal(obj)
		return res, copied, err
	}
	res, _, err := rewriteObjectField(routing, field, rb)
	return res, copied, err
}

// ruleTargets: r leaves through the main path.
func ruleTargets(r map[string]json.RawMessage, target string, isBalancer bool) bool {
	if isBalancer {
		return jsonString(ruleField(r, "balancerTag")) == target
	}
	return jsonString(ruleField(r, "balancerTag")) == "" && jsonString(ruleField(r, "outboundTag")) == target
}

// ruleAppliesTo: r takes the LAN's traffic — it names no inbound, or the
// LAN's among them. A provider stage (STAGE_MAIN: what another balancer
// falls back with) is not the LAN's.
func ruleAppliesTo(r map[string]json.RawMessage, inbound string) bool {
	raw := ruleField(r, "inboundTag")
	tags := append(jsonStrings(raw), jsonString(raw))
	if raw == nil || isJSONNull(raw) || strings.Join(tags, "") == "" {
		return true // unset, [] or "": xray takes every inbound
	}
	for _, t := range tags {
		if t == inbound {
			return true
		}
	}
	return false
}

// mainCopies are r's copies for the classes: first (passBT) BitTorrent back
// to r's own target (it must not load the one stable node), then per class the
// meet of r's network and ports with the class's, for the LAN's inbound, to
// the stable exit. r's other matchers are kept (xray ANDs them); its ruleTag
// never (xray ≥ 26.9.8 refuses a duplicate). A network or port field that
// does not read gives no copy: r is left as it is.
func mainCopies(r map[string]json.RawMessage, inbound string, classes []catalog.Traffic, passBT bool) []json.RawMessage {
	nets, ok := ruleNetworks(ruleField(r, "network"))
	if !ok {
		return nil
	}
	ports := allPorts
	if raw := ruleField(r, "port"); isJSONNumber(raw) || strings.TrimSpace(jsonString(raw)) != "" {
		if ports, ok = parsePortField(raw); !ok {
			return nil
		}
	}
	for _, p := range append(jsonStrings(ruleField(r, "protocol")), jsonString(ruleField(r, "protocol"))) {
		if strings.EqualFold(p, "bittorrent") {
			return nil // BitTorrent never onto the one stable node
		}
	}
	base := map[string]json.RawMessage{}
	hasProtocol := false
	for k, v := range r {
		switch foldKey(k) {
		case foldKey("outboundTag"), foldKey("balancerTag"), foldKey("inboundTag"), foldKey("network"),
			foldKey("port"), foldKey("ruleTag"), foldKey("type"):
			continue
		case foldKey("protocol"):
			hasProtocol = true
		}
		base[k] = v
	}
	in := marshalNoEscape([]string{inbound})
	var copies []json.RawMessage
	for _, c := range classes {
		if !nets[c.Network] {
			continue
		}
		cp, _ := parsePortList(c.Ports())
		meet := intersectPorts(cp, ports)
		if len(meet) == 0 {
			continue
		}
		m := cloneRule(base)
		m["inboundTag"] = in
		m["network"] = marshalNoEscape(c.Network)
		m["port"] = marshalNoEscape(formatPorts(meet))
		m["balancerTag"] = marshalNoEscape(StableBalancerTag)
		copies = append(copies, marshalNoEscape(m))
	}
	if len(copies) == 0 || !passBT || hasProtocol {
		// r naming its protocols itself: its copies keep them, and BitTorrent
		// is whatever r says.
		return copies
	}
	bt := cloneRule(base)
	for k, v := range r {
		switch foldKey(k) {
		case foldKey("outboundTag"), foldKey("balancerTag"), foldKey("network"), foldKey("port"):
			bt[k] = v
		}
	}
	bt["inboundTag"] = in
	bt["protocol"] = marshalNoEscape([]string{"bittorrent"})
	return append([]json.RawMessage{marshalNoEscape(bt)}, copies...)
}

func cloneRule(m map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(m)+4)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ruleNetworks: the networks a rule takes ("tcp,udp", ["tcp","udp"], none
// = both). Anything else does not read.
func ruleNetworks(raw json.RawMessage) (map[string]bool, bool) {
	if raw == nil || isJSONNull(raw) || (jsonString(raw) == "" && len(jsonStrings(raw)) == 0) {
		return map[string]bool{"tcp": true, "udp": true}, true // unset, "" or []: both
	}
	var parts []string
	if s := jsonString(raw); s != "" {
		parts = strings.Split(s, ",")
	} else {
		parts = jsonStrings(raw)
	}
	out := map[string]bool{}
	for _, p := range parts {
		switch n := strings.ToLower(strings.TrimSpace(p)); n {
		case "tcp", "udp":
			out[n] = true
		case "":
		default:
			return nil, false
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// portRange is a closed range of ports.
type portRange struct{ lo, hi int }

var allPorts = []portRange{{1, 65535}}

// parsePortField reads xray's port field: a number, or a string of numbers
// and ranges ("53,443,1000-2000").
func parsePortField(raw json.RawMessage) ([]portRange, bool) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return parsePortList(n.String())
	}
	if s := jsonString(raw); s != "" {
		return parsePortList(s)
	}
	return nil, false
}

func parsePortList(s string) ([]portRange, bool) {
	var out []portRange
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			return nil, false
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				return nil, false
			}
		}
		if a < 1 || b > 65535 || a > b {
			return nil, false
		}
		out = append(out, portRange{a, b})
	}
	if len(out) == 0 {
		return nil, false
	}
	return normalizePorts(out), true
}

func normalizePorts(in []portRange) []portRange {
	sort.Slice(in, func(i, j int) bool { return in[i].lo < in[j].lo })
	var out []portRange
	for _, r := range in {
		if n := len(out); n > 0 && r.lo <= out[n-1].hi+1 {
			if r.hi > out[n-1].hi {
				out[n-1].hi = r.hi
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func intersectPorts(a, b []portRange) []portRange {
	var out []portRange
	for _, x := range a {
		for _, y := range b {
			lo, hi := max(x.lo, y.lo), min(x.hi, y.hi)
			if lo <= hi {
				out = append(out, portRange{lo, hi})
			}
		}
	}
	return normalizePorts(out)
}

func formatPorts(rs []portRange) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		if r.lo == r.hi {
			parts[i] = strconv.Itoa(r.lo)
		} else {
			parts[i] = strconv.Itoa(r.lo) + "-" + strconv.Itoa(r.hi)
		}
	}
	return strings.Join(parts, ",")
}

// isJSONNumber: raw is a JSON number (a port given as 443).
func isJSONNumber(raw json.RawMessage) bool {
	var n json.Number
	return raw != nil && json.Unmarshal(raw, &n) == nil
}
