package config

// Node is one outbound proxy entry (real node or pseudo-outbound like freedom/blackhole).
type Node struct {
	ID       string   `json:"id"`              // operator-stable id (used by routing rules / panel)
	Tag      string   `json:"tag,omitempty"`   // Xray outbound tag; if empty, defaults to "node-"+ID
	Remark   string   `json:"remark,omitempty"`
	Group    string   `json:"group,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Enabled  bool     `json:"enabled"`
	Outbound Outbound `json:"outbound"`

	// Origin info for diagnostics. Set by subscription engine when this node
	// is imported from a subscription.
	Origin *NodeOrigin `json:"origin,omitempty"`
}

// NodeOrigin records where this node came from for traceability.
//
// ParserDefaults captures every field the parser filled in BECAUSE the
// protocol requires it but the upstream URI did NOT set it. The audit trail
// makes the "no silent normalization" guarantee verifiable: if a field
// appears in ParserDefaults, the parser chose it; if it doesn't, the
// upstream did.
type NodeOrigin struct {
	SubscriptionID  string            `json:"subscriptionId,omitempty"`
	SubscriptionURL string            `json:"subscriptionUrl,omitempty"`
	RawLink         string            `json:"rawLink,omitempty"`   // original vless:// URI
	ImportedAt      string            `json:"importedAt,omitempty"`
	Fingerprint     string            `json:"fingerprint,omitempty"` // sha256 of canonical form for dedupe
	ParserDefaults  map[string]string `json:"parserDefaults,omitempty"`
}

// Outbound holds the protocol-specific settings + transport + security.
// Server/Port are top-level convenience: most protocols put address into the
// "vnext" or "servers" sub-tree but we keep a flat address for clarity. The
// builder is responsible for placing it correctly per protocol.
type Outbound struct {
	Protocol    string             `json:"protocol"`
	Server      string             `json:"server,omitempty"`
	Port        int                `json:"port,omitempty"`
	Settings    ProtocolSettings   `json:"settings"`
	Stream      *StreamSettings    `json:"stream,omitempty"`
	Mux         *MuxSettings       `json:"mux,omitempty"`
	SendThrough string             `json:"sendThrough,omitempty"`
	ProxySettings *ProxySettings   `json:"proxySettings,omitempty"`
	Tag         string             `json:"tag,omitempty"` // override (rarely used)
}

// ProxySettings (Xray outbound proxySettings — chains one outbound through another).
type ProxySettings struct {
	Tag            string `json:"tag"`            // tag of the front outbound
	TransportLayer bool   `json:"transportLayer,omitempty"`
}

// ProtocolSettings is a tagged union: exactly one inner pointer is set.
type ProtocolSettings struct {
	VLESS       *VLESSSettings       `json:"vless,omitempty"`
	VMess       *VMessSettings       `json:"vmess,omitempty"`
	Trojan      *TrojanSettings      `json:"trojan,omitempty"`
	Shadowsocks *ShadowsocksSettings `json:"shadowsocks,omitempty"`
	Socks       *SocksOutboundSettings `json:"socks,omitempty"`
	HTTP        *HTTPOutboundSettings  `json:"http,omitempty"`
	Hysteria2   *Hysteria2Settings   `json:"hysteria2,omitempty"`
	Wireguard   *WireguardSettings   `json:"wireguard,omitempty"`
	Freedom     *FreedomSettings     `json:"freedom,omitempty"`
	Blackhole   *BlackholeSettings   `json:"blackhole,omitempty"`
	DNS         *DNSOutboundSettings `json:"dns,omitempty"`
	Loopback    *LoopbackSettings    `json:"loopback,omitempty"`
}

// VLESSSettings — operator-controlled, NO silent normalization.
type VLESSSettings struct {
	UUID       string `json:"uuid"`
	Flow       string `json:"flow,omitempty"`       // "" | xtls-rprx-vision | xtls-rprx-vision-udp443
	Encryption string `json:"encryption,omitempty"` // none
	Level      int    `json:"level,omitempty"`
}

type VMessSettings struct {
	UUID     string `json:"uuid"`
	Security string `json:"security,omitempty"` // auto|aes-128-gcm|chacha20-poly1305|none|zero
	AlterID  int    `json:"alterId,omitempty"`
	Level    int    `json:"level,omitempty"`
}

type TrojanSettings struct {
	Password string `json:"password"`
	Email    string `json:"email,omitempty"`
	Level    int    `json:"level,omitempty"`
}

type ShadowsocksSettings struct {
	Method     string `json:"method"`
	Password   string `json:"password"`
	UoT        bool   `json:"uot,omitempty"`        // UDP over TCP
	UoTVersion int    `json:"uotVersion,omitempty"` // 1 or 2
	Level      int    `json:"level,omitempty"`
}

type SocksOutboundSettings struct {
	User     string `json:"user,omitempty"`
	Pass     string `json:"pass,omitempty"`
	Level    int    `json:"level,omitempty"`
	UDPOverTCP bool `json:"udpOverTcp,omitempty"`
}

type HTTPOutboundSettings struct {
	User     string            `json:"user,omitempty"`
	Pass     string            `json:"pass,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

type Hysteria2Settings struct {
	Password    string     `json:"password,omitempty"`
	Obfs        *Hy2Obfs   `json:"obfs,omitempty"`
	HopPorts    string     `json:"hopPorts,omitempty"`
	HopInterval int        `json:"hopInterval,omitempty"`
	Up          int        `json:"up,omitempty"`   // Mbps
	Down        int        `json:"down,omitempty"` // Mbps
	IgnoreClientBandwidth bool `json:"ignoreClientBandwidth,omitempty"`
}

type Hy2Obfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

type WireguardSettings struct {
	SecretKey      string   `json:"secretKey"`
	Address        []string `json:"address"`
	Peers          []WGPeer `json:"peers"`
	MTU            int      `json:"mtu,omitempty"`
	Workers        int      `json:"workers,omitempty"`
	Reserved       []int    `json:"reserved,omitempty"`
	DomainStrategy string   `json:"domainStrategy,omitempty"`
	KernelMode     bool     `json:"kernelMode,omitempty"`
}

type WGPeer struct {
	PublicKey    string   `json:"publicKey"`
	PreSharedKey string   `json:"preSharedKey,omitempty"`
	Endpoint     string   `json:"endpoint"`
	AllowedIPs   []string `json:"allowedIps,omitempty"`
	KeepAlive    int      `json:"keepAlive,omitempty"`
}

type FreedomSettings struct {
	DomainStrategy string             `json:"domainStrategy,omitempty"` // AsIs|UseIP|UseIPv4|UseIPv6|ForceIP|ForceIPv4|ForceIPv6
	Redirect       string             `json:"redirect,omitempty"`
	Fragment       *FragmentSettings  `json:"fragment,omitempty"`
	Noises         []NoisePacket      `json:"noises,omitempty"`
	ProxyProtocol  int                `json:"proxyProtocol,omitempty"`
}

type FragmentSettings struct {
	Packets  string `json:"packets"`  // tlshello|1-3|...
	Length   string `json:"length"`
	Interval string `json:"interval"`
	Host1Header string `json:"host1Header,omitempty"`
	Host2Header string `json:"host2Header,omitempty"`
}

type NoisePacket struct {
	Type   string `json:"type"`
	Packet string `json:"packet,omitempty"`
	Delay  string `json:"delay,omitempty"`
}

type BlackholeSettings struct {
	Response *struct {
		Type string `json:"type"` // none|http
	} `json:"response,omitempty"`
}

type DNSOutboundSettings struct {
	Network string `json:"network,omitempty"` // tcp|udp
	Address string `json:"address,omitempty"`
	Port    int    `json:"port,omitempty"`
}

type LoopbackSettings struct {
	InboundTag string `json:"inboundTag"`
}

// MuxSettings: Xray mux/xudp configuration.
// All fields are operator-set — controller does not invent defaults.
type MuxSettings struct {
	Enabled         bool   `json:"enabled"`
	Concurrency     int    `json:"concurrency,omitempty"`     // -1..1024
	XUDPConcurrency int    `json:"xudpConcurrency,omitempty"` // -1..1024
	XUDPProxyUDP443 string `json:"xudpProxyUDP443,omitempty"` // reject|allow|skip
	PacketEncoding  string `json:"packetEncoding,omitempty"`  // none|packet|xudp
}
