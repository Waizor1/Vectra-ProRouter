package xray

import (
	"vectra-controller-pro/internal/config"
)

// Outbound settings types.

type xVLESSOutbound struct {
	Vnext []xVLESSServer `json:"vnext"`
}
type xVLESSServer struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []xVLESSUser `json:"users"`
}
type xVLESSUser struct {
	ID         string `json:"id"`
	Flow       string `json:"flow,omitempty"`
	Encryption string `json:"encryption,omitempty"`
	Level      int    `json:"level,omitempty"`
}

type xVMessOutbound struct {
	Vnext []xVMessServer `json:"vnext"`
}
type xVMessServer struct {
	Address string       `json:"address"`
	Port    int          `json:"port"`
	Users   []xVMessUser `json:"users"`
}
type xVMessUser struct {
	ID       string `json:"id"`
	Security string `json:"security,omitempty"`
	AlterID  int    `json:"alterId,omitempty"`
	Level    int    `json:"level,omitempty"`
}

type xTrojanOutbound struct {
	Servers []xTrojanServer `json:"servers"`
}
type xTrojanServer struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
	Level    int    `json:"level,omitempty"`
}

type xSSOutbound struct {
	Servers []xSSServer `json:"servers"`
}
type xSSServer struct {
	Address    string `json:"address"`
	Port       int    `json:"port"`
	Method     string `json:"method"`
	Password   string `json:"password"`
	UoT        bool   `json:"uot,omitempty"`
	UoTVersion int    `json:"uotVersion,omitempty"`
	Level      int    `json:"level,omitempty"`
}

type xHy2Outbound struct {
	Servers []xHy2Server `json:"servers"`
}
type xHy2Server struct {
	Address               string     `json:"address"`
	Port                  int        `json:"port"`
	Password              string     `json:"password,omitempty"`
	Obfs                  *xHy2Obfs  `json:"obfs,omitempty"`
	HopPorts              string     `json:"hopPorts,omitempty"`
	HopInterval           int        `json:"hopInterval,omitempty"`
	Up                    int        `json:"up,omitempty"`
	Down                  int        `json:"down,omitempty"`
	IgnoreClientBandwidth bool       `json:"ignoreClientBandwidth,omitempty"`
}

type xHy2Obfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

type xSocksOutbound struct {
	Servers []xSocksServer `json:"servers"`
}
type xSocksServer struct {
	Address string         `json:"address"`
	Port    int            `json:"port"`
	Users   []xSocksOutboundUser `json:"users,omitempty"`
	UDPOverTCP bool        `json:"udpOverTcp,omitempty"`
}
type xSocksOutboundUser struct {
	User  string `json:"user"`
	Pass  string `json:"pass"`
	Level int    `json:"level,omitempty"`
}

type xHTTPOutbound struct {
	Servers []xHTTPServer `json:"servers"`
}
type xHTTPServer struct {
	Address string             `json:"address"`
	Port    int                `json:"port"`
	Users   []xHTTPOutboundUser `json:"users,omitempty"`
	Headers map[string]string  `json:"headers,omitempty"`
}
type xHTTPOutboundUser struct {
	User string `json:"user"`
	Pass string `json:"pass"`
}

type xWGOutbound struct {
	SecretKey      string   `json:"secretKey"`
	Address        []string `json:"address"`
	Peers          []xWGPeer `json:"peers"`
	MTU            int      `json:"mtu,omitempty"`
	Workers        int      `json:"workers,omitempty"`
	Reserved       []int    `json:"reserved,omitempty"`
	DomainStrategy string   `json:"domainStrategy,omitempty"`
	KernelMode     bool     `json:"kernelMode,omitempty"`
}
type xWGPeer struct {
	PublicKey    string   `json:"publicKey"`
	PreSharedKey string   `json:"preSharedKey,omitempty"`
	Endpoint     string   `json:"endpoint"`
	AllowedIPs   []string `json:"allowedIPs,omitempty"`
	KeepAlive    int      `json:"keepAlive,omitempty"`
}

type xFreedomOutbound struct {
	DomainStrategy string         `json:"domainStrategy,omitempty"`
	Redirect       string         `json:"redirect,omitempty"`
	Fragment       *xFragment     `json:"fragment,omitempty"`
	Noises         []xNoise       `json:"noises,omitempty"`
	ProxyProtocol  int            `json:"proxyProtocol,omitempty"`
}

type xFragment struct {
	Packets  string `json:"packets"`
	Length   string `json:"length"`
	Interval string `json:"interval"`
	Host1Header string `json:"host1Header,omitempty"`
	Host2Header string `json:"host2Header,omitempty"`
}

