// Package config holds the operator-facing configuration schema for
// Vectra Controller Pro. This is NOT a UCI mirror — it is a deliberate,
// flat-but-typed model designed to translate cleanly into Xray's JSON
// while staying readable to humans.
//
// Design rules:
//   - No silent normalization. What the operator sets is what Xray gets.
//   - Every field is either operator-set or explicitly defaulted via
//     defaults.go and the change is loggable.
//   - Forward-compatible: unknown fields are tolerated when the schema
//     version matches; mismatches are rejected.
package config

// SchemaVersion is the current top-level schema version.
const SchemaVersion = 1

// Config is the root operator config consumed by Vectra Controller Pro.
type Config struct {
	Schema        int             `json:"schema"`
	Instance      Instance        `json:"instance"`
	Process       Process         `json:"process"`
	Inbounds      Inbounds        `json:"inbounds"`
	DNS           DNS             `json:"dns"`
	Nodes         []Node          `json:"nodes"`
	Routing       Routing         `json:"routing"`
	Subscriptions []Subscription  `json:"subscriptions,omitempty"`
	Geo           Geo             `json:"geo"`
	Policy        *Policy         `json:"policy,omitempty"`
	Stats         *StatsConfig    `json:"stats,omitempty"`
	API           *APIConfig      `json:"api,omitempty"`
	Reverse       []ReverseConfig `json:"reverse,omitempty"`
	Metrics       *MetricsConfig  `json:"metrics,omitempty"`
	FakeDNS       *FakeDNS        `json:"fakedns,omitempty"`
	// Observatory and BurstObservatory feed the health/latency data that
	// leastPing / leastLoad balancer strategies consume. Without one of these
	// blocks those strategies have no probe data to rank outbounds.
	Observatory      *ObservatoryConfig      `json:"observatory,omitempty"`
	BurstObservatory *BurstObservatoryConfig `json:"burstObservatory,omitempty"`
	// Normalization captures operator-explicit normalization toggles.
	// All defaults here are "off". When on, the controller WILL transform
	// the value BUT will log every change at INFO level.
	Normalization Normalization `json:"normalization"`
}

// Instance metadata (identity + logging).
type Instance struct {
	Name     string `json:"name"`               // e.g., router hostname
	LogLevel string `json:"logLevel,omitempty"` // debug|info|warning|error|none
}

// Process settings for the supervised Xray process.
type Process struct {
	XrayBinary     string  `json:"xrayBinary"`
	WorkDir        string  `json:"workDir"`
	ConfigFile     string  `json:"configFile,omitempty"`
	LogDir         string  `json:"logDir,omitempty"`
	MemorySoftMiB  int     `json:"memorySoftMiB,omitempty"` // 0 = no soft cap
	MemoryHardMiB  int     `json:"memoryHardMiB,omitempty"` // 0 = no hard cap (rlimit)
	OOMScoreAdj    int     `json:"oomScoreAdj"`             // -1000..1000, lower = less likely OOM-killed
	NiceLevel      int     `json:"niceLevel,omitempty"`     // -20..19
	GOMAXPROCS     int     `json:"gomaxprocs,omitempty"`
	RestartBackoff Backoff `json:"restartBackoff"`
	ReloadGrace    string  `json:"reloadGrace,omitempty"`  // duration (e.g. "5s") to wait for graceful reload
	StartTimeout   string  `json:"startTimeout,omitempty"` // duration
}

// Backoff is an exponential restart-backoff policy.
type Backoff struct {
	InitialMs int     `json:"initialMs"`
	Factor    float64 `json:"factor"`
	MaxMs     int     `json:"maxMs"`
	// Reset is a duration string ("60s"); a process that stays up at least
	// this long resets the backoff to InitialMs.
	Reset string `json:"reset,omitempty"`
}

