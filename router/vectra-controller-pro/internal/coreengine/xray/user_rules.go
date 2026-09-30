package xray

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/sites"
)

// The router owner's own sites ("My sites") are the THIRD deliberate change
// to the provider document, after the inbound swap and the egress mark: a few
// routing rules at the very top of routing.rules, and — only when the
// provider has no plain freedom outbound — one freedom outbound appended.
// The provider's own rules and outbounds are re-emitted byte for byte.
//
// Placement: xray takes the FIRST rule that matches, so the owner's sites
// win over every provider rule. They sit right after the router's own
// internal rules — of which there are none today: the API and metrics are
// served on their own "listen" and need no routing rule.
//
// Every rule is restricted to the tproxy inbound (inboundTag), i.e. to the
// LAN's traffic. The provider chains its balancers through loopback
// outbounds (BL-MAIN falls back to "stage-main-backup", which re-enters
// routing as inbound STAGE_MAIN_BACKUP). A rule for everyone would catch
// that re-entered connection again before the provider's inboundTag rule
// sees it: a proxy site whose balancer has fallen back would loop between
// BL-MAIN and its own fallback without end.

// RoutingKey is the top-level key the owner's sites are rendered into.
const RoutingKey = "routing"

// DirectTag is the freedom outbound the splice appends when the provider
// document has no plain freedom to send the owner's direct sites through.
// Like APITag and MetricsTag no provider selector — a PREFIX match over
// outbound tags — picks it up, and here the splice checks: a balancer that
// selected it would send its traffic around the VPN.
const DirectTag = "vctl-direct"

// UserRules are the owner's own sites (internal/sites): Direct always without
// the VPN, Proxy always through it, in the canonical form the router keeps.
// The splice parses every entry again, so an overrides file edited by hand
// cannot put anything but sites into the render.
type UserRules struct {
	Direct []string
	Proxy  []string
}

func (r UserRules) empty() bool { return len(r.Direct) == 0 && len(r.Proxy) == 0 }

// key fingerprints the lists for SpliceOptions.Key. "v1" names the rendering:
// a change to how sites become rules changes it, and every router re-renders.
func (r UserRules) key() string {
	h := sha256.New()
	for _, list := range [][]string{r.Direct, r.Proxy} {
		for _, s := range list {
			h.Write([]byte(s))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
	}
	return "v1:" + hex.EncodeToString(h.Sum(nil))[:16]
}

// UserRulesResult is what the owner's sites became in one render.
type UserRulesResult struct {
	// Rules is how many routing rules went to the top of routing.rules.
	Rules int
	// Direct and Proxy count the sites rendered. Dropped counts entries that
	// are not sites, repeat another, or are past sites.Max — only an overrides
	// file edited by hand has any.
	Direct, Proxy, Dropped int
	// DirectVia is the freedom outbound direct sites leave through;
	// DirectAdded when the splice appended it (DirectTag).
	DirectVia   string
	DirectAdded bool
	// ProxyVia is where proxy sites are sent — the target of the provider's
	// catch-all rule — a balancer when ProxyViaBalancer.
	ProxyVia         string
	ProxyViaBalancer bool
	// Skipped says why a whole list was not rendered: the document has
	// nowhere safe to send it. A real provider document has none.
	Skipped []string
	// SniffingChanged: the tproxy inbound sniffs differently from the
	// operator's config, so the rules can match (sniffingForRules).
	SniffingChanged bool
}

// Describe is the result in one line, for the apply's operation log.
func (r UserRulesResult) Describe() string {
	s := fmt.Sprintf("the router owner's sites: %d routing rule(s) first", r.Rules)
	if r.Direct > 0 {
		via := r.DirectVia
		if r.DirectAdded {
			via += " (added)"
		}
		s += fmt.Sprintf("; %d direct via %s", r.Direct, via)
	}
	if r.Proxy > 0 {
		kind := "outbound"
		if r.ProxyViaBalancer {
			kind = "balancer"
		}
		s += fmt.Sprintf("; %d through the VPN via %s %s", r.Proxy, kind, r.ProxyVia)
	}
	if r.SniffingChanged {
		s += "; tproxy sniffing http,tls,quic with routeOnly, for them"
	}
	if r.Dropped > 0 {
		s += fmt.Sprintf("; %d entries dropped (not a site, a repeat, or past %d)", r.Dropped, sites.Max)
	}
	for _, k := range r.Skipped {
		s += "; not rendered: " + k
	}
	return s
}

// rulesPlan is what the owner's sites become in one provider document.
type rulesPlan struct {
	rules     []json.RawMessage // for the top of routing.rules, in order
	addDirect bool              // append the DirectTag freedom outbound
	res       UserRulesResult
}

// providerOutbound is what the plan reads of one outbound.
type providerOutbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Settings *struct {
		Redirect      string          `json:"redirect"`
		ProxyProtocol json.RawMessage `json:"proxyProtocol"`
	} `json:"settings"`
	ProxySettings *struct {
		Tag string `json:"tag"`
	} `json:"proxySettings"`
	StreamSettings *struct {
		Sockopt *struct {
			DialerProxy string `json:"dialerProxy"`
		} `json:"sockopt"`
	} `json:"streamSettings"`
	// odd: a field the plan reads has a shape it did not expect; such an
	// outbound is never used as the direct way out.
	odd bool
}

