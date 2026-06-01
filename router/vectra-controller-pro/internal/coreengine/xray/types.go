package xray

// xConfig is the root Xray JSON document.
type xConfig struct {
	Log         *xLog          `json:"log,omitempty"`
	API         *xAPI          `json:"api,omitempty"`
	DNS         *xDNS          `json:"dns,omitempty"`
	FakeDNS     []xFakeDNSPool `json:"fakedns,omitempty"`
	Stats       *struct{}      `json:"stats,omitempty"`
	Policy      *xPolicy       `json:"policy,omitempty"`
	Inbounds    []xInbound     `json:"inbounds"`
	Outbounds   []xOutbound    `json:"outbounds"`
	Routing     *xRouting      `json:"routing,omitempty"`
	Reverse     *xReverse      `json:"reverse,omitempty"`
	Transport   *xTransport    `json:"transport,omitempty"`
	Metrics     *xMetrics      `json:"metrics,omitempty"`
	Observatory *xObservatory  `json:"observatory,omitempty"`
	BurstObservatory *xBurstObservatory `json:"burstObservatory,omitempty"`
}

type xLog struct {
	Loglevel string `json:"loglevel,omitempty"`
	Access   string `json:"access,omitempty"`
	Error    string `json:"error,omitempty"`
	DNSLog   bool   `json:"dnsLog,omitempty"`
}

type xAPI struct {
	Tag      string   `json:"tag"`
	Services []string `json:"services"`
	Listen   string   `json:"listen,omitempty"`
}

type xDNS struct {
	Servers []any             `json:"servers,omitempty"`
	Hosts   map[string]string `json:"hosts,omitempty"`
	ClientIP string           `json:"clientIp,omitempty"`
	QueryStrategy string      `json:"queryStrategy,omitempty"`
	DisableCache bool         `json:"disableCache,omitempty"`
	DisableFallback bool      `json:"disableFallback,omitempty"`
	DisableFallbackIfMatch bool `json:"disableFallbackIfMatch,omitempty"`
	Tag string                `json:"tag,omitempty"`
}

type xDNSServer struct {
	Address       string   `json:"address"`
	Port          int      `json:"port,omitempty"`
	ClientIP      string   `json:"clientIp,omitempty"`
	SkipFallback  bool     `json:"skipFallback,omitempty"`
	Domains       []string `json:"domains,omitempty"`
	ExpectIPs     []string `json:"expectIPs,omitempty"`
	QueryStrategy string   `json:"queryStrategy,omitempty"`
	FinalQuery    bool     `json:"finalQuery,omitempty"`
	Tag           string   `json:"tag,omitempty"`
}

type xFakeDNSPool struct {
	IPPool   string `json:"ipPool"`
	PoolSize int    `json:"poolSize"`
}

type xPolicy struct {
	Levels map[string]xPolicyLevel `json:"levels,omitempty"`
	System *xSystemPolicy          `json:"system,omitempty"`
}

type xPolicyLevel struct {
	Handshake         int  `json:"handshake,omitempty"`
	ConnIdle          int  `json:"connIdle,omitempty"`
	UplinkOnly        int  `json:"uplinkOnly,omitempty"`
	DownlinkOnly      int  `json:"downlinkOnly,omitempty"`
	StatsUserUplink   bool `json:"statsUserUplink,omitempty"`
	StatsUserDownlink bool `json:"statsUserDownlink,omitempty"`
	BufferSize        int  `json:"bufferSize,omitempty"`
}

type xSystemPolicy struct {
	StatsInboundUplink    bool `json:"statsInboundUplink,omitempty"`
	StatsInboundDownlink  bool `json:"statsInboundDownlink,omitempty"`
	StatsOutboundUplink   bool `json:"statsOutboundUplink,omitempty"`
	StatsOutboundDownlink bool `json:"statsOutboundDownlink,omitempty"`
}

