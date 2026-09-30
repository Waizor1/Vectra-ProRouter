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
	// Route is where the main traffic goes now and what the failover
	// watchdog moved it off (spec decision 6); null before its first look.
	Route *RouteView `json:"route"`
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
