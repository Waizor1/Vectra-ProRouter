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
	RouterID                 string                               `json:"router_id,omitempty"`
	AgentToken               string                               `json:"agent_token,omitempty"`
	DeviceIdentifier         string                               `json:"device_identifier,omitempty"`
	DevicePublicKey          string                               `json:"device_public_key,omitempty"`
	DevicePrivateKey         string                               `json:"device_private_key,omitempty"`
	AppliedRevisionID        string                               `json:"applied_revision_id,omitempty"`
	ConfigDigest             string                               `json:"config_digest,omitempty"`
	LastImportedConfigDigest string                               `json:"last_imported_config_digest,omitempty"`
	LastDesiredRevision      *controlplane.DesiredRevisionSummary `json:"last_desired_revision,omitempty"`
	RequestImport            bool                                 `json:"request_import,omitempty"`
	Rescue                   RescueSnapshot                       `json:"rescue,omitempty"`
	ControlPlaneRecovery     recovery.State                       `json:"control_plane_recovery,omitempty"`
	CurrentJob               CurrentJob                           `json:"current_job,omitempty"`
	PendingJobResult         *controlplane.JobResultRequest       `json:"pending_job_result,omitempty"`
}

func Load(path string) (PersistedState, error) {
	bytes, err := os.ReadFile(path)
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

	persisted, err := decode(bytes)
	if err == nil {
		return persisted, nil
	}

	backupCorrupted(path, bytes)

	if recovered, ok := loadLastGood(path); ok {
		fmt.Fprintf(os.Stderr, "warning: recovered persisted state from %s after %v\n", lastGoodPath(path), err)
		_ = Save(path, recovered)
		return recovered, nil
	}

	if recovered, ok := salvage(bytes); ok {
		fmt.Fprintf(os.Stderr, "warning: salvaged partial persisted state from %s after %v\n", path, err)
		return recovered, nil
	}

	fmt.Fprintf(os.Stderr, "warning: ignoring unreadable persisted state %s after %v; a new state will be created\n", path, err)
	return PersistedState{}, nil
}

func decode(bytes []byte) (PersistedState, error) {
	if strings.TrimSpace(string(bytes)) == "" {
		return PersistedState{}, fmt.Errorf("empty state file")
	}

	var persisted PersistedState
	if err := json.Unmarshal(bytes, &persisted); err != nil {
		return PersistedState{}, fmt.Errorf("decode state: %w", err)
	}
	persisted.ControlPlaneRecovery.Normalize()
	return persisted, nil
}

func lastGoodPath(path string) string {
	return path + ".last-good"
}

func corruptPath(path string) string {
	return fmt.Sprintf("%s.corrupt-%s", path, time.Now().UTC().Format("20060102T150405Z"))
}

func loadLastGood(path string) (PersistedState, bool) {
	bytes, err := os.ReadFile(lastGoodPath(path))
	if err != nil {
		return PersistedState{}, false
	}

	persisted, err := decode(bytes)
	if err != nil {
		return PersistedState{}, false
	}
	return persisted, true
}

func backupCorrupted(path string, bytes []byte) {
	if path == "" {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	backupPath := corruptPath(path)
	if err := os.WriteFile(backupPath, bytes, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to back up corrupted state %s: %v\n", path, err)
	}
}

func salvage(bytes []byte) (PersistedState, bool) {
	persisted := PersistedState{
		RouterID:                 salvageString(bytes, "router_id"),
		AgentToken:               salvageString(bytes, "agent_token"),
		DeviceIdentifier:         salvageString(bytes, "device_identifier"),
		DevicePublicKey:          salvageString(bytes, "device_public_key"),
		DevicePrivateKey:         salvageString(bytes, "device_private_key"),
		AppliedRevisionID:        salvageString(bytes, "applied_revision_id"),
		ConfigDigest:             salvageString(bytes, "config_digest"),
		LastImportedConfigDigest: salvageString(bytes, "last_imported_config_digest"),
	}

	if persisted.RouterID == "" &&
		persisted.AgentToken == "" &&
		persisted.DeviceIdentifier == "" &&
		persisted.DevicePublicKey == "" &&
		persisted.DevicePrivateKey == "" {
		return PersistedState{}, false
	}

	return persisted, true
}

func salvageString(bytes []byte, field string) string {
	re := regexp.MustCompile(`"` + regexp.QuoteMeta(field) + `"\s*:\s*("(?:\\.|[^"\\])*")`)
	match := re.FindSubmatch(bytes)
	if len(match) != 2 {
		return ""
	}

	var value string
	if err := json.Unmarshal(match[1], &value); err != nil {
		return ""
	}
	return value
}

func Save(path string, persisted PersistedState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	bytes, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	if err := writeAtomic(path, bytes, 0o600); err != nil {
		return err
	}

	if err := writeAtomic(lastGoodPath(path), bytes, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update last-good state backup %s: %v\n", lastGoodPath(path), err)
	}

	return nil
}

func writeAtomic(path string, bytes []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
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
