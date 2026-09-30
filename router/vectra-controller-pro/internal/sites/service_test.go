package sites

import (
	"reflect"
	"testing"
)

// «+ сервис» (spec decision 4): a service is a category of the router's geo
// file, kept as "geosite:<category>" in either list and matched by xray as
// such. Its name is the file's, lowercased; whether the file has it is the
// router's to check (rpcd), not the parser's.
func TestAServiceIsAGeoCategory(t *testing.T) {
	for in, want := range map[string]string{
		"geosite:discord":         "geosite:discord",
		" GeoSite:Google-AI ":     "geosite:google-ai",
		"geosite:category-gov-ru": "geosite:category-gov-ru",
	} {
		s, err := Parse(in)
		if err != nil || s.Display != want || s.Service == "" || !s.Domain || s.Matcher() != want {
			t.Fatalf("Parse(%q) = %+v, %v; want service %s", in, s, err, want)
		}
	}
	for _, bad := range []string{"geosite:", "geosite:dis cord", "geosite:../x", "geosite:a/b", "geosite:" + string(make([]byte, 65))} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("Parse(%q) accepted", bad)
		}
	}
}

// A service may hold any domain, so an explicit site of the other list is
// always the more specific: it goes first, and wins. Two services never
// cover each other.
func TestAServiceGivesWayToTheOwnersOwnSites(t *testing.T) {
	svc, _ := Parse("geosite:discord")
	dom, _ := Parse("discord.com")
	ip, _ := Parse("1.2.3.0/24")
	other, _ := Parse("geosite:meta")
	if !svc.Covers(dom) || dom.Covers(svc) || svc.Covers(ip) || svc.Covers(other) || !svc.Covers(svc) {
		t.Fatal("service coverage wrong")
	}
	if svc.Specificity() >= dom.Specificity() {
		t.Fatalf("a service (%d) is not less specific than a domain (%d)", svc.Specificity(), dom.Specificity())
	}
}

func TestListsKeepServicesLikeSites(t *testing.T) {
	d, p, err := NormalizeLists([]string{"geosite:Discord", "bank.example"}, []string{"geosite:meta", "geosite:discord "})
	if err == nil {
		t.Fatalf("a service in both lists was accepted: %v %v", d, p)
	}
	d, p, err = NormalizeLists([]string{"geosite:Discord", "bank.example"}, []string{"geosite:meta"})
	if err != nil || !reflect.DeepEqual(d, []string{"geosite:discord", "bank.example"}) || !reflect.DeepEqual(p, []string{"geosite:meta"}) {
		t.Fatalf("lists %v %v %v", d, p, err)
	}
}
