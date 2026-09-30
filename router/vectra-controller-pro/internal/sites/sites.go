// Package sites reads the router owner's own exceptions ("My sites"): a site
// that must always go WITHOUT the VPN (a bank that refuses VPN addresses) or
// always THROUGH it (blocked, but not in the provider's lists).
//
// An entry is whatever a person pastes into the router UI —
// "https://www.Sberbank.ru/ru/person", "госуслуги.рф", "1.2.3.0/24",
// "[2001:db8::1]:443". Parse reduces it to one canonical form, a domain, an
// IP address or a CIDR, and refuses anything else. The router is
// authoritative: the UI may normalize for display, but what is kept and
// rendered is what Parse says.
//
// Domains are kept in Unicode, as a person reads them, and rendered for xray
// in punycode (IDNA A-labels), as they travel in SNI and Host headers. The
// standard library has no IDNA: punycode (RFC 3492) is implemented here, and
// the mapping is the part of UTS #46 a keyboard produces — lowercase, the
// full-width forms and ideographic full stops of East Asian input. There is
// no Unicode normalization (NFC) in the standard library: a name typed with
// a combining sequence instead of a precomposed letter is kept as typed.
package sites

import (
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Max is how many sites each list may hold.
const Max = 100

// maxEntryLen bounds one entry as sent: a pasted URL may be long, a site is
// not.
const maxEntryLen = 2048

// Site is one entry, normalized.
type Site struct {
	// Display is the canonical form — what is kept, shown and compared: a
	// domain in lowercase Unicode ("госуслуги.рф"), an address in canonical
	// text ("2001:db8::1"), a CIDR masked ("1.2.3.0/24").
	Display string
	// Domain is true for a domain, false for an address or a CIDR.
	Domain bool
	// ASCII is the domain as it travels and as xray matches it, in IDNA
	// A-labels: "xn--c1aapkosapc.xn--p1ai". Empty for an address.
	ASCII string
	// Prefix is the address or CIDR; an address is its full-length prefix.
	// The zero Prefix for a domain.
	Prefix netip.Prefix
	// Service is a category of the router's geo file ("discord"), what
	// «+ сервис» adds (spec decision 4): Display "geosite:discord", matched
	// as a domain matcher. Whether the file has it the router checks.
	Service string
}

// ServicePrefix starts a service entry.
const ServicePrefix = "geosite:"

// maxServiceLen bounds a category name; the geo files' longest is ~40.
const maxServiceLen = 64

// parseService reads "geosite:<category>": letters, digits and "-", "_",
// "!", "@", "." — the characters geo files name categories and attributes
// with — and nothing that could leave the name.
func parseService(s string) (Site, error) {
	name := s[len(ServicePrefix):]
	if name == "" || len(name) > maxServiceLen {
		return Site{}, errors.New("a service needs a name of 1 to 64 characters")
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '!' || r == '@' || r == '.') {
			return Site{}, fmt.Errorf("%s is not a service name", quoteBounded(name))
		}
	}
	if strings.Contains(name, "..") {
		return Site{}, fmt.Errorf("%s is not a service name", quoteBounded(name))
	}
	return Site{Display: ServicePrefix + name, Domain: true, Service: name}, nil
}

// Matcher is the site as an xray routing matcher: "domain:<ascii>" — the
// domain and every subdomain of it — or the address or CIDR itself.
func (s Site) Matcher() string {
	if s.Service != "" {
		return s.Display
	}
	if s.Domain {
		return "domain:" + s.ASCII
	}
	return s.Display
}

// Covers reports whether a rule for s also matches everything a rule for
// other matches: the same site, a subdomain, an address or a CIDR inside it.
func (s Site) Covers(other Site) bool {
	// A service may hold any domain: it covers every one — so an explicit
	// site of the other list goes first and wins — and no other service.
	if s.Service != "" {
		return other.Service == s.Service || (other.Domain && other.Service == "")
	}
	if other.Service != "" {
		return false
	}
	if s.Domain != other.Domain {
		return false
	}
	if s.Domain {
		return other.ASCII == s.ASCII || strings.HasSuffix(other.ASCII, "."+s.ASCII)
	}
	return s.Prefix.Bits() <= other.Prefix.Bits() && s.Prefix.Contains(other.Prefix.Addr())
}

