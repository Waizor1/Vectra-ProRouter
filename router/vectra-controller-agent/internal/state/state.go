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

	"vectra-controller-agent/internal/controlplane"
	"vectra-controller-agent/internal/recovery"
	"vectra-controller-agent/internal/rescue"
)

type RescueSnapshot struct {
	State      rescue.State `json:"state"`
	LastMode   string       `json:"last_mode,omitempty"`
	LastReason string       `json:"last_reason,omitempty"`
	HappenedAt string       `json:"happened_at,omitempty"`
}

type CurrentJob struct {
	JobID                     string `json:"job_id,omitempty"`
	JobType                   string `json:"job_type,omitempty"`
	AcceptedAt                string `json:"accepted_at,omitempty"`
	ExpectedControllerVersion string `json:"expected_controller_version,omitempty"`
}

type PersistedState struct {
	RouterID                 string                         `json:"router_id,omitempty"`
	AgentToken               string                         `json:"agent_token,omitempty"`
	DeviceIdentifier         string                         `json:"device_identifier,omitempty"`
	DevicePublicKey          string                         `json:"device_public_key,omitempty"`
	DevicePrivateKey         string                         `json:"device_private_key,omitempty"`
	AppliedRevisionID        string                         `json:"applied_revision_id,omitempty"`
	ConfigDigest             string                         `json:"config_digest,omitempty"`
	LastImportedConfigDigest string                         `json:"last_imported_config_digest,omitempty"`
	RequestImport            bool                           `json:"request_import,omitempty"`
	Rescue                   RescueSnapshot                 `json:"rescue,omitempty"`
	ControlPlaneRecovery     recovery.State                 `json:"control_plane_recovery,omitempty"`
	CurrentJob               CurrentJob                     `json:"current_job,omitempty"`
	PendingJobResult         *controlplane.JobResultRequest `json:"pending_job_result,omitempty"`
}

func Load(path string) (PersistedState, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PersistedState{}, nil
		}
		return PersistedState{}, fmt.Errorf("read state: %w", err)
	}

	var persisted PersistedState
	if err := json.Unmarshal(bytes, &persisted); err != nil {
		return PersistedState{}, fmt.Errorf("decode state: %w", err)
	}
	persisted.ControlPlaneRecovery.Normalize()
	return persisted, nil
}

func Save(path string, persisted PersistedState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	bytes, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), ".vectra-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}

	tempPath := tempFile.Name()
	if _, err := tempFile.Write(bytes); err != nil {
		tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("write temp state: %w", err)
	}
	if err := tempFile.Chmod(0o600); err != nil {
		tempFile.Close()
		_ = os.Remove(tempPath)
		return fmt.Errorf("chmod temp state: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close temp state: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}

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
