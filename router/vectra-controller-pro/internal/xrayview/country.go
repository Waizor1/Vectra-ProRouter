package xrayview

import "strings"

// countryTokens are the tag tokens that name a country. Tokens that are also
// English words or common tag fragments ("in", "it", "at", "am", "ch", "lv"
// for a whitelist level) are deliberately absent: a wrong country is worse
// than none.
var countryTokens = map[string]string{
	"pl": "PL", "de": "DE", "fin": "FI", "fi": "FI", "fr": "FR", "nl": "NL", "us": "US", "usa": "US",
	"tr": "TR", "ae": "AE", "uae": "AE", "ru": "RU", "by": "BY", "kz": "KZ", "uk": "GB", "gb": "GB",
	"se": "SE", "jp": "JP", "sg": "SG", "ge": "GE", "es": "ES", "cz": "CZ", "ee": "EE", "hk": "HK", "kr": "KR",
}

// CountryHint derives an ISO code from a node TAG, and only when a dash-
// separated token is exactly a known country token (optionally followed by
// digits): "bridge-de5" → DE, "hy2-fin5" → FI. "whitelist-lv3" is whitelist
// LEVEL 3, not Latvia, and gets "".
func CountryHint(tag string) string {
	for _, tok := range strings.Split(strings.ToLower(tag), "-") {
		base := strings.TrimRight(tok, "0123456789")
		if cc, ok := countryTokens[base]; ok && base != "" {
			return cc
		}
	}
	return ""
}

// WhitelistLevel: the tag names a whitelist level ("whitelist-lv3",
// "whitelist-lv2-2") — a node for a mobile network under a whitelist regime,
// reached last, through the whitelist stages. On any other connection it
// does not answer, and that is no fault.
func WhitelistLevel(tag string) bool {
	for _, tok := range strings.Split(strings.ToLower(tag), "-") {
		if tok == "whitelist" {
			return true
		}
	}
	return false
}
