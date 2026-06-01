package xray

import (
	"vectra-controller-pro/internal/config"
)

// Inbound settings types (Xray-native shapes).

type xDokodemoSettings struct {
	Address        string `json:"address,omitempty"`
	Port           int    `json:"port,omitempty"`
	Network        string `json:"network,omitempty"`
	FollowRedirect bool   `json:"followRedirect,omitempty"`
	UserLevel      int    `json:"userLevel,omitempty"`
}

type xSocksInboundSettings struct {
	Auth     string            `json:"auth,omitempty"`
	Accounts []xSocksAccount   `json:"accounts,omitempty"`
	UDP      bool              `json:"udp"`
	IP       string            `json:"ip,omitempty"`
	UserLevel int              `json:"userLevel,omitempty"`
}

type xSocksAccount struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type xHTTPInboundSettings struct {
	Accounts        []xHTTPAccount `json:"accounts,omitempty"`
	AllowTransparent bool          `json:"allowTransparent,omitempty"`
	UserLevel       int            `json:"userLevel,omitempty"`
}

type xHTTPAccount struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type xDNSInboundSettings struct {
	Address string `json:"address,omitempty"`
	Port    int    `json:"port,omitempty"`
	Network string `json:"network,omitempty"`
	UserLevel int  `json:"userLevel,omitempty"`
}

type xSSInboundSettings struct {
	Method   string `json:"method"`
	Password string `json:"password"`
	Network  string `json:"network,omitempty"`
}

func buildInbounds(c *config.Config) []xInbound {
	out := make([]xInbound, 0, 4)

	if t := c.Inbounds.Tproxy; t != nil {
		network := "tcp"
		if t.UDPEnabled {
			network = "tcp,udp"
		}
		ib := xInbound{
			Tag:      t.Tag,
			Listen:   t.ListenIP,
			Port:     t.Port,
			Protocol: "dokodemo-door",
			Settings: xDokodemoSettings{
				Network:        network,
				FollowRedirect: true,
			},
			StreamSettings: &xStreamSettings{
				Sockopt: &xSockopt{TProxy: "tproxy", Mark: t.FwMark},
			},
			Sniffing: convertSniffing(t.Sniffing),
		}
		out = append(out, ib)
	}
	if s := c.Inbounds.Socks; s != nil {
		settings := xSocksInboundSettings{
			Auth: orString(s.Auth, "noauth"),
			UDP:  s.UDP,
			IP:   s.IP,
		}
		if settings.Auth == "password" && s.Username != "" {
			settings.Accounts = []xSocksAccount{{User: s.Username, Pass: s.Password}}
		}
		out = append(out, xInbound{
			Tag:            s.Tag,
			Listen:         s.ListenIP,
			Port:           s.Port,
			Protocol:       "socks",
			Settings:       settings,
			StreamSettings: buildStream(s.Stream),
			Sniffing:       convertSniffing(s.Sniffing),
		})
	}
	if h := c.Inbounds.HTTP; h != nil {
		settings := xHTTPInboundSettings{}
		if h.Username != "" {
			settings.Accounts = []xHTTPAccount{{User: h.Username, Pass: h.Password}}
		}
		out = append(out, xInbound{
			Tag:            h.Tag,
			Listen:         h.ListenIP,
			Port:           h.Port,
			Protocol:       "http",
			Settings:       settings,
			StreamSettings: buildStream(h.Stream),
			Sniffing:       convertSniffing(h.Sniffing),
		})
	}
	if d := c.Inbounds.DNS; d != nil {
		// Address is required by config.Validate — we forbid silently
		// inventing an upstream (no hardcoded 1.1.1.1).
		out = append(out, xInbound{
			Tag:      d.Tag,
			Listen:   d.ListenIP,
			Port:     d.Port,
			Protocol: "dokodemo-door",
			Settings: xDNSInboundSettings{
				Address: d.Address,
				Port:    53,
				Network: orString(d.Network, "tcp,udp"),
			},
		})
	}
	if dk := c.Inbounds.Dokodemo; dk != nil {
		out = append(out, xInbound{
			Tag:      dk.Tag,
			Listen:   dk.ListenIP,
			Port:     dk.Port,
			Protocol: "dokodemo-door",
			Settings: xDokodemoSettings{
				Address:        dk.Address,
				Port:           dk.TargetPort,
				Network:        orString(dk.Network, "tcp"),
				FollowRedirect: dk.FollowRedir,
			},
			Sniffing: convertSniffing(dk.Sniffing),
		})
	}
	if ss := c.Inbounds.Shadowsocks; ss != nil {
		out = append(out, xInbound{
			Tag:      ss.Tag,
			Listen:   ss.ListenIP,
			Port:     ss.Port,
			Protocol: "shadowsocks",
			Settings: xSSInboundSettings{
				Method:   ss.Method,
				Password: ss.Password,
				Network:  orString(ss.Network, "tcp,udp"),
			},
			StreamSettings: buildStream(ss.Stream),
			Sniffing:       convertSniffing(ss.Sniffing),
		})
	}
	if r := c.Inbounds.Reality; r != nil {
		out = append(out, buildRealityInbound(r))
	}
	// Xray API inbound — synthesized if c.API is set with internal listen.
	if c.API != nil && c.API.Listen != "" {
		host, port, ok := splitHostPort(c.API.Listen)
		if ok {
			out = append(out, xInbound{
				Tag:      c.API.Tag,
				Listen:   host,
				Port:     port,
				Protocol: "dokodemo-door",
				Settings: xDokodemoSettings{Address: host, Port: port, Network: "tcp"},
			})
		}
	}
	// Metrics inbound — synthesized if c.Metrics has a listen address, mirroring
	// the API path. Xray's metrics block needs a matching dokodemo-door inbound
	// bound to the same tag to be scrapable.
	if c.Metrics != nil && c.Metrics.Tag != "" && c.Metrics.Listen != "" {
		host, port, ok := splitHostPort(c.Metrics.Listen)
		if ok {
			out = append(out, xInbound{
				Tag:      c.Metrics.Tag,
				Listen:   host,
				Port:     port,
				Protocol: "dokodemo-door",
				Settings: xDokodemoSettings{Address: host, Port: port, Network: "tcp"},
			})
		}
	}
	return out
}

