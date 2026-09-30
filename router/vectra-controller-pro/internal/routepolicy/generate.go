package routepolicy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// Generate is PassWall2 26.8.10's generator — util_xray.lua gen_config, for
// flag=global with a _shunt global node, the fleet's configuration — in Go:
// the same UCI sections in, the same arguments (vctl's passwallArgs), the
// same xray JSON out, so that everything downstream of it (AdaptPassWall, the
// splice, the DNS steer, the kernel's direct routes) is unchanged. Proven
// against the real generator's output: testdata/fleet*.gen-*.json.
//
// What the fleet does not use is refused with ErrUnsupported rather than
// guessed: balancers, pre-proxy chains, non-Xray node types, protocols other
// than vless/vmess/trojan/shadowsocks, fragment and noise, sniffing override,
// per-node DNS resolvers, a socks or http inbound.
func Generate(secs []Section, args map[string]string, xrayVersion string) ([]byte, error) {
	g := &gen{secs: secs, args: args, xray: xrayVersion}
	cfg, err := g.config()
	if err != nil {
		return nil, err
	}
	return json.Marshal(cfg)
}

// ErrUnsupported is wrapped by Generate for configurations outside the
// fleet's subset of PassWall2.
var ErrUnsupported = errors.New("routepolicy: not supported without PassWall2")

type obj = map[string]any

type dnsEntry struct {
	outboundTag string // "" = none
	server      obj
}

type domainRule struct {
	shuntRule   string
	outboundTag string
	domains     []string
	fakedns     bool
}

// has reports whether a section has an option at all: in PassWall's Lua an
// empty string is true and an absent option is not.
func has(s *Section, key string) bool {
	_, ok := s.Options[key]
	return ok
}

type gen struct {
	secs []Section
	args map[string]string
	xray string

	outbounds []obj
	rules     []obj
	inbounds  []obj
}

func (g *gen) arg(k string) string { return g.args[k] }

func (g *gen) section(name string) *Section {
	for i := range g.secs {
		if g.secs[i].Name == name {
			return &g.secs[i]
		}
	}
	return nil
}

func (g *gen) first(typ string) Section {
	for _, s := range g.secs {
		if s.Type == typ {
			return s
		}
	}
	return Section{Options: map[string]string{}, Lists: map[string][]string{}}
}

func unsupported(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, a...))
}

