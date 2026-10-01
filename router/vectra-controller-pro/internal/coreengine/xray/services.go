package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"vectra-controller-pro/internal/xrayview"
)

// A server per service (docs/superpowers/specs/2026-09-30-vctl-service-
// routing-design.md, decision 3). A few services — only those a person
// really picks a country for — can leave the entry's own path for a country
// the entry has. The choice is an overlay on the provider's document: a
// balancer over that country's outbounds, backed by the service's own path
// in the entry through a loopback, so a dead country falls back to the
// default and never to nothing; its rules sit below the owner's own sites
// and above the provider's.

// Service is one service the owner can send through a country.
type Service struct {
	ID string
	// Domains and IPs name the service in the entry: the entry's own rule
	// that names one of them (and nothing else) is the service's path, and
	// its whole list is what the overlay matches — the provider names a
	// service by more than its geosite category. A rule's conditions are
	// ANDed, so a service reached by name or by address has a rule for each.
	Domains []string
	IPs     []string
}

// Services are the services with a choice.
var Services = []Service{
	{ID: "youtube", Domains: []string{"geosite:youtube"}},
	{ID: "tiktok", Domains: []string{"geosite:tiktok"}},
	{ID: "telegram", Domains: []string{"geosite:telegram"}, IPs: []string{"geoip:telegram"}},
}

// ServiceByID finds a service.
func ServiceByID(id string) (Service, bool) {
	for _, s := range Services {
		if s.ID == id {
			return s, true
		}
	}
	return Service{}, false
}

func serviceBalancerTag(id string) string { return "VCTL-SVC-" + strings.ToUpper(id) }
func serviceLoopbackTag(id string) string { return "vctl-svc-" + id }

// ServicesResult says what the splice made of the owner's choices.
type ServicesResult struct {
	Applied []string // "tiktok=DE"
	Stale   []string // a country the entry has no outbound for: not rendered
}

// ServiceCountriesResult is what the UI can offer for the running entry.
type ServiceCountriesResult struct {
	// Countries are the ISO codes the entry's dialling outbounds name.
	Countries []string
	// Defaults are the country of each service's own path, where every
	// outbound of it names one country.
	Defaults map[string]string
	// Offered are the services the entry has a path of its own for: only
	// those can be given a country (the overlay falls back to that path).
	Offered map[string]bool
}

// ServiceCountries reads what a provider document offers.
func ServiceCountries(providerRaw []byte) ServiceCountriesResult {
	res := ServiceCountriesResult{Defaults: map[string]string{}, Offered: map[string]bool{}}
	v, err := xrayview.Parse(providerRaw)
	if err != nil {
		return res
	}
	res.Countries = entryCountries(v)
	rules := providerRules(providerRaw)
	for _, s := range Services {
		p, ok := servicePath(rules, v, s)
		if !ok {
			continue
		}
		res.Offered[s.ID] = true
		members := []string{p.target}
		if p.isBalancer {
			if b := v.Balancer(p.target); b != nil {
				members = b.Members
			}
		}
		cc := ""
		for _, m := range members {
			h := xrayview.CountryHint(m)
			if h == "" || (cc != "" && h != cc) {
				cc = ""
				break
			}
			cc = h
		}
		if cc != "" {
			res.Defaults[s.ID] = cc
		}
	}
	return res
}

func entryCountries(v *xrayview.View) []string {
	seen := map[string]bool{}
	var out []string
	for _, o := range v.Outbounds {
		if !o.Dials {
			continue
		}
		if cc := xrayview.CountryHint(o.Tag); cc != "" && !seen[cc] {
			seen[cc] = true
			out = append(out, cc)
		}
	}
	sort.Strings(out)
	return out
}

func providerRules(providerRaw []byte) []map[string]json.RawMessage {
	var doc struct {
		Routing struct {
			Rules []map[string]json.RawMessage `json:"rules"`
		} `json:"routing"`
	}
	_ = json.Unmarshal(providerRaw, &doc)
	return doc.Routing.Rules
}

