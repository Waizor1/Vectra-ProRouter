package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The top-level keys the router-side options rewrite, beyond inbounds and
// outbounds.
const (
	APIKey              = "api"
	MetricsKey          = "metrics"
	BurstObservatoryKey = "burstObservatory"
	ObservatoryKey      = "observatory"
	LogKey              = "log"
)

// Tags of the objects the options install. Chosen so no provider selector
// (a PREFIX match over outbound tags: "bridge-", "whitelist-lv3", ...) can
// ever pick them up — xray registers the metrics endpoint as an outbound.
const (
	APITag     = "vctl-api"
	MetricsTag = "vctl-metrics"
)

// APIServices are the gRPC services the router enables: RoutingService for the
// balancers (GetBalancerInfo / OverrideBalancerTarget) and StatsService for
// traffic. NOT HandlerService — it lists every outbound WITH its credentials
// and can add or remove outbounds — and not LoggerService: the UI needs
// neither, and the API has no authentication. (`vctl api add-outbound` and
// friends therefore do not work against a router's xray; they are dev tools.)
var APIServices = []string{"RoutingService", "StatsService"}

// The router's defaults. The API and metrics ports must not collide with
// anything else on an OpenWrt router; PassWall2 (stopped while vctl owns the
// router) uses neither.
const (
	DefaultAPIListen     = "127.0.0.1:10085"
	DefaultMetricsListen = "127.0.0.1:10086"
	// DefaultRouterProbeInterval replaces a provider interval LONGER than it.
	// Ten minutes with the provider's 2 samples notices a dead node in 10-20
	// minutes instead of hours, for ~1 GB/month of probe traffic across ~22
	// nodes (each probe is a fresh connection through the node).
	DefaultRouterProbeInterval = 10 * time.Minute
)

// MinProbeInterval and MaxProbeInterval bound an observatory override. Below a
// minute the probes become a noticeable share of a metered plan; above a day
// the balancers stop noticing a dead node at all.
const (
	MinProbeInterval = time.Minute
	MaxProbeInterval = 24 * time.Hour
)

// SpliceOptions are the router-side additions to a provider document beyond
// the inbound swap and the egress mark. The zero value adds nothing.
type SpliceOptions struct {
	// APIListen installs xray's gRPC API on this LOOPBACK address. Empty = no
	// API. The router UI reads and overrides the balancers through it.
	APIListen string
	// MetricsListen installs xray's metrics endpoint (/debug/vars: observatory
	// results and per-outbound traffic) on this LOOPBACK address. Empty = none.
	MetricsListen string
	// ProbeInterval overrides the observatory's probe interval when > 0.
	//
	// The provider ships burstObservatory at 43200s (12 h) with 2 samples: xray
	// probes every node once at start and then twice per 24 h at random
	// moments. A node that dies after the start is still "alive" to the
	// balancers for hours, and leastLoad keeps handing it connections. The
	// provider's documents target phone apps that restart constantly; a router
	// runs for a day between its nightly reboots.
	ProbeInterval time.Duration
	// NoAccessLog sets log.access to "none". The provider leaves it unset,
	// and xray then prints one line PER CONNECTION to stdout — on a router,
	// every destination every LAN client opens, churned through a pipe into
	// RAM, burying the warnings the log is kept for.
	NoAccessLog bool
	// Rules are the router owner's own sites, rendered at the top of the
	// routing (user_rules.go). Empty = nothing added, the inbound's sniffing
	// exactly the operator's.
	Rules UserRules
	// DNS sends the router's resolver through the tunnel (dns_steer.go). nil =
	// the provider's "dns" and routing untouched, no DNS inbound.
	DNS *DNSOptions
	// Services are the owner's country per service ("tiktok": "DE"; ""
	// = the entry's own path), rendered as overlays (services.go).
	Services map[string]string
	// RussiaDirect sends the provider's Russian BL-RU rules to its plain
	// freedom outbound (russia_direct.go). YouTube's BL-RU rule stays.
	RussiaDirect bool
	// FakeDNS answers every name a rule proxies with a FakeDNS address (the
	// DNS inbound must be on): such a connection reaches xray whatever the
	// name's real address, so the kernel may send the rest of the Russian
	// networks straight out (DirectBypass).
	FakeDNS bool
	// ExitProbeListen installs the exit probe on this LOOPBACK address: an
	// HTTP proxy with one account per foreign exit (exit_check.go), through
	// which the router asks each exit for blocked sites itself. Empty = none.
	ExitProbeListen string
	// LeaveOut are exits found unfit: they leave every balancer that keeps
	// another member (exit_check.go). Their outbounds stay.
	LeaveOut []string
}