// buildRealityInbound renders a server-side REALITY inbound. Protocol defaults
// to "vless" (the only protocol REALITY pairs with). Settings is an
// operator-supplied passthrough map (e.g. clients[], decryption) — the
// controller does not invent it. Stream typically carries
// security:"reality" with the server-side REALITY fields (privateKey/dest/
// serverNames/shortIds), but the operator owns the whole block.
func buildRealityInbound(r *config.RealityInbound) xInbound {
	return xInbound{
		Tag:            r.Tag,
		Listen:         r.ListenIP,
		Port:           r.Port,
		Protocol:       orString(r.Protocol, "vless"),
		Settings:       r.Settings,
		StreamSettings: buildStream(r.Stream),
		Sniffing:       convertSniffing(r.Sniffing),
	}
}

func convertSniffing(s config.Sniffing) *xSniffing {
	if !s.Enabled {
		return nil
	}
	return &xSniffing{
		Enabled:         true,
		DestOverride:    s.DestOverride,
		DomainsExcluded: s.DomainsExcluded,
		MetadataOnly:    s.MetadataOnly,
		RouteOnly:       s.RouteOnly,
	}
}

func orString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

func splitHostPort(hp string) (string, int, bool) {
	// minimal: host:port (no ipv6 brackets at the API listen field)
	for i := len(hp) - 1; i >= 0; i-- {
		if hp[i] == ':' {
			host := hp[:i]
			port := 0
			for _, c := range hp[i+1:] {
				if c < '0' || c > '9' {
					return "", 0, false
				}
				port = port*10 + int(c-'0')
			}
			if host == "" || port <= 0 || port > 65535 {
				return "", 0, false
			}
			return host, port, true
		}
	}
	return "", 0, false
}
