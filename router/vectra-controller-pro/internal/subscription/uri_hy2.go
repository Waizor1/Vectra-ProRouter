package subscription

import (
	"fmt"
	"net/url"
	"strconv"
)

// parseHysteria2 handles the hysteria2://-style URI used by Hiddify/V2RayN
// and PassWall2:
//
//	hysteria2://<password>@<host>:<port>?obfs=salamander&obfs-password=…&sni=…&pinSHA256=…&insecure=0#name
//	hy2:// alias likewise.
func parseHysteria2(s string) (ParsedNode, error) {
	u, err := url.Parse(s)
	if err != nil {
		return ParsedNode{}, fmt.Errorf("hy2: url.Parse: %w", err)
	}
	if u.User == nil {
		return ParsedNode{}, fmt.Errorf("hy2: missing password (userinfo)")
	}
	host, port, err := splitHostPort(u.Host)
	if err != nil {
		return ParsedNode{}, fmt.Errorf("hy2: host: %w", err)
	}
	q := u.Query()
	password := u.User.Username()
	if password == "" {
		// some providers put password in 'auth' query
		password = q.Get("auth")
	}
	node := ParsedNode{
		Protocol:      "hysteria2",
		Server:        host,
		Port:          port,
		Remark:        decodeFragment(u.Fragment),
		RawURI:        s,
		UnknownParams: map[string]string{},
		Hysteria2: &ParsedHy2{
			Password: password,
			Obfs:     q.Get("obfs"),
			ObfsPass: q.Get("obfs-password"),
			HopPorts: q.Get("mport"),
		},
		Stream: ParsedStream{
			Transport: "tcp", // Hysteria2 is UDP-based; we set transport=tcp as a placeholder — the builder routes hy2 to its dedicated outbound, no stream needed.
			Security:  "tls",
			SNI:       q.Get("sni"),
			Fingerprint: q.Get("fp"),
		},
	}
	if v := q.Get("up"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			node.Hysteria2.Up = n
		}
	}
	if v := q.Get("down"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			node.Hysteria2.Down = n
		}
	}
	if v := q.Get("insecure"); v == "1" {
		node.Stream.AllowInsec = true
	}
	consumed := map[string]bool{
		"obfs": true, "obfs-password": true, "mport": true,
		"up": true, "down": true, "insecure": true,
		"sni": true, "fp": true, "auth": true,
	}
	for k := range q {
		if !consumed[k] && len(q[k]) > 0 {
			node.UnknownParams[k] = q.Get(k)
		}
	}
	return node, nil
}
