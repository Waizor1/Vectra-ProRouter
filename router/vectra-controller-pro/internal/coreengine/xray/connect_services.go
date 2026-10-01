package xray

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	if _, ok := servicePath(providerRules(raw), v, s); !ok {
		return errors.New("service_path_unavailable")
	}
	return nil
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
		path, _ := servicePath(providerRules(raw), v, svc)
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
				return errors.New("unsupported_entry_graph")
			}
			b["selector"] = marshalNoEscape(selectors)
			if jsonString(b["fallbackTag"]) == "" {
				if v.Default == nil || tags[v.Default.Tag] == "" {
					return errors.New("unsupported_entry_graph")
				}
				b["fallbackTag"] = marshalNoEscape(v.Default.Tag)
			}
			if t := jsonString(b["fallbackTag"]); t != "" {
				if tags[t] == "" {
					return errors.New("unsupported_entry_graph")
				}
				b["fallbackTag"] = marshalNoEscape(tags[t])
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
					if tags[tag] == "" {
						return errors.New("unsupported_entry_graph")
					}
					rule[field] = marshalNoEscape(tags[tag])
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
