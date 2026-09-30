package uaguard

import "testing"

func TestCheckRefusesEveryHappThatIsNotTheRealClient(t *testing.T) {
	for _, ua := range []string{
		"Happ/1.0", // the agent this codebase used to document
		"Happ",
		"happ/1.0",
		"HAPP/4.2.1/Windows/2609041405606", // right shape, wrong casing
		"Happ/4.2.1/Windows",
		"Happ/4.2.1/Windows/",
		"Happ//Windows/2609041405606",
		"Happ/4.2.1/Windows/2609041405606/extra",
		"Happ/4.2.1/Win dows/2609041405606",
		" Happ/4.2.1/Windows/2609041405606",
		"Happ/4.2.1/Windows/2609041405606\n",
		"HappVPN/2.0",
		"Happ/1.0/OpenWrt/1",            // four parts, no real build
		"Happ/latest/Windows/123456789", // not a version
		"Happ/4.2.1/Win-dows/2609041405606",
	} {
		if err := Check(ua); err == nil {
			t.Errorf("Check(%q) = nil, want a refusal", ua)
		}
	}
}

func TestCheckLetsTheRealClientShapeAndOtherAgentsThrough(t *testing.T) {
	for _, ua := range []string{
		"Happ/4.2.1/Windows/2609041405606",
		"Happ/3.9.0/Android/1712345678",
		"v2rayNG/1.9.5",
		"passwall2/26.8.10",
		"",
		"Go-http-client/1.1",
	} {
		if err := Check(ua); err != nil {
			t.Errorf("Check(%q) = %v, want nil", ua, err)
		}
	}
}

func TestFromHeadersMatchesTheKeyCaseInsensitively(t *testing.T) {
	ua, ok := FromHeaders(map[string]string{"x-hwid": "h", "user-agent": "Happ/1.0"})
	if !ok || ua != "Happ/1.0" {
		t.Fatalf("FromHeaders = %q, %v; want the lower-case key's value", ua, ok)
	}
	if _, ok := FromHeaders(map[string]string{"x-hwid": "h"}); ok {
		t.Fatal("FromHeaders reported a User-Agent in a map that sets none")
	}
}

// The router's own agent is made by the router for every request; written
// into a config or a header it is a copy, which is what a forger sends.
func TestCheckConfiguredRefusesACopyOfTheRoutersOwnAgent(t *testing.T) {
	for _, ua := range []string{
		"VectraRouter/0.5.0 (AX3000T)",
		"vectrarouter/0.5.0 vr1.AQID",
		" VectraRouter/1",
		"Happ/1.0", // Check's refusals still hold
	} {
		if err := CheckConfigured(ua); err == nil {
			t.Errorf("CheckConfigured(%q) = nil, want a refusal", ua)
		}
	}
	for _, ua := range []string{"", "v2rayNG/1.9.5", "passwall2/26.8.10", "Happ/4.2.1/Windows/2609041405606", "NotVectraRouter/1"} {
		if err := CheckConfigured(ua); err != nil {
			t.Errorf("CheckConfigured(%q) = %v, want nil", ua, err)
		}
	}
	// On the wire the router's own passes Check: it made it just now.
	if err := Check("VectraRouter/0.5.0 vr1.AQID"); err != nil {
		t.Errorf("Check of the router's own agent = %v, want nil", err)
	}
}
