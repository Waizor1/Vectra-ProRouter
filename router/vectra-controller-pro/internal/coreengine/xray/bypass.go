package xray

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// Direct destinations, routed by the kernel.
//
// Everything the transparent proxy catches goes into xray, and every
// connection xray carries costs it ~38 KB of heap (measured on the data-plane
// stand: 1000 connections, 47 MB; 2000, 82 MB) plus two kernel sockets where
// plain forwarding needs none. Most of a Russian home's connections are to
// Russian sites the routing sends straight out anyway — through xray, for
// nothing. PassWall2, which the fleet ran for months, never let them in: its
// nft rules return the addresses of its "direct" shunt rule (the panel's
// geoip:DIRECT, ~35k prefixes) before the TPROXY rule. DirectBypass finds the
// same addresses in a render, so the data plane can do the same.
//
// It takes the leading rules only. xray's routing is first-match, so an
// address a direct rule lists is direct only if no earlier rule could have
// claimed the connection for something else. The scan stops at the first
// rule that applies to the transparent proxy's traffic and does not send it
// straight out; the IP lists of the direct rules before it are what xray
// sends straight out by address. A direct rule that also looks at something
// the kernel cannot see (a domain, a port, a protocol) contributes nothing and
// does not stop the scan: connections it does not match fall through to the
// next rule, in the kernel as in xray.
//
// One difference, PassWall2's own: with domainStrategy IPOnDemand (the
// fleet's) and sniffing, xray matches IP rules against ITS resolution of the
// sniffed name, not the address the client dialled. A client that resolves a
// listed service itself (its own DoH or DoT, a hard-coded resolver) and gets
// an address inside a direct list — a CDN node in a Russian network — is sent
// straight out by the kernel, where xray would have re-resolved the name and
// proxied it. Clients that ask the router get FakeDNS addresses for the
// listed services and are unaffected. PassWall2's nft rules return the direct
// shunt's addresses before its TPROXY the same way.

// DirectSource is what a render sends straight out by address, before any
// other rule: literal prefixes and geoip categories, still to be read.
type DirectSource struct {
	Prefixes []netip.Prefix
	GeoIP    []GeoRef
	// ExcludePrefixes and ExcludeGeoIP are what earlier rules send elsewhere
	// by address: never direct, whatever the direct lists say.
	ExcludePrefixes []netip.Prefix
	ExcludeGeoIP    []GeoRef
	// Rules is how many rules contributed; Stop the index of the rule the
	// scan stopped at (-1: the end), StopReason why.
	Rules      int
	Stop       int
	StopReason string
}

// GeoRef names a geoip category: File is "geoip.dat", or the file of an
// "ext:file:code" reference.
type GeoRef struct {
	File string
	Code string
}

// Empty reports whether there is nothing to route in the kernel.
func (s DirectSource) Empty() bool { return len(s.Prefixes) == 0 && len(s.GeoIP) == 0 }

// Key identifies the source, for "is this what is loaded".
func (s DirectSource) Key() string {
	var b strings.Builder
	for _, g := range s.GeoIP {
		b.WriteString(g.File + ":" + g.Code + ",")
	}
	for _, p := range s.Prefixes {
		b.WriteString(p.String() + ",")
	}
	for _, g := range s.ExcludeGeoIP {
		b.WriteString("-" + g.File + ":" + g.Code + ",")
	}
	for _, p := range s.ExcludePrefixes {
		b.WriteString("-" + p.String() + ",")
	}
	return b.String()
}

// DirectBypass reads a render's direct destinations (see above) for the
// traffic of the inbound tagged tproxyTag.
func DirectBypass(render []byte, tproxyTag string) (DirectSource, error) {
	src := DirectSource{Stop: -1}
	var doc struct {
		Outbounds []struct {
			Tag            string                     `json:"tag"`
			Protocol       string                     `json:"protocol"`
			Settings       map[string]json.RawMessage `json:"settings"`
			StreamSettings map[string]json.RawMessage `json:"streamSettings"`
			ProxySettings  json.RawMessage            `json:"proxySettings"`
		} `json:"outbounds"`
		Routing struct {
			Rules []map[string]json.RawMessage `json:"rules"`
		} `json:"routing"`
		DNS json.RawMessage `json:"dns"`
	}
	if err := json.Unmarshal(render, &doc); err != nil {
		return src, fmt.Errorf("direct bypass: render: %w", err)
	}
	// With the names the rules proxy answered by FakeDNS (the splice's
	// FakeDNS), a rule that proxies by name claims no real address: its
	// connections come to fake ones.
	fakeNames := renderHasFakeDNSServer(doc.DNS)
	direct := map[string]bool{}
	for _, ob := range doc.Outbounds {
		if ob.Protocol == "freedom" && plainFreedom(ob.Settings, ob.StreamSettings, ob.ProxySettings) {
			direct[ob.Tag] = true
		}
	}
	for i, r := range doc.Routing.Rules {
		if !appliesTo(r, tproxyTag) {
			continue
		}
		out := jsonString(r["outboundTag"])
		if _, bal := r["balancerTag"]; bal || !direct[out] {
			if fakeNames && len(jsonStrings(ruleField(r, "domain"))) > 0 {
				continue
			}
			if fakeNames && excludeAddresses(&src, r) {
				continue
			}
			src.Stop, src.StopReason = i, fmt.Sprintf("rule %d sends to %q", i, firstNonEmpty(out, jsonString(r["balancerTag"])))
			return src, nil
		}
		ips, ok := addressOnly(r)
		if !ok {
			continue // direct on something the kernel cannot see: no claim either way
		}
		if len(ips) == 0 {
			// Direct, and nothing else to match: everything after is dead.
			src.Stop, src.StopReason = i, fmt.Sprintf("rule %d sends everything else straight out", i)
			return src, nil
		}
		var pfx []netip.Prefix
		var geo []GeoRef
		usable := true
		for _, s := range ips {
			p, g, ok := parseIPEntry(s)
			if !ok {
				usable = false // a negation, or a form the kernel cannot hold
				break
			}
			if g != nil {
				if strings.EqualFold(g.Code, "private") {
					continue // the data plane's bypass holds the private ranges already
				}
				geo = append(geo, *g)
			} else {
				pfx = append(pfx, p)
			}
		}
		if !usable {
			continue
		}
		src.Prefixes = append(src.Prefixes, pfx...)
		src.GeoIP = append(src.GeoIP, geo...)
		src.Rules++
	}
	return src, nil
}

