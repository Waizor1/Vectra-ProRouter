// Package xrayview reads a rendered xray config into what the router UI shows
// and what the daemon validates a balancer pin against: nodes, balancers,
// their members and fallback chains, the traffic each one carries, and where
// the API and metrics listen.
//
// It extracts shape, never secrets. A node is reduced to its tag, protocol,
// transport, security, address and port; ids, keys, passwords, short ids and
// paths are not read into any field, so nothing built from a View can leak
// them.
package xrayview

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Outbound is one outbound, credentials stripped.
type Outbound struct {
	Tag       string
	Protocol  string
	Transport string
	Security  string
	Address   string
	Port      int
	// Dials is false for outbounds that never open a connection towards a
	// server: freedom (direct), blackhole, loopback, dns.
	Dials bool
	// LoopbackInbound is a loopback outbound's settings.inboundTag: traffic
	// sent to it re-enters routing as if it had arrived on that inbound.
	LoopbackInbound string
}

// Matcher summarises one routing rule that sends traffic to a balancer.
type Matcher struct {
	Kind    string   // domain | ip | all | other
	Network string   // "" when the rule does not restrict it
	Sample  []string // up to SampleSize raw matchers
	Total   int
}

// SampleSize is how many raw matchers a Matcher keeps.
const SampleSize = 6

// Balancer is one routing balancer, resolved.
type Balancer struct {
	Tag         string
	Selector    []string
	Strategy    string
	Expected    *int
	FallbackTag string
	// FallbackBalancer is the balancer FallbackTag leads to through a
	// loopback outbound, "" when FallbackTag is a plain outbound (or unset).
	FallbackBalancer string
	// Members are the outbound tags the selector matches, as xray resolves
	// them: a PREFIX match over every tagged outbound, sorted.
	Members []string
	Rules   []Matcher
	Role    string // main | routed | reserve | unused
}

// View is a parsed config.
type View struct {
	Outbounds []Outbound
	Balancers []Balancer
	// Default is the first outbound, tagged or not: xray's default handler.
	// A balancer with neither a candidate nor a fallbackTag hands its traffic
	// there. nil when the config has no outbounds.
	Default *Outbound

	APIListen     string
	MetricsListen string

	HasObservatory   bool
	ProbeInterval    time.Duration
	ProbeSampling    int
	ProbeTimeout     time.Duration
	ProbeDestination string

	byTag map[string]int
}

var nonDialling = map[string]bool{"freedom": true, "blackhole": true, "loopback": true, "dns": true}