func readOutbound(raw json.RawMessage) providerOutbound {
	var o providerOutbound
	if json.Unmarshal(raw, &o) == nil {
		return o
	}
	var bare struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	}
	_ = json.Unmarshal(raw, &bare)
	return providerOutbound{Tag: bare.Tag, Protocol: bare.Protocol, odd: true}
}

// xray looks protocols up in lowercase.
func (o providerOutbound) protocol() string { return strings.ToLower(o.Protocol) }

// plainDirect: a freedom that sends a connection straight where it was going
// — not redirected to a fixed address, not chained through another outbound,
// not prefixed with a PROXY protocol header no website expects.
func (o providerOutbound) plainDirect() bool {
	switch {
	case o.protocol() != "freedom", o.Tag == "", o.odd:
		return false
	case o.Settings != nil && (o.Settings.Redirect != "" || !emptyJSON(o.Settings.ProxyProtocol)):
		return false
	case o.ProxySettings != nil && o.ProxySettings.Tag != "":
		return false
	case o.StreamSettings != nil && o.StreamSettings.Sockopt != nil && o.StreamSettings.Sockopt.DialerProxy != "":
		return false
	}
	return true
}

// dials: an outbound that carries traffic to a server — a node.
func (o providerOutbound) dials() bool {
	switch o.protocol() {
	case "freedom", "blackhole", "loopback", "dns", "":
		return false
	}
	return o.Tag != ""
}

