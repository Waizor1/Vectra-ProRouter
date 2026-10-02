package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
)

func TestConnectRouteChanges(t *testing.T) {
	raw := json.RawMessage(`{"outbounds":[{"tag":"test","protocol":"vless"}],"routing":{"rules":[{"domain":["geosite:youtube"],"outboundTag":"test"}]}}`)
	// A location whose main path goes direct carries no service.
	directEntry := json.RawMessage(`{"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[]}}`)
	cache := &localctl.EntriesCache{Remarks: []string{"one", "direct"}, Entries: []json.RawMessage{raw, directEntry}}
	id := localctl.Summarize(cache)[0].Digest
	directID := localctl.Summarize(cache)[1].Digest
	for _, tc := range []struct{ action, params, code string }{
		{"select_entry", fmt.Sprintf(`{"entryId":%q}`, id), ""}, {"select_entry", `{"entryId":null}`, ""}, {"select_entry", `{"entryId":"bad"}`, "unknown_entry"},
		{"set_service", fmt.Sprintf(`{"service":"youtube","entryId":%q}`, id), ""}, {"set_service", fmt.Sprintf(`{"service":"telegram","entryId":%q}`, id), ""}, {"set_service", fmt.Sprintf(`{"service":"telegram","entryId":%q}`, directID), "service_path_unavailable"}, {"set_service", `{"service":"shell","entryId":null}`, "unknown_service"},
		{"set_rules", `{"direct":["Example.COM","*.example.org",".example.net","тест.рф"],"vpn":[]}`, ""}, {"set_rules", `{"direct":["x.example"],"vpn":["X.example"]}`, "invalid_params"}, {"set_rules", `{"direct":["https://example.org"],"vpn":[]}`, "invalid_params"}, {"set_rules", `{"direct":["1.2.3.4"],"vpn":[]}`, "invalid_params"},
	} {
		t.Run(tc.action+tc.params, func(t *testing.T) {
			c, code := connectRouteChange(tc.action, json.RawMessage(tc.params), cache)
			if code != tc.code {
				t.Fatalf("code=%s want=%s", code, tc.code)
			}
			if code == "" {
				var first localctl.Overrides
				c.ApplyTo(&first)
				var second localctl.Overrides
				c.ApplyTo(&second)
				if !reflect.DeepEqual(first, second) {
					t.Fatal("non-deterministic replay")
				}
				c.ApplyTo(&first)
				if !reflect.DeepEqual(first, second) {
					t.Fatal("non-idempotent replay")
				}
			}
		})
	}
	var direct []string
	for i := 0; i < 300; i++ {
		direct = append(direct, fmt.Sprintf("site%d.example", i))
	}
	p, _ := json.Marshal(map[string]any{"direct": direct, "vpn": []string{}})
	if _, code := connectRouteChange("set_rules", p, cache); code != "" {
		t.Fatal(code)
	}
	direct = append(direct, "extra.example")
	p, _ = json.Marshal(map[string]any{"direct": direct, "vpn": []string{}})
	if _, code := connectRouteChange("set_rules", p, cache); code != "invalid_params" {
		t.Fatal(code)
	}
	c, code := connectRouteChange("select_entry", json.RawMessage(fmt.Sprintf(`{"entryId":%q}`, id)), cache)
	if code != "" || c.SetEntry.Digest != id {
		t.Fatal("exact digest lost")
	}
}

