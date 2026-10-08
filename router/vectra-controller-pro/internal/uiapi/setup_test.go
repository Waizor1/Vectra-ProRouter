package uiapi

import (
	"testing"

	"vectra-controller-pro/internal/brand"
	"vectra-controller-pro/internal/setup"
)

// status.brand: every field of a known brand, the subscription's support bot
// over the brand's own; neutral is nulls and router.lan.
func TestTheBrandViewNamesTheServiceOrNothing(t *testing.T) {
	vectra, _ := brand.Lookup(brand.Vectra)
	v := BuildBrand(Who{Brand: vectra, Known: true})
	if *v.ID != "vectra" || *v.Name != "Vectra" || *v.Bot != "VectraConnect_bot" || *v.Support != "VectraConnect_support_bot" ||
		v.LANName != "vectra.lan" || *v.Site != "my.vectra-pro.net" {
		t.Fatalf("Vectra: %+v", v)
	}
	bloopcat, _ := brand.Lookup(brand.BloopCat)
	if v := BuildBrand(Who{Brand: bloopcat, Known: true, Support: "BloopCat_help_bot"}); *v.Support != "BloopCat_help_bot" || v.Site != nil {
		t.Fatalf("BloopCat with the subscription's support: %+v", v)
	}
	if v := BuildBrand(Who{NeutralPrefix: "AX3000T"}); v != (BrandView{LANName: "router.lan"}) {
		t.Fatalf("neutral: %+v", v)
	}
	// A status built without a brand (an older caller) is neutral, never an
	// empty lanName.
	if st := BuildStatus(Inputs{}); st.Brand != (BrandView{LANName: "router.lan"}) {
		t.Fatalf("status without a brand: %+v", st.Brand)
	}
}

// setup.wifi.rename: the brand's name, offered only while every access point
// still has the model's — never for a neutral router, a router with no access
// point, or one whose suggested name is unknown.
func TestTheBrandsNetworkNameIsOfferedOnlyOverTheModels(t *testing.T) {
	bloopcat, _ := brand.Lookup(brand.BloopCat)
	who := Who{Brand: bloopcat, Known: true, NeutralPrefix: "AX3000T"}
	ap := func(ssid string) setup.Radio { return setup.Radio{Device: "radio0", SSID: ssid, AP: true} }
	for _, tc := range []struct {
		name   string
		who    Who
		wifi   setup.Wifi
		rename string
	}{
		{"every access point has the model's name", who,
			setup.Wifi{Suggested: "BloopCat-3F2A", Radios: []setup.Radio{ap("AX3000T-3F2A"), ap("AX3000T-3F2A"), {Device: "radio2"}}}, "BloopCat-3F2A"},
		{"the owner renamed one", who, setup.Wifi{Suggested: "BloopCat-3F2A", Radios: []setup.Radio{ap("AX3000T-3F2A"), ap("Home")}}, ""},
		{"no access point", who, setup.Wifi{Suggested: "BloopCat-3F2A", Radios: []setup.Radio{{Device: "radio0"}}}, ""},
		{"no suggested name", who, setup.Wifi{Radios: []setup.Radio{ap("AX3000T-3F2A")}}, ""},
		{"a neutral router", Who{NeutralPrefix: "AX3000T"}, setup.Wifi{Suggested: "AX3000T-3F2A", Radios: []setup.Radio{ap("AX3000T-3F2A")}}, ""},
		{"the model's name alone, not a prefix of a word", who, setup.Wifi{Suggested: "BloopCat-3F2A", Radios: []setup.Radio{ap("AX3000TX")}}, ""},
	} {
		s := BuildSetup(setup.Facts{Wifi: tc.wifi}, nil, false, "", nil, tc.who)
		got := ""
		if s.Wifi.Rename != nil {
			got = *s.Wifi.Rename
		}
		if got != tc.rename {
			t.Errorf("%s: rename %q, want %q", tc.name, got, tc.rename)
		}
	}
}
