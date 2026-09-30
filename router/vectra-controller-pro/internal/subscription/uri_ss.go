package subscription

import (
	"fmt"
	"net/url"
	"strings"
)

// parseShadowsocks handles three observed flavors:
//
//	(a) SIP002 with userinfo base64:    ss://<b64(method:password)>@host:port?plugin=…#name
//	(b) SIP002 plaintext userinfo:      ss://method:password@host:port#name        (less common)
//	(c) Legacy V2RayN:                  ss://<b64(method:password@host:port)>#name
//
// We try SIP002 first, then fall back to legacy.
func parseShadowsocks(s string) (ParsedNode, error) {
	// Quick remark separation.
	frag := ""
	if i := strings.LastIndex(s, "#"); i > 0 {
		frag = decodeFragment(s[i+1:])
		s = s[:i]
	}

	// Try SIP002 parsing.
	if n, err := parseSSSIP002(s); err == nil {
		n.Remark = frag
		return n, nil
	}

	// Fall back to legacy.
	return parseSSLegacy(s, frag)
}

func parseSSSIP002(s string) (ParsedNode, error) {
	u, err := url.Parse(s)
	if err != nil {
		return ParsedNode{}, err
	}
	if u.Host == "" {
		return ParsedNode{}, fmt.Errorf("ss: no host")
	}
	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return ParsedNode{}, err
	}
	var methodPass string
	if u.User != nil {
		// Userinfo can be base64 (typical) or plain "method:password" (rare).
		raw := u.User.String()
		if strings.Contains(raw, ":") {
			methodPass = raw
		} else {
			dec, err := decodeBase64Tolerant([]byte(raw))
			if err != nil {
				return ParsedNode{}, fmt.Errorf("ss: userinfo base64: %w", err)
			}
			methodPass = string(dec)
		}
	}
	method, password, ok := strings.Cut(methodPass, ":")
	if !ok {
		return ParsedNode{}, fmt.Errorf("ss: bad method:password")
	}
	node := ParsedNode{
		Protocol:      "shadowsocks",
		Server:        host,
		Port:          port,
		RawURI:        s,
		UnknownParams: map[string]string{},
		Shadowsocks: &ParsedSS{
			Method:   method,
			Password: password,
		},
	}
	for k, v := range u.Query() {
		if len(v) > 0 {
			node.UnknownParams[k] = v[0]
		}
	}
	return node, nil
}

func parseSSLegacy(s, frag string) (ParsedNode, error) {
	const prefix = "ss://"
	if !strings.HasPrefix(s, prefix) {
		return ParsedNode{}, fmt.Errorf("ss: missing scheme")
	}
	dec, err := decodeBase64Tolerant([]byte(s[len(prefix):]))
	if err != nil {
		return ParsedNode{}, fmt.Errorf("ss legacy: base64: %w", err)
	}
	body := string(dec)
	// Expected: method:password@host:port
	atIdx := strings.LastIndex(body, "@")
	if atIdx < 0 {
		return ParsedNode{}, fmt.Errorf("ss legacy: missing '@'")
	}
	methodPass := body[:atIdx]
	hostPart := body[atIdx+1:]
	method, password, ok := strings.Cut(methodPass, ":")
	if !ok {
		return ParsedNode{}, fmt.Errorf("ss legacy: bad method:password")
	}
	host, port, err := splitHostPort(hostPart)
	if err != nil {
		return ParsedNode{}, err
	}
	return ParsedNode{
		Protocol: "shadowsocks",
		Server:   host,
		Port:     port,
		Remark:   frag,
		RawURI:   "ss://" + body, // de-base64'd for diagnostic
		Shadowsocks: &ParsedSS{
			Method:   method,
			Password: password,
		},
		UnknownParams: map[string]string{},
	}, nil
}