// excludeAddresses takes a rule that sends somewhere else by address into the
// source's exclusions; false when it has no addresses the kernel can hold.
func excludeAddresses(src *DirectSource, r map[string]json.RawMessage) bool {
	ips := jsonStrings(ruleField(r, "ip"))
	if len(ips) == 0 {
		return false
	}
	var pfx []netip.Prefix
	var geo []GeoRef
	for _, s := range ips {
		p, g, ok := parseIPEntry(s)
		if !ok {
			return false
		}
		if g != nil {
			geo = append(geo, *g)
		} else {
			pfx = append(pfx, p)
		}
	}
	src.ExcludePrefixes = append(src.ExcludePrefixes, pfx...)
	src.ExcludeGeoIP = append(src.ExcludeGeoIP, geo...)
	return true
}

// plainFreedom reports whether a freedom outbound sends a connection out as
// the kernel would: to its own destination, over the default route, with
// nothing done to it. A redirect, fragments, noise, a dialer proxy or an
// interface binding is xray's doing, and such an outbound is not "direct" for
// the kernel's purposes.
func plainFreedom(settings, stream map[string]json.RawMessage, proxy json.RawMessage) bool {
	for k, v := range settings {
		switch k {
		case "domainStrategy", "userLevel":
		case "finalRules":
			// PassWall2 26.x writes [{"action":"allow"}]: allowing is what
			// the kernel does. A rule that blocks is not.
			var rules []struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(v, &rules) != nil {
				return false
			}
			for _, r := range rules {
				if r.Action != "allow" {
					return false
				}
			}
		default:
			return false
		}
	}
	if len(proxy) > 0 && string(proxy) != "null" && string(proxy) != "{}" {
		return false
	}
	for k, v := range stream {
		if k != "sockopt" {
			return false
		}
		var so map[string]json.RawMessage
		if json.Unmarshal(v, &so) != nil {
			return false
		}
		for sk := range so {
			switch sk {
			case "mark", "tcpFastOpen", "tcpKeepAliveIdle", "tcpKeepAliveInterval", "tcpNoDelay", "domainStrategy":
			default:
				return false
			}
		}
	}
	return true
}

// appliesTo reports whether a rule can match the transparent proxy's traffic.
func appliesTo(r map[string]json.RawMessage, tproxyTag string) bool {
	raw, ok := r["inboundTag"]
	if !ok {
		return true
	}
	var tags []string
	if json.Unmarshal(raw, &tags) != nil {
		var one string
		if json.Unmarshal(raw, &one) != nil {
			return true // unreadable: assume it may apply
		}
		tags = []string{one}
	}
	for _, t := range tags {
		if t == tproxyTag {
			return true
		}
	}
	return false
}

// addressOnly returns a rule's destination IP list when that list is all it
// matches on (besides the inbound, a tag, and a network covering both tcp
// and udp); ok false otherwise.
func addressOnly(r map[string]json.RawMessage) ([]string, bool) {
	var ips []string
	for k, v := range r {
		switch k {
		case "type", "outboundTag", "inboundTag", "ruleTag":
		case "network":
			if !bothNetworks(v) {
				return nil, false
			}
		case "ip":
			if json.Unmarshal(v, &ips) != nil {
				return nil, false
			}
		default:
			return nil, false
		}
	}
	return ips, true
}

func bothNetworks(v json.RawMessage) bool {
	var s string
	if json.Unmarshal(v, &s) != nil {
		var list []string
		if json.Unmarshal(v, &list) != nil {
			return false
		}
		s = strings.Join(list, ",")
	}
	tcp, udp := false, false
	for _, n := range strings.Split(s, ",") {
		switch strings.TrimSpace(n) {
		case "tcp":
			tcp = true
		case "udp":
			udp = true
		}
	}
	return tcp && udp
}

// parseIPEntry reads one entry of a rule's ip list: an address, a prefix, a
// geoip category or an ext:file:code reference. ok false for what the kernel
// cannot hold (a negated category, an unknown form).
func parseIPEntry(s string) (netip.Prefix, *GeoRef, bool) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "geoip:"):
		code := strings.TrimPrefix(s, "geoip:")
		if code == "" || strings.HasPrefix(code, "!") {
			return netip.Prefix{}, nil, false
		}
		return netip.Prefix{}, &GeoRef{File: "geoip.dat", Code: code}, true
	case strings.HasPrefix(s, "ext:"):
		parts := strings.Split(strings.TrimPrefix(s, "ext:"), ":")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[1], "!") || strings.ContainsAny(parts[0], "/\\") {
			return netip.Prefix{}, nil, false
		}
		return netip.Prefix{}, &GeoRef{File: parts[0], Code: parts[1]}, true
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil, true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()), nil, true
	}
	return netip.Prefix{}, nil, false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
