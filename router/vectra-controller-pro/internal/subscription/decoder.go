package subscription

import (
	"bytes"
	"encoding/base64"
	"strings"
	"unicode"
)

// Body formats DecodeBody can report.
const (
	FormatJSON        = "json"
	FormatBase64Links = "base64-link-list"
	FormatPlainLinks  = "plain-link-list"
	FormatUnknown     = "unknown"
)

// DecodeBody classifies a subscription body and returns the payload to parse.
//
// The provider serves DIFFERENT payloads for the same URL depending on the
// User-Agent (measured on the live fleet):
//
//	v2rayNG/1.9.5, Happ/…     -> application/json, a JSON ARRAY of complete
//	                             Xray configs (~485 KB, 26 entries)
//	passwall2/*, Xray/*, ...  -> text/plain, base64 vless:// link list (~11.7 KB)
//
// (The Happ measurement was taken with "Happ/1.0", which the provider's
// anti-fraud sweep treats as a fake client: the device is deleted and the
// account disabled on the first request. Never send it — internal/uaguard
// refuses it.)
//
// contentType is consulted FIRST (it is authoritative when the provider sets
// it) and the byte sniffing is the fallback.
//
// For JSON the ORIGINAL body slice is returned — not a trimmed copy and not a
// string round-trip. Byte exactness is load-bearing downstream: the splice
// re-emits provider bytes verbatim, and any normalization corrupts empty
// objects (see internal/coreengine/xray/doc.go).
func DecodeBody(body []byte, contentType string) (payload []byte, format string) {
	if isJSONContentType(contentType) && looksLikeJSON(body) {
		return body, FormatJSON
	}

	trim := bytes.TrimSpace(body)

	// Try base64-decoding the body verbatim (after stripping whitespace).
	if maybeBase64(trim) {
		// V2RayN convention often uses standard alphabet + padding; some providers
		// use URL-safe without padding. Try both, tolerant of any internal whitespace.
		dec, err := decodeBase64Tolerant(trim)
		if err == nil && looksLikeLinkList(dec) {
			return dec, FormatBase64Links
		}
	}

	// Plain link list?
	if looksLikeLinkList(trim) {
		return trim, FormatPlainLinks
	}

	// JSON without (or with a misleading) Content-Type.
	if looksLikeJSON(body) {
		return body, FormatJSON
	}

	return trim, FormatUnknown
}

func isJSONContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return false
	}
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct == "application/json" || ct == "text/json" ||
		strings.HasSuffix(ct, "+json")
}

func looksLikeJSON(body []byte) bool {
	trim := bytes.TrimSpace(body)
	return len(trim) > 0 && (trim[0] == '{' || trim[0] == '[')
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
