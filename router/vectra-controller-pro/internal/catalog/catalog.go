// Package catalog is what vctl knows about services and sites that is neither
// the provider's nor the owner's: which names are AI («Нейросети»), where
// «Нейросети» go while the owner chose nothing, sites for every router, and
// how often a router asks for a newer catalog. The binary carries one
// (catalog.json, embedded: what a release knows); a newer one, signed with
// the Vectra feed's key, comes from the feed's channel without a release
// (docs/superpowers/specs/2026-10-17-vctl-catalog.md).
//
// Nothing here can run code or name a node: a catalog only chooses among the
// provider's own rules and outbounds, and its sites go through the parser of
// «Мои сайты».
package catalog

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"vectra-controller-pro/internal/feedverify"
	"vectra-controller-pro/internal/sites"
)

//go:embed catalog.json
var builtinRaw []byte

const (
	// Schema is the only catalog format this vctl reads. A catalog of
	// another schema is refused whole; a newer field in a schema-1 catalog is
	// ignored here (Parse without strict) and refused by the publisher.
	Schema = 1
	// MaxBytes caps a catalog and MaxSignatureBytes its signature: what a
	// misbehaving server can cost a 234 MB router.
	MaxBytes          = 256 << 10
	MaxSignatureBytes = 4 << 10
	// DefaultRefreshHours is how often a router asks when the catalog says
	// nothing; MinRefreshHours and MaxRefreshHours bound what it may say.
	DefaultRefreshHours = 6
	MinRefreshHours     = 1
	MaxRefreshHours     = 48

	// maxServices: r24 and r25 cap at 16 and keep their built-in catalog
	// when a catalog holds more.
	maxServices = 64
	maxGroups   = 16
	maxNames    = 1000
	maxIPs      = 1000
	maxGeosites = 100
	maxExits    = 10
	maxExitLen  = 4
	// MaxSites is how many sites each fleet-wide list may hold.
	MaxSites = 300
)

// Service is what the catalog says about one service of the router
// (internal/coreengine/xray.Services names them).
type Service struct {
	// OnlyKnown: the provider's own rule for the service is not trusted as a
	// list — a provider may put other things into the rule that names the
	// service (on 2026-10-17 its AI rule held Steam, Epic, Riot, Blizzard
	// and Star Citizen). The rule still decides the service's path; the
	// service matches only what Knows.
	OnlyKnown bool `json:"onlyKnown,omitempty"`
	// Names are xray domain matchers (domain:, full: or geosite:) the
	// service always covers besides what the provider names.
	Names []string `json:"names,omitempty"`
	// Geosites are geosite categories that are the service: a provider's
	// geosite:<one of them> is kept under OnlyKnown. They are not added.
	Geosites []string `json:"geosites,omitempty"`

	// What the UIs show (r26): a service without a Title is not offered.
	Title Title  `json:"title,omitempty"`
	Group string `json:"group,omitempty"` // a key of Catalog.Groups; "" = top level
	Order int    `json:"order,omitempty"` // within its group
	Icon  string `json:"icon,omitempty"`  // a key of the UIs' fixed icon set
	// Marker is the provider's matcher that finds the service's own rule
	// (domain:steampowered.com); "" = the code's marker for the four r25
	// services, none for the others — «Как основной VPN» is then the main
	// path. IPMarker likewise by address (geoip:telegram).
	Marker   string `json:"marker,omitempty"`
	IPMarker string `json:"ipMarker,omitempty"`
	// IPs the service also covers by address: CIDRs, addresses or geoip:.
	IPs []string `json:"ips,omitempty"`
	// Modes the owner may choose (Mode*), and the one while they chose
	// nothing. None: the r25 behaviour of the service (LegacyModes).
	Modes   []string `json:"modes,omitempty"`
	Default string   `json:"default,omitempty"`
	// Traffic (r28) are classes of traffic the service is, instead of names:
	// «Игровые соединения» is every UDP and TCP port but the web's — what a
	// game sends by address, with nothing to sniff (Star Citizen's hub on
	// TCP 8000 and its servers on UDP 64xxx, 1111, 2026-10-19). Such a service
	// names nothing and is never taken out of the tunnel (validateTraffic):
	// it only chooses, for what the main path carries, one of its nodes.
	Traffic []Traffic `json:"traffic,omitempty"`
}

// Traffic is one class: every port of Network but Except.
type Traffic struct {
	Network string `json:"network"`          // "tcp" or "udp"
	Except  []int  `json:"except,omitempty"` // ports left to the main path (web, DNS, NTP)
}

