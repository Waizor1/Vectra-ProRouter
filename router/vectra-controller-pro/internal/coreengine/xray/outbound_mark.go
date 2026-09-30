package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// OutboundsKey is the second top-level key the splice rewrites.
const OutboundsKey = "outbounds"

// markExemptProtocols never open a socket towards the network, so a fwmark on
// them would be meaningless: "loopback" re-injects into xray's own routing and
// "blackhole" drops.
var markExemptProtocols = map[string]bool{
	"loopback":  true,
	"blackhole": true,
}

// injectOutboundMark stamps sockopt.mark onto every outbound that actually
// dials out, and reports how many it touched.
//
// This is the SECOND deliberate modification of the provider document (the
// first being the inbounds replacement), and it is required for the router
// case specifically.
//
// The nft output chain exempts the router's own egress with a single rule —
// `meta mark 0x<fwmark> return`. Xray's outbound sockets must therefore carry
// that mark themselves. The provider ships zero sockopt and zero mark across
// all of its outbounds (measured: 0 of 29), because its documents target
// client apps where nothing re-captures local egress. Left alone on a router,
// xray's own outbound traffic falls through to the marking rule at the bottom
// of the output chain, gets steered by the fwmark policy route into the local
// TPROXY socket, and arrives back at xray: a loop that presents as outbound
// connections dying with "io: read/write on closed pipe".
//
// This does not alter the provider's routing semantics in any way — sockopt is
// a transport-level socket option, invisible to routing rules, DNS and the
// observatory. Every other byte of every outbound is re-emitted verbatim, so
// the corruption class this package exists to prevent (an empty object being
// re-serialized as an empty array) is unaffected.
func injectOutboundMark(raw json.RawMessage, mark int) (json.RawMessage, int, error) {
	if mark == 0 {
		return raw, 0, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, 0, fmt.Errorf("xray splice: outbounds must be an array: %w", err)
	}

	var out bytes.Buffer
	out.WriteByte('[')
	touched := 0
	for i, ob := range arr {
		if i > 0 {
			out.WriteByte(',')
		}
		rewritten, changed, err := markOneOutbound(ob, mark)
		if err != nil {
			return nil, 0, fmt.Errorf("xray splice: outbound[%d]: %w", i, err)
		}
		if changed {
			touched++
		}
		out.Write(rewritten)
	}
	out.WriteByte(']')
	return out.Bytes(), touched, nil
}

