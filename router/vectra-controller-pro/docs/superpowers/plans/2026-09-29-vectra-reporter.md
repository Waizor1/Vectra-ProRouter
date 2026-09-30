# vectra-reporter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An autonomous bug reporter for Vectra routers: a separate binary and package that turns crashes and "should not happen" incidents into small, signed, redacted reports and sends them to Vectra Connect.

**Architecture:** vctl and its shell (the dead-man, the init script) drop one JSON file per incident into a tmpfs inbox; vctl also points Go's crash output at a file of its own (`debug.SetCrashOutput`). A separate static Go binary, `vectra-reporter`, run by cron every minute, adds evidence of its own (dmesg OOM kills and kernel errors, vctl's data plane loaded without xray, a reboot it did not see coming), makes redacted reports into a capped spool on flash, and sends them signed with the router's `vr1` token (body hash in the query) over a control-marked socket. Nothing in it depends on vctl's process or binary.

**Tech Stack:** Go (module `vectra-controller-pro`, toolchain 1.26, `go 1.22.0` directive), busybox ash, OpenWrt 24.10 opkg/procd/cron; existing packages `uatoken`, `claim`, `state`, `controlplane`, `memguard`, `routepolicy`, `config`.

**Spec:** `ProRouter/03 Decisions/ADR-0007-router-bug-reports.md` (design); `router/vectra-controller-pro/docs/CONNECT-BUG-REPORTS.md` (server contract).

## Global Constraints

- Endpoint default `https://api-app.vectra-pro.net/errors/router`; request `POST <url>?b=<sha256-hex of the body>`.
- Headers: `User-Agent: VectraRouter/<vctl version> vr1.<token>` (token over the URL **with** `?b=`), `x-hwid`, `x-device-os: OpenWrt`, `x-device-model`, `x-ver-os`, `x-vectra-report-id` (= `reportId`), `Content-Type: application/json`.
- Body schema 1; required: `schema, reportId, code, severity, fingerprint, title, firstAt, lastAt, count, router.deviceId, router.vctl`.
- `evidence.stack` ≤ 16 KB, `evidence.log` ≤ 80 lines, `evidence.kernel` ≤ 40 lines, title ≤ 300 characters, body ≤ 32 KB.
- Severities `low|medium|high|critical`. Codes and default severities: `VCTL_PANIC` critical, `VCTL_CRASH_LOOP` critical, `VCTL_DOWN` high, `DATAPLANE_WITHOUT_XRAY` critical, `XRAY_CRASH` high, `XRAY_RESTART_STORM` high, `OOM_KILL` high, `KERNEL_ERROR` medium, `UNEXPECTED_REBOOT` high, `HANDBACK` high, `APPLY_REFUSED` high, `RESCUE_DIRECT` medium, `SUBSCRIPTION_REFUSED` medium, `GEO_REFUSED` medium, `MEMORY_GROWTH` medium, `REPORTER_ERROR` low, `TEST` low.
- Spool `/etc/vectra-reporter/spool/`: ≤ 32 reports, ≤ 512 KB; a new report of a fingerprint sent in the last 6 h waits for the window's end and counts what comes meanwhile; ≤ 12 sends an hour, ≤ 100 a day; older than 7 days → dropped.
- Responses: 2xx → delete; 400/413/422 → drop; 401/403 → retry in 6 h; 404 → 1 h; 429 → `Retry-After` (default 1 h); 5xx/network → 1, 2, 5, 15, 60 min by attempt.
- Redaction before anything reaches flash: links, URLs, UUIDs, key/token/password values, IPs and MACs of home devices, e-mails, visited hosts, long secret-like tokens; xray's access log is never read.
- Sockets carry `SO_MARK 0x5643` (`firewall.DefaultControlMark`); names are resolved on the marked path (`controlplane`).
- Config `/etc/config/vectra-reporter`: `option enabled '1'` (default), `option url`.
- Inbox `/var/run/vectra-reporter/inbox`, crash files `/var/run/vectra-reporter/crash/vctl.<pid>` (tmpfs); a producer never leaves more than 32 files in the inbox.
- No new third-party dependencies; `CGO_ENABLED=0`; the go directive stays 1.22 — crash capture sits behind `//go:build go1.23` with a no-op beside it.
- Every new user-visible router UI string in ru/en/zh (none in this plan). Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Shell commands RTK-prefixed where the repo's CLAUDE.md asks.

## Review Focus

1. A subscription URL, UUID or Reality key inside `details` (nested maps and lists), not only in log lines, must leave redacted — Task 3 test `TestRedactValueReachesNestedDetails`.
2. A router whose clock is not set (1970, no NTP yet): no token, no send, nothing lost — Task 8 test `TestSendWaitsForTheClock`.
3. An unwritable spool (full flash): the run must not fail or loop; the report is counted as dropped — Task 5 test `TestSpoolAddOnAnUnwritableDirFails` and Task 9 test `TestRunCountsWhatItCannotKeep`.
4. vctl crash-looping: many crash files in an hour → one report with a count, crash files cleared — Task 6 test `TestCrashesOfOneKindMergeIntoOneReport`.
5. An answer that is a redirect elsewhere (a captive portal, a login page): not delivered — Task 8 test `TestSendDoesNotFollowRedirects`.

---

### Task 1: The router's signature, shared

vctl's signed User-Agent maker moves from `package main` into `internal/routersign` so the reporter signs with the same code.

**Files:**
- Create: `internal/routersign/routersign.go`, `internal/routersign/routersign_test.go`
- Modify: `cmd/vctl/signed_ua.go`

**Interfaces:**
- Produces: `routersign.Signer(st state.PersistedState, version string, now func() time.Time) func(hwid, rawURL string) (string, error)`; `routersign.Key(st state.PersistedState) (claim.Key, error)`.

- [ ] **Step 1: Write the failing test** — `internal/routersign/routersign_test.go`:

```go
package routersign

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uatoken"
)

// A token made for a URL with a query opens with the key the panel named, for
// that path and that query only: a report's body hash rides in the query.
func TestSignerCoversThePathAndQuery(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	vectra, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	st := state.PersistedState{
		DeviceIdentifier: "vectra-0123456789ab",
		DevicePrivateKey: base64.StdEncoding.EncodeToString(priv),
		ClaimKey:         &controlplane.ClaimKey{Kid: 7, PublicKey: base64.StdEncoding.EncodeToString(vectra.PublicKey().Bytes())},
	}
	now := time.Unix(1790000000, 0)
	hwid := "4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"
	ua, err := Signer(st, "0.6.0-r19", func() time.Time { return now })(hwid, "https://api-app.vectra-pro.net/errors/router?b=abc123")
	if err != nil {
		t.Fatal(err)
	}
	keys := func(kid byte) (*ecdh.PrivateKey, bool) { return vectra, kid == 7 }
	devices := func(id string) (ed25519.PublicKey, bool) { return pub, id == st.DeviceIdentifier }
	if _, err := uatoken.Open(ua, keys, devices, hwid, "/errors/router?b=abc123", now, time.Minute); err != nil {
		t.Fatalf("does not open for its own path and query: %v", err)
	}
	if _, err := uatoken.Open(ua, keys, devices, hwid, "/errors/router?b=abc124", now, time.Minute); err == nil {
		t.Fatal("opened for another body hash")
	}
}

// Without a key from check-in a token is sealed to the built-in one.
func TestKeyIsTheBuiltinOneWithoutCheckIn(t *testing.T) {
	k, err := Key(state.PersistedState{})
	if err != nil || k.Kid != uatoken.BuiltinKid {
		t.Fatalf("key %+v, err %v", k, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/routersign/`
Expected: FAIL — `undefined: Signer`, `undefined: Key`.

- [ ] **Step 3: Write the package** — `internal/routersign/routersign.go`:

```go
// Package routersign makes the router's own User-Agent,
// VectraRouter/<version> vr1.<token> (internal/uatoken), from what vctl keeps
// in state.json: the device id and key, and Vectra's key — the one the panel
// named in check-in (claimKey), else the built-in one. vctl signs its
// subscription fetches with it; the reporter (vectra-reporter) its reports.
package routersign

import (
	"crypto/rand"
	"fmt"
	"time"

	"vectra-controller-pro/internal/claim"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uatoken"
)

// Signer makes the User-Agent for one request from st. Without a device key,
// or with a clock that is not set yet, it fails, and the request then
// carries none.
func Signer(st state.PersistedState, version string, now func() time.Time) func(hwid, rawURL string) (string, error) {
	return func(hwid, rawURL string) (string, error) {
		dev, err := claim.DeviceKey(st.DevicePrivateKey)
		if err != nil {
			return "", fmt.Errorf("no device key in state.json: %w", err)
		}
		key, err := Key(st)
		if err != nil {
			return "", err
		}
		return uatoken.UserAgent(uatoken.Request{
			Version:   version,
			DeviceID:  st.DeviceIdentifier,
			DeviceKey: dev,
			HWID:      hwid,
			URL:       rawURL,
			Kid:       key.Kid,
			ServerPub: key.PublicKey,
			Now:       now(),
		}, rand.Reader)
	}
}

// Key is Vectra's key a token is sealed to: the one the panel named in
// check-in (claimKey), else the built-in one.
func Key(st state.PersistedState) (claim.Key, error) {
	if k := st.ClaimKey; k != nil {
		if key, err := claim.ParseKey(k.Kid, k.PublicKey); err == nil {
			return key, nil
		}
	}
	return claim.ParseKey(uatoken.BuiltinKid, uatoken.BuiltinKey)
}
```

- [ ] **Step 4: Point vctl at it** — in `cmd/vctl/signed_ua.go` replace the bodies of `uaSigner` and `uaKey`:

```go
func uaSigner(st state.PersistedState, version string, now func() time.Time) func(hwid, url string) (string, error) {
	return routersign.Signer(st, version, now)
}

func uaKey(st state.PersistedState) (claim.Key, error) { return routersign.Key(st) }
```

and the imports to `time`, `vectra-controller-pro/internal/claim`, `vectra-controller-pro/internal/config`, `vectra-controller-pro/internal/routersign`, `vectra-controller-pro/internal/state`, `vectra-controller-pro/internal/uaguard` (drop `crypto/rand`, `fmt`, `uatoken`).

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/routersign/ ./cmd/vctl/ -run 'Signer|Key|OwnAgent|SignedAgent|ConfiguredAgent'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
rtk git add internal/routersign cmd/vctl/signed_ua.go
rtk git commit -m "refactor(vctl): the router's signature in internal/routersign"
```

---

### Task 2: The inbox

One JSON file per incident, written by vctl (Go) and its shell, read by the reporter.

**Files:**
- Create: `internal/incident/incident.go`, `internal/incident/incident_test.go`

**Interfaces:**
- Produces:
  - `type Incident struct { Code, Severity, Key, Title string; At time.Time; Source string; Details map[string]any; Log []string; Stack string; Kernel []string }` (JSON: `code, severity, key, title, at, source, details, log, stack, kernel`)
  - `var Dir, CrashDir string` (`/var/run/vectra-reporter/inbox`, `/var/run/vectra-reporter/crash`); `const MaxQueued = 32`
  - `func NewRecorder(dir string, every time.Duration) *Recorder`; `func (r *Recorder) Record(in Incident) bool`
  - `type Pending struct { Path string; Incident }`; `func Read(dir string) []Pending` (oldest `At` first; an unreadable file comes back with `Code == ""`)

- [ ] **Step 1: Write the failing tests** — `internal/incident/incident_test.go`:

```go
package incident

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecorderWritesOnePerCodePerInterval(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(1790000000, 0)
	r := NewRecorder(dir, 10*time.Minute)
	r.now = func() time.Time { return now }
	if !r.Record(Incident{Code: "XRAY_CRASH", Key: "exit 2", Title: "xray exited", Source: "vctl"}) {
		t.Fatal("the first was not written")
	}
	if r.Record(Incident{Code: "XRAY_CRASH", Key: "exit 2", Title: "again", Source: "vctl"}) {
		t.Fatal("the same code within the interval was written")
	}
	if !r.Record(Incident{Code: "APPLY_REFUSED", Key: "k", Title: "t", Source: "vctl"}) {
		t.Fatal("another code was held back")
	}
	now = now.Add(11 * time.Minute)
	if !r.Record(Incident{Code: "XRAY_CRASH", Key: "exit 2", Title: "later", Source: "vctl"}) {
		t.Fatal("after the interval it was held back")
	}
	got := Read(dir)
	if len(got) != 3 || got[0].Code != "XRAY_CRASH" || got[0].At.IsZero() || got[2].Title != "later" {
		t.Fatalf("%+v", got)
	}
}

func TestRecorderNeverFillsTheInboxPastItsCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxQueued; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.json", i)), []byte(`{"code":"X"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if NewRecorder(dir, time.Minute).Record(Incident{Code: "Y", Key: "k", Title: "t"}) {
		t.Fatal("wrote past the cap")
	}
}

