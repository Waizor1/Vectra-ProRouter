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
//
// It covers BOTH panel shapes, which are not the same object:
//
//   - routerReachabilityProbeSchema — the entries of `checks[]`. It declares
//     `id` and `label`.
//   - routerGroupedReachabilitySchema — the top-level panelReachability /
//     ruReachability / foreignReachability fields. It declares NEITHER, plus
//     `status`, `reachableCount`, `totalCount` and `checks`.
//
// zod strips undeclared keys instead of rejecting them, so an `id`/`label` set
// on a GROUPED field is discarded on arrival: computed on the router, sent over
// the wire, logged as a successful check-in, and never stored. Set them only on
// entries that go into Checks.
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
	ProtocolVersion          string `json:"protocolVersion"`
	EngineMode               string `json:"engineMode"`
	DeviceIdentifier         string `json:"deviceIdentifier"`
	DevicePublicKey          string `json:"devicePublicKey"`
	ControllerVersion        string `json:"controllerVersion"`
	ControllerRuntimeVersion string `json:"controllerRuntimeVersion,omitempty"`
	Hostname                 string `json:"hostname,omitempty"`
	PanelDomain              string `json:"panelDomain,omitempty"`
	Model                    string `json:"model"`
	BoardName                string `json:"boardName"`
	LayoutFamily             string `json:"layoutFamily,omitempty"`
	Target                   string `json:"target"`
	Architecture             string `json:"architecture"`
	OpenWrtRelease           string `json:"openwrtRelease"`
	OpenWrtDescription       string `json:"openwrtDescription,omitempty"`
	// PasswallEnabled is required by the panel's inventory schema; on an
	// xray-direct router it is always false (PassWall2 is not the data plane).
	PasswallEnabled     bool                     `json:"passwallEnabled"`
	XrayEnabled         bool                     `json:"xrayEnabled"`
	XrayVersion         string                   `json:"xrayVersion,omitempty"`
	SelectedNodeID      string                   `json:"selectedNodeId,omitempty"`
	SelectedNodeLabel   string                   `json:"selectedNodeLabel,omitempty"`
	NodeCount           int                      `json:"nodeCount"`
	SubscriptionCount   int                      `json:"subscriptionCount"`
	PackageVersions     map[string]string        `json:"packageVersions,omitempty"`
	BinaryVersions      map[string]string        `json:"binaryVersions,omitempty"`
	RulesAssets         RouterRulesAssets        `json:"rulesAssets"`
	Resources           RouterResources          `json:"resources"`
	ServiceHealth       RouterServiceHealth      `json:"serviceHealth"`
	PanelReachability   *RouterReachabilityProbe `json:"panelReachability,omitempty"`
	RUReachability      *RouterReachabilityProbe `json:"ruReachability,omitempty"`
	ForeignReachability *RouterReachabilityProbe `json:"foreignReachability,omitempty"`
	ConfigDigest        string                   `json:"configDigest,omitempty"`
	AppliedRevisionID   string                   `json:"appliedRevisionId,omitempty"`
	// RemoteShell: the router's owner allows the panel's support shell here
	// (run_terminal_command; UCI vectra-controller-pro.main.remote_shell). Off,
	// the router refuses it.
	RemoteShell bool `json:"remoteShell"`
}

// MissingRequiredFields returns the JSON names of the inventory fields the
// panel declares as `z.string().min(1)` WITHOUT `.optional()` and that this
// report would send as "".
//
// The panel answers such a payload with a flat
// `{"error":"Invalid request payload","issues":{"fieldErrors":{"inventory":
// ["String must contain at least 1 character(s)", ...]}}}` — the offending
// field NAMES are erased by zod's flatten(), so a router in this state cannot
// tell an operator what is wrong. Callers log this list before sending so the
// diagnosis is on the router, not guessed from the panel's opaque 400.
//
// Keep in lockstep with routerInventorySchema in packages/contracts/src/schemas.ts.
func (inv RouterInventory) MissingRequiredFields() []string {
	required := []struct {
		name  string
		value string
	}{
		{"deviceIdentifier", inv.DeviceIdentifier},
		{"devicePublicKey", inv.DevicePublicKey},
		{"controllerVersion", inv.ControllerVersion},
		{"model", inv.Model},
		{"boardName", inv.BoardName},
		{"target", inv.Target},
		{"architecture", inv.Architecture},
		{"openwrtRelease", inv.OpenWrtRelease},
	}
	var missing []string
	for _, f := range required {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	return missing
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
	// Claim is the code the router shows for linking it to a Vectra account
	// (ADR-0006), as its hash; null once the router is linked.
	Claim *ClaimAnnouncement `json:"claim"`
}

// ClaimAnnouncement tells the panel which code the router shows: never the
// code, only claim.CodeHash of it, and until when it is valid.
type ClaimAnnouncement struct {
	CodeHash  string `json:"codeHash"`
	ExpiresAt string `json:"expiresAt"`
}

// ClaimKey is Vectra's key the router seals its claim QR to: a raw X25519
// public key, standard base64, and its number.
type ClaimKey struct {
	Kid       int    `json:"kid"`
	PublicKey string `json:"publicKey"`
}

// ClaimOwner is who claimed the router, as the panel may show them.
type ClaimOwner struct {
	Label string `json:"label"`
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
	ClaimInfo
}

// ClaimInfo is what a register or check-in answer may say about claiming the
// router (ADR-0006). Owner is raw so that "absent" (no news) and null (no
// owner) stay apart. Released: the owner unbound the router in the Vectra
// app (sent with "owner": null until it is claimed again).
type ClaimInfo struct {
	ClaimKey    *ClaimKey       `json:"claimKey,omitempty"`
	BotUsername string          `json:"botUsername,omitempty"`
	Owner       json.RawMessage `json:"owner,omitempty"`
	Released    bool            `json:"released,omitempty"`
}

type RegisterRequest struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Inventory       RouterInventory `json:"inventory"`
	// Proof that the registering router holds its device key, for the panel
	// to adopt a record it created in advance (a pre-claimed router). Absent
	// only when the router cannot read its own key.
	Proof *RegisterProof `json:"proof,omitempty"`
}

// RegisterProof is an ed25519 signature by the device key (inventory's
// devicePublicKey, the claim QR's pk), standard base64, over exactly the
// UTF-8 bytes "vectra-register/v1\n<deviceIdentifier>\n<timestamp>"
// (claim.RegisterMessage); timestamp in unix seconds.
type RegisterProof struct {
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
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
	ClaimInfo
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
