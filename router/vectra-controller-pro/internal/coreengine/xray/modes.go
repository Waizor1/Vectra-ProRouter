package xray

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/xrayview"
)

// A service's mode (r26, docs/superpowers/specs/2026-10-18-vctl-services-as-
// data.md): «Напрямую» sends what the service is straight out; «VPN ·
// постоянный IP» sends it through the stable exit — one node the router
// found to keep one address (the provider's nodes mostly change it between
// connections, and a launcher that ties its login to the address logs out).
// A service on «Как основной VPN» gets no rule: the provider's routing
// decides, as without vctl.

const (
	// StableBalancerTag is the stable exit's balancer: one member, the pin;
	// its fallback leads back into the main path, so a dead pin falls back
	// and never to nothing.
	StableBalancerTag = "VCTL-STABLE"
	stableBackTag     = "vctl-stable-back"
)

// ModesResult says what the services' modes became.
type ModesResult struct {
	Direct  []string // services sent straight out
	Stable  []string // services through the stable exit
	Pin     string   // the stable exit rendered ("" = none)
	Main    bool     // the main path through the stable exit too
	Skipped []string // "<id>: why"
}

// modesKey fingerprints the modes, the pin and Main for SpliceOptions.Key.
func modesKey(modes map[string]string, pin string, main bool) string {
	var parts []string
	for id, m := range modes {
		if m == catalog.ModeDirect || m == catalog.ModeStable {
			parts = append(parts, id+"="+m)
		}
	}
	if len(parts) == 0 && !main {
		return ""
	}
	sort.Strings(parts)
	k := strings.Join(parts, ",")
	if pin != "" {
		k += ";pin=" + pin
	}
	if main {
		k += ";main"
	}
	return k
}

// modesPlan is what the modes add to the render.
type modesPlan struct {
	directRules []json.RawMessage
	stableRules []json.RawMessage
	backRules   []json.RawMessage
	loopbacks   []json.RawMessage
	balancers   []json.RawMessage
	observe     []string
	addDirect   bool
	// catchAll: rewrite the provider's catch-all to the stable balancer.
	catchAll bool
	res      ModesResult
}

// planModes renders the modes of opts against the document. It never
// refuses a document: a service whose mode cannot be rendered here is left
// to the provider's routing (res.Skipped).
func planModes(providerRaw []byte, modes map[string]string, pin string, main bool, inbound string) (modesPlan, error) {
	var p modesPlan
	if modesKey(modes, "", main) == "" {
		return p, nil
	}
	v, err := xrayview.Parse(providerRaw)
	if err != nil {
		return p, fmt.Errorf("xray splice: modes: %w", err)
	}
	rules := providerRules(providerRaw)

	pinOK, why := stablePinUsable(v, pin)
	if pinOK && !v.HasObservatory {
		// Without an observatory xray's balancer never notices a dead pin
		// and never takes its fallback.
		pinOK, why = false, "the document has no observatory to notice a dead pin"
	}
	backTarget, backBalancer, hasMain := catchAllRule(rules)
	if !hasMain && v.Default != nil && v.Default.Tag != "" {
		backTarget, backBalancer, hasMain = v.Default.Tag, false, true
	}
	if pinOK && !hasMain {
		pinOK, why = false, "the document has no main path to fall back to"
	}
	if pinOK {
		// The fallback must be a tunnel: a dead pin never sends a stable
		// service straight out.
		// Every way out of the main path — its members, its fallbacks,
		// the provider's loopback stages — is a tunnel (neverDirect).
		if !neverDirect(v, rules, backTarget, backBalancer, neverDirectDepth) {
			pinOK, why = false, "the main path is not a tunnel"
		}
	}

	directTag, addDirect, directWhy := directForModes(providerRaw)
	for _, s := range Services() {
		mode := modes[s.ID]
		if mode != catalog.ModeDirect && mode != catalog.ModeStable {
			continue
		}
		domains, ips := s.ownMatchers(rules, v)
		domains = usableNames(domains)
		if len(domains) == 0 && len(ips) == 0 {
			p.res.Skipped = append(p.res.Skipped, s.ID+": no names or addresses")
			continue
		}
		var target map[string]any
		switch mode {
		case catalog.ModeDirect:
			if directTag == "" {
				p.res.Skipped = append(p.res.Skipped, s.ID+": "+directWhy)
				continue
			}
			target = map[string]any{"outboundTag": directTag}
			p.addDirect = p.addDirect || addDirect
			p.res.Direct = append(p.res.Direct, s.ID)
		case catalog.ModeStable:
			if !pinOK {
				// Without a stable exit the service rides the provider's
				// routing, as on «Как основной VPN».
				p.res.Skipped = append(p.res.Skipped, s.ID+": "+why)
				continue
			}
			target = map[string]any{"balancerTag": StableBalancerTag}
			p.res.Stable = append(p.res.Stable, s.ID)
		}
		for _, m := range []struct {
			key  string
			list []string
		}{{"domain", domains}, {"ip", ips}} {
			if len(m.list) == 0 {
				continue
			}
			r := map[string]any{"inboundTag": []string{inbound}, m.key: m.list}
			for k, val := range target {
				r[k] = val
			}
			if mode == catalog.ModeDirect {
				p.directRules = append(p.directRules, marshalNoEscape(r))
			} else {
				p.stableRules = append(p.stableRules, marshalNoEscape(r))
			}
		}
	}
	if main {
		switch {
		case !pinOK:
			p.res.Skipped = append(p.res.Skipped, "main: "+why)
		case !hasCatchAll(rules):
			p.res.Skipped = append(p.res.Skipped, "main: the document has no catch-all rule")
		default:
			p.catchAll, p.res.Main = true, true
		}
	}
	if len(p.stableRules) > 0 || p.catchAll {
		strategy := "leastPing"
		if !v.HasObservatory {
			strategy = "random"
		}
		p.balancers = append(p.balancers, marshalNoEscape(map[string]any{
			"tag": StableBalancerTag, "selector": []string{pin},
			"strategy": map[string]string{"type": strategy}, "fallbackTag": stableBackTag}))
		p.loopbacks = append(p.loopbacks, marshalNoEscape(map[string]any{
			"tag": stableBackTag, "protocol": "loopback", "settings": map[string]string{"inboundTag": stableBackTag}}))
		back := map[string]any{"inboundTag": []string{stableBackTag}}
		if backBalancer {
			back["balancerTag"] = backTarget
		} else {
			back["outboundTag"] = backTarget
		}
		p.backRules = append(p.backRules, marshalNoEscape(back))
		p.observe = append(p.observe, pin)
		p.res.Pin = pin
	}
	return p, nil
}