// Key is a stable fingerprint of the options, so the daemon can tell that a
// config rendered under different options is stale even when the provider
// bytes are identical. Without rules it is what it was before rules
// existed, so an upgrade alone re-renders nothing.
func (o SpliceOptions) Key() string {
	k := fmt.Sprintf("api=%s;metrics=%s;probe=%s;noaccess=%t", o.APIListen, o.MetricsListen, o.ProbeInterval, o.NoAccessLog)
	if !o.Rules.empty() {
		k += ";rules=" + o.Rules.key()
	}
	if o.RussiaDirect {
		k += ";ru=direct"
	}
	if o.FakeDNS {
		k += ";fakedns"
	}
	if sk := servicesKey(o.Services); sk != "" {
		k += ";svc=" + sk
	}
	if o.ExitProbeListen != "" {
		k += ";exitprobe=" + o.ExitProbeListen
	}
	if len(o.LeaveOut) > 0 {
		out := append([]string(nil), o.LeaveOut...)
		sort.Strings(out)
		k += ";leaveout=" + strings.Join(out, ",")
	}
	return k + o.DNS.key()
}

func (o SpliceOptions) validate() error {
	for _, l := range []struct{ name, addr string }{{"api", o.APIListen}, {"metrics", o.MetricsListen}} {
		if l.addr == "" {
			continue
		}
		host, port, err := net.SplitHostPort(l.addr)
		if err != nil {
			return fmt.Errorf("xray splice: %s listen %q: %w", l.name, l.addr, err)
		}
		// Never beyond loopback: the API can re-point every balancer and add
		// outbounds, and neither endpoint has authentication.
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("xray splice: %s listen %q is not a loopback address; refusing to expose it", l.name, l.addr)
		}
		if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("xray splice: %s listen %q has no usable port", l.name, l.addr)
		}
	}
	if o.ProbeInterval != 0 && (o.ProbeInterval < MinProbeInterval || o.ProbeInterval > MaxProbeInterval) {
		return fmt.Errorf("xray splice: probe interval %s outside [%s, %s]", o.ProbeInterval, MinProbeInterval, MaxProbeInterval)
	}
	if err := o.DNS.validate(); err != nil {
		return err
	}
	if o.ExitProbeListen != "" {
		if _, _, err := loopbackListen("exit probe", o.ExitProbeListen); err != nil {
			return err
		}
	}
	return nil
}

func apiObject(listen string) json.RawMessage {
	b, _ := json.Marshal(struct {
		Tag      string   `json:"tag"`
		Listen   string   `json:"listen"`
		Services []string `json:"services"`
	}{APITag, listen, APIServices})
	return b
}

func metricsObject(listen string) json.RawMessage {
	b, _ := json.Marshal(struct {
		Tag    string `json:"tag"`
		Listen string `json:"listen"`
	}{MetricsTag, listen})
	return b
}

// ParseXrayDuration decodes a duration the way xray's config loader does: a
// Go duration string ("43200s", "10m") or a bare number of NANOSECONDS.
func ParseXrayDuration(raw json.RawMessage) (time.Duration, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return 0, err
	}
	switch x := v.(type) {
	case string:
		return time.ParseDuration(x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, err
		}
		return time.Duration(f), nil
	default:
		return 0, fmt.Errorf("not a duration: %s", compactJSON(raw))
	}
}

