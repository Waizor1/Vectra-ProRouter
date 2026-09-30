package xray_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
)

const fixturePath = "testdata/provider/entry-00.json"

func providerFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(fixturePath))
	if err != nil {
		t.Fatalf("read provider fixture: %v", err)
	}
	return raw
}

func testTproxy() *config.TproxyInbound {
	return &config.TproxyInbound{
		ListenIP:   "0.0.0.0",
		Port:       12345,
		FwMark:     1,
		UDPEnabled: true,
		Tag:        "tproxy-in",
		Sniffing: config.Sniffing{
			Enabled:      true,
			DestOverride: []string{"http", "tls", "quic"},
			RouteOnly:    false,
		},
	}
}

// orderedKeys decodes the top-level object keys in document order.
func orderedKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		t.Fatalf("not an object: %v", tok)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatalf("key token: %v", err)
		}
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip value: %v", err)
		}
	}
	return keys
}

// topLevel returns key -> compacted value bytes.
func topLevel(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal top level: %v", err)
	}
	out := map[string][]byte{}
	for k, v := range m {
		var buf bytes.Buffer
		if err := json.Compact(&buf, v); err != nil {
			t.Fatalf("compact %s: %v", k, err)
		}
		out[k] = buf.Bytes()
	}
	return out
}

// TestSplicePreservesEveryByteExceptInboundsAndOutbounds is the core contract.
//
// Exactly TWO top-level keys may change, and both changes are deliberate:
//   - "inbounds" is replaced by the controller's TPROXY inbound;
//   - "outbounds" gets sockopt.mark stamped on every dialling outbound, which
//     the nft output chain needs to tell xray's own egress from client traffic
//     (without it the router's egress loops back into TPROXY — see
//     injectOutboundMark).
//
// Every other key keeps its exact bytes and the key ORDER is preserved.
// TestOutboundMarkIsTheOnlyOutboundChange pins down what may differ inside
// "outbounds"; this test deliberately does not weaken to "outbounds may be
// anything".
func TestSplicePreservesEveryByteExceptInboundsAndOutbounds(t *testing.T) {
	in := providerFixture(t)
	out, res, err := xray.SpliceInbounds(in, testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds: %v", err)
	}

	inKeys := orderedKeys(t, in)
	outKeys := orderedKeys(t, out)
	if len(inKeys) != len(outKeys) {
		t.Fatalf("key count changed: %v -> %v", inKeys, outKeys)
	}
	for i := range inKeys {
		if inKeys[i] != outKeys[i] {
			t.Fatalf("key order changed at %d: %v -> %v", i, inKeys, outKeys)
		}
	}
	if got, want := strings.Join(res.TopLevelKeys, ","), strings.Join(inKeys, ","); got != want {
		t.Errorf("SpliceResult.TopLevelKeys = %s, want %s", got, want)
	}

	before, after := topLevel(t, in), topLevel(t, out)
	for k, want := range before {
		if k == xray.InboundsKey || k == xray.OutboundsKey {
			continue
		}
		if !bytes.Equal(after[k], want) {
			t.Errorf("top-level key %q was modified:\n  before: %s\n   after: %s", k, want, after[k])
		}
	}

	// Sanity: the fixture really is the shape we claim to test against.
	for _, k := range []string{"burstObservatory", "dns", "log", "outbounds", "policy", "remarks", "routing", "stats"} {
		if _, ok := before[k]; !ok {
			t.Errorf("fixture is missing top-level key %q", k)
		}
	}
}

// TestSplicePreservesNonAlphabeticalKeyOrder proves order preservation is real
// and not an artifact of the fixture's (alphabetical) provider ordering.
func TestSplicePreservesNonAlphabeticalKeyOrder(t *testing.T) {
	in := []byte(`{"zulu":1,"log":{"loglevel":"warning"},"inbounds":[],"alpha":{"nested":{}},"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`)
	out, _, err := xray.SpliceInbounds(in, testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds: %v", err)
	}
	want := []string{"zulu", "log", "inbounds", "alpha", "outbounds"}
	got := orderedKeys(t, out)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("key order = %v, want %v", got, want)
	}
}