// The shell's one line of JSON (the dead-man's, the init script's) reads as
// an incident; a file that is not one comes back without a code.
func TestReadTakesTheShellsLine(t *testing.T) {
	dir := t.TempDir()
	line := `{"code":"HANDBACK","key":"renderer missing","title":"vctl handed the router back: renderer missing","at":"2026-09-29T21:03:11Z","source":"init"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "1790000000000000000-HANDBACK-42.json"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.json"), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Read(dir)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	var hb Pending
	for _, p := range got {
		if p.Code == "HANDBACK" {
			hb = p
		}
	}
	if hb.Source != "init" || hb.At.UTC().Format(time.RFC3339) != "2026-09-29T21:03:11Z" || hb.Path == "" {
		t.Fatalf("%+v", got)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/incident/`
Expected: FAIL — `undefined: NewRecorder`.

- [ ] **Step 3: Write the package** — `internal/incident/incident.go`:

```go
// Package incident is how vctl and its shell — the dead-man, the init
// script — tell the reporter (vectra-reporter, ADR-0007) that something went
// other than meant: one small JSON file per incident in the inbox, a tmpfs
// directory the reporter empties every minute. Nothing here sends anything:
// without the reporter the files stay, never more than MaxQueued, until a
// reboot clears tmpfs.
package incident

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Incident is one thing that went other than meant. Key is what makes two
// alike — the reporter's fingerprint is made of Code and Key — so it holds
// no times, counts or addresses.
type Incident struct {
	Code     string         `json:"code"`
	Severity string         `json:"severity,omitempty"`
	Key      string         `json:"key"`
	Title    string         `json:"title"`
	At       time.Time      `json:"at"`
	Source   string         `json:"source"`
	Details  map[string]any `json:"details,omitempty"`
	Log      []string       `json:"log,omitempty"`
	Stack    string         `json:"stack,omitempty"`
	Kernel   []string       `json:"kernel,omitempty"`
}

// Dir is the inbox; CrashDir holds vctl's crash output (vctl.<pid>).
var (
	Dir      = "/var/run/vectra-reporter/inbox"
	CrashDir = "/var/run/vectra-reporter/crash"
)

// MaxQueued caps the inbox: a producer never writes past it.
const MaxQueued = 32

// Recorder writes incidents into its directory, one per code in every
// interval at most.
type Recorder struct {
	dir   string
	every time.Duration
	now   func() time.Time
	mu    sync.Mutex
	last  map[string]time.Time
}

// NewRecorder writes into dir, at most one incident of a code per every.
func NewRecorder(dir string, every time.Duration) *Recorder {
	return &Recorder{dir: dir, every: every, now: time.Now, last: map[string]time.Time{}}
}

// Record writes in, and says whether it did: not within the interval of the
// same code, not into a full inbox, not when the write fails.
func (r *Recorder) Record(in Incident) bool {
	if r == nil || in.Code == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if last, ok := r.last[in.Code]; ok && now.Sub(last) < r.every {
		return false
	}
	if in.At.IsZero() {
		in.At = now.UTC()
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return false
	}
	if queued, _ := filepath.Glob(filepath.Join(r.dir, "*.json")); len(queued) >= MaxQueued {
		return false
	}
	b, err := json.Marshal(in)
	if err != nil {
		return false
	}
	name := filepath.Join(r.dir, fmt.Sprintf("%d-%s-%d.json", now.UnixNano(), in.Code, os.Getpid()))
	tmp := filepath.Join(r.dir, "."+filepath.Base(name)+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		_ = os.Remove(tmp)
		return false
	}
	if err := os.Rename(tmp, name); err != nil {
		_ = os.Remove(tmp)
		return false
	}
	r.last[in.Code] = now
	return true
}

// Pending is an incident in the inbox and its file.
type Pending struct {
	Path string
	Incident
}

// Read lists the inbox, oldest first. A file that is not an incident comes
// back with no Code: the reporter takes it away.
func Read(dir string) []Pending {
	names, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	out := make([]Pending, 0, len(names))
	for _, n := range names {
		p := Pending{Path: n}
		if b, err := os.ReadFile(n); err == nil {
			if json.Unmarshal(b, &p.Incident) != nil {
				p.Incident = Incident{}
			}
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/incident/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/incident
rtk git commit -m "feat(reporter): the incident inbox"
```

---

### Task 3: Redaction

**Files:**
- Create: `internal/bugreport/redact.go`, `internal/bugreport/redact_test.go`

**Interfaces:**
- Produces: `func Redact(s string) string`; `func RedactLines(lines []string) []string`; `func RedactValue(v any) any` (maps, slices and strings, recursively; other values as they are).

- [ ] **Step 1: Write the failing tests** — `internal/bugreport/redact_test.go`:

```go
package bugreport

import (
	"reflect"
	"testing"
)

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"fetch https://sub.example.com/api/sub/AbCdEf?x=1 failed":              "fetch <url> failed",
		"node vless://2b1c8f4a-9d3e-4f5a-8b6c-7d8e9f0a1b2c@1.2.3.4:443?sni=x#DE": "node <link>",
		"user 2b1c8f4a-9d3e-4f5a-8b6c-7d8e9f0a1b2c logged":                       "user <uuid> logged",
		"client 192.168.1.23 (aa:bb:cc:dd:ee:ff)":                                "client <lan> (<mac>)",
		"dial tcp 203.0.113.9:443: i/o timeout":                                  "dial tcp <ip>:443: i/o timeout",
		"fake 198.18.0.7, lo 127.0.0.1, any 0.0.0.0":                             "fake 198.18.0.7, lo 127.0.0.1, any 0.0.0.0",
		"lookup instagram.com: no such host":                                     "lookup <host>: no such host",
		"panel router.vectra-pro.net answered":                                   "panel router.vectra-pro.net answered",
		"geosite.dat and xray.json stay":                                         "geosite.dat and xray.json stay",
		"password=hunter2 token: abc api_key=zzz":                                "password=<redacted> token: <redacted> api_key=<redacted>",
		`{"privateKey":"kL0mNoPq","port":443}`:                                   `{"privateKey":"<redacted>","port":443}`,
		"hwid 4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7":  "hwid <secret>",
		"v6 2001:db8:85a3::8a2e:370:7334 and ::1":                                "v6 <ip6> and ::1",
		"at 21:03:11 it stopped":                                                 "at 21:03:11 it stopped",
		"mail me at someone@example.org":                                         "mail me at <email>",
		"vectra-controller-pro/internal/apply.(*Applier).Apply(0x4000123456)":    "vectra-controller-pro/internal/apply.(*Applier).Apply(0x4000123456)",
		"\t/usr/lib/go/src/runtime/proc.go:402 +0x1c8":                           "\t/usr/lib/go/src/runtime/proc.go:402 +0x1c8",
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestRedactValueReachesNestedDetails(t *testing.T) {
	in := map[string]any{
		"url":   "https://sub.example.com/api/sub/secret",
		"nodes": []any{"vless://2b1c8f4a-9d3e-4f5a-8b6c-7d8e9f0a1b2c@h:1", map[string]any{"pbk": "x key=abc"}},
		"count": 3,
		"ok":    true,
	}
	want := map[string]any{
		"url":   "<url>",
		"nodes": []any{"<link>", map[string]any{"pbk": "x key=<redacted>"}},
		"count": 3,
		"ok":    true,
	}
	if got := RedactValue(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/bugreport/ -run Redact`
Expected: FAIL — `undefined: Redact`.

- [ ] **Step 3: Write the redaction** — `internal/bugreport/redact.go`:

```go
package bugreport

import (
	"net"
	"regexp"
	"strings"
)

// What leaves the router says what went wrong, never whose it was: no links
// or URLs (a subscription's is its owner's key), no UUIDs, keys, tokens or
// passwords, no addresses of the home's devices, no e-mails, no host a person
// visited. The router's own and Vectra's names stay, and so do what makes a
// stack a stack: package paths, file:line, pointers.

var (
	reLink   = regexp.MustCompile(`(?i)\b(?:vless|vmess|trojan|ss|ssr|hy2|hysteria2?|tuic|wireguard|socks5?)://\S+`)
	reURL    = regexp.MustCompile(`(?i)\bhttps?://[^\s"'<>]+`)
	reEmail  = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	reJSONKV = regexp.MustCompile(`(?i)"(\w*(?:password|passwd|token|secret|auth|pbk|psk|key)|sid|shortId|uuid)"\s*:\s*"[^"]*"`)
	reKV     = regexp.MustCompile(`(?i)\b(\w*(?:password|passwd|token|secret|auth|pbk|psk|key)|sid|uuid)(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&"']+)`)
	reUUID   = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	reMAC    = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}\b`)
	reIPv6   = regexp.MustCompile(`(?i)[0-9a-f]{0,4}(?::[0-9a-f]{0,4}){2,7}`)
	reIPv4   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	reHost   = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,24}\b`)
	reLong   = regexp.MustCompile(`[A-Za-z0-9+=_-]{32,}`)
)

// ownDomains are names that say nothing about a person: Vectra's, OpenWrt's.
var ownDomains = []string{"vectra-pro.net", "openwrt.org"}

// tlds are the endings a visited host has; a file name (geosite.dat,
// xray.json, proc.go) has none of them.
var tlds = map[string]bool{}

func init() {
	for _, t := range strings.Fields(`com net org ru su io me app dev co cc tv info biz xyz top site online
		store pro uk de nl fr us eu by kz ua pl ae jp cn in link to gg ai cloud live news shop space
		tech fm so sh ws im ly tk ga ml cf gq ir tr vn id br mx ca au es it se no fi dk ch at be cz`) {
		tlds[t] = true
	}
}

// Redact takes out of one line whatever says whose it was.
func Redact(s string) string {
	s = reLink.ReplaceAllString(s, "<link>")
	s = reURL.ReplaceAllString(s, "<url>")
	s = reEmail.ReplaceAllString(s, "<email>")
	s = reJSONKV.ReplaceAllString(s, `"$1":"<redacted>"`)
	s = reKV.ReplaceAllString(s, "$1$2<redacted>")
	s = reUUID.ReplaceAllString(s, "<uuid>")
	s = reMAC.ReplaceAllString(s, "<mac>")
	s = reIPv6.ReplaceAllStringFunc(s, ipv6)
	s = reIPv4.ReplaceAllStringFunc(s, ipv4)
	s = reHost.ReplaceAllStringFunc(s, host)
	s = reLong.ReplaceAllStringFunc(s, long)
	return s
}

// RedactLines is Redact on every line.
func RedactLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = Redact(l)
	}
	return out
}

// RedactValue is Redact on every string in v, however deep in maps and lists.
func RedactValue(v any) any {
	switch x := v.(type) {
	case string:
		return Redact(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = RedactValue(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = RedactValue(e)
		}
		return out
	case []string:
		return RedactLines(x)
	}
	return v
}

func ipv4(s string) string {
	ip := net.ParseIP(s).To4()
	if ip == nil {
		return s
	}
	switch {
	case ip.IsLoopback(), ip.IsUnspecified(), ip.IsMulticast(), ip.Equal(net.IPv4bcast):
		return s
	case ip[0] == 198 && (ip[1] == 18 || ip[1] == 19): // xray's fake-IP pool
		return s
	case ip.IsPrivate(), ip.IsLinkLocalUnicast(), ip[0] == 100 && ip[1]&0xc0 == 64:
		return "<lan>"
	}
	return "<ip>"
}

func ipv6(s string) string {
	ip := net.ParseIP(strings.Trim(s, "[]"))
	if ip == nil || ip.To4() != nil || ip.IsLoopback() || ip.IsUnspecified() {
		return s
	}
	return "<ip6>"
}

func host(s string) string {
	l := strings.ToLower(s)
	i := strings.LastIndexByte(l, '.')
	if i < 0 || !tlds[l[i+1:]] {
		return s
	}
	for _, d := range ownDomains {
		if l == d || strings.HasSuffix(l, "."+d) {
			return s
		}
	}
	return "<host>"
}

// long: a run of 32 or more with digits and letters in it is a key, a token
// or a hash; package paths and words have no digits.
func long(s string) string {
	var digit, letter bool
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
			letter = true
		}
	}
	if digit && letter {
		return "<secret>"
	}
	return s
}
```

- [ ] **Step 4: Run the tests; adjust patterns only where a case in the table fails, never the table**

Run: `go test ./internal/bugreport/ -run Redact -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/bugreport/redact.go internal/bugreport/redact_test.go
rtk git commit -m "feat(reporter): redaction"
```

---

### Task 4: The report

**Files:**
- Create: `internal/bugreport/report.go`, `internal/bugreport/report_test.go`

**Interfaces:**
- Consumes: `incident.Incident` (Task 2), `Redact`, `RedactLines`, `RedactValue` (Task 3).
- Produces:
  - `type Report struct { Schema int; ReportID, Code, Severity, Fingerprint, Title string; FirstAt, LastAt time.Time; Count int; Router Router; Evidence Evidence }` (JSON names as in the contract)
  - `type Router struct { DeviceID, Model, Arch, OpenWrt, Vctl, Xray, Reporter, RouteSource string; KillSwitch *bool; UptimeSec int64; MemTotalMiB, MemAvailableMiB uint64 }`
  - `type Evidence struct { Stack string; Log, Kernel []string; Memory, Details map[string]any }`
  - `const MaxStack = 16 << 10; MaxLog = 80; MaxKernel = 40; MaxBody = 32 << 10; MaxTitle = 300`
  - `func Fingerprint(code, key string) string`; `func NewID() string`; `func DefaultSeverity(code string) string`; `func SeverityRank(s string) int`
  - `func FromIncident(in incident.Incident, r Router, now time.Time) Report`; `func (r Report) Encode() ([]byte, error)`

- [ ] **Step 1: Write the failing tests** — `internal/bugreport/report_test.go`:

```go
package bugreport

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vectra-controller-pro/internal/incident"
)

func TestFromIncidentRedactsAndFingerprints(t *testing.T) {
	now := time.Unix(1790000000, 0).UTC()
	in := incident.Incident{
		Code: "APPLY_REFUSED", Key: "xray: failed to load geosite", Title: "xray -test refused https://sub.example.com/x",
		Source: "vctl", Details: map[string]any{"url": "https://sub.example.com/x"},
		Log: []string{"dial 203.0.113.9:443"},
	}
	r := FromIncident(in, Router{DeviceID: "vectra-0123456789ab", Vctl: "0.6.0-r19"}, now)
	if r.Schema != 1 || r.Count != 1 || r.Severity != "high" || !r.FirstAt.Equal(now) || !r.LastAt.Equal(now) {
		t.Fatalf("%+v", r)
	}
	if r.Fingerprint != Fingerprint("APPLY_REFUSED", "xray: failed to load geosite") || len(r.Fingerprint) != 64 {
		t.Fatalf("fingerprint %q", r.Fingerprint)
	}
	if strings.Contains(r.Title, "sub.example.com") || r.Evidence.Details["url"] != "<url>" || r.Evidence.Log[0] != "dial <ip>:443" {
		t.Fatalf("not redacted: %+v", r)
	}
	if len(r.ReportID) != 36 || r.ReportID == NewID() {
		t.Fatalf("report id %q", r.ReportID)
	}
}

func TestEncodeKeepsTheBodyUnderItsCap(t *testing.T) {
	r := Report{Schema: 1, ReportID: NewID(), Code: "VCTL_PANIC", Severity: "critical", Fingerprint: Fingerprint("VCTL_PANIC", "k"),
		Title: strings.Repeat("т", 500), Count: 1, Router: Router{DeviceID: "d", Vctl: "v"}}
	r.Evidence.Stack = strings.Repeat("goroutine 1 [running]:\n", 3000)
	for i := 0; i < 500; i++ {
		r.Evidence.Log = append(r.Evidence.Log, strings.Repeat("x", 200))
	}
	b, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > MaxBody {
		t.Fatalf("%d bytes", len(b))
	}
	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if len([]rune(back.Title)) > MaxTitle || len(back.Evidence.Log) > MaxLog || len(back.Evidence.Stack) > MaxStack+8 {
		t.Fatalf("title %d, log %d, stack %d", len([]rune(back.Title)), len(back.Evidence.Log), len(back.Evidence.Stack))
	}
	if back.Router.Vctl != "v" || back.Code != "VCTL_PANIC" {
		t.Fatal("the facts went with the evidence")
	}
}