// Inbounds: the set of Xray inbounds the controller will create.
type Inbounds struct {
	Tproxy    *TproxyInbound    `json:"tproxy,omitempty"`
	Socks     *SocksInbound     `json:"socks,omitempty"`
	HTTP      *HTTPInbound      `json:"http,omitempty"`
	DNS       *DNSInbound       `json:"dns,omitempty"`
	Dokodemo  *DokodemoInbound  `json:"dokodemo,omitempty"`
	Shadowsocks *SSInbound      `json:"shadowsocks,omitempty"`
	Reality   *RealityInbound   `json:"realityInbound,omitempty"`
}

// TproxyInbound: transparent proxy via TPROXY (Linux only).
type TproxyInbound struct {
	ListenIP     string   `json:"listenIP"`
	Port         int      `json:"port"`
	FwMark       int      `json:"fwmark,omitempty"`
	UDPEnabled   bool     `json:"udpEnabled"`
	FollowRedirect bool   `json:"followRedirect,omitempty"`
	Sniffing     Sniffing `json:"sniffing"`
	Tag          string   `json:"tag,omitempty"` // default: "tproxy-in"
}

// SocksInbound: local SOCKS5 endpoint (e.g. for LAN clients).
type SocksInbound struct {
	ListenIP string   `json:"listenIP"`
	Port     int      `json:"port"`
	Auth     string   `json:"auth,omitempty"` // noauth|password
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	UDP      bool     `json:"udp"`
	IP       string   `json:"ip,omitempty"` // bind IP for UDP
	Sniffing Sniffing `json:"sniffing"`
	// Stream is optional: lets the operator wrap the inbound in TLS/REALITY/
	// a transport (e.g. SOCKS-over-TLS). Empty = plain SOCKS, as before.
	Stream *StreamSettings `json:"stream,omitempty"`
	Tag    string          `json:"tag,omitempty"`
}

// HTTPInbound: local HTTP proxy endpoint.
type HTTPInbound struct {
	ListenIP string   `json:"listenIP"`
	Port     int      `json:"port"`
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	Sniffing Sniffing `json:"sniffing"`
	// Stream is optional (see SocksInbound.Stream). Empty = plain HTTP.
	Stream *StreamSettings `json:"stream,omitempty"`
	Tag    string          `json:"tag,omitempty"`
}

// DNSInbound: Xray DNS server inbound for split DNS / fakedns routing.
// Address is REQUIRED — it is the "upstream" address Xray's DNS handler
// sees as the destination of incoming queries; the controller refuses to
// invent a default (e.g., a 1.1.1.1 placeholder) per the "no silent
// normalization" rule. Operator sets it explicitly.
type DNSInbound struct {
	ListenIP string `json:"listenIP"`
	Port     int    `json:"port"`
	Address  string `json:"address"`
	Network  string `json:"network,omitempty"` // tcp|udp|tcp,udp
	Tag      string `json:"tag,omitempty"`
}

// DokodemoInbound: arbitrary-destination forwarder (Xray-specific).
type DokodemoInbound struct {
	ListenIP    string   `json:"listenIP"`
	Port        int      `json:"port"`
	Address     string   `json:"address,omitempty"`
	TargetPort  int      `json:"targetPort,omitempty"`
	Network     string   `json:"network,omitempty"` // tcp|udp|tcp,udp
	FollowRedir bool     `json:"followRedirect,omitempty"`
	Sniffing    Sniffing `json:"sniffing"`
	Tag         string   `json:"tag,omitempty"`
}

// SSInbound: local Shadowsocks server inbound.
type SSInbound struct {
	ListenIP string   `json:"listenIP"`
	Port     int      `json:"port"`
	Method   string   `json:"method"`
	Password string   `json:"password"`
	Network  string   `json:"network,omitempty"`
	Sniffing Sniffing `json:"sniffing"`
	// Stream is optional (see SocksInbound.Stream). Empty = plain Shadowsocks.
	Stream *StreamSettings `json:"stream,omitempty"`
	Tag    string          `json:"tag,omitempty"`
}

