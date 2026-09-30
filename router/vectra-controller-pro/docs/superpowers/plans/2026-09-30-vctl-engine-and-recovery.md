# vctl engine and recovery (Plan A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Run 1111 on the subscription's own entry («Авто Самый стабильный») with Russian sites direct, and make a node that stops answering lose NEW connections within 10 s — measured on the stand, drilled live.

**Architecture:** The provider document stays the engine (route_source `provider`); the splice gains one rewrite (BL-RU's Russian rules → DIRECT). A failover watchdog goroutine reads conntrack every 2 s, marks a node failing when xray's attempts to it go unanswered, re-points the affected balancer through xray's RoutingService and confirms once. A `vctl route provider` command migrates a native router.

**Tech Stack:** Go (module `vectra-controller-pro`), xray-core 26.x (RoutingService over h2c: `internal/api`), OpenWrt 24.10 (busybox ash, procd, nftables), the dataplane stand (`test/dataplane`, colima).

**Spec:** `docs/superpowers/specs/2026-09-30-vctl-service-routing-design.md`

## Global Constraints

- Only a proven data plane goes to 1111 (`xray -test` is not proof); both stands green and a fresh review first.
- One terminal job per router, wait for the exact jobId, job timeout ≤ 115 s; a job blocks vctl's loop while it runs — never wait inside a job for something the loop does.
- Never print subscription URLs, UUIDs, keys, node addresses in jobs or logs; redact.
- i18n ru/en/zh for every user-visible string (Plan A adds none to the UI; incidents are English).
- The owner's pins (`Overrides.Pins`) are never touched by automation.
- `DOCKER_CONTEXT=colima` per docker command; RTK-first in the shell.
- Observatory interval stays the provider's (no probing every node faster).
- Failover budget: ≤ 10 s from «node stops answering» to «new connections flow through another node».

## Review Focus

1. A node that answers some connections and drops others (flapping) — the watchdog must not bounce the override every 2 s: release needs answered connections opened after the override, and a released node is not overridden again for 30 s unless it fails again. Test in Task 4.
2. All members of a balancer failing — the watchdog must not override to a failing or dead candidate; it leaves the balancer to its fallback chain and raises PROXY_DOWN after 60 s. Test in Task 4.
3. Node hostnames that resolve to several IPs, or whose resolution fails — endpoints map each resolved IP; a node with no resolved IP is never judged failing (no evidence). Test in Task 5.
4. Conntrack lines of other shapes (IPv6, ICMP, `[OFFLOAD]`, missing fields, a truncated last line) — the parser skips what it does not understand and never panics. Test in Task 3.
5. The render changing under the watchdog (entry switched, provider refresh) — overrides it set for tags that no longer exist are forgotten, and its view is re-read when the render's mtime changes. Test in Task 6.

---

### Task 1: Russian sites direct in the provider splice

**Files:**
- Create: `internal/coreengine/xray/russia_direct.go`
- Create: `internal/coreengine/xray/russia_direct_test.go`
- Modify: `internal/coreengine/xray/runtime_options.go` (SpliceOptions gains `RussiaDirect bool`; `Key()` appends `;ru=direct` when set)
- Modify: `internal/coreengine/xray/splice.go` (routing branch: rewrite when `opts.RussiaDirect`, also when there are no owner rules; `SpliceResult.RussiaDirectRules int`)

**Interfaces:**
- Produces: `func rewriteRussianRules(routing json.RawMessage, directTag string) (json.RawMessage, int, error)` — returns the routing object with every Russian BL-RU rule's target set to `outboundTag: directTag`, and how many rules changed.
- Produces: `func directOutboundTag(providerRaw []byte) string` — the tag of the document's plain freedom outbound (reuses `readOutbound(...).plainDirect()`), "" if none.
- Rule classification: a rule is Russian when its `balancerTag` is `BL-RU` (exact), it has at least one marker from `ruMarkers` = {`geoip:ru`, `geosite:category-gov-ru`, `geosite:category-ru`, `domain:ru`, `domain:xn--p1ai`, `domain:su`, `domain:рф`}, and no marker from `youtubeMarkers` = {`geosite:youtube`, `domain:youtube.com`, `domain:googlevideo.com`, `domain:ytimg.com`, `domain:youtu.be`, `domain:ggpht.com`} in its `domain` or `ip` arrays.

- [ ] **Step 1: Write the failing tests** (`russia_direct_test.go`)

```go
package xray

import (
	"encoding/json"
	"strings"
	"testing"
)

const ruDoc = `{"outbounds":[{"tag":"bridge-pl5","protocol":"vless"},{"tag":"DIRECT","protocol":"freedom"},{"tag":"BLOCK","protocol":"blackhole"}],
"routing":{"domainStrategy":"IPIfNonMatch","rules":[
{"protocol":["bittorrent"],"outboundTag":"DIRECT"},
{"domain":["geosite:youtube","domain:googlevideo.com"],"balancerTag":"BL-RU"},
{"domain":["domain:gosuslugi.ru","domain:vk.com"],"outboundTag":"DIRECT"},
{"domain":["geosite:category-gov-ru","domain:gu-st.ru"],"balancerTag":"BL-RU"},
{"domain":["domain:ru","domain:xn--p1ai","domain:by"],"balancerTag":"BL-RU"},
{"ip":["geoip:ru"],"balancerTag":"BL-RU"},
{"domain":["domain:chatgpt.com"],"balancerTag":"BL-MAIN"},
{"network":"tcp,udp","balancerTag":"BL-MAIN"}],
"balancers":[{"tag":"BL-MAIN","selector":["bridge-"]},{"tag":"BL-RU","selector":["bridge-"]}]}}`

func rulesOf(t *testing.T, routing []byte) []map[string]any {
	t.Helper()
	var r struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(routing, &r); err != nil {
		t.Fatal(err)
	}
	return r.Rules
}

func TestRussianRulesGoDirectAndYouTubeKeepsItsBridge(t *testing.T) {
	var doc struct {
		Routing json.RawMessage `json:"routing"`
	}
	if err := json.Unmarshal([]byte(ruDoc), &doc); err != nil {
		t.Fatal(err)
	}
	out, n, err := rewriteRussianRules(doc.Routing, "DIRECT")
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	rules := rulesOf(t, out)
	if len(rules) != 8 {
		t.Fatalf("rules %d", len(rules))
	}
	if rules[1]["balancerTag"] != "BL-RU" {
		t.Fatalf("YouTube moved: %v", rules[1])
	}
	for _, i := range []int{3, 4, 5} {
		if rules[i]["outboundTag"] != "DIRECT" || rules[i]["balancerTag"] != nil {
			t.Fatalf("rule %d not direct: %v", i, rules[i])
		}
	}
	if rules[6]["balancerTag"] != "BL-MAIN" || rules[7]["balancerTag"] != "BL-MAIN" {
		t.Fatalf("main rules touched: %v %v", rules[6], rules[7])
	}
}

func TestNoDirectOutboundLeavesTheRulesAlone(t *testing.T) {
	if got := directOutboundTag([]byte(strings.Replace(ruDoc, `"protocol":"freedom"`, `"protocol":"blackhole"`, 1))); got != "" {
		t.Fatalf("direct tag %q", got)
	}
	if got := directOutboundTag([]byte(ruDoc)); got != "DIRECT" {
		t.Fatalf("direct tag %q", got)
	}
}

func TestSpliceSendsRussiaDirectAndSaysHowMany(t *testing.T) {
	out, res, err := Splice([]byte(ruDoc), testTproxy(), SpliceOptions{RussiaDirect: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.RussiaDirectRules != 3 {
		t.Fatalf("res %d", res.RussiaDirectRules)
	}
	if !strings.Contains(string(out), `"ip":["geoip:ru"],"outboundTag":"DIRECT"`) {
		t.Fatalf("render: %s", out)
	}
	if k := (SpliceOptions{RussiaDirect: true}).Key(); !strings.Contains(k, ";ru=direct") {
		t.Fatalf("key %q", k)
	}
}
```

(`testTproxy()` is the existing test helper in `splice_test.go` that returns the router's `*config.TproxyInbound`; if it is named differently, use that helper — check with `grep -n 'func test' internal/coreengine/xray/splice_test.go`.)

- [ ] **Step 2: Run them to watch them fail**

Run: `go test ./internal/coreengine/xray/ -run 'Russia|NoDirectOutbound' -count=1`
Expected: build failure — `rewriteRussianRules`, `directOutboundTag`, `RussiaDirect`, `RussiaDirectRules` undefined.

- [ ] **Step 3: Implement** (`russia_direct.go`)

```go
package xray

import (
	"encoding/json"
	"fmt"
)

// On a home router in Russia, Russian sites are faster and lighter direct
// than through the provider's Russian bridge (BL-RU), which exists for phones
// abroad and on whitelisted mobile networks. YouTube's BL-RU rule is the
// provider's ad-free path and stays. The provider's order of rules is kept.
var ruMarkers = map[string]bool{
	"geoip:ru": true, "geosite:category-gov-ru": true, "geosite:category-ru": true,
	"domain:ru": true, "domain:xn--p1ai": true, "domain:su": true, "domain:рф": true,
}

var youtubeMarkers = map[string]bool{
	"geosite:youtube": true, "domain:youtube.com": true, "domain:googlevideo.com": true,
	"domain:ytimg.com": true, "domain:youtu.be": true, "domain:ggpht.com": true,
}

const russianBalancer = "BL-RU"

func isRussianRule(r map[string]json.RawMessage) bool {
	var tag string
	if json.Unmarshal(r["balancerTag"], &tag) != nil || tag != russianBalancer {
		return false
	}
	ru := false
	for _, k := range []string{"domain", "ip"} {
		var list []string
		if len(r[k]) == 0 || json.Unmarshal(r[k], &list) != nil {
			continue
		}
		for _, m := range list {
			if youtubeMarkers[m] {
				return false
			}
			ru = ru || ruMarkers[m]
		}
	}
	return ru
}

func rewriteRussianRules(routing json.RawMessage, directTag string) (json.RawMessage, int, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(routing, &obj); err != nil {
		return nil, 0, fmt.Errorf("xray splice: routing is not an object: %w", err)
	}
	var rules []json.RawMessage
	if len(obj["rules"]) == 0 || json.Unmarshal(obj["rules"], &rules) != nil {
		return routing, 0, nil
	}
	n := 0
	for i, raw := range rules {
		var r map[string]json.RawMessage
		if json.Unmarshal(raw, &r) != nil || !isRussianRule(r) {
			continue
		}
		delete(r, "balancerTag")
		r["outboundTag"], _ = json.Marshal(directTag)
		b, err := json.Marshal(r)
		if err != nil {
			return nil, 0, err
		}
		rules[i] = b
		n++
	}
	if n == 0 {
		return routing, 0, nil
	}
	rb, err := json.Marshal(rules)
	if err != nil {
		return nil, 0, err
	}
	out, _, err := rewriteObjectField(routing, "rules", rb)
	return out, n, err
}

func directOutboundTag(providerRaw []byte) string {
	var doc struct {
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if json.Unmarshal(providerRaw, &doc) != nil {
		return ""
	}
	for _, raw := range doc.Outbounds {
		if o := readOutbound(raw); o.plainDirect() {
			return o.Tag
		}
	}
	return ""
}
```

In `runtime_options.go` add to `SpliceOptions`:

```go
	// RussiaDirect sends the provider's Russian BL-RU rules to its plain
	// freedom outbound (russia_direct.go). YouTube's BL-RU rule stays.
	RussiaDirect bool
```

and in `Key()` before the DNS part: `if o.RussiaDirect { k += ";ru=direct" }`.

In `splice.go`: add `RussiaDirectRules int` to `SpliceResult`; compute `ruTag := ""; if opts.RussiaDirect { ruTag = directOutboundTag(providerRaw) }` before the decode loop; change the routing branch condition to `(len(rules) > 0 || ruTag != "") && foldKey(key) == foldKey(RoutingKey)`, and inside it rewrite first:

```go
			routing := nullAsObject(raw)
			if ruTag != "" {
				rw, n, err := rewriteRussianRules(routing, ruTag)
				if err != nil {
					return nil, res, err
				}
				routing, res.RussiaDirectRules = rw, n
			}
			if len(rules) > 0 {
				rewritten, err := insertRules(routing, rules)
				if err != nil {
					return nil, res, err
				}
				routing = rewritten
			}
			out.Write(routing)
			continue
```

- [ ] **Step 4: Run the package's tests**

Run: `go test ./internal/coreengine/xray/ -count=1`
Expected: ok (all existing splice tests still pass; the three new ones pass).

- [ ] **Step 5: Wire the default and commit**

In `cmd/vctl/localui.go` `spliceOptionsFor` gains a parameter `russiaDirect bool` and sets `opts.RussiaDirect = russiaDirect`; every caller passes `!d.cfg.NoRussiaDirect` (grep `spliceOptionsFor(` — callers in localui.go and passwall_source.go; passwall/native documents have no BL-RU, so nothing changes there). In `internal/agentcfg/agentcfg.go` add `NoRussiaDirect bool \`json:"noRussiaDirect,omitempty"\`` next to `NoDirectBypass`, and in `openwrt/files/usr/libexec/vectra-controller-pro/render-xray-config.sh` write it from UCI `russia_direct '0'` exactly as `direct_bypass '0'` writes `noDirectBypass`.

Run: `go test ./cmd/vctl/ ./internal/agentcfg/ ./openwrt/ -count=1`
Expected: ok.

```bash
git add internal/coreengine/xray cmd/vctl/localui.go cmd/vctl/passwall_source.go internal/agentcfg openwrt/files/usr/libexec/vectra-controller-pro/render-xray-config.sh
git commit -m "feat(xray): Russian sites direct in a provider document (BL-RU's Russian rules, YouTube keeps its bridge)"
```

### Task 2: The conntrack reader

**Files:**
- Create: `internal/conntrack/conntrack.go`
- Create: `internal/conntrack/conntrack_test.go`

**Interfaces:**
- Produces: `type Entry struct { Proto string; State string; Src, Dst netip.Addr; SPort, DPort uint16; Replied bool }` — the ORIGINAL direction's tuple; `Replied` is false when the line carries `[UNREPLIED]`.
- Produces: `func Parse(r io.Reader) []Entry` — skips lines it cannot read; ipv4 and ipv6 tcp/udp only.
- Produces: `var Path = "/proc/net/nf_conntrack"` and `func Read() ([]Entry, error)`.

- [ ] **Step 1: Write the failing test** with lines in the shape 1111 printed on 2026-09-29 (addresses replaced by documentation ranges):

```go
package conntrack

import (
	"net/netip"
	"strings"
	"testing"
)

const sample = `ipv4     2 tcp      6 52 SYN_SENT src=198.51.100.7 dst=203.0.113.5 sport=51054 dport=50055 packets=1 bytes=60 [UNREPLIED] src=203.0.113.5 dst=198.51.100.7 sport=50055 dport=51054 packets=0 bytes=0 mark=0 zone=0 use=2
ipv4     2 tcp      6 7431 ESTABLISHED src=192.168.1.141 dst=203.0.113.9 sport=58843 dport=40060 packets=1017 bytes=923632 src=203.0.113.9 dst=198.51.100.7 sport=40060 dport=58843 packets=1096 bytes=88041 [OFFLOAD] mark=268435456 zone=0 use=3
ipv4     2 udp      17 28 src=198.51.100.7 dst=203.0.113.6 sport=41000 dport=50052 packets=3 bytes=900 [UNREPLIED] src=203.0.113.6 dst=198.51.100.7 sport=50052 dport=41000 packets=0 bytes=0 mark=0 zone=0 use=2
ipv6     10 tcp      6 100 ESTABLISHED src=2001:db8::1 dst=2001:db8::2 sport=1 dport=443 packets=1 bytes=1 src=2001:db8::2 dst=2001:db8::1 sport=443 dport=1 packets=1 bytes=1 [ASSURED] mark=0 zone=0 use=2
ipv4     2 icmp     1 29 src=192.168.1.1 dst=8.8.8.8 type=8 code=0 id=1 packets=1 bytes=84 src=8.8.8.8 dst=192.168.1.1 type=0 code=0 id=1 packets=1 bytes=84 mark=0 zone=0 use=2
garbage
ipv4     2 tcp      6 10 SYN_SENT src=198.51.100.7 dst=203.0.113.5`

func TestParseReadsTheOriginalTuple(t *testing.T) {
	es := Parse(strings.NewReader(sample))
	if len(es) != 4 {
		t.Fatalf("entries %d: %+v", len(es), es)
	}
	a := es[0]
	if a.Proto != "tcp" || a.State != "SYN_SENT" || a.Replied || a.Dst != netip.MustParseAddr("203.0.113.5") || a.DPort != 50055 {
		t.Fatalf("%+v", a)
	}
	if !es[1].Replied || es[1].DPort != 40060 {
		t.Fatalf("%+v", es[1])
	}
	if es[2].Proto != "udp" || es[2].Replied || es[2].DPort != 50052 {
		t.Fatalf("%+v", es[2])
	}
	if es[3].Dst != netip.MustParseAddr("2001:db8::2") || !es[3].Replied {
		t.Fatalf("%+v", es[3])
	}
}
```

- [ ] **Step 2: Run it to watch it fail**

Run: `go test ./internal/conntrack/ -count=1`
Expected: build failure — package has no `Parse`.

- [ ] **Step 3: Implement**

```go
// Package conntrack reads the kernel's connection table
// (/proc/net/nf_conntrack): enough of each tcp/udp line to tell whether the
// original direction ever got an answer. Lines it cannot read are skipped.
package conntrack

import (
	"bufio"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

var Path = "/proc/net/nf_conntrack"

type Entry struct {
	Proto        string
	State        string
	Src, Dst     netip.Addr
	SPort, DPort uint16
	Replied      bool
}

func Read() ([]Entry, error) {
	f, err := os.Open(Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f), nil
}

func Parse(r io.Reader) []Entry {
	var out []Entry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		if e, ok := parseLine(sc.Text()); ok {
			out = append(out, e)
		}
	}
	return out
}

func parseLine(line string) (Entry, bool) {
	f := strings.Fields(line)
	if len(f) < 6 || (f[0] != "ipv4" && f[0] != "ipv6") {
		return Entry{}, false
	}
	e := Entry{Proto: f[2], Replied: true}
	if e.Proto != "tcp" && e.Proto != "udp" {
		return Entry{}, false
	}
	var seen = map[string]bool{}
	for _, tok := range f[5:] {
		if tok == "[UNREPLIED]" {
			e.Replied = false
			continue
		}
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			if e.State == "" && e.Proto == "tcp" && seen["src"] == false && strings.ToUpper(tok) == tok {
				e.State = tok
			}
			continue
		}
		if seen[k] { // the reply direction repeats the keys: only the original counts
			continue
		}
		seen[k] = true
		switch k {
		case "src":
			e.Src, _ = netip.ParseAddr(v)
		case "dst":
			e.Dst, _ = netip.ParseAddr(v)
		case "sport":
			p, _ := strconv.ParseUint(v, 10, 16)
			e.SPort = uint16(p)
		case "dport":
			p, _ := strconv.ParseUint(v, 10, 16)
			e.DPort = uint16(p)
		}
	}
	if !e.Src.IsValid() || !e.Dst.IsValid() || e.DPort == 0 || !seen["packets"] {
		return Entry{}, false
	}
	return e, true
}
```

- [ ] **Step 4: Run it**

Run: `go test ./internal/conntrack/ -count=1`
Expected: ok. (The truncated last line has no `packets=` and is skipped; the ICMP line is skipped by proto.)

- [ ] **Step 5: Commit**

```bash
git add internal/conntrack && git commit -m "feat(conntrack): read the kernel's connection table, original tuple and whether it was answered"
```

### Task 3: The failing detector (pure)

**Files:**
- Create: `internal/failover/detect.go`
- Create: `internal/failover/detect_test.go`

**Interfaces:**
- Consumes: `conntrack.Entry` (Task 2).
- Produces: `type Detector struct` with `func NewDetector() *Detector`, `func (d *Detector) Observe(now time.Time, entries []conntrack.Entry, endpoints map[netip.AddrPort]string)`, `func (d *Detector) Failing(tag string, now time.Time) bool`, `func (d *Detector) AnsweredSince(tag string, t time.Time) bool`.
- Semantics: an unanswered attempt is a `!Replied` entry whose original `Dst:DPort` maps to a node tag; its first-seen time is kept across calls (keyed by the 5-tuple); it counts once it is ≥ 2 s old. An answered connection (Replied) first seen at time t records `lastAnswered[tag] = t`. `Failing(tag, now)` = at least 2 unanswered attempts ≥ 2 s old that are still in the table, and no answered connection first seen within the last 10 s. Entries not seen in a scan are forgotten.

- [ ] **Step 1: Write the failing test**

```go
package failover

import (
	"net/netip"
	"testing"
	"time"

	"vectra-controller-pro/internal/conntrack"
)

var nl = netip.MustParseAddrPort("203.0.113.5:50055")
var eps = map[netip.AddrPort]string{nl: "bridge-nl5", netip.MustParseAddrPort("203.0.113.9:443"): "direct-de5"}

func syn(sport uint16) conntrack.Entry {
	return conntrack.Entry{Proto: "tcp", State: "SYN_SENT", Src: netip.MustParseAddr("198.51.100.7"), Dst: nl.Addr(), SPort: sport, DPort: nl.Port()}
}

func TestTwoUnansweredAttemptsTwoSecondsOldAreFailing(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	d.Observe(t0, []conntrack.Entry{syn(1), syn(2)}, eps)
	if d.Failing("bridge-nl5", t0.Add(time.Second)) {
		t.Fatal("failing before 2 s")
	}
	d.Observe(t0.Add(2*time.Second), []conntrack.Entry{syn(1), syn(2)}, eps)
	if !d.Failing("bridge-nl5", t0.Add(2*time.Second)) {
		t.Fatal("not failing after 2 s")
	}
	if d.Failing("direct-de5", t0.Add(2*time.Second)) {
		t.Fatal("another node judged")
	}
}

func TestAnAnsweredConnectionClearsIt(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	ok := syn(3)
	ok.Replied, ok.State = true, "ESTABLISHED"
	d.Observe(t0, []conntrack.Entry{syn(1), syn(2)}, eps)
	d.Observe(t0.Add(3*time.Second), []conntrack.Entry{syn(1), syn(2), ok}, eps)
	if d.Failing("bridge-nl5", t0.Add(3*time.Second)) {
		t.Fatal("failing with a fresh answered connection")
	}
	if !d.AnsweredSince("bridge-nl5", t0.Add(2*time.Second)) {
		t.Fatal("answer not recorded")
	}
}

func TestOneAttemptIsNotEnoughAndGoneAttemptsAreForgotten(t *testing.T) {
	d := NewDetector()
	t0 := time.Unix(1790000000, 0)
	d.Observe(t0, []conntrack.Entry{syn(1)}, eps)
	d.Observe(t0.Add(5*time.Second), []conntrack.Entry{syn(1)}, eps)
	if d.Failing("bridge-nl5", t0.Add(5*time.Second)) {
		t.Fatal("one attempt")
	}
	d.Observe(t0.Add(6*time.Second), nil, eps)
	d.Observe(t0.Add(7*time.Second), []conntrack.Entry{syn(9)}, eps)
	if d.Failing("bridge-nl5", t0.Add(7*time.Second)) {
		t.Fatal("forgotten attempts still counted")
	}
}
```

- [ ] **Step 2: Run it to watch it fail**

Run: `go test ./internal/failover/ -count=1`
Expected: build failure — no `NewDetector`.

- [ ] **Step 3: Implement**

```go
// Package failover decides when a node xray balances over has stopped
// answering, from what the kernel's conntrack already shows — no probe
// traffic — and what to do about it.
package failover

import (
	"fmt"
	"net/netip"
	"time"

	"vectra-controller-pro/internal/conntrack"
)

const (
	UnansweredAge  = 2 * time.Second
	UnansweredMin  = 2
	AnsweredWithin = 10 * time.Second
)

type attempt struct {
	tag   string
	first time.Time
}

type Detector struct {
	attempts     map[string]attempt // key: 5-tuple
	lastAnswered map[string]time.Time
	seenAnswered map[string]bool
}

func NewDetector() *Detector {
	return &Detector{attempts: map[string]attempt{}, lastAnswered: map[string]time.Time{}, seenAnswered: map[string]bool{}}
}

func key(e conntrack.Entry) string {
	return fmt.Sprintf("%s|%s|%d|%s|%d", e.Proto, e.Src, e.SPort, e.Dst, e.DPort)
}

func (d *Detector) Observe(now time.Time, entries []conntrack.Entry, endpoints map[netip.AddrPort]string) {
	live := map[string]bool{}
	answeredNow := map[string]bool{}
	for _, e := range entries {
		tag, ok := endpoints[netip.AddrPortFrom(e.Dst, e.DPort)]
		if !ok {
			continue
		}
		k := key(e)
		if e.Replied {
			answeredNow[k] = true
			if !d.seenAnswered[k] {
				d.lastAnswered[tag] = now
			}
			continue
		}
		live[k] = true
		if _, ok := d.attempts[k]; !ok {
			d.attempts[k] = attempt{tag: tag, first: now}
		}
	}
	for k := range d.attempts {
		if !live[k] {
			delete(d.attempts, k)
		}
	}
	d.seenAnswered = answeredNow
}

func (d *Detector) Failing(tag string, now time.Time) bool {
	n := 0
	for _, a := range d.attempts {
		if a.tag == tag && now.Sub(a.first) >= UnansweredAge {
			n++
		}
	}
	if n < UnansweredMin {
		return false
	}
	return !d.AnsweredSince(tag, now.Add(-AnsweredWithin))
}

func (d *Detector) AnsweredSince(tag string, t time.Time) bool {
	a, ok := d.lastAnswered[tag]
	return ok && !a.Before(t)
}
```

- [ ] **Step 4: Run it**

Run: `go test ./internal/failover/ -count=1`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/failover && git commit -m "feat(failover): judge a node failing by its unanswered attempts in conntrack"
```

### Task 4: The failover policy (pure)

**Files:**
- Create: `internal/failover/policy.go`
- Create: `internal/failover/policy_test.go`

**Interfaces:**
- Consumes: `Detector.Failing`, `Detector.AnsweredSince` (Task 3).
- Produces:

```go
type Balancer struct {
	Tag       string
	Members   []string
	Principle []string // xray's current preference (api.BalancerInfo.Principle)
	Override  string   // api.BalancerInfo.Override
	OwnerPin  string   // localctl.Overrides.Pins[tag], "" if none
}
type Health struct{ Alive bool; DelayMs int64 } // from api.Observation
type Action struct {
	Balancer string
	Target   string // "" = release the override
	From     string // the failing node, for the log and the incident
	Reason   string // "failing" | "recovered" | "gone"
}
type Policy struct { ours map[string]override; downSince map[string]time.Time }
type override struct { target, from string; at time.Time; releasedAt time.Time }
func NewPolicy() *Policy
func (p *Policy) Decide(now time.Time, bals []Balancer, health map[string]Health, det *Detector) []Action
func (p *Policy) Confirmed(balancer string, ok bool, now time.Time) // after the watchdog's confirmation probe
func (p *Policy) DownFor(balancer string, now time.Time) time.Duration // how long no candidate existed
```

- Rules: skip a balancer with an `OwnerPin`; skip one whose `Override` is set and not ours. If ours is set and its target is failing → next candidate. If ours is set and `det.AnsweredSince(from, ours.at)` or `from` is not in `Principle` any more → release (`Target ""`, Reason "recovered"/"gone"), and remember `releasedAt` (no new override for that `from` within 30 s unless failing again after it). If no override and any `Principle` member is failing → candidate = the member with Alive && !failing && lowest DelayMs (ties: member order); none → no action and `downSince[tag]` starts; a candidate clears `downSince`.

- [ ] **Step 1: Write the failing tests**

```go
package failover

import (
	"testing"
	"time"

	"vectra-controller-pro/internal/conntrack"
)

func failingDetector(t0 time.Time, tag string) *Detector {
	d := NewDetector()
	e1, e2 := syn(1), syn(2)
	d.Observe(t0, []conntrack.Entry{e1, e2}, eps)
	d.Observe(t0.Add(2*time.Second), []conntrack.Entry{e1, e2}, eps)
	return d
}

var main3 = Balancer{Tag: "BL-MAIN", Members: []string{"bridge-de5", "bridge-nl5", "bridge-pl5"}, Principle: []string{"bridge-nl5"}}
var alive = map[string]Health{"bridge-de5": {true, 80}, "bridge-nl5": {true, 40}, "bridge-pl5": {true, 60}}

func TestAFailingPrincipleMovesToTheFastestLiveMember(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	acts := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, failingDetector(t0, "bridge-nl5"))
	if len(acts) != 1 || acts[0].Target != "bridge-pl5" || acts[0].From != "bridge-nl5" || acts[0].Reason != "failing" {
		t.Fatalf("%+v", acts)
	}
}

func TestTheOwnersPinIsNeverTouched(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	b := main3
	b.OwnerPin, b.Override = "bridge-nl5", "bridge-nl5"
	if acts := NewPolicy().Decide(t0.Add(2*time.Second), []Balancer{b}, alive, failingDetector(t0, "bridge-nl5")); len(acts) != 0 {
		t.Fatalf("%+v", acts)
	}
}

func TestNoLiveCandidateMeansNoOverrideAndDownTime(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	dead := map[string]Health{"bridge-de5": {false, 0}, "bridge-nl5": {true, 40}, "bridge-pl5": {false, 0}}
	p := NewPolicy()
	if acts := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, dead, failingDetector(t0, "bridge-nl5")); len(acts) != 0 {
		t.Fatalf("%+v", acts)
	}
	if p.DownFor("BL-MAIN", t0.Add(62*time.Second)) < 60*time.Second {
		t.Fatalf("down %s", p.DownFor("BL-MAIN", t0.Add(62*time.Second)))
	}
}

