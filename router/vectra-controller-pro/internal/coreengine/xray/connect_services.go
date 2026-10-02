package xray

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"vectra-controller-pro/internal/xrayview"
)

func connectServicesKey(entries map[string]json.RawMessage) string {
	var keys []string
	for id, raw := range entries {
		sum := sha256.Sum256(raw)
		keys = append(keys, id+"="+hex.EncodeToString(sum[:]))
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// ValidateConnectServiceEntry refuses ambiguous paths and unsupported graph
// references rather than silently using another provider location.
func ValidateConnectServiceEntry(raw []byte, id string) error {
	s, ok := ServiceByID(id)
	if !ok {
		return errors.New("unknown_service")
	}
	if len(raw) > 1<<20 {
		return errors.New("entry_too_large")
	}
	v, err := xrayview.Parse(raw)
	if err != nil {
		return errors.New("invalid_entry")
	}
	if _, ok := connectServicePath(raw, v, s); !ok {
		return errors.New("service_path_unavailable")
	}
	return nil
}

// connectServicePath is where a location chosen in the Vectra app sends a
// service. The location's own rule for it decides, as in the router's own
// service choice. A one-country location has none — everything that is not
// Russian goes down its main path — and choosing it for YouTube means that
// path: its catch-all rule (every network, nothing else), or else xray's
// default handler. Only a tunnel is a path: a main path that goes direct, is
// blocked, or is missing refuses the choice instead of quietly unproxying the
// service.
func connectServicePath(raw []byte, v *xrayview.View, s Service) (servicePathOf, bool) {
	rules := providerRules(raw)
	if p, ok := servicePath(rules, v, s); ok {
		return p, true
	}
	// An own rule that is no path (to a blackhole, to a missing tag) still
	// decides: the location says "not through me".
	if namesService(rules, s) {
		return servicePathOf{}, false
	}
	p := servicePathOf{domains: s.Domains, ips: s.IPs}
	if target, balancer, found := catchAllRule(rules); found {
		p.target, p.isBalancer = target, balancer
	} else if v.Default != nil && v.Default.Tag != "" {
		p.target = v.Default.Tag
	} else {
		return servicePathOf{}, false
	}
	if p.isBalancer {
		return p, balancerTunnels(v, p.target)
	}
	o := v.Outbound(p.target)
	return p, o != nil && o.Dials
}

// namesService: some rule names the service by its own matcher, with no
// other condition — the location's own rule, whatever it says.
func namesService(rules []map[string]json.RawMessage, s Service) bool {
	for _, r := range rules {
		for field, markers := range map[string][]string{"domain": s.Domains, "ip": s.IPs} {
			if len(markers) > 0 && slices.Contains(jsonStrings(ruleField(r, field)), markers[0]) && onlyMatcher(r, field) {
				return true
			}
		}
	}
	return false
}

// balancerTunnels: every way out of the balancer is a tunnel — some member
// dials, and its fallback (or, without one, xray's default handler, where a
// balancer with no live member sends traffic) dials too.
func balancerTunnels(v *xrayview.View, tag string) bool { return balancerTunnelsDepth(v, tag, 8) }

func balancerTunnelsDepth(v *xrayview.View, tag string, depth int) bool {
	b := v.Balancer(tag)
	if b == nil || depth == 0 {
		return false
	}
	member := false
	for _, m := range b.Members {
		if o := v.Outbound(m); o != nil && o.Dials {
			member = true
			break
		}
	}
	if !member {
		return false
	}
	switch {
	case b.FallbackBalancer != "":
		return balancerTunnelsDepth(v, b.FallbackBalancer, depth-1)
	case b.FallbackTag != "":
		o := v.Outbound(b.FallbackTag)
		return o != nil && o.Dials
	default:
		return v.Default != nil && v.Default.Dials
	}
}

// catchAllRule is the first rule that matches all traffic: no condition but
// a network naming both tcp and udp. Rules after it are never reached.
func catchAllRule(rules []map[string]json.RawMessage) (target string, balancer bool, found bool) {
	for _, r := range rules {
		network := ""
		other := false
		for k, val := range r {
			switch foldKey(k) {
			case foldKey("type"), foldKey("outboundTag"), foldKey("balancerTag"), foldKey("ruleTag"):
			case foldKey("network"):
				// "tcp,udp" or ["tcp","udp"]: xray takes both.
				network = strings.ToLower(jsonString(val) + "," + strings.Join(jsonStrings(val), ","))
			default:
				if !emptyJSON(val) {
					other = true
				}
			}
		}
		if other || !strings.Contains(network, "tcp") || !strings.Contains(network, "udp") {
			continue
		}
		if t := jsonString(ruleField(r, "balancerTag")); t != "" {
			return t, true, true
		}
		return jsonString(ruleField(r, "outboundTag")), false, true
	}
	return "", false, false
}

func addConnectServices(p *servicePlan, base []byte, entries map[string]json.RawMessage, inbound string) error {
	ids := make([]string, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	baseView, err := xrayview.Parse(base)
	if err != nil && len(ids) > 0 {
		return errors.New("invalid_entry")
	}
	for _, id := range ids {
		raw := entries[id]
		if err := ValidateConnectServiceEntry(raw, id); err != nil {
			return err
		}
		if len(raw) > 1<<20 {
			return errors.New("entry_too_large")
		}
		svc, _ := ServiceByID(id)
		v, _ := xrayview.Parse(raw)
		path, _ := connectServicePath(raw, v, svc)
		var doc struct {
			Outbounds []map[string]json.RawMessage `json:"outbounds"`
			Routing   struct {
				Balancers []map[string]json.RawMessage `json:"balancers"`
				Rules     []map[string]json.RawMessage `json:"rules"`
			} `json:"routing"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			return errors.New("invalid_entry")
		}
		if len(doc.Outbounds) > 256 || len(doc.Routing.Balancers) > 128 || len(doc.Routing.Rules) > 512 {
			return errors.New("entry_graph_too_large")
		}
		doc.Outbounds, doc.Routing.Balancers, doc.Routing.Rules = reachedGraph(doc.Outbounds, doc.Routing.Balancers, doc.Routing.Rules, path.target, v)
		prefix := "vctl-connect-" + id + "-"
		tags := map[string]string{}
		for _, o := range doc.Outbounds {
			tag := jsonString(o["tag"])
			if tag == "" {
				return errors.New("unsupported_entry_graph")
			}
			if tags[tag] != "" {
				return errors.New("duplicate_entry_tag")
			}
			tags[tag] = prefix + tag
		}
		for _, b := range doc.Routing.Balancers {
			tag := jsonString(b["tag"])
			if tag == "" {
				return errors.New("unsupported_entry_graph")
			}
			if tags[tag] != "" {
				return errors.New("duplicate_entry_tag")
			}
			tags[tag] = prefix + tag
		}
		for _, o := range doc.Outbounds {
			newTag := tags[jsonString(o["tag"])]
			for _, existing := range baseView.Outbounds {
				if existing.Tag == newTag {
					return errors.New("entry_tag_collision")
				}
			}
			for _, existing := range baseView.Balancers {
				for _, selector := range existing.Selector {
					if strings.HasPrefix(newTag, selector) {
						return errors.New("entry_selector_collision")
					}
				}
			}
			protocol := jsonString(o["protocol"])
			if protocol == "loopback" {
				var settings map[string]json.RawMessage
				if json.Unmarshal(o["settings"], &settings) != nil || jsonString(settings["inboundTag"]) == "" {
					return errors.New("unsupported_entry_graph")
				}
				newInbound := prefix + jsonString(settings["inboundTag"])
				for _, baseRule := range providerRules(base) {
					for _, existing := range jsonStrings(baseRule["inboundTag"]) {
						if existing == newInbound {
							return errors.New("entry_inbound_collision")
						}
					}
				}
				settings["inboundTag"] = marshalNoEscape(newInbound)
				o["settings"] = marshalNoEscape(settings)
			}
			o["tag"] = marshalNoEscape(tags[jsonString(o["tag"])])
			// Proxy chains and dialer proxies are references, never executable strings.
			for _, field := range []string{"proxySettings", "streamSettings"} {
				if val, ok := o[field]; ok {
					var obj map[string]json.RawMessage
					if json.Unmarshal(val, &obj) != nil {
						return errors.New("unsupported_entry_graph")
					}
					if field == "proxySettings" {
						if t := jsonString(obj["tag"]); t != "" {
							if tags[t] == "" {
								return errors.New("unsupported_entry_graph")
							}
							obj["tag"] = marshalNoEscape(tags[t])
						}
					}
					if field == "streamSettings" {
						if sock, ok := obj["sockopt"]; ok {
							var so map[string]json.RawMessage
							if json.Unmarshal(sock, &so) != nil {
								return errors.New("unsupported_entry_graph")
							}
							if t := jsonString(so["dialerProxy"]); t != "" {
								if tags[t] == "" {
									return errors.New("unsupported_entry_graph")
								}
								so["dialerProxy"] = marshalNoEscape(tags[t])
								obj["sockopt"] = marshalNoEscape(so)
							}
						}
					}
					o[field] = marshalNoEscape(obj)
				}
			}
			p.loopbacks = append(p.loopbacks, marshalNoEscape(o))
			if out := v.Outbound(strings.TrimPrefix(jsonString(o["tag"]), prefix)); out != nil && out.Dials {
				p.observe = append(p.observe, jsonString(o["tag"]))
			}
		}
		for _, b := range doc.Routing.Balancers {
			for _, existing := range baseView.Balancers {
				if existing.Tag == tags[jsonString(b["tag"])] {
					return errors.New("entry_tag_collision")
				}
			}
			old := jsonString(b["tag"])
			b["tag"] = marshalNoEscape(tags[old])
			var selectors []string
			for _, sel := range jsonStrings(b["selector"]) {
				for _, o := range v.Outbounds {
					if strings.HasPrefix(o.Tag, sel) {
						selectors = append(selectors, tags[o.Tag])
					}
				}
			}
			if len(selectors) == 0 {
				// A balancer of nodes the location does not have (the
				// provider's whitelist levels) passes through to its
				// fallback, here as in the provider's document: its own
				// selectors, namespaced, still match nothing.
				for _, sel := range jsonStrings(b["selector"]) {
					selectors = append(selectors, prefix+sel)
				}
				if len(selectors) == 0 {
					return errors.New("unsupported_entry_graph")
				}
			}
			b["selector"] = marshalNoEscape(selectors)
			if jsonString(b["fallbackTag"]) == "" {
				if v.Default == nil || tags[v.Default.Tag] == "" {
					return errors.New("unsupported_entry_graph")
				}
				b["fallbackTag"] = marshalNoEscape(v.Default.Tag)
			}
			if t := jsonString(b["fallbackTag"]); t != "" {
				// A fallback to a tag the location lacks (the end of the
				// provider's whitelist chain) leads nowhere, as in the
				// provider's document: namespaced, it still names nothing.
				b["fallbackTag"] = marshalNoEscape(prefix + t)
			}
			if !baseView.HasObservatory {
				var strategy map[string]json.RawMessage
				_ = json.Unmarshal(b["strategy"], &strategy)
				typ := jsonString(strategy["type"])
				if typ == "leastPing" || typ == "leastLoad" {
					return errors.New("entry_observatory_unavailable")
				}
			}
			p.balancers = append(p.balancers, marshalNoEscape(b))
		}
		for _, rule := range doc.Routing.Rules {
			inbounds := jsonStrings(rule["inboundTag"])
			if len(inbounds) == 0 {
				continue
			}
			for i, tag := range inbounds {
				if strings.HasPrefix(tag, "!") {
					return errors.New("unsupported_entry_graph")
				}
				inbounds[i] = prefix + tag
			}
			rule["inboundTag"] = marshalNoEscape(inbounds)
			for _, field := range []string{"outboundTag", "balancerTag"} {
				if tag := jsonString(rule[field]); tag != "" {
					// Namespaced like every tag; one the location lacks still
					// names nothing, as in the provider's document.
					rule[field] = marshalNoEscape(prefix + tag)
				}
			}
			p.backRules = append(p.backRules, marshalNoEscape(rule))
		}
		target := tags[path.target]
		if target == "" {
			return errors.New("unsupported_entry_graph")
		}
		field := "outboundTag"
		if path.isBalancer {
			field = "balancerTag"
		}
		for _, key := range []string{"domain", "ip"} {
			match := path.domains
			if key == "ip" {
				match = path.ips
			}
			if len(match) > 0 {
				p.rules = append(p.rules, marshalNoEscape(map[string]any{"inboundTag": []string{inbound}, key: match, field: target}))
			}
		}
		p.res.Applied = append(p.res.Applied, id+"=entry")
	}
	return nil
}

// reachedGraph keeps the part of a location's graph that the service's path
// reaches: from the target, a balancer's members and fallback (or xray's
// default handler), an outbound's proxy and dialer, and a loopback's stage —
// the rules on its inbound and their targets. A provider's location also
// carries balancers it never uses (a selector naming no node it has, a
// fallback to a tag it lacks); xray ignores them, and so does the import.
// Whatever is reached keeps every reference it makes, so a broken reached
// graph is still refused further on.
func reachedGraph(outbounds, balancers, rules []map[string]json.RawMessage, target string, v *xrayview.View) ([]map[string]json.RawMessage, []map[string]json.RawMessage, []map[string]json.RawMessage) {
	outByTag := map[string]map[string]json.RawMessage{}
	for _, o := range outbounds {
		if t := jsonString(o["tag"]); t != "" {
			outByTag[t] = o
		}
	}
	balByTag := map[string]map[string]json.RawMessage{}
	for _, b := range balancers {
		if t := jsonString(b["tag"]); t != "" {
			balByTag[t] = b
		}
	}
	keep := map[string]bool{}
	keepRule := map[int]bool{}
	queue := []string{target}
	for len(queue) > 0 {
		tag := queue[0]
		queue = queue[1:]
		if tag == "" || keep[tag] {
			continue
		}
		keep[tag] = true
		if b, ok := balByTag[tag]; ok {
			for _, sel := range jsonStrings(b["selector"]) {
				for t := range outByTag {
					if strings.HasPrefix(t, sel) {
						queue = append(queue, t)
					}
				}
			}
			if fb := jsonString(b["fallbackTag"]); fb != "" {
				queue = append(queue, fb)
			} else if v.Default != nil {
				queue = append(queue, v.Default.Tag)
			}
			continue
		}
		o, ok := outByTag[tag]
		if !ok {
			continue
		}
		var ps map[string]json.RawMessage
		if json.Unmarshal(o["proxySettings"], &ps) == nil {
			queue = append(queue, jsonString(ps["tag"]))
		}
		var ss struct {
			Sockopt map[string]json.RawMessage `json:"sockopt"`
		}
		if json.Unmarshal(o["streamSettings"], &ss) == nil && ss.Sockopt != nil {
			queue = append(queue, jsonString(ss.Sockopt["dialerProxy"]))
		}
		if jsonString(o["protocol"]) == "loopback" {
			var st map[string]json.RawMessage
			_ = json.Unmarshal(o["settings"], &st)
			stage := jsonString(st["inboundTag"])
			for i, r := range rules {
				if ruleOnStage(jsonStrings(r["inboundTag"]), stage) {
					keepRule[i] = true
					queue = append(queue, jsonString(r["outboundTag"]), jsonString(r["balancerTag"]))
				}
			}
		}
	}
	var outs, bals, rs []map[string]json.RawMessage
	for _, o := range outbounds {
		// An untagged outbound is no reference target; keep it so the import
		// still refuses it (unsupported_entry_graph) rather than hide it.
		if t := jsonString(o["tag"]); t == "" || keep[t] {
			outs = append(outs, o)
		}
	}
	for _, b := range balancers {
		if t := jsonString(b["tag"]); t == "" || keep[t] {
			bals = append(bals, b)
		}
	}
	for i, r := range rules {
		// Rules on an inbound are stages: only those of a reached loopback.
		// Rules without one are never imported (see addConnectServices).
		if keepRule[i] {
			rs = append(rs, r)
		}
	}
	return outs, bals, rs
}

// ruleOnStage: a rule's inbound list names the stage. A negated inbound is
// refused by the import outright (its reach over the stages is not checked),
// so a rule carrying one is kept to be refused, never silently dropped.
func ruleOnStage(inbounds []string, stage string) bool {
	for _, t := range inbounds {
		if t == stage || strings.HasPrefix(t, "!") {
			return true
		}
	}
	return false
}
