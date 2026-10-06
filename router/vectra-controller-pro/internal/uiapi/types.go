// Package uiapi builds the answers of the router UI's ubus object, `vectra`.
//
// The shapes are the contract in ui/contract/: every type here decodes its
// fixture with unknown fields disallowed and re-encodes it to the same JSON
// (cmd/vctl/rpcd_contract_test.go), so a renamed, retyped or dropped field
// fails a test instead of the UI.
//
// Conventions from the contract: a value the router could not measure is
// null, never 0; times are RFC 3339 UTC; the router answers in codes, never
// prose, so the UI can speak ru, en and zh; credentials never appear.
package uiapi

import "time"

// Status answers `status`.
type Status struct {
	Version      string            `json:"version"`
	Power        Power             `json:"power"`
	Controller   Controller        `json:"controller"`
	Engine       Engine            `json:"engine"`
	API          Endpoint          `json:"api"`
	Metrics      Endpoint          `json:"metrics"`
	Dataplane    Dataplane         `json:"dataplane"`
	ControlPlane ControlPlane      `json:"controlPlane"`
	Subscription SubscriptionState `json:"subscription"`
	Probe        ProbeState        `json:"probe"`
	Pins         map[string]string `json:"pins"`
	Legacy       Legacy            `json:"legacy"`
	Router       Router            `json:"router"`
	UI           UIPolicy          `json:"ui"`
	// RemoteShell: the router's owner lets the panel's support shell run here
	// (UCI remote_shell; ui/contract/README.md, "Support shell").
	RemoteShell bool `json:"remoteShell"`
	// Route is where the main traffic goes now and what the failover
	// watchdog moved it off (spec decision 6); null before its first look.
	Route *RouteView `json:"route"`
	// Tune is the router's tune (internal/tune): what it set, found set or
	// left alone; null when it could not be read.
	Tune *Tune `json:"tune"`
}

// Tune is the router's tune as the status carries it.
type Tune struct {
	// Enabled: vectra-controller-pro.main.tune is not '0'.
	Enabled bool `json:"enabled"`
	// Profile: lowmem (under 384 MiB of RAM) or standard.
	Profile string     `json:"profile"`
	Items   []TuneItem `json:"items"`
}

// TuneItem is one thing the tune sets: zram, swappiness,
// vfs_cache_pressure, packet_steering, flow_offloading, cron_loglevel,
// tmp_leftovers.
type TuneItem struct {
	ID string `json:"id"`
	// State: applied, already, pending, user_set, skipped.
	State string `json:"state"`
	// Value is what the router has now: the zram swap's MiB, a sysctl's
	// value, a UCI option's, the MiB of vctl's leftovers; null when none or
	// unset.
	Value  *string `json:"value"`
	Target string  `json:"target"`
	// Reason is why it is skipped (or not set yet); null when there is none.
	Reason *string `json:"reason"`
}

// RouteView is the server card's line: the nodes the main traffic goes
// through, with their countries, and the node the watchdog moved it off.
type RouteView struct {
	Nodes     []NodeRef  `json:"nodes"`
	MovedFrom *NodeRef   `json:"movedFrom"`
	MovedAt   *time.Time `json:"movedAt"`
	// Reason: failing (another node of the same balancer), fallback (the
	// entry's own reserve), borrowed (another country of the entry).
	Reason *string `json:"reason"`
	// Unfit are exits the router's own check found unable to carry blocked
	// sites, left out of the balancers until they can again.
	Unfit []NodeRef `json:"unfit"`
	// UnfitKept are such exits the main traffic still goes through: the
	// main balancer has no other node, so blocked sites may fail.
	UnfitKept []NodeRef `json:"unfitKept"`
}

// NodeRef is a node and the country its tag names (null when none).
type NodeRef struct {
	Tag     string  `json:"tag"`
	Country *string `json:"country"`
	// Egress is where the router saw it really leave, when that is not the
	// country its name says (1111, 2026-09-30: «ОАЭ» left in Poland).
	Egress *string `json:"egress"`
}

// Power is Vectra's own switch (`vectra on|off`, set_power) and who carries
// the LAN's traffic: internal/power's facts, as the contract names them.
type Power struct {
	// Enabled: switched on — the UCI switch and the boot links, or a trial.
	Enabled bool `json:"enabled"`
	// Running: procd runs vctl.
	Running bool `json:"running"`
	// Holder: vectra, passwall2, agent or direct.
	Holder string `json:"holder"`
	// HandBack: who takes the traffic when Vectra is turned off — passwall2,
	// agent or direct; nil while it is off.
	HandBack *string `json:"handBack"`
	// WouldIdle: Vectra does not run, and switched on now it would carry
	// nothing — no operator config, and no PassWall2 to route by: the LAN
	// would go out without a VPN until the router is linked. set_power
	// refuses to turn it on then unless asked with force (the UI confirms).
	WouldIdle bool `json:"wouldIdle"`
}