// Parse reads a rendered (or provider) xray config.
func Parse(raw []byte) (*View, error) {
	var doc struct {
		API       *struct{ Listen string } `json:"api"`
		Metrics   *struct{ Listen string } `json:"metrics"`
		Outbounds []json.RawMessage        `json:"outbounds"`
		Routing   struct {
			Rules     []map[string]json.RawMessage `json:"rules"`
			Balancers []struct {
				Tag      string   `json:"tag"`
				Selector []string `json:"selector"`
				Strategy *struct {
					Type     string          `json:"type"`
					Settings json.RawMessage `json:"settings"`
				} `json:"strategy"`
				FallbackTag string `json:"fallbackTag"`
			} `json:"balancers"`
		} `json:"routing"`
		Burst *struct {
			PingConfig struct {
				Interval    json.RawMessage `json:"interval"`
				Sampling    int             `json:"sampling"`
				Timeout     json.RawMessage `json:"timeout"`
				Destination string          `json:"destination"`
			} `json:"pingConfig"`
		} `json:"burstObservatory"`
		Classic *struct {
			ProbeURL      string          `json:"probeUrl"`
			ProbeInterval json.RawMessage `json:"probeInterval"`
		} `json:"observatory"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("xrayview: %w", err)
	}
	v := &View{byTag: map[string]int{}}
	if doc.API != nil {
		v.APIListen = doc.API.Listen
	}
	if doc.Metrics != nil {
		v.MetricsListen = doc.Metrics.Listen
	}
	for _, ob := range doc.Outbounds {
		o := parseOutbound(ob)
		if v.Default == nil {
			first := o
			v.Default = &first
		}
		if o.Tag == "" {
			continue
		}
		v.byTag[o.Tag] = len(v.Outbounds)
		v.Outbounds = append(v.Outbounds, o)
	}
	switch {
	case doc.Burst != nil:
		v.HasObservatory = true
		v.ProbeInterval = durationOr(doc.Burst.PingConfig.Interval, time.Minute)
		v.ProbeSampling = doc.Burst.PingConfig.Sampling
		if v.ProbeSampling <= 0 {
			v.ProbeSampling = 10
		}
		v.ProbeTimeout = durationOr(doc.Burst.PingConfig.Timeout, 5*time.Second)
		v.ProbeDestination = doc.Burst.PingConfig.Destination
		if v.ProbeDestination == "" {
			v.ProbeDestination = "https://connectivitycheck.gstatic.com/generate_204"
		}
	case doc.Classic != nil:
		v.HasObservatory = true
		v.ProbeInterval = durationOr(doc.Classic.ProbeInterval, 10*time.Second)
		v.ProbeSampling = 1
		v.ProbeDestination = doc.Classic.ProbeURL
	}

	// Every tag xray can select from: the outbounds, plus the metrics
	// endpoint, which xray registers as an outbound handler.
	allTags := make([]string, 0, len(v.Outbounds)+1)
	for _, o := range v.Outbounds {
		allTags = append(allTags, o.Tag)
	}
	if doc.Metrics != nil {
		allTags = append(allTags, metricsTag(raw))
	}

	// inboundTag -> balancer, for loopback fallbacks.
	inboundBalancer := map[string]string{}
	catchAll := ""
	rulesFor := map[string][]Matcher{}
	for _, r := range doc.Routing.Rules {
		bt := jsonString(r["balancerTag"])
		if bt == "" {
			continue
		}
		if in := jsonStrings(r["inboundTag"]); len(in) > 0 {
			for _, t := range in {
				// xray takes the FIRST rule that matches.
				if _, seen := inboundBalancer[t]; !seen {
					inboundBalancer[t] = bt
				}
			}
			continue
		}
		m := summarizeRule(r)
		if m.Kind == "all" && catchAll == "" {
			catchAll = bt
		}
		rulesFor[bt] = append(rulesFor[bt], m)
	}
	reachedByFallback := map[string]bool{}
	for _, b := range doc.Routing.Balancers {
		nb := Balancer{
			Tag:         b.Tag,
			Selector:    b.Selector,
			Strategy:    "random",
			FallbackTag: b.FallbackTag,
			Members:     selectPrefix(allTags, b.Selector),
			Rules:       rulesFor[b.Tag],
		}
		if b.Strategy != nil && b.Strategy.Type != "" {
			nb.Strategy = b.Strategy.Type
			var s struct {
				Expected *int `json:"expected"`
			}
			if len(b.Strategy.Settings) > 0 && json.Unmarshal(b.Strategy.Settings, &s) == nil {
				nb.Expected = s.Expected
			}
		}
		if i, ok := v.byTag[b.FallbackTag]; ok && v.Outbounds[i].Protocol == "loopback" {
			nb.FallbackBalancer = inboundBalancer[v.Outbounds[i].LoopbackInbound]
			if nb.FallbackBalancer != "" {
				reachedByFallback[nb.FallbackBalancer] = true
			}
		}
		v.Balancers = append(v.Balancers, nb)
	}
	for _, t := range inboundBalancer {
		reachedByFallback[t] = true
	}
	for i := range v.Balancers {
		b := &v.Balancers[i]
		switch {
		case b.Tag == catchAll:
			b.Role = "main"
		case len(b.Rules) > 0:
			b.Role = "routed"
		case reachedByFallback[b.Tag]:
			b.Role = "reserve"
		default:
			b.Role = "unused"
		}
	}
	return v, nil
}

// Outbound returns the outbound with tag, or nil.
func (v *View) Outbound(tag string) *Outbound {
	if v.byTag == nil { // a View built by hand, not by Parse
		for i := range v.Outbounds {
			if v.Outbounds[i].Tag == tag {
				return &v.Outbounds[i]
			}
		}
		return nil
	}
	if i, ok := v.byTag[tag]; ok {
		return &v.Outbounds[i]
	}
	return nil
}

// Balancer returns the balancer with tag, or nil.
func (v *View) Balancer(tag string) *Balancer {
	for i := range v.Balancers {
		if v.Balancers[i].Tag == tag {
			return &v.Balancers[i]
		}
	}
	return nil
}

// CanPin reports whether node may be pinned on balancer: the balancer exists
// and node is one of its members that actually dials out. xray itself accepts
// ANY tag as an override target and routes everything there — a typo, or a
// member of a different balancer, would silently re-route a whole class of
// traffic.
func (v *View) CanPin(balancer, node string) error {
	b := v.Balancer(balancer)
	if b == nil {
		return fmt.Errorf("no balancer %q in the running config", balancer)
	}
	for _, m := range b.Members {
		if m == node {
			if o := v.Outbound(node); o == nil || !o.Dials {
				return fmt.Errorf("%q is not a node that can carry traffic", node)
			}
			return nil
		}
	}
	return fmt.Errorf("%q is not a member of balancer %q", node, balancer)
}

// BalancersOf returns the balancers a node is a member of, in config order.
func (v *View) BalancersOf(node string) []string {
	var out []string
	for _, b := range v.Balancers {
		for _, m := range b.Members {
			if m == node {
				out = append(out, b.Tag)
				break
			}
		}
	}
	return out
}

func parseOutbound(raw json.RawMessage) Outbound {
	var ob struct {
		Tag      string          `json:"tag"`
		Protocol string          `json:"protocol"`
		Settings json.RawMessage `json:"settings"`
		Stream   *struct {
			Network  string `json:"network"`
			Security string `json:"security"`
		} `json:"streamSettings"`
	}
	if json.Unmarshal(raw, &ob) != nil {
		return Outbound{}
	}
	o := Outbound{Tag: ob.Tag, Protocol: ob.Protocol, Transport: "tcp", Security: "none", Dials: !nonDialling[ob.Protocol]}
	if ob.Stream != nil {
		if ob.Stream.Network != "" {
			o.Transport = ob.Stream.Network
		}
		if ob.Stream.Security != "" {
			o.Security = ob.Stream.Security
		}
	}
	if ob.Protocol == "hysteria" {
		o.Transport = "hysteria"
	}
	var st struct {
		Vnext []struct {
			Address string      `json:"address"`
			Port    json.Number `json:"port"`
		} `json:"vnext"`
		Servers []struct {
			Address string      `json:"address"`
			Port    json.Number `json:"port"`
		} `json:"servers"`
		Peers []struct {
			Endpoint string `json:"endpoint"`
		} `json:"peers"`
		Address    string      `json:"address"`
		Port       json.Number `json:"port"`
		InboundTag string      `json:"inboundTag"`
	}
	if len(ob.Settings) > 0 && json.Unmarshal(ob.Settings, &st) == nil {
		switch {
		case len(st.Vnext) > 0:
			o.Address, o.Port = st.Vnext[0].Address, atoi(st.Vnext[0].Port)
		case len(st.Servers) > 0:
			o.Address, o.Port = st.Servers[0].Address, atoi(st.Servers[0].Port)
		case len(st.Peers) > 0:
			if h, p, ok := strings.Cut(st.Peers[0].Endpoint, ":"); ok && !strings.Contains(p, ":") {
				o.Address, o.Port = h, atoi(json.Number(p))
			} else {
				o.Address = st.Peers[0].Endpoint
			}
		case st.Address != "" && o.Dials:
			o.Address, o.Port = st.Address, atoi(st.Port)
		}
		o.LoopbackInbound = st.InboundTag
	}
	if !o.Dials {
		o.Address, o.Port = "", 0
	}
	return o
}

func summarizeRule(r map[string]json.RawMessage) Matcher {
	m := Matcher{Network: jsonString(r["network"])}
	domains, ips := jsonStrings(r["domain"]), jsonStrings(r["ip"])
	vals := append(append([]string{}, domains...), ips...)
	switch {
	case len(domains) > 0:
		m.Kind = "domain"
	case len(ips) > 0:
		m.Kind = "ip"
	default:
		// Conditions other than network narrow the rule; without any, it
		// takes everything that reached it.
		var other []string
		for _, k := range []string{"port", "sourcePort", "protocol", "source", "sourceIP", "user", "attrs", "localPort", "localIP", "vlessRoute"} {
			if v, ok := r[k]; ok {
				other = append(other, k+":"+compact(v))
			}
		}
		if len(other) == 0 {
			m.Kind = "all"
			m.Sample = []string{}
			return m
		}
		m.Kind = "other"
		vals = other
	}
	m.Total = len(vals)
	if len(vals) > SampleSize {
		vals = vals[:SampleSize]
	}
	m.Sample = vals
	return m
}

func selectPrefix(tags, selectors []string) []string {
	var out []string
	for _, t := range tags {
		for _, s := range selectors {
			if strings.HasPrefix(t, s) {
				out = append(out, t)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func metricsTag(raw []byte) string {
	var d struct {
		Metrics struct {
			Tag string `json:"tag"`
		} `json:"metrics"`
	}
	_ = json.Unmarshal(raw, &d)
	if d.Metrics.Tag == "" {
		return "Metrics"
	}
	return d.Metrics.Tag
}

func jsonString(raw json.RawMessage) string {
	var s string
	if len(raw) > 0 && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func jsonStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var ss []string
	if json.Unmarshal(raw, &ss) == nil {
		return ss
	}
	if s := jsonString(raw); s != "" {
		return []string{s}
	}
	return nil
}

func compact(raw json.RawMessage) string {
	if s := jsonString(raw); s != "" {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	out := strings.Join(strings.Fields(string(raw)), "")
	if len(out) > 40 {
		out = out[:40] + "…"
	}
	return out
}

func atoi(n json.Number) int {
	i, err := strconv.Atoi(n.String())
	if err != nil {
		return 0
	}
	return i
}

func durationOr(raw json.RawMessage, def time.Duration) time.Duration {
	if len(raw) == 0 {
		return def
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
		return def
	}
	var f float64
	if json.Unmarshal(raw, &f) == nil {
		return time.Duration(f)
	}
	return def
}

// Loopback reports whether addr ("host:port") is on loopback. The router only
// ever talks to xray's API and metrics there: an address a rendered config
// claims anywhere else is not followed, whatever put it there.
func Loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