// ownRule is the entry's own rule for one matcher of a service.
type ownRule struct {
	matchers   []string // the rule's whole list: what the entry calls the service
	target     string
	isBalancer bool
}

// findOwnRule finds the first rule whose field ("domain" or "ip") names
// marker and that has no other condition: conditions are ANDed, so «YouTube
// over UDP → BLOCK» is not where YouTube goes, and a rule tied to an inbound
// of its own is a stage. That rule decides: one to a blackhole, or to a tag
// the document lacks, is no path.
func findOwnRule(rules []map[string]json.RawMessage, v *xrayview.View, field, marker string) (ownRule, bool) {
	for _, r := range rules {
		list := jsonStrings(ruleField(r, field))
		if !slices.Contains(list, marker) || !onlyMatcher(r, field) {
			continue
		}
		if t := jsonString(ruleField(r, "balancerTag")); t != "" {
			return ownRule{matchers: list, target: t, isBalancer: true}, v.Balancer(t) != nil
		}
		t := jsonString(ruleField(r, "outboundTag"))
		o := v.Outbound(t)
		return ownRule{matchers: list, target: t}, o != nil && !strings.EqualFold(o.Protocol, "blackhole")
	}
	return ownRule{}, false
}

// onlyMatcher: r has no condition but field.
func onlyMatcher(r map[string]json.RawMessage, field string) bool {
	for k, val := range r {
		switch foldKey(k) {
		case foldKey("type"), foldKey("outboundTag"), foldKey("balancerTag"), foldKey("ruleTag"), foldKey("domainMatcher"), foldKey(field):
		default:
			if !emptyJSON(val) {
				return false
			}
		}
	}
	return true
}

// servicePathOf is where the entry sends a service, and by which matchers.
type servicePathOf struct {
	target     string
	isBalancer bool
	domains    []string // nil: no rule by name
	ips        []string // nil: no rule by address to the same path
}

// servicePath is the service's own path in the entry: the path of its rule
// by name (or, without one, by address); a rule by address to another path
// is left to the provider.
func servicePath(rules []map[string]json.RawMessage, v *xrayview.View, s Service) (servicePathOf, bool) {
	var p servicePathOf
	dom, hasDom := ownRule{}, false
	if len(s.Domains) > 0 {
		dom, hasDom = findOwnRule(rules, v, "domain", s.Domains[0])
	}
	ip, hasIP := ownRule{}, false
	if len(s.IPs) > 0 {
		ip, hasIP = findOwnRule(rules, v, "ip", s.IPs[0])
	}
	switch {
	case hasDom:
		p = servicePathOf{target: dom.target, isBalancer: dom.isBalancer, domains: dom.matchers}
		if hasIP && ip.target == dom.target && ip.isBalancer == dom.isBalancer {
			p.ips = ip.matchers
		}
	case hasIP:
		p = servicePathOf{target: ip.target, isBalancer: ip.isBalancer, ips: ip.matchers}
	default:
		return p, false
	}
	return p, true
}

type servicePlan struct {
	loopbacks []json.RawMessage // outbounds to append
	balancers []json.RawMessage
	backRules []json.RawMessage // loopback → the service's own path
	rules     []json.RawMessage // the services, for the LAN's traffic
	observe   []string          // outbounds the observatory must probe
	res       ServicesResult
}

func (p servicePlan) empty() bool { return len(p.balancers) == 0 && len(p.rules) == 0 }

