package routepolicy

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sort"
	"strings"
)

// A subscription's refresh, as PassWall2 26.8.10's subscribe.lua does it
// (execute, parse_link, update_node, select_node): the subscription's nodes
// (add_mode 2, its group) replaced by the feed's, under new names, and every
// reference to one of them — the global node, a shunt's slots, a balancer's
// list — pointed at its successor.
//
// Three deliberate differences, each where PassWall's behaviour has cost the
// fleet:
//
//  1. A feed with no usable node changes nothing (ErrEmptyFeed). Nor does the
//     provider's placeholder, "…@0.0.0.0:1#App not supported" or "#Limit of
//     devices reached": PassWall applied it, and every slot lost its node.
//  2. A reference is never re-pointed by its node's bare host. The provider
//     moves exits between its bridge hosts overnight (the port says the
//     exit: NL on :50055 moved from ru17 to ru3), so PassWall's host-only
//     match took a slot to whichever exit now sits on the old host — another
//     country. Nor by host:port when the names say another country.
//  3. A reference whose node is gone from the feed — the host switched off
//     on the panel — moves the way the owner chose (2026-09-29): to the same
//     country by the same path (🇷🇺🇳🇱 to another 🇷🇺🇳🇱), then the same
//     country by another (🇷🇺🇳🇱 to 🇳🇱 direct), then the main slot's node
//     (WorldProxy's), then «Авто Самый стабильный», then the feed's first
//     node. PassWall pointed it at the first node of the list as it was
//     before the refresh — one it had just deleted — so the slot named
//     nothing.

// ErrEmptyFeed: the subscription gave no usable node; nothing was changed.
var ErrEmptyFeed = errors.New("routepolicy: the subscription gave no usable node")

// RefreshReport says what a refresh did — remarks and host:port only, never
// a key or an id of the subscription's.
type RefreshReport struct {
	Subscription string
	Group        string
	Nodes        int
	Skipped      []string
	Rebound      []Rebinding
}

// Rebinding is one reference and where it points now.
type Rebinding struct {
	Ref  string // "global.node", "myshunt.WorldProxy", "bal.balancing_node[0]"
	From string // the old node: remarks (address:port)
	To   string // the new one
	How  string // what matched: "name+address:port", "address:port", "name", "same country, same path", "same country", or the fallback taken
}

