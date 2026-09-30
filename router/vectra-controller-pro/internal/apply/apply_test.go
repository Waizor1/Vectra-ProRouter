package apply_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/coreengine/xray"
)

func providerFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "coreengine", "xray", "testdata", "provider", "entry-00.json"))
	if err != nil {
		t.Fatalf("read provider fixture: %v", err)
	}
	return raw
}

func tproxy() *config.TproxyInbound {
	return &config.TproxyInbound{
		ListenIP: "0.0.0.0", Port: 12345, FwMark: 1, UDPEnabled: true, Tag: "tproxy-in",
		Sniffing: config.Sniffing{Enabled: true, DestOverride: []string{"http", "tls", "quic"}},
	}
}

// stubValidator stands in for `xray run -test`.
type stubValidator struct {
	err    error
	called int
	seen   []byte
}

func (s *stubValidator) Test(_ context.Context, candidate []byte) error {
	s.called++
	s.seen = append([]byte(nil), candidate...)
	return s.err
}

type harness struct {
	a         *apply.Applier
	validator *stubValidator
	dir       string
	written   [][]byte
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{dir: t.TempDir(), validator: &stubValidator{}}
	h.a = &apply.Applier{
		Tproxy:       tproxy(),
		ProviderPath: filepath.Join(h.dir, "provider-config.json"),
		WriteXray: func(data []byte) error {
			h.written = append(h.written, append([]byte(nil), data...))
			return nil
		},
		Validate: h.validator,
	}
	return h
}

func TestApplyInstallsProviderDocument(t *testing.T) {
	h := newHarness(t)
	raw := providerFixture(t)

	res, err := h.a.Apply(context.Background(), raw, "", false)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Changed || res.Noop {
		t.Fatalf("expected a change on first apply: %+v", res)
	}
	if res.DesiredDigest != apply.Digest(raw) {
		t.Errorf("digest must be sha256 of the provider bytes directly")
	}
	if res.AppliedDigest != res.DesiredDigest {
		t.Errorf("digests differ: %+v", res)
	}
	if len(h.written) != 1 || !json.Valid(h.written[0]) {
		t.Fatalf("xray.json not written or invalid (%d writes)", len(h.written))
	}
	if h.validator.called != 1 {
		t.Errorf("xray -test must run exactly once, ran %d times", h.validator.called)
	}

	// The provider document must be persisted VERBATIM — no trailing newline,
	// no re-indent, or the digest changes on the next boot.
	onDisk, err := os.ReadFile(h.a.ProviderPath)
	if err != nil {
		t.Fatalf("provider config not persisted: %v", err)
	}
	if string(onDisk) != string(raw) {
		t.Errorf("persisted provider document is not byte-identical (%d vs %d bytes)", len(onDisk), len(raw))
	}
	if apply.Digest(onDisk) != res.AppliedDigest {
		t.Error("digest of the persisted file differs from the applied digest")
	}

	// Second apply with the same digest + render present -> noop.
	res2, err := h.a.Apply(context.Background(), raw, res.DesiredDigest, true)
	if err != nil {
		t.Fatalf("Apply (noop): %v", err)
	}
	if !res2.Noop || res2.Changed {
		t.Errorf("expected noop on identical apply: %+v", res2)
	}
	if h.validator.called != 1 {
		t.Errorf("a noop must not re-run xray -test (ran %d times)", h.validator.called)
	}
}

// A truncated document must be refused and the previous good config kept.
func TestApplyRefusesTruncatedJSONAndKeepsPreviousConfig(t *testing.T) {
	h := newHarness(t)
	good := providerFixture(t)

	first, err := h.a.Apply(context.Background(), good, "", false)
	if err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	goodOnDisk, _ := os.ReadFile(h.a.ProviderPath)
	writesBefore := len(h.written)

	truncated := good[:len(good)*2/3]
	if _, err := h.a.Apply(context.Background(), truncated, first.AppliedDigest, true); err == nil {
		t.Fatal("expected a refusal on truncated JSON")
	}
	if len(h.written) != writesBefore {
		t.Error("a refused apply must not write xray.json")
	}
	after, _ := os.ReadFile(h.a.ProviderPath)
	if string(after) != string(goodOnDisk) {
		t.Error("the previously good provider config must be left in place")
	}
}

// A provider document that ships its own streamSettings.sockopt.mark must fail
// CLOSED here: xray would happily start such a config (it is valid Xray), so
// `xray -test` cannot catch it — the splice refusal is the only gate, and the
// previously installed config has to survive it.
func TestApplyRefusesProviderSuppliedSockoptMarkAndKeepsPreviousConfig(t *testing.T) {
	h := newHarness(t)
	good := providerFixture(t)

	first, err := h.a.Apply(context.Background(), good, "", false)
	if err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	goodOnDisk, _ := os.ReadFile(h.a.ProviderPath)
	writesBefore := len(h.written)
	validatedBefore := h.validator.called

	// The provider's own document, with one dialling outbound re-declaring the
	// mark as 0 — i.e. "do not mark my egress".
	hostile := []byte(`{"log":{"loglevel":"warning"},"inbounds":[],"outbounds":[` +
		`{"tag":"PROXY","protocol":"vless","streamSettings":{"sockopt":{"mark":0}}}]}`)

	_, err = h.a.Apply(context.Background(), hostile, first.AppliedDigest, true)
	if err == nil {
		t.Fatal("expected a refusal for a provider-supplied sockopt.mark")
	}
	if !strings.Contains(err.Error(), "sockopt.mark") {
		t.Errorf("refusal should name the offending field: %v", err)
	}
	if len(h.written) != writesBefore {
		t.Error("a refused apply must not write xray.json")
	}
	if h.validator.called != validatedBefore {
		t.Error("the refusal must happen before xray -test, not after")
	}
	if after, _ := os.ReadFile(h.a.ProviderPath); string(after) != string(goodOnDisk) {
		t.Error("the previously good provider config must be left in place")
	}
}

