package main

// The upstream releases Vectra's geo lists are built from. Together they
// reproduce the files vectra-geodata shipped by hand (2026.9.28-r2):
//
//   - geosite.dat: itdoginfo/allow-domains whole (anime … youtube: the
//     Russian-blocking lists the provider's routing and «Мои сайты» name),
//     then CATEGORY-GOV-RU, CATEGORY-RU and PRIVATE from Loyalsoldier's
//     v2ray-rules-dat, which builds them from v2fly/domain-list-community
//     (the shipped records are Loyalsoldier's byte for byte).
//   - geoip.dat: hydraponique/roscomvpn-geoip whole (DIRECT — Russian
//     networks to send straight out — PRIVATE, WHITELIST), then RU and
//     TELEGRAM from Loyalsoldier's v2ray-rules-dat (RU from MaxMind
//     GeoLite2 Country; the shipped TELEGRAM is Loyalsoldier's byte for
//     byte).
//
// A URL is the release's latest/download link, so every build takes the
// newest release; the manifest records the tagged URL it resolved to and the
// sha256 of what was read. A release that publishes its own checksum is
// checked against it, read from the same tagged release.
var pinned = []source{
	{
		Name: "itdoginfo/allow-domains geosite.dat",
		File: siteFile,
		URL:  "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat",
		Max:  4 << 20,
	},
	{
		Name:  "Loyalsoldier/v2ray-rules-dat geosite.dat",
		File:  siteFile,
		URL:   "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
		Sum:   ".sha256sum",
		Max:   64 << 20,
		Codes: []string{"CATEGORY-GOV-RU", "CATEGORY-RU", "PRIVATE"},
	},
	{
		Name: "hydraponique/roscomvpn-geoip geoip.dat",
		File: ipFile,
		URL:  "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat",
		Sum:  ".sha256",
		Max:  16 << 20,
	},
	{
		Name:  "Loyalsoldier/v2ray-rules-dat geoip.dat",
		File:  ipFile,
		URL:   "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
		Sum:   ".sha256sum",
		Max:   64 << 20,
		Codes: []string{"RU", "TELEGRAM"},
	},
}

// The files a build writes and what each must hold whatever the previous
// build had: the categories vectra-geodata 2026.9.28-r2 shipped. Max keeps
// an upstream that suddenly balloons off a 234 MB router (xray reads the
// file it routes by); the shipped files were 124 KB and 822 KB.
var outputs = []output{
	{
		Name:   siteFile,
		Prefix: "geosite",
		Max:    1 << 20,
		Required: []string{
			// itdoginfo/allow-domains
			"ANIME", "BLOCK", "CLOUDFLARE", "CLOUDFRONT", "DIGITALOCEAN", "DISCORD",
			"GEOBLOCK", "GOOGLE-AI", "GOOGLE-MEET", "GOOGLE-PLAY", "HDREZKA", "HETZNER",
			"HODCA", "META", "NEWS", "OVH", "PORN", "ROBLOX", "RUSSIA-INSIDE",
			"RUSSIA-OUTSIDE", "TELEGRAM", "TIKTOK", "TWITTER", "UKRAINE-INSIDE", "YOUTUBE",
			// v2fly/domain-list-community, as Loyalsoldier builds it
			"CATEGORY-GOV-RU", "CATEGORY-RU", "PRIVATE",
		},
	},
	{
		Name:     ipFile,
		Prefix:   "geoip",
		Max:      2 << 20,
		Required: []string{"DIRECT", "PRIVATE", "WHITELIST", "RU", "TELEGRAM"},
	},
}
