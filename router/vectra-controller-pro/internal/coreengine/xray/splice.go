package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"vectra-controller-pro/internal/config"
)

// InboundsKey is the single top-level key the splice is allowed to rewrite.
const InboundsKey = "inbounds"

// SpliceResult reports what the splice did, for operator-visible job results.
type SpliceResult struct {
	// TopLevelKeys is the key order of the provider document, preserved.
	TopLevelKeys []string
	// DroppedInbounds lists the provider inbounds that were removed
	// ("<protocol>:<tag>"), e.g. socks:socks, http:http.
	DroppedInbounds []string
	// InboundsReplaced is false when the provider document had no "inbounds"
	// key at all (the tproxy inbound is then appended).
	InboundsReplaced bool
	// OutboundsMarked counts the dialling outbounds that CARRY sockopt.mark so
	// the nft output chain can tell xray's own egress apart from client traffic
	// (whether the splice stamped it or the provider already had the exact
	// right value — any other value is refused outright, see injectOutboundMark).
	// Zero here on a router means the egress will loop back into TPROXY.
	OutboundsMarked int
	Bytes           int
	// Services is what became of the owner's per-service countries.
	Services ServicesResult
	// RussiaDirectRules counts the provider's Russian BL-RU rules sent to its
	// freedom outbound (SpliceOptions.RussiaDirect).
	RussiaDirectRules int

	// APIListen / MetricsListen are where the spliced document serves xray's
	// API and metrics ("" = not installed).
	APIListen     string
	MetricsListen string
	// ProviderReplaced lists provider top-level keys the options replaced
	// outright ("api", "metrics") — the provider ships neither today.
	ProviderReplaced []string
	// ProviderProbeInterval is the observatory interval the provider asked for
	// (0 = it had no observatory, or the option was not used), and
	// ProbeInterval the one the spliced document runs with.
	ProviderProbeInterval time.Duration
	ProbeInterval         time.Duration
	// UserRules is what the owner's sites became (zero without any).
	UserRules UserRulesResult
	// DNS is what DNS through the tunnel became (zero when not asked for).
	DNS DNSResult
	// ExitProbe are the exits the probe inbound reaches (exit_check.go), in
	// its accounts' order; nil without the probe.
	ExitProbe []string
	// LeftOut are the unfit exits that left at least one balancer.
	LeftOut []string
}

// SpliceInbounds returns the provider document with its "inbounds" array
// replaced by exactly one controller-owned TPROXY inbound.
//
// Every other top-level key is re-emitted BYTE-FOR-BYTE from the provider's
// own bytes, in the provider's own order. Nothing is parsed into a Go struct
// and re-serialized, so empty objects stay empty objects (see doc.go).
//
// The provider's socks:10808 / http:10809 inbounds are DROPPED rather than
// rewritten. They carry no "listen" key, so Xray would bind them to 0.0.0.0 —
// an unauthenticated open proxy reachable from the whole LAN. Dropping is
// simpler than rewriting `listen` to 127.0.0.1 and removes the failure mode
// entirely; nothing on the router consumes them (the data plane is TPROXY).
func SpliceInbounds(providerRaw []byte, t *config.TproxyInbound) ([]byte, SpliceResult, error) {
	return Splice(providerRaw, t, SpliceOptions{})
}

