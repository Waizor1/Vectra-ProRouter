// Package state persists the controller's identity and job journal across
// restarts. It is ported from vectra-controller-agent/internal/state (the
// atomic-write + last-good + salvage recovery logic), trimmed to the
// xray-direct controller's needs (no passwall import digests, no full
// control-plane recovery state machine).
package state

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"vectra-controller-pro/internal/controlplane"
)

// RescueSnapshot captures the local rescue mode across restarts. Plain
// strings keep this package decoupled from internal/rescue.
type RescueSnapshot struct {
	Mode               string `json:"mode,omitempty"`
	LastReason         string `json:"last_reason,omitempty"`
	HappenedAt         string `json:"happened_at,omitempty"`
	ProxyFailureCount  int    `json:"proxy_failure_count,omitempty"`
	DirectSuccessCount int    `json:"direct_success_count,omitempty"`
	LastTransitionAt   string `json:"last_transition_at,omitempty"`
}

// CurrentJob tracks the job being executed so a crash mid-job is reported as
// a failure on the next loop (journal recovery).
type CurrentJob struct {
	JobID                     string `json:"job_id,omitempty"`
	JobType                   string `json:"job_type,omitempty"`
	AcceptedAt                string `json:"accepted_at,omitempty"`
	ExpectedControllerVersion string `json:"expected_controller_version,omitempty"`
}

// PersistedState is the on-disk controller state.
type PersistedState struct {
	RouterID            string                               `json:"router_id,omitempty"`
	AgentToken          string                               `json:"agent_token,omitempty"`
	DeviceIdentifier    string                               `json:"device_identifier,omitempty"`
	DevicePublicKey     string                               `json:"device_public_key,omitempty"`
	DevicePrivateKey    string                               `json:"device_private_key,omitempty"`
	AppliedRevisionID   string                               `json:"applied_revision_id,omitempty"`
	ConfigDigest        string                               `json:"config_digest,omitempty"`
	LastDesiredRevision *controlplane.DesiredRevisionSummary `json:"last_desired_revision,omitempty"`
	Rescue              RescueSnapshot                       `json:"rescue,omitempty"`
	CurrentJob          CurrentJob                           `json:"current_job,omitempty"`
	PendingJobResult    *controlplane.JobResultRequest       `json:"pending_job_result,omitempty"`
}

// Load reads persisted state, recovering from a last-good copy or salvaging
// identity fields from a corrupt file rather than losing the router's token.
func Load(path string) (PersistedState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if recovered, ok := loadLastGood(path); ok {
				_ = Save(path, recovered)
				return recovered, nil
			}
			return PersistedState{}, nil
		}
		return PersistedState{}, fmt.Errorf("read state: %w", err)
	}

	persisted, err := decode(raw)
	if err == nil {
		return persisted, nil
	}

	backupCorrupted(path, raw)

	if recovered, ok := loadLastGood(path); ok {
		fmt.Fprintf(os.Stderr, "warning: recovered persisted state from %s after %v\n", lastGoodPath(path), err)
		_ = Save(path, recovered)
		return recovered, nil
	}
	if recovered, ok := salvage(raw); ok {
		fmt.Fprintf(os.Stderr, "warning: salvaged partial persisted state from %s after %v\n", path, err)
		return recovered, nil
	}
	fmt.Fprintf(os.Stderr, "warning: ignoring unreadable persisted state %s after %v; a new state will be created\n", path, err)
	return PersistedState{}, nil
}

func decode(raw []byte) (PersistedState, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return PersistedState{}, fmt.Errorf("empty state file")
	}
	var persisted PersistedState
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return PersistedState{}, fmt.Errorf("decode state: %w", err)
	}
	return persisted, nil
}

func lastGoodPath(path string) string { return path + ".last-good" }

func corruptPath(path string) string {
	return fmt.Sprintf("%s.corrupt-%s", path, time.Now().UTC().Format("20060102T150405Z"))
}

func loadLastGood(path string) (PersistedState, bool) {
	raw, err := os.ReadFile(lastGoodPath(path))
	if err != nil {
		return PersistedState{}, false
	}
	persisted, err := decode(raw)
	if err != nil {
		return PersistedState{}, false
	}
	return persisted, true
}

func backupCorrupted(path string, raw []byte) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(corruptPath(path), raw, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to back up corrupted state %s: %v\n", path, err)
	}
}

