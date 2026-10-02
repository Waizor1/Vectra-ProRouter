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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vectra-controller-pro/internal/controlplane"
	"vectra-controller-pro/internal/vault"
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
	RouterID          string `json:"router_id,omitempty"`
	AgentToken        string `json:"agent_token,omitempty"`
	DeviceIdentifier  string `json:"device_identifier,omitempty"`
	DevicePublicKey   string `json:"device_public_key,omitempty"`
	DevicePrivateKey  string `json:"device_private_key,omitempty"`
	AppliedRevisionID string `json:"applied_revision_id,omitempty"`
	ConfigDigest      string `json:"config_digest,omitempty"`
	// SpliceKey fingerprints the router-side options (xray.SpliceOptions.Key)
	// the installed render was made with. The same provider bytes under
	// different options (a new probe interval, the API added by an upgrade)
	// are a different render, and ConfigDigest alone cannot tell.
	SpliceKey string `json:"splice_key,omitempty"`
	// RenderAssetDir is the geo directory the installed render passed
	// `xray -test` with — what xray runs it with, across vctl's restarts,
	// whatever an operator config the gate refused names since.
	RenderAssetDir string `json:"render_asset_dir,omitempty"`
	// UnfitExits are the exits the installed render leaves out, found unfit
	// by the router's exit check (cmd/vctl/exitcheck.go) — since when, RFC
	// 3339. The render after a restart leaves them out again.
	UnfitExits map[string]string `json:"unfit_exits,omitempty"`
	// ExitEgress is where each foreign exit was seen leaving (the exit
	// check's Cloudflare trace, once a day) and when, RFC 3339: the card's
	// «(выход: …)» outlives a restart, and nothing is asked again early.
	ExitEgress          map[string]ExitEgress                `json:"exit_egress,omitempty"`
	LastDesiredRevision *controlplane.DesiredRevisionSummary `json:"last_desired_revision,omitempty"`
	Rescue              RescueSnapshot                       `json:"rescue,omitempty"`
	CurrentJob          CurrentJob                           `json:"current_job,omitempty"`
	PendingJobResult    *controlplane.JobResultRequest       `json:"pending_job_result,omitempty"`
	// Claiming the router (ADR-0006), as the panel last said: Vectra's key
	// the claim QR is sealed to, the Vectra bot, and who claimed the router.
	ClaimKey    *controlplane.ClaimKey   `json:"claim_key,omitempty"`
	BotUsername string                   `json:"bot_username,omitempty"`
	ClaimOwner  *controlplane.ClaimOwner `json:"claim_owner,omitempty"`
}

// ExitEgress is one exit's located country (ISO) and when it was seen.
type ExitEgress struct {
	CC string `json:"cc"`
	At string `json:"at"`
}

// Load reads persisted state, recovering from a last-good copy or salvaging
// identity fields from a corrupt file rather than losing the router's token.
func Load(path string) (PersistedState, error) {
	raw, err := vault.ReadFile(path)
	if err == nil {
		defer clear(raw)
		persisted, e := decode(raw)
		if e == nil {
			return persisted, nil
		}
		err = e
	}
	if recovered, ok := loadLastGood(path); ok {
		if e := Save(path, recovered); e != nil {
			return PersistedState{}, fmt.Errorf("restore encrypted state: %w", e)
		}
		return recovered, nil
	}
	if os.IsNotExist(err) {
		// A missing primary with an existing backup is not a fresh enrollment.
		if _, e := os.Stat(lastGoodPath(path)); os.IsNotExist(e) {
			return PersistedState{}, nil
		}
	}
	return PersistedState{}, fmt.Errorf("read persisted state: %w", err)
}

// Migrate explicitly seals valid legacy state and its last-good copy. Corrupt
// plaintext is never salvaged or copied into diagnostic backups.
func Migrate(path string) error {
	// Seal older raw crash artifacts as opaque recovery documents. They are
	// never parsed or salvaged into identity.
	artifacts, err := filepath.Glob(path + ".corrupt-*")
	if err != nil {
		return err
	}
	oldTemps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".vctl-state-*.tmp"))
	if err != nil {
		return err
	}
	artifacts = append(artifacts, oldTemps...)
	artifacts = append(artifacts, path+".tmp")
	for _, artifact := range artifacts {
		// An old reader's ciphertext copy (state.json.corrupt-*) cannot be
		// opened under its own name: MigrateArtifact takes it away.
		if err := vault.MigrateArtifact(artifact); err != nil {
			return err
		}
	}

	validate := func(raw []byte) error { _, err := decode(raw); return err }
	// Last-good first gives interrupted upgrades an encrypted recovery copy.
	if err := vault.MigrateFile(lastGoodPath(path), validate); err != nil {
		// A damaged backup must not prevent booting a valid primary: seal the
		// primary first (it may still be the old plaintext — reading it
		// through the vault before that refused it), then rewrite the backup
		// from it.
		if e := vault.MigrateFile(path, validate); e == nil {
			if raw, e := vault.ReadFile(path); e == nil {
				defer clear(raw)
				if recovered, e := decode(raw); e == nil {
					return Save(path, recovered)
				}
			}
		}
		return err
	}
	if err := vault.MigrateFile(path, validate); err != nil {
		// The normal loader can restore a corrupt primary from the authenticated
		// backup. Never salvage fields out of unauthenticated bytes.
		if recovered, ok := loadLastGood(path); ok {
			return Save(path, recovered)
		}
		return err
	}
	return nil
}

