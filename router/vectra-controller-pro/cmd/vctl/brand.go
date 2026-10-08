package main

import (
	"vectra-controller-pro/internal/brand"
	"vectra-controller-pro/internal/logging"
	"vectra-controller-pro/internal/state"
	"vectra-controller-pro/internal/subscription"
)

// noteBrand records a brand the router learned, when it outranks the one
// held (brand.Adopt). The subscription's support bot comes along with its
// brand. True when anything changed.
func noteBrand(st *state.PersistedState, id brand.ID, from brand.Source, support string) bool {
	changed := false
	if brand.Adopt(brand.ID(st.Brand), brand.Source(st.BrandSource), id, from) &&
		(st.Brand != string(id) || st.BrandSource != string(from)) {
		if st.Brand != string(id) {
			st.BrandSupport = ""
		}
		st.Brand, st.BrandSource = string(id), string(from)
		changed = true
	}
	// The support bot belongs to the brand held: with none held (an
	// unrecognised subscription) there is nothing for it to belong to.
	if from == brand.SourceSubscription && support != "" && st.Brand != "" && st.Brand == string(id) && st.BrandSupport != support {
		st.BrandSupport = support
		changed = true
	}
	return changed
}

// brandFromFetch: the brand and support bot a fetched subscription names.
func brandFromFetch(fr *subscription.FetchResult) (brand.ID, string, bool) {
	if fr == nil {
		return "", "", false
	}
	id, ok := brand.FromSubscription(fr.ProfileWebPageURL, fr.ProfileTitle)
	if !ok {
		return "", "", false
	}
	return id, brand.SupportFromURL(fr.SupportURL), true
}

// forgetBrand: the owner unbound the router — its next owner's word decides.
func forgetBrand(st *state.PersistedState) {
	st.Brand, st.BrandSource, st.BrandSupport = "", "", ""
}

// learnBrand is noteBrand on the daemon's state, persisted when it changed.
func (d *daemon) learnBrand(id brand.ID, from brand.Source, support string) {
	if !noteBrand(&d.st, id, from, support) {
		return
	}
	logging.L().Info("the router's brand", "brand", d.st.Brand, "from", d.st.BrandSource)
	if err := d.persist(); err != nil {
		logging.L().Warn("persist the router's brand", "err", err.Error())
	}
}