func TestReleasedWhenTheNodeAnswersAgainAndNotBouncedWithin30s(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := failingDetector(t0, "bridge-nl5")
	acts := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, det)
	p.Confirmed("BL-MAIN", true, t0.Add(3*time.Second))
	b := main3
	b.Override = acts[0].Target
	ok := syn(7)
	ok.Replied, ok.State = true, "ESTABLISHED"
	det.Observe(t0.Add(10*time.Second), []conntrack.Entry{ok}, eps)
	rel := p.Decide(t0.Add(10*time.Second), []Balancer{b}, alive, det)
	if len(rel) != 1 || rel[0].Target != "" || rel[0].Reason != "recovered" {
		t.Fatalf("%+v", rel)
	}
	b.Override = ""
	// The same node flaps: unanswered again at once, but within 30 s of the release.
	det.Observe(t0.Add(11*time.Second), []conntrack.Entry{syn(20), syn(21)}, eps)
	det.Observe(t0.Add(13*time.Second), []conntrack.Entry{syn(20), syn(21)}, eps)
	if again := p.Decide(t0.Add(13*time.Second), []Balancer{b}, alive, det); len(again) != 1 {
		t.Fatalf("a node failing AGAIN after the release must be moved: %+v", again)
	}
}

func TestAFailedConfirmationTriesTheNextCandidate(t *testing.T) {
	t0 := time.Unix(1790000000, 0)
	p := NewPolicy()
	det := failingDetector(t0, "bridge-nl5")
	first := p.Decide(t0.Add(2*time.Second), []Balancer{main3}, alive, det)
	p.Confirmed("BL-MAIN", false, t0.Add(3*time.Second))
	b := main3
	b.Override = first[0].Target
	next := p.Decide(t0.Add(3*time.Second), []Balancer{b}, alive, det)
	if len(next) != 1 || next[0].Target != "bridge-de5" {
		t.Fatalf("%+v", next)
	}
}
```

- [ ] **Step 2: Run them to watch them fail**

Run: `go test ./internal/failover/ -run 'Principle|Pin|Candidate|Released|Confirmation' -count=1`
Expected: build failure — no `NewPolicy`.

- [ ] **Step 3: Implement** (`policy.go`)

```go
package failover

