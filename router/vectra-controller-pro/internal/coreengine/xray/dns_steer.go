package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// DNS through the tunnel.
//
// Measured on the test router (2026-09-29): the ISP's filter answers NXDOMAIN
// for www.instagram.com and www.youtube.com, and not only from its own
// resolver. A query to 8.8.8.8 or 1.1.1.1 over the open path gets a forged
// NXDOMAIN too, injected in flight and racing the real answer. Whatever the
// tunnel carries, a LAN whose resolver asks over the open path cannot open
// those sites at all: the name never becomes an address.
//
// So the router's resolver asks through xray. dnsmasq keeps serving the LAN
// (local names, DHCP hosts, its cache); only its upstream queries are
// redirected, by the nft table (internal/firewall, Spec.DNSRedirectPort), to a
// loopback DNS inbound. The inbound hands every query to xray's built-in DNS:
// the provider's own "dns" section, whose servers are reached through the
// provider's routing like any other connection, i.e. through the tunnel. That
// is how the provider's phone apps resolve, too.
//
// Two kinds of names must NOT go through the tunnel:
//   - the nodes' own host names. xray resolves them through the system
//     resolver, which is dnsmasq, which is redirected back here: asked through
//     the tunnel, the tunnel would need itself to come up;
//   - the control plane's (the panel, the subscription). vctl must reach its
//     panel with every node dead.
//
// Those go first in the built-in DNS, answered by "tcp+local" servers: xray
// dials them itself, around its routing, so no rule and no outbound of the
// provider's is involved, and nothing the provider routes can change it.

const (
	// DNSInboundTag is the loopback inbound dnsmasq's redirected queries land on.
	DNSInboundTag = "vctl-dns-in"
	// DNSOutboundTag hands a query to xray's built-in DNS. With no settings
	// but its level, every xray from 26.3.27 answers A/AAAA from the built-in
	// DNS and every other type at once (an empty answer or a refusal), never
	// over the open path.
	DNSOutboundTag = "vctl-dns-out"
	// DefaultDNSListen is the inbound's address. Nothing else on an OpenWrt
	// router uses it; PassWall2 (stopped while vctl carries) uses 15353 and
	// 15354.
	DefaultDNSListen = "127.0.0.1:10053"
	// dnsInboundTarget is where the inbound says a query was going. The DNS
	// outbound answers A/AAAA itself and refuses the rest, so it is never
	// dialled; it only has to be a valid address.
	dnsInboundTarget = "1.1.1.1"
	// addedGeneralServer is the one server a document without a "dns" of its
	// own gets beside the direct ones. It is reached through the provider's
	// routing, like its connections.
	addedGeneralServer = "1.1.1.1"
)

// DefaultDirectResolvers answer the nodes' and the control plane's names, over
// TCP from the router itself. Two operators, so one being unreachable is not
// the end of the tunnel.
var DefaultDirectResolvers = []string{"8.8.8.8", "77.88.8.8"}

// DNSOptions switch DNS through the tunnel on. The zero value (nil in
// SpliceOptions) leaves the provider's "dns" and routing exactly as they were.
type DNSOptions struct {
	// Listen is the DNS inbound's loopback address:port.
	Listen string
	// DirectResolvers are plain IP addresses; each becomes a "tcp+local"
	// server for the direct names.
	DirectResolvers []string
	// DirectDomains are xray domain matchers ("full:api.example.com",
	// "domain:example.com") resolved directly besides the nodes' own names,
	// which the splice finds itself.
	DirectDomains []string
	// AllowFakeDNS lets a document whose DNS hands out FakeDNS addresses be
	// steered: only when the data plane carries those addresses into xray
	// instead of bypassing them (RenderFakeDNSPools, PassWall-compatible
	// routing). Without it such a document is left on the open path.
	AllowFakeDNS bool
	// IPv4Only makes the resolver answer IPv4 addresses only (queryStrategy
	// UseIPv4), whatever the document asks: the router refuses the LAN's
	// IPv6 (the nodes carry none), so no device should try it first.
	IPv4Only bool
}