// MigrateLegacy seals the old Vectra agent's state as Migrate seals vctl's,
// except that a plaintext rewrite over the sealed copy is accepted: after a
// hand-back the old agent cannot read its sealed state, recovers its
// credentials from its own identity mirror and saves the state as plaintext.
// That is the owner's legitimate write, not a downgrade: it is sealed again.
//
// sealIdentity also seals the old agent's identity mirror (router id, agent
// token, device private key), when the installed old agent reads sealed
// files: an old agent that reads only plaintext recovers from that mirror
// after a hand-back, and without it would mint a new identity.
func MigrateLegacy(path string, sealIdentity bool) error {
	validate := func(raw []byte) error { _, err := decode(raw); return err }
	for _, p := range []string{lastGoodPath(path), path} {
		if err := vault.ResealRewritten(p, validate); err != nil {
			return err
		}
	}
	if err := Migrate(path); err != nil {
		return err
	}
	if !sealIdentity {
		return nil
	}
	identity := path + ".identity"
	credentials := func(raw []byte) error {
		var legacy legacyAgentState
		if err := json.Unmarshal(raw, &legacy); err != nil {
			return err
		}
		if legacy.RouterID == "" || legacy.AgentToken == "" {
			return errors.New("identity mirror without credentials")
		}
		return nil
	}
	if err := vault.ResealRewritten(identity, credentials); err != nil {
		return err
	}
	artifacts, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".vectra-state-*.tmp"))
	if err != nil {
		return err
	}
	for _, artifact := range artifacts {
		if err := vault.MigrateArtifact(artifact); err != nil {
			return err
		}
	}
	return vault.MigrateFile(identity, credentials)
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

func loadLastGood(path string) (PersistedState, bool) {
	raw, err := vault.ReadFile(lastGoodPath(path))
	if err != nil {
		return PersistedState{}, false
	}
	defer clear(raw)
	persisted, err := decode(raw)
	if err != nil {
		return PersistedState{}, false
	}
	return persisted, true
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
	defer clear(raw)
	if err := vault.WriteFile(path, raw); err != nil {
		return err
	}
	if err := vault.WriteFile(lastGoodPath(path), raw); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to update last-good state backup %s: %v\n", lastGoodPath(path), err)
	}
	return nil
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
// The old agent keeps its credentials in three files (its state, the
// last-good copy, the identity mirror); the first that holds router_id and
// agent_token wins. No legacy file is not an error (fresh enrollment). A
// legacy file that is there but cannot be read is: the panel already knows
// this router, and minting a new identity would split it in two records.
func ImportLegacyIdentity(persisted *PersistedState, legacyStatePath string) (bool, error) {
	if legacyStatePath == "" || persisted.RouterID != "" || persisted.AgentToken != "" {
		return false, nil
	}
	var unreadable error
	for _, p := range []string{legacyStatePath, legacyStatePath + ".identity", lastGoodPath(legacyStatePath)} {
		raw, err := ReadLegacy(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			unreadable = fmt.Errorf("read legacy state: %w", err)
			continue
		}
		var legacy legacyAgentState
		err = json.Unmarshal(raw, &legacy)
		clear(raw)
		if err != nil {
			unreadable = fmt.Errorf("decode legacy state: %w", err)
			continue
		}
		if legacy.RouterID == "" || legacy.AgentToken == "" {
			continue
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
	return false, unreadable
}

// ReadLegacy reads one of the old agent's files: sealed by vctl, or the
// plaintext the old agent itself writes (it has no vault, and after a
// hand-back it rewrites its files over the sealed copies — its legitimate
// write, not a downgrade).
func ReadLegacy(path string) ([]byte, error) {
	raw, err := vault.ReadFile(path)
	if err == nil {
		return raw, nil
	}
	if errors.Is(err, vault.ErrDowngrade) || vault.Unsealed(path) {
		return os.ReadFile(path)
	}
	return nil, err
}

// LoadReadOnly is a reader's load (vectra-reporter): it never saves state —
// Load restores a primary from last-good and saves it, a race with the daemon
// (the vault may still finish an interrupted seal of the same bytes). A
// sealed state is opened; one that was never sealed (a router on 0.6.0-r36, or
// rolled back to it) is read as it is.
func LoadReadOnly(path string) (PersistedState, error) {
	raw, err := vault.ReadFile(path)
	if err != nil {
		if !vault.Unsealed(path) {
			return PersistedState{}, err
		}
		if raw, err = os.ReadFile(path); err != nil {
			return PersistedState{}, err
		}
	}
	defer clear(raw)
	return decode(raw)
}
