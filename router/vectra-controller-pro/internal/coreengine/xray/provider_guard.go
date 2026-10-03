package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// What the router takes from a provider's document, and what it does not.
//
// The provider writes the whole xray document; the router runs it as root.
// Everything in it that names a file on the router, opens a port, or lets a
// stranger in is the router's to decide, not the provider's. A document that
// asks for any of it is refused whole: it is not applied, and the router
// keeps the config it runs (apply.go).
//
// Top level — an allowlist, keys matched as xray matches them (foldKey):
//
//   - "dns", "routing", "outbounds", "policy", "stats", "observatory",
//     "burstObservatory": the provider's, kept byte for byte apart from what
//     the router's own options add.
//   - "log": REPLACED whole by the router's own (routerLogObject): the
//     provider cannot point xray's access or error log at a file, raise the
//     level until the log floods RAM, or switch on the DNS log.
//   - "inbounds": replaced by the tproxy inbound (splice.go).
//   - "remarks": the provider's name for the document — every real document
//     carries it, and xray reads nothing from it.
//   - "version" and "fakedns": what the router's own route-policy generator
//     writes (routepolicy, AdaptPassWall). A FakeDNS pool must stay inside
//     xray's own reserved ranges (198.18.0.0/15, fc00::/7) at most 65535
//     addresses each — a pool over the LAN's addresses would answer names
//     with the LAN's own devices.
//
// Everything else refuses the document: "api" and "metrics" (on 0.0.0.0 they
// re-point every balancer and serve xray's debug pages — the router installs
// its own, on loopback), "reverse" (bridges and portals let the far side open
// connections INTO the router's network), "env" (XRAY_LOCATION_ASSET moves
// where xray reads files from), "transport", and any key xray may learn later.
//
// Anywhere in the document:
//
//   - "reverse" (VLESS's reverse proxy in an outbound's settings) and a
//     freedom outbound's "redirect" (every connection sent to an address of
//     the provider's choosing — the LAN's among them) refuse it.
//   - A field naming a file — any key ending in "file" (certificateFile,
//     keyFile, ...; inline certificates stay) — and TLS's masterKeyLog, which
//     writes every session key to a file, refuse it. So does a geo list read
//     from outside xray's asset directory ("ext:../../etc/x:tag").
//   - Identifiers — every tag, outboundTag, balancerTag, ruleTag,
//     fallbackTag, dialerProxy, inboundTag, selector, subjectSelector, user
//     and email, and every object key — refuse it when they carry a control
//     character (a newline in a tag would write a line of the provider's
//     choosing into the router's log) or run past maxIdentifierLen bytes.
//
// With DNS through the tunnel, "dns.hosts" may not pin a name the router
// resolves directly — the panel, NTP (dnsHostsConflict).

// maxIdentifierLen is far past any real tag (the provider's longest is
// "stage-main-backup") and well short of a payload.
const maxIdentifierLen = 256

// allowedTopLevel are the provider's top-level keys the router takes (see
// the top of this file), by foldKey.
var allowedTopLevel = map[string]bool{}

// identifierKeys hold names that end up in xray's log and the router's UI.
var identifierKeys = map[string]bool{}

func init() {
	for _, k := range []string{LogKey, DNSKey, RoutingKey, OutboundsKey, InboundsKey, PolicyKey, "stats",
		ObservatoryKey, BurstObservatoryKey, "remarks", "version", FakeDNSKey} {
		allowedTopLevel[foldKey(k)] = true
	}
	for _, k := range []string{"tag", "outboundTag", "balancerTag", "ruleTag", "fallbackTag", "dialerProxy",
		"inboundTag", "selector", "subjectSelector", "user", "email"} {
		identifierKeys[foldKey(k)] = true
	}
}

// fileKey: the field names a file for xray to read or write.
func fileKey(k string) bool {
	f := foldKey(k)
	return strings.HasSuffix(f, foldKey("file")) || f == foldKey("masterKeyLog")
}

// routerLogObject is the log the router runs xray with, whatever the
// provider's said: errors at warning to stdout (the router's log), no DNS
// log, and with noAccess no line per connection either.
func routerLogObject(noAccess bool) json.RawMessage {
	if noAccess {
		return json.RawMessage(`{"access":"none","loglevel":"warning"}`)
	}
	return json.RawMessage(`{"loglevel":"warning"}`)
}

