package config

// Base is the operator config a router has before the panel sends its own:
// what the panel's buildXrayOperatorConfig gives every router
// (apps/web/src/server/vectra/xray-operator-config.ts — the golden file
// testdata/panel/operator-config.json is its output) without the
// subscription, which only the panel knows. vctl runs on it while it routes
// by PassWall2's configuration on its own, before it is linked
// (cmd/vctl/auto_route.go): the TPROXY inbound, its mark and sniffing, the
// geo files — PassWall2's generator brings the rest, its DNS included.
//
// It is never written to disk: the operator config on /etc is the panel's
// alone, and its absence is what says the router is not linked yet.
func Base(name string) *Config {
	c := &Config{
		Schema:   SchemaVersion,
		Instance: Instance{Name: name, LogLevel: "warning"},
		Process: Process{
			XrayBinary:    "/usr/bin/xray",
			WorkDir:       "/var/run/vectra-controller-pro",
			MemorySoftMiB: 80,
			OOMScoreAdj:   -500,
			RestartBackoff: Backoff{
				InitialMs: 500,
				Factor:    2,
				MaxMs:     60_000,
				Reset:     "60s",
			},
			ReloadGrace:  "5s",
			StartTimeout: "15s",
		},
		Inbounds: Inbounds{Tproxy: &TproxyInbound{
			ListenIP:   "0.0.0.0",
			Port:       12345,
			FwMark:     1,
			UDPEnabled: true,
			Tag:        "tproxy-in",
			Sniffing: Sniffing{
				Enabled:      true,
				DestOverride: []string{"http", "tls", "quic"},
				RouteOnly:    true,
			},
		}},
		Geo: Geo{
			AssetDir:   LegacyGeoAssetDir,
			GeoIPURL:   "https://github.com/hydraponique/roscomvpn-geoip/releases/latest/download/geoip.dat",
			GeoSiteURL: "https://github.com/itdoginfo/allow-domains/releases/latest/download/geosite.dat",
		},
	}
	ApplyDefaults(c)
	return c
}
