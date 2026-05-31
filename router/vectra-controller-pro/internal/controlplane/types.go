// Package controlplane is the wire layer between a Vectra Controller Pro
// router agent and the operator panel. It speaks the SAME HTTP contract as
// the legacy vectra-controller-agent (protocol "2026-04-v1": register /
// check-in / job-result) so the panel treats an xray-direct router as the
// same fleet member — only the reported engineMode and the job/config types
// differ.
package controlplane

import "encoding/json"

// ProtocolVersion is the wire contract version shared with the panel and the
// legacy agent. It MUST stay in lockstep with packages/contracts
// (VECTRA_PROTOCOL_VERSION) and vectra-controller-agent.
const ProtocolVersion = "2026-04-v1"

// EngineModeXrayDirect is the engineMode this controller reports.
const EngineModeXrayDirect = "xray-direct"

// RouterResources mirrors the legacy agent's resource report so the panel's
// resource-guard and version-drift surfaces work unchanged.
type RouterResources struct {
	MemoryTotalMB     int `json:"memoryTotalMb"`
	MemoryAvailableMB int `json:"memoryAvailableMb"`
	SwapTotalMB       int `json:"swapTotalMb"`
	SwapFreeMB        int `json:"swapFreeMb"`
	OverlayFreeMB     int `json:"overlayFreeMb"`
	TMPFreeMB         int `json:"tmpFreeMb"`
}

// RouterRulesAssets reports the on-disk geo asset versions.
type RouterRulesAssets struct {
	AssetDirectory   string `json:"assetDirectory,omitempty"`
	GeoIPVersion     string `json:"geoipVersion,omitempty"`
	GeoSiteVersion   string `json:"geositeVersion,omitempty"`
	GeoIPUpdatedAt   string `json:"geoipUpdatedAt,omitempty"`
	GeoSiteUpdatedAt string `json:"geositeUpdatedAt,omitempty"`
}

// RouterServiceHealth keeps the legacy fields (controller/passwall/dnsmasq)
// for panel compatibility and ADDS xray-native fields. On an xray-direct
// router, Passwall/PasswallServer report "disabled" and Xray carries the live
// proxy state.
type RouterServiceHealth struct {
	Controller     string `json:"controller"`
	Xray           string `json:"xray"`
	DNSMasq        string `json:"dnsmasq"`
	Passwall       string `json:"passwall,omitempty"`
	PasswallServer string `json:"passwallServer,omitempty"`
}

// RouterReachabilityProbe is a single (or aggregated) connectivity check.
type RouterReachabilityProbe struct {
	ID             string                    `json:"id,omitempty"`
	Label          string                    `json:"label,omitempty"`
	Reachable      bool                      `json:"reachable"`
	CheckedAt      string                    `json:"checkedAt"`
	TargetURL      string                    `json:"targetUrl,omitempty"`
	StatusCode     int                       `json:"statusCode,omitempty"`
	Error          string                    `json:"error,omitempty"`
	Status         string                    `json:"status,omitempty"`
	ReachableCount int                       `json:"reachableCount,omitempty"`
	TotalCount     int                       `json:"totalCount,omitempty"`
	Checks         []RouterReachabilityProbe `json:"checks,omitempty"`
}

// RouterInventory is the device state report. It is a superset-compatible
// version of the legacy inventory: every field the panel already parses is
// preserved, and engineMode + xray-native fields are added (the panel accepts
// these as optional after the Phase 2 contract change).
type RouterInventory struct {
	ProtocolVersion          string                   `json:"protocolVersion"`
	EngineMode               string                   `json:"engineMode"`
	DeviceIdentifier         string                   `json:"deviceIdentifier"`
	DevicePublicKey          string                   `json:"devicePublicKey"`
	ControllerVersion        string                   `json:"controllerVersion"`
	ControllerRuntimeVersion string                   `json:"controllerRuntimeVersion,omitempty"`
	Hostname                 string                   `json:"hostname,omitempty"`
	PanelDomain              string                   `json:"panelDomain,omitempty"`
	Model                    string                   `json:"model"`
	BoardName                string                   `json:"boardName"`
	LayoutFamily             string                   `json:"layoutFamily,omitempty"`
	Target                   string                   `json:"target"`
	Architecture             string                   `json:"architecture"`
	OpenWrtRelease           string                   `json:"openwrtRelease"`
	OpenWrtDescription       string                   `json:"openwrtDescription,omitempty"`
	// PasswallEnabled is required by the panel's inventory schema; on an
	// xray-direct router it is always false (PassWall2 is not the data plane).
	PasswallEnabled bool   `json:"passwallEnabled"`
	XrayEnabled     bool   `json:"xrayEnabled"`
	XrayVersion     string `json:"xrayVersion,omitempty"`
	SelectedNodeID           string                   `json:"selectedNodeId,omitempty"`
	SelectedNodeLabel        string                   `json:"selectedNodeLabel,omitempty"`
	NodeCount                int                      `json:"nodeCount"`
	SubscriptionCount        int                      `json:"subscriptionCount"`
	PackageVersions          map[string]string        `json:"packageVersions,omitempty"`
	BinaryVersions           map[string]string        `json:"binaryVersions,omitempty"`
	RulesAssets              RouterRulesAssets        `json:"rulesAssets"`
	Resources                RouterResources          `json:"resources"`
	ServiceHealth            RouterServiceHealth      `json:"serviceHealth"`
	PanelReachability        *RouterReachabilityProbe `json:"panelReachability,omitempty"`
	RUReachability           *RouterReachabilityProbe `json:"ruReachability,omitempty"`
	ForeignReachability      *RouterReachabilityProbe `json:"foreignReachability,omitempty"`
	ConfigDigest             string                   `json:"configDigest,omitempty"`
	AppliedRevisionID        string                   `json:"appliedRevisionId,omitempty"`
}

