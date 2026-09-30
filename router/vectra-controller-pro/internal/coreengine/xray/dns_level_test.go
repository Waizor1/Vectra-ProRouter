package xray

import (
	"strings"
	"testing"
)

// The self-check refuses a result whose DNS sessions would not run on the DNS
// level — a policy another writer changed, or a DNS outbound without it.
func TestCheckDNSLevelRefusesAnotherLevel(t *testing.T) {
	ok := `{"inbounds":[{"tag":"tp"},{"tag":"vctl-dns-in","settings":{"userLevel":16}}],
"outbounds":[{"tag":"node"},{"tag":"vctl-dns-out","settings":{"userLevel":16}}],
"policy":{"levels":{"0":{"connIdle":120},"16":{"handshake":4,"connIdle":8,"uplinkOnly":0,"downlinkOnly":0,"bufferSize":0}}}}`
	if err := checkDNSLevel([]byte(ok), 16); err != nil {
		t.Fatalf("a right result refused: %v", err)
	}
	for name, bad := range map[string]string{
		"provider's idle": strings.Replace(ok, `"connIdle":8`, `"connIdle":120`, 1),
		"outbound unset":  strings.Replace(ok, `{"tag":"vctl-dns-out","settings":{"userLevel":16}}`, `{"tag":"vctl-dns-out"}`, 1),
		"inbound level 0": strings.Replace(ok, `{"tag":"vctl-dns-in","settings":{"userLevel":16}}`, `{"tag":"vctl-dns-in","settings":{"userLevel":0}}`, 1),
		"level missing":   strings.Replace(ok, `"16":{`, `"17":{`, 1),
		"extra field":     strings.Replace(ok, `"bufferSize":0}`, `"bufferSize":0,"statsUserUplink":1}`, 1),
	} {
		if err := checkDNSLevel([]byte(bad), 16); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// freeLevel skips every level the document defines or names, at any depth and
// in any spelling xray accepts.
func TestFreeLevelSkipsLevelsInUse(t *testing.T) {
	for doc, want := range map[string]uint32{
		`{}`: 16,
		`{"policy":{"levels":{"16":{},"17":{}}}}`:                                          18,
		`{"outbounds":[{"settings":{"vnext":[{"users":[{"Level":16}]}]}}]}`:                17,
		`{"inbounds":[{"settings":{"UserLevel":16}}],"Policy":{"Levels":{"017":{}}}}`:      18,
		`{"policy":{"levels":{"0":{},"8":{}}},"outbounds":[{"settings":{"userLevel":8}}]}`: 16,
	} {
		if got := freeLevel([]byte(doc)); got != want {
			t.Errorf("freeLevel(%s) = %d, want %d", doc, got, want)
		}
	}
}

// The self-check reads the other levels as xray does — booleans among them —
// and looks for the DNS inbound among the inbounds and the DNS outbound among
// the outbounds only: a provider outbound that happens to carry the inbound's
// tag is not the DNS inbound.
func TestCheckDNSLevelReadsTheRestAsXrayDoes(t *testing.T) {
	doc := `{"inbounds":[{"tag":"tp"},{"tag":"vctl-dns-in","settings":{"userLevel":16}}],
"outbounds":[{"tag":"vctl-dns-in"},{"tag":"node"},{"tag":"vctl-dns-out","settings":{"userLevel":16}}],
"policy":{"levels":{"0":{"connIdle":120,"statsUserUplink":false,"statsUserDownlink":true},"16":{"handshake":4,"connIdle":8,"uplinkOnly":0,"downlinkOnly":0,"bufferSize":0}},"system":{"statsOutboundUplink":true}}}`
	if err := checkDNSLevel([]byte(doc), 16); err != nil {
		t.Fatalf("a right result refused: %v", err)
	}
}

// The splice key names the DNS level: a router that upgrades re-renders its
// installed config at start (reconcileRender compares the keys), so the level
// reaches it before the provider's next change (review of r35).
func TestTheSpliceKeyNamesTheDNSLevel(t *testing.T) {
	o := &DNSOptions{Listen: DefaultDNSListen, DirectResolvers: DefaultDirectResolvers, IPv4Only: true}
	r34 := ";dns=" + o.Listen + "|" + strings.Join(o.DirectResolvers, ",") + "|" + strings.Join(o.DirectDomains, ",") + "|v4"
	if o.key() == r34 {
		t.Fatalf("the key is r34's: %q", o.key())
	}
}