// RealityInbound: server-side REALITY inbound (advanced; rarely used on a client controller).
type RealityInbound struct {
	ListenIP   string             `json:"listenIP"`
	Port       int                `json:"port"`
	Protocol   string             `json:"protocol"` // vless typically
	Settings   map[string]any     `json:"settings"`
	Stream     *StreamSettings    `json:"stream,omitempty"`
	Sniffing   Sniffing           `json:"sniffing"`
	Tag        string             `json:"tag,omitempty"`
}

// Sniffing: traffic-type sniff at inbound for routing.
type Sniffing struct {
	Enabled         bool     `json:"enabled"`
	DestOverride    []string `json:"destOverride,omitempty"` // http|tls|quic|fakedns
	DomainsExcluded []string `json:"domainsExcluded,omitempty"`
	MetadataOnly    bool     `json:"metadataOnly,omitempty"`
	RouteOnly       bool     `json:"routeOnly,omitempty"`
}

// DNS holds Xray DNS server configuration (separate from any local resolver).
type DNS struct {
	Servers       []DNSServer       `json:"servers"`
	Hosts         map[string]string `json:"hosts,omitempty"`
	ClientIP      string            `json:"clientIp,omitempty"`
	QueryStrategy string            `json:"queryStrategy,omitempty"` // UseIP|UseIPv4|UseIPv6
	DisableCache  bool              `json:"disableCache,omitempty"`
	DisableFallback bool            `json:"disableFallback,omitempty"`
	DisableFallbackIfMatch bool     `json:"disableFallbackIfMatch,omitempty"`
	Tag           string            `json:"tag,omitempty"` // for routing back through this DNS
}

// DNSServer is either a simple address ("1.1.1.1") or a typed object form.
// JSON marshalling produces the object form when any non-default field is set.
type DNSServer struct {
	Address      string   `json:"address"`
	Port         int      `json:"port,omitempty"`
	ClientIP     string   `json:"clientIp,omitempty"`
	SkipFallback bool     `json:"skipFallback,omitempty"`
	Domains      []string `json:"domains,omitempty"`
	ExpectIPs    []string `json:"expectIPs,omitempty"`
	QueryStrategy string  `json:"queryStrategy,omitempty"`
	// FinalQuery indicates the resolved IPs of this server itself.
	// Used to break recursion when remote DNS is reached via proxy.
	FinalQuery   bool     `json:"finalQuery,omitempty"`
	Tag          string   `json:"tag,omitempty"`
}

// FakeDNS configures Xray's FakeDNS pool (used with sniffing).
type FakeDNS struct {
	IPPool    string `json:"ipPool"`
	PoolSize  int    `json:"poolSize"`
}

// Routing settings.
type Routing struct {
	DomainStrategy string         `json:"domainStrategy,omitempty"` // AsIs|IPIfNonMatch|IPOnDemand
	DomainMatcher  string         `json:"domainMatcher,omitempty"`  // hybrid|linear
	Rules          []RoutingRule  `json:"rules"`
	Balancers      []Balancer     `json:"balancers,omitempty"`
}

// RoutingRule mirrors Xray routing rule semantics.
type RoutingRule struct {
	Type        string   `json:"type,omitempty"` // "field" (Xray's default)
	Domain      []string `json:"domain,omitempty"`
	Domains     []string `json:"domains,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Port        string   `json:"port,omitempty"` // "53,80,443" or "10000-20000"
	SourcePort  string   `json:"sourcePort,omitempty"`
	Network     string   `json:"network,omitempty"` // tcp|udp|tcp,udp
	Source      []string `json:"source,omitempty"`
	User        []string `json:"user,omitempty"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	Protocol    []string `json:"protocol,omitempty"` // http|tls|bittorrent
	Attrs       string   `json:"attrs,omitempty"`
	// Exactly one of OutboundTag or BalancerTag.
	OutboundTag string   `json:"outboundTag,omitempty"`
	BalancerTag string   `json:"balancerTag,omitempty"`
	// Diagnostics:
	Tag         string   `json:"tag,omitempty"`     // operator-visible label
	Comment     string   `json:"comment,omitempty"` // free text, stripped on render
}