import (
	"sort"
	"time"
)

const ReleaseQuiet = 30 * time.Second

type Balancer struct {
	Tag       string
	Members   []string
	Principle []string
	Override  string
	OwnerPin  string
}

type Health struct {
	Alive   bool
	DelayMs int64
}

type Action struct {
	Balancer, Target, From, Reason string
}

type override struct {
	target, from string // target: what xray holds now, as we set it
	at           time.Time
	tried        map[string]bool
	retry        bool // the confirmation through target failed
}

type Policy struct {
	ours      map[string]*override
	released  map[string]time.Time // balancer|from → when released
	downSince map[string]time.Time
}

func NewPolicy() *Policy {
	return &Policy{ours: map[string]*override{}, released: map[string]time.Time{}, downSince: map[string]time.Time{}}
}

func (p *Policy) candidate(b Balancer, health map[string]Health, det *Detector, now time.Time, skip map[string]bool) string {
	type c struct {
		tag   string
		delay int64
		i     int
	}
	var cs []c
	for i, m := range b.Members {
		h := health[m]
		if !h.Alive || skip[m] || det.Failing(m, now) {
			continue
		}
		cs = append(cs, c{m, h.DelayMs, i})
	}
	if len(cs) == 0 {
		return ""
	}
	sort.Slice(cs, func(a, b int) bool {
		if cs[a].delay != cs[b].delay {
			return cs[a].delay < cs[b].delay
		}
		return cs[a].i < cs[b].i
	})
	return cs[0].tag
}