// versionLess compares dotted versions numerically: a < b.
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func (g *gen) config() (obj, error) {
	if g.arg("flag") != "global" {
		return nil, unsupported("flag %q", g.arg("flag"))
	}
	if g.arg("local_socks_port") != "" || g.arg("local_http_port") != "" {
		return nil, unsupported("a socks or http inbound")
	}
	xs := g.first("global_xray")
	if xs.Get("fragment") == "1" || xs.Get("noise") == "1" {
		return nil, unsupported("fragment/noise")
	}
	if xs.Get("sniffing_override_dest") == "1" {
		return nil, unsupported("sniffing_override_dest")
	}
	node := g.section(g.arg("node"))
	if node == nil || node.Type != "nodes" {
		return nil, unsupported("global node %q not found", g.arg("node"))
	}
	if node.Get("protocol") != "_shunt" {
		return nil, unsupported("a global node that is not a shunt (%q)", node.Get("protocol"))
	}

	innerFake := node.Get("fakedns")
	if innerFake == "" {
		innerFake = "0"
	}
	remoteFake := g.arg("remote_dns_fake") != ""

	// The default route.
	defID := node.Get("default_node")
	if defID == "" {
		defID = "_direct"
	}
	defTag, err := g.shuntNode(node, "default", defID)
	if err != nil {
		return nil, err
	}
	if innerFake == "1" && node.Get("default_fakedns") == "1" {
		remoteFake = true
	}

	// The shunt rules, in UCI order.
	var dnsDomainRules []domainRule
	for _, e := range g.secs {
		if e.Type != "shunt_rules" || node.Get("shunt_group") != e.Get("group") {
			continue
		}
		bind, set := node.Options[e.Name]
		if !set {
			continue
		}
		tag, err := g.shuntNode(node, e.Name, bind)
		if err != nil {
			return nil, err
		}
		if tag == "" || !has(&e, "remarks") {
			continue
		}
		if tag == "default" {
			tag = defTag
		}
		var protocols []any
		if p := e.Get("protocol"); p != "" {
			for _, w := range strings.Fields(p) {
				protocols = append(protocols, w)
			}
		}
		var inboundTag []any
		inboundSet := false
		if in := e.Get("inbound"); in != "" {
			inboundSet = true
			inboundTag = []any{}
			if strings.Contains(in, "tproxy") && g.arg("redir_port") != "" {
				inboundTag = append(inboundTag, "tcp_redir", "udp_redir")
			}
		}
		domains := listLines(e.Get("domain_list"))
		if has(&e, "domain_list") {
			dr := domainRule{shuntRule: e.Name, outboundTag: tag, domains: domains}
			if innerFake == "1" && node.Get(e.Name+"_fakedns") == "1" && len(domains) > 0 {
				dr.fakedns = true
			}
			dnsDomainRules = append(dnsDomainRules, dr)
		}
		ips := listLines(e.Get("ip_list"))
		var source []any
		if src := e.Get("source"); src != "" {
			for _, w := range strings.Fields(src) {
				source = append(source, w)
			}
		}
		network := e.Get("network")
		if network == "" {
			network = "tcp,udp"
		}
		base := obj{"ruleTag": e.Get("remarks"), "outboundTag": tag, "network": network}
		if inboundSet {
			base["inboundTag"] = inboundTag
		}
		if source != nil {
			base["source"] = source
		}
		if p := e.Get("port"); p != "" {
			base["port"] = p
		}
		if protocols != nil {
			base["protocol"] = protocols
		}
		if len(domains) > 0 {
			r := clone(base)
			r["ruleTag"] = e.Get("remarks") + " Domains"
			r["domains"] = anySlice(domains)
			g.rules = append(g.rules, r)
		}
		if len(ips) > 0 {
			r := clone(base)
			r["ruleTag"] = e.Get("remarks") + " IP"
			r["ip"] = anySlice(ips)
			g.rules = append(g.rules, r)
		}
		if len(domains) == 0 && len(ips) == 0 {
			g.rules = append(g.rules, base)
		}
	}
	if defTag != "" {
		r := obj{"ruleTag": "default", "outboundTag": defTag}
		if node.Get("domainStrategy") == "IPIfNonMatch" {
			r["port"] = "1-65535"
		} else {
			r["network"] = "tcp,udp"
		}
		g.rules = append(g.rules, r)
	}
	domainStrategy := node.Get("domainStrategy")
	if domainStrategy == "" {
		domainStrategy = "AsIs"
	}
	domainMatcher := node.Get("domainMatcher")
	if domainMatcher == "" {
		domainMatcher = "hybrid"
	}

	dns, fakedns, err := g.dns(node, defTag, innerFake, remoteFake, dnsDomainRules)
	if err != nil {
		return nil, err
	}

	// The transparent-proxy inbounds.
	if rp := g.arg("redir_port"); rp != "" {
		port, _ := strconv.Atoi(rp)
		sniff := obj{"enabled": true, "destOverride": []any{"http", "tls", "quic"}, "metadataOnly": false, "routeOnly": true}
		if remoteFake || innerFake == "1" {
			sniff["destOverride"] = append(sniff["destOverride"].([]any), "fakedns")
		}
		mk := func(tag, network, tproxy string) obj {
			return obj{
				"tag": tag, "port": port, "protocol": "dokodemo-door",
				"settings":       obj{"network": network, "followRedirect": true},
				"streamSettings": obj{"sockopt": obj{"tproxy": tproxy}},
				"sniffing":       clone(sniff),
			}
		}
		way := g.arg("tcp_proxy_way")
		g.inbounds = append(g.inbounds, mk("tcp_redir", "tcp", way), mk("udp_redir", "udp", "tproxy"))
	}

	asset := g.first("global_rules").Get("v2ray_location_asset")
	if asset == "" {
		asset = "/usr/share/v2ray/"
	}
	loglevel := g.arg("loglevel")
	if loglevel == "" {
		loglevel = "warning"
	}
	if loglevel == "warn" {
		loglevel = "warning"
	}
	level0 := obj{"statsUserUplink": false, "statsUserDownlink": false}
	if b, err := strconv.Atoi(xs.Get("buffer_size")); err == nil {
		level0["bufferSize"] = b
	}

	directStrategy := g.arg("direct_dns_query_strategy")
	if directStrategy == "" {
		directStrategy = "UseIP"
	}
	direct := obj{
		"protocol": "freedom", "tag": "direct",
		"settings":       obj{"domainStrategy": directStrategy, "finalRules": []any{obj{"action": "allow"}}},
		"streamSettings": obj{"sockopt": obj{"mark": 255}},
	}
	if defTag == "direct" {
		g.outbounds = append([]obj{direct}, g.outbounds...)
	} else {
		g.outbounds = append(g.outbounds, direct)
	}
	blackhole := obj{"protocol": "blackhole", "tag": "blackhole"}
	if defTag == "blackhole" {
		g.outbounds = append([]obj{blackhole}, g.outbounds...)
	} else {
		g.outbounds = append(g.outbounds, blackhole)
	}

	cfg := obj{
		"env":       obj{"XRAY_LOCATION_ASSET": asset},
		"log":       obj{"loglevel": loglevel},
		"dns":       dns,
		"inbounds":  toAny(g.inbounds),
		"outbounds": toAny(g.outbounds),
		"routing":   obj{"domainStrategy": domainStrategy, "domainMatcher": domainMatcher, "rules": toAny(g.rules)},
		"policy":    obj{"levels": obj{"0": level0}},
		"version":   obj{"min": "26.3.27"},
	}
	if fakedns != nil {
		cfg["fakedns"] = fakedns
	}
	return cfg, nil
}