type xNoise struct {
	Type   string `json:"type"`
	Packet string `json:"packet,omitempty"`
	Delay  string `json:"delay,omitempty"`
}

type xBlackholeOutbound struct {
	Response *struct {
		Type string `json:"type"`
	} `json:"response,omitempty"`
}

type xDNSOutboundSettings struct {
	Network string `json:"network,omitempty"`
	Address string `json:"address,omitempty"`
	Port    int    `json:"port,omitempty"`
}

type xLoopbackSettings struct {
	InboundTag string `json:"inboundTag"`
}

func buildOutbounds(c *config.Config) []xOutbound {
	// Always include the synthetic "direct" and "block" outbounds — routing
	// rules can reference them without the operator needing to declare nodes.
	// These are deliberate, explicit "defaults" needed for any router config.
	out := []xOutbound{
		{
			Tag:      "direct",
			Protocol: "freedom",
			Settings: xFreedomOutbound{DomainStrategy: "AsIs"},
		},
		{
			Tag:      "block",
			Protocol: "blackhole",
			Settings: xBlackholeOutbound{},
		},
		{
			Tag:      "dns-out",
			Protocol: "dns",
			Settings: xDNSOutboundSettings{},
		},
	}
	for _, n := range c.Nodes {
		if !n.Enabled {
			continue
		}
		out = append(out, buildOneOutbound(n))
	}
	return out
}

