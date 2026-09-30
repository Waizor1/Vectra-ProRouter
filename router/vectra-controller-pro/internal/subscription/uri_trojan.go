package subscription

import (
	"fmt"
	"net/url"
)

// parseTrojan handles the V2RayN-flavor Trojan URI:
//
//	trojan://<password>@<host>:<port>?<params>#<remark>
//
// Trojan always implies TLS; security defaults to "tls" if unset.
func parseTrojan(s string) (ParsedNode, error) {
	u, err := url.Parse(s)
	if err != nil {
		return ParsedNode{}, fmt.Errorf("trojan: url.Parse: %w", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return ParsedNode{}, fmt.Errorf("trojan: missing password (userinfo)")
	}
	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return ParsedNode{}, fmt.Errorf("trojan: host: %w", err)
	}
	q := u.Query()
	node := ParsedNode{
		Protocol:      "trojan",
		Server:        host,
		Port:          port,
		Remark:        decodeFragment(u.Fragment),
		RawURI:        s,
		UnknownParams: map[string]string{},
		Trojan: &ParsedTrojan{
			Password: u.User.Username(),
		},
	}
	consumed := map[string]bool{}
	fillStream(&node.Stream, q, consumed)
	if node.Stream.Security == "" {
		// Trojan REQUIRES TLS at the protocol level. The upstream URI did
		// not set 'security=', so we fill it in — but record the choice
		// in the parser-defaults audit trail (see NodeOrigin.ParserDefaults
		// surfaced via the adapter) so it isn't a silent normalization.
		node.Stream.Security = "tls"
		if node.parserDefaults == nil {
			node.parserDefaults = map[string]string{}
		}
		node.parserDefaults["stream.security"] = "tls (protocol requirement; upstream URI omitted it)"
	}
	for k := range q {
		if !consumed[k] && len(q[k]) > 0 {
			node.UnknownParams[k] = q.Get(k)
		}
	}
	return node, nil
}
