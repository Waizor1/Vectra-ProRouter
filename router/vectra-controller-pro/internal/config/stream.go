package config

// StreamSettings is Xray's stream/transport/security configuration.
type StreamSettings struct {
	Transport string `json:"transport"`           // tcp|ws|grpc|kcp|quic|http|xhttp|httpupgrade|domainsocket
	Security  string `json:"security,omitempty"`  // none|tls|reality

	TCP         *TCPSettings         `json:"tcp,omitempty"`
	WS          *WSSettings          `json:"ws,omitempty"`
	GRPC        *GRPCSettings        `json:"grpc,omitempty"`
	KCP         *KCPSettings         `json:"kcp,omitempty"`
	QUIC        *QUICSettings        `json:"quic,omitempty"`
	HTTP        *HTTPSettings        `json:"http,omitempty"`
	XHTTP       *XHTTPSettings       `json:"xhttp,omitempty"`
	HTTPUpgrade *HTTPUpgradeSettings `json:"httpupgrade,omitempty"`
	DS          *DSSettings          `json:"ds,omitempty"`

	TLS     *TLSSettings     `json:"tls,omitempty"`
	REALITY *REALITYSettings `json:"reality,omitempty"`

	Sockopt *Sockopt `json:"sockopt,omitempty"`
}

type TCPSettings struct {
	AcceptProxyProtocol bool      `json:"acceptProxyProtocol,omitempty"`
	Header              *TCPHeader `json:"header,omitempty"`
}

type TCPHeader struct {
	Type     string         `json:"type"` // none|http
	Request  map[string]any `json:"request,omitempty"`
	Response map[string]any `json:"response,omitempty"`
}

type WSSettings struct {
	Path                string            `json:"path,omitempty"`
	Host                string            `json:"host,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	AcceptProxyProtocol bool              `json:"acceptProxyProtocol,omitempty"`
	HeartbeatPeriod     int               `json:"heartbeatPeriod,omitempty"`
}

type GRPCSettings struct {
	ServiceName         string `json:"serviceName,omitempty"`
	Authority           string `json:"authority,omitempty"`
	MultiMode           bool   `json:"multiMode,omitempty"`
	IdleTimeout         int    `json:"idleTimeout,omitempty"`
	HealthCheckTimeout  int    `json:"healthCheckTimeout,omitempty"`
	PermitWithoutStream bool   `json:"permitWithoutStream,omitempty"`
	InitialWindowsSize  int    `json:"initialWindowsSize,omitempty"`
	UserAgent           string `json:"userAgent,omitempty"`
}

type KCPSettings struct {
	MTU              int             `json:"mtu,omitempty"`
	TTI              int             `json:"tti,omitempty"`
	UplinkCapacity   int             `json:"uplinkCapacity,omitempty"`
	DownlinkCapacity int             `json:"downlinkCapacity,omitempty"`
	Congestion       bool            `json:"congestion,omitempty"`
	ReadBufferSize   int             `json:"readBufferSize,omitempty"`
	WriteBufferSize  int             `json:"writeBufferSize,omitempty"`
	Seed             string          `json:"seed,omitempty"`
	Header           *KCPHeader      `json:"header,omitempty"`
}

type KCPHeader struct {
	Type string `json:"type"` // none|srtp|utp|wechat-video|dtls|wireguard
}

type QUICSettings struct {
	Security string         `json:"security,omitempty"` // none|aes-128-gcm|chacha20-poly1305
	Key      string         `json:"key,omitempty"`
	Header   *QUICHeader    `json:"header,omitempty"`
}

type QUICHeader struct {
	Type string `json:"type"`
}

// HTTPSettings is the classic HTTP/2 (network "http") transport. Distinct from
// XHTTP (the newer "xhttp") and HTTPUpgrade. Host is a list of authorities.
type HTTPSettings struct {
	Host []string `json:"host,omitempty"`
	Path string   `json:"path,omitempty"`
}

type XHTTPSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Mode    string            `json:"mode,omitempty"` // auto|packet-up|stream-up|stream-one
	Extra   map[string]any    `json:"extra,omitempty"`
}

type HTTPUpgradeSettings struct {
	Path                string            `json:"path,omitempty"`
	Host                string            `json:"host,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	AcceptProxyProtocol bool              `json:"acceptProxyProtocol,omitempty"`
}

type DSSettings struct {
	Path     string `json:"path"`
	Abstract bool   `json:"abstract,omitempty"`
	Padding  bool   `json:"padding,omitempty"`
}