type xInbound struct {
	Tag            string             `json:"tag,omitempty"`
	Listen         string             `json:"listen,omitempty"`
	Port           any                `json:"port,omitempty"` // int or "1000-2000"
	Protocol       string             `json:"protocol"`
	Settings       any                `json:"settings,omitempty"`
	StreamSettings *xStreamSettings   `json:"streamSettings,omitempty"`
	Sniffing       *xSniffing         `json:"sniffing,omitempty"`
	Allocate       *xAllocate         `json:"allocate,omitempty"`
}

type xSniffing struct {
	Enabled         bool     `json:"enabled"`
	DestOverride    []string `json:"destOverride,omitempty"`
	DomainsExcluded []string `json:"domainsExcluded,omitempty"`
	MetadataOnly    bool     `json:"metadataOnly,omitempty"`
	RouteOnly       bool     `json:"routeOnly,omitempty"`
}

type xAllocate struct {
	Strategy    string `json:"strategy,omitempty"` // always|random
	Refresh     int    `json:"refresh,omitempty"`
	Concurrency int    `json:"concurrency,omitempty"`
}

type xOutbound struct {
	Tag            string           `json:"tag,omitempty"`
	Protocol       string           `json:"protocol"`
	Settings       any              `json:"settings,omitempty"`
	StreamSettings *xStreamSettings `json:"streamSettings,omitempty"`
	Mux            *xMux            `json:"mux,omitempty"`
	SendThrough    string           `json:"sendThrough,omitempty"`
	ProxySettings  *xProxySettings  `json:"proxySettings,omitempty"`
}

type xProxySettings struct {
	Tag            string `json:"tag"`
	TransportLayer bool   `json:"transportLayer,omitempty"`
}

type xMux struct {
	Enabled         bool   `json:"enabled"`
	Concurrency     int    `json:"concurrency,omitempty"`
	XUDPConcurrency int    `json:"xudpConcurrency,omitempty"`
	XUDPProxyUDP443 string `json:"xudpProxyUDP443,omitempty"`
	PacketEncoding  string `json:"packetEncoding,omitempty"`
}

type xStreamSettings struct {
	Network         string                 `json:"network,omitempty"`
	Security        string                 `json:"security,omitempty"`
	TLSSettings     *xTLSSettings          `json:"tlsSettings,omitempty"`
	RealitySettings *xRealitySettings      `json:"realitySettings,omitempty"`
	TCPSettings     *xTCPSettings          `json:"tcpSettings,omitempty"`
	KCPSettings     *xKCPSettings          `json:"kcpSettings,omitempty"`
	WSSettings      *xWSSettings           `json:"wsSettings,omitempty"`
	HTTPSettings    *xHTTPSettings         `json:"httpSettings,omitempty"`
	QUICSettings    *xQUICSettings         `json:"quicSettings,omitempty"`
	GRPCSettings    *xGRPCSettings         `json:"grpcSettings,omitempty"`
	XHTTPSettings   *xXHTTPSettings        `json:"xhttpSettings,omitempty"`
	HTTPUpgradeSettings *xHTTPUpgradeSettings `json:"httpupgradeSettings,omitempty"`
	DSSettings      *xDSSettings           `json:"dsSettings,omitempty"`
	Sockopt         *xSockopt              `json:"sockopt,omitempty"`
}

type xTLSSettings struct {
	ServerName       string         `json:"serverName,omitempty"`
	AllowInsecure    bool           `json:"allowInsecure,omitempty"`
	ALPN             []string       `json:"alpn,omitempty"`
	Fingerprint      string         `json:"fingerprint,omitempty"`
	EnableSessionResumption          bool   `json:"enableSessionResumption,omitempty"`
	CurvePreferences []string       `json:"curvePreferences,omitempty"`
	Certificates     []xTLSCertificate `json:"certificates,omitempty"`
	DisableSystemRoot bool          `json:"disableSystemRoot,omitempty"`
	MasterKeyLog     string         `json:"masterKeyLog,omitempty"`
	PinnedPeerCertificateChainSha256 []string `json:"pinnedPeerCertificateChainSha256,omitempty"`
	CipherSuites     string         `json:"cipherSuites,omitempty"`
	MinVersion       string         `json:"minVersion,omitempty"`
	MaxVersion       string         `json:"maxVersion,omitempty"`
	RejectUnknownSNI bool           `json:"rejectUnknownSni,omitempty"`
	ECHConfig        string         `json:"echConfig,omitempty"`
	ECHServerKeys    string         `json:"echServerKeys,omitempty"`
	NextProtos       []string       `json:"nextProtos,omitempty"`
	VerifyPeerCertInNames []string  `json:"verifyPeerCertInNames,omitempty"`
}