// Specificity orders sites so that one only ever covers sites at least as
// specific as itself: the labels of a domain, the prefix length of an
// address.
func (s Site) Specificity() int {
	if s.Service != "" {
		return 0 // below every domain it covers
	}
	if s.Domain {
		return strings.Count(s.ASCII, ".") + 1
	}
	return s.Prefix.Bits()
}

// Parse normalizes one entry: trim and lowercase; drop a scheme, userinfo, a
// path, query and fragment, a port (bracketed IPv6 included), a leading
// "*.", "." or "www." and a trailing dot; then it must be an IP address, a
// CIDR or a domain.
func Parse(entry string) (Site, error) {
	s := strings.TrimSpace(entry)
	switch {
	case s == "":
		return Site{}, errors.New("empty")
	case len(s) > maxEntryLen:
		return Site{}, fmt.Errorf("longer than %d bytes", maxEntryLen)
	case !utf8.ValidString(s):
		return Site{}, errors.New("not valid UTF-8")
	case strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0:
		return Site{}, errors.New("contains a space or a control character")
	}
	s = strings.ToLower(foldWidth(s))
	if strings.HasPrefix(s, ServicePrefix) {
		return parseService(s)
	}

	url := false
	if i := strings.Index(s, "://"); i >= 0 {
		if !isScheme(s[:i]) {
			return Site{}, fmt.Errorf("%s is not a URL scheme", quoteBounded(s[:i]))
		}
		s, url = s[i+3:], true
	} else if strings.HasPrefix(s, "//") {
		s, url = s[2:], true
	}
	if !url {
		// Not a URL: after an address, "/" starts a prefix length, never a
		// path — "1.2.3.4/33" is a mistyped CIDR, not the address 1.2.3.4.
		if host, bits, ok := strings.Cut(s, "/"); ok {
			if a, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")); err == nil {
				return prefixSite(a, bits)
			}
		}
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	host, err := stripPort(s)
	if err != nil {
		return Site{}, err
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return addrSite(a)
	}
	return domainSite(trimWildcard(strings.TrimSuffix(host, ".")))
}

// foldWidth maps what an East Asian keyboard types for ASCII and for a dot —
// full-width forms, ideographic full stops — to what a browser sends; UTS #46
// maps them the same way.
func foldWidth(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\u3002' || r == '\uFF0E' || r == '\uFF61':
			return '.'
		case r >= '\uFF01' && r <= '\uFF5E':
			return r - 0xFEE0
		}
		return r
	}, s)
}

// isScheme: a letter, then letters, digits, "+", "-" or "." (RFC 3986 3.1).
func isScheme(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// stripPort drops a port: "host:443", "[v6]:443", "[v6]". An address with
// more than one colon and no brackets is IPv6 and carries no port.
func stripPort(s string) (string, error) {
	if strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return "", errors.New("an unclosed [")
		}
		host, rest := s[1:end], s[end+1:]
		if rest != "" && (rest[0] != ':' || !isPort(rest[1:])) {
			return "", fmt.Errorf("%s after ] is not a port", quoteBounded(rest))
		}
		if a, err := netip.ParseAddr(host); err != nil || !a.Is6() {
			return "", fmt.Errorf("[%s] is not an IPv6 address", host)
		}
		return host, nil
	}
	if strings.Count(s, ":") != 1 {
		return s, nil
	}
	host, port, _ := strings.Cut(s, ":")
	if !isPort(port) {
		return "", fmt.Errorf("%s is not a port", quoteBounded(port))
	}
	return host, nil
}

// isPort: 1-65535, or empty ("example.com:" is how a URL may end).
func isPort(p string) bool {
	if p == "" {
		return true
	}
	if len(p) > 5 {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return false
		}
	}
	n, _ := strconv.Atoi(p)
	return n >= 1 && n <= 65535
}

