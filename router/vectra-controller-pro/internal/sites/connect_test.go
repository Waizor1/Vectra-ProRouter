package sites

import "testing"

func TestConnectDomainGrammar(t *testing.T) {
	for _, s := range []string{"*.example.org", ".example.org", "example.org.", "тест.рф", "_service.example", "example.org"} {
		if _, err := ParseConnectDomain(s); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
	}
	for _, s := range []string{"https://example.org", "1.2.3.4", "bad..example", "exa$mple.example", "_service.exa$mple", "_service..example", "example.org/path", "geosite:youtube"} {
		if _, err := ParseConnectDomain(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
