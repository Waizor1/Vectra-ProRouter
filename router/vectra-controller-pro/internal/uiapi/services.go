package uiapi

import (
	"sort"
	"time"

	"vectra-controller-pro/internal/catalog"
	"vectra-controller-pro/internal/coreengine/xray"
	"vectra-controller-pro/internal/localctl"
	"vectra-controller-pro/internal/xrayview"
)

// Services answers `services`: the few services with a country of their own
// (xray.Services), as the running entry offers them.
type Services struct {
	// Available is false where the router routes by the operator's policy
	// (native, PassWall): there is no subscription entry to choose from.
	Available bool          `json:"available"`
	Services  []ServiceInfo `json:"services"`
	// Groups are the catalog's headings («Игры»), in its order (r26).
	Groups []GroupInfo `json:"groups"`
	// StableExit is the stable exit (r26); null when the daemon did not say.
	StableExit *StableExitInfo `json:"stableExit"`
}

// GroupInfo is a heading the UI puts services under.
type GroupInfo struct {
	ID    string            `json:"id"`
	Title map[string]string `json:"title"`
	Order int               `json:"order"`
	Icon  string            `json:"icon"`
	Hint  map[string]string `json:"hint"`
}

// StableExitInfo: pinned (Country is where it leaves), none, checking or
// off; Main: the whole main path rides it. Never the address.
type StableExitInfo struct {
	State     string  `json:"state"`
	Country   *string `json:"country"`
	CheckedAt *string `json:"checkedAt"`
	Main      bool    `json:"main"`
}

// ServiceInfo is one service.
type ServiceInfo struct {
	ID string `json:"id"`
	// Choice is the owner's country; null = the entry's own path.
	Choice *string `json:"choice"`
	// DefaultCountry is the country of the entry's own path; null when it
	// names none or several.
	DefaultCountry *string `json:"defaultCountry"`
	// Countries are the ISO codes the running entry's outbounds name; empty
	// when the entry has no path of the service's own to fall back to.
	Countries []string `json:"countries"`
	// Active: the choice runs now. Stale: a choice the running render does
	// not carry — the entry lost the country or the service's own path; the
	// service is on its default.
	Active bool `json:"active"`
	Stale  bool `json:"stale"`
	// Egress names, for an offered country whose every exit the router saw
	// leave in one other country, that country (1111, 2026-09-30: TikTok
	// «through Turkey» was Poland).
	Egress map[string]string `json:"egress"`

	// r26: what the catalog says the service is. Title in ru/en/zh (empty:
	// an r25 service the UI knows by heart); Modes ⊂ main, stable, direct,
	// country, location; Mode what runs now; Default what «Авто» means;
	// Chosen: the owner made a choice.
	Title   map[string]string `json:"title"`
	Group   string            `json:"group"`
	Order   int               `json:"order"`
	Icon    string            `json:"icon"`
	Modes   []string          `json:"modes"`
	Mode    string            `json:"mode"`
	Default string            `json:"default"`
	Chosen  bool              `json:"chosen"`
}

// BuildServices reads the running render, the owner's choices and where the
// router saw each exit leave.
func BuildServices(available bool, render []byte, ov localctl.Overrides, egress map[string]string, stable *localctl.StableExit) Services {
	out := Services{Available: available, Services: []ServiceInfo{}, Groups: []GroupInfo{}}
	if !available {
		return out
	}
	cat := xray.ActiveCatalog()
	for _, id := range groupIDs(cat) {
		g := cat.Groups[id]
		out.Groups = append(out.Groups, GroupInfo{ID: id, Title: nonNilMap(g.Title), Order: g.Order, Icon: g.Icon, Hint: nonNilMap(g.Hint)})
	}
	if stable != nil {
		se := &StableExitInfo{State: stable.State, Main: ov.StableMainOn(cat)}
		if stable.Country != "" {
			cc := stable.Country
			se.Country = &cc
		}
		if stable.CheckedAt != nil {
			at := stable.CheckedAt.UTC().Format(time.RFC3339)
			se.CheckedAt = &at
		}
		out.StableExit = se
	}
	sc := xray.ServiceCountries(render)
	countries := nonNil(sc.Countries)
	view, _ := xrayview.Parse(render)
	elsewhere := leavesElsewhere(view, egress)
	for _, s := range xray.Services() {
		info := ServiceInfo{ID: s.ID, Countries: []string{}, Egress: map[string]string{}, Title: map[string]string{}, Modes: []string{}}
		cs := cat.Service(s.ID)
		modes, def := cs.EffectiveModes(s.ID)
		info.Title, info.Group, info.Order, info.Icon = nonNilMap(cs.Title), cs.Group, cs.Order, cs.Icon
		info.Modes, info.Default = append([]string{}, modes...), def
		info.Mode, info.Chosen, _ = ov.ServiceMode(s.ID, cat)
		if sc.Offered[s.ID] {
			info.Countries = countries
			for _, cc := range countries {
				if e := elsewhere[cc]; e != "" {
					info.Egress[cc] = e
				}
			}
		}
		if cc, ok := sc.Defaults[s.ID]; ok {
			info.DefaultCountry = &cc
		}
		if cc := ov.Services[s.ID]; cc != "" {
			c := cc
			info.Choice = &c
			// The choice is kept only once a render carrying it runs, so a
			// choice without its overlay in the render is not running.
			info.Active = view != nil && view.Balancer("VCTL-SVC-"+upper(s.ID)) != nil
			info.Stale = !info.Active
		}
		out.Services = append(out.Services, info)
	}
	return out
}

// leavesElsewhere: for each country the render's dialling outbounds name,
// the one other country all of them were seen leaving in — none when one is
// not known yet, or they leave where the name says, or in several.
func leavesElsewhere(view *xrayview.View, egress map[string]string) map[string]string {
	out := map[string]string{}
	if view == nil || len(egress) == 0 {
		return out
	}
	seen := map[string]string{}
	bad := map[string]bool{}
	for _, o := range view.Outbounds {
		cc := xrayview.CountryHint(o.Tag)
		if !o.Dials || cc == "" || bad[cc] {
			continue
		}
		e := egress[o.Tag]
		switch {
		case e == "" || e == cc:
			bad[cc] = true
		case seen[cc] == "":
			seen[cc] = e
		case seen[cc] != e:
			bad[cc] = true
		}
	}
	for cc, e := range seen {
		if !bad[cc] {
			out[cc] = e
		}
	}
	return out
}

func upper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

// groupIDs are the catalog's groups in its order.
func groupIDs(c *catalog.Catalog) []string {
	ids := make([]string, 0, len(c.Groups))
	for id := range c.Groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		gi, gj := c.Groups[ids[i]], c.Groups[ids[j]]
		if gi.Order != gj.Order {
			return gi.Order < gj.Order
		}
		return ids[i] < ids[j]
	})
	return ids
}

func nonNilMap(t catalog.Title) map[string]string {
	if t == nil {
		return map[string]string{}
	}
	return map[string]string(t)
}