func salvage(raw []byte) (PersistedState, bool) {
	persisted := PersistedState{
		RouterID:          salvageString(raw, "router_id"),
		AgentToken:        salvageString(raw, "agent_token"),
		DeviceIdentifier:  salvageString(raw, "device_identifier"),
		DevicePublicKey:   salvageString(raw, "device_public_key"),
		DevicePrivateKey:  salvageString(raw, "device_private_key"),
		AppliedRevisionID: salvageString(raw, "applied_revision_id"),
		ConfigDigest:      salvageString(raw, "config_digest"),
	}
	if persisted.RouterID == "" && persisted.AgentToken == "" && persisted.DeviceIdentifier == "" &&
		persisted.DevicePublicKey == "" && persisted.DevicePrivateKey == "" {
		return PersistedState{}, false
	}
	return persisted, true
}

func salvageString(raw []byte, field string) string {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(field) + `"\s*:\s*("(?:\\.|[^"\\])*")`)
	match := re.FindSubmatch(raw)
	if len(match) != 2 {
		return ""
	}
	var value string
	if err := json.Unmarshal(match[1], &value); err != nil {
		return ""
	}
	return value
}

// Save writes state atomically and updates the last-good backup.
func Save(path string, persisted PersistedState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	raw, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := writeAtomic(path, raw, 0o600); err != nil {
		return err
	}
	if err := writeAtomic(lastGoodPath(path), raw, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update last-good state backup %s: %v\n", lastGoodPath(path), err)
	}
	return nil
}

func writeAtomic(path string, raw []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".vctl-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tempPath := tempFile.Name()
	if _, err := tempFile.Write(raw); err != nil {
		tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tempFile.Chmod(perm); err != nil {
		tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("chmod temp state: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("sync temp state: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("replace state: %w", err)
	}
	syncDir(filepath.Dir(path))
	return nil
}

func syncDir(path string) {
	dir, err := os.Open(path)
	if err != nil {
		return
	}
	defer dir.Close()
	_ = dir.Sync()
}

// EnsureIdentity generates a device identifier + ed25519 keypair if missing.
func EnsureIdentity(persisted *PersistedState) error {
	if persisted.DeviceIdentifier == "" {
		randomBytes := make([]byte, 6)
		if _, err := rand.Read(randomBytes); err != nil {
			return fmt.Errorf("generate device identifier: %w", err)
		}
		persisted.DeviceIdentifier = "vectra-" + hex.EncodeToString(randomBytes)
	}
	if persisted.DevicePublicKey == "" || persisted.DevicePrivateKey == "" {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("generate device keypair: %w", err)
		}
		persisted.DevicePublicKey = base64.StdEncoding.EncodeToString(publicKey)
		persisted.DevicePrivateKey = base64.StdEncoding.EncodeToString(privateKey)
	}
	return nil
}

// legacyAgentState is the subset of the vectra-controller-agent state file we
// reuse so a canary router keeps the SAME panel identity when it switches to
// xray-direct (the panel sees one router flip engineMode, not a duplicate).
type legacyAgentState struct {
	RouterID         string `json:"router_id"`
	AgentToken       string `json:"agent_token"`
	DeviceIdentifier string `json:"device_identifier"`
	DevicePublicKey  string `json:"device_public_key"`
	DevicePrivateKey string `json:"device_private_key"`
}

// ImportLegacyIdentity copies identity from a legacy agent state file into
// persisted IF persisted has no identity yet. Returns true if it imported.
// Missing/unreadable legacy file is not an error (fresh enrollment path).
func ImportLegacyIdentity(persisted *PersistedState, legacyStatePath string) (bool, error) {
	if legacyStatePath == "" || persisted.RouterID != "" || persisted.AgentToken != "" {
		return false, nil
	}
	raw, err := os.ReadFile(legacyStatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read legacy state: %w", err)
	}
	var legacy legacyAgentState
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return false, fmt.Errorf("decode legacy state: %w", err)
	}
	if legacy.RouterID == "" || legacy.AgentToken == "" {
		return false, nil
	}
	persisted.RouterID = legacy.RouterID
	persisted.AgentToken = legacy.AgentToken
	if persisted.DeviceIdentifier == "" {
		persisted.DeviceIdentifier = legacy.DeviceIdentifier
	}
	if persisted.DevicePublicKey == "" {
		persisted.DevicePublicKey = legacy.DevicePublicKey
	}
	if persisted.DevicePrivateKey == "" {
		persisted.DevicePrivateKey = legacy.DevicePrivateKey
	}
	return true, nil
}