// Ports is the class's ports as xray's port list ("1-52,54-122,…").
func (t Traffic) Ports() string {
	out := map[int]bool{}
	for _, p := range t.Except {
		out[p] = true
	}
	var b strings.Builder
	start := 0
	flush := func(end int) {
		if start == 0 {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		if start == end {
			fmt.Fprintf(&b, "%d", start)
		} else {
			fmt.Fprintf(&b, "%d-%d", start, end)
		}
		start = 0
	}
	for p := 1; p <= 65535; p++ {
		if out[p] {
			flush(p - 1)
			continue
		}
		if start == 0 {
			start = p
		}
	}
	flush(65535)
	return b.String()
}

// Title is a name in the UIs' languages.
type Title map[string]string

// In picks the language, else Russian, else any.
func (t Title) In(lang string) string {
	if v := t[lang]; v != "" {
		return v
	}
	if v := t["ru"]; v != "" {
		return v
	}
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if t[k] != "" {
			return t[k]
		}
	}
	return ""
}

// Modes a service may offer.
const (
	ModeMain     = "main"     // «Как основной VPN»: the provider's routing
	ModeStable   = "stable"   // «VPN · постоянный IP»: the stable exit
	ModeDirect   = "direct"   // «Напрямую»
	ModeCountry  = "country"  // a country of the entry (router UI)
	ModeLocation = "location" // a provider location (Vectra app)
)

// Group is a heading the UIs put services under («Игры»).
type Group struct {
	Title Title  `json:"title"`
	Order int    `json:"order,omitempty"`
	Icon  string `json:"icon,omitempty"`
	Hint  Title  `json:"hint,omitempty"`
}

// Tunables are settings a release used to fix, each bounded (r26).
type Tunables struct {
	StableExit *StableExit `json:"stableExit,omitempty"`
	Door       *Door       `json:"door,omitempty"`
	QUIC       *QUIC       `json:"quic,omitempty"`
}

// StableExit is how the router finds an exit that keeps one address.
type StableExit struct {
	Samples        int    `json:"samples,omitempty"`
	EveryHours     int    `json:"everyHours,omitempty"`
	RecheckMinutes int    `json:"recheckMinutes,omitempty"`
	URL            string `json:"url,omitempty"`
	// Main: every router sends its whole main path through the stable exit
	// unless the owner says otherwise.
	Main bool `json:"main,omitempty"`
}

// Door is a device's new connections per second into xray, and the burst.
type Door struct {
	Rate  int `json:"rate"`
	Burst int `json:"burst"`
}

// QUIC: Refuse is the fleet's default for refusing QUIC into the tunnel;
// the owner's refuse_quic '0' (QUIC kept) wins over a refusal.
type QUIC struct {
	Refuse *bool `json:"refuse,omitempty"`
}

// The defaults and bounds of the tunables.
const (
	DefaultStableSamples       = 6
	DefaultStableEvery         = 6
	DefaultStableRecheck       = 30
	DefaultStableURL           = "https://www.cloudflare.com/cdn-cgi/trace"
	minStableSamples           = 3
	maxStableSamples           = 12
	minStableEvery             = 1
	maxStableEvery             = 48
	minStableRecheck           = 10
	maxStableRecheck           = 240
	minDoorRate, maxDoorRate   = 10, 100
	minDoorBurst, maxDoorBurst = 30, 400
)

// AIDefault is where «Нейросети» go while the owner chose nothing.
type AIDefault struct {
	// Exits are the provider's locations to try, best first, by the country
	// flags of their names read in order (the last is the exit): ["RU","KZ"]
	// is a location entering in Russia and leaving in Kazakhstan. None:
	// «Нейросети» stay on the running location's own path.
	Exits [][]string `json:"exits"`
}

// Sites are sites for every router, in the form «Мои сайты» takes. They come
// right after the owner's own sites: the owner's choice wins.
type Sites struct {
	Direct []string `json:"direct,omitempty"`
	Proxy  []string `json:"proxy,omitempty"`
}

// Catalog is one catalog document.
type Catalog struct {
	Schema int `json:"schema"`
	// Serial orders catalogs: a router takes only a higher one, so an old
	// signed catalog cannot be served again. 2026101701: the date and a
	// number within the day.
	Serial       int64              `json:"serial"`
	Services     map[string]Service `json:"services,omitempty"`
	Groups       map[string]Group   `json:"groups,omitempty"`
	AIDefault    *AIDefault         `json:"aiDefault,omitempty"`
	Sites        Sites              `json:"sites"`
	RefreshHours int                `json:"refreshHours,omitempty"`
	Tunables     *Tunables          `json:"tunables,omitempty"`
}