// shuntNode is gen_shunt_node: the outbound tag a binding routes to, the
// node's outbound added when it is one. "" = no rule.
func (g *gen) shuntNode(shunt *Section, rule, id string) (string, error) {
	switch {
	case id == "":
		return "", nil
	case id == "_direct":
		return "direct", nil
	case id == "_blackhole":
		return "blackhole", nil
	case id == "_default" && rule != "default":
		return "default", nil
	}
	if pre := shunt.Get(rule + "_proxy_tag"); pre != "" && pre != id {
		return "", unsupported("slot %q goes through a pre-proxy", rule)
	}
	n := g.section(id)
	if n == nil || n.Type != "nodes" {
		if n != nil && n.Type == "socks" {
			return "", unsupported("slot %q is bound to a socks section", rule)
		}
		return "", nil
	}
	switch n.Get("protocol") {
	case "_balancing", "_iface", "_shunt", "_urltest":
		return "", unsupported("slot %q is bound to a %s node", rule, n.Get("protocol"))
	}
	if n.Get("type") != "Xray" {
		return "", unsupported("slot %q is bound to a %s node", rule, n.Get("type"))
	}
	ob, err := g.outbound(n, rule)
	if err != nil {
		return "", err
	}
	if rule == "default" {
		g.outbounds = append([]obj{ob}, g.outbounds...)
	} else {
		g.outbounds = append(g.outbounds, ob)
	}
	return ob["tag"].(string), nil
}