// checkProviderDocument refuses a provider document that asks for what the
// router keeps to itself (see the top of this file). The keys the router
// replaces whole — log, inbounds — are not looked into.
func checkProviderDocument(doc []byte) error {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var top map[string]interface{}
	if err := dec.Decode(&top); err != nil {
		// Not an object: the splice's own reader says so.
		return nil
	}
	for key, v := range top {
		fk := foldKey(key)
		if !allowedTopLevel[fk] {
			return fmt.Errorf("xray splice: the provider document carries the top-level key %s, which the router does not take — refusing the document", strconv.Quote(clip(key)))
		}
		switch fk {
		case foldKey(LogKey), foldKey(InboundsKey):
			continue
		case foldKey(FakeDNSKey):
			if err := checkFakeDNSPools(v); err != nil {
				return err
			}
		case foldKey(OutboundsKey):
			if err := checkFreedomRedirect(v); err != nil {
				return err
			}
		}
		if err := guardValue(v, "$."+key, 1); err != nil {
			return err
		}
	}
	return nil
}

func guardValue(v interface{}, path string, depth int) error {
	if depth > maxScanDepth {
		return fmt.Errorf("xray splice: the provider document is nested deeper than %d levels — refusing the document", maxScanDepth)
	}
	switch x := v.(type) {
	case map[string]interface{}:
		for k, child := range x {
			if !cleanIdentifier(k) {
				return fmt.Errorf("xray splice: the provider document has a key under %s with a control character or longer than %d bytes — refusing the document", strconv.Quote(path), maxIdentifierLen)
			}
			p := path + "." + k
			fk := foldKey(k)
			if fk == foldKey("reverse") {
				return fmt.Errorf("xray splice: the provider document asks for a reverse proxy at %s — refusing the document", strconv.Quote(p))
			}
			if fileKey(k) && !emptyValue(child) {
				return fmt.Errorf("xray splice: the provider document names a file for xray at %s — refusing the document", strconv.Quote(p))
			}
			if identifierKeys[fk] {
				if err := checkIdentifiers(child, p); err != nil {
					return err
				}
			}
			if err := guardValue(child, p, depth+1); err != nil {
				return err
			}
		}
	case []interface{}:
		for i, child := range x {
			if err := guardValue(child, path+"["+strconv.Itoa(i)+"]", depth+1); err != nil {
				return err
			}
		}
	case string:
		if err := checkExtFile(x, path); err != nil {
			return err
		}
	}
	return nil
}

// checkIdentifiers checks a name or a list of names (xray's StringList).
func checkIdentifiers(v interface{}, path string) error {
	var names []string
	switch x := v.(type) {
	case string:
		names = []string{x}
	case []interface{}:
		for _, e := range x {
			if s, ok := e.(string); ok {
				names = append(names, s)
			}
		}
	}
	for _, s := range names {
		if !cleanIdentifier(s) {
			return fmt.Errorf("xray splice: the provider's identifier at %s has a control character or is longer than %d bytes — refusing the document", strconv.Quote(path), maxIdentifierLen)
		}
	}
	return nil
}

// cleanIdentifier: no control character (nor a Unicode line or paragraph
// separator, which some logs and terminals break lines on) and not absurdly
// long.
func cleanIdentifier(s string) bool {
	if len(s) > maxIdentifierLen {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return false
		}
	}
	return true
}

// clip is s cut short for an error message.
func clip(s string) string {
	if len(s) > 64 {
		return s[:64] + "…"
	}
	return s
}