func (p *Policy) Decide(now time.Time, bals []Balancer, health map[string]Health, det *Detector) []Action {
	var acts []Action
	for _, b := range bals {
		if b.OwnerPin != "" {
			continue
		}
		o := p.ours[b.Tag]
		if b.Override != "" && (o == nil || o.target != b.Override) {
			continue // somebody else's override
		}
		if o != nil {
			switch {
			case o.retry || det.Failing(o.target, now):
				o.tried[o.target] = true
				o.retry = false
				if next := p.candidate(b, health, det, now, o.tried); next != "" {
					o.target, o.at = next, now
					acts = append(acts, Action{b.Tag, next, o.from, "failing"})
				}
			case det.AnsweredSince(o.from, o.at) && !det.Failing(o.from, now):
				acts = append(acts, Action{b.Tag, "", o.from, "recovered"})
				p.released[b.Tag+"|"+o.from] = now
				delete(p.ours, b.Tag)
			}
			continue
		}
		var failing string
		for _, m := range b.Principle {
			if det.Failing(m, now) {
				failing = m
				break
			}
		}
		if failing == "" {
			delete(p.downSince, b.Tag)
			continue
		}
		if r, ok := p.released[b.Tag+"|"+failing]; ok && now.Sub(r) < ReleaseQuiet && !det.AnsweredSince(failing, r) && !p.failedAfter(det, failing, r, now) {
			continue
		}
		next := p.candidate(b, health, det, now, map[string]bool{failing: true})
		if next == "" {
			if _, ok := p.downSince[b.Tag]; !ok {
				p.downSince[b.Tag] = now
			}
			continue
		}
		delete(p.downSince, b.Tag)
		p.ours[b.Tag] = &override{target: next, from: failing, at: now, tried: map[string]bool{failing: true}}
		acts = append(acts, Action{b.Tag, next, failing, "failing"})
	}
	return acts
}

