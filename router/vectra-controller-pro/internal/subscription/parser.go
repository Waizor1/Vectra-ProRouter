package subscription

import (
	"fmt"
	"strings"
)

// ParseBody decodes and parses a subscription body. Returns ParseResult
// even on partial failures (per-line errors are collected, not fatal).
func ParseBody(body []byte) ParseResult {
	plain, format := DecodeBody(body)
	res := ParseResult{
		BodyFormat:   format,
		DecodedBytes: len(plain),
	}
	if format == "" || format == "unknown" {
		return res
	}
	if format == "json" {
		// Reserved for future "universal" JSON subscription support. For now,
		// surface as unparsed with a clear message.
		res.UnparsedLines = append(res.UnparsedLines, ParseError{
			LineNumber: 0,
			Reason:     "json subscription format not yet supported in v0.1 alpha",
			Snippet:    snippet(plain),
		})
		return res
	}

	lines := strings.Split(plain, "\n")
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