func TestConnectRoutingFailureThenReplayAndStartup(t *testing.T) {
	d, provider, entries, _ := newLocalUIDaemon(t)
	ctx := context.Background()
	raw, _, err := d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.applyProvider(ctx, raw, false); err != nil {
		t.Fatal(err)
	}
	id := apply.Digest(entries[1])
	params := json.RawMessage(fmt.Sprintf(`{"entryId":%q}`, id))
	oldDigest := d.st.ConfigDigest
	validator := d.applier.Validate
	d.applier.Validate = failingValidator{}
	if resp := d.connectRoutingAction(ctx, "select_entry", params); resp.OK || resp.Code != "apply_failed" || resp.Detail != "" {
		t.Fatalf("failed apply=%+v", resp)
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); ov.EntryDigest != "" {
		t.Fatal("failed action persisted choice")
	}
	if d.st.ConfigDigest != oldDigest {
		t.Fatal("failed action changed live digest")
	}
	d.applier.Validate = validator
	fetches := len(provider.headers())
	for i := 0; i < 2; i++ {
		if resp := d.connectRoutingAction(ctx, "select_entry", params); !resp.OK {
			t.Fatalf("replay=%+v", resp)
		}
	}
	if len(provider.headers()) != fetches {
		t.Fatal("replay fetched provider")
	}
	ov, err := localctl.LoadOverrides(d.cfg.OverridesPath)
	if err != nil || ov.EntryDigest != id {
		t.Fatal("exact applied choice not persisted")
	}
	if d.st.ConfigDigest != id {
		t.Fatal("wrong selected document")
	}
	// A daemon recreated from its persisted config honors the exact digest,
	// even when array order changes and both display remarks are duplicated.
	reversed := []json.RawMessage{entries[1], entries[0]}
	idx, err := connectDigestIndex(reversed, ov.EntryDigest)
	if err != nil || idx != 0 {
		t.Fatal("reorder changed digest choice")
	}
	if _, err := connectDigestIndex([]json.RawMessage{entries[0]}, id); err == nil {
		t.Fatal("removed digest silently fell back")
	}
	if resp := d.connectRoutingAction(ctx, "select_entry", json.RawMessage(`{"entryId":null}`)); !resp.OK {
		t.Fatal(resp)
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); ov.EntryDigest != "" {
		t.Fatal("reset retained digest")
	}
}

func TestConnectServiceStartupFailsClosedAndClears(t *testing.T) {
	d, _, entries, _ := newLocalUIDaemon(t)
	ctx := context.Background()
	raw, _, err := d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.applyProvider(ctx, raw, false); err != nil {
		t.Fatal(err)
	}
	id := apply.Digest(entries[1])
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(ov *localctl.Overrides) error { ov.ServiceEntries = map[string]string{"tiktok": id}; return nil }); err != nil {
		t.Fatal(err)
	}
	opts, _ := d.spliceOptions(raw)
	if !bytes.Equal(opts.ServiceEntries["tiktok"], entries[1]) {
		t.Fatal("startup omitted exact cached service document")
	}
	if _, err := localctl.UpdateOverrides(d.cfg.OverridesPath, func(ov *localctl.Overrides) error { ov.ServiceEntries["tiktok"] = strings.Repeat("a", 64); return nil }); err != nil {
		t.Fatal(err)
	}
	opts, _ = d.spliceOptions(raw)
	if _, _, err := xray.Splice(raw, d.applier.Tproxy, opts); err == nil {
		t.Fatal("missing startup service digest accepted")
	}
	change, code := connectRouteChange("set_service", json.RawMessage(`{"service":"tiktok","entryId":null}`), nil)
	if code != "" {
		t.Fatal(code)
	}
	ov := localctl.Overrides{Services: map[string]string{"tiktok": "DE"}, ServiceEntries: map[string]string{"tiktok": id}}
	change.ApplyTo(&ov)
	if len(ov.Services) != 0 || len(ov.ServiceEntries) != 0 {
		t.Fatal("service reset did not clear both overlays")
	}
}

type connectCanceledValidator struct{}

func (connectCanceledValidator) Test(ctx context.Context, _ []byte) error { return ctx.Err() }

func TestConnectRoutingInterruptedBeforeApplyRetainsChoice(t *testing.T) {
	d, _, entries, _ := newLocalUIDaemon(t)
	ctx := context.Background()
	raw, _, err := d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.applyProvider(ctx, raw, false); err != nil {
		t.Fatal(err)
	}
	original := d.st.ConfigDigest
	validator := d.applier.Validate
	d.applier.Validate = connectCanceledValidator{}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	params := json.RawMessage(fmt.Sprintf(`{"entryId":%q}`, apply.Digest(entries[1])))
	if resp := d.connectRoutingAction(cancelCtx, "select_entry", params); resp.OK {
		t.Fatal("interrupted apply succeeded")
	}
	if d.st.ConfigDigest != original {
		t.Fatal("interrupted flow changed running config")
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); ov.EntryDigest != "" {
		t.Fatal("interrupted flow remembered unrun choice")
	}
	d.applier.Validate = validator
	if resp := d.connectRoutingAction(ctx, "select_entry", params); !resp.OK {
		t.Fatal("retry did not recover", resp)
	}
}