func addrSite(a netip.Addr) (Site, error) {
	if a.Zone() != "" {
		return Site{}, errors.New("an address with a zone (%) names this router's own link")
	}
	a = a.Unmap()
	return Site{Display: a.String(), Prefix: netip.PrefixFrom(a, a.BitLen())}, nil
}

// prefixSite is a CIDR: masked, and a full-length one is its address.
func prefixSite(a netip.Addr, bits string) (Site, error) {
	if a.Zone() != "" {
		return Site{}, errors.New("an address with a zone (%) names this router's own link")
	}
	n, err := strconv.Atoi(bits)
	if err != nil || bits == "" || bits[0] < '0' || bits[0] > '9' || n > a.BitLen() {
		return Site{}, fmt.Errorf("%s is not a prefix length for %s", quoteBounded("/"+bits), a)
	}
	if a.Is4In6() && n >= 96 {
		a, n = a.Unmap(), n-96
	}
	p := netip.PrefixFrom(a, n).Masked()
	if n == p.Addr().BitLen() {
		return Site{Display: p.Addr().String(), Prefix: p}, nil
	}
	return Site{Display: p.String(), Prefix: p}, nil
}

// trimWildcard drops a leading "*." or "." (the domain and its subdomains,
// which is what a site already means) and then "www." — unless that would
// leave a single label: "www.com" is a site of its own, not all of .com.
func trimWildcard(host string) string {
	switch {
	case strings.HasPrefix(host, "*."):
		host = host[2:]
	case strings.HasPrefix(host, "."):
		host = host[1:]
	}
	if rest := strings.TrimPrefix(host, "www."); rest != host && strings.Contains(rest, ".") {
		host = rest
	}
	return host
}

// domainSite checks a name label by label. A single label is a whole
// top-level domain ("рф", "ru"), as the provider's own rules use them.
func domainSite(host string) (Site, error) {
	if host == "" {
		return Site{}, errors.New("no host")
	}
	labels := strings.Split(host, ".")
	uni := make([]string, len(labels))
	asc := make([]string, len(labels))
	for i, l := range labels {
		u, a, err := domainLabel(l)
		if err != nil {
			return Site{}, err
		}
		uni[i], asc[i] = u, a
	}
	if last := asc[len(asc)-1]; strings.Trim(last, "0123456789") == "" {
		// "1.2.3", "256.1.1.1": a mistyped address, not a name.
		return Site{}, errors.New("not an IP address, and a domain does not end in a number")
	}
	ascii := strings.Join(asc, ".")
	if len(ascii) > 253 {
		return Site{}, fmt.Errorf("the name is %d characters long in punycode; at most 253", len(ascii))
	}
	return Site{Display: strings.Join(uni, "."), Domain: true, ASCII: ascii}, nil
}

// domainLabel returns a label's Unicode and ASCII forms. An ASCII label is
// letters, digits and inner hyphens; an A-label ("xn--…") is decoded, and
// kept only in the one form the encoder produces; a Unicode label is
// letters, digits, combining marks (not first) and inner hyphens.
func domainLabel(l string) (uni, ascii string, err error) {
	if l == "" {
		return "", "", errors.New("an empty label: two dots in a row, or a dot at the start")
	}
	if isASCII(l) {
		if err := checkLDH(l); err != nil {
			return "", "", err
		}
		if !strings.HasPrefix(l, "xn--") {
			return l, l, nil
		}
		u, err := punycodeDecode(l[len("xn--"):])
		if err != nil || isASCII(u) || checkULabel(u) != nil || strings.ToLower(u) != u {
			return "", "", fmt.Errorf("%s is not a valid IDN label", quoteBounded(l))
		}
		if enc, err := punycodeEncode(u); err != nil || "xn--"+enc != l {
			return "", "", fmt.Errorf("%s is not a valid IDN label", quoteBounded(l))
		}
		return u, l, nil
	}
	if strings.HasPrefix(l, "xn--") {
		return "", "", fmt.Errorf("%s: xn-- starts only a punycode label", quoteBounded(l))
	}
	if err := checkULabel(l); err != nil {
		return "", "", err
	}
	enc, err := punycodeEncode(l)
	if err != nil {
		return "", "", fmt.Errorf("%s: %v", quoteBounded(l), err)
	}
	if ascii = "xn--" + enc; len(ascii) > 63 {
		return "", "", fmt.Errorf("%s is %d characters long in punycode; a label holds 63", quoteBounded(l), len(ascii))
	}
	return l, ascii, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func checkLDH(l string) error {
	if len(l) > 63 {
		return fmt.Errorf("%s is %d characters long; a label holds 63", quoteBounded(l), len(l))
	}
	if l[0] == '-' || l[len(l)-1] == '-' {
		return fmt.Errorf("%s starts or ends with a hyphen", quoteBounded(l))
	}
	for i := 0; i < len(l); i++ {
		if c := l[i]; !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return fmt.Errorf("%s: %q is not a letter, a digit or a hyphen", quoteBounded(l), rune(c))
		}
	}
	return nil
}