// TestEmptyObjectsSurvive is the direct regression test for the corruption
// that killed the earlier Lua attempt: `{}` re-serialized as `[]`, after which
// Xray died with "cannot unmarshal array into Go struct field ... tcpSettings".
func TestEmptyObjectsSurvive(t *testing.T) {
	in := providerFixture(t)

	wantTCP := bytes.Count(in, []byte(`"tcpSettings":{}`)) + bytes.Count(in, []byte(`"tcpSettings": {}`))
	if wantTCP == 0 {
		t.Fatal("fixture must contain at least one empty tcpSettings object")
	}
	if !bytes.Contains(in, []byte(`"stats": {}`)) && !bytes.Contains(in, []byte(`"stats":{}`)) {
		t.Fatal("fixture must contain an empty stats object")
	}

	out, _, err := xray.SpliceInbounds(in, testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds: %v", err)
	}

	gotTCP := bytes.Count(out, []byte(`"tcpSettings":{}`)) + bytes.Count(out, []byte(`"tcpSettings": {}`))
	if gotTCP != wantTCP {
		t.Errorf("empty tcpSettings objects: got %d, want %d", gotTCP, wantTCP)
	}
	if bytes.Contains(out, []byte(`"tcpSettings":[]`)) || bytes.Contains(out, []byte(`"tcpSettings": []`)) {
		t.Error("REGRESSION: tcpSettings became an empty ARRAY (xray will refuse to start)")
	}
	if !bytes.Contains(out, []byte(`"stats": {}`)) && !bytes.Contains(out, []byte(`"stats":{}`)) {
		t.Error("REGRESSION: empty stats object did not survive")
	}
	if bytes.Contains(out, []byte(`"stats":[]`)) || bytes.Contains(out, []byte(`"stats": []`)) {
		t.Error("REGRESSION: stats became an empty ARRAY")
	}

	// Nothing anywhere may have flipped from an empty object to an empty array.
	if n := countEmptyArrays(t, out) - countEmptyArrays(t, in); n != 0 {
		t.Errorf("empty-array count changed by %d (any increase means an empty object was corrupted)", n)
	}
}

// countEmptyArrays counts `:[]` occurrences after compaction, so whitespace
// differences between input and output do not skew the comparison.
func countEmptyArrays(t *testing.T, raw []byte) int {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatalf("compact: %v", err)
	}
	// The spliced doc replaces the provider inbounds array, so ignore that key.
	return bytes.Count(buf.Bytes(), []byte(`:[]`))
}