// Balancer for round-robin / health-based outbound selection.
type Balancer struct {
	Tag         string            `json:"tag"`
	Selector    []string          `json:"selector,omitempty"`
	FallbackTag string            `json:"fallbackTag,omitempty"`
	Strategy    *BalancerStrategy `json:"strategy,omitempty"`
}

type BalancerStrategy struct {
	Type     string         `json:"type"` // random|leastPing|leastLoad|roundRobin
	Settings map[string]any `json:"settings,omitempty"`
}

// Subscription describes an upstream node feed.
type Subscription struct {
	ID            string            `json:"id"`
	Remark        string            `json:"remark,omitempty"`
	URL           string            `json:"url"`
	Enabled       bool              `json:"enabled"`
	UserAgent     string            `json:"userAgent,omitempty"`     // empty = process default; "passwall2" auto-expands
	UpdateMinutes int               `json:"updateMinutes,omitempty"` // 0 = manual only
	Fetch         FetchPolicy       `json:"fetch"`
	Filter        FilterPolicy      `json:"filter"`
	Group         string            `json:"group,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"` // extra headers to send
	// PreservedTags ensures these node tags are kept across refreshes
	// even if they disappear from the upstream feed.
	PreservedTags []string          `json:"preservedTags,omitempty"`
}

// FetchPolicy controls HTTP behavior for subscription fetches.
type FetchPolicy struct {
	Mode            string `json:"mode,omitempty"`            // auto|direct|proxy
	ConnectTimeoutS int    `json:"connectTimeoutS,omitempty"` // default 5
	MaxTimeoutS     int    `json:"maxTimeoutS,omitempty"`     // default 30
	Retries         int    `json:"retries,omitempty"`         // default 2
	ImpersonatePassWall bool `json:"impersonatePassWall"`     // send PassWall headers (x-device-* + x-hwid)
}

// FilterPolicy is post-fetch node filtering.
type FilterPolicy struct {
	Mode           string   `json:"mode,omitempty"`           // off|discard|keep
	Keywords       []string `json:"keywords,omitempty"`
	IncludeRegex   []string `json:"includeRegex,omitempty"`
	ExcludeRegex   []string `json:"excludeRegex,omitempty"`
	OnlyProtocols  []string `json:"onlyProtocols,omitempty"`  // vless|vmess|trojan|shadowsocks|hysteria2
	OnlyTransports []string `json:"onlyTransports,omitempty"` // tcp|ws|grpc|...
}

// Geo data sources.
type Geo struct {
	AssetDir       string    `json:"assetDir"`       // e.g., /usr/share/xray
	GeoIPURL       string    `json:"geoipUrl"`
	GeoSiteURL     string    `json:"geositeUrl"`
	UpdateSchedule string    `json:"updateSchedule,omitempty"` // cron expression or "weekly"|"daily"
	UpdateOnStart  bool      `json:"updateOnStart"`
	ExtraAssets    []GeoFile `json:"extraAssets,omitempty"`
}

type GeoFile struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256,omitempty"`
}

// Policy passes through Xray policy block.
type Policy struct {
	Levels map[string]PolicyLevel `json:"levels,omitempty"`
	System *SystemPolicy          `json:"system,omitempty"`
}

type PolicyLevel struct {
	Handshake         int  `json:"handshake,omitempty"`
	ConnIdle          int  `json:"connIdle,omitempty"`
	UplinkOnly        int  `json:"uplinkOnly,omitempty"`
	DownlinkOnly      int  `json:"downlinkOnly,omitempty"`
	StatsUserUplink   bool `json:"statsUserUplink,omitempty"`
	StatsUserDownlink bool `json:"statsUserDownlink,omitempty"`
	BufferSize        int  `json:"bufferSize,omitempty"`
}