// outbound is gen_outbound for an Xray-type node.
func (g *gen) outbound(n *Section, tag string) (obj, error) {
	proto := n.Get("protocol")
	switch proto {
	case "vless", "vmess", "trojan", "shadowsocks":
	default:
		return nil, unsupported("node %q: protocol %q", n.Name, proto)
	}
	if n.Get("domain_resolver") != "" && (n.Get("domain_resolver_dns") != "" || n.Get("domain_resolver_dns_https") != "") {
		return nil, unsupported("node %q: its own DNS resolver", n.Name)
	}
	if n.Get("finalmask") != "" || n.Get("chain_proxy") != "" || n.Get("preproxy_node") != "" || n.Get("to_node") != "" {
		return nil, unsupported("node %q: finalmask or a proxy chain", n.Name)
	}
	if has(n, "remarks") {
		tag = tag + ":" + n.Get("remarks")
	}
	security := ""
	if n.Get("tls") == "1" {
		security = "tls"
		if n.Get("reality") == "1" {
			security = "reality"
		}
	}
	transport := n.Get("transport")
	switch transport {
	case "raw", "tcp", "grpc", "ws", "httpupgrade", "xhttp":
	default:
		return nil, unsupported("node %q: transport %q", n.Name, transport)
	}

	mux := obj{"enabled": n.Get("mux") == "1"}
	if n.Get("mux") == "1" {
		mux["concurrency"] = atoiOr(n.Get("mux_concurrency"), -1)
		mux["xudpConcurrency"] = atoiOr(n.Get("xudp_concurrency"), 8)
	}

	sockopt := obj{"mark": 255, "domainStrategy": orDefault(n.Get("domain_strategy"), "UseIP")}
	if n.Get("tcp_fast_open") == "1" {
		sockopt["tcpFastOpen"] = true
	}
	if n.Get("tcpMptcp") == "1" {
		sockopt["tcpMptcp"] = true
	}
	if n.Get("happy_eyeballs") == "1" {
		sockopt["happyEyeballs"] = obj{"TryDelayMs": 250, "PrioritizeIPv6": false, "Interleave": 1, "MaxConcurrentTry": 4}
	}
	stream := obj{"sockopt": sockopt}
	netKey := "method"
	if versionLess(g.xray, "26.7.11") {
		netKey = "network"
	}
	stream[netKey] = transport
	if security != "" {
		stream["security"] = security
	}
	fp := n.Get("fingerprint")
	switch security {
	case "tls":
		tls := obj{
			"pinnedPeerCertSha256": n.Get("tls_pinSHA256"),
			"verifyPeerCertByName": n.Get("tls_CertByName"),
		}
		if sni := n.Get("tls_serverName"); sni != "" {
			tls["serverName"] = sni
		}
		if n.Get("utls") == "1" && fp != "" {
			tls["fingerprint"] = fp
		}
		if n.Get("ech") == "1" && n.Get("ech_config") != "" {
			tls["echConfigList"] = n.Get("ech_config")
		}
		if n.Get("tls_certificate") == "1" || len(n.Lists["cipherSuites"]) > 0 {
			return nil, unsupported("node %q: pinned certificates or cipher suites", n.Name)
		}
		if a := n.Get("alpn"); a != "" && a != "default" {
			var alpn []any
			for _, w := range strings.Split(a, ",") {
				if w != "" {
					alpn = append(alpn, w)
				}
			}
			if len(alpn) > 0 {
				tls["alpn"] = alpn
			}
		}
		stream["tlsSettings"] = tls
	case "reality":
		if n.Get("use_mldsa65Verify") == "1" {
			return nil, unsupported("node %q: mldsa65Verify", n.Name)
		}
		r := obj{
			"shortId":     n.Get("reality_shortId"),
			"spiderX":     orDefault(n.Get("reality_spiderX"), "/"),
			"fingerprint": orDefault(fp, "chrome"),
		}
		if has(n, "reality_publicKey") {
			r["publicKey"] = n.Get("reality_publicKey")
		}
		if sni := n.Get("tls_serverName"); sni != "" {
			r["serverName"] = sni
		}
		stream["realitySettings"] = r
	}
	ua := n.Get("user_agent")
	switch transport {
	case "raw", "tcp":
		if gz := n.Get("tcp_guise"); gz != "" && gz != "none" {
			return nil, unsupported("node %q: tcp guise %q", n.Name, gz)
		}
	case "grpc":
		gs := obj{
			"multiMode":             n.Get("grpc_mode") == "multi",
			"permit_without_stream": n.Get("grpc_permit_without_stream") == "1",
			"initial_windows_size":  atoiOr(n.Get("grpc_initial_windows_size"), 0),
		}
		if v := n.Get("grpc_serviceName"); v != "" {
			gs["serviceName"] = v
		}
		if v := n.Get("grpc_idle_timeout"); v != "" {
			if t, err := strconv.Atoi(v); err == nil {
				if t < 10 {
					t = 10
				}
				gs["idle_timeout"] = t
			}
		}
		if v, err := strconv.Atoi(n.Get("grpc_health_check_timeout")); err == nil {
			gs["health_check_timeout"] = v
		}
		if ua != "" {
			gs["user_agent"] = ua
		}
		stream["grpcSettings"] = gs
	case "ws":
		ws := obj{"path": orDefault(n.Get("ws_path"), "/")}
		if h := n.Get("ws_host"); h != "" {
			ws["host"] = h
		}
		if ua != "" {
			ws["headers"] = obj{"User-Agent": ua}
		}
		if v, err := strconv.Atoi(n.Get("ws_maxEarlyData")); err == nil {
			ws["maxEarlyData"] = v
		}
		if v := n.Get("ws_earlyDataHeaderName"); v != "" {
			ws["earlyDataHeaderName"] = v
		}
		if v, err := strconv.Atoi(n.Get("ws_heartbeatPeriod")); err == nil {
			ws["heartbeatPeriod"] = v
		}
		stream["wsSettings"] = ws
	case "httpupgrade":
		hu := obj{"path": orDefault(n.Get("httpupgrade_path"), "/")}
		if h := n.Get("httpupgrade_host"); h != "" {
			hu["host"] = h
		}
		if ua != "" {
			hu["headers"] = obj{"User-Agent": ua}
		}
		stream["httpupgradeSettings"] = hu
	case "xhttp":
		xh := obj{"mode": orDefault(n.Get("xhttp_mode"), "auto"), "path": orDefault(n.Get("xhttp_path"), "/")}
		if h := n.Get("xhttp_host"); h != "" {
			xh["host"] = h
		}
		extra := obj{}
		if e := n.Get("xhttp_extra"); e != "" {
			if raw, ok := decodeB64(e); ok {
				var parsed obj
				if json.Unmarshal(raw, &parsed) == nil {
					if inner, ok := parsed["extra"].(map[string]any); ok {
						extra = inner
					} else {
						extra = parsed
					}
				}
			}
		}
		if ua != "" {
			headers, _ := extra["headers"].(map[string]any)
			if headers == nil {
				headers = obj{}
			}
			if headers["User-Agent"] == nil && headers["user-agent"] == nil {
				headers["User-Agent"] = ua
			}
			extra["headers"] = headers
		}
		if c := cleanEmpty(extra); c != nil {
			xh["extra"] = c
		}
		stream["xhttpSettings"] = xh
	}

	settings := obj{
		"address": strings.ToLower(n.Get("address")),
		"level":   0,
	}
	if p, err := strconv.Atoi(n.Get("port")); err == nil {
		settings["port"] = p
	}
	setIf := func(key, opt string) {
		if has(n, opt) {
			settings[key] = n.Get(opt)
		}
	}
	switch proto {
	case "vless":
		setIf("id", "uuid")
		enc := orDefault(n.Get("encryption"), "none")
		settings["encryption"] = enc
		if (n.Get("tls") == "1" || enc != "none") && n.Get("flow") != "" {
			settings["flow"] = n.Get("flow")
		}
	case "vmess":
		setIf("id", "uuid")
		setIf("security", "security")
	case "trojan":
		setIf("password", "password")
	case "shadowsocks":
		setIf("password", "password")
		m := n.Get("method")
		switch m {
		case "chacha20-ietf-poly1305":
			m = "chacha20-poly1305"
		case "xchacha20-ietf-poly1305":
			m = "xchacha20-poly1305"
		}
		if m != "" {
			settings["method"] = m
		}
	}
	return obj{"tag": tag, "protocol": proto, "mux": mux, "streamSettings": stream, "settings": settings}, nil
}