// failedAfter: the node's current unanswered attempts all began after the
// release — it failed again, not still.
func (p *Policy) failedAfter(det *Detector, tag string, released, now time.Time) bool {
	n := 0
	for _, a := range det.attempts {
		if a.tag == tag {
			if a.first.Before(released) {
				return false
			}
			if now.Sub(a.first) >= UnansweredAge {
				n++
			}
		}
	}
	return n >= UnansweredMin
}

func (p *Policy) Confirmed(balancer string, ok bool, now time.Time) {
	if o := p.ours[balancer]; o != nil && !ok {
		o.retry = true // the next Decide moves on to the next candidate
	}
}

func (p *Policy) DownFor(balancer string, now time.Time) time.Duration {
	if t, ok := p.downSince[balancer]; ok {
		return now.Sub(t)
	}
	return 0
}
```

- [ ] **Step 4: Run the package**

Run: `go test ./internal/failover/ -count=1`
Expected: ok (all five policy tests and the three detector tests).

- [ ] **Step 5: Commit**

```bash
git add internal/failover && git commit -m "feat(failover): move a balancer off a failing node, release it when the node answers, never touch the owner's pin"
```

### Task 5: Node endpoints from the render

**Files:**
- Create: `internal/failover/endpoints.go`
- Create: `internal/failover/endpoints_test.go`
- Modify: `internal/controlplane/resolve.go` (export `func MarkedResolver(mark int) *net.Resolver` returning `directResolver(setSocketMark(mark))`)

**Interfaces:**
- Consumes: `xrayview.View.Outbounds` (`Tag`, `Address`, `Port`, `Dials`).
- Produces: `type Lookup func(ctx context.Context, host string) ([]netip.Addr, error)`; `func Endpoints(ctx context.Context, outs []xrayview.Outbound, lookup Lookup) map[netip.AddrPort]string` — every dialling outbound's `Address:Port`, literal IPs as they are, names through `lookup`; a name that fails to resolve is left out (no evidence, never judged).

- [ ] **Step 1: Write the failing test**

```go
package failover

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"vectra-controller-pro/internal/xrayview"
)

