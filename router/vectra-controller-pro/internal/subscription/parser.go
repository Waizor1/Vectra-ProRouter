package subscription

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ParseBody decodes and parses a subscription body. Returns ParseResult
// even on partial failures (per-line errors are collected, not fatal).
//
// contentType comes from FetchResult.ContentType; pass "" when unknown.
func ParseBody(body []byte, contentType string) ParseResult {
	payload, format := DecodeBody(body, contentType)
	res := ParseResult{
		BodyFormat:   format,
		DecodedBytes: len(payload),
	}
	switch format {
	case FormatJSON:
		entries, remarks, err := SplitJSONEntries(payload)
		if err != nil {
			res.UnparsedLines = append(res.UnparsedLines, ParseError{
				LineNumber: 0,
				Reason:     err.Error(),
				Snippet:    snippet(string(payload)),
			})
			return res
		}
		res.Entries = entries
		res.Remarks = remarks
		return res
	case FormatBase64Links, FormatPlainLinks:
		// fall through to the link-list path below
	default:
		return res
	}

	lines := strings.Split(string(payload), "\n")
	res.LineCount = len(lines)
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		node, err := parseURI(line)
		if err != nil {
			res.UnparsedLines = append(res.UnparsedLines, ParseError{
				LineNumber: i + 1,
				Reason:     err.Error(),
				Snippet:    snippetLine(line),
			})
			continue
		}
		// Stable id: use position in feed to keep things deterministic when
		// remarks collide. Operators get a stable handle.
		if node.ID == "" {
			node.ID = fmt.Sprintf("sub-%03d", i+1)
		}
		res.Nodes = append(res.Nodes, node)
	}
	return res
}

// SplitJSONEntries splits the provider's top-level JSON array into N raw byte
// slices, one per complete Xray document, plus each entry's "remarks".
//
// json.RawMessage preserves each element VERBATIM — no struct round-trip, so
// `"tcpSettings":{}` stays an empty object. The remark is extracted by
// unmarshalling into a SEPARATE tiny struct; the entry itself is never
// re-serialized.
//
// A single top-level object (some providers return one config, not an array)
// is accepted and treated as a one-entry list.
func SplitJSONEntries(payload []byte) ([]json.RawMessage, []string, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil, nil, fmt.Errorf("json subscription: empty body")
	}

	var entries []json.RawMessage
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, nil, fmt.Errorf("json subscription: not an array of configs: %w", err)
		}
	case '{':
		entries = []json.RawMessage{json.RawMessage(trimmed)}
	default:
		return nil, nil, fmt.Errorf("json subscription: body is neither an object nor an array")
	}
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf("json subscription: array contained zero configs")
	}

	remarks := make([]string, len(entries))
	for i, e := range entries {
		// Separate struct — the entry bytes are read, never rewritten.
		var meta struct {
			Remarks string `json:"remarks"`
		}
		if err := json.Unmarshal(e, &meta); err != nil {
			return nil, nil, fmt.Errorf("json subscription: entry %d is not an object: %w", i, err)
		}
		remarks[i] = meta.Remarks
	}
	return entries, remarks, nil
}

// SelectEntry picks the entry to adopt: by exact remark when remark != "",
// otherwise by index. Returns the raw entry and its position.
func SelectEntry(entries []json.RawMessage, remarks []string, remark string, index int) (json.RawMessage, int, error) {
	if len(entries) == 0 {
		return nil, 0, fmt.Errorf("select entry: no entries")
	}
	if remark != "" {
		for i, r := range remarks {
			if r == remark {
				return entries[i], i, nil
			}
		}
		return nil, 0, fmt.Errorf("select entry: no entry with remarks=%q (have %d entries: %s)",
			remark, len(entries), strings.Join(remarks, ", "))
	}
	if index < 0 || index >= len(entries) {
		return nil, 0, fmt.Errorf("select entry: index %d out of range (have %d entries)", index, len(entries))
	}
	return entries[index], index, nil
}

func parseURI(line string) (ParsedNode, error) {
	switch {
	case strings.HasPrefix(line, "vless://"):
		return parseVLESS(line)
	case strings.HasPrefix(line, "vmess://"):
		return parseVMess(line)
	case strings.HasPrefix(line, "trojan://"):
		return parseTrojan(line)
	case strings.HasPrefix(line, "ss://"):
		return parseShadowsocks(line)
	case strings.HasPrefix(line, "hysteria2://"), strings.HasPrefix(line, "hy2://"):
		return parseHysteria2(line)
	default:
		// We DO NOT silently skip — operator must know we received this.
		scheme := line
		if i := strings.Index(line, "://"); i > 0 {
			scheme = line[:i]
		}
		return ParsedNode{}, fmt.Errorf("unsupported scheme %q (supported: vless, vmess, trojan, ss, hysteria2)", scheme)
	}
}

func snippetLine(line string) string {
	// Redact obvious secrets in the snippet to keep error logs safer.
	// (Full secrets remain in the in-memory ParsedNode for the operator;
	// only the textual error snippet is sanitized.)
	if i := strings.Index(line, "@"); i > 0 {
		line = "***@" + line[i+1:]
	}
	return snippet(line)
}

func snippet(s string) string {
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}
