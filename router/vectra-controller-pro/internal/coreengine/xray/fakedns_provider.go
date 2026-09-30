package xray

import (
	"encoding/json"
	"fmt"
	"strings"

	"vectra-controller-pro/internal/config"
)

// FakeDNS for what the rules proxy (SpliceOptions.FakeDNS; spec decision 8).
//
// The provider's Russian rules send geoip:ru straight out, but its domain
// rules come first: a blocked site whose address is in a Russian network is
// proxied by name, and a kernel that sent geoip:ru straight out would send it
// out too. So the router answers every name a rule sends anywhere but
// straight out with a FakeDNS address: such a connection reaches xray, which
// knows the name each address stands for, and the kernel may take the rest of
// the Russian networks (DirectBypass). PassWall2 kept the fleet's proxied
// names apart the same way.
//
// A client that resolves names itself (its own DoH) gets real addresses: a
// proxied site in a Russian network then leaves by the kernel, as it did
// under PassWall2.

// FakeDNSKey is the top-level key of xray's FakeDNS pools.
const FakeDNSKey = "fakedns"

// providerFakePool is the pool the router's FakeDNS hands out: carried by the
// data plane, not bypassed (cmd/vctl carryFakeDNS).
const providerFakePool = "198.18.0.0/16"

func fakeDNSObject() []byte {
	return []byte(`[{"ipPool":"` + providerFakePool + `","poolSize":65535}]`)
}

// fakeDNSServer answers names with FakeDNS addresses — its own names only:
// xray asks every server that does not skip the fallback for a name no
// server lists, and this one would hand them all a fake address too.
func fakeDNSServer(names []string) json.RawMessage {
	return marshalNoEscape(struct {
		Address      string   `json:"address"`
		Domains      []string `json:"domains"`
		SkipFallback bool     `json:"skipFallback"`
	}{"fakedns", names, true})
}

// proxiedNames are the domain matchers of every rule that sends the LAN's
// traffic anywhere but straight out — the owner's, the services', the
// provider's as the router renders them — in the order xray meets them, up
// to the rule that takes everything: nothing after it can be reached.
func proxiedNames(providerRaw []byte, ruTag string, owner, services []json.RawMessage, addDirect bool, tproxyTag string) ([]string, error) {
	direct := plainFreedomTags(providerRaw)
	if addDirect {
		direct[DirectTag] = true
	}
	var rules []map[string]json.RawMessage
	for _, raw := range append(append([]json.RawMessage{}, owner...), services...) {
		var r map[string]json.RawMessage
		if json.Unmarshal(raw, &r) == nil {
			rules = append(rules, r)
		}
	}
	provider, err := renderedProviderRules(providerRaw, ruTag)
	if err != nil {
		return nil, err
	}
	rules = append(rules, provider...)
	seen := map[string]bool{}
	var out []string
	for _, r := range rules {
		if !appliesTo(r, tproxyTag) {
			continue
		}
		bal := jsonString(ruleField(r, "balancerTag"))
		if bal != "" || !direct[jsonString(ruleField(r, "outboundTag"))] {
			for _, d := range jsonStrings(ruleField(r, "domain")) {
				if !seen[d] {
					seen[d] = true
					out = append(out, d)
				}
			}
		}
		if catchAll(r) {
			break
		}
	}
	return out, nil
}

// renderedProviderRules are the provider's routing rules as the render has
// them: the Russian ones sent straight out when ruTag is set.
func renderedProviderRules(providerRaw []byte, ruTag string) ([]map[string]json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(providerRaw, &doc); err != nil {
		return nil, fmt.Errorf("xray splice: read the document for FakeDNS: %w", err)
	}
	routing := ruleField(doc, RoutingKey)
	if len(routing) == 0 || isJSONNull(routing) {
		return nil, nil
	}
	if ruTag != "" {
		rw, _, err := rewriteRussianRules(routing, ruTag)
		if err != nil {
			return nil, err
		}
		routing = rw
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(routing, &obj); err != nil {
		return nil, fmt.Errorf("xray splice: routing is not an object: %w", err)
	}
	var rules []map[string]json.RawMessage
	if raw := ruleField(obj, "rules"); len(raw) > 0 && !isJSONNull(raw) {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return nil, fmt.Errorf("xray splice: routing.rules is not an array of objects: %w", err)
		}
	}
	return rules, nil
}

// plainFreedomTags are the document's outbounds that send a connection out
// as the kernel would.
func plainFreedomTags(providerRaw []byte) map[string]bool {
	var doc struct {
		Outbounds []struct {
			Tag            string                     `json:"tag"`
			Protocol       string                     `json:"protocol"`
			Settings       map[string]json.RawMessage `json:"settings"`
			StreamSettings map[string]json.RawMessage `json:"streamSettings"`
			ProxySettings  json.RawMessage            `json:"proxySettings"`
		} `json:"outbounds"`
	}
	_ = json.Unmarshal(providerRaw, &doc)
	out := map[string]bool{}
	for _, ob := range doc.Outbounds {
		if ob.Protocol == "freedom" && plainFreedom(ob.Settings, ob.StreamSettings, ob.ProxySettings) {
			out[ob.Tag] = true
		}
	}
	return out
}

// hasTopLevelKey: the document has key at its top level, as xray matches it.
func hasTopLevelKey(raw []byte, key string) bool {
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return false
	}
	return len(ruleField(doc, key)) > 0
}

// sniffingWithFakeDNS is the tproxy inbound's sniffing as FakeDNS needs it:
// on, the names sniffed for routing (sniffingForRules), and "fakedns" — xray
// then dials the name a FakeDNS address stands for, routeOnly or not.
func sniffingWithFakeDNS(s config.Sniffing) config.Sniffing {
	out, _ := sniffingForRules(s)
	for _, d := range out.DestOverride {
		if strings.EqualFold(d, "fakedns") {
			return out
		}
	}
	out.DestOverride = append([]string{"fakedns"}, out.DestOverride...)
	return out
}

// renderHasFakeDNSServer: the render's DNS answers some names with FakeDNS
// addresses (this splice's, or a PassWall2 document's own).
func renderHasFakeDNSServer(dns json.RawMessage) bool {
	var obj map[string]json.RawMessage
	if json.Unmarshal(dns, &obj) != nil {
		return false
	}
	var servers []json.RawMessage
	_ = json.Unmarshal(ruleField(obj, "servers"), &servers)
	for _, s := range servers {
		addr := jsonString(s)
		if addr == "" {
			var so map[string]json.RawMessage
			if json.Unmarshal(s, &so) == nil {
				addr = jsonString(ruleField(so, "address"))
			}
		}
		if strings.EqualFold(strings.TrimSpace(addr), "fakedns") {
			return true
		}
	}
	return false
}
