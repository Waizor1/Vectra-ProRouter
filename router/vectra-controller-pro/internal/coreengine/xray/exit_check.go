package xray

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"vectra-controller-pro/internal/xrayview"
)

// The exits the router checks itself. The provider's observatory asks every
// node one neutral URL (cp.cloudflare.com/generate_204). An exit whose leg
// lets Russia's filter see the inner handshake passes it and fails every
// blocked site — 1111, 2026-09-30: through bridge-us5 Instagram, Facebook, X,
// LinkedIn and Discord ended in a failed handshake while Google answered, and
// leastLoad kept handing it connections. The router asks each foreign exit
// for blocked sites itself, through a loopback HTTP proxy with one account
// per exit (internal/exitcheck), and leaves an exit that fails them all out
// of every balancer until it passes again.

const (
	// ExitProbeTag is the probe's inbound.
	ExitProbeTag = "vctl-exit-probe"
	// DefaultExitProbeListen is its address: loopback, beside the API
	// (10085), the metrics (10086) and the DNS inbound (10053).
	DefaultExitProbeListen = "127.0.0.1:10087"
	// ExitProbePass is every probe account's password. The inbound is on
	// loopback and the account only picks the exit; it guards nothing.
	ExitProbePass = "vctl"
)

// ExitProbeUser is the probe account that reaches an exit: a hash of its
// tag, never the tag — xray splits a user at its first colon, and a tag may
// hold anything.
func ExitProbeUser(tag string) string {
	sum := sha256.Sum256([]byte(tag))
	return "vctl-exit-" + hex.EncodeToString(sum[:6])
}

