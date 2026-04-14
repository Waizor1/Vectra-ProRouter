package controlplane

import (
	"encoding/json"

	"vectra-controller-agent/internal/passwall"
)

const ProtocolVersion = "2026-04-v1"

type PasswallImportedState struct {
	Config       json.RawMessage        `json:"config"`
	RawSnapshot  map[string]interface{} `json:"rawSnapshot,omitempty"`
	ConfigDigest string                 `json:"configDigest"`
	ImportedAt   string                 `json:"importedAt,omitempty"`
	Source       string                 `json:"source,omitempty"`
}

type RouterResources struct {
	MemoryTotalMB     int `json:"memoryTotalMb"`
	MemoryAvailableMB int `json:"memoryAvailableMb"`
	SwapTotalMB       int `json:"swapTotalMb"`
	SwapFreeMB        int `json:"swapFreeMb"`
	OverlayFreeMB     int `json:"overlayFreeMb"`
	TMPFreeMB         int `json:"tmpFreeMb"`
}

type RouterRulesAssets struct {
	AssetDirectory   string `json:"assetDirectory,omitempty"`
	GeoIPVersion     string `json:"geoipVersion,omitempty"`
	GeoSiteVersion   string `json:"geositeVersion,omitempty"`
	GeoIPUpdatedAt   string `json:"geoipUpdatedAt,omitempty"`
	GeoSiteUpdatedAt string `json:"geositeUpdatedAt,omitempty"`
}

type RouterServiceHealth struct {
	Controller     string `json:"controller"`
	Passwall       string `json:"passwall"`
	PasswallServer string `json:"passwallServer"`
	DNSMasq        string `json:"dnsmasq"`
}

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

type LastRescue struct {
	Mode       string `json:"mode"`
	Reason     string `json:"reason"`
	HappenedAt string `json:"happenedAt"`
}

type RouterInventory struct {
	ProtocolVersion      string                   `json:"protocolVersion"`
	DeviceIdentifier     string                   `json:"deviceIdentifier"`
	DevicePublicKey      string                   `json:"devicePublicKey"`
	ControllerVersion    string                   `json:"controllerVersion"`
	Hostname             string                   `json:"hostname,omitempty"`
	PanelDomain          string                   `json:"panelDomain,omitempty"`
	Model                string                   `json:"model"`
	BoardName            string                   `json:"boardName"`
	LayoutFamily         string                   `json:"layoutFamily,omitempty"`
	Target               string                   `json:"target"`
	Architecture         string                   `json:"architecture"`
	OpenWrtRelease       string                   `json:"openwrtRelease"`
	OpenWrtDescription   string                   `json:"openwrtDescription,omitempty"`
	PasswallEnabled      bool                     `json:"passwallEnabled"`
	SelectedNodeID       string                   `json:"selectedNodeId,omitempty"`
	SelectedNodeLabel    string                   `json:"selectedNodeLabel,omitempty"`
	NodeCount            int                      `json:"nodeCount"`
	SubscriptionCount    int                      `json:"subscriptionCount"`
	PackageVersions      map[string]string        `json:"packageVersions"`
	BinaryVersions       map[string]string        `json:"binaryVersions"`
	RulesAssets          RouterRulesAssets        `json:"rulesAssets"`
	Resources            RouterResources          `json:"resources"`
	ServiceHealth        RouterServiceHealth      `json:"serviceHealth"`
	LastRescue           *LastRescue              `json:"lastRescue,omitempty"`
	TelegramReachability *RouterReachabilityProbe `json:"telegramReachability,omitempty"`
	RawSnapshot          map[string]interface{}   `json:"rawSnapshot,omitempty"`
	ConfigDigest         string                   `json:"configDigest,omitempty"`
	AppliedRevisionID    string                   `json:"appliedRevisionId,omitempty"`
}

