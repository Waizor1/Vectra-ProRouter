package localctl

import "vectra-controller-pro/internal/catalog"

// ServiceMode is the mode service id runs in under the owner's choices o and
// the catalog c (r26): the owner's choice while c still offers it, else c's
// default. chosen: the owner made a choice; stale: c no longer offers it,
// the default runs meanwhile. «Как основной VPN» is always possible: it is
// the provider's own routing.
func (o Overrides) ServiceMode(id string, c *catalog.Catalog) (mode string, chosen, stale bool) {
	cs := c.Service(id)
	_, def := cs.EffectiveModes(id)
	pick := func(m string) (string, bool, bool) {
		if m == catalog.ModeDirect && o.DirectConsent[id] != cs.MatchersHash() {
			// Chosen for what the service matched then: the catalog changed
			// it since, so the owner is asked again — its default runs.
			return def, true, true
		}
		if m == catalog.ModeMain || cs.Offers(id, m) {
			return m, true, false
		}
		return def, true, true
	}
	switch e := o.ServiceEntries[id]; {
	case o.ServiceModes[id] != "":
		return pick(o.ServiceModes[id])
	case e == ServiceMainPath:
		return catalog.ModeMain, true, false
	case e != "":
		return pick(catalog.ModeLocation)
	case o.Services[id] != "":
		return pick(catalog.ModeCountry)
	}
	return def, false, false
}

// RenderModes are the services whose mode renders a rule of vctl's own —
// direct or stable — for xray.SpliceOptions.ServiceModes.
func (o Overrides) RenderModes(c *catalog.Catalog) map[string]string {
	var out map[string]string
	for _, id := range c.ServiceIDs() {
		if m, _, _ := o.ServiceMode(id, c); m == catalog.ModeDirect || m == catalog.ModeStable {
			if out == nil {
				out = map[string]string{}
			}
			out[id] = m
		}
	}
	return out
}

// StableMainOn: the owner's choice to send the main path through the stable
// exit, else the catalog's default.
func (o Overrides) StableMainOn(c *catalog.Catalog) bool {
	if o.StableMain != nil {
		return *o.StableMain
	}
	return c.StableExitSettings().Main
}

// NeedsStableExit: some service, or the main path, rides the stable exit —
// only then does the router look for one.
func (o Overrides) NeedsStableExit(c *catalog.Catalog) bool {
	if o.StableMainOn(c) {
		return true
	}
	for _, m := range o.RenderModes(c) {
		if m == catalog.ModeStable {
			return true
		}
	}
	return false
}