// TestTproxyInboundReplacesSocksHttp: the provider's socks/http inbounds are
// gone and exactly one correctly-shaped tproxy inbound is present.
func TestTproxyInboundReplacesSocksHttp(t *testing.T) {
	out, res, err := xray.SpliceInbounds(providerFixture(t), testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds: %v", err)
	}
	if len(res.DroppedInbounds) != 2 {
		t.Errorf("expected 2 dropped provider inbounds, got %v", res.DroppedInbounds)
	}

	var doc struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Listen   string `json:"listen"`
			Port     int    `json:"port"`
			Protocol string `json:"protocol"`
			Settings struct {
				Network        string `json:"network"`
				FollowRedirect bool   `json:"followRedirect"`
			} `json:"settings"`
			StreamSettings struct {
				Sockopt struct {
					Mark   int    `json:"mark"`
					TProxy string `json:"tproxy"`
				} `json:"sockopt"`
			} `json:"streamSettings"`
			Sniffing struct {
				Enabled      bool     `json:"enabled"`
				DestOverride []string `json:"destOverride"`
				RouteOnly    bool     `json:"routeOnly"`
			} `json:"sniffing"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal spliced: %v", err)
	}
	if len(doc.Inbounds) != 1 {
		t.Fatalf("expected exactly 1 inbound, got %d", len(doc.Inbounds))
	}
	ib := doc.Inbounds[0]
	for _, p := range []string{"socks", "http"} {
		if ib.Protocol == p {
			t.Fatalf("provider %s inbound survived the splice", p)
		}
	}
	if ib.Protocol != "dokodemo-door" {
		t.Errorf("protocol = %q, want dokodemo-door", ib.Protocol)
	}
	if ib.Tag != "tproxy-in" || ib.Port != 12345 {
		t.Errorf("tag/port = %q/%d, want tproxy-in/12345", ib.Tag, ib.Port)
	}
	if ib.Settings.Network != "tcp,udp" || !ib.Settings.FollowRedirect {
		t.Errorf("settings = %+v, want network=tcp,udp followRedirect=true", ib.Settings)
	}
	// The inbound's socket mark is the xray sock mark, NOT the tproxy fwmark:
	// a reply carrying the fwmark resolves to the `local ... dev lo` policy
	// route and loops back into this same socket instead of reaching the client.
	if ib.StreamSettings.Sockopt.TProxy != "tproxy" ||
		ib.StreamSettings.Sockopt.Mark != config.DefaultXraySockMark {
		t.Errorf("sockopt = %+v, want tproxy=tproxy mark=%d",
			ib.StreamSettings.Sockopt, config.DefaultXraySockMark)
	}
	if ib.StreamSettings.Sockopt.Mark == testTproxy().FwMark {
		t.Error("inbound socket mark must never equal the tproxy fwmark")
	}
	if !ib.Sniffing.Enabled || ib.Sniffing.RouteOnly {
		t.Errorf("sniffing = %+v, want enabled=true routeOnly=false", ib.Sniffing)
	}
	if strings.Join(ib.Sniffing.DestOverride, ",") != "http,tls,quic" {
		t.Errorf("destOverride = %v, want [http tls quic]", ib.Sniffing.DestOverride)
	}
	// The provider config has no fakedns block; sniffing into a pool that does
	// not exist is a hard start failure.
	for _, d := range ib.Sniffing.DestOverride {
		if d == "fakedns" {
			t.Error("destOverride must not contain fakedns (provider config has no fakedns block)")
		}
	}
}

// TestNoOpenProxyInbound: nothing in the installed config listens on a
// non-loopback address other than the TPROXY inbound, which is firewall-gated.
func TestNoOpenProxyInbound(t *testing.T) {
	out, _, err := xray.SpliceInbounds(providerFixture(t), testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds: %v", err)
	}
	var doc struct {
		Inbounds []struct {
			Tag      string `json:"tag"`
			Listen   string `json:"listen"`
			Protocol string `json:"protocol"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, ib := range doc.Inbounds {
		switch ib.Protocol {
		case "socks", "http", "shadowsocks", "vless", "vmess", "trojan":
			// A generic proxy inbound MUST be loopback-bound or absent.
			if ib.Listen != "127.0.0.1" && ib.Listen != "::1" {
				t.Errorf("open proxy: inbound %q (%s) listens on %q", ib.Tag, ib.Protocol, ib.Listen)
			}
		case "dokodemo-door":
			// TPROXY is expected on 0.0.0.0 — it is unreachable without the
			// kernel TPROXY target, which only the vctl firewall installs.
		default:
			t.Errorf("unexpected inbound protocol %q survived the splice", ib.Protocol)
		}
	}
}

func TestSpliceAppendsInboundsWhenProviderHasNone(t *testing.T) {
	in := []byte(`{"log":{"loglevel":"warning"},"outbounds":[{"tag":"DIRECT","protocol":"freedom"}]}`)
	out, res, err := xray.SpliceInbounds(in, testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds: %v", err)
	}
	if res.InboundsReplaced {
		t.Error("InboundsReplaced should be false when the provider had no inbounds key")
	}
	keys := orderedKeys(t, out)
	if keys[len(keys)-1] != "inbounds" {
		t.Fatalf("expected inbounds appended last, got %v", keys)
	}
}

