package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// maxScanDepth bounds the read-only walk so a hostile deeply-nested document
// cannot blow the stack. The real provider payload nests ~5 levels.
const maxScanDepth = 64

// ErrAllowInsecure is returned when a provider document disables TLS
// certificate verification anywhere inside it.
type ErrAllowInsecure struct {
	Path string
}

func (e *ErrAllowInsecure) Error() string {
	return "provider config sets allowInsecure=true at " + e.Path +
		" (refusing: an upstream that disables TLS verification can be MITM'd end-to-end)"
}

// ScanAllowInsecure walks the document READ-ONLY and returns an *ErrAllowInsecure
// if any `"allowInsecure": true` (or the numeric/string truthy spellings Xray
// also accepts) is present.
//
// The old gate lived in the subscription URI adapter, which the JSON path never
// touches. This replaces it without rewriting a single byte: the walk only ever
// unmarshals into json.RawMessage / map[string]json.RawMessage, never back out.
func ScanAllowInsecure(raw []byte) error {
	return scanAllowInsecure(raw, "$", 0)
}

func scanAllowInsecure(raw []byte, path string, depth int) error {
	if depth > maxScanDepth {
		return fmt.Errorf("xray scan: document nested deeper than %d levels (refusing)", maxScanDepth)
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	switch trimmed[0] {
	case '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return fmt.Errorf("xray scan: %s: %w", path, err)
		}
		for key, val := range obj {
			child := path + "." + key
			if strings.EqualFold(key, "allowInsecure") && isTruthyJSON(val) {
				return &ErrAllowInsecure{Path: child}
			}
			if err := scanAllowInsecure(val, child, depth+1); err != nil {
				return err
			}
		}
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return fmt.Errorf("xray scan: %s: %w", path, err)
		}
		for i, val := range arr {
			if err := scanAllowInsecure(val, fmt.Sprintf("%s[%d]", path, i), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// isTruthyJSON accepts every spelling Xray's config loader treats as true for
// a boolean field, so the guard cannot be sidestepped with `1` or `"true"`.
func isTruthyJSON(v json.RawMessage) bool {
	switch string(bytes.TrimSpace(v)) {
	case "true", "1", `"true"`, `"1"`:
		return true
	}
	return false
}