func durationJSON(d time.Duration) json.RawMessage {
	return json.RawMessage(strconv.Quote(strconv.FormatInt(int64(d/time.Second), 10) + "s"))
}

// rewriteBurstInterval sets burstObservatory.pingConfig.interval, keeping every
// other byte of the object as the provider wrote it. It returns the interval
// the provider had (0 when it set none — xray then defaults to 1 minute).
func rewriteBurstInterval(raw json.RawMessage, d time.Duration) (json.RawMessage, time.Duration, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, 0, fmt.Errorf("xray splice: burstObservatory is not an object: %w", err)
	}
	ping := obj["pingConfig"]
	if len(ping) == 0 || bytes.Equal(bytes.TrimSpace(ping), []byte("null")) {
		ping = json.RawMessage("{}")
	}
	var pingObj map[string]json.RawMessage
	if err := json.Unmarshal(ping, &pingObj); err != nil {
		return nil, 0, fmt.Errorf("xray splice: burstObservatory.pingConfig is not an object: %w", err)
	}
	var was time.Duration
	if cur, ok := pingObj["interval"]; ok {
		p, err := ParseXrayDuration(cur)
		if err != nil {
			return nil, 0, fmt.Errorf("xray splice: burstObservatory.pingConfig.interval: %w", err)
		}
		was = p
	}
	newPing, _, err := rewriteObjectField(ping, "interval", durationJSON(d))
	if err != nil {
		return nil, 0, err
	}
	out, _, err := rewriteObjectField(raw, "pingConfig", newPing)
	if err != nil {
		return nil, 0, err
	}
	return out, was, nil
}

// rewriteClassicInterval sets observatory.probeInterval (the non-burst
// observatory), returning the provider's value (0 when unset: xray uses 10s).
func rewriteClassicInterval(raw json.RawMessage, d time.Duration) (json.RawMessage, time.Duration, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, 0, fmt.Errorf("xray splice: observatory is not an object: %w", err)
	}
	var was time.Duration
	if cur, ok := obj["probeInterval"]; ok {
		p, err := ParseXrayDuration(cur)
		if err != nil {
			return nil, 0, fmt.Errorf("xray splice: observatory.probeInterval: %w", err)
		}
		was = p
	}
	out, _, err := rewriteObjectField(raw, "probeInterval", durationJSON(d))
	return out, was, err
}

// ProviderProbeInterval reads the probe interval a provider document asks for,
// without modifying anything. ok is false when the document has no
// observatory at all.
func ProviderProbeInterval(providerRaw []byte) (d time.Duration, sampling int, ok bool) {
	var doc struct {
		Burst *struct {
			PingConfig struct {
				Interval json.RawMessage `json:"interval"`
				Sampling int             `json:"sampling"`
			} `json:"pingConfig"`
		} `json:"burstObservatory"`
		Classic *struct {
			ProbeInterval json.RawMessage `json:"probeInterval"`
		} `json:"observatory"`
	}
	if err := json.Unmarshal(providerRaw, &doc); err != nil {
		return 0, 0, false
	}
	switch {
	case doc.Burst != nil:
		d = time.Minute // xray's default when unset
		if len(doc.Burst.PingConfig.Interval) > 0 {
			if p, err := ParseXrayDuration(doc.Burst.PingConfig.Interval); err == nil {
				d = p
			}
		}
		sampling = doc.Burst.PingConfig.Sampling
		if sampling <= 0 {
			sampling = 10 // xray's default
		}
		return d, sampling, true
	case doc.Classic != nil:
		d = 10 * time.Second
		if len(doc.Classic.ProbeInterval) > 0 {
			if p, err := ParseXrayDuration(doc.Classic.ProbeInterval); err == nil {
				d = p
			}
		}
		return d, 1, true
	}
	return 0, 0, false
}