// UIPolicy is how the operator set up this router's UI.
type UIPolicy struct {
	// Locked: only the simple view; the router refuses the Pro methods itself
	// (UCI vectra-controller-pro.main.ui_lock).
	Locked bool `json:"locked"`
}

type Controller struct {
	Running   bool `json:"running"`
	PID       *int `json:"pid"`
	UptimeSec *int `json:"uptimeSec"`
}

type Engine struct {
	State          string    `json:"state"`
	XrayVersion    *string   `json:"xrayVersion"`
	PID            *int      `json:"pid"`
	UptimeSec      *int      `json:"uptimeSec"`
	RSSMiB         *float64  `json:"rssMiB"`
	MemoryLimitMiB *int      `json:"memoryLimitMiB"`
	Restarts       int       `json:"restarts"`
	LastExit       *LastExit `json:"lastExit"`
}

type LastExit struct {
	At    *string `json:"at"`
	Code  int     `json:"code"`
	Error string  `json:"error"`
}

type Endpoint struct {
	Listen    *string `json:"listen"`
	Reachable bool    `json:"reachable"`
}

type Dataplane struct {
	Loaded     bool             `json:"loaded"`
	KillSwitch bool             `json:"killSwitch"`
	Counters   map[string]int64 `json:"counters"`
}

type ControlPlane struct {
	Reachable   *bool   `json:"reachable"`
	LastCheckIn *string `json:"lastCheckIn"`
	RouterID    *string `json:"routerId"`
}

type SubscriptionState struct {
	EntryIndex    *int    `json:"entryIndex"`
	EntryRemark   *string `json:"entryRemark"`
	EntryCount    *int    `json:"entryCount"`
	FetchedAt     *string `json:"fetchedAt"`
	Source        string  `json:"source"`
	OverrideStale bool    `json:"overrideStale"`
}

type ProbeState struct {
	IntervalSec *int   `json:"intervalSec"`
	Source      string `json:"source"`
}

type Legacy struct {
	AgentEnabled    bool `json:"agentEnabled"`
	PasswallRunning bool `json:"passwallRunning"`
	// Passwall is PassWall2 on the router (internal/retire): installed (the
	// takeover's way back), retired (vctl removed it after a day of carrying
	// the traffic: PasswallRetiredAt says when) or absent; nil when not
	// looked at.
	Passwall          *string `json:"passwall"`
	PasswallRetiredAt *string `json:"passwallRetiredAt"`
}

type Router struct {
	Hostname        string    `json:"hostname"`
	Model           *string   `json:"model"`
	Release         *string   `json:"release"`
	MemTotalMiB     *int      `json:"memTotalMiB"`
	MemAvailableMiB *int      `json:"memAvailableMiB"`
	OverlayFreeMiB  *int      `json:"overlayFreeMiB"`
	TmpFreeMiB      *int      `json:"tmpFreeMiB"`
	Load            []float64 `json:"load"`
	UptimeSec       *int      `json:"uptimeSec"`
}

// Balancers answers `balancers`.
type Balancers struct {
	APIReachable bool           `json:"apiReachable"`
	Probe        BalancersProbe `json:"probe"`
	// DefaultOutbound is the tag of xray's default outbound — the config's
	// FIRST outbound: where xray sends a connection no rule routes, and a
	// balancer's that has neither a target nor a fallback. nil when there are
	// no outbounds, or the first has no tag.
	DefaultOutbound *string    `json:"defaultOutbound"`
	Balancers       []Balancer `json:"balancers"`
}

type BalancersProbe struct {
	IntervalSec         *int    `json:"intervalSec"`
	ProviderIntervalSec *int    `json:"providerIntervalSec"`
	Source              string  `json:"source"`
	Sampling            *int    `json:"sampling"`
	TimeoutSec          *int    `json:"timeoutSec"`
	Destination         *string `json:"destination"`
}

type Balancer struct {
	Tag      string    `json:"tag"`
	Role     string    `json:"role"`
	Strategy string    `json:"strategy"`
	Expected *int      `json:"expected"`
	Selector []string  `json:"selector"`
	Members  []string  `json:"members"`
	Fallback *Fallback `json:"fallback"`
	// Selected is nil (JSON null) when xray's API could not be asked, and an
	// empty slice when it was asked and no member qualifies.
	Selected []string  `json:"selected"`
	Pinned   *string   `json:"pinned"`
	Matchers []Matcher `json:"matchers"`
}

type Fallback struct {
	Tag      string  `json:"tag"`
	Balancer *string `json:"balancer"`
}