// Splice is SpliceInbounds plus the router-side options: xray's API and
// metrics on loopback, and an observatory probe interval. Each option touches
// only its own key — "api", "metrics", or the one interval field inside
// "burstObservatory"/"observatory" — and every other byte is re-emitted exactly
// as the provider wrote it. The owner's sites (opts.Rules) add rules at the
// top of routing.rules and, when the provider has no plain freedom outbound,
// one appended outbound (user_rules.go).
func Splice(providerRaw []byte, t *config.TproxyInbound, opts SpliceOptions) ([]byte, SpliceResult, error) {
	var res SpliceResult
	if err := opts.validate(); err != nil {
		return nil, res, err
	}
	if err := checkKeyFolding(providerRaw); err != nil {
		return nil, res, err
	}
	res.APIListen = opts.APIListen
	res.MetricsListen = opts.MetricsListen
	seenAPI, seenMetrics, seenLog, seenRouting := false, false, false, false

	var plan rulesPlan
	if !opts.Rules.empty() {
		p, err := planUserRules(providerRaw, opts.Rules, inboundTagOf(t))
		if err != nil {
			return nil, res, err
		}
		plan = p
	}
	sp, err := planServices(providerRaw, opts.Services, inboundTagOf(t))
	if err != nil {
		return nil, res, err
	}
	if err := addConnectServices(&sp, providerRaw, opts.ServiceEntries, inboundTagOf(t)); err != nil {
		return nil, res, err
	}
	res.Services = sp.res
	ep, err := planExitProbe(providerRaw, opts.ExitProbeListen)
	if err != nil {
		return nil, res, err
	}
	res.ExitProbe = ep.exits
	tp := t
	if (len(plan.rules) > 0 || !sp.empty()) && t != nil {
		if s, changed := sniffingForRules(t.Sniffing); changed {
			c := *t
			c.Sniffing = s
			tp = &c
			plan.res.SniffingChanged = true
		}
	}
	res.UserRules = plan.res

	var dp dnsPlan
	if opts.DNS.enabled() {
		p, err := planDNS(providerRaw, opts.DNS)
		if err != nil {
			return nil, res, err
		}
		dp = p
		res.DNS = dp.res
	}
	// Every name a rule proxies answered with a FakeDNS address (FakeDNS).
	fakeOn := false
	if opts.FakeDNS && dp.on && !hasTopLevelKey(providerRaw, FakeDNSKey) {
		ruTag := ""
		if opts.RussiaDirect {
			ruTag = directOutboundTag(providerRaw)
		}
		names, err := proxiedNames(providerRaw, ruTag, plan.rules, sp.rules, plan.addDirect, inboundTagOf(t))
		if err != nil {
			return nil, res, err
		}
		if len(names) > 0 && t != nil {
			dp.servers = append(dp.servers, fakeDNSServer(names))
			fakeOn = true
			dp.res.FakeDomains = len(names)
			res.DNS = dp.res
			c := *tp
			c.Sniffing = sniffingWithFakeDNS(tp.Sniffing)
			tp = &c
		}
	}
	// The DNS rule first: it is for the DNS inbound alone, and the owner's
	// rules, for the tproxy inbound alone, follow it (checkSpliced).
	var rules []json.RawMessage
	if dp.on {
		rules = append(rules, dp.rule)
	}
	// The exit probe's rules next — for its inbound alone, and above every
	// rule that names no inbound, so nothing takes a probe elsewhere.
	rules = append(rules, ep.rules...)
	rules = append(rules, plan.rules...)
	// The loopbacks back into the services' own paths before the services'
	// rules: traffic sent back must never meet its service's rule again.
	rules = append(rules, sp.backRules...)
	rules = append(rules, sp.rules...)
	seenDNS, seenPolicy := false, false
	ruTag := ""
	if opts.RussiaDirect {
		ruTag = directOutboundTag(providerRaw)
	}

	inbound, err := TproxyInboundJSON(tp)
	if err != nil {
		return nil, res, err
	}

	dec := json.NewDecoder(bytes.NewReader(providerRaw))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return nil, res, fmt.Errorf("xray splice: read provider document: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, res, fmt.Errorf("xray splice: provider document must be a JSON object, got %v", tok)
	}

	var out bytes.Buffer
	out.WriteByte('{')
	first := true

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, res, fmt.Errorf("xray splice: read key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, res, fmt.Errorf("xray splice: non-string object key %v", keyTok)
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, res, fmt.Errorf("xray splice: read value for %q: %w", key, err)
		}
		res.TopLevelKeys = append(res.TopLevelKeys, key)

		if !first {
			out.WriteByte(',')
		}
		first = false
		writeJSONString(&out, key)
		out.WriteByte(':')

		if key == InboundsKey {
			dropped, err := describeInbounds(raw)
			if err != nil {
				return nil, res, err
			}
			res.DroppedInbounds = dropped
			res.InboundsReplaced = true
			out.WriteByte('[')
			out.Write(inbound)
			if dp.on {
				out.WriteByte(',')
				out.Write(dp.inbound)
			}
			if ep.inbound != nil {
				out.WriteByte(',')
				out.Write(ep.inbound)
			}
			out.WriteByte(']')
			continue
		}
		if key == OutboundsKey {
			if plan.addDirect {
				// Appended BEFORE the marking below, so it is stamped like every
				// other dialling outbound; and last, so the first outbound —
				// xray's default — stays the provider's.
				if raw, err = appendToArray(raw, directOutboundJSON()); err != nil {
					return nil, res, err
				}
			}
			if dp.on {
				// Last, like the direct one: the first outbound stays xray's
				// default.
				if raw, err = appendToArray(raw, dnsOutboundJSON(dp.level)); err != nil {
					return nil, res, err
				}
			}
			for _, lb := range sp.loopbacks {
				if raw, err = appendToArray(raw, lb); err != nil {
					return nil, res, err
				}
			}
			// config.DefaultXraySockMark, NOT t.FwMark — see the constant's doc:
			// marking egress with the tproxy fwmark makes it unroutable and
			// loops it back into TPROXY.
			marked, touched, err := injectOutboundMark(raw, config.DefaultXraySockMark)
			if err != nil {
				return nil, res, err
			}
			res.OutboundsMarked = touched
			out.Write(marked)
			continue
		}
		if (len(rules) > 0 || ruTag != "" || !sp.empty() || len(opts.LeaveOut) > 0) && foldKey(key) == foldKey(RoutingKey) {
			// Matched as xray matches it: the provider's rules must not end up
			// under a second, differently spelled "routing" that wins.
			seenRouting = true
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
			if !sp.empty() {
				rewritten, err := appendBalancers(routing, sp.balancers)
				if err != nil {
					return nil, res, err
				}
				routing = rewritten
			}
			// Last, so the services' balancers lose an unfit exit too.
			if len(opts.LeaveOut) > 0 {
				rewritten, gone, err := leaveOut(routing, documentTags(providerRaw), opts.LeaveOut)
				if err != nil {
					return nil, res, err
				}
				routing, res.LeftOut = rewritten, gone
			}
			out.Write(routing)
			continue
		}
		if len(sp.observe) > 0 && (key == BurstObservatoryKey || key == ObservatoryKey) {
			rewritten, err := observeTags(nullAsObject(raw), sp.observe)
			if err != nil {
				return nil, res, err
			}
			raw = rewritten
		}
		if dp.on && foldKey(key) == foldKey(PolicyKey) {
			seenPolicy = true
			rewritten, err := withDNSLevel(nullAsObject(raw), dp.level)
			if err != nil {
				return nil, res, err
			}
			out.Write(rewritten)
			continue
		}
		if dp.on && foldKey(key) == foldKey(DNSKey) {
			seenDNS = true
			rewritten, err := prependDNSServers(nullAsObject(raw), dp.servers)
			if err != nil {
				return nil, res, err
			}
			if opts.DNS.IPv4Only {
				// The field found as xray finds it.
				field := "queryStrategy"
				var obj map[string]json.RawMessage
				if json.Unmarshal(rewritten, &obj) == nil {
					for k := range obj {
						if foldKey(k) == foldKey(field) {
							field = k
						}
					}
				}
				if rewritten, _, err = rewriteObjectField(rewritten, field, json.RawMessage(`"UseIPv4"`)); err != nil {
					return nil, res, err
				}
			}
			out.Write(rewritten)
			continue
		}
		switch {
		case key == APIKey && opts.APIListen != "":
			seenAPI = true
			res.ProviderReplaced = append(res.ProviderReplaced, key)
			out.Write(apiObject(opts.APIListen))
			continue
		case key == MetricsKey && opts.MetricsListen != "":
			seenMetrics = true
			res.ProviderReplaced = append(res.ProviderReplaced, key)
			out.Write(metricsObject(opts.MetricsListen))
			continue
		case key == BurstObservatoryKey && opts.ProbeInterval > 0:
			rewritten, was, err := rewriteBurstInterval(nullAsObject(raw), opts.ProbeInterval)
			if err != nil {
				return nil, res, err
			}
			if was == 0 {
				was = time.Minute // xray's default for an unset interval
			}
			res.ProviderProbeInterval, res.ProbeInterval = was, opts.ProbeInterval
			out.Write(rewritten)
			continue
		case key == LogKey && opts.NoAccessLog:
			seenLog = true
			rewritten, _, err := rewriteObjectField(nullAsObject(raw), "access", json.RawMessage(`"none"`))
			if err != nil {
				return nil, res, fmt.Errorf("xray splice: log: %w", err)
			}
			out.Write(rewritten)
			continue
		case key == ObservatoryKey && opts.ProbeInterval > 0:
			rewritten, was, err := rewriteClassicInterval(nullAsObject(raw), opts.ProbeInterval)
			if err != nil {
				return nil, res, err
			}
			if was == 0 {
				was = 10 * time.Second
			}
			res.ProviderProbeInterval, res.ProbeInterval = was, opts.ProbeInterval
			out.Write(rewritten)
			continue
		}
		// Verbatim: the provider's own bytes, untouched.
		out.Write(raw)
	}

	if tok, err := dec.Token(); err != nil {
		return nil, res, fmt.Errorf("xray splice: read closing brace: %w", err)
	} else if d, ok := tok.(json.Delim); !ok || d != '}' {
		return nil, res, fmt.Errorf("xray splice: unexpected trailing token %v", tok)
	}
	// Reject trailing garbage after the object — a truncated/concatenated
	// payload must not be adopted.
	if _, err := dec.Token(); err != io.EOF {
		return nil, res, fmt.Errorf("xray splice: trailing data after provider document")
	}

	if !res.InboundsReplaced {
		// No "inbounds" key at all: append ours so the document is runnable.
		if !first {
			out.WriteByte(',')
		}
		writeJSONString(&out, InboundsKey)
		out.WriteString(":[")
		out.Write(inbound)
		if dp.on {
			out.WriteByte(',')
			out.Write(dp.inbound)
		}
		if ep.inbound != nil {
			out.WriteByte(',')
			out.Write(ep.inbound)
		}
		out.WriteByte(']')
	}
	// Absent from the provider (today: always) — appended, so the provider's
	// own keys keep their positions.
	if opts.APIListen != "" && !seenAPI {
		out.WriteByte(',')
		writeJSONString(&out, APIKey)
		out.WriteByte(':')
		out.Write(apiObject(opts.APIListen))
	}
	if opts.MetricsListen != "" && !seenMetrics {
		out.WriteByte(',')
		writeJSONString(&out, MetricsKey)
		out.WriteByte(':')
		out.Write(metricsObject(opts.MetricsListen))
	}
	if opts.NoAccessLog && !seenLog {
		out.WriteByte(',')
		writeJSONString(&out, LogKey)
		out.WriteString(`:{"access":"none"}`)
	}
	if dp.on && !seenDNS {
		out.WriteByte(',')
		writeJSONString(&out, DNSKey)
		out.WriteByte(':')
		out.Write(addedDNSObject(dp.servers, opts.DNS.IPv4Only))
	}
	if dp.on && !seenPolicy {
		policy, err := withDNSLevel(json.RawMessage("{}"), dp.level)
		if err != nil {
			return nil, res, err
		}
		out.WriteByte(',')
		writeJSONString(&out, PolicyKey)
		out.WriteByte(':')
		out.Write(policy)
	}
	if fakeOn {
		out.WriteByte(',')
		writeJSONString(&out, FakeDNSKey)
		out.WriteByte(':')
		out.Write(fakeDNSObject())
	}
	if len(rules) > 0 && !seenRouting {
		rewritten, err := insertRules(json.RawMessage("{}"), rules)
		if err != nil {
			return nil, res, err
		}
		out.WriteByte(',')
		writeJSONString(&out, RoutingKey)
		out.WriteByte(':')
		out.Write(rewritten)
	}
	out.WriteByte('}')

	spliced := out.Bytes()
	if !json.Valid(spliced) {
		return nil, res, fmt.Errorf("xray splice: produced invalid JSON (refusing)")
	}
	if err := checkSpliced(spliced, t, opts, plan, dp, ep); err != nil {
		return nil, res, err
	}
	res.Bytes = len(spliced)
	return spliced, res, nil
}

