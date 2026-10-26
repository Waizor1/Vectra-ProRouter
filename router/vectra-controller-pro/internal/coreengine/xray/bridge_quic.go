package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// QUIC through the Russian bridge (r31, SpliceOptions.QUICOverBridge).
//
// Since 2026-10-22 TLS over TCP 443 from the cloud.ru bridges to the Moscow
// Google caches hangs: Google takes the ClientHello and its answer never
// arrives. The bridges race it (several TCP connections at once), and 4–5 %
// of the races are still lost after 8 s; measured from 1111's LAN on
// 2026-10-26, 5 of 40 TLS handshakes to those caches took 0.6–5.1 s and one
// failed. QUIC to the same caches is not cut: through the bridges' gRPC
// entries the PassWall routers get an answer in ~0.08 s.
//
// The router could not send QUIC there. Every bridge outbound of the
// provider's document is VLESS with flow xtls-rprx-vision, and the CLIENT
// side of Vision rejects UDP/443 — so r24 refused it at once in the kernel
// (firewall.Spec.RefuseQUIC) rather than letting it hang. The client-side
// variant xtls-rprx-vision-udp443 is the same Vision without that check; the
// bridge sees the same flow either way. The splice gives it to the outbounds
// the Russian bridge's balancer (BL-RU) selects, and only to those: every
// other path is still plain Vision, and QUIC to its names is still refused.
//
// Which names may send QUIC is not read off the rules alone — an owner's site
// or a service's country may take a name before the provider's BL-RU rule.
// RenderBridgeQUIC gives the candidates; the daemon asks xray (TestRoute)
// where each one goes and lets through only those that land on a member.

const (
	// VisionFlow is the provider's flow on its bridge outbounds.
	VisionFlow = "xtls-rprx-vision"
	// VisionUDP443Flow is Vision without the client's UDP/443 rejection.
	VisionUDP443Flow = "xtls-rprx-vision-udp443"
)

// balancerSelector is the selector of the document's balancer tag, nil when
// it has none. xray picks a balancer's members by tag prefix.
func balancerSelector(doc []byte, tag string) []string {
	return jsonStrings(ruleField(balancerOf(doc, tag), "selector"))
}

// balancerOf is the document's balancer tag, nil when it has none.
func balancerOf(doc []byte, tag string) map[string]json.RawMessage {
	var d map[string]json.RawMessage
	if json.Unmarshal(doc, &d) != nil {
		return nil
	}
	var routing map[string]json.RawMessage
	if json.Unmarshal(ruleField(d, RoutingKey), &routing) != nil {
		return nil
	}
	var balancers []map[string]json.RawMessage
	if json.Unmarshal(ruleField(routing, "balancers"), &balancers) != nil {
		return nil
	}
	for _, b := range balancers {
		if jsonString(ruleField(b, "tag")) == tag {
			return b
		}
	}
	return nil
}

func selectedBy(tag string, selector []string) bool {
	for _, s := range selector {
		if s != "" && strings.HasPrefix(tag, s) {
			return true
		}
	}
	return false
}

// allowQUICOverBalancer gives VisionUDP443Flow to every VLESS outbound of the
// outbounds array that selector picks and that has plain Vision — in the
// flat settings ({"flow": …}) or in vnext's users — and says how many it
// changed. Every other outbound keeps its bytes.
func allowQUICOverBalancer(raw json.RawMessage, selector []string) (json.RawMessage, int, error) {
	if len(selector) == 0 {
		return raw, 0, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, 0, fmt.Errorf("xray splice: outbounds must be an array: %w", err)
	}
	changed := 0
	for i, ob := range arr {
		var probe map[string]json.RawMessage
		if json.Unmarshal(ob, &probe) != nil {
			continue
		}
		if !strings.EqualFold(jsonString(ruleField(probe, "protocol")), "vless") ||
			!selectedBy(jsonString(ruleField(probe, "tag")), selector) {
			continue
		}
		settings, ok := visionToUDP443(ruleField(probe, "settings"))
		if !ok {
			continue
		}
		rewritten, _, err := rewriteObjectField(ob, settingsKey(probe), settings)
		if err != nil {
			return nil, 0, fmt.Errorf("xray splice: outbound[%d]: %w", i, err)
		}
		arr[i] = rewritten
		changed++
	}
	if changed == 0 {
		return raw, 0, nil
	}
	// Joined by hand, as injectOutboundMark does: json.Marshal would compact
	// and HTML-escape the other outbounds' bytes.
	var out bytes.Buffer
	out.WriteByte('[')
	for i, ob := range arr {
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(ob)
	}
	out.WriteByte(']')
	return out.Bytes(), changed, nil
}

// settingsKey is the outbound's own spelling of "settings".
func settingsKey(ob map[string]json.RawMessage) string {
	for k := range ob {
		if foldKey(k) == foldKey("settings") {
			return k
		}
	}
	return "settings"
}

// visionToUDP443 rewrites plain Vision in a VLESS outbound's settings; ok is
// false when there was none.
func visionToUDP443(raw json.RawMessage) (json.RawMessage, bool) {
	var s map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return nil, false
	}
	changed := setVisionUDP443(s)
	if vk := foldedKey(s, "vnext"); vk != "" {
		var servers []map[string]json.RawMessage
		if json.Unmarshal(s[vk], &servers) == nil {
			serversChanged := false
			for _, srv := range servers {
				uk := foldedKey(srv, "users")
				if uk == "" {
					continue
				}
				var users []map[string]json.RawMessage
				if json.Unmarshal(srv[uk], &users) != nil {
					continue
				}
				usersChanged := false
				for _, u := range users {
					if setVisionUDP443(u) {
						usersChanged = true
					}
				}
				if usersChanged {
					srv[uk] = mustMarshal(users)
					serversChanged = true
				}
			}
			if serversChanged {
				s[vk] = mustMarshal(servers)
				changed = true
			}
		}
	}
	if !changed {
		return nil, false
	}
	return mustMarshal(s), true
}