func checkULabel(l string) error {
	if l == "" {
		return errors.New("an empty label")
	}
	for i, r := range l {
		switch {
		case r == '-':
			if i == 0 || i == len(l)-1 {
				return fmt.Errorf("%s starts or ends with a hyphen", quoteBounded(l))
			}
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case unicode.In(r, unicode.Mn, unicode.Mc, unicode.Me):
			if i == 0 {
				return fmt.Errorf("%s starts with a combining mark", quoteBounded(l))
			}
		default:
			return fmt.Errorf("%s: %q is not a letter, a digit or a hyphen", quoteBounded(l), r)
		}
	}
	return nil
}

// ListError names the first entry a list was refused for.
type ListError struct {
	List string // "direct" or "proxy"
	// Index is the entry's position in the list as sent; -1 when the list
	// as a whole is refused.
	Index  int
	Entry  string
	Reason string
}

func (e *ListError) Error() string {
	if e.Index < 0 {
		return e.List + ": " + e.Reason
	}
	return fmt.Sprintf("%s[%d] %s: %s", e.List, e.Index, quoteBounded(e.Entry), e.Reason)
}

// quoteBounded quotes an entry for an error, at most 64 runes of it: the
// detail goes back to the UI verbatim.
func quoteBounded(s string) string {
	if utf8.RuneCountInString(s) > 64 {
		r := []rune(s)
		return strconv.Quote(string(r[:64])) + "…"
	}
	return strconv.Quote(s)
}

// NormalizeLists normalizes both lists as the router keeps them: every entry
// parsed (the first that is not a site refuses the lot), duplicates dropped
// (the first stays, in the order sent), at most Max each, and no site in
// both.
func NormalizeLists(direct, proxy []string) (d, p []string, err error) {
	d, _, err = normalizeList("direct", direct)
	if err != nil {
		return nil, nil, err
	}
	p, pIdx, err := normalizeList("proxy", proxy)
	if err != nil {
		return nil, nil, err
	}
	inDirect := make(map[string]bool, len(d))
	for _, s := range d {
		inDirect[s] = true
	}
	for i, s := range p {
		if inDirect[s] {
			return nil, nil, &ListError{List: "proxy", Index: pIdx[i], Entry: proxy[pIdx[i]],
				Reason: fmt.Sprintf("%s is in the direct list too; a site goes one way", quoteBounded(s))}
		}
	}
	return d, p, nil
}

// normalizeList returns the list's canonical sites and, for each, the index
// of the entry it came from.
func normalizeList(name string, entries []string) ([]string, []int, error) {
	out := make([]string, 0, len(entries))
	from := make([]int, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		s, err := Parse(e)
		if err != nil {
			return nil, nil, &ListError{List: name, Index: i, Entry: e, Reason: err.Error()}
		}
		if seen[s.Display] {
			continue
		}
		seen[s.Display] = true
		out, from = append(out, s.Display), append(from, i)
	}
	if len(out) > Max {
		return nil, nil, &ListError{List: name, Index: -1, Reason: fmt.Sprintf("%d sites; at most %d", len(out), Max)}
	}
	return out, from, nil
}