// RouterHealth is the rescue/connectivity summary sent on check-in.
type RouterHealth struct {
	CurrentMode                 string `json:"currentMode"`
	PublicConnectivityFailures  int    `json:"publicConnectivityFailures"`
	DirectConnectivitySuccesses int    `json:"directConnectivitySuccesses"`
	ProxyConnectivitySuccesses  int    `json:"proxyConnectivitySuccesses"`
	ServerReachable             bool   `json:"serverReachable"`
}

// Job is a unit of work delivered by the panel.
type Job struct {
	ID                string                 `json:"id"`
	Type              string                 `json:"type"`
	State             string                 `json:"state"`
	CreatedAt         string                 `json:"createdAt"`
	DesiredRevisionID string                 `json:"desiredRevisionId,omitempty"`
	Payload           map[string]interface{} `json:"payload"`
}

// DesiredRevisionImpact describes what applying a revision will touch.
type DesiredRevisionImpact struct {
	ChangedSections      []string `json:"changedSections"`
	RequiresRestart      bool     `json:"requiresRestart"`
	RefreshSubscriptions bool     `json:"refreshSubscriptions"`
	RefreshRules         bool     `json:"refreshRules"`
	PackageInstall       bool     `json:"packageInstall"`
}

// DesiredRevisionSummary carries the operator's desired xray config. The
// Config field is the raw JSON of an xray config.Config (schema 1); apply
// decodes it lazily so this package stays decoupled from internal/config.
type DesiredRevisionSummary struct {
	ID             string                `json:"id"`
	RevisionNumber int                   `json:"revisionNumber"`
	Status         string                `json:"status"`
	Origin         string                `json:"origin"`
	EngineMode     string                `json:"engineMode,omitempty"`
	ConfigDigest   string                `json:"configDigest,omitempty"`
	Config         json.RawMessage       `json:"config"`
	Impact         DesiredRevisionImpact `json:"impact"`
}

// ConfigSyncState is the panel's view of where this router's config stands.
type ConfigSyncState struct {
	ImportState           string `json:"importState"`
	ActiveRevisionID      string `json:"activeRevisionId,omitempty"`
	LastAppliedRevisionID string `json:"lastAppliedRevisionId,omitempty"`
	LastConfigDigest      string `json:"lastConfigDigest,omitempty"`
	RequestImport         bool   `json:"requestImport,omitempty"`
}

type CheckInRequest struct {
	ProtocolVersion string          `json:"protocolVersion"`
	RouterID        string          `json:"routerId"`
	Inventory       RouterInventory `json:"inventory"`
	Health          RouterHealth    `json:"health"`
}

type CheckInResponse struct {
	ProtocolVersion        string                 `json:"protocolVersion"`
	RouterID               string                 `json:"routerId"`
	Status                 string                 `json:"status"`
	PollingIntervalSeconds int                    `json:"pollingIntervalSeconds"`
	ConfigSyncState        ConfigSyncState        `json:"configSyncState"`
	RescuePolicy           map[string]interface{} `json:"rescuePolicy"`
	UpdatePolicy           map[string]interface{} `json:"updatePolicy"`
	Jobs                   []Job                  `json:"jobs"`
	OperatorMessage        string                 `json:"operatorMessage"`
	DesiredRevision        json.RawMessage        `json:"desiredRevision"`
}

type RegisterRequest struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Inventory       RouterInventory `json:"inventory"`
}

type RegisterResponse struct {
	ProtocolVersion        string          `json:"protocolVersion"`
	RouterID               string          `json:"routerId"`
	Status                 string          `json:"status"`
	IssuedToken            string          `json:"issuedToken"`
	PollingIntervalSeconds int             `json:"pollingIntervalSeconds"`
	PendingApproval        bool            `json:"pendingApproval"`
	ConfigSyncState        ConfigSyncState `json:"configSyncState"`
	OperatorMessage        string          `json:"operatorMessage"`
}

type JobResultRequest struct {
	ProtocolVersion   string                 `json:"protocolVersion"`
	RouterID          string                 `json:"routerId"`
	JobID             string                 `json:"jobId"`
	Status            string                 `json:"status"`
	AppliedRevisionID string                 `json:"appliedRevisionId,omitempty"`
	ConfigDigest      string                 `json:"configDigest,omitempty"`
	Stdout            string                 `json:"stdout,omitempty"`
	Stderr            string                 `json:"stderr,omitempty"`
	Result            map[string]interface{} `json:"result"`
}

type JobResultResponse struct {
	ProtocolVersion string `json:"protocolVersion"`
	Acknowledged    bool   `json:"acknowledged"`
}