// `xray -test` failure must refuse BEFORE anything is written.
func TestApplyRefusesWhenXrayTestFails(t *testing.T) {
	h := newHarness(t)
	h.validator.err = errors.New("xray: failed to parse config: unknown transport")

	_, err := h.a.Apply(context.Background(), providerFixture(t), "", false)
	if err == nil {
		t.Fatal("expected a refusal when xray -test fails")
	}
	if !strings.Contains(err.Error(), "previous config left in place") {
		t.Errorf("error should say the previous config is kept: %v", err)
	}
	if len(h.written) != 0 {
		t.Error("xray.json must NOT be written when xray -test fails")
	}
	if _, statErr := os.Stat(h.a.ProviderPath); statErr == nil {
		t.Error("the provider document must NOT be persisted when xray -test fails")
	}
	if h.validator.called != 1 {
		t.Errorf("validator should have been consulted once, got %d", h.validator.called)
	}
	// It must have been asked about the SPLICED document, not the raw one.
	if !strings.Contains(string(h.validator.seen), "dokodemo-door") {
		t.Error("xray -test must run against the spliced document")
	}
}

func TestApplyRefusesAllowInsecure(t *testing.T) {
	h := newHarness(t)
	hostile := []byte(`{"log":{"loglevel":"warning"},"inbounds":[],` +
		`"outbounds":[{"tag":"x","protocol":"vless","streamSettings":{"security":"tls","tlsSettings":{"allowInsecure":true}}}]}`)

	_, err := h.a.Apply(context.Background(), hostile, "", false)
	if err == nil {
		t.Fatal("expected a refusal when the provider config disables TLS verification")
	}
	if !strings.Contains(err.Error(), "allowInsecure") {
		t.Errorf("error should name allowInsecure: %v", err)
	}
	if len(h.written) != 0 || h.validator.called != 0 {
		t.Error("the allowInsecure guard must fire before splice/validate/write")
	}

	// It is overridable by explicit operator config.
	h2 := newHarness(t)
	h2.a.AllowInsecureTLS = true
	if _, err := h2.a.Apply(context.Background(), hostile, "", false); err != nil {
		t.Fatalf("AllowInsecureTLS=true must permit it: %v", err)
	}
}

func TestApplyRefusesMisconfiguration(t *testing.T) {
	raw := providerFixture(t)
	cases := map[string]func(*apply.Applier){
		"no tproxy inbound": func(a *apply.Applier) { a.Tproxy = nil },
		"no xray writer":    func(a *apply.Applier) { a.WriteXray = nil },
		"no validator":      func(a *apply.Applier) { a.Validate = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			mutate(h.a)
			if _, err := h.a.Apply(context.Background(), raw, "", false); err == nil {
				t.Fatal("expected a refusal")
			}
			if len(h.written) != 0 {
				t.Error("nothing may be written on a misconfigured applier")
			}
		})
	}
}

func TestApplyRefusesEmptyConfig(t *testing.T) {
	h := newHarness(t)
	if _, err := h.a.Apply(context.Background(), nil, "", false); err == nil {
		t.Fatal("expected a refusal on an empty provider config")
	}
}

// The owner's sites change the render, never the provider document kept on
// disk (its digest is what the panel compares), and the job's trail says
// what they became. Without sites there is no such line.
func TestApplyRecordsTheOwnersSites(t *testing.T) {
	raw := providerFixture(t)
	h := newHarness(t)
	h.a.Splice.Rules = xray.UserRules{Direct: []string{"bank.example", "not a site"}, Proxy: []string{"example.org"}}
	res, err := h.a.Apply(context.Background(), raw, "", false)
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, op := range res.Operations {
		if op.Kind == "splice_user_rules" {
			line = op.Description
		}
	}
	for _, want := range []string{"2 routing rule(s)", "1 direct via DIRECT", "via balancer BL-MAIN", "1 entries dropped"} {
		if !strings.Contains(line, want) {
			t.Errorf("trail %q lacks %q", line, want)
		}
	}
	if onDisk, _ := os.ReadFile(h.a.ProviderPath); string(onDisk) != string(raw) || res.AppliedDigest != apply.Digest(raw) {
		t.Error("the sites reached the provider document on disk")
	}
	if !strings.Contains(string(h.written[0]), `"domain":["domain:bank.example"]`) {
		t.Error("the sites are not in the render")
	}

	plain := newHarness(t)
	res, err = plain.a.Apply(context.Background(), raw, "", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range res.Operations {
		if op.Kind == "splice_user_rules" {
			t.Errorf("a render without sites says %q", op.Description)
		}
	}
}
