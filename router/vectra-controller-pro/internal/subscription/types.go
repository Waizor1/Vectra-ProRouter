// Package subscription fetches the provider subscription URL and classifies
// what came back.
//
// The DEFAULT path is JSON: with a UA such as v2rayNG/1.9.5 the provider
// returns a JSON array of COMPLETE Xray configurations, which the
// controller adopts wholesale (only the inbounds are replaced — see
// internal/coreengine/xray). Each entry is carried as a json.RawMessage so its
// bytes are never rewritten.
//
// The base64 vless:// link list (what passwall2/Xray/sing-box user agents get)
// is the DEGRADED fallback. It is still parsed — per-protocol URI parsers below
// — but only for diagnostics via `vctl subscribe parse`; it is not on the
// default apply path.
//
// Design rules (mirror project-level: no silent normalization):
//   - Every URI parser preserves operator/provider-set values byte-for-byte
//     (uuid, password, sni, pbk, shortId, flow, fingerprint, ...).
//   - The parser does NOT rewrite TLS fingerprints (PassWall2's fp=firefox→
//     chrome trick is explicitly forbidden here).
//   - Unknown query params are kept in ParsedNode.UnknownParams so an operator
//     can inspect them later via `vctl subscribe parse -v`.
package subscription

import (
	"encoding/json"
	"time"
)

// FetchResult is what Fetch returns: raw body + metadata extracted from
// V2RayN-style headers (subscription-userinfo, profile-title, ...).
type FetchResult struct {
	URL                       string            `json:"url"`
	StatusCode                int               `json:"statusCode"`
	ContentType               string            `json:"contentType"`
	Body                      []byte            `json:"-"` // raw bytes
	BodyBytes                 int               `json:"bodyBytes"`
	FetchedAt                 time.Time         `json:"fetchedAt"`
	UpstreamHeaders           map[string]string `json:"upstreamHeaders,omitempty"` // selected response headers
	UserInfo                  *UserInfo         `json:"userInfo,omitempty"`
	ProfileTitle              string            `json:"profileTitle,omitempty"`
	ProfileUpdateIntervalDays int               `json:"profileUpdateIntervalDays,omitempty"`
	ProfileWebPageURL         string            `json:"profileWebPageUrl,omitempty"`
	SupportURL                string            `json:"supportUrl,omitempty"`
	Announcement              string            `json:"announcement,omitempty"`

	// Entries holds the provider's complete Xray configs, one raw slice per
	// array element, VERBATIM. Populated only for the JSON variant.
	Entries []json.RawMessage `json:"-"`
	// Remarks[i] is the "remarks" field of Entries[i] (may be empty).
	Remarks []string `json:"remarks,omitempty"`
	// BodyFormat mirrors ParseResult.BodyFormat for the fetched body.
	BodyFormat string `json:"bodyFormat,omitempty"`
}

// UserInfo parses the V2RayN "subscription-userinfo" response header:
//
//	upload=…; download=…; total=…; expire=<unix-seconds>
type UserInfo struct {
	UploadBytes   uint64    `json:"uploadBytes"`
	DownloadBytes uint64    `json:"downloadBytes"`
	TotalBytes    uint64    `json:"totalBytes"` // 0 = unlimited
	ExpireAt      time.Time `json:"expireAt,omitempty"`
}

// ParseResult is what ParseBody returns: the JSON entries (default path) or
// the decoded node list (degraded link-list path) + diagnostics.
type ParseResult struct {
	DecodedBytes int `json:"decodedBytes"`

	// JSON path (default): complete provider Xray configs, verbatim.
	Entries []json.RawMessage `json:"-"`
	Remarks []string          `json:"remarks,omitempty"`

	// Link-list path (degraded fallback).
	LineCount int          `json:"lineCount,omitempty"`
	Nodes     []ParsedNode `json:"nodes,omitempty"`

	// Lines that failed to parse — preserved for operator visibility.
	UnparsedLines []ParseError `json:"unparsedLines,omitempty"`
	// Source format detected.
	BodyFormat string `json:"bodyFormat"` // "json" | "base64-link-list" | "plain-link-list" | "unknown"
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
	ID       string
	Remark   string
	Group    string
	Protocol string
	Server   string
	Port     int
	// Protocol-specific fields are stored in lightly-typed structs to keep
	// the parser small. The adapter knows how to map them into the strict
	// config.* types.
	VLESS       *ParsedVLESS
	VMess       *ParsedVMess
	Trojan      *ParsedTrojan
	Shadowsocks *ParsedSS
	Hysteria2   *ParsedHy2
	// Stream/security parameters extracted from the URI. Empty fields mean
	// "not set by upstream" — they MUST NOT be defaulted by the parser.
	Stream ParsedStream
	// Original URI for traceability.
	RawURI string
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
	Transport string // tcp|ws|grpc|kcp|quic|xhttp|httpupgrade
	Security  string // none|tls|reality (empty = unset)
	// TLS
	SNI         string
	ALPN        []string
	Fingerprint string
	AllowInsec  bool
	// REALITY
	PublicKey string
	ShortID   string
	SpiderX   string
	// TCP-specific
	TCPHeaderType string // none|http
	// WS / xhttp / httpupgrade
	Path string
	Host string
	// gRPC
	ServiceName string
	GRPCMode    string // gun|multi
	// KCP / QUIC
	Seed         string
	HeaderType   string // for kcp: none|srtp|utp|wechat-video|dtls|wireguard
	QUICKey      string
	QUICSecurity string
	// Generic
	Flow      string
	XHTTPMode string
}