// FeedMD5 is the md5 PassWall2 keeps for a feed: of the body without its
// blank lines (sed '/^[ \t]*$/d; /^[ \t]*\r$/d', then md5sum).
func FeedMD5(body []byte) string {
	var b strings.Builder
	s := string(body)
	for s != "" {
		line := s
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			line, s = s[:i+1], s[i+1:]
		} else {
			s = ""
		}
		if strings.Trim(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), " \t") == "" {
			continue
		}
		b.WriteString(line)
	}
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// Refresh applies one subscription's feed body to the sections. newName makes
// a fresh section name (nil: eight random letters and digits, PassWall's
// gen_random_char); it is asked again until the name is free.
func Refresh(secs []Section, subscription string, body []byte, newName func() string) ([]Section, RefreshReport, error) {
	rep := RefreshReport{Subscription: subscription}
	si := -1
	for i, s := range secs {
		if s.Type == "subscribe_list" && s.Name == subscription {
			si = i
		}
	}
	if si < 0 {
		return nil, rep, fmt.Errorf("routepolicy: no subscription %q", subscription)
	}
	sub := secs[si]
	rep.Group = sub.Get("remark")
	// What update_node gives every node of the subscription, where vctl's
	// generator can carry it; what it cannot, the refresh refuses, so the
	// policy stays one vctl can render.
	if chainsNodes(secs, sub) {
		return nil, rep, fmt.Errorf("%w: the subscription chains its nodes (chain_proxy %s)", ErrUnsupported, sub.Get("chain_proxy"))
	}
	if sub.Get("domain_resolver") != "" && (sub.Get("domain_resolver_dns") != "" || sub.Get("domain_resolver_dns_https") != "") {
		return nil, rep, fmt.Errorf("%w: the subscription gives its nodes a DNS resolver of their own", ErrUnsupported)
	}
	o := LinkOptions{Group: rep.Group, AllowInsecure: true}
	if v, ok := sub.Options["allowInsecure"]; ok && v != "1" {
		o.AllowInsecure = false
	}
	filter := keywordFilter(secs, sub)

	var fresh []Section
	for _, line := range strings.Split(strings.ReplaceAll(base64Decode(luaTrim(string(body))), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n, err := ParseLink(line, o)
		if err != nil {
			rep.Skipped = append(rep.Skipped, err.Error())
			continue
		}
		if why := unusable(n, filter); why != "" {
			rep.Skipped = append(rep.Skipped, why+": "+n.Get("remarks"))
			continue
		}
		fresh = append(fresh, n)
	}
	rep.Nodes = len(fresh)
	if len(fresh) == 0 {
		return nil, rep, ErrEmptyFeed
	}

	// What points at a node, and the node as it was — before the
	// subscription's old nodes go.
	byName := map[string]Section{}
	taken := map[string]bool{}
	for _, s := range secs {
		taken[s.Name] = true
		if s.Type == "nodes" {
			byName[s.Name] = s
		}
	}
	refs := references(secs, byName)

	group := strings.ToLower(rep.Group)
	out := make([]Section, 0, len(secs)+len(fresh))
	for _, s := range secs {
		if s.Type == "nodes" && s.Get("add_mode") == "2" && hasOpt(s, "group") && strings.ToLower(s.Get("group")) == group {
			continue
		}
		out = append(out, s.clone())
	}
	if newName == nil {
		newName = randomName
	}
	ds := sub.Get("domain_strategy")
	freshNames := map[string]bool{}
	for _, n := range fresh {
		name := newName()
		for taken[name] || name == "" {
			name = newName()
		}
		taken[name] = true
		n.Name = name
		if ds == "UseIPv4" || ds == "UseIPv6" {
			n.Options["domain_strategy"] = ds
		}
		if dr := sub.Get("domain_resolver"); dr != "" {
			n.Options["domain_resolver"] = dr
		}
		freshNames[name] = true
		out = append(out, n)
	}

	// First what the old node is again — the same node, on a new host, by
	// another name — or the same country; then, with those settled, the
	// fallbacks, which follow where the main slot went.
	var pending []ref
	for _, r := range refs {
		i, how := pickNode(out, r.current)
		if i < 0 {
			pending = append(pending, r)
			continue
		}
		setRef(out, r, out[i].Name)
		if how != "same node" {
			rep.Rebound = append(rep.Rebound, Rebinding{Ref: r.label, From: describe(r.current), To: describe(out[i]), How: how})
		}
	}
	// The main slot first: the others' fallback follows where it went.
	sort.SliceStable(pending, func(i, j int) bool {
		return pending[i].option == "WorldProxy" && pending[j].option != "WorldProxy"
	})
	for _, r := range pending {
		i, how := fallbackNode(out, r, freshNames)
		if i < 0 {
			// Not reachable while the feed gave a node; never an index out of
			// range if it were.
			rep.Rebound = append(rep.Rebound, Rebinding{Ref: r.label, From: describe(r.current), How: "left as it was: nothing to put it on"})
			continue
		}
		setRef(out, r, out[i].Name)
		rep.Rebound = append(rep.Rebound, Rebinding{Ref: r.label, From: describe(r.current), To: describe(out[i]), How: how})
	}

	for i := range out {
		if out[i].Type == "subscribe_list" && out[i].Name == subscription {
			out[i].Options["md5"] = FeedMD5(body)
			delete(out[i].Options, "expired_date")
			delete(out[i].Options, "rem_traffic")
		}
	}
	return out, rep, nil
}

// chainsNodes: update_node would chain the subscription's nodes — through a
// pre-proxy or landing node it finds usable (valid_chain_node: a manual node,
// Xray or sing-box, not chained itself), or an outbound interface. A
// chain_proxy naming nothing usable is ignored there, and here.
func chainsNodes(secs []Section, sub Section) bool {
	usable := func(name string) bool {
		for _, s := range secs {
			if s.Type == "nodes" && s.Name == name {
				t := s.Get("type")
				return s.Get("chain_proxy") == "" && s.Get("add_mode") != "2" && (t == "Xray" || t == "sing-box")
			}
		}
		return false
	}
	switch sub.Get("chain_proxy") {
	case "1":
		return usable(sub.Get("preproxy_node"))
	case "2":
		return usable(sub.Get("to_node"))
	case "3":
		return sub.Get("outbound_iface") != ""
	}
	return false
}

// unusable says why PassWall's nodeFilter drops a node, or why this one
// does: the provider's placeholder.
func unusable(n Section, filter func(string) bool) string {
	addr, ok := n.Options["address"]
	switch {
	case filter(n.Get("remarks")):
		return "filtered by keyword"
	case !ok:
		return "no address"
	case n.Get("remarks") == "NULL":
		return "no name"
	case addr == "127.0.0.1":
		return "a loopback address"
	case !isHostname(addr) && !isIP(addr):
		return "not a host name or an address"
	}
	if ip := net.ParseIP(strings.Trim(addr, "[]")); ip != nil && (ip.IsUnspecified() || ip.IsLoopback()) {
		return "the provider's placeholder"
	}
	return ""
}

// keywordFilter is subscribe.lua's is_filter_keyword for this subscription:
// its own mode and lists, or the global ones (mode 5, the default).
func keywordFilter(secs []Section, sub Section) func(string) bool {
	var global Section
	for _, s := range secs {
		if s.Type == "global_subscribe" {
			global = s
			break
		}
	}
	list := func(s Section, k string) []string {
		if l, ok := s.Lists[k]; ok {
			return l
		}
		if v, ok := s.Options[k]; ok {
			return []string{v}
		}
		return nil
	}
	mode := global.Get("filter_keyword_mode")
	if mode == "" {
		mode = "0"
	}
	discard, keep := list(global, "filter_discard_list"), list(global, "filter_keep_list")
	switch m := sub.Get("filter_keyword_mode"); m {
	case "0":
		mode = "0"
	case "1":
		mode, discard = "1", list(sub, "filter_discard_list")
	case "2":
		mode, keep = "2", list(sub, "filter_keep_list")
	case "3", "4":
		mode, keep, discard = m, list(sub, "filter_keep_list"), list(sub, "filter_discard_list")
	}
	any := func(l []string, v string) bool {
		for _, k := range l {
			if strings.Contains(v, k) {
				return true
			}
		}
		return false
	}
	return func(v string) bool {
		switch mode {
		case "1":
			return any(discard, v)
		case "2":
			return !any(keep, v)
		case "3":
			if any(keep, v) {
				return false
			}
			return any(discard, v)
		case "4":
			if any(discard, v) {
				return true
			}
			return !any(keep, v)
		}
		return false
	}
}

// ref is one place that names a node.
type ref struct {
	label   string
	section string
	option  string
	index   int // in a list; -1 for an option
	current Section
}

// references are the places update_node re-points (its CONFIG), in its
// order, each with the node it names as that node is now. Only names of
// "nodes" sections count: _direct, _default and a socks section are not.
func references(secs []Section, byName map[string]Section) []ref {
	var out []ref
	one := func(s Section, opt string) {
		if v, ok := s.Options[opt]; ok {
			if n, ok := byName[v]; ok {
				out = append(out, ref{label: s.Name + "." + opt, section: s.Name, option: opt, index: -1, current: n})
			}
		}
	}
	many := func(s Section, opt string) {
		for i, v := range s.Lists[opt] {
			if n, ok := byName[v]; ok {
				out = append(out, ref{label: fmt.Sprintf("%s.%s[%d]", s.Name, opt, i), section: s.Name, option: opt, index: i, current: n})
			}
		}
	}
	for _, s := range secs {
		if s.Type == "global" {
			one(s, "node")
			break // PassWall reads @global[0]
		}
	}
	for _, s := range secs {
		if s.Type == "socks" {
			one(s, "node")
			many(s, "autoswitch_backup_node")
		}
	}
	for _, s := range secs {
		if s.Type == "haproxy_config" {
			one(s, "lbss")
		}
	}
	for _, s := range secs {
		if s.Type == "acl_rule" {
			one(s, "node")
		}
	}
	for _, s := range secs {
		if s.Type != "nodes" {
			continue
		}
		switch s.Get("protocol") {
		case "_shunt":
			for _, r := range secs {
				if r.Type == "shunt_rules" && hasOpt(r, "remarks") {
					one(s, r.Name)
					one(s, r.Name+"_proxy_tag")
				}
			}
			one(s, "default_node")
			one(s, "default_proxy_tag")
		case "_balancing":
			many(s, "balancing_node")
			one(s, "fallback_node")
		case "_urltest":
			many(s, "urltest_node")
		default:
			one(s, "preproxy_node")
			one(s, "to_node")
		}
	}
	return out
}

func setRef(secs []Section, r ref, name string) {
	for i := range secs {
		if secs[i].Name != r.section {
			continue
		}
		if r.index < 0 {
			secs[i].Options[r.option] = name
		} else if r.index < len(secs[i].Lists[r.option]) {
			secs[i].Lists[r.option][r.index] = name
		}
	}
}

// pickNode is select_node without its host-only match (difference 2): the
// first node, in file order and within the old node's group, that is the old
// one — by section name; by type, name and address:port; by type and
// address:port, then address:port, when the names do not say another
// country; by name — or then the same country: by the same path (the same
// flags, 🇷🇺🇳🇱), by another (the same last flag, 🇳🇱).
func pickNode(secs []Section, cur Section) (int, string) {
	type key func(Section) (string, bool)
	opt := func(k string) key { return func(s Section) (string, bool) { v, ok := s.Options[k]; return v, ok } }
	addrPort := func(s Section) (string, bool) {
		a, ok1 := s.Options["address"]
		p, ok2 := s.Options["port"]
		return a + ":" + p, ok1 && ok2
	}
	flags := func(s Section) (string, bool) { f := flagsOf(s.Get("remarks")); return strings.Join(f, ""), len(f) > 0 }
	country := func(s Section) (string, bool) {
		f := flagsOf(s.Get("remarks"))
		if len(f) == 0 {
			return "", false
		}
		return f[len(f)-1], true
	}
	sameGroup := func(s Section) bool {
		a, okA := s.Options["group"]
		b, okB := cur.Options["group"]
		return okA == okB && a == b
	}
	// A host:port match must not name another country than the old node.
	sameCountryOrUnnamed := func(s Section) bool {
		a, okA := country(s)
		b, okB := country(cur)
		return !okA || !okB || a == b
	}
	match := func(guard func(Section) bool, keys ...key) int {
		want := make([]string, len(keys))
		for i, k := range keys {
			v, ok := k(cur)
			if !ok {
				return -1
			}
			want[i] = v
		}
		for i, s := range secs {
			if s.Type != "nodes" || !sameGroup(s) || (guard != nil && !guard(s)) {
				continue
			}
			all := true
			for j, k := range keys {
				if v, ok := k(s); !ok || v != want[j] {
					all = false
					break
				}
			}
			if all {
				return i
			}
		}
		return -1
	}
	for i, s := range secs {
		if s.Type == "nodes" && s.Name == cur.Name {
			return i, "same node"
		}
	}
	for _, m := range []struct {
		how   string
		guard func(Section) bool
		keys  []key
	}{
		{"name+address:port", nil, []key{opt("type"), opt("remarks"), addrPort}},
		{"address:port", sameCountryOrUnnamed, []key{opt("type"), addrPort}},
		{"address:port", sameCountryOrUnnamed, []key{addrPort}},
		{"name", nil, []key{opt("remarks")}},
		{"same country, same path", nil, []key{flags}},
		{"same country", nil, []key{country}},
	} {
		if i := match(m.guard, m.keys...); i >= 0 {
			return i, m.how
		}
	}
	return -1, ""
}

// fallbackNode is where a reference goes when nothing in the feed is its old
// node or its country (difference 3): the main slot's node — the node its
// shunt's WorldProxy slot names now, else the one most of the shunt's slots
// name — then «Авто Самый стабильный», any «Авто», the feed's first node.
func fallbackNode(secs []Section, r ref, fresh map[string]bool) (int, string) {
	idx := map[string]int{}
	for i, s := range secs {
		if s.Type == "nodes" {
			idx[s.Name] = i
		}
	}
	if main := mainSlotNode(secs, r, idx); main >= 0 && secs[main].Name != r.current.Name {
		return main, "the main slot's node"
	}
	// Among the nodes this refresh made: a subscription named "default"
	// gives its nodes no group, so they are known by name, not by group.
	first := func(want func(string) bool) int {
		for i, s := range secs {
			if s.Type == "nodes" && fresh[s.Name] && want(strings.ToLower(s.Get("remarks"))) {
				return i
			}
		}
		return -1
	}
	if i := first(func(n string) bool {
		return strings.Contains(n, "авто") && strings.Contains(n, "самый стабильный")
	}); i >= 0 {
		return i, "«Авто Самый стабильный»"
	}
	if i := first(func(n string) bool { return strings.Contains(n, "авто") }); i >= 0 {
		return i, "«Авто»"
	}
	return first(func(string) bool { return true }), "the feed's first node"
}

// mainSlotNode is the node the main slot names: WorldProxy of the shunt the
// reference is in (or of the global node's shunt, for a reference outside
// one), else the node most of that shunt's other slots name; -1 when there
// is none. For WorldProxy itself there is no main slot but the others.
func mainSlotNode(secs []Section, r ref, idx map[string]int) int {
	var shunt *Section
	for i := range secs {
		if secs[i].Type == "nodes" && secs[i].Name == r.section && secs[i].Get("protocol") == "_shunt" {
			shunt = &secs[i]
		}
	}
	if shunt == nil {
		for _, s := range secs {
			if s.Type == "global" {
				if i, ok := idx[s.Get("node")]; ok && secs[i].Get("protocol") == "_shunt" {
					shunt = &secs[i]
				}
				break
			}
		}
	}
	if shunt == nil {
		return -1
	}
	self := shunt.Name == r.section
	if i, ok := idx[shunt.Get("WorldProxy")]; ok && secs[i].Get("protocol") != "_shunt" && !(self && r.option == "WorldProxy") {
		return i
	}
	if self && r.option == "WorldProxy" {
		return -1 // the main slot itself: «Авто» next
	}
	count := map[int]int{}
	best, most := -1, 0
	for _, rule := range secs {
		if rule.Type != "shunt_rules" || (self && rule.Name == r.option) {
			continue
		}
		i, ok := idx[shunt.Get(rule.Name)]
		if !ok || secs[i].Get("protocol") == "_shunt" {
			continue
		}
		count[i]++
		if count[i] > most || (count[i] == most && i < best) {
			best, most = i, count[i]
		}
	}
	return best
}

// flagsOf is the flags in a node's name, in order: 🇷🇺🇳🇱 is ["RU", "NL"] —
// the path (the entry, then the exit) and, last, the country.
func flagsOf(name string) []string {
	r := []rune(name)
	var out []string
	for i := 0; i+1 < len(r); i++ {
		if isRegional(r[i]) && isRegional(r[i+1]) {
			out = append(out, string([]rune{'A' + (r[i] - 0x1F1E6), 'A' + (r[i+1] - 0x1F1E6)}))
			i++
		}
	}
	return out
}

func isRegional(c rune) bool { return c >= 0x1F1E6 && c <= 0x1F1FF }

func describe(s Section) string {
	return fmt.Sprintf("%s (%s:%s)", s.Get("remarks"), s.Get("address"), s.Get("port"))
}

func hasOpt(s Section, k string) bool { _, ok := s.Options[k]; return ok }

func (s Section) clone() Section {
	c := Section{Type: s.Type, Name: s.Name, Anonymous: s.Anonymous, Options: map[string]string{}, Lists: map[string][]string{}}
	for k, v := range s.Options {
		c.Options[k] = v
	}
	for k, v := range s.Lists {
		c.Lists[k] = append([]string(nil), v...)
	}
	c.Keys = append([]string(nil), s.Keys...)
	return c
}

const nameChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// randomName is api.gen_random_char(): eight letters and digits.
func randomName() string {
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(nameChars))))
		if err != nil {
			return ""
		}
		b[i] = nameChars[n.Int64()]
	}
	return string(b)
}
