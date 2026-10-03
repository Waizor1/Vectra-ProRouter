package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/uiapi"
)

// "My sites": set_rules is checked by `vctl rpcd` before anything reaches the
// daemon, and the daemon keeps the lists only once a render with them runs.

func TestSetRulesIsRefusedBeforeTheDaemonIsAsked(t *testing.T) {
	s := newLockStand(t)
	fakeGather(t)
	fakeUILock(t, "0", nil)
	ctx := context.Background()
	var many []string
	for i := 0; i <= 100; i++ {
		many = append(many, fmt.Sprintf("%q", fmt.Sprintf("s%d.example", i)))
	}
	for _, tc := range []struct{ params, detail string }{
		{`{"direct":["sberbank.ru","exa mple.com"],"proxy":[]}`, `direct[1] "exa mple.com": `},
		{`{"direct":[],"proxy":["1.2.3.4/33"]}`, `proxy[0] "1.2.3.4/33": `},
		{`{"direct":["example.org"],"proxy":["https://www.example.org/x"]}`, `proxy[0] "https://www.example.org/x": `},
		{`{"direct":[` + strings.Join(many, ",") + `],"proxy":[]}`, "direct: 101 sites; at most 100"},
		{`{"direct":["example.org"]}`, "direct and proxy are both required"},
		{`{"direct":null,"proxy":[]}`, "direct and proxy are both required"},
		{`{}`, "direct and proxy are both required"},
		{``, "direct and proxy are both required"},
		{`{"direct":"example.org","proxy":[]}`, "direct and proxy must be lists of strings"},
		{`{"direct":[1],"proxy":[]}`, "direct and proxy must be lists of strings"},
		{`["example.org"]`, "params are not a JSON object"},
	} {
		a, ok := rpcdCall(ctx, s.cfg, "set_rules", []byte(tc.params)).(uiapi.Action)
		if !ok || a.OK || a.Code != "invalid_params" || a.Detail == nil || !strings.HasPrefix(*a.Detail, tc.detail) {
			t.Errorf("set_rules %.60s = %+v (detail %v), want invalid_params %q", tc.params, a, deref(a.Detail), tc.detail)
		}
	}
	if n := len(s.daemonRequests()); n > 0 {
		t.Errorf("the daemon was asked %d time(s) for refused lists", n)
	}
	if _, err := os.Stat(s.cfg.OverridesPath); !os.IsNotExist(err) {
		t.Errorf("refused lists reached the overrides (%v)", err)
	}

	// Anti-vacuity: accepted lists reach the daemon, normalized — and rpcd
	// itself writes nothing: the daemon keeps them once they run.
	a := rpcdCall(ctx, s.cfg, "set_rules", []byte(
		`{"direct":["HTTPS://WWW.Sberbank.RU/ru/person","госуслуги.рф","sberbank.ru:443","1.2.3.4/24"],"proxy":["XN--E1AFMKFD.XN--P1AI"]}`)).(uiapi.Action)
	if !a.OK || a.Code != "rules_set" {
		t.Fatalf("set_rules = %+v", a)
	}
	reqs := s.daemonRequests()
	if len(reqs) != 1 || reqs[0].Op != localctl.OpReapply || reqs[0].Change == nil || reqs[0].Change.SetRules == nil {
		t.Fatalf("the daemon was asked %+v", reqs)
	}
	got := reqs[0].Change.SetRules
	if strings.Join(got.Direct, ",") != "sberbank.ru,госуслуги.рф,1.2.3.0/24" || strings.Join(got.Proxy, ",") != "пример.рф" {
		t.Errorf("the daemon was handed %+v", got)
	}
	if _, err := os.Stat(s.cfg.OverridesPath); !os.IsNotExist(err) {
		t.Errorf("rpcd wrote the overrides itself (%v)", err)
	}
	// Emptying both lists is a change like any other.
	if a := rpcdCall(ctx, s.cfg, "set_rules", []byte(`{"direct":[],"proxy":[]}`)).(uiapi.Action); !a.OK || a.Code != "rules_set" {
		t.Errorf("emptying = %+v", a)
	}
	if reqs := s.daemonRequests(); len(reqs) != 2 || reqs[1].Change.SetRules == nil || len(reqs[1].Change.SetRules.Direct) != 0 {
		t.Errorf("emptying asked the daemon %+v", reqs)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestTheRulesAnswerIsWhatTheOverridesKeep(t *testing.T) {
	s := newLockStand(t)
	fakeGather(t)
	fakeUILock(t, "0", nil)
	ctx := context.Background()
	out, _ := json.Marshal(rpcdCall(ctx, s.cfg, "rules", nil))
	// No geo file in this stand: no services offered (geoservices_test.go).
	if string(out) != `{"direct":[],"proxy":[],"max":100,"catalog":[],"missing":[]}` {
		t.Errorf("no sites = %s", out)
	}
	if _, err := localctl.UpdateOverrides(s.cfg.OverridesPath, func(o *localctl.Overrides) error {
		o.Direct, o.Proxy = []string{"sberbank.ru", "госуслуги.рф"}, []string{"example.org"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out, _ = json.Marshal(rpcdCall(ctx, s.cfg, "rules", nil))
	if string(out) != `{"direct":["sberbank.ru","госуслуги.рф"],"proxy":["example.org"],"max":100,"catalog":[],"missing":[]}` {
		t.Errorf("rules = %s", out)
	}
	if n := len(s.daemonRequests()); n > 0 {
		t.Errorf("reading the rules asked the daemon %d time(s)", n)
	}
}

// The daemon's side: the lists go into every render — at the top of the
// routing — and are kept only once that render is installed. A change that
// fails leaves the router and the overrides as they were.
func TestTheOwnersSitesAreKeptOnlyOnceTheyRun(t *testing.T) {
	d, _, entries, remarks := newLocalUIDaemon(t)
	ctx := context.Background()
	raw, _, err := d.fetchProviderDocument(ctx, d.desired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.applyProvider(ctx, raw, false); err != nil {
		t.Fatal(err)
	}
	noRulesKey := d.st.SpliceKey
	render := func() string {
		t.Helper()
		b, err := readEncryptedTestFile(t, d.cfg.XrayRenderPath)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	firstRule := func() string {
		t.Helper()
		var doc struct {
			Routing struct {
				Rules []json.RawMessage `json:"rules"`
			} `json:"routing"`
		}
		if err := json.Unmarshal([]byte(render()), &doc); err != nil || len(doc.Routing.Rules) == 0 {
			t.Fatalf("render: %v", err)
		}
		// The DNS inbound's rule comes first (DNS through the tunnel), then
		// the exit probe's (exitcheck.go); the first one for the LAN's
		// traffic is what this test is about.
		for _, r := range doc.Routing.Rules {
			if !strings.Contains(string(r), `"inboundTag":["vctl-dns-in"]`) && !strings.Contains(string(r), `"inboundTag":["vctl-exit-probe"]`) {
				return string(r)
			}
		}
		return ""
	}
	const direct = `{"type":"field","inboundTag":["tproxy-in"],"domain":["domain:bank.example"],"outboundTag":"DIRECT"}`

	resp := d.localReapply(ctx, &localctl.Change{SetRules: &localctl.Rules{Direct: []string{"bank.example"}, Proxy: []string{"example.org"}}})
	if !resp.OK {
		t.Fatalf("set = %+v", resp)
	}
	if got := firstRule(); got != direct {
		t.Fatalf("first rule = %s", got)
	}
	if !strings.Contains(render(), `"domain":["domain:example.org"],"balancerTag":"BL-MAIN"`) {
		t.Error("the proxy site is not in the render")
	}
	ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath)
	if strings.Join(ov.Direct, ",") != "bank.example" || strings.Join(ov.Proxy, ",") != "example.org" {
		t.Fatalf("the running lists were not kept: %+v", ov)
	}
	withRules := d.st.SpliceKey
	if withRules == noRulesKey {
		t.Fatal("the splice key did not change with the sites")
	}

	// A render xray refuses: nothing kept, nothing installed.
	before := render()
	validate := d.applier.Validate
	d.applier.Validate = failingValidator{}
	resp = d.localReapply(ctx, &localctl.Change{SetRules: &localctl.Rules{Direct: []string{"vtb.ru"}, Proxy: []string{}}})
	if resp.OK || resp.Code != "apply_failed" {
		t.Fatalf("refused render = %+v", resp)
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); strings.Join(ov.Direct, ",") != "bank.example" || len(ov.Proxy) != 1 {
		t.Fatalf("lists that never ran were kept: %+v", ov)
	}
	if render() != before || d.st.SpliceKey != withRules {
		t.Fatal("the failed change touched the installed render")
	}
	d.applier.Validate = validate

	// Every later render carries them: the same document again (a nightly
	// refresh, a panel job), and another location.
	if _, err := d.applyProvider(ctx, raw, true); err != nil {
		t.Fatal(err)
	}
	if firstRule() != direct {
		t.Error("a forced re-render dropped the sites")
	}
	if resp := d.localReapply(ctx, &localctl.Change{SetEntry: &localctl.EntryChoice{Remark: remarks[1], Index: 1}}); !resp.OK {
		t.Fatalf("location switch = %+v", resp)
	}
	if firstRule() != direct || d.st.ConfigDigest == "" || !bytes.Contains(entries[1], []byte(remarks[1])) {
		t.Error("the location switch dropped the sites")
	}
	// And an unchanged document under unchanged options is still a no-op.
	onDisk, _ := readEncryptedTestFile(t, d.cfg.ProviderConfigPath)
	if res, err := d.applyProvider(ctx, onDisk, false); err != nil || !res.Noop {
		t.Errorf("an unchanged render was redone: %+v %v", res, err)
	}

	// Emptied: gone from the render, from the overrides file, and the key is
	// the one without sites again.
	if resp := d.localReapply(ctx, &localctl.Change{SetRules: &localctl.Rules{}}); !resp.OK {
		t.Fatalf("emptying = %+v", resp)
	}
	if strings.Contains(render(), "domain:bank.example") || strings.Contains(render(), `"inboundTag":["tproxy-in"]`) {
		t.Error("the emptied sites are still rendered")
	}
	file, _ := os.ReadFile(d.cfg.OverridesPath)
	if bytes.Contains(file, []byte(`"direct"`)) || bytes.Contains(file, []byte(`"proxy"`)) {
		t.Errorf("the overrides keep empty lists: %s", file)
	}
	if d.st.SpliceKey != noRulesKey {
		t.Errorf("key = %q, want the one without sites %q", d.st.SpliceKey, noRulesKey)
	}
}

// With no daemon to run them, set_rules writes nothing anywhere.
func TestRPCDWritesNoSitesWhenTheDaemonIsDown(t *testing.T) {
	d, _, _, _ := newLocalUIDaemon(t)
	dir, err := os.MkdirTemp("/tmp", "vrr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	d.cfg.UISocketPath = filepath.Join(dir, "absent.sock")
	a := rpcdMutate(context.Background(), d.cfg, "set_rules", []byte(`{"direct":["sberbank.ru"],"proxy":[]}`))
	if a.OK || a.Code != "controller_down" {
		t.Fatalf("set_rules with no daemon = %+v", a)
	}
	if ov, _ := localctl.LoadOverrides(d.cfg.OverridesPath); len(ov.Direct) != 0 {
		t.Fatalf("rpcd wrote sites nobody will run: %+v", ov)
	}
}
