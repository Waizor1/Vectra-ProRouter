package routepolicy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Share links, as PassWall2 26.8.10's subscribe.lua turns them into nodes
// (parse_link and processData, the "vless" branch, for a subscription —
// add_mode 2). The same options with the same values, quirks included, so the
// generator — PassWall's and ours — makes the same outbound from a node
// whoever wrote it. Only VLESS: the fleet's subscription carries nothing else,
// and a scheme this does not know is skipped with a reason, never guessed at.

// ErrUnsupportedLink: a line that is not a VLESS share link.
var ErrUnsupportedLink = errors.New("routepolicy: not a link this router can use")

// LinkOptions are the subscription's settings a link is read with.
type LinkOptions struct {
	// Group is the subscription's remark: every node it makes carries it.
	Group string
	// AllowInsecure is what tls_allowInsecure becomes when the link does not
	// say: the subscription's allowInsecure, on unless set to something else.
	AllowInsecure bool
}

// hashSpaces is Lua's "%s*#%s*": parse_link drops the spaces around every #.
var hashSpaces = regexp.MustCompile("[ \t\n\v\f\r]*#[ \t\n\v\f\r]*")

// ParseLink reads one line of a subscription into a node section (no name
// yet: Refresh gives it one).
func ParseLink(line string, o LinkOptions) (Section, error) {
	node := luaTrim(line)
	dat := luaSplit(node, "://")
	if len(dat) < 2 {
		return Section{}, fmt.Errorf("%w: no scheme", ErrUnsupportedLink)
	}
	if dat[0] != "vless" {
		return Section{}, fmt.Errorf("%w: %s", ErrUnsupportedLink, dat[0])
	}
	link := hashSpaces.ReplaceAllString(strings.ReplaceAll(dat[1], "&amp;", "&"), "#")
	return parseVLESS(link, o), nil
}

// node is a section being filled the way processData fills its result table:
// a value set to nil is simply not there.
type node struct {
	opts  map[string]string
	lists map[string][]string
}

func (n node) set(k, v string) { n.opts[k] = v }

// setIf sets k when the link had the parameter — even empty: an empty string
// is a value in Lua.
func (n node) setIf(k string, v string, ok bool) {
	if ok {
		n.opts[k] = v
	} else {
		delete(n.opts, k)
	}
}

