package main

import "time"

const (
	helperVersion      = "0.1.0"
	helperServiceName  = "vectra-install-helper"
	defaultListenAddr  = "127.0.0.1:38471"
	defaultOrigins     = "https://router.vectra-pro.net,http://localhost:3000"
	bootstrapScriptRel = "/install/ax3000t-bootstrap.sh"
)

var installStageOrder = []string{
	"helper detected",
	"router found",
	"ssh authenticated",
	"bootstrap downloaded",
	"packages installed",
	"controller running",
	"passwall verified",
	"completed",
}

type helperCapabilities struct {
	Scan          bool `json:"scan"`
	Install       bool `json:"install"`
	Events        bool `json:"events"`
	SecureStorage bool `json:"secureStorage"`
}

type credentialProfile struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Username   string     `json:"username"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

type healthResponse struct {
	Service                 string              `json:"service"`
	Version                 string              `json:"version"`
	SessionToken            string              `json:"sessionToken"`
	Capabilities            helperCapabilities  `json:"capabilities"`
	SavedCredentialProfiles []credentialProfile `json:"savedCredentialProfiles"`
}

type scanCandidate struct {
	IP                 string `json:"ip"`
	Source             string `json:"source"`
	SSHReachable       bool   `json:"sshReachable"`
	FingerprintState   string `json:"fingerprintState"`
	HostKeyFingerprint string `json:"hostKeyFingerprint,omitempty"`
	Recommended        bool   `json:"recommended"`
}

type scanResponse struct {
	RecommendedTargetIP string          `json:"recommendedTargetIp,omitempty"`
	Candidates          []scanCandidate `json:"candidates"`
}

type installRequest struct {
	TargetIP            string `json:"targetIp"`
	CredentialProfileID string `json:"credentialProfileId"`
	Password            string `json:"password"`
	SaveProfile         bool   `json:"saveProfile"`
}

type installResponse struct {
	SessionID string `json:"sessionId"`
}

type checklistItem struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	Details string `json:"details,omitempty"`
}

type installEvent struct {
	Stage            string          `json:"stage"`
	State            string          `json:"state"`
	Message          string          `json:"message"`
	Timestamp        string          `json:"timestamp"`
	CopyableLogChunk string          `json:"copyableLogChunk,omitempty"`
	ChecklistDelta   []checklistItem `json:"checklistDelta,omitempty"`
	Code             string          `json:"code,omitempty"`
}

type trustedHostRecord struct {
	Fingerprint string     `json:"fingerprint"`
	TrustedAt   time.Time  `json:"trustedAt"`
	LastSeenAt  *time.Time `json:"lastSeenAt,omitempty"`
}

type helperDiskState struct {
	CredentialProfiles []credentialProfile          `json:"credentialProfiles"`
	TrustedHosts       map[string]trustedHostRecord `json:"trustedHosts"`
}

type gatewayResolver interface {
	DefaultGatewayIP() (string, error)
}

type hostFingerprinter interface {
	ProbeFingerprint(targetIP string) (fingerprint string, reachable bool, err error)
}

type remoteInstaller interface {
	RunInstall(session *installSession, origin string, request installRequest)
}
