package subscription

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// parseVLESS handles the V2RayN-flavor VLESS URI:
//
//	vless://<uuid>@<host>:<port>?<params>#<remark>
//
// All upstream params are preserved verbatim. Unknown params are stored in
// ParsedNode.UnknownParams for operator inspection.
func parseVLESS(s string) (ParsedNode, error) {
	u, err := url.Parse(s)
	if err != nil {
		return ParsedNode{}, fmt.Errorf("vless: url.Parse: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return ParsedNode{}, fmt.Errorf("vless: missing uuid (userinfo)")
	}
	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return ParsedNode{}, fmt.Errorf("vless: host: %w", err)
	}
	q := u.Query()
	node := ParsedNode{
		Protocol:      "vless",
		Server:        host,
		Port:          port,
		Remark:        decodeFragment(u.Fragment),
		RawURI:        s,
		UnknownParams: map[string]string{},
		VLESS: &ParsedVLESS{
			UUID:       u.User.Username(),
			Flow:       q.Get("flow"),
			Encryption: q.Get("encryption"),
		},
	}
	consumed := map[string]bool{
		"flow": true, "encryption": true,
	}
	fillStream(&node.Stream, q, consumed)
	for k := range q {
		if !consumed[k] && len(q[k]) > 0 {
			node.UnknownParams[k] = q.Get(k)
		}
	}
	return node, nil
}

// fillStream pulls common stream/security params from the query.
// Every consumed key is recorded so unknown params can be surfaced.
func fillStream(s *ParsedStream, q url.Values, consumed map[string]bool) {
	get := func(k string) string {
		consumed[k] = true
		return q.Get(k)
	}
	s.Transport = orDefault(get("type"), "tcp") // V2RayN convention: missing type = tcp
	s.Security = get("security")
	// TLS / REALITY common
	s.SNI = firstNonEmpty(get("sni"), get("peer"))
	s.Fingerprint = get("fp")
	if v := get("allowInsecure"); v != "" {
		s.AllowInsec = v == "1" || strings.EqualFold(v, "true")
	}
	if v := get("alpn"); v != "" {
		// URI encodes alpn as csv (h2,http/1.1).
		for _, item := range strings.Split(v, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				s.ALPN = append(s.ALPN, item)
			}
		}
	}
	// REALITY
	s.PublicKey = get("pbk")
	s.ShortID = get("sid")
	s.SpiderX = get("spx")
	// Per-transport
	switch s.Transport {
	case "tcp":
		s.TCPHeaderType = get("headerType")
		// host/path may still appear for "http" header type; we don't separate them out
		// from the stream struct to keep this simple, the builder reads tcp header from raw extras.
		s.Host = get("host")
		s.Path = get("path")
	case "ws":
		s.Host = get("host")
		s.Path = get("path")
	case "grpc":
		s.ServiceName = get("serviceName")
		s.GRPCMode = get("mode")
	case "kcp":
		s.Seed = get("seed")
		s.HeaderType = get("headerType")
	case "quic":
		s.QUICKey = get("key")
		s.QUICSecurity = get("quicSecurity")
		s.HeaderType = get("headerType")
	case "xhttp":
		s.Path = get("path")
		s.Host = get("host")
		s.XHTTPMode = get("mode")
	case "httpupgrade":
		s.Path = get("path")
		s.Host = get("host")
	}
	if s.Flow == "" {
		s.Flow = q.Get("flow") // already consumed by VLESS path; harmless read
	}
}

// splitHostPort handles host:port with IPv6 brackets.
func splitHostPort(hp string) (string, int, error) {
	host := hp
	port := ""
	// IPv6 bracket form: [::1]:443
	if strings.HasPrefix(hp, "[") {
		end := strings.Index(hp, "]")
		if end < 0 {
			return "", 0, fmt.Errorf("bad ipv6 host %q", hp)
		}
		host = hp[1:end]
		if len(hp) > end+1 && hp[end+1] == ':' {
			port = hp[end+2:]
		}
	} else if i := strings.LastIndex(hp, ":"); i >= 0 {
		host = hp[:i]
		port = hp[i+1:]
	}
	if host == "" {
		return "", 0, fmt.Errorf("empty host in %q", hp)
	}
	if port == "" {
		return host, 0, nil
	}
	p, err := strconv.Atoi(port)
	if err != nil || p <= 0 || p > 65535 {
		return "", 0, fmt.Errorf("bad port %q", port)
	}
	return host, p, nil
}

func decodeFragment(frag string) string {
	if frag == "" {
		return ""
	}
	if s, err := url.QueryUnescape(frag); err == nil {
		return s
	}
	return frag
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