type SystemPolicy struct {
	StatsInboundUplink   bool `json:"statsInboundUplink,omitempty"`
	StatsInboundDownlink bool `json:"statsInboundDownlink,omitempty"`
	StatsOutboundUplink  bool `json:"statsOutboundUplink,omitempty"`
	StatsOutboundDownlink bool `json:"statsOutboundDownlink,omitempty"`
}

// StatsConfig enables the Stats service.
type StatsConfig struct {
	Enabled bool `json:"enabled"`
}

// APIConfig enables the Xray gRPC API surface.
type APIConfig struct {
	Tag      string   `json:"tag"`              // e.g. "api"
	Services []string `json:"services"`         // HandlerService|StatsService|LoggerService|ObservatoryService|RoutingService
	Listen   string   `json:"listen,omitempty"` // address+port for the gRPC inbound; if empty, an internal inbound is created.
}

// ReverseConfig: reverse proxy entries (bridge/portal).
type ReverseConfig struct {
	Bridges []ReverseEndpoint `json:"bridges,omitempty"`
	Portals []ReverseEndpoint `json:"portals,omitempty"`
}

type ReverseEndpoint struct {
	Tag    string `json:"tag"`
	Domain string `json:"domain"`
}

// MetricsConfig: separate metrics inbound for internal stats scraping.
// Listen is optional: when set (host:port), the controller synthesizes the
// matching dokodemo-door inbound bound to Tag plus a routing rule, exactly as
// it does for the API surface. When empty, only the metrics block is emitted
// and the operator is responsible for declaring the inbound — the controller
// will not invent a listen address (no silent normalization).
type MetricsConfig struct {
	Tag    string `json:"tag"`
	Listen string `json:"listen,omitempty"` // host:port for the synthesized metrics inbound
}

// ObservatoryConfig configures Xray's connection observatory, which probes the
// outbounds named by SubjectSelector and exposes health/latency the leastPing
// balancer strategy uses. All fields are operator-set; the controller invents
// nothing.
type ObservatoryConfig struct {
	SubjectSelector   []string `json:"subjectSelector,omitempty"` // outbound-tag prefixes to observe
	ProbeURL          string   `json:"probeUrl,omitempty"`        // e.g. https://www.google.com/generate_204
	ProbeInterval     string   `json:"probeInterval,omitempty"`   // duration ("10m", "1h")
	EnableConcurrency bool     `json:"enableConcurrency,omitempty"`
}

// BurstObservatoryConfig configures Xray's burst observatory (used by the
// leastLoad strategy). PingConfig drives the latency sampling.
type BurstObservatoryConfig struct {
	SubjectSelector []string         `json:"subjectSelector,omitempty"`
	PingConfig      *ObservatoryPing `json:"pingConfig,omitempty"`
}

// ObservatoryPing is the burst-observatory probe configuration.
type ObservatoryPing struct {
	Destination   string `json:"destination,omitempty"`   // e.g. https://connectivitycheck.gstatic.com/generate_204
	Connectivity  string `json:"connectivity,omitempty"`  // optional connectivity-check URL
	Interval      string `json:"interval,omitempty"`      // duration ("5m")
	Timeout       string `json:"timeout,omitempty"`       // duration ("30s")
	SamplingCount int    `json:"samplingCount,omitempty"` // number of samples to keep
}

// Normalization toggles. Each toggle, when true, will TRANSFORM operator values.
// The default for every toggle is FALSE — we do not silently rewrite anything.
type Normalization struct {
	// ForceFingerprint overrides every node's TLS/REALITY fingerprint with the
	// FingerprintValue. Default: false. Logged on every change.
	ForceFingerprint   bool   `json:"forceFingerprint"`
	FingerprintValue   string `json:"fingerprintValue,omitempty"`
	// CollapseDuplicateSubscriptionPrefix collapses repeated "vectra_sub_" / "passwall_sub_" prefixes.
	CollapseDuplicateSubscriptionPrefix bool `json:"collapseDuplicateSubscriptionPrefix"`
	// DropDeadNodes removes nodes that fail final URL test during build. Default false.
	DropDeadNodes bool `json:"dropDeadNodes"`
}
