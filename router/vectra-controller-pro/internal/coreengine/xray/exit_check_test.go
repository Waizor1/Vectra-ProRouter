package xray_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"vectra-controller-pro/internal/coreengine/xray"
)

// 1111, 2026-09-30: through bridge-us5 Instagram, Facebook, X, LinkedIn and
// Discord ended in a failed TLS handshake while Google answered, and the
// observatory (cp.cloudflare.com/generate_204) held it alive — leastLoad kept
// handing it connections. The router checks each foreign exit itself; the
// exits it checks are the dialling members of the balancers whose country is
// abroad: the Russian and Belarusian ones carry what must look local, and a
// whitelist level names no country.
func TestTheExitsCheckedAreTheForeignMembersOfBalancers(t *testing.T) {
	got := xray.ExitsToCheck(providerFixture(t))
	want := []string{"bridge-ae5", "bridge-de5", "bridge-fin5", "bridge-fr5", "bridge-nl5", "bridge-pl5", "bridge-tr5", "bridge-us5", "hy2-de5", "hy2-fin5", "hy2-nl5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("exits to check:\n got %v\nwant %v", got, want)
	}
}

type probeDoc struct {
	Inbounds []struct {
		Tag      string `json:"tag"`
		Listen   string `json:"listen"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
		Settings struct {
			Accounts []struct {
				User string `json:"user"`
				Pass string `json:"pass"`
			} `json:"accounts"`
		} `json:"settings"`
	} `json:"inbounds"`
	Routing struct {
		Rules []struct {
			InboundTag  []string `json:"inboundTag"`
			User        []string `json:"user"`
			OutboundTag string   `json:"outboundTag"`
			BalancerTag string   `json:"balancerTag"`
		} `json:"rules"`
		Balancers []struct {
			Tag      string   `json:"tag"`
			Selector []string `json:"selector"`
		} `json:"balancers"`
	} `json:"routing"`
}

func spliceProbe(t *testing.T, raw []byte, opts xray.SpliceOptions) (probeDoc, xray.SpliceResult) {
	t.Helper()
	out, res, err := xray.Splice(raw, testTproxy(), opts)
	if err != nil {
		t.Fatalf("Splice: %v", err)
	}
	var d probeDoc
	if err := json.Unmarshal(out, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return d, res
}

// Each checked exit is reachable on its own through one loopback HTTP proxy:
// the account names the exit, and a rule for that inbound and that account
// alone sends the request to it — above every rule of the provider's, so no
// catch-all or service rule can take the probe elsewhere.
func TestTheExitProbeReachesEachExitAndNothingElse(t *testing.T) {
	for _, dns := range []bool{false, true} {
		opts := xray.SpliceOptions{ExitProbeListen: xray.DefaultExitProbeListen}
		if dns {
			opts.DNS = &xray.DNSOptions{Listen: xray.DefaultDNSListen, DirectResolvers: []string{"77.88.8.8"}}
		}
		d, res := spliceProbe(t, providerFixture(t), opts)
		exits := xray.ExitsToCheck(providerFixture(t))
		if !reflect.DeepEqual(res.ExitProbe, exits) {
			t.Fatalf("dns=%v: probe serves %v, want %v", dns, res.ExitProbe, exits)
		}
		last := d.Inbounds[len(d.Inbounds)-1]
		if last.Tag != xray.ExitProbeTag || last.Protocol != "http" || last.Listen != "127.0.0.1" || last.Port != 10087 {
			t.Fatalf("dns=%v: last inbound %+v, want the exit probe on 127.0.0.1:10087", dns, last)
		}
		if len(last.Settings.Accounts) != len(exits) {
			t.Fatalf("dns=%v: %d accounts for %d exits", dns, len(last.Settings.Accounts), len(exits))
		}
		first := 0
		if dns {
			first = 1
		}
		for i, tag := range exits {
			if last.Settings.Accounts[i].User != xray.ExitProbeUser(tag) || last.Settings.Accounts[i].Pass == "" {
				t.Fatalf("dns=%v: account %d is %+v, want user %s", dns, i, last.Settings.Accounts[i], xray.ExitProbeUser(tag))
			}
			r := d.Routing.Rules[first+i]
			if !reflect.DeepEqual(r.InboundTag, []string{xray.ExitProbeTag}) || !reflect.DeepEqual(r.User, []string{xray.ExitProbeUser(tag)}) || r.OutboundTag != tag || r.BalancerTag != "" {
				t.Fatalf("dns=%v: rule %d is %+v, want the probe account for %s to %s", dns, first+i, r, tag, tag)
			}
		}
		if n := len(d.Routing.Rules); n <= first+len(exits) || len(d.Routing.Rules[first+len(exits)].InboundTag) != 0 && d.Routing.Rules[first+len(exits)].InboundTag[0] == xray.ExitProbeTag {
			t.Fatalf("dns=%v: the probe rules do not end where the provider's begin", dns)
		}
	}
}

// Users are names xray splits at the first colon; a tag may hold anything.
func TestTheProbeAccountNeverCarriesTheTag(t *testing.T) {
	for _, tag := range []string{"bridge-us5", "🇺🇸 US: fast", "a:b"} {
		u := xray.ExitProbeUser(tag)
		if strings.ContainsAny(u, ": ") || !strings.HasPrefix(u, "vctl-exit-") || u != xray.ExitProbeUser(tag) {
			t.Fatalf("user for %q is %q", tag, u)
		}
	}
	if xray.ExitProbeUser("bridge-us5") == xray.ExitProbeUser("bridge-de5") {
		t.Fatal("two exits share a probe account")
	}
}

func TestTheExitProbeStaysOnLoopback(t *testing.T) {
	_, _, err := xray.Splice(providerFixture(t), testTproxy(), xray.SpliceOptions{ExitProbeListen: "0.0.0.0:10087"})
	if err == nil {
		t.Fatal("an exit probe on 0.0.0.0 was accepted")
	}
}

// selected resolves a balancer's selector the way xray does: every outbound
// tag with one of the selector entries as a prefix.
func selected(selector, tags []string) []string {
	var out []string
	for _, tag := range tags {
		for _, s := range selector {
			if strings.HasPrefix(tag, s) {
				out = append(out, tag)
				break
			}
		}
	}
	return out
}

func outboundTags(t *testing.T, raw []byte) []string {
	t.Helper()
	var d struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, o := range d.Outbounds {
		out = append(out, o.Tag)
	}
	return out
}

// An exit found unfit leaves every balancer it is in — the provider's and the
// services' — and nothing else changes; the outbound itself stays, so the
// probe can keep asking it and give it back.
func TestAnUnfitExitLeavesEveryBalancer(t *testing.T) {
	raw := providerFixture(t)
	tags := outboundTags(t, raw)
	unfit := []string{"bridge-us5", "hy2-de5"}
	d, res := spliceProbe(t, raw, xray.SpliceOptions{ExitProbeListen: xray.DefaultExitProbeListen, LeaveOut: unfit,
		Services: map[string]string{"tiktok": "DE"}})
	if !reflect.DeepEqual(res.LeftOut, unfit) {
		t.Fatalf("left out %v, want %v", res.LeftOut, unfit)
	}
	if !reflect.DeepEqual(res.Services.Applied, []string{"tiktok=DE"}) {
		t.Fatalf("services %+v", res.Services)
	}
	want := map[string][]string{
		"BL-MAIN":         {"bridge-pl5", "bridge-de5", "bridge-fin5", "bridge-fr5", "bridge-nl5", "bridge-tr5", "bridge-ae5"},
		"BL-MAIN-BACKUP":  {"hy2-fin5", "hy2-nl5"},
		"VCTL-SVC-TIKTOK": {"bridge-de5"},
	}
	for _, b := range d.Routing.Balancers {
		got := selected(b.Selector, tags)
		for _, g := range got {
			if contains(unfit, g) {
				t.Fatalf("%s still selects %s: %v", b.Tag, g, b.Selector)
			}
		}
		if w, ok := want[b.Tag]; ok && !reflect.DeepEqual(got, w) {
			t.Fatalf("%s selects %v, want %v", b.Tag, got, w)
		}
		delete(want, b.Tag)
	}
	if len(want) != 0 {
		t.Fatalf("balancers missing from the render: %v", want)
	}
	if !contains(outboundTags(t, mustSplice(t, raw, xray.SpliceOptions{LeaveOut: unfit})), "bridge-us5") {
		t.Fatal("the unfit exit's outbound was removed")
	}
}

// A balancer is never emptied: with its only exit unfit it keeps it — a bad
// path the observatory still judges beats none (its fallback may be nothing).
func TestABalancerIsNeverEmptied(t *testing.T) {
	raw := providerFixture(t)
	d, res := spliceProbe(t, raw, xray.SpliceOptions{LeaveOut: []string{"hy2-de5", "hy2-fin5", "hy2-nl5", "bridge-us5"}})
	for _, b := range d.Routing.Balancers {
		if b.Tag == "BL-MAIN-BACKUP" && !reflect.DeepEqual(b.Selector, []string{"hy2-de5", "hy2-fin5", "hy2-nl5"}) {
			t.Fatalf("BL-MAIN-BACKUP emptied to %v", b.Selector)
		}
	}
	if !reflect.DeepEqual(res.LeftOut, []string{"bridge-us5"}) {
		t.Fatalf("left out %v, want only bridge-us5 (the backup keeps its exits)", res.LeftOut)
	}
}

// A selector is a prefix: "bridge-" picks every bridge. It is spelled out
// without the unfit one — unless a kept tag is itself a prefix of the unfit
// one, which no selector can express; that balancer stays as it was.
func TestAPrefixSelectorIsSpelledOutWithoutTheUnfitExit(t *testing.T) {
	doc := `{"outbounds":[{"tag":"bridge-de5","protocol":"vless"},{"tag":"bridge-us5","protocol":"vless"},{"tag":"hy2-de5","protocol":"hysteria"},{"tag":"hy2-de5-2","protocol":"hysteria"},{"tag":"DIRECT","protocol":"freedom"}],
	"routing":{"rules":[{"type":"field","network":"tcp,udp","balancerTag":"BL-MAIN"}],"balancers":[
	 {"tag":"BL-MAIN","selector":["bridge-"],"strategy":{"type":"leastLoad"}},
	 {"tag":"BL-HY","selector":["hy2-de5"],"strategy":{"type":"leastLoad"}}]}}`
	d, res := spliceProbe(t, []byte(doc), xray.SpliceOptions{LeaveOut: []string{"bridge-us5", "hy2-de5-2"}})
	sel := map[string][]string{}
	for _, b := range d.Routing.Balancers {
		sel[b.Tag] = b.Selector
	}
	if !reflect.DeepEqual(sel["BL-MAIN"], []string{"bridge-de5"}) {
		t.Fatalf("BL-MAIN selector %v, want [bridge-de5]", sel["BL-MAIN"])
	}
	if !reflect.DeepEqual(sel["BL-HY"], []string{"hy2-de5"}) {
		t.Fatalf("BL-HY selector %v, want it untouched", sel["BL-HY"])
	}
	if !reflect.DeepEqual(res.LeftOut, []string{"bridge-us5"}) {
		t.Fatalf("left out %v", res.LeftOut)
	}
}

func TestTheSpliceKeyCarriesTheProbeAndTheUnfitExits(t *testing.T) {
	base := xray.SpliceOptions{}.Key()
	a := xray.SpliceOptions{LeaveOut: []string{"b", "a"}}.Key()
	b := xray.SpliceOptions{LeaveOut: []string{"a", "b"}}.Key()
	p := xray.SpliceOptions{ExitProbeListen: xray.DefaultExitProbeListen}.Key()
	if a != b || a == base || p == base || p == a {
		t.Fatalf("keys: base %q, unfit %q / %q, probe %q", base, a, b, p)
	}
}

func mustSplice(t *testing.T, raw []byte, opts xray.SpliceOptions) []byte {
	t.Helper()
	out, _, err := xray.Splice(raw, testTproxy(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Review of r26: an unfit exit every balancer must keep (the last of one)
// changed the splice key all the same — a re-render to identical bytes and an
// xray restart for nothing, again on absolution. Leavable says which unfit
// exits a render would actually move, the services' balancers included.
func TestLeavableAreTheExitsARenderWouldMove(t *testing.T) {
	raw := providerFixture(t)
	hy2 := []string{"hy2-de5", "hy2-fin5", "hy2-nl5"}
	if got := xray.Leavable(raw, nil, hy2); len(got) != 0 {
		t.Fatalf("the backup's every exit is unfit: nothing moves, got %v", got)
	}
	if got := xray.Leavable(raw, map[string]string{"tiktok": "DE"}, hy2); !reflect.DeepEqual(got, []string{"hy2-de5"}) {
		t.Fatalf("Germany's overlay can drop hy2-de5 (bridge-de5 stays): got %v", got)
	}
	if got := xray.Leavable(raw, nil, []string{"bridge-us5", "hy2-nl5"}); !reflect.DeepEqual(got, []string{"bridge-us5", "hy2-nl5"}) {
		t.Fatalf("got %v", got)
	}
}
