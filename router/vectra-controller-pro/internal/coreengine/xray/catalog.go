package xray

import (
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
