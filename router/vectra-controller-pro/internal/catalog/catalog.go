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
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

	maxServices = 16
	maxNames    = 1000
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
}

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
	AIDefault    *AIDefault         `json:"aiDefault,omitempty"`
	Sites        Sites              `json:"sites"`
	RefreshHours int                `json:"refreshHours,omitempty"`
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