// setVisionUDP443 turns the object's plain Vision flow into the udp443 one.
func setVisionUDP443(obj map[string]json.RawMessage) bool {
	k := foldedKey(obj, "flow")
	if k == "" || jsonString(obj[k]) != VisionFlow {
		return false
	}
	obj[k] = json.RawMessage(`"` + VisionUDP443Flow + `"`)
	return true
}

func foldedKey(obj map[string]json.RawMessage, name string) string {
	for k := range obj {
		if foldKey(k) == foldKey(name) {
			return k
		}
	}
	return ""
}

// BridgeQUIC is what a render offers QUIC through the Russian bridge.
type BridgeQUIC struct {
	// Names are the names the bridge's rules route by name (domain: and
	// full: matchers of the rules to BL-RU that can match UDP): the
	// candidates — YouTube's video (googlevideo.com, ytimg.com, ggpht.com)
	// and the foreign sites the provider bridges. Keywords, regexps,
	// geosites and whole top-level domains are not names a resolver can be
	// told about.
	Names []string
	// Members are the bridge's outbounds that carry UDP/443: selected by
	// BL-RU and not plain Vision.
	Members []string
	// Fallback is BL-RU's fallbackTag: where xray sends the names while it
	// knows no member alive (a fresh start before the observatory's first
	// round, a bridge down).
	Fallback string
}

// RenderBridgeQUIC reads a render for BridgeQUIC; zero when the document has
// no Russian bridge or no member of it carries UDP/443.
func RenderBridgeQUIC(render []byte) BridgeQUIC {
	bl := balancerOf(render, RussianBalancer)
	selector := jsonStrings(ruleField(bl, "selector"))
	if len(selector) == 0 {
		return BridgeQUIC{}
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(render, &doc) != nil {
		return BridgeQUIC{}
	}
	out := BridgeQUIC{Fallback: jsonString(ruleField(bl, "fallbackTag"))}
	var outbounds []map[string]json.RawMessage
	_ = json.Unmarshal(ruleField(doc, OutboundsKey), &outbounds)
	for _, ob := range outbounds {
		tag := jsonString(ruleField(ob, "tag"))
		if !selectedBy(tag, selector) || !carriesUDP443(ob) {
			continue
		}
		out.Members = append(out.Members, tag)
	}
	if len(out.Members) == 0 {
		return BridgeQUIC{}
	}
	var routing map[string]json.RawMessage
	_ = json.Unmarshal(ruleField(doc, RoutingKey), &routing)
	var rules []map[string]json.RawMessage
	_ = json.Unmarshal(ruleField(routing, "rules"), &rules)
	seen := map[string]bool{}
	for _, r := range rules {
		if jsonString(ruleField(r, "balancerTag")) != RussianBalancer || !matchesUDP(r) {
			continue
		}
		for _, m := range jsonStrings(ruleField(r, "domain")) {
			name := ""
			switch {
			case strings.HasPrefix(m, "domain:"):
				name = strings.TrimPrefix(m, "domain:")
			case strings.HasPrefix(m, "full:"):
				name = strings.TrimPrefix(m, "full:")
			}
			name = strings.ToLower(strings.TrimSpace(name))
			if seen[name] || !resolverName(name) {
				continue
			}
			seen[name] = true
			out.Names = append(out.Names, name)
		}
	}
	sort.Strings(out.Names)
	if len(out.Names) == 0 {
		return BridgeQUIC{}
	}
	return out
}

// resolverName: a name to hand dnsmasq — letters, digits, hyphens and inner
// dots only, and not a whole top-level domain ("domain:ru"), under which
// dnsmasq would put every answer into the set.
func resolverName(n string) bool {
	if len(n) > 253 || !strings.Contains(n, ".") || strings.HasPrefix(n, ".") || strings.HasSuffix(n, ".") || strings.Contains(n, "..") {
		return false
	}
	for _, c := range n {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

// carriesUDP443: an outbound that does not reject UDP/443 itself. The client
// side of plain Vision does, and so does mux, unless its xudpProxyUDP443 says
// otherwise ("reject" by default: the PassWall routers' YouTube, 2026-10-26).
func carriesUDP443(ob map[string]json.RawMessage) bool {
	var mux map[string]json.RawMessage
	if json.Unmarshal(ruleField(ob, "mux"), &mux) == nil && string(mux[foldedKey(mux, "enabled")]) == "true" {
		switch strings.ToLower(jsonString(mux[foldedKey(mux, "xudpProxyUDP443")])) {
		case "allow", "skip":
		default:
			return false
		}
	}
	var s map[string]json.RawMessage
	if json.Unmarshal(ruleField(ob, "settings"), &s) != nil {
		return true
	}
	if jsonString(s[foldedKey(s, "flow")]) == VisionFlow {
		return false
	}
	var servers []map[string]json.RawMessage
	if json.Unmarshal(s[foldedKey(s, "vnext")], &servers) == nil {
		for _, srv := range servers {
			var users []map[string]json.RawMessage
			if json.Unmarshal(srv[foldedKey(srv, "users")], &users) != nil {
				continue
			}
			for _, u := range users {
				if jsonString(u[foldedKey(u, "flow")]) == VisionFlow {
					return false
				}
			}
		}
	}
	return true
}

// matchesUDP: the rule's network, if it names one, includes udp.
func matchesUDP(r map[string]json.RawMessage) bool {
	n := strings.ToLower(jsonString(ruleField(r, "network")))
	return n == "" || strings.Contains(n, "udp")
}