func (o *DNSOptions) enabled() bool { return o != nil && o.Listen != "" }

func (o *DNSOptions) key() string {
	if !o.enabled() {
		return ""
	}
	k := ";dns=" + o.Listen + "|" + strings.Join(o.DirectResolvers, ",") + "|" + strings.Join(o.DirectDomains, ",")
	// The DNS sessions' level: a render made before it (or with another) is
	// made again at start (reconcileRender), not at the provider's next change.
	k += "|lvl" + strconv.Itoa(dnsLevelConnIdle)
	if o.AllowFakeDNS {
		k += "|fakedns"
	}
	if o.IPv4Only {
		k += "|v4"
	}
	return k
}

func (o *DNSOptions) validate() error {
	if !o.enabled() {
		return nil
	}
	if _, _, err := dnsListen(o.Listen); err != nil {
		return err
	}
	if len(o.DirectResolvers) == 0 {
		return fmt.Errorf("xray splice: DNS through the tunnel needs at least one direct resolver")
	}
	for _, r := range o.DirectResolvers {
		if ip := net.ParseIP(r); ip == nil || ip.To4() == nil {
			return fmt.Errorf("xray splice: direct resolver %q is not an IPv4 address", r)
		}
	}
	for _, d := range o.DirectDomains {
		if d == "" || strings.ContainsAny(d, " \t\r\n\",") {
			return fmt.Errorf("xray splice: direct domain %q is not a domain matcher", d)
		}
	}
	return nil
}

// dnsListen splits and checks the inbound's address: loopback only, since
// anything that reaches it resolves through the tunnel.
func dnsListen(listen string) (string, int, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", 0, fmt.Errorf("xray splice: DNS listen %q: %w", listen, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || ip.To4() == nil {
		return "", 0, fmt.Errorf("xray splice: DNS listen %q is not an IPv4 loopback address", listen)
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", 0, fmt.Errorf("xray splice: DNS listen %q has no usable port", listen)
	}
	return host, p, nil
}

// DNSResult is what the splice did about DNS, for the job result and the log.
type DNSResult struct {
	// FakeDomains is how many names get a FakeDNS address (SpliceOptions.FakeDNS).
	FakeDomains int
	// Listen is where the DNS inbound listens; "" when DNS is not steered for
	// this document (Skipped says why).
	Listen string
	// NodeHosts counts the host names resolved directly that the document
	// itself names: its nodes' and its DNS servers'.
	NodeHosts int
	// AddedDNS: the document had no "dns"; one was added.
	AddedDNS bool
	// Level is the policy level the DNS sessions run on (dnsLevelPolicy).
	Level   uint32
	Skipped string
}

// dnsPlan is what DNS through the tunnel becomes in one provider document.
type dnsPlan struct {
	on      bool
	inbound []byte            // the DNS inbound
	rule    json.RawMessage   // for the top of routing.rules
	servers []json.RawMessage // for the front of dns.servers
	level   uint32            // the DNS sessions' policy level (dnsLevelPolicy)
	res     DNSResult
}

// DNS sessions live seconds, not the provider's minutes. dnsmasq asks from a
// new port for every query, so each is a session of the DNS inbound and of
// the DNS outbound — and they lived the provider's connIdle (1111: 120 s).
// 600 lookups left 2136 sessions and 6400 goroutines behind; an ordinary
// browsing burst left hundreds (AAAA answers, empty under IPv4Only, are never
// cached by dnsmasq), each a buffer and stacks the GC walks. The two get a
// level of their own: idle 8 s — above a slow answer's 4 s; xray looks once
// per connIdle, so a session ends 8-16 s after its last packet (the provider's:
// 120-240 s) — closed as soon as a side is done, no buffer.
const (
	dnsLevelBase     = 16 // the first number tried: below it the provider's own
	dnsLevelConnIdle = 8
)