type Matcher struct {
	Kind    string   `json:"kind"`
	Network *string  `json:"network"`
	Sample  []string `json:"sample"`
	Total   int      `json:"total"`
}

// Nodes answers `nodes`.
type Nodes struct {
	ObservedAt *string `json:"observedAt"`
	Nodes      []Node  `json:"nodes"`
}

type Node struct {
	Tag         string   `json:"tag"`
	Protocol    string   `json:"protocol"`
	Transport   string   `json:"transport"`
	Security    string   `json:"security"`
	Address     *string  `json:"address"`
	Port        *int     `json:"port"`
	CountryHint *string  `json:"countryHint"`
	Alive       *bool    `json:"alive"`
	DelayMs     *int     `json:"delayMs"`
	LastSeen    *string  `json:"lastSeen"`
	LastTry     *string  `json:"lastTry"`
	Traffic     *Traffic `json:"traffic"`
	Balancers   []string `json:"balancers"`
}

type Traffic struct {
	UpBytes   int64 `json:"upBytes"`
	DownBytes int64 `json:"downBytes"`
}

// Entries answers `entries`.
type Entries struct {
	Active        *int    `json:"active"`
	PanelIndex    *int    `json:"panelIndex"`
	Source        string  `json:"source"`
	OverrideStale bool    `json:"overrideStale"`
	FetchedAt     *string `json:"fetchedAt"`
	Cached        bool    `json:"cached"`
	Entries       []Entry `json:"entries"`
}

type Entry struct {
	Index         int    `json:"index"`
	Remark        string `json:"remark"`
	NodeCount     int    `json:"nodeCount"`
	BalancerCount int    `json:"balancerCount"`
}

// Diagnostics answers `diagnostics`.
type Diagnostics struct {
	CheckedAt string  `json:"checkedAt"`
	Checks    []Check `json:"checks"`
}

type Check struct {
	ID     string                 `json:"id"`
	Status string                 `json:"status"`
	Params map[string]interface{} `json:"params"`
}

// Logs answers `logs`.
type Logs struct {
	Lines []LogLine `json:"lines"`
}

type LogLine struct {
	Time    *string `json:"time"`
	Level   string  `json:"level"`
	Source  *string `json:"source"`
	Message string  `json:"message"`
}

// Rules answers `rules`: the router owner's own sites ("My sites"), in the
// form the router keeps them (internal/sites).
type Rules struct {
	// Direct always goes without the VPN, Proxy always through it.
	Direct []string `json:"direct"`
	Proxy  []string `json:"proxy"`
	// Max is how many sites each list may hold.
	Max int `json:"max"`
	// Catalog are the services «+ сервис» offers: the categories of the
	// router's geo file, each kept in a list as "geosite:<name>".
	Catalog []string `json:"catalog"`
	// Missing are services in the lists the geo file no longer has (an
	// update dropped them): kept, but not routed until it has them again.
	Missing []string `json:"missing"`
}

// Action answers every mutating method.
type Action struct {
	OK     bool    `json:"ok"`
	Code   string  `json:"code"`
	Detail *string `json:"detail"`
}

// PortForwards answers `port_forwards` (ui/contract/README.md, "Port
// forwards"): the owner's port forwards as the router keeps them (fw4
// redirects vectra_pf_*), the devices a forward can point at, and whether the
// WAN cannot be reached from outside at all. No MAC address is ever part of
// it, and nobody else's redirects are listed.
type PortForwards struct {
	Rules   []PortForward       `json:"rules"`
	Devices []PortForwardDevice `json:"devices"`
	// CGNAT: the WAN's IPv4 address is the provider's shared one
	// (100.64.0.0/10) or a private one — a forward is not reachable from the
	// internet (portfwd.CGNAT).
	CGNAT bool `json:"cgnat"`
	// DirectActive: whether «past the VPN» is in effect for every enabled rule
	// asking for it — null when none asks, false when some rule's flag is not
	// carried out (Vectra off or stopped, the data plane's set not written;
	// portfwd.DirectActive).
	DirectActive *bool `json:"directActive"`
	// Max is how many rules of its own the router keeps.
	Max int `json:"max"`
}

// PortForward is one of the owner's rules: the same port outside and on the
// device. DeviceName is read from the DHCP leases and static hosts.
type PortForward struct {
	ID string `json:"id"`
	// Preset is the UI's tag for what the forward is for; null for none.
	Preset     *string `json:"preset"`
	DestIP     string  `json:"destIp"`
	DeviceName *string `json:"deviceName"`
	Port       string  `json:"port"`
	Proto      string  `json:"proto"`
	Direct     bool    `json:"direct"`
	Enabled    bool    `json:"enabled"`
}

// PortForwardDevice is a LAN device a forward can point at.
type PortForwardDevice struct {
	Name *string `json:"name"`
	IP   string  `json:"ip"`
}
