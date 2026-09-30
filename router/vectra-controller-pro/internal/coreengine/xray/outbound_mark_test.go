package xray_test

import (
	"encoding/json"
	"strings"
	"testing"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
)

// docWithOutbounds wraps raw outbound objects in a minimal but realistic
// provider document (the splice only ever looks at inbounds + outbounds).
func docWithOutbounds(outbounds ...string) []byte {
	return []byte(`{"log":{"loglevel":"warning"},` +
		`"inbounds":[{"tag":"socks","port":10808,"protocol":"socks"}],` +
		`"outbounds":[` + strings.Join(outbounds, ",") + `],` +
		`"routing":{"rules":[]}}`)
}

// dialingOutboundWithMark is a vless outbound that already ships
// streamSettings.sockopt.mark with the given RAW JSON value.
func dialingOutboundWithMark(markJSON string) string {
	return `{"tag":"PROXY","protocol":"vless",` +
		`"settings":{"vnext":[{"address":"pl1.example","port":443}]},` +
		`"streamSettings":{"network":"tcp","tcpSettings":{},"sockopt":{"mark":` + markJSON + `}}}`
}

// TestSpliceRefusesProviderSuppliedSockoptMark is the regression test for the
// "a provider document can switch off its own outbound marking" defect.
//
// injectOutboundMark used to skip any outbound that merely had the sockopt.mark
// KEY, whatever its value. Every value below therefore produced a document with
// OutboundsMarked == 0 that `xray -test` happily accepted, and each one recreates
// the TPROXY egress loop documented on config.DefaultXraySockMark:
//
//   - 0 / null  -> the socket is unmarked, so xray's egress falls through to the
//     output chain's `meta mark set 0x1`, is steered by the fwmark policy route
//     and re-enters TPROXY (the pre-e48235c behaviour);
//   - 1 (the tproxy FwMark) -> nft returns early, but the fwmark policy route
//     still applies to the OUTPUT lookup and resolves `local ... dev lo`;
//   - "1" / false -> not integers at all; the old key-presence check accepted
//     them, and any decode into an int would have silently produced 0.
//
// The mark is a router-side routing fact, not a provider preference, so the only
// acceptable outcome is a REFUSAL of the whole document.
func TestSpliceRefusesProviderSuppliedSockoptMark(t *testing.T) {
	cases := map[string]string{
		"zero (leaves the socket unmarked)":         `0`,
		"tproxy fwmark (loops via the local route)": `1`,
		"null (decodes to 0 without erroring)":      `null`,
		"string instead of number":                  `"1"`,
		"boolean instead of number":                 `false`,
		"float that is not an integer":              `1.5`,
		"object instead of number":                  `{"mark":22084}`,
	}
	for name, markJSON := range cases {
		t.Run(name, func(t *testing.T) {
			in := docWithOutbounds(dialingOutboundWithMark(markJSON))

			out, res, err := xray.SpliceInbounds(in, testTproxy())
			if err == nil {
				t.Fatalf("SpliceInbounds accepted sockopt.mark=%s (OutboundsMarked=%d, %d bytes); "+
					"the document must be refused so the previous good config stays live",
					markJSON, res.OutboundsMarked, len(out))
			}
			// The operator has to be able to act on this without reading the source.
			msg := err.Error()
			for _, want := range []string{"sockopt.mark", "PROXY"} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal must mention %q, got: %v", want, err)
				}
			}
			if !strings.Contains(msg, "22084") {
				t.Errorf("refusal must name the required mark (%d): %v", config.DefaultXraySockMark, err)
			}
		})
	}
}

// The one value that is allowed through: exactly the mark the router requires.
// The outbound is re-emitted BYTE-FOR-BYTE (there is nothing to change) and is
// still counted, so OutboundsMarked keeps meaning "dialling outbounds the nft
// output chain can recognise" rather than "outbounds we happened to rewrite".
func TestSpliceAcceptsCorrectProviderSuppliedSockoptMark(t *testing.T) {
	ob := dialingOutboundWithMark("22084") // config.DefaultXraySockMark
	if config.DefaultXraySockMark != 22084 {
		t.Fatalf("fixture is stale: DefaultXraySockMark = %d", config.DefaultXraySockMark)
	}
	in := docWithOutbounds(ob)

	out, res, err := xray.SpliceInbounds(in, testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds refused the correct mark: %v", err)
	}
	if res.OutboundsMarked != 1 {
		t.Errorf("OutboundsMarked = %d, want 1 (the outbound does carry the mark)", res.OutboundsMarked)
	}
	if !strings.Contains(string(out), ob) {
		t.Errorf("an already-correct outbound must be re-emitted verbatim:\n  want substring: %s\n  got: %s", ob, out)
	}
	// And the mark really is the one nft matches on, not the tproxy fwmark.
	var doc struct {
		Outbounds []struct {
			StreamSettings struct {
				Sockopt struct {
					Mark int `json:"mark"`
				} `json:"sockopt"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal spliced: %v", err)
	}
	if got := doc.Outbounds[0].StreamSettings.Sockopt.Mark; got != config.DefaultXraySockMark {
		t.Errorf("sockopt.mark = %d, want %d", got, config.DefaultXraySockMark)
	}
}

// An outbound WITHOUT any sockopt.mark is still stamped — refusing must not have
// turned into "refuse everything".
func TestSpliceStillStampsOutboundsWithoutAMark(t *testing.T) {
	in := docWithOutbounds(`{"tag":"PROXY","protocol":"vless","streamSettings":{"network":"tcp"}}`)
	out, res, err := xray.SpliceInbounds(in, testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds: %v", err)
	}
	if res.OutboundsMarked != 1 {
		t.Fatalf("OutboundsMarked = %d, want 1", res.OutboundsMarked)
	}
	if !strings.Contains(string(out), `"mark":22084`) {
		t.Errorf("outbound was not stamped:\n%s", out)
	}
}

// Non-dialling outbounds never open a socket, so a mark on them is meaningless
// and cannot create the loop. They are exempt BEFORE the check, deliberately:
// refusing them would reject documents over a field that has no effect.
func TestSpliceIgnoresSockoptMarkOnNonDiallingOutbounds(t *testing.T) {
	for _, protocol := range []string{"blackhole", "loopback"} {
		t.Run(protocol, func(t *testing.T) {
			ob := `{"tag":"X","protocol":"` + protocol + `","streamSettings":{"sockopt":{"mark":0}}}`
			in := docWithOutbounds(ob, `{"tag":"PROXY","protocol":"vless","streamSettings":{}}`)
			out, res, err := xray.SpliceInbounds(in, testTproxy())
			if err != nil {
				t.Fatalf("SpliceInbounds refused a non-dialling outbound: %v", err)
			}
			if res.OutboundsMarked != 1 {
				t.Errorf("OutboundsMarked = %d, want 1 (only the vless outbound dials)", res.OutboundsMarked)
			}
			if !strings.Contains(string(out), ob) {
				t.Errorf("%s outbound must be untouched:\n%s", protocol, out)
			}
		})
	}
}

// A hostile mark anywhere in the array must refuse the WHOLE document, and the
// error must locate it.
func TestSpliceRefusalNamesTheOffendingOutboundIndex(t *testing.T) {
	in := docWithOutbounds(
		`{"tag":"A","protocol":"vless","streamSettings":{}}`,
		`{"tag":"B","protocol":"freedom","streamSettings":{}}`,
		dialingOutboundWithMark("0"),
	)
	_, _, err := xray.SpliceInbounds(in, testTproxy())
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "outbound[2]") {
		t.Errorf("refusal should locate the offending outbound: %v", err)
	}
}
