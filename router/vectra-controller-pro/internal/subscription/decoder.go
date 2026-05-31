package subscription

import (
	"bytes"
	"encoding/base64"
	"strings"
	"unicode"
)

// DecodeBody normalizes a subscription body into a plain (newline-separated)
// link list. It handles three observed real-world flavors:
//   - Single-line base64 of the link list (most common: V2RayN/PassWall2)
//   - Multi-line base64 (some providers wrap at 76 chars)
//   - Plain link list (already decoded — happens on some providers)
//
// The detected format is returned alongside the decoded text.
func DecodeBody(body []byte) (plain string, format string) {
	trim := bytes.TrimSpace(body)

	// Try base64-decoding the body verbatim (after stripping whitespace).
	if maybeBase64(trim) {
		// V2RayN convention often uses standard alphabet + padding; some providers
		// use URL-safe without padding. Try both, tolerant of any internal whitespace.
		dec, err := decodeBase64Tolerant(trim)
		if err == nil && looksLikeLinkList(dec) {
			return string(dec), "base64-link-list"
		}
	}

	// Plain link list?
	if looksLikeLinkList(trim) {
		return string(trim), "plain-link-list"
	}

	// JSON? (uncommon for "subscriptions" but possible.)
	if len(trim) > 0 && (trim[0] == '{' || trim[0] == '[') {
		return string(trim), "json"
	}

	return string(trim), "unknown"
}

func maybeBase64(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	// All bytes (ignoring whitespace) must be in the base64 alphabet.
	for _, c := range b {
		if c == '\n' || c == '\r' || c == ' ' || c == '\t' {
			continue
		}
		if !(c >= 'A' && c <= 'Z') &&
			!(c >= 'a' && c <= 'z') &&
			!(c >= '0' && c <= '9') &&
			c != '+' && c != '/' && c != '-' && c != '_' && c != '=' {
			return false
		}
	}
	return true
}

func decodeBase64Tolerant(b []byte) ([]byte, error) {
	// Remove whitespace first so std/url decoders don't fail on wrapping.
	stripped := stripWhitespace(b)
	// Try standard with padding.
	if out, err := base64.StdEncoding.DecodeString(string(stripped)); err == nil {
		return out, nil
	}
	// Try standard without padding (RawStd).
	if out, err := base64.RawStdEncoding.DecodeString(string(stripped)); err == nil {
		return out, nil
	}
	// Try URL-safe with padding.
	if out, err := base64.URLEncoding.DecodeString(string(stripped)); err == nil {
		return out, nil
	}
	// Try URL-safe without padding.
	return base64.RawURLEncoding.DecodeString(string(stripped))
}

func stripWhitespace(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if !unicode.IsSpace(rune(c)) {
			out = append(out, c)
		}
	}
	return out
}

func looksLikeLinkList(b []byte) bool {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return false
	}
	// First non-empty line must be one of the supported schemes.
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, p := range []string{"vless://", "vmess://", "trojan://", "ss://", "ssr://", "hysteria2://", "hy2://", "tuic://", "wireguard://", "socks://"} {
			if strings.HasPrefix(line, p) {
				return true
			}
		}
		return false
	}
	return false
}