func TestEndpointsResolveNamesAndSkipWhatDoesNotResolve(t *testing.T) {
	outs := []xrayview.Outbound{
		{Tag: "bridge-nl5", Address: "ru14.example.net", Port: 50055, Dials: true},
		{Tag: "direct-de5", Address: "203.0.113.9", Port: 443, Dials: true},
		{Tag: "hy2-nl5", Address: "gone.example.net", Port: 50055, Dials: true},
		{Tag: "DIRECT", Dials: false},
	}
	lookup := func(_ context.Context, h string) ([]netip.Addr, error) {
		if h == "ru14.example.net" {
			return []netip.Addr{netip.MustParseAddr("203.0.113.5"), netip.MustParseAddr("203.0.113.6")}, nil
		}
		return nil, errors.New("nxdomain")
	}
	got := Endpoints(context.Background(), outs, lookup)
	want := map[string]string{"203.0.113.5:50055": "bridge-nl5", "203.0.113.6:50055": "bridge-nl5", "203.0.113.9:443": "direct-de5"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[netip.MustParseAddrPort(k)] != v {
			t.Fatalf("%s → %q (%v)", k, got[netip.MustParseAddrPort(k)], got)
		}
	}
}
```

- [ ] **Step 2: Watch it fail**

Run: `go test ./internal/failover/ -run Endpoints -count=1`
Expected: build failure — no `Endpoints`.

- [ ] **Step 3: Implement**

```go
package failover

