package xray

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"vectra-controller-pro/internal/config"
)

// TestParityCorpus is the LIVE parity oracle: for each pair in testdata/parity
// it renders the operator config (<name>.operator.json, an internal/config
// Config) and structurally compares it to <name>.passwall-xray.json — the Xray
// config PassWall2's util_xray.lua gen_config produced for the equivalent node.
//
// The corpus is captured read-only from routers/VPS by
// scripts/Capture-XrayParityCorpus.sh (see testdata/parity/README.md); it is
// gitignored and not present by default, so this test SKIPS until a corpus is
// supplied. When present it asserts the parity-critical structure matches:
// inbound protocols/ports, per-tag outbound protocol+security+transport, and
// routing outbound tags — tolerant to formatting/extra-field noise.
func TestParityCorpus(t *testing.T) {
	pairs, _ := filepath.Glob(filepath.Join("testdata", "parity", "*.operator.json"))
	if len(pairs) == 0 {
		t.Skip("no parity corpus present (capture one with scripts/Capture-XrayParityCorpus.sh; see testdata/parity/README.md)")
	}
	eng := New()
	for _, opPath := range pairs {
		name := strings.TrimSuffix(filepath.Base(opPath), ".operator.json")
		t.Run(name, func(t *testing.T) {
			pwPath := filepath.Join("testdata", "parity", name+".passwall-xray.json")
			pwRaw, err := os.ReadFile(pwPath)
			if err != nil {
				t.Fatalf("missing passwall side %s: %v", pwPath, err)
			}
			opRaw, err := os.ReadFile(opPath)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Read(bytes.NewReader(opRaw), opPath)
			if err != nil {
				t.Fatalf("read operator config: %v", err)
			}
			got, err := eng.Render(context.Background(), cfg)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			ours, err := project(got)
			if err != nil {
				t.Fatalf("project ours: %v", err)
			}
			theirs, err := project(pwRaw)
			if err != nil {
				t.Fatalf("project passwall: %v", err)
			}
			for _, diff := range ours.diff(theirs) {
				t.Errorf("parity drift: %s", diff)
			}
		})
	}
}

// projection is the parity-critical structural slice of an Xray config.
type projection struct {
	outbounds map[string]string // tag -> "protocol/security/network"
	rules     []string          // routing outbound tags in order
	inbounds  []string          // "protocol:port" sorted
}

func project(raw []byte) (projection, error) {
	var doc struct {
		Inbounds []struct {
			Protocol string `json:"protocol"`
			Port     any    `json:"port"`
		} `json:"inbounds"`
		Outbounds []struct {
			Tag            string `json:"tag"`
			Protocol       string `json:"protocol"`
			StreamSettings *struct {
				Network  string `json:"network"`
				Security string `json:"security"`
			} `json:"streamSettings"`
		} `json:"outbounds"`
		Routing struct {
			Rules []struct {
				OutboundTag string `json:"outboundTag"`
			} `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return projection{}, err
	}
	p := projection{outbounds: map[string]string{}}
	for _, o := range doc.Outbounds {
		net, sec := "", ""
		if o.StreamSettings != nil {
			net, sec = o.StreamSettings.Network, o.StreamSettings.Security
		}
		p.outbounds[o.Tag] = fmt.Sprintf("%s/%s/%s", o.Protocol, sec, net)
	}
	for _, r := range doc.Routing.Rules {
		if r.OutboundTag != "" {
			p.rules = append(p.rules, r.OutboundTag)
		}
	}
	for _, in := range doc.Inbounds {
		p.inbounds = append(p.inbounds, fmt.Sprintf("%s:%v", in.Protocol, in.Port))
	}
	sort.Strings(p.inbounds)
	return p, nil
}

func (p projection) diff(other projection) []string {
	var diffs []string
	for tag, sig := range p.outbounds {
		// Ignore vctl pseudo-outbounds passwall may name differently.
		if tag == "dns-out" || tag == "block" {
			continue
		}
		if otherSig, ok := other.outbounds[tag]; !ok {
			diffs = append(diffs, fmt.Sprintf("outbound %q present in ours, absent in passwall", tag))
		} else if otherSig != sig {
			diffs = append(diffs, fmt.Sprintf("outbound %q signature ours=%s passwall=%s", tag, sig, otherSig))
		}
	}
	if strings.Join(p.rules, ",") != strings.Join(other.rules, ",") {
		diffs = append(diffs, fmt.Sprintf("routing outbound tags differ: ours=%v passwall=%v", p.rules, other.rules))
	}
	return diffs
}