func TestSpliceRefusesMalformedInput(t *testing.T) {
	full := providerFixture(t)
	cases := map[string][]byte{
		"truncated mid-document": full[:len(full)/2],
		"truncated at one byte":  full[:1],
		"empty":                  nil,
		"not an object":          []byte(`[{"log":{}}]`),
		"trailing garbage":       append(append([]byte{}, full...), []byte(`{"extra":1}`)...),
		"non-array inbounds":     []byte(`{"inbounds":{"tag":"x"},"outbounds":[]}`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := xray.SpliceInbounds(raw, testTproxy()); err == nil {
				t.Fatal("expected SpliceInbounds to refuse, got nil error")
			}
		})
	}
}

func TestSpliceRefusesBadTproxy(t *testing.T) {
	in := providerFixture(t)
	if _, _, err := xray.SpliceInbounds(in, nil); err == nil {
		t.Error("expected refusal on nil tproxy inbound")
	}
	bad := testTproxy()
	bad.Port = 0
	if _, _, err := xray.SpliceInbounds(in, bad); err == nil {
		t.Error("expected refusal on port 0")
	}
}

func TestSpliceOutputIsValidJSON(t *testing.T) {
	out, _, err := xray.SpliceInbounds(providerFixture(t), testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds: %v", err)
	}
	if !json.Valid(out) {
		t.Fatal("spliced document is not valid JSON")
	}
}

