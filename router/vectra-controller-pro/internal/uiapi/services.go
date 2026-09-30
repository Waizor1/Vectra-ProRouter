package uiapi

import (
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
}

// BuildServices reads the running render, the owner's choices and where the
// router saw each exit leave.
func BuildServices(available bool, render []byte, ov localctl.Overrides, egress map[string]string) Services {
	out := Services{Available: available, Services: []ServiceInfo{}}
	if !available {
		return out
	}
	sc := xray.ServiceCountries(render)
	countries := nonNil(sc.Countries)
	view, _ := xrayview.Parse(render)
	elsewhere := leavesElsewhere(view, egress)
	for _, s := range xray.Services {
		info := ServiceInfo{ID: s.ID, Countries: []string{}, Egress: map[string]string{}}
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