var (
	ErrSchema     = errors.New("catalog: unknown schema")
	ErrTooLarge   = errors.New("catalog: too large")
	ErrNotFeedKey = errors.New("catalog: not signed by the Vectra feed's key")
	ErrOlder      = errors.New("catalog: older than the one in use")
	ErrReplay     = errors.New("catalog: same serial as the one in use, other content")
	ErrSame       = errors.New("catalog: already in use")
)

// Parse reads and checks a catalog. strict (the publisher) refuses a field
// this vctl does not know; a router reads a newer catalog's known fields.
func Parse(raw []byte, strict bool) (*Catalog, error) {
	if len(raw) > MaxBytes {
		return nil, ErrTooLarge
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if strict {
		dec.DisallowUnknownFields()
	}
	var c Catalog
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("catalog: data after the document")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

var (
	serviceID   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	geositeName = regexp.MustCompile(`^[a-z0-9][a-z0-9!@._-]{0,63}$`)
	hostName    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	countryCode = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Validate checks every list against its form and cap.
func (c *Catalog) Validate() error {
	if c.Schema != Schema {
		return fmt.Errorf("%w %d (this vctl reads %d)", ErrSchema, c.Schema, Schema)
	}
	if c.Serial <= 0 {
		return errors.New("catalog: serial must be positive")
	}
	if len(c.Services) > maxServices {
		return fmt.Errorf("catalog: %d services, at most %d", len(c.Services), maxServices)
	}
	for id, s := range c.Services {
		if !serviceID.MatchString(id) {
			return fmt.Errorf("catalog: service id %q", id)
		}
		if len(s.Names) > maxNames {
			return fmt.Errorf("catalog: service %s: %d names, at most %d", id, len(s.Names), maxNames)
		}
		for _, n := range s.Names {
			if !validName(n) {
				return fmt.Errorf("catalog: service %s: name %q is not domain:, full: or geosite:", id, n)
			}
		}
		if len(s.Geosites) > maxGeosites {
			return fmt.Errorf("catalog: service %s: %d geosites, at most %d", id, len(s.Geosites), maxGeosites)
		}
		for _, g := range s.Geosites {
			if !geositeName.MatchString(g) {
				return fmt.Errorf("catalog: service %s: geosite %q", id, g)
			}
		}
		if err := s.validateR26(id, c.Groups); err != nil {
			return err
		}
	}
	if len(c.Groups) > maxGroups {
		return fmt.Errorf("catalog: %d groups, at most %d", len(c.Groups), maxGroups)
	}
	for id, g := range c.Groups {
		if !serviceID.MatchString(id) {
			return fmt.Errorf("catalog: group id %q", id)
		}
		if len(g.Title) == 0 {
			return fmt.Errorf("catalog: group %s: no title", id)
		}
		for what, t := range map[string]Title{"title": g.Title, "hint": g.Hint} {
			if err := validTitle(t); err != nil {
				return fmt.Errorf("catalog: group %s: %s: %w", id, what, err)
			}
		}
		if err := validOrderIcon(g.Order, g.Icon); err != nil {
			return fmt.Errorf("catalog: group %s: %w", id, err)
		}
	}
	if err := c.Tunables.validate(); err != nil {
		return err
	}
	if c.AIDefault != nil {
		if len(c.AIDefault.Exits) > maxExits {
			return fmt.Errorf("catalog: aiDefault: %d exits, at most %d", len(c.AIDefault.Exits), maxExits)
		}
		for _, e := range c.AIDefault.Exits {
			if len(e) == 0 || len(e) > maxExitLen {
				return fmt.Errorf("catalog: aiDefault: exit %v: one to %d countries", e, maxExitLen)
			}
			for _, cc := range e {
				if !countryCode.MatchString(cc) {
					return fmt.Errorf("catalog: aiDefault: %q is not a country code", cc)
				}
			}
		}
	}
	for name, list := range map[string][]string{"direct": c.Sites.Direct, "proxy": c.Sites.Proxy} {
		if len(list) > MaxSites {
			return fmt.Errorf("catalog: sites.%s: %d sites, at most %d", name, len(list), MaxSites)
		}
		for _, e := range list {
			if _, err := sites.Parse(e); err != nil {
				return fmt.Errorf("catalog: sites.%s: %q: %w", name, e, err)
			}
		}
	}
	if c.RefreshHours != 0 && (c.RefreshHours < MinRefreshHours || c.RefreshHours > MaxRefreshHours) {
		return fmt.Errorf("catalog: refreshHours %d, %d to %d", c.RefreshHours, MinRefreshHours, MaxRefreshHours)
	}
	return nil
}

var (
	iconName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	geoipRef = regexp.MustCompile(`^geoip:[a-z0-9][a-z0-9@._-]{0,63}$`)
	allModes = map[string]bool{ModeMain: true, ModeStable: true, ModeDirect: true, ModeCountry: true, ModeLocation: true}
	// defaultModes: what a service may do while nobody chose (country needs
	// someone's country).
	defaultModes = map[string]bool{ModeMain: true, ModeStable: true, ModeDirect: true, ModeLocation: true}
)

const (
	maxTitleRunes = 64
	maxOrder      = 1000
)

func (s Service) validateR26(id string, groups map[string]Group) error {
	if err := validTitle(s.Title); err != nil {
		return fmt.Errorf("catalog: service %s: title: %w", id, err)
	}
	if s.Group != "" {
		if _, ok := groups[s.Group]; !ok {
			return fmt.Errorf("catalog: service %s: group %q is not in groups", id, s.Group)
		}
	}
	if err := validOrderIcon(s.Order, s.Icon); err != nil {
		return fmt.Errorf("catalog: service %s: %w", id, err)
	}
	if s.Marker != "" && !validName(s.Marker) {
		return fmt.Errorf("catalog: service %s: marker %q is not domain:, full: or geosite:", id, s.Marker)
	}
	if s.IPMarker != "" && !geoipRef.MatchString(s.IPMarker) && !validNetwork(s.IPMarker) {
		return fmt.Errorf("catalog: service %s: ipMarker %q", id, s.IPMarker)
	}
	if len(s.IPs) > maxIPs {
		return fmt.Errorf("catalog: service %s: %d ips, at most %d", id, len(s.IPs), maxIPs)
	}
	for _, ip := range s.IPs {
		// Addresses and networks of the service's own — never geoip: (a
		// whole country, or a name the router's file may lack) and never
		// wider than /16 (IPv6 /32): a catalog cannot move the internet.
		if !validNetwork(ip) {
			return fmt.Errorf("catalog: service %s: ip %q is not an address or a network of /%d (IPv6 /%d) or narrower", id, ip, minPrefix4, minPrefix6)
		}
	}
	seen := map[string]bool{}
	for _, m := range s.Modes {
		if !allModes[m] || seen[m] {
			return fmt.Errorf("catalog: service %s: mode %q", id, m)
		}
		seen[m] = true
	}
	if s.Default != "" {
		if !defaultModes[s.Default] {
			return fmt.Errorf("catalog: service %s: default %q", id, s.Default)
		}
		if s.Default != ModeMain && len(s.Modes) > 0 && !seen[s.Default] {
			return fmt.Errorf("catalog: service %s: default %q is not one of its modes", id, s.Default)
		}
	}
	if s.Default == ModeDirect {
		// Out of the VPN only by the owner's own choice: a catalog never
		// takes a service out of the tunnel on every router at once.
		return fmt.Errorf("catalog: service %s: «Напрямую» is the owner's choice, never a default", id)
	}
	if (seen[ModeDirect] || seen[ModeStable]) && (s.Marker != "" || s.IPMarker != "") && !s.OnlyKnown {
		// A provider's rule names more than the service (its AI rule held
		// the games): its whole list never goes out directly or elsewhere.
		return fmt.Errorf("catalog: service %s: modes direct and stable need onlyKnown with a marker", id)
	}
	if seen[ModeDirect] {
		if err := s.validateDirectScope(); err != nil {
			return fmt.Errorf("catalog: service %s: «Напрямую»: %w", id, err)
		}
	}
	if len(s.Traffic) > 0 {
		if err := s.validateTraffic(seen); err != nil {
			return fmt.Errorf("catalog: service %s: traffic: %w", id, err)
		}
	}
	return nil
}

const (
	maxTraffic       = 4
	maxTrafficExcept = 64
)

// validateTraffic: a traffic service is a class of the main path's traffic
// and nothing else. It names no site (its rule is the class alone), and it
// offers only the stable exit and the main path: «Напрямую» or a country
// would take most of what is not the web out of the tunnel, or elsewhere,
// on every router at once.
func (s Service) validateTraffic(modes map[string]bool) error {
	if len(s.Traffic) > maxTraffic {
		return fmt.Errorf("%d classes, at most %d", len(s.Traffic), maxTraffic)
	}
	if len(s.Names) > 0 || len(s.IPs) > 0 || len(s.Geosites) > 0 || s.Marker != "" || s.IPMarker != "" || s.OnlyKnown {
		return errors.New("a traffic service names no site (names, ips, geosites, markers, onlyKnown)")
	}
	if len(modes) == 0 {
		return errors.New("modes must be given: stable and main")
	}
	for m := range modes {
		if m != ModeStable && m != ModeMain {
			return fmt.Errorf("mode %q: only stable and main", m)
		}
	}
	nets := map[string]bool{}
	for _, t := range s.Traffic {
		if t.Network != "tcp" && t.Network != "udp" {
			return fmt.Errorf("network %q: tcp or udp", t.Network)
		}
		if nets[t.Network] {
			return fmt.Errorf("network %s twice", t.Network)
		}
		nets[t.Network] = true
		if len(t.Except) > maxTrafficExcept {
			return fmt.Errorf("%s: %d ports left out, at most %d", t.Network, len(t.Except), maxTrafficExcept)
		}
		for _, p := range t.Except {
			if p < 1 || p > 65535 {
				return fmt.Errorf("%s: port %d", t.Network, p)
			}
		}
	}
	return nil
}

// directMaxV4 is how many IPv4 addresses a service on «Напрямую» may cover
// in all (four /16s): what one tap of the owner's takes out of the VPN stays
// what the title says.
const directMaxV4 = 1 << 18

// multiLabelSuffixes are public suffixes of two labels a name of a direct
// service may not be (a «domain:co.uk» is a country's sites).
var multiLabelSuffixes = map[string]bool{
	"co.uk": true, "org.uk": true, "com.ru": true, "net.ru": true, "org.ru": true, "pp.ru": true,
	"com.ua": true, "co.jp": true, "com.au": true, "com.br": true, "com.tr": true, "co.kr": true,
	"com.cn": true, "com.kz": true, "co.in": true, "com.mx": true, "co.za": true, "com.ar": true,
}

// validateDirectScope: a service the owner may take out of the VPN names
// exactly itself — its own domains (two labels at least, never a public
// suffix), no geosite category, its own networks in a bounded sum — and
// knows only that (onlyKnown), so neither the catalog nor the provider's
// rule can widen it.
func (s Service) validateDirectScope() error {
	if !s.OnlyKnown {
		return errors.New("needs onlyKnown")
	}
	if len(s.Geosites) > 0 {
		return errors.New("no geosites")
	}
	for _, n := range s.Names {
		host, ok := strings.CutPrefix(n, "domain:")
		if !ok {
			host, ok = strings.CutPrefix(n, "full:")
		}
		if !ok {
			return fmt.Errorf("name %q: domain: or full: only", n)
		}
		if strings.Count(host, ".") < 1 || multiLabelSuffixes[host] {
			return fmt.Errorf("name %q is a whole domain zone", n)
		}
	}
	var v4 uint64
	for _, ip := range s.IPs {
		bits, is6 := 32, false
		if p, err := netip.ParsePrefix(ip); err == nil {
			bits, is6 = p.Bits(), p.Addr().Is6()
		} else if a, err := netip.ParseAddr(ip); err == nil {
			is6 = a.Is6()
			if is6 {
				bits = 128
			}
		}
		if !is6 {
			v4 += 1 << uint(32-bits)
		}
	}
	if v4 > directMaxV4 {
		return fmt.Errorf("its networks cover %d IPv4 addresses, at most %d", v4, directMaxV4)
	}
	return nil
}

// MatchersHash fingerprints what the service matches as the catalog says it
// (its markers, names, addresses, geosites, onlyKnown): an owner's
// «Напрямую» is bound to it, and a catalog that changes it asks again.
func (s Service) MatchersHash() string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	write(s.Marker, s.IPMarker, fmt.Sprint(s.OnlyKnown))
	for _, list := range [][]string{s.Names, s.IPs, s.Geosites} {
		sorted := append([]string(nil), list...)
		sort.Strings(sorted)
		write(sorted...)
		write("|")
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func validTitle(t Title) error {
	for lang, v := range t {
		switch lang {
		case "ru", "en", "zh":
		default:
			return fmt.Errorf("language %q (ru, en, zh)", lang)
		}
		if v == "" || len([]rune(v)) > maxTitleRunes {
			return fmt.Errorf("%s: 1 to %d characters", lang, maxTitleRunes)
		}
		for _, r := range v {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return fmt.Errorf("%s: a control or format character", lang)
			}
		}
	}
	return nil
}

func validOrderIcon(order int, icon string) error {
	if order < -maxOrder || order > maxOrder {
		return fmt.Errorf("order %d, %d to %d", order, -maxOrder, maxOrder)
	}
	if icon != "" && !iconName.MatchString(icon) {
		return fmt.Errorf("icon %q", icon)
	}
	return nil
}

// The widest network a catalog may name.
const (
	minPrefix4 = 16
	minPrefix6 = 32
)

// validNetwork: an address, or a network no wider than /16 (IPv6 /32).
func validNetwork(v string) bool {
	if p, err := netip.ParsePrefix(v); err == nil {
		min := minPrefix4
		if p.Addr().Is6() {
			min = minPrefix6
		}
		return p == p.Masked() && p.Bits() >= min && !p.Addr().Is4In6()
	}
	a, err := netip.ParseAddr(v)
	return err == nil && !a.Is4In6()
}

func (t *Tunables) validate() error {
	if t == nil {
		return nil
	}
	if s := t.StableExit; s != nil {
		for _, b := range []struct {
			name      string
			v, lo, hi int
		}{
			{"samples", s.Samples, minStableSamples, maxStableSamples},
			{"everyHours", s.EveryHours, minStableEvery, maxStableEvery},
			{"recheckMinutes", s.RecheckMinutes, minStableRecheck, maxStableRecheck},
		} {
			if b.v != 0 && (b.v < b.lo || b.v > b.hi) {
				return fmt.Errorf("catalog: tunables.stableExit.%s %d, %d to %d", b.name, b.v, b.lo, b.hi)
			}
		}
		if s.URL != "" && !validTraceURL(s.URL) {
			return fmt.Errorf("catalog: tunables.stableExit.url %q: https://www.cloudflare.com/… or https://cloudflare.com/… only", s.URL)
		}
	}
	if d := t.Door; d != nil {
		if d.Rate < minDoorRate || d.Rate > maxDoorRate || d.Burst < minDoorBurst || d.Burst > maxDoorBurst || d.Burst < d.Rate {
			return fmt.Errorf("catalog: tunables.door %d/%d: rate %d to %d, burst %d to %d and not below the rate",
				d.Rate, d.Burst, minDoorRate, maxDoorRate, minDoorBurst, maxDoorBurst)
		}
	}
	return nil
}

// validTraceURL: the stable exit's question must answer with ip= — a
// Cloudflare trace, nothing else.
func validTraceURL(u string) bool {
	rest, ok := strings.CutPrefix(u, "https://")
	if !ok {
		return false
	}
	host, path, _ := strings.Cut(rest, "/")
	return (host == "www.cloudflare.com" || host == "cloudflare.com") && path == "cdn-cgi/trace"
}

// validName: domain:<host>, full:<host> or geosite:<name>, lower case.
func validName(n string) bool {
	switch {
	case strings.HasPrefix(n, "domain:"):
		return validHost(strings.TrimPrefix(n, "domain:"))
	case strings.HasPrefix(n, "full:"):
		return validHost(strings.TrimPrefix(n, "full:"))
	case strings.HasPrefix(n, "geosite:"):
		return geositeName.MatchString(strings.TrimPrefix(n, "geosite:"))
	}
	return false
}

func validHost(h string) bool { return len(h) <= 253 && hostName.MatchString(h) }

// Builtin is the catalog this binary carries.
func Builtin() *Catalog {
	c, err := Parse(builtinRaw, true)
	if err != nil {
		panic("catalog: the built-in catalog is invalid: " + err.Error()) // a test keeps it valid
	}
	return c
}

// BuiltinRaw is the built-in catalog's document.
func BuiltinRaw() []byte { return bytes.Clone(builtinRaw) }

// Service is what the catalog says about service id (the zero Service: the
// provider's list as it is, nothing added).
func (c *Catalog) Service(id string) Service {
	if c == nil {
		return Service{}
	}
	return c.Services[id]
}

// ServiceIDs are the services in the UIs' order: by group (its order, then
// id; the top level first), then the service's order, then id.
func (c *Catalog) ServiceIDs() []string {
	if c == nil {
		return nil
	}
	ids := make([]string, 0, len(c.Services))
	for id := range c.Services {
		ids = append(ids, id)
	}
	groupKey := func(id string) (int, int, string) {
		g := c.Services[id].Group
		if g == "" {
			return 0, 0, ""
		}
		return 1, c.Groups[g].Order, g
	}
	sort.Slice(ids, func(i, j int) bool {
		ai, ao, ag := groupKey(ids[i])
		bi, bo, bg := groupKey(ids[j])
		if ai != bi {
			return ai < bi
		}
		if ao != bo {
			return ao < bo
		}
		if ag != bg {
			return ag < bg
		}
		si, sj := c.Services[ids[i]], c.Services[ids[j]]
		if si.Order != sj.Order {
			return si.Order < sj.Order
		}
		return ids[i] < ids[j]
	})
	return ids
}

// LegacyModes are what the four r25 services offer and default to when the
// catalog says nothing of their modes: a catalog published before r26 keeps
// them as r25 had them.
func LegacyModes(id string) (modes []string, def string) {
	switch id {
	case "ai":
		return []string{ModeLocation, ModeMain, ModeCountry}, ModeLocation
	case "youtube", "tiktok", "telegram":
		return []string{ModeMain, ModeCountry, ModeLocation}, ModeMain
	}
	return nil, ModeMain
}

// EffectiveModes are what service id offers and its default (Modes, else
// LegacyModes).
func (s Service) EffectiveModes(id string) (modes []string, def string) {
	if len(s.Modes) == 0 {
		return LegacyModes(id)
	}
	def = s.Default
	if def == "" {
		def = s.Modes[0]
		if !defaultModes[def] || def == ModeDirect {
			def = ModeMain
		}
	}
	if def == ModeDirect {
		def = ModeMain // never a default (validateR26); an older binary's catalog
	}
	return s.Modes, def
}

// Offers: the service offers mode.
func (s Service) Offers(id, mode string) bool {
	modes, _ := s.EffectiveModes(id)
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}

// StableExitSettings are the stable exit's settings, defaults filled.
func (c *Catalog) StableExitSettings() StableExit {
	out := StableExit{Samples: DefaultStableSamples, EveryHours: DefaultStableEvery, RecheckMinutes: DefaultStableRecheck, URL: DefaultStableURL}
	if c == nil || c.Tunables == nil || c.Tunables.StableExit == nil {
		return out
	}
	s := c.Tunables.StableExit
	if s.Samples != 0 {
		out.Samples = s.Samples
	}
	if s.EveryHours != 0 {
		out.EveryHours = s.EveryHours
	}
	if s.RecheckMinutes != 0 {
		out.RecheckMinutes = s.RecheckMinutes
	}
	if s.URL != "" {
		out.URL = s.URL
	}
	out.Main = s.Main
	return out
}

// DoorSettings is the catalog's device door, ok false when it says nothing.
func (c *Catalog) DoorSettings() (Door, bool) {
	if c == nil || c.Tunables == nil || c.Tunables.Door == nil {
		return Door{}, false
	}
	return *c.Tunables.Door, true
}

// RefuseQUIC is the catalog's fleet default for refusing QUIC, ok false when
// it says nothing.
func (c *Catalog) RefuseQUIC() (refuse, ok bool) {
	if c == nil || c.Tunables == nil || c.Tunables.QUIC == nil || c.Tunables.QUIC.Refuse == nil {
		return false, false
	}
	return *c.Tunables.QUIC.Refuse, true
}

// AIExits are the locations «Нейросети» default to, best first.
func (c *Catalog) AIExits() [][]string {
	if c == nil || c.AIDefault == nil {
		return nil
	}
	return c.AIDefault.Exits
}

// Refresh is how often a router asks for a newer catalog, in hours.
func (c *Catalog) Refresh() int {
	if c == nil || c.RefreshHours == 0 {
		return DefaultRefreshHours
	}
	return c.RefreshHours
}

// Knows: matcher (one of a provider's rule) names one of the service's own
// names — a domain: or full: host equal to or under a Names host, or a
// geosite the service lists. A plain string is a substring match in xray
// (plain "x.ai" matches max.airline.com), and keyword:, regexp:, ext: and the
// rest prove nothing about what they match: none of them is known. A parent
// is not known: domain:google.com is not full:gemini.google.com.
func (s Service) Knows(matcher string) bool {
	m := strings.ToLower(strings.TrimSpace(matcher))
	if g, ok := strings.CutPrefix(m, "geosite:"); ok {
		g, _, _ = strings.Cut(g, "@")
		for _, k := range s.Geosites {
			if g == k {
				return true
			}
		}
		for _, n := range s.Names {
			if k, ok := strings.CutPrefix(n, "geosite:"); ok && g == k {
				return true
			}
		}
		return false
	}
	host := ""
	switch {
	case strings.HasPrefix(m, "domain:"):
		host = strings.TrimPrefix(m, "domain:")
	case strings.HasPrefix(m, "full:"):
		host = strings.TrimPrefix(m, "full:")
	default:
		return false
	}
	if host == "" {
		return false
	}
	for _, n := range s.Names {
		base := ""
		switch {
		case strings.HasPrefix(n, "domain:"):
			base = strings.TrimPrefix(n, "domain:")
		case strings.HasPrefix(n, "full:"):
			base = strings.TrimPrefix(n, "full:")
		default:
			continue
		}
		if host == base || strings.HasSuffix(host, "."+base) {
			return true
		}
	}
	return false
}

// Verify checks a downloaded catalog's signature: by a key in keysDir, and
// that key is feedKey — the key whose signature over the Vectra feed's
// Packages the router just verified. That is the trust opkg gives the feed,
// no more: an OpenWrt key in /etc/opkg/keys that does not sign the feed is
// no authority for routing.
func Verify(raw, sig []byte, keysDir string, feedKey feedverify.KeyNumber) error {
	if len(raw) > MaxBytes || len(sig) > MaxSignatureBytes {
		return ErrTooLarge
	}
	k, err := feedverify.VerifyWithKeys(keysDir, raw, sig)
	if err != nil {
		return fmt.Errorf("catalog.json.sig: %w", err)
	}
	if k != feedKey {
		return fmt.Errorf("%w: signed by %s, the feed by %s", ErrNotFeedKey, k, feedKey)
	}
	return nil
}

// Newer: next may replace cur (the catalog in use with its document) —
// a higher serial; the same serial is the same document or a replay.
func Newer(cur *Catalog, curRaw []byte, next *Catalog, nextRaw []byte) error {
	switch {
	case next.Serial > cur.Serial:
		return nil
	case next.Serial < cur.Serial:
		return fmt.Errorf("%w (%d < %d)", ErrOlder, next.Serial, cur.Serial)
	case bytes.Equal(bytes.TrimSpace(curRaw), bytes.TrimSpace(nextRaw)):
		return ErrSame
	default:
		return fmt.Errorf("%w (%d)", ErrReplay, next.Serial)
	}
}

// Source says where the catalog in use comes from.
type Source string

const (
	SourceBuiltin    Source = "built-in"
	SourceDownloaded Source = "downloaded"
)

// Loaded is the catalog in use and where it comes from.
type Loaded struct {
	Catalog *Catalog
	Raw     []byte
	Source  Source
	// Note says why a stored catalog is not in use (unreadable, invalid,
	// older than the built-in one); empty otherwise. Older: the stored one
	// is only older than the release's — usual after an upgrade, no fault.
	Note  string
	Older bool
}

// Load is the catalog in use: the stored one at path when it reads and is
// newer than the built-in one, else the built-in one. It never fails: a
// router always has a catalog.
func Load(path string) Loaded {
	builtin := Loaded{Catalog: Builtin(), Raw: BuiltinRaw(), Source: SourceBuiltin}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return builtin
	}
	if err != nil {
		builtin.Note = err.Error()
		return builtin
	}
	c, err := Parse(raw, false)
	if err != nil {
		builtin.Note = err.Error()
		return builtin
	}
	if q := Quarantined(path); q != 0 && c.Serial <= q {
		builtin.Note = fmt.Sprintf("stored catalog %d broke the render (xray -test); quarantined until a newer one", c.Serial)
		return builtin
	}
	if c.Serial <= builtin.Catalog.Serial {
		builtin.Note = fmt.Sprintf("stored catalog %d is not newer than the built-in %d", c.Serial, builtin.Catalog.Serial)
		builtin.Older = true
		return builtin
	}
	return Loaded{Catalog: c, Raw: raw, Source: SourceDownloaded}
}

// Store writes a verified catalog and its signature next to it, each
// atomically: the signature first, so a catalog on disk never lacks one.
func Store(path string, raw, sig []byte) error {
	if err := writeAtomic(path+".sig", sig); err != nil {
		return err
	}
	return writeAtomic(path, raw)
}

// Quarantine sets serial aside (r26): a catalog whose render xray refused,
// while the built-in catalog's render of the same document passed. Load
// skips it, and a catalog of that serial or below is not taken again; a
// higher one is.
func Quarantine(path string, serial int64) error {
	return writeAtomic(path+".refused", []byte(fmt.Sprintf("%d\n", serial)))
}

// Quarantined is the serial set aside at path, 0 when none.
func Quarantined(path string) int64 {
	raw, err := os.ReadFile(path + ".refused")
	if err != nil {
		return 0
	}
	var n int64
	if _, err := fmt.Sscan(strings.TrimSpace(string(raw)), &n); err != nil || n < 0 {
		return 0
	}
	return n
}

// Remove drops the stored catalog: the built-in one is in use again.
func Remove(path string) error {
	for _, p := range []string{path, path + ".sig"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
