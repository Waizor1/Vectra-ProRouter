package brand

import "testing"

func TestTheSubscriptionNamesTheBrandByItsBotThenByItsWholeTitle(t *testing.T) {
	for _, tc := range []struct {
		name, url, title string
		want             ID
		ok               bool
	}{
		{"BloopCat's squad", "https://t.me/BloopCat_bot", "BloopCat ", BloopCat, true},
		{"Vectra Connect's squad", "https://t.me/VectraConnect_bot", "Vectra Connect", Vectra, true},
		{"the bot alone", "https://t.me/bloopcat_bot/app", "", BloopCat, true},
		{"telegram.me", "https://telegram.me/VectraConnect_bot", "", Vectra, true},
		{"the title alone", "", "  vectra connect ", Vectra, true},
		{"no squad: the panel's global title", "", "BloopCat | TriadConnect", "", false},
		{"Triad", "https://t.me/TriadVPN_bot", "TC Premium 12", "", false},
		{"a lookalike host", "https://t.me.evil.example/BloopCat_bot", "", "", false},
		{"not a link", "BloopCat_bot", "", "", false},
		{"a stub", "", "🚨 Подписка истекла", "", false},
	} {
		got, ok := FromSubscription(tc.url, tc.title)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTheSupportLinkIsATelegramName(t *testing.T) {
	for raw, want := range map[string]string{
		"https://t.me/BloopCat_supbot":      "BloopCat_supbot",
		"https://t.me/triadbloop_support/2": "triadbloop_support",
		"https://example.com/support":       "",
		"":                                  "",
		"https://t.me/ab":                   "",
	} {
		if got := SupportFromURL(raw); got != want {
			t.Errorf("SupportFromURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseKnowsOnlyTheRegisteredBrands(t *testing.T) {
	for s, want := range map[string]ID{"vectra": Vectra, " BloopCat ": BloopCat, "triad": "", "": ""} {
		got, _ := Parse(s)
		if got != want {
			t.Errorf("Parse(%q) = %q, want %q", s, got, want)
		}
	}
	b, ok := Lookup(BloopCat)
	if !ok || b.Name != "BloopCat" || b.SSIDPrefix != "BloopCat" || b.LANName != "bloopcat.lan" || b.Bot != "BloopCat_bot" || b.Support != "BloopCat_supbot" || b.Site != "" {
		t.Fatalf("BloopCat: %+v", b)
	}
	if v, _ := Lookup(Vectra); v.Site != "my.vectra-pro.net" || v.LANName != "vectra.lan" {
		t.Fatalf("Vectra: %+v", v)
	}
}

func TestTheSubscriptionOutranksTheClaimWhichOutranksTheInstaller(t *testing.T) {
	for _, tc := range []struct {
		name     string
		held     ID
		heldFrom Source
		seen     ID
		from     Source
		want     bool
	}{
		{"nothing held", "", "", BloopCat, SourceInstall, true},
		{"the claim over the installer", Vectra, SourceInstall, BloopCat, SourceClaim, true},
		{"the subscription over the claim", Vectra, SourceClaim, BloopCat, SourceSubscription, true},
		{"the claim does not undo the subscription", BloopCat, SourceSubscription, Vectra, SourceClaim, false},
		{"the installer does not undo the claim", BloopCat, SourceClaim, Vectra, SourceInstall, false},
		{"a later subscription changes it", BloopCat, SourceSubscription, Vectra, SourceSubscription, true},
		{"the same brand from a weaker source", BloopCat, SourceSubscription, BloopCat, SourceClaim, false},
		{"an unknown brand", Vectra, SourceInstall, "triad", SourceSubscription, false},
	} {
		if got := Adopt(tc.held, tc.heldFrom, tc.seen, tc.from); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTheNeutralNetworkIsNamedAfterTheModel(t *testing.T) {
	for model, want := range map[string]string{
		"Xiaomi Mi Router AX3000T":                         "AX3000T",
		"Xiaomi Mi Router AX3000T (OpenWrt U-Boot layout)": "AX3000T",
		"Cudy WR3000E v1":                                  "WR3000E",
		"GL.iNet GL-MT6000":                                "GL-MT6000",
		"Linksys E8450 (UBI)":                              "E8450",
		"Bananapi BPI-R3":                                  "BPI-R3",
		"":                                                 "Router",
		"Generic":                                          "Router",
	} {
		if got := ModelPrefix(model); got != want {
			t.Errorf("ModelPrefix(%q) = %q, want %q", model, got, want)
		}
	}
}
