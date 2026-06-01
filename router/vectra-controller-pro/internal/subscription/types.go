// Package subscription handles fetching upstream subscription URLs,
// decoding the response body (V2RayN base64-link-list format), and parsing
// each per-protocol URI into the controller's config.Node shape.
//
// Design rules (mirror project-level: no silent normalization):
//   - Every URI parser preserves operator/provider-set values byte-for-byte
//     (uuid, password, sni, pbk, shortId, flow, fingerprint, ...).
//   - The parser does NOT rewrite TLS fingerprints (PassWall2's fp=firefox→
//     chrome trick is explicitly forbidden here).
//   - Unknown query params are kept in NodeOrigin.UnknownParams so an operator
//     can inspect them later via `vctl subscribe parse --verbose`.
//   - The parser is reversible: given a parsed Node, an operator should be
//     able to reconstruct the original URI (modulo whitespace).
package subscription

import (
	"time"
)

// FetchResult is what Fetch returns: raw body + metadata extracted from
// V2RayN-style headers (subscription-userinfo, profile-title, ...).
type FetchResult struct {
	URL             string            `json:"url"`
	StatusCode      int               `json:"statusCode"`
	ContentType     string            `json:"contentType"`
	Body            []byte            `json:"-"` // raw bytes
	BodyBytes       int               `json:"bodyBytes"`
	FetchedAt       time.Time         `json:"fetchedAt"`
	UpstreamHeaders map[string]string `json:"upstreamHeaders,omitempty"` // selected response headers
	UserInfo        *UserInfo         `json:"userInfo,omitempty"`
	ProfileTitle    string            `json:"profileTitle,omitempty"`
	ProfileUpdateIntervalDays int     `json:"profileUpdateIntervalDays,omitempty"`
	ProfileWebPageURL string          `json:"profileWebPageUrl,omitempty"`
	SupportURL      string            `json:"supportUrl,omitempty"`
	Announcement    string            `json:"announcement,omitempty"`
}

// UserInfo parses the V2RayN "subscription-userinfo" response header:
//
//	upload=…; download=…; total=…; expire=<unix-seconds>
type UserInfo struct {
	UploadBytes   uint64    `json:"uploadBytes"`
	DownloadBytes uint64    `json:"downloadBytes"`
	TotalBytes    uint64    `json:"totalBytes"`   // 0 = unlimited
	ExpireAt      time.Time `json:"expireAt,omitempty"`
}

// ParseResult is what ParseBody returns: decoded node list + diagnostics.
type ParseResult struct {
	DecodedBytes int          `json:"decodedBytes"`
	LineCount    int          `json:"lineCount"`
	Nodes        []ParsedNode `json:"nodes"`
	// Lines that failed to parse — preserved for operator visibility.
	UnparsedLines []ParseError `json:"unparsedLines,omitempty"`
	// Source format detected.
	BodyFormat string `json:"bodyFormat"` // "base64-link-list" | "plain-link-list" | "json" | "unknown"
}

type ParseError struct {
	LineNumber int    `json:"lineNumber"` // 1-based
	Reason     string `json:"reason"`
	Snippet    string `json:"snippet"` // first 80 chars of the offending line, redacted of obvious secrets
}

// ParsedNode is the parser's output: it carries the same fields as
// config.Node but additionally exposes raw / debug information through
// the embedded UnknownParams.
type ParsedNode struct {
	// The exported result is meant to be merged into config.Nodes. We deliberately
	// use the full Node shape (via a small adapter in the engine) to avoid
	// recreating a parallel type tree. See AsConfigNode().
	ID            string
	Remark        string
	Group         string
	Protocol      string
	Server        string
	Port          int
	// Protocol-specific fields are stored in lightly-typed structs to keep
	// the parser small. The adapter knows how to map them into the strict
	// config.* types.
	VLESS        *ParsedVLESS
	VMess        *ParsedVMess
	Trojan       *ParsedTrojan
	Shadowsocks  *ParsedSS
	Hysteria2    *ParsedHy2
	// Stream/security parameters extracted from the URI. Empty fields mean
	// "not set by upstream" — they MUST NOT be defaulted by the parser.
	Stream       ParsedStream
	// Original URI for traceability.
	RawURI        string
	// Unrecognized query parameters preserved verbatim for operator inspection.
	UnknownParams map[string]string
	// parserDefaults records every value the parser had to fill in because
	// the protocol requires it and the upstream URI did not provide it.
	// Surfaced via NodeOrigin.ParserDefaults by the adapter.
	parserDefaults map[string]string
}

// ParserDefaults exposes the parser-defaults map (read-only). Nil when none.
func (p *ParsedNode) ParserDefaults() map[string]string {
	if len(p.parserDefaults) == 0 {
		return nil
	}
	// Defensive copy — caller shouldn't mutate.
	out := make(map[string]string, len(p.parserDefaults))
	for k, v := range p.parserDefaults {
		out[k] = v
	}
	return out
}

type ParsedVLESS struct {
	UUID       string
	Flow       string
	Encryption string
}

type ParsedVMess struct {
	UUID     string
	Security string
	AlterID  int
}

type ParsedTrojan struct {
	Password string
}

type ParsedSS struct {
	Method   string
	Password string
}

type ParsedHy2 struct {
	Password string
	Obfs     string
	ObfsPass string
	HopPorts string
	Up       int
	Down     int
}

type ParsedStream struct {
	Transport   string // tcp|ws|grpc|kcp|quic|xhttp|httpupgrade
	Security    string // none|tls|reality (empty = unset)
	// TLS
	SNI         string
	ALPN        []string
	Fingerprint string
	AllowInsec  bool
	// REALITY
	PublicKey   string
	ShortID     string
	SpiderX     string
	// TCP-specific
	TCPHeaderType string // none|http
	// WS / xhttp / httpupgrade
	Path    string
	Host    string
	// gRPC
	ServiceName string
	GRPCMode    string // gun|multi
	// KCP / QUIC
	Seed         string
	HeaderType   string // for kcp: none|srtp|utp|wechat-video|dtls|wireguard
	QUICKey      string
	QUICSecurity string
	// Generic
	Flow         string
	XHTTPMode    string
}