// ExitsToCheck are the dialling members of a document's balancers whose
// country is abroad, sorted. The Russian and Belarusian ones carry what must
// look local — blocked sites fail through them by design — and a whitelist
// level names no country.
func ExitsToCheck(providerRaw []byte) []string {
	v, err := xrayview.Parse(providerRaw)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, b := range v.Balancers {
		for _, m := range b.Members {
			o := v.Outbound(m)
			if seen[m] || o == nil || !o.Dials {
				continue
			}
			switch xrayview.CountryHint(m) {
			case "", "RU", "BY":
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Strings(out)
	return out
}

// exitProbe is the inbound and the rules that reach each exit on its own.
type exitProbe struct {
	exits   []string
	inbound json.RawMessage
	rules   []json.RawMessage
}

func planExitProbe(providerRaw []byte, listen string) (exitProbe, error) {
	var p exitProbe
	if listen == "" {
		return p, nil
	}
	p.exits = ExitsToCheck(providerRaw)
	if len(p.exits) == 0 {
		return p, nil
	}
	host, port, err := loopbackListen("exit probe", listen)
	if err != nil {
		return p, err
	}
	type account struct {
		User string `json:"user"`
		Pass string `json:"pass"`
	}
	accounts := make([]account, 0, len(p.exits))
	for _, tag := range p.exits {
		accounts = append(accounts, account{User: ExitProbeUser(tag), Pass: ExitProbePass})
		p.rules = append(p.rules, marshalNoEscape(map[string]any{
			"type":        "field",
			"inboundTag":  []string{ExitProbeTag},
			"user":        []string{ExitProbeUser(tag)},
			"outboundTag": tag,
		}))
	}
	p.inbound = marshalNoEscape(map[string]any{
		"tag":      ExitProbeTag,
		"listen":   host,
		"port":     port,
		"protocol": "http",
		"settings": map[string]any{"accounts": accounts, "allowTransparent": false},
	})
	return p, nil
}

// leaveOut takes the unfit exits out of every balancer's selector. A
// selector entry is a prefix, so an entry that also picks an unfit exit is
// spelled out as the tags it picks besides; a balancer whose new selector
// would pick anything but its old members less the unfit ones — a kept tag
// that is itself a prefix of an unfit one — keeps its selector, and so does
// one the unfit exits would empty: xray refuses an empty selector, and a bad
// path the observatory still judges beats none. It returns the exits that
// left at least one balancer, sorted.
func leaveOut(routing json.RawMessage, tags, unfit []string) (json.RawMessage, []string, error) {
	if len(unfit) == 0 {
		return routing, nil, nil
	}
	bad := map[string]bool{}
	for _, u := range unfit {
		bad[u] = true
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(routing, &obj); err != nil {
		return nil, nil, fmt.Errorf("xray splice: routing is not an object: %w", err)
	}
	field := ""
	var balancers []json.RawMessage
	for k, v := range obj {
		if foldKey(k) != foldKey("balancers") || isJSONNull(v) {
			continue
		}
		field = k
		if err := json.Unmarshal(v, &balancers); err != nil {
			return nil, nil, fmt.Errorf("xray splice: routing.balancers is not an array: %w", err)
		}
	}
	if field == "" {
		return routing, nil, nil
	}
	left := map[string]bool{}
	changed := false
	for i, raw := range balancers {
		var b map[string]json.RawMessage
		if err := json.Unmarshal(raw, &b); err != nil {
			return nil, nil, fmt.Errorf("xray splice: a balancer is not an object: %w", err)
		}
		selField := ""
		var sel []string
		for k, v := range b {
			if foldKey(k) == foldKey("selector") {
				selField = k
				_ = json.Unmarshal(v, &sel)
			}
		}
		members := pickedBy(sel, tags)
		var keep, drop []string
		for _, m := range members {
			if bad[m] {
				drop = append(drop, m)
			} else {
				keep = append(keep, m)
			}
		}
		if len(drop) == 0 || len(keep) == 0 {
			continue
		}
		kept := map[string]bool{}
		for _, k := range keep {
			kept[k] = true
		}
		var next []string
		have := map[string]bool{}
		add := func(s string) {
			if !have[s] {
				have[s] = true
				next = append(next, s)
			}
		}
		for _, s := range sel {
			picks := pickedBy([]string{s}, tags)
			clean := true
			for _, p := range picks {
				if bad[p] {
					clean = false
				}
			}
			if clean {
				add(s)
				continue
			}
			for _, p := range picks {
				if kept[p] {
					add(p)
				}
			}
		}
		if !sameSet(pickedBy(next, tags), keep) {
			continue
		}
		rewritten, _, err := rewriteObjectField(raw, selField, marshalNoEscape(next))
		if err != nil {
			return nil, nil, err
		}
		balancers[i] = rewritten
		changed = true
		for _, d := range drop {
			left[d] = true
		}
	}
	if !changed {
		return routing, nil, nil
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, r := range balancers {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(r)
	}
	buf.WriteByte(']')
	out, _, err := rewriteObjectField(routing, field, buf.Bytes())
	if err != nil {
		return nil, nil, err
	}
	var gone []string
	for d := range left {
		gone = append(gone, d)
	}
	sort.Strings(gone)
	return out, gone, nil
}

// pickedBy is what a selector picks, as xray picks it: every tag with one of
// its entries as a prefix, in the tags' order.
func pickedBy(sel, tags []string) []string {
	var out []string
	for _, t := range tags {
		for _, s := range sel {
			if strings.HasPrefix(t, s) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	in := map[string]bool{}
	for _, x := range a {
		in[x] = true
	}
	for _, x := range b {
		if !in[x] {
			return false
		}
	}
	return true
}

// loopbackListen splits an IPv4 loopback host:port; nothing else is
// accepted — the probe is an HTTP proxy onto every exit.
func loopbackListen(what, listen string) (string, int, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", 0, fmt.Errorf("xray splice: %s listen %q: %w", what, listen, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() || ip.To4() == nil {
		return "", 0, fmt.Errorf("xray splice: %s listen %q is not an IPv4 loopback address; refusing to expose it", what, listen)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", 0, fmt.Errorf("xray splice: %s listen %q has no usable port", what, listen)
	}
	return host, n, nil
}

// documentTags are the outbound tags of a document, in order.
func documentTags(raw []byte) []string {
	v, err := xrayview.Parse(raw)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(v.Outbounds))
	for _, o := range v.Outbounds {
		out = append(out, o.Tag)
	}
	return out
}

// Leavable are the unfit exits a render of providerRaw, with these service
// choices, takes out of at least one balancer — the services' included —
// sorted. An exit every balancer must keep (it is the last of one) moves
// nothing: asking for it would only change the splice key and restart xray
// on the same config.
func Leavable(providerRaw []byte, services map[string]string, unfit []string) []string {
	if len(unfit) == 0 {
		return nil
	}
	routing := json.RawMessage("{}")
	var top map[string]json.RawMessage
	if json.Unmarshal(providerRaw, &top) == nil {
		for k, v := range top {
			if foldKey(k) == foldKey(RoutingKey) && !isJSONNull(v) {
				routing = v
			}
		}
	}
	if sp, err := planServices(providerRaw, services, ""); err == nil && !sp.empty() {
		if r, err := appendBalancers(routing, sp.balancers); err == nil {
			routing = r
		}
	}
	_, gone, err := leaveOut(routing, documentTags(providerRaw), unfit)
	if err != nil {
		// Cannot tell: as asked; the render decides.
		out := append([]string(nil), unfit...)
		sort.Strings(out)
		return out
	}
	return gone
}