type RouterHealth struct {
	CurrentMode                 string `json:"currentMode"`
	PublicConnectivityFailures  int    `json:"publicConnectivityFailures"`
	DirectConnectivitySuccesses int    `json:"directConnectivitySuccesses"`
	ProxyConnectivitySuccesses  int    `json:"proxyConnectivitySuccesses"`
	ServerReachable             bool   `json:"serverReachable"`
}

type Job struct {
	ID                string                 `json:"id"`
	Type              string                 `json:"type"`
	State             string                 `json:"state"`
	CreatedAt         string                 `json:"createdAt"`
	DesiredRevisionID string                 `json:"desiredRevisionId,omitempty"`
	Payload           map[string]interface{} `json:"payload"`
}

type DesiredRevisionImpact struct {
	ChangedSections      []string `json:"changedSections"`
	RequiresRestart      bool     `json:"requiresRestart"`
	RefreshSubscriptions bool     `json:"refreshSubscriptions"`
	RefreshRules         bool     `json:"refreshRules"`
	PackageInstall       bool     `json:"packageInstall"`
	FirmwareValidation   bool     `json:"firmwareValidation"`
}

type DesiredRevisionSummary struct {
	ID             string                 `json:"id"`
	RevisionNumber int                    `json:"revisionNumber"`
	Status         string                 `json:"status"`
	Origin         string                 `json:"origin"`
	ConfigDigest   string                 `json:"configDigest,omitempty"`
	Config         passwall.DesiredConfig `json:"config"`
	Impact         DesiredRevisionImpact  `json:"impact"`
}

type ConfigSyncState struct {
	ImportState             string `json:"importState"`
	PendingImportRevisionID string `json:"pendingImportRevisionId,omitempty"`
	ActiveRevisionID        string `json:"activeRevisionId,omitempty"`
	LastAppliedRevisionID   string `json:"lastAppliedRevisionId,omitempty"`
	LastConfigDigest        string `json:"lastConfigDigest,omitempty"`
	RequestImport           bool   `json:"requestImport,omitempty"`
}

type CheckInRequest struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	RouterID        string                 `json:"routerId"`
	Inventory       RouterInventory        `json:"inventory"`
	Health          RouterHealth           `json:"health"`
	PasswallImport  *PasswallImportedState `json:"passwallImport,omitempty"`
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
	ProtocolVersion string                 `json:"protocolVersion"`
	Inventory       RouterInventory        `json:"inventory"`
	PasswallImport  *PasswallImportedState `json:"passwallImport,omitempty"`
}

type RegisterResponse struct {
	ProtocolVersion        string                 `json:"protocolVersion"`
	RouterID               string                 `json:"routerId"`
	Status                 string                 `json:"status"`
	IssuedToken            string                 `json:"issuedToken"`
	PollingIntervalSeconds int                    `json:"pollingIntervalSeconds"`
	PendingApproval        bool                   `json:"pendingApproval"`
	ConfigSyncState        ConfigSyncState        `json:"configSyncState"`
	RescuePolicy           map[string]interface{} `json:"rescuePolicy"`
	UpdatePolicy           map[string]interface{} `json:"updatePolicy"`
	OperatorMessage        string                 `json:"operatorMessage"`
}

type JobResultRequest struct {
	ProtocolVersion     string                   `json:"protocolVersion"`
	RouterID            string                   `json:"routerId"`
	JobID               string                   `json:"jobId"`
	Status              string                   `json:"status"`
	AppliedRevisionID   string                   `json:"appliedRevisionId,omitempty"`
	ConfigDigest        string                   `json:"configDigest,omitempty"`
	Stdout              string                   `json:"stdout,omitempty"`
	Stderr              string                   `json:"stderr,omitempty"`
	IncidentTransitions []map[string]interface{} `json:"incidentTransitions,omitempty"`
	Result              map[string]interface{}   `json:"result"`
}

type JobResultResponse struct {
	ProtocolVersion string `json:"protocolVersion"`
	Acknowledged    bool   `json:"acknowledged"`
}