// planServices resolves the owner's choices against the document.
func planServices(providerRaw []byte, choices map[string]string, inboundTag string) (servicePlan, error) {
	var p servicePlan
	if len(choices) == 0 {
		return p, nil
	}
	v, err := xrayview.Parse(providerRaw)
	if err != nil {
		return p, fmt.Errorf("xray splice: services: %w", err)
	}
	rules := providerRules(providerRaw)
	hasObservatory := v.HasObservatory
	for _, s := range Services {
		cc := choices[s.ID]
		if cc == "" {
			continue
		}
		var tags []string
		for _, o := range v.Outbounds {
			if o.Dials && xrayview.CountryHint(o.Tag) == cc {
				tags = append(tags, o.Tag)
			}
		}
		own, ok := servicePath(rules, v, s)
		if len(tags) == 0 || !ok {
			p.res.Stale = append(p.res.Stale, s.ID)
			continue
		}
		target := own.target
		bal := serviceBalancerTag(s.ID)
		fallback := target
		if own.isBalancer {
			// A fallbackTag names an outbound, and the service's own path is
			// a balancer: a loopback leads back into it.
			lb := serviceLoopbackTag(s.ID)
			fallback = lb
			p.loopbacks = append(p.loopbacks, marshalNoEscape(map[string]any{
				"tag": lb, "protocol": "loopback", "settings": map[string]string{"inboundTag": lb}}))
			p.backRules = append(p.backRules, marshalNoEscape(map[string]any{
				"inboundTag": []string{lb}, "balancerTag": target}))
		}
		// leastPing wants the observatory's word; without an observatory it
		// has none and would hand everything to the fallback.
		strategy := "leastPing"
		if !hasObservatory {
			strategy = "random"
		}
		p.balancers = append(p.balancers, marshalNoEscape(map[string]any{
			"tag": bal, "selector": tags, "strategy": map[string]string{"type": strategy}, "fallbackTag": fallback}))
		if own.domains != nil {
			p.rules = append(p.rules, marshalNoEscape(map[string]any{
				"inboundTag": []string{inboundTag}, "domain": own.domains, "balancerTag": bal}))
		}
		if own.ips != nil {
			p.rules = append(p.rules, marshalNoEscape(map[string]any{
				"inboundTag": []string{inboundTag}, "ip": own.ips, "balancerTag": bal}))
		}
		p.observe = append(p.observe, tags...)
		p.res.Applied = append(p.res.Applied, s.ID+"="+cc)
	}
	return p, nil
}

// servicesKey is the choices in a stable order, for the render key.
func servicesKey(choices map[string]string) string {
	var parts []string
	for id, cc := range choices {
		if cc != "" {
			parts = append(parts, id+"="+cc)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// appendBalancers adds balancers at the end of routing.balancers, the key
// found as xray finds it.
func appendBalancers(routing json.RawMessage, balancers []json.RawMessage) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(routing, &obj); err != nil {
		return nil, fmt.Errorf("xray splice: routing is not an object: %w", err)
	}
	field := "balancers"
	var existing []json.RawMessage
	for k, v := range obj {
		if foldKey(k) != foldKey("balancers") {
			continue
		}
		field = k
		if !isJSONNull(v) {
			if err := json.Unmarshal(v, &existing); err != nil {
				return nil, fmt.Errorf("xray splice: routing.balancers is not an array: %w", err)
			}
		}
	}
	var b bytes.Buffer
	b.WriteByte('[')
	for i, r := range append(existing, balancers...) {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(r)
	}
	b.WriteByte(']')
	out, _, err := rewriteObjectField(routing, field, b.Bytes())
	return out, err
}

// observeTags adds to an observatory's subjectSelector the tags it does not
// already cover (a selector entry is a prefix).
func observeTags(obs json.RawMessage, tags []string) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(obs, &obj); err != nil {
		return nil, fmt.Errorf("xray splice: observatory is not an object: %w", err)
	}
	field := "subjectSelector"
	var sel []string
	for k, v := range obj {
		if foldKey(k) == foldKey("subjectSelector") {
			field = k
			_ = json.Unmarshal(v, &sel)
		}
	}
	added := false
	for _, t := range tags {
		covered := false
		for _, s := range sel {
			if strings.HasPrefix(t, s) {
				covered = true
				break
			}
		}
		if !covered {
			sel = append(sel, t)
			added = true
		}
	}
	if !added {
		return obs, nil
	}
	out, _, err := rewriteObjectField(obs, field, marshalNoEscape(sel))
	return out, err
}