func TestSeverities(t *testing.T) {
	if DefaultSeverity("VCTL_PANIC") != "critical" || DefaultSeverity("NOT_A_CODE") != "medium" {
		t.Fatal(DefaultSeverity("VCTL_PANIC"), DefaultSeverity("NOT_A_CODE"))
	}
	if !(SeverityRank("critical") > SeverityRank("high") && SeverityRank("high") > SeverityRank("medium") && SeverityRank("medium") > SeverityRank("low")) {
		t.Fatal("ranks out of order")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/bugreport/ -run 'FromIncident|Encode|Severities'`
Expected: FAIL — `undefined: FromIncident`.

- [ ] **Step 3: Write the model** — `internal/bugreport/report.go`:

```go
// Package bugreport is the reporter's (vectra-reporter, ADR-0007): reports
// made of incidents, redacted before they reach flash, kept in a capped
// spool, and sent signed to Vectra Connect (docs/CONNECT-BUG-REPORTS.md).
package bugreport

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"vectra-controller-pro/internal/incident"
)

// Caps of the contract.
const (
	MaxStack  = 16 << 10
	MaxLog    = 80
	MaxKernel = 40
	MaxBody   = 32 << 10
	MaxTitle  = 300
)

// Report is one report: schema 1 of the contract.
type Report struct {
	Schema      int       `json:"schema"`
	ReportID    string    `json:"reportId"`
	Code        string    `json:"code"`
	Severity    string    `json:"severity"`
	Fingerprint string    `json:"fingerprint"`
	Title       string    `json:"title"`
	FirstAt     time.Time `json:"firstAt"`
	LastAt      time.Time `json:"lastAt"`
	Count       int       `json:"count"`
	Router      Router    `json:"router"`
	Evidence    Evidence  `json:"evidence"`
}

// Router is what every report says about the router.
type Router struct {
	DeviceID        string `json:"deviceId"`
	Model           string `json:"model,omitempty"`
	Arch            string `json:"arch,omitempty"`
	OpenWrt         string `json:"openwrt,omitempty"`
	Vctl            string `json:"vctl"`
	Xray            string `json:"xray,omitempty"`
	Reporter        string `json:"reporter,omitempty"`
	RouteSource     string `json:"routeSource,omitempty"`
	KillSwitch      *bool  `json:"killSwitch,omitempty"`
	UptimeSec       int64  `json:"uptimeSec,omitempty"`
	MemTotalMiB     uint64 `json:"memTotalMiB,omitempty"`
	MemAvailableMiB uint64 `json:"memAvailableMiB,omitempty"`
}

// Evidence is what the report shows of the failure.
type Evidence struct {
	Stack   string         `json:"stack,omitempty"`
	Log     []string       `json:"log,omitempty"`
	Kernel  []string       `json:"kernel,omitempty"`
	Memory  map[string]any `json:"memory,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

var severities = map[string]string{
	"VCTL_PANIC": "critical", "VCTL_CRASH_LOOP": "critical", "DATAPLANE_WITHOUT_XRAY": "critical",
	"VCTL_DOWN": "high", "XRAY_CRASH": "high", "XRAY_RESTART_STORM": "high", "OOM_KILL": "high",
	"UNEXPECTED_REBOOT": "high", "HANDBACK": "high", "APPLY_REFUSED": "high",
	"KERNEL_ERROR": "medium", "RESCUE_DIRECT": "medium", "SUBSCRIPTION_REFUSED": "medium",
	"GEO_REFUSED": "medium", "MEMORY_GROWTH": "medium",
	"REPORTER_ERROR": "low", "TEST": "low", "MANUAL": "low",
}

// DefaultSeverity is a code's severity; an unknown code's is medium.
func DefaultSeverity(code string) string {
	if s, ok := severities[code]; ok {
		return s
	}
	return "medium"
}

// SeverityRank orders severities: low 0 … critical 3; unknown as medium.
func SeverityRank(s string) int {
	switch s {
	case "low":
		return 0
	case "high":
		return 2
	case "critical":
		return 3
	}
	return 1
}

// Fingerprint is what makes two reports one problem: the code and the
// incident's key.
func Fingerprint(code, key string) string {
	h := sha256.Sum256([]byte(code + "\n" + key))
	return hex.EncodeToString(h[:])
}

// NewID is a random UUID (version 4): a report's id, the same through every
// retry, so the server counts it once.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// FromIncident makes a report of in: its own words, redacted and capped.
func FromIncident(in incident.Incident, r Router, now time.Time) Report {
	at := in.At
	if at.IsZero() {
		at = now
	}
	sev := in.Severity
	if _, ok := map[string]bool{"low": true, "medium": true, "high": true, "critical": true}[sev]; !ok {
		sev = DefaultSeverity(in.Code)
	}
	rep := Report{
		Schema: 1, ReportID: NewID(), Code: in.Code, Severity: sev,
		Fingerprint: Fingerprint(in.Code, in.Key), Title: capRunes(Redact(in.Title), MaxTitle),
		FirstAt: at.UTC(), LastAt: at.UTC(), Count: 1, Router: r,
	}
	if len(in.Details) > 0 {
		rep.Evidence.Details, _ = RedactValue(in.Details).(map[string]any)
	}
	rep.Evidence.Log = tail(RedactLines(in.Log), MaxLog)
	rep.Evidence.Kernel = tail(RedactLines(in.Kernel), MaxKernel)
	if in.Stack != "" {
		rep.Evidence.Stack = capBytes(Redact(in.Stack), MaxStack)
	}
	return rep
}

// Encode is the report's body, at most MaxBody: the log gives way first,
// then the kernel's lines, then the stack, then the details.
func (r Report) Encode() ([]byte, error) {
	r.Title = capRunes(r.Title, MaxTitle)
	r.Evidence.Stack = capBytes(r.Evidence.Stack, MaxStack)
	r.Evidence.Log = tail(r.Evidence.Log, MaxLog)
	r.Evidence.Kernel = tail(r.Evidence.Kernel, MaxKernel)
	for step := 0; ; step++ {
		b, err := json.Marshal(r)
		if err != nil || len(b) <= MaxBody {
			return b, err
		}
		switch {
		case len(r.Evidence.Log) > 0:
			r.Evidence.Log = r.Evidence.Log[(len(r.Evidence.Log)+1)/2:]
		case len(r.Evidence.Kernel) > 0:
			r.Evidence.Kernel = r.Evidence.Kernel[(len(r.Evidence.Kernel)+1)/2:]
		case len(r.Evidence.Stack) > 512:
			r.Evidence.Stack = capBytes(r.Evidence.Stack, len(r.Evidence.Stack)/2)
		case r.Evidence.Details != nil || r.Evidence.Memory != nil:
			r.Evidence.Details, r.Evidence.Memory = nil, nil
		default:
			return b, nil // the facts alone; nothing more to give
		}
	}
}

func capRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !isRuneStart(s[n]) {
		n--
	}
	return s[:n] + "\n…"
}

func isRuneStart(b byte) bool { return b&0xc0 != 0x80 }

func tail(lines []string, n int) []string {
	if len(lines) > n {
		return lines[len(lines)-n:]
	}
	return lines
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bugreport/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/bugreport/report.go internal/bugreport/report_test.go
rtk git commit -m "feat(reporter): reports, fingerprints, the body's caps"
```

---

### Task 5: The spool

**Files:**
- Create: `internal/bugreport/spool.go`, `internal/bugreport/spool_test.go`

**Interfaces:**
- Consumes: `Report`, `SeverityRank` (Task 4).
- Produces:
  - `type Meta struct { QueuedAt, NextAt time.Time; Attempts int; LastStatus string }`
  - `type Entry struct { Report Report; Meta Meta }` (JSON `report`, `meta`)
  - `type Spool struct { Dir string; MaxFiles int; MaxBytes int64 }`
  - `func (s Spool) Entries() []Entry` (oldest queued first)
  - `func (s Spool) Add(r Report, now, notBefore time.Time) (merged bool, evicted int, err error)`
  - `func (s Spool) Save(e Entry) error`; `func (s Spool) Remove(id string) error`

- [ ] **Step 1: Write the failing tests** — `internal/bugreport/spool_test.go`:

```go
package bugreport

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func rep(code, key, sev string, at time.Time) Report {
	return Report{Schema: 1, ReportID: NewID(), Code: code, Severity: sev, Fingerprint: Fingerprint(code, key),
		Title: code, FirstAt: at, LastAt: at, Count: 1, Router: Router{DeviceID: "d", Vctl: "v"}}
}

func TestSpoolMergesTheSameFingerprint(t *testing.T) {
	s := Spool{Dir: t.TempDir(), MaxFiles: 32, MaxBytes: 512 << 10}
	t0 := time.Unix(1790000000, 0).UTC()
	if merged, _, err := s.Add(rep("XRAY_CRASH", "exit 2", "high", t0), t0, time.Time{}); merged || err != nil {
		t.Fatal(merged, err)
	}
	if merged, _, err := s.Add(rep("XRAY_CRASH", "exit 2", "high", t0.Add(time.Minute)), t0.Add(time.Minute), time.Time{}); !merged || err != nil {
		t.Fatal(merged, err)
	}
	es := s.Entries()
	if len(es) != 1 || es[0].Report.Count != 2 || !es[0].Report.LastAt.Equal(t0.Add(time.Minute)) || !es[0].Report.FirstAt.Equal(t0) {
		t.Fatalf("%+v", es)
	}
}

func TestSpoolEvictsTheLeastSevereOldestFirst(t *testing.T) {
	s := Spool{Dir: t.TempDir(), MaxFiles: 3, MaxBytes: 512 << 10}
	t0 := time.Unix(1790000000, 0).UTC()
	crit := rep("VCTL_PANIC", "p", "critical", t0)
	s.Add(crit, t0, time.Time{})
	for i := 0; i < 4; i++ {
		at := t0.Add(time.Duration(i+1) * time.Minute)
		s.Add(rep("KERNEL_ERROR", string(rune('a'+i)), "medium", at), at, time.Time{})
	}
	es := s.Entries()
	if len(es) != 3 || es[0].Report.ReportID != crit.ReportID {
		t.Fatalf("%d kept, first %s", len(es), es[0].Report.Code)
	}
	if es[1].Report.Fingerprint != Fingerprint("KERNEL_ERROR", "c") {
		t.Fatalf("the oldest medium ones did not go first: %+v", es)
	}
}

func TestSpoolHoldsANewOneBackAndDropsWhatItCannotRead(t *testing.T) {
	s := Spool{Dir: t.TempDir(), MaxFiles: 32, MaxBytes: 512 << 10}
	t0 := time.Unix(1790000000, 0).UTC()
	s.Add(rep("OOM_KILL", "xray", "high", t0), t0, t0.Add(6*time.Hour))
	if err := os.WriteFile(filepath.Join(s.Dir, "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	es := s.Entries()
	if len(es) != 1 || !es[0].Meta.NextAt.Equal(t0.Add(6*time.Hour)) {
		t.Fatalf("%+v", es)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "broken.json")); !os.IsNotExist(err) {
		t.Fatal("a file that is no entry stayed")
	}
}

func TestSpoolAddOnAnUnwritableDirFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "file-not-dir")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := Spool{Dir: dir, MaxFiles: 32, MaxBytes: 512 << 10}
	t0 := time.Unix(1790000000, 0).UTC()
	if _, _, err := s.Add(rep("TEST", "t", "low", t0), t0, time.Time{}); err == nil {
		t.Fatal("no error")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/bugreport/ -run Spool`
Expected: FAIL — `undefined: Spool`.

- [ ] **Step 3: Write the spool** — `internal/bugreport/spool.go`:

```go
package bugreport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Meta is what the spool knows of a report's way out.
type Meta struct {
	QueuedAt   time.Time `json:"queuedAt"`
	NextAt     time.Time `json:"nextAt,omitempty"`
	Attempts   int       `json:"attempts,omitempty"`
	LastStatus string    `json:"lastStatus,omitempty"`
}

// Entry is a report waiting to go.
type Entry struct {
	Report Report `json:"report"`
	Meta   Meta   `json:"meta"`
}

// Spool keeps reports on flash until they are sent: one file each, at most
// MaxFiles and MaxBytes; when over, the least severe go first, oldest first.
type Spool struct {
	Dir      string
	MaxFiles int
	MaxBytes int64
}

func (s Spool) path(id string) string { return filepath.Join(s.Dir, id+".json") }

// Entries lists what waits, oldest queued first. A file that is no entry is
// taken away.
func (s Spool) Entries() []Entry {
	names, _ := filepath.Glob(filepath.Join(s.Dir, "*.json"))
	out := make([]Entry, 0, len(names))
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			continue
		}
		var e Entry
		if json.Unmarshal(b, &e) != nil || e.Report.ReportID == "" || filepath.Base(n) != e.Report.ReportID+".json" {
			_ = os.Remove(n)
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Meta.QueuedAt.Before(out[j].Meta.QueuedAt) })
	return out
}

// Add queues r — or, when an unsent report of its fingerprint waits already,
// counts it there. notBefore holds a new one back: the window after one of
// the same fingerprint was sent.
func (s Spool) Add(r Report, now, notBefore time.Time) (merged bool, evicted int, err error) {
	for _, e := range s.Entries() {
		if e.Report.Fingerprint != r.Fingerprint {
			continue
		}
		e.Report.Count += r.Count
		if r.LastAt.After(e.Report.LastAt) {
			e.Report.LastAt = r.LastAt
		}
		return true, 0, s.Save(e)
	}
	if err := s.Save(Entry{Report: r, Meta: Meta{QueuedAt: now, NextAt: notBefore}}); err != nil {
		return false, 0, err
	}
	return false, s.evict(), nil
}

// Save writes e whole or not at all.
func (s Spool) Save(e Entry) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	p := s.path(e.Report.ReportID)
	tmp := filepath.Join(s.Dir, "."+e.Report.ReportID+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Remove takes a report out of the spool.
func (s Spool) Remove(id string) error {
	if err := os.Remove(s.path(id)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s Spool) evict() int {
	es := s.Entries()
	size := func() (n int64) {
		for _, e := range es {
			if st, err := os.Stat(s.path(e.Report.ReportID)); err == nil {
				n += st.Size()
			}
		}
		return n
	}
	gone := 0
	for len(es) > 0 && (len(es) > s.MaxFiles || size() > s.MaxBytes) {
		victim := 0
		for i, e := range es {
			if SeverityRank(e.Report.Severity) < SeverityRank(es[victim].Report.Severity) {
				victim = i
			}
		}
		_ = s.Remove(es[victim].Report.ReportID)
		es = append(es[:victim], es[victim+1:]...)
		gone++
	}
	return gone
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bugreport/ -run Spool -v`
Expected: PASS (the merge, eviction, hold-back and unwritable-dir cases).

- [ ] **Step 5: Commit**

```bash
rtk git add internal/bugreport/spool.go internal/bugreport/spool_test.go
rtk git commit -m "feat(reporter): the spool"
```

---

### Task 6: What the reporter sees for itself

Crash files, the kernel's log, a data plane without xray, a reboot it did not see coming; and the reporter's small state.

**Files:**
- Create: `internal/bugreport/collect.go`, `internal/bugreport/collect_test.go`, `internal/bugreport/state.go`

**Interfaces:**
- Consumes: `incident.Incident` (Task 2); `capRunes`, `tail` (Task 4).
- Produces:
  - `type State struct { BootID string; DmesgAt float64; Sent map[string]time.Time; HourStart, DayStart time.Time; HourCount, DayCount, XrayMissing, Dropped int; LastError string }`; `func LoadState(path string) State`; `func (st State) Save(path string) error`; `func (st *State) Allow(now time.Time) bool`
  - `func Crashes(dir string, alive func(pid int) bool) []incident.Incident` (three crashes or more at one look also make a `VCTL_CRASH_LOOP`)
  - `func PanicKey(stack string) (title, key string)`
  - `func Dmesg(text string, after float64) (found []incident.Incident, last float64)`
  - `func DataplaneWithoutXray(tableLoaded, xrayRunning bool, st *State) *incident.Incident`
  - `func Boot(marker, pstoreDir string, now time.Time) *incident.Incident`

- [ ] **Step 1: Write the failing tests** — `internal/bugreport/collect_test.go`:

```go
package bugreport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const panicStack = `panic: runtime error: index out of range [3] with length 3

goroutine 42 [running]:
vectra-controller-pro/internal/routepolicy.pickNode(0x4000123456, 0x3)
	/build/internal/routepolicy/refresh.go:211 +0x1c8
vectra-controller-pro/internal/routepolicy.Refresh({0x40001, 0x2}, 0x1)
	/build/internal/routepolicy/refresh.go:120 +0x3f0
main.(*daemon).refreshNative(0x4000010000)
	/build/cmd/vctl/native_source.go:480 +0x120
main.(*daemon).loop(0x4000010000)
	/build/cmd/vctl/cmd_agent.go:600 +0x88
`

func TestPanicKeyIgnoresNumbersAndAddresses(t *testing.T) {
	title, key := PanicKey(panicStack)
	if title != "panic: runtime error: index out of range [3] with length 3" {
		t.Fatalf("title %q", title)
	}
	_, key2 := PanicKey(strings.ReplaceAll(strings.ReplaceAll(panicStack, "[3] with length 3", "[7] with length 7"), "0x4000123456", "0x4000999999"))
	if key != key2 {
		t.Fatalf("keys differ:\n%s\n---\n%s", key, key2)
	}
	if !strings.Contains(key, "routepolicy.pickNode") || strings.Contains(key, "cmd_agent.go") {
		t.Fatalf("key %q", key)
	}
}

func TestCrashesOfOneKindMergeIntoOneReport(t *testing.T) {
	dir := t.TempDir()
	for _, pid := range []string{"101", "102", "103"} {
		if err := os.WriteFile(filepath.Join(dir, "vctl."+pid), []byte(panicStack), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "vctl.104"), nil, 0o644); err != nil { // stopped cleanly
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vctl.200"), []byte(panicStack), 0o644); err != nil { // still runs
		t.Fatal(err)
	}
	found := Crashes(dir, func(pid int) bool { return pid == 200 })
	codes := map[string]int{}
	for _, in := range found {
		codes[in.Code]++
	}
	if codes["VCTL_PANIC"] != 3 || codes["VCTL_CRASH_LOOP"] != 1 || found[0].Stack == "" {
		t.Fatalf("%+v", found)
	}
	s := Spool{Dir: t.TempDir(), MaxFiles: 32, MaxBytes: 512 << 10}
	now := time.Unix(1790000000, 0).UTC()
	for _, in := range found {
		s.Add(FromIncident(in, Router{DeviceID: "d", Vctl: "v"}, now), now, time.Time{})
	}
	counts := map[string]int{}
	for _, e := range s.Entries() {
		counts[e.Report.Code] = e.Report.Count
	}
	if len(counts) != 2 || counts["VCTL_PANIC"] != 3 || counts["VCTL_CRASH_LOOP"] != 1 {
		t.Fatalf("%+v", counts)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "vctl.*"))
	if len(left) != 1 || filepath.Base(left[0]) != "vctl.200" {
		t.Fatalf("left %v", left)
	}
}

const dmesgText = `[    5.100000] random: crng init done
[38201.412345] xray invoked oom-killer: gfp_mask=0x140cca(GFP_HIGHUSER_MOVABLE|__GFP_COMP), order=0
[38201.500000] Out of memory: Killed process 2211 (xray) total-vm:1263452kB, anon-rss:98304kB, file-rss:0kB
[38300.000000] kworker/0:1: page allocation failure: order:2, mode:0x40cc0(GFP_KERNEL|__GFP_COMP)
[38300.000001] CPU: 0 PID: 12 Comm: kworker/0:1
[38300.000002] Call trace:
`

func TestDmesgFindsOOMKillsAndKernelErrorsAfterTheCursor(t *testing.T) {
	found, last := Dmesg(dmesgText, 10)
	if last != 38300.000002 || len(found) != 2 {
		t.Fatalf("last %v, %+v", last, found)
	}
	if found[0].Code != "OOM_KILL" || found[0].Key != "oom xray" || found[0].Details["anonRssKB"] != 98304 {
		t.Fatalf("%+v", found[0])
	}
	if found[1].Code != "KERNEL_ERROR" || found[1].Key != "page allocation failure order:2" || len(found[1].Kernel) != 3 {
		t.Fatalf("%+v", found[1])
	}
	if again, _ := Dmesg(dmesgText, last); len(again) != 0 {
		t.Fatalf("read twice: %+v", again)
	}
}

func TestDataplaneWithoutXrayIsSaidOnceAtTheSecondMinute(t *testing.T) {
	var st State
	if DataplaneWithoutXray(true, false, &st) != nil {
		t.Fatal("said at the first minute")
	}
	if in := DataplaneWithoutXray(true, false, &st); in == nil || in.Code != "DATAPLANE_WITHOUT_XRAY" {
		t.Fatal("not said at the second")
	}
	if DataplaneWithoutXray(true, false, &st) != nil {
		t.Fatal("said again")
	}
	DataplaneWithoutXray(true, true, &st)
	if st.XrayMissing != 0 {
		t.Fatal("xray back, the count stayed")
	}
}

func TestBootSaysWhatItDidNotSeeComing(t *testing.T) {
	dir := t.TempDir()
	marker, pstore := filepath.Join(dir, "clean-shutdown"), filepath.Join(dir, "pstore")
	now := time.Unix(1790000000, 0).UTC()
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if Boot(marker, pstore, now) != nil {
		t.Fatal("a clean shutdown reported")
	}
	if in := Boot(marker, pstore, now); in == nil || in.Code != "UNEXPECTED_REBOOT" {
		t.Fatal("no marker, and nothing said")
	}
	if err := os.MkdirAll(pstore, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pstore, "dmesg-ramoops-0"), []byte("Kernel panic - not syncing: Oops\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(marker, nil, 0o644)
	in := Boot(marker, pstore, now)
	if in == nil || len(in.Kernel) != 1 || !strings.Contains(in.Title, "kernel") {
		t.Fatalf("%+v", in)
	}
	if left, _ := filepath.Glob(filepath.Join(pstore, "*")); len(left) != 0 {
		t.Fatal("pstore kept what was read")
	}
}

func TestStateAllowsTwelveAnHour(t *testing.T) {
	var st State
	now := time.Unix(1790000000, 0).UTC()
	n := 0
	for i := 0; i < 20; i++ {
		if st.Allow(now) {
			n++
		}
	}
	if n != 12 || !st.Allow(now.Add(61*time.Minute)) {
		t.Fatalf("%d allowed in an hour", n)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/bugreport/ -run 'PanicKey|Crashes|Dmesg|Dataplane|Boot|State'`
Expected: FAIL — `undefined: PanicKey`.

- [ ] **Step 3: Write the state** — `internal/bugreport/state.go`:

```go
package bugreport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// State is the reporter's memory between runs, a few hundred bytes on flash.
type State struct {
	BootID      string               `json:"bootId,omitempty"`
	DmesgAt     float64              `json:"dmesgAt,omitempty"`
	Sent        map[string]time.Time `json:"sent,omitempty"`
	HourStart   time.Time            `json:"hourStart,omitempty"`
	HourCount   int                  `json:"hourCount,omitempty"`
	DayStart    time.Time            `json:"dayStart,omitempty"`
	DayCount    int                  `json:"dayCount,omitempty"`
	XrayMissing int                  `json:"xrayMissing,omitempty"`
	Dropped     int                  `json:"dropped,omitempty"`
	LastError   string               `json:"lastError,omitempty"`
}

// Sends allowed: an hour's and a day's.
const (
	MaxPerHour = 12
	MaxPerDay  = 100
)

// LoadState reads path; a missing or broken file is a fresh state.
func LoadState(path string) State {
	var st State
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// Save writes st whole or not at all.
func (st State) Save(path string) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Allow counts a send when the hour's and the day's limits leave room.
func (st *State) Allow(now time.Time) bool {
	if now.Sub(st.HourStart) >= time.Hour || now.Before(st.HourStart) {
		st.HourStart, st.HourCount = now, 0
	}
	if now.Sub(st.DayStart) >= 24*time.Hour || now.Before(st.DayStart) {
		st.DayStart, st.DayCount = now, 0
	}
	if st.HourCount >= MaxPerHour || st.DayCount >= MaxPerDay {
		return false
	}
	st.HourCount++
	st.DayCount++
	return true
}
```

- [ ] **Step 4: Write the collectors** — `internal/bugreport/collect.go`:

```go
package bugreport

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vectra-controller-pro/internal/incident"
)

var reNumber = regexp.MustCompile(`0x[0-9a-fA-F]+|\d+`)

// PanicKey is a crash's title — its "panic:" or "fatal error:" line — and
// its key: that line with numbers and addresses as N, and the first three
// frames of vctl's own code, so the same crash on another router, or with
// another index, is the same problem.
func PanicKey(stack string) (title, key string) {
	lines := strings.Split(stack, "\n")
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "panic:") || strings.HasPrefix(t, "fatal error:") || strings.HasPrefix(t, "SIG") {
			title = t
			break
		}
	}
	if title == "" {
		for _, l := range lines {
			if t := strings.TrimSpace(l); t != "" {
				title = t
				break
			}
		}
	}
	var frames []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "vectra-controller-pro/") && !strings.HasPrefix(t, "main.") {
			continue
		}
		if i := strings.LastIndexByte(t, '('); i > 0 {
			t = t[:i]
		}
		frames = append(frames, t)
		if len(frames) == 3 {
			break
		}
	}
	return capRunes(title, 200), reNumber.ReplaceAllString(title, "N") + "\n" + strings.Join(frames, "\n")
}

// Crashes turns vctl's crash output (CrashDir/vctl.<pid>) into incidents once
// its process is gone: a file with a stack is a VCTL_PANIC, an empty one — a
// vctl that stopped as it should — is taken away. A process that still runs
// keeps its file.
func Crashes(dir string, alive func(pid int) bool) []incident.Incident {
	names, _ := filepath.Glob(filepath.Join(dir, "vctl.*"))
	var out []incident.Incident
	for _, n := range names {
		pid, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(n), "vctl."))
		if err != nil {
			_ = os.Remove(n)
			continue
		}
		if alive(pid) {
			continue
		}
		at := time.Now()
		if st, err := os.Stat(n); err == nil {
			at = st.ModTime()
		}
		b, _ := os.ReadFile(n)
		_ = os.Remove(n)
		if len(bytes.TrimSpace(b)) == 0 {
			continue
		}
		title, key := PanicKey(string(b))
		out = append(out, incident.Incident{Code: "VCTL_PANIC", Key: key, Title: "vctl crashed: " + title,
			At: at.UTC(), Source: "reporter", Stack: string(b)})
	}
	// procd starts a vctl that crashes again within seconds: three at one
	// look (a minute) are a loop, whatever each one says.
	if len(out) >= 3 {
		out = append(out, incident.Incident{Code: "VCTL_CRASH_LOOP", Key: "vctl crash loop",
			Title: fmt.Sprintf("vctl crashed %d times in a minute", len(out)), Source: "reporter"})
	}
	return out
}

var (
	reDmesgLine = regexp.MustCompile(`^\[\s*(\d+\.\d+)\]\s?(.*)$`)
	reOOMKill   = regexp.MustCompile(`Out of memory: Killed process (\d+) \(([^)]+)\)`)
	reAnonRSS   = regexp.MustCompile(`anon-rss:(\d+)kB`)
	reOrder     = regexp.MustCompile(`order:(\d+)`)
	kernelFaults = []struct {
		re  *regexp.Regexp
		key string
	}{
		{regexp.MustCompile(`page allocation failure`), "page allocation failure"},
		{regexp.MustCompile(`Internal error: Oops|\bOops\b`), "oops"},
		{regexp.MustCompile(`\bBUG: `), "BUG"},
		{regexp.MustCompile(`soft lockup`), "soft lockup"},
		{regexp.MustCompile(`blocked for more than \d+ seconds`), "hung task"},
		{regexp.MustCompile(`Kernel panic`), "kernel panic"},
	}
)

// Dmesg reads the kernel's lines after the cursor (seconds since boot):
// OOM kills and kernel faults, each fault with up to 14 lines after it (its
// call trace). last is the newest line's time, the next cursor.
func Dmesg(text string, after float64) (found []incident.Incident, last float64) {
	last = after
	type line struct {
		at   float64
		text string
	}
	var lines []line
	for _, l := range strings.Split(text, "\n") {
		m := reDmesgLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		at, err := strconv.ParseFloat(m[1], 64)
		if err != nil || at <= after {
			continue
		}
		lines = append(lines, line{at, m[2]})
		if at > last {
			last = at
		}
	}
	for i, l := range lines {
		if m := reOOMKill.FindStringSubmatch(l.text); m != nil {
			details := map[string]any{"victim": m[2]}
			if r := reAnonRSS.FindStringSubmatch(l.text); r != nil {
				kb, _ := strconv.Atoi(r[1])
				details["anonRssKB"] = kb
			}
			found = append(found, incident.Incident{Code: "OOM_KILL", Key: "oom " + m[2],
				Title: "the kernel killed " + m[2] + " for want of memory", Source: "reporter",
				Details: details, Kernel: []string{l.text}})
			continue
		}
		for _, f := range kernelFaults {
			if !f.re.MatchString(l.text) {
				continue
			}
			key := f.key
			if o := reOrder.FindStringSubmatch(l.text); o != nil && f.key == "page allocation failure" {
				key += " order:" + o[1]
			}
			var kernel []string
			for j := i; j < len(lines) && j < i+15; j++ {
				kernel = append(kernel, lines[j].text)
			}
			found = append(found, incident.Incident{Code: "KERNEL_ERROR", Key: key,
				Title: "kernel: " + capRunes(l.text, 200), Source: "reporter", Kernel: kernel})
			break
		}
	}
	return found, last
}

// DataplaneWithoutXray says once, at the second minute in a row, that vctl's
// data plane is loaded while no xray runs: the LAN has no way out.
func DataplaneWithoutXray(tableLoaded, xrayRunning bool, st *State) *incident.Incident {
	if !tableLoaded || xrayRunning {
		st.XrayMissing = 0
		return nil
	}
	st.XrayMissing++
	if st.XrayMissing != 2 {
		return nil
	}
	return &incident.Incident{Code: "DATAPLANE_WITHOUT_XRAY", Key: "dataplane without xray",
		Title: "vctl's data plane is loaded and xray is not running: the LAN has no way out", Source: "reporter"}
}

// Boot is the reporter's look at a boot: the router went down cleanly (the
// shutdown marker, which it takes away) or not — then an UNEXPECTED_REBOOT,
// with whatever the kernel kept of its end (pstore, emptied as it is read).
func Boot(marker, pstoreDir string, now time.Time) *incident.Incident {
	_, err := os.Stat(marker)
	clean := err == nil
	_ = os.Remove(marker)
	var kept []string
	files, _ := filepath.Glob(filepath.Join(pstoreDir, "*"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			kept = append(kept, tail(strings.Split(strings.TrimRight(string(b), "\n"), "\n"), MaxKernel)...)
		}
		_ = os.Remove(f)
	}
	kept = tail(kept, MaxKernel)
	if clean && len(kept) == 0 {
		return nil
	}
	in := &incident.Incident{Code: "UNEXPECTED_REBOOT", Key: "unexpected reboot",
		Title: "the router rebooted without going down cleanly", At: now.UTC(), Source: "reporter"}
	if len(kept) > 0 {
		in.Key, in.Title, in.Kernel = "reboot after a kernel failure", "the router rebooted after a kernel failure", kept
	}
	return in
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/bugreport/ -v -run 'PanicKey|Crashes|Dmesg|Dataplane|Boot|State'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
rtk git add internal/bugreport/collect.go internal/bugreport/collect_test.go internal/bugreport/state.go
rtk git commit -m "feat(reporter): crash files, the kernel's log, reboots, the data plane without xray"
```

---

### Task 7: What every report says about the router

**Files:**
- Create: `internal/bugreport/facts.go`, `internal/bugreport/facts_test.go`

**Interfaces:**
- Consumes: `Router` (Task 4), `RedactLines` (Task 3); `state.Load`, `memguard.TakeSnapshot`, `memguard.ReadFrom`, `routepolicy.ParseUCI`, `config.Load`.
- Produces: `type FactsEnv struct { Root, Reporter string; XrayVersion func() string; Logread func() []string }`; `func Facts(e FactsEnv) (Router, []string)` — the router's facts and vctl's last log lines (redacted, ≤ MaxLog).

- [ ] **Step 1: Write the failing test** — `internal/bugreport/facts_test.go`:

```go
package bugreport

import (
	"os"
	"path/filepath"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFactsReadTheRouter(t *testing.T) {
	root := tree(t, map[string]string{
		"etc/vectra-controller-pro/state.json":             `{"device_identifier":"vectra-0123456789ab"}`,
		"usr/lib/opkg/info/vectra-controller-pro.control":  "Package: vectra-controller-pro\nVersion: 0.6.0-r19\n",
		"etc/openwrt_release":                              "DISTRIB_RELEASE='24.10.6'\nDISTRIB_ARCH='aarch64_cortex-a53'\n",
		"tmp/sysinfo/model":                                "Xiaomi Mi Router AX3000T\n",
		"proc/uptime":                                      "38207.12 70000.00\n",
		"proc/meminfo":                                     "MemTotal: 239720 kB\nMemAvailable: 98696 kB\n",
		"etc/config/vectra-controller-pro":                 "config controller 'main'\n\toption route_source 'native'\n",
		"etc/vectra-controller-pro/xray-desired.json":      `{"schema":1,"inbounds":{"tproxy":{"listenIP":"0.0.0.0","port":12345,"killSwitch":true}}}`,
	})
	r, log := Facts(FactsEnv{Root: root, Reporter: "1.0.0-r1",
		XrayVersion: func() string { return "26.7.28" },
		Logread: func() []string {
			return []string{"daemon.info dnsmasq[1]: ok", "daemon.warn vctl[12]: fetch https://sub.example.com/x failed"}
		}})
	if r.DeviceID != "vectra-0123456789ab" || r.Vctl != "0.6.0-r19" || r.OpenWrt != "24.10.6" || r.Arch != "aarch64_cortex-a53" ||
		r.Model != "Xiaomi Mi Router AX3000T" || r.UptimeSec != 38207 || r.MemTotalMiB != 234 || r.MemAvailableMiB != 96 ||
		r.RouteSource != "native" || r.Xray != "26.7.28" || r.Reporter != "1.0.0-r1" || r.KillSwitch == nil || !*r.KillSwitch {
		t.Fatalf("%+v", r)
	}
	if len(log) != 1 || log[0] != "daemon.warn vctl[12]: fetch <url> failed" {
		t.Fatalf("log %q", log)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/bugreport/ -run Facts`
Expected: FAIL — `undefined: Facts`.

- [ ] **Step 3: Write the facts** — `internal/bugreport/facts.go`:

```go
package bugreport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"vectra-controller-pro/internal/config"
	"vectra-controller-pro/internal/memguard"
	"vectra-controller-pro/internal/routepolicy"
	"vectra-controller-pro/internal/state"
)

// FactsEnv is where the facts are read: Root is "/" on a router.
type FactsEnv struct {
	Root        string
	Reporter    string
	XrayVersion func() string
	Logread     func() []string
}

// Facts is what every report says about the router, and vctl's last lines
// in the system log (its own and its init script's), redacted.
func Facts(e FactsEnv) (Router, []string) {
	p := func(rel string) string { return filepath.Join(e.Root, rel) }
	var r Router
	if st, err := state.Load(p("etc/vectra-controller-pro/state.json")); err == nil {
		r.DeviceID = st.DeviceIdentifier
	}
	r.Vctl = field(p("usr/lib/opkg/info/vectra-controller-pro.control"), "Version:")
	r.OpenWrt = strings.Trim(field(p("etc/openwrt_release"), "DISTRIB_RELEASE="), `'"`)
	r.Arch = strings.Trim(field(p("etc/openwrt_release"), "DISTRIB_ARCH="), `'"`)
	if b, err := os.ReadFile(p("tmp/sysinfo/model")); err == nil {
		r.Model = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(p("proc/uptime")); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				r.UptimeSec = int64(v)
			}
		}
	}
	if mi, err := memguard.ReadFrom(p("proc/meminfo")); err == nil {
		r.MemTotalMiB, r.MemAvailableMiB = memguard.MiB(mi.TotalKB), memguard.MiB(mi.AvailableKB)
	}
	if b, err := os.ReadFile(p("etc/config/vectra-controller-pro")); err == nil {
		if secs, err := routepolicy.ParseUCI(string(b)); err == nil {
			for _, s := range secs {
				if s.Name == "main" {
					r.RouteSource = s.Get("route_source")
				}
			}
		}
	}
	if c, err := config.Load(p("etc/vectra-controller-pro/xray-desired.json")); err == nil && c.Inbounds.Tproxy != nil {
		ks := c.Inbounds.Tproxy.KillSwitch
		r.KillSwitch = &ks
	}
	r.Reporter = e.Reporter
	if e.XrayVersion != nil {
		r.Xray = e.XrayVersion()
	}
	var log []string
	if e.Logread != nil {
		for _, l := range e.Logread() {
			if strings.Contains(l, "vctl[") || strings.Contains(l, "vectra-controller-pro") {
				log = append(log, l)
			}
		}
	}
	return r, tail(RedactLines(log), MaxLog)
}