// checkExtFile refuses a geo list ("ext:file.dat:tag") read from outside
// xray's asset directory: xray joins the file name onto that directory as it
// is.
func checkExtFile(s, path string) error {
	if len(s) < 4 || !strings.EqualFold(s[:4], "ext:") {
		return nil
	}
	file := s[4:]
	if i := strings.IndexByte(file, ':'); i >= 0 {
		file = file[:i]
	}
	if file == "" || strings.ContainsAny(file, `/\`) || strings.Contains(file, "..") || !cleanIdentifier(file) {
		return fmt.Errorf("xray splice: the provider document reads a geo list from outside xray's asset directory at %s — refusing the document", strconv.Quote(path))
	}
	return nil
}

// checkFreedomRedirect refuses a freedom outbound that sends every
// connection to one address of the provider's choosing.
func checkFreedomRedirect(v interface{}) error {
	obs, _ := v.([]interface{})
	for i, o := range obs {
		ob, ok := o.(map[string]interface{})
		if !ok {
			continue
		}
		proto, _ := fieldOf(ob, "protocol").(string)
		if !strings.EqualFold(strings.TrimSpace(proto), "freedom") {
			continue
		}
		settings, _ := fieldOf(ob, "settings").(map[string]interface{})
		if r := fieldOf(settings, "redirect"); !emptyValue(r) {
			return fmt.Errorf("xray splice: the provider's freedom outbound %d redirects every connection — refusing the document", i)
		}
	}
	return nil
}

// fieldOf is obj's value for name, matched as xray matches keys.
func fieldOf(obj map[string]interface{}, name string) interface{} {
	for k, v := range obj {
		if foldKey(k) == foldKey(name) {
			return v
		}
	}
	return nil
}

// checkFakeDNSPools keeps a provider's FakeDNS inside xray's own reserved
// ranges and at most 65535 addresses a pool.
func checkFakeDNSPools(v interface{}) error {
	var pools []interface{}
	switch x := v.(type) {
	case nil:
		return nil
	case []interface{}:
		pools = x
	case map[string]interface{}:
		pools = []interface{}{x}
	default:
		return fmt.Errorf("xray splice: the provider's fakedns is neither a pool nor a list of pools — refusing the document")
	}
	_, v4, _ := net.ParseCIDR("198.18.0.0/15")
	_, v6, _ := net.ParseCIDR("fc00::/7")
	for i, p := range pools {
		obj, ok := p.(map[string]interface{})
		if !ok {
			return fmt.Errorf("xray splice: the provider's fakedns pool %d is not an object — refusing the document", i)
		}
		for k, val := range obj {
			switch foldKey(k) {
			case foldKey("ipPool"):
				s, _ := val.(string)
				ip, n, err := net.ParseCIDR(s)
				if err != nil {
					return fmt.Errorf("xray splice: the provider's fakedns pool %d is not a network — refusing the document", i)
				}
				ones, bits := n.Mask.Size()
				if !(ip.To4() != nil && v4.Contains(ip) && bits == 32 && ones >= 15) && !(ip.To4() == nil && v6.Contains(ip) && ones >= 7) {
					return fmt.Errorf("xray splice: the provider's fakedns pool %s is outside 198.18.0.0/15 and fc00::/7 — refusing the document", strconv.Quote(clip(s)))
				}
			case foldKey("poolSize"):
				num, _ := val.(json.Number)
				size, err := num.Int64()
				if err != nil || size < 1 || size > 65535 {
					return fmt.Errorf("xray splice: the provider's fakedns pool %d asks for more than 65535 addresses — refusing the document", i)
				}
			}
		}
	}
	return nil
}

// emptyValue: what xray takes for unset.
func emptyValue(v interface{}) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []interface{}:
		return len(x) == 0
	case map[string]interface{}:
		return len(x) == 0
	case bool:
		return !x
	}
	return false
}

// dnsHostsConflict names the first entry of the provider's dns.hosts that
// would answer a name the router resolves directly (pinned: xray domain
// matchers, "full:" or "domain:"), or "". With DNS through the tunnel the
// router's own lookups are answered by xray, and hosts come before every
// server: an entry for the panel or pool.ntp.org would send the router there
// — NTP has no TLS to notice.
func dnsHostsConflict(dnsRaw json.RawMessage, pinned []string) string {
	var obj map[string]json.RawMessage
	if json.Unmarshal(dnsRaw, &obj) != nil {
		return ""
	}
	var hosts map[string]json.RawMessage
	if raw := ruleField(obj, "hosts"); len(raw) == 0 || json.Unmarshal(raw, &hosts) != nil {
		return ""
	}
	for key := range hosts {
		for _, p := range pinned {
			if hostsKeyCovers(key, p) {
				return key
			}
		}
	}
	return ""
}

// hostsKeyCovers: the hosts key answers the pinned name, or (for a
// "domain:" pin) a name under it. A geosite or ext list is the provider's
// category, not a pin of its own, and is left to it.
func hostsKeyCovers(key, pinned string) bool {
	name, sub := strings.CutPrefix(strings.ToLower(pinned), "domain:")
	if !sub {
		name = strings.TrimPrefix(name, "full:")
	}
	k := strings.ToLower(strings.TrimSpace(key))
	under := func(x string) bool { return x == name || (sub && strings.HasSuffix(x, "."+name)) }
	switch {
	case strings.HasPrefix(k, "domain:"):
		x := strings.TrimPrefix(k, "domain:")
		return under(x) || strings.HasSuffix(name, "."+x)
	case strings.HasPrefix(k, "full:"):
		return under(strings.TrimPrefix(k, "full:"))
	case strings.HasPrefix(k, "keyword:"):
		return strings.Contains(name, strings.TrimPrefix(k, "keyword:"))
	case strings.HasPrefix(k, "regexp:"):
		re, err := regexp.Compile(strings.TrimSpace(key)[len("regexp:"):])
		return err != nil || re.MatchString(name)
	case strings.HasPrefix(k, "geosite:"), strings.HasPrefix(k, "ext:"), strings.HasPrefix(k, "dotless:"):
		return false
	}
	return under(k)
}