// describeInbounds lists the provider inbounds being dropped, for the audit
// trail. It never rewrites them.
func describeInbounds(raw json.RawMessage) ([]string, error) {
	var arr []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
		Listen   string `json:"listen"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("xray splice: provider inbounds is not an array of objects: %w", err)
	}
	out := make([]string, 0, len(arr))
	for _, ib := range arr {
		d := ib.Protocol + ":" + ib.Tag
		if ib.Listen != "" {
			// Worth surfacing: today the provider sets no `listen` at all, which
			// is precisely why these inbounds are dropped. If that ever changes,
			// the operator should see it in the job result.
			d += "@" + ib.Listen
		}
		out = append(out, d)
	}
	return out, nil
}

// writeJSONString emits a JSON string without Go's HTML escaping, so a key is
// re-emitted the way the provider wrote it.
func writeJSONString(w *bytes.Buffer, s string) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encoding a string never fails.
	_ = enc.Encode(s)
	w.Write(bytes.TrimRight(buf.Bytes(), "\n"))
}

// nullAsObject lets a key xray accepts as null ("log": null) be rewritten like
// an empty object.
func nullAsObject(raw json.RawMessage) json.RawMessage {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return json.RawMessage("{}")
	}
	return raw
}

// checkSpliced decodes the result the way xray will — encoding/json, case-
// insensitive, last key wins — and confirms the router's additions are what
// xray will actually run: exactly one inbound (the tproxy one), the API and
// metrics on the requested loopback addresses, the owner's sites first in the
// routing and for the tproxy inbound only, and an added direct outbound
// marked and not first. checkKeyFolding makes this hold by construction; this
// proves it on the bytes that get installed.
func checkSpliced(spliced []byte, t *config.TproxyInbound, opts SpliceOptions, plan rulesPlan, dp dnsPlan, ep exitProbe) error {
	var got struct {
		API *struct {
			Listen string `json:"listen"`
		} `json:"api"`
		Metrics *struct {
			Listen string `json:"listen"`
		} `json:"metrics"`
		Inbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
			Listen   string `json:"listen"`
			Port     int    `json:"port"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(spliced, &got); err != nil {
		return fmt.Errorf("xray splice: re-read the result: %w", err)
	}
	want := 1
	if dp.on {
		want = 2
	}
	if ep.inbound != nil {
		want++
	}
	if len(got.Inbounds) != want || (t != nil && t.Tag != "" && got.Inbounds[0].Tag != t.Tag) {
		return fmt.Errorf("xray splice: the result would run %d inbound(s), not the tproxy one alone — refusing", len(got.Inbounds))
	}
	if ep.inbound != nil {
		last := got.Inbounds[len(got.Inbounds)-1]
		if last.Tag != ExitProbeTag || last.Protocol != "http" || net.JoinHostPort(last.Listen, strconv.Itoa(last.Port)) != opts.ExitProbeListen {
			return fmt.Errorf("xray splice: the result's last inbound is not the exit probe on %s — refusing", opts.ExitProbeListen)
		}
	}
	if dp.on {
		if got.Inbounds[1].Tag != DNSInboundTag {
			return fmt.Errorf("xray splice: the result's second inbound is not the DNS one — refusing")
		}
		if port, ok := RenderDNSListen(spliced); !ok || strconv.Itoa(port) != portOf(opts.DNS.Listen) {
			return fmt.Errorf("xray splice: the result's DNS inbound does not listen on %s — refusing", opts.DNS.Listen)
		}
		if err := checkDNSLevel(spliced, dp.level); err != nil {
			return err
		}
	}
	if opts.APIListen != "" && (got.API == nil || got.API.Listen != opts.APIListen) {
		return fmt.Errorf("xray splice: the result's api does not listen on %s — refusing", opts.APIListen)
	}
	if opts.MetricsListen != "" && (got.Metrics == nil || got.Metrics.Listen != opts.MetricsListen) {
		return fmt.Errorf("xray splice: the result's metrics does not listen on %s — refusing", opts.MetricsListen)
	}
	if len(plan.rules) == 0 && !plan.addDirect && !dp.on && len(ep.rules) == 0 {
		// Nothing of the owner's and no DNS: nothing more is read, and no
		// document is refused that was accepted before either existed.
		return nil
	}
	var added struct {
		Outbounds []json.RawMessage `json:"outbounds"`
		Routing   *struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(spliced, &added); err != nil {
		return fmt.Errorf("xray splice: re-read the owner's sites in the result: %w", err)
	}
	offset := 0
	if dp.on {
		var r struct {
			InboundTag  []string `json:"inboundTag"`
			OutboundTag string   `json:"outboundTag"`
		}
		if added.Routing == nil || len(added.Routing.Rules) == 0 ||
			json.Unmarshal(added.Routing.Rules[0], &r) != nil ||
			len(r.InboundTag) != 1 || r.InboundTag[0] != DNSInboundTag || r.OutboundTag != DNSOutboundTag {
			return fmt.Errorf("xray splice: the result's first routing rule does not send the DNS inbound to the DNS outbound — refusing")
		}
		dnsOut := false
		for i, raw := range added.Outbounds {
			o := readOutbound(raw)
			if o.Tag == DNSOutboundTag {
				dnsOut = i > 0 && o.protocol() == "dns"
			}
		}
		if !dnsOut {
			return fmt.Errorf("xray splice: the added %s is not a dns outbound after the provider's — refusing", DNSOutboundTag)
		}
		offset = 1
	}
	// The exit probe's rules: its inbound alone, one account each, to that
	// account's exit.
	for i, tag := range ep.exits {
		var r struct {
			InboundTag  []string `json:"inboundTag"`
			User        []string `json:"user"`
			OutboundTag string   `json:"outboundTag"`
		}
		if added.Routing == nil || len(added.Routing.Rules) <= offset+i || json.Unmarshal(added.Routing.Rules[offset+i], &r) != nil ||
			len(r.InboundTag) != 1 || r.InboundTag[0] != ExitProbeTag || len(r.User) != 1 || r.User[0] != ExitProbeUser(tag) || r.OutboundTag != tag {
			return fmt.Errorf("xray splice: routing rule %d does not send the exit probe's account for %s to it — refusing", offset+i, tag)
		}
	}
	offset += len(ep.rules)
	if n := len(plan.rules); n > 0 {
		if added.Routing == nil || len(added.Routing.Rules) < n+offset {
			return fmt.Errorf("xray splice: the owner's %d rule(s) are not in the result's routing — refusing", n)
		}
		for i := 0; i < n; i++ {
			var r struct {
				InboundTag []string `json:"inboundTag"`
			}
			if json.Unmarshal(added.Routing.Rules[i+offset], &r) != nil || len(r.InboundTag) != 1 || r.InboundTag[0] != inboundTagOf(t) {
				return fmt.Errorf("xray splice: routing rule %d is not the owner's, for the tproxy inbound only — refusing", i+offset)
			}
		}
	}
	if plan.addDirect {
		ok := false
		for i, raw := range added.Outbounds {
			var o struct {
				Tag            string `json:"tag"`
				Protocol       string `json:"protocol"`
				StreamSettings struct {
					Sockopt struct {
						Mark int `json:"mark"`
					} `json:"sockopt"`
				} `json:"streamSettings"`
			}
			if json.Unmarshal(raw, &o) != nil || o.Tag != DirectTag {
				continue
			}
			ok = i > 0 && o.Protocol == "freedom" && o.StreamSettings.Sockopt.Mark == config.DefaultXraySockMark
		}
		if !ok {
			return fmt.Errorf("xray splice: the added %s is not a marked freedom after the provider's outbounds — refusing", DirectTag)
		}
	}
	return nil
}

// DNSKey is the top-level key DNS through the tunnel adds its servers to.
const DNSKey = "dns"

// PolicyKey is xray's session policy; the DNS level is added to it
// (dnsLevelPolicy).
const PolicyKey = "policy"

// portOf is the port of a host:port, "" when there is none.
func portOf(hostport string) string {
	_, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return ""
	}
	return port
}