// stablePinUsable: the pin is a dialling outbound of the document that no
// other tag extends — a balancer selector is a prefix, and "bridge-fr5"
// must not also pick "bridge-fr5-2".
func stablePinUsable(v *xrayview.View, pin string) (bool, string) {
	if pin == "" {
		return false, "no stable exit yet"
	}
	o := v.Outbound(pin)
	if o == nil || !o.Dials {
		return false, "the stable exit " + pin + " is not in this document"
	}
	for _, other := range v.Outbounds {
		if other.Tag != pin && strings.HasPrefix(other.Tag, pin) {
			return false, "the stable exit " + pin + " is a prefix of " + other.Tag
		}
	}
	return true, ""
}

// hasCatchAll: some rule for everything (catchAll) exists.
func hasCatchAll(rules []map[string]json.RawMessage) bool {
	_, _, ok := catchAllRule(rules)
	return ok
}

// usableNames drops the catalog's geosite: names the router's geo file
// lacks (catalogNameUsable): xray would refuse the whole render.
func usableNames(list []string) []string {
	out := list[:0:0]
	for _, m := range list {
		if catalogNameUsable(m) {
			out = append(out, m)
		}
	}
	return out
}

// directForModes is the plain freedom outbound the direct services leave
// through: the provider's own, else DirectTag appended (add) when that is
// safe; "" and why otherwise.
func directForModes(providerRaw []byte) (tag string, add bool, why string) {
	var top map[string]json.RawMessage
	var doc struct {
		Outbounds []json.RawMessage `json:"outbounds"`
		Routing   *struct {
			Balancers []struct {
				Selector []string `json:"selector"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if json.Unmarshal(providerRaw, &top) != nil || json.Unmarshal(providerRaw, &doc) != nil {
		return "", false, "the document does not read"
	}
	obs := make([]providerOutbound, len(doc.Outbounds))
	for i, raw := range doc.Outbounds {
		obs[i] = readOutbound(raw)
		if obs[i].plainDirect() {
			return obs[i].Tag, false, ""
		}
	}
	var selectors []string
	if doc.Routing != nil {
		for _, b := range doc.Routing.Balancers {
			selectors = append(selectors, b.Selector...)
		}
	}
	if why := cannotAddDirect(top, obs, selectors); why != "" {
		return "", false, why
	}
	return DirectTag, true, ""
}

// rewriteCatchAll sends the provider's first catch-all rule (catchAll) to
// the stable balancer; its old target is reached through the stable
// fallback (stableBackTag).
func rewriteCatchAll(routing json.RawMessage) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(routing, &obj); err != nil {
		return nil, fmt.Errorf("xray splice: routing is not an object: %w", err)
	}
	field := "rules"
	for k := range obj {
		if foldKey(k) == foldKey("rules") {
			field = k
		}
	}
	var rules []json.RawMessage
	if json.Unmarshal(obj[field], &rules) != nil {
		return routing, nil
	}
	for i, raw := range rules {
		var r map[string]json.RawMessage
		if json.Unmarshal(raw, &r) != nil || !isCatchAllRule(r) {
			continue
		}
		for k := range r {
			if fk := foldKey(k); fk == foldKey("outboundTag") || fk == foldKey("balancerTag") {
				delete(r, k)
			}
		}
		r["balancerTag"] = marshalNoEscape(StableBalancerTag)
		b, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		rules[i] = b
		rb, err := json.Marshal(rules)
		if err != nil {
			return nil, err
		}
		out, _, err := rewriteObjectField(routing, field, rb)
		return out, err
	}
	return routing, nil
}