// field is the rest of the first line of path that starts with prefix.
func field(path, prefix string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(l, prefix))
		}
	}
	return ""
}
```

- [ ] **Step 4: Run the test; if `memguard.ReadFrom`'s `Info` names differ from `TotalKB`/`AvailableKB`, use its field names (read `internal/memguard/memguard.go`), not new ones**

Run: `go test ./internal/bugreport/ -run Facts -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/bugreport/facts.go internal/bugreport/facts_test.go
rtk git commit -m "feat(reporter): the router's facts"
```

---

### Task 8: Sending

**Files:**
- Create: `internal/bugreport/send.go`, `internal/bugreport/send_test.go`
- Modify: `internal/controlplane/client.go` (export the marked client)

**Interfaces:**
- Consumes: `Entry`, `Report.Encode` (Tasks 4–5).
- Produces:
  - `controlplane.MarkedHTTPClient(mark int, timeout time.Duration) *http.Client` (marked transport, names on the marked path, no redirects followed)
  - `type Sender struct { URL string; Client *http.Client; Sign func(hwid, rawURL string) (string, error); HWID, Model, OSRelease string; Now func() time.Time }`
  - `type Outcome struct { Status int; Done bool; Retry time.Duration; Err error }`
  - `func (s Sender) Send(ctx context.Context, e Entry) Outcome`

- [ ] **Step 1: Export the marked client** — append to `internal/controlplane/client.go`:

```go
// MarkedHTTPClient is an HTTP client on the control plane's path: SO_MARK =
// mark on every socket, names resolved on the same path, and no redirect
// followed — an answer from anywhere but the endpoint is no answer.
func MarkedHTTPClient(mark int, timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if t := markedTransport(mark); t != nil {
		c.Transport = t
	}
	return c
}
```

- [ ] **Step 2: Write the failing tests** — `internal/bugreport/send_test.go`:

```go
package bugreport

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/routersign"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uatoken"
)

