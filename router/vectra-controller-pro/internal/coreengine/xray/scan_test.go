package xray_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
)

// The fixture is a real capture from the production subscription. Measured
// 2026-08-05: the provider sets allowInsecure nowhere at all — not even
// explicitly false. Lock that in: if the provider ever starts emitting the key
// we want this test to fail loudly, because the scanner refusing an apply
// fleet-wide is a very different incident from it never firing.
//
// The scanner's actual refusal logic is exercised by
// TestScanAllowInsecure_Refuses below, which uses crafted inputs rather than
// depending on the capture to contain a particular key.
func TestScanAllowInsecure_CleanFixturePasses(t *testing.T) {
	raw := providerFixture(t)
	if bytes.Contains(bytes.ToLower(raw), []byte(`"allowinsecure"`)) {
		t.Fatal("provider capture now contains allowInsecure; re-check the security posture before updating this fixture")
	}
	if err := xray.ScanAllowInsecure(raw); err != nil {
		t.Fatalf("clean provider config must pass: %v", err)
	}
}

func TestScanAllowInsecure_Refuses(t *testing.T) {
	cases := map[string]string{
		"bool true":       `{"outbounds":[{"streamSettings":{"tlsSettings":{"allowInsecure":true}}}]}`,
		"numeric 1":       `{"outbounds":[{"streamSettings":{"tlsSettings":{"allowInsecure":1}}}]}`,
		"string true":     `{"outbounds":[{"streamSettings":{"tlsSettings":{"allowInsecure":"true"}}}]}`,
		"case-insensitve": `{"outbounds":[{"streamSettings":{"tlsSettings":{"allowinsecure":true}}}]}`,
		"deep in array":   `{"outbounds":[{"a":1},{"b":[{"c":{"allowInsecure":true}}]}]}`,
		"top level":       `{"allowInsecure":true}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			err := xray.ScanAllowInsecure([]byte(doc))
			if err == nil {
				t.Fatal("expected refusal")
			}
			var target *xray.ErrAllowInsecure
			if !errors.As(err, &target) {
				t.Fatalf("expected *ErrAllowInsecure, got %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), "allowInsecure") {
				t.Errorf("error should name the offending field: %v", err)
			}
		})
	}
}

func TestScanAllowInsecure_AllowsFalseAndAbsent(t *testing.T) {
	for name, doc := range map[string]string{
		"false":  `{"outbounds":[{"streamSettings":{"tlsSettings":{"allowInsecure":false}}}]}`,
		"zero":   `{"outbounds":[{"streamSettings":{"tlsSettings":{"allowInsecure":0}}}]}`,
		"absent": `{"outbounds":[{"streamSettings":{"tlsSettings":{"serverName":"x"}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := xray.ScanAllowInsecure([]byte(doc)); err != nil {
				t.Fatalf("must pass: %v", err)
			}
		})
	}
}

// The scanner must never rewrite the document it inspects.
func TestScanAllowInsecure_DoesNotMutate(t *testing.T) {
	raw := providerFixture(t)
	before := append([]byte(nil), raw...)
	_ = xray.ScanAllowInsecure(raw)
	if !bytes.Equal(before, raw) {
		t.Fatal("ScanAllowInsecure mutated its input")
	}
}