type xTLSCertificate struct {
	Usage           string   `json:"usage,omitempty"`
	Certificate     []string `json:"certificate,omitempty"`
	Key             []string `json:"key,omitempty"`
	CertificateFile string   `json:"certificateFile,omitempty"`
	KeyFile         string   `json:"keyFile,omitempty"`
	OCSPStapling    int      `json:"ocspStapling,omitempty"`
}

type xRealitySettings struct {
	Show        bool   `json:"show,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	ServerName  string `json:"serverName,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
	MaxTimeDiff int    `json:"maxTimeDiff,omitempty"`
	// Server-side (REALITY inbound) fields.
	PrivateKey  string   `json:"privateKey,omitempty"`
	Dest        string   `json:"dest,omitempty"`
	Xver        int      `json:"xver,omitempty"`
	ServerNames []string `json:"serverNames,omitempty"`
	ShortIDs    []string `json:"shortIds,omitempty"`
}

type xTCPSettings struct {
	AcceptProxyProtocol bool      `json:"acceptProxyProtocol,omitempty"`
	Header              *xTCPHeader `json:"header,omitempty"`
}

type xTCPHeader struct {
	Type     string         `json:"type"`
	Request  map[string]any `json:"request,omitempty"`
	Response map[string]any `json:"response,omitempty"`
}

type xKCPSettings struct {
	MTU              int        `json:"mtu,omitempty"`
	TTI              int        `json:"tti,omitempty"`
	UplinkCapacity   int        `json:"uplinkCapacity,omitempty"`
	DownlinkCapacity int        `json:"downlinkCapacity,omitempty"`
	Congestion       bool       `json:"congestion,omitempty"`
	ReadBufferSize   int        `json:"readBufferSize,omitempty"`
	WriteBufferSize  int        `json:"writeBufferSize,omitempty"`
	Seed             string     `json:"seed,omitempty"`
	Header           *xKCPHeader `json:"header,omitempty"`
}

type xKCPHeader struct {
	Type string `json:"type"`
}

type xWSSettings struct {
	Path                string            `json:"path,omitempty"`
	Host                string            `json:"host,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	AcceptProxyProtocol bool              `json:"acceptProxyProtocol,omitempty"`
	HeartbeatPeriod     int               `json:"heartbeatPeriod,omitempty"`
}

type xHTTPSettings struct {
	Host []string `json:"host,omitempty"`
	Path string   `json:"path,omitempty"`
}

type xQUICSettings struct {
	Security string      `json:"security,omitempty"`
	Key      string      `json:"key,omitempty"`
	Header   *xQUICHeader `json:"header,omitempty"`
}

type xQUICHeader struct {
	Type string `json:"type"`
}

type xGRPCSettings struct {
	ServiceName         string `json:"serviceName,omitempty"`
	Authority           string `json:"authority,omitempty"`
	MultiMode           bool   `json:"multiMode,omitempty"`
	IdleTimeout         int    `json:"idle_timeout,omitempty"`
	HealthCheckTimeout  int    `json:"health_check_timeout,omitempty"`
	PermitWithoutStream bool   `json:"permit_without_stream,omitempty"`
	InitialWindowsSize  int    `json:"initial_windows_size,omitempty"`
	UserAgent           string `json:"user_agent,omitempty"`
}

type xXHTTPSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Mode    string            `json:"mode,omitempty"`
	Extra   map[string]any    `json:"extra,omitempty"`
}

type xHTTPUpgradeSettings struct {
	Path                string            `json:"path,omitempty"`
	Host                string            `json:"host,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	AcceptProxyProtocol bool              `json:"acceptProxyProtocol,omitempty"`
}

