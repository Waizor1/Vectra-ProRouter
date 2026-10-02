// Package redact takes credentials out of text: the VPN's user ids,
// passwords and keys, the subscription's token, the router's own panel token.
// What a diagnosis needs stays — hosts, addresses, ports, node tags, times —
// so the router UI can show its journal and its errors to the router's owner
// without handing anyone the VPN. Bug reports (internal/bugreport) build on it
// and take out more: whatever says whose the router is.
package redact

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var (
	// A share link is a credential whole: user id, password, keys and all.
	reLink = regexp.MustCompile(`(?i)\b(?:vless|vmess|trojan|ss|ssr|hy2|hysteria2?|tuic|wireguard|wg|socks5?|anytls|happ)://\S+`)
	reURL  = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	// A value under a secret's name, in JSON and as key=value / key: value.
	reJSONKV = regexp.MustCompile(`(?i)"(\w*(?:password|passwd|token|secret|auth|pbk|psk|key|hwid)|pass|sid|shortId|uuid)"\s*:\s*"[^"]*"`)
	reKV     = regexp.MustCompile(`(?i)\b(\w*(?:password|passwd|token|secret|auth|pbk|psk|key|hwid)|pass|sid|uuid)(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&"']+)`)
	// The same names as a UCI option: `option password 'x'` (PassWall2's
	// nodes, Wi-Fi's key) has neither = nor :.
	reUCI = regexp.MustCompile(`(?i)\b(option\s+(?:\w*(?:password|passwd|token|secret|auth|pbk|psk|key|hwid)|pass|sid|uuid|short_id|public_key)\s+)("[^"]*"|'[^']*'|\S+)`)
	// A UUID is what VLESS, VMess and TUIC take as the user's id.
	reUUID = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reLong = regexp.MustCompile(`[A-Za-z0-9+=_-]{32,}`)
)

// Credentials takes out of s what is a credential by its shape: share links,
// what follows a web address's host (a subscription's token is in its path or
// query) and its user:password@, values under a secret's name, UUIDs.
func Credentials(s string) string {
	s = Links(s)
	s = URLs(s)
	s = KeyValues(s)
	return UUIDs(s)
}

// Text is Credentials for free text — a log line, an error — where a key or
// a token may also be printed bare: any run of 32 or more letters and digits
// (with both in it) goes too. Package paths, node tags and words have no such
// run.
func Text(s string) string {
	return Tokens(Credentials(s))
}

// Links replaces every proxy share link with <link>.
func Links(s string) string { return reLink.ReplaceAllString(s, "<link>") }

// URLs keeps a web address's scheme and host (and port) and replaces the rest
// with /<redacted>; an address that is nothing but its host stays as it is.
func URLs(s string) string { return reURL.ReplaceAllStringFunc(s, redactURL) }

// KeyValues replaces the value under every secret's name: "password": "…",
// token=…, pbk=…, sid: ….
func KeyValues(s string) string {
	s = reJSONKV.ReplaceAllString(s, `"$1":"<redacted>"`)
	s = reUCI.ReplaceAllString(s, "$1<redacted>")
	return reKV.ReplaceAllString(s, "$1$2<redacted>")
}

// UUIDs replaces every UUID with <uuid>.
func UUIDs(s string) string { return reUUID.ReplaceAllString(s, "<uuid>") }

// Tokens replaces every run of 32 or more letters and digits that has both
// in it — a key, a token or a hash — with <secret>.
func Tokens(s string) string { return reLong.ReplaceAllStringFunc(s, long) }

// redactURL is one address as URLs leaves it. What closes a sentence or a
// bracket after it is not part of it.
func redactURL(m string) string {
	trail := ""
	for len(m) > 0 && strings.IndexByte(".,;:!?)]}", m[len(m)-1]) >= 0 {
		trail = m[len(m)-1:] + trail
		m = m[:len(m)-1]
	}
	u, err := url.Parse(m)
	if err != nil || u.Host == "" {
		return "<url>" + trail
	}
	if u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.Opaque == "" && (u.Path == "" || u.Path == "/") {
		return m + trail
	}
	return u.Scheme + "://" + u.Host + "/<redacted>" + trail
}

func long(s string) string {
	var digit, letter bool
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			letter = true
		}
	}
	if digit && letter {
		return "<secret>"
	}
	return s
}

// MinKnown is the shortest value Known takes: a shorter one could be a word,
// and replacing it everywhere would shred the text.
const MinKnown = 8

// Known are secret values the router holds — its panel token, the VPN's ids,
// passwords and keys — replaced wherever they appear, whatever their shape.
type Known []string

// NewKnown keeps each value of at least MinKnown bytes once, longest first,
// so a value inside another leaves no remnant.
func NewKnown(values ...string) Known {
	seen := map[string]bool{}
	var k Known
	for _, v := range values {
		if len(v) < MinKnown || seen[v] {
			continue
		}
		seen[v] = true
		k = append(k, v)
	}
	sort.SliceStable(k, func(i, j int) bool { return len(k[i]) > len(k[j]) })
	return k
}

// Redact replaces every known value in s with <redacted>.
func (k Known) Redact(s string) string {
	for _, v := range k {
		if strings.Contains(s, v) {
			s = strings.ReplaceAll(s, v, "<redacted>")
		}
	}
	return s
}