// TestOutboundMarkIsTheOnlyOutboundChange pins down the second deliberate
// modification. The splice may add sockopt.mark to dialling outbounds and
// nothing else: same count, same tags in the same order, same protocols, and
// every field other than streamSettings.sockopt byte-identical.
//
// Non-dialling outbounds (loopback re-injects into xray's own routing,
// blackhole drops) must come through completely untouched — a mark there is
// meaningless and would be noise in the diff an operator reviews.
func TestOutboundMarkIsTheOnlyOutboundChange(t *testing.T) {
	in := providerFixture(t)
	out, res, err := xray.SpliceInbounds(in, testTproxy())
	if err != nil {
		t.Fatalf("SpliceInbounds: %v", err)
	}

	type outbound struct {
		Tag            string                     `json:"tag"`
		Protocol       string                     `json:"protocol"`
		StreamSettings map[string]json.RawMessage `json:"streamSettings"`
	}
	decode := func(doc []byte) ([]outbound, []map[string]json.RawMessage) {
		t.Helper()
		var top map[string]json.RawMessage
		if err := json.Unmarshal(doc, &top); err != nil {
			t.Fatalf("unmarshal document: %v", err)
		}
		var typed []outbound
		if err := json.Unmarshal(top[xray.OutboundsKey], &typed); err != nil {
			t.Fatalf("unmarshal outbounds: %v", err)
		}
		var raw []map[string]json.RawMessage
		if err := json.Unmarshal(top[xray.OutboundsKey], &raw); err != nil {
			t.Fatalf("unmarshal raw outbounds: %v", err)
		}
		return typed, raw
	}

	beforeTyped, beforeRaw := decode(in)
	afterTyped, afterRaw := decode(out)

	if len(beforeTyped) != len(afterTyped) {
		t.Fatalf("outbound count changed: %d -> %d", len(beforeTyped), len(afterTyped))
	}

	exempt := map[string]bool{"loopback": true, "blackhole": true}
	marked := 0
	for i := range beforeTyped {
		b, a := beforeTyped[i], afterTyped[i]
		if b.Tag != a.Tag || b.Protocol != a.Protocol {
			t.Fatalf("outbound[%d] identity changed: %s/%s -> %s/%s", i, b.Tag, b.Protocol, a.Tag, a.Protocol)
		}

		if exempt[b.Protocol] {
			if !bytes.Equal(mustMarshal(t, beforeRaw[i]), mustMarshal(t, afterRaw[i])) {
				t.Errorf("outbound[%d] %s (%s) must be untouched", i, b.Tag, b.Protocol)
			}
			continue
		}

		var sockopt struct {
			Mark int `json:"mark"`
		}
		if err := json.Unmarshal(a.StreamSettings["sockopt"], &sockopt); err != nil {
			t.Fatalf("outbound[%d] %s: no usable sockopt: %v", i, a.Tag, err)
		}
		if sockopt.Mark != config.DefaultXraySockMark {
			t.Errorf("outbound[%d] %s: mark = %d, want %d", i, a.Tag, sockopt.Mark, config.DefaultXraySockMark)
		}
		// Stamping the tproxy fwmark here makes xray's egress unroutable (the
		// fwmark policy route sends it to `local ... dev lo`) and loops it back
		// into TPROXY. Measured on the data-plane stand: 85,150 packets and
		// 381 MiB RSS for a handful of client requests.
		if sockopt.Mark == testTproxy().FwMark {
			t.Fatalf("outbound[%d] %s: sockopt.mark must never equal the tproxy fwmark", i, a.Tag)
		}
		marked++

		// Everything except streamSettings.sockopt must survive unchanged.
		strippedBefore := stripSockopt(t, beforeRaw[i])
		strippedAfter := stripSockopt(t, afterRaw[i])
		if !bytes.Equal(strippedBefore, strippedAfter) {
			t.Errorf("outbound[%d] %s changed beyond sockopt:\n  before: %s\n   after: %s",
				i, a.Tag, strippedBefore, strippedAfter)
		}
	}

	if res.OutboundsMarked != marked {
		t.Errorf("SpliceResult.OutboundsMarked = %d, want %d", res.OutboundsMarked, marked)
	}
	if marked == 0 {
		t.Fatal("fixture exercised nothing: no dialling outbound was marked")
	}

	// The corruption class this package exists to prevent must not reappear
	// via the outbound rewrite.
	if n := bytes.Count(out, []byte(":[]")); n != 0 {
		t.Errorf("spliced document contains %d empty arrays; provider ships none", n)
	}
	if bytes.Count(out, []byte(`"tcpSettings":{}`)) != bytes.Count(in, []byte(`"tcpSettings":{}`)) {
		t.Error(`"tcpSettings":{} count changed across the splice`)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// stripSockopt removes streamSettings.sockopt so the rest of the outbound can
// be compared for byte-equality regardless of key ordering.
func stripSockopt(t *testing.T, ob map[string]json.RawMessage) []byte {
	t.Helper()
	clone := map[string]json.RawMessage{}
	for k, v := range ob {
		clone[k] = v
	}
	if rawStream, ok := clone["streamSettings"]; ok {
		stream := map[string]json.RawMessage{}
		if err := json.Unmarshal(rawStream, &stream); err != nil {
			t.Fatalf("unmarshal streamSettings: %v", err)
		}
		delete(stream, "sockopt")
		if len(stream) == 0 {
			delete(clone, "streamSettings")
		} else {
			clone["streamSettings"] = mustMarshal(t, stream)
		}
	}
	return mustMarshal(t, clone)
}

// The fixture is a real capture, committed to a public repository. Every value
// under a credential key must be a placeholder: the first scrub missed the
// hysteria2 "auth" and the xhttp "path", and this is what would have caught it.
func TestProviderFixtureCarriesNoCredential(t *testing.T) {
	var doc interface{}
	if err := json.Unmarshal(providerFixture(t), &doc); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]map[string]bool{
		"id":       {"00000000-0000-4000-8000-000000000000": true},
		"auth":     {"00000000-0000-4000-8000-000000000000": true, "noauth": true},
		"shortId":  {"00000000000000": true, "": true},
		"password": {"REDACTED": true},
		"path":     {"/static/redacted": true},
	}
	var walk func(x interface{})
	walk = func(x interface{}) {
		switch y := x.(type) {
		case map[string]interface{}:
			for k, v := range y {
				if s, ok := v.(string); ok {
					if ok := allowed[k]; ok != nil && !ok[s] {
						t.Errorf("fixture key %q holds a non-placeholder value (%d chars)", k, len(s))
					}
				}
				walk(v)
			}
		case []interface{}:
			for _, e := range y {
				walk(e)
			}
		}
	}
	walk(doc)
}