func parseVLESS(content string, o LinkOptions) Section {
	n := node{opts: map[string]string{}, lists: map[string][]string{}}
	n.set("timeout", "60")
	n.set("add_mode", "2")
	if o.Group != "default" {
		n.set("group", o.Group)
	}
	n.set("type", "Xray")
	n.set("protocol", "vless")
	alias := ""
	if i := strings.Index(content, "#"); i >= 0 {
		alias = content[i+1:]
		content = content[:i]
	}
	n.set("remarks", urlDecode(alias))
	var address string
	var haveAddress bool
	port := "443"
	if strings.Contains(content, "@") {
		info := luaSplit(content, "@")
		n.set("uuid", urlDecode(info[0]))
		rest := ""
		if len(info) > 1 {
			rest = info[1]
		}
		rest = strings.ReplaceAll(rest, "/?", "?")
		query := luaSplit(rest, "?")
		hostPort := ""
		if len(query) > 0 {
			hostPort = query[0]
		}
		params := map[string]string{}
		if len(query) > 1 {
			for _, v := range luaSplit(query[1], "&") {
				if s := strings.Index(v, "="); s > 0 {
					params[v[:s]] = urlDecode(v[s+1:])
				}
			}
		}
		p := func(k string) (string, bool) { v, ok := params[k]; return v, ok }
		or := func(k, def string) string {
			if v, ok := params[k]; ok {
				return v
			}
			return def
		}
		nonEmpty := func(k string) (string, bool) { v, ok := params[k]; return v, ok && v != "" }

		if strings.Contains(hostPort, ":") {
			sp := luaSplit(hostPort, ":")
			port = sp[len(sp)-1]
			if isIPv6AddrPort(hostPort) {
				address = ipv6Only(hostPort)
			} else {
				address = sp[0]
			}
		} else {
			address = hostPort
		}
		haveAddress = true
		n.set("address", address)

		typ := strings.ToLower(or("type", "tcp"))
		if typ == "tcp" {
			typ = "raw" // an Xray node
		}
		if typ == "h2" || typ == "http" {
			typ = "http"
			n.set("transport", "xhttp")
		} else {
			n.set("transport", typ)
		}
		host, hostOK := p("host")
		path, pathOK := p("path")
		switch typ {
		case "ws":
			n.setIf("ws_host", host, hostOK)
			n.setIf("ws_path", path, pathOK)
		case "http":
			n.set("transport", "xhttp")
			n.set("xhttp_mode", "stream-one")
			n.setIf("xhttp_host", host, hostOK)
			n.setIf("xhttp_path", path, pathOK)
		case "raw":
			n.set("tcp_guise", or("headerType", "none"))
			if hostOK && host != "" {
				n.lists["tcp_guise_http_host"] = []string{host}
			}
			if pathOK && path != "" {
				n.lists["tcp_guise_http_path"] = []string{path}
			}
		case "kcp", "mkcp":
			n.set("transport", "mkcp")
			n.set("mkcp_guise", or("headerType", "none"))
			seed, ok := p("seed")
			n.setIf("mkcp_seed", seed, ok)
		case "quic":
			n.set("quic_guise", or("headerType", "none"))
			key, ok := p("key")
			n.setIf("quic_key", key, ok)
			n.set("quic_security", or("quicSecurity", "none"))
		case "grpc":
			if pathOK {
				n.set("grpc_serviceName", path)
			}
			if v, ok := p("serviceName"); ok {
				n.set("grpc_serviceName", v)
			}
			n.set("grpc_mode", or("mode", "gun"))
		case "xhttp", "splithttp":
			n.setIf("xhttp_host", host, hostOK)
			n.setIf("xhttp_path", path, pathOK)
			n.set("xhttp_mode", or("mode", "auto"))
			if extra, ok := nonEmpty("extra"); ok {
				n.set("use_xhttp_extra", "1")
				n.set("xhttp_extra", base64.StdEncoding.EncodeToString([]byte(extra)))
				if a := downloadAddress(extra); a != "" {
					n.set("download_address", a)
				}
			}
		case "httpupgrade":
			n.setIf("httpupgrade_host", host, hostOK)
			n.setIf("httpupgrade_path", path, pathOK)
		}
		n.set("encryption", or("encryption", "none"))
		flow, flowOK := p("flow")
		n.setIf("flow", flow, flowOK)
		security, secOK := p("security")
		if (!secOK || security == "") && flowOK {
			security = "tls"
		}
		n.set("tls", "0")
		if security == "tls" || security == "reality" {
			n.set("tls", "1")
			if sni, ok := nonEmpty("sni"); ok {
				n.set("tls_serverName", sni)
			} else {
				n.setIf("tls_serverName", host, hostOK)
			}
			alpn, ok := p("alpn")
			n.setIf("alpn", alpn, ok)
			if fp, ok := nonEmpty("fp"); ok {
				n.set("utls", "1")
				n.set("fingerprint", fp)
			}
			if ech, ok := nonEmpty("ech"); ok {
				n.set("ech", "1")
				n.set("ech_config", ech)
			}
			pcs, ok := p("pcs")
			n.setIf("tls_pinSHA256", pcs, ok)
			vcn, ok := p("vcn")
			n.setIf("tls_CertByName", vcn, ok)
			if security == "reality" {
				n.set("reality", "1")
				pbk, ok := p("pbk")
				n.setIf("reality_publicKey", pbk, ok)
				sid, ok := p("sid")
				n.setIf("reality_shortId", sid, ok)
				spx, ok := p("spx")
				n.setIf("reality_spiderX", spx, ok)
				if _, ok := nonEmpty("pqv"); ok {
					n.set("use_mldsa65Verify", "1")
				}
				pqv, ok := p("pqv")
				n.setIf("reality_mldsa65Verify", pqv, ok)
			}
			insecure, ok := p("allowinsecure")
			if !ok {
				insecure, ok = p("allowInsecure")
			}
			if !ok {
				insecure, ok = p("insecure")
			}
			if ok && (insecure == "1" || insecure == "0") {
				n.set("tls_allowInsecure", insecure)
			} else if o.AllowInsecure {
				n.set("tls_allowInsecure", "1")
			} else {
				n.set("tls_allowInsecure", "0")
			}
		}
		n.set("port", port)
		tfo, ok := p("tfo")
		n.setIf("tcp_fast_open", tfo, ok)
		if fm, ok := nonEmpty("fm"); ok {
			n.set("use_finalmask", "1")
			n.set("finalmask", base64.StdEncoding.EncodeToString([]byte(fm)))
		}
	}
	if n.opts["remarks"] == "" {
		if haveAddress {
			n.set("remarks", address+":"+port)
		} else {
			n.set("remarks", "NULL")
		}
	}
	// An empty value is no value: uci stores none (serviceName= in a link
	// leaves no grpc_serviceName in PassWall's node).
	for k, v := range n.opts {
		if v == "" {
			delete(n.opts, k)
		}
	}
	return Section{Type: "nodes", Options: n.opts, Lists: n.lists}
}