import (
	"context"
	"net/netip"

	"vectra-controller-pro/internal/xrayview"
)

type Lookup func(ctx context.Context, host string) ([]netip.Addr, error)

func Endpoints(ctx context.Context, outs []xrayview.Outbound, lookup Lookup) map[netip.AddrPort]string {
	eps := map[netip.AddrPort]string{}
	for _, o := range outs {
		if !o.Dials || o.Address == "" || o.Port <= 0 || o.Port > 65535 {
			continue
		}
		var addrs []netip.Addr
		if a, err := netip.ParseAddr(o.Address); err == nil {
			addrs = []netip.Addr{a}
		} else if lookup != nil {
			if got, err := lookup(ctx, o.Address); err == nil {
				addrs = got
			}
		}
		for _, a := range addrs {
			eps[netip.AddrPortFrom(a.Unmap(), uint16(o.Port))] = o.Tag
		}
	}
	return eps
}
```

In `internal/controlplane/resolve.go`:

```go
// MarkedResolver resolves on the control plane's own path (public resolvers,
// SO_MARK = mark), not through the tunnel: node names must resolve while the
// node they name is dead.
func MarkedResolver(mark int) *net.Resolver { return directResolver(setSocketMark(mark)) }
```

- [ ] **Step 4: Run it**

Run: `go test ./internal/failover/ ./internal/controlplane/ -count=1`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add internal/failover internal/controlplane && git commit -m "feat(failover): the render's node endpoints, names resolved on the control plane's path"
```

### Task 6: The watchdog in the daemon

**Files:**
- Create: `cmd/vctl/failover.go`
- Create: `cmd/vctl/failover_test.go`
- Modify: `cmd/vctl/cmd_agent.go` (start `go d.watchFailover(ctx)` next to `go d.watchMemory(ctx)` at line ~487)
- Modify: `internal/agentcfg/agentcfg.go` (`NoFailoverWatchdog bool \`json:"noFailoverWatchdog,omitempty"\``) and `render-xray-config.sh` (UCI `failover_watchdog '0'`)