const testHWID = "4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"

type signed struct {
	st      state.PersistedState
	devPub  ed25519.PublicKey
	vectra  *ecdh.PrivateKey
	now     time.Time
}

func newSigned(t *testing.T) signed {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	vectra, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signed{
		st: state.PersistedState{DeviceIdentifier: "vectra-0123456789ab", DevicePrivateKey: base64.StdEncoding.EncodeToString(priv),
			ClaimKey: &controlplane.ClaimKey{Kid: 9, PublicKey: base64.StdEncoding.EncodeToString(vectra.PublicKey().Bytes())}},
		devPub: pub, vectra: vectra, now: time.Unix(1790000000, 0),
	}
}

// The server's side of the contract: the body's hash is the query's b, and
// the token opens for this path and query, this HWID, this device.
func (s signed) verify(t *testing.T, r *http.Request, body []byte) {
	t.Helper()
	sum := sha256.Sum256(body)
	if r.URL.Query().Get("b") != hex.EncodeToString(sum[:]) {
		t.Errorf("b = %q", r.URL.Query().Get("b"))
	}
	keys := func(kid byte) (*ecdh.PrivateKey, bool) { return s.vectra, kid == 9 }
	devices := func(id string) (ed25519.PublicKey, bool) { return s.devPub, id == s.st.DeviceIdentifier }
	if _, err := uatoken.Open(r.UserAgent(), keys, devices, r.Header.Get("x-hwid"), r.URL.RequestURI(), s.now, time.Minute); err != nil {
		t.Errorf("token: %v", err)
	}
	for h, want := range map[string]string{"x-device-os": "OpenWrt", "x-device-model": "Xiaomi Mi Router AX3000T", "x-ver-os": "24.10.6", "Content-Type": "application/json"} {
		if r.Header.Get(h) != want {
			t.Errorf("%s = %q", h, r.Header.Get(h))
		}
	}
}

func (s signed) sender(url string, client *http.Client) Sender {
	return Sender{URL: url, Client: client, Sign: routersign.Signer(s.st, "0.6.0-r19", func() time.Time { return s.now }),
		HWID: testHWID, Model: "Xiaomi Mi Router AX3000T", OSRelease: "24.10.6", Now: func() time.Time { return s.now }}
}

func entry() Entry {
	at := time.Unix(1790000000, 0).UTC()
	return Entry{Report: rep("TEST", "t", "low", at), Meta: Meta{QueuedAt: at}}
}

func TestSendSignsAndCountsWhatTheServerSays(t *testing.T) {
	s := newSigned(t)
	status := http.StatusAccepted
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.verify(t, r, body)
		if r.Header.Get("x-vectra-report-id") == "" {
			t.Error("no report id")
		}
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "120")
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	snd := s.sender(srv.URL+"/errors/router", srv.Client())
	for _, c := range []struct {
		status int
		done   bool
		retry  time.Duration
	}{
		{202, true, 0}, {400, true, 0}, {413, true, 0}, {422, true, 0},
		{401, false, 6 * time.Hour}, {403, false, 6 * time.Hour}, {404, false, time.Hour},
		{429, false, 2 * time.Minute}, {503, false, time.Minute},
	} {
		status = c.status
		o := snd.Send(context.Background(), entry())
		if o.Status != c.status || o.Done != c.done || o.Retry != c.retry {
			t.Errorf("%d: %+v", c.status, o)
		}
	}
}

func TestSendBacksOffByAttempt(t *testing.T) {
	s := newSigned(t)
	snd := s.sender("https://127.0.0.1:1/errors/router", &http.Client{Timeout: time.Second})
	e := entry()
	for attempts, want := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, time.Hour} {
		e.Meta.Attempts = attempts
		if o := snd.Send(context.Background(), e); o.Done || o.Retry != want || o.Err == nil {
			t.Errorf("attempt %d: %+v", attempts, o)
		}
	}
}

func TestSendWaitsForTheClock(t *testing.T) {
	s := newSigned(t)
	hit := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	snd := s.sender(srv.URL+"/errors/router", srv.Client())
	snd.Now = func() time.Time { return time.Unix(100, 0) }
	if o := snd.Send(context.Background(), entry()); o.Done || o.Retry != 10*time.Minute || hit {
		t.Fatalf("%+v, hit %v", o, hit)
	}
}

func TestSendDoesNotFollowRedirects(t *testing.T) {
	s := newSigned(t)
	portal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer portal.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, portal.URL+"/login", http.StatusFound)
	}))
	defer srv.Close()
	client := srv.Client()
	client.CheckRedirect = controlplane.MarkedHTTPClient(0, time.Second).CheckRedirect
	if o := s.sender(srv.URL+"/errors/router", client).Send(context.Background(), entry()); o.Done || o.Status != http.StatusFound {
		t.Fatalf("%+v", o)
	}
}
```

- [ ] **Step 3: Run them to see them fail**

Run: `go test ./internal/bugreport/ -run Send`
Expected: FAIL — `undefined: Sender`.

- [ ] **Step 4: Write the sender** — `internal/bugreport/send.go`:

```go
package bugreport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sender posts reports to Vectra Connect (docs/CONNECT-BUG-REPORTS.md).
type Sender struct {
	URL       string
	Client    *http.Client
	Sign      func(hwid, rawURL string) (string, error)
	HWID      string
	Model     string
	OSRelease string
	Now       func() time.Time
}

// Outcome is what became of one send: Done takes the report out of the
// spool (delivered, or refused for good); otherwise it goes again after
// Retry.
type Outcome struct {
	Status int
	Done   bool
	Retry  time.Duration
	Err    error
}