// downloadAddress is the xhttp extra's download server, if it names one.
func downloadAddress(extra string) string {
	var data map[string]any
	if json.Unmarshal([]byte(extra), &data) != nil {
		return ""
	}
	addr := func(m map[string]any) string {
		ds, _ := m["downloadSettings"].(map[string]any)
		a, _ := ds["address"].(string)
		return a
	}
	a := ""
	if ex, ok := data["extra"].(map[string]any); ok {
		a = addr(ex)
	}
	if a == "" {
		a = addr(data)
	}
	return strings.TrimSuffix(strings.TrimPrefix(a, "["), "]")
}

// --------------------------------------------- PassWall's api.lua, in Go ---

// luaSplit is api.split: the pieces between each sep, an empty one kept in
// the middle, the last one only when it is not empty.
func luaSplit(full, sep string) []string {
	full = strings.ReplaceAll(full, "\x00", "")
	var out []string
	for {
		i := strings.Index(full, sep)
		if i < 0 {
			if full != "" {
				out = append(out, full)
			}
			return out
		}
		out = append(out, full[:i])
		full = full[i+len(sep):]
	}
}

// luaTrim is api.trim: every byte up to space off both ends.
func luaTrim(s string) string {
	i, j := 0, len(s)
	for i < j && s[i] <= ' ' {
		i++
	}
	for j > i && s[j-1] <= ' ' {
		j--
	}
	return s[i:j]
}

// urlDecode is api.UrlDecode: "+" is a space, %XX a byte, anything else as
// it is (a stray % stays).
func urlDecode(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			v, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			b.WriteByte(byte(v))
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// base64Decode is api.base64Decode: control characters out, the URL-safe
// alphabet read as the standard one, padding made good — and the text itself
// back when it is not base64 at all (a plain list of links).
func base64Decode(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c < 32 || c == 127 {
			continue
		}
		switch c {
		case '_':
			c = '/'
		case '-':
			c = '+'
		}
		b.WriteByte(c)
	}
	enc := strings.TrimRight(b.String(), "=")
	out, err := base64.RawStdEncoding.DecodeString(enc)
	if err != nil {
		return text
	}
	return strings.ReplaceAll(string(out), "\x00", "")
}

// isIPv6AddrPort is api.is_ipv6addrport: "[v6]:port".
func isIPv6AddrPort(v string) bool {
	m := ipv6AddrPort.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	ip := net.ParseIP(m[1])
	p, err := strconv.Atoi(m[2])
	return ip != nil && ip.To4() == nil && strings.Contains(m[1], ":") && err == nil && p >= 1 && p <= 65535
}

var ipv6AddrPort = regexp.MustCompile(`\[(.*?)\]:([0-9]+)$`)

// ipv6Only is api.get_ipv6_only: the address inside the brackets.
func ipv6Only(v string) string {
	inner := v
	if i := strings.Index(v, "["); i >= 0 {
		if j := strings.Index(v[i+1:], "]"); j >= 0 {
			inner = v[i+1 : i+1+j]
		}
	}
	if ip := net.ParseIP(inner); ip != nil && strings.Contains(inner, ":") {
		return inner
	}
	return ""
}

// isHostname is LuCI's datatypes.hostname (not strict).
func isHostname(v string) bool {
	if v == "" || len(v) >= 254 {
		return false
	}
	if hostOnlyLetters.MatchString(v) {
		return true
	}
	return hostName.MatchString(v) && notDigitsDots.MatchString(v)
}

var (
	hostOnlyLetters = regexp.MustCompile(`^[a-zA-Z_]+$`)
	hostName        = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*[a-zA-Z0-9]$`)
	notDigitsDots   = regexp.MustCompile(`[^0-9.]`)
)

// isIP is api.is_ip: an IPv4 or IPv6 address, brackets allowed.
func isIP(v string) bool {
	v = strings.ToLower(luaTrim(v))
	if i := strings.Index(v, "["); i >= 0 {
		if j := strings.Index(v[i+1:], "]"); j >= 0 {
			v = v[i+1 : i+1+j]
		}
	}
	return net.ParseIP(v) != nil
}