// TLSSettings: TLS security parameters. Operator-controlled.
type TLSSettings struct {
	ServerName                       string            `json:"serverName,omitempty"`
	AllowInsecure                    bool              `json:"allowInsecure,omitempty"`
	ALPN                             []string          `json:"alpn,omitempty"`
	Fingerprint                      string            `json:"fingerprint,omitempty"` // uTLS profile — OPERATOR value, not normalized
	EnableSessionResumption          bool              `json:"enableSessionResumption,omitempty"`
	CurvePreferences                 []string          `json:"curvePreferences,omitempty"`
	Certificates                     []TLSCertificate  `json:"certificates,omitempty"`
	DisableSystemRoot                bool              `json:"disableSystemRoot,omitempty"`
	MasterKeyLog                     string            `json:"masterKeyLog,omitempty"`
	PinnedPeerCertificateChainSha256 []string          `json:"pinnedPeerCertificateChainSha256,omitempty"`
	CipherSuites                     string            `json:"cipherSuites,omitempty"`
	MinVersion                       string            `json:"minVersion,omitempty"`
	MaxVersion                       string            `json:"maxVersion,omitempty"`
	RejectUnknownSNI                 bool              `json:"rejectUnknownSni,omitempty"`
	ECHConfig                        string            `json:"echConfig,omitempty"`
	ECHServerKeys                    string            `json:"echServerKeys,omitempty"`
	NextProtos                       []string          `json:"nextProtos,omitempty"`
	VerifyPeerCertInNames            []string          `json:"verifyPeerCertInNames,omitempty"`
}

type TLSCertificate struct {
	Usage           string   `json:"usage,omitempty"` // encipherment|verify|issue
	Certificate     []string `json:"certificate,omitempty"`
	Key             []string `json:"key,omitempty"`
	CertificateFile string   `json:"certificateFile,omitempty"`
	KeyFile         string   `json:"keyFile,omitempty"`
	OCSPStapling    int      `json:"ocspStapling,omitempty"`
}

// REALITYSettings: REALITY parameters. The first block is client-side
// (outbound) and the second block is server-side (inbound). All fields are
// operator-set — no silent rewrite. An outbound uses publicKey/serverName/
// shortId/spiderX; a REALITY inbound uses privateKey/dest/serverNames/shortIds/
// xver. The two never mix in a single valid Xray block, but keeping one struct
// avoids a parallel type tree.
type REALITYSettings struct {
	Show        bool   `json:"show,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"` // OPERATOR-CONTROLLED — no silent rewrite
	ServerName  string `json:"serverName"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId,omitempty"`
	SpiderX     string `json:"spiderX,omitempty"`
	MaxTimeDiff int    `json:"maxTimeDiff,omitempty"`

	// Server-side (REALITY inbound) fields. Optional; ignored on outbounds.
	PrivateKey  string   `json:"privateKey,omitempty"`
	Dest        string   `json:"dest,omitempty"`        // e.g. "www.microsoft.com:443"
	Xver        int      `json:"xver,omitempty"`        // PROXY protocol version (0|1|2)
	ServerNames []string `json:"serverNames,omitempty"` // accepted SNIs
	ShortIDs    []string `json:"shortIds,omitempty"`    // accepted shortIds
}

// Sockopt: low-level socket options on the connection.
type Sockopt struct {
	Mark                int    `json:"mark,omitempty"`
	TCPFastOpen         bool   `json:"tcpFastOpen,omitempty"`
	TCPFastOpenQueueLen int    `json:"tcpFastOpenQueueLength,omitempty"`
	TProxy              string `json:"tproxy,omitempty"` // off|redirect|tproxy
	DomainStrategy      string `json:"domainStrategy,omitempty"`
	DialerProxy         string `json:"dialerProxy,omitempty"`
	TCPKeepAliveInterval int   `json:"tcpKeepAliveInterval,omitempty"`
	TCPKeepAliveIdle    int    `json:"tcpKeepAliveIdle,omitempty"`
	TCPCongestion       string `json:"tcpCongestion,omitempty"`
	Interface           string `json:"interface,omitempty"`
	V6Only              bool   `json:"v6Only,omitempty"`
	TCPMaxSeg           int    `json:"tcpMaxSeg,omitempty"`
	Penetrate           bool   `json:"penetrate,omitempty"`
	TCPMptcp            bool   `json:"tcpMptcp,omitempty"`
	CustomSockopt       []map[string]any `json:"customSockopt,omitempty"`
}