// dns is the DNS half of gen_config: the dns object, the fakedns pools, the
// dns-in inbound and the dns-out outbound with its routing rule.
func (g *gen) dns(node *Section, defTag, innerFake string, remoteFake bool, domainRules []domainRule) (obj, []any, error) {
	const (
		directTag  = "dns-in-direct"
		remoteTag  = "dns-in-remote"
		fakeTag    = "dns-in-remote-fakedns"
		defaultTag = "dns-in-default"
	)
	hosts := obj{}
	dns := obj{
		"tag": "dns-global", "disableCache": g.arg("dns_cache") == "0",
		"disableFallback": true, "disableFallbackIfMatch": true, "queryStrategy": "UseIP",
	}
	var dnsServers []dnsEntry
	var servers []any
	var fakedns []any

	var directDNS obj
	if srv := g.arg("direct_dns_udp_server"); srv != "" {
		directDNS = obj{
			"tag": directTag, "address": srv, "port": atoiOr(g.arg("direct_dns_udp_port"), 53),
			"queryStrategy": orDefault(g.arg("direct_dns_query_strategy"), "UseIP"),
		}
		dnsServers = append(dnsServers, dnsEntry{outboundTag: "direct", server: directDNS})
	}

	var remoteDNS, remoteFakeDNS obj
	if g.arg("dns_listen_port") == "" {
		return nil, nil, unsupported("no DNS listen port")
	}
	global := g.first("global")
	for _, line := range strings.Split(strings.ReplaceAll(global.Get("dns_hosts"), "\r", ""), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			hosts[f[0]] = f[1]
		}
	}
	remoteDNS = obj{"tag": remoteTag, "queryStrategy": orDefault(g.arg("remote_dns_query_strategy"), "UseIPv4")}
	remoteProto := "tcp"
	if s := g.arg("remote_dns_udp_server"); s != "" {
		remoteDNS["address"] = s
		remoteDNS["port"] = atoiOr(g.arg("remote_dns_udp_port"), 53)
		remoteProto = "udp"
	}
	if s := g.arg("remote_dns_tcp_server"); s != "" {
		remoteDNS["address"] = "tcp://" + s + ":" + g.arg("remote_dns_tcp_port")
		remoteDNS["port"] = atoiOr(g.arg("remote_dns_tcp_port"), 53)
		remoteProto = "tcp"
	}
	if u, h := g.arg("remote_dns_doh_url"), g.arg("remote_dns_doh_host"); u != "" && h != "" {
		if ip := g.arg("remote_dns_doh_ip"); ip != "" && h != ip && net.ParseIP(h) == nil {
			hosts[h] = ip
		}
		remoteDNS["address"] = u
		remoteDNS["port"] = atoiOr(g.arg("remote_dns_doh_port"), 443)
	}
	if remoteDNS["address"] != nil {
		out := ""
		if g.arg("remote_dns_detour") == "direct" {
			out = "direct"
		}
		dnsServers = append(dnsServers, dnsEntry{outboundTag: out, server: remoteDNS})
	}
	if remoteFake || innerFake == "1" {
		fakedns = []any{}
		v4 := obj{"ipPool": "198.18.0.0/16", "poolSize": 65535}
		v6 := obj{"ipPool": "fc00::/18", "poolSize": 65535}
		switch g.arg("remote_dns_query_strategy") {
		case "UseIP":
			fakedns = append(fakedns, v4, v6)
		case "UseIPv4":
			fakedns = append(fakedns, v4)
		case "UseIPv6":
			fakedns = append(fakedns, v6)
		}
		remoteFakeDNS = obj{"tag": fakeTag, "address": "fakedns"}
		dnsServers = append(dnsServers, dnsEntry{server: remoteFakeDNS})
	}
	if g.arg("direct_dns_udp_server") != "" {
		if d := g.nodeHostDomains(); len(d) > 0 {
			servers = append(servers, obj{
				"tag": "dns-in-vpslist", "address": "localhost", "domains": anySlice(d),
				"finalQuery": true, "disableCache": false, "serveStale": true,
			})
		}
	}

	// dns-in and dns-out.
	port, _ := strconv.Atoi(g.arg("dns_listen_port"))
	g.inbounds = append(g.inbounds, obj{
		"listen": "127.0.0.1", "port": port, "protocol": "dokodemo-door", "tag": "dns-in",
		"settings": obj{"address": "0.0.0.0", "network": "tcp,udp"},
	})
	dnsOutSettings := obj{
		"address": g.arg("direct_dns_udp_server"), "port": atoiOr(g.arg("direct_dns_udp_port"), 53), "network": "udp",
	}
	if g.arg("direct_dns_udp_server") == "" {
		delete(dnsOutSettings, "address")
	}
	if versionLess(g.xray, "26.4.25") {
		dnsOutSettings["nonIPQuery"] = "skip"
		dnsOutSettings["blockTypes"] = []any{65}
	}
	var dnsOutRules []any
	if versionLess("26.4.17", g.xray) {
		dnsOutRules = []any{
			obj{"qType": "1,28", "action": "hijack"},
			obj{"qType": 65, "action": "return", "rCode": 0},
			obj{"action": "direct"},
		}
	}
	dnsOut := obj{"tag": "dns-out", "protocol": "dns", "proxySettings": obj{"tag": "direct"}, "settings": dnsOutSettings}
	_ = remoteProto // the remote type's settings are PassWall's dead branch
	g.outbounds = append(g.outbounds, dnsOut)
	g.rules = append([]obj{{"inboundTag": []any{"dns-in"}, "outboundTag": "dns-out"}}, g.rules...)

	defaultName := remoteTag
	if defTag == "" || defTag == "direct" {
		defaultName = directTag
	}
	if len(dnsServers) > 0 {
		for _, v := range dnsServers {
			if v.server["tag"] != defaultName {
				continue
			}
			d := dnsEntry{outboundTag: v.outboundTag, server: clone(v.server)}
			d.server["tag"] = defaultTag
			if v.server["tag"] == remoteTag {
				if remoteFake {
					d.server = clone(remoteFakeDNS)
					d.server["tag"] = defaultTag
				} else if d.outboundTag == "" {
					d.outboundTag = defTag
				}
			}
			dnsServers = append([]dnsEntry{d}, dnsServers...)
			break
		}
		var outRules []any
		for _, v := range domainRules {
			if v.outboundTag == "" {
				continue
			}
			var srv obj
			out := v.outboundTag
			switch {
			case v.outboundTag == "direct":
				if directDNS != nil {
					srv = clone(directDNS)
				}
			case v.fakedns:
				srv = clone(remoteFakeDNS)
			default:
				srv = clone(remoteDNS)
				if g.arg("remote_dns_detour") == "direct" {
					out = "direct"
				}
			}
			if out == "blackhole" {
				outRules = append(outRules, obj{"action": "return", "rCode": 0, "domain": anySlice(v.domains)})
				srv = nil
			} else {
				outRules = append(outRules, obj{"action": "hijack", "qType": "1,28", "domain": anySlice(v.domains)})
			}
			if srv != nil {
				srv["finalQuery"] = true
				srv["domains"] = anySlice(v.domains)
				srv["tag"] = "dns-in-" + v.shuntRule
				dnsServers = append(dnsServers, dnsEntry{outboundTag: out, server: srv})
			}
		}
		if dnsOutRules != nil && len(outRules) > 0 {
			dnsOutRules = append(outRules, dnsOutRules...)
		}
	}
	if dnsOutRules != nil {
		dnsOutSettings["rules"] = dnsOutRules
	}

	// The default rule goes last.
	for i, r := range g.rules {
		if r["ruleTag"] == "default" {
			g.rules = append(append(g.rules[:i:i], g.rules[i+1:]...), r)
			break
		}
	}

	var front []any
	var frontRules []obj
	for _, v := range dnsServers {
		tag := v.server["tag"]
		if tag == directTag || tag == remoteTag {
			continue
		}
		if v.outboundTag != "" && v.server["address"] != "fakedns" {
			frontRules = append(frontRules, obj{"inboundTag": []any{tag}, "outboundTag": v.outboundTag})
		}
		if d, ok := v.server["domains"].([]any); (ok && len(d) > 0) || tag == defaultTag {
			front = append(front, v.server)
		}
	}
	g.rules = append(frontRules, g.rules...)
	servers = append(front, servers...)
	if len(servers) == 0 {
		servers = []any{obj{"tag": "local", "address": "localhost"}}
	}
	dns["servers"] = servers
	if len(hosts) > 0 {
		dns["hosts"] = hosts
	}
	return dns, fakedns, nil
}