// dnsLevelPolicy is the DNS level's policy (xray's policy.levels entry).
func dnsLevelPolicy() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"handshake":4,"connIdle":%d,"uplinkOnly":0,"downlinkOnly":0,"bufferSize":0}`, dnsLevelConnIdle))
}

// freeLevel is the first level from dnsLevelBase the document neither defines
// in policy.levels nor names as a "level"/"userLevel" anywhere — a level the
// provider's users are on would get the DNS sessions' timeouts.
func freeLevel(raw []byte) uint32 {
	used := map[uint64]bool{}
	var doc interface{}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&doc) == nil {
		var walk func(v interface{})
		walk = func(v interface{}) {
			switch x := v.(type) {
			case map[string]interface{}:
				for k, vv := range x {
					switch foldKey(k) {
					case foldKey("level"), foldKey("userLevel"):
						if n, ok := vv.(json.Number); ok {
							if u, err := strconv.ParseUint(n.String(), 10, 32); err == nil {
								used[u] = true
							}
						}
					case foldKey("levels"):
						if m, ok := vv.(map[string]interface{}); ok {
							for lk := range m {
								if u, err := strconv.ParseUint(lk, 10, 32); err == nil {
									used[u] = true
								}
							}
						}
					}
					walk(vv)
				}
			case []interface{}:
				for _, vv := range x {
					walk(vv)
				}
			}
		}
		walk(doc)
	}
	l := uint64(dnsLevelBase)
	for used[l] {
		l++
	}
	return uint32(l)
}

// planDNS decides whether this document can resolve through the tunnel and
// renders what that takes. It never refuses a document: one it cannot steer
// safely is left as it was (res.Skipped), and the firewall then redirects
// nothing (RenderDNSListen finds no inbound).
func planDNS(providerRaw []byte, o *DNSOptions) (dnsPlan, error) {
	var plan dnsPlan
	host, port, err := dnsListen(o.Listen)
	if err != nil {
		return plan, err
	}
	var doc struct {
		DNS       json.RawMessage   `json:"dns"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal(providerRaw, &doc); err != nil {
		return plan, fmt.Errorf("xray splice: read the document for DNS: %w", err)
	}
	if len(doc.Outbounds) == 0 {
		// The DNS outbound is appended to the provider's; without them the
		// routing rule would name an outbound that does not exist.
		plan.res.Skipped = "the document has no outbounds to add the DNS one to"
		return plan, nil
	}
	for _, raw := range doc.Outbounds {
		if o := readOutbound(raw); o.Tag == DNSOutboundTag {
			plan.res.Skipped = "an outbound is already tagged " + DNSOutboundTag
			return plan, nil
		}
	}
	if why := unsafeProviderDNS(doc.DNS, o.AllowFakeDNS); why != "" {
		plan.res.Skipped = why
		return plan, nil
	}

	hosts := nodeHosts(doc.Outbounds)
	// The DNS servers' own names too: a DoH server named by its host is
	// resolved through the system resolver — back here — to be dialled.
	hosts = mergeHosts(hosts, dnsServerHosts(doc.DNS))
	domains := make([]string, 0, len(hosts)+len(o.DirectDomains))
	for _, h := range hosts {
		domains = append(domains, "full:"+h)
	}
	domains = append(domains, o.DirectDomains...)
	for _, r := range o.DirectResolvers {
		if len(domains) == 0 {
			// No node name to loop on, no control-plane name asked for.
			break
		}
		plan.servers = append(plan.servers, marshalNoEscape(struct {
			Address      string   `json:"address"`
			Domains      []string `json:"domains"`
			SkipFallback bool     `json:"skipFallback"`
		}{"tcp+local://" + r, domains, true}))
	}

	plan.level = freeLevel(providerRaw)
	plan.res.Level = plan.level
	plan.inbound = marshalNoEscape(struct {
		Tag      string `json:"tag"`
		Listen   string `json:"listen"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
		Settings struct {
			Address   string `json:"address"`
			Port      int    `json:"port"`
			Network   string `json:"network"`
			UserLevel uint32 `json:"userLevel"`
		} `json:"settings"`
	}{
		Tag: DNSInboundTag, Listen: host, Port: port, Protocol: "dokodemo-door",
		Settings: struct {
			Address   string `json:"address"`
			Port      int    `json:"port"`
			Network   string `json:"network"`
			UserLevel uint32 `json:"userLevel"`
		}{dnsInboundTarget, 53, "tcp,udp", plan.level},
	})
	plan.rule = marshalNoEscape(userRule{Type: "field", InboundTag: []string{DNSInboundTag}, OutboundTag: DNSOutboundTag})
	plan.on = true
	plan.res.Listen = o.Listen
	plan.res.NodeHosts = len(hosts)
	plan.res.AddedDNS = !hasDNSServers(doc.DNS)
	return plan, nil
}

// unsafeProviderDNS says why the provider's "dns" cannot answer the LAN, or "".
//
//   - "fakedns" hands out 198.18.0.0/15 addresses. The router bypasses that
//     range (it is also where a LAN's own fake-IP tools live), so a client
//     given one would connect nowhere.
//   - "localhost" asks the system resolver — dnsmasq, redirected back here.
//     Harmless only where xray never falls back to it (skipFallback, and no
//     domains of its own); as a fallback, every failed query would come back
//     around until dnsmasq ran out of room.
func unsafeProviderDNS(raw json.RawMessage, allowFake bool) string {
	if len(bytes.TrimSpace(raw)) == 0 || isJSONNull(raw) {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "the provider's dns is not an object"
	}
	var servers []json.RawMessage
	if s := ruleField(obj, "servers"); len(s) > 0 && !isJSONNull(s) {
		if err := json.Unmarshal(s, &servers); err != nil {
			return "the provider's dns.servers is not an array"
		}
	}
	for _, s := range servers {
		addr := jsonString(s)
		skip, hasDomains := false, false
		if addr == "" {
			var so map[string]json.RawMessage
			if json.Unmarshal(s, &so) != nil {
				return "a server in the provider's dns is neither a string nor an object"
			}
			addr = jsonString(ruleField(so, "address"))
			skip = string(bytes.TrimSpace(ruleField(so, "skipFallback"))) == "true"
			hasDomains = !emptyJSON(ruleField(so, "domains"))
		}
		switch strings.ToLower(strings.TrimSpace(addr)) {
		case "fakedns":
			if !allowFake {
				return "the provider's dns hands out fake addresses (fakedns)"
			}
		case "localhost":
			if !skip || hasDomains {
				return "the provider's dns asks the system resolver (localhost), which would ask itself"
			}
		}
	}
	return ""
}

// nodeHosts are the host names the document's nodes are dialled at, sorted
// and each once: vnext/servers addresses, the flat "address" newer documents
// use, and WireGuard peers' endpoints.
func nodeHosts(outbounds []json.RawMessage) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
		if h == "" || seen[h] || !hostName(h) {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, raw := range outbounds {
		if !readOutbound(raw).dials() {
			continue
		}
		var o struct {
			Settings struct {
				Address json.RawMessage `json:"address"`
				Vnext   []struct {
					Address json.RawMessage `json:"address"`
				} `json:"vnext"`
				Servers []struct {
					Address json.RawMessage `json:"address"`
				} `json:"servers"`
				Peers []struct {
					Endpoint string `json:"endpoint"`
				} `json:"peers"`
			} `json:"settings"`
		}
		if json.Unmarshal(raw, &o) != nil {
			continue
		}
		add(jsonString(o.Settings.Address))
		for _, v := range o.Settings.Vnext {
			add(jsonString(v.Address))
		}
		for _, s := range o.Settings.Servers {
			add(jsonString(s.Address))
		}
		for _, p := range o.Settings.Peers {
			if h, _, err := net.SplitHostPort(p.Endpoint); err == nil {
				add(h)
			}
		}
	}
	sort.Strings(out)
	return out
}

// dnsServerHosts are the host names in the provider's DNS server addresses
// ("https://dns.example/dns-query", "tcp://dns.example:53"), sorted.
func dnsServerHosts(raw json.RawMessage) []string {
	var obj map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &obj) != nil {
		return nil
	}
	var servers []json.RawMessage
	if s := ruleField(obj, "servers"); len(s) == 0 || json.Unmarshal(s, &servers) != nil {
		return nil
	}
	var out []string
	for _, s := range servers {
		addr := jsonString(s)
		if addr == "" {
			var so map[string]json.RawMessage
			if json.Unmarshal(s, &so) == nil {
				addr = jsonString(ruleField(so, "address"))
			}
		}
		if i := strings.Index(addr, "://"); i >= 0 {
			addr = addr[i+3:]
		}
		if i := strings.IndexAny(addr, "/?"); i >= 0 {
			addr = addr[:i]
		}
		if h, _, err := net.SplitHostPort(addr); err == nil {
			addr = h
		}
		addr = strings.ToLower(strings.TrimSuffix(addr, "."))
		if hostName(addr) {
			out = append(out, addr)
		}
	}
	sort.Strings(out)
	return out
}

// mergeHosts is a and b, sorted, each once.
func mergeHosts(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range append(append([]string(nil), a...), b...) {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// hostName: a DNS name xray would resolve — not an address, and made only of
// what a matcher can hold.
func hostName(h string) bool {
	if net.ParseIP(h) != nil || !strings.Contains(h, ".") || h == "localhost" {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
		default:
			return false
		}
	}
	return true
}

// dnsOutboundJSON is the DNS outbound on the DNS level (see DNSOutboundTag).
func dnsOutboundJSON(level uint32) []byte {
	return marshalNoEscape(struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
		Settings struct {
			UserLevel uint32 `json:"userLevel"`
		} `json:"settings"`
	}{DNSOutboundTag, "dns", struct {
		UserLevel uint32 `json:"userLevel"`
	}{level}})
}

// withDNSLevel adds the DNS level to a policy object, every other byte of it
// as it was; levels found as xray finds the key.
func withDNSLevel(policy json.RawMessage, level uint32) (json.RawMessage, error) {
	field := "levels"
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(policy, &obj); err != nil {
		return nil, fmt.Errorf("xray splice: policy is not an object: %w", err)
	}
	levels := map[string]json.RawMessage{}
	for k, v := range obj {
		if foldKey(k) == foldKey(field) {
			field = k
			if !isJSONNull(v) {
				if err := json.Unmarshal(v, &levels); err != nil {
					return nil, fmt.Errorf("xray splice: policy.levels is not an object: %w", err)
				}
			}
		}
	}
	key := strconv.FormatUint(uint64(level), 10)
	if _, taken := levels[key]; taken {
		return nil, fmt.Errorf("xray splice: policy level %s is the provider's — refusing", key)
	}
	levels[key] = dnsLevelPolicy()
	out, _, err := rewriteObjectField(policy, field, marshalNoEscape(levels))
	return out, err
}

// hasDNSServers: the provider's "dns" names at least one server. Without one
// xray asks the system resolver — here, itself.
func hasDNSServers(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 || isJSONNull(raw) {
		return false
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return false
	}
	var servers []json.RawMessage
	if s := ruleField(obj, "servers"); len(s) > 0 && !isJSONNull(s) && json.Unmarshal(s, &servers) == nil {
		return len(servers) > 0
	}
	return false
}

// prependDNSServers puts the direct servers at the front of dns.servers,
// every other byte of the provider's "dns" kept as it was. A "dns" with no
// servers of its own also gets the general one (addedDNSObject).
func prependDNSServers(raw json.RawMessage, servers []json.RawMessage) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("xray splice: dns is not an object: %w", err)
	}
	field := "servers"
	var existing []json.RawMessage
	for k, v := range obj {
		if foldKey(k) != foldKey("servers") {
			continue
		}
		field = k
		if !isJSONNull(v) {
			if err := json.Unmarshal(v, &existing); err != nil {
				return nil, fmt.Errorf("xray splice: dns.servers is not an array: %w", err)
			}
		}
	}
	if len(existing) == 0 {
		existing = []json.RawMessage{json.RawMessage(strconv.Quote(addedGeneralServer))}
	}
	var b bytes.Buffer
	b.WriteByte('[')
	for i, s := range append(append([]json.RawMessage(nil), servers...), existing...) {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(s)
	}
	b.WriteByte(']')
	out, _, err := rewriteObjectField(raw, field, b.Bytes())
	return out, err
}

// addedDNSObject is the "dns" a document without one gets: the direct servers,
// then one general server reached through the provider's routing. Without a
// "dns" xray would ask the system resolver — here, itself.
func addedDNSObject(servers []json.RawMessage, ipv4Only bool) json.RawMessage {
	var b bytes.Buffer
	b.WriteString(`{`)
	if ipv4Only {
		b.WriteString(`"queryStrategy":"UseIPv4",`)
	}
	b.WriteString(`"servers":[`)
	for _, s := range servers {
		b.Write(s)
		b.WriteByte(',')
	}
	b.WriteString(strconv.Quote(addedGeneralServer))
	b.WriteString(`]}`)
	return b.Bytes()
}

// RenderDNSListen reads, from an installed render, the port its DNS inbound
// listens on — what the firewall may redirect dnsmasq's upstream to. ok is
// false when the render has no such inbound: nothing may be redirected then,
// or every lookup on the router would go to a closed port.
func RenderDNSListen(render []byte) (port int, ok bool) {
	var doc struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Listen   string `json:"listen"`
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
		} `json:"inbounds"`
	}
	if json.Unmarshal(render, &doc) != nil {
		return 0, false
	}
	for _, ib := range doc.Inbounds {
		if ib.Tag != DNSInboundTag || ib.Protocol != "dokodemo-door" {
			continue
		}
		if ip := net.ParseIP(ib.Listen); ip == nil || !ip.IsLoopback() || ib.Port < 1 || ib.Port > 65535 {
			return 0, false
		}
		return ib.Port, true
	}
	return 0, false
}

// checkDNSLevel: the DNS inbound and outbound both run on level, and level is
// dnsLevelPolicy — read as xray reads the result.
func checkDNSLevel(spliced []byte, level uint32) error {
	type withLevel struct {
		Tag      string `json:"tag"`
		Settings struct {
			UserLevel *uint32 `json:"userLevel"`
		} `json:"settings"`
	}
	var got struct {
		Inbounds  []withLevel `json:"inbounds"`
		Outbounds []withLevel `json:"outbounds"`
		// The other levels as they are: a generator writes booleans in them
		// (statsUserUplink), and only the DNS level is judged here.
		Policy struct {
			Levels map[string]json.RawMessage `json:"levels"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(spliced, &got); err != nil {
		return fmt.Errorf("xray splice: re-read the result: %w", err)
	}
	on := func(obs []withLevel, tag string) bool {
		for _, o := range obs {
			if o.Tag == tag {
				return o.Settings.UserLevel != nil && *o.Settings.UserLevel == level
			}
		}
		return false
	}
	if !on(got.Inbounds, DNSInboundTag) || !on(got.Outbounds, DNSOutboundTag) {
		return fmt.Errorf("xray splice: the DNS inbound and outbound are not both on level %d — refusing", level)
	}
	var want map[string]json.Number
	if err := json.Unmarshal(dnsLevelPolicy(), &want); err != nil {
		return err
	}
	var have map[string]json.Number
	if raw, ok := got.Policy.Levels[strconv.FormatUint(uint64(level), 10)]; !ok || json.Unmarshal(raw, &have) != nil {
		return fmt.Errorf("xray splice: policy level %d is not the DNS sessions' — refusing", level)
	}
	if len(have) != len(want) {
		return fmt.Errorf("xray splice: policy level %d is not the DNS sessions' — refusing", level)
	}
	for k, v := range want {
		if have[k] != v {
			return fmt.Errorf("xray splice: policy level %d is not the DNS sessions' — refusing", level)
		}
	}
	return nil
}