func markOneOutbound(ob json.RawMessage, mark int) (json.RawMessage, bool, error) {
	var probe struct {
		Protocol string `json:"protocol"`
		Tag      string `json:"tag"`
	}
	if err := json.Unmarshal(ob, &probe); err != nil {
		return nil, false, fmt.Errorf("read protocol: %w", err)
	}
	if markExemptProtocols[probe.Protocol] {
		return ob, false, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(ob, &fields); err != nil {
		return nil, false, fmt.Errorf("read object: %w", err)
	}

	stream := map[string]json.RawMessage{}
	if existing, ok := fields["streamSettings"]; ok {
		if err := json.Unmarshal(existing, &stream); err != nil {
			return nil, false, fmt.Errorf("read streamSettings: %w", err)
		}
	}
	sockopt := map[string]json.RawMessage{}
	if existing, ok := stream["sockopt"]; ok {
		if err := json.Unmarshal(existing, &sockopt); err != nil {
			return nil, false, fmt.Errorf("read sockopt: %w", err)
		}
	}
	// A provider-supplied mark is REFUSED unless it is already exactly the mark
	// the router requires.
	//
	// This used to skip any outbound that merely had the "mark" KEY, reasoning
	// that "an operator-set mark wins". That was wrong on both counts. The
	// provider is not the operator — there is no operator anywhere on this path,
	// the document arrives straight off the subscription URL — and the mark is
	// not a preference at all: it is a router-side ROUTING FACT that the nft
	// output chain matches on. Key-presence-only meant a document could switch
	// its own marking off with any value and still pass `xray -test`:
	//
	//	{"mark":0} / {"mark":null} — socket unmarked, so xray's egress falls
	//	  through to the output chain's `meta mark set 0x<fwmark>`, gets steered
	//	  by the fwmark policy route and comes back into TPROXY;
	//	{"mark":1} (== FwMark) — returns early in nft, but the policy route still
	//	  applies to the OUTPUT lookup and resolves `local ... dev lo` anyway.
	//
	// Both are the loop documented on config.DefaultXraySockMark (85,150 packets
	// / 381 MiB RSS). Refusing fails CLOSED: apply.Apply propagates the error
	// without writing, so the previous good config stays live.
	if existing, already := sockopt["mark"]; already {
		got, ok := numericMark(existing)
		if !ok {
			return nil, false, fmt.Errorf(
				"provider set streamSettings.sockopt.mark to %s on outbound %q, which is not an integer; "+
					"the socket mark is a router routing fact and must be exactly %d (0x%x) — refusing the document",
				compactJSON(existing), probe.Tag, mark, mark)
		}
		if got != mark {
			return nil, false, fmt.Errorf(
				"provider set streamSettings.sockopt.mark to %d on outbound %q, but the router requires exactly %d (0x%x); "+
					"any other value un-marks xray's own egress and loops it back into TPROXY — refusing the document",
				got, probe.Tag, mark, mark)
		}
		// Already exactly right: re-emit the provider's bytes untouched, but
		// still count it as carrying the mark so OutboundsMarked keeps meaning
		// "dialling outbounds the nft output chain can recognise".
		return ob, true, nil
	}
	sockopt["mark"] = json.RawMessage(fmt.Sprintf("%d", mark))

	encodedSockopt, err := json.Marshal(sockopt)
	if err != nil {
		return nil, false, err
	}
	stream["sockopt"] = encodedSockopt
	encodedStream, err := json.Marshal(stream)
	if err != nil {
		return nil, false, err
	}

	// Re-emit the outbound's own keys in their original order, replacing only
	// streamSettings; append it when the outbound had none (freedom/DIRECT).
	return rewriteObjectField(ob, "streamSettings", encodedStream)
}

// numericMark decodes a sockopt.mark value as an integer. It reports false for
// everything that is not a JSON integer — `null`, `"1"`, `false`, `1.5`,
// objects, arrays — rather than letting Go's zero-value decoding turn any of
// them into a silent 0. `null` in particular unmarshals into an int without
// error, which is precisely how a document could disable its own marking.
func numericMark(raw json.RawMessage) (int, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return 0, false
	}
	num, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := num.Int64()
	if err != nil {
		return 0, false
	}
	return int(n), true
}

// compactJSON renders a raw value for an error message, bounded so a hostile
// document cannot turn a log line into a payload.
func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "<unreadable>"
	}
	s := buf.String()
	if len(s) > 64 {
		s = s[:64] + "…"
	}
	return s
}

// rewriteObjectField re-emits a JSON object with one field replaced (or
// appended if absent), keeping every other field's bytes and the original key
// order intact.
func rewriteObjectField(obj json.RawMessage, field string, value json.RawMessage) (json.RawMessage, bool, error) {
	dec := json.NewDecoder(bytes.NewReader(obj))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, false, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, false, fmt.Errorf("expected object, got %v", tok)
	}

	var out bytes.Buffer
	out.WriteByte('{')
	first := true
	replaced := false
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, false, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, false, fmt.Errorf("non-string key %v", keyTok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, false, err
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		writeJSONString(&out, key)
		out.WriteByte(':')
		if key == field {
			out.Write(value)
			replaced = true
			continue
		}
		out.Write(raw)
	}
	if !replaced {
		if !first {
			out.WriteByte(',')
		}
		writeJSONString(&out, field)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), true, nil
}