var backoff = []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// clockSet: before this the router's clock is not set (no NTP yet), and a
// token made now would be refused.
var clockSet = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Send posts e once.
func (s Sender) Send(ctx context.Context, e Entry) Outcome {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	if now().Before(clockSet) {
		return Outcome{Retry: 10 * time.Minute, Err: errors.New("the clock is not set")}
	}
	body, err := e.Report.Encode()
	if err != nil {
		return Outcome{Done: true, Err: fmt.Errorf("encode: %w", err)}
	}
	sum := sha256.Sum256(body)
	sep := "?"
	if strings.Contains(s.URL, "?") {
		sep = "&"
	}
	u := s.URL + sep + "b=" + hex.EncodeToString(sum[:])
	ua, err := s.Sign(s.HWID, u)
	if err != nil {
		return Outcome{Retry: 10 * time.Minute, Err: fmt.Errorf("sign: %w", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return Outcome{Done: true, Err: err}
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-hwid", s.HWID)
	req.Header.Set("x-device-os", "OpenWrt")
	if s.Model != "" {
		req.Header.Set("x-device-model", s.Model)
	}
	if s.OSRelease != "" {
		req.Header.Set("x-ver-os", s.OSRelease)
	}
	req.Header.Set("x-vectra-report-id", e.Report.ReportID)
	resp, err := s.Client.Do(req)
	if err != nil {
		return Outcome{Retry: backoff[min(e.Meta.Attempts, len(backoff)-1)], Err: err}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	o := Outcome{Status: resp.StatusCode}
	switch c := resp.StatusCode; {
	case c >= 200 && c < 300:
		o.Done = true
	case c == 400, c == 413, c == 422:
		o.Done, o.Err = true, fmt.Errorf("refused for good: %s", resp.Status)
	case c == 401, c == 403:
		o.Retry, o.Err = 6*time.Hour, fmt.Errorf("not recognised: %s", resp.Status)
	case c == 404:
		o.Retry, o.Err = time.Hour, errors.New("no endpoint yet")
	case c == 429:
		o.Retry, o.Err = retryAfter(resp.Header.Get("Retry-After"), now()), errors.New("too many")
	default:
		o.Retry, o.Err = backoff[min(e.Meta.Attempts, len(backoff)-1)], fmt.Errorf("server: %s", resp.Status)
	}
	return o
}

func retryAfter(v string, now time.Time) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return time.Hour
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/bugreport/ ./internal/controlplane/ -run 'Send|Marked' -v`
Expected: PASS. (A redirect comes back as `Status 302`, not delivered; the backoff table matches the contract.)

- [ ] **Step 6: Commit**

```bash
rtk git add internal/bugreport/send.go internal/bugreport/send_test.go internal/controlplane/client.go
rtk git commit -m "feat(reporter): signed sends and what the server's answers mean"
```

---

### Task 9: One run

**Files:**
- Create: `internal/bugreport/run.go`, `internal/bugreport/run_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `type Env struct { Root, Reporter, SpoolDir, StatePath, InboxDir, CrashDir string; Now func() time.Time; Alive func(pid int) bool; Dmesg func() string; BootID func() string; TableLoaded, XrayRunning func() bool; Facts func() (Router, []string); Sender Sender; Budget time.Duration }`
  - `func Run(ctx context.Context, e Env) (queued, sent int, err error)`
  - `func Queue(e Env, st *State, in incident.Incident, r Router, log []string) error` (used by `run`, `boot`, `test`)

- [ ] **Step 1: Write the failing tests** — `internal/bugreport/run_test.go`:

```go
package bugreport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vectra-controller-pro/internal/incident"
)

func runEnv(t *testing.T, s signed, url string, client *http.Client) Env {
	dir := t.TempDir()
	return Env{
		SpoolDir: filepath.Join(dir, "spool"), StatePath: filepath.Join(dir, "state.json"),
		InboxDir: filepath.Join(dir, "inbox"), CrashDir: filepath.Join(dir, "crash"),
		Now: func() time.Time { return s.now }, Alive: func(int) bool { return false },
		Dmesg: func() string { return "" }, BootID: func() string { return "boot-1" },
		TableLoaded: func() bool { return false }, XrayRunning: func() bool { return true },
		Facts:  func() (Router, []string) { return Router{DeviceID: "vectra-0123456789ab", Vctl: "0.6.0-r19"}, nil },
		Sender: s.sender(url, client), Budget: 20 * time.Second,
	}
}

func TestRunTakesTheInboxAndSendsWhatIsDue(t *testing.T) {
	s := newSigned(t)
	var got []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.verify(t, r, body)
		got = append(got, r.Header.Get("x-vectra-report-id"))
		w.WriteHeader(202)
	}))
	defer srv.Close()
	e := runEnv(t, s, srv.URL+"/errors/router", srv.Client())
	rec := incident.NewRecorder(e.InboxDir, time.Minute)
	rec.Record(incident.Incident{Code: "APPLY_REFUSED", Key: "k", Title: "refused", Source: "vctl"})
	queued, sent, err := Run(context.Background(), e)
	if err != nil || queued != 1 || sent != 1 || len(got) != 1 {
		t.Fatalf("queued %d, sent %d, err %v, got %v", queued, sent, err, got)
	}
	if left := incident.Read(e.InboxDir); len(left) != 0 {
		t.Fatal("the inbox kept what was taken")
	}
	if es := (Spool{Dir: e.SpoolDir, MaxFiles: 32, MaxBytes: 512 << 10}).Entries(); len(es) != 0 {
		t.Fatal("a delivered report stayed in the spool")
	}
	// The same problem again within 6 hours waits for the window's end.
	rec2 := incident.NewRecorder(e.InboxDir, time.Minute)
	rec2.Record(incident.Incident{Code: "APPLY_REFUSED", Key: "k", Title: "refused again", Source: "vctl"})
	if _, sent, _ := Run(context.Background(), e); sent != 0 {
		t.Fatal("sent again within the window")
	}
}

func TestRunCountsWhatItCannotKeep(t *testing.T) {
	s := newSigned(t)
	e := runEnv(t, s, "https://127.0.0.1:1/errors/router", &http.Client{Timeout: time.Second})
	if err := os.WriteFile(e.SpoolDir, nil, 0o600); err != nil { // a file where the spool goes
		t.Fatal(err)
	}
	incident.NewRecorder(e.InboxDir, time.Minute).Record(incident.Incident{Code: "TEST", Key: "t", Title: "t"})
	if _, _, err := Run(context.Background(), e); err != nil {
		t.Fatalf("the run failed: %v", err)
	}
	if st := LoadState(e.StatePath); st.Dropped != 1 || st.LastError == "" {
		t.Fatalf("%+v", st)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/bugreport/ -run Run`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Write the run** — `internal/bugreport/run.go`:

```go
package bugreport

import (
	"context"
	"os"
	"time"

	"vectra-controller-pro/internal/incident"
)

// Spool caps and the time a report may wait at most.
const (
	SpoolFiles = 32
	SpoolBytes = 512 << 10
	MaxAge     = 7 * 24 * time.Hour
	Window     = 6 * time.Hour
)

// Env is one run's world; on a router cmd/vectra-reporter fills it.
type Env struct {
	Root, Reporter                        string
	SpoolDir, StatePath, InboxDir, CrashDir string
	Now                                   func() time.Time
	Alive                                 func(pid int) bool
	Dmesg                                 func() string
	BootID                                func() string
	TableLoaded, XrayRunning              func() bool
	Facts                                 func() (Router, []string)
	Sender                                Sender
	Budget                                time.Duration
}

func (e Env) spool() Spool { return Spool{Dir: e.SpoolDir, MaxFiles: SpoolFiles, MaxBytes: SpoolBytes} }

// Run is one minute's work: what went wrong since the last run into the
// spool, and what is due out of it. A failure to keep a report is counted in
// the state, never the run's end.
func Run(ctx context.Context, e Env) (queued, sent int, err error) {
	ctx, cancel := context.WithTimeout(ctx, e.Budget)
	defer cancel()
	st := LoadState(e.StatePath)
	now := e.Now()

	var found []incident.Incident
	found = append(found, Crashes(e.CrashDir, e.Alive)...)
	pending := incident.Read(e.InboxDir)
	for _, p := range pending {
		if p.Code != "" {
			found = append(found, p.Incident)
		}
	}
	if id := e.BootID(); id != st.BootID {
		st.BootID, st.DmesgAt = id, 0
	}
	var kernel []incident.Incident
	kernel, st.DmesgAt = Dmesg(e.Dmesg(), st.DmesgAt)
	found = append(found, kernel...)
	if in := DataplaneWithoutXray(e.TableLoaded(), e.XrayRunning(), &st); in != nil {
		found = append(found, *in)
	}
	if len(found) > 0 {
		r, log := e.Facts()
		for _, in := range found {
			if Queue(e, &st, in, r, log) == nil {
				queued++
			}
		}
	}
	for _, p := range pending {
		_ = os.Remove(p.Path)
	}

	for _, en := range e.spool().Entries() {
		if ctx.Err() != nil {
			break
		}
		if now.Sub(en.Meta.QueuedAt) > MaxAge {
			_ = e.spool().Remove(en.Report.ReportID)
			st.Dropped++
			continue
		}
		if now.Before(en.Meta.NextAt) || !st.Allow(now) {
			continue
		}
		o := e.Sender.Send(ctx, en)
		if o.Done {
			_ = e.spool().Remove(en.Report.ReportID)
			if o.Err == nil {
				sent++
				if st.Sent == nil {
					st.Sent = map[string]time.Time{}
				}
				st.Sent[en.Report.Fingerprint] = now
			} else {
				st.Dropped++
				st.LastError = o.Err.Error()
			}
			continue
		}
		en.Meta.Attempts++
		en.Meta.NextAt = now.Add(o.Retry)
		if o.Err != nil {
			en.Meta.LastStatus = o.Err.Error()
			st.LastError = o.Err.Error()
		}
		_ = e.spool().Save(en)
	}
	for f, at := range st.Sent {
		if now.Sub(at) > Window {
			delete(st.Sent, f)
		}
	}
	return queued, sent, st.Save(e.StatePath)
}

// Queue makes a report of in and spools it: held to the end of the window
// when one of its fingerprint went out within it.
func Queue(e Env, st *State, in incident.Incident, r Router, log []string) error {
	now := e.Now()
	rep := FromIncident(in, r, now)
	if len(rep.Evidence.Log) == 0 {
		rep.Evidence.Log = log
	}
	var notBefore time.Time
	if at, ok := st.Sent[rep.Fingerprint]; ok && now.Sub(at) < Window {
		notBefore = at.Add(Window)
	}
	_, evicted, err := e.spool().Add(rep, now, notBefore)
	st.Dropped += evicted
	if err != nil {
		st.Dropped++
		st.LastError = err.Error()
	}
	return err
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bugreport/ -v -run Run`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
rtk git add internal/bugreport/run.go internal/bugreport/run_test.go
rtk git commit -m "feat(reporter): one run: collect, spool, send"
```

---

### Task 10: The binary

**Files:**
- Create: `cmd/vectra-reporter/main.go`, `cmd/vectra-reporter/main_test.go`

**Interfaces:**
- Consumes: `bugreport.Run`, `bugreport.Queue`, `bugreport.Boot`, `bugreport.Facts`, `routersign.Signer`, `subscription.ReadDeviceFacts`, `controlplane.MarkedHTTPClient`, `firewall.DefaultControlMark`, `routepolicy.ParseUCI`, `state.Load`.
- Produces: the commands `run`, `boot`, `shutdown`, `test`, `status`; `var Version` (ldflags).

- [ ] **Step 1: Write the failing test** — `cmd/vectra-reporter/main_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigReadsEnabledAndURL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vectra-reporter")
	if c := readConfig(p); !c.enabled || c.url != defaultURL {
		t.Fatalf("no file: %+v", c)
	}
	if err := os.WriteFile(p, []byte("config reporter 'main'\n\toption enabled '0'\n\toption url 'https://example.test/errors/router'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := readConfig(p); c.enabled || c.url != "https://example.test/errors/router" {
		t.Fatalf("%+v", c)
	}
}

func TestXrayRunningFindsVctlsXray(t *testing.T) {
	root := t.TempDir()
	for pid, cmd := range map[string]string{"10": "/usr/sbin/vctl\x00agent\x00", "20": "/usr/bin/xray\x00run\x00-c\x00/var/run/vectra-controller-pro/xray.json\x00"} {
		if err := os.MkdirAll(filepath.Join(root, pid), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, pid, "cmdline"), []byte(cmd), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !xrayRunning(root) {
		t.Fatal("xray not found")
	}
	os.Remove(filepath.Join(root, "20", "cmdline"))
	if xrayRunning(root) {
		t.Fatal("found without xray")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./cmd/vectra-reporter/`
Expected: FAIL — no Go files / `undefined: readConfig`.

- [ ] **Step 3: Write the binary** — `cmd/vectra-reporter/main.go`:

```go
// vectra-reporter sends Vectra's bug reports (ADR-0007): cron runs `run`
// every minute; the init script runs `boot` at boot and `shutdown` on the
// way down; `test` sends a TEST report now; `status` shows the spool.
// Nothing here needs vctl running, or vctl's binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vectra-controller-pro/internal/bugreport"
	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/firewall"
	"vectra-controller-pro/internal/incident"
	"vectra-controller-pro/internal/routepolicy"
	"vectra-controller-pro/internal/routersign"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/subscription"
)

// Version is stamped at build time.
var Version = "dev"

const (
	defaultURL = "https://api-app.vectra-pro.net/errors/router"
	configPath = "/etc/config/vectra-reporter"
	baseDir    = "/etc/vectra-reporter"
	lockPath   = "/var/lock/vectra-reporter.lock"
)

type cfg struct {
	enabled bool
	url     string
}

func readConfig(path string) cfg {
	c := cfg{enabled: true, url: defaultURL}
	b, err := os.ReadFile(path)
	if err != nil {
		return c
	}
	secs, err := routepolicy.ParseUCI(string(b))
	if err != nil {
		return c
	}
	for _, s := range secs {
		if s.Name != "main" {
			continue
		}
		switch s.Get("enabled") {
		case "0", "off", "false", "no":
			c.enabled = false
		}
		if u := s.Get("url"); u != "" {
			c.url = u
		}
	}
	return c
}

// xrayRunning: a process runs vctl's render (`… run -c /var/run/vectra-controller-pro/…`).
func xrayRunning(procDir string) bool {
	dirs, _ := filepath.Glob(filepath.Join(procDir, "[0-9]*"))
	for _, d := range dirs {
		b, err := os.ReadFile(filepath.Join(d, "cmdline"))
		if err != nil {
			continue
		}
		if strings.Contains(strings.ReplaceAll(string(b), "\x00", " "), "run -c /var/run/vectra-controller-pro/") {
			return true
		}
	}
	return false
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

func out(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, _ := exec.CommandContext(ctx, name, args...).Output()
	return string(b)
}

func env(c cfg) bugreport.Env {
	st, _ := state.Load("/etc/vectra-controller-pro/state.json")
	dev := subscription.ReadDeviceFacts()
	vctl := strings.TrimSpace(strings.TrimPrefix(firstLine("/usr/lib/opkg/info/vectra-controller-pro.control", "Version:"), "Version:"))
	return bugreport.Env{
		Root: "/", Reporter: Version,
		SpoolDir: filepath.Join(baseDir, "spool"), StatePath: filepath.Join(baseDir, "state.json"),
		InboxDir: incident.Dir, CrashDir: incident.CrashDir,
		Now: time.Now, Alive: alive,
		Dmesg:       func() string { return out("dmesg") },
		BootID:      func() string { b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id"); return strings.TrimSpace(string(b)) },
		TableLoaded: func() bool { return exec.Command("nft", "-t", "list", "table", "inet", "vctl").Run() == nil },
		XrayRunning: func() bool { return xrayRunning("/proc") },
		Facts: func() (bugreport.Router, []string) {
			return bugreport.Facts(bugreport.FactsEnv{Root: "/", Reporter: Version,
				XrayVersion: func() string {
					f := strings.Fields(out("xray", "version"))
					if len(f) > 1 {
						return f[1]
					}
					return ""
				},
				Logread: func() []string { return strings.Split(out("logread", "-l", "300"), "\n") }})
		},
		Sender: bugreport.Sender{URL: c.url, Client: controlplane.MarkedHTTPClient(firewall.DefaultControlMark, 20*time.Second),
			Sign: routersign.Signer(st, vctl, time.Now), HWID: dev.HWID, Model: dev.Model, OSRelease: dev.OSRelease},
		Budget: 50 * time.Second,
	}
}

func firstLine(path, prefix string) string {
	b, _ := os.ReadFile(path)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// lock: one reporter at a time; a second run just goes.
func lock() (*os.File, bool) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return nil, false
	}
	return f, true
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: vectra-reporter run|boot|shutdown|test|status")
		os.Exit(2)
	}
	// When memory runs out, the kernel takes this before anything that
	// carries the router.
	_ = os.WriteFile("/proc/self/oom_score_adj", []byte("500"), 0o644)
	c := readConfig(configPath)
	var err error
	switch os.Args[1] {
	case "shutdown":
		err = os.MkdirAll(baseDir, 0o700)
		if err == nil {
			err = os.WriteFile(filepath.Join(baseDir, "clean-shutdown"), nil, 0o600)
		}
	case "boot":
		for _, d := range []string{incident.Dir, incident.CrashDir} {
			_ = os.MkdirAll(d, 0o755)
		}
		if in := bugreport.Boot(filepath.Join(baseDir, "clean-shutdown"), "/sys/fs/pstore", time.Now()); in != nil && c.enabled {
			// Into the inbox: the next run spools and sends it, with the
			// facts of a router that is up.
			incident.NewRecorder(incident.Dir, time.Minute).Record(*in)
		}
	case "run":
		if !c.enabled {
			return
		}
		f, ok := lock()
		if !ok {
			return
		}
		defer f.Close()
		_, _, err = bugreport.Run(context.Background(), env(c))
	case "test":
		if !c.enabled {
			err = errors.New("switched off (uci vectra-reporter.main.enabled)")
			break
		}
		e := env(c)
		st := bugreport.LoadState(e.StatePath)
		r, log := e.Facts()
		if err = bugreport.Queue(e, &st, incident.Incident{Code: "TEST", Key: "test " + strconv.FormatInt(time.Now().Unix(), 10),
			Title: "a test report from vectra-reporter", Source: "reporter"}, r, log); err == nil {
			_ = st.Save(e.StatePath)
			var sent int
			_, sent, err = bugreport.Run(context.Background(), e)
			if err == nil && sent == 0 {
				err = errors.New("queued; not sent yet: see `vectra-reporter status`")
			}
		}
	case "status":
		e := env(c)
		for _, en := range (bugreport.Spool{Dir: e.SpoolDir, MaxFiles: bugreport.SpoolFiles, MaxBytes: bugreport.SpoolBytes}).Entries() {
			fmt.Printf("%s %-22s x%-3d attempts %d next %s %s\n", en.Report.Severity, en.Report.Code, en.Report.Count,
				en.Meta.Attempts, en.Meta.NextAt.Format(time.RFC3339), en.Meta.LastStatus)
		}
		st := bugreport.LoadState(e.StatePath)
		fmt.Printf("enabled %v, url %s, sent this hour %d, today %d, dropped %d, last error %q\n",
			c.enabled, c.url, st.HourCount, st.DayCount, st.Dropped, st.LastError)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vectra-reporter:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run the tests and a cross build**

Run: `go test ./cmd/vectra-reporter/ && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/vectra-reporter`
Expected: PASS; the build succeeds.

- [ ] **Step 5: Commit**

```bash
rtk git add cmd/vectra-reporter
rtk git commit -m "feat(reporter): the vectra-reporter binary"
```

---

### Task 11: vctl says what it sees

**Files:**
- Create: `cmd/vctl/crash_go123.go`, `cmd/vctl/crash_old.go`, `cmd/vctl/incidents.go`, `cmd/vctl/incidents_test.go`
- Modify: `internal/apply/apply.go:144-146`, `internal/coreengine/xray/validator.go:55`, `internal/supervisor/process.go` (exit hook), `cmd/vctl/cmd_agent.go` (recorder, crash capture, rescue hook, supervisor hook), `cmd/vctl/localui.go:111-118` (apply refused), `cmd/vctl/native_source.go` (subscription refused), `cmd/vctl/native_geo.go` (geo refused), `cmd/vctl/memwatch.go` (growth)

**Interfaces:**
- Consumes: `incident.NewRecorder`, `incident.Dir`, `incident.CrashDir` (Task 2).
- Produces: `apply.ErrRefused`; `xray.ErrLowMemory`; `(*supervisor.Process).SetOnExit(fn func(code int, err error, ran time.Duration))`; `(*daemon).incident(code, key, title string, details map[string]any)`.

- [ ] **Step 1: Write the failing tests** — `cmd/vctl/incidents_test.go`:

```go
package main

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/incident"
)

func TestApplyRefusedIsAnIncidentButLowMemoryIsNot(t *testing.T) {
	d := &daemon{incidents: incident.NewRecorder(t.TempDir(), time.Minute)}
	refused := fmt.Errorf("apply: refusing to install: %w: %w", apply.ErrRefused, errors.New("xray: failed to load geosite: RUSSIA-OUTSIDE"))
	lowMem := fmt.Errorf("apply: refusing to install: %w: %w", apply.ErrRefused, xray.ErrLowMemory)
	if !d.noteApplyErr(refused) {
		t.Fatal("a refused config was not said")
	}
	if d.noteApplyErr(lowMem) || d.noteApplyErr(errors.New("apply: write xray.json: no space")) {
		t.Fatal("not a refusal, yet said")
	}
}

func TestXrayExitsAreSaidAndAStormToo(t *testing.T) {
	dir := t.TempDir()
	d := &daemon{incidents: incident.NewRecorder(dir, 0)}
	now := time.Unix(1790000000, 0)
	for i := 0; i < 5; i++ {
		d.noteXrayExit(2, errors.New("exit status 2"), 3*time.Second, now.Add(time.Duration(i)*time.Minute))
	}
	codes := map[string]int{}
	for _, p := range incident.Read(dir) {
		codes[p.Code]++
	}
	if codes["XRAY_CRASH"] == 0 || codes["XRAY_RESTART_STORM"] != 1 {
		t.Fatalf("%v", codes)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./cmd/vctl/ -run 'ApplyRefused|XrayExits'`
Expected: FAIL — `undefined: apply.ErrRefused`.

- [ ] **Step 3: Sentinels** — in `internal/apply/apply.go` add after the imports:

```go
// ErrRefused marks an apply `xray -test` refused: the previous render stays.
var ErrRefused = errors.New("xray -test refused the config")
```

and change the write gate (line 144-146) to:

```go
	if err := a.Validate.Test(ctx, spliced); err != nil {
		return res, fmt.Errorf("apply: refusing to install (previous config left in place): %w: %w", ErrRefused, err)
	}
```

(add `"errors"` to its imports). In `internal/coreengine/xray/validator.go` add:

```go
// ErrLowMemory: the check was not run for want of memory — tried again later,
// not a verdict on the config.
var ErrLowMemory = errors.New("not enough memory to run xray -test now")
```

and wrap the low-memory return at line 55 with it: `return fmt.Errorf("xray validate: %w (%d MiB free, %d MiB wanted); nothing was changed, it is tried again later", ErrLowMemory, ...)` keeping its existing arguments (add `"errors"` to the imports if missing).

- [ ] **Step 4: The supervisor's exit hook** — in `internal/supervisor/process.go` add a field beside `onStart`:

```go
	// onExit runs after xray exits when nobody asked it to.
	onExit atomic.Pointer[func(code int, err error, ran time.Duration)]
```

a setter:

```go
// SetOnExit runs fn (in its own goroutine) every time xray exits without
// having been asked to: its exit code, the error, and how long it ran.
func (p *Process) SetOnExit(fn func(code int, err error, ran time.Duration)) {
	if fn == nil {
		p.onExit.Store(nil)
		return
	}
	p.onExit.Store(&fn)
}
```

and, in the loop right after `log.Warn("xray exited; will restart", …)`:

```go
			if hook := p.onExit.Load(); hook != nil {
				go (*hook)(exitCodeOf(exitErr), exitErr, runDuration)
			}
```

- [ ] **Step 5: vctl's incidents** — `cmd/vctl/incidents.go`:

```go
package main

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"vectra-controller-pro/internal/apply"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/incident"
)

// What vctl sees going other than meant goes to the reporter's inbox
// (internal/incident, ADR-0007): at most one of a code every 10 minutes.

var reKeyNumber = regexp.MustCompile(`\d+`)

// incident records one; without a recorder (tests, CLI commands) nothing.
func (d *daemon) incident(code, key, title string, details map[string]any) bool {
	return d.incidents.Record(incident.Incident{Code: code, Key: key, Title: title, Source: "vctl", Details: details})
}

// noteApplyErr: `xray -test` refused a render vctl made — not for want of
// memory, which is tried again and says nothing of the config.
func (d *daemon) noteApplyErr(err error) bool {
	if !errors.Is(err, apply.ErrRefused) || errors.Is(err, xray.ErrLowMemory) {
		return false
	}
	msg := err.Error()
	if i := strings.Index(msg, apply.ErrRefused.Error()); i >= 0 {
		msg = strings.TrimPrefix(msg[i+len(apply.ErrRefused.Error()):], ": ")
	}
	first := strings.SplitN(msg, "\n", 2)[0]
	return d.incident("APPLY_REFUSED", reKeyNumber.ReplaceAllString(first, "N"),
		"xray -test refused the config vctl made: "+first, map[string]any{"routeSource": d.cfg.RouteSource})
}

// noteXrayExit: xray exited unasked; five in ten minutes are a storm.
func (d *daemon) noteXrayExit(code int, err error, ran time.Duration, now time.Time) {
	reason := "exit"
	if err != nil {
		reason = reKeyNumber.ReplaceAllString(err.Error(), "N")
	}
	d.incident("XRAY_CRASH", reason, "xray exited without being asked to", map[string]any{"exitCode": code, "ranSec": int(ran.Seconds())})
	d.xrayExitsMu.Lock() // the supervisor's goroutine calls this
	defer d.xrayExitsMu.Unlock()
	d.xrayExits = append(d.xrayExits, now)
	for len(d.xrayExits) > 0 && now.Sub(d.xrayExits[0]) > 10*time.Minute {
		d.xrayExits = d.xrayExits[1:]
	}
	if len(d.xrayExits) >= 5 {
		if d.incident("XRAY_RESTART_STORM", "xray restart storm", "xray keeps exiting: 5 times in 10 minutes", map[string]any{"exits": len(d.xrayExits)}) {
			d.xrayExits = nil
		}
	}
}
```

Add to the `daemon` struct in `cmd/vctl/cmd_agent.go`: `incidents *incident.Recorder`, `xrayExitsMu sync.Mutex` and `xrayExits []time.Time`.

- [ ] **Step 6: Crash capture** — `cmd/vctl/crash_go123.go`:

```go
//go:build go1.23

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"

	"vectra-controller-pro/internal/incident"
)

// captureCrashes points Go's crash output — an unhandled panic, a fatal
// error, SIGQUIT — at a file of this process's own, which the reporter turns
// into a report once the process is gone. stderr gets it as before.
func captureCrashes() {
	if err := os.MkdirAll(incident.CrashDir, 0o755); err != nil {
		return
	}
	f, err := os.Create(filepath.Join(incident.CrashDir, fmt.Sprintf("vctl.%d", os.Getpid())))
	if err != nil {
		return
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	_ = f.Close() // SetCrashOutput keeps its own duplicate
}
```

`cmd/vctl/crash_old.go`:

```go
//go:build !go1.23

package main

// captureCrashes needs Go 1.23 (debug.SetCrashOutput); a build with an older
// toolchain — the OpenWrt SDK's — leaves crashes to stderr and logd.
func captureCrashes() {}
```

- [ ] **Step 7: Wire them** — in `cmd/vctl/cmd_agent.go`:
  - in `cmdAgent`, right after `setupLogging(*logLevel)`: `captureCrashes()`;
  - in `newDaemon`, in the `&daemon{…}` literal: `incidents: incident.NewRecorder(incident.Dir, 10*time.Minute),`; after `sup.SetOnStart(d.onXrayStart)`: `sup.SetOnExit(func(code int, err error, ran time.Duration) { d.noteXrayExit(code, err, ran, time.Now()) })`;
  - at line 744 (`d.storeRescueState(decision.NextState, decision.Reason)`), before it:

```go
	if decision.NextState.Mode == rescue.ModeDirect && d.st.Rescue.Mode != string(rescue.ModeDirect) {
		d.incident("RESCUE_DIRECT", reKeyNumber.ReplaceAllString(decision.Reason, "N"), "the rescue switched the router to direct: "+decision.Reason, nil)
	}
```

  In `cmd/vctl/localui.go` `applyProviderWith`, in `if err != nil {`: add `d.noteApplyErr(err)` before `return res, err`.
  In `cmd/vctl/native_source.go` `maybeRefreshNative`, inside `if err != nil {` before `return`:

```go
			if errors.Is(err, routepolicy.ErrEmptyFeed) || errors.Is(err, routepolicy.ErrUnsupported) {
				d.incident("SUBSCRIPTION_REFUSED", reKeyNumber.ReplaceAllString(err.Error(), "N"), "the subscription answered what vctl refuses: "+err.Error(), map[string]any{"subscription": s.Name})
			}
```

  In `cmd/vctl/native_geo.go` `startNativeGeo`'s goroutine, in `if err != nil {`: `if strings.Contains(err.Error(), "lacks") || strings.Contains(err.Error(), "sha256") { d.incident("GEO_REFUSED", reKeyNumber.ReplaceAllString(err.Error(), "N"), "a geo update was refused: "+err.Error(), nil) }`.
  In `cmd/vctl/memwatch.go` `review`, next to each `logging.L().Warn` of a growth (after `w.reported[g.Series] = delta`): `if w.onGrowth != nil { w.onGrowth(g.Series, memguard.MiB(g.FromKB), memguard.MiB(g.ToKB)) }` with a field `onGrowth func(series string, fromMiB, toMiB uint64)` on `memWatch`, set where the daemon makes its memWatch to `func(s string, f, t uint64) { d.incident("MEMORY_GROWTH", s, fmt.Sprintf("memory keeps growing: %s %d → %d MiB", s, f, t), nil) }`.

- [ ] **Step 8: Run all of vctl's tests**

Run: `go build ./... && go vet ./... && go test ./cmd/vctl/ ./internal/apply/ ./internal/supervisor/ ./internal/coreengine/xray/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
rtk git add cmd/vctl internal/apply internal/supervisor internal/coreengine/xray
rtk git commit -m "feat(vctl): say to the reporter what goes other than meant"
```

---

### Task 12: The shell says it too

**Files:**
- Modify: `openwrt/files/etc/init.d/vectra-controller-pro` (an `incident` helper; `hand_back_and_fail`), `openwrt/files/usr/libexec/vectra-controller-pro/deadman.sh` (vctl started again; the hand-back)
- Test: `openwrt/watch_test.go`, `openwrt/packaging_test.go`

**Interfaces:**
- Produces: shell `incident <code> <key> <title>` writing one JSON line into `${INCIDENT_DIR:-/var/run/vectra-reporter/inbox}`; words only, no quotes or backslashes in its arguments.

- [ ] **Step 1: Write the failing tests** — in `openwrt/watch_test.go`:

```go
// A start that has to give the router back says so to the reporter.
func TestAFailedStartTellsTheReporter(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	_ = os.Remove(s.path("render"))
	s.run("1", "start_service\n", "INCIDENT_DIR="+s.path("inbox"))
	b, err := os.ReadFile(firstFile(t, s.path("inbox")))
	if err != nil || !strings.Contains(string(b), `"code":"HANDBACK"`) || !strings.Contains(string(b), `"source":"init"`) {
		t.Fatalf("%s %v", b, err)
	}
}

// The dead-man's start of a vctl that was not running is an incident.
func TestTheDeadManTellsTheReporterWhenItStartsVctl(t *testing.T) {
	t.Parallel()
	s := newStack(t)
	on(t, s)
	_ = os.Remove(s.path("vctl.running"))
	s.deadman("VCTL_CRASHES=1", "INCIDENT_DIR="+s.path("inbox"))
	b, err := os.ReadFile(firstFile(t, s.path("inbox")))
	if err != nil || !strings.Contains(string(b), `"code":"VCTL_DOWN"`) || !strings.Contains(string(b), `"source":"deadman"`) {
		t.Fatalf("%s %v", b, err)
	}
}

func firstFile(t *testing.T, dir string) string {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(names) == 0 {
		t.Fatalf("nothing in %s", dir)
	}
	return names[0]
}
```

(add `"path/filepath"` to its imports if missing; add `"date"` and `"ls"` and `"wc"` to the stack's tool links in `openwrt/packaging_test.go`'s prologue if the helper needs them).

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./openwrt/ -run 'FailedStartTells|DeadManTellsTheReporter'`
Expected: FAIL — nothing in the inbox.

- [ ] **Step 3: The helper** — in `openwrt/files/etc/init.d/vectra-controller-pro`, after `MEMINFO=…`:

```sh
# incident <code> <key> <title>: one line of JSON into the reporter's inbox
# (vectra-reporter, ADR-0007), which it sends as a bug report. Never more than
# 32 files there, and never a failure of what called it. Words only: no quotes
# or backslashes in the arguments.
INCIDENT_DIR="${INCIDENT_DIR:-/var/run/vectra-reporter/inbox}"
incident() {
	local f
	mkdir -p "$INCIDENT_DIR" 2>/dev/null || return 0
	[ "$(ls "$INCIDENT_DIR" 2>/dev/null | wc -l)" -lt 32 ] || return 0
	f="$INCIDENT_DIR/$(date +%s)000000000-$1-$$.json"
	printf '{"code":"%s","key":"%s","title":"%s","at":"%s","source":"%s"}\n' \
		"$1" "$2" "$3" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${INCIDENT_SOURCE:-init}" > "$f.tmp" 2>/dev/null &&
		mv "$f.tmp" "$f" 2>/dev/null
	rm -f "$f.tmp" 2>/dev/null
	return 0
}
```

and in `hand_back_and_fail`, as its first line: `incident HANDBACK "$1" "vctl handed the router back: $1"`.

- [ ] **Step 4: The dead-man** — in `deadman.sh`, set `INCIDENT_SOURCE=deadman` before it sources the init script (the helper comes with it), and where it starts vctl again add `incident VCTL_DOWN "vctl was not running" "vctl was switched on and not running; the dead-man started it"`; where it hands the router back (`vectra off`) add `incident HANDBACK "vctl down for 10 minutes" "vctl stayed down for 10 minutes; the dead-man gave the router back"`.

- [ ] **Step 5: Run the packaging tests**

Run: `go test ./openwrt/`
Expected: PASS (the two new tests and every existing one).

- [ ] **Step 6: Commit**

```bash
rtk git add openwrt
rtk git commit -m "feat(vctl): the dead-man and the init script tell the reporter"
```

---

### Task 13: The package

**Files:**
- Create: `openwrt/reporter/etc/init.d/vectra-reporter`, `openwrt/reporter/etc/config/vectra-reporter`, `openwrt/reporter/postinst`, `openwrt/reporter/prerm`, `openwrt/reporter/postrm`
- Modify: `scripts/build-pro-feed.sh` (build and pack the reporter; vctl depends on it in the pro feed), `install/install.sh` (uninstall takes it too), `test/install/router.sh` (the lifecycle checks it)

**Interfaces:**
- Produces: `vectra-reporter_<REPORTER_VERSION>_<arch>.ipk` in every arch of the pro feed; `REPORTER_VERSION="1.0.0-r1"`.

- [ ] **Step 1: The init script** — `openwrt/reporter/etc/init.d/vectra-reporter`:

```sh
#!/bin/sh /etc/rc.common
# vectra-reporter's marks around a boot (ADR-0007): a boot after no clean
# shutdown is an incident. Its work is cron's, every minute.
START=12
STOP=05

start() {
	/usr/sbin/vectra-reporter boot >/dev/null 2>&1 &
}

stop() {
	/usr/sbin/vectra-reporter shutdown >/dev/null 2>&1
}
```

- [ ] **Step 2: The config** — `openwrt/reporter/etc/config/vectra-reporter`:

```
config reporter 'main'
	option enabled '1'
	option url 'https://api-app.vectra-pro.net/errors/router'
```

- [ ] **Step 3: The maintainer scripts** — `openwrt/reporter/postinst`:

```sh
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
# Every minute from root's crontab, in a block of its own; cron told only
# when the block changed.
CRONTAB="${CRONTAB:-/etc/crontabs/root}"
tmp="$CRONTAB.vectra-reporter"
mkdir -p "${CRONTAB%/*}" && { [ -f "$CRONTAB" ] || true >> "$CRONTAB"; }
{ sed '/^# >>> vectra-reporter (managed) >>>$/,/^# <<< vectra-reporter (managed) <<<$/d' "$CRONTAB" &&
	printf '%s\n' "# >>> vectra-reporter (managed) >>>" \
		"* * * * * /usr/sbin/vectra-reporter run >/dev/null 2>&1" \
		"# <<< vectra-reporter (managed) <<<"; } > "$tmp"
if cmp -s "$tmp" "$CRONTAB"; then
	rm -f "$tmp"
elif mv "$tmp" "$CRONTAB"; then
	/etc/init.d/cron enable >/dev/null 2>&1
	/etc/init.d/cron restart >/dev/null 2>&1
fi
rm -f "$tmp"
mkdir -p /etc/vectra-reporter/spool /var/run/vectra-reporter/inbox /var/run/vectra-reporter/crash
# The first boot after the install is no surprise.
[ -e /etc/vectra-reporter/state.json ] || true > /etc/vectra-reporter/clean-shutdown
/etc/init.d/vectra-reporter enable >/dev/null 2>&1
exit 0
```

`openwrt/reporter/prerm`:

```sh
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
[ "${PKG_UPGRADE:-0}" = 1 ] && exit 0
[ "${1:-}" = upgrade ] && exit 0
CRONTAB="${CRONTAB:-/etc/crontabs/root}"
if grep -qxF '# >>> vectra-reporter (managed) >>>' "$CRONTAB" 2>/dev/null &&
	sed '/^# >>> vectra-reporter (managed) >>>$/,/^# <<< vectra-reporter (managed) <<<$/d' "$CRONTAB" > "$CRONTAB.vectra-reporter" &&
	mv "$CRONTAB.vectra-reporter" "$CRONTAB"; then
	/etc/init.d/cron restart >/dev/null 2>&1
fi
/etc/init.d/vectra-reporter disable >/dev/null 2>&1
exit 0
```

`openwrt/reporter/postrm`:

```sh
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
[ "${PKG_UPGRADE:-0}" = 1 ] && exit 0
[ "${1:-}" = upgrade ] && exit 0
rm -rf /etc/vectra-reporter /var/run/vectra-reporter
exit 0
```

- [ ] **Step 4: Build and pack it** — in `scripts/build-pro-feed.sh`, next to `GEODATA_VERSION=`: `REPORTER_VERSION="1.0.0-r1"`; in the per-arch loop, after the vctl ipk, build `./cmd/vectra-reporter` with the same Go target as vctl (`GOOS=linux GOARCH=… GOARM=… GOMIPS=… CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.Version=$REPORTER_VERSION"`), install it at `usr/sbin/vectra-reporter` (0755) with `openwrt/reporter/etc` under the data tree, write `control` (`Package: vectra-reporter`, `Version: $REPORTER_VERSION`, `Architecture: $arch`, `Section: net`, `Maintainer: Vectra`, `Description: Vectra's bug reports: crashes and what goes other than meant, sent signed and redacted to Vectra (ADR-0007).`), `conffiles` (`/etc/config/vectra-reporter`), and the three maintainer scripts (0755), and pack `vectra-reporter_${REPORTER_VERSION}_${arch}.ipk` with the script's `ipk` helper. Add `vectra-reporter` to vctl's `Depends` in the pro feed where `vectra-geodata` is added.

- [ ] **Step 5: Uninstall takes it** — in `install/install.sh` `uninstall()`, after the vectra-geodata line: `installed vectra-reporter && run opkg remove vectra-reporter`.

- [ ] **Step 6: The installer test** — in `test/install/router.sh` lifecycle, after `fresh_geodata`:

```sh
	check fresh_reporter "vectra-reporter $(version_of vectra-reporter): cron runs it every minute, it answers" \
		sh -c 'grep -qxF "* * * * * /usr/sbin/vectra-reporter run >/dev/null 2>&1" /etc/crontabs/root && /usr/sbin/vectra-reporter status >/dev/null'
```

and in the uninstall checks: `check uninstall_reporter "vectra-reporter removed, its cron block and its spool with it" sh -c "! opkg list-installed | grep -q '^vectra-reporter ' && ! grep -q vectra-reporter /etc/crontabs/root && [ ! -d /etc/vectra-reporter ]"`.

- [ ] **Step 7: Run the installer test**

Run: `DOCKER_CONTEXT=colima ./test/install/run.sh lifecycle`
Expected: `lifecycle N pass, 0 fail`.

- [ ] **Step 8: Commit**

```bash
rtk git add openwrt/reporter scripts/build-pro-feed.sh install/install.sh test/install/router.sh
rtk git commit -m "feat(reporter): the vectra-reporter package"
```

---

### Task 14: End to end on the stand, and the contract's vector

**Files:**
- Create: `test/dataplane/reportsink/main.go`, `test/dataplane/standkey/main.go`, `test/dataplane/stand/mode-reports.sh`, `ui/contract/report-vector.json`, `internal/bugreport/vector_test.go`
- Modify: `test/dataplane/run.sh` (build the sink and the reporter; the `reports` mode), `test/dataplane/stand/stand.sh` (dispatch), `test/dataplane/Dockerfile` (copy the reporter), `test/dataplane/README.md`

**Interfaces:**
- Produces: stand mode `reports`; `ui/contract/report-vector.json` (a signed request made with fixed keys and a fixed random source, for Connect's tests).

- [ ] **Step 1: The vector test (writes the file with `-update`)** — `internal/bugreport/vector_test.go`:

```go
package bugreport

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"testing"
	"time"

	"vectra-controller-pro/internal/uatoken"
)

var update = flag.Bool("update", false, "rewrite ui/contract/report-vector.json")

// The contract's vector: a report and the exact request a router makes of it
// — fixed keys, fixed randomness — for Vectra Connect's tests.
func TestReportVector(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, 32)
	dev := ed25519.NewKeyFromSeed(seed)
	vectraPriv, _ := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{9}, 32))
	at := time.Date(2026, 9, 29, 21, 3, 11, 0, time.UTC)
	r := Report{Schema: 1, ReportID: "0f8d3c2e-6a51-4c1e-9d0b-7f3e2a1c9b44", Code: "VCTL_PANIC", Severity: "critical",
		Fingerprint: Fingerprint("VCTL_PANIC", "panic: runtime error: index out of range [N] with length N"),
		Title: "vctl crashed: panic: runtime error: index out of range [3] with length 3", FirstAt: at, LastAt: at, Count: 1,
		Router: Router{DeviceID: "vectra-0123456789ab", Model: "Xiaomi Mi Router AX3000T", Arch: "aarch64_cortex-a53", OpenWrt: "24.10.6", Vctl: "0.6.0-r19"},
		Evidence: Evidence{Stack: "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:\n"}}
	body, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	url := "https://api-app.vectra-pro.net/errors/router?b=" + hex.EncodeToString(sum[:])
	hwid := "4f8e0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7"
	ua, err := uatoken.UserAgent(uatoken.Request{Version: "0.6.0-r19", DeviceID: r.Router.DeviceID, DeviceKey: dev, HWID: hwid, URL: url,
		Kid: 1, ServerPub: vectraPriv.PublicKey().Bytes(), Now: at}, bytes.NewReader(bytes.Repeat([]byte{5}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	vector := map[string]any{
		"comment":          "A router's report request (docs/CONNECT-BUG-REPORTS.md), made with fixed keys: open the token with vectraPrivateKey (kid 1) and devicePublicKey for target, and check b = sha256(body).",
		"vectraPrivateKey": base64.StdEncoding.EncodeToString(vectraPriv.Bytes()),
		"devicePublicKey":  base64.StdEncoding.EncodeToString(dev.Public().(ed25519.PublicKey)),
		"now":              at.Format(time.RFC3339),
		"method":           "POST",
		"url":              url,
		"target":           url[len("https://api-app.vectra-pro.net"):],
		"headers": map[string]string{"User-Agent": ua, "x-hwid": hwid, "x-device-os": "OpenWrt", "x-device-model": r.Router.Model,
			"x-ver-os": r.Router.OpenWrt, "x-vectra-report-id": r.ReportID, "Content-Type": "application/json"},
		"body": string(body),
	}
	keys := func(kid byte) (*ecdh.PrivateKey, bool) { return vectraPriv, kid == 1 }
	devices := func(id string) (ed25519.PublicKey, bool) { return dev.Public().(ed25519.PublicKey), id == r.Router.DeviceID }
	if _, err := uatoken.Open(ua, keys, devices, hwid, vector["target"].(string), at, time.Minute); err != nil {
		t.Fatalf("the vector does not open: %v", err)
	}
	out, _ := json.MarshalIndent(vector, "", "  ")
	const path = "../../ui/contract/report-vector.json"
	if *update {
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if old, err := os.ReadFile(path); err != nil || !bytes.Equal(bytes.TrimSpace(old), bytes.TrimSpace(out)) {
		t.Fatalf("ui/contract/report-vector.json is not this vector (run with -update): %v", err)
	}
}
```

Run: `go test ./internal/bugreport/ -run ReportVector -update && go test ./internal/bugreport/ -run ReportVector`
Expected: the file is written, then PASS.

- [ ] **Step 2: The sink** — `test/dataplane/reportsink/main.go`:

```go
// reportsink is the stand's Vectra Connect for bug reports: HTTPS on -listen
// with the stand's certificate, every POST checked as the contract says (the
// body's hash, the token for this path, this HWID, this device) and one line
// per report appended to -log: "OK <code> <count>" or "BAD <reason>".
package main

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/uatoken"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:443", "")
	cert := flag.String("cert", "", "")
	key := flag.String("key", "", "")
	vectraKey := flag.String("vectra-key", "", "Vectra's X25519 private key, base64 (kid 1)")
	statePath := flag.String("state", "/etc/vectra-controller-pro/state.json", "the router's state.json: its device key")
	out := flag.String("log", "/tmp/reportsink.log", "")
	flag.Parse()
	raw, err := base64.StdEncoding.DecodeString(*vectraKey)
	if err != nil {
		log.Fatal(err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		log.Fatal(err)
	}
	note := func(format string, a ...any) {
		f, err := os.OpenFile(*out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, format+"\n", a...)
			f.Close()
		}
	}
	http.HandleFunc("/errors/router", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		sum := sha256.Sum256(body)
		if r.URL.Query().Get("b") != hex.EncodeToString(sum[:]) {
			note("BAD body-hash")
			w.WriteHeader(400)
			return
		}
		st, err := state.Load(*statePath)
		if err != nil {
			note("BAD state %v", err)
			w.WriteHeader(503)
			return
		}
		pub, _ := base64.StdEncoding.DecodeString(st.DevicePublicKey)
		keys := func(kid byte) (*ecdh.PrivateKey, bool) { return priv, kid == 1 }
		devices := func(id string) (ed25519.PublicKey, bool) { return ed25519.PublicKey(pub), id == st.DeviceIdentifier }
		if _, err := uatoken.Open(r.UserAgent(), keys, devices, r.Header.Get("x-hwid"), r.URL.RequestURI(), time.Now(), 5*time.Minute); err != nil {
			note("BAD token %v", err)
			w.WriteHeader(401)
			return
		}
		var rep struct {
			Code  string `json:"code"`
			Count int    `json:"count"`
		}
		if json.Unmarshal(body, &rep) != nil {
			note("BAD json")
			w.WriteHeader(422)
			return
		}
		note("OK %s %d", rep.Code, rep.Count)
		w.WriteHeader(202)
	})
	log.Fatal(http.ListenAndServeTLS(*listen, *cert, *key, nil))
}
```

- [ ] **Step 3: The stand's key tool** — `test/dataplane/standkey/main.go` (Vectra's test key, and the router's state told of it, as check-in's `claimKey` would):

```go
// standkey: `standkey new` prints a fresh X25519 private key (base64);
// `standkey claim -state P -private K` writes its public half into the
// router's state.json as check-in's claimKey (kid 1) — the key the router
// seals its tokens to.
package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/state"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "new" {
		k, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(k.Bytes()))
		return
	}
	fs := flag.NewFlagSet("claim", flag.ExitOnError)
	path := fs.String("state", "/etc/vectra-controller-pro/state.json", "")
	private := fs.String("private", "", "")
	_ = fs.Parse(os.Args[2:])
	raw, err := base64.StdEncoding.DecodeString(*private)
	if err != nil {
		log.Fatal(err)
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		log.Fatal(err)
	}
	st, err := state.Load(*path)
	if err != nil {
		log.Fatal(err)
	}
	st.ClaimKey = &controlplane.ClaimKey{Kid: 1, PublicKey: base64.StdEncoding.EncodeToString(k.PublicKey().Bytes())}
	if err := state.Save(*path, st); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 4: The mode** — `test/dataplane/stand/mode-reports.sh`, sourced by `stand.sh` like the other modes (a `reports)` case in its dispatch that calls `reports_main`), using its helpers `say`, `record` and `in_inet`:

```sh
# MODE=reports — the reporter end to end (ADR-0007): a vctl crash, vctl's
# data plane without xray and a reboot nobody saw coming reach the stand's
# Connect (reportsink, on the origin's address with the stand's certificate)
# as reports it verifies; nothing goes out with the reporter switched off.

SINK_LOG=/tmp/reportsink.log
AGENT_CFG=/tmp/reports-agent.json

reports_setup() {
	say "the stand's Connect on $ORIGIN_IP:443; the router's model; no cron (no dead-man, no scheduled runs)"
	/etc/init.d/cron stop >/dev/null 2>&1
	mkdir -p /tmp/sysinfo && echo "Xiaomi Mi Router AX3000T" > /tmp/sysinfo/model
	cat /stand/tls/cert.pem >> /etc/ssl/certs/ca-certificates.crt
	cat > "$AGENT_CFG" <<JSON
{"controlUrl":"https://api.vectra-pro.net","statePath":"/etc/vectra-controller-pro/state.json",
 "xrayConfigPath":"/tmp/reports-operator.json","xrayRenderPath":"/var/run/vectra-controller-pro/xray.json",
 "legacyStatePath":"/tmp/no-legacy.json"}
JSON
	/usr/sbin/vctl agent -config "$AGENT_CFG" >/tmp/reports-vctl.log 2>&1 &
	REPORTS_VCTL=$!
	sleep 3 # its identity is in state.json now
	VKEY=$(/stand/bin/standkey new)
	/stand/bin/standkey claim -state /etc/vectra-controller-pro/state.json -private "$VKEY"
	in_inet /stand/bin/reportsink -listen "$ORIGIN_IP:443" -cert /stand/tls/cert.pem -key /stand/tls/key.pem \
		-vectra-key "$VKEY" -log "$SINK_LOG" >/tmp/reportsink.out 2>&1 &
	uci -q set vectra-reporter.main.url="https://$ORIGIN_IP/errors/router" && uci commit vectra-reporter
	/usr/sbin/vectra-reporter boot >/dev/null 2>&1 # the inbox and crash dirs; the install's clean mark
}

reports_run_until() { # <result> <pattern>: a run every 5 s until the sink logs the pattern (2 min)
	_i=0
	while [ $_i -lt 24 ]; do
		/usr/sbin/vectra-reporter run >/dev/null 2>&1
		if grep -q "$2" "$SINK_LOG" 2>/dev/null; then
			record "$1" PASS "$(grep "$2" "$SINK_LOG" | tail -1)"
			return
		fi
		_i=$((_i + 1))
		sleep 5
	done
	record "$1" FAIL "the sink has: $(tail -3 "$SINK_LOG" 2>/dev/null | tr '\n' ' ') status: $(/usr/sbin/vectra-reporter status 2>&1 | tail -2 | tr '\n' ' ')"
}

reports_main() {
	reports_setup
	say "vctl crashes (SIGQUIT: Go writes its crash output to the reporter's file)"
	kill -QUIT "$REPORTS_VCTL"
	sleep 1
	reports_run_until reports_vctl_panic "OK VCTL_PANIC"

	say "vctl's table loaded, no xray: said at the second run in a row"
	printf 'table inet vctl {\n}\n' > /tmp/vctl-empty.nft && nft -f /tmp/vctl-empty.nft
	reports_run_until reports_dataplane "OK DATAPLANE_WITHOUT_XRAY"
	nft delete table inet vctl 2>/dev/null

	say "a boot after no clean shutdown"
	rm -f /etc/vectra-reporter/clean-shutdown
	/usr/sbin/vectra-reporter boot >/dev/null 2>&1
	reports_run_until reports_reboot "OK UNEXPECTED_REBOOT"

	say "switched off: nothing goes out"
	uci -q set vectra-reporter.main.enabled=0 && uci commit vectra-reporter
	_before=$(wc -l < "$SINK_LOG")
	/usr/sbin/vectra-reporter test >/dev/null 2>&1
	/usr/sbin/vectra-reporter run >/dev/null 2>&1
	if [ "$(wc -l < "$SINK_LOG")" = "$_before" ]; then
		record reports_off PASS "nothing sent while switched off"
	else
		record reports_off FAIL "sent while switched off: $(tail -1 "$SINK_LOG")"
	fi

	if grep -q '^BAD' "$SINK_LOG"; then
		record reports_all_verified FAIL "$(grep '^BAD' "$SINK_LOG" | head -3 | tr '\n' ' ')"
	else
		record reports_all_verified PASS "every report the sink got was verified: $(grep -c '^OK' "$SINK_LOG")"
	fi
}
```

Note `test` while switched off: `main.go`'s `test` must honour `enabled` too — add `if !c.enabled { err = errors.New("switched off (uci vectra-reporter.main.enabled)"); break }` at the top of its case in Task 10.

In `test/dataplane/run.sh`, next to the vctl build (same `GOOS=linux GOARCH=arm64 CGO_ENABLED=0`, same `-trimpath`):

```bash
( cd "$MODULE" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o "$BUILD/reportsink" ./test/dataplane/reportsink )
( cd "$MODULE" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o "$BUILD/standkey" ./test/dataplane/standkey )
( cd "$MODULE" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=stand" -o "$BUILD/vectra-reporter" ./cmd/vectra-reporter )
```

and add `reports` to its list of modes with the description "the reporter end to end: crash, data plane without xray, unexpected reboot, switched off". In `test/dataplane/Dockerfile`:

```dockerfile
COPY build/reportsink build/standkey /stand/bin/
COPY build/vectra-reporter /usr/sbin/vectra-reporter
COPY reporter-etc/ /etc/
```

with `run.sh` copying `openwrt/reporter/etc` to `$BUILD/../reporter-etc` before the image build (the build context is `test/dataplane`).

- [ ] **Step 5: Run the mode**

Run: `DOCKER_CONTEXT=colima ./test/dataplane/run.sh reports`
Expected: `reports_vctl_panic`, `reports_dataplane`, `reports_reboot`, `reports_off`, `reports_all_verified` PASS.

- [ ] **Step 6: Docs** — `CHANGELOG.md`: a `## vectra-reporter 1.0.0-r1 — bug reports` entry and vctl's `0.6.0-r19` entry (the producers); `test/dataplane/README.md`: the `reports` mode's row; `docs/CANARY.md`: a short section "Bug reports" — `vectra-reporter status`, `vectra-reporter test`, the switch `uci set vectra-reporter.main.enabled=0`.

- [ ] **Step 7: Commit**

```bash
rtk git add test/dataplane ui/contract/report-vector.json internal/bugreport/vector_test.go CHANGELOG.md docs/CANARY.md
rtk git commit -m "test(reporter): end to end on the stand; the contract's vector"
```