func buildOneOutbound(n config.Node) xOutbound {
	ob := xOutbound{
		Tag:           n.Tag,
		Protocol:      n.Outbound.Protocol,
		SendThrough:   n.Outbound.SendThrough,
	}
	switch n.Outbound.Protocol {
	case "vless":
		if n.Outbound.Settings.VLESS != nil {
			ob.Settings = xVLESSOutbound{
				Vnext: []xVLESSServer{{
					Address: n.Outbound.Server,
					Port:    n.Outbound.Port,
					Users: []xVLESSUser{{
						ID:         n.Outbound.Settings.VLESS.UUID,
						Flow:       n.Outbound.Settings.VLESS.Flow,
						Encryption: orString(n.Outbound.Settings.VLESS.Encryption, "none"),
						Level:      n.Outbound.Settings.VLESS.Level,
					}},
				}},
			}
		}
	case "vmess":
		if n.Outbound.Settings.VMess != nil {
			ob.Settings = xVMessOutbound{
				Vnext: []xVMessServer{{
					Address: n.Outbound.Server,
					Port:    n.Outbound.Port,
					Users: []xVMessUser{{
						ID:       n.Outbound.Settings.VMess.UUID,
						Security: n.Outbound.Settings.VMess.Security,
						AlterID:  n.Outbound.Settings.VMess.AlterID,
						Level:    n.Outbound.Settings.VMess.Level,
					}},
				}},
			}
		}
	case "trojan":
		if n.Outbound.Settings.Trojan != nil {
			ob.Settings = xTrojanOutbound{
				Servers: []xTrojanServer{{
					Address:  n.Outbound.Server,
					Port:     n.Outbound.Port,
					Password: n.Outbound.Settings.Trojan.Password,
					Email:    n.Outbound.Settings.Trojan.Email,
					Level:    n.Outbound.Settings.Trojan.Level,
				}},
			}
		}
	case "shadowsocks":
		if n.Outbound.Settings.Shadowsocks != nil {
			ss := n.Outbound.Settings.Shadowsocks
			ob.Settings = xSSOutbound{
				Servers: []xSSServer{{
					Address:    n.Outbound.Server,
					Port:       n.Outbound.Port,
					Method:     ss.Method,
					Password:   ss.Password,
					UoT:        ss.UoT,
					UoTVersion: ss.UoTVersion,
					Level:      ss.Level,
				}},
			}
		}
	case "hysteria2":
		if n.Outbound.Settings.Hysteria2 != nil {
			hy := n.Outbound.Settings.Hysteria2
			srv := xHy2Server{
				Address:               n.Outbound.Server,
				Port:                  n.Outbound.Port,
				Password:              hy.Password,
				HopPorts:              hy.HopPorts,
				HopInterval:           hy.HopInterval,
				Up:                    hy.Up,
				Down:                  hy.Down,
				IgnoreClientBandwidth: hy.IgnoreClientBandwidth,
			}
			if hy.Obfs != nil {
				srv.Obfs = &xHy2Obfs{Type: hy.Obfs.Type, Password: hy.Obfs.Password}
			}
			ob.Settings = xHy2Outbound{Servers: []xHy2Server{srv}}
		}
	case "socks":
		if n.Outbound.Settings.Socks != nil {
			s := n.Outbound.Settings.Socks
			srv := xSocksServer{
				Address: n.Outbound.Server,
				Port:    n.Outbound.Port,
			}
			if s.User != "" {
				srv.Users = []xSocksOutboundUser{{User: s.User, Pass: s.Pass, Level: s.Level}}
			}
			srv.UDPOverTCP = s.UDPOverTCP
			ob.Settings = xSocksOutbound{Servers: []xSocksServer{srv}}
		}
	case "http":
		if n.Outbound.Settings.HTTP != nil {
			h := n.Outbound.Settings.HTTP
			srv := xHTTPServer{
				Address: n.Outbound.Server,
				Port:    n.Outbound.Port,
				Headers: h.Headers,
			}
			if h.User != "" {
				srv.Users = []xHTTPOutboundUser{{User: h.User, Pass: h.Pass}}
			}
			ob.Settings = xHTTPOutbound{Servers: []xHTTPServer{srv}}
		}
	case "wireguard":
		if n.Outbound.Settings.Wireguard != nil {
			wg := n.Outbound.Settings.Wireguard
			peers := make([]xWGPeer, 0, len(wg.Peers))
			for _, p := range wg.Peers {
				peers = append(peers, xWGPeer{
					PublicKey:    p.PublicKey,
					PreSharedKey: p.PreSharedKey,
					Endpoint:     p.Endpoint,
					AllowedIPs:   p.AllowedIPs,
					KeepAlive:    p.KeepAlive,
				})
			}
			ob.Settings = xWGOutbound{
				SecretKey:      wg.SecretKey,
				Address:        wg.Address,
				Peers:          peers,
				MTU:            wg.MTU,
				Workers:        wg.Workers,
				Reserved:       wg.Reserved,
				DomainStrategy: wg.DomainStrategy,
				KernelMode:     wg.KernelMode,
			}
		}
	case "freedom":
		if n.Outbound.Settings.Freedom != nil {
			f := n.Outbound.Settings.Freedom
			out := xFreedomOutbound{
				DomainStrategy: f.DomainStrategy,
				Redirect:       f.Redirect,
				ProxyProtocol:  f.ProxyProtocol,
			}
			if f.Fragment != nil {
				out.Fragment = &xFragment{
					Packets: f.Fragment.Packets, Length: f.Fragment.Length, Interval: f.Fragment.Interval,
					Host1Header: f.Fragment.Host1Header, Host2Header: f.Fragment.Host2Header,
				}
			}
			for _, np := range f.Noises {
				out.Noises = append(out.Noises, xNoise{Type: np.Type, Packet: np.Packet, Delay: np.Delay})
			}
			ob.Settings = out
		}
	case "blackhole":
		if n.Outbound.Settings.Blackhole != nil && n.Outbound.Settings.Blackhole.Response != nil {
			ob.Settings = xBlackholeOutbound{Response: &struct {
				Type string `json:"type"`
			}{Type: n.Outbound.Settings.Blackhole.Response.Type}}
		} else {
			ob.Settings = xBlackholeOutbound{}
		}
	case "dns":
		if n.Outbound.Settings.DNS != nil {
			ob.Settings = xDNSOutboundSettings{
				Network: n.Outbound.Settings.DNS.Network,
				Address: n.Outbound.Settings.DNS.Address,
				Port:    n.Outbound.Settings.DNS.Port,
			}
		}
	case "loopback":
		if n.Outbound.Settings.Loopback != nil {
			ob.Settings = xLoopbackSettings{InboundTag: n.Outbound.Settings.Loopback.InboundTag}
		}
	}
	if n.Outbound.Stream != nil {
		ob.StreamSettings = buildStream(n.Outbound.Stream)
	}
	if n.Outbound.Mux != nil && n.Outbound.Mux.Enabled {
		ob.Mux = &xMux{
			Enabled:         true,
			Concurrency:     n.Outbound.Mux.Concurrency,
			XUDPConcurrency: n.Outbound.Mux.XUDPConcurrency,
			XUDPProxyUDP443: n.Outbound.Mux.XUDPProxyUDP443,
			PacketEncoding:  n.Outbound.Mux.PacketEncoding,
		}
	}
	if n.Outbound.ProxySettings != nil {
		ob.ProxySettings = &xProxySettings{
			Tag:            n.Outbound.ProxySettings.Tag,
			TransportLayer: n.Outbound.ProxySettings.TransportLayer,
		}
	}
	return ob
}
