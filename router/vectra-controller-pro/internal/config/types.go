// Package config holds the operator-facing configuration schema for
// Vectra Controller Pro.
//
// Since the "consume provider JSON" pivot this schema is deliberately SMALL.
// The proxy config itself (log/dns/outbounds/routing/policy/stats/...) is no
// longer authored here — it arrives as a complete Xray document from the
// provider subscription and is adopted byte-for-byte. What remains is only
// what the controller itself has to know:
//
//	Process       — how to supervise the xray child process
//	Inbounds.Tproxy — the ONE block the controller splices into the provider doc
//	Geo           — where geo assets live and where to refresh them from
//	Subscriptions — which URL/UA/headers to fetch the provider document with
//
// Design rules:
//   - No silent normalization. What the operator sets is what the controller uses.
//   - Every default is applied in defaults.go and is loggable.
//   - Unknown fields are rejected (Unmarshal uses DisallowUnknownFields) so a
//     provider document can never be mistaken for an operator config.
package config

// SchemaVersion is the current top-level schema version.
const SchemaVersion = 1

// Config is the root operator config consumed by Vectra Controller Pro.
type Config struct {
	Schema        int            `json:"schema"`
	Instance      Instance       `json:"instance"`
	Process       Process        `json:"process"`
	Inbounds      Inbounds       `json:"inbounds"`
	Geo           Geo            `json:"geo"`
	Subscriptions []Subscription `json:"subscriptions,omitempty"`
	// UI is how the operator sets up the router's own UI from the panel.
	UI *UIPolicy `json:"ui,omitempty"`
}

// UIPolicy: Lock keeps the router UI to its simple view, like UCI
// vectra-controller-pro.main.ui_lock (either one locks it).
type UIPolicy struct {
	Lock bool `json:"lock"`
}

// Instance metadata (identity + logging).
type Instance struct {
	Name     string `json:"name,omitempty"`     // e.g., router hostname
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

// Inbounds: the inbound the controller owns. There is exactly one — the
// provider document supplies everything else, and its own socks/http inbounds
// are DROPPED by the splice (they carry no "listen" key and would otherwise
// bind 0.0.0.0, i.e. an open proxy on the LAN).
type Inbounds struct {
	Tproxy *TproxyInbound `json:"tproxy,omitempty"`
}

// TproxyInbound: transparent proxy via TPROXY (Linux only).
type TproxyInbound struct {
	ListenIP   string   `json:"listenIP"`
	Port       int      `json:"port"`
	FwMark     int      `json:"fwmark,omitempty"`
	UDPEnabled bool     `json:"udpEnabled"`
	Sniffing   Sniffing `json:"sniffing"`
	Tag        string   `json:"tag,omitempty"` // default: "tproxy-in"
	// KillSwitch makes forwarded LAN-client traffic fail CLOSED at the firewall:
	// a client packet that was not carried by the proxy is dropped at the FORWARD
	// hook rather than forwarded out the WAN in the clear. The router's own
	// traffic (control plane, xray egress, DNS service) never traverses that hook
	// and is structurally unaffected. See internal/firewall Spec.KillSwitch for
	// the two leak paths it closes.
	//
	// DEFAULT: OFF, deliberately, and it stays off until an operator turns it on
	// per router. The failure this fleet actually has is xray dying — RSS soft-cap
	// reloads and OOM crash-loops on 234 MB units. Today that degrades to direct
	// (PassWall2 parity): the customer keeps working, unproxied. On-by-default
	// would convert every one of those episodes into a total internet outage for
	// that customer, delivered fleet-wide by a controller update rather than by an
	// operator decision — and this package's contract is that a controller upgrade
	// never silently changes what the operator configured (see the package doc's
	// "no silent normalization" rule).
	//
	// That is a bounded trade, not a dismissal of the leak: the guard now COUNTS
	// what it would have dropped (firewall.CounterKillSwitchDrops), so the
	// exposure is measurable per router before anyone commits to it fleet-wide.
	// Intended rollout: enable on the operator's own router first, then a canary,
	// then the fleet — each step an explicit config change the panel already
	// supports per router.
	KillSwitch bool `json:"killSwitch,omitempty"`
}

// Sniffing: traffic-type sniff at inbound for routing.
type Sniffing struct {
	Enabled         bool     `json:"enabled"`
	DestOverride    []string `json:"destOverride,omitempty"` // http|tls|quic (never fakedns: the provider document has no fakedns block)
	DomainsExcluded []string `json:"domainsExcluded,omitempty"`
	MetadataOnly    bool     `json:"metadataOnly,omitempty"`
	RouteOnly       bool     `json:"routeOnly,omitempty"`
}

// Geo data sources.
type Geo struct {
	AssetDir       string    `json:"assetDir"` // vctl\'s own by default (GeoAssetDir); the old /usr/share/v2ray reads as that
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

// Subscription describes the upstream provider feed. Fetching it is the
// router's job — the document must never travel through the panel's revision
// pipeline, which would re-serialize (and therefore corrupt) it.
type Subscription struct {
	ID      string `json:"id"`
	Remark  string `json:"remark,omitempty"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	// UserAgent selects WHICH payload the provider returns. "v2rayNG/1.9.5"
	// yields the complete JSON config array; "passwall2/*", "Xray/*" and
	// "sing-box/*" yield the degraded base64 vless:// link list.
	//
	// Empty — and no User-Agent in Headers either — is the router's own agent,
	// VectraRouter/<version> vr1.<token>: signed per request with the device
	// key and sealed to Vectra's (internal/uatoken). The provider then answers
	// JSON by the URL (Remnawave: the subscription URL plus /json) or by a
	// response rule for it.
	//
	// A "Happ…" agent that is not the real client's
	// "Happ/<version>/<OS>/<build>" is REFUSED (internal/uaguard), here and at
	// fetch time: the provider's anti-fraud sweep deletes the device that
	// sends one and disables the customer's account.
	UserAgent string            `json:"userAgent,omitempty"`
	Group     string            `json:"group,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"` // extra headers to send
	// Mode picks the parse path. "json" (default) consumes the provider's
	// complete Xray documents. "link-list" is the legacy base64 vless:// path,
	// kept reachable for diagnostics but deliberately OFF the default path.
	Mode string `json:"mode,omitempty"` // json|link-list
	// EntryIndex selects which document of the provider array to adopt.
	// EntryRemark takes precedence when set (matched exactly against "remarks").
	EntryIndex  int    `json:"entryIndex,omitempty"`
	EntryRemark string `json:"entryRemark,omitempty"`
	// MaxBytes caps the response read. 0 = DefaultMaxFetchBytes.
	MaxBytes int `json:"maxBytes,omitempty"`
	// AllowInsecureTLS disables the refuse-on-`"allowInsecure":true` guard.
	// Default false: a provider document that disables TLS verification is
	// refused outright rather than silently adopted.
	AllowInsecureTLS bool `json:"allowInsecureTls,omitempty"`
}

// SubscriptionModeJSON / SubscriptionModeLinkList are the valid Mode values.
const (
	SubscriptionModeJSON     = "json"
	SubscriptionModeLinkList = "link-list"
)