type xDSSettings struct {
	Path     string `json:"path"`
	Abstract bool   `json:"abstract,omitempty"`
	Padding  bool   `json:"padding,omitempty"`
}

type xSockopt struct {
	Mark                 int    `json:"mark,omitempty"`
	TCPFastOpen          bool   `json:"tcpFastOpen,omitempty"`
	TCPFastOpenQueueLen  int    `json:"tcpFastOpenQueueLength,omitempty"`
	TProxy               string `json:"tproxy,omitempty"`
	DomainStrategy       string `json:"domainStrategy,omitempty"`
	DialerProxy          string `json:"dialerProxy,omitempty"`
	TCPKeepAliveInterval int    `json:"tcpKeepAliveInterval,omitempty"`
	TCPKeepAliveIdle     int    `json:"tcpKeepAliveIdle,omitempty"`
	TCPCongestion        string `json:"tcpCongestion,omitempty"`
	Interface            string `json:"interface,omitempty"`
	V6Only               bool   `json:"v6Only,omitempty"`
	TCPMaxSeg            int    `json:"tcpMaxSeg,omitempty"`
	Penetrate            bool   `json:"penetrate,omitempty"`
	TCPMptcp             bool   `json:"tcpMptcp,omitempty"`
	CustomSockopt        []map[string]any `json:"customSockopt,omitempty"`
}

type xRouting struct {
	DomainStrategy string         `json:"domainStrategy,omitempty"`
	DomainMatcher  string         `json:"domainMatcher,omitempty"`
	Rules          []xRoutingRule `json:"rules"`
	Balancers      []xBalancer    `json:"balancers,omitempty"`
}

type xRoutingRule struct {
	Type        string   `json:"type"`
	Domain      []string `json:"domain,omitempty"`
	Domains     []string `json:"domains,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Port        string   `json:"port,omitempty"`
	SourcePort  string   `json:"sourcePort,omitempty"`
	Network     string   `json:"network,omitempty"`
	Source      []string `json:"source,omitempty"`
	User        []string `json:"user,omitempty"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	Protocol    []string `json:"protocol,omitempty"`
	Attrs       string   `json:"attrs,omitempty"`
	OutboundTag string   `json:"outboundTag,omitempty"`
	BalancerTag string   `json:"balancerTag,omitempty"`
	RuleTag     string   `json:"ruleTag,omitempty"`
}

type xBalancer struct {
	Tag         string            `json:"tag"`
	Selector    []string          `json:"selector,omitempty"`
	FallbackTag string            `json:"fallbackTag,omitempty"`
	Strategy    *xBalancerStrategy `json:"strategy,omitempty"`
}

type xBalancerStrategy struct {
	Type     string         `json:"type"`
	Settings map[string]any `json:"settings,omitempty"`
}

type xReverse struct {
	Bridges []xReverseEndpoint `json:"bridges,omitempty"`
	Portals []xReverseEndpoint `json:"portals,omitempty"`
}

type xReverseEndpoint struct {
	Tag    string `json:"tag"`
	Domain string `json:"domain"`
}

type xTransport struct{} // placeholder, kept for future protocol-specific transport globals

type xMetrics struct {
	Tag string `json:"tag"`
}

type xObservatory struct {
	SubjectSelector []string `json:"subjectSelector,omitempty"`
	ProbeURL        string   `json:"probeURL,omitempty"`
	ProbeInterval   string   `json:"probeInterval,omitempty"`
	EnableConcurrency bool   `json:"enableConcurrency,omitempty"`
}

type xBurstObservatory struct {
	SubjectSelector []string `json:"subjectSelector,omitempty"`
	PingConfig      *xPingConfig `json:"pingConfig,omitempty"`
}

type xPingConfig struct {
	Destination string `json:"destination,omitempty"`
	Connectivity string `json:"connectivity,omitempty"`
	Interval string `json:"interval,omitempty"`
	SamplingCount int `json:"samplingCount,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}