// nodeHostDomains is PassWall's vpslist: every option named …address in the
// whole configuration whose value ends in a letter (a host name), sorted as
// `sort -u` sorts it, lowercased, as full: domains.
func (g *gen) nodeHostDomains() []string {
	seen := map[string]bool{}
	var hosts []string
	for _, s := range g.secs {
		for k, v := range s.Options {
			// `uci show | grep ".address="`: any option whose name ends in
			// "address" (the grep's dot is any character).
			if !strings.HasSuffix(k, "address") || v == "" {
				continue
			}
			c := v[len(v)-1]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') || seen[v] {
				continue
			}
			seen[v] = true
			hosts = append(hosts, v)
		}
	}
	sort.Strings(hosts)
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, "full:"+strings.ToLower(h))
	}
	return out
}

// listLines splits a PassWall list option: one entry a line, # comments and
// rule-set entries out.
func listLines(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		if strings.HasPrefix(w, "#") || strings.HasPrefix(w, "rule-set:") || strings.HasPrefix(w, "rs:") {
			continue
		}
		out = append(out, w)
	}
	return out
}

func anySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func toAny(os []obj) []any {
	out := make([]any, len(os))
	for i, o := range os {
		out[i] = o
	}
	return out
}

func clone(o obj) obj {
	b, _ := json.Marshal(o)
	var c obj
	_ = json.Unmarshal(b, &c)
	return c
}

// cleanEmpty drops empty objects and arrays, recursively, as PassWall's
// api.cleanEmptyTables does; nil when nothing is left.
func cleanEmpty(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if c := cleanEmpty(x); c == nil {
				delete(t, k)
			} else {
				t[k] = c
			}
		}
		if len(t) == 0 {
			return nil
		}
		return t
	case []any:
		var out []any
		for _, x := range t {
			if c := cleanEmpty(x); c != nil {
				out = append(out, c)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return v
}

func atoiOr(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// decodeB64 decodes base64 as PassWall's api.base64Decode accepts it:
// standard or URL alphabet, padded or not.
func decodeB64(s string) ([]byte, bool) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}
