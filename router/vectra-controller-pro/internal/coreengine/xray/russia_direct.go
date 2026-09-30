package xray

import (
	"encoding/json"
	"fmt"
	"strings"
)

// On a home router in Russia, Russian sites are faster and lighter direct
// than through the provider's Russian bridge (BL-RU), which serves phones
// abroad and on whitelisted mobile networks. YouTube's BL-RU rule is the
// provider's ad-free path and stays. The provider's order of rules is kept:
// nothing moves ahead of YouTube.
var ruMarkers = map[string]bool{
	"geoip:ru": true, "geosite:category-gov-ru": true, "geosite:category-ru": true,
	"domain:ru": true, "domain:xn--p1ai": true, "domain:su": true, "domain:рф": true,
	"domain:by": true,
}

// ruTLDs are the top-level domains a domain matcher is Russian by.
var ruTLDs = []string{"ru", "su", "by", "xn--p1ai", "рф"}

// russianMatcher: a matcher of Russian destinations — a marker, or in the
// domain field a domain or full name under a Russian top-level domain.
// Anything else — a name elsewhere, a keyword, an address the provider wrote,
// another country's category — the provider sends through its Russian server
// on purpose (1111, 2026-09-30: nnmclub.to, kinozal.tv — they want a Russian
// address, and the filter blocks them at home). What is not known Russian
// keeps the bridge: through it a Russian destination still works, direct a
// foreign one meets the filter.
func russianMatcher(field, m string) bool {
	m = foldValue(m)
	if ruMarkers[m] {
		return true
	}
	if field == "ip" {
		return false
	}
	for _, p := range []string{"domain:", "full:"} {
		host, ok := strings.CutPrefix(m, p)
		if !ok {
			continue
		}
		for _, tld := range ruTLDs {
			if host == tld || strings.HasSuffix(host, "."+tld) {
				return true
			}
		}
	}
	return false
}

// foldValue lowercases a matcher past its prefix, as xray reads it: the
// prefix is exact ("geoip:"), the name or code after it is not ("RU", "YA.RU").
func foldValue(m string) string {
	for _, p := range []string{"geoip:", "geosite:", "domain:", "full:"} {
		if v, ok := strings.CutPrefix(m, p); ok {
			return p + strings.ToLower(v)
		}
	}
	return m
}

var youtubeMarkers = map[string]bool{
	"geosite:youtube": true, "domain:youtube.com": true, "domain:googlevideo.com": true,
	"domain:ytimg.com": true, "domain:youtu.be": true, "domain:ggpht.com": true,
}

// RussianBalancer is the provider's balancer through a Russian bridge.
const RussianBalancer = "BL-RU"

// russianSplit reads a rule to BL-RU: its Russian and other matchers, in
// order, of the field it matches by ("domain" or "ip"). ok is false for a
// rule that is not the Russian bridge's, names YouTube (the provider's
// ad-free path stays) or names no Russian destination. A rule matching by
// both a domain and an address list (ANDed) is not split — a split would
// change what it matches: wholly Russian it goes direct whole (field ""),
// mixed it keeps the bridge.
func russianSplit(r map[string]json.RawMessage) (field string, ru, other []string, ok bool) {
	var tag string
	if json.Unmarshal(r["balancerTag"], &tag) != nil || tag != RussianBalancer {
		return "", nil, nil, false
	}
	var fields []string
	for _, k := range []string{"domain", "ip"} {
		var list []string
		if len(r[k]) == 0 || json.Unmarshal(r[k], &list) != nil {
			continue
		}
		fields = append(fields, k)
		for _, m := range list {
			if youtubeMarkers[m] {
				return "", nil, nil, false
			}
			if russianMatcher(k, m) {
				ru = append(ru, m)
			} else {
				other = append(other, m)
			}
		}
	}
	switch {
	case len(ru) == 0:
		return "", nil, nil, false
	case len(fields) == 1:
		return fields[0], ru, other, true
	case len(other) == 0:
		return "", nil, nil, true
	}
	return "", nil, nil, false
}

// rewriteRussianRules sends the Russian destinations of every BL-RU rule of a
// routing object to directTag, in place, and says how many rules it changed.
// A rule naming other sites too is split: those keep the bridge, first — a
// keyword of theirs wins over a Russian TLD — and the Russian ones follow,
// direct. The other rules keep their bytes.
func rewriteRussianRules(routing json.RawMessage, directTag string) (json.RawMessage, int, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(routing, &obj); err != nil {
		return nil, 0, fmt.Errorf("xray splice: routing is not an object: %w", err)
	}
	var rules []json.RawMessage
	if len(obj["rules"]) == 0 || json.Unmarshal(obj["rules"], &rules) != nil {
		return routing, 0, nil
	}
	n := 0
	split := make([]json.RawMessage, 0, len(rules)+1)
	for _, raw := range rules {
		var r map[string]json.RawMessage
		if json.Unmarshal(raw, &r) != nil {
			split = append(split, raw)
			continue
		}
		field, ru, other, ok := russianSplit(r)
		if !ok {
			split = append(split, raw)
			continue
		}
		if len(other) > 0 {
			bridge := make(map[string]json.RawMessage, len(r))
			for k, v := range r {
				bridge[k] = v
			}
			bridge[field] = marshalNoEscape(other)
			b, err := json.Marshal(bridge)
			if err != nil {
				return nil, 0, err
			}
			split = append(split, b)
		}
		direct := make(map[string]json.RawMessage, len(r))
		for k, v := range r {
			direct[k] = v
		}
		delete(direct, "balancerTag")
		direct["outboundTag"] = marshalNoEscape(directTag)
		if field != "" {
			direct[field] = marshalNoEscape(ru)
		}
		b, err := json.Marshal(direct)
		if err != nil {
			return nil, 0, err
		}
		split = append(split, b)
		n++
	}
	rules = split
	if n == 0 {
		return routing, 0, nil
	}
	rb, err := json.Marshal(rules)
	if err != nil {
		return nil, 0, err
	}
	out, _, err := rewriteObjectField(routing, "rules", rb)
	if err != nil {
		return nil, 0, fmt.Errorf("xray splice: routing: %w", err)
	}
	return out, n, nil
}

// directOutboundTag is the tag of the document's plain freedom outbound, ""
// when it has none (the Russian rules then stay the provider's).
func directOutboundTag(providerRaw []byte) string {
	var doc struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if json.Unmarshal(providerRaw, &doc) != nil {
		return ""
	}
	for _, raw := range doc.Outbounds {
		if o := readOutbound(raw); o.plainDirect() {
			return o.Tag
		}
	}
	return ""
}

// HasRussianBalancer reports whether a document routes through the provider's
// Russian bridge at all — only then is RussiaDirect worth a new splice key.
func HasRussianBalancer(providerRaw []byte) bool {
	var doc struct {
		Routing struct {
			Balancers []struct {
				Tag string `json:"tag"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if json.Unmarshal(providerRaw, &doc) != nil {
		return false
	}
	for _, b := range doc.Routing.Balancers {
		if b.Tag == RussianBalancer {
			return true
		}
	}
	return false
}