// planUserRules resolves where the owner's sites go in this document and
// renders the rules. It never refuses a document for them: a list with
// nowhere safe to go is left out (res.Skipped), so a router's own choices
// can never keep the provider's next config from being installed.
func planUserRules(providerRaw []byte, r UserRules, inboundTag string) (rulesPlan, error) {
	var plan rulesPlan
	direct, dropped := parseSites(r.Direct, nil)
	proxy, droppedProxy := parseSites(r.Proxy, direct) // a site in both goes direct
	plan.res.Dropped = dropped + droppedProxy

	var top map[string]json.RawMessage
	var doc struct {
		Outbounds []json.RawMessage `json:"outbounds"`
		Routing   *struct {
			Rules     []map[string]json.RawMessage `json:"rules"`
			Balancers []struct {
				Tag      string   `json:"tag"`
				Selector []string `json:"selector"`
			} `json:"balancers"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(providerRaw, &top); err != nil {
		return plan, fmt.Errorf("xray splice: read the document for the owner's sites: %w", err)
	}
	if err := json.Unmarshal(providerRaw, &doc); err != nil {
		return plan, fmt.Errorf("xray splice: read the document for the owner's sites: %w", err)
	}
	obs := make([]providerOutbound, len(doc.Outbounds))
	for i, raw := range doc.Outbounds {
		obs[i] = readOutbound(raw)
	}
	var rules []map[string]json.RawMessage
	var selectors []string
	balancers := map[string]bool{}
	if doc.Routing != nil {
		rules = doc.Routing.Rules
		for _, b := range doc.Routing.Balancers {
			balancers[b.Tag] = true
			selectors = append(selectors, b.Selector...)
		}
	}

	if len(direct) > 0 {
		for _, o := range obs {
			if o.plainDirect() {
				plan.res.DirectVia = o.Tag
				break
			}
		}
		if plan.res.DirectVia == "" {
			if why := cannotAddDirect(top, obs, selectors); why != "" {
				plan.res.Skipped = append(plan.res.Skipped, fmt.Sprintf("%d direct site(s): %s", len(direct), why))
				direct = nil
			} else {
				plan.addDirect, plan.res.DirectVia, plan.res.DirectAdded = true, DirectTag, true
			}
		}
	}
	if len(proxy) > 0 {
		tag, balancer, ok := proxyTarget(rules, balancers, obs)
		if !ok {
			plan.res.Skipped = append(plan.res.Skipped, fmt.Sprintf("%d proxy site(s): the document has no node to send them to", len(proxy)))
			proxy = nil
		} else {
			plan.res.ProxyVia, plan.res.ProxyViaBalancer = tag, balancer
		}
	}
	plan.res.Direct, plan.res.Proxy = len(direct), len(proxy)
	plan.rules = renderRules(direct, proxy, inboundTag, plan.res)
	plan.res.Rules = len(plan.rules)
	return plan, nil
}

// parseSites parses a list as the splice renders it: every entry through
// sites.Parse, each site once, none already in exclude, at most sites.Max.
func parseSites(entries []string, exclude []sites.Site) (out []sites.Site, dropped int) {
	seen := map[string]bool{}
	for _, s := range exclude {
		seen[s.Display] = true
	}
	for _, e := range entries {
		s, err := sites.Parse(e)
		if err != nil || seen[s.Display] || len(out) >= sites.Max {
			dropped++
			continue
		}
		seen[s.Display] = true
		out = append(out, s)
	}
	return out, dropped
}

// cannotAddDirect says why DirectTag cannot be appended safely, or "".
func cannotAddDirect(top map[string]json.RawMessage, obs []providerOutbound, selectors []string) string {
	if _, ok := top[OutboundsKey]; !ok || len(obs) == 0 {
		// Appended to nothing it would be the FIRST outbound — xray's default,
		// where everything no rule routes goes: all of it around the VPN.
		return "the document has no outbounds to add a direct one after"
	}
	for _, o := range obs {
		if o.Tag == DirectTag {
			return "an outbound is already tagged " + DirectTag
		}
	}
	for _, s := range selectors {
		if strings.HasPrefix(DirectTag, s) {
			return fmt.Sprintf("a balancer's selector %q would pick %s up", s, DirectTag)
		}
	}
	return ""
}

// proxyTarget is where the provider sends everything no other rule matched:
// the target of its first catch-all rule — when that leads to a node, a
// balancer, or a loopback into the provider's own routing. A catch-all to
// freedom or blackhole is not a way through the VPN; then, as with no
// catch-all at all, the first node.
func proxyTarget(rules []map[string]json.RawMessage, balancers map[string]bool, obs []providerOutbound) (tag string, balancer, ok bool) {
	for _, r := range rules {
		if !catchAll(r) {
			continue
		}
		if t := jsonString(ruleField(r, "outboundTag")); t != "" {
			for _, o := range obs {
				if o.Tag == t && (o.dials() || o.protocol() == "loopback") {
					return t, false, true
				}
			}
		} else if t := jsonString(ruleField(r, "balancerTag")); t != "" && balancers[t] {
			return t, true, true
		}
		break
	}
	for _, o := range obs {
		if o.dials() {
			return o.Tag, false, true
		}
	}
	return "", false, false
}

// catchAll: a rule with no condition but the network, and one that carries
// TCP — where xray sends everything no earlier rule matched.
func catchAll(r map[string]json.RawMessage) bool {
	for k, v := range r {
		switch strings.ToLower(k) {
		case "type", "outboundtag", "balancertag", "ruletag", "domainmatcher":
		case "network":
			if n := strings.ToLower(strings.Join(jsonStrings(v), ",")); n != "" && !strings.Contains(n, "tcp") {
				return false
			}
		default:
			if !emptyJSON(v) {
				return false
			}
		}
	}
	return true
}

// userRule is one rendered rule, keys in this order.
type userRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	OutboundTag string   `json:"outboundTag,omitempty"`
	BalancerTag string   `json:"balancerTag,omitempty"`
}

// renderRules turns the sites into rules: direct domains, direct addresses,
// proxy domains, proxy addresses — in that order, empty ones left out.
//
// A site in one list may lie inside a site of the other: "online.sberbank.ru"
// through the VPN, "sberbank.ru" direct. xray takes the first rule that
// matches, so the more specific site must come first, or it never applies.
// Each site gets a level — 0, or one more than the highest level of the
// other list's sites it covers — and the four rules are written per level,
// level 0 first. Without such an overlap everything is level 0: at most four
// rules.
func renderRules(direct, proxy []sites.Site, inboundTag string, res UserRulesResult) []json.RawMessage {
	type entry struct {
		site  sites.Site
		proxy bool
	}
	all := make([]entry, 0, len(direct)+len(proxy))
	for _, s := range direct {
		all = append(all, entry{s, false})
	}
	for _, s := range proxy {
		all = append(all, entry{s, true})
	}
	// A site covers only sites more specific than itself: in this order, the
	// levels a site depends on are final before it is looked at.
	order := make([]int, len(all))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return all[order[a]].site.Specificity() > all[order[b]].site.Specificity()
	})
	level := make([]int, len(all))
	top := 0
	for _, i := range order {
		for j := range all {
			if all[i].proxy != all[j].proxy && all[i].site.Covers(all[j].site) && level[j]+1 > level[i] {
				level[i] = level[j] + 1
			}
		}
		if level[i] > top {
			top = level[i]
		}
	}

	var out []json.RawMessage
	for l := 0; l <= top; l++ {
		for _, isProxy := range []bool{false, true} {
			for _, domains := range []bool{true, false} {
				var matchers []string
				for i, e := range all {
					if level[i] == l && e.proxy == isProxy && e.site.Domain == domains {
						matchers = append(matchers, e.site.Matcher())
					}
				}
				if len(matchers) == 0 {
					continue
				}
				r := userRule{Type: "field", InboundTag: []string{inboundTag}}
				if domains {
					r.Domain = matchers
				} else {
					r.IP = matchers
				}
				switch {
				case !isProxy:
					r.OutboundTag = res.DirectVia
				case res.ProxyViaBalancer:
					r.BalancerTag = res.ProxyVia
				default:
					r.OutboundTag = res.ProxyVia
				}
				out = append(out, marshalNoEscape(r))
			}
		}
	}
	return out
}

// sniffingForRules is the tproxy inbound's sniffing with what the owner's
// rules need, and whether that differs from the operator's.
//
// TPROXY hands xray bare addresses: a domain rule matches only a name
// sniffed from the connection — http (Host), tls (SNI), quic (HTTP/3's SNI).
// routeOnly routes on that name and still dials the address the client
// resolved. Without it xray REPLACES the destination by the name: the owner's
// IP and CIDR entries never match a sniffed connection, and freedom resolves
// the name again itself. metadataOnly would sniff no name at all. The
// operator's other settings (domainsExcluded) stay.
func sniffingForRules(s config.Sniffing) (config.Sniffing, bool) {
	out := s
	out.DestOverride = append([]string(nil), s.DestOverride...)
	changed := false
	if !out.Enabled {
		out.Enabled, changed = true, true
	}
	for _, p := range []string{"http", "tls", "quic"} {
		has := false
		for _, d := range out.DestOverride {
			has = has || d == p
		}
		if !has {
			out.DestOverride, changed = append(out.DestOverride, p), true
		}
	}
	if !out.RouteOnly {
		out.RouteOnly, changed = true, true
	}
	if out.MetadataOnly {
		out.MetadataOnly, changed = false, true
	}
	return out, changed
}

// directOutboundJSON is the freedom appended as DirectTag. It carries no
// sockopt: injectOutboundMark stamps it like every other dialling outbound,
// so its packets are not captured by TPROXY again.
func directOutboundJSON() []byte {
	return marshalNoEscape(struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	}{DirectTag, "freedom"})
}

// insertRules puts rules at the top of routing.rules, before the provider's
// own, which are re-emitted byte for byte. The key is found as xray finds it,
// case-insensitively (checkKeyFolding refused a document that spells it
// twice); without one, "rules" is added.
func insertRules(routing json.RawMessage, rules []json.RawMessage) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(routing, &obj); err != nil {
		return nil, fmt.Errorf("xray splice: routing is not an object: %w", err)
	}
	field := "rules"
	var existing []json.RawMessage
	for k, v := range obj {
		if foldKey(k) != foldKey("rules") {
			continue
		}
		field = k
		if !isJSONNull(v) {
			if err := json.Unmarshal(v, &existing); err != nil {
				return nil, fmt.Errorf("xray splice: routing.rules is not an array: %w", err)
			}
		}
	}
	var b bytes.Buffer
	b.WriteByte('[')
	for i, r := range append(append([]json.RawMessage(nil), rules...), existing...) {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(r)
	}
	b.WriteByte(']')
	out, _, err := rewriteObjectField(routing, field, b.Bytes())
	return out, err
}

// appendToArray adds elem at the end of a JSON array.
func appendToArray(arr json.RawMessage, elem []byte) (json.RawMessage, error) {
	body := bytes.TrimSpace(arr)
	if len(body) < 2 || body[0] != '[' || body[len(body)-1] != ']' {
		return nil, errors.New("xray splice: outbounds must be an array")
	}
	body = bytes.TrimSpace(body[:len(body)-1])
	out := append([]byte(nil), body...)
	if len(body) > 1 {
		out = append(out, ',')
	}
	out = append(out, elem...)
	return append(out, ']'), nil
}

// ruleField is r's value for name, matched as xray matches keys.
func ruleField(r map[string]json.RawMessage, name string) json.RawMessage {
	for k, v := range r {
		if foldKey(k) == foldKey(name) {
			return v
		}
	}
	return nil
}

func jsonString(raw json.RawMessage) string {
	var s string
	if len(raw) > 0 && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// jsonStrings reads a string or a list of strings, as xray's StringList does.
func jsonStrings(raw json.RawMessage) []string {
	var ss []string
	if len(raw) > 0 && json.Unmarshal(raw, &ss) == nil {
		return ss
	}
	if s := jsonString(raw); s != "" {
		return []string{s}
	}
	return nil
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// emptyJSON: absent, null, "", 0, false, [] or {} — what xray treats as not
// set.
func emptyJSON(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", `""`, "0", "false", "[]", "{}":
		return true
	}
	var v interface{}
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch x := v.(type) {
	case []interface{}:
		return len(x) == 0
	case map[string]interface{}:
		return len(x) == 0
	}
	return false
}

// marshalNoEscape encodes v without Go's HTML escaping, so provider tags are
// written as the provider wrote them.
func marshalNoEscape(v interface{}) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v) // these values always encode
	return bytes.TrimRight(b.Bytes(), "\n")
}