**Interfaces:**
- Consumes: `conntrack.Read`, `failover.NewDetector/NewPolicy/Endpoints`, `xrayview.Parse`, `api.GetBalancerInfo`, `api.OverrideBalancerTarget`, `api.FetchMetrics`, `localctl.LoadOverrides`, `controlplane.MarkedResolver(firewall.DefaultControlMark)`.
- Produces: `func (d *daemon) watchFailover(ctx context.Context)` (2 s ticker; returns when ctx ends); `func (d *daemon) failoverTick(ctx context.Context, now time.Time)` (one round, testable); package vars for tests: `var failoverConntrack = conntrack.Read`, `var failoverBalancers = api.GetBalancerInfo`, `var failoverOverride = api.OverrideBalancerTarget`, `var failoverMetrics = api.FetchMetrics`, `var failoverConfirm = func(ctx context.Context) bool` (one GET of `http://cp.cloudflare.com/generate_204` with a 3 s timeout through the router's own unmarked path, i.e. through xray).
- Behaviour per tick: return at once when `d.cfg.NoFailoverWatchdog`, route_source is not provider, or the render is missing; re-parse the view only when the render's mtime changed (and then reset endpoints; forget overrides whose balancer or target tag vanished); refresh endpoints every 5 min; `Observe` conntrack; `Decide`; for each action call `failoverOverride`, log `level=WARN msg="node failing; balancer moved"` (balancer, from, to, seconds since first unanswered attempt) or `INFO "balancer back to its own choice"`; after an override of the balancer the catch-all rule uses (`view.Balancers` with Role "main"), call `failoverConfirm` and `Policy.Confirmed`; record incidents: `PROXY_FAILOVER` (key "<balancer> from <node>", details: to, seconds) when a move is confirmed, `PROXY_DOWN` when `DownFor(main) ≥ 60 s` (once per down period).

- [ ] **Step 1: Write the failing test** (`failover_test.go`) — a daemon with a temp render (the `ruDoc`-shaped document with BL-MAIN over `bridge-nl5`/`bridge-pl5` with literal IPs 203.0.113.5/.7), fakes for the five vars: conntrack returns two unanswered SYNs to 203.0.113.5:50055 on every call; balancer info says principle `[bridge-nl5]`, no override; metrics say both alive (nl 40 ms, pl 60 ms); override records its calls; confirm returns true. Tick at t0 and t0+2s. Assert: one override `BL-MAIN → bridge-pl5`, one `PROXY_FAILOVER` incident in the recorder's dir, a WARN log line. A second test with `d.cfg.NoFailoverWatchdog = true` asserts no override. A third: the render replaced by one without `bridge-nl5` → the policy forgets its override (no release call against a missing tag).

(Use the existing test helpers: `testmain_test.go` sets a temp incident dir; `newTestDaemon`-style construction as in `update_controller_test.go` `buildGuardTestDaemon`; set `d.cfg.RouteSource = "provider"` and `d.cfg.XrayRenderPath`.)

- [ ] **Step 2: Watch it fail**

Run: `go test ./cmd/vctl/ -run Failover -count=1`
Expected: build failure — no `failoverTick`.

- [ ] **Step 3: Implement** `cmd/vctl/failover.go` following the interface block above (the daemon keeps `failDet *failover.Detector`, `failPol *failover.Policy`, `failView *xrayview.View`, `failViewStamp string`, `failEps map[netip.AddrPort]string`, `failEpsAt time.Time`, `failDownReported bool`, `failFirst map[string]time.Time` for the seconds figure). The render path is `d.cfg.XrayRenderPath`; the API/metrics addresses are `view.APIListen` / `view.MetricsListen`; owner pins come from `localctl.LoadOverrides(d.cfg.OverridesPath)`.

- [ ] **Step 4: Run cmd/vctl's tests**

Run: `go test ./cmd/vctl/ -count=1`
Expected: ok.

- [ ] **Step 5: Commit**

```bash
git add cmd/vctl internal/agentcfg openwrt/files/usr/libexec/vectra-controller-pro/render-xray-config.sh
git commit -m "feat(vctl): the failover watchdog — a failing node loses new connections in seconds"
```

### Task 7: Stand mode `failover` and its negative control

**Files:**
- Create: `test/dataplane/stand/mode-failover.sh`
- Create: `test/dataplane/stand/failover/subscription.json` (a provider-shaped entry: outbounds `node-a` (10.44.0.2:2001), `node-b` (:2002), `DIRECT` freedom, `BLOCK`; `BL-MAIN` leastLoad expected 1 over `["node-"]`, fallback `DIRECT`; `burstObservatory` interval `300s`, destination `http://44.44.44.44:8081/probe`; rules: bittorrent → DIRECT, catch-all tcp,udp → BL-MAIN)
- Modify: `test/dataplane/stand/stand.sh` (dispatch `failover*` to `run_failover_mode`), `test/dataplane/run.sh` (modes `failover`, `failover-no-watchdog` in ALL_MODES, short names, explanations, MUSTFAIL `failover-no-watchdog="failover_recovers_in_10s"`), `test/dataplane/Dockerfile` (copy `stand/failover/`), `test/dataplane/README.md` (the two rows).

- Scenario (reuse the `ui` mode's nodes xray in the inet ns and its package install; `MODE=failover-no-watchdog` sets UCI `failover_watchdog '0'`):
  1. install, provider mode with the failover subscription, wait for xray; `curl` from the client ns to `http://44.44.44.44/hello` succeeds;
  2. `vctl api` balancer info (or the nodes' access log) names the node BL-MAIN uses — call it U;
  3. in the inet ns drop U's port (`nft add rule inet standguard input tcp dport <port> drop` in a table of its own) and note T0;
  4. every 0.5 s the client requests `/hello` with `--connect-timeout 1`; the first success at T1; record `failover_recovers_in_10s` PASS iff `T1 - T0 ≤ 10 s` (the whole loop bounded at 30 s);
  5. record `failover_logged` (the WARN line in logread) and `failover_incident` (a `PROXY_FAILOVER` file in the reporter's inbox) — skip both in the negative control;
  6. remove the drop; within 30 s `failover_released` (balancer info shows no override) — watchdog mode only.

- [ ] **Step 1:** Write the fixture and the mode script; add the modes to run.sh/stand.sh/Dockerfile/README.
- [ ] **Step 2:** Run `DOCKER_CONTEXT=colima ./test/dataplane/run.sh failover failover-no-watchdog` — Expected: `failover OK — every assertion passed`, `failover-no-watchdog OK — defect detected` (recovery > 10 s with the watchdog off: the observatory's 300 s decides).
- [ ] **Step 3:** If the watchdog run exceeds 10 s, read the timings in the log (first unanswered attempt, override, confirmation) and fix the cause in Task 6's code with a test first — do not relax the assertion.
- [ ] **Step 4:** Commit: `git add test/dataplane && git commit -m "test(stand): failover mode — a dead node's traffic moves within 10 s; the control without the watchdog must fail"`

### Task 8: Migrate a native router to the provider engine

**Files:**
- Create: `cmd/vctl/cmd_route.go`, `cmd/vctl/cmd_route_test.go`
- Modify: `cmd/vctl/main.go` (register `route`)

**Interfaces:**
- Consumes: `routepolicy.ParseUCI` (the native store `/etc/config/vectra_route`), `localctl.UpdateOverrides`, the route geo file for `geoip:ru` (`internal/geodat`), `isRussianDomain` (a helper here: `.ru`, `.su`, `.xn--p1ai`, `.рф` suffixes).
- Produces: `vctl route provider [-entry "🇷🇺🇪🇺 Авто Самый стабильный"] [-apply]` — dry run by default: prints what it would carry (counts, never values), the entry and the UCI change; with `-apply`: writes `Overrides.EntryRemark` (and index from the entries index), appends the carried entries to `Overrides.Proxy` (dedup), sets UCI `vectra-controller-pro.main.route_source=provider`, commits. Carried = entries of the WorldProxy and Special shunt rules' `domain_list` that are Russian domains, and their `ip_list` entries inside `geoip:ru` (from the router's geoip.dat). `geosite:`/`geoip:` references and non-Russian entries are not carried (the provider sends them through the VPN anyway).

- [ ] **Step 1:** Tests: a temp native store with WorldProxy `domain_list='geosite:ANIME\ndomain:kinopoisk.ru\ndomain:example.com'`, `ip_list='5.255.255.5\n1.1.1.1'` (5.255.255.0/24 in a test geoip RU list), Special `domain_list='domain:pornhub.org\ndomain:rutracker.ru'`; dry run prints `carry 3 (2 domains, 1 address)` and changes nothing; `-apply` leaves Overrides.Proxy = [kinopoisk.ru, rutracker.ru, 5.255.255.5], EntryRemark set, and the UCI stand-in records `route_source=provider`.
- [ ] **Step 2:** Watch them fail (`go test ./cmd/vctl/ -run Route -count=1`: no command).
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./cmd/vctl/ -count=1` ok.
- [ ] **Step 5:** Commit: `feat(vctl): route provider — a native router moves to the subscription's engine, carrying what would otherwise go direct`.

### Task 9: Release r21, then 1111

- [ ] Version: `openwrt/Makefile` VECTRA_RELEASE=21; CHANGELOG entry (the outage, the three decisions, the numbers); `docs/CANARY.md` gains «Switching a native router to the provider engine» (dry run, apply, verify, how to switch back: UCI `route_source=native`).
- [ ] Go suite green (`go test -count=1 ./...`), `go vet ./...` clean.
- [ ] Full dataplane stand (all modes incl. `failover`, `failover-no-watchdog`) and the installer suite green.
- [ ] A fresh review of the branch diff since `540827b` (the Review Focus list above verbatim).
- [ ] Publish r21 to pro-canary from a clean checkout; upgrade 1111 with the terminal-job script (signed index, sha256, setsid opkg).
- [ ] On 1111, one job each, never waiting inside a job for the loop: (a) `vctl route provider` dry run; (b) render check of the entry through vctl + `xray -test`; (c) `vctl route provider -apply`; (d) after the loop has re-rendered: sites through the main path, YouTube, TikTok, Telegram, Russian sites (direct), `vectra-reporter status`; (e) the drill: find BL-MAIN's node, drop its address with nft on the router for 60 s, time the first successful request from the router through xray (≤ 10 s), confirm the WARN line and the PROXY_FAILOVER incident, remove the drop, confirm the release.
- [ ] Memory and status entry (`python3 ./scripts/Add-ProRouterStatusEntry.py`).
