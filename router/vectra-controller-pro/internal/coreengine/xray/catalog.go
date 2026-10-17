package xray

import (
	"strings"
	"sync/atomic"

	"vectra-controller-pro/internal/catalog"
)

// The catalog in use (internal/catalog): what the services' overlays add and
// keep. Every command that renders or reports services sets it once at
// start (cmd/vctl loads it from disk); the daemon sets it again when a newer
// catalog arrives. Unset, it is the catalog this binary carries.
var activeCatalog atomic.Pointer[catalog.Catalog]

// UseCatalog sets the catalog the splice applies (nil: the built-in one).
func UseCatalog(c *catalog.Catalog) {
	if c == nil {
		c = catalog.Builtin()
	}
	activeCatalog.Store(c)
}

// ActiveCatalog is the catalog the splice applies.
func ActiveCatalog() *catalog.Catalog {
	if c := activeCatalog.Load(); c != nil {
		return c
	}
	c := catalog.Builtin()
	activeCatalog.CompareAndSwap(nil, c)
	return activeCatalog.Load()
}

// knownGeosites are the geosite categories of the router's geo file, lower
// case (nil: not known). The catalog is one for every router, and xray
// refuses a render that names a category its file lacks: a catalog's
// geosite: name the file does not have is left out of the render, as a
// service gone from «Мои сайты» is (cmd/vctl withKnownServices).
var knownGeosites atomic.Pointer[map[string]bool]

// UseGeosites tells the splice which geosite categories the router's geo
// file has; nil: not known, every name is kept and `xray -test` judges.
func UseGeosites(codes []string) {
	if codes == nil {
		knownGeosites.Store(nil)
		return
	}
	m := make(map[string]bool, len(codes))
	for _, c := range codes {
		m[strings.ToLower(c)] = true
	}
	knownGeosites.Store(&m)
}

// catalogNameUsable: a catalog matcher the router's geo file can carry.
func catalogNameUsable(m string) bool {
	name, ok := strings.CutPrefix(strings.ToLower(m), "geosite:")
	if !ok {
		return true
	}
	known := knownGeosites.Load()
	if known == nil {
		return true
	}
	name, _, _ = strings.Cut(name, "@")
	return (*known)[name]
}
